// Package apikey rejects requests that do not present a configured API key.
//
// Settings:
//
//	keys:   accepted keys (required, non-empty)
//	header: request header carrying the key (default "X-API-Key")
//	query:  optional query parameter checked when the header is absent
package apikey

import (
	"crypto/subtle"
	"fmt"

	"github.com/gofiber/fiber/v2"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

const Name = "api_key"

func Factory(ctx plugin.BuildContext) (plugin.Plugin, error) {
	keys, err := ctx.Settings.Strings("keys")
	if err != nil {
		return plugin.Plugin{}, err
	}
	if len(keys) == 0 {
		return plugin.Plugin{}, fmt.Errorf("at least one key is required")
	}
	header, err := ctx.Settings.String("header", "X-API-Key")
	if err != nil {
		return plugin.Plugin{}, err
	}
	query, err := ctx.Settings.String("query", "")
	if err != nil {
		return plugin.Plugin{}, err
	}
	return plugin.Plugin{Handler: func(c *fiber.Ctx) error {
		candidate := c.Get(header)
		if candidate == "" && query != "" {
			candidate = c.Query(query)
		}
		if !matchesAny(candidate, keys) {
			return plugin.WriteError(c, sferrors.New(sferrors.CodeUnauthenticated, "invalid api key"))
		}
		return c.Next()
	}}, nil
}

func matchesAny(candidate string, keys []string) bool {
	if candidate == "" {
		return false
	}
	for _, key := range keys {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(key)) == 1 {
			return true
		}
	}
	return false
}
