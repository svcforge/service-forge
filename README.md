# Service Forge

Service Forge is a Go microservice framework and scaffolding tool built around
Core + Ports + Adapters + Runtime Modules.

## Architecture

- Gateway exposes REST/JSON.
- Business services expose gRPC only.
- Business code depends on `ports/*` interfaces.
- Redis, SQL, MQ, registry, tracing, and storage are replaceable adapters.
- Runtime components are mounted through the `module.Module` lifecycle.

```text
Client -> REST/JSON Gateway -> gRPC Services -> Ports -> Adapters
```

## Packages

- `core/app`: lifecycle, module composition, graceful shutdown.
- `core/config`: framework config loading without infrastructure coupling.
- `core/errors`: unified error codes and HTTP/gRPC mapping.
- `ports/*`: stable dependency interfaces for business services.
- `adapters/*`: Redis, Postgres, RabbitMQ, Consul, OpenTelemetry, memory, noop.
- `transport/*`: gRPC server/client and REST Gateway.
- `cmd/svcforge`: project scaffolding CLI.

## Quick Start

```bash
go run ./cmd/svcforge new demo
cd demo
go mod tidy
go run ./gateway/cmd
```

The generated project starts with local-friendly adapters:

- `store: noop`
- `cache: memory`
- `eventbus: memory`
- `registry: memory`
- `tracing: noop`

Default logs are intentionally quiet: module lifecycle logs and the Fiber
startup banner are off. Enable module-level startup details only when debugging:

```yaml
log:
  format: text
  level: debug
  module_lifecycle: true
```

Use real infrastructure when you want it:

```bash
svcforge new demo --db postgres --cache redis --mq rabbitmq --registry consul --tracing otel
```

## Configured Components

Projects choose runtime components in `config/base.yaml`:

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

Set `enabled: false` to keep a component in config without starting it:

```yaml
runtime:
  components:
    - name: eventbus
      provider: rabbitmq
      enabled: false
```

Code builds modules from the catalog:

```go
mods, err := adapters.DefaultCatalog().Build(bundle.Core.Runtime.Components)
mods = append(mods, gatewayModule)
application := app.New(bundle.Core, app.WithModules(mods...))
```

Custom components register their own provider:

```go
catalog := adapters.DefaultCatalog()
catalog.Register("eventbus", "nats", func() module.Module {
    return natsadapter.NewModule()
})
mods, err := catalog.Build(bundle.Core.Runtime.Components)
```
