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
	"google.golang.org/grpc"
)

const defaultWebSocketBufferSize = 4096
const defaultReadLimitBytes int64 = 16 << 20
const bridgeTraceParentMetadataKey = "traceparent"
const bridgeTraceStateMetadataKey = "tracestate"
const bridgeRequestIDMetadataKey = "x-request-id"
const bridgeCorrelationIDMetadataKey = "x-correlation-id"

var cacheWebSocketWriteBufferPools sync.Map

// ServerOption configures the WebSocket server behavior.
type ServerOption func(*serverOptions)

type serverOptions struct {
	checkOrigin              func(r *http.Request) bool
	authorize                func(r *http.Request) error
	readBufferSize           int
	writeBufferSize          int
	readLimitBytes           int64
	shouldDisableReadLimit   bool
	pingInterval             time.Duration
	idleTimeout              time.Duration
	sessionMaxLifetime       time.Duration
	shouldDisableKeepalive   bool
	shouldUseNativeTransport bool
	onConnect                func(r *http.Request)
	onDisconnect             func(r *http.Request)
	shouldEnableCompression  bool
	maxActiveConnections     int
	maxConnectionsPerClient  int
	maxUpgradesPerClient     int
}

// buildWebSocketWriteBufferPool returns a shared pool for a websocket write-buffer size.
func buildWebSocketWriteBufferPool(bufferSize int) *sync.Pool {
	if bufferSize <= 0 {
		bufferSize = defaultWebSocketBufferSize
	}
	if !isCacheableWebSocketWriteBufferSize(bufferSize) {
		return &sync.Pool{}
	}
	cached, found := cacheWebSocketWriteBufferPools.Load(bufferSize)
	if found {
		pool, ok := cached.(*sync.Pool)
		if ok {
			return pool
		}
	}

	pool := &sync.Pool{}
	stored, _ := cacheWebSocketWriteBufferPools.LoadOrStore(bufferSize, pool)
	typedPool, ok := stored.(*sync.Pool)
	if ok {
		return typedPool
	}
	return pool
}

// isCacheableWebSocketWriteBufferSize reports whether a buffer size should use the global shared pool cache.
func isCacheableWebSocketWriteBufferSize(bufferSize int) bool {
	switch bufferSize {
	case defaultWebSocketBufferSize, 8 * 1024, 16 * 1024, 32 * 1024, 64 * 1024:
		return true
	default:
		return false
	}
}

// WithOriginCheck sets a custom origin validation function.
// If not set, gorilla/websocket applies its default same-origin policy.
func WithOriginCheck(fn func(r *http.Request) bool) ServerOption {
	return func(o *serverOptions) {
		o.checkOrigin = fn
	}
}

// WithAllowedOrigins sets an origin allowlist policy for websocket upgrades.
// See BuildOriginAllowlistCheck for the matching rules ("*" wildcard, "*."
// subdomain wildcards, and non-browser requests without an Origin header).
func WithAllowedOrigins(allowedOrigins ...string) ServerOption {
	return func(o *serverOptions) {
		o.checkOrigin = BuildOriginAllowlistCheck(allowedOrigins...)
	}
}

// WithAuthorize sets a pre-upgrade authorization hook. A non-nil returned
// error rejects the request with 403 Forbidden before the websocket upgrade.
func WithAuthorize(fn func(r *http.Request) error) ServerOption {
	return func(o *serverOptions) {
		o.authorize = fn
	}
}

// WithBufferSizes sets custom WebSocket buffer sizes.
func WithBufferSizes(read, write int) ServerOption {
	return func(o *serverOptions) {
		o.readBufferSize = read
		o.writeBufferSize = write
	}
}

// WithReadLimitBytes sets a websocket read limit for bridged clients.
func WithReadLimitBytes(limit int64) ServerOption {
	return func(o *serverOptions) {
		o.readLimitBytes = limit
	}
}

// WithReadLimitDisabled disables websocket read-size limiting for bridge handlers.
func WithReadLimitDisabled() ServerOption {
	return func(o *serverOptions) {
		o.shouldDisableReadLimit = true
	}
}

