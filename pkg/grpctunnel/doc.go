// Package grpctunnel provides a high-level API for running gRPC over WebSocket.
//
// Server-side entry points:
//   - BuildBridgeHandler and HandleBridgeMux for typed composition
//   - Wrap for middleware-style integration
//   - NewServer for a shutdown-capable *http.Server
//   - Serve, ListenAndServe, and ListenAndServeTLS for convenience startup
//
// Server-side hardening:
//   - WithAllowedOrigins / BuildOriginAllowlistCheck for origin allowlisting
//   - WithAuthorize for pre-upgrade request authorization (403 on failure)
//   - WithMaxActiveConnections, WithMaxConnectionsPerClient, and
//     WithMaxUpgradesPerClientPerMinute for abuse controls
//
// Client-side entry points:
//   - BuildTunnelConn for typed connection setup
//   - Dial and DialContext for gRPC client connections over WebSocket
//   - WithTLS for configuring wss:// client dialing
//
// This package is the recommended public API for most users of GoGRPCBridge.
package grpctunnel
