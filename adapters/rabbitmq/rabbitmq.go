package rabbitmq

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/core/module"
	"github.com/svcforge/service-forge/ports/eventbus"
)

type Config struct {
	URL      string `yaml:"url"`
	Exchange string `yaml:"exchange"`
}

type EventBus struct {
	conn     *amqp.Connection
	channel  *amqp.Channel
	exchange string
}

func New(cfg Config) (*EventBus, error) {
	if cfg.URL == "" {
		cfg.URL = "amqp://guest:guest@localhost:5672/"
	}
	if cfg.Exchange == "" {
		cfg.Exchange = "events"
	}
	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := ch.ExchangeDeclare(cfg.Exchange, "topic", true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	return &EventBus{conn: conn, channel: ch, exchange: cfg.Exchange}, nil
}

func (b *EventBus) Publish(ctx context.Context, topic string, msg eventbus.Message) error {
	headers := amqp.Table{}
	for key, value := range msg.Headers {
		headers[key] = value
	}
	return b.channel.PublishWithContext(ctx, b.exchange, topic, false, false, amqp.Publishing{
		MessageId:     msg.ID,
		ContentType:   msg.ContentType,
		CorrelationId: msg.Key,
		Headers:       headers,
		Body:          msg.Body,
	})
}

func (b *EventBus) Subscribe(ctx context.Context, topic string, handler eventbus.Handler) error {
	queue, err := b.channel.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		return err
	}
	if err := b.channel.QueueBind(queue.Name, topic, b.exchange, false, nil); err != nil {
		return err
	}
	deliveries, err := b.channel.Consume(queue.Name, "", false, true, false, false, nil)
	if err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case delivery, ok := <-deliveries:
				if !ok {
					return
				}
				headers := map[string]string{}
				for key, value := range delivery.Headers {
					headers[key] = fmt.Sprint(value)
				}
				err := handler(ctx, eventbus.Message{
					ID:          delivery.MessageId,
					Topic:       delivery.RoutingKey,
					Key:         delivery.CorrelationId,
					Headers:     headers,
					ContentType: delivery.ContentType,
					Body:        delivery.Body,
				})
				if err != nil {
					_ = delivery.Nack(false, true)
					continue
				}
				_ = delivery.Ack(false)
			}
		}
	}()
	return nil
}

func (b *EventBus) Close() error {
	if b.channel != nil {
		_ = b.channel.Close()
	}
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

func (b *EventBus) Health(ctx context.Context) error {
	if b.conn == nil || b.conn.IsClosed() {
		return fmt.Errorf("rabbitmq connection is closed")
	}
	return nil
}

type Module struct {
	bus *EventBus
}

func NewModule() *Module { return &Module{} }

func (m *Module) Name() string { return "eventbus.rabbitmq" }

func (m *Module) Init(ctx context.Context, app module.Runtime) error {
	cfg, err := config.ModuleConfig[Config](app.Config(), "rabbitmq")
	if err != nil {
		return err
	}
	bus, err := New(*cfg)
	if err != nil {
		return err
	}
	m.bus = bus
	app.Set("eventbus", bus)
	app.Set("publisher", bus)
	app.Set("subscriber", bus)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error {
	if m.bus == nil {
		return nil
	}
	return m.bus.Close()
}

func (m *Module) Health(ctx context.Context) error {
	if m.bus == nil {
		return nil
	}
	return m.bus.Health(ctx)
}
