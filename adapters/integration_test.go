package adapters_test

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/svcforge/service-forge/adapters/consul"
	"github.com/svcforge/service-forge/adapters/otel"
	"github.com/svcforge/service-forge/adapters/postgres"
	"github.com/svcforge/service-forge/adapters/rabbitmq"
	"github.com/svcforge/service-forge/adapters/redis"
	"github.com/svcforge/service-forge/ports/eventbus"
	"github.com/svcforge/service-forge/ports/registry"
	tracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

func integrationEndpoint(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		t.Skipf("set %s to run this integration test", key)
	}
	return value
}

func integrationContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func requireSuccess(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRedisIntegration(t *testing.T) {
	addr := integrationEndpoint(t, "SVCFORGE_TEST_REDIS_ADDR")
	ctx := integrationContext(t)
	cache, err := redis.New(redis.Config{Addr: addr})
	requireSuccess(t, err)
	defer cache.Close()
	requireSuccess(t, cache.Health(ctx))
	key := "svcforge-test:" + uuid.NewString()
	requireSuccess(t, cache.Set(ctx, key, []byte("todo"), time.Minute))
	defer cache.Delete(ctx, key)
	value, err := cache.Get(ctx, key)
	requireSuccess(t, err)
	if string(value) != "todo" {
		t.Fatalf("value = %q", value)
	}
	requireSuccess(t, cache.Delete(ctx, key))
	exists, err := cache.Exists(ctx, key)
	requireSuccess(t, err)
	if exists {
		t.Fatal("key still exists after deletion")
	}
}

func TestPostgresIntegration(t *testing.T) {
	addr := integrationEndpoint(t, "SVCFORGE_TEST_POSTGRES_ADDR")
	host, rawPort, err := net.SplitHostPort(addr)
	requireSuccess(t, err)
	port, err := strconv.Atoi(rawPort)
	requireSuccess(t, err)
	ctx := integrationContext(t)
	store, err := postgres.New(postgres.Config{
		Host: host, Port: port, User: "postgres", Password: "svcforge-test", Database: "postgres", MaxOpenConns: 1, MaxIdleConns: 1,
	})
	requireSuccess(t, err)
	defer store.Close()
	requireSuccess(t, store.Health(ctx))

	requireSuccess(t, store.DB(ctx).Exec("CREATE TEMP TABLE svcforge_tx_test (id integer)").Error)
	rollback := errors.New("rollback")
	for _, tt := range []struct {
		id     int
		result error
	}{{1, nil}, {2, rollback}} {
		err := store.WithinTx(ctx, func(ctx context.Context) error {
			if err := store.DB(ctx).Exec("INSERT INTO svcforge_tx_test VALUES (?)", tt.id).Error; err != nil {
				return err
			}
			return tt.result
		})
		if !errors.Is(err, tt.result) {
			t.Fatalf("transaction %d error = %v, want %v", tt.id, err, tt.result)
		}
	}
	var ids []int
	requireSuccess(t, store.DB(ctx).Raw("SELECT id FROM svcforge_tx_test ORDER BY id").Scan(&ids).Error)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("rows after commit and rollback = %v", ids)
	}
}

func TestRabbitMQIntegration(t *testing.T) {
	url := integrationEndpoint(t, "SVCFORGE_TEST_RABBITMQ_URL")
	ctx := integrationContext(t)
	bus, err := rabbitmq.New(rabbitmq.Config{URL: url})
	requireSuccess(t, err)
	defer bus.Close()
	requireSuccess(t, bus.Health(ctx))
	topic := "svcforge.test." + uuid.NewString()
	messages := make(chan eventbus.Message, 1)
	requireSuccess(t, bus.Subscribe(ctx, topic, func(ctx context.Context, msg eventbus.Message) error {
		select {
		case messages <- msg:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	requireSuccess(t, bus.Publish(ctx, topic, eventbus.Message{ID: topic, Body: []byte("todo"), Headers: map[string]string{"source": "test"}}))
	select {
	case msg := <-messages:
		if msg.ID != topic || msg.Topic != topic || string(msg.Body) != "todo" || msg.Headers["source"] != "test" {
			t.Fatalf("unexpected delivery: %+v", msg)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestConsulIntegration(t *testing.T) {
	addr := integrationEndpoint(t, "SVCFORGE_TEST_CONSUL_ADDR")
	ctx := integrationContext(t)
	adapter, err := consul.New(consul.Config{Address: addr})
	requireSuccess(t, err)
	requireSuccess(t, adapter.Health(ctx))

	id := "svcforge-test-" + uuid.NewString()
	client, err := consulapi.NewClient(&consulapi.Config{Address: addr, Scheme: "http"})
	requireSuccess(t, err)
	requireSuccess(t, adapter.Register(ctx, registry.ServiceInstance{
		ID: id, Name: id, Address: "127.0.0.1", Port: 9000, Metadata: map[string]string{"source": "test"},
	}))
	defer adapter.Deregister(ctx, id)
	services, err := client.Agent().Services()
	requireSuccess(t, err)
	if service := services[id]; service == nil || service.Service != id || service.Meta["source"] != "test" {
		t.Fatalf("unexpected registration: %+v", service)
	}
	requireSuccess(t, adapter.Deregister(ctx, id))
	found, err := adapter.Resolve(ctx, id)
	requireSuccess(t, err)
	if len(found) != 0 {
		t.Fatalf("resolved deregistered service: %+v", found)
	}
}

type traceReceiver struct {
	tracepb.UnimplementedTraceServiceServer
	exports chan *tracepb.ExportTraceServiceRequest
}

func (r *traceReceiver) Export(ctx context.Context, req *tracepb.ExportTraceServiceRequest) (*tracepb.ExportTraceServiceResponse, error) {
	select {
	case r.exports <- req:
		return &tracepb.ExportTraceServiceResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestOpenTelemetryIntegration(t *testing.T) {
	ctx := integrationContext(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	requireSuccess(t, err)
	server := grpc.NewServer()
	receiver := &traceReceiver{exports: make(chan *tracepb.ExportTraceServiceRequest, 1)}
	tracepb.RegisterTraceServiceServer(server, receiver)
	go server.Serve(listener)
	defer server.Stop()
	provider, err := otel.New(ctx, otel.Config{Endpoint: listener.Addr().String()}, "svcforge-test")
	requireSuccess(t, err)
	_, span := provider.Tracer("integration").Start(ctx, "todo")
	span.End()
	requireSuccess(t, provider.Shutdown(ctx))
	select {
	case req := <-receiver.exports:
		spans := req.GetResourceSpans()
		if len(spans) != 1 || len(spans[0].GetScopeSpans()) != 1 ||
			len(spans[0].GetScopeSpans()[0].GetSpans()) != 1 ||
			spans[0].GetScopeSpans()[0].GetSpans()[0].GetName() != "todo" {
			t.Fatalf("unexpected OTLP export: %v", req)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
