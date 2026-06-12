package consul

import (
	"context"
	"fmt"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/core/module"
	"github.com/svcforge/service-forge/ports/registry"
)

type Config struct {
	Address string `yaml:"address"`
	Scheme  string `yaml:"scheme"`
}

type Registry struct {
	client *consulapi.Client
}

func New(cfg Config) (*Registry, error) {
	cc := consulapi.DefaultConfig()
	if cfg.Address != "" {
		cc.Address = cfg.Address
	}
	if cfg.Scheme != "" {
		cc.Scheme = cfg.Scheme
	}
	client, err := consulapi.NewClient(cc)
	if err != nil {
		return nil, err
	}
	return &Registry{client: client}, nil
}

func (r *Registry) Register(ctx context.Context, service registry.ServiceInstance) error {
	return r.client.Agent().ServiceRegister(&consulapi.AgentServiceRegistration{
		ID:      service.ID,
		Name:    service.Name,
		Tags:    service.Tags,
		Address: service.Address,
		Port:    service.Port,
		Meta:    service.Metadata,
		Check: &consulapi.AgentServiceCheck{
			GRPC:                           fmt.Sprintf("%s:%d", service.Address, service.Port),
			Interval:                       "10s",
			Timeout:                        "2s",
			DeregisterCriticalServiceAfter: "1m",
		},
	})
}

func (r *Registry) Deregister(ctx context.Context, serviceID string) error {
	return r.client.Agent().ServiceDeregister(serviceID)
}

func (r *Registry) Resolve(ctx context.Context, serviceName string) ([]registry.ServiceInstance, error) {
	entries, _, err := r.client.Health().Service(serviceName, "", true, nil)
	if err != nil {
		return nil, err
	}
	out := make([]registry.ServiceInstance, 0, len(entries))
	for _, entry := range entries {
		out = append(out, registry.ServiceInstance{
			ID:       entry.Service.ID,
			Name:     entry.Service.Service,
			Address:  entry.Service.Address,
			Port:     entry.Service.Port,
			Tags:     entry.Service.Tags,
			Metadata: entry.Service.Meta,
		})
	}
	return out, nil
}

func (r *Registry) Health(ctx context.Context) error {
	_, err := r.client.Status().Leader()
	return err
}

type Module struct {
	registry *Registry
}

func NewModule() *Module { return &Module{} }

func (m *Module) Name() string { return "registry.consul" }

func (m *Module) Init(ctx context.Context, app module.Runtime) error {
	cfg, err := config.ModuleConfig[Config](app.Config(), "consul")
	if err != nil {
		return err
	}
	reg, err := New(*cfg)
	if err != nil {
		return err
	}
	m.registry = reg
	app.Set("registry", reg)
	app.Set("registrar", reg)
	app.Set("resolver", reg)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) Health(ctx context.Context) error {
	if m.registry == nil {
		return nil
	}
	return m.registry.Health(ctx)
}