// WithKeepalive enables server-side websocket ping and idle timeout handling.
// When neither WithKeepalive nor WithKeepaliveDisabled is used, secure
// defaults apply (30s ping, 120s idle timeout).
func WithKeepalive(pingInterval time.Duration, idleTimeout time.Duration) ServerOption {
	return func(o *serverOptions) {
		o.pingInterval = pingInterval
		o.idleTimeout = idleTimeout
	}
}

// WithKeepaliveDisabled turns off default server keepalive probing. Without
// keepalive, silently dropped clients hold connection slots until the OS TCP
// timeout; disable only when an upstream boundary owns connection liveness.
func WithKeepaliveDisabled() ServerOption {
	return func(o *serverOptions) {
		o.shouldDisableKeepalive = true
	}
}

// WithSessionMaxLifetime force-closes tunnel sessions after the given
// duration. Use it to bound how long a session can outlive its upgrade-time
// authorization (token expiry) and to stay under reverse-proxy
// maximum-connection lifetimes. Clients reconnect automatically and re-pass
// the Authorize hook on the new upgrade.
func WithSessionMaxLifetime(maxLifetime time.Duration) ServerOption {
	return func(o *serverOptions) {
		o.sessionMaxLifetime = maxLifetime
	}
}

// WithNativeGRPCTransport serves tunneled sessions through grpc.Server.Serve
// and gRPC's own HTTP/2 transport instead of the net/http handler path. See
// BridgeConfig.ShouldUseNativeGRPCTransport for the tradeoffs.
func WithNativeGRPCTransport() ServerOption {
	return func(o *serverOptions) {
		o.shouldUseNativeTransport = true
	}
}

// WithBridgeWebSocketCompression enables websocket per-message compression for bridge handlers.
func WithBridgeWebSocketCompression() ServerOption {
	return func(o *serverOptions) {
		o.shouldEnableCompression = true
	}
}

// WithMaxActiveConnections sets a global concurrent connection cap for websocket bridge sessions.
func WithMaxActiveConnections(maxConnections int) ServerOption {
	return func(o *serverOptions) {
		o.maxActiveConnections = maxConnections
	}
}

// WithMaxConnectionsPerClient sets a per-client concurrent connection cap for websocket bridge sessions.
func WithMaxConnectionsPerClient(maxConnections int) ServerOption {
	return func(o *serverOptions) {
		o.maxConnectionsPerClient = maxConnections
	}
}

// WithMaxUpgradesPerClientPerMinute sets a per-client websocket upgrade-attempt limit over one minute.
func WithMaxUpgradesPerClientPerMinute(maxUpgrades int) ServerOption {
	return func(o *serverOptions) {
		o.maxUpgradesPerClient = maxUpgrades
	}
}

// WithConnectHook sets a callback for when clients connect.
func WithConnectHook(fn func(r *http.Request)) ServerOption {
	return func(o *serverOptions) {
		o.onConnect = fn
	}
}

// WithDisconnectHook sets a callback for when clients disconnect.
func WithDisconnectHook(fn func(r *http.Request)) ServerOption {
	return func(o *serverOptions) {
		o.onDisconnect = fn
	}
}

