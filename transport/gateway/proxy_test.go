package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/core/module"
	"github.com/valyala/fasthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

var healthServingJSON = []byte(`{"status":"SERVING"}`)

func TestConfiguredRouteProxiesToGRPC(t *testing.T) {
	registerHealthProxyInvoker(t)
	server, target := startHealthServer(t)
	defer server.Stop()

	gw := newHealthGateway(t, healthRoute(target))
	assertHealthGateway(t, gw)
}

func TestConfiguredRouteRequiresRegisteredInvoker(t *testing.T) {
	unregisterProxyInvoker("/grpc.health.v1.Health/Check")
	server, target := startHealthServer(t)
	defer server.Stop()

	cfg := config.Default()
	cfg.Gateway.Routes = []config.GatewayRouteConfig{healthRoute(target)}

	runtime := app.New(cfg, app.WithLogger(noopLogger{}))
	gw := New()
	if err := gw.Init(context.Background(), runtime); err == nil {
		t.Fatal("expected missing invoker error")
	}
}

func BenchmarkConfiguredRouteProxyToGRPC(b *testing.B) {
	server, gw := setupBenchmarkGateway(b)
	defer server.Stop()

	warmupGateway(b, gw)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchGatewayRequest(b, gw)
	}
}

func BenchmarkConfiguredRouteProxyToGRPCParallel(b *testing.B) {
	server, gw := setupBenchmarkGateway(b)
	defer server.Stop()

	warmupGateway(b, gw)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			benchGatewayRequest(b, gw)
		}
	})
}

func BenchmarkConfiguredRouteProxyToGRPCFasthttp(b *testing.B) {
	server, gw := setupBenchmarkGateway(b)
	defer server.Stop()

	handler := gw.app.Handler()
	warmupGatewayFasthttp(b, handler)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchGatewayFasthttpRequest(b, handler)
	}
}

func BenchmarkConfiguredRouteProxyToGRPCFasthttpParallel(b *testing.B) {
	server, gw := setupBenchmarkGateway(b)
	defer server.Stop()

	handler := gw.app.Handler()
	warmupGatewayFasthttp(b, handler)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			benchGatewayFasthttpRequest(b, handler)
		}
	})
}

func BenchmarkConfiguredRouteProxyToGRPCFasthttpNoTimeoutParallel(b *testing.B) {
	server, gw := setupBenchmarkGatewayWithRoute(b, healthRouteNoTimeout)
	defer server.Stop()

	handler := gw.app.Handler()
	warmupGatewayFasthttp(b, handler)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			benchGatewayFasthttpRequest(b, handler)
		}
	})
}

func BenchmarkConfiguredRouteProxyToGRPCFasthttpPooledNoTimeoutParallel(b *testing.B) {
	server, gw := setupBenchmarkGatewayWithRoute(b, healthRoutePooledNoTimeout)
	defer server.Stop()

	handler := gw.app.Handler()
	warmupGatewayFasthttp(b, handler)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			benchGatewayFasthttpRequest(b, handler)
		}
	})
}

func setupBenchmarkGateway(b *testing.B) (*grpc.Server, *Gateway) {
	b.Helper()
	return setupBenchmarkGatewayWithRoute(b, healthRoute)
}

func setupBenchmarkGatewayWithRoute(b *testing.B, routeFunc func(string) config.GatewayRouteConfig) (*grpc.Server, *Gateway) {
	b.Helper()
	registerHealthProxyInvoker(b)
	server, target := startHealthServer(b)
	gw := newHealthGateway(b, routeFunc(target))
	return server, gw
}

func healthRoute(target string) config.GatewayRouteConfig {
	route := healthRouteNoTimeout(target)
	route.Timeout = 2 * time.Second
	return route
}

func healthRouteNoTimeout(target string) config.GatewayRouteConfig {
	return config.GatewayRouteConfig{
		Name:   "grpc-health",
		Method: http.MethodGet,
		Path:   "/api/health",
		Target: target,
		RPC:    "/grpc.health.v1.Health/Check",
	}
}

func healthRoutePooledNoTimeout(target string) config.GatewayRouteConfig {
	route := healthRouteNoTimeout(target)
	route.PoolSize = runtime.GOMAXPROCS(0)
	return route
}

func newHealthGateway(t testing.TB, route config.GatewayRouteConfig) *Gateway {
	t.Helper()
	cfg := config.Default()
	cfg.Gateway.Routes = []config.GatewayRouteConfig{route}

	runtime := app.New(cfg, app.WithLogger(noopLogger{}))
	gw := New()
	if err := gw.Init(context.Background(), runtime); err != nil {
		t.Fatalf("init gateway: %v", err)
	}
	return gw
}

func assertHealthGateway(t testing.TB, gw *Gateway) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	resp, err := gw.app.Test(req)
	if err != nil {
		t.Fatalf("request gateway: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body sferrors.Response
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != sferrors.CodeOK {
		t.Fatalf("code = %s", body.Code)
	}
	data, ok := body.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v", body.Data)
	}
	if data["status"] != "SERVING" {
		t.Fatalf("status data = %#v", data)
	}
}

func warmupGateway(b *testing.B, gw *Gateway) {
	b.Helper()
	benchGatewayRequest(b, gw)
}

func warmupGatewayFasthttp(b *testing.B, handler fasthttp.RequestHandler) {
	b.Helper()
	benchGatewayFasthttpRequest(b, handler)
}

func benchGatewayRequest(b *testing.B, gw *Gateway) {
	b.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	resp, err := gw.app.Test(req)
	if err != nil {
		b.Fatalf("request gateway: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		b.Fatalf("status = %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func benchGatewayFasthttpRequest(b *testing.B, handler fasthttp.RequestHandler) {
	b.Helper()
	var req fasthttp.Request
	req.Header.SetMethod(http.MethodGet)
	req.SetRequestURI("/api/health")

	var ctx fasthttp.RequestCtx
	ctx.Init(&req, nil, nil)
	handler(&ctx)
	if ctx.Response.StatusCode() != http.StatusOK {
		b.Fatalf("status = %d", ctx.Response.StatusCode())
	}
}

func startHealthServer(t testing.TB) (*grpc.Server, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
	})
	return server, listener.Addr().String()
}

func registerHealthProxyInvoker(t testing.TB) {
	t.Helper()
	MustRegisterProxyInvoker("/grpc.health.v1.Health/Check", NewUnaryCodecProxy(
		func() *healthpb.HealthCheckRequest {
			return &healthpb.HealthCheckRequest{}
		},
		func() *healthpb.HealthCheckResponse {
			return &healthpb.HealthCheckResponse{}
		},
		nil,
		func(ctx context.Context, conn *grpc.ClientConn, req *healthpb.HealthCheckRequest, resp *healthpb.HealthCheckResponse) error {
			return conn.Invoke(ctx, "/grpc.health.v1.Health/Check", req, resp)
		},
		func(c *fiber.Ctx, resp *healthpb.HealthCheckResponse) error {
			if resp.Status == healthpb.HealthCheckResponse_SERVING {
				return WriteSuccessJSON(c, healthServingJSON)
			}
			return writeProtoSuccess(c, resp)
		},
	))
	t.Cleanup(func() {
		unregisterProxyInvoker("/grpc.health.v1.Health/Check")
	})
}

type noopLogger struct{}

func (noopLogger) With(fields ...any) module.Logger { return noopLogger{} }
func (noopLogger) Debug(msg string, fields ...any)  {}
func (noopLogger) Info(msg string, fields ...any)   {}
func (noopLogger) Warn(msg string, fields ...any)   {}
func (noopLogger) Error(msg string, fields ...any)  {}
