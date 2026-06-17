package gateway

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

func boolPtr(v bool) *bool { return &v }

func initGateway(t *testing.T, cfg *config.Config, routes ...RouteFunc) *Gateway {
	t.Helper()
	runtime := app.New(cfg, app.WithLogger(noopLogger{}))
	gw := New(routes...)
	if err := gw.Init(context.Background(), runtime); err != nil {
		t.Fatalf("gateway init: %v", err)
	}
	return gw
}

func pingRoute() RouteFunc {
	return func(router *fiber.App, gw *Gateway) {
		router.Get("/api/v1/ping", gw.Handle(func(ctx context.Context, c *fiber.Ctx) (any, error) {
			return fiber.Map{"message": "pong"}, nil
		}))
	}
}

func TestGlobalPluginEnforcedFromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Plugins = []config.GatewayPluginConfig{
		{Name: "api_key", Config: map[string]any{"keys": []any{"k-1"}}},
	}
	gw := initGateway(t, cfg, pingRoute())

	resp, _ := gw.app.Test(httptest.NewRequest("GET", "/api/v1/ping", nil))
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("without key: expected 401, got %d", resp.StatusCode)
	}

	req := httptest.NewRequest("GET", "/api/v1/ping", nil)
	req.Header.Set("X-API-Key", "k-1")
	resp, _ = gw.app.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("with key: expected 200, got %d", resp.StatusCode)
	}
}

