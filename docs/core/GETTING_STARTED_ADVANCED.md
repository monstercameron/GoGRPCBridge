# Getting Started Advanced

This guide starts where [GETTING_STARTED_TODOS.md](./GETTING_STARTED_TODOS.md) ends.

Goal: move from local proof-of-life to a production-ready operating posture.

## 1. Choose a Deployment Profile First

Pick one profile and enforce it explicitly.

- Local development:
  - permissive host setup
  - rapid iteration
  - low blast radius
- Trusted internal network:
  - strict origin allow-list
  - bounded client populations
  - internal observability stack
- Internet-facing reverse-proxied production:
  - strict origin + auth boundaries
  - connection/upgrade abuse limits
  - mandatory TLS/WSS and operational SLOs

Document your selected profile in runbooks before rollout.

## 2. Build a Hardened Bridge Handler

Use explicit options, not implicit defaults, so behavior is auditable.

```go
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
)

func isBridgeOriginAllowed(r *http.Request) bool {
	switch r.Header.Get("Origin") {
	case "https://app.example.com":
		return true
	default:
		return false
	}
}

func main() {
	parseGrpcServer := grpc.NewServer()
	parseBridgeHandler := grpctunnel.Wrap(
		parseGrpcServer,
		grpctunnel.WithOriginCheck(isBridgeOriginAllowed),
		grpctunnel.WithReadLimitBytes(4<<20),
		grpctunnel.WithKeepalive(30*time.Second, 2*time.Minute),
		grpctunnel.WithMaxActiveConnections(3000),
		grpctunnel.WithMaxConnectionsPerClient(25),
		grpctunnel.WithMaxUpgradesPerClientPerMinute(90),
		grpctunnel.WithConnectHook(func(r *http.Request) {
			log.Printf("INFO tunnel connected remote=%s", r.RemoteAddr)
		}),
		grpctunnel.WithDisconnectHook(func(r *http.Request) {
			log.Printf("INFO tunnel disconnected remote=%s", r.RemoteAddr)
		}),
	)

	parseMux := http.NewServeMux()
	parseMux.Handle("/grpc", parseBridgeHandler)

	parseServer := &http.Server{
		Addr:              ":8443",
		Handler:           parseMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	log.Fatal(parseServer.ListenAndServeTLS("/etc/certs/fullchain.pem", "/etc/certs/privkey.pem"))
}
```

Why this matters:

- Explicit limits provide deterministic behavior under load and abuse.
- Timeouts reduce resource pinning from slow clients.
- Hook logs give operational traceability for connect/disconnect churn.

## 3. Harden Client Reconnect Semantics

Use reconnect policy intentionally and validate it under fault conditions.

```go
parseReconnectPolicy := grpctunnel.ReconnectConfig{
	InitialDelay:      250 * time.Millisecond,
	MaxDelay:          8 * time.Second,
	Multiplier:        1.7,
	Jitter:            0.2,
	MinConnectTimeout: 4 * time.Second,
}

parseConn, parseErr := grpctunnel.BuildTunnelConn(parseCtx, grpctunnel.TunnelConfig{
	Target:          "wss://bridge.example.com/grpc",
	ReconnectConfig: &parseReconnectPolicy,
	GRPCOptions:     grpctunnel.ApplyTunnelInsecureCredentials(nil),
})
```

Validation focus:

- reconnect storms
- reverse proxy restart windows
- browser tab suspend/resume behavior

## 4. Operationalize Quality and Security Gates

Run these before each release candidate:

```bash
go run ./tools/runner.go quality
go run ./tools/runner.go quality-trend
go run ./tools/runner.go canonical-publish-check
go run github.com/securego/gosec/v2/cmd/gosec@v2.25.0 -severity high -confidence high -exclude G103 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
```

Release sign-off references:

- [SECURITY_RELEASE_CHECKLIST.md](./SECURITY_RELEASE_CHECKLIST.md)
- [RELEASE_CHECKLIST.md](./RELEASE_CHECKLIST.md)
- [QUALITY_GATES.md](./QUALITY_GATES.md)

## 5. Instrument and Alert Like a Service Owner

Follow contract-driven observability, not ad hoc logging.

- Use [OBSERVABILITY_CONTRACT.md](./OBSERVABILITY_CONTRACT.md) as the minimum telemetry baseline.
- Wire dashboard queries from `docs/observability/DASHBOARD_QUERIES.md`.
- Track:
  - upgrade failure rates
  - active connections
  - latency distributions
  - backend dial failures

## 6. Lock in Compatibility Discipline

Before shipping a version:

- Run compatibility guard:

```bash
go run ./tools/api_compat_guard check
```

- Review:
  - [API_COMPATIBILITY.md](./API_COMPATIBILITY.md)
  - [MIGRATION.md](./MIGRATION.md)
  - [CHANGELOG.md](./CHANGELOG.md)

## 7. Advanced Done Checklist

- [ ] Deployment profile is documented and enforced.
- [ ] Bridge handler uses explicit origin, limit, and timeout controls.
- [ ] Reconnect policy is validated against failure-mode scenarios.
- [ ] Quality/security/release gates pass on release candidate commit.
- [ ] Observability dashboards and alerts are wired and reviewed.
- [ ] API compatibility checks pass with migration/changelog updates.

Next:

- Use [OPERATIONS_RUNBOOK.md](./OPERATIONS_RUNBOOK.md) for day-2 operations.
- Use [ROLLBACK_AND_HOTFIX.md](./ROLLBACK_AND_HOTFIX.md) for incident rollback workflow.
