package recovery

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

func TestRecoveryConvertsPanicToEnvelope(t *testing.T) {
	built, err := Factory(plugin.BuildContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	app := fiber.New()
	app.Use(built.Handler)
	app.Get("/boom", func(c *fiber.Ctx) error {
		panic("kaboom")
	})
	resp, err := app.Test(httptest.NewRequest("GET", "/boom", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("invalid json body: %v", err)
	}
	if body["code"] != "INTERNAL" {
		t.Fatalf("expected INTERNAL code, got %v", body["code"])
	}
}

func TestRecoveryPassesThrough(t *testing.T) {
	built, err := Factory(plugin.BuildContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	app := fiber.New()
	app.Use(built.Handler)
	app.Get("/ok", func(c *fiber.Ctx) error { return c.SendString("ok") })
	resp, err := app.Test(httptest.NewRequest("GET", "/ok", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
