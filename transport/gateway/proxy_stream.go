package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Stream kinds. "bidi" bridges a WebSocket to a bidirectional gRPC stream;
// "sse" bridges a single request to a server-streaming gRPC call delivered as
// Server-Sent Events.
const (
	streamKindBidi = "bidi"
	streamKindSSE  = "sse"
)

// streamCodec carries the per-frame message factories for a streaming route.
// newRecv builds a message for each frame flowing server->client. newSend builds
// one for each frame flowing client->server (bidi), or the single request
// message (sse).
type streamCodec struct {
	kind    string
	newRecv func() proto.Message
	newSend func() proto.Message
}

var streamProxies sync.Map // fullRPC -> *streamCodec

// RegisterBidiStreamProxy registers a bidirectional WebSocket<->gRPC stream
// bridge for an RPC. The encoding is negotiated from the first client frame:
// text frames use protojson for the entire connection; binary frames use
// proto.Marshal/Unmarshal. The server always mirrors the client's frame type.
func RegisterBidiStreamProxy(rpc string, newRecv, newSend func() proto.Message) error {
	if newRecv == nil || newSend == nil {
		return fmt.Errorf("stream proxy requires newRecv and newSend factories")
	}
	fullRPC, _, err := normalizeRPC(rpc)
	if err != nil {
		return err
	}
	streamProxies.Store(fullRPC, &streamCodec{kind: streamKindBidi, newRecv: newRecv, newSend: newSend})
	return nil
}

func MustRegisterBidiStreamProxy(rpc string, newRecv, newSend func() proto.Message) {
	if err := RegisterBidiStreamProxy(rpc, newRecv, newSend); err != nil {
		panic(err)
	}
}

// RegisterServerStreamProxy registers a server-streaming bridge delivered as
// Server-Sent Events. The single request is built from newReq (bound from the
// HTTP body/query/path like a unary route); each gRPC response message is built
// from newResp and written as one SSE `data:` event.
func RegisterServerStreamProxy(rpc string, newReq, newResp func() proto.Message) error {
	if newReq == nil || newResp == nil {
		return fmt.Errorf("server stream proxy requires newReq and newResp factories")
	}
	fullRPC, _, err := normalizeRPC(rpc)
	if err != nil {
		return err
	}
	streamProxies.Store(fullRPC, &streamCodec{kind: streamKindSSE, newRecv: newResp, newSend: newReq})
	return nil
}

func MustRegisterServerStreamProxy(rpc string, newReq, newResp func() proto.Message) {
	if err := RegisterServerStreamProxy(rpc, newReq, newResp); err != nil {
		panic(err)
	}
}

func lookupStreamProxy(fullRPC string) (*streamCodec, bool) {
	raw, ok := streamProxies.Load(fullRPC)
	if !ok {
		return nil, false
	}
	codec, ok := raw.(*streamCodec)
	return codec, ok
}

func unregisterStreamProxy(rpc string) {
	fullRPC, _, err := normalizeRPC(rpc)
	if err != nil {
		return
	}
	streamProxies.Delete(fullRPC)
}

// InvokeStream opens a gRPC stream for the route and bridges it to the upgraded
// WebSocket connection. A single connection is selected from the pool (or dialed
// on demand) for the lifetime of the stream. Retries and per-call timeouts do
// not apply to streams; the breaker only guards stream establishment.
func (p *grpcProxy) InvokeStream(ctx context.Context, ws *websocket.Conn, route *proxyRoute) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, release := route.pickConn()
	defer release()
	if conn == nil {
		var err error
		conn, err = p.dial(ctx, route.cfg)
		if err != nil {
			return sferrors.New(sferrors.CodeUnavailable, "grpc target unavailable").WithCause(err)
		}
	}

	ctx, err := route.claims.outgoing(ctx, wsLocals(ws))
	if err != nil {
		return err
	}
	desc := &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}
	stream, err := conn.NewStream(ctx, desc, route.fullRPC)
	if err != nil {
		return sferrors.New(sferrors.CodeUnavailable, "grpc stream unavailable").WithCause(err)
	}
	return proxyBidi(ctx, cancel, ws, stream, route.streamCodec)
}

// InvokeServerStream opens a server-streaming gRPC call and relays each response
// message to the client as a Server-Sent Event. The request is bound from the
// HTTP body/query/path once, then the response stream is pumped inside the
// body-stream writer. The stream uses a detached context cancelled when the
// writer returns (client disconnect surfaces as a flush error on the next
// frame). As with bidi streams, retries and per-call timeouts do not apply.
func (p *grpcProxy) InvokeServerStream(c *fiber.Ctx, route *proxyRoute) error {
	conn, release := route.pickConn()
	if conn == nil {
		var err error
		conn, err = p.dial(c.UserContext(), route.cfg)
		if err != nil {
			release()
			return writeProxyError(c, sferrors.New(sferrors.CodeUnavailable, "grpc target unavailable").WithCause(err))
		}
	}

	req := route.streamCodec.newSend()
	if err := bindProtoRequest(c, req); err != nil {
		release()
		return writeProxyError(c, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx, err := route.claims.outgoing(ctx, fiberLocals(c))
	if err != nil {
		cancel()
		release()
		return writeProxyError(c, err)
	}
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, route.fullRPC)
	if err != nil {
		cancel()
		release()
		return writeProxyError(c, sferrors.New(sferrors.CodeUnavailable, "grpc stream unavailable").WithCause(err))
	}
	if err := stream.SendMsg(req); err != nil {
		cancel()
		release()
		return writeProxyError(c, sferrors.FromGRPCError(err))
	}
	_ = stream.CloseSend()

	setSSEHeaders(c)
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer cancel()
		defer release()
		for {
			msg := route.streamCodec.newRecv()
			if err := stream.RecvMsg(msg); err != nil {
				return // io.EOF on normal completion
			}
			data, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(msg)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			if err := w.Flush(); err != nil {
				return // client disconnected
			}
		}
	})
	return nil
}

