//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"
	"google.golang.org/grpc"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
)

func TestBuildTunnelDialer_HandshakeOptions(parseT *testing.T) {
	parseHeaderValuesChannel := make(chan struct{}, 1)
	var parseHeaderValue string
	var parseExtensionValue string
	var parseSubprotocolValue string
	isProxyCalled := false

	parseUpgrader := websocket.Upgrader{
		CheckOrigin:       func(parseR *http.Request) bool { return true },
		EnableCompression: true,
		Subprotocols:      []string{"proto.v1"},
	}

	parseServer := httptest.NewServer(http.HandlerFunc(func(parseW http.ResponseWriter, parseR *http.Request) {
		parseHeaderValue = parseR.Header.Get("X-Tunnel-Test")
		parseExtensionValue = parseR.Header.Get("Sec-WebSocket-Extensions")

		parseSocket, parseErr := parseUpgrader.Upgrade(parseW, parseR, nil)
		if parseErr != nil {
			return
		}
		defer parseSocket.Close()

		parseSubprotocolValue = parseSocket.Subprotocol()
		select {
		case parseHeaderValuesChannel <- struct{}{}:
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}))
	defer parseServer.Close()

	parseTunnelURL := "ws" + strings.TrimPrefix(parseServer.URL, "http")
	parseDialContext, clearDial := context.WithTimeout(context.Background(), time.Second)
	defer clearDial()

	parseDialer := buildTunnelDialer(TunnelConfig{
		Target: parseTunnelURL,
		Headers: http.Header{
			"X-Tunnel-Test": []string{"present"},
		},
		Subprotocols:            []string{"proto.v1"},
		HandshakeTimeout:        250 * time.Millisecond,
		ShouldEnableCompression: true,
		Proxy: func(parseRequest *http.Request) (*url.URL, error) {
			isProxyCalled = true
			return nil, nil
		},
	})

	parseConn, parseErr := parseDialer(parseDialContext, "ignored")
	if parseErr != nil {
		parseT.Fatalf("buildTunnelDialer() error: %v", parseErr)
	}
	defer parseConn.Close()

	select {
	case <-parseHeaderValuesChannel:
	case <-time.After(time.Second):
		parseT.Fatal("expected websocket handshake to reach server")
	}

	if !isProxyCalled {
		parseT.Fatal("expected proxy selector to be called")
	}
	if parseHeaderValue != "present" {
		parseT.Fatalf("X-Tunnel-Test header = %q, want %q", parseHeaderValue, "present")
	}
	if parseSubprotocolValue != "proto.v1" {
		parseT.Fatalf("websocket subprotocol = %q, want %q", parseSubprotocolValue, "proto.v1")
	}
	if !strings.Contains(parseExtensionValue, "permessage-deflate") {
		parseT.Fatalf("Sec-WebSocket-Extensions = %q, want compression negotiation", parseExtensionValue)
	}
}

func TestClientOptions_AdditiveOptions(parseT *testing.T) {
	parseOptions := &clientOptions{}
	parseHeaders := http.Header{
		"X-Base": []string{"value"},
	}
	parseProxy := func(parseRequest *http.Request) (*url.URL, error) {
		return nil, nil
	}
	parseReconnect := ReconnectConfig{
		InitialDelay: 100 * time.Millisecond,
	}

	WithHeaders(parseHeaders)(parseOptions)
	WithHeader("X-Extra", "extra")(parseOptions)
	WithSubprotocols("proto.v1", "proto.v2")(parseOptions)
	WithProxy(parseProxy)(parseOptions)
	WithHandshakeTimeout(time.Second)(parseOptions)
	WithDialCompression()(parseOptions)
	WithReconnectPolicy(parseReconnect)(parseOptions)

	if parseOptions.setTunnelHeaders.Get("X-Base") != "value" {
		parseT.Fatalf("WithHeaders() base header = %q, want %q", parseOptions.setTunnelHeaders.Get("X-Base"), "value")
	}
	if parseOptions.setTunnelHeaders.Get("X-Extra") != "extra" {
		parseT.Fatalf("WithHeader() extra header = %q, want %q", parseOptions.setTunnelHeaders.Get("X-Extra"), "extra")
	}
	if len(parseOptions.setTunnelSubprotocols) != 2 {
		parseT.Fatalf("WithSubprotocols() count = %d, want 2", len(parseOptions.setTunnelSubprotocols))
	}
	if parseOptions.setTunnelProxy == nil {
		parseT.Fatal("WithProxy() expected proxy function")
	}
	if parseOptions.setTunnelTimeout != time.Second {
		parseT.Fatalf("WithHandshakeTimeout() = %v, want %v", parseOptions.setTunnelTimeout, time.Second)
	}
	if !parseOptions.shouldEnableCompression {
		parseT.Fatal("WithDialCompression() expected compression flag")
	}
	if parseOptions.setTunnelReconnect == nil || parseOptions.setTunnelReconnect.InitialDelay != 100*time.Millisecond {
		parseT.Fatal("WithReconnectPolicy() expected reconnect config")
	}
}

func TestWithHeader_InitializesHeaderMap(parseT *testing.T) {
	parseOptions := &clientOptions{}
	WithHeader("X-Test", "value")(parseOptions)

	if parseOptions.setTunnelHeaders.Get("X-Test") != "value" {
		parseT.Fatalf("WithHeader() = %q, want %q", parseOptions.setTunnelHeaders.Get("X-Test"), "value")
	}
}

func TestGetBridgeConfigError_KeepaliveValidation(parseT *testing.T) {
	parseTests := []struct {
		parseName   string
		parseConfig BridgeConfig
	}{
		{
			parseName: "negative read limit",
			parseConfig: BridgeConfig{
				ReadLimitBytes: -1,
			},
		},
		{
			parseName: "negative ping interval",
			parseConfig: BridgeConfig{
				PingInterval: -time.Second,
			},
		},
		{
			parseName: "negative idle timeout",
			parseConfig: BridgeConfig{
				IdleTimeout: -time.Second,
			},
		},
		{
			parseName: "idle timeout without ping interval",
			parseConfig: BridgeConfig{
				IdleTimeout: time.Second,
			},
		},
		{
			parseName: "ping interval not less than idle timeout",
			parseConfig: BridgeConfig{
				PingInterval: 250 * time.Millisecond,
				IdleTimeout:  250 * time.Millisecond,
			},
		},
		{
			parseName: "read limit with disable flag",
			parseConfig: BridgeConfig{
				ReadLimitBytes:         1024,
				ShouldDisableReadLimit: true,
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

func TestApplyTunnelReconnectPolicy_CustomValues(parseT *testing.T) {
	parseDialOptions, parseErr := ApplyTunnelReconnectPolicy(nil, ReconnectConfig{
		InitialDelay:      100 * time.Millisecond,
		MaxDelay:          time.Second,
		Multiplier:        1.2,
		Jitter:            0.1,
		MinConnectTimeout: 2 * time.Second,
	})
	if parseErr != nil {
		parseT.Fatalf("ApplyTunnelReconnectPolicy() error: %v", parseErr)
	}
	if len(parseDialOptions) != 1 {
		parseT.Fatalf("ApplyTunnelReconnectPolicy() option count = %d, want 1", len(parseDialOptions))
	}
}

func TestGetReconnectConfigError_InvalidValues(parseT *testing.T) {
	parseTests := []struct {
		parseName   string
		parseConfig ReconnectConfig
	}{
		{
			parseName: "negative initial delay",
			parseConfig: ReconnectConfig{
				InitialDelay: -time.Second,
			},
		},
		{
			parseName: "negative max delay",
			parseConfig: ReconnectConfig{
				MaxDelay: -time.Second,
			},
		},
		{
			parseName: "negative connect timeout",
			parseConfig: ReconnectConfig{
				MinConnectTimeout: -time.Second,
			},
		},
		{
			parseName: "negative multiplier",
			parseConfig: ReconnectConfig{
				Multiplier: -1,
			},
		},
		{
			parseName: "nan multiplier",
			parseConfig: ReconnectConfig{
				Multiplier: math.NaN(),
			},
		},
		{
			parseName: "infinite multiplier",
			parseConfig: ReconnectConfig{
				Multiplier: math.Inf(1),
			},
		},
		{
			parseName: "negative jitter",
			parseConfig: ReconnectConfig{
				Jitter: -1,
			},
		},
		{
			parseName: "nan jitter",
			parseConfig: ReconnectConfig{
				Jitter: math.NaN(),
			},
		},
		{
			parseName: "infinite jitter",
			parseConfig: ReconnectConfig{
				Jitter: math.Inf(-1),
			},
		},
	}

	for _, parseTestCase := range parseTests {
		parseT.Run(parseTestCase.parseName, func(parseT2 *testing.T) {
			parseErr := GetReconnectConfigError(parseTestCase.parseConfig)
			if parseErr == nil {
				parseT2.Fatal("GetReconnectConfigError() expected validation error, got nil")
			}
		})
	}
}

func TestGetTunnelConfigError_AdditiveValidation(parseT *testing.T) {
	parseTests := []struct {
		parseName   string
		parseConfig TunnelConfig
	}{
		{
			parseName: "negative handshake timeout",
			parseConfig: TunnelConfig{
				Target:           "ws://localhost:8080",
				HandshakeTimeout: -time.Second,
			},
		},
		{
			parseName: "invalid reconnect config",
			parseConfig: TunnelConfig{
				Target:          "ws://localhost:8080",
				ReconnectConfig: &ReconnectConfig{Jitter: -1},
			},
		},
	}

	for _, parseTestCase := range parseTests {
		parseT.Run(parseTestCase.parseName, func(parseT2 *testing.T) {
			parseErr := GetTunnelConfigError(parseTestCase.parseConfig)
			if parseErr == nil {
				parseT2.Fatal("GetTunnelConfigError() expected validation error, got nil")
			}
		})
	}
}

func TestBuildTunnelConn_WithReconnectConfig(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	parseServer := httptest.NewServer(Wrap(parseGrpcServer))
	defer parseServer.Close()

	parseTunnelURL := "ws" + strings.TrimPrefix(parseServer.URL, "http")
	parseDialContext, clearDial := context.WithTimeout(context.Background(), 5*time.Second)
	defer clearDial()

	parseConn, parseErr := BuildTunnelConn(parseDialContext, TunnelConfig{
		Target:                  parseTunnelURL,
		HandshakeTimeout:        time.Second,
		ReconnectConfig:         &ReconnectConfig{InitialDelay: 100 * time.Millisecond},
		ShouldEnableCompression: true,
		GRPCOptions:             ApplyTunnelInsecureCredentials(nil),
	})
	if parseErr != nil {
		parseT.Fatalf("BuildTunnelConn() error: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)
	parseResponse, parseErr := parseClient.CreateTodo(parseDialContext, &proto.CreateTodoRequest{Text: "reconnect"})
	if parseErr != nil {
		parseT.Fatalf("CreateTodo() error: %v", parseErr)
	}
	if parseResponse.GetTodo().GetText() != "reconnect" {
		parseT.Fatalf("todo text = %q, want %q", parseResponse.GetTodo().GetText(), "reconnect")
	}
}

// TestBuildTunnelGRPCDialTarget_WebSocketSchemes verifies websocket URL targets map to resolver-safe gRPC dial targets.
func TestBuildTunnelGRPCDialTarget_WebSocketSchemes(parseT *testing.T) {
	parseTests := []struct {
		parseName     string
		parseTarget   string
		parseTunnel   string
		parseExpected string
	}{
		{
			parseName:     "ws scheme uses host",
			parseTarget:   "ws://127.0.0.1:8080/grpc",
			parseTunnel:   "ws://127.0.0.1:8080/grpc",
			parseExpected: "127.0.0.1:8080",
		},
		{
			parseName:     "wss scheme uses host",
			parseTarget:   "wss://api.example.com/grpc",
			parseTunnel:   "wss://api.example.com/grpc",
			parseExpected: "api.example.com",
		},
		{
			parseName:     "host port target preserved",
			parseTarget:   "127.0.0.1:8080",
			parseTunnel:   "ws://127.0.0.1:8080",
			parseExpected: "127.0.0.1:8080",
		},
		{
			parseName:     "empty target falls back to tunnel url",
			parseTarget:   " ",
			parseTunnel:   "ws://127.0.0.1:8080",
			parseExpected: "ws://127.0.0.1:8080",
		},
	}

	for _, parseTestCase := range parseTests {
		parseT.Run(parseTestCase.parseName, func(parseT2 *testing.T) {
			parseDialTarget := buildTunnelGRPCDialTarget(parseTestCase.parseTarget, parseTestCase.parseTunnel)
			if parseDialTarget != parseTestCase.parseExpected {
				parseT2.Fatalf("buildTunnelGRPCDialTarget() = %q, want %q", parseDialTarget, parseTestCase.parseExpected)
			}
		})
	}
}

// TestBuildTunnelConn_ReconnectsAfterServerStart verifies reconnect behavior when dialing before the bridge is available.
func TestBuildTunnelConn_ReconnectsAfterServerStart(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	parseDelayedListener, parseErr := net.Listen("tcp", "127.0.0.1:0")
	if parseErr != nil {
		parseT.Fatalf("Listen() error: %v", parseErr)
	}
	parseTunnelURL := "ws://" + parseDelayedListener.Addr().String()

	parseDelayedServer := &http.Server{
		Handler: Wrap(parseGrpcServer),
	}
	defer parseDelayedServer.Close()
	parseServeErrorChannel := make(chan error, 1)

	go func() {
		time.Sleep(300 * time.Millisecond)
		parseServeErr := parseDelayedServer.Serve(parseDelayedListener)
		if parseServeErr != nil && !errors.Is(parseServeErr, http.ErrServerClosed) {
			select {
			case parseServeErrorChannel <- parseServeErr:
			default:
			}
		}
	}()

	parseDialContext, clearDial := context.WithTimeout(context.Background(), 8*time.Second)
	defer clearDial()

	parseConn, parseErr := BuildTunnelConn(parseDialContext, TunnelConfig{
		Target:           parseTunnelURL,
		HandshakeTimeout: 100 * time.Millisecond,
		ReconnectConfig: &ReconnectConfig{
			InitialDelay: 50 * time.Millisecond,
			MaxDelay:     200 * time.Millisecond,
		},
		GRPCOptions: ApplyTunnelInsecureCredentials(nil),
	})
	if parseErr != nil {
		parseT.Fatalf("BuildTunnelConn() error: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)
	parseCallContext, clearCall := context.WithTimeout(context.Background(), 5*time.Second)
	defer clearCall()

	parseResponse, parseErr := parseClient.CreateTodo(
		parseCallContext,
		&proto.CreateTodoRequest{Text: "reconnect-behavior"},
		grpc.WaitForReady(true),
	)
	if parseErr != nil {
		parseT.Fatalf("CreateTodo() with reconnect behavior error: %v", parseErr)
	}
	if parseResponse.GetTodo().GetText() != "reconnect-behavior" {
		parseT.Fatalf("todo text = %q, want %q", parseResponse.GetTodo().GetText(), "reconnect-behavior")
	}
	select {
	case parseServeErr := <-parseServeErrorChannel:
		parseT.Fatalf("Serve() error: %v", parseServeErr)
	default:
	}
}

func TestServerOptions_AdditiveOptions(parseT *testing.T) {
	parseOptions := buildServerOptions(
		WithReadLimitBytes(2048),
		WithReadLimitDisabled(),
		WithKeepalive(50*time.Millisecond, 200*time.Millisecond),
		WithBridgeWebSocketCompression(),
	)

	if parseOptions.readLimitBytes != 2048 {
		parseT.Fatalf("WithReadLimitBytes() = %d, want %d", parseOptions.readLimitBytes, 2048)
	}
	if !parseOptions.shouldDisableReadLimit {
		parseT.Fatal("WithReadLimitDisabled() expected disable flag")
	}
	if parseOptions.pingInterval != 50*time.Millisecond {
		parseT.Fatalf("WithKeepalive() ping interval = %v, want %v", parseOptions.pingInterval, 50*time.Millisecond)
	}
	if parseOptions.idleTimeout != 200*time.Millisecond {
		parseT.Fatalf("WithKeepalive() idle timeout = %v, want %v", parseOptions.idleTimeout, 200*time.Millisecond)
	}
	if !parseOptions.shouldEnableCompression {
		parseT.Fatal("WithBridgeWebSocketCompression() expected compression flag")
	}
}

func TestBuildServerOptions_DefaultOriginPolicy(parseT *testing.T) {
	parseOptions := buildServerOptions()
	if parseOptions.checkOrigin != nil {
		parseT.Fatal("buildServerOptions() expected nil CheckOrigin to use websocket default same-origin policy")
	}
}

func TestGetBridgeReadLimitBytes(parseT *testing.T) {
	parseTests := []struct {
		parseName     string
		parseConfig   BridgeConfig
		parseExpected int64
	}{
		{
			parseName: "default secure limit",
			parseConfig: BridgeConfig{
				ReadLimitBytes: 0,
			},
			parseExpected: parseDefaultReadLimitBytes,
		},
		{
			parseName: "custom read limit",
			parseConfig: BridgeConfig{
				ReadLimitBytes: 1024,
			},
			parseExpected: 1024,
		},
		{
			parseName: "read limit disabled",
			parseConfig: BridgeConfig{
				ShouldDisableReadLimit: true,
			},
			parseExpected: 0,
		},
	}

	for _, parseTestCase := range parseTests {
		parseT.Run(parseTestCase.parseName, func(parseT2 *testing.T) {
			parseResult := getBridgeReadLimitBytes(parseTestCase.parseConfig)
			if parseResult != parseTestCase.parseExpected {
				parseT2.Fatalf("getBridgeReadLimitBytes() = %d, want %d", parseResult, parseTestCase.parseExpected)
			}
		})
	}
}

func TestBuildBridgePingWriteTimeout(parseT *testing.T) {
	parseTests := []struct {
		parseName     string
		parseConfig   BridgeConfig
		parseExpected time.Duration
	}{
		{
			parseName: "use ping interval",
			parseConfig: BridgeConfig{
				PingInterval: 25 * time.Millisecond,
				IdleTimeout:  100 * time.Millisecond,
			},
			parseExpected: 25 * time.Millisecond,
		},
		{
			parseName: "cap by idle timeout",
			parseConfig: BridgeConfig{
				PingInterval: 2 * time.Second,
				IdleTimeout:  time.Second,
			},
			parseExpected: time.Second,
		},
		{
			parseName:     "fallback to one second",
			parseConfig:   BridgeConfig{},
			parseExpected: time.Second,
		},
	}

	for _, parseTestCase := range parseTests {
		parseT.Run(parseTestCase.parseName, func(parseT2 *testing.T) {
			parseResult := buildBridgePingWriteTimeout(parseTestCase.parseConfig)
			if parseResult != parseTestCase.parseExpected {
				parseT2.Fatalf("buildBridgePingWriteTimeout() = %v, want %v", parseResult, parseTestCase.parseExpected)
			}
		})
	}
}

func TestBuildWebSocketWriteBufferPool_CacheableBufferSize(parseT *testing.T) {
	parsePoolOne := buildWebSocketWriteBufferPool(parseDefaultWebSocketBufferSize)
	parsePoolTwo := buildWebSocketWriteBufferPool(parseDefaultWebSocketBufferSize)
	if parsePoolOne != parsePoolTwo {
		parseT.Fatal("buildWebSocketWriteBufferPool() expected shared pool for cacheable size")
	}
}

func TestBuildWebSocketWriteBufferPool_UncacheableBufferSize(parseT *testing.T) {
	parsePoolOne := buildWebSocketWriteBufferPool(12345)
	parsePoolTwo := buildWebSocketWriteBufferPool(12345)
	if parsePoolOne == parsePoolTwo {
		parseT.Fatal("buildWebSocketWriteBufferPool() expected distinct pools for uncacheable size")
	}
}

func TestBuildBridgeHandler_PingAndCompression(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	defer parseGrpcServer.Stop()

	parseHandler, parseErr := BuildBridgeHandler(parseGrpcServer, BridgeConfig{
		PingInterval:            25 * time.Millisecond,
		IdleTimeout:             100 * time.Millisecond,
		ShouldEnableCompression: true,
	})
	if parseErr != nil {
		parseT.Fatalf("BuildBridgeHandler() error: %v", parseErr)
	}

	parseServer := httptest.NewServer(parseHandler)
	defer parseServer.Close()

	parseWebSocketURL := "ws" + strings.TrimPrefix(parseServer.URL, "http")
	parsePingChannel := make(chan struct{}, 1)
	parseDialer := websocket.Dialer{
		EnableCompression: true,
	}
	parseClientSocket, parseResponse, parseErr := parseDialer.Dial(parseWebSocketURL, nil)
	if parseErr != nil {
		parseT.Fatalf("Dial() error: %v", parseErr)
	}
	defer parseClientSocket.Close()

	parseClientSocket.SetPingHandler(func(parseApplicationData string) error {
		select {
		case parsePingChannel <- struct{}{}:
		default:
		}
		return parseClientSocket.WriteControl(websocket.PongMessage, []byte(parseApplicationData), time.Now().Add(time.Second))
	})
	go func() {
		for {
			if _, _, parseErr := parseClientSocket.NextReader(); parseErr != nil {
				return
			}
		}
	}()

	select {
	case <-parsePingChannel:
	case <-time.After(time.Second):
		parseT.Fatal("expected bridge keepalive ping")
	}

	if !strings.Contains(parseResponse.Header.Get("Sec-WebSocket-Extensions"), "permessage-deflate") {
		parseT.Fatalf("Sec-WebSocket-Extensions = %q, want compression negotiation", parseResponse.Header.Get("Sec-WebSocket-Extensions"))
	}
}

func TestApplyBridgeConnectionSettings_ClosedSocketReturnsError(parseT *testing.T) {
	parseUpgrader := websocket.Upgrader{
		CheckOrigin: func(parseR *http.Request) bool { return true },
	}

	parseServer := httptest.NewServer(http.HandlerFunc(func(parseW http.ResponseWriter, parseR *http.Request) {
		parseSocket, parseErr := parseUpgrader.Upgrade(parseW, parseR, nil)
		if parseErr != nil {
			return
		}
		defer parseSocket.Close()
		time.Sleep(100 * time.Millisecond)
	}))
	defer parseServer.Close()

	parseWebSocketURL := "ws" + strings.TrimPrefix(parseServer.URL, "http")
	parseClientSocket, _, parseErr := websocket.DefaultDialer.Dial(parseWebSocketURL, nil)
	if parseErr != nil {
		parseT.Fatalf("Dial() error: %v", parseErr)
	}
	_ = parseClientSocket.Close()

	_, parseErr = applyBridgeConnectionSettings(parseClientSocket, BridgeConfig{
		ReadLimitBytes: 32,
		IdleTimeout:    time.Second,
	})
	if parseErr == nil {
		parseT.Fatal("applyBridgeConnectionSettings() expected closed-socket error, got nil")
	}
}

func TestBuildBridgeHandler_InvalidConfig(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	defer parseGrpcServer.Stop()

	_, parseErr := BuildBridgeHandler(parseGrpcServer, BridgeConfig{
		IdleTimeout: time.Second,
	})
	if parseErr == nil {
		parseT.Fatal("BuildBridgeHandler() expected validation error, got nil")
	}
}

func TestHandleBridgeMux_InvalidBridgeConfig(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	defer parseGrpcServer.Stop()

	parseErr := HandleBridgeMux(http.NewServeMux(), "/grpc", parseGrpcServer, BridgeConfig{
		IdleTimeout: time.Second,
	})
	if parseErr == nil {
		parseT.Fatal("HandleBridgeMux() expected validation error, got nil")
	}
}

func TestBuildToolingHandler_RegistersServices(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	defer parseGrpcServer.Stop()

	parseHandler, parseHealthServer, parseErr := BuildToolingHandler(parseGrpcServer, ToolingConfig{
		ShouldEnableReflection:    true,
		ShouldEnableHealthService: true,
	})
	if parseErr != nil {
		parseT.Fatalf("BuildToolingHandler() error: %v", parseErr)
	}
	if parseHandler == nil {
		parseT.Fatal("BuildToolingHandler() returned nil handler")
	}
	if parseHealthServer == nil {
		parseT.Fatal("BuildToolingHandler() expected health server")
	}

	parseServiceInfo := parseGrpcServer.GetServiceInfo()
	if _, hasReflection := parseServiceInfo["grpc.reflection.v1alpha.ServerReflection"]; !hasReflection {
		parseT.Fatal("expected reflection service to be registered")
	}
	if _, hasHealth := parseServiceInfo[grpc_health_v1.Health_ServiceDesc.ServiceName]; !hasHealth {
		parseT.Fatal("expected health service to be registered")
	}

	parseResponse, parseErr := parseHealthServer.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if parseErr != nil {
		parseT.Fatalf("health.Check() error: %v", parseErr)
	}
	if parseResponse.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		parseT.Fatalf("health status = %v, want %v", parseResponse.GetStatus(), grpc_health_v1.HealthCheckResponse_SERVING)
	}
}

func TestBuildToolingHandler_Errors(parseT *testing.T) {
	_, _, parseErr := BuildToolingHandler(nil, ToolingConfig{})
	if parseErr == nil {
		parseT.Fatal("BuildToolingHandler(nil) expected error, got nil")
	}

	_, _, parseErr = BuildToolingHandler(grpc.NewServer(), ToolingConfig{
		DebugPathPrefix: "debug/pprof/",
	})
	if parseErr == nil {
		parseT.Fatal("BuildToolingHandler() expected invalid prefix error, got nil")
	}
}

func TestBuildToolingHandler_Pprof(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	defer parseGrpcServer.Stop()

	parseHandler, _, parseErr := BuildToolingHandler(parseGrpcServer, ToolingConfig{
		ShouldEnablePprof: true,
		DebugPathPrefix:   "/debug/custom/",
	})
	if parseErr != nil {
		parseT.Fatalf("BuildToolingHandler() error: %v", parseErr)
	}

	parseServer := httptest.NewServer(parseHandler)
	defer parseServer.Close()

	parseResponse, parseErr := http.Get(parseServer.URL + "/debug/custom/")
	if parseErr != nil {
		parseT.Fatalf("GET pprof index error: %v", parseErr)
	}
	defer parseResponse.Body.Close()

	if parseResponse.StatusCode != http.StatusOK {
		parseT.Fatalf("GET /debug/custom/ status = %d, want %d", parseResponse.StatusCode, http.StatusOK)
	}
}

func TestBuildToolingHandler_DefaultPprofPrefix(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	defer parseGrpcServer.Stop()

	parseHandler, _, parseErr := BuildToolingHandler(parseGrpcServer, ToolingConfig{
		ShouldEnablePprof: true,
	})
	if parseErr != nil {
		parseT.Fatalf("BuildToolingHandler() error: %v", parseErr)
	}

	parseServer := httptest.NewServer(parseHandler)
	defer parseServer.Close()

	parseResponse, parseErr := http.Get(parseServer.URL + "/debug/pprof/")
	if parseErr != nil {
		parseT.Fatalf("GET default pprof index error: %v", parseErr)
	}
	defer parseResponse.Body.Close()

	if parseResponse.StatusCode != http.StatusOK {
		parseT.Fatalf("GET /debug/pprof/ status = %d, want %d", parseResponse.StatusCode, http.StatusOK)
	}
}

func TestGetToolingConfigError_InvalidPrefix(parseT *testing.T) {
	parseTests := []struct {
		parseName   string
		parseConfig ToolingConfig
	}{
		{
			parseName: "missing leading slash",
			parseConfig: ToolingConfig{
				DebugPathPrefix: "debug/pprof/",
			},
		},
		{
			parseName: "missing trailing slash",
			parseConfig: ToolingConfig{
				DebugPathPrefix: "/debug/pprof",
			},
		},
	}

	for _, parseTestCase := range parseTests {
		parseT.Run(parseTestCase.parseName, func(parseT2 *testing.T) {
			parseErr := GetToolingConfigError(parseTestCase.parseConfig)
			if parseErr == nil {
				parseT2.Fatalf("GetToolingConfigError(%+v) expected error, got nil", parseTestCase.parseConfig)
			}
		})
	}
}

func TestListenAndServeTooling_InvalidAddress(parseT *testing.T) {
	parseErr := ListenAndServeTooling("127.0.0.1:invalid", grpc.NewServer(), ToolingConfig{})
	if parseErr == nil {
		parseT.Fatal("ListenAndServeTooling() expected address error, got nil")
	}
}

func TestListenAndServeTooling_NilServer(parseT *testing.T) {
	parseErr := ListenAndServeTooling("127.0.0.1:0", nil, ToolingConfig{})
	if parseErr == nil {
		parseT.Fatal("ListenAndServeTooling() expected server error, got nil")
	}
}

func TestListenAndServeTooling_WildcardBindWithPprofReturnsError(parseT *testing.T) {
	parseErr := ListenAndServeTooling(":0", grpc.NewServer(), ToolingConfig{
		ShouldEnablePprof: true,
	})
	if parseErr == nil {
		parseT.Fatal("ListenAndServeTooling() expected wildcard bind security error, got nil")
	}
	if !strings.Contains(parseErr.Error(), "refusing tooling listen address") {
		parseT.Fatalf("ListenAndServeTooling() error = %q, want wildcard bind security message", parseErr.Error())
	}
}

func TestGetToolingListenAddressError_WildcardAndLoopback(parseT *testing.T) {
	parseTests := []struct {
		parseName     string
		parseAddr     string
		parseConfig   ToolingConfig
		shouldHaveErr bool
	}{
		{
			parseName: "wildcard with reflection",
			parseAddr: ":9090",
			parseConfig: ToolingConfig{
				ShouldEnableReflection: true,
			},
			shouldHaveErr: true,
		},
		{
			parseName: "explicit wildcard host with pprof",
			parseAddr: "0.0.0.0:9090",
			parseConfig: ToolingConfig{
				ShouldEnablePprof: true,
			},
			shouldHaveErr: true,
		},
		{
			parseName: "loopback with reflection",
			parseAddr: "127.0.0.1:9090",
			parseConfig: ToolingConfig{
				ShouldEnableReflection: true,
			},
			shouldHaveErr: false,
		},
		{
			parseName: "wildcard without introspection",
			parseAddr: ":9090",
			parseConfig: ToolingConfig{
				ShouldEnableReflection: false,
				ShouldEnablePprof:      false,
			},
			shouldHaveErr: false,
		},
	}

	for _, parseTestCase := range parseTests {
		parseT.Run(parseTestCase.parseName, func(parseT2 *testing.T) {
			parseErr := getToolingListenAddressError(parseTestCase.parseAddr, parseTestCase.parseConfig)
			if parseTestCase.shouldHaveErr && parseErr == nil {
				parseT2.Fatalf("getToolingListenAddressError(%q) expected error, got nil", parseTestCase.parseAddr)
			}
			if !parseTestCase.shouldHaveErr && parseErr != nil {
				parseT2.Fatalf("getToolingListenAddressError(%q) expected nil error, got %v", parseTestCase.parseAddr, parseErr)
			}
		})
	}
}
