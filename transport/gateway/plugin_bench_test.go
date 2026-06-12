package gateway

import (
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	"github.com/valyala/fasthttp"
)

const benchJWTSecret = "bench-secret"

func corePluginSpecs() []config.GatewayPluginConfig {
	return []config.GatewayPluginConfig{
		{Name: "recovery"},
		{Name: "access_log"},
		{Name: "rate_limit", Config: map[string]any{"max": 1 << 30, "window": "1m"}},
		{Name: "api_key", Config: map[string]any{"keys": []any{"bench-key"}}},
		{Name: "metrics"},
	}
}

func benchJWTToken(b *testing.B) string {
	b.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "bench",
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	}).SignedString([]byte(benchJWTSecret))
	if err != nil {
		b.Fatalf("sign token: %v", err)
	}
	return token
}

// BenchmarkProxyPluginChainParallel measures the proxy hot path with the
// plugin chain off, with the core production set (recovery, access_log,
// rate_limit, api_key, metrics), and with JWT auth added on top.
func BenchmarkProxyPluginChainParallel(b *testing.B) {
	jwtSpec := config.GatewayPluginConfig{
		Name:   "jwt",
		Config: map[string]any{"secret": benchJWTSecret},
	}
	variants := []struct {
		name    string
		plugins []config.GatewayPluginConfig
		headers map[string]string
	}{
		{name: "plugins=off"},
		{
			name:    "plugins=core",
			plugins: corePluginSpecs(),
			headers: map[string]string{"X-API-Key": "bench-key"},
		},
		{
			name:    "plugins=core+jwt",
			plugins: append(corePluginSpecs(), jwtSpec),
			headers: map[string]string{
				"X-API-Key":     "bench-key",
				"Authorization": "Bearer " + benchJWTToken(b),
			},
		},
	}
	for _, variant := range variants {
		b.Run(variant.name, func(b *testing.B) {
			registerHealthProxyInvoker(b)
			server, target := startHealthServer(b)
			defer server.Stop()

			cfg := config.Default()
			cfg.Gateway.Plugins = variant.plugins
			cfg.Gateway.Routes = []config.GatewayRouteConfig{healthRoutePooledNoTimeout(target)}
			runtime := app.New(cfg, app.WithLogger(noopLogger{}))
			gw := New()
			if err := gw.Init(b.Context(), runtime); err != nil {
				b.Fatalf("init gateway: %v", err)
			}
			handler := gw.app.Handler()

			benchPluginRequest(b, handler, variant.headers)

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					benchPluginRequest(b, handler, variant.headers)
				}
			})
		})
	}
}

func benchPluginRequest(b *testing.B, handler fasthttp.RequestHandler, headers map[string]string) {
	var req fasthttp.Request
	req.Header.SetMethod(http.MethodGet)
	req.SetRequestURI("/api/health")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	var ctx fasthttp.RequestCtx
	ctx.Init(&req, nil, nil)
	handler(&ctx)
	if ctx.Response.StatusCode() != http.StatusOK {
		b.Fatalf("status = %d", ctx.Response.StatusCode())
	}
}