// wsLocals adapts an upgraded WebSocket connection to the locals lookup the
// claim forwarder expects. contrib/websocket copies the request locals (set by
// route plugins during the HTTP handshake) into the connection, so claims from
// the jwt plugin are available here.
func wsLocals(ws *websocket.Conn) func(string) any {
	return func(key string) any { return ws.Locals(key) }
}

func setSSEHeaders(c *fiber.Ctx) {
	c.Set(fiber.HeaderContentType, "text/event-stream")
	c.Set(fiber.HeaderCacheControl, "no-cache")
	c.Set(fiber.HeaderConnection, "keep-alive")
	c.Set("X-Accel-Buffering", "no") // disable proxy buffering (e.g. nginx)
}

// writeProxyError maps an error to the standard JSON failure envelope, matching
// the unary handler's behaviour for errors that occur before the SSE stream
// starts.
func writeProxyError(c *fiber.Ctx, err error) error {
	appErr := sferrors.FromGRPCError(err)
	if ae, ok := err.(*sferrors.AppError); ok {
		appErr = ae
	}
	return c.Status(appErr.HTTPStatus).JSON(sferrors.Failure(appErr))
}

// proxyBidi pumps frames in both directions until either side closes. Each
// direction runs in its own goroutine; when one finishes it unblocks the other
// (cancelling the stream context unblocks RecvMsg, closing the socket unblocks
// ReadMessage). Normal terminations (client close frame, server io.EOF) are
// reported as a nil error.
//
// Encoding is determined by the first client frame: text frames (protojson)
// or binary frames (proto wire format). The server mirrors the client's choice
// for the lifetime of the connection.
func proxyBidi(ctx context.Context, cancel context.CancelFunc, ws *websocket.Conn, stream grpc.ClientStream, codec *streamCodec) error {
	// encodingC carries the binary flag from the first client frame to the server
	// pump. Buffered so the client goroutine never blocks on send.
	encodingC := make(chan bool, 1)

	g := new(errgroup.Group)

	// client -> server
	g.Go(func() error {
		binary := false
		first := true
		for {
			msgType, data, err := ws.ReadMessage()
			if err != nil {
				cancel()
				_ = stream.CloseSend()
				return err
			}
			if first {
				binary = msgType == websocket.BinaryMessage
				encodingC <- binary
				first = false
			}
			msg := codec.newSend()
			if err := unmarshalWSFrame(binary, data, msg); err != nil {
				cancel()
				return sferrors.New(sferrors.CodeInvalidArgument, "stream frame does not match grpc input").WithCause(err)
			}
			if err := stream.SendMsg(msg); err != nil {
				return err
			}
		}
	})

	// server -> client
	g.Go(func() error {
		defer ws.Close()
		binary := false
		// Wait for the encoding decision from the first client frame.
		// If the context is cancelled before the client speaks, the client goroutine
		// has already returned its own error; returning nil here lets g.Wait return
		// that error unchanged.
		select {
		case binary = <-encodingC:
		case <-ctx.Done():
			return nil
		}
		for {
			msg := codec.newRecv()
			if err := stream.RecvMsg(msg); err != nil {
				return err
			}
			data, wsType, err := marshalWSFrame(binary, msg)
			if err != nil {
				return err
			}
			if err := ws.WriteMessage(wsType, data); err != nil {
				return err
			}
		}
	})

	if err := g.Wait(); err != nil && !isNormalStreamClose(err) {
		return err
	}
	return nil
}

// unmarshalWSFrame decodes a WebSocket frame into msg.
// Binary frames (proto wire format) use proto.Unmarshal; text frames use protojson.
func unmarshalWSFrame(binary bool, data []byte, msg proto.Message) error {
	if binary {
		return proto.Unmarshal(data, msg)
	}
	return protojson.Unmarshal(data, msg)
}

// marshalWSFrame encodes msg for a WebSocket frame, returning (data, wsMessageType, error).
// Binary encoding produces a proto wire-format payload in a BinaryMessage frame;
// JSON encoding produces a protojson payload in a TextMessage frame.
func marshalWSFrame(binary bool, msg proto.Message) ([]byte, int, error) {
	if binary {
		data, err := proto.Marshal(msg)
		return data, websocket.BinaryMessage, err
	}
	data, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(msg)
	return data, websocket.TextMessage, err
}

// isNormalStreamClose reports whether an error represents an expected end of a
// stream rather than a real failure: the gRPC side returning io.EOF or the
// client closing the WebSocket.
func isNormalStreamClose(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	return websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived,
	)
}
