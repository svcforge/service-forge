package module

import (
	"context"

	"github.com/svcforge/service-forge/core/config"
)

// Logger is the small logging surface required by framework modules.
type Logger interface {
	With(fields ...any) Logger
	Debug(msg string, fields ...any)
	Info(msg string, fields ...any)
	Warn(msg string, fields ...any)
	Error(msg string, fields ...any)
}

// Runtime is the app surface exposed to modules during composition.
type Runtime interface {
	Config() *config.Config
	Logger() Logger
	Set(key string, value any)
	Get(key string) (any, bool)
}

// Module is the lifecycle contract for runtime components.
type Module interface {
	Name() string
	Init(ctx context.Context, app Runtime) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Health(ctx context.Context) error
}

// BaseModule provides no-op lifecycle methods for simple modules.
type BaseModule struct {
	ModuleName string
}

func (m BaseModule) Name() string {
	return m.ModuleName
}

func (m BaseModule) Init(context.Context, Runtime) error {
	return nil
}

func (m BaseModule) Start(context.Context) error {
	return nil
}

func (m BaseModule) Stop(context.Context) error {
	return nil
}

func (m BaseModule) Health(context.Context) error {
	return nil
}
