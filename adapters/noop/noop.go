package noop

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/svcforge/service-forge/core/module"
	"github.com/svcforge/service-forge/ports/eventbus"
	"github.com/svcforge/service-forge/ports/objectstore"
	"github.com/svcforge/service-forge/ports/registry"
	"github.com/svcforge/service-forge/ports/tracing"
)

var ErrDisabled = errors.New("adapter disabled")

type Cache struct{}

func (Cache) Get(context.Context, string) ([]byte, error)              { return nil, ErrDisabled }
func (Cache) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (Cache) Delete(context.Context, ...string) error                  { return nil }
func (Cache) Exists(context.Context, string) (bool, error)             { return false, nil }

type EventBus struct{}

func (EventBus) Publish(context.Context, string, eventbus.Message) error   { return nil }
func (EventBus) Subscribe(context.Context, string, eventbus.Handler) error { return nil }

type Store struct{}

func (Store) Raw() any { return nil }
func (Store) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

type Registry struct{}

func (Registry) Register(context.Context, registry.ServiceInstance) error            { return nil }
func (Registry) Deregister(context.Context, string) error                            { return nil }
func (Registry) Resolve(context.Context, string) ([]registry.ServiceInstance, error) { return nil, nil }

type TracingProvider struct{}
type Tracer struct{}
type Span struct{}

func (TracingProvider) Tracer(string) tracing.Tracer   { return Tracer{} }
func (TracingProvider) Shutdown(context.Context) error { return nil }
func (Tracer) Start(ctx context.Context, name string, attrs ...tracing.Attribute) (context.Context, tracing.Span) {
	return ctx, Span{}
}
func (Span) End()                               {}
func (Span) RecordError(error)                  {}
func (Span) SetAttributes(...tracing.Attribute) {}

type ObjectStore struct{}

func (ObjectStore) Put(context.Context, string, string, io.Reader, objectstore.PutOptions) error {
	return nil
}
func (ObjectStore) Get(context.Context, string, string) (*objectstore.Object, error) {
	return nil, ErrDisabled
}
func (ObjectStore) Delete(context.Context, string, string) error { return nil }
func (ObjectStore) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	return "", ErrDisabled
}

type Module struct {
	name string
	key  string
	val  any
}

func NewModule(name, key string, value any) Module {
	return Module{name: name, key: key, val: value}
}

func (m Module) Name() string { return m.name }
func (m Module) Init(ctx context.Context, app module.Runtime) error {
	if logger := app.Logger(); logger != nil {
		app.Set("logger."+m.name, logger.With("source", "adapter", "component", m.name))
	}
	if m.key != "" {
		app.Set(m.key, m.val)
	}
	return nil
}
func (m Module) Start(context.Context) error  { return nil }
func (m Module) Stop(context.Context) error   { return nil }
func (m Module) Health(context.Context) error { return nil }
