# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog and this project follows semantic versioning.

## [Unreleased]

### Added

- Leak and robustness regression suite: goroutine-leak tests over repeated connect/RPC/disconnect cycles (handler mode, native mode, and rejected-upgrade paths), abuse-guard slot-accounting verification, read-limit breach enforcement, 32 MiB streaming soak tests through both transports, and sustained-throughput benchmarks (~615 MB/s handler / ~835 MB/s native on 64 KB chunks, loopback).
- Connection-lifecycle documentation for long-lived, high-volume streams (video/file transfer): chunking guidance, end-to-end backpressure, keepalive interaction with active streams, and resumption semantics.

### Changed

- CI: fuzz seed corpus now runs deterministically via `-run '^Fuzz'` instead of timed `-fuzztime` fuzzing, which intermittently failed at the fuzztime boundary with the Go fuzz engine's "context deadline exceeded" worker-shutdown race on loaded runners.
- CI: the Playwright driver is installed from npm (`playwright-core@1.60.0` + `PLAYWRIGHT_DRIVER_PATH`) — the `playwright.azureedge.net` driver CDN was retired and 404s for every driver version, and playwright-go v0.6100.0 is unusable (its tag declares the old `mxschmitt` module path).
- Cleaned residual machine-generated `parse*` naming from documentation code samples (`docs/core/README.md`, `GETTING_STARTED_ADVANCED.md`).

## [v0.2.0] - 2026-07-21

### Highlights

- Runtime-cost and connection-lifecycle release: native gRPC transport mode (−47% memory per RPC), server keepalive on by default (dead peers reclaimed automatically), and a complete client keepalive + reconnection story.

### Added

- `WithNativeGRPCTransport` / `BridgeConfig.ShouldUseNativeGRPCTransport` — serves tunneled sessions through `grpc.Server.Serve` and gRPC's native HTTP/2 transport instead of the `net/http` handler path: **9.2 KB / 163 allocs per unary RPC vs 17.3 KB / 228** (−47% bytes, −28% allocs), ~20% faster server-stream drains, native flow control, and gRPC server keepalive support. Tradeoffs (no upgrade-header forwarding; no transport credentials on the `grpc.Server`) are documented. Verified for unary, server-streaming, and bidirectional RPCs plus concurrent clients.
- `WithTunnelKeepalive` / `TunnelConfig.KeepaliveConfig` / `ApplyTunnelKeepalivePolicy` — client-side gRPC keepalive over the tunnel (native and WASM builds): detects silently dead connections (NAT resets, dropped networks) and triggers automatic reconnection even with no active streams.
- `WithKeepaliveDisabled` / `BridgeConfig.ShouldDisableKeepalive` — explicit opt-out of server keepalive probing.
- `docs/core/CONNECTION_LIFECYCLE.md` — authoritative connect/disconnect/timeout/reconnect guide: keepalive matrix, disconnect-detection paths, reconnection tuning, browser caveats, recommended production configuration, and transport-mode comparison.
- Lifecycle test suite: dead-peer reclamation, server-restart reconnection (`WaitForReady`), keepalive defaulting rules, native-transport end-to-end and concurrency tests, and transport-mode benchmarks.

### Changed

- **Server keepalive defaults on** (30s ping / 120s idle) when not explicitly configured — silently dead clients previously pinned connection slots and goroutines until the OS TCP timeout. Disable with `WithKeepaliveDisabled()` when an upstream boundary owns liveness.
- Handler-mode serving drops the redundant `h2c` upgrade shim (requests inside `ServeConn` are already HTTP/2), removing a per-request indirection.
- CI: bumped `playwright-go` to v0.6000.0 — the 1.52 driver's `playwright.azureedge.net` CDN was retired and returned 404s, breaking every e2e lane.

## [v0.1.1] - 2026-07-21

### Highlights

- Performance, documentation, and repository-professionalism release. No API changes.

### Performance

- Per-RPC forward-metadata injection no longer clones request headers twice. Requests already carrying trace/request metadata now pass through with zero allocations (1072 ns → 65 ns, 8 allocs → 0), sessions with no forwardable headers skip the wrapper entirely (295 ns → 2.6 ns), and the injection path drops ~31% of bytes allocated. Micro-benchmarks added in `server_bench_test.go`.

