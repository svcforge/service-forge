package ratelimit

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

func TestFactoryValidatesSettings(t *testing.T) {
	if _, err := Factory(plugin.BuildContext{Settings: plugin.Settings{"max": -1}}); err == nil {
		t.Fatal("expected error for negative max")
	}
	if _, err := Factory(plugin.BuildContext{Settings: plugin.Settings{"window": "bogus"}}); err == nil {
		t.Fatal("expected error for invalid window")
	}
}

func TestLimitsRequestsOverMax(t *testing.T) {
	built, err := Factory(plugin.BuildContext{Settings: plugin.Settings{"max": 2, "window": "1m"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	app := fiber.New()
	app.Use(built.Handler)
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	for i := range 2 {
		resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, resp.StatusCode)
		}
	}
	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
}
