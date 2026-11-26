package grpcserver

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/ports/registry"
)

type failingRegistrar struct {
	address string
	err     error
}

func (r *failingRegistrar) Register(_ context.Context, service registry.ServiceInstance) error {
	r.address = config.Address(service.Address, service.Port)
	return r.err
}

func (r *failingRegistrar) Deregister(context.Context, string) error {
	return errors.New("unexpected deregistration")
}

func TestFailedRegistrationReleasesListener(t *testing.T) {
	ctx := context.Background()
	cfg := config.Default()
	cfg.GRPC.ListenIP = "127.0.0.1"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.GRPC.Port = listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	runtime := app.New(cfg)
	registrar := &failingRegistrar{err: errors.New("registry unavailable")}
	runtime.Set("registrar", registrar)
	server := NewModule()
	if err := server.Init(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := server.Start(ctx); !errors.Is(err, registrar.err) {
		t.Fatalf("Start = %v, want registration error", err)
	}
	if err := server.Health(ctx); err == nil {
		t.Fatal("server reported healthy after failed registration")
	}
	rebound, err := net.Listen("tcp", registrar.address)
	if err != nil {
		t.Fatalf("listener still owns %s: %v", registrar.address, err)
	}
	rebound.Close()
}
