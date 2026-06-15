# Service Forge

Service Forge 是一个面向 Go 微服务项目的脚手架与运行时框架，目标是把
REST/JSON 网关、gRPC 服务、配置化运行组件和可替换基础设施适配器整合成
一套清晰、可扩展、适合业务落地的工程结构。

它默认采用 `Client -> REST/JSON Gateway -> gRPC Services -> Ports -> Adapters`
的分层模型：对外由网关提供 HTTP API，对内业务服务保持 gRPC 边界，业务逻辑
只依赖稳定的 `ports/*` 接口，Redis、PostgreSQL、RabbitMQ、Consul、
OpenTelemetry 等基础设施通过 adapter 按配置装配。框架内置 CLI，可快速创建
项目、添加服务、生成 protobuf 代码，并支持通过配置声明 API 到 gRPC 的静态
转发路由。

Service Forge 适合用于从零搭建标准化 Go 微服务项目，也适合在团队内部沉淀
统一的服务模板、网关规范、组件选型和工程约束。它强调生成代码与显式配置：
网关转发走静态 protobuf Go 代码，不依赖运行时反射或动态描述符，从而在保持
开发体验的同时兼顾性能与可维护性。

Service Forge is a Go microservice framework and scaffolding tool built around
**Core + Ports + Adapters + Runtime Modules**.

The default architecture is:

```text
Client -> REST/JSON Gateway -> gRPC Services -> Ports -> Adapters
```

- Gateway exposes REST/JSON, and can proxy unary, bidirectional-stream
  (WebSocket), and server-stream (SSE) gRPC routes with optional per-route
  retry, circuit breaking, and load balancing.
- Business services expose gRPC only.
- Business code depends on `ports/*` interfaces.
- Redis, SQL, MQ, registry, tracing, and storage are replaceable adapters.
- Runtime components are selected from config and mounted as modules.

## Install The CLI

After the repository is published:

```bash
go install github.com/svcforge/service-forge/cmd/svcforge@latest
```

While developing Service Forge locally, run the CLI from this repository:

```bash
go run ./cmd/svcforge --help
```

Or build a local binary:

```bash
go build -o ./bin/svcforge ./cmd/svcforge
./bin/svcforge --help
```

## Create A Project

From inside the `service-forge` repository during local development:

```bash
go run ./cmd/svcforge new demo
cd demo
go mod tidy
```

The generated `go.mod` will include a local replace automatically:

```go
require github.com/svcforge/service-forge v0.0.0

replace github.com/svcforge/service-forge => ..
```

If you create a project somewhere else before Service Forge is published, pass
the local framework path explicitly:

```bash
svcforge new demo --replace /path/to/service-forge
```

When creating a project without a local `replace`, the generated `go.mod` omits
the framework requirement. `go mod tidy` resolves
`github.com/svcforge/service-forge` at the latest available version from the
imports generated in the project.

To upgrade an existing generated project later:

```bash
go get github.com/svcforge/service-forge@latest
```

## Run The Generated Project

Start the REST/JSON gateway:

```bash
go run ./gateway/cmd
```

Start the example gRPC service in another terminal:

```bash
go run ./services/example-service/cmd
```

Try the generated gateway route:

```bash
curl http://localhost:8080/api/v1/ping
```

Expected response:

```json
{"code":"OK","message":"ok","data":{"message":"pong"},"timestamp":...}
```

## Add A Service

Inside a generated project:

```bash
svcforge add service order-service
```

This creates:

```text
api/proto/order-service/v1/order-service.proto
services/order-service/cmd/main.go
services/order-service/internal/README.md
```

The intended service layout is:

```text
services/<service>/
├── cmd/
└── internal/
    ├── handler/rpc      # gRPC server implementation
    ├── service          # business logic, depends on ports
    ├── repository       # persistence details
    ├── model
    └── setup            # adapter wiring
```

Business services should expose gRPC only. REST/JSON routes belong in the
gateway.

## Configure API To gRPC Routes

