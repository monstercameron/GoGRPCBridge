package grpctunnel

import (
	"crypto/tls"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc"
	grpcbackoff "google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// TunnelConfig configures typed client connection creation for grpctunnel.
type TunnelConfig struct {
	// Target is the connection target. In non-WASM builds this should be a
	// host:port, :port, ws:// URL, or wss:// URL. In WASM builds it may also be
	// empty or a path (for same-origin inference). http:// and https:// URLs
	// are accepted and mapped to ws:// and wss:// respectively.
	Target string
	// TLSConfig configures the TLS settings for non-WASM websocket dialing.
	TLSConfig *tls.Config
	// ShouldUseTLS forces wss:// inference for non-WASM target normalization.
	ShouldUseTLS bool
	// Headers configures optional HTTP headers for the websocket handshake.
	Headers http.Header
	// Subprotocols configures optional websocket subprotocol negotiation.
	Subprotocols []string
	// Proxy configures an optional proxy selector for non-WASM websocket dialing.
	Proxy func(*http.Request) (*url.URL, error)
	// HandshakeTimeout limits websocket handshake duration for non-WASM clients.
	HandshakeTimeout time.Duration
	// ShouldEnableCompression enables websocket per-message compression where supported.
	ShouldEnableCompression bool
	// ReconnectConfig configures optional gRPC reconnect and backoff behavior.
	ReconnectConfig *ReconnectConfig
	// KeepaliveConfig configures optional client-side gRPC keepalive probing
	// over the tunnel, which detects silently dead connections (NAT resets,
	// dropped networks) and triggers automatic reconnection.
	KeepaliveConfig *KeepaliveConfig
	// GRPCOptions passes through grpc.DialOption values.
	GRPCOptions []grpc.DialOption
}

// KeepaliveConfig configures client-side gRPC keepalive over the tunnel.
type KeepaliveConfig struct {
	// Interval is how often to probe an idle connection with HTTP/2 pings.
	// gRPC enforces a 10s minimum. Zero uses 30s.
	Interval time.Duration
	// Timeout is how long to wait for a ping ack before declaring the
	// connection dead and reconnecting. Zero uses 20s.
	Timeout time.Duration
}

// BridgeConfig configures typed server handler creation for grpctunnel.
type BridgeConfig struct {
	// CheckOrigin validates websocket upgrade origins.
	// If nil, gorilla/websocket applies its default same-origin policy.
	// See BuildOriginAllowlistCheck for a ready-made allowlist policy.
	CheckOrigin func(r *http.Request) bool
	// Authorize validates a request before the websocket upgrade is attempted.
	// A non-nil returned error rejects the request with 403 Forbidden before
	// any websocket or gRPC resources are allocated. Use this for token or
	// cookie checks on the upgrade request. Nil disables the hook.
	Authorize func(r *http.Request) error
	// ReadBufferSize configures websocket read buffer size. Zero uses defaults.
	ReadBufferSize int
	// WriteBufferSize configures websocket write buffer size. Zero uses defaults.
	WriteBufferSize int
	// ReadLimitBytes configures an optional websocket read limit.
	// Zero applies a secure default limit.
	ReadLimitBytes int64
	// ShouldDisableReadLimit disables websocket read-size limiting.
	// Use this only when an upstream boundary enforces strict payload limits.
	ShouldDisableReadLimit bool
	// PingInterval configures server-initiated websocket ping cadence.
	// When PingInterval and IdleTimeout are both zero and keepalive is not
	// disabled, secure defaults apply (30s ping, 120s idle timeout) so dead
	// peers cannot pin server resources indefinitely.
	PingInterval time.Duration
	// IdleTimeout configures how long the bridge waits for client activity or
	// pong frames before closing the connection. See PingInterval for defaults.
	IdleTimeout time.Duration
	// ShouldDisableKeepalive turns off the default server keepalive probing.
	// Without keepalive, silently dropped clients hold connection slots until
	// the OS TCP timeout; disable only when an upstream boundary owns liveness.
	ShouldDisableKeepalive bool
	// SessionMaxLifetime force-closes tunnel sessions after this duration,
	// bounding how long a connection can outlive its upgrade-time
	// authorization (token expiry) and aligning with reverse-proxy
	// maximum-connection lifetimes. Clients reconnect automatically and
	// re-pass the Authorize hook. Zero disables the bound.
	SessionMaxLifetime time.Duration
	// ShouldUseNativeGRPCTransport serves tunneled sessions through
	// grpc.Server.Serve and gRPC's own HTTP/2 transport instead of the
	// net/http handler path. This is significantly cheaper per RPC (fewer
	// allocations, native flow control, server-side gRPC keepalive support)
	// with two tradeoffs: upgrade-request headers are not forwarded into
	// per-RPC metadata, and the grpc.Server must not have transport
	// credentials configured (TLS belongs on the websocket listener).
	ShouldUseNativeGRPCTransport bool
	// ShouldEnableCompression enables websocket per-message compression where supported.
	ShouldEnableCompression bool
	// MaxActiveConnections limits total concurrent websocket tunnel connections.
	// Zero disables this guard.
	MaxActiveConnections int
	// MaxConnectionsPerClient limits concurrent websocket tunnel connections per client key.
	// Client key is derived from request remote address host. Zero disables this guard.
	MaxConnectionsPerClient int
	// MaxUpgradesPerClientPerMinute limits websocket upgrade attempts per client key over a 1-minute window.
	// Zero disables this guard.
	MaxUpgradesPerClientPerMinute int
	// OnConnect is called when a websocket client connects.
	OnConnect func(r *http.Request)
	// OnDisconnect is called when a websocket client disconnects.
	OnDisconnect func(r *http.Request)
}

// ReconnectConfig configures optional gRPC reconnect backoff behavior.
type ReconnectConfig struct {
	// InitialDelay configures the first reconnect delay. Zero uses gRPC defaults.
	InitialDelay time.Duration
	// MaxDelay configures the maximum reconnect delay. Zero uses gRPC defaults.
	MaxDelay time.Duration
	// Multiplier configures exponential backoff growth. Zero uses gRPC defaults.
	Multiplier float64
	// Jitter configures reconnect jitter. Zero uses gRPC defaults.
	Jitter float64
	// MinConnectTimeout configures the minimum connection timeout. Zero uses gRPC defaults.
	MinConnectTimeout time.Duration
}

// ToolingConfig configures the optional direct gRPC tooling server helpers.
type ToolingConfig struct {
	// ShouldEnableReflection registers the gRPC reflection service when absent.
	ShouldEnableReflection bool
	// ShouldEnableHealthService registers the standard gRPC health service when absent.
	ShouldEnableHealthService bool
	// ShouldEnablePprof exposes net/http/pprof handlers under DebugPathPrefix.
	ShouldEnablePprof bool
	// DebugPathPrefix configures the pprof route prefix. Empty uses /debug/pprof/.
	DebugPathPrefix string
}

// BuildOriginAllowlistCheck returns a CheckOrigin function that allows requests
// whose Origin header matches one of the given origins.
//
// Matching rules:
//   - Origins compare case-insensitively on scheme://host[:port].
//   - "*" allows every origin.
//   - A leading "*." in the host allows any subdomain, e.g.
//     "https://*.example.com" matches "https://app.example.com".
//   - Requests without an Origin header (non-browser clients) are allowed,
//     matching the conventional browser-only scope of origin policies.
//
// Use with BridgeConfig.CheckOrigin or the WithAllowedOrigins server option.
func BuildOriginAllowlistCheck(allowedOrigins ...string) func(r *http.Request) bool {
	normalized := make([]string, 0, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origin = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(origin, "/")))
		if origin != "" {
			normalized = append(normalized, origin)
		}
	}

	return func(r *http.Request) bool {
		if r == nil {
			return false
		}
		requestOrigin := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(r.Header.Get("Origin"), "/")))
		if requestOrigin == "" {
			return true
		}
		for _, allowed := range normalized {
			if allowed == "*" || allowed == requestOrigin {
				return true
			}
			if scheme, hostSuffix, ok := splitOriginWildcard(allowed); ok {
				if strings.HasPrefix(requestOrigin, scheme+"://") && strings.HasSuffix(requestOrigin, hostSuffix) {
					return true
				}
			}
		}
		return false
	}
}

