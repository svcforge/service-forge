package gateway

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/ports/registry"
	"github.com/svcforge/service-forge/transport/grpcserver"
	"github.com/svcforge/service-forge/transport/routeconfig"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

type testRouteCatalog struct {
	mu       sync.Mutex
	manifest []byte
	instance registry.ServiceInstance
}

func (r *testRouteCatalog) PublishRoutes(_ context.Context, _ string, manifest []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manifest = bytes.Clone(manifest)
	return nil
}

func (r *testRouteCatalog) LoadRoutes(context.Context, string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Clone(r.manifest), nil
}

func (r *testRouteCatalog) ListRouteServices(context.Context) ([]string, error) {
	return []string{"echo-service"}, nil
}

func (r *testRouteCatalog) Register(_ context.Context, instance registry.ServiceInstance) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instance = instance
	return nil
}

func (r *testRouteCatalog) Deregister(context.Context, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instance = registry.ServiceInstance{}
	return nil
}

func (r *testRouteCatalog) Resolve(context.Context, string) ([]registry.ServiceInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.instance.ID == "" {
		return nil, nil
	}
	return []registry.ServiceInstance{r.instance}, nil
}

func testRouteDescriptor(t *testing.T, path string) *descriptorpb.FileDescriptorProto {
	t.Helper()
	options := &descriptorpb.MethodOptions{}
	proto.SetExtension(options, annotations.E_Http, &annotations.HttpRule{
		Pattern: &annotations.HttpRule_Post{Post: path}, Body: "*",
	})
	return &descriptorpb.FileDescriptorProto{
		Name: proto.String("svcforge_dynamic_test.proto"), Package: proto.String("svcforge_dynamic_test"),
		Syntax: proto.String("proto3"), Dependency: []string{"google/api/annotations.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Message"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: proto.String("id"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
				{Name: proto.String("message"), Number: proto.Int32(2), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			},
		}},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("Echo"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name: proto.String("Say"), InputType: proto.String(".svcforge_dynamic_test.Message"),
				OutputType: proto.String(".svcforge_dynamic_test.Message"), Options: options,
			}},
		}},
	}
}

func TestDynamicRoutePublicationAndReload(t *testing.T) {
	t.Run("gateway policy", func(t *testing.T) { testDynamicRoutePublicationAndReload(t, "gateway") })
	t.Run("service policy", func(t *testing.T) { testDynamicRoutePublicationAndReload(t, "service") })
	t.Run("proto annotations", func(t *testing.T) { testDynamicRoutePublicationAndReload(t, "proto") })
}

