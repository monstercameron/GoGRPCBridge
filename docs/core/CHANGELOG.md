# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog and this project follows semantic versioning.

## [Unreleased]

### Commits

- `be70bef` ci: harden security and release workflow guards
- `430fe06` docs: add deep security fuzz release process
- `d39af64` fix: harden websocket read and error-channel concurrency
- `b37d222` feat: advance GoGRPCBridge dev-to-prod readiness
- `7206dd4` chore: group current submodule updates

### Changed

- Reorganized repository docs into `docs/core`, `docs/examples`, `docs/benchmarks`, and `docs/observability`, and updated `docs/catalog.json` + docs portal path resolution accordingly.
- Added root GitHub-facing wrapper files (`README.md`, `CONTRIBUTING.md`, `SECURITY.md`, `LICENSE`) that point to canonical docs under `docs/`.
- Removed stale `Makefile` references from docs and removed `Makefile` from the repository in favor of the Go runner workflow (`go run ./tools/runner.go ...`).
- Expanded ignore coverage for local benchmark and coverage artifacts (`coverage.txt`, `perf_*.out`, `benchmarks.test.exe`) and cleaned generated local artifacts.
- Reworked root `README.md` into a technical product landing page with executable server/WASM integration snippets, architecture flow, production-hardening controls, and benchmark evidence drawn from `benchmarks/quality_baseline.json`.
- Added host-repo operational docs (`GOGRPCBRIDGE_*`) into canonical `docs/core/` and indexed them in docs navigation/catalog.
- Added explicit module/repository identity policy docs (`docs/core/MODULE_IDENTITY.md`) and linked the policy from README and docs index.
- Hardened `canonical-publish-check` to accept canonical and legacy repository URLs, support fork-safe CI mode (`RUNNER_CANONICAL_SKIP_ORIGIN=1`), and validate clean-consumer server and WASM compile smoke builds.
- Updated release and CI workflows to align with Go 1.25.x, moved release changelog extraction to `docs/core/CHANGELOG.md`, added `pkg.go.dev` discoverability checks in release validation, and replaced blind push-based auto patch tagging with intentional workflow-dispatch semver tagging.

## [v0.0.18] - 2026-03-27

### Highlights

- Fixed release artifact build path for WASM client by building from nested module directory (`examples/wasm-client`) instead of root module package path.

## [v0.0.17] - 2026-03-27

### Highlights

- Unignored and committed `api_compatibility_baseline.json` so release CI can run API governance checks from clean clones/tags.

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

- Canonical import path is aligned to `github.com/monstercameron/grpc-tunnel` across module metadata, examples, and documentation.
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
