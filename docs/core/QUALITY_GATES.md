# Quality Gates

This document defines enforceable quality gates for local development and CI.

Run all gates:

```bash
go run ./tools/runner.go quality
```

Run baseline trend tracking:

```bash
go run ./tools/runner.go quality-trend
```

Quality summary output:

- `bin/quality/summary.json`
- `bin/quality/trend.json` (when `quality-trend` is run)

## Gate Set

1. Lint gate
   - Command: `go run ./tools/runner.go lint`
   - Requirement: no lint errors.

2. Reliability gate
   - Command: `go test ./pkg/... -v -race -coverprofile=coverage.txt -covermode=atomic`
   - Requirement:
     - Preferred: test suite passes with race detector.
     - Fallback: if local toolchain cannot run race mode (`CGO`/compiler unavailable), runner falls back to non-race coverage tests and emits a warning.
   - CI strict mode:
     - Set `RUNNER_STRICT_RACE=1` to fail instead of falling back.

3. Coverage gate
   - Source: `coverage.txt` parsed via `go tool cover -func=coverage.txt`
   - Requirement: total statement coverage `>= 90.00%`.

4. Compile gate
   - Command: `go test ./... -run '^$'`
   - Requirement: all packages compile for the current host environment.

5. Benchmark value-proposition gate
   - Command:
     - `go test ./benchmarks -bench "Benchmark(GRPC_(PayloadSize_1000Items|StreamLargeDataset_1000Items|BidirectionalStream_100Messages)|REST_(PayloadSize_1000Items|LargeDataset_1000Items|Bidirectional_100Messages))$" -run "^$" -benchmem -benchtime=300ms -count=1`
   - Requirements:
     - Payload size savings (gRPC vs REST for 1000 items): `>= 15%`
     - Bidirectional stream memory savings (gRPC vs REST): `>= 40%`
     - Bidirectional stream allocation savings (gRPC vs REST): `>= 30%`
     - Large dataset memory savings (gRPC stream vs REST large dataset): `>= 3%`

## Baseline Snapshot

Capture a benchmark snapshot:

```bash
go run ./tools/runner.go quality-baseline
```

Output file:

- `benchmarks/quality_baseline.json`

Use this snapshot for trend tracking and release notes evidence.

## CI Trend Tracking

CI should run:

```bash
go run ./tools/runner.go quality-trend
```

after baseline updates are committed, and upload `bin/quality/trend.json` as an artifact for review.
