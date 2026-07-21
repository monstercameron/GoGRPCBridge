# GoGRPCBridge

**Native gRPC in the browser** — run your existing gRPC services over WebSocket transport, with zero changes to your protobuf contracts.

[![Go Reference](https://pkg.go.dev/badge/github.com/monstercameron/GoGRPCBridge.svg)](https://pkg.go.dev/github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel)
[![Test](https://github.com/monstercameron/GoGRPCBridge/actions/workflows/test.yml/badge.svg)](https://github.com/monstercameron/GoGRPCBridge/actions/workflows/test.yml)
[![Release](https://github.com/monstercameron/GoGRPCBridge/actions/workflows/release.yml/badge.svg)](https://github.com/monstercameron/GoGRPCBridge/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/monstercameron/GoGRPCBridge)](https://goreportcard.com/report/github.com/monstercameron/GoGRPCBridge)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

Browsers can't open raw TCP connections, so standard gRPC clients don't work in `js/wasm` builds. GoGRPCBridge tunnels HTTP/2 gRPC frames over a WebSocket, so your Go WASM frontend talks to your Go backend with **real gRPC** — unary, server-streaming, client-streaming, and bidirectional — not a REST shim or a gRPC-Web subset.

```text
Browser (Go WASM gRPC client)
  └─ WebSocket (ws:// or wss://)
       └─ grpctunnel bridge handler (net/http)
            └─ HTTP/2 ⇄ in-process grpc.Server
                 └─ your existing protobuf services
```

## Features

- **Full gRPC semantics** — all four RPC types, deadlines, metadata, interceptors.
- **Zero contract changes** — your `.proto` files and generated code stay untouched.
- **One-line server integration** — `grpctunnel.Wrap(grpcServer)` is an `http.Handler`.
- **Production hardening built in** — origin allowlists, pre-upgrade authorization, read limits, connection caps, upgrade rate limiting.
- **Liveness by default** — server keepalive (30s ping / 120s idle) reclaims dead peers automatically; client-side `WithTunnelKeepalive` + `WithReconnectPolicy` give transparent reconnection. See the [connection lifecycle guide](./docs/core/CONNECTION_LIFECYCLE.md).
- **Native transport mode** — `WithNativeGRPCTransport()` serves sessions through gRPC's own HTTP/2 transport: −47% memory and −28% allocations per RPC versus the handler path.
- **Graceful shutdown & TLS** — `NewServer` hands you the `*http.Server`; `ListenAndServeTLS` for one-liner `wss://`.
- **Observability** — structured logs plus OpenTelemetry spans and metrics; W3C trace context forwarded into gRPC metadata.
- **Flexible targets** — clients accept `ws://`, `wss://`, `http://`, `https://`, `host:port`, `:port`, and same-origin paths in the browser.

## Install

```bash
go get github.com/monstercameron/GoGRPCBridge@latest
```

Requires Go 1.25+. As of v1.0.0 the exported API of `pkg/grpctunnel` follows semantic versioning: no breaking changes without a major version bump, enforced in CI by an API-compatibility guard.

## Quick Start

### Server

```go
package main

import (
	"log"
	"net/http"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
)

func main() {
	grpcServer := grpc.NewServer()
	// proto.RegisterYourServiceServer(grpcServer, &yourImpl{})

	mux := http.NewServeMux()
	mux.Handle("/grpc", grpctunnel.Wrap(grpcServer))
	mux.Handle("/", http.FileServer(http.Dir("./public"))) // serve your WASM app

	log.Fatal(http.ListenAndServe(":8080", mux))
}
```

### Browser client (`js/wasm`)

```go
//go:build js && wasm

package main

import (
	"context"
	"log"

	pb "github.com/your-org/your-proto/gen"
	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// Same-origin path: on https://example.com this dials wss://example.com/grpc.
	// Transport-level TLS is managed by the browser; gRPC creds stay insecure.
	conn, err := grpctunnel.Dial("/grpc",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	client := pb.NewTodoServiceClient(conn)
	res, err := client.ListTodos(context.Background(), &pb.ListTodosRequest{})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("todos: %d", len(res.GetTodos()))
}
```

### Native client (tests, CLIs, service-to-service)

```go
conn, err := grpctunnel.Dial("localhost:8080",
	grpc.WithTransportCredentials(insecure.NewCredentials()),
)
```

More runnable examples: [pkg.go.dev examples](https://pkg.go.dev/github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel#pkg-examples) and the [`examples/`](./examples) directory.

## Production Hardening

Every control is opt-in and composable:

```go
handler := grpctunnel.Wrap(grpcServer,
	// Browser origin allowlist: exact origins and *. subdomain wildcards.
	grpctunnel.WithAllowedOrigins("https://app.example.com", "https://*.staging.example.com"),

	// Authorization before the websocket upgrade: failing requests get 403
	// before any tunnel resources are allocated.
	grpctunnel.WithAuthorize(func(r *http.Request) error {
		return validateSession(r)
	}),

	// Transport bounds and liveness.
	grpctunnel.WithReadLimitBytes(4<<20),
	grpctunnel.WithKeepalive(30*time.Second, 2*time.Minute),

	// Abuse controls.
	grpctunnel.WithMaxActiveConnections(2000),
	grpctunnel.WithMaxConnectionsPerClient(20),
	grpctunnel.WithMaxUpgradesPerClientPerMinute(60),
)
```

For graceful shutdown and TLS, own the server:

```go
srv := grpctunnel.NewServer(":8080", grpcServer, opts...)
go srv.ListenAndServe()
// on SIGTERM:
srv.Shutdown(ctx)

// or one-liner TLS:
grpctunnel.ListenAndServeTLS(":443", "cert.pem", "key.pem", grpcServer, opts...)
```

Deployment notes:

- A 16 MB websocket read limit applies by default even with no options set.
- Requests without an `Origin` header (non-browser clients) pass origin checks by convention; use `WithAuthorize` for actual authentication.
- Behind a reverse proxy, per-client caps key on the proxy's address — enforce per-user limits at the proxy in that topology.
- Forwarded metadata (`x-request-id`, `x-correlation-id`, `traceparent`) is client-influenceable; treat it as diagnostics, not identity.

## Performance

Benchmarked against an equivalent REST/JSON transport (`benchmarks/quality_baseline.json`, windows/amd64):

| Scenario | gRPC bridge | REST baseline | Delta |
| --- | ---: | ---: | ---: |
| Payload 1000 items (`B/op`) | 352,567 | 751,877 | `-53.1%` |
| Bidirectional 100 messages (`B/op`) | 200,639 | 1,087,660 | `-81.6%` |
| Bidirectional 100 messages (`allocs/op`) | 5,333 | 10,854 | `-50.9%` |
| Large dataset stream 1000 items (`B/op`) | 805,667 | 905,704 | `-11.0%` |

The per-request forwarding path is allocation-free when tunneled requests already carry trace/request metadata, and the bridge shares websocket write-buffer pools across connections. Opting into `WithNativeGRPCTransport()` serves sessions through gRPC's native HTTP/2 transport for a further **−47% bytes and −28% allocations per unary RPC** (9.2 KB/163 allocs vs 17.3 KB/228) and ~20% faster server-stream drains — see [CONNECTION_LIFECYCLE.md](./docs/core/CONNECTION_LIFECYCLE.md) for the tradeoffs. CI enforces benchmark trend gates against the recorded baseline on every release.

## API Overview

| Need | Use |
| --- | --- |
| Mount the bridge as middleware | `Wrap(grpcServer, opts...)` |
| Typed config + error handling | `BuildBridgeHandler(grpcServer, BridgeConfig)` |
| Shutdown-capable server | `NewServer(addr, grpcServer, opts...)` |
| One-liner startup | `ListenAndServe` / `ListenAndServeTLS` / `Serve` |
| Dial from browser or native Go | `Dial` / `DialContext` / `BuildTunnelConn` |
| Origin policy | `WithAllowedOrigins` / `WithOriginCheck` / `BuildOriginAllowlistCheck` |
| Pre-upgrade auth | `WithAuthorize` |
| Cheapest per-RPC serving path | `WithNativeGRPCTransport` |
| Keepalive / dead-peer detection | server default (or `WithKeepalive`), client `WithTunnelKeepalive` |
| Reconnect tuning | `WithReconnectPolicy` / `ReconnectConfig` |
| grpcurl / grpcui / pprof side-channel | `BuildToolingHandler` / `ListenAndServeTooling` |

`pkg/grpctunnel` is the supported public API. `pkg/bridge` (low-level reverse-proxy primitives) is deprecated; see the [migration guide](./docs/core/MIGRATION.md).

## Quality Gates

Every push runs lint, race + coverage (≥90% enforced), fuzz seed corpus, Playwright browser e2e, `gosec`, and `govulncheck`. Releases additionally enforce API-compatibility governance, benchmark trend gates, changelog validation, and clean-consumer install smoke tests.

```bash
go run ./tools/runner.go quality                 # local quality gates
go run ./tools/runner.go canonical-publish-check # module identity + consumer smoke
```

## Documentation

- [Docs index](./docs/core/DOCS_INDEX.md) — all technical docs
- [Migration guide](./docs/core/MIGRATION.md) — typed API migration and behavior changes
- [Threat model](./docs/core/THREAT_MODEL.md) and [security policy](./SECURITY.md)
- [Operations runbook](./docs/core/OPERATIONS_RUNBOOK.md) and [troubleshooting](./docs/core/TROUBLESHOOTING.md)
- [Changelog](./docs/core/CHANGELOG.md)

## Contributing

Issues and PRs welcome — see [CONTRIBUTING.md](./CONTRIBUTING.md). Run `go run ./tools/runner.go quality` before submitting.

## License

[MIT](./LICENSE)
