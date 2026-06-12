package plugin

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/core/config"
)

func boolPtr(v bool) *bool { return &v }

func TestRegistryRegisterValidation(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("", func(BuildContext) (Plugin, error) { return Plugin{}, nil }); err == nil {
		t.Fatal("expected error for empty name")
	}
	if err := registry.Register("demo", nil); err == nil {
		t.Fatal("expected error for nil factory")
	}
	if err := registry.Register("demo", func(BuildContext) (Plugin, error) { return Plugin{}, nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := registry.Register("Demo", func(BuildContext) (Plugin, error) { return Plugin{}, nil }); err == nil {
		t.Fatal("expected duplicate registration error")
	}
}

func TestRegistryBuildRespectsEnabledAndOrder(t *testing.T) {
	registry := NewRegistry()
	var order []string
	for _, name := range []string{"first", "second", "third"} {
		registry.MustRegister(name, func(BuildContext) (Plugin, error) {
			order = append(order, name)
			return Plugin{Handler: func(c *fiber.Ctx) error { return c.Next() }}, nil
		})
	}
	specs := []config.GatewayPluginConfig{
		{Name: "second"},
		{Name: "third", Enabled: boolPtr(false)},
		{Name: "first", Enabled: boolPtr(true)},
	}
	plugins, err := registry.Build(specs, BuildContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(plugins))
	}
	if order[0] != "second" || order[1] != "first" {
		t.Fatalf("expected config order [second first], got %v", order)
	}
}

func TestRegistryBuildUnknownPlugin(t *testing.T) {
	registry := NewRegistry()
	_, err := registry.Build([]config.GatewayPluginConfig{{Name: "missing"}}, BuildContext{})
	if err == nil {
		t.Fatal("expected error for unknown plugin")
	}
}

func TestRegistryBuildPassesSettings(t *testing.T) {
	registry := NewRegistry()
	registry.MustRegister("echo", func(ctx BuildContext) (Plugin, error) {
		value, err := ctx.Settings.String("greeting", "")
		if err != nil {
			return Plugin{}, err
		}
		return Plugin{Handler: func(c *fiber.Ctx) error {
			return c.SendString(value)
		}}, nil
	})
	plugins, err := registry.Build([]config.GatewayPluginConfig{
		{Name: "echo", Config: map[string]any{"greeting": "hello"}},
	}, BuildContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	app := fiber.New()
	app.Get("/", plugins[0].Handler)
	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
