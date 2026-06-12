package memory

import (
	"context"
	"sync"

	"github.com/svcforge/service-forge/core/module"
	"github.com/svcforge/service-forge/ports/eventbus"
)

type EventBus struct {
	mu       sync.RWMutex
	handlers map[string][]eventbus.Handler
}

func NewEventBus() *EventBus {
	return &EventBus{handlers: map[string][]eventbus.Handler{}}
}

func (b *EventBus) Publish(ctx context.Context, topic string, msg eventbus.Message) error {
	msg.Topic = topic
	b.mu.RLock()
	handlers := append([]eventbus.Handler(nil), b.handlers[topic]...)
	b.mu.RUnlock()
	for _, handler := range handlers {
		if err := handler(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

func (b *EventBus) Subscribe(ctx context.Context, topic string, handler eventbus.Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[topic] = append(b.handlers[topic], handler)
	return nil
}

type EventBusModule struct {
	*EventBus
}

func NewEventBusModule() *EventBusModule {
	return &EventBusModule{EventBus: NewEventBus()}
}

func (m *EventBusModule) Name() string { return "eventbus.memory" }

func (m *EventBusModule) Init(ctx context.Context, app module.Runtime) error {
	app.Set("eventbus", m.EventBus)
	app.Set("publisher", m.EventBus)
	app.Set("subscriber", m.EventBus)
	return nil
}

func (m *EventBusModule) Start(context.Context) error  { return nil }
func (m *EventBusModule) Stop(context.Context) error   { return nil }
func (m *EventBusModule) Health(context.Context) error { return nil }
