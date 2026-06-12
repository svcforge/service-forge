// Package metrics records Prometheus request metrics and exposes them on a
// scrape endpoint mounted on the gateway app.
//
// Settings:
//
//	path:      scrape endpoint path (default "/metrics")
//	namespace: metric namespace prefix (default "gateway")
package metrics

import (
	"strconv"
	"time"

	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

const Name = "metrics"

func Factory(ctx plugin.BuildContext) (plugin.Plugin, error) {
	path, err := ctx.Settings.String("path", "/metrics")
	if err != nil {
		return plugin.Plugin{}, err
	}
	namespace, err := ctx.Settings.String("namespace", "gateway")
	if err != nil {
		return plugin.Plugin{}, err
	}

	registry := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "http_requests_total",
		Help:      "Total HTTP requests handled by the gateway.",
	}, []string{"method", "path", "status"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request latency in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "path"})
	if err := registry.Register(requests); err != nil {
		return plugin.Plugin{}, err
	}
	if err := registry.Register(duration); err != nil {
		return plugin.Plugin{}, err
	}

	return plugin.Plugin{
		Handler: func(c *fiber.Ctx) error {
			start := time.Now()
			err := c.Next()
			// Route pattern (e.g. /api/v1/users/:id) keeps label cardinality
			// bounded; raw paths would explode the metric series.
			routePath := c.Route().Path
			method := c.Method()
			requests.WithLabelValues(method, routePath, strconv.Itoa(c.Response().StatusCode())).Inc()
			duration.WithLabelValues(method, routePath).Observe(time.Since(start).Seconds())
			return err
		},
		Mount: func(app *fiber.App) error {
			app.Get(path, adaptor.HTTPHandler(promhttp.HandlerFor(registry, promhttp.HandlerOpts{})))
			return nil
		},
	}, nil
}