Gateway can mount REST/JSON routes from config and proxy them to gRPC methods.
The configured `rpc` is resolved through a static proxy invoker registered in
the gateway binary. This keeps the request path on generated protobuf Go code
instead of gRPC server reflection or runtime descriptors.

```yaml
gateway:
  routes:
    - name: example-ping
      method: POST
      path: /api/v1/ping
      target: 127.0.0.1:9000
      rpc: /example.v1.ExampleService/Ping
      pool_size: 1
      timeout: 3s
```

Use `target` for a direct `host:port` backend. Use `service` when the gateway
should resolve a backend through a shared registry such as Consul:

```yaml
gateway:
  routes:
    - method: POST
      path: /api/v1/ping
      service: example-service
      rpc: /example.v1.ExampleService/Ping
```

Request JSON body, query parameters, and path parameters are merged into the
protobuf request by field name. For example, `/api/v1/users/:id` can populate an
`id` field on the gRPC request message. The response is encoded as the standard
Service Forge JSON response envelope.

Each configured `rpc` must have a static proxy invoker registered by the gateway
binary. In generated projects this registration should live next to the gateway
entrypoint after protobuf code generation:

```go
gateway.MustRegisterProxyInvoker("/example.v1.ExampleService/Ping", gateway.NewUnaryProxy(
	func() *examplev1.PingRequest {
		return &examplev1.PingRequest{}
	},
	func(ctx context.Context, conn *grpc.ClientConn, req *examplev1.PingRequest) (*examplev1.PingResponse, error) {
		return examplev1.NewExampleServiceClient(conn).Ping(ctx, req)
	},
))
```

For performance-sensitive routes, generated code should use `NewUnaryCodecProxy`
and provide static request binding plus response JSON writing:

```go
gateway.MustRegisterProxyInvoker("/example.v1.ExampleService/Ping", gateway.NewUnaryCodecProxy(
	func() *examplev1.PingRequest {
		return &examplev1.PingRequest{}
	},
	func() *examplev1.PingResponse {
		return &examplev1.PingResponse{}
	},
	func(c *fiber.Ctx, req *examplev1.PingRequest) error {
		// Generated code should bind body/query/path fields directly.
		return nil
	},
	func(ctx context.Context, conn *grpc.ClientConn, req *examplev1.PingRequest, resp *examplev1.PingResponse) error {
		return conn.Invoke(ctx, "/example.v1.ExampleService/Ping", req, resp)
	},
	func(c *fiber.Ctx, resp *examplev1.PingResponse) error {
		// Generated code should write response JSON directly.
		return gateway.WriteSuccessJSON(c, []byte(`{"message":"pong"}`))
	},
))
```

The gateway does not use gRPC server reflection or dynamic protobuf descriptors
for configured proxy routes. Config selects the HTTP route and backend; static
generated Go code performs the protobuf request binding and gRPC client call.

`pool_size` controls the number of gRPC client connections created for a direct
`target`. Keep it at `1` unless profiling shows a single HTTP/2 connection is a
contention point for your workload.

For local multi-process development, `registry: memory` is process-local. Use
`target` directly or switch to a shared registry provider before relying on
`service` discovery across processes.

## Route Resilience

Configured proxy routes support optional retry, circuit breaking, and load
balancing. All three are off by default and add no overhead unless enabled per
route. Failures are classified by framework error code, so client errors such as
`INVALID_ARGUMENT` are never retried and never count against the breaker.

### Retry

Retries a failed attempt on a freshly selected pooled connection, routing around
a bad endpoint:

```yaml
gateway:
  routes:
    - name: example-ping
      path: /api/v1/ping
      target: 127.0.0.1:9000
      rpc: /example.v1.ExampleService/Ping
      retry:
        max_attempts: 3      # total tries including the first; <= 1 disables
        per_try_timeout: 1s  # per-attempt deadline; falls back to route timeout
        backoff: 50ms        # fixed delay before each retry; 0 fires immediately
        retry_on:            # framework codes safe to retry
          - UNAVAILABLE
          - DEADLINE_EXCEEDED
```

