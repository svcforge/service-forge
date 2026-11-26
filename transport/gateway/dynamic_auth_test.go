package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/ports/registry"
	"github.com/svcforge/service-forge/transport/routeconfig"
)

func TestProtobufRoutesUseGlobalAuthentication(t *testing.T) {
	jwt := config.GatewayPluginConfig{Name: "jwt", Config: map[string]any{"secret": "fixture"}}
	disabled := jwt
	disabled.Enabled = boolPtr(false)
	key := config.GatewayPluginConfig{Name: "api_key", Config: map[string]any{"keys": []any{"fixture"}}}
	for _, tc := range []struct {
		name       string
		plugins    []config.GatewayPluginConfig
		path       string
		anonymous  bool
		static     bool
		headerName string
		header     string
		want       int
	}{
		{name: "open by default", path: "/resource/new", want: 503},
		{name: "disabled JWT", plugins: []config.GatewayPluginConfig{disabled}, path: "/resource/new", want: 503},
		{name: "global JWT", plugins: []config.GatewayPluginConfig{jwt}, path: "/resource/new", want: 401},
		{name: "anonymous with JWT", plugins: []config.GatewayPluginConfig{jwt}, path: "/resource/new", anonymous: true, want: 503},
		{name: "global API key", plugins: []config.GatewayPluginConfig{key}, path: "/resource/new", want: 401},
		{name: "valid API key", plugins: []config.GatewayPluginConfig{key}, path: "/resource/new", headerName: "X-API-Key", header: "fixture", want: 503},
		{name: "anonymous with API key", plugins: []config.GatewayPluginConfig{key}, path: "/resource/new", anonymous: true, want: 503},
		{name: "static route remains protected", plugins: []config.GatewayPluginConfig{jwt}, path: "/resource/new", anonymous: true, static: true, want: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Gateway.Plugins = tc.plugins
			runtime := app.New(cfg)
			catalog := &testRouteCatalog{}
			runtime.Set(registry.RouteCatalogKey, catalog)
			runtime.Set("resolver", catalog)
			gw := New()
			if tc.static {
				gw = New(func(router *fiber.App, _ *Gateway) {
					router.Get("/resource/:id", func(c *fiber.Ctx) error { return c.SendStatus(200) })
				})
			}
			if err := gw.Init(context.Background(), runtime); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = gw.Stop(context.Background()) })
			route := dynamicRoute{
				Route:     routeconfig.Route{Method: http.MethodGet, Path: tc.path},
				parts:     strings.Split(strings.TrimPrefix(tc.path, "/"), "/"),
				automatic: true, public: tc.anonymous,
			}
			// A 503 means authentication let the request reach the dynamic
			// handler, which has no healthy targets in this fixture.
			gw.dynamic.table.Store(&dynamicSnapshot{
				matches: []dynamicMatch{{route: route, service: &dynamicService{}}},
			})
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.headerName != "" {
				req.Header.Set(tc.headerName, tc.header)
			}
			resp, err := gw.app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}
