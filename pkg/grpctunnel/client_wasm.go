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
func WithTLS(parseConfig *tls.Config) ClientOption {
	return func(parseO *clientOptions) {
		parseO.hasTunnelTLS = true
		parseO.setTunnelConfig = parseConfig
	}
}

// WithHeaders records websocket handshake headers for WASM validation.
func WithHeaders(parseHeaders http.Header) ClientOption {
	return func(parseO *clientOptions) {
		parseO.hasTunnelHeaders = true
		parseO.setTunnelHeaders = parseHeaders.Clone()
	}
}

// WithHeader records one websocket handshake header for WASM validation.
func WithHeader(parseKey string, parseValue string) ClientOption {
	return func(parseO *clientOptions) {
		parseO.hasTunnelHeaders = true
		if parseO.setTunnelHeaders == nil {
			parseO.setTunnelHeaders = make(http.Header)
		}
		parseO.setTunnelHeaders.Add(parseKey, parseValue)
	}
}

// WithSubprotocols configures websocket subprotocol negotiation for browsers.
func WithSubprotocols(parseSubprotocols ...string) ClientOption {
	return func(parseO *clientOptions) {
		parseO.setTunnelSubprotocols = append([]string{}, parseSubprotocols...)
	}
}

// WithProxy records proxy intent for WASM validation.
func WithProxy(parseProxy func(*http.Request) (*url.URL, error)) ClientOption {
	return func(parseO *clientOptions) {
		parseO.hasTunnelProxy = true
		parseO.setTunnelProxy = parseProxy
	}
}

// WithHandshakeTimeout records handshake timeout intent for WASM validation.
func WithHandshakeTimeout(parseTimeout time.Duration) ClientOption {
	return func(parseO *clientOptions) {
		parseO.hasTunnelTimeout = true
		parseO.setTunnelTimeout = parseTimeout
	}
}

// WithDialCompression records compression intent for WASM validation.
func WithDialCompression() ClientOption {
	return func(parseO *clientOptions) {
		parseO.shouldEnableCompression = true
	}
}

// WithReconnectPolicy configures optional gRPC reconnect backoff behavior.
func WithReconnectPolicy(parseConfig ReconnectConfig) ClientOption {
	return func(parseO *clientOptions) {
		parseReconnectConfig := parseConfig
		parseO.setTunnelReconnect = &parseReconnectConfig
	}
}

// splitDialOptions separates grpctunnel client options from grpc dial options.
func splitDialOptions(parseOpts []interface{}) ([]ClientOption, []grpc.DialOption, error) {
	var parseTunnelOpts []ClientOption
	var parseGrpcOpts []grpc.DialOption

	for _, parseOpt := range parseOpts {
		// Keep browser tunnel options distinct from grpc dial options so wasm
		// callers can pass either in one Dial invocation.
		switch parseTypedOption := parseOpt.(type) {
		case ClientOption:
			parseTunnelOpts = append(parseTunnelOpts, parseTypedOption)
		case grpc.DialOption:
			parseGrpcOpts = append(parseGrpcOpts, parseTypedOption)
		default:
			return nil, nil, fmt.Errorf("grpctunnel: unsupported dial option type %T", parseOpt)
		}
	}

	return parseTunnelOpts, parseGrpcOpts, nil
}

// inferBrowserWebSocketURL infers the WebSocket URL from the browser's current location.
// If target is empty or just a path, it uses window.location to build the URL.
func inferBrowserWebSocketURL(parseTarget string) string {
	// If already a full WebSocket URL, use it
	if len(parseTarget) >= 5 && parseTarget[:5] == "ws://" {
		return parseTarget
	}
	if len(parseTarget) >= 6 && parseTarget[:6] == "wss://" {
		return parseTarget
	}

	// Access window.location
	parseLocation := js.Global().Get("location")
	if !parseLocation.Truthy() {
		// Fallback if window.location not available (shouldn't happen in browser)
		if parseTarget == "" {
			return "ws://localhost:8080"
		}
		// If target looks like host:port, add ws://
		return "ws://" + parseTarget
	}

	// Determine scheme (ws or wss based on current page)
	parseProtocol := parseLocation.Get("protocol").String()
	parseScheme := "ws"
	if parseProtocol == "https:" {
		parseScheme = "wss"
	}

	// Get host (includes port if present)
	parseHost := parseLocation.Get("host").String()

	// If target is empty, connect to same host
	if parseTarget == "" {
		return fmt.Sprintf("%s://%s", parseScheme, parseHost)
	}

	// If target starts with "/", it's a path - use current host
	if len(parseTarget) > 0 && parseTarget[0] == '/' {
		return fmt.Sprintf("%s://%s%s", parseScheme, parseHost, parseTarget)
	}

	// If target is just "host:port", add scheme
	return fmt.Sprintf("%s://%s", parseScheme, parseTarget)
}

// ParseTunnelTargetURL normalizes a target into a websocket URL for WASM clients.
func ParseTunnelTargetURL(parseTarget string, shouldTunnelUseTLS bool) (string, error) {
	if shouldTunnelUseTLS {
		return "", fmt.Errorf("grpctunnel: explicit TLS flags are not supported in WASM")
	}

	parseTunnelURL := inferBrowserWebSocketURL(parseTarget)
	if strings.TrimSpace(parseTunnelURL) == "" {
		return "", fmt.Errorf("grpctunnel: inferred websocket URL is empty")
	}
	return parseTunnelURL, nil
}

