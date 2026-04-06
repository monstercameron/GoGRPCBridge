//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
)

const parseDefaultWebSocketBufferSize = 4096
const parseDefaultReadLimitBytes int64 = 16 << 20
const parseBridgeTraceParentMetadataKey = "traceparent"
const parseBridgeTraceStateMetadataKey = "tracestate"
const parseBridgeRequestIDMetadataKey = "x-request-id"
const parseBridgeCorrelationIDMetadataKey = "x-correlation-id"

var cacheWebSocketWriteBufferPools sync.Map

// ServerOption configures the WebSocket server behavior.
type ServerOption func(*serverOptions)

type serverOptions struct {
	checkOrigin             func(r *http.Request) bool
	readBufferSize          int
	writeBufferSize         int
	readLimitBytes          int64
	shouldDisableReadLimit  bool
	pingInterval            time.Duration
	idleTimeout             time.Duration
	onConnect               func(r *http.Request)
	onDisconnect            func(r *http.Request)
	shouldEnableCompression bool
	maxActiveConnections    int
	maxConnectionsPerClient int
	maxUpgradesPerClient    int
}

// buildWebSocketWriteBufferPool returns a shared pool for a websocket write-buffer size.
func buildWebSocketWriteBufferPool(parseBufferSize int) *sync.Pool {
	if parseBufferSize <= 0 {
		parseBufferSize = parseDefaultWebSocketBufferSize
	}
	if !isCacheableWebSocketWriteBufferSize(parseBufferSize) {
		return &sync.Pool{}
	}
	parseCachedPool, isFoundPool := cacheWebSocketWriteBufferPools.Load(parseBufferSize)
	if isFoundPool {
		parsePool, parseOK := parseCachedPool.(*sync.Pool)
		if parseOK {
			return parsePool
		}
	}

	parsePool := &sync.Pool{}
	parseStoredPool, _ := cacheWebSocketWriteBufferPools.LoadOrStore(parseBufferSize, parsePool)
	parseTypedPool, parseOK := parseStoredPool.(*sync.Pool)
	if parseOK {
		return parseTypedPool
	}
	return parsePool
}

// isCacheableWebSocketWriteBufferSize reports whether a buffer size should use the global shared pool cache.
func isCacheableWebSocketWriteBufferSize(parseBufferSize int) bool {
	switch parseBufferSize {
	case parseDefaultWebSocketBufferSize, 8 * 1024, 16 * 1024, 32 * 1024, 64 * 1024:
		return true
	default:
		return false
	}
}

// WithOriginCheck sets a custom origin validation function.
// If not set, gorilla/websocket applies its default same-origin policy.
func WithOriginCheck(parseFn func(r *http.Request) bool) ServerOption {
	return func(parseO *serverOptions) {
		parseO.checkOrigin = parseFn
	}
}

// WithBufferSizes sets custom WebSocket buffer sizes.
func WithBufferSizes(parseRead, parseWrite int) ServerOption {
	return func(parseO *serverOptions) {
		parseO.readBufferSize = parseRead
		parseO.writeBufferSize = parseWrite
	}
}

// WithReadLimitBytes sets a websocket read limit for bridged clients.
func WithReadLimitBytes(parseLimit int64) ServerOption {
	return func(parseO *serverOptions) {
		parseO.readLimitBytes = parseLimit
	}
}

// WithReadLimitDisabled disables websocket read-size limiting for bridge handlers.
func WithReadLimitDisabled() ServerOption {
	return func(parseO *serverOptions) {
		parseO.shouldDisableReadLimit = true
	}
}

// WithKeepalive enables server-side websocket ping and idle timeout handling.
func WithKeepalive(parsePingInterval time.Duration, parseIdleTimeout time.Duration) ServerOption {
	return func(parseO *serverOptions) {
		parseO.pingInterval = parsePingInterval
		parseO.idleTimeout = parseIdleTimeout
	}
}

// WithBridgeWebSocketCompression enables websocket per-message compression for bridge handlers.
func WithBridgeWebSocketCompression() ServerOption {
	return func(parseO *serverOptions) {
		parseO.shouldEnableCompression = true
	}
}

