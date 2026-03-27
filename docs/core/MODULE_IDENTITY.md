# Module Identity

This project uses one canonical public identity for repository, module, and imports.

## Canonical Names

- Repository name: `github.com/monstercameron/GoGRPCBridge`
- Canonical module path: `github.com/monstercameron/GoGRPCBridge`
- Public integration package: `github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel`

## Why This Exists

- New consumers should see one unambiguous install/import path.
- Release tags, `go get`, and pkg.go.dev resolve against the same identity.
- CI guards fail fast if repository and module identity drift.

## Consumer Rules

- Use `github.com/monstercameron/GoGRPCBridge` for `go get`.
- Use `github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel` for imports.
- Use `GoGRPCBridge` as the project/repository name in docs and communications.

```bash
go get github.com/monstercameron/GoGRPCBridge@latest
```

```go
import "github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
```

## Validation Gate

`go run ./tools/runner.go canonical-publish-check` enforces:

- `go.mod` module path equals `github.com/monstercameron/GoGRPCBridge`
- repository identity matches accepted canonical URLs
- clean-consumer `go get ...@latest` succeeds
- clean-consumer compile smoke succeeds for:
  - tiny server target
  - tiny `js/wasm` target

Use `RUNNER_CANONICAL_SKIP_ORIGIN=1` in PR CI when origin remote is expected to differ (for example forked repositories).
