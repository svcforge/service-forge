package otel

import (
	"context"
	"time"

	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/core/module"
	porttrace "github.com/svcforge/service-forge/ports/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	Enabled     bool    `yaml:"enabled"`
	Endpoint    string  `yaml:"endpoint"`
	ServiceName string  `yaml:"service_name"`
	SampleRate  float64 `yaml:"sample_rate"`
}

type Provider struct {
	provider *sdktrace.TracerProvider
}

func New(ctx context.Context, cfg Config, serviceName string) (*Provider, error) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = serviceName
	}
	if cfg.SampleRate <= 0 || cfg.SampleRate > 1 {
		cfg.SampleRate = 1
	}
	opts := []otlptracegrpc.Option{}
	if cfg.Endpoint != "" {
		opts = append(opts, otlptracegrpc.WithEndpoint(cfg.Endpoint), otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(cfg.ServiceName)))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SampleRate)),
	)
	otel.SetTracerProvider(tp)
	return &Provider{provider: tp}, nil
}

func (p *Provider) Tracer(name string) porttrace.Tracer {
	return Tracer{tracer: p.provider.Tracer(name)}
}

func (p *Provider) Shutdown(ctx context.Context) error {
	return p.provider.Shutdown(ctx)
}

type Tracer struct {
	tracer trace.Tracer
}

func (t Tracer) Start(ctx context.Context, name string, attrs ...porttrace.Attribute) (context.Context, porttrace.Span) {
	converted := make([]attribute.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		converted = append(converted, attribute.String(attr.Key, attr.Value))
	}
	ctx, span := t.tracer.Start(ctx, name, trace.WithAttributes(converted...))
	return ctx, Span{span: span}
}

type Span struct {
	span trace.Span
}

func (s Span) End() {
	s.span.End()
}

func (s Span) RecordError(err error) {
	s.span.RecordError(err)
}

func (s Span) SetAttributes(attrs ...porttrace.Attribute) {
	converted := make([]attribute.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		converted = append(converted, attribute.String(attr.Key, attr.Value))
	}
	s.span.SetAttributes(converted...)
}

type Module struct {
	provider *Provider
}

func NewModule() *Module { return &Module{} }

func (m *Module) Name() string { return "tracing.otel" }

func (m *Module) Init(ctx context.Context, app module.Runtime) error {
	cfg, err := config.ModuleConfig[Config](app.Config(), "otel")
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	provider, err := New(ctx, *cfg, app.Config().App.Name)
	if err != nil {
		return err
	}
	m.provider = provider
	app.Set("tracing", provider)
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(ctx context.Context) error {
	if m.provider == nil {
		return nil
	}
	return m.provider.Shutdown(ctx)
}

func (m *Module) Health(context.Context) error { return nil }
