package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMergesConfigByPriority(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "base.yaml"), `
app:
  name: base
gateway:
  port: 8080
modules:
  redis:
    addr: base:6379
`)
	mustWrite(t, filepath.Join(dir, "envs", "development.yaml"), `
gateway:
  port: 8081
modules:
  redis:
    db: 2
`)
	mustWrite(t, filepath.Join(dir, "services", "user-service.yaml"), `
gateway:
  port: 8082
`)
	mustWrite(t, filepath.Join(dir, "local.yaml"), `
modules:
  redis:
    addr: local:6379
`)

	bundle, err := Load[struct{}](LoadOptions{
		ConfigDir:   dir,
		ServiceName: "user-service",
		Environment: "development",
		EnableLocal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Core.App.Name != "user-service" {
		t.Fatalf("service name = %q", bundle.Core.App.Name)
	}
	if bundle.Core.Gateway.Port != 8082 {
		t.Fatalf("gateway port = %d", bundle.Core.Gateway.Port)
	}
	redisCfg, err := ModuleConfig[struct {
		Addr string `yaml:"addr"`
		DB   int    `yaml:"db"`
	}](bundle.Core, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if redisCfg.Addr != "local:6379" || redisCfg.DB != 2 {
		t.Fatalf("unexpected redis cfg: %+v", redisCfg)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
