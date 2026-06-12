// Package plugins wires the built-in gateway plugins into a plugin registry.
// All built-ins are inert until explicitly listed under gateway.plugins in
// config.
package plugins

import (
	"github.com/svcforge/service-forge/transport/gateway/plugin"
	"github.com/svcforge/service-forge/transport/gateway/plugins/accesslog"
	"github.com/svcforge/service-forge/transport/gateway/plugins/apikey"
	"github.com/svcforge/service-forge/transport/gateway/plugins/cors"
	"github.com/svcforge/service-forge/transport/gateway/plugins/jwtauth"
	"github.com/svcforge/service-forge/transport/gateway/plugins/metrics"
	"github.com/svcforge/service-forge/transport/gateway/plugins/ratelimit"
	"github.com/svcforge/service-forge/transport/gateway/plugins/recovery"
)

// RegisterBuiltins registers every built-in plugin on the given registry.
// The gateway module calls this once for the default registry.
func RegisterBuiltins(registry *plugin.Registry) {
	registry.MustRegister(recovery.Name, recovery.Factory)
	registry.MustRegister(accesslog.Name, accesslog.Factory)
	registry.MustRegister(cors.Name, cors.Factory)
	registry.MustRegister(ratelimit.Name, ratelimit.Factory)
	registry.MustRegister(apikey.Name, apikey.Factory)
	registry.MustRegister(jwtauth.Name, jwtauth.Factory)
	registry.MustRegister(metrics.Name, metrics.Factory)
}