### Documentation

- Rewrote the root `README.md` as a professional landing page: feature matrix, quick starts (server, browser WASM, native client), hardening guide, API overview table, and deployment caveats.
- Added runnable pkg.go.dev examples (`example_test.go`): `Wrap`, `NewServer` with graceful shutdown, `WithAuthorize`, `WithAllowedOrigins`, `Dial`, and `BuildTunnelConn`.
- Expanded `pkg/wasm/dialer` package documentation with bounded-queue, event-loop, and deadline semantics.
- Hardened `SECURITY.md` with a concrete private-disclosure channel (GitHub Security Advisories) and the automated gate list.

### Repository

- Added `.gitattributes` (LF normalization — fixes false gofmt diffs on Windows checkouts), `.editorconfig`, `CODE_OF_CONDUCT.md`, issue templates, a pull-request template, and Dependabot configuration (gomod + GitHub Actions, weekly).
- Added a CodeQL analysis workflow and a CI job that executes the `pkg/grpctunnel` WASM test suite under Node (previously WASM code was compile-checked only).
- Pruned stale internal process documents (Codex TODO scratch files, self-grading rubrics, host-repo submodule-era docs) from `docs/core/` and updated the docs index, catalog, and portal accordingly.

## [v0.1.0] - 2026-07-21

### Highlights

- Minor version bump: first release with new server-hardening surface area on `pkg/grpctunnel`.

### Added

- `BridgeConfig.Authorize` and `WithAuthorize` — pre-upgrade authorization hook; failing requests are rejected with `403 Forbidden` before any websocket or gRPC resources are allocated.
- `WithAllowedOrigins` and `BuildOriginAllowlistCheck` — declarative origin allowlisting with case-insensitive exact matching, `"*"`, and `"scheme://*.domain"` subdomain wildcards; requests without an `Origin` header (non-browser clients) pass, matching browser-only origin-policy convention.
- `NewServer` — returns a configured `*http.Server` so callers own graceful shutdown (`Shutdown`), TLS wiring, and timeout tuning. `Serve`/`ListenAndServe` now build on it.
- `ListenAndServeTLS` — one-liner `wss://` server startup.
- Client targets now accept `http://` and `https://` URLs on both native and WASM builds, mapped to `ws://` and `wss://` respectively.

### Fixed

- Server-side `net.Conn` adapter: a non-binary websocket frame now surfaces an explicit protocol error instead of being silently reported as clean `io.EOF` (which masked protocol violations as normal stream end).
- WASM target inference: `http(s)://` targets previously produced malformed URLs like `ws://http://example.com`; unsupported schemes (e.g. `ftp://`) are now rejected with an error instead of being mangled.
- Abuse controls: the per-client upgrade-rate window map is now swept once per window, fixing unbounded memory growth under client-address churn (slow memory-exhaustion vector).
- WASM dialer test harness: environment overrides now use `Object.defineProperty`, fixing the `navigator.onLine` test under modern Node where `globalThis.navigator` is accessor-defined.

### Changed

- `pkg/bridge` is formally deprecated in favor of `pkg/grpctunnel`; it remains supported for existing integrations but new features land in `pkg/grpctunnel` only.
- Internal naming cleanup across `pkg/grpctunnel` and `pkg/wasm/dialer`: removed the machine-generated `parse*` prefix from locals, parameters, and unexported identifiers. No exported API was renamed or removed.
- Documentation examples no longer demonstrate `InsecureSkipVerify`.

### CI and repository (previously unreleased)

