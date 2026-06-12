// Package cors enables cross-origin resource sharing on the gateway.
//
// Settings:
//
//	allow_origins:     list of allowed origins (default ["*"])
//	allow_methods:     list of allowed methods (default fiber defaults)
//	allow_headers:     list of allowed request headers
//	expose_headers:    list of response headers exposed to the browser
//	allow_credentials: bool (default false; not allowed with origin "*")
//	max_age:           preflight cache seconds (default 0)
package cors

import (
	"fmt"
	"strings"

	fibercors "github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

const Name = "cors"

func Factory(ctx plugin.BuildContext) (plugin.Plugin, error) {
	cfg, err := buildConfig(ctx.Settings)
	if err != nil {
		return plugin.Plugin{}, err
	}
	return plugin.Plugin{Handler: fibercors.New(cfg)}, nil
}

func buildConfig(settings plugin.Settings) (fibercors.Config, error) {
	cfg := fibercors.ConfigDefault
	origins, err := settings.Strings("allow_origins")
	if err != nil {
		return cfg, err
	}
	if len(origins) > 0 {
		cfg.AllowOrigins = strings.Join(origins, ",")
	}
	methods, err := settings.Strings("allow_methods")
	if err != nil {
		return cfg, err
	}
	if len(methods) > 0 {
		cfg.AllowMethods = strings.Join(methods, ",")
	}
	headers, err := settings.Strings("allow_headers")
	if err != nil {
		return cfg, err
	}
	if len(headers) > 0 {
		cfg.AllowHeaders = strings.Join(headers, ",")
	}
	exposeHeaders, err := settings.Strings("expose_headers")
	if err != nil {
		return cfg, err
	}
	if len(exposeHeaders) > 0 {
		cfg.ExposeHeaders = strings.Join(exposeHeaders, ",")
	}
	credentials, err := settings.Bool("allow_credentials", false)
	if err != nil {
		return cfg, err
	}
	if credentials && cfg.AllowOrigins == "*" {
		return cfg, fmt.Errorf("allow_credentials requires explicit allow_origins, not %q", "*")
	}
	cfg.AllowCredentials = credentials
	maxAge, err := settings.Int("max_age", 0)
	if err != nil {
		return cfg, err
	}
	cfg.MaxAge = maxAge
	return cfg, nil
}
