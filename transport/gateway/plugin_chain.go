package gateway

import (
	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

// mountGlobalPlugins applies gateway.plugins to every route registered after
// this point. Plugin-owned endpoints (for example /metrics) are mounted
// before the chain so they stay reachable even when auth or rate limiting is
// enabled.
func (g *Gateway) mountGlobalPlugins(cfg *config.Config) error {
	built, err := plugin.Default().Build(cfg.Gateway.Plugins, g.pluginBuildContext(cfg))
	if err != nil {
		return err
	}
	for _, item := range built {
		if item.Mount == nil {
			continue
		}
		if err := item.Mount(g.app); err != nil {
			return err
		}
	}
	for _, item := range built {
		if item.Handler != nil {
			g.app.Use(item.Handler)
		}
	}
	if g.logger != nil && len(built) > 0 {
		g.logger.Info("mounted gateway plugins", "count", len(built))
	}
	return nil
}

// buildRoutePlugins resolves one route's plugin chain. The returned handlers
// run before the route's proxy handler, in config order.
func (g *Gateway) buildRoutePlugins(cfg *config.Config, routeCfg config.GatewayRouteConfig) ([]fiber.Handler, error) {
	built, err := plugin.Default().Build(routeCfg.Plugins, g.pluginBuildContext(cfg))
	if err != nil {
		return nil, err
	}
	handlers := make([]fiber.Handler, 0, len(built))
	for _, item := range built {
		if item.Mount != nil {
			if err := item.Mount(g.app); err != nil {
				return nil, err
			}
		}
		if item.Handler != nil {
			handlers = append(handlers, item.Handler)
		}
	}
	return handlers, nil
}

func (g *Gateway) pluginBuildContext(cfg *config.Config) plugin.BuildContext {
	return plugin.BuildContext{AppName: cfg.App.Name, Logger: g.logger}
}
