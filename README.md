# Service Forge

Service Forge is a Go microservice framework and scaffolding tool built around
**Core + Ports + Adapters + Runtime Modules**.

The default architecture is:

```text
Client -> REST/JSON Gateway -> gRPC Services -> Ports -> Adapters
```

- Gateway exposes REST/JSON.
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
replace github.com/svcforge/service-forge => ..
```

If you create a project somewhere else before Service Forge is published, pass
the local framework path explicitly:

```bash
svcforge new demo --replace /path/to/service-forge
```

After Service Forge is published and tagged, remove the local `replace` line and
use a real version:

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
