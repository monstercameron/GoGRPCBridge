# Pre-Production Rollout TODOs

Environment-specific validation that cannot be proven from inside this repository. Work through this list in a staging environment that mirrors production before a high-criticality rollout. The behaviors the test suite already verifies (backpressure, concurrent streaming, reconnect storms, draining, session-lifetime re-authorization, leak regression) are documented in [CONNECTION_LIFECYCLE.md](./CONNECTION_LIFECYCLE.md#pre-production-rollout-checklist) and do not need re-validation per deployment.

## Reverse proxy / load balancer

- [ ] Confirm the proxy passes WebSocket upgrades on the bridge path (`Upgrade`/`Connection` headers intact) with response buffering disabled.
- [ ] Record the proxy's **idle timeout** and verify `PingInterval` (default 30s) is below it; lower via `WithKeepalive` if the idle window is under ~35s.
- [ ] Record the proxy's **maximum connection lifetime** (e.g., ALB hard cap) and set `WithSessionMaxLifetime` slightly below it so terminations are orderly bridge-side closes, then observe a reconnect cycle through the proxy.
- [ ] Verify client source addressing: behind the proxy all clients share the proxy's IP for per-client caps and rate limits — move per-user limits to the proxy/LB tier if needed.

## Horizontal scaling

- [ ] Deploy ≥2 bridge instances behind the LB with **no session affinity** and verify RPCs and reconnects succeed landing on different instances.
- [ ] Confirm global connection/rate limits are enforced at the LB tier (bridge counters are per-instance).
- [ ] If backend services keep per-session state, decide and test the affinity story for *those services* (the bridge itself needs none).

## Authentication

- [ ] Align `WithSessionMaxLifetime` with the token TTL and verify a token that expires mid-session is rejected by `Authorize` on the reconnect attempt (not silently re-admitted).
- [ ] If per-RPC enforcement is required, add a gRPC auth interceptor on the `grpc.Server` and test an in-flight stream outliving its token.

## Browser longevity

- [ ] Run a multi-hour browser soak with the real application traffic profile; watch `performance.memory` / DevTools heap snapshots for growth (app-retained state dominates; the tunnel's buffers are bounded by design).
- [ ] Verify background-tab behavior: timers are throttled, so keepalive probes and reconnect backoff may be delayed until foregrounded — wire `visibilitychange`/`online` handlers if eager recovery matters.
- [ ] Verify mobile-browser sleep/wake reconnection.

## CDN / WAF / corporate networks

- [ ] Confirm the CDN/WAF supports WebSocket passthrough on the bridge path and record its connection-duration cap; align `WithSessionMaxLifetime` below it.
- [ ] Test from a representative corporate network: `wss://` on port 443, HTTP proxy traversal, TLS inspection appliances.
- [ ] Decide the degraded-mode UX for environments that strip WebSocket upgrades — **the bridge has no HTTP fallback**; the client sees a failed upgrade and the app must handle it.

## Deployment procedure

- [ ] Rehearse the drain procedure for your transport mode: native mode → `grpcServer.GracefulStop()`; handler mode → stop new upgrades, let `SessionMaxLifetime` bound the tail, then `Stop()` (**never** `GracefulStop` with active handler-mode tunnels — it panics; see CONNECTION_LIFECYCLE.md).
- [ ] Verify rollout tooling treats the reconnect blip as expected (no alert storm) and that error budgets account for `UNAVAILABLE` retries during deploys.
- [ ] Confirm observability end-to-end in production: `bridge_connections_active`, upgrade failure counters, and structured disconnect events reaching your dashboards ([DASHBOARD_QUERIES.md](../observability/DASHBOARD_QUERIES.md), [PROMETHEUS_ALERT_RULES.yaml](../../observability/PROMETHEUS_ALERT_RULES.yaml)).