// GetBridgeConfigError validates BridgeConfig for server handler creation.
func GetBridgeConfigError(cfg BridgeConfig) error {
	if cfg.ReadBufferSize < 0 {
		return fmt.Errorf("grpctunnel: ReadBufferSize must be >= 0")
	}
	if cfg.WriteBufferSize < 0 {
		return fmt.Errorf("grpctunnel: WriteBufferSize must be >= 0")
	}
	if cfg.ReadLimitBytes < 0 {
		return fmt.Errorf("grpctunnel: ReadLimitBytes must be >= 0")
	}
	if cfg.ShouldDisableReadLimit && cfg.ReadLimitBytes > 0 {
		return fmt.Errorf("grpctunnel: ReadLimitBytes cannot be set when ShouldDisableReadLimit is true")
	}
	if cfg.PingInterval < 0 {
		return fmt.Errorf("grpctunnel: PingInterval must be >= 0")
	}
	if cfg.IdleTimeout < 0 {
		return fmt.Errorf("grpctunnel: IdleTimeout must be >= 0")
	}
	if cfg.IdleTimeout > 0 && cfg.PingInterval <= 0 {
		return fmt.Errorf("grpctunnel: PingInterval must be > 0 when IdleTimeout is set")
	}
	if cfg.IdleTimeout > 0 && cfg.PingInterval >= cfg.IdleTimeout {
		return fmt.Errorf("grpctunnel: PingInterval must be less than IdleTimeout")
	}
	if cfg.ShouldDisableKeepalive && (cfg.PingInterval > 0 || cfg.IdleTimeout > 0) {
		return fmt.Errorf("grpctunnel: PingInterval/IdleTimeout cannot be set when ShouldDisableKeepalive is true")
	}
	if cfg.SessionMaxLifetime < 0 {
		return fmt.Errorf("grpctunnel: SessionMaxLifetime must be >= 0")
	}
	if cfg.MaxActiveConnections < 0 {
		return fmt.Errorf("grpctunnel: MaxActiveConnections must be >= 0")
	}
	if cfg.MaxConnectionsPerClient < 0 {
		return fmt.Errorf("grpctunnel: MaxConnectionsPerClient must be >= 0")
	}
	if cfg.MaxUpgradesPerClientPerMinute < 0 {
		return fmt.Errorf("grpctunnel: MaxUpgradesPerClientPerMinute must be >= 0")
	}
	return nil
}

// Default keepalive cadence applied when the caller configures neither
// explicit keepalive values nor ShouldDisableKeepalive. 30s pings survive
// common proxy and NAT idle windows; a dead peer is reclaimed within 120s.
const defaultBridgePingInterval = 30 * time.Second
const defaultBridgeIdleTimeout = 120 * time.Second

// applyBridgeKeepaliveDefaults fills in default keepalive probing when the
// caller neither configured keepalive nor disabled it.
func applyBridgeKeepaliveDefaults(cfg BridgeConfig) BridgeConfig {
	if cfg.ShouldDisableKeepalive || cfg.PingInterval > 0 || cfg.IdleTimeout > 0 {
		return cfg
	}
	cfg.PingInterval = defaultBridgePingInterval
	cfg.IdleTimeout = defaultBridgeIdleTimeout
	return cfg
}

// applyBridgeConnectionSettings applies optional websocket limits and keepalive behavior.
func applyBridgeConnectionSettings(ws *websocket.Conn, cfg BridgeConfig) (func(), error) {
	readLimitBytes := getBridgeReadLimitBytes(cfg)
	if readLimitBytes > 0 {
		ws.SetReadLimit(readLimitBytes)
	}

	if cfg.IdleTimeout > 0 {
		if err := ws.SetReadDeadline(time.Now().Add(cfg.IdleTimeout)); err != nil {
			return nil, err
		}
		ws.SetPongHandler(func(string) error {
			return ws.SetReadDeadline(time.Now().Add(cfg.IdleTimeout))
		})
	}

	if cfg.PingInterval <= 0 {
		return func() {}, nil
	}

	stopChannel := make(chan struct{})
	var stopOnce sync.Once
	writeTimeout := buildBridgePingWriteTimeout(cfg)
	go func() {
		ticker := time.NewTicker(cfg.PingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout)); err != nil {
					_ = ws.Close()
					return
				}
			case <-stopChannel:
				return
			}
		}
	}()

	return func() {
		stopOnce.Do(func() {
			close(stopChannel)
		})
	}, nil
}

// getBridgeReadLimitBytes resolves websocket read-size guarding for bridge handlers.
func getBridgeReadLimitBytes(cfg BridgeConfig) int64 {
	if cfg.ShouldDisableReadLimit {
		return 0
	}
	if cfg.ReadLimitBytes > 0 {
		return cfg.ReadLimitBytes
	}
	return defaultReadLimitBytes
}

