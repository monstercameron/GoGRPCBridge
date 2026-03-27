# Getting Started TODOs

Use this checklist to get from clone to validated tunnel traffic fast, with proof at each step.

Estimated time: 30 to 45 minutes.

## Success Criteria

- [ ] You can run a GoGRPCBridge server locally.
- [ ] You can build and load the WASM client.
- [ ] You can verify gRPC calls over WebSocket in browser console logs.
- [ ] You can run baseline quality checks.
- [ ] You can move into the advanced guide with a known-good local baseline.

Continue to the advanced guide when complete: [GETTING_STARTED_ADVANCED.md](./GETTING_STARTED_ADVANCED.md)

## 1. Prepare Tooling

- [ ] Go toolchain is available and version is compatible.
- [ ] Git is available.
- [ ] Browser (Chromium/Chrome/Edge) is available for WASM verification.

```bash
go version
git --version
```

Expected:

- `go version` prints Go 1.25.x or newer.
- `git --version` prints without errors.

## 2. Enter Project Root

- [ ] Open the GoGRPCBridge repository root.

```bash
cd third_party/GoGRPCBridge
```

- [ ] Confirm runner commands are available.

```bash
go run ./tools/runner.go help
```

## 3. Build the WASM Client Artifact

- [ ] Build browser client artifact to `examples/_shared/public/client.wasm`.

```bash
GOOS=js GOARCH=wasm go build -o ./examples/_shared/public/client.wasm ./examples/wasm-client
```

- [ ] Confirm output exists.

```bash
ls ./examples/_shared/public/client.wasm
```

## 4. Start the Direct Bridge Example

- [ ] Run the bridge server in terminal A.

```bash
go run ./examples/direct-bridge
```

Expected server signal:

- Logs include a line similar to `Direct gRPC-over-WebSocket server listening on 127.0.0.1:5000`.

## 5. Open the Browser Client

- [ ] Serve `examples/_shared/public` with any static server in terminal B.

```bash
go run ./tools/runner.go examples
```

If your environment uses a different static-server workflow, serve this directory directly:

- `./examples/_shared/public`

- [ ] Open the page in your browser and inspect DevTools console.

Expected browser signal:

- WASM startup logs appear.
- Todo RPC operations produce logs without WebSocket upgrade failures.

## 6. Verify Bridge Wiring with a Minimal Call Path

- [ ] Confirm the server uses canonical `pkg/grpctunnel` path with explicit origin policy.

```go
package main

import (
	"log"
	"net/http"
	"strings"

	"github.com/monstercameron/grpc-tunnel/pkg/grpctunnel"
	"google.golang.org/grpc"
)

func isBridgeOriginAllowed(r *http.Request) bool {
	parseOrigin := strings.ToLower(r.Header.Get("Origin"))
	return parseOrigin == "http://localhost:8080" || parseOrigin == "http://127.0.0.1:8080"
}

func main() {
	parseGrpcServer := grpc.NewServer()
	parseMux := http.NewServeMux()
	parseMux.Handle("/grpc", grpctunnel.Wrap(parseGrpcServer, grpctunnel.WithOriginCheck(isBridgeOriginAllowed)))
	log.Fatal(http.ListenAndServe(":5000", parseMux))
}
```

## 7. Run Local Quality Baseline

- [ ] Run short test lane.

```bash
go run ./tools/runner.go test-short
```

- [ ] Run full quality gate.

```bash
go run ./tools/runner.go quality
```

Expected:

- Lint and tests pass.
- Coverage gate and benchmark gates pass.
- `bin/quality/summary.json` exists.

## 8. Record Your Baseline Snapshot

- [ ] Store benchmark baseline for future comparisons.

```bash
go run ./tools/runner.go quality-baseline
```

- [ ] Verify baseline file exists.

```bash
ls ./benchmarks/quality_baseline.json
```

## 9. Transition to Advanced Hardening

- [ ] Move to [GETTING_STARTED_ADVANCED.md](./GETTING_STARTED_ADVANCED.md).
- [ ] Implement production controls before internet-facing rollout.
