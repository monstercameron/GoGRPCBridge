//go:build !js && !wasm

//lint:file-ignore SA1019 grpc.DialContext is retained to preserve 1.x dial semantics and WithBlock behavior.

package grpctunnel

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
)

// ClientOption configures the WebSocket client behavior.
type ClientOption func(*clientOptions)

type clientOptions struct {
	tlsConfig               *tls.Config
	setTunnelHeaders        http.Header
	setTunnelSubprotocols   []string
	setTunnelProxy          func(*http.Request) (*url.URL, error)
	setTunnelReconnect      *ReconnectConfig
	setTunnelKeepalive      *KeepaliveConfig
	setTunnelTimeout        time.Duration
	isUseTLS                bool
	shouldEnableCompression bool
}

// WithTLS enables secure WebSocket connections (wss://).
// This sets the TLS configuration for the WebSocket dialer.
// Passing nil still forces wss:// URL inference while using default TLS settings.
//
// Example:
//
//	conn, _ := grpctunnel.Dial("api.example.com:443",
//	    grpctunnel.WithTLS(nil), // wss:// with default TLS verification
//	)
func WithTLS(cfg *tls.Config) ClientOption {
	return func(o *clientOptions) {
		o.isUseTLS = true
		o.tlsConfig = cfg
	}
}

// WithHeaders configures websocket handshake headers.
func WithHeaders(headers http.Header) ClientOption {
	return func(o *clientOptions) {
		o.setTunnelHeaders = headers.Clone()
	}
}

// WithHeader appends one websocket handshake header value.
func WithHeader(key string, value string) ClientOption {
	return func(o *clientOptions) {
		if o.setTunnelHeaders == nil {
			o.setTunnelHeaders = make(http.Header)
		}
		o.setTunnelHeaders.Add(key, value)
	}
}

// WithSubprotocols configures websocket subprotocol negotiation.
func WithSubprotocols(subprotocols ...string) ClientOption {
	return func(o *clientOptions) {
		o.setTunnelSubprotocols = append([]string{}, subprotocols...)
	}
}

// WithProxy configures a proxy selector for websocket dialing.
func WithProxy(proxy func(*http.Request) (*url.URL, error)) ClientOption {
	return func(o *clientOptions) {
		o.setTunnelProxy = proxy
	}
}

// WithHandshakeTimeout configures the websocket handshake timeout.
func WithHandshakeTimeout(timeout time.Duration) ClientOption {
	return func(o *clientOptions) {
		o.setTunnelTimeout = timeout
	}
}

// WithDialCompression enables websocket per-message compression for client dialing.
func WithDialCompression() ClientOption {
	return func(o *clientOptions) {
		o.shouldEnableCompression = true
	}
}

// WithReconnectPolicy configures optional gRPC reconnect backoff behavior.
func WithReconnectPolicy(cfg ReconnectConfig) ClientOption {
	return func(o *clientOptions) {
		reconnectConfig := cfg
		o.setTunnelReconnect = &reconnectConfig
	}
}

// WithTunnelKeepalive enables client-side gRPC keepalive probing over the
// tunnel. Probes detect silently dead connections (NAT resets, dropped
// networks) and trigger automatic reconnection even with no active streams.
// interval zero uses 30s; timeout zero uses 20s. gRPC enforces a 10s minimum
// probe interval.
func WithTunnelKeepalive(interval time.Duration, timeout time.Duration) ClientOption {
	return func(o *clientOptions) {
		o.setTunnelKeepalive = &KeepaliveConfig{Interval: interval, Timeout: timeout}
	}
}

// splitDialOptions separates grpctunnel client options from grpc dial options.
func splitDialOptions(opts []interface{}) ([]ClientOption, []grpc.DialOption, error) {
	var tunnelOpts []ClientOption
	var grpcOpts []grpc.DialOption

	for _, opt := range opts {
		// Keep tunnel-specific options separate so they affect websocket dialing,
		// while grpc options continue through to grpc.DialContext untouched.
		switch typedOption := opt.(type) {
		case ClientOption:
			tunnelOpts = append(tunnelOpts, typedOption)
		case grpc.DialOption:
			grpcOpts = append(grpcOpts, typedOption)
		default:
			return nil, nil, fmt.Errorf("grpctunnel: unsupported dial option type %T", opt)
		}
	}

	return tunnelOpts, grpcOpts, nil
}

// mapHTTPSchemeToWebSocket rewrites http:// and https:// target prefixes to
// their websocket equivalents so callers can pass plain service URLs.
func mapHTTPSchemeToWebSocket(target string) string {
	if rest, ok := strings.CutPrefix(target, "https://"); ok {
		return "wss://" + rest
	}
	if rest, ok := strings.CutPrefix(target, "http://"); ok {
		return "ws://" + rest
	}
	return target
}

