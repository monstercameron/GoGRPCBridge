//go:build js && wasm

package grpctunnel

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"syscall/js"
	"time"

	"github.com/monstercameron/GoGRPCBridge/pkg/wasm/dialer"

	"google.golang.org/grpc"
)

// ClientOption configures browser websocket dialing behavior.
type ClientOption func(*clientOptions)

type clientOptions struct {
	hasTunnelHeaders        bool
	hasTunnelProxy          bool
	hasTunnelTimeout        bool
	hasTunnelTLS            bool
	setTunnelConfig         *tls.Config
	setTunnelHeaders        http.Header
	setTunnelSubprotocols   []string
	setTunnelProxy          func(*http.Request) (*url.URL, error)
	setTunnelReconnect      *ReconnectConfig
	setTunnelTimeout        time.Duration
	shouldEnableCompression bool
}

// WithTLS records TLS intent in WASM builds.
// BuildTunnelConn and Dial/DialContext reject this option because browser TLS
// is controlled by the user agent, not Go TLS configuration.
func WithTLS(cfg *tls.Config) ClientOption {
	return func(o *clientOptions) {
		o.hasTunnelTLS = true
		o.setTunnelConfig = cfg
	}
}

// WithHeaders records websocket handshake headers for WASM validation.
func WithHeaders(headers http.Header) ClientOption {
	return func(o *clientOptions) {
		o.hasTunnelHeaders = true
		o.setTunnelHeaders = headers.Clone()
	}
}

// WithHeader records one websocket handshake header for WASM validation.
func WithHeader(key string, value string) ClientOption {
	return func(o *clientOptions) {
		o.hasTunnelHeaders = true
		if o.setTunnelHeaders == nil {
			o.setTunnelHeaders = make(http.Header)
		}
		o.setTunnelHeaders.Add(key, value)
	}
}

// WithSubprotocols configures websocket subprotocol negotiation for browsers.
func WithSubprotocols(subprotocols ...string) ClientOption {
	return func(o *clientOptions) {
		o.setTunnelSubprotocols = append([]string{}, subprotocols...)
	}
}

// WithProxy records proxy intent for WASM validation.
func WithProxy(proxy func(*http.Request) (*url.URL, error)) ClientOption {
	return func(o *clientOptions) {
		o.hasTunnelProxy = true
		o.setTunnelProxy = proxy
	}
}

// WithHandshakeTimeout records handshake timeout intent for WASM validation.
func WithHandshakeTimeout(timeout time.Duration) ClientOption {
	return func(o *clientOptions) {
		o.hasTunnelTimeout = true
		o.setTunnelTimeout = timeout
	}
}

// WithDialCompression records compression intent for WASM validation.
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

