package gateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	wsclient "github.com/fasthttp/websocket"
	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// echoStreamServiceDesc is a hand-written bidirectional gRPC service that echoes
// every StringValue frame back, avoiding the need for generated protobuf code.
var echoStreamServiceDesc = grpc.ServiceDesc{
	ServiceName: "test.Echo",
	HandlerType: (*any)(nil),
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Stream",
			Handler:       echoStreamHandler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
}

func echoStreamHandler(_ any, stream grpc.ServerStream) error {
	for {
		msg := &wrapperspb.StringValue{}
		if err := stream.RecvMsg(msg); err != nil {
			return err // io.EOF once the client half-closes
		}
		if err := stream.SendMsg(msg); err != nil {
			return err
		}
	}
}

// feedStreamServiceDesc is a hand-written server-streaming service: it reads one
// request, then pushes three responses and ends the stream.
var feedStreamServiceDesc = grpc.ServiceDesc{
	ServiceName: "test.Feed",
	HandlerType: (*any)(nil),
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Stream",
			Handler:       feedStreamHandler,
			ServerStreams: true,
		},
	},
}

func feedStreamHandler(_ any, stream grpc.ServerStream) error {
	req := &healthpb.HealthCheckRequest{}
	if err := stream.RecvMsg(req); err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		resp := &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}
		if err := stream.SendMsg(resp); err != nil {
			return err
		}
	}
	return nil
}

func startStreamServer(t testing.TB, desc *grpc.ServiceDesc) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	server.RegisterService(desc, nil)
	go func() {
		_ = server.Serve(ln)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = ln.Close()
	})
	return ln.Addr().String()
}

func startEchoStreamServer(t testing.TB) string {
	t.Helper()
	return startStreamServer(t, &echoStreamServiceDesc)
}

func registerEchoStreamProxy(t testing.TB) {
	t.Helper()
	MustRegisterBidiStreamProxy("/test.Echo/Stream",
		func() proto.Message { return &wrapperspb.StringValue{} },
		func() proto.Message { return &wrapperspb.StringValue{} },
	)
	t.Cleanup(func() {
		unregisterStreamProxy("/test.Echo/Stream")
	})
}

func newStreamGateway(t testing.TB, route config.GatewayRouteConfig) string {
	t.Helper()
	cfg := config.Default()
	cfg.Gateway.Routes = []config.GatewayRouteConfig{route}

	runtime := app.New(cfg, app.WithLogger(noopLogger{}))
	gw := New()
	if err := gw.Init(context.Background(), runtime); err != nil {
		t.Fatalf("init gateway: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen gateway: %v", err)
	}
	go func() {
		_ = gw.app.Listener(ln)
	}()
	t.Cleanup(func() {
		_ = gw.app.Shutdown()
	})
	return ln.Addr().String()
}