// WithMaxActiveConnections sets a global concurrent connection cap for websocket bridge sessions.
func WithMaxActiveConnections(parseMax int) ServerOption {
	return func(parseO *serverOptions) {
		parseO.maxActiveConnections = parseMax
	}
}

// WithMaxConnectionsPerClient sets a per-client concurrent connection cap for websocket bridge sessions.
func WithMaxConnectionsPerClient(parseMax int) ServerOption {
	return func(parseO *serverOptions) {
		parseO.maxConnectionsPerClient = parseMax
	}
}

// WithMaxUpgradesPerClientPerMinute sets a per-client websocket upgrade-attempt limit over one minute.
func WithMaxUpgradesPerClientPerMinute(parseMax int) ServerOption {
	return func(parseO *serverOptions) {
		parseO.maxUpgradesPerClient = parseMax
	}
}

// WithConnectHook sets a callback for when clients connect.
func WithConnectHook(parseFn func(r *http.Request)) ServerOption {
	return func(parseO *serverOptions) {
		parseO.onConnect = parseFn
	}
}

// WithDisconnectHook sets a callback for when clients disconnect.
func WithDisconnectHook(parseFn func(r *http.Request)) ServerOption {
	return func(parseO *serverOptions) {
		parseO.onDisconnect = parseFn
	}
}

// GetBridgeConfigError validates BridgeConfig for server handler creation.
func GetBridgeConfigError(parseConfig BridgeConfig) error {
	if parseConfig.ReadBufferSize < 0 {
		return fmt.Errorf("grpctunnel: ReadBufferSize must be >= 0")
	}
	if parseConfig.WriteBufferSize < 0 {
		return fmt.Errorf("grpctunnel: WriteBufferSize must be >= 0")
	}
	if parseConfig.ReadLimitBytes < 0 {
		return fmt.Errorf("grpctunnel: ReadLimitBytes must be >= 0")
	}
	if parseConfig.ShouldDisableReadLimit && parseConfig.ReadLimitBytes > 0 {
		return fmt.Errorf("grpctunnel: ReadLimitBytes cannot be set when ShouldDisableReadLimit is true")
	}
	if parseConfig.PingInterval < 0 {
		return fmt.Errorf("grpctunnel: PingInterval must be >= 0")
	}
	if parseConfig.IdleTimeout < 0 {
		return fmt.Errorf("grpctunnel: IdleTimeout must be >= 0")
	}
	if parseConfig.IdleTimeout > 0 && parseConfig.PingInterval <= 0 {
		return fmt.Errorf("grpctunnel: PingInterval must be > 0 when IdleTimeout is set")
	}
	if parseConfig.IdleTimeout > 0 && parseConfig.PingInterval >= parseConfig.IdleTimeout {
		return fmt.Errorf("grpctunnel: PingInterval must be less than IdleTimeout")
	}
	if parseConfig.MaxActiveConnections < 0 {
		return fmt.Errorf("grpctunnel: MaxActiveConnections must be >= 0")
	}
	if parseConfig.MaxConnectionsPerClient < 0 {
		return fmt.Errorf("grpctunnel: MaxConnectionsPerClient must be >= 0")
	}
	if parseConfig.MaxUpgradesPerClientPerMinute < 0 {
		return fmt.Errorf("grpctunnel: MaxUpgradesPerClientPerMinute must be >= 0")
	}
	return nil
}

