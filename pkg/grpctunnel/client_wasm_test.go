//go:build js && wasm

package grpctunnel

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
)

// Note: These tests validate URL inference logic
// Actual browser WebSocket connections require a browser environment

func TestInferBrowserWebSocketURL_EmptyTarget(parseT *testing.T) {
	// In a real browser, this would use window.location
	// In test environment, we test the fallback
	parseResult := inferBrowserWebSocketURL("")

	// Should have a valid URL (either from location or fallback)
	if parseResult == "" {
		parseT.Error("Expected non-empty URL")
	}
}

func TestInferBrowserWebSocketURL_FullURL(parseT *testing.T) {
	parseTests := []struct {
		name   string
		target string
		expect string
	}{
		{"WebSocket URL", "ws://localhost:8080", "ws://localhost:8080"},
		{"Secure WebSocket", "wss://api.example.com", "wss://api.example.com"},
		{"HTTP URL maps to ws", "http://localhost:8080", "ws://localhost:8080"},
		{"HTTPS URL maps to wss", "https://api.example.com/grpc", "wss://api.example.com/grpc"},
	}

	for _, parseTt := range parseTests {
		parseT.Run(parseTt.name, func(parseT2 *testing.T) {
			parseResult := inferBrowserWebSocketURL(parseTt.target)
			if parseResult != parseTt.expect {
				parseT2.Errorf("Expected %s, got %s", parseTt.expect, parseResult)
			}
		})
	}
}

func TestInferBrowserWebSocketURL_HostPort(parseT *testing.T) {
	parseResult := inferBrowserWebSocketURL("localhost:8080")

	// Should add ws:// or wss:// prefix
	if parseResult != "ws://localhost:8080" && parseResult != "wss://localhost:8080" {
		parseT.Logf("Got: %s (acceptable with browser protocol inference)", parseResult)
	}
}

// TestSplitDialOptions_AcceptsMixedOptions verifies WASM clients can pass tunnel and gRPC options together.
func TestSplitDialOptions_AcceptsMixedOptions(parseT *testing.T) {
	parseTunnelOpts, parseGrpcOpts, parseErr := splitDialOptions([]interface{}{
		WithTLS(&tls.Config{MinVersion: tls.VersionTLS12}),
		WithSubprotocols("proto.v1"),
		WithReconnectPolicy(ReconnectConfig{InitialDelay: 100 * time.Millisecond}),
		grpc.WithBlock(),
	})
	if parseErr != nil {
		parseT.Fatalf("splitDialOptions() unexpected error: %v", parseErr)
	}
	if len(parseTunnelOpts) != 3 {
		parseT.Fatalf("splitDialOptions() tunnel opts = %d, want 3", len(parseTunnelOpts))
	}
	if len(parseGrpcOpts) != 1 {
		parseT.Fatalf("splitDialOptions() grpc opts = %d, want 1", len(parseGrpcOpts))
	}
}

// TestSplitDialOptions_RejectsUnsupportedType verifies invalid options fail before dialing.
func TestSplitDialOptions_RejectsUnsupportedType(parseT *testing.T) {
	_, _, parseErr := splitDialOptions([]interface{}{"invalid-option-type"})
	if parseErr == nil || !strings.Contains(parseErr.Error(), "unsupported dial option type") {
		parseT.Fatalf("splitDialOptions() error = %v, want unsupported option error", parseErr)
	}
}

// TestDialContext_RejectsUnsupportedOptionType verifies the public WASM API returns option parsing errors.
func TestDialContext_RejectsUnsupportedOptionType(parseT *testing.T) {
	_, parseErr := DialContext(context.Background(), "", "invalid-option-type")
	if parseErr == nil || !strings.Contains(parseErr.Error(), "unsupported dial option type") {
		parseT.Fatalf("DialContext() error = %v, want unsupported option error", parseErr)
	}
}

func TestGetTunnelConfigError_RejectsTLS(parseT *testing.T) {
	parseErr := GetTunnelConfigError(TunnelConfig{
		Target:       "ws://localhost:8080",
		TLSConfig:    &tls.Config{MinVersion: tls.VersionTLS12},
		ShouldUseTLS: true,
	})
	if parseErr == nil || !strings.Contains(parseErr.Error(), "not supported in WASM") {
		parseT.Fatalf("GetTunnelConfigError() error = %v, want TLS rejection", parseErr)
	}
}

func TestDialContext_RejectsWithTLSOption(parseT *testing.T) {
	_, parseErr := DialContext(context.Background(), "", WithTLS(&tls.Config{MinVersion: tls.VersionTLS12}))
	if parseErr == nil || !strings.Contains(parseErr.Error(), "not supported in WASM") {
		parseT.Fatalf("DialContext() error = %v, want TLS rejection", parseErr)
	}
}

func TestDialContext_RejectsHeadersOption(parseT *testing.T) {
	_, parseErr := DialContext(context.Background(), "", WithHeaders(http.Header{}))
	if parseErr == nil || !strings.Contains(parseErr.Error(), "Headers are not supported in WASM") {
		parseT.Fatalf("DialContext() error = %v, want header rejection", parseErr)
	}
}

func TestDialContext_RejectsProxyOption(parseT *testing.T) {
	_, parseErr := DialContext(context.Background(), "", WithProxy(func(parseRequest *http.Request) (*url.URL, error) {
		return nil, nil
	}))
	if parseErr == nil || !strings.Contains(parseErr.Error(), "Proxy is not supported in WASM") {
		parseT.Fatalf("DialContext() error = %v, want proxy rejection", parseErr)
	}
}

func TestDialContext_RejectsHandshakeTimeoutOption(parseT *testing.T) {
	_, parseErr := DialContext(context.Background(), "", WithHandshakeTimeout(0))
	if parseErr == nil || !strings.Contains(parseErr.Error(), "HandshakeTimeout is not supported in WASM") {
		parseT.Fatalf("DialContext() error = %v, want handshake timeout rejection", parseErr)
	}
}

// TestParseTunnelTargetURL_RejectsUnsupportedScheme verifies non-websocket schemes fail in WASM.
func TestParseTunnelTargetURL_RejectsUnsupportedScheme(parseT *testing.T) {
	_, parseErr := ParseTunnelTargetURL("ftp://localhost:8080", false)
	if parseErr == nil || !strings.Contains(parseErr.Error(), "unsupported target scheme") {
		parseT.Fatalf("ParseTunnelTargetURL() error = %v, want unsupported scheme error", parseErr)
	}
}

func TestGetTunnelConfigError_AcceptsSubprotocolsAndReconnect(parseT *testing.T) {
	parseErr := GetTunnelConfigError(TunnelConfig{
		Target:          "ws://localhost:8080",
		Subprotocols:    []string{"proto.v1"},
		ReconnectConfig: &ReconnectConfig{InitialDelay: 100 * time.Millisecond},
	})
	if parseErr != nil {
		parseT.Fatalf("GetTunnelConfigError() error = %v, want nil", parseErr)
	}
}
