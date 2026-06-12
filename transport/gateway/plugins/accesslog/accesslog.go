// Package accesslog writes one structured log line per request.
//
// Settings:
//
//	skip_paths: paths excluded from logging, e.g. ["/health"]
package accesslog

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

const Name = "access_log"

func Factory(ctx plugin.BuildContext) (plugin.Plugin, error) {
	skipPaths, err := ctx.Settings.Strings("skip_paths")
	if err != nil {
		return plugin.Plugin{}, err
	}
	skip := make(map[string]struct{}, len(skipPaths))
	for _, path := range skipPaths {
		skip[path] = struct{}{}
	}
	logger := ctx.Logger
	return plugin.Plugin{Handler: func(c *fiber.Ctx) error {
		if _, skipped := skip[c.Path()]; skipped {
			return c.Next()
		}
		start := time.Now()
		err := c.Next()
		if logger != nil {
			logger.Info("request",
				"method", c.Method(),
				"path", c.Path(),
				"status", c.Response().StatusCode(),
				"duration_ms", time.Since(start).Milliseconds(),
				"ip", c.IP(),
				"request_id", c.GetRespHeader(fiber.HeaderXRequestID),
			)
		}
		return err
	}}, nil
}
