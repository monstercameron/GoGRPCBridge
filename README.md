# GoGRPCBridge

Native gRPC in the browser using WebSocket transport, without rewriting your service contracts.

[![Go Reference](https://pkg.go.dev/badge/github.com/monstercameron/GoGRPCBridge.svg)](https://pkg.go.dev/github.com/monstercameron/GoGRPCBridge)
[![Test](https://github.com/monstercameron/GoGRPCBridge/actions/workflows/test.yml/badge.svg)](https://github.com/monstercameron/GoGRPCBridge/actions/workflows/test.yml)
[![Release](https://github.com/monstercameron/GoGRPCBridge/actions/workflows/release.yml/badge.svg)](https://github.com/monstercameron/GoGRPCBridge/actions/workflows/release.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## What You Get

- One public integration package for new work: `pkg/grpctunnel`.
- Unary and streaming RPC support from `js/wasm` clients.
- Server-side hardening hooks (origin policy, limits, connection controls).
- CI quality gates for lint, race/coverage, security scanners, compile, and value-proposition benchmarks.

## Architecture

```text
Browser WASM gRPC client
  -> websocket (ws/wss)
    -> grpctunnel bridge handler (Go HTTP server)
      -> in-process grpc.Server
        -> your existing protobuf services
```

The project keeps your protobuf and generated client/server contracts intact. It only adapts transport for browser constraints.

## Install

```bash
go get github.com/monstercameron/GoGRPCBridge@latest
```

Requires Go 1.25+ (see `go.mod` toolchain requirements).

## Module Identity

- GitHub repository: `github.com/monstercameron/GoGRPCBridge`
- Canonical Go module path: `github.com/monstercameron/GoGRPCBridge`

The module path remains `github.com/monstercameron/GoGRPCBridge` for consumer compatibility and existing `go get` installs. Use the module path for imports and `go get`, and use `GoGRPCBridge` as the project/repository name.

## Show, Not Tell: Minimal Integration

### 1. Server bridge in front of your `grpc.Server`

```go
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
)

func isAllowedOrigin(r *http.Request) bool {
	switch r.Header.Get("Origin") {
	case "https://app.example.com":
		return true
	default:
		return false
	}
}

func main() {
	grpcServer := grpc.NewServer()
	// Register generated services on grpcServer.

	handler := grpctunnel.Wrap(
		grpcServer,
		grpctunnel.WithOriginCheck(isAllowedOrigin),
		grpctunnel.WithKeepalive(30*time.Second, 2*time.Minute),
		grpctunnel.WithReadLimitBytes(4<<20),
		grpctunnel.WithMaxActiveConnections(2000),
		grpctunnel.WithMaxConnectionsPerClient(20),
		grpctunnel.WithMaxUpgradesPerClientPerMinute(60),
	)

	mux := http.NewServeMux()
	mux.Handle("/grpc", handler)

	log.Fatal(http.ListenAndServe(":8080", mux))
}
```

### 2. Browser `js/wasm` gRPC client over tunnel

```go
//go:build js && wasm

package main

import (
	"context"
	"log"
	"time"

	pb "github.com/your-org/your-proto/gen"
	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpctunnel.BuildTunnelConn(ctx, grpctunnel.TunnelConfig{
		Target:      "/grpc", // same-origin route on your host app
		GRPCOptions: grpctunnel.ApplyTunnelInsecureCredentials(nil),
	})
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

## Evidence: Baseline Benchmark Snapshot

Source: `benchmarks/quality_baseline.json` (generated `2026-03-26T13:07:50Z`, `windows/amd64`, Go `1.25.0`).

| Scenario | gRPC bridge | REST baseline | Delta |
| --- | ---: | ---: | ---: |
| Payload 1000 items (`B/op`) | 352,567 | 751,877 | `-53.1%` |
| Bidirectional 100 messages (`B/op`) | 200,639 | 1,087,660 | `-81.6%` |
| Bidirectional 100 messages (`allocs/op`) | 5,333 | 10,854 | `-50.9%` |
| Large dataset stream 1000 items (`B/op`) | 805,667 | 905,704 | `-11.0%` |

This is why the project exists: keep gRPC semantics while reducing transport overhead in browser-driven workflows.

## Production Guardrails You Can Enforce

- Origin allow-list with `WithOriginCheck`.
- Frame/read bounds with `WithReadLimitBytes` (or explicit disable only when upstream bounds exist).
- Abuse controls with:
  - `WithMaxActiveConnections`
  - `WithMaxConnectionsPerClient`
  - `WithMaxUpgradesPerClientPerMinute`
- Keepalive and idle behavior with `WithKeepalive`.
- Structured logs plus OpenTelemetry span/metric integration in the canonical path (`pkg/grpctunnel`).

## Quality and Security Gates

Run from this repository root (`third_party/GoGRPCBridge`):

```bash
go run ./tools/runner.go quality
go run ./tools/runner.go quality-trend
go run ./tools/runner.go canonical-publish-check
```

`canonical-publish-check` verifies module path alignment, canonical repository identity (`GoGRPCBridge`), and a clean-consumer `go get` + compile smoke test.

Release workflow also enforces:

- `gosec` high-severity/high-confidence policy
- `govulncheck`
- API governance checks
- compile verification
- benchmark trend artifact generation

## Documentation Map

- Core technical docs: [docs/core/README.md](./docs/core/README.md)
- Docs index: [docs/core/DOCS_INDEX.md](./docs/core/DOCS_INDEX.md)
- Module/repository identity policy: [docs/core/MODULE_IDENTITY.md](./docs/core/MODULE_IDENTITY.md)
- Threat model: [docs/core/THREAT_MODEL.md](./docs/core/THREAT_MODEL.md)
- Security release checklist: [docs/core/SECURITY_RELEASE_CHECKLIST.md](./docs/core/SECURITY_RELEASE_CHECKLIST.md)
- Performance notes: [docs/core/PERFORMANCE_OPTIMIZATION_NOTES.md](./docs/core/PERFORMANCE_OPTIMIZATION_NOTES.md)
- Examples catalog: [docs/examples/README.md](./docs/examples/README.md)
- Changelog: [docs/core/CHANGELOG.md](./docs/core/CHANGELOG.md)
- Public docs portal: [docs/index.html](./docs/index.html)
- Contributing: [CONTRIBUTING.md](./CONTRIBUTING.md)
- Security policy: [SECURITY.md](./SECURITY.md)
- License: [LICENSE](./LICENSE)
