# GoGRPCBridge

> **Native gRPC in the browser.** Type-safe streaming from WebAssembly clients to your Go backend.

Modern web development shouldn't force you to choose between developer experience and browser compatibility. GoGRPCBridge brings the full power of gRPC over WebSocket transport to WebAssembly clients and standard Go clients.

[![Go Reference](https://pkg.go.dev/badge/github.com/monstercameron/GoGRPCBridge.svg)](https://pkg.go.dev/github.com/monstercameron/GoGRPCBridge)
[![Go Report Card](https://goreportcard.com/badge/github.com/monstercameron/GoGRPCBridge)](https://goreportcard.com/report/github.com/monstercameron/GoGRPCBridge)
[![Build](https://github.com/monstercameron/GoGRPCBridge/workflows/Build/badge.svg)](https://github.com/monstercameron/GoGRPCBridge/actions/workflows/build.yml)
[![Test](https://github.com/monstercameron/GoGRPCBridge/workflows/Test/badge.svg)](https://github.com/monstercameron/GoGRPCBridge/actions/workflows/test.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go)](https://go.dev/)

---

## What This Solves

Browsers do not expose raw TCP/HTTP2 sockets for native gRPC clients. GoGRPCBridge provides a WebSocket transport bridge so you can keep gRPC contracts, streaming semantics, and generated clients.

## Key Features

- Bidirectional streaming support from browser WASM clients
- Typed request/response contracts with existing protobuf services
- Minimal server integration for Go gRPC backends
- Single public package (`pkg/grpctunnel`) for most use cases

---

## Documentation Guide

| Goal | Start Here |
| --- | --- |
| Browse docs by topic | [DOCS_INDEX.md](./DOCS_INDEX.md) |
| Get running quickly | [Quick Start](#quick-start) |
| Use the typed public API | [Core API](#core-api-recommended) |
| Integrate with an existing HTTP mux | [Server API](#server-api) |
| Understand WASM behavior and TLS | [WASM Notes](#wasm-notes) |
| Migrate from legacy wrappers | [Migration Guide](./MIGRATION.md) |
| Debug setup and runtime failures | [TROUBLESHOOTING.md](./TROUBLESHOOTING.md) |
| Review transport threat model | [THREAT_MODEL.md](./THREAT_MODEL.md) |
| Define auth propagation boundaries | [AUTH_PROPAGATION_BOUNDARIES.md](./AUTH_PROPAGATION_BOUNDARIES.md) |
| Prepare secure release sign-off | [SECURITY_RELEASE_CHECKLIST.md](./SECURITY_RELEASE_CHECKLIST.md) |
| Understand compatibility guarantees | [API_COMPATIBILITY.md](./API_COMPATIBILITY.md) |
| Review API compatibility baseline | [api_compatibility_baseline.json](../../api_compatibility_baseline.json) |
| Run tests and benchmarks | [Development Runner](#development-runner) |
| Understand release quality gates | [QUALITY_GATES.md](./QUALITY_GATES.md) |
| Run release sign-off checklist | [RELEASE_CHECKLIST.md](./RELEASE_CHECKLIST.md) |
| Review measured optimization evidence | [PERFORMANCE_OPTIMIZATION_NOTES.md](./PERFORMANCE_OPTIMIZATION_NOTES.md) |
| Fill release performance notes | [RELEASE_PERFORMANCE_TEMPLATE.md](./RELEASE_PERFORMANCE_TEMPLATE.md) |
| Review release changelog | [CHANGELOG.md](./CHANGELOG.md) |
| Run rollback/hotfix process | [ROLLBACK_AND_HOTFIX.md](./ROLLBACK_AND_HOTFIX.md) |
| Follow deployment runbook | [OPERATIONS_RUNBOOK.md](./OPERATIONS_RUNBOOK.md) |
| Implement observability contract | [OBSERVABILITY_CONTRACT.md](./OBSERVABILITY_CONTRACT.md) |
| Wire dashboard and alert templates | [observability/](../observability/) |
| Review production SLO policy | [SLO_POLICY.md](./SLO_POLICY.md) |
| Execute canary and smoke rollout | [CANARY_AND_SMOKE.md](./CANARY_AND_SMOKE.md) |
| Use tunnel state diagnostics | [TUNNEL_STATE_DIAGNOSTICS.md](./TUNNEL_STATE_DIAGNOSTICS.md) |
| Follow community process | [COMMUNITY_WORKFLOW.md](./COMMUNITY_WORKFLOW.md) |
| Apply triage SLA policy | [TRIAGE_SLA.md](./TRIAGE_SLA.md) |
| Follow maintenance schedule | [MAINTENANCE_SCHEDULE.md](./MAINTENANCE_SCHEDULE.md) |
| Apply docs freshness policy | [DOCS_FRESHNESS_POLICY.md](./DOCS_FRESHNESS_POLICY.md) |
| Track adoption and friction metrics | [ADOPTION_METRICS.md](./ADOPTION_METRICS.md) |
| Run incident-feedback backlog loop | [BACKLOG_FEEDBACK_LOOP.md](./BACKLOG_FEEDBACK_LOOP.md) |
| Track 10/10 maturity plan | [ROADMAP_TO_10S.md](./ROADMAP_TO_10S.md) |

---

## Install

```bash
go get github.com/monstercameron/GoGRPCBridge
```

Requirements:

- Go 1.24+
- gRPC service definitions generated with `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc`

If module resolution fails during `go get`, see [TROUBLESHOOTING.md](./TROUBLESHOOTING.md).

---

## Quick Start

### 1. Start the bridge server

```go
package main

import (
    "log"
    "net/http"

    "github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
    "google.golang.org/grpc"
)

func main() {
    parseGrpcServer := grpc.NewServer()
    // Register your generated service servers here.
    // examplepb.RegisterGreeterServer(parseGrpcServer, &greeterServer{})

    parseMux := http.NewServeMux()
    if parseHandleError := grpctunnel.HandleBridgeMux(parseMux, "/grpc", parseGrpcServer, grpctunnel.BridgeConfig{}); parseHandleError != nil {
        log.Fatal(parseHandleError)
    }

    log.Fatal(http.ListenAndServe(":8080", parseMux))
}
```

### 2. Connect from WASM client

```go
//go:build js && wasm

package main

import (
    "context"
    "log"

    examplepb "github.com/your-org/your-project/gen/example"
    "github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
)

func main() {
    parseConnection, parseConnectionError := grpctunnel.BuildTunnelConn(context.Background(), grpctunnel.TunnelConfig{
        Target:      "/grpc", // same-origin path
        GRPCOptions: grpctunnel.ApplyTunnelInsecureCredentials(nil),
    })
    if parseConnectionError != nil {
        log.Fatal(parseConnectionError)
    }
    defer parseConnection.Close()

    parseClient := examplepb.NewGreeterClient(parseConnection)
    parseResponse, parseCallError := parseClient.SayHello(
        context.Background(),
        &examplepb.HelloRequest{Name: "web"},
    )
    if parseCallError != nil {
        log.Fatal(parseCallError)
    }

    log.Printf("RPC response: %s", parseResponse.GetMessage())
}
```

### 3. Build WASM

```bash
GOOS=js GOARCH=wasm go build -o app.wasm ./cmd/client
```

---

## Core API (Recommended)

Use `pkg/grpctunnel` for all new integration work.

### Client API

Primary function:

- `BuildTunnelConn(ctx, TunnelConfig) (*grpc.ClientConn, error)`

Config:

- `Target string`
- `TLSConfig *tls.Config` (non-WASM)
- `ShouldUseTLS bool` (non-WASM URL inference)
- `GRPCOptions []grpc.DialOption`

Helpers:

- `ApplyTunnelInsecureCredentials(opts []grpc.DialOption) []grpc.DialOption`
- `GetTunnelConfigError(cfg TunnelConfig) error`
- `ParseTunnelTargetURL(target string, shouldUseTLS bool) (string, error)`

### Server API

Primary functions:

- `BuildBridgeHandler(grpcServer, BridgeConfig) (http.Handler, error)`
- `HandleBridgeMux(mux, path, grpcServer, BridgeConfig) error`

Config:

- `CheckOrigin func(*http.Request) bool`
- `ReadBufferSize int`
- `WriteBufferSize int`
- `OnConnect func(*http.Request)`
- `OnDisconnect func(*http.Request)`

Helper:

- `GetBridgeConfigError(cfg BridgeConfig) error`

---

## WASM Notes

- In WASM builds, browser networking controls TLS.
- `TunnelConfig.TLSConfig` and `TunnelConfig.ShouldUseTLS` are rejected in WASM typed flows.
- Prefer `Target: ""` (same host) or `Target: "/grpc"` (same-origin path).

---

## Legacy API (Still Supported)

The following remain available for compatibility:

- `Dial` / `DialContext`
- `Wrap`
- `Serve`
- `ListenAndServe`

For new code, prefer typed APIs listed above. Migration mapping is documented in [MIGRATION.md](./MIGRATION.md).

---

## Security Notes

- Enforce strict `CheckOrigin` allow-lists in production (`Origin` exact-match or controlled suffix rules).
- Use HTTPS/WSS for all public bridge traffic; avoid exposing plaintext h2c paths on untrusted networks.
- For `pkg/bridge` backend hops, enable `bridge.Config{ShouldRequireLoopbackBackend: true}` to reject non-loopback plaintext backend targets at startup.
- Keep HTTP/WebSocket timeout controls enabled (`ReadTimeout`, `WriteTimeout`, `IdleTimeout`, ping/idle settings).
- Keep websocket read-size limits enabled by default unless a stricter upstream boundary is guaranteed.
- Enable abuse controls for public endpoints: `MaxActiveConnections`, `MaxConnectionsPerClient`, and `MaxUpgradesPerClientPerMinute`.
- Restrict tooling endpoints (reflection/pprof) to loopback or trusted internal networks only.
- Keep authentication and authorization at your application/service layer.

---

## Development Runner

Development and validation workflow is provided through a Go runner at `tools/runner.go`.

Run from `third_party/GoGRPCBridge`:

```bash
go run ./tools/runner.go help
```

Commands:

| Command | What it does |
| --- | --- |
| `test` | `go test ./pkg/... -v -race -coverprofile=coverage.txt` |
| `test-short` | `go test ./pkg/... -short` |
| `fmt` | Runs `gofmt -w -s .` then `goimports -w .` |
| `lint` | Runs `golangci-lint` with `.golangci.yml` |
| `lint-fix` | Runs `golangci-lint --fix` |
| `check` | Runs `fmt`, `lint`, and `test-short` |
| `pre-commit` | Runs `check` and prints status |
| `install-hooks` | Marks `.git/hooks/pre-commit` executable on non-Windows systems |
| `fuzz` | Runs all bridge fuzz tests for 60 seconds each |
| `fuzz-quick` | Runs all bridge fuzz tests for 5 seconds each |
| `e2e` | Runs `go test ./e2e/... -v -timeout 5m` |
| `build` | Builds `direct-bridge`, `grpc-server`, and WASM client example |
| `quality` | Runs lint + race tests + coverage gate + compile gate + benchmark gates |
| `quality-baseline` | Stores benchmark snapshot JSON in `benchmarks/quality_baseline.json` |
| `quality-trend` | Compares current benchmark run against baseline and writes `bin/quality/trend.json` |
| `clean` | Runs `go clean` and removes generated artifacts |

Examples:

```bash
# Standard local validation before commit
go run ./tools/runner.go check

# End-to-end tests
go run ./tools/runner.go e2e

# Quick fuzz validation
go run ./tools/runner.go fuzz-quick

# Full quality gate suite
go run ./tools/runner.go quality
```

Additional ad hoc commands:

```bash
# Full compile check without running tests
go test ./... -run '^$'

# Benchmarks
go test ./benchmarks -bench . -benchmem

# WASM compile checks
GOOS=js GOARCH=wasm go test ./pkg/grpctunnel -run '^$' -c -o ./bin/grpctunnel_wasm.test.wasm
GOOS=js GOARCH=wasm go build ./examples/wasm-client
```

---

## Examples

See the examples status catalog: [examples/README.md](../examples/README.md).

Canonical (`pkg/grpctunnel`) examples:

- `examples/direct-bridge`
- `examples/wasm-client`
- `examples/external-consumer` (standalone module, no local `replace`)

Compatibility (`helpers`/`pkg/bridge`) examples:

- `examples/simple-bridge`
- `examples/custom-router`
- `examples/production-bridge`

---

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

MIT. See [LICENSE](../../LICENSE).

## Support

- Issues: https://github.com/monstercameron/GoGRPCBridge/issues
- Discussions: https://github.com/monstercameron/GoGRPCBridge/discussions
