package memory

import (
	"context"
	"sync"

	"github.com/svcforge/service-forge/core/module"
	"github.com/svcforge/service-forge/ports/registry"
)

type Registry struct {
	mu       sync.RWMutex
	services map[string]registry.ServiceInstance
}

func NewRegistry() *Registry {
	return &Registry{services: map[string]registry.ServiceInstance{}}
}

func (r *Registry) Register(ctx context.Context, service registry.ServiceInstance) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services[service.ID] = service
	return nil
}

func (r *Registry) Deregister(ctx context.Context, serviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.services, serviceID)
	return nil
}

func (r *Registry) Resolve(ctx context.Context, serviceName string) ([]registry.ServiceInstance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	services := make([]registry.ServiceInstance, 0)
	for _, service := range r.services {
		if service.Name == serviceName {
			services = append(services, service)
		}
	}
	return services, nil
}

type RegistryModule struct {
	*Registry
}

func NewRegistryModule() *RegistryModule {
	return &RegistryModule{Registry: NewRegistry()}
}

func (m *RegistryModule) Name() string { return "registry.memory" }

func (m *RegistryModule) Init(ctx context.Context, app module.Runtime) error {
	app.Set("registry", m.Registry)
	app.Set("registrar", m.Registry)
	app.Set("resolver", m.Registry)
	return nil
}

func (m *RegistryModule) Start(context.Context) error  { return nil }
func (m *RegistryModule) Stop(context.Context) error   { return nil }
func (m *RegistryModule) Health(context.Context) error { return nil }
