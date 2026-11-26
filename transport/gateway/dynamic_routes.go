package gateway

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/grpc-ecosystem/grpc-gateway/v2/utilities"
	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/core/module"
	"github.com/svcforge/service-forge/ports/registry"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
	"github.com/svcforge/service-forge/transport/grpcclient"
	"github.com/svcforge/service-forge/transport/routeconfig"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

var dynamicLiteral = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
var dynamicParameter = regexp.MustCompile(`^\{[A-Za-z_][A-Za-z0-9_]*\}$`)

type dynamicRoute struct {
	routeconfig.Route
	parts     []string
	public    bool
	auth      fiber.Handler
	claims    *claimForwarder
	automatic bool
}

type dynamicService struct {
	revision string
	routes   []dynamicRoute
	targets  []string
	next     atomic.Uint64
}

type dynamicMatch struct {
	route   dynamicRoute
	service *dynamicService
}

type dynamicSnapshot struct {
	services map[string]*dynamicService
	matches  []dynamicMatch
}

type dynamicSelection struct {
	dynamicMatch
	parts  []string
	public bool
}

type dynamicSelectionKey struct{}

type dynamicRoutes struct {
	cfg           config.DynamicRoutesConfig
	catalog       registry.RouteCatalog
	lister        registry.RouteCatalogLister
	pluginContext plugin.BuildContext
	resolver      registry.Resolver
	dialer        *grpcclient.Dialer
	table         atomic.Pointer[dynamicSnapshot]
	cancel        context.CancelFunc
	done          chan struct{}
	claims        *claimForwarder
	public        map[string]bool
	logger        module.Logger
	globalJWT     bool
}

