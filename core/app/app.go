package app

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/core/health"
	"github.com/svcforge/service-forge/core/module"
)

type Option func(*Application)

type Application struct {
	cfg     *config.Config
	logger  module.Logger
	coreLog module.Logger
	modules []module.Module
	values  map[string]any
	health  *health.Registry
	mu      sync.RWMutex
}

func New(cfg *config.Config, opts ...Option) *Application {
	if cfg == nil {
		cfg = config.Default()
	}
	logger := NewSlogLogger(defaultSlog(cfg)).With(
		"app", cfg.App.Name,
		"service", cfg.App.Name,
	)
	app := &Application{
		cfg:     cfg,
		logger:  logger,
		coreLog: logger.With("source", "core", "component", "runtime"),
		values:  map[string]any{},
		health:  health.NewRegistry(),
	}
	for _, opt := range opts {
		opt(app)
	}
	return app
}

func WithLogger(logger module.Logger) Option {
	return func(app *Application) {
		if logger != nil {
			app.logger = logger
			app.coreLog = logger.With("source", "core", "component", "runtime")
		}
	}
}

func WithModules(modules ...module.Module) Option {
	return func(app *Application) {
		app.modules = append(app.modules, modules...)
		for _, mod := range modules {
			app.health.Add(moduleHealth{mod: mod})
		}
	}
}

func (a *Application) Config() *config.Config {
	return a.cfg
}

func (a *Application) Logger() module.Logger {
	return a.logger
}

func (a *Application) Set(key string, value any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.values[key] = value
}

func (a *Application) Get(key string) (any, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	value, ok := a.values[key]
	return value, ok
}

func (a *Application) Health(ctx context.Context) health.Report {
	return a.health.Check(ctx)
}

func (a *Application) Init(ctx context.Context) error {
	for _, mod := range a.modules {
		a.logModuleLifecycle("initializing module", mod.Name())
		if err := mod.Init(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

func (a *Application) Start(ctx context.Context) error {
	for _, mod := range a.modules {
		a.logModuleLifecycle("starting module", mod.Name())
		if err := mod.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (a *Application) Stop(ctx context.Context) error {
	for i := len(a.modules) - 1; i >= 0; i-- {
		mod := a.modules[i]
		a.logModuleLifecycle("stopping module", mod.Name())
		if err := mod.Stop(ctx); err != nil {
			a.coreLog.Error("module stop failed", "module", mod.Name(), "error", err)
		}
	}
	return nil
}

func (a *Application) Run(ctx context.Context) error {
	if err := a.Init(ctx); err != nil {
		return err
	}
	if err := a.Start(ctx); err != nil {
		return err
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-ctx.Done():
	case sig := <-quit:
		a.coreLog.Info("received shutdown signal", "signal", sig.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return a.Stop(shutdownCtx)
}

func (a *Application) logModuleLifecycle(message, moduleName string) {
	if a.cfg.Log.ModuleLifecycle {
		a.coreLog.Info(message, "module", moduleName)
	}
}

type moduleHealth struct {
	mod module.Module
}

func (m moduleHealth) Name() string {
	return m.mod.Name()
}

func (m moduleHealth) Health(ctx context.Context) error {
	return m.mod.Health(ctx)
}

type SlogLogger struct {
	base *slog.Logger
}

func NewSlogLogger(base *slog.Logger) SlogLogger {
	return SlogLogger{base: base}
}

func (l SlogLogger) With(fields ...any) module.Logger {
	return SlogLogger{base: l.base.With(fields...)}
}

func (l SlogLogger) Debug(msg string, fields ...any) { l.base.Debug(msg, fields...) }
func (l SlogLogger) Info(msg string, fields ...any)  { l.base.Info(msg, fields...) }
func (l SlogLogger) Warn(msg string, fields ...any)  { l.base.Warn(msg, fields...) }
func (l SlogLogger) Error(msg string, fields ...any) { l.base.Error(msg, fields...) }

func defaultSlog(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.Log.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Log.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
