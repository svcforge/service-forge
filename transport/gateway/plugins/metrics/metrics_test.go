package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

func TestRecordsAndExposesMetrics(t *testing.T) {
	built, err := Factory(plugin.BuildContext{Settings: plugin.Settings{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	app := fiber.New()
	app.Use(built.Handler)
	app.Get("/api/v1/users/:id", func(c *fiber.Ctx) error { return c.SendString("ok") })
	if err := built.Mount(app); err != nil {
		t.Fatalf("mount: %v", err)
	}

	if _, err := app.Test(httptest.NewRequest("GET", "/api/v1/users/42", nil)); err != nil {
		t.Fatalf("request: %v", err)
	}

	resp, err := app.Test(httptest.NewRequest("GET", "/metrics", nil))
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if !strings.Contains(text, "gateway_http_requests_total") {
		t.Fatalf("expected requests counter in exposition, got:\n%s", text)
	}
	if !strings.Contains(text, `path="/api/v1/users/:id"`) {
		t.Fatalf("expected route pattern label, got:\n%s", text)
	}
}

func TestCustomPathAndNamespace(t *testing.T) {
	built, err := Factory(plugin.BuildContext{Settings: plugin.Settings{
		"path":      "/internal/metrics",
		"namespace": "myapp",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	app := fiber.New()
	app.Use(built.Handler)
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })
	if err := built.Mount(app); err != nil {
		t.Fatalf("mount: %v", err)
	}
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, _ := app.Test(httptest.NewRequest("GET", "/internal/metrics", nil))
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "myapp_http_requests_total") {
		t.Fatal("expected custom namespace in exposition")
	}
}
