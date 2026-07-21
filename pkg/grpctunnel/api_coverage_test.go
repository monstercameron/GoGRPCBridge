//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
)

// TestParseTunnelTargetURL verifies typed target normalization across success and error cases.
func TestParseTunnelTargetURL(parseT *testing.T) {
	parseTests := []struct {
		parseName      string
		parseTarget    string
		isUseTLS       bool
		parseExpected  string
		isWantError    bool
		parseErrorText string
	}{
		{
			parseName:      "empty target",
			parseTarget:    "",
			isWantError:    true,
			parseErrorText: "target is required",
		},
		{
			parseName:     "bare port",
			parseTarget:   ":8080",
			parseExpected: "ws://localhost:8080",
		},
		{
			parseName:     "bare port with tls",
			parseTarget:   ":8443",
			isUseTLS:      true,
			parseExpected: "wss://localhost:8443",
		},
		{
			parseName:     "explicit websocket url",
			parseTarget:   "ws://localhost:8080/grpc",
			parseExpected: "ws://localhost:8080/grpc",
		},
		{
			parseName:     "explicit secure websocket url",
			parseTarget:   "wss://api.example.com/grpc",
			parseExpected: "wss://api.example.com/grpc",
		},
		{
			parseName:     "http url maps to ws",
			parseTarget:   "http://localhost:8080",
			parseExpected: "ws://localhost:8080",
		},
		{
			parseName:     "https url maps to wss",
			parseTarget:   "https://api.example.com/grpc",
			parseExpected: "wss://api.example.com/grpc",
		},
		{
			parseName:      "unsupported scheme",
			parseTarget:    "ftp://localhost:8080",
			isWantError:    true,
			parseErrorText: "unsupported target scheme",
		},
		{
			parseName:      "invalid percent host",
			parseTarget:    "%",
			isWantError:    true,
			parseErrorText: "invalid target",
		},
		{
			parseName:      "missing host",
			parseTarget:    "ws://",
			isWantError:    true,
			parseErrorText: "target host is required",
		},
	}

	for _, parseTestCase := range parseTests {
		parseT.Run(parseTestCase.parseName, func(parseT2 *testing.T) {
			parseResult, parseErr := ParseTunnelTargetURL(parseTestCase.parseTarget, parseTestCase.isUseTLS)
			if parseTestCase.isWantError {
				if parseErr == nil {
					parseT2.Fatal("ParseTunnelTargetURL() expected error, got nil")
				}
				if !strings.Contains(parseErr.Error(), parseTestCase.parseErrorText) {
					parseT2.Fatalf("ParseTunnelTargetURL() error = %v, want text %q", parseErr, parseTestCase.parseErrorText)
				}
				return
			}

			if parseErr != nil {
				parseT2.Fatalf("ParseTunnelTargetURL() error: %v", parseErr)
			}
			if parseResult != parseTestCase.parseExpected {
				parseT2.Fatalf("ParseTunnelTargetURL() = %q, want %q", parseResult, parseTestCase.parseExpected)
			}
		})
	}
}

// TestInferWebSocketURL_InvalidTargetFallback verifies the legacy wrapper preserves invalid input on parse failure.
func TestInferWebSocketURL_InvalidTargetFallback(parseT *testing.T) {
	parseTarget := "%"
	parseResult := inferWebSocketURL(parseTarget, false)
	if parseResult != parseTarget {
		parseT.Fatalf("inferWebSocketURL() = %q, want original target %q", parseResult, parseTarget)
	}
}

// TestBuildTunnelDialer_InvalidURL verifies invalid tunnel URLs fail before dialing.
func TestBuildTunnelDialer_InvalidURL(parseT *testing.T) {
	parseDialContext, clearDial := context.WithTimeout(context.Background(), time.Second)
	defer clearDial()

	_, parseErr := buildTunnelDialer(TunnelConfig{Target: "%"})(parseDialContext, "ignored")
	if parseErr == nil {
		parseT.Fatal("buildTunnelDialer() expected URL parse error, got nil")
	}
}

// TestBuildTunnelConn_InvalidTarget verifies typed dialing surfaces validation failures.
func TestBuildTunnelConn_InvalidTarget(parseT *testing.T) {
	parseDialContext, clearDial := context.WithTimeout(context.Background(), time.Second)
	defer clearDial()

	_, parseErr := BuildTunnelConn(parseDialContext, TunnelConfig{
		Target:      "ftp://localhost:8080",
		GRPCOptions: ApplyTunnelInsecureCredentials(nil),
	})
	if parseErr == nil {
		parseT.Fatal("BuildTunnelConn() expected validation error, got nil")
	}
}

// TestGetBridgeConfigError verifies typed bridge validation rejects negative buffers.
func TestGetBridgeConfigError(parseT *testing.T) {
	parseTests := []struct {
		parseName   string
		parseConfig BridgeConfig
	}{
		{
			parseName: "negative read buffer",
			parseConfig: BridgeConfig{
				ReadBufferSize: -1,
			},
		},
		{
			parseName: "negative write buffer",
			parseConfig: BridgeConfig{
				WriteBufferSize: -1,
			},
		},
	}

	for _, parseTestCase := range parseTests {
		parseT.Run(parseTestCase.parseName, func(parseT2 *testing.T) {
			parseErr := GetBridgeConfigError(parseTestCase.parseConfig)
			if parseErr == nil {
				parseT2.Fatal("GetBridgeConfigError() expected validation error, got nil")
			}
		})
	}
}

// TestHandleBridgeMux_Validation verifies mux registration rejects invalid inputs.
func TestHandleBridgeMux_Validation(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	defer parseGrpcServer.Stop()

	parseErr := HandleBridgeMux(nil, "/grpc", parseGrpcServer, BridgeConfig{})
	if parseErr == nil {
		parseT.Fatal("HandleBridgeMux() expected nil mux error, got nil")
	}

	parseMux := http.NewServeMux()
	parseErr = HandleBridgeMux(parseMux, "", parseGrpcServer, BridgeConfig{})
	if parseErr == nil {
		parseT.Fatal("HandleBridgeMux() expected empty path error, got nil")
	}
}

// TestWrap_NilServerReturnsErrorHandler verifies Wrap exposes typed handler construction errors to callers.
func TestWrap_NilServerReturnsErrorHandler(parseT *testing.T) {
	parseHandler := Wrap(nil)

	parseRecorder := httptest.NewRecorder()
	parseRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	parseHandler.ServeHTTP(parseRecorder, parseRequest)

	if parseRecorder.Code != http.StatusInternalServerError {
		parseT.Fatalf("Wrap(nil) status = %d, want %d", parseRecorder.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(parseRecorder.Body.String(), http.StatusText(http.StatusInternalServerError)) {
		parseT.Fatalf("Wrap(nil) body = %q, want generic internal server error", parseRecorder.Body.String())
	}
}

// TestWebSocketConn_SetDeadlineReturnsUnderlyingError verifies SetDeadline propagates websocket setter failures.
func TestWebSocketConn_SetDeadlineReturnsUnderlyingError(parseT *testing.T) {
	parseTunnelConn, clearConn := getGrpctunnelTestConn(parseT, func(parseServerSocket *websocket.Conn) {
		time.Sleep(50 * time.Millisecond)
	})
	defer clearConn()

	_ = parseTunnelConn.ws.Close()

	parseErr := parseTunnelConn.SetDeadline(time.Now().Add(time.Second))
	if parseErr == nil {
		parseT.Fatal("SetDeadline() expected underlying websocket error, got nil")
	}
}
