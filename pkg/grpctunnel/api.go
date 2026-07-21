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
	// GRPCOptions passes through grpc.DialOption values.
	GRPCOptions []grpc.DialOption
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
	// PingInterval configures optional server-initiated websocket ping cadence.
	PingInterval time.Duration
	// IdleTimeout configures how long the bridge waits for client activity or pong frames.
	IdleTimeout time.Duration
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