func testDynamicRoutePublicationAndReload(t *testing.T, mode string) {
	const service = "echo-service"
	const rpcService = "svcforge_dynamic_test.Echo"
	const firstPath = "/api/echo/{id}"
	const secondPath = "/api/echo/new/{id}"
	ctx := context.Background()
	store := &testRouteCatalog{}

	cfg := config.Default()
	cfg.Gateway.ListenIP = "127.0.0.1"
	cfg.Gateway.Port = 0
	var policy *config.PublishedRoutePolicy
	var token string
	if mode != "gateway" {
		secretEnv := "JWT_SECRET"
		if mode == "service" {
			secretEnv = "TEST_JWT_SECRET"
			policy = &config.PublishedRoutePolicy{Prefix: "/api/echo", JWTSecretEnv: secretEnv}
		}
		t.Setenv(secretEnv, "only-for-this-test")
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"account_id": "123", "exp": time.Now().Add(time.Minute).Unix(),
		}).SignedString([]byte("only-for-this-test"))
		if err != nil {
			t.Fatal(err)
		}
		token = signed
	} else {
		cfg.Gateway.DynamicRoutes.Services = map[string]string{service: "/api/echo"}
	}
	cfg.Gateway.DynamicRoutes.PollInterval = 20 * time.Millisecond
	cfg.Gateway.Plugins = []config.GatewayPluginConfig{
		{Name: "api_key", Config: map[string]any{"keys": []any{"test-key"}}},
	}
	gatewayRuntime := app.New(cfg)
	gatewayRuntime.Set(registry.RouteCatalogKey, store)
	gatewayRuntime.Set("resolver", store)
	gw := New(pingRoute())
	if err := gw.Init(ctx, gatewayRuntime); err != nil {
		t.Fatal(err)
	}
	if err := gw.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := gw.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	addr := "http://" + gw.listener.Addr().String()
	client := &http.Client{Timeout: time.Second}
	request := func(method, path, key string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, addr+path, strings.NewReader(`{"id":"body","message":"hello"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-API-Key", key)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(data)
	}
	if status, _ := request(http.MethodPost, "/api/echo/1", "test-key"); status != http.StatusNotFound {
		t.Fatalf("before service publication: status %d", status)
	}

	desc, err := protodesc.NewFile(testRouteDescriptor(t, firstPath), protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protoregistry.GlobalFiles.FindFileByPath(desc.Path()); err != nil {
		if err := protoregistry.GlobalFiles.RegisterFile(desc); err != nil {
			t.Fatal(err)
		}
	}
	input := desc.Messages().Get(0)
	server := grpcserver.NewModule(func(s *grpc.Server) {
		s.RegisterService(&grpc.ServiceDesc{
			ServiceName: rpcService,
			Methods: []grpc.MethodDesc{{
				MethodName: "Say",
				Handler: func(_ any, _ context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					msg := dynamicpb.NewMessage(input)
					return msg, decode(msg)
				},
			}},
		}, nil)
	})
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serviceCfg := config.Default()
	serviceCfg.App.Name = service
	serviceCfg.GRPC.ListenIP = "127.0.0.1"
	serviceCfg.GRPC.Port = reserved.Addr().(*net.TCPAddr).Port
	serviceCfg.GRPC.PublishRoutes = mode != "proto"
	serviceCfg.GRPC.RoutePolicy = policy
	reserved.Close()
	serviceRuntime := app.New(serviceCfg)
	serviceRuntime.Set(registry.RouteCatalogKey, store)
	serviceRuntime.Set("registrar", store)
	if err := server.Init(ctx, serviceRuntime); err != nil {
		t.Fatal(err)
	}
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop(ctx) })

	waitFor := func(path string, want int) string {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			status, body := request(http.MethodPost, path, "test-key")
			if status == want {
				return body
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: want %d, got %d: %s", path, want, status, body)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	checkEcho := func(path string) {
		t.Helper()
		body := waitFor(path, http.StatusOK)
		if !strings.Contains(body, `"id":"1"`) || !strings.Contains(body, `"message":"hello"`) {
			t.Fatalf("unexpected echo: %s", body)
		}
	}
	checkEcho("/api/echo/1")
	if mode != "gateway" {
		saved := token
		token = ""
		if status, _ := request(http.MethodPost, "/api/echo/1", "test-key"); status != http.StatusUnauthorized {
			t.Fatalf("published JWT was not enforced: %d", status)
		}
		token = saved
	}
	if status, _ := request(http.MethodPost, "/api/echo/1", ""); status != http.StatusUnauthorized {
		t.Fatalf("dynamic route bypassed global auth: %d", status)
	}
	if status, _ := request(http.MethodGet, "/api/v1/ping", "test-key"); status != http.StatusOK {
		t.Fatalf("static route failed: %d", status)
	}

	changed, err := protodesc.NewFile(testRouteDescriptor(t, secondPath), protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	files := new(protoregistry.Files)
	if err := files.RegisterFile(changed); err != nil {
		t.Fatal(err)
	}
	var manifest []byte
	if mode == "proto" {
		manifest, err = routeconfig.EncodeAnnotated([]string{rpcService}, files)
	} else {
		manifest, err = routeconfig.EncodeWithPolicy([]string{rpcService}, files, policy)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishRoutes(ctx, service, manifest); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	checkEcho("/api/echo/1")
	waitFor("/api/echo/new/1", http.StatusNotFound)

	store.mu.Lock()
	store.instance.Metadata = map[string]string{registry.RouteRevisionMetadata: routeconfig.Revision(manifest)}
	store.mu.Unlock()
	checkEcho("/api/echo/new/1")
	waitFor("/api/echo/1", http.StatusNotFound)

	if err := store.PublishRoutes(ctx, service, []byte("invalid manifest")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	checkEcho("/api/echo/new/1")

	if err := store.PublishRoutes(ctx, service, nil); err != nil {
		t.Fatal(err)
	}
	waitFor("/api/echo/new/1", http.StatusNotFound)
}