// applyBridgeConnectionSettings applies optional websocket limits and keepalive behavior.
func applyBridgeConnectionSettings(parseWebSocket *websocket.Conn, parseConfig BridgeConfig) (func(), error) {
	parseReadLimitBytes := getBridgeReadLimitBytes(parseConfig)
	if parseReadLimitBytes > 0 {
		parseWebSocket.SetReadLimit(parseReadLimitBytes)
	}

	if parseConfig.IdleTimeout > 0 {
		parseErr := parseWebSocket.SetReadDeadline(time.Now().Add(parseConfig.IdleTimeout))
		if parseErr != nil {
			return nil, parseErr
		}
		parseWebSocket.SetPongHandler(func(parseApplicationData string) error {
			return parseWebSocket.SetReadDeadline(time.Now().Add(parseConfig.IdleTimeout))
		})
	}

	if parseConfig.PingInterval <= 0 {
		return func() {}, nil
	}

	parseStopChannel := make(chan struct{})
	var parseStopOnce sync.Once
	parseWriteTimeout := buildBridgePingWriteTimeout(parseConfig)
	go func() {
		parseTicker := time.NewTicker(parseConfig.PingInterval)
		defer parseTicker.Stop()

		for {
			select {
			case <-parseTicker.C:
				parseErr := parseWebSocket.WriteControl(websocket.PingMessage, nil, time.Now().Add(parseWriteTimeout))
				if parseErr != nil {
					_ = parseWebSocket.Close()
					return
				}
			case <-parseStopChannel:
				return
			}
		}
	}()

	return func() {
		parseStopOnce.Do(func() {
			close(parseStopChannel)
		})
	}, nil
}

// getBridgeReadLimitBytes resolves websocket read-size guarding for bridge handlers.
func getBridgeReadLimitBytes(parseConfig BridgeConfig) int64 {
	if parseConfig.ShouldDisableReadLimit {
		return 0
	}
	if parseConfig.ReadLimitBytes > 0 {
		return parseConfig.ReadLimitBytes
	}
	return parseDefaultReadLimitBytes
}

// buildBridgePingWriteTimeout derives a deadline for websocket ping control frames.
func buildBridgePingWriteTimeout(parseConfig BridgeConfig) time.Duration {
	parseWriteTimeout := parseConfig.PingInterval
	if parseConfig.IdleTimeout > 0 && (parseWriteTimeout <= 0 || parseWriteTimeout > parseConfig.IdleTimeout) {
		parseWriteTimeout = parseConfig.IdleTimeout
	}
	if parseWriteTimeout <= 0 {
		parseWriteTimeout = time.Second
	}
	return parseWriteTimeout
}

// parseBuildBridgeForwardHeaders builds per-connection HTTP/2 headers forwarded to backend gRPC metadata.
func parseBuildBridgeForwardHeaders(parseRequest *http.Request, parseContext context.Context) http.Header {
	parseForwardHeaders := make(http.Header)
	if parseRequest != nil {
		parseRequestID := strings.TrimSpace(getGrpctunnelRequestID(parseRequest))
		if parseRequestID != "" {
			parseForwardHeaders.Set(parseBridgeRequestIDMetadataKey, parseRequestID)
		}
		parseCorrelationID := parseResolveBridgeHeaderValue(parseRequest.Header, "X-Correlation-Id", "X-Correlation-ID")
		if parseCorrelationID == "" {
			parseCorrelationID = parseRequestID
		}
		if parseCorrelationID != "" {
			parseForwardHeaders.Set(parseBridgeCorrelationIDMetadataKey, parseCorrelationID)
		}
		parseTraceParent := parseResolveBridgeHeaderValue(parseRequest.Header, parseBridgeTraceParentMetadataKey)
		if parseTraceParent != "" {
			parseForwardHeaders.Set(parseBridgeTraceParentMetadataKey, parseTraceParent)
		}
		parseTraceState := parseResolveBridgeHeaderValue(parseRequest.Header, parseBridgeTraceStateMetadataKey)
		if parseTraceState != "" {
			parseForwardHeaders.Set(parseBridgeTraceStateMetadataKey, parseTraceState)
		}
	}
	if strings.TrimSpace(parseForwardHeaders.Get(parseBridgeTraceParentMetadataKey)) == "" {
		parseTraceParent := parseBuildBridgeTraceParentFromContext(parseContext)
		if parseTraceParent != "" {
			parseForwardHeaders.Set(parseBridgeTraceParentMetadataKey, parseTraceParent)
		}
	}
	if strings.TrimSpace(parseForwardHeaders.Get(parseBridgeTraceStateMetadataKey)) == "" {
		parseTraceState := parseBuildBridgeTraceStateFromContext(parseContext)
		if parseTraceState != "" {
			parseForwardHeaders.Set(parseBridgeTraceStateMetadataKey, parseTraceState)
		}
	}
	return parseForwardHeaders
}

