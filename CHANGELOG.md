# Service Forge changelog

## v0.1.0-preview.3 — November 26, 2026

### Protobuf route discovery

- Services using Consul automatically publish registered RPCs with
  `google.api.http` annotations. Gateways discover the published manifests
  and activate them after matching service instances become healthy. New
  services and RPCs can be deployed without editing gateway routing YAML.
- Protobuf annotations cover unary HTTP/JSON and bidirectional WebSocket
  mappings. RPCs without an HTTP mapping remain internal. Static routes still
  take precedence when the same request matches both routing modes.
- The gateway keeps the last valid snapshot when a candidate manifest is
  invalid or dynamic routes conflict. In shared environments, give each
  trusted service write access to its own Consul keys and the gateway read
  access to the route catalog and service health data.

### Authentication

- Protobuf-only routes inherit enabled authentication entries from
  `gateway.plugins`. The gateway accepts unauthenticated requests when
  no global authentication plugin is enabled. Enable `jwt` or `api_key` to
  protect automatically published routes.
- The `allow_anonymous` method option bypasses global authentication for that
  dynamic route. Logging, rate limiting and other global middleware still
  run. Matching static routes retain their configured authentication.
- The `forward_account_id` service option forwards a verified JWT
  `account_id` as `x-account-id` when global JWT is enabled. A protected
  endpoint using this option requires the claim.
- To keep existing automatically published routes protected during upgrade,
  explicitly configure the desired global authentication plugin. For JWT,
  configure its secret from the gateway environment, such as `JWT_SECRET`.
  Gateway-managed static routes and explicit legacy policy modes remain
  available.

### CLI and upgrade

- `svcforge new` includes the shared gateway `policy.proto`.
  `svcforge proto gen` supplies the schema to existing projects with the
  standard `api/proto` layout and preserves their Buf path exclusions.
  An incompatible existing schema is reported and preserved.
- Install the CLI with
  `go install github.com/svcforge/service-forge/cmd/svcforge@v0.1.0-preview.3`.
  Update applications with
  `go get github.com/svcforge/service-forge@v0.1.0-preview.3` and `go mod tidy`.
- See [Publish Gateway Routes From Services](README.md#publish-gateway-routes-from-services)
  for a deployment walkthrough and examples.

### Verification

- The full framework suite passed with the Go race detector, including
  CLI scaffolding, generated-project end-to-end tests, external adapters,
  live route updates, global authentication and anonymous route handling.
- An isolated application deployment verified 11 HTTP endpoints and
  authenticated WebSocket communication through Consul-discovered routes.

## v0.1.0-preview.2 — November 26, 2026

- gRPC services can publish annotated HTTP routes and protobuf descriptors to
  Consul. Enable `grpc.publish_routes` and set `grpc.register_ip` when the
  bind address differs from the reachable service address.
- Gateways can allow specific services and path prefixes, poll for changes,
  and atomically activate a revision once matching instances are healthy.
  An invalid manifest leaves the previous routing snapshot active.
- Dynamic forwarding supports unary HTTP/JSON and bidirectional WebSocket
  routes. Existing static routes take precedence. Literal paths take priority
  over parameter templates.
- Dynamic route policies support scoped plugins, explicit public RPCs and JWT
  claim forwarding. Global gateway plugins also apply to dynamic endpoints.
- See [Publish Gateway Routes From Services](README.md#publish-gateway-routes-from-services)
  for configuration and the supported annotation subset.

## v0.1.0-preview.1 — November 26, 2026

This preview improves startup failure reporting, runtime cleanup and gRPC
service discovery. It also adds a repeatable test suite for generated projects
and external adapters.

### Runtime and gateway

- Report the module name and lifecycle phase when initialization or startup
  fails. During `Application.Run`, stop successfully initialized modules in
  reverse order, including when initialization or startup fails partway through.
  Run cleanup with a separate 15-second context and return shutdown errors
  together with any startup error.
- Bind the gateway listener before returning from startup. A port conflict
  now fails startup immediately. Close static connection pools and cached
  service-discovery connections during shutdown, and close partially initialized
  route connections when gateway initialization fails.
- Close the gRPC listener if registration fails, report the transport as healthy
  while it is serving, and log unexpected serve failures.
- Reuse a single cached gRPC connection for concurrent dials to the same target.
  Rotate through unique discovered addresses and format IPv6 endpoints correctly.

### Scaffolding and upgrade

- Generated modules declare Go 1.25.0, matching the framework baseline.
- The quickstart generates protobuf code before resolving Go dependencies. The
  Todo endpoint verifies the complete HTTP-to-gRPC path.
- Install the preview CLI with
  `go install github.com/svcforge/service-forge/cmd/svcforge@v0.1.0-preview.1`.
  Projects using published modules can update with
  `go get github.com/svcforge/service-forge@v0.1.0-preview.1` followed by
  `go mod tidy`.
- Custom modules should release partial resources inside a failing `Init`.
  After a successful `Init`, `Stop` must be safe even when `Start` was
  skipped or failed. Callers of `Application.Stop` and `Application.Run`
  should handle returned shutdown errors.
- If a generated project uses a local `replace`, adjust it when moving the
  framework checkout. Sibling checkouts can use `../service-forge`.

### Verification

- The generated-project test exercises the CLI, protobuf generation with local
  plugins, compilation, Todo CRUD through the gateway and graceful shutdown.
- Lifecycle regression tests cover rollback, port conflicts, connection
  cleanup, concurrent dialing and gRPC registration failures.
- Integration tests exercise PostgreSQL transactions, Redis cache operations,
  RabbitMQ delivery, Consul registration and OpenTelemetry OTLP export.
- The full suite passed with the Go race detector. It recorded 99 passing tests
  and subtests, no skips, and 61.0% statement coverage.

### Benchmark

On an Apple M5 Pro, the no-plugin HTTP-to-gRPC Health example reached
113,833 requests/second at 1.97 ms p99. With five core plugins and access logs
written to a file, it reached 102,193 requests/second at 2.96 ms p99.
See [Benchmarks](README.md#benchmarks) for the grpc-gateway comparison, test
conditions, workload details and reproduction commands.
