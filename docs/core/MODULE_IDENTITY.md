# Module Identity

This project uses a split between repository branding and module import path by design.

## Canonical Names

- Repository name: `github.com/monstercameron/GoGRPCBridge`
- Canonical module path: `github.com/monstercameron/grpc-tunnel`
- Public integration package: `github.com/monstercameron/grpc-tunnel/pkg/grpctunnel`

## Why This Exists

- The repository was renamed for product branding and discoverability.
- The module path remains stable to preserve existing consumer installs and imports.
- Changing module path would be a public breaking change for all existing users.

## Consumer Rules

- Use `github.com/monstercameron/grpc-tunnel` for `go get`.
- Use `github.com/monstercameron/grpc-tunnel/pkg/grpctunnel` for imports.
- Use `GoGRPCBridge` as the project/repository name in docs and communications.

```bash
go get github.com/monstercameron/grpc-tunnel@latest
```

```go
import "github.com/monstercameron/grpc-tunnel/pkg/grpctunnel"
```

## Validation Gate

`go run ./tools/runner.go canonical-publish-check` enforces:

- `go.mod` module path equals `github.com/monstercameron/grpc-tunnel`
- repository identity matches accepted canonical URLs
- clean-consumer `go get ...@latest` succeeds
- clean-consumer compile smoke succeeds for:
  - tiny server target
  - tiny `js/wasm` target

Use `RUNNER_CANONICAL_SKIP_ORIGIN=1` in PR CI when origin remote is expected to differ (for example forked repositories).
