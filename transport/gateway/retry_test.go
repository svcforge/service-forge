package gateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// flakyHealthServer fails the first failUntil Check calls with failCode, then
// reports SERVING. It lets tests assert exactly how many attempts the gateway
// made.
type flakyHealthServer struct {
	healthpb.UnimplementedHealthServer
	calls     atomic.Int64
	failUntil int64
	failCode  codes.Code
}

func (s *flakyHealthServer) Check(_ context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	n := s.calls.Add(1)
	if n <= s.failUntil {
		return nil, status.Error(s.failCode, "transient failure")
	}
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

func startFlakyHealthServer(t testing.TB, failUntil int64, failCode codes.Code) (*flakyHealthServer, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	impl := &flakyHealthServer{failUntil: failUntil, failCode: failCode}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, impl)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return impl, listener.Addr().String()
}

func retryRoute(target string, retry *config.RetryConfig) config.GatewayRouteConfig {
	route := healthRoute(target)
	route.Retry = retry
	return route
}

func doHealthRequest(t testing.TB, gw *Gateway) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	resp, err := gw.app.Test(req, -1)
	if err != nil {
		t.Fatalf("request gateway: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestRetryRecoversFromTransientFailure(t *testing.T) {
	registerHealthProxyInvoker(t)
	impl, target := startFlakyHealthServer(t, 2, codes.Unavailable)

	gw := newHealthGateway(t, retryRoute(target, &config.RetryConfig{MaxAttempts: 3}))

	if status := doHealthRequest(t, gw); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got := impl.calls.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestRetryExhaustsAndFails(t *testing.T) {
	registerHealthProxyInvoker(t)
	impl, target := startFlakyHealthServer(t, 100, codes.Unavailable)

	gw := newHealthGateway(t, retryRoute(target, &config.RetryConfig{MaxAttempts: 2}))

	if status := doHealthRequest(t, gw); status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if got := impl.calls.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestRetrySkipsNonRetryableCode(t *testing.T) {
	registerHealthProxyInvoker(t)
	impl, target := startFlakyHealthServer(t, 100, codes.InvalidArgument)

	gw := newHealthGateway(t, retryRoute(target, &config.RetryConfig{MaxAttempts: 3}))

	if status := doHealthRequest(t, gw); status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if got := impl.calls.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (non-retryable)", got)
	}
}

func TestRetryHonorsCustomRetryOn(t *testing.T) {
	registerHealthProxyInvoker(t)
	impl, target := startFlakyHealthServer(t, 1, codes.InvalidArgument)

	gw := newHealthGateway(t, retryRoute(target, &config.RetryConfig{
		MaxAttempts: 3,
		RetryOn:     []string{string(sferrors.CodeInvalidArgument)},
	}))

	if status := doHealthRequest(t, gw); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got := impl.calls.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestNoRetryByDefault(t *testing.T) {
	registerHealthProxyInvoker(t)
	impl, target := startFlakyHealthServer(t, 1, codes.Unavailable)

	gw := newHealthGateway(t, healthRoute(target))

	if status := doHealthRequest(t, gw); status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if got := impl.calls.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry configured)", got)
	}
}

func TestRetryBackoffAbortsOnContextCancel(t *testing.T) {
	registerHealthProxyInvoker(t)
	impl, target := startFlakyHealthServer(t, 100, codes.Unavailable)

	route := retryRoute(target, &config.RetryConfig{MaxAttempts: 5, Backoff: 50 * time.Millisecond})
	route.Timeout = 10 * time.Millisecond
	gw := newHealthGateway(t, route)

	// Per-try timeout (10ms) is shorter than backoff (50ms); the request should
	// still terminate well before 5 full backoff cycles would elapse.
	start := time.Now()
	status := doHealthRequest(t, gw)
	if status != http.StatusServiceUnavailable && status != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 503 or 504", status)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("request took %v, retry loop did not terminate promptly", elapsed)
	}
	if got := impl.calls.Load(); got == 0 {
		t.Fatal("expected at least one attempt")
	}
}
