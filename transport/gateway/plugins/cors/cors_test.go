package cors

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

func TestPreflightUsesConfiguredOrigins(t *testing.T) {
	built, err := Factory(plugin.BuildContext{Settings: plugin.Settings{
		"allow_origins": []any{"https://app.example.com"},
		"allow_methods": []any{"GET", "POST"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	app := fiber.New()
	app.Use(built.Handler)
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set(fiber.HeaderOrigin, "https://app.example.com")
	req.Header.Set(fiber.HeaderAccessControlRequestMethod, "GET")
	resp, _ := app.Test(req)
	if got := resp.Header.Get(fiber.HeaderAccessControlAllowOrigin); got != "https://app.example.com" {
		t.Fatalf("expected allow-origin header, got %q", got)
	}

	req = httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set(fiber.HeaderOrigin, "https://evil.example.com")
	req.Header.Set(fiber.HeaderAccessControlRequestMethod, "GET")
	resp, _ = app.Test(req)
	if got := resp.Header.Get(fiber.HeaderAccessControlAllowOrigin); got != "" {
		t.Fatalf("expected no allow-origin for unknown origin, got %q", got)
	}
}

func TestCredentialsWithWildcardOriginFails(t *testing.T) {
	_, err := Factory(plugin.BuildContext{Settings: plugin.Settings{
		"allow_credentials": true,
	}})
	if err == nil {
		t.Fatal("expected error for credentials with wildcard origin")
	}
}
