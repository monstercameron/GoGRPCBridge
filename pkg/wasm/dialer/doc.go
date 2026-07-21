//go:build js && wasm

// Package dialer provides browser-specific gRPC dial integration for WebAssembly.
//
// The package adapts the browser WebSocket API to net.Conn so gRPC can run
// over WebSocket transport from Go/WASM clients. Most applications should use
// the higher-level pkg/grpctunnel Dial/BuildTunnelConn API instead; use this
// package directly only when composing grpc.DialOption values yourself:
//
//	conn, err := grpc.DialContext(ctx, "bridge",
//	    dialer.New("wss://api.example.com/grpc"),
//	    grpc.WithTransportCredentials(insecure.NewCredentials()),
//	)
//
// Implementation notes:
//   - Inbound messages are queued with hard bounds (256 messages / 16 MB) so a
//     slow reader cannot grow browser memory without limit; overflow closes
//     the socket with an error rather than buffering indefinitely.
//   - JavaScript event callbacks never block: signals are edge-triggered and
//     error delivery is best-effort, keeping the JS event loop responsive.
//   - Deadlines are no-ops because browser WebSockets expose no deadline
//     controls; use context cancellation on the dial and per-RPC deadlines.
package dialer
