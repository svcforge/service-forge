// Bench gateway: svcforge REST/JSON -> gRPC gateway wired exactly like a
// generated project (static codec proxy invoker, configured route).
//
// Usage:
//
//	go run ./examples/bench/svcforgegw [-port 8080] [-target 127.0.0.1:9100] [-plugins core]
//
// -plugins core enables recovery, access_log, rate_limit, api_key and
// metrics; requests must then send X-API-Key: bench-key.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/transport/gateway"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

var healthServingJSON = []byte(`{"status":"SERVING"}`)

func main() {
	port := flag.Int("port", 8080, "gateway port")
	target := flag.String("target", "127.0.0.1:9100", "grpc backend target")
	plugins := flag.String("plugins", "", `plugin profile: "" or "core"`)
	flag.Parse()

	registerHealthInvoker()

	cfg := config.Default()
	cfg.App.Name = "bench"
	cfg.Gateway.Port = *port
	cfg.Gateway.DisableStartupMessage = true
	cfg.Gateway.Routes = []config.GatewayRouteConfig{{
		Name:   "health",
		Method: "GET",
		Path:   "/api/health",
		Target: *target,
		RPC:    "/grpc.health.v1.Health/Check",
	}}
	if *plugins == "core" {
		cfg.Gateway.Plugins = corePlugins()
	}

	application := app.New(cfg, app.WithModules(gateway.New()))
	if err := application.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func corePlugins() []config.GatewayPluginConfig {
	return []config.GatewayPluginConfig{
		{Name: "recovery"},
		{Name: "access_log", Config: map[string]any{"skip_paths": []any{}}},
		{Name: "rate_limit", Config: map[string]any{"max": 1 << 30, "window": "1m"}},
		{Name: "api_key", Config: map[string]any{"keys": []any{"bench-key"}}},
		{Name: "metrics"},
	}
}

func registerHealthInvoker() {
	gateway.MustRegisterProxyInvoker("/grpc.health.v1.Health/Check", gateway.NewUnaryCodecProxy(
		func() *healthpb.HealthCheckRequest { return &healthpb.HealthCheckRequest{} },
		func() *healthpb.HealthCheckResponse { return &healthpb.HealthCheckResponse{} },
		nil,
		func(ctx context.Context, conn *grpc.ClientConn, req *healthpb.HealthCheckRequest, resp *healthpb.HealthCheckResponse) error {
			return conn.Invoke(ctx, "/grpc.health.v1.Health/Check", req, resp)
		},
		func(c *fiber.Ctx, resp *healthpb.HealthCheckResponse) error {
			if resp.Status == healthpb.HealthCheckResponse_SERVING {
				return gateway.WriteSuccessJSON(c, healthServingJSON)
			}
			return gateway.WriteSuccessJSON(c, []byte(`{"status":"`+resp.Status.String()+`"}`))
		},
	))
}
