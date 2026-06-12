package eventbus

import "context"

type Message struct {
	ID          string
	Topic       string
	Key         string
	Headers     map[string]string
	ContentType string
	Body        []byte
}

type Publisher interface {
	Publish(ctx context.Context, topic string, msg Message) error
}

type Handler func(ctx context.Context, msg Message) error

type Subscriber interface {
	Subscribe(ctx context.Context, topic string, handler Handler) error
}

type EventBus interface {
	Publisher
	Subscriber
}
