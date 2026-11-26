package gateway

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/svcforge/service-forge/core/config"
	"google.golang.org/grpc/connectivity"
)

func TestGatewayStartReportsBindFailure(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	gw := New()
	gw.address = occupied.Addr().String()
	var opErr *net.OpError
	if err := gw.Start(context.Background()); !errors.As(err, &opErr) {
		t.Fatalf("Start = %v, want listen error", err)
	}
	if err := gw.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after failed Start: %v", err)
	}
}

func TestStopBeforeStartClosesStaticProxyConnections(t *testing.T) {
	registerHealthProxyInvoker(t)
	cfg := config.Default()
	route := healthRoute("127.0.0.1:9000")
	route.PoolSize = 2
	cfg.Gateway.Routes = []config.GatewayRouteConfig{route}
	gw := initGateway(t, cfg)
	conns := gw.configuredRoutes[0].conns
	if len(conns) != route.PoolSize {
		t.Fatalf("connection count = %d", len(conns))
	}
	if err := gw.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, conn := range conns {
		if conn.GetState() != connectivity.Shutdown {
			t.Fatalf("connection state after stopping = %s", conn.GetState())
		}
	}
}