// ParseTunnelTargetURL normalizes a tunnel target into a websocket URL.
// Accepted forms: host:port, :port, ws:// or wss:// URLs, and http:// or
// https:// URLs (mapped to ws:// and wss:// respectively).
func ParseTunnelTargetURL(target string, shouldTunnelUseTLS bool) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("grpctunnel: target is required")
	}

	target = mapHTTPSchemeToWebSocket(target)

	if strings.Contains(target, "://") &&
		!strings.HasPrefix(target, "ws://") &&
		!strings.HasPrefix(target, "wss://") {
		targetURL, err := url.Parse(target)
		if err != nil {
			return "", fmt.Errorf("grpctunnel: invalid target %q: %w", target, err)
		}
		return "", fmt.Errorf("grpctunnel: unsupported target scheme %q", targetURL.Scheme)
	}

	if strings.HasPrefix(target, ":") {
		target = "localhost" + target
	}

	if !strings.HasPrefix(target, "ws://") && !strings.HasPrefix(target, "wss://") {
		scheme := "ws"
		if shouldTunnelUseTLS {
			scheme = "wss"
		}
		target = scheme + "://" + target
	}

	targetURL, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("grpctunnel: invalid target %q: %w", target, err)
	}
	if targetURL.Scheme != "ws" && targetURL.Scheme != "wss" {
		return "", fmt.Errorf("grpctunnel: unsupported target scheme %q", targetURL.Scheme)
	}
	if targetURL.Host == "" {
		return "", fmt.Errorf("grpctunnel: target host is required")
	}
	return targetURL.String(), nil
}

// inferWebSocketURL converts a target address to a WebSocket URL.
// It keeps compatibility with legacy tests and wrappers.
func inferWebSocketURL(target string, isUseTLS bool) string {
	result, err := ParseTunnelTargetURL(target, isUseTLS)
	if err != nil {
		return target
	}
	return result
}

// buildTunnelDialer creates a custom gRPC dialer that establishes WebSocket connections.
func buildTunnelDialer(cfg TunnelConfig) func(context.Context, string) (net.Conn, error) {
	targetURL, targetErr := url.Parse(cfg.Target)
	if targetErr != nil {
		return func(context.Context, string) (net.Conn, error) {
			return nil, targetErr
		}
	}

	// Reuse parsed target and dialer configuration for each reconnect attempt.
	dialURL := targetURL.String()
	headersTemplate := http.Header(nil)
	if cfg.Headers != nil {
		headersTemplate = cfg.Headers.Clone()
	}
	dialer := websocket.Dialer{
		TLSClientConfig:   cfg.TLSConfig,
		Subprotocols:      append([]string{}, cfg.Subprotocols...),
		Proxy:             cfg.Proxy,
		HandshakeTimeout:  cfg.HandshakeTimeout,
		WriteBufferPool:   buildWebSocketWriteBufferPool(defaultWebSocketBufferSize),
		EnableCompression: cfg.ShouldEnableCompression,
	}

	return func(ctx context.Context, addr string) (net.Conn, error) {
		ws, _, err := dialer.DialContext(ctx, dialURL, headersTemplate)
		if err != nil {
			return nil, err
		}
		return newWebSocketConn(ws), nil
	}
}

// getTunnelConfigErrorWithoutTarget validates non-target TunnelConfig fields for non-WASM builds.
func getTunnelConfigErrorWithoutTarget(cfg TunnelConfig) error {
	if cfg.HandshakeTimeout < 0 {
		return fmt.Errorf("grpctunnel: HandshakeTimeout must be >= 0")
	}
	if cfg.ReconnectConfig != nil {
		if err := GetReconnectConfigError(*cfg.ReconnectConfig); err != nil {
			return err
		}
	}
	if cfg.KeepaliveConfig != nil {
		if err := GetKeepaliveConfigError(*cfg.KeepaliveConfig); err != nil {
			return err
		}
	}
	return nil
}

// buildTunnelTargetURL normalizes websocket target URL from TunnelConfig.
func buildTunnelTargetURL(cfg TunnelConfig) (string, error) {
	shouldTunnelUseTLS := cfg.ShouldUseTLS || cfg.TLSConfig != nil
	return ParseTunnelTargetURL(cfg.Target, shouldTunnelUseTLS)
}