// splitOriginWildcard decomposes an "scheme://*.host" allowlist entry into its
// scheme and required host suffix (including the leading dot).
func splitOriginWildcard(allowedOrigin string) (scheme string, hostSuffix string, ok bool) {
	scheme, host, found := strings.Cut(allowedOrigin, "://")
	if !found || !strings.HasPrefix(host, "*.") {
		return "", "", false
	}
	return scheme, host[1:], true
}

// ApplyTunnelInsecureCredentials appends insecure transport credentials to the
// provided grpc dial option slice and returns the resulting slice.
func ApplyTunnelInsecureCredentials(dialOptions []grpc.DialOption) []grpc.DialOption {
	result := append([]grpc.DialOption{}, dialOptions...)
	result = append(result, grpc.WithTransportCredentials(insecure.NewCredentials()))
	return result
}

// GetReconnectConfigError validates optional reconnect policy settings.
func GetReconnectConfigError(cfg ReconnectConfig) error {
	if cfg.InitialDelay < 0 {
		return fmt.Errorf("grpctunnel: reconnect InitialDelay must be >= 0")
	}
	if cfg.MaxDelay < 0 {
		return fmt.Errorf("grpctunnel: reconnect MaxDelay must be >= 0")
	}
	if cfg.MinConnectTimeout < 0 {
		return fmt.Errorf("grpctunnel: reconnect MinConnectTimeout must be >= 0")
	}
	if cfg.Multiplier < 0 {
		return fmt.Errorf("grpctunnel: reconnect Multiplier must be >= 0")
	}
	if math.IsNaN(cfg.Multiplier) || math.IsInf(cfg.Multiplier, 0) {
		return fmt.Errorf("grpctunnel: reconnect Multiplier must be finite")
	}
	if cfg.Jitter < 0 {
		return fmt.Errorf("grpctunnel: reconnect Jitter must be >= 0")
	}
	if math.IsNaN(cfg.Jitter) || math.IsInf(cfg.Jitter, 0) {
		return fmt.Errorf("grpctunnel: reconnect Jitter must be finite")
	}
	return nil
}