func TestHealthExemptFromGlobalPlugins(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Plugins = []config.GatewayPluginConfig{
		{Name: "api_key", Config: map[string]any{"keys": []any{"k-1"}}},
	}
	gw := initGateway(t, cfg)
	resp, _ := gw.app.Test(httptest.NewRequest("GET", "/health", nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("health should bypass plugins, got %d", resp.StatusCode)
	}
}

func TestDisabledPluginIsNotApplied(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Plugins = []config.GatewayPluginConfig{
		{Name: "api_key", Enabled: boolPtr(false), Config: map[string]any{"keys": []any{"k-1"}}},
	}
	gw := initGateway(t, cfg, pingRoute())
	resp, _ := gw.app.Test(httptest.NewRequest("GET", "/api/v1/ping", nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("disabled plugin must not run, got %d", resp.StatusCode)
	}
}

func TestUnknownPluginFailsInit(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Plugins = []config.GatewayPluginConfig{{Name: "no-such-plugin"}}
	runtime := app.New(cfg, app.WithLogger(noopLogger{}))
	if err := New().Init(context.Background(), runtime); err == nil {
		t.Fatal("expected init error for unknown plugin")
	}
}

func TestMetricsEndpointMountedAndExempt(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Plugins = []config.GatewayPluginConfig{
		{Name: "metrics"},
		{Name: "api_key", Config: map[string]any{"keys": []any{"k-1"}}},
	}
	gw := initGateway(t, cfg, pingRoute())

	req := httptest.NewRequest("GET", "/api/v1/ping", nil)
	req.Header.Set("X-API-Key", "k-1")
	if _, err := gw.app.Test(req); err != nil {
		t.Fatalf("ping: %v", err)
	}

	resp, _ := gw.app.Test(httptest.NewRequest("GET", "/metrics", nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("metrics endpoint should bypass auth, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "gateway_http_requests_total") {
		t.Fatal("expected request metrics in exposition")
	}
}

func TestRouteLevelPluginOnConfiguredRoute(t *testing.T) {
	registerHealthProxyInvoker(t)
	server, target := startHealthServer(t)
	defer server.Stop()

	route := healthRoute(target)
	route.Plugins = []config.GatewayPluginConfig{
		{Name: "api_key", Config: map[string]any{"keys": []any{"route-key"}}},
	}
	cfg := config.Default()
	cfg.Gateway.Routes = []config.GatewayRouteConfig{route}
	gw := initGateway(t, cfg, pingRoute())

	resp, _ := gw.app.Test(httptest.NewRequest("GET", "/api/health", nil))
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("configured route without key: expected 401, got %d", resp.StatusCode)
	}
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("X-API-Key", "route-key")
	resp, _ = gw.app.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("configured route with key: expected 200, got %d", resp.StatusCode)
	}

	// Route-level plugins must not leak onto other routes.
	resp, _ = gw.app.Test(httptest.NewRequest("GET", "/api/v1/ping", nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("unrelated route: expected 200, got %d", resp.StatusCode)
	}
}

func TestRouteSkipsNamedGlobalPlugin(t *testing.T) {
	registerHealthProxyInvoker(t)
	server, target := startHealthServer(t)
	defer server.Stop()

	route := healthRoute(target)
	route.SkipGlobalPlugins = []string{"api_key"}
	cfg := config.Default()
	cfg.Gateway.Plugins = []config.GatewayPluginConfig{
		{Name: "api_key", Config: map[string]any{"keys": []any{"k-1"}}},
	}
	cfg.Gateway.Routes = []config.GatewayRouteConfig{route}
	gw := initGateway(t, cfg, pingRoute())

	// The configured route opted out of global auth: reachable without a key.
	resp, _ := gw.app.Test(httptest.NewRequest("GET", "/api/health", nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("skip route without key: expected 200, got %d", resp.StatusCode)
	}

	// Other routes still enforce the global plugin.
	resp, _ = gw.app.Test(httptest.NewRequest("GET", "/api/v1/ping", nil))
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("unrelated route must still enforce auth: expected 401, got %d", resp.StatusCode)
	}
}

func TestRouteSkipsAllGlobalPlugins(t *testing.T) {
	registerHealthProxyInvoker(t)
	server, target := startHealthServer(t)
	defer server.Stop()

	route := healthRoute(target)
	route.SkipGlobalPlugins = []string{"*"}
	cfg := config.Default()
	cfg.Gateway.Plugins = []config.GatewayPluginConfig{
		{Name: "api_key", Config: map[string]any{"keys": []any{"k-1"}}},
	}
	cfg.Gateway.Routes = []config.GatewayRouteConfig{route}
	gw := initGateway(t, cfg)

	resp, _ := gw.app.Test(httptest.NewRequest("GET", "/api/health", nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("skip-all route without key: expected 200, got %d", resp.StatusCode)
	}
}

func TestRouteSkipIsMethodScoped(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Plugins = []config.GatewayPluginConfig{
		{Name: "api_key", Config: map[string]any{"keys": []any{"k-1"}}},
	}
	// A GET route at /api/v1/ping skips auth, but pingRoute is also GET there,
	// so use a distinct path to prove the skip does not leak to other methods.
	cfg.Gateway.Routes = []config.GatewayRouteConfig{{
		Name:              "skip-post",
		Method:            "POST",
		Path:              "/api/v1/ping",
		Target:            "passthrough:///unused",
		RPC:               "/grpc.health.v1.Health/Check",
		SkipGlobalPlugins: []string{"api_key"},
	}}
	registerHealthProxyInvoker(t)
	gw := initGateway(t, cfg, pingRoute())

	// GET keeps enforcing auth even though a POST skip exists on the same path.
	resp, _ := gw.app.Test(httptest.NewRequest("GET", "/api/v1/ping", nil))
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("GET must still enforce auth: expected 401, got %d", resp.StatusCode)
	}
}

func TestCustomProjectPlugin(t *testing.T) {
	plugin.MustRegister("test-header", func(ctx plugin.BuildContext) (plugin.Plugin, error) {
		value, err := ctx.Settings.String("value", "")
		if err != nil {
			return plugin.Plugin{}, err
		}
		return plugin.Plugin{Handler: func(c *fiber.Ctx) error {
			c.Set("X-Custom", value)
			return c.Next()
		}}, nil
	})
	cfg := config.Default()
	cfg.Gateway.Plugins = []config.GatewayPluginConfig{
		{Name: "test-header", Config: map[string]any{"value": "forged"}},
	}
	gw := initGateway(t, cfg, pingRoute())
	resp, _ := gw.app.Test(httptest.NewRequest("GET", "/api/v1/ping", nil))
	if got := resp.Header.Get("X-Custom"); got != "forged" {
		t.Fatalf("expected custom plugin header, got %q", got)
	}
}
