# Troubleshooting

This page lists common setup and runtime failures for GoGRPCBridge and the fastest fix path.

## 1) `go get github.com/monstercameron/GoGRPCBridge@latest` fails

Symptom:
- module-path mismatch error when resolving current `@latest` tag

Cause:
- local source now declares module path `github.com/monstercameron/GoGRPCBridge`
- currently published tags may still point to older releases that declared `github.com/monstercameron/GoGRPCBridge`

Fix:
- publish a new semver tag from this repository state (module path aligned to `github.com/monstercameron/GoGRPCBridge`)
- then retry clean consumer smoke

Verification:

```bash
go mod init example.com/smoke
go get github.com/monstercameron/GoGRPCBridge@latest
```

Track:
- see root roadmap item `S10.3` for direct consumer `go get` verification

## 2) Browser client fails with TLS config error

Symptom:
- validation rejects `TunnelConfig.TLSConfig` or `TunnelConfig.ShouldUseTLS` in WASM

Cause:
- browser networking owns TLS in `js/wasm` builds

Fix:
- use same-origin target (`""` or `"/grpc"`) in WASM
- remove direct TLS config fields from WASM tunnel config

## 3) Bridge rejects connection with origin error

Symptom:
- WebSocket upgrade fails or clients disconnect immediately

Cause:
- `CheckOrigin` denies incoming `Origin` header

Fix:
- add explicit allow-list logic for trusted origins
- confirm exact browser `Origin` value in server logs before tightening rules

## 4) Tooling server fails to start when reflection or pprof is enabled

Symptom:
- `ListenAndServeTooling` returns a bind-safety validation error when using wildcard addresses like `:8081` or `0.0.0.0:8081`

Cause:
- tooling bind guard blocks wildcard/public exposure when introspection features are enabled

Fix:
- bind tooling to loopback (`127.0.0.1:<port>` or `[::1]:<port>`) for local/dev usage
- for internal-network tooling, use an explicit private interface IP and enforce network ACL/auth controls

## 5) Tunnel reconnect config fails validation with finite-value error

Symptom:
- validation rejects `ReconnectConfig.Multiplier` or `ReconnectConfig.Jitter` as non-finite

Cause:
- reconnect policy now rejects `NaN`/`Inf` values to prevent undefined backoff behavior

Fix:
- provide finite non-negative numbers for reconnect fields
- use zero values to inherit gRPC defaults when custom backoff tuning is not required

## 6) gRPC calls fail after tunnel connects

Symptom:
- tunnel opens but RPC requests fail or hang

Cause:
- backend gRPC server is not reachable from bridge target address

Fix:
- confirm bridge target host and port
- verify backend gRPC service is listening
- test backend with a direct local client before routing through tunnel

## 7) Build or codegen tools missing

Symptom:
- build/bootstrap reports missing `protoc` or plugin binaries

Fix:
- install required tools and ensure they are on `PATH`:
  - `go`
  - `git`
  - `protoc`
  - `protoc-gen-go`
  - `protoc-gen-go-grpc`

From repo root you can run:

```powershell
.\scripts\bootstrap-gogrpcbridge.ps1
```