`retry_on` defaults to `UNAVAILABLE` and `DEADLINE_EXCEEDED` when omitted. Only
enable retries for idempotent RPCs: a transient `UNAVAILABLE` may still have been
applied by the backend.

### Circuit Breaker

Trips when the failure ratio over a rolling window exceeds the threshold,
short-circuiting further calls with `UNAVAILABLE` until a probe succeeds. The
breaker wraps retries, so one fully-retried-then-failed call counts as a single
breaker failure:

```yaml
gateway:
  routes:
    - name: example-ping
      path: /api/v1/ping
      target: 127.0.0.1:9000
      rpc: /example.v1.ExampleService/Ping
      circuit_breaker:
        min_requests: 20        # minimum calls in the window before it can trip
        failure_ratio: 0.5      # failure fraction (0,1] that trips the breaker
        window: 10s             # rolling window for counting calls while closed
        open_timeout: 5s        # time open before allowing a half-open probe
        half_open_max_calls: 1  # concurrent probes allowed while half-open
```

Only server-side and transport failures (`UNAVAILABLE`, `DEADLINE_EXCEEDED`,
`INTERNAL`) count toward tripping; client errors never open the breaker.

### Load Balancing

When `pool_size` is greater than 1, `load_balance` selects how requests are
spread across the pooled connections:

```yaml
gateway:
  routes:
    - name: example-ping
      path: /api/v1/ping
      target: 127.0.0.1:9000
      rpc: /example.v1.ExampleService/Ping
      pool_size: 4
      load_balance: least_conn  # round_robin (default) | least_conn | random
```

`round_robin` cycles through connections in order. `least_conn` routes to the
connection with the fewest in-flight calls, which is preferable when request
latencies are uneven. `random` picks uniformly. Unknown or empty values fall
back to `round_robin`.

## WebSocket To gRPC Streaming

A route can bridge a WebSocket connection to a bidirectional gRPC stream. Set
`stream: bidi` and register a stream proxy that supplies the per-frame message
types:

```yaml
gateway:
  routes:
    - name: chat
      path: /ws/chat
      target: 127.0.0.1:9000
      rpc: /example.v1.Chat/Stream
      stream: bidi
```

```go
gateway.MustRegisterBidiStreamProxy("/example.v1.Chat/Stream",
	func() proto.Message { return &chatv1.ServerMessage{} }, // server -> client frames
	func() proto.Message { return &chatv1.ClientMessage{} }, // client -> server frames
)
```

The route is exposed as a WebSocket endpoint via HTTP upgrade; non-upgrade
requests receive `426 Upgrade Required`. Each WebSocket text frame is decoded
from JSON into the client message and sent on the gRPC stream; each gRPC message
is encoded to JSON and written back as a text frame. Two goroutines pump the
directions independently and unblock each other on close.

- Retries and per-call timeouts do not apply to streams. A configured circuit
  breaker only guards stream establishment, not frames in flight.
- One pooled connection is selected per stream and held for its lifetime.
- Route-level plugins (auth, etc.) run during the HTTP handshake before upgrade.
- Closing the WebSocket half-closes the gRPC stream; a server-side end of stream
  (`io.EOF`) closes the WebSocket.

## Server Streaming As SSE

For one-way server streaming, set `stream: sse`. The route takes a single
request (bound from body/query/path like a unary route) and relays each gRPC
response message to the client as a Server-Sent Event:

```yaml
gateway:
  routes:
    - name: feed
      path: /sse/feed
      target: 127.0.0.1:9000
      rpc: /example.v1.Feed/Stream
      stream: sse
```

```go
gateway.MustRegisterServerStreamProxy("/example.v1.Feed/Stream",
	func() proto.Message { return &feedv1.FeedRequest{} }, // single request
	func() proto.Message { return &feedv1.FeedEvent{} },   // each streamed event
)
```

