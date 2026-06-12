package apikey

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

func newApp(t *testing.T, settings plugin.Settings) *fiber.App {
	t.Helper()
	built, err := Factory(plugin.BuildContext{Settings: settings})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	app := fiber.New()
	app.Use(built.Handler)
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func TestFactoryRequiresKeys(t *testing.T) {
	if _, err := Factory(plugin.BuildContext{Settings: plugin.Settings{}}); err == nil {
		t.Fatal("expected error when keys are missing")
	}
}

func TestAcceptsValidHeaderKey(t *testing.T) {
	app := newApp(t, plugin.Settings{"keys": []any{"secret-1", "secret-2"}})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "secret-2")
	resp, _ := app.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRejectsMissingAndWrongKey(t *testing.T) {
	app := newApp(t, plugin.Settings{"keys": []any{"secret-1"}})
	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("missing key: expected 401, got %d", resp.StatusCode)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	resp, _ = app.Test(req)
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("wrong key: expected 401, got %d", resp.StatusCode)
	}
}

func TestCustomHeaderAndQueryFallback(t *testing.T) {
	app := newApp(t, plugin.Settings{
		"keys":   []any{"secret-1"},
		"header": "X-Token",
		"query":  "token",
	})
	req := httptest.NewRequest("GET", "/?token=secret-1", nil)
	resp, _ := app.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("query key: expected 200, got %d", resp.StatusCode)
	}
}
