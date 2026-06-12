// Package recovery turns panics in downstream handlers into the standard
// INTERNAL failure envelope instead of crashing the gateway.
package recovery

import (
	"runtime/debug"

	"github.com/gofiber/fiber/v2"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

const Name = "recovery"

func Factory(ctx plugin.BuildContext) (plugin.Plugin, error) {
	logger := ctx.Logger
	return plugin.Plugin{Handler: func(c *fiber.Ctx) (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if logger != nil {
					logger.Error("panic recovered",
						"panic", recovered,
						"method", c.Method(),
						"path", c.Path(),
						"stack", string(debug.Stack()),
					)
				}
				err = plugin.WriteError(c, sferrors.New(sferrors.CodeInternal, "internal server error"))
			}
		}()
		return c.Next()
	}}, nil
}
