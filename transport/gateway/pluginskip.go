package gateway

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/core/config"
)

// skipAllPlugins is the SkipGlobalPlugins entry that opts a route out of every
// global plugin, turning it into a fully public endpoint.
const skipAllPlugins = "*"

// routePluginSkip records that requests matching a configured route should
// bypass some (or all) global plugins. Matching uses the same pattern semantics
// as fiber routing, so parameterized paths (for example /users/:id) work.
type routePluginSkip struct {
	method  string
	pattern string
	all     bool
	names   map[string]bool
}

// registerPluginSkip records a route's SkipGlobalPlugins so the global plugin
// wrappers can honour it at request time. Routes without a skip list are
// ignored. cfg.Method is expected to be resolved (defaulted) already.
func (g *Gateway) registerPluginSkip(cfg config.GatewayRouteConfig) {
	if len(cfg.SkipGlobalPlugins) == 0 {
		return
	}
	skip := routePluginSkip{
		method:  strings.ToUpper(strings.TrimSpace(cfg.Method)),
		pattern: cfg.Path,
		names:   make(map[string]bool, len(cfg.SkipGlobalPlugins)),
	}
	for _, name := range cfg.SkipGlobalPlugins {
		name = strings.ToLower(strings.TrimSpace(name))
		switch {
		case name == skipAllPlugins:
			skip.all = true
		case name != "":
			skip.names[name] = true
		}
	}
	if !skip.all && len(skip.names) == 0 {
		return
	}
	g.globalSkips = append(g.globalSkips, skip)
}

// wrapGlobalPlugin wraps a global plugin handler so it is bypassed for requests
// targeting a route that opted out of it. Plugins without a name (defensive) are
// returned unwrapped.
func (g *Gateway) wrapGlobalPlugin(name string, handler fiber.Handler) fiber.Handler {
	if name == "" {
		return handler
	}
	return func(c *fiber.Ctx) error {
		if g.skipsGlobalPlugin(name, c) {
			return c.Next()
		}
		return handler(c)
	}
}

// skipsGlobalPlugin reports whether the current request matches a configured
// route that skips the named global plugin.
func (g *Gateway) skipsGlobalPlugin(name string, c *fiber.Ctx) bool {
	if len(g.globalSkips) == 0 {
		return false
	}
	method := c.Method()
	path := c.Path()
	for _, skip := range g.globalSkips {
		if skip.method != "" && skip.method != method {
			continue
		}
		if !skip.all && !skip.names[name] {
			continue
		}
		if fiber.RoutePatternMatch(path, skip.pattern) {
			return true
		}
	}
	return false
}
