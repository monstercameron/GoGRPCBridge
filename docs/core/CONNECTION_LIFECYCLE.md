# Connection Lifecycle: Connect, Disconnect, Timeouts, Reconnect

This is the authoritative guide to how tunnel connections are established, kept alive, detected as dead, torn down, and re-established.

## Connection establishment

1. The client dials the bridge URL. The HTTP request passes, in order:
   - `Authorize` hook (if set) — failure returns `403` before any resources are allocated
   - abuse controls (global cap, per-client cap, upgrade rate limit) — failure returns `429`
   - origin check (`CheckOrigin` / `WithAllowedOrigins`) — failure fails the upgrade
2. The websocket upgrade completes; `OnConnect` fires; metrics/spans record the session.
3. gRPC's HTTP/2 client transport runs over the socket. All four RPC types work.

On the client, `Dial`/`DialContext` return immediately (non-blocking, standard gRPC channel semantics); the first RPC triggers the actual connection. Use `grpc.WaitForReady(true)` per call, or a deadline context, to control what happens while the channel is connecting.

## Keepalive and timeout matrix

| Layer | Direction | Knob | Default | What it does |
| --- | --- | --- | --- | --- |
| WebSocket ping | server → client | `WithKeepalive(ping, idle)` | **on: 30s ping / 120s idle** | Server pings; a peer that stops answering pongs is closed and reclaimed within the idle window |
| WebSocket keepalive off | — | `WithKeepaliveDisabled()` | off | Disables the above; dead peers then linger until OS TCP timeout — only use when a proxy in front owns liveness |
| gRPC keepalive | client → server | `WithTunnelKeepalive(interval, timeout)` | off | HTTP/2 pings inside the tunnel; detects silently dead connections (NAT reset, dropped Wi-Fi, sleeping laptop) and triggers reconnection, even with no active streams |
| gRPC server keepalive | server → client | `grpc.KeepaliveParams` on your `grpc.Server` | grpc defaults | Applies in native transport mode only (`WithNativeGRPCTransport`) |
| Upgrade-request timeouts | — | `NewServer` (`ReadTimeout` 15s etc.) | on | Bound the HTTP request before the upgrade; they do not affect live tunnels (the hijacked connection clears them) |
| Handshake timeout | client | `WithHandshakeTimeout` (native) / dial context deadline (WASM) | none | Bounds the websocket handshake itself |
| Per-RPC deadlines | client | `context.WithTimeout` per call | none | Standard gRPC; always set one in production |

**Server keepalive is on by default** (since v0.2.0). Without it, a client that vanishes without a close frame (power loss, network drop) pins its connection slot, its goroutines, and an abuse-guard slot until the OS gives up the TCP connection — often 15+ minutes. The 30s/120s defaults survive common proxy idle windows (ALB/nginx default to 60s) while reclaiming dead peers within two minutes.

## How disconnects are detected

- **Clean close** (tab closed, `conn.Close()`): the websocket close frame ends the session immediately. `OnDisconnect` fires; metrics decrement; abuse-guard slots release.
- **Dead peer, server side**: missed pongs hit the idle timeout → server closes the socket → same cleanup path. Bounded by `IdleTimeout` (default 120s).
- **Dead peer, client side**: with `WithTunnelKeepalive`, a missed ping ack marks the connection dead after `interval + timeout` (default 50s worst case) and the channel starts reconnecting. Without it, detection happens on the next write failure.
- **Server shutdown**: `NewServer(...).Shutdown(ctx)` stops new upgrades. Note that hijacked websocket connections are not tracked by `http.Server`, so live tunnels are not waited on — stop the gRPC server (`GracefulStop`) to drain RPCs, then close.

## Reconnection

Reconnection is automatic — it is standard gRPC channel behavior, and the bridge's dialer participates in it:

