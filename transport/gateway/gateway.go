package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/core/module"
	"github.com/svcforge/service-forge/ports/registry"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
	"github.com/svcforge/service-forge/transport/gateway/plugins"
	"github.com/svcforge/service-forge/transport/grpcclient"
	"google.golang.org/grpc"
)

func init() {
	plugins.RegisterBuiltins(plugin.Default())
}

type HandlerFunc func(ctx context.Context, c *fiber.Ctx) (any, error)
type RouteFunc func(app *fiber.App, gateway *Gateway)

type Gateway struct {
	app              *fiber.App
	logger           module.Logger
	routes           []RouteFunc
	configuredRoutes []*proxyRoute
	address          string
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
	// Routes registered before the global plugin chain (health, plugin
	// endpoints such as /metrics) are exempt from it.
	g.app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(sferrors.Success(map[string]string{"status": "ok"}))
	})
	if err := g.mountGlobalPlugins(cfg); err != nil {
		return err
	}
	if err := g.mountConfiguredRoutes(ctx, cfg, runtime); err != nil {
		return err
	}
	for _, route := range g.routes {
		route(g.app, g)
	}
	runtime.Set("gateway", g)
	runtime.Set("fiber", g.app)
	return nil
}

func (g *Gateway) mountConfiguredRoutes(ctx context.Context, cfg *config.Config, runtime module.Runtime) error {
	if len(cfg.Gateway.Routes) == 0 {
		return nil
	}
	var resolver registry.Resolver
	if raw, ok := runtime.Get("resolver"); ok {
		if candidate, ok := raw.(registry.Resolver); ok {
			resolver = candidate
		}
	}
	proxy := newGRPCProxy(grpcclient.NewDialer(resolver))
	for _, routeCfg := range cfg.Gateway.Routes {
		route, err := newProxyRoute(routeCfg)
		if err != nil {
			return err
		}
		invoker, ok := lookupProxyInvoker(route.fullRPC)
		if !ok {
			return sferrors.New(sferrors.CodeFailedPrecondition, "grpc proxy invoker is not registered").
				WithDetails("rpc", route.fullRPC)
		}
		route.invoker = invoker
		if strings.TrimSpace(route.cfg.Target) != "" {
			poolSize := route.cfg.PoolSize
			if poolSize <= 0 {
				poolSize = 1
			}
			route.conns = make([]*grpc.ClientConn, 0, poolSize)
			for i := 0; i < poolSize; i++ {
				conn, err := proxy.dialer.DialTargetFresh(ctx, route.cfg.Target)
				if err != nil {
					return sferrors.New(sferrors.CodeUnavailable, "grpc target unavailable").WithCause(err)
				}
				route.conns = append(route.conns, conn)
			}
		}
		g.configuredRoutes = append(g.configuredRoutes, route)
		routeHandlers, err := g.buildRoutePlugins(cfg, routeCfg)
		if err != nil {
			return err
		}
		method := strings.ToUpper(route.cfg.Method)
		handlers := append(routeHandlers, g.Handle(func(ctx context.Context, c *fiber.Ctx) (any, error) {
			return proxy.Invoke(ctx, c, route)
		}))
		g.app.Add(method, route.cfg.Path, handlers...)
		if g.logger != nil {
			g.logger.Info("mounted grpc proxy route", "method", method, "path", route.cfg.Path, "rpc", route.fullRPC)
		}
	}
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
	err := g.app.ShutdownWithContext(ctx)
	for _, route := range g.configuredRoutes {
		for _, conn := range route.conns {
			if closeErr := conn.Close(); err == nil && closeErr != nil {
				err = closeErr
			}
		}
	}
	return err
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
		if _, ok := data.(handledResponse); ok {
			return nil
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
