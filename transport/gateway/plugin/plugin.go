// Package plugin defines the gateway plugin framework. Built-in plugins live
// in transport/gateway/plugins. Projects register custom plugins with
// Register before the gateway module is initialized:
//
//	plugin.MustRegister("my-auth", func(ctx plugin.BuildContext) (plugin.Plugin, error) {
//		return plugin.Plugin{Handler: myAuthHandler}, nil
//	})
//
// Plugins are off by default. Only plugins listed under gateway.plugins (or a
// route's plugins) in config are built and mounted, in config order.
package plugin

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/core/module"
)

// Plugin is the built artifact a Factory produces for one config entry.
type Plugin struct {
	// Handler is mounted on the request chain. May be nil for plugins that
	// only mount routes (for example a metrics endpoint).
	Handler fiber.Handler
	// Mount lets a plugin register its own routes on the gateway app, for
	// example GET /metrics. May be nil.
	Mount func(app *fiber.App) error
}

// BuildContext carries everything a Factory may need at build time.
type BuildContext struct {
	AppName  string
	Settings Settings
	Logger   module.Logger
}

// Factory builds a Plugin from one gateway.plugins config entry.
type Factory func(ctx BuildContext) (Plugin, error)

// Registry maps plugin names to factories.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

func (r *Registry) Register(name string, factory Factory) error {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return fmt.Errorf("plugin name is required")
	}
	if factory == nil {
		return fmt.Errorf("plugin %s: factory is required", normalized)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[normalized]; exists {
		return fmt.Errorf("plugin %s is already registered", normalized)
	}
	r.factories[normalized] = factory
	return nil
}

func (r *Registry) MustRegister(name string, factory Factory) {
	if err := r.Register(name, factory); err != nil {
		panic(err)
	}
}

func (r *Registry) Lookup(name string) (Factory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	factory, ok := r.factories[strings.ToLower(strings.TrimSpace(name))]
	return factory, ok
}

// Names returns the registered plugin names sorted alphabetically.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Build resolves enabled config entries against the registry, in config
// order. Unknown plugin names fail fast so config typos surface at startup.
func (r *Registry) Build(specs []config.GatewayPluginConfig, base BuildContext) ([]Plugin, error) {
	plugins := make([]Plugin, 0, len(specs))
	for _, spec := range specs {
		if !spec.IsEnabled() {
			continue
		}
		factory, ok := r.Lookup(spec.Name)
		if !ok {
			return nil, fmt.Errorf("gateway plugin %q is not registered (available: %s)",
				spec.Name, strings.Join(r.Names(), ", "))
		}
		built, err := factory(BuildContext{
			AppName:  base.AppName,
			Settings: Settings(spec.Config),
			Logger:   base.Logger,
		})
		if err != nil {
			return nil, fmt.Errorf("gateway plugin %q: %w", spec.Name, err)
		}
		plugins = append(plugins, built)
	}
	return plugins, nil
}

// WriteError writes the standard Service Forge failure envelope so plugin
// rejections look identical to handler errors.
func WriteError(c *fiber.Ctx, err *sferrors.AppError) error {
	return c.Status(err.HTTPStatus).JSON(sferrors.Failure(err))
}

var defaultRegistry = NewRegistry()

// Default returns the process-wide registry used by the gateway module.
func Default() *Registry {
	return defaultRegistry
}

// Register adds a custom plugin to the default registry.
func Register(name string, factory Factory) error {
	return defaultRegistry.Register(name, factory)
}

// MustRegister adds a custom plugin to the default registry and panics on
// conflict. Intended for package init in generated projects.
func MustRegister(name string, factory Factory) {
	defaultRegistry.MustRegister(name, factory)
}
