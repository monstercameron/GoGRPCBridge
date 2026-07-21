// Package bridge provides low-level gRPC-over-WebSocket primitives.
//
// This package exposes:
//   - Handler-based server bridging via NewHandler
//   - net.Conn adaptation for gorilla/websocket via NewWebSocketConn
//   - a client dial helper via DialOption
//
// Deprecated: prefer the higher-level pkg/grpctunnel package for new work.
// This package remains supported for existing integrations that need direct
// control over bridge handler wiring, but new features (authorization hooks,
// origin allowlisting, graceful shutdown helpers) land in pkg/grpctunnel only.
package bridge