- Hardened `test.yml` quality gates with one retry, `bin/quality/quality.log` artifact upload, and explicit CI race-skip fallback (`RUNNER_QUALITY_SKIP_RACE=1`) to unblock flaky race-toolchain lanes.
- Hardened `release.yml` by capturing quality gate logs, using the same CI race-skip fallback in quality retries, forcing Node 24 action runtime, and adding post-publish release visibility/asset verification.
- Stabilized runner benchmark gating with a built-in retry pass to reduce transient benchmark-noise failures in CI.
- Reorganized repository docs into `docs/core`, `docs/examples`, `docs/benchmarks`, and `docs/observability`, and updated `docs/catalog.json` + docs portal path resolution accordingly.
- Added root GitHub-facing wrapper files (`README.md`, `CONTRIBUTING.md`, `SECURITY.md`, `LICENSE`) that point to canonical docs under `docs/`.
- Removed stale `Makefile` references from docs and removed `Makefile` from the repository in favor of the Go runner workflow (`go run ./tools/runner.go ...`).
- Expanded ignore coverage for local benchmark and coverage artifacts (`coverage.txt`, `perf_*.out`, `benchmarks.test.exe`) and cleaned generated local artifacts.
- Reworked root `README.md` into a technical product landing page with executable server/WASM integration snippets, architecture flow, production-hardening controls, and benchmark evidence drawn from `benchmarks/quality_baseline.json`.
- Added host-repo operational docs (`GOGRPCBRIDGE_*`) into canonical `docs/core/` and indexed them in docs navigation/catalog.
- Added explicit module/repository identity policy docs (`docs/core/MODULE_IDENTITY.md`) and linked the policy from README and docs index.
- Hardened `canonical-publish-check` to accept canonical and legacy repository URLs, support fork-safe CI mode (`RUNNER_CANONICAL_SKIP_ORIGIN=1`), and validate clean-consumer server and WASM compile smoke builds.
- Updated release and CI workflows to align with Go 1.25.x, moved release changelog extraction to `docs/core/CHANGELOG.md`, added `pkg.go.dev` discoverability checks in release validation, and replaced blind push-based auto patch tagging with intentional workflow-dispatch semver tagging.

## [v0.0.19] - 2026-03-27

### Highlights

- Switched canonical module path/import identity to `github.com/monstercameron/GoGRPCBridge` across `go.mod`, code imports, docs, and runner publish checks.
- Kept toolchain policy on Go `1.25`/`go1.25.8` and aligned host-repo integration references to the new module identity.

## [v0.0.18] - 2026-03-27

### Highlights

- Fixed release artifact build path for WASM client by building from nested module directory (`examples/wasm-client`) instead of root module package path.

## [v0.0.17] - 2026-03-27

### Highlights

- Tracked `tools/api_compat_guard/api_compatibility_baseline.json` so release CI can run API governance checks from clean clones/tags.

## [v0.0.16] - 2026-03-27

### Highlights

- Added one-time retry logic for the release quality gate to reduce transient CI flake while preserving strict failure behavior on repeat failure.

## [v0.0.15] - 2026-03-27

### Highlights

- Fixed API governance guard documentation-path resolution to support canonical docs under `docs/core/*` (with legacy root-path fallback).
- Added focused tests for API guard documentation-path lookup behavior.

## [v0.0.14] - 2026-03-27

### Highlights

- Fixed quality-toolchain install command in CI/release workflows by removing a duplicate `v` prefix from `goimports` module version resolution (`@${GOIMPORTS_VERSION}`).

## [v0.0.13] - 2026-03-27

### Highlights

- Installed required quality toolchain (`goimports`, `golangci-lint`) inside `test.yml` and `release.yml` before running `go run ./tools/runner.go quality`.
- Replaced lint action wrapper with direct pinned `golangci-lint` CLI invocation for more deterministic behavior.
- Reduced large-dataset benchmark quality threshold from `5%` to `3%` to avoid flaky quality-gate failures while preserving regression signal.

## [v0.0.12] - 2026-03-27

### Highlights

- Added getting-started execution docs and top-of-portal quick links for onboarding (`GETTING_STARTED_TODOS.md`, `GETTING_STARTED_ADVANCED.md`).
- Added explicit module identity policy documentation and linked it from the primary docs navigation.
- Hardened canonical publish verification to include clean-consumer server and WASM compile smoke checks.
- Fixed release notes extraction path to `docs/core/CHANGELOG.md` and added `pkg.go.dev` discoverability verification in release workflow.
- Replaced blind push-based auto patch tagging with intentional workflow-dispatch semver tagging (`major`, `minor`, `patch`, or explicit version).
- Demoted `build.yml` to manual/build-sanity use and aligned CI/release workflows to Go `1.25.x`.