// GetTunnelConfigError validates TunnelConfig for non-WASM builds.
func GetTunnelConfigError(cfg TunnelConfig) error {
	if err := getTunnelConfigErrorWithoutTarget(cfg); err != nil {
		return err
	}

	_, err := buildTunnelTargetURL(cfg)
	return err
}

// BuildTunnelConn creates a typed gRPC client connection over websocket transport.
func BuildTunnelConn(ctx context.Context, cfg TunnelConfig) (*grpc.ClientConn, error) {
	if err := getTunnelConfigErrorWithoutTarget(cfg); err != nil {
		return nil, err
	}

	tunnelURL, err := buildTunnelTargetURL(cfg)
	if err != nil {
		return nil, err
	}

	dialOptions := make([]grpc.DialOption, 0, len(cfg.GRPCOptions)+3)
	if cfg.ReconnectConfig != nil {
		reconnectOptions, err := ApplyTunnelReconnectPolicy(nil, *cfg.ReconnectConfig)
		if err != nil {
			return nil, err
		}
		dialOptions = append(dialOptions, reconnectOptions...)
	}
	if cfg.KeepaliveConfig != nil {
		keepaliveOptions, err := ApplyTunnelKeepalivePolicy(nil, *cfg.KeepaliveConfig)
		if err != nil {
			return nil, err
		}
		dialOptions = append(dialOptions, keepaliveOptions...)
	}
	dialOptions = append(dialOptions, cfg.GRPCOptions...)
	dialOptions = append(dialOptions, grpc.WithContextDialer(buildTunnelDialer(TunnelConfig{
		Target:                  tunnelURL,
		TLSConfig:               cfg.TLSConfig,
		Headers:                 cfg.Headers,
		Subprotocols:            cfg.Subprotocols,
		Proxy:                   cfg.Proxy,
		HandshakeTimeout:        cfg.HandshakeTimeout,
		ShouldEnableCompression: cfg.ShouldEnableCompression,
	})))

	return grpc.DialContext(ctx, buildTunnelGRPCDialTarget(cfg.Target, tunnelURL), dialOptions...)
}

// Dial creates a gRPC client connection over WebSocket.
// The target can be:
//   - A WebSocket URL: "ws://localhost:8080" or "wss://api.example.com"
//   - An HTTP URL: "https://api.example.com" (mapped to wss://)
//   - A host:port: "localhost:8080" (infers ws://)
//   - A port: ":8080" (infers ws://localhost:8080)
//
// Additional options can include grpctunnel client options (e.g., WithTLS)
// and grpc.DialOption values (credentials, interceptors, etc.).
// Any option value that is neither ClientOption nor grpc.DialOption
// returns an error from Dial/DialContext.
//
// Example:
//
//	conn, err := grpctunnel.Dial("localhost:8080",
//	    grpc.WithTransportCredentials(insecure.NewCredentials()),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer conn.Close()
//
//	client := proto.NewYourServiceClient(conn)
func Dial(target string, opts ...interface{}) (*grpc.ClientConn, error) {
	return DialContext(context.Background(), target, opts...)
}

// DialContext creates a gRPC client connection over WebSocket with context.
// This is the context-aware version of Dial.
//
// The opts list accepts a mix of:
//   - ClientOption values from this package (for tunnel behavior)
//   - grpc.DialOption values from google.golang.org/grpc
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//
//	conn, err := grpctunnel.DialContext(ctx, "localhost:8080",
//	    grpc.WithTransportCredentials(insecure.NewCredentials()),
//	)
func DialContext(ctx context.Context, target string, opts ...interface{}) (*grpc.ClientConn, error) {
	tunnelOpts, grpcOpts, err := splitDialOptions(opts)
	if err != nil {
		return nil, err
	}

	tunnelOptions := &clientOptions{}
	for _, tunnelOption := range tunnelOpts {
		tunnelOption(tunnelOptions)
	}

	return BuildTunnelConn(ctx, TunnelConfig{
		Target:                  target,
		TLSConfig:               tunnelOptions.tlsConfig,
		ShouldUseTLS:            tunnelOptions.isUseTLS,
		Headers:                 tunnelOptions.setTunnelHeaders,
		Subprotocols:            tunnelOptions.setTunnelSubprotocols,
		Proxy:                   tunnelOptions.setTunnelProxy,
		HandshakeTimeout:        tunnelOptions.setTunnelTimeout,
		ShouldEnableCompression: tunnelOptions.shouldEnableCompression,
		ReconnectConfig:         tunnelOptions.setTunnelReconnect,
		KeepaliveConfig:         tunnelOptions.setTunnelKeepalive,
		GRPCOptions:             grpcOpts,
	})
}