1. The channel notices the connection is gone (write failure, read EOF, or keepalive timeout).
2. It redials through the websocket dialer with exponential backoff.
3. In-flight RPCs on the dead connection fail (`UNAVAILABLE`); the application retries them, or uses `grpc.WaitForReady(true)` to have new calls block until the channel is ready again.

Tune the backoff with `WithReconnectPolicy`:

```go
conn, err := grpctunnel.Dial("wss://api.example.com/grpc",
	grpctunnel.WithReconnectPolicy(grpctunnel.ReconnectConfig{
		InitialDelay: 500 * time.Millisecond,
		MaxDelay:     30 * time.Second,
	}),
	grpctunnel.WithTunnelKeepalive(30*time.Second, 20*time.Second),
	grpc.WithTransportCredentials(insecure.NewCredentials()),
)
```

Verified end-to-end in `TestReconnect_SurvivesServerRestart`: a client survives the bridge process being torn down and restarted on the same address, with the follow-up RPC succeeding via `WaitForReady`.

### Browser caveats

- Background tabs throttle timers: keepalive probes and reconnect backoff may be delayed until the tab is foregrounded. Listen for `visibilitychange`/`online` events in the host page if you need eager reconnection.
- The browser controls websocket TLS, proxies, and handshake headers; per-connection auth belongs in cookies (sent automatically on the upgrade request) or a first-RPC token check.

## Recommended production configuration

```go
// Server
srv := grpctunnel.NewServer(":8080", grpcServer,
	grpctunnel.WithAllowedOrigins("https://app.example.com"),
	grpctunnel.WithAuthorize(validateSession),
	// keepalive defaults (30s/120s) are already on
	grpctunnel.WithReadLimitBytes(4<<20),
	grpctunnel.WithMaxActiveConnections(2000),
	grpctunnel.WithMaxConnectionsPerClient(20),
	grpctunnel.WithMaxUpgradesPerClientPerMinute(60),
	grpctunnel.WithNativeGRPCTransport(), // cheapest per-RPC path; see below
)

// Client
conn, _ := grpctunnel.Dial("/grpc",
	grpctunnel.WithReconnectPolicy(grpctunnel.ReconnectConfig{MaxDelay: 30 * time.Second}),
	grpctunnel.WithTunnelKeepalive(30*time.Second, 20*time.Second),
	grpc.WithTransportCredentials(insecure.NewCredentials()),
)
```

If a load balancer sits in front with an idle timeout below 30s, lower `PingInterval` below the LB's window via `WithKeepalive`.

## Transport modes and runtime cost

| | Handler mode (default) | Native mode (`WithNativeGRPCTransport`) |
| --- | --- | --- |
| Serving path | `x/net http2.Server` → gRPC `ServeHTTP` | `grpc.Server.Serve` → native HTTP/2 transport |
| Memory per unary RPC | ~17.3 KB / 228 allocs | **~9.2 KB / 163 allocs (−47% / −28%)** |
| Server-stream drain | baseline | **~20% faster, −46% bytes** |
| Upgrade-header → RPC metadata forwarding | yes (`x-request-id`, `traceparent`, …) | no (connection-level logging only) |
| gRPC server keepalive/settings | not applied | fully applied |
| Requirement | — | `grpc.Server` must not carry transport credentials (TLS terminates at the websocket/HTTP layer) |

Prefer native mode when you don't rely on forwarded upgrade headers — it roughly halves GC pressure per RPC.

## Session hooks

- `WithConnectHook` / `WithDisconnectHook` fire at true session start/end in both transport modes (native mode blocks the upgrade handler until gRPC finishes with the connection).
- Structured log events: `ws_upgrade_succeeded`, `tunnel_connect`, `tunnel_disconnect`, `ws_upgrade_rejected_unauthorized`, `ws_upgrade_rejected_abuse_control`.
- OTel: request + session spans, `bridge_connections_active`, `bridge_connections_total`, `bridge_upgrade_failures_total`, `bridge_request_latency_ms`.
