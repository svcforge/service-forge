package templates

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"unicode"
)

type ProjectOptions struct {
	Name        string
	ServiceName string
	Replace     string
	DB          string
	Cache       string
	MQ          string
	Registry    string
	Tracing     string
}

func GenerateProject(root string, opts ProjectOptions) error {
	if opts.Name == "" {
		return fmt.Errorf("project name is required")
	}
	if opts.DB == "" {
		opts.DB = "noop"
	}
	if opts.Cache == "" {
		opts.Cache = "memory"
	}
	if opts.MQ == "" {
		opts.MQ = "memory"
	}
	if opts.Registry == "" {
		opts.Registry = "memory"
	}
	if opts.Tracing == "" {
		opts.Tracing = "noop"
	}
	if opts.ServiceName == "" {
		opts.ServiceName = "example-service"
	}
	base := filepath.Join(root, opts.Name)
	if opts.Replace == "" {
		opts.Replace = detectLocalReplace(root, base)
	}
	files := map[string]string{
		"go.mod":                             projectGoMod,
		"buf.yaml":                           bufYaml,
		"buf.gen.yaml":                       bufGenYaml,
		"README.md":                          projectReadme,
		"config/base.yaml":                   baseConfig,
		"gateway/cmd/main.go":                gatewayMain,
		"api/proto/example/v1/example.proto": exampleProto,
		// example-service: a complete vertical slice (handler -> service -> model -> repository).
		// Self-contained and dependency-free: in-memory store with seed data, runs out of the box.
		"services/example-service/cmd/main.go":                          exampleServiceMain,
		"services/example-service/internal/example/module.go":           exampleModule,
		"services/example-service/internal/model/todo.go":               exampleModelTodo,
		"services/example-service/internal/model/errors.go":             exampleModelErrors,
		"services/example-service/internal/repository/todo_repo.go":     exampleRepo,
		"services/example-service/internal/service/todo_service.go":     exampleService,
		"services/example-service/internal/handler/rpc/todo_handler.go": exampleHandler,
	}
	for path, body := range files {
		if err := writeTemplate(filepath.Join(base, path), body, opts); err != nil {
			return err
		}
	}
	return nil
}

// serviceData is the template context for a generated service. Names are
// derived from the service name so the output is a compilable vertical slice.
type serviceData struct {
	Module   string // go module path, read from the project's go.mod
	Service  string // service directory name, e.g. "order-service"
	ProtoPkg string // proto package + dir, e.g. "order"
	GoPkg    string // wiring package identifier, e.g. "order"
	Type     string // PascalCase prefix, e.g. "Order" -> OrderService
}

func GenerateService(root, name string) error {
	if name == "" {
		return fmt.Errorf("service name is required")
	}
	module, err := readModulePath(root)
	if err != nil {
		return err
	}
	base := serviceBaseName(name)
	data := serviceData{
		Module:   module,
		Service:  name,
		ProtoPkg: toSnake(base),
		GoPkg:    toIdent(base),
		Type:     toPascal(base),
	}
	files := map[string]string{
		filepath.Join("api/proto", data.ProtoPkg, "v1", data.ProtoPkg+".proto"): addServiceProto,
		filepath.Join("services", name, "cmd/main.go"):                          addServiceMain,
		filepath.Join("services", name, "internal", data.GoPkg, "module.go"):    addServiceModule,
		filepath.Join("services", name, "internal/model/item.go"):               addServiceModelItem,
		filepath.Join("services", name, "internal/model/errors.go"):             addServiceModelErrors,
		filepath.Join("services", name, "internal/repository/item_repo.go"):     addServiceRepo,
		filepath.Join("services", name, "internal/service/item_service.go"):     addServiceService,
		filepath.Join("services", name, "internal/handler/rpc/item_handler.go"): addServiceHandler,
		filepath.Join("services", name, "README.md"):                            addServiceReadme,
	}
	for path, body := range files {
		if err := writeTemplate(filepath.Join(root, path), body, data); err != nil {
			return err
		}
	}
	return nil
}

// readModulePath returns the module path from the project's go.mod.
func readModulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod (run inside a project root): %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("module directive not found in go.mod")
}

// serviceBaseName strips a trailing -service/_service suffix so a service named
// "order-service" yields proto package "order" and type "Order".
func serviceBaseName(name string) string {
	for _, suffix := range []string{"-service", "_service"} {
		if base := strings.TrimSuffix(name, suffix); base != name && base != "" {
			return base
		}
	}
	return name
}

func toSnake(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "-", "_"))
}

func toIdent(s string) string {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(s))
}

