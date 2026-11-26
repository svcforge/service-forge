package grpcclient

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
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
	next     map[string]int
}

func NewDialer(resolver registry.Resolver, opts ...grpc.DialOption) *Dialer {
	if len(opts) == 0 {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	return &Dialer{
		resolver: resolver,
		conns:    map[string]*grpc.ClientConn{},
		options:  opts,
		next:     make(map[string]int),
	}
}

func (d *Dialer) DialTarget(ctx context.Context, target string) (*grpc.ClientConn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if conn, ok := d.conns[target]; ok {
		return conn, nil
	}
	conn, err := grpc.NewClient(target, d.options...)
	if err != nil {
		return nil, err
	}
	d.conns[target] = conn
	return conn, nil
}

func (d *Dialer) DialTargetFresh(_ context.Context, target string) (*grpc.ClientConn, error) {
	return grpc.NewClient(target, d.options...)
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
	targets := make([]string, 0, len(instances))
	for _, instance := range instances {
		targets = append(targets, net.JoinHostPort(instance.Address, strconv.Itoa(instance.Port)))
	}
	slices.Sort(targets)
	targets = slices.Compact(targets)
	d.mu.Lock()
	index := d.next[serviceName] % len(targets)
	d.next[serviceName] = (index + 1) % len(targets)
	d.mu.Unlock()
	return d.DialTarget(ctx, targets[index])
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
