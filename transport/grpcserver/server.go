package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/core/module"
	"github.com/svcforge/service-forge/ports/registry"
	"github.com/svcforge/service-forge/transport/routeconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/reflect/protoregistry"
)

type RegisterFunc func(*grpc.Server)
type Option func(*Module)

type Module struct {
	serviceName   string
	address       string
	registerIP    string
	routeCatalog  registry.RouteCatalog
	routeManifest []byte
	port          int
	server        *grpc.Server
	health        *health.Server
	serving       atomic.Bool
	registers     []RegisterFunc
	registrar     registry.Registrar
	serviceID     string
	logger        module.Logger
}

func NewModule(registers ...RegisterFunc) *Module {
	return &Module{registers: registers}
}

func WithUnaryInterceptors(interceptors ...grpc.UnaryServerInterceptor) Option {
	return func(m *Module) {
		m.server = grpc.NewServer(grpc.ChainUnaryInterceptor(interceptors...))
	}
}

func (m *Module) Name() string { return "transport.grpcserver" }

func (m *Module) Init(ctx context.Context, app module.Runtime) error {
	cfg := app.Config()
	m.logger = app.Logger().With("source", "transport", "component", "grpcserver")
	m.serviceName = cfg.App.Name
	m.address = cfg.GRPC.ListenIP
	m.registerIP = cfg.GRPC.RegisterIP
	m.port = cfg.GRPC.Port
	if m.address == "" {
		m.address = "0.0.0.0"
	}
	if m.port == 0 {
		m.port = 9000
	}
	if m.server == nil {
		m.server = grpc.NewServer(grpc.ChainUnaryInterceptor(ErrorInterceptor()))
	}
	m.health = health.NewServer()
	healthpb.RegisterHealthServer(m.server, m.health)
	for _, register := range m.registers {
		register(m.server)
	}
	if raw, ok := app.Get("registrar"); ok {
		if registrar, ok := raw.(registry.Registrar); ok {
			m.registrar = registrar
		}
	}
	raw, _ := app.Get(registry.RouteCatalogKey)
	catalog, canPublish := raw.(registry.RouteCatalog)
	_, canDiscover := raw.(registry.RouteCatalogLister)
	if cfg.GRPC.PublishRoutes && (!canPublish || m.registrar == nil) {
		return fmt.Errorf("publishing routes requires a route catalog and service registrar")
	}
	if canPublish && m.registrar != nil && (cfg.GRPC.PublishRoutes || canDiscover) {
		serviceInfo := m.server.GetServiceInfo()
		names := make([]string, 0, len(serviceInfo))
		for name := range serviceInfo {
			names = append(names, name)
		}
		var manifest []byte
		var err error
		if cfg.GRPC.PublishRoutes {
			manifest, err = routeconfig.EncodeWithPolicy(names, protoregistry.GlobalFiles, cfg.GRPC.RoutePolicy)
		} else {
			manifest, err = routeconfig.EncodeAnnotated(names, protoregistry.GlobalFiles)
		}
		if err != nil {
			return fmt.Errorf("encode routes: %w", err)
		}
		if len(manifest) != 0 {
			m.routeCatalog, m.routeManifest = catalog, manifest
		}
	}
	app.Set("grpc_server", m.server)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", config.Address(m.address, m.port))
	if err != nil {
		return err
	}
	if m.registrar != nil {
		serviceID := fmt.Sprintf("%s-%s", m.serviceName, uuid.NewString())
		registerAddr := m.registerIP
		if registerAddr == "" {
			registerAddr = hostForRegistry(m.address)
		}
		metadata := map[string]string{"transport": "grpc"}
		if m.routeCatalog != nil {
			metadata[registry.RouteRevisionMetadata] = routeconfig.Revision(m.routeManifest)
		}
		if err := m.registrar.Register(ctx, registry.ServiceInstance{
			ID:       serviceID,
			Name:     m.serviceName,
			Address:  registerAddr,
			Port:     m.port,
			Protocol: "grpc",
			Tags:     []string{"grpc"},
			Metadata: metadata,
		}); err != nil {
			return errors.Join(err, listener.Close())
		}
		if m.routeCatalog != nil {
			if err := m.routeCatalog.PublishRoutes(ctx, m.serviceName, m.routeManifest); err != nil {
				return errors.Join(fmt.Errorf("publish routes: %w", err), m.registrar.Deregister(ctx, serviceID), listener.Close())
			}
		}
		m.serviceID = serviceID
	}
	m.serving.Store(true)
	m.health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	if m.logger != nil {
		m.logger.Info("grpc server listening", "address", config.Address(m.address, m.port))
	}
	go func() {
		defer m.serving.Store(false)
		if err := m.server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) && m.logger != nil {
			m.logger.Error("grpc server stopped serving", "error", err)
		}
	}()
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	m.serving.Store(false)
	if m.health != nil {
		m.health.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	}
	if m.registrar != nil && m.serviceID != "" {
		_ = m.registrar.Deregister(ctx, m.serviceID)
	}
	stopped := make(chan struct{})
	go func() {
		if m.server != nil {
			m.server.GracefulStop()
		}
		close(stopped)
	}()
	select {
	case <-ctx.Done():
		if m.server != nil {
			m.server.Stop()
		}
	case <-time.After(10 * time.Second):
		if m.server != nil {
			m.server.Stop()
		}
	case <-stopped:
	}
	return nil
}

func (m *Module) Health(ctx context.Context) error {
	if !m.serving.Load() {
		return fmt.Errorf("grpc server is not listening")
	}
	return nil
}

func ErrorInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			return nil, sferrors.ToGRPCError(err)
		}
		return resp, nil
	}
}

func hostForRegistry(host string) string {
	if host == "" || host == "0.0.0.0" || host == "::" {
		return "127.0.0.1"
	}
	return host
}
