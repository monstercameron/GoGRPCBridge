# API Migration Guide

This guide covers migration to the typed `grpctunnel` API.

## v0.2.0 Behavior Changes

- **Server keepalive is now on by default** (30s websocket ping, 120s idle timeout) so silently dead clients are reclaimed instead of pinning resources until the OS TCP timeout. If a proxy in front of the bridge owns connection liveness, restore the old behavior with `WithKeepaliveDisabled()` (or `BridgeConfig.ShouldDisableKeepalive`). Setting explicit `WithKeepalive` values together with `WithKeepaliveDisabled` is now a validation error.
- Long-lived tunnels idle for more than 120 seconds whose clients do not answer websocket pings will now be closed. Standard clients (browsers, gorilla) answer pings automatically; only hand-rolled websocket clients that ignore ping frames are affected.

New in v0.2.0 (additive): `WithNativeGRPCTransport`/`BridgeConfig.ShouldUseNativeGRPCTransport` (serve sessions via gRPC's native HTTP/2 transport; −47% memory per RPC, no upgrade-header forwarding), `WithTunnelKeepalive`/`TunnelConfig.KeepaliveConfig`/`ApplyTunnelKeepalivePolicy` (client dead-connection detection), and `WithKeepaliveDisabled`. See [CONNECTION_LIFECYCLE.md](./CONNECTION_LIFECYCLE.md).

## v0.1.0 Behavior Changes

No exported API was renamed or removed in v0.1.0. Behavior deltas to review:

- Server `net.Conn` adapter: a non-binary websocket frame now returns an explicit protocol error instead of a clean `io.EOF`. Code that treated a text frame as normal stream end (unlikely by design) now sees an error.
- Client targets: `http://` and `https://` URLs are now accepted and mapped to `ws://`/`wss://` (both native and WASM builds). Previously native builds rejected them and WASM builds produced malformed URLs.
- WASM target validation: unsupported schemes (e.g. `ftp://`) are now rejected with an error instead of being silently mangled into a `ws://` prefix.
- `pkg/bridge` is formally deprecated; migrate to `pkg/grpctunnel` using the mapping below.

New in v0.1.0 (additive): `BridgeConfig.Authorize`/`WithAuthorize`, `WithAllowedOrigins`/`BuildOriginAllowlistCheck`, `NewServer`, and `ListenAndServeTLS`.

## Quick Migration Checklist

1. Replace legacy client dial calls with `BuildTunnelConn`.
2. Replace legacy server wrapping with `BuildBridgeHandler` or `HandleBridgeMux`.
3. Move ad-hoc options into `TunnelConfig` and `BridgeConfig`.
4. Add startup validation with `GetTunnelConfigError` and `GetBridgeConfigError`.
5. Keep legacy wrappers only where migration is intentionally deferred.

For known module-resolution issues during dependency setup, see [TROUBLESHOOTING.md](./TROUBLESHOOTING.md).

## Recommended API

Use these typed entry points for new code:

- `BuildTunnelConn(ctx, TunnelConfig)`
- `BuildBridgeHandler(grpcServer, BridgeConfig)`
- `HandleBridgeMux(mux, path, grpcServer, BridgeConfig)`
- `GetTunnelConfigError(TunnelConfig)`
- `GetBridgeConfigError(BridgeConfig)`

Legacy wrappers (`Dial`, `DialContext`, `Wrap`, `Serve`, `ListenAndServe`) remain supported.

## Old to New Mapping

| Old API | New API |
| --- | --- |
| `Dial` / `DialContext` | `BuildTunnelConn` |
| `Wrap` | `BuildBridgeHandler` |
| `mux.Handle(path, Wrap(...))` | `HandleBridgeMux` |
| ad-hoc TLS/options | `TunnelConfig` fields + `GRPCOptions` |

## Client Example (Non-WASM)

```go
conn, err := grpctunnel.BuildTunnelConn(ctx, grpctunnel.TunnelConfig{
    Target: "wss://api.example.com/grpc",
    GRPCOptions: []grpc.DialOption{
        grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
        grpc.WithBlock(),
    },
})
if err != nil {
    return err
}
defer conn.Close()
```

## Client Example (WASM)

```go
conn, err := grpctunnel.BuildTunnelConn(ctx, grpctunnel.TunnelConfig{
    Target: "", // same-origin inference
    GRPCOptions: grpctunnel.ApplyTunnelInsecureCredentials(nil),
})
if err != nil {
    return err
}
defer conn.Close()
```

Important:

- In WASM, `TLSConfig` and `ShouldUseTLS` are rejected by validation.
- Browser TLS is controlled by page origin and browser networking.

## Server Example (Mux)

```go
mux := http.NewServeMux()

if err := grpctunnel.HandleBridgeMux(mux, "/grpc", grpcServer, grpctunnel.BridgeConfig{
    CheckOrigin: checkOrigin,
}); err != nil {
    return err
}
```

## Validation Before Startup

```go
if err := grpctunnel.GetTunnelConfigError(tunnelConfig); err != nil {
    return err
}
if err := grpctunnel.GetBridgeConfigError(bridgeConfig); err != nil {
    return err
}
```

## Security-Impacting Behavior Changes

When migrating from permissive legacy wrappers, account for these security deltas:

- Bridge origin defaults now rely on websocket same-origin policy when `CheckOrigin` is nil.
- Bridge read-size protection uses a secure default limit when `ReadLimitBytes` is zero.
- Disabling read-size protection is explicit via `ShouldDisableReadLimit` (typed config) and should remain rare.
- Tooling listeners enforce stricter bind safety for introspection features (reflection/pprof).
- Reconnect config validation now rejects non-finite numeric inputs (`NaN`/`Inf`) for jitter and multiplier.