// splitDialOptions separates grpctunnel client options from grpc dial options.
func splitDialOptions(opts []interface{}) ([]ClientOption, []grpc.DialOption, error) {
	var tunnelOpts []ClientOption
	var grpcOpts []grpc.DialOption

	for _, opt := range opts {
		// Keep browser tunnel options distinct from grpc dial options so wasm
		// callers can pass either in one Dial invocation.
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

// inferBrowserWebSocketURL infers the WebSocket URL from the browser's current location.
// If target is empty or just a path, it uses window.location to build the URL.
func inferBrowserWebSocketURL(target string) string {
	target = mapHTTPSchemeToWebSocket(target)

	// If already a full WebSocket URL, use it
	if strings.HasPrefix(target, "ws://") || strings.HasPrefix(target, "wss://") {
		return target
	}

	// Access window.location
	location := js.Global().Get("location")
	if !location.Truthy() {
		// Fallback if window.location not available (shouldn't happen in browser)
		if target == "" {
			return "ws://localhost:8080"
		}
		// If target looks like host:port, add ws://
		return "ws://" + target
	}

	// Determine scheme (ws or wss based on current page)
	protocol := location.Get("protocol").String()
	scheme := "ws"
	if protocol == "https:" {
		scheme = "wss"
	}

	// Get host (includes port if present)
	host := location.Get("host").String()

	// If target is empty, connect to same host
	if target == "" {
		return fmt.Sprintf("%s://%s", scheme, host)
	}

	// If target starts with "/", it's a path - use current host
	if target[0] == '/' {
		return fmt.Sprintf("%s://%s%s", scheme, host, target)
	}

	// If target is just "host:port", add scheme
	return fmt.Sprintf("%s://%s", scheme, target)
}

// ParseTunnelTargetURL normalizes a target into a websocket URL for WASM clients.
// Accepted forms: empty (same-origin), a path, host:port, ws:// or wss:// URLs,
// and http:// or https:// URLs (mapped to ws:// and wss:// respectively).
func ParseTunnelTargetURL(target string, shouldTunnelUseTLS bool) (string, error) {
	if shouldTunnelUseTLS {
		return "", fmt.Errorf("grpctunnel: explicit TLS flags are not supported in WASM")
	}

	target = strings.TrimSpace(target)
	mapped := mapHTTPSchemeToWebSocket(target)
	if strings.Contains(mapped, "://") &&
		!strings.HasPrefix(mapped, "ws://") &&
		!strings.HasPrefix(mapped, "wss://") {
		return "", fmt.Errorf("grpctunnel: unsupported target scheme in %q", target)
	}

	tunnelURL := inferBrowserWebSocketURL(target)
	if strings.TrimSpace(tunnelURL) == "" {
		return "", fmt.Errorf("grpctunnel: inferred websocket URL is empty")
	}
	return tunnelURL, nil
}

// getTunnelConfigErrorWithoutTarget validates non-target TunnelConfig fields for WASM builds.
func getTunnelConfigErrorWithoutTarget(cfg TunnelConfig) error {
	if cfg.ShouldUseTLS || cfg.TLSConfig != nil {
		return fmt.Errorf("grpctunnel: TLSConfig/ShouldUseTLS are not supported in WASM; browser manages TLS")
	}
	if cfg.Headers != nil {
		return fmt.Errorf("grpctunnel: Headers are not supported in WASM; browser manages websocket headers")
	}
	if cfg.Proxy != nil {
		return fmt.Errorf("grpctunnel: Proxy is not supported in WASM; browser manages proxy settings")
	}
	if cfg.HandshakeTimeout != 0 {
		return fmt.Errorf("grpctunnel: HandshakeTimeout is not supported in WASM; use context deadlines instead")
	}
	if cfg.ShouldEnableCompression {
		return fmt.Errorf("grpctunnel: websocket compression is not configurable in WASM; browser manages compression negotiation")
	}
	if cfg.ReconnectConfig != nil {
		if err := GetReconnectConfigError(*cfg.ReconnectConfig); err != nil {
			return err
		}
	}
	return nil
}

// buildTunnelTargetURL normalizes websocket target URL from TunnelConfig.
func buildTunnelTargetURL(cfg TunnelConfig) (string, error) {
	return ParseTunnelTargetURL(cfg.Target, false)
}

// GetTunnelConfigError validates TunnelConfig for WASM builds.
func GetTunnelConfigError(cfg TunnelConfig) error {
	if err := getTunnelConfigErrorWithoutTarget(cfg); err != nil {
		return err
	}

	_, err := buildTunnelTargetURL(cfg)
	return err
}

// BuildTunnelConn creates a typed gRPC client connection over websocket transport in WASM.
func BuildTunnelConn(ctx context.Context, cfg TunnelConfig) (*grpc.ClientConn, error) {
	if err := getTunnelConfigErrorWithoutTarget(cfg); err != nil {
		return nil, err
	}

	tunnelURL, err := buildTunnelTargetURL(cfg)
	if err != nil {
		return nil, err
	}

	dialOptions := make([]grpc.DialOption, 0, len(cfg.GRPCOptions)+2)
	if cfg.ReconnectConfig != nil {
		reconnectOptions, err := ApplyTunnelReconnectPolicy(nil, *cfg.ReconnectConfig)
		if err != nil {
			return nil, err
		}
		dialOptions = append(dialOptions, reconnectOptions...)
	}
	dialOptions = append(dialOptions, cfg.GRPCOptions...)
	dialOptions = append(dialOptions, dialer.NewWithConfig(tunnelURL, dialer.Config{
		Subprotocols: cfg.Subprotocols,
	}))

	return grpc.DialContext(ctx, buildTunnelGRPCDialTarget(cfg.Target, tunnelURL), dialOptions...)
}

// Dial creates a gRPC client connection over WebSocket in the browser.
//
// The target can be:
//   - Empty "" - automatically uses current page's host (ws://current-host or wss://current-host)
//   - A path "/grpc" - uses current host + path (ws://current-host/grpc)
//   - A WebSocket URL "ws://localhost:8080" or "wss://api.example.com"
//   - An HTTP URL "https://api.example.com" (mapped to wss://)
//   - A host:port "localhost:8080" - adds ws:// or wss:// based on current page protocol
//
// The opts list accepts both ClientOption and grpc.DialOption values.
// Any other option type returns an error.
//
// Example (automatic):
//
//	// On page https://example.com, automatically connects to wss://example.com
//	conn, err := grpctunnel.Dial("",
//	    grpc.WithTransportCredentials(insecure.NewCredentials()),
//	)
//
// Example (explicit):
//
//	conn, err := grpctunnel.Dial("ws://localhost:8080",
//	    grpc.WithTransportCredentials(insecure.NewCredentials()),
//	)
func Dial(target string, opts ...interface{}) (*grpc.ClientConn, error) {
	return DialContext(context.Background(), target, opts...)
}

// DialContext creates a gRPC client connection over WebSocket in the browser with context.
//
// The opts list accepts a mix of:
//   - ClientOption values from this package
//   - grpc.DialOption values from google.golang.org/grpc
func DialContext(ctx context.Context, target string, opts ...interface{}) (*grpc.ClientConn, error) {
	tunnelOpts, grpcOpts, err := splitDialOptions(opts)
	if err != nil {
		return nil, err
	}

	tunnelOptions := &clientOptions{}
	for _, tunnelOption := range tunnelOpts {
		tunnelOption(tunnelOptions)
	}
	if tunnelOptions.hasTunnelHeaders {
		return nil, fmt.Errorf("grpctunnel: Headers are not supported in WASM; browser manages websocket headers")
	}
	if tunnelOptions.hasTunnelProxy {
		return nil, fmt.Errorf("grpctunnel: Proxy is not supported in WASM; browser manages proxy settings")
	}
	if tunnelOptions.hasTunnelTimeout {
		return nil, fmt.Errorf("grpctunnel: HandshakeTimeout is not supported in WASM; use context deadlines instead")
	}

	return BuildTunnelConn(ctx, TunnelConfig{
		Target:                  target,
		TLSConfig:               tunnelOptions.setTunnelConfig,
		ShouldUseTLS:            tunnelOptions.hasTunnelTLS,
		Headers:                 tunnelOptions.setTunnelHeaders,
		Subprotocols:            tunnelOptions.setTunnelSubprotocols,
		Proxy:                   tunnelOptions.setTunnelProxy,
		HandshakeTimeout:        tunnelOptions.setTunnelTimeout,
		ShouldEnableCompression: tunnelOptions.shouldEnableCompression,
		ReconnectConfig:         tunnelOptions.setTunnelReconnect,
		GRPCOptions:             grpcOpts,
	})
}