// getTunnelConfigErrorWithoutTarget validates non-target TunnelConfig fields for WASM builds.
func getTunnelConfigErrorWithoutTarget(parseConfig TunnelConfig) error {
	if parseConfig.ShouldUseTLS || parseConfig.TLSConfig != nil {
		return fmt.Errorf("grpctunnel: TLSConfig/ShouldUseTLS are not supported in WASM; browser manages TLS")
	}
	if parseConfig.Headers != nil {
		return fmt.Errorf("grpctunnel: Headers are not supported in WASM; browser manages websocket headers")
	}
	if parseConfig.Proxy != nil {
		return fmt.Errorf("grpctunnel: Proxy is not supported in WASM; browser manages proxy settings")
	}
	if parseConfig.HandshakeTimeout != 0 {
		return fmt.Errorf("grpctunnel: HandshakeTimeout is not supported in WASM; use context deadlines instead")
	}
	if parseConfig.ShouldEnableCompression {
		return fmt.Errorf("grpctunnel: websocket compression is not configurable in WASM; browser manages compression negotiation")
	}
	if parseConfig.ReconnectConfig != nil {
		if parseErr := GetReconnectConfigError(*parseConfig.ReconnectConfig); parseErr != nil {
			return parseErr
		}
	}
	return nil
}

// buildTunnelTargetURL normalizes websocket target URL from TunnelConfig.
func buildTunnelTargetURL(parseConfig TunnelConfig) (string, error) {
	return ParseTunnelTargetURL(parseConfig.Target, false)
}

// GetTunnelConfigError validates TunnelConfig for WASM builds.
func GetTunnelConfigError(parseConfig TunnelConfig) error {
	if parseErr := getTunnelConfigErrorWithoutTarget(parseConfig); parseErr != nil {
		return parseErr
	}

	_, parseErr := buildTunnelTargetURL(parseConfig)
	return parseErr
}

// BuildTunnelConn creates a typed gRPC client connection over websocket transport in WASM.
func BuildTunnelConn(parseCtx context.Context, parseConfig TunnelConfig) (*grpc.ClientConn, error) {
	if parseErr := getTunnelConfigErrorWithoutTarget(parseConfig); parseErr != nil {
		return nil, parseErr
	}

	parseTunnelURL, parseErr := buildTunnelTargetURL(parseConfig)
	if parseErr != nil {
		return nil, parseErr
	}

	parseDialOptions := make([]grpc.DialOption, 0, len(parseConfig.GRPCOptions)+2)
	if parseConfig.ReconnectConfig != nil {
		parseReconnectOptions, parseErr := ApplyTunnelReconnectPolicy(nil, *parseConfig.ReconnectConfig)
		if parseErr != nil {
			return nil, parseErr
		}
		parseDialOptions = append(parseDialOptions, parseReconnectOptions...)
	}
	parseDialOptions = append(parseDialOptions, parseConfig.GRPCOptions...)
	parseDialOptions = append(parseDialOptions, dialer.NewWithConfig(parseTunnelURL, dialer.Config{
		Subprotocols: parseConfig.Subprotocols,
	}))

	return grpc.DialContext(parseCtx, buildTunnelGRPCDialTarget(parseConfig.Target, parseTunnelURL), parseDialOptions...)
}

// Dial creates a gRPC client connection over WebSocket in the browser.
//
// The target can be:
//   - Empty "" - automatically uses current page's host (ws://current-host or wss://current-host)
//   - A path "/grpc" - uses current host + path (ws://current-host/grpc)
//   - A WebSocket URL "ws://localhost:8080" or "wss://api.example.com"
//   - A host:port "localhost:8080" - adds ws:// or wss:// based on current page protocol
//
// The parseOpts list accepts both ClientOption and grpc.DialOption values.
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
func Dial(parseTarget string, parseOpts ...interface{}) (*grpc.ClientConn, error) {
	return DialContext(context.Background(), parseTarget, parseOpts...)
}

// DialContext creates a gRPC client connection over WebSocket in the browser with context.
//
// The parseOpts list accepts a mix of:
//   - ClientOption values from this package
//   - grpc.DialOption values from google.golang.org/grpc
func DialContext(parseCtx context.Context, parseTarget string, parseOpts ...interface{}) (*grpc.ClientConn, error) {
	parseTunnelOpts, parseGrpcOpts, parseErr := splitDialOptions(parseOpts)
	if parseErr != nil {
		return nil, parseErr
	}

	parseTunnelOptions := &clientOptions{}
	for _, parseTunnelOption := range parseTunnelOpts {
		parseTunnelOption(parseTunnelOptions)
	}
	if parseTunnelOptions.hasTunnelHeaders {
		return nil, fmt.Errorf("grpctunnel: Headers are not supported in WASM; browser manages websocket headers")
	}
	if parseTunnelOptions.hasTunnelProxy {
		return nil, fmt.Errorf("grpctunnel: Proxy is not supported in WASM; browser manages proxy settings")
	}
	if parseTunnelOptions.hasTunnelTimeout {
		return nil, fmt.Errorf("grpctunnel: HandshakeTimeout is not supported in WASM; use context deadlines instead")
	}

	return BuildTunnelConn(parseCtx, TunnelConfig{
		Target:                  parseTarget,
		TLSConfig:               parseTunnelOptions.setTunnelConfig,
		ShouldUseTLS:            parseTunnelOptions.hasTunnelTLS,
		Headers:                 parseTunnelOptions.setTunnelHeaders,
		Subprotocols:            parseTunnelOptions.setTunnelSubprotocols,
		Proxy:                   parseTunnelOptions.setTunnelProxy,
		HandshakeTimeout:        parseTunnelOptions.setTunnelTimeout,
		ShouldEnableCompression: parseTunnelOptions.shouldEnableCompression,
		ReconnectConfig:         parseTunnelOptions.setTunnelReconnect,
		GRPCOptions:             parseGrpcOpts,
	})
}
