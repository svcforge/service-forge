// Package ratelimit applies a per-client request limit using a sliding
// window kept in gateway memory.
//
// Settings:
//
//	max:    requests allowed per window (default 100)
//	window: window length, e.g. "1m" (default 1m)
package ratelimit

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

const Name = "rate_limit"

func Factory(ctx plugin.BuildContext) (plugin.Plugin, error) {
	max, err := ctx.Settings.Int("max", 100)
	if err != nil {
		return plugin.Plugin{}, err
	}
	if max <= 0 {
		return plugin.Plugin{}, fmt.Errorf("max must be positive, got %d", max)
	}
	window, err := ctx.Settings.Duration("window", time.Minute)
	if err != nil {
		return plugin.Plugin{}, err
	}
	if window <= 0 {
		return plugin.Plugin{}, fmt.Errorf("window must be positive, got %s", window)
	}
	handler := limiter.New(limiter.Config{
		Max:        max,
		Expiration: window,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return plugin.WriteError(c, sferrors.New(sferrors.CodeRateLimited, "rate limit exceeded"))
		},
	})
	return plugin.Plugin{Handler: handler}, nil
}