The response uses `Content-Type: text/event-stream`; each gRPC message is written
as one `data: <json>` event. Streaming routes default to HTTP `GET` (so browser
`EventSource` works) unless `method` is set. The gRPC stream ending (`io.EOF`)
closes the response; a client disconnect is detected on the next frame's flush.

The same constraints as the WebSocket case apply: retries and per-call timeouts
do not apply, a configured breaker only guards stream establishment, and
route-level plugins run before the stream starts.

## Generate Protobuf Code

Service Forge creates `buf.yaml` and `buf.gen.yaml`.

Install `buf`, then run:

```bash
svcforge proto gen
```

Generated code is written to:

```text
api/gen/go
```

## Choose Runtime Components

Generated projects start with local-friendly adapters:

- `store: noop`
- `cache: memory`
- `eventbus: memory`
- `registry: memory`
- `tracing: noop`

Switch components in `config/base.yaml`:

```yaml
runtime:
  components:
    - name: store
      provider: postgres
    - name: cache
      provider: redis
    - name: eventbus
      provider: rabbitmq
    - name: registry
      provider: consul
    - name: tracing
      provider: otel
```

You can generate a project with real infrastructure selected from the start:

```bash
svcforge new demo \
  --db postgres \
  --cache redis \
  --mq rabbitmq \
  --registry consul \
  --tracing otel
```

Keep a component in config without starting it:

```yaml
runtime:
  components:
    - name: eventbus
      provider: rabbitmq
      enabled: false
```

## Register A Custom Component

Use the default catalog and register your own provider before building modules:

```go
catalog := adapters.DefaultCatalog()
catalog.Register("eventbus", "nats", func() module.Module {
    return natsadapter.NewModule()
})

mods, err := catalog.Build(bundle.Core.Runtime.Components)
if err != nil {
    log.Fatal(err)
}
mods = append(mods, gatewayModule)

application := app.New(bundle.Core, app.WithModules(mods...))
```

Then select it in config:

```yaml
runtime:
  components:
    - name: eventbus
      provider: nats
```

## Gateway Plugins

The gateway has a plugin chain. All plugins, including built-ins, are off by
default: only plugins listed under `gateway.plugins` run, in config order.

```yaml
gateway:
  plugins:
    - name: recovery
    - name: access_log
      config:
        skip_paths: ["/health"]
    - name: cors
      config:
        allow_origins: ["https://app.example.com"]
    - name: rate_limit
      config:
        max: 100
        window: 1m
    - name: api_key
      config:
        keys: ["${API_KEY}"]
    - name: jwt
      config:
        secret: "${JWT_SECRET}"
        issuer: my-app
    - name: metrics
```

Built-in plugins:

- `recovery`: converts handler panics into the standard `INTERNAL` envelope.
- `access_log`: one structured log line per request (`skip_paths` supported).
- `cors`: cross-origin support (`allow_origins`, `allow_methods`,
  `allow_headers`, `expose_headers`, `allow_credentials`, `max_age`).
- `rate_limit`: per-client-IP in-memory sliding window (`max`, `window`).
- `api_key`: key auth via header or query (`keys`, `header`, `query`).
- `jwt`: bearer token validation with HS256/384/512 (`secret`, `algorithm`,
  `issuer`, `audience`); verified claims land in `c.Locals("jwt_claims")`.
- `metrics`: Prometheus request counter and latency histogram exposed on
  `path` (default `/metrics`).

Notes:

- Set `enabled: false` to keep a plugin entry in config without running it.
- `/health` and plugin-mounted endpoints such as `/metrics` bypass the global
  chain.
- Routes under `gateway.routes` accept a route-level `plugins` list that runs
  after the global chain, only for that route.

Projects register custom plugins before the gateway module starts, then
select them in config like any built-in:

```go
import "github.com/svcforge/service-forge/transport/gateway/plugin"

plugin.MustRegister("tenant-header", func(ctx plugin.BuildContext) (plugin.Plugin, error) {
	tenant, err := ctx.Settings.String("tenant", "")
	if err != nil {
		return plugin.Plugin{}, err
	}
	return plugin.Plugin{Handler: func(c *fiber.Ctx) error {
		c.Set("X-Tenant", tenant)
		return c.Next()
	}}, nil
})
```

