package memory

import (
	"context"
	"testing"
	"time"

	"github.com/svcforge/service-forge/ports/eventbus"
	"github.com/svcforge/service-forge/ports/registry"
)

func TestCacheSetGetExistsDelete(t *testing.T) {
	ctx := context.Background()
	cache := NewCache()
	if err := cache.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	ok, err := cache.Exists(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("exists = %v, %v", ok, err)
	}
	value, err := cache.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "v" {
		t.Fatalf("value = %q", value)
	}
	if err := cache.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	ok, _ = cache.Exists(ctx, "k")
	if ok {
		t.Fatal("expected key to be deleted")
	}
}

func TestEventBusPublishSubscribe(t *testing.T) {
	ctx := context.Background()
	bus := NewEventBus()
	var got string
	if err := bus.Subscribe(ctx, "users.created", func(ctx context.Context, msg eventbus.Message) error {
		got = string(msg.Body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, "users.created", eventbus.Message{Body: []byte("ok")}); err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Fatalf("got = %q", got)
	}
}

func TestRegistryResolve(t *testing.T) {
	ctx := context.Background()
	reg := NewRegistry()
	if err := reg.Register(ctx, registry.ServiceInstance{ID: "1", Name: "user", Address: "127.0.0.1", Port: 9000}); err != nil {
		t.Fatal(err)
	}
	services, err := reg.Resolve(ctx, "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Port != 9000 {
		t.Fatalf("services = %+v", services)
	}
}
