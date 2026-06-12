package tracing

import "context"

type Attribute struct {
	Key   string
	Value string
}

type Span interface {
	End()
	RecordError(err error)
	SetAttributes(attrs ...Attribute)
}

type Tracer interface {
	Start(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span)
}

type Provider interface {
	Tracer(name string) Tracer
	Shutdown(ctx context.Context) error
}
