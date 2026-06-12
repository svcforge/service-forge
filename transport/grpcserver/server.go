package grpcserver

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/core/module"
	"github.com/svcforge/service-forge/ports/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type RegisterFunc func(*grpc.Server)
type Option func(*Module)

type Module struct {
	serviceName string
	address     string
	port        int
	server      *grpc.Server
	health      *health.Server
	listener    net.Listener
	registers   []RegisterFunc
	registrar   registry.Registrar
	serviceID   string
	logger      module.Logger
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
	app.Set("grpc_server", m.server)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", config.Address(m.address, m.port))
	if err != nil {
		return err
	}
	m.listener = listener
	m.health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	if m.registrar != nil {
		m.serviceID = fmt.Sprintf("%s-%s", m.serviceName, uuid.NewString())
		registerAddr := hostForRegistry(m.address)
		if err := m.registrar.Register(ctx, registry.ServiceInstance{
			ID:       m.serviceID,
			Name:     m.serviceName,
			Address:  registerAddr,
			Port:     m.port,
			Protocol: "grpc",
			Tags:     []string{"grpc"},
			Metadata: map[string]string{"transport": "grpc"},
		}); err != nil {
			return err
		}
	}
	if m.logger != nil {
		m.logger.Info("grpc server listening", "address", config.Address(m.address, m.port))
	}
	go func() {
		_ = m.server.Serve(listener)
	}()
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
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
	if m.listener == nil {
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
