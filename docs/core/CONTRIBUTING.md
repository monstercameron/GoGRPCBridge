# Development Guide

## Quick Start

```bash
# Clone the repo
git clone https://github.com/monstercameron/GoGRPCBridge.git
cd GoGRPCBridge

# Install golangci-lint (if not already installed)
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Run tests
go run ./tools/runner.go test

# Make changes, then run pre-commit check
go run ./tools/runner.go check
```

## Pre-Commit Workflow

The repository has **strict pre-commit hooks** that will **block commits** if:
- Code is not formatted with `gofmt`
- Code has linting errors from `golangci-lint`

### Commands

```bash
# Run all pre-commit checks (format + lint + tests)
go run ./tools/runner.go check

# Format code
go run ./tools/runner.go fmt

# Run linter
go run ./tools/runner.go lint

# Auto-fix lint issues (when possible)
go run ./tools/runner.go lint-fix

# Run tests
go run ./tools/runner.go test

# Quick tests (no race detector)
go run ./tools/runner.go test-short
```

## Testing

```bash
# Unit tests with race detector and coverage
go run ./tools/runner.go test

# Quick tests
go run ./tools/runner.go test-short

# Fuzz tests (1 minute each)
go run ./tools/runner.go fuzz

# Quick fuzz validation (5 seconds each) - same as CI
go run ./tools/runner.go fuzz-quick

# Run specific fuzz test
go test ./pkg/bridge -v -fuzz=FuzzWebSocketConnWrite -fuzztime=60s

# E2E tests (requires Playwright)
go run ./tools/runner.go e2e

# Run specific test
go test ./pkg/bridge -v -run TestWebSocketConn
```

### Important: Fuzz Test Usage

**❌ Don't use:** `go test ./pkg/bridge -fuzz=. -fuzztime=10s`

This will fail with: `testing: will not fuzz, -fuzz matches more than one fuzz test`

**✅ Instead use:**
- `go run ./tools/runner.go fuzz` - Runs each fuzzer individually for 1 minute
- `go run ./tools/runner.go fuzz-quick` - Quick validation (5s each, same as CI)
- Individual fuzzer: `go test ./pkg/bridge -fuzz=FuzzWebSocketConnWrite -fuzztime=60s`

The `-fuzz` flag must match exactly ONE fuzz function.

## CI/CD Pipeline

When you open a pull request or push bridge-related changes, GitHub Actions runs:

1. **Lint** - `golangci-lint` with `.golangci.yml` config
2. **Test** - Unit tests with race detector
3. **WASM Compile** - js/wasm compile coverage for tunnel packages
4. **Browser Lane** - end-to-end browser coverage (full gate)
5. **Security Scan** - gosec + govulncheck with fail-on-severity policy
6. **Go-Get Smoke** - clean consumer `go get github.com/monstercameron/GoGRPCBridge@latest`

### Release Policy

Releases are intentionally no longer patch-auto-incremented on `main`.

Current policy:
- release from an explicit semver tag push (`vX.Y.Z`) or manual `workflow_dispatch` with explicit `release_tag`
- release workflow must pass verification lanes before publishing
- release workflow must pass `go run ./tools/runner.go canonical-publish-check`
- release artifacts include checksum and GitHub provenance attestation

## Runner Commands

```bash
go run ./tools/runner.go help          # Show all available commands
go run ./tools/runner.go test          # Run tests with coverage
go run ./tools/runner.go test-short    # Quick tests without race detector
go run ./tools/runner.go fmt           # Format all code
go run ./tools/runner.go lint          # Run linter
go run ./tools/runner.go lint-fix      # Auto-fix lint issues
go run ./tools/runner.go check         # Format + lint + tests (pre-commit)
go run ./tools/runner.go pre-commit    # Same as check
go run ./tools/runner.go fuzz          # Run fuzz tests (1 min each)
go run ./tools/runner.go e2e           # Run end-to-end tests
go run ./tools/runner.go build         # Build example binaries
go run ./tools/runner.go quality       # Run lint + tests + coverage + benchmark gates
go run ./tools/runner.go quality-baseline # Store benchmark baseline snapshot
go run ./tools/runner.go canonical-publish-check # Verify canonical module/repo identity and go-get smoke
go run ./tools/runner.go clean         # Clean caches and artifacts
```

`make <target>` remains available as a compatibility wrapper.

## Common Issues

### "golangci-lint: command not found"

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

### Pre-commit hook not executable

```bash
chmod +x .git/hooks/pre-commit
# Or
go run ./tools/runner.go install-hooks
```

### Tests fail with "playwright not found"

```bash
go run github.com/playwright-community/playwright-go/cmd/playwright@latest install --with-deps chromium
```

### Want to commit despite lint errors (not recommended)

```bash
git commit --no-verify -m "Your message"
```

## Security Scan

The `security` job runs [Gosec](https://github.com/securego/gosec) to detect:
- SQL injection vulnerabilities
- Hardcoded credentials
- Weak crypto algorithms (MD5, SHA1, etc.)
- File path traversal
- Unhandled errors that could cause security issues
- HTTP servers without timeouts
- Insecure file permissions

### Severity Levels

Gosec categorizes issues by **severity** (LOW, MEDIUM, HIGH) and **confidence** (LOW, MEDIUM, HIGH):

- **HIGH severity + HIGH confidence**: Critical security issues - **CI WILL FAIL** ❌
  - Examples: SQL injection, hardcoded secrets, weak crypto
- **MEDIUM severity**: Important but not critical - **CI passes but issues logged** ⚠️
  - Examples: Missing timeouts, file permissions
- **LOW severity**: Informational - **CI passes** ℹ️
  - Examples: Unsafe calls in generated code

### CI Behavior

The security scan will **fail the build** if it finds issues with:
- `severity=high` AND `confidence=high`

This prevents deploying code with serious security vulnerabilities.

**Note:** G103 (unsafe calls) is excluded as it appears in auto-generated protobuf code.

### What is SARIF?

**SARIF** (Static Analysis Results Interchange Format) is a standard JSON format for security scan results. It allows GitHub to:
- Display security issues in the "Security" tab
- Show warnings on pull requests
- Track security trends over time

**Note:** SARIF upload to GitHub Security tab requires **GitHub Advanced Security** (not available on free public repos). 

### How to View Results

1. **CI Workflow:** Results are uploaded as artifacts and shown in the workflow logs
2. **Local Scan:** Run `gosec ./...` to see all security issues
3. **Strict Check:** Run `gosec -severity=high -confidence=high -exclude=G103 ./...` (same as CI)
4. **CI Artifacts:** Download `gosec-results` artifact from workflow runs (contains SARIF file)

### Current Security Issues

As of now, there are only **4 low-severity issues** (all in auto-generated protobuf code):
- **Generated Code:** Unsafe calls in protobuf files (G103) - can't fix, excluded from CI

**Production code** (`pkg/`) has **zero security issues**. ✅

## Code Style

- Use `gofmt` with simplify flag (`-s`)
- Follow standard Go conventions
- Add tests for new features
- Update README for API changes
- Keep test coverage above 80%

## Pull Request Checklist

- [ ] Code is formatted (`go run ./tools/runner.go fmt`)
- [ ] Linting passes (`go run ./tools/runner.go lint`)
- [ ] All tests pass (`go run ./tools/runner.go test`)
- [ ] Added tests for new features
- [ ] Updated README if needed
- [ ] No security issues from Gosec