func dialWS(t testing.TB, url string) *wsclient.Conn {
	t.Helper()
	var (
		conn *wsclient.Conn
		err  error
	)
	for i := 0; i < 50; i++ {
		conn, _, err = wsclient.DefaultDialer.Dial(url, nil)
		if err == nil {
			return conn
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("dial ws: %v", err)
	return nil
}

func TestBidiStreamProxyEchoesFrames(t *testing.T) {
	registerEchoStreamProxy(t)
	target := startEchoStreamServer(t)

	addr := newStreamGateway(t, config.GatewayRouteConfig{
		Name:   "echo-stream",
		Path:   "/ws/echo",
		Target: target,
		RPC:    "/test.Echo/Stream",
		Stream: "bidi",
	})

	conn := dialWS(t, "ws://"+addr+"/ws/echo")
	defer conn.Close()

	for _, want := range []string{`"hello"`, `"world"`, `"third frame"`} {
		if err := conn.WriteMessage(wsclient.TextMessage, []byte(want)); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(data) != want {
			t.Fatalf("echo = %s, want %s", data, want)
		}
	}
}

func registerFeedStreamProxy(t testing.TB) {
	t.Helper()
	MustRegisterServerStreamProxy("/test.Feed/Stream",
		func() proto.Message { return &healthpb.HealthCheckRequest{} },
		func() proto.Message { return &healthpb.HealthCheckResponse{} },
	)
	t.Cleanup(func() {
		unregisterStreamProxy("/test.Feed/Stream")
	})
}

func TestServerStreamProxyDeliversSSE(t *testing.T) {
	registerFeedStreamProxy(t)
	target := startStreamServer(t, &feedStreamServiceDesc)

	addr := newStreamGateway(t, config.GatewayRouteConfig{
		Name:   "feed-sse",
		Path:   "/sse/feed",
		Target: target,
		RPC:    "/test.Feed/Stream",
		Stream: "sse",
	})

	resp, err := http.Get("http://" + addr + "/sse/feed?service=tick")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if events := strings.Count(string(body), "data: "); events != 3 {
		t.Fatalf("got %d SSE events, want 3; body=%q", events, body)
	}
}

func TestBidiStreamProxyEchoesFrames_Binary(t *testing.T) {
	registerEchoStreamProxy(t)
	target := startEchoStreamServer(t)

	addr := newStreamGateway(t, config.GatewayRouteConfig{
		Name:   "echo-stream-binary",
		Path:   "/ws/echo-bin",
		Target: target,
		RPC:    "/test.Echo/Stream",
		Stream: "bidi",
	})

	conn := dialWS(t, "ws://"+addr+"/ws/echo-bin")
	defer conn.Close()

	for _, want := range []string{"hello", "world", "third frame"} {
		data, err := proto.Marshal(&wrapperspb.StringValue{Value: want})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := conn.WriteMessage(wsclient.BinaryMessage, data); err != nil {
			t.Fatalf("write: %v", err)
		}
		msgType, resp, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if msgType != wsclient.BinaryMessage {
			t.Fatalf("frame type = %d, want BinaryMessage (%d)", msgType, wsclient.BinaryMessage)
		}
		got := &wrapperspb.StringValue{}
		if err := proto.Unmarshal(resp, got); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if got.Value != want {
			t.Fatalf("echo = %q, want %q", got.Value, want)
		}
	}
}

func TestBidiStreamProxyEncodingLockedPerConnection(t *testing.T) {
	// Two concurrent connections on the same route: one text, one binary.
	// Each should independently negotiate and maintain its own encoding.
	registerEchoStreamProxy(t)
	target := startEchoStreamServer(t)

	addr := newStreamGateway(t, config.GatewayRouteConfig{
		Name:   "echo-stream-mixed",
		Path:   "/ws/echo-mixed",
		Target: target,
		RPC:    "/test.Echo/Stream",
		Stream: "bidi",
	})

	textConn := dialWS(t, "ws://"+addr+"/ws/echo-mixed")
	defer textConn.Close()
	binConn := dialWS(t, "ws://"+addr+"/ws/echo-mixed")
	defer binConn.Close()

	// text connection: send JSON, expect text frame back
	if err := textConn.WriteMessage(wsclient.TextMessage, []byte(`"ping"`)); err != nil {
		t.Fatalf("text write: %v", err)
	}
	msgType, data, err := textConn.ReadMessage()
	if err != nil {
		t.Fatalf("text read: %v", err)
	}
	if msgType != wsclient.TextMessage {
		t.Fatalf("text conn: frame type = %d, want TextMessage (%d)", msgType, wsclient.TextMessage)
	}
	if string(data) != `"ping"` {
		t.Fatalf("text conn: echo = %s, want \"ping\"", data)
	}

	// binary connection: send proto binary, expect binary frame back
	binData, err := proto.Marshal(&wrapperspb.StringValue{Value: "pong"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := binConn.WriteMessage(wsclient.BinaryMessage, binData); err != nil {
		t.Fatalf("binary write: %v", err)
	}
	msgType, resp, err := binConn.ReadMessage()
	if err != nil {
		t.Fatalf("binary read: %v", err)
	}
	if msgType != wsclient.BinaryMessage {
		t.Fatalf("binary conn: frame type = %d, want BinaryMessage (%d)", msgType, wsclient.BinaryMessage)
	}
	got := &wrapperspb.StringValue{}
	if err := proto.Unmarshal(resp, got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Value != "pong" {
		t.Fatalf("binary conn: echo = %q, want %q", got.Value, "pong")
	}
}

func TestStreamRouteRequiresRegisteredProxy(t *testing.T) {
	unregisterStreamProxy("/test.Echo/Stream")
	target := startEchoStreamServer(t)

	cfg := config.Default()
	cfg.Gateway.Routes = []config.GatewayRouteConfig{{
		Path:   "/ws/echo",
		Target: target,
		RPC:    "/test.Echo/Stream",
		Stream: "bidi",
	}}

	runtime := app.New(cfg, app.WithLogger(noopLogger{}))
	gw := New()
	if err := gw.Init(context.Background(), runtime); err == nil {
		t.Fatal("expected missing stream proxy error")
	}
}