// buildBridgePingWriteTimeout derives a deadline for websocket ping control frames.
func buildBridgePingWriteTimeout(cfg BridgeConfig) time.Duration {
	writeTimeout := cfg.PingInterval
	if cfg.IdleTimeout > 0 && (writeTimeout <= 0 || writeTimeout > cfg.IdleTimeout) {
		writeTimeout = cfg.IdleTimeout
	}
	if writeTimeout <= 0 {
		writeTimeout = time.Second
	}
	return writeTimeout
}

// buildBridgeForwardHeaders builds per-connection HTTP/2 headers forwarded to backend gRPC metadata.
func buildBridgeForwardHeaders(r *http.Request, ctx context.Context) http.Header {
	forwardHeaders := make(http.Header)
	if r != nil {
		requestID := strings.TrimSpace(getGrpctunnelRequestID(r))
		if requestID != "" {
			forwardHeaders.Set(bridgeRequestIDMetadataKey, requestID)
		}
		correlationID := resolveBridgeHeaderValue(r.Header, "X-Correlation-Id", "X-Correlation-ID")
		if correlationID == "" {
			correlationID = requestID
		}
		if correlationID != "" {
			forwardHeaders.Set(bridgeCorrelationIDMetadataKey, correlationID)
		}
		traceParent := resolveBridgeHeaderValue(r.Header, bridgeTraceParentMetadataKey)
		if traceParent != "" {
			forwardHeaders.Set(bridgeTraceParentMetadataKey, traceParent)
		}
		traceState := resolveBridgeHeaderValue(r.Header, bridgeTraceStateMetadataKey)
		if traceState != "" {
			forwardHeaders.Set(bridgeTraceStateMetadataKey, traceState)
		}
	}
	if strings.TrimSpace(forwardHeaders.Get(bridgeTraceParentMetadataKey)) == "" {
		traceParent := buildBridgeTraceParentFromContext(ctx)
		if traceParent != "" {
			forwardHeaders.Set(bridgeTraceParentMetadataKey, traceParent)
		}
	}
	if strings.TrimSpace(forwardHeaders.Get(bridgeTraceStateMetadataKey)) == "" {
		traceState := buildBridgeTraceStateFromContext(ctx)
		if traceState != "" {
			forwardHeaders.Set(bridgeTraceStateMetadataKey, traceState)
		}
	}
	return forwardHeaders
}

// resolveBridgeHeaderValue returns the first non-empty value among candidate request headers.
func resolveBridgeHeaderValue(headers http.Header, headerNames ...string) string {
	if headers == nil {
		return ""
	}
	for _, headerName := range headerNames {
		headerName = strings.TrimSpace(headerName)
		if headerName == "" {
			continue
		}
		headerValue := strings.TrimSpace(headers.Get(headerName))
		if headerValue != "" {
			return headerValue
		}
	}
	return ""
}

// buildBridgeTraceParentFromContext formats one W3C traceparent value from context span state.
func buildBridgeTraceParentFromContext(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	flags := byte(spanContext.TraceFlags())
	return fmt.Sprintf("00-%s-%s-%02x", spanContext.TraceID().String(), spanContext.SpanID().String(), flags)
}

// buildBridgeTraceStateFromContext returns a trimmed tracestate value from context span state.
func buildBridgeTraceStateFromContext(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return strings.TrimSpace(spanContext.TraceState().String())
}

// forwardHeaderEntry is one precomputed header the bridge forwards into
// tunneled requests that do not already carry it.
type forwardHeaderEntry struct {
	key    string
	values []string
}

