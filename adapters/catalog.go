package adapters

import (
	"github.com/svcforge/service-forge/adapters/consul"
	"github.com/svcforge/service-forge/adapters/memory"
	"github.com/svcforge/service-forge/adapters/noop"
	"github.com/svcforge/service-forge/adapters/otel"
	"github.com/svcforge/service-forge/adapters/postgres"
	"github.com/svcforge/service-forge/adapters/rabbitmq"
	"github.com/svcforge/service-forge/adapters/redis"
	"github.com/svcforge/service-forge/core/module"
)

func DefaultCatalog() *module.Catalog {
	catalog := module.NewCatalog()

	catalog.Register("cache", "redis", func() module.Module { return redis.NewModule() })
	catalog.Register("cache", "memory", func() module.Module { return memory.NewCacheModule() })
	catalog.Register("cache", "noop", func() module.Module {
		return noop.NewModule("cache.noop", "cache", noop.Cache{})
	})

	catalog.Register("store", "postgres", func() module.Module { return postgres.NewModule() })
	catalog.Register("store", "noop", func() module.Module {
		return noop.NewModule("store.noop", "store", noop.Store{})
	})

	catalog.Register("eventbus", "rabbitmq", func() module.Module { return rabbitmq.NewModule() })
	catalog.Register("eventbus", "memory", func() module.Module { return memory.NewEventBusModule() })
	catalog.Register("eventbus", "noop", func() module.Module {
		return noop.NewModule("eventbus.noop", "eventbus", noop.EventBus{})
	})

	catalog.Register("registry", "consul", func() module.Module { return consul.NewModule() })
	catalog.Register("registry", "memory", func() module.Module { return memory.NewRegistryModule() })
	catalog.Register("registry", "noop", func() module.Module {
		return noop.NewModule("registry.noop", "registry", noop.Registry{})
	})

	catalog.Register("tracing", "otel", func() module.Module { return otel.NewModule() })
	catalog.Register("tracing", "noop", func() module.Module {
		return noop.NewModule("tracing.noop", "tracing", noop.TracingProvider{})
	})

	return catalog
}
