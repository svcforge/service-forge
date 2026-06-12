package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/core/module"
)

type HandlerFunc func(ctx context.Context, c *fiber.Ctx) (any, error)
type RouteFunc func(app *fiber.App, gateway *Gateway)

type Gateway struct {
	app     *fiber.App
	logger  module.Logger
	routes  []RouteFunc
	address string
}

func New(routes ...RouteFunc) *Gateway {
	return &Gateway{routes: routes}
}

func (g *Gateway) Name() string { return "transport.gateway" }

func (g *Gateway) Init(ctx context.Context, runtime module.Runtime) error {
	cfg := runtime.Config()
	g.logger = runtime.Logger().With("source", "transport", "component", "gateway")
	g.address = config.Address(cfg.Gateway.ListenIP, cfg.Gateway.Port)
	g.app = fiber.New(fiber.Config{
		AppName:               cfg.App.Name + "-gateway",
		DisableStartupMessage: cfg.Gateway.DisableStartupMessage,
	})
	g.app.Use(requestid.New())
	g.app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(sferrors.Success(map[string]string{"status": "ok"}))
	})
	for _, route := range g.routes {
		route(g.app, g)
	}
	runtime.Set("gateway", g)
	runtime.Set("fiber", g.app)
	return nil
}

func (g *Gateway) Start(ctx context.Context) error {
	if g.logger != nil {
		g.logger.Info("gateway listening", "address", g.address)
	}
	go func() {
		_ = g.app.Listen(g.address)
	}()
	return nil
}

func (g *Gateway) Stop(ctx context.Context) error {
	return g.app.ShutdownWithContext(ctx)
}

func (g *Gateway) Health(ctx context.Context) error {
	if g.app == nil {
		return fmt.Errorf("gateway is not initialized")
	}
	return nil
}

func (g *Gateway) Handle(handler HandlerFunc) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx := c.UserContext()
		if deadline, ok := c.Locals("deadline").(time.Duration); ok && deadline > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, deadline)
			defer cancel()
		}
		data, err := handler(ctx, c)
		if err != nil {
			appErr := sferrors.FromGRPCError(err)
			if _, ok := err.(*sferrors.AppError); ok {
				appErr = err.(*sferrors.AppError)
			}
			return c.Status(appErr.HTTPStatus).JSON(sferrors.Failure(appErr))
		}
		return c.Status(fiber.StatusOK).JSON(sferrors.Success(data))
	}
}

func BindBody[T any](c *fiber.Ctx) (*T, error) {
	var req T
	if err := c.BodyParser(&req); err != nil {
		return nil, sferrors.New(sferrors.CodeInvalidArgument, "invalid request body").WithCause(err)
	}
	return &req, nil
}

func BindQuery[T any](c *fiber.Ctx) (*T, error) {
	var req T
	if err := c.QueryParser(&req); err != nil {
		return nil, sferrors.New(sferrors.CodeInvalidArgument, "invalid query").WithCause(err)
	}
	return &req, nil
}