// wrapBridgeForwardMetadataHandler injects forwarded bridge headers into each
// tunneled HTTP/2 request. The forward set is fixed per websocket session, so
// it is snapshotted once; per request the handler clones nothing unless at
// least one header is actually missing, and then clones exactly once.
func wrapBridgeForwardMetadataHandler(handler http.Handler, forwardHeaders http.Header) http.Handler {
	if handler == nil {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}

	forwardEntries := make([]forwardHeaderEntry, 0, len(forwardHeaders))
	for headerKey, headerValues := range forwardHeaders {
		if len(headerValues) == 0 {
			continue
		}
		forwardEntries = append(forwardEntries, forwardHeaderEntry{
			key:    http.CanonicalHeaderKey(headerKey),
			values: append([]string{}, headerValues...),
		})
	}
	if len(forwardEntries) == 0 {
		return handler
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r == nil {
			handler.ServeHTTP(w, r)
			return
		}

		needsInjection := false
		for _, entry := range forwardEntries {
			if strings.TrimSpace(r.Header.Get(entry.key)) == "" {
				needsInjection = true
				break
			}
		}
		if !needsInjection {
			handler.ServeHTTP(w, r)
			return
		}

		forwarded := r.Clone(r.Context())
		if forwarded.Header == nil {
			forwarded.Header = make(http.Header)
		}
		for _, entry := range forwardEntries {
			if strings.TrimSpace(forwarded.Header.Get(entry.key)) != "" {
				continue
			}
			forwarded.Header[entry.key] = append([]string{}, entry.values...)
		}
		handler.ServeHTTP(w, forwarded)
	})
}