// parseResolveBridgeHeaderValue returns the first non-empty value among candidate request headers.
func parseResolveBridgeHeaderValue(parseHeaders http.Header, parseHeaderNames ...string) string {
	if parseHeaders == nil {
		return ""
	}
	for _, parseHeaderName := range parseHeaderNames {
		parseHeaderName = strings.TrimSpace(parseHeaderName)
		if parseHeaderName == "" {
			continue
		}
		parseHeaderValue := strings.TrimSpace(parseHeaders.Get(parseHeaderName))
		if parseHeaderValue != "" {
			return parseHeaderValue
		}
	}
	return ""
}

// parseBuildBridgeTraceParentFromContext formats one W3C traceparent value from context span state.
func parseBuildBridgeTraceParentFromContext(parseContext context.Context) string {
	parseSpanContext := trace.SpanContextFromContext(parseContext)
	if !parseSpanContext.IsValid() {
		return ""
	}
	parseFlags := byte(parseSpanContext.TraceFlags())
	return fmt.Sprintf("00-%s-%s-%02x", parseSpanContext.TraceID().String(), parseSpanContext.SpanID().String(), parseFlags)
}

// parseBuildBridgeTraceStateFromContext returns a trimmed tracestate value from context span state.
func parseBuildBridgeTraceStateFromContext(parseContext context.Context) string {
	parseSpanContext := trace.SpanContextFromContext(parseContext)
	if !parseSpanContext.IsValid() {
		return ""
	}
	return strings.TrimSpace(parseSpanContext.TraceState().String())
}