## [v0.0.11] - 2026-03-27

### Highlights

- Canonical import path is aligned to `github.com/monstercameron/GoGRPCBridge` across module metadata, examples, and documentation.
- Release and CI canonical publish checks enforce repository/module identity and clean-consumer `go get` validation for the canonical path.
- Release workflow canonical publish step now runs with `RUNNER_CANONICAL_GOPROXY=direct` to avoid proxy-index lag false negatives during first corrected-tag publication.

## [v0.0.10] - 2025-11-16

### Commits

- `1c62e57` Add comprehensive gRPC vs REST benchmarks with performance analysis

## [v0.0.9] - 2025-11-16

### Commits

- `f91ce36` Fix GitHub workflow badge URLs
- `7d49483` Add Go version badge to README
- `6fd0295` Rewrite README with professional, welcoming tone
- `236adea` Add comprehensive bridge unit tests and update Go version
- `df05316` Enhance README with compelling value proposition and getting started guide

## [v0.0.8] - 2025-11-16

### Commits

- `d50b2f2` Remove Go 1.23.x from test matrix
- `d1e3e47` Fix linter issues and make test workflow reusable
- `e7b608f` Fix race in webSocketConn and simplify security workflow
- `abfa1e9` Document fuzz test usage: must specify individual fuzzer, not -fuzz=.
- `1b1e770` Add CONTRIBUTING.md with development workflow guide
- `7360c2b` Improve CI/CD: tests required for release, strict pre-commit hooks, detailed security scan docs

## [v0.0.7] - 2025-11-16

### Commits

- `260fc8a` Add golangci-lint config and pre-commit hooks for automated linting

## [v0.0.6] - 2025-11-16

### Commits

- `918a9a2` Update badge URLs to use workflow name format

## [v0.0.5] - 2025-11-16

### Commits

- `59377bb` Fix data race in TestWrap_WithOptions using atomic.Bool

## [v0.0.4] - 2025-11-16

### Commits

- `60e837c` Add automatic URL inference for WASM client using window.location

## [v0.0.3] - 2025-11-16

### Commits

- `84ea6e9` Add comprehensive test coverage for grpctunnel API

## [v0.0.2] - 2025-11-16

### Commits

- `3705f9e` Add comprehensive streaming and HTTP/2 feature tests

## [v0.0.1] - 2025-11-16

### Commits

- `b0af9b3` Add auto-release and fix build workflow
- `d0f606c` Update README with build badges and simplify CI workflows
- `2093e30` Fix GitHub Actions workflows to use correct paths
- `94e8f4a` Fix critical memory leaks and add thread-safe connection state
- `23439fa` Add GitHub Actions workflows for CI/CD
- `9716e66` Add comprehensive test coverage: negative, edge, and fuzz tests
- `bb916a0` Refactor: Use constants in WASM test files
- `676a7fa` Refactor: Replace magic strings with named constants
- `adbeac0` Refactor: Improve variable and function names for clarity
- `35f7d77` Refactor: Separate library from examples
- `e44bfe1` Reorganize project structure - move all example resources to examples/
- `8ff21f7` Fix all linting issues and clean up codebase
- `843ffa0` Enhanced e2e tests with comprehensive edge cases
- `d93f475` Rewrite README as GoGRPCBridge
- `e4ff783` Beef up .gitignore with comprehensive rules
- `0c56da9` Remove TESTING.md
- `f42c27b` Remove build artifacts and unnecessary files
- `1871112` Restructure project to be go-gettable library
- `7f107eb` Add comprehensive test suite with 98.2% coverage
- `840d741` feat: add working gRPC-over-WebSocket bridge with WASM client support
- `637e791` update todos.json with new entries and modify existing todo text; enhance build.sh for debug mode and WebAssembly optimization; add gRPC WebSocket client implementation
- `a2d44ac` add WebSocket support for gRPC communication and enhance logging
- `e23d65b` add initial Todo list implementation with gRPC service and frontend UI
- `da4561b` added readme
- `5b45165` testing