// BuildBridgeHandler creates a typed websocket handler for a gRPC server.
func BuildBridgeHandler(grpcServer *grpc.Server, cfg BridgeConfig) (http.Handler, error) {
	if grpcServer == nil {
		return nil, fmt.Errorf("grpctunnel: grpc server is required")
	}
	if err := GetBridgeConfigError(cfg); err != nil {
		return nil, err
	}
	cfg = applyBridgeKeepaliveDefaults(cfg)

	readBufferSize := cfg.ReadBufferSize
	if readBufferSize == 0 {
		readBufferSize = defaultWebSocketBufferSize
	}

	writeBufferSize := cfg.WriteBufferSize
	if writeBufferSize == 0 {
		writeBufferSize = defaultWebSocketBufferSize
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:    readBufferSize,
		WriteBufferSize:   writeBufferSize,
		WriteBufferPool:   buildWebSocketWriteBufferPool(writeBufferSize),
		CheckOrigin:       cfg.CheckOrigin,
		EnableCompression: cfg.ShouldEnableCompression,
	}
	// Inside ServeConn every request is already HTTP/2, so the gRPC server's
	// ServeHTTP handles them directly; no h2c upgrade shim is needed.
	http2Server := &http2.Server{}
	observability := buildBridgeObservability()
	abuseGuard := buildBridgeAbuseGuard(cfg)

	// Native mode feeds upgraded connections to grpc.Server.Serve so gRPC's
	// own HTTP/2 transport handles the session. The serve loop runs for the
	// handler's lifetime; grpcServer.Stop/GracefulStop closes the listener.
	var nativeListener *bridgeConnListener
	if cfg.ShouldUseNativeGRPCTransport {
		nativeListener = newBridgeConnListener()
		go func() {
			if err := grpcServer.Serve(nativeListener); err != nil && err != grpc.ErrServerStopped {
				logGrpctunnelEvent("grpctunnel.bridge", "WARN", "native_serve_stopped", nil, err, "Native gRPC serve loop stopped")
			}
		}()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgradeStart := time.Now()
		requestContext, requestSpan := observability.startBridgeRequestSpan(r.Context(), r)
		defer requestSpan.End()
		r = r.WithContext(requestContext)
		if cfg.Authorize != nil {
			if err := cfg.Authorize(r); err != nil {
				observability.storeBridgeUpgradeFailure(requestContext, time.Since(upgradeStart), r)
				logGrpctunnelEvent("grpctunnel.bridge", "WARN", "ws_upgrade_rejected_unauthorized", r, err, "WebSocket upgrade rejected by authorization hook")
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
		}
		if err := abuseGuard.reserveBridgeConnection(r, time.Now()); err != nil {
			observability.storeBridgeUpgradeFailure(requestContext, time.Since(upgradeStart), r)
			logGrpctunnelEvent("grpctunnel.bridge", "WARN", "ws_upgrade_rejected_abuse_control", r, err, "WebSocket upgrade rejected by abuse controls")
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
		defer abuseGuard.clearBridgeConnection(r)

		// Upgrade to WebSocket
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			observability.storeBridgeUpgradeFailure(requestContext, time.Since(upgradeStart), r)
			logGrpctunnelEvent("grpctunnel.bridge", "WARN", "ws_upgrade_failed", r, err, "WebSocket upgrade failed")
			return
		}
		observability.storeBridgeUpgradeSuccess(requestContext, time.Since(upgradeStart), r)
		observability.storeBridgeConnectionDelta(requestContext, r, 1)
		defer observability.storeBridgeConnectionDelta(requestContext, r, -1)
		sessionContext, sessionSpan := observability.startBridgeSessionSpan(requestContext, r)
		defer sessionSpan.End()
		r = r.WithContext(sessionContext)
		logGrpctunnelEvent("grpctunnel.bridge", "INFO", "ws_upgrade_succeeded", r, nil, "WebSocket upgrade succeeded")
		defer ws.Close()

		stopKeepalive, err := applyBridgeConnectionSettings(ws, cfg)
		if err != nil {
			logGrpctunnelEvent("grpctunnel.bridge", "WARN", "ws_connection_setup_failed", r, err, "WebSocket connection setup failed")
			return
		}
		defer stopKeepalive()

		// Lifecycle hooks
		if cfg.OnConnect != nil {
			cfg.OnConnect(r)
		}
		logGrpctunnelEvent("grpctunnel.bridge", "INFO", "tunnel_connect", r, nil, "Tunnel connected")
		defer func() {
			logGrpctunnelEvent("grpctunnel.bridge", "INFO", "tunnel_disconnect", r, nil, "Tunnel disconnected")
			if cfg.OnDisconnect != nil {
				cfg.OnDisconnect(r)
			}
		}()

		// Wrap WebSocket as net.Conn
		conn := newWebSocketConn(ws)
		defer conn.Close()

		if cfg.SessionMaxLifetime > 0 {
			lifetimeTimer := time.AfterFunc(cfg.SessionMaxLifetime, func() {
				logGrpctunnelEvent("grpctunnel.bridge", "INFO", "session_max_lifetime_reached", r, nil, "Session max lifetime reached; closing tunnel")
				_ = conn.Close()
			})
			defer lifetimeTimer.Stop()
		}

		if nativeListener != nil {
			// Hand the session to gRPC's native HTTP/2 transport and block
			// until the transport closes it, so disconnect hooks, metrics,
			// and abuse-guard release fire at the true end of the session.
			session := newNotifyCloseConn(conn)
			if !nativeListener.deliver(session) {
				logGrpctunnelEvent("grpctunnel.bridge", "WARN", "native_serve_unavailable", r, nil, "gRPC server stopped; rejecting tunneled session")
				return
			}
			<-session.done
			return
		}

		forwardHeaders := buildBridgeForwardHeaders(r, sessionContext)
		forwardHandler := wrapBridgeForwardMetadataHandler(grpcServer, forwardHeaders)

		// Serve gRPC over HTTP/2 on the WebSocket connection
		http2Server.ServeConn(conn, &http2.ServeConnOpts{
			Context: sessionContext,
			Handler: forwardHandler,
		})
	}), nil
}

// HandleBridgeMux registers a typed bridge handler on a mux path.
func HandleBridgeMux(mux *http.ServeMux, bridgePath string, grpcServer *grpc.Server, cfg BridgeConfig) error {
	if mux == nil {
		return fmt.Errorf("grpctunnel: mux is required")
	}
	if bridgePath == "" {
		return fmt.Errorf("grpctunnel: bridge path is required")
	}

	handler, err := BuildBridgeHandler(grpcServer, cfg)
	if err != nil {
		return err
	}
	mux.Handle(bridgePath, handler)
	return nil
}

// buildServerOptions applies functional server options onto defaults.
func buildServerOptions(opts ...ServerOption) *serverOptions {
	options := &serverOptions{
		readBufferSize:  defaultWebSocketBufferSize,
		writeBufferSize: defaultWebSocketBufferSize,
	}

	for _, opt := range opts {
		opt(options)
	}
	return options
}