func (g *Gateway) mountDynamicRoutes(runtime module.Runtime, cfg config.DynamicRoutesConfig) error {
	if cfg.Disabled {
		return nil
	}
	raw, _ := runtime.Get(registry.RouteCatalogKey)
	catalog, ok := raw.(registry.RouteCatalog)
	lister, discovery := raw.(registry.RouteCatalogLister)
	if len(cfg.Services) == 0 && !discovery {
		return nil
	}
	if !ok {
		return fmt.Errorf("dynamic routes require a route catalog")
	}
	for service, prefix := range cfg.Services {
		if service == "" || !validDynamicPrefix(prefix) {
			return fmt.Errorf("invalid dynamic route service %q or prefix %q", service, prefix)
		}
	}
	if len(cfg.Services) != 0 {
		lister = nil
	}
	raw, _ = runtime.Get("resolver")
	resolver, ok := raw.(registry.Resolver)
	if !ok {
		return fmt.Errorf("dynamic routes require a service resolver")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	built, err := plugin.Default().Build(cfg.Plugins, g.pluginBuildContext(runtime.Config()))
	if err != nil {
		return err
	}
	for _, item := range built {
		if item.Mount != nil {
			return fmt.Errorf("dynamic plugin %s cannot mount endpoints; configure it globally", item.Name)
		}
	}
	dynamic := &dynamicRoutes{cfg: cfg, catalog: catalog, lister: lister, pluginContext: g.pluginBuildContext(runtime.Config()), resolver: resolver, dialer: grpcclient.NewDialer(nil),
		claims: buildClaimForwarder(cfg.ForwardClaims), public: make(map[string]bool), logger: g.logger, globalJWT: g.globalJWT}
	for _, rpc := range cfg.PublicRPCs {
		normalized, _, err := normalizeRPC(rpc)
		if err != nil {
			return err
		}
		dynamic.public[normalized] = true
	}
	dynamic.table.Store(&dynamicSnapshot{services: map[string]*dynamicService{}})
	g.dynamic = dynamic
	// This fallback runs after static routes and the gateway's global plugins.
	g.app.Use(func(c *fiber.Ctx) error {
		match, ok := dynamic.requestRoute(c)
		if !ok {
			return c.Next()
		}
		c.Locals(dynamicSelectionKey{}, match)
		return c.Next()
	})
	for _, item := range built {
		if item.Handler != nil {
			g.app.Use(func(c *fiber.Ctx) error {
				if selection, ok := c.Locals(dynamicSelectionKey{}).(dynamicSelection); ok && !selection.public {
					return item.Handler(c)
				}
				return c.Next()
			})
		}
	}
	g.app.Use(func(c *fiber.Ctx) error {
		if selection, ok := c.Locals(dynamicSelectionKey{}).(dynamicSelection); ok && selection.route.auth != nil {
			return selection.route.auth(c)
		}
		return c.Next()
	})
	invoke := g.Handle(dynamic.invoke)
	g.app.Use(func(c *fiber.Ctx) error {
		if c.Locals(dynamicSelectionKey{}) == nil {
			return c.Next()
		}
		return invoke(c)
	})
	return nil
}

func (d *dynamicRoutes) start(report func(error)) {
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.done = make(chan struct{})
	go func() {
		defer close(d.done)
		ticker := time.NewTicker(d.cfg.PollInterval)
		defer ticker.Stop()
		for {
			pollContext, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
			err := d.refresh(pollContext)
			cancel()
			if err != nil && ctx.Err() == nil {
				report(err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (d *dynamicRoutes) stop(ctx context.Context) error {
	if d.cancel != nil {
		d.cancel()
		select {
		case <-d.done:
		case <-ctx.Done():
			// The watcher only reads the catalog and swaps snapshots.
		}
	}
	return d.dialer.Close()
}

func (d *dynamicRoutes) refresh(ctx context.Context) error {
	previous := d.table.Load()
	snapshot := &dynamicSnapshot{services: make(map[string]*dynamicService)}
	services := d.cfg.Services
	if d.lister != nil {
		names, err := d.lister.ListRouteServices(ctx)
		if err != nil {
			return fmt.Errorf("discover route services: %w", err)
		}
		services = make(map[string]string, len(names))
		for _, service := range names {
			services[service] = ""
		}
	}
	for service, prefix := range services {
		data, err := d.catalog.LoadRoutes(ctx, service)
		if err != nil {
			return fmt.Errorf("load routes for %s: %w", service, err)
		}
		if len(data) == 0 {
			continue
		}
		current := previous.services[service]
		revision := routeconfig.Revision(data)
		instances, err := d.resolver.Resolve(ctx, service)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", service, err)
		}
		targets := dynamicTargets(instances, revision)
		if current != nil && current.revision != revision && len(targets) == 0 {
			revision = current.revision
			targets = dynamicTargets(instances, revision)
		}
		if d.lister != nil && len(targets) == 0 {
			continue
		}
		var routes []dynamicRoute
		if current != nil && revision == current.revision {
			routes = current.routes
		} else {
			routes, err = d.compileRoutes(data, prefix)
			if err != nil {
				return fmt.Errorf("compile routes for %s: %w", service, err)
			}
		}
		if current != nil && current.revision == revision && slices.Equal(current.targets, targets) {
			snapshot.services[service] = current
		} else {
			snapshot.services[service] = &dynamicService{revision: revision, routes: routes, targets: targets}
		}
	}
	for _, service := range snapshot.services {
		for _, route := range service.routes {
			for _, existing := range snapshot.matches {
				if routesOverlap(existing.route, route) &&
					(existing.route.literalCount() == route.literalCount() || (d.lister != nil && existing.service != service)) {
					return fmt.Errorf("conflicting dynamic routes %s %s and %s", route.Method, route.Path, existing.route.Path)
				}
			}
			snapshot.matches = append(snapshot.matches, dynamicMatch{route: route, service: service})
		}
	}
	slices.SortFunc(snapshot.matches, func(a, b dynamicMatch) int {
		return b.route.literalCount() - a.route.literalCount()
	})
	d.table.Store(snapshot)
	return nil
}

func dynamicTargets(instances []registry.ServiceInstance, revision string) []string {
	var targets []string
	for _, instance := range instances {
		if instance.Metadata[registry.RouteRevisionMetadata] == revision {
			targets = append(targets, net.JoinHostPort(instance.Address, strconv.Itoa(instance.Port)))
		}
	}
	slices.Sort(targets)
	return slices.Compact(targets)
}

func validDynamicPrefix(prefix string) bool {
	if !strings.HasPrefix(prefix, "/") || strings.HasSuffix(prefix, "/") {
		return false
	}
	for _, part := range strings.Split(prefix[1:], "/") {
		if !dynamicLiteral.MatchString(part) || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (d *dynamicRoutes) compileRoutes(data []byte, prefix string) ([]dynamicRoute, error) {
	spec, err := routeconfig.DecodeManifest(data)
	if err != nil {
		return nil, err
	}
	automatic := d.lister != nil && spec.Automatic
	var public map[string]bool
	var auth fiber.Handler
	var claims *claimForwarder
	var secretEnv string
	if d.lister != nil {
		switch {
		case automatic:
			// Authentication comes from the gateway global chain.
		case spec.Policy != nil:
			prefix = spec.Policy.Prefix
			if !validDynamicPrefix(prefix) {
				return nil, fmt.Errorf("invalid published route prefix %q", prefix)
			}
			public = make(map[string]bool, len(spec.Policy.PublicRPCs))
			for _, rpc := range spec.Policy.PublicRPCs {
				name, _, err := normalizeRPC(rpc)
				if err != nil {
					return nil, err
				}
				public[name] = true
			}
			secretEnv = spec.Policy.JWTSecretEnv
			claims = buildClaimForwarder(spec.Policy.ForwardClaims)
		default:
			return nil, nil
		}
	}
	isPublic := func(route routeconfig.Route) bool {
		return public[route.RPC] || (automatic && route.AllowAnonymous)
	}
	accountClaims := buildClaimForwarder([]config.ClaimForwardConfig{{
		Claim: "account_id", Header: "x-account-id", Required: true,
	}})
	var routes []dynamicRoute
	for _, route := range spec.Routes {
		if prefix != "" && route.Path != prefix && !strings.HasPrefix(route.Path, prefix+"/") {
			return nil, fmt.Errorf("%s is outside allowed prefix %s", route.Path, prefix)
		}
		parts := strings.Split(route.Path[1:], "/")
		for _, part := range parts {
			if dynamicParameter.MatchString(part) {
				field := route.Input.Fields().ByName(protoreflect.Name(part[1 : len(part)-1]))
				if field == nil || field.IsList() || field.IsMap() || field.Message() != nil {
					return nil, fmt.Errorf("%s: %q must bind a scalar input field", route.Path, part)
				}
			} else if !dynamicLiteral.MatchString(part) || part == "." || part == ".." {
				return nil, fmt.Errorf("unsupported path segment %q in %s", part, route.Path)
			}
		}
		entry := dynamicRoute{Route: route, parts: parts, public: isPublic(route), automatic: automatic}
		if automatic {
			if !entry.public && route.ForwardAccountID && d.globalJWT {
				entry.claims = accountClaims
			}
		} else if d.lister != nil && !entry.public {
			if auth == nil {
				if secretEnv == "" {
					return nil, fmt.Errorf("protected route %s requires jwt_secret_env", route.RPC)
				}
				secret := os.Getenv(secretEnv)
				if secret == "" {
					return nil, fmt.Errorf("required gateway JWT environment variable %s is unset", secretEnv)
				}
				built, err := plugin.Default().Build([]config.GatewayPluginConfig{{
					Name: "jwt", Config: map[string]any{"secret": secret},
				}}, d.pluginContext)
				if err != nil {
					return nil, err
				}
				auth = built[0].Handler
			}
			entry.auth, entry.claims = auth, claims
		}
		routes = append(routes, entry)
	}
	return routes, nil
}

func routesOverlap(a, b dynamicRoute) bool {
	if a.Method != b.Method || len(a.parts) != len(b.parts) {
		return false
	}
	for i, part := range a.parts {
		other := b.parts[i]
		if part != other && part[0] != '{' && other[0] != '{' {
			return false
		}
	}
	return true
}

func (r dynamicRoute) literalCount() int {
	count := 0
	for _, part := range r.parts {
		if part[0] != '{' {
			count++
		}
	}
	return count
}

func (d *dynamicRoutes) selectRoute(c *fiber.Ctx) (dynamicSelection, bool) {
	method := c.Method()
	parts := strings.Split(strings.TrimPrefix(c.Path(), "/"), "/")
	for _, match := range d.table.Load().matches {
		if match.route.Method == method && match.route.matches(parts) {
			return dynamicSelection{dynamicMatch: match, parts: parts, public: match.route.public || d.public[match.route.RPC]}, true
		}
	}
	return dynamicSelection{}, false
}

func (d *dynamicRoutes) invoke(ctx context.Context, c *fiber.Ctx) (any, error) {
	selection := c.Locals(dynamicSelectionKey{}).(dynamicSelection)
	if selection.public {
		return d.call(ctx, c, selection, nil)
	}
	if d.lister != nil {
		return d.call(ctx, c, selection, selection.route.claims)
	}
	return d.call(ctx, c, selection, d.claims)
}

func (r dynamicRoute) matches(parts []string) bool {
	if len(parts) != len(r.parts) {
		return false
	}
	for i, part := range r.parts {
		if part[0] == '{' {
			if parts[i] == "" {
				return false
			}
		} else if part != parts[i] {
			return false
		}
	}
	return true
}

func (d *dynamicRoutes) call(ctx context.Context, c *fiber.Ctx, selection dynamicSelection, claims *claimForwarder) (any, error) {
	service, route := selection.service, selection.route
	if len(service.targets) == 0 {
		return nil, sferrors.New(sferrors.CodeUnavailable, "no healthy instance for route revision")
	}
	index := (service.next.Add(1) - 1) % uint64(len(service.targets))
	target := service.targets[index]
	if route.Stream {
		return d.upgrade(c, route, target, claims)
	}
	var err error
	ctx, err = claims.outgoing(ctx, fiberLocals(c))
	if err != nil {
		return nil, err
	}
	req := dynamicpb.NewMessage(route.Input)
	if err := route.bind(c, req, selection.parts); err != nil {
		return nil, sferrors.New(sferrors.CodeInvalidArgument, "invalid request payload").WithCause(err)
	}
	ctx, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
	defer cancel()
	conn, err := d.dialer.DialTarget(ctx, target)
	if err != nil {
		return nil, sferrors.New(sferrors.CodeUnavailable, "grpc target unavailable").WithCause(err)
	}
	resp := dynamicpb.NewMessage(route.Output)
	if err := conn.Invoke(ctx, route.RPC, req, resp); err != nil {
		return nil, err
	}
	data, err := (protojson.MarshalOptions{
		EmitUnpopulated: true, UseProtoNames: true, Resolver: route.Types,
	}).Marshal(resp)
	if err != nil {
		return nil, err
	}
	if err := WriteSuccessJSON(c, data); err != nil {
		return nil, err
	}
	return handledResponse{}, nil
}

func (d *dynamicRoutes) upgrade(c *fiber.Ctx, route dynamicRoute, target string, claims *claimForwarder) (any, error) {
	if !websocket.IsWebSocketUpgrade(c) {
		return handledResponse{}, c.SendStatus(fiber.StatusUpgradeRequired)
	}
	proxy := newGRPCProxy(d.dialer)
	stream := &proxyRoute{
		cfg: config.GatewayRouteConfig{Target: target}, fullRPC: route.RPC, claims: claims,
		streamCodec: &streamCodec{
			kind:    streamKindBidi,
			newRecv: func() proto.Message { return dynamicpb.NewMessage(route.Output) },
			newSend: func() proto.Message { return dynamicpb.NewMessage(route.Input) },
		},
	}
	upgrade := websocket.New(func(ws *websocket.Conn) {
		if err := proxy.InvokeStream(context.Background(), ws, stream); err != nil && d.logger != nil {
			d.logger.Warn("dynamic stream ended with error", "rpc", route.RPC, "error", err)
		}
	})
	return handledResponse{}, upgrade(c)
}

func (r dynamicRoute) bind(c *fiber.Ctx, req *dynamicpb.Message, parts []string) error {
	if r.Body == "*" {
		if body := c.Body(); len(body) > 0 {
			if err := (protojson.UnmarshalOptions{Resolver: r.Types}).Unmarshal(body, req); err != nil {
				return err
			}
		}
	} else {
		values := url.Values{}
		c.Context().QueryArgs().VisitAll(func(key, value []byte) {
			values.Add(string(key), string(value))
		})
		if err := gwruntime.PopulateQueryParameters(req, values, &utilities.DoubleArray{}); err != nil {
			return err
		}
	}
	for i, part := range r.parts {
		if part[0] != '{' {
			continue
		}
		value, err := url.PathUnescape(parts[i])
		if err != nil {
			return err
		}
		if err := gwruntime.PopulateFieldFromPath(req, part[1:len(part)-1], value); err != nil {
			return err
		}
	}
	return nil
}
