package grpcclient

import (
	"context"
	"fmt"
	"sync"

	"github.com/svcforge/service-forge/ports/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Dialer struct {
	resolver registry.Resolver
	mu       sync.Mutex
	conns    map[string]*grpc.ClientConn
	options  []grpc.DialOption
}

func NewDialer(resolver registry.Resolver, opts ...grpc.DialOption) *Dialer {
	if len(opts) == 0 {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	return &Dialer{
		resolver: resolver,
		conns:    map[string]*grpc.ClientConn{},
		options:  opts,
	}
}

func (d *Dialer) DialTarget(ctx context.Context, target string) (*grpc.ClientConn, error) {
	d.mu.Lock()
	if conn, ok := d.conns[target]; ok {
		d.mu.Unlock()
		return conn, nil
	}
	d.mu.Unlock()

	conn, err := grpc.NewClient(target, d.options...)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	d.conns[target] = conn
	d.mu.Unlock()
	return conn, nil
}

func (d *Dialer) DialService(ctx context.Context, serviceName string) (*grpc.ClientConn, error) {
	if d.resolver == nil {
		return d.DialTarget(ctx, serviceName)
	}
	instances, err := d.resolver.Resolve(ctx, serviceName)
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("service not found: %s", serviceName)
	}
	target := fmt.Sprintf("%s:%d", instances[0].Address, instances[0].Port)
	return d.DialTarget(ctx, target)
}

func (d *Dialer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var firstErr error
	for target, conn := range d.conns {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(d.conns, target)
	}
	return firstErr
}
