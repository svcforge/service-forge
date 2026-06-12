package main

import (
	"context"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/adapters"
	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/transport/gateway"
)

func main() {
	cfg := config.Default()
	cfg.App.Name = "basic-service"
	cfg.Gateway.Port = 8080
	cfg.Runtime.Components = []config.ComponentConfig{
		{Name: "cache", Provider: "memory"},
		{Name: "eventbus", Provider: "memory"},
		{Name: "registry", Provider: "memory"},
		{Name: "tracing", Provider: "noop"},
	}

	gw := gateway.New(func(router *fiber.App, gw *gateway.Gateway) {
		router.Get("/api/v1/ping", gw.Handle(func(ctx context.Context, c *fiber.Ctx) (any, error) {
			return fiber.Map{"message": "pong"}, nil
		}))
	})

	mods, err := adapters.DefaultCatalog().Build(cfg.Runtime.Components)
	if err != nil {
		log.Fatal(err)
	}
	mods = append(mods, gw)
	application := app.New(cfg, app.WithModules(mods...))
	if err := application.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