```yaml
gateway:
  plugins:
    - name: tenant-header
      config:
        tenant: acme
```

## Logging

Default logs are intentionally quiet. Module lifecycle logs and the Fiber
startup banner are off.

Every framework log includes source fields:

```text
app=demo service=demo source=transport component=gateway
```

Enable module startup details when debugging:

```yaml
log:
  format: text
  level: debug
  module_lifecycle: true
```

Use JSON logs in production:

```yaml
log:
  format: json
  level: info
```

## Project Health Check

Run:

```bash
svcforge doctor
```

The command checks that the current project has the basic Service Forge layout:

- `go.mod`
- `config/`
- `api/proto/`

## Benchmarks

Run standard Go benchmarks with allocation reporting:

```bash
go test ./transport/gateway -run '^$' -bench . -benchmem
```

The gateway benchmarks cover configured REST/JSON to gRPC proxy calls in
single-threaded and parallel hot-path scenarios, plus the plugin chain
(`BenchmarkProxyPluginChainParallel`) with the chain off, the core production
set, and JWT auth on top.

### End-To-End Results

Measured 2026-06-12 on Apple M5 Pro (18 cores), Go 1.26.2: wrk -> gateway ->
gRPC backend over loopback, all on one machine, identical backend for every
gateway. `wrk -t8 -c128 -d10s --latency`, mean of 3 runs, run-to-run variance
under 4%. The grpc-gateway comparison handler mirrors protoc-gen-grpc-gateway
generated code. Reproduce with the programs under `examples/bench/`.

| Gateway | Requests/sec | p50 | p99 | Response size |
|---|---|---|---|---|
| Service Forge, no plugins | 97,016 | 1.19ms | 2.27ms | 254 B |
| Service Forge, 5 plugins on | 89,545 (-7.7%) | 1.48ms | 3.40ms | 343 B |
| grpc-gateway v2 | 81,779 | 1.50ms | 2.69ms | 174 B |

Notes:

- Service Forge proxies the same route 18.6% faster than grpc-gateway while
  returning a larger response (the standard envelope adds ~80 bytes).
- "5 plugins on" is recovery, access_log, rate_limit, api_key and metrics —
  with access_log writing a real structured log line per request (2.7M lines
  over the run) and rate_limit attaching X-RateLimit-* response headers. The
  full chain costs 7.7% throughput and still outruns plain grpc-gateway.
- Numbers are REST/JSON to gRPC transcoding, Service Forge's target workload.
  They are not comparable to plain HTTP reverse-proxy benchmarks
  (Nginx/Envoy), and cross-hardware comparisons with published Kong/APISIX
  figures are indicative only.

Reproduce:

```bash
go run ./examples/bench/backend &
go run ./examples/bench/svcforgegw &
go run ./examples/bench/svcforgegw -port 8082 -plugins core &
go run ./examples/bench/grpcgatewaygw &
wrk -t8 -c128 -d10s --latency http://127.0.0.1:8080/api/health
wrk -t8 -c128 -d10s --latency http://127.0.0.1:8081/api/health
wrk -t8 -c128 -d10s --latency -H "X-API-Key: bench-key" http://127.0.0.1:8082/api/health
```

## Package Map

- `core/app`: lifecycle, module composition, graceful shutdown.
- `core/config`: framework config loading without infrastructure coupling.
- `core/errors`: unified error codes and HTTP/gRPC mapping.
- `core/module`: module lifecycle and provider catalog.
- `ports/*`: stable dependency interfaces for business services.
- `adapters/*`: Redis, Postgres, RabbitMQ, Consul, OpenTelemetry, memory, noop.
- `transport/*`: gRPC server/client and REST Gateway.
- `cmd/svcforge`: project scaffolding CLI.
