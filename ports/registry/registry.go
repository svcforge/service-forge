package registry

import "context"

type ServiceInstance struct {
	ID       string
	Name     string
	Version  string
	Address  string
	Port     int
	Protocol string
	Tags     []string
	Metadata map[string]string
}

type Registrar interface {
	Register(ctx context.Context, service ServiceInstance) error
	Deregister(ctx context.Context, serviceID string) error
}

type Resolver interface {
	Resolve(ctx context.Context, serviceName string) ([]ServiceInstance, error)
}

type Registry interface {
	Registrar
	Resolver
}