// parseWrapBridgeForwardMetadataHandler injects forwarded bridge headers into each tunneled HTTP/2 request.
func parseWrapBridgeForwardMetadataHandler(parseHandler http.Handler, parseForwardHeaders http.Header) http.Handler {
	if parseHandler == nil {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	parseForwardHeaders = parseForwardHeaders.Clone()
	return http.HandlerFunc(func(parseW http.ResponseWriter, parseR *http.Request) {
		if parseR == nil {
			parseHandler.ServeHTTP(parseW, parseR)
			return
		}
		parseRequestHeaders := parseR.Header.Clone()
		if parseRequestHeaders == nil {
			parseRequestHeaders = make(http.Header)
		}
		for parseHeaderKey, parseHeaderValues := range parseForwardHeaders {
			if strings.TrimSpace(parseRequestHeaders.Get(parseHeaderKey)) != "" {
				continue
			}
			if len(parseHeaderValues) == 0 {
				continue
			}
			parseRequestHeaders[parseHeaderKey] = append([]string{}, parseHeaderValues...)
		}
		parseRequest := parseR.Clone(parseR.Context())
		parseRequest.Header = parseRequestHeaders
		parseHandler.ServeHTTP(parseW, parseRequest)
	})
}

// BuildBridgeHandler creates a typed websocket handler for a gRPC server.
func BuildBridgeHandler(parseGrpcServer *grpc.Server, parseConfig BridgeConfig) (http.Handler, error) {
	if parseGrpcServer == nil {
		return nil, fmt.Errorf("grpctunnel: grpc server is required")
	}
	if parseErr := GetBridgeConfigError(parseConfig); parseErr != nil {
		return nil, parseErr
	}

	parseReadBufferSize := parseConfig.ReadBufferSize
	if parseReadBufferSize == 0 {
		parseReadBufferSize = parseDefaultWebSocketBufferSize
	}

	parseWriteBufferSize := parseConfig.WriteBufferSize
	if parseWriteBufferSize == 0 {
		parseWriteBufferSize = parseDefaultWebSocketBufferSize
	}

	parseUpgrader := websocket.Upgrader{
		ReadBufferSize:    parseReadBufferSize,
		WriteBufferSize:   parseWriteBufferSize,
		WriteBufferPool:   buildWebSocketWriteBufferPool(parseWriteBufferSize),
		CheckOrigin:       parseConfig.CheckOrigin,
		EnableCompression: parseConfig.ShouldEnableCompression,
	}
	parseHTTP2Server := &http2.Server{}
	parseServeH2CHandler := h2c.NewHandler(parseGrpcServer, parseHTTP2Server)
	parseObservability := buildBridgeObservability()
	parseAbuseGuard := buildBridgeAbuseGuard(parseConfig)

	return http.HandlerFunc(func(parseW http.ResponseWriter, parseR2 *http.Request) {
		parseUpgradeStart := time.Now()
		parseRequestContext, parseRequestSpan := parseObservability.startBridgeRequestSpan(parseR2.Context(), parseR2)
		defer parseRequestSpan.End()
		parseR2 = parseR2.WithContext(parseRequestContext)
		if parseErr := parseAbuseGuard.reserveBridgeConnection(parseR2, time.Now()); parseErr != nil {
			parseObservability.storeBridgeUpgradeFailure(parseRequestContext, time.Since(parseUpgradeStart), parseR2)
			logGrpctunnelEvent("grpctunnel.bridge", "WARN", "ws_upgrade_rejected_abuse_control", parseR2, parseErr, "WebSocket upgrade rejected by abuse controls")
			http.Error(parseW, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
		defer parseAbuseGuard.clearBridgeConnection(parseR2)

		// Upgrade to WebSocket
		parseWs, parseErr := parseUpgrader.Upgrade(parseW, parseR2, nil)
		if parseErr != nil {
			parseObservability.storeBridgeUpgradeFailure(parseRequestContext, time.Since(parseUpgradeStart), parseR2)
			logGrpctunnelEvent("grpctunnel.bridge", "WARN", "ws_upgrade_failed", parseR2, parseErr, "WebSocket upgrade failed")
			return
		}
		parseObservability.storeBridgeUpgradeSuccess(parseRequestContext, time.Since(parseUpgradeStart), parseR2)
		parseObservability.storeBridgeConnectionDelta(parseRequestContext, parseR2, 1)
		defer parseObservability.storeBridgeConnectionDelta(parseRequestContext, parseR2, -1)
		parseSessionContext, parseSessionSpan := parseObservability.startBridgeSessionSpan(parseRequestContext, parseR2)
		defer parseSessionSpan.End()
		parseR2 = parseR2.WithContext(parseSessionContext)
		logGrpctunnelEvent("grpctunnel.bridge", "INFO", "ws_upgrade_succeeded", parseR2, nil, "WebSocket upgrade succeeded")
		defer parseWs.Close()

		parseStopKeepalive, parseErr := applyBridgeConnectionSettings(parseWs, parseConfig)
		if parseErr != nil {
			logGrpctunnelEvent("grpctunnel.bridge", "WARN", "ws_connection_setup_failed", parseR2, parseErr, "WebSocket connection setup failed")
			return
		}
		defer parseStopKeepalive()

		// Lifecycle hooks
		if parseConfig.OnConnect != nil {
			parseConfig.OnConnect(parseR2)
		}
		logGrpctunnelEvent("grpctunnel.bridge", "INFO", "tunnel_connect", parseR2, nil, "Tunnel connected")
		defer func() {
			logGrpctunnelEvent("grpctunnel.bridge", "INFO", "tunnel_disconnect", parseR2, nil, "Tunnel disconnected")
			if parseConfig.OnDisconnect != nil {
				parseConfig.OnDisconnect(parseR2)
			}
		}()

		// Wrap WebSocket as net.Conn
		parseConn := newWebSocketConn(parseWs)
		defer parseConn.Close()
		parseForwardHeaders := parseBuildBridgeForwardHeaders(parseR2, parseSessionContext)
		parseForwardHandler := parseWrapBridgeForwardMetadataHandler(parseServeH2CHandler, parseForwardHeaders)

		// Serve gRPC over HTTP/2 on the WebSocket connection
		parseHTTP2Server.ServeConn(parseConn, &http2.ServeConnOpts{
			Context: parseSessionContext,
			Handler: parseForwardHandler,
		})
	}), nil
}

// HandleBridgeMux registers a typed bridge handler on a mux path.
func HandleBridgeMux(parseMux *http.ServeMux, parseBridgePath string, parseGrpcServer *grpc.Server, parseConfig BridgeConfig) error {
	if parseMux == nil {
		return fmt.Errorf("grpctunnel: mux is required")
	}
	if parseBridgePath == "" {
		return fmt.Errorf("grpctunnel: bridge path is required")
	}

	parseHandler, parseErr := BuildBridgeHandler(parseGrpcServer, parseConfig)
	if parseErr != nil {
		return parseErr
	}
	parseMux.Handle(parseBridgePath, parseHandler)
	return nil
}

// buildServerOptions applies functional server options onto defaults.
func buildServerOptions(parseOpts ...ServerOption) *serverOptions {
	parseOptions := &serverOptions{
		readBufferSize:  parseDefaultWebSocketBufferSize,
		writeBufferSize: parseDefaultWebSocketBufferSize,
	}

	for _, parseOpt := range parseOpts {
		parseOpt(parseOptions)
	}
	return parseOptions
}

// Wrap creates an http.Handler that serves a gRPC server over WebSocket.
// This is the middleware-style API for integrating WebSocket transport.
//
// Example:
//
//	grpcServer := grpc.NewServer()
//	proto.RegisterYourServiceServer(grpcServer, &yourImpl{})
//	http.ListenAndServe(":8080", grpctunnel.Wrap(grpcServer))
func Wrap(parseGrpcServer *grpc.Server, parseOpts ...ServerOption) http.Handler {
	parseOptions := buildServerOptions(parseOpts...)
	parseHandler, parseErr := BuildBridgeHandler(parseGrpcServer, BridgeConfig{
		CheckOrigin:                   parseOptions.checkOrigin,
		ReadBufferSize:                parseOptions.readBufferSize,
		WriteBufferSize:               parseOptions.writeBufferSize,
		ReadLimitBytes:                parseOptions.readLimitBytes,
		ShouldDisableReadLimit:        parseOptions.shouldDisableReadLimit,
		PingInterval:                  parseOptions.pingInterval,
		IdleTimeout:                   parseOptions.idleTimeout,
		ShouldEnableCompression:       parseOptions.shouldEnableCompression,
		MaxActiveConnections:          parseOptions.maxActiveConnections,
		MaxConnectionsPerClient:       parseOptions.maxConnectionsPerClient,
		MaxUpgradesPerClientPerMinute: parseOptions.maxUpgradesPerClient,
		OnConnect:                     parseOptions.onConnect,
		OnDisconnect:                  parseOptions.onDisconnect,
	})
	if parseErr != nil {
		return http.HandlerFunc(func(parseW http.ResponseWriter, parseR *http.Request) {
			logGrpctunnelEvent("grpctunnel.bridge", "ERROR", "bridge_handler_init_failed", parseR, parseErr, "Bridge handler initialization failed")
			http.Error(parseW, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		})
	}
	return parseHandler
}

// Serve accepts connections on the listener and serves gRPC over WebSocket.
// This is a convenience wrapper for simple server setup.
//
// Example:
//
//	grpcServer := grpc.NewServer()
//	proto.RegisterYourServiceServer(grpcServer, &yourImpl{})
//
//	lis, _ := net.Listen("tcp", ":8080")
//	grpctunnel.Serve(lis, grpcServer)
func Serve(parseListener net.Listener, parseGrpcServer *grpc.Server, parseOpts ...ServerOption) error {
	parseServer := &http.Server{
		Handler:      Wrap(parseGrpcServer, parseOpts...),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return parseServer.Serve(parseListener)
}

// ListenAndServe listens on the TCP network address and serves gRPC over WebSocket.
// This is the simplest one-liner for starting a gRPC-over-WebSocket server.
//
// Example:
//
//	grpcServer := grpc.NewServer()
//	proto.RegisterYourServiceServer(grpcServer, &yourImpl{})
//	grpctunnel.ListenAndServe(":8080", grpcServer)
func ListenAndServe(parseAddr string, parseGrpcServer *grpc.Server, parseOpts ...ServerOption) error {
	parseServer := &http.Server{
		Addr:         parseAddr,
		Handler:      Wrap(parseGrpcServer, parseOpts...),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return parseServer.ListenAndServe()
}
