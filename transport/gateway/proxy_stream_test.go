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
