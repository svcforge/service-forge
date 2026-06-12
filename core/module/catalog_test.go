package module

import (
	"context"
	"testing"

	"github.com/svcforge/service-forge/core/config"
)

type testModule struct{ BaseModule }

func TestCatalogBuildsEnabledModules(t *testing.T) {
	disabled := false
	catalog := NewCatalog()
	catalog.Register("cache", "memory", func() Module {
		return testModule{BaseModule{ModuleName: "cache.memory"}}
	})
	catalog.Register("cache", "noop", func() Module {
		return testModule{BaseModule{ModuleName: "cache.noop"}}
	})

	modules, err := catalog.Build([]config.ComponentConfig{
		{Name: "cache", Provider: "memory"},
		{Name: "cache", Provider: "noop", Enabled: &disabled},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 1 {
		t.Fatalf("module count = %d", len(modules))
	}
	if modules[0].Name() != "cache.memory" {
		t.Fatalf("module = %s", modules[0].Name())
	}
}

func TestBaseModuleLifecycleIsNoop(t *testing.T) {
	mod := BaseModule{ModuleName: "test"}
	if err := mod.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if mod.Name() != "test" {
		t.Fatalf("name = %s", mod.Name())
	}
}
