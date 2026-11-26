package gateway

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

type dynamicAuthKey struct{}

type dynamicAuthSelection struct {
	match   dynamicSelection
	method  string
	path    string
	skipped bool
}

func (selection *dynamicAuthSelection) matches(c *fiber.Ctx) bool {
	return selection.method == c.Method() && selection.path == c.Path()
}

func (g *Gateway) skipsDynamicAuthentication(c *fiber.Ctx) bool {
	if g.dynamic == nil {
		return false
	}
	selection, _ := c.Locals(dynamicAuthKey{}).(*dynamicAuthSelection)
	if selection == nil {
		route, found := g.dynamic.selectRoute(c)
		if !found || !route.route.automatic {
			return false
		}
		path := strings.Clone(c.Path())
		route.parts = strings.Split(strings.TrimPrefix(path, "/"), "/")
		selection = &dynamicAuthSelection{
			match: route, method: strings.Clone(c.Method()), path: path,
		}
		c.Locals(dynamicAuthKey{}, selection)
	}
	if !selection.matches(c) || !selection.match.public || g.hasStaticMiddleware {
		return false
	}
	for _, route := range g.staticRoutes {
		if route.Method == selection.method && fiber.RoutePatternMatch(selection.path, route.Path, g.app.Config()) {
			return false
		}
	}
	selection.skipped = true
	return true
}

// guardAnonymousRoute prevents middleware rewrites from taking an anonymous
// request past the global chain to a different endpoint.
func (g *Gateway) guardAnonymousRoute(c *fiber.Ctx) error {
	selection, _ := c.Locals(dynamicAuthKey{}).(*dynamicAuthSelection)
	if selection != nil && selection.skipped && !selection.matches(c) {
		return fiber.ErrForbidden
	}
	return c.Next()
}

func (d *dynamicRoutes) requestRoute(c *fiber.Ctx) (dynamicSelection, bool) {
	// Use the same revision for authentication and dispatch.
	if selection, ok := c.Locals(dynamicAuthKey{}).(*dynamicAuthSelection); ok && selection.matches(c) {
		return selection.match, true
	}
	return d.selectRoute(c)
}