// buildBridgeConfig converts resolved server options into a BridgeConfig.
func buildBridgeConfig(options *serverOptions) BridgeConfig {
	return BridgeConfig{
		CheckOrigin:                   options.checkOrigin,
		Authorize:                     options.authorize,
		ReadBufferSize:                options.readBufferSize,
		WriteBufferSize:               options.writeBufferSize,
		ReadLimitBytes:                options.readLimitBytes,
		ShouldDisableReadLimit:        options.shouldDisableReadLimit,
		PingInterval:                  options.pingInterval,
		IdleTimeout:                   options.idleTimeout,
		SessionMaxLifetime:            options.sessionMaxLifetime,
		ShouldDisableKeepalive:        options.shouldDisableKeepalive,
		ShouldUseNativeGRPCTransport:  options.shouldUseNativeTransport,
		ShouldEnableCompression:       options.shouldEnableCompression,
		MaxActiveConnections:          options.maxActiveConnections,
		MaxConnectionsPerClient:       options.maxConnectionsPerClient,
		MaxUpgradesPerClientPerMinute: options.maxUpgradesPerClient,
		OnConnect:                     options.onConnect,
		OnDisconnect:                  options.onDisconnect,
	}
}

// Wrap creates an http.Handler that serves a gRPC server over WebSocket.
// This is the middleware-style API for integrating WebSocket transport.
//
// Example:
//
//	grpcServer := grpc.NewServer()
//	proto.RegisterYourServiceServer(grpcServer, &yourImpl{})
//	http.ListenAndServe(":8080", grpctunnel.Wrap(grpcServer))
func Wrap(grpcServer *grpc.Server, opts ...ServerOption) http.Handler {
	handler, err := BuildBridgeHandler(grpcServer, buildBridgeConfig(buildServerOptions(opts...)))
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logGrpctunnelEvent("grpctunnel.bridge", "ERROR", "bridge_handler_init_failed", r, err, "Bridge handler initialization failed")
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		})
	}
	return handler
}

// NewServer builds an *http.Server that serves gRPC over WebSocket on addr.
// Callers own the returned server and can use its Shutdown method for
// graceful termination, wire in TLSConfig, or adjust timeouts.
//
// Example:
//
//	srv := grpctunnel.NewServer(":8080", grpcServer)
//	go srv.ListenAndServe()
//	...
//	srv.Shutdown(ctx)
func NewServer(addr string, grpcServer *grpc.Server, opts ...ServerOption) *http.Server {
	return &http.Server{
		Addr:         addr,
		Handler:      Wrap(grpcServer, opts...),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
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
func Serve(listener net.Listener, grpcServer *grpc.Server, opts ...ServerOption) error {
	return NewServer("", grpcServer, opts...).Serve(listener)
}

// ListenAndServe listens on the TCP network address and serves gRPC over WebSocket.
// This is the simplest one-liner for starting a gRPC-over-WebSocket server.
// For graceful shutdown, use NewServer instead.
//
// Example:
//
//	grpcServer := grpc.NewServer()
//	proto.RegisterYourServiceServer(grpcServer, &yourImpl{})
//	grpctunnel.ListenAndServe(":8080", grpcServer)
func ListenAndServe(addr string, grpcServer *grpc.Server, opts ...ServerOption) error {
	return NewServer(addr, grpcServer, opts...).ListenAndServe()
}

// ListenAndServeTLS listens on the TCP network address and serves gRPC over
// WebSocket with TLS (wss://). certFile and keyFile follow the semantics of
// http.Server.ListenAndServeTLS.
//
// Example:
//
//	grpctunnel.ListenAndServeTLS(":443", "cert.pem", "key.pem", grpcServer)
func ListenAndServeTLS(addr string, certFile string, keyFile string, grpcServer *grpc.Server, opts ...ServerOption) error {
	return NewServer(addr, grpcServer, opts...).ListenAndServeTLS(certFile, keyFile)
}
