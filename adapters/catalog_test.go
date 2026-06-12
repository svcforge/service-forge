package adapters

import (
	"testing"

	"github.com/svcforge/service-forge/core/config"
)

func TestDefaultCatalogBuildsConfiguredModules(t *testing.T) {
	mods, err := DefaultCatalog().Build([]config.ComponentConfig{
		{Name: "cache", Provider: "memory"},
		{Name: "eventbus", Provider: "noop"},
		{Name: "registry", Provider: "memory"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 3 {
		t.Fatalf("module count = %d", len(mods))
	}
}
