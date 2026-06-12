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

## Package Map

- `core/app`: lifecycle, module composition, graceful shutdown.
- `core/config`: framework config loading without infrastructure coupling.
- `core/errors`: unified error codes and HTTP/gRPC mapping.
- `core/module`: module lifecycle and provider catalog.
- `ports/*`: stable dependency interfaces for business services.
- `adapters/*`: Redis, Postgres, RabbitMQ, Consul, OpenTelemetry, memory, noop.
- `transport/*`: gRPC server/client and REST Gateway.
- `cmd/svcforge`: project scaffolding CLI.