// GetKeepaliveConfigError validates optional client keepalive settings.
func GetKeepaliveConfigError(cfg KeepaliveConfig) error {
	if cfg.Interval < 0 {
		return fmt.Errorf("grpctunnel: keepalive Interval must be >= 0")
	}
	if cfg.Timeout < 0 {
		return fmt.Errorf("grpctunnel: keepalive Timeout must be >= 0")
	}
	return nil
}

// ApplyTunnelKeepalivePolicy appends client keepalive dial options onto an
// option slice. Probing runs even without active streams so idle tunnels
// detect dead connections and reconnect.
func ApplyTunnelKeepalivePolicy(dialOptions []grpc.DialOption, cfg KeepaliveConfig) ([]grpc.DialOption, error) {
	if err := GetKeepaliveConfigError(cfg); err != nil {
		return nil, err
	}

	interval := cfg.Interval
	if interval == 0 {
		interval = 30 * time.Second
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}

	result := append([]grpc.DialOption{}, dialOptions...)
	result = append(result, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                interval,
		Timeout:             timeout,
		PermitWithoutStream: true,
	}))
	return result, nil
}

// ApplyTunnelReconnectPolicy appends reconnect dial options onto an option slice.
func ApplyTunnelReconnectPolicy(dialOptions []grpc.DialOption, cfg ReconnectConfig) ([]grpc.DialOption, error) {
	if err := GetReconnectConfigError(cfg); err != nil {
		return nil, err
	}

	backoffConfig := grpcbackoff.DefaultConfig
	if cfg.InitialDelay > 0 {
		backoffConfig.BaseDelay = cfg.InitialDelay
	}
	if cfg.MaxDelay > 0 {
		backoffConfig.MaxDelay = cfg.MaxDelay
	}
	if cfg.Multiplier > 0 {
		backoffConfig.Multiplier = cfg.Multiplier
	}
	if cfg.Jitter > 0 {
		backoffConfig.Jitter = cfg.Jitter
	}

	connectParams := grpc.ConnectParams{
		Backoff: backoffConfig,
	}
	if cfg.MinConnectTimeout > 0 {
		connectParams.MinConnectTimeout = cfg.MinConnectTimeout
	}

	result := append([]grpc.DialOption{}, dialOptions...)
	result = append(result, grpc.WithConnectParams(connectParams))
	return result, nil
}

// buildTunnelGRPCDialTarget normalizes gRPC dial target values for custom websocket dialers.
func buildTunnelGRPCDialTarget(target string, tunnelURL string) string {
	trimmedTarget := strings.TrimSpace(target)
	if trimmedTarget == "" {
		return tunnelURL
	}

	targetURL, err := url.Parse(trimmedTarget)
	if err == nil && (targetURL.Scheme == "ws" || targetURL.Scheme == "wss") && targetURL.Host != "" {
		return targetURL.Host
	}

	return trimmedTarget
}