func toPascal(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
	var b strings.Builder
	for _, p := range parts {
		runes := []rune(strings.ToLower(p))
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	return b.String()
}

func writeTemplate(path, body string, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tpl, err := template.New(filepath.Base(path)).Parse(body)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func detectLocalReplace(root, projectDir string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	if !strings.Contains(string(data), "module github.com/svcforge/service-forge") {
		return ""
	}
	rel, err := filepath.Rel(projectDir, root)
	if err != nil {
		return root
	}
	return rel
}

const projectGoMod = `module {{ .Name }}

go 1.23
{{ if .Replace }}
require github.com/svcforge/service-forge v0.0.0

replace github.com/svcforge/service-forge => {{ .Replace }}
{{ end }}
`

const bufYaml = `version: v2
modules:
  - path: api/proto
`

const bufGenYaml = `version: v2
plugins:
  - remote: buf.build/protocolbuffers/go
    out: api/gen/go
    opt: paths=source_relative
  - remote: buf.build/grpc/go
    out: api/gen/go
    opt: paths=source_relative
`

const projectReadme = `# {{ .Name }}

Generated by Service Forge.

## Architecture

- Gateway exposes REST/JSON.
- Services expose gRPC only.
- Business code depends on ports, while adapters provide Redis/Postgres/RabbitMQ/Consul/etc.

The bundled ` + "`example-service`" + ` is a complete vertical slice you can copy:

` + "```" + `
services/example-service/internal/
  handler/rpc/   gRPC handlers + proto mapping
  service/       business logic + Input/Result DTOs
  repository/    storage (in-memory, swap for a real adapter)
  model/         domain model + errors
  example/       module wiring
` + "```" + `

## Local start

` + "```bash" + `
buf generate          # generate gRPC stubs into api/gen (run first)
go mod tidy
go run ./services/example-service/cmd   # gRPC :9000
go run ./gateway/cmd                     # REST :8080 (separate terminal)
` + "```" + `

## Try it (Todo CRUD, no database required)

` + "```bash" + `
# list seeded todos
curl localhost:8080/api/v1/todos

# create one
curl -X POST localhost:8080/api/v1/todos \
  -H 'Content-Type: application/json' \
  -d '{"title":"my first todo","content":"hello"}'

# get / update / delete (use an id from the responses above)
curl 'localhost:8080/api/v1/todos/detail?id=1'
curl -X PUT localhost:8080/api/v1/todos \
  -H 'Content-Type: application/json' -d '{"id":"1","done":true}'
curl -X DELETE localhost:8080/api/v1/todos \
  -H 'Content-Type: application/json' -d '{"id":"1"}'
` + "```" + `
`

const baseConfig = `app:
  name: {{ .Name }}
  version: v0.1.0
  env: development
  debug: true

gateway:
  listen_ip: 0.0.0.0
  port: 8080
  disable_startup_message: true
  # Built-in plugins are off until listed here. They run in config order.
  # Available: recovery, access_log, cors, rate_limit, api_key, jwt, metrics.
  # plugins:
  #   - name: recovery
  #   - name: access_log
  #   - name: rate_limit
  #     config:
  #       max: 100
  #       window: 1m
  #   - name: metrics
  routes:
    # REST/JSON -> gRPC proxy routes for example-service (Todo CRUD).
    # For local multi-process dev, dial the service directly with target.
    # With consul or another shared registry, use "service: example-service" instead.
    - name: todo-create
      method: POST
      path: /api/v1/todos
      target: 127.0.0.1:9000
      rpc: /example.v1.ExampleService/CreateTodo
      pool_size: 4
      timeout: 3s
    - name: todo-list
      method: GET
      path: /api/v1/todos
      target: 127.0.0.1:9000
      rpc: /example.v1.ExampleService/ListTodos
      pool_size: 4
      timeout: 3s
    - name: todo-get
      method: GET
      path: /api/v1/todos/detail
      target: 127.0.0.1:9000
      rpc: /example.v1.ExampleService/GetTodo
      pool_size: 4
      timeout: 3s
    - name: todo-update
      method: PUT
      path: /api/v1/todos
      target: 127.0.0.1:9000
      rpc: /example.v1.ExampleService/UpdateTodo
      pool_size: 4
      timeout: 3s
    - name: todo-delete
      method: DELETE
      path: /api/v1/todos
      target: 127.0.0.1:9000
      rpc: /example.v1.ExampleService/DeleteTodo
      pool_size: 4
      timeout: 3s

log:
  format: text
  level: info
  module_lifecycle: false

grpc:
  listen_ip: 0.0.0.0
  port: 9000

runtime:
  components:
    - name: store
      provider: {{ .DB }}
    - name: cache
      provider: {{ .Cache }}
    - name: eventbus
      provider: {{ .MQ }}
    - name: registry
      provider: {{ .Registry }}
    - name: tracing
      provider: {{ .Tracing }}

modules:
  postgres:
    host: localhost
    port: 5432
    user: postgres
    password: postgres
    database: {{ .Name }}
    sslmode: disable
  redis:
    addr: localhost:6379
  rabbitmq:
    url: amqp://guest:guest@localhost:5672/
    exchange: events
  consul:
    address: localhost:8500
`

const gatewayMain = `package main

import (
	"context"
	"log"

	examplev1 "{{ .Name }}/api/gen/go/example/v1"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/adapters"
	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/transport/gateway"
	"google.golang.org/grpc"
)

func main() {
	registerInvokers()

	bundle, err := config.Load[struct{}](config.LoadOptions{
		ConfigDir:   "config",
		ServiceName: "{{ .Name }}",
		EnableLocal: true,
	})
	if err != nil {
		log.Fatal(err)
	}

	gw := gateway.New(func(router *fiber.App, gw *gateway.Gateway) {
		router.Get("/api/v1/ping", gw.Handle(func(ctx context.Context, c *fiber.Ctx) (any, error) {
			return fiber.Map{"message": "pong"}, nil
		}))
	})

	mods, err := adapters.DefaultCatalog().Build(bundle.Core.Runtime.Components)
	if err != nil {
		log.Fatal(err)
	}
	mods = append(mods, gw)

	application := app.New(bundle.Core, app.WithModules(mods...))
	if err := application.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// registerInvokers maps each REST route's rpc to a typed gRPC proxy so the
// gateway can encode/decode the request and response messages.
func registerInvokers() {
	gateway.MustRegisterProxyInvoker("/example.v1.ExampleService/CreateTodo", gateway.NewUnaryProxy(
		func() *examplev1.CreateTodoRequest { return &examplev1.CreateTodoRequest{} },
		func(ctx context.Context, conn *grpc.ClientConn, req *examplev1.CreateTodoRequest) (*examplev1.CreateTodoResponse, error) {
			return examplev1.NewExampleServiceClient(conn).CreateTodo(ctx, req)
		},
	))
	gateway.MustRegisterProxyInvoker("/example.v1.ExampleService/GetTodo", gateway.NewUnaryProxy(
		func() *examplev1.GetTodoRequest { return &examplev1.GetTodoRequest{} },
		func(ctx context.Context, conn *grpc.ClientConn, req *examplev1.GetTodoRequest) (*examplev1.GetTodoResponse, error) {
			return examplev1.NewExampleServiceClient(conn).GetTodo(ctx, req)
		},
	))
	gateway.MustRegisterProxyInvoker("/example.v1.ExampleService/ListTodos", gateway.NewUnaryProxy(
		func() *examplev1.ListTodosRequest { return &examplev1.ListTodosRequest{} },
		func(ctx context.Context, conn *grpc.ClientConn, req *examplev1.ListTodosRequest) (*examplev1.ListTodosResponse, error) {
			return examplev1.NewExampleServiceClient(conn).ListTodos(ctx, req)
		},
	))
	gateway.MustRegisterProxyInvoker("/example.v1.ExampleService/UpdateTodo", gateway.NewUnaryProxy(
		func() *examplev1.UpdateTodoRequest { return &examplev1.UpdateTodoRequest{} },
		func(ctx context.Context, conn *grpc.ClientConn, req *examplev1.UpdateTodoRequest) (*examplev1.UpdateTodoResponse, error) {
			return examplev1.NewExampleServiceClient(conn).UpdateTodo(ctx, req)
		},
	))
	gateway.MustRegisterProxyInvoker("/example.v1.ExampleService/DeleteTodo", gateway.NewUnaryProxy(
		func() *examplev1.DeleteTodoRequest { return &examplev1.DeleteTodoRequest{} },
		func(ctx context.Context, conn *grpc.ClientConn, req *examplev1.DeleteTodoRequest) (*examplev1.DeleteTodoResponse, error) {
			return examplev1.NewExampleServiceClient(conn).DeleteTodo(ctx, req)
		},
	))
}
`

const exampleProto = `syntax = "proto3";

package example.v1;

option go_package = "{{ .Name }}/api/gen/go/example/v1;examplev1";

import "google/protobuf/timestamp.proto";

// ExampleService demonstrates a complete vertical slice:
// handler -> service -> repository -> model, with pagination.
service ExampleService {
  // Ping is a dependency-free connectivity probe.
  rpc Ping(PingRequest) returns (PingResponse);

  // Todo CRUD over an in-memory store (no database required).
  rpc CreateTodo(CreateTodoRequest) returns (CreateTodoResponse);
  rpc GetTodo(GetTodoRequest) returns (GetTodoResponse);
  rpc ListTodos(ListTodosRequest) returns (ListTodosResponse);
  rpc UpdateTodo(UpdateTodoRequest) returns (UpdateTodoResponse);
  rpc DeleteTodo(DeleteTodoRequest) returns (DeleteTodoResponse);
}

message PingRequest {
  string message = 1;
}

message PingResponse {
  string message = 1;
}

message Todo {
  string id = 1;
  string title = 2;
  string content = 3;
  bool done = 4;
  google.protobuf.Timestamp created_at = 5;
  google.protobuf.Timestamp updated_at = 6;
}

message CreateTodoRequest {
  string title = 1;
  string content = 2;
}

message CreateTodoResponse {
  Todo todo = 1;
}

message GetTodoRequest {
  string id = 1;
}

message GetTodoResponse {
  Todo todo = 1;
}

message ListTodosRequest {
  int32 page = 1;          // 1-based, defaults to 1
  int32 page_size = 2;     // defaults to 20, max 100
  bool only_pending = 3;   // when true, hide completed todos
}

message ListTodosResponse {
  repeated Todo todos = 1;
  int32 page = 2;
  int32 page_size = 3;
  int64 total = 4;
}

message UpdateTodoRequest {
  string id = 1;
  string title = 2;
  string content = 3;
  bool done = 4;
}

message UpdateTodoResponse {
  Todo todo = 1;
}

message DeleteTodoRequest {
  string id = 1;
}

message DeleteTodoResponse {}
`

const exampleServiceMain = `package main

import (
	"context"
	"log"

	examplemod "{{ .Name }}/services/example-service/internal/example"

	"github.com/svcforge/service-forge/adapters"
	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/transport/grpcserver"
)

func main() {
	bundle, err := config.Load[struct{}](config.LoadOptions{
		ConfigDir:   "config",
		ServiceName: "example-service",
		EnableLocal: true,
	})
	if err != nil {
		log.Fatal(err)
	}

	mods, err := adapters.DefaultCatalog().Build(bundle.Core.Runtime.Components)
	if err != nil {
		log.Fatal(err)
	}

	exampleMod := examplemod.NewModule()
	grpcMod := grpcserver.NewModule(exampleMod.RegisterOnServer)

	mods = append(mods, exampleMod, grpcMod)

	application := app.New(bundle.Core, app.WithModules(mods...))
	if err := application.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
`

const exampleModule = `package example

import (
	"context"

	examplev1 "{{ .Name }}/api/gen/go/example/v1"
	"{{ .Name }}/services/example-service/internal/handler/rpc"
	"{{ .Name }}/services/example-service/internal/repository"
	"{{ .Name }}/services/example-service/internal/service"

	"github.com/svcforge/service-forge/core/module"
	"google.golang.org/grpc"
)

// Module wires the example service. The data layer is an in-memory store, so it
// has no external dependencies and runs out of the box.
type Module struct {
	module.BaseModule
	handler *rpc.ExampleHandler
}

// NewModule creates a new example Module.
func NewModule() *Module {
	return &Module{
		BaseModule: module.BaseModule{ModuleName: "example-service"},
	}
}

// Init assembles the layers: repository(in-memory) -> service -> handler.
func (m *Module) Init(ctx context.Context, app module.Runtime) error {
	todoRepo := repository.NewTodoRepo()        // in-memory store + seed data
	todoSvc := service.NewTodoService(todoRepo) // business logic
	m.handler = rpc.NewExampleHandler(todoSvc)  // gRPC handler
	return nil
}

// RegisterOnServer registers the example service on a gRPC server.
func (m *Module) RegisterOnServer(s *grpc.Server) {
	if m.handler == nil {
		return
	}
	examplev1.RegisterExampleServiceServer(s, m.handler)
}
`

const exampleModelTodo = `package model

import "time"

// Todo is the example domain model (in-memory; no persistence tags).
type Todo struct {
	ID        string
	Title     string
	Content   string
	Done      bool
	CreatedAt time.Time
	UpdatedAt time.Time
}
`

const exampleModelErrors = `package model

import "errors"

// Domain errors for the example service.
var (
	ErrTodoNotFound = errors.New("todo not found")
	ErrInvalidTitle = errors.New("title must be 1-128 characters")
)
`

const exampleRepo = `package repository

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"{{ .Name }}/services/example-service/internal/model"
)

// TodoRepo is an in-memory todo store (process-local, no database).
// Swap this implementation for a real adapter (postgres, etc.) without touching
// the service or handler layers.
type TodoRepo struct {
	mu    sync.RWMutex
	seq   atomic.Int64
	todos map[string]model.Todo
}

// NewTodoRepo creates an in-memory TodoRepo seeded with sample data.
func NewTodoRepo() *TodoRepo {
	r := &TodoRepo{todos: make(map[string]model.Todo)}
	r.seed()
	return r
}

func (r *TodoRepo) nextID() string {
	return strconv.FormatInt(r.seq.Add(1), 10)
}

func (r *TodoRepo) seed() {
	now := time.Now()
	fixtures := []model.Todo{
		{Title: "Try service-forge", Content: "Run the example-service end to end", Done: true},
		{Title: "Read the docs", Content: "Learn how ports and adapters fit together", Done: false},
		{Title: "Build your own service", Content: "Copy example-service and rename the resource", Done: false},
	}
	for _, t := range fixtures {
		id := r.nextID()
		t.ID = id
		t.CreatedAt = now
		t.UpdatedAt = now
		r.todos[id] = t
	}
}

// Create inserts a new todo and assigns it an ID.
func (r *TodoRepo) Create(ctx context.Context, todo *model.Todo) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	created := *todo
	created.ID = r.nextID()
	created.CreatedAt = now
	created.UpdatedAt = now

	r.todos[created.ID] = created
	*todo = created
	return nil
}

// FindByID returns a todo by ID.
func (r *TodoRepo) FindByID(ctx context.Context, id string) (*model.Todo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	todo, ok := r.todos[id]
	if !ok {
		return nil, model.ErrTodoNotFound
	}
	return &todo, nil
}

// List returns a page of todos (newest first), optionally hiding completed ones.
func (r *TodoRepo) List(ctx context.Context, onlyPending bool, offset, limit int) ([]model.Todo, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matched []model.Todo
	for _, t := range r.todos {
		if onlyPending && t.Done {
			continue
		}
		matched = append(matched, t)
	}

	// Newest first: ids are monotonically increasing.
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].ID > matched[j].ID
	})

	total := int64(len(matched))
	if offset >= len(matched) {
		return []model.Todo{}, total, nil
	}
	end := min(offset+limit, len(matched))
	return matched[offset:end], total, nil
}

// Update saves the mutable fields of an existing todo.
func (r *TodoRepo) Update(ctx context.Context, todo *model.Todo) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.todos[todo.ID]
	if !ok {
		return model.ErrTodoNotFound
	}

	updated := existing
	updated.Title = todo.Title
	updated.Content = todo.Content
	updated.Done = todo.Done
	updated.UpdatedAt = time.Now()

	r.todos[updated.ID] = updated
	*todo = updated
	return nil
}

// Delete removes a todo, returning ErrTodoNotFound when it does not exist.
func (r *TodoRepo) Delete(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.todos[id]; !ok {
		return model.ErrTodoNotFound
	}
	delete(r.todos, id)
	return nil
}
`

const exampleService = `package service

import (
	"context"

	"{{ .Name }}/services/example-service/internal/model"
	"{{ .Name }}/services/example-service/internal/repository"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// TodoService holds the business logic for todos.
type TodoService struct {
	repo *repository.TodoRepo
}

// NewTodoService creates a new TodoService.
func NewTodoService(repo *repository.TodoRepo) *TodoService {
	return &TodoService{repo: repo}
}

// TodoResult is the DTO returned to the API layer (decoupled from model/proto).
type TodoResult struct {
	ID        string
	Title     string
	Content   string
	Done      bool
	CreatedAt int64
	UpdatedAt int64
}

// CreateTodoInput holds the fields needed to create a todo.
type CreateTodoInput struct {
	Title   string
	Content string
}

// CreateTodo creates a new todo.
func (s *TodoService) CreateTodo(ctx context.Context, input CreateTodoInput) (*TodoResult, error) {
	if err := validateTitle(input.Title); err != nil {
		return nil, err
	}
	todo := &model.Todo{Title: input.Title, Content: input.Content}
	if err := s.repo.Create(ctx, todo); err != nil {
		return nil, err
	}
	return toTodoResult(todo), nil
}

// GetTodo returns a single todo by ID.
func (s *TodoService) GetTodo(ctx context.Context, id string) (*TodoResult, error) {
	todo, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return toTodoResult(todo), nil
}

// ListTodosInput holds list parameters.
type ListTodosInput struct {
	OnlyPending bool
	Page        int32
	PageSize    int32
}

// ListTodosResult holds a page of todos.
type ListTodosResult struct {
	Todos    []TodoResult
	Page     int32
	PageSize int32
	Total    int64
}

// ListTodos returns a page of todos.
func (s *TodoService) ListTodos(ctx context.Context, input ListTodosInput) (*ListTodosResult, error) {
	page, pageSize := normalizePage(input.Page, input.PageSize)
	offset := int((page - 1) * pageSize)

	todos, total, err := s.repo.List(ctx, input.OnlyPending, offset, int(pageSize))
	if err != nil {
		return nil, err
	}

	results := make([]TodoResult, len(todos))
	for i := range todos {
		results[i] = *toTodoResult(&todos[i])
	}

	return &ListTodosResult{Todos: results, Page: page, PageSize: pageSize, Total: total}, nil
}

// UpdateTodoInput holds the fields to update.
type UpdateTodoInput struct {
	ID      string
	Title   string
	Content string
	Done    bool
}

// UpdateTodo updates a todo's title, content and done flag.
func (s *TodoService) UpdateTodo(ctx context.Context, input UpdateTodoInput) (*TodoResult, error) {
	todo, err := s.repo.FindByID(ctx, input.ID)
	if err != nil {
		return nil, err
	}

	if input.Title != "" {
		if err := validateTitle(input.Title); err != nil {
			return nil, err
		}
		todo.Title = input.Title
	}
	if input.Content != "" {
		todo.Content = input.Content
	}
	todo.Done = input.Done

	if err := s.repo.Update(ctx, todo); err != nil {
		return nil, err
	}
	return toTodoResult(todo), nil
}

// DeleteTodo removes a todo.
func (s *TodoService) DeleteTodo(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

func validateTitle(title string) error {
	if l := len(title); l < 1 || l > 128 {
		return model.ErrInvalidTitle
	}
	return nil
}

func normalizePage(page, pageSize int32) (int32, int32) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > maxPageSize {
		pageSize = defaultPageSize
	}
	return page, pageSize
}

func toTodoResult(t *model.Todo) *TodoResult {
	return &TodoResult{
		ID:        t.ID,
		Title:     t.Title,
		Content:   t.Content,
		Done:      t.Done,
		CreatedAt: t.CreatedAt.Unix(),
		UpdatedAt: t.UpdatedAt.Unix(),
	}
}
`

const exampleHandler = `package rpc

import (
	"context"
	"errors"
	"time"

	examplev1 "{{ .Name }}/api/gen/go/example/v1"
	"{{ .Name }}/services/example-service/internal/model"
	"{{ .Name }}/services/example-service/internal/service"

	sferrors "github.com/svcforge/service-forge/core/errors"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ExampleHandler implements the examplev1.ExampleServiceServer gRPC interface.
type ExampleHandler struct {
	examplev1.UnimplementedExampleServiceServer
	todoSvc *service.TodoService
}

// NewExampleHandler creates a new ExampleHandler.
func NewExampleHandler(todoSvc *service.TodoService) *ExampleHandler {
	return &ExampleHandler{todoSvc: todoSvc}
}

// Ping is a dependency-free connectivity probe.
func (h *ExampleHandler) Ping(ctx context.Context, req *examplev1.PingRequest) (*examplev1.PingResponse, error) {
	return &examplev1.PingResponse{Message: req.GetMessage()}, nil
}

// CreateTodo creates a new todo.
func (h *ExampleHandler) CreateTodo(ctx context.Context, req *examplev1.CreateTodoRequest) (*examplev1.CreateTodoResponse, error) {
	todo, err := h.todoSvc.CreateTodo(ctx, service.CreateTodoInput{
		Title:   req.GetTitle(),
		Content: req.GetContent(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return &examplev1.CreateTodoResponse{Todo: toProtoTodo(todo)}, nil
}

// GetTodo returns a single todo.
func (h *ExampleHandler) GetTodo(ctx context.Context, req *examplev1.GetTodoRequest) (*examplev1.GetTodoResponse, error) {
	todo, err := h.todoSvc.GetTodo(ctx, req.GetId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return &examplev1.GetTodoResponse{Todo: toProtoTodo(todo)}, nil
}

// ListTodos returns a page of todos.
func (h *ExampleHandler) ListTodos(ctx context.Context, req *examplev1.ListTodosRequest) (*examplev1.ListTodosResponse, error) {
	result, err := h.todoSvc.ListTodos(ctx, service.ListTodosInput{
		OnlyPending: req.GetOnlyPending(),
		Page:        req.GetPage(),
		PageSize:    req.GetPageSize(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}

	todos := make([]*examplev1.Todo, len(result.Todos))
	for i := range result.Todos {
		todos[i] = toProtoTodo(&result.Todos[i])
	}

	return &examplev1.ListTodosResponse{
		Todos:    todos,
		Page:     result.Page,
		PageSize: result.PageSize,
		Total:    result.Total,
	}, nil
}

// UpdateTodo updates a todo.
func (h *ExampleHandler) UpdateTodo(ctx context.Context, req *examplev1.UpdateTodoRequest) (*examplev1.UpdateTodoResponse, error) {
	todo, err := h.todoSvc.UpdateTodo(ctx, service.UpdateTodoInput{
		ID:      req.GetId(),
		Title:   req.GetTitle(),
		Content: req.GetContent(),
		Done:    req.GetDone(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return &examplev1.UpdateTodoResponse{Todo: toProtoTodo(todo)}, nil
}

// DeleteTodo removes a todo.
func (h *ExampleHandler) DeleteTodo(ctx context.Context, req *examplev1.DeleteTodoRequest) (*examplev1.DeleteTodoResponse, error) {
	if err := h.todoSvc.DeleteTodo(ctx, req.GetId()); err != nil {
		return nil, toGRPCError(err)
	}
	return &examplev1.DeleteTodoResponse{}, nil
}

// toProtoTodo maps the service Result DTO to the proto message.
func toProtoTodo(t *service.TodoResult) *examplev1.Todo {
	return &examplev1.Todo{
		Id:        t.ID,
		Title:     t.Title,
		Content:   t.Content,
		Done:      t.Done,
		CreatedAt: timestamppb.New(time.Unix(t.CreatedAt, 0)),
		UpdatedAt: timestamppb.New(time.Unix(t.UpdatedAt, 0)),
	}
}

// toGRPCError maps domain errors to framework AppErrors. The grpcserver's
// ErrorInterceptor turns these into the right gRPC status, and the gateway
// turns them into the right HTTP status.
func toGRPCError(err error) error {
	switch {
	case errors.Is(err, model.ErrTodoNotFound):
		return sferrors.New(sferrors.CodeNotFound, err.Error())
	case errors.Is(err, model.ErrInvalidTitle):
		return sferrors.New(sferrors.CodeInvalidArgument, err.Error())
	default:
		return sferrors.New(sferrors.CodeInternal, err.Error())
	}
}
`

// ── add service templates ───────────────────────────────────────────────
// These produce a complete vertical slice for `svcforge add service <name>`,
// mirroring example-service: handler -> service -> repository -> model with an
// in-memory store (no database, no auth) so it compiles and runs immediately.

const addServiceProto = `syntax = "proto3";

package {{ .ProtoPkg }}.v1;

option go_package = "{{ .Module }}/api/gen/go/{{ .ProtoPkg }}/v1;{{ .ProtoPkg }}v1";

import "google/protobuf/timestamp.proto";

// {{ .Type }}Service is a complete vertical slice over an in-memory Item store.
service {{ .Type }}Service {
  rpc CreateItem(CreateItemRequest) returns (CreateItemResponse);
  rpc GetItem(GetItemRequest) returns (GetItemResponse);
  rpc ListItems(ListItemsRequest) returns (ListItemsResponse);
  rpc UpdateItem(UpdateItemRequest) returns (UpdateItemResponse);
  rpc DeleteItem(DeleteItemRequest) returns (DeleteItemResponse);
}

message Item {
  string id = 1;
  string name = 2;
  string description = 3;
  google.protobuf.Timestamp created_at = 4;
  google.protobuf.Timestamp updated_at = 5;
}

message CreateItemRequest {
  string name = 1;
  string description = 2;
}

message CreateItemResponse {
  Item item = 1;
}

message GetItemRequest {
  string id = 1;
}

message GetItemResponse {
  Item item = 1;
}

message ListItemsRequest {
  int32 page = 1;       // 1-based, defaults to 1
  int32 page_size = 2;  // defaults to 20, max 100
}

message ListItemsResponse {
  repeated Item items = 1;
  int32 page = 2;
  int32 page_size = 3;
  int64 total = 4;
}

message UpdateItemRequest {
  string id = 1;
  string name = 2;
  string description = 3;
}

message UpdateItemResponse {
  Item item = 1;
}

message DeleteItemRequest {
  string id = 1;
}

message DeleteItemResponse {}
`

const addServiceMain = `package main

import (
	"context"
	"log"

	{{ .GoPkg }}mod "{{ .Module }}/services/{{ .Service }}/internal/{{ .GoPkg }}"

	"github.com/svcforge/service-forge/adapters"
	"github.com/svcforge/service-forge/core/app"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/transport/grpcserver"
)

func main() {
	bundle, err := config.Load[struct{}](config.LoadOptions{
		ConfigDir:   "config",
		ServiceName: "{{ .Service }}",
		EnableLocal: true,
	})
	if err != nil {
		log.Fatal(err)
	}

	mods, err := adapters.DefaultCatalog().Build(bundle.Core.Runtime.Components)
	if err != nil {
		log.Fatal(err)
	}

	svcMod := {{ .GoPkg }}mod.NewModule()
	grpcMod := grpcserver.NewModule(svcMod.RegisterOnServer)

	mods = append(mods, svcMod, grpcMod)

	application := app.New(bundle.Core, app.WithModules(mods...))
	if err := application.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
`

const addServiceModule = `package {{ .GoPkg }}

import (
	"context"

	{{ .ProtoPkg }}v1 "{{ .Module }}/api/gen/go/{{ .ProtoPkg }}/v1"
	"{{ .Module }}/services/{{ .Service }}/internal/handler/rpc"
	"{{ .Module }}/services/{{ .Service }}/internal/repository"
	"{{ .Module }}/services/{{ .Service }}/internal/service"

	"github.com/svcforge/service-forge/core/module"
	"google.golang.org/grpc"
)

// Module wires the {{ .Service }} components. The data layer is an in-memory
// store, so it has no external dependencies and runs out of the box.
type Module struct {
	module.BaseModule
	handler *rpc.{{ .Type }}Handler
}

// NewModule creates a new {{ .Service }} Module.
func NewModule() *Module {
	return &Module{
		BaseModule: module.BaseModule{ModuleName: "{{ .Service }}"},
	}
}

// Init assembles the layers: repository(in-memory) -> service -> handler.
func (m *Module) Init(ctx context.Context, app module.Runtime) error {
	itemRepo := repository.NewItemRepo()
	itemSvc := service.NewItemService(itemRepo)
	m.handler = rpc.New{{ .Type }}Handler(itemSvc)
	return nil
}

// RegisterOnServer registers the service on a gRPC server.
func (m *Module) RegisterOnServer(s *grpc.Server) {
	if m.handler == nil {
		return
	}
	{{ .ProtoPkg }}v1.Register{{ .Type }}ServiceServer(s, m.handler)
}
`

const addServiceModelItem = `package model

import "time"

// Item is the domain model (in-memory; no persistence tags).
type Item struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
`

const addServiceModelErrors = `package model

import "errors"

// Domain errors for the service.
var (
	ErrItemNotFound = errors.New("item not found")
	ErrInvalidName  = errors.New("name must be 1-128 characters")
)
`

const addServiceRepo = `package repository

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"{{ .Module }}/services/{{ .Service }}/internal/model"
)

// ItemRepo is an in-memory store (process-local, no database). Swap this for a
// real adapter (postgres, etc.) without touching the service or handler layers.
type ItemRepo struct {
	mu    sync.RWMutex
	seq   atomic.Int64
	items map[string]model.Item
}

// NewItemRepo creates an in-memory ItemRepo seeded with sample data.
func NewItemRepo() *ItemRepo {
	r := &ItemRepo{items: make(map[string]model.Item)}
	r.seed()
	return r
}

func (r *ItemRepo) nextID() string {
	return strconv.FormatInt(r.seq.Add(1), 10)
}

func (r *ItemRepo) seed() {
	now := time.Now()
	fixtures := []model.Item{
		{Name: "first item", Description: "seeded sample data"},
		{Name: "second item", Description: "edit or delete me"},
	}
	for _, it := range fixtures {
		id := r.nextID()
		it.ID = id
		it.CreatedAt = now
		it.UpdatedAt = now
		r.items[id] = it
	}
}

// Create inserts a new item and assigns it an ID.
func (r *ItemRepo) Create(ctx context.Context, item *model.Item) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	created := *item
	created.ID = r.nextID()
	created.CreatedAt = now
	created.UpdatedAt = now

	r.items[created.ID] = created
	*item = created
	return nil
}

// FindByID returns an item by ID.
func (r *ItemRepo) FindByID(ctx context.Context, id string) (*model.Item, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	item, ok := r.items[id]
	if !ok {
		return nil, model.ErrItemNotFound
	}
	return &item, nil
}

// List returns a page of items, newest first.
func (r *ItemRepo) List(ctx context.Context, offset, limit int) ([]model.Item, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	matched := make([]model.Item, 0, len(r.items))
	for _, it := range r.items {
		matched = append(matched, it)
	}

	// Newest first: ids are monotonically increasing.
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].ID > matched[j].ID
	})

	total := int64(len(matched))
	if offset >= len(matched) {
		return []model.Item{}, total, nil
	}
	end := min(offset+limit, len(matched))
	return matched[offset:end], total, nil
}

// Update saves the mutable fields of an existing item.
func (r *ItemRepo) Update(ctx context.Context, item *model.Item) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.items[item.ID]
	if !ok {
		return model.ErrItemNotFound
	}

	updated := existing
	updated.Name = item.Name
	updated.Description = item.Description
	updated.UpdatedAt = time.Now()

	r.items[updated.ID] = updated
	*item = updated
	return nil
}

// Delete removes an item, returning ErrItemNotFound when it does not exist.
func (r *ItemRepo) Delete(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.items[id]; !ok {
		return model.ErrItemNotFound
	}
	delete(r.items, id)
	return nil
}
`

const addServiceService = `package service

import (
	"context"

	"{{ .Module }}/services/{{ .Service }}/internal/model"
	"{{ .Module }}/services/{{ .Service }}/internal/repository"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// ItemService holds the business logic for items.
type ItemService struct {
	repo *repository.ItemRepo
}

// NewItemService creates a new ItemService.
func NewItemService(repo *repository.ItemRepo) *ItemService {
	return &ItemService{repo: repo}
}

// ItemResult is the DTO returned to the API layer (decoupled from model/proto).
type ItemResult struct {
	ID          string
	Name        string
	Description string
	CreatedAt   int64
	UpdatedAt   int64
}

// CreateItemInput holds the fields needed to create an item.
type CreateItemInput struct {
	Name        string
	Description string
}

// CreateItem creates a new item.
func (s *ItemService) CreateItem(ctx context.Context, input CreateItemInput) (*ItemResult, error) {
	if err := validateName(input.Name); err != nil {
		return nil, err
	}
	item := &model.Item{Name: input.Name, Description: input.Description}
	if err := s.repo.Create(ctx, item); err != nil {
		return nil, err
	}
	return toItemResult(item), nil
}

// GetItem returns a single item by ID.
func (s *ItemService) GetItem(ctx context.Context, id string) (*ItemResult, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return toItemResult(item), nil
}

// ListItemsInput holds list parameters.
type ListItemsInput struct {
	Page     int32
	PageSize int32
}

// ListItemsResult holds a page of items.
type ListItemsResult struct {
	Items    []ItemResult
	Page     int32
	PageSize int32
	Total    int64
}

// ListItems returns a page of items.
func (s *ItemService) ListItems(ctx context.Context, input ListItemsInput) (*ListItemsResult, error) {
	page, pageSize := normalizePage(input.Page, input.PageSize)
	offset := int((page - 1) * pageSize)

	items, total, err := s.repo.List(ctx, offset, int(pageSize))
	if err != nil {
		return nil, err
	}

	results := make([]ItemResult, len(items))
	for i := range items {
		results[i] = *toItemResult(&items[i])
	}

	return &ListItemsResult{Items: results, Page: page, PageSize: pageSize, Total: total}, nil
}

// UpdateItemInput holds the fields to update.
type UpdateItemInput struct {
	ID          string
	Name        string
	Description string
}

// UpdateItem updates an item's name and description.
func (s *ItemService) UpdateItem(ctx context.Context, input UpdateItemInput) (*ItemResult, error) {
	item, err := s.repo.FindByID(ctx, input.ID)
	if err != nil {
		return nil, err
	}

	if input.Name != "" {
		if err := validateName(input.Name); err != nil {
			return nil, err
		}
		item.Name = input.Name
	}
	if input.Description != "" {
		item.Description = input.Description
	}

	if err := s.repo.Update(ctx, item); err != nil {
		return nil, err
	}
	return toItemResult(item), nil
}

// DeleteItem removes an item.
func (s *ItemService) DeleteItem(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

func validateName(name string) error {
	if l := len(name); l < 1 || l > 128 {
		return model.ErrInvalidName
	}
	return nil
}

func normalizePage(page, pageSize int32) (int32, int32) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > maxPageSize {
		pageSize = defaultPageSize
	}
	return page, pageSize
}

func toItemResult(it *model.Item) *ItemResult {
	return &ItemResult{
		ID:          it.ID,
		Name:        it.Name,
		Description: it.Description,
		CreatedAt:   it.CreatedAt.Unix(),
		UpdatedAt:   it.UpdatedAt.Unix(),
	}
}
`

const addServiceHandler = `package rpc

import (
	"context"
	"errors"
	"time"

	{{ .ProtoPkg }}v1 "{{ .Module }}/api/gen/go/{{ .ProtoPkg }}/v1"
	"{{ .Module }}/services/{{ .Service }}/internal/model"
	"{{ .Module }}/services/{{ .Service }}/internal/service"

	sferrors "github.com/svcforge/service-forge/core/errors"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// {{ .Type }}Handler implements the {{ .ProtoPkg }}v1.{{ .Type }}ServiceServer gRPC interface.
type {{ .Type }}Handler struct {
	{{ .ProtoPkg }}v1.Unimplemented{{ .Type }}ServiceServer
	itemSvc *service.ItemService
}

// New{{ .Type }}Handler creates a new {{ .Type }}Handler.
func New{{ .Type }}Handler(itemSvc *service.ItemService) *{{ .Type }}Handler {
	return &{{ .Type }}Handler{itemSvc: itemSvc}
}

// CreateItem creates a new item.
func (h *{{ .Type }}Handler) CreateItem(ctx context.Context, req *{{ .ProtoPkg }}v1.CreateItemRequest) (*{{ .ProtoPkg }}v1.CreateItemResponse, error) {
	item, err := h.itemSvc.CreateItem(ctx, service.CreateItemInput{
		Name:        req.GetName(),
		Description: req.GetDescription(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return &{{ .ProtoPkg }}v1.CreateItemResponse{Item: toProtoItem(item)}, nil
}

// GetItem returns a single item.
func (h *{{ .Type }}Handler) GetItem(ctx context.Context, req *{{ .ProtoPkg }}v1.GetItemRequest) (*{{ .ProtoPkg }}v1.GetItemResponse, error) {
	item, err := h.itemSvc.GetItem(ctx, req.GetId())
	if err != nil {
		return nil, toGRPCError(err)
	}
	return &{{ .ProtoPkg }}v1.GetItemResponse{Item: toProtoItem(item)}, nil
}

// ListItems returns a page of items.
func (h *{{ .Type }}Handler) ListItems(ctx context.Context, req *{{ .ProtoPkg }}v1.ListItemsRequest) (*{{ .ProtoPkg }}v1.ListItemsResponse, error) {
	result, err := h.itemSvc.ListItems(ctx, service.ListItemsInput{
		Page:     req.GetPage(),
		PageSize: req.GetPageSize(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}

	items := make([]*{{ .ProtoPkg }}v1.Item, len(result.Items))
	for i := range result.Items {
		items[i] = toProtoItem(&result.Items[i])
	}

	return &{{ .ProtoPkg }}v1.ListItemsResponse{
		Items:    items,
		Page:     result.Page,
		PageSize: result.PageSize,
		Total:    result.Total,
	}, nil
}

// UpdateItem updates an item.
func (h *{{ .Type }}Handler) UpdateItem(ctx context.Context, req *{{ .ProtoPkg }}v1.UpdateItemRequest) (*{{ .ProtoPkg }}v1.UpdateItemResponse, error) {
	item, err := h.itemSvc.UpdateItem(ctx, service.UpdateItemInput{
		ID:          req.GetId(),
		Name:        req.GetName(),
		Description: req.GetDescription(),
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	return &{{ .ProtoPkg }}v1.UpdateItemResponse{Item: toProtoItem(item)}, nil
}

// DeleteItem removes an item.
func (h *{{ .Type }}Handler) DeleteItem(ctx context.Context, req *{{ .ProtoPkg }}v1.DeleteItemRequest) (*{{ .ProtoPkg }}v1.DeleteItemResponse, error) {
	if err := h.itemSvc.DeleteItem(ctx, req.GetId()); err != nil {
		return nil, toGRPCError(err)
	}
	return &{{ .ProtoPkg }}v1.DeleteItemResponse{}, nil
}

// toProtoItem maps the service Result DTO to the proto message.
func toProtoItem(it *service.ItemResult) *{{ .ProtoPkg }}v1.Item {
	return &{{ .ProtoPkg }}v1.Item{
		Id:          it.ID,
		Name:        it.Name,
		Description: it.Description,
		CreatedAt:   timestamppb.New(time.Unix(it.CreatedAt, 0)),
		UpdatedAt:   timestamppb.New(time.Unix(it.UpdatedAt, 0)),
	}
}

// toGRPCError maps domain errors to framework AppErrors. The grpcserver's
// ErrorInterceptor turns these into the right gRPC status, and the gateway
// turns them into the right HTTP status.
func toGRPCError(err error) error {
	switch {
	case errors.Is(err, model.ErrItemNotFound):
		return sferrors.New(sferrors.CodeNotFound, err.Error())
	case errors.Is(err, model.ErrInvalidName):
		return sferrors.New(sferrors.CodeInvalidArgument, err.Error())
	default:
		return sferrors.New(sferrors.CodeInternal, err.Error())
	}
}
`

const addServiceReadme = `# {{ .Service }}

Generated by ` + "`svcforge add service`" + `. A complete vertical slice over an
in-memory store (no database, no auth) — runs as soon as the stubs are generated.

` + "```" + `
services/{{ .Service }}/internal/
  handler/rpc/   gRPC handlers + proto mapping
  service/       business logic + Input/Result DTOs
  repository/    in-memory store (swap for a real adapter)
  model/         domain model + errors
  {{ .GoPkg }}/  module wiring
` + "```" + `

## Next steps

1. Generate the gRPC stubs:

   ` + "```bash" + `
   buf generate
   ` + "```" + `

2. Run the service (defaults to the grpc port in config):

   ` + "```bash" + `
   go run ./services/{{ .Service }}/cmd
   ` + "```" + `

3. (Optional) Expose it through the gateway. In ` + "`gateway/cmd/main.go`" + `
   register a proxy invoker per rpc, e.g.:

   ` + "```go" + `
   gateway.MustRegisterProxyInvoker("/{{ .ProtoPkg }}.v1.{{ .Type }}Service/ListItems", gateway.NewUnaryProxy(
       func() *{{ .ProtoPkg }}v1.ListItemsRequest { return &{{ .ProtoPkg }}v1.ListItemsRequest{} },
       func(ctx context.Context, conn *grpc.ClientConn, req *{{ .ProtoPkg }}v1.ListItemsRequest) (*{{ .ProtoPkg }}v1.ListItemsResponse, error) {
           return {{ .ProtoPkg }}v1.New{{ .Type }}ServiceClient(conn).ListItems(ctx, req)
       },
   ))
   ` + "```" + `

   and add a route in ` + "`config/base.yaml`" + ` pointing at the service
   (` + "`target: 127.0.0.1:<port>`" + ` for local dev, or ` + "`service: {{ .Service }}`" + `
   with a shared registry).
`
