package bridge

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const parseDefaultBackendDialTimeout = 10 * time.Second
const parseDefaultWebSocketBufferSize = 4096
const parseDefaultReadLimitBytes int64 = 16 << 20
const parseReverseProxyBufferSize = 32 * 1024

var cacheWebSocketWriteBufferPools sync.Map

// Config holds configuration options for the gRPC-over-WebSocket bridge.
type Config struct {
	// TargetAddress is the address of the backend gRPC server (e.g., "localhost:50051")
	TargetAddress string

	// CheckOrigin is called during the WebSocket upgrade to determine whether the origin is allowed.
	// If nil, gorilla/websocket applies its default same-origin policy.
	CheckOrigin func(r *http.Request) bool

	// ReadBufferSize is the WebSocket read buffer size in bytes.
	// Default: 4096
	ReadBufferSize int

	// WriteBufferSize is the WebSocket write buffer size in bytes.
	// Default: 4096
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

	// BackendDialTimeout limits backend TCP dial time for proxied gRPC traffic.
	// Default: 10s
	BackendDialTimeout time.Duration

	// ShouldRequireLoopbackBackend rejects handler startup when plaintext backend transport
	// targets a non-loopback host. Enable this for production boundaries where bridge-to-backend
	// traffic must remain local to the host network namespace.
	ShouldRequireLoopbackBackend bool

	// MaxActiveConnections limits total concurrent websocket tunnel connections.
	// Zero disables this guard.
	MaxActiveConnections int

	// MaxConnectionsPerClient limits concurrent websocket tunnel connections per client key.
	// Client key is derived from request remote address host. Zero disables this guard.
	MaxConnectionsPerClient int

	// MaxUpgradesPerClientPerMinute limits websocket upgrade attempts per client key over a 1-minute window.
	// Zero disables this guard.
	MaxUpgradesPerClientPerMinute int

	// ShouldEnableCompression enables websocket per-message compression where supported.
	ShouldEnableCompression bool

	// Logger is used for logging. If nil, the default logger is used.
	Logger Logger

	// OnConnect is called when a WebSocket connection is established.
	OnConnect func(r *http.Request)

	// OnDisconnect is called when a WebSocket connection ends.
	OnDisconnect func(r *http.Request)
}

// Logger interface for custom logging.
type Logger interface {
	Printf(format string, v ...interface{})
}

type defaultLogger struct{}

// Printf writes a formatted log line via the standard library logger.
func (defaultLogger) Printf(format string, parseV ...interface{}) {
	log.Printf(format, parseV...)
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

// reverseProxyBufferPool reuses copy buffers for ReverseProxy streams.
type reverseProxyBufferPool struct {
	buildBufferPool sync.Pool
}

// Get returns a reusable proxy copy buffer.
func (parsePool *reverseProxyBufferPool) Get() []byte {
	parseBuffer := parsePool.buildBufferPool.Get()
	if parseBuffer == nil {
		return make([]byte, parseReverseProxyBufferSize)
	}

	switch parseTypedBuffer := parseBuffer.(type) {
	case *[parseReverseProxyBufferSize]byte:
		return parseTypedBuffer[:]
	case []byte:
		if cap(parseTypedBuffer) < parseReverseProxyBufferSize {
			return make([]byte, parseReverseProxyBufferSize)
		}
		return parseTypedBuffer[:parseReverseProxyBufferSize]
	default:
		return make([]byte, parseReverseProxyBufferSize)
	}
}

// Put returns a proxy copy buffer to the pool.
func (parsePool *reverseProxyBufferPool) Put(parseBuffer []byte) {
	if cap(parseBuffer) < parseReverseProxyBufferSize {
		return
	}
	parseSizedBuffer := parseBuffer[:parseReverseProxyBufferSize]
	parsePool.buildBufferPool.Put((*[parseReverseProxyBufferSize]byte)(parseSizedBuffer))
}

// Handler is the gRPC-over-WebSocket bridge handler.
type Handler struct {
	config          Config
	upgrader        websocket.Upgrader
	proxy           *httputil.ReverseProxy
	logger          Logger
	http2Server     *http2.Server
	serveH2CHandler http.Handler
	abuseGuard      *handlerAbuseGuard
	initErr         error
}

// NewHandler creates a new gRPC-over-WebSocket bridge handler.
// This is the main entry point for integrating the bridge into your application.
//
// Example:
//
//	bridge := bridge.NewHandler(bridge.Config{
//	    TargetAddress: "localhost:50051",
//	})
//	http.Handle("/", bridge)
//	http.ListenAndServe(":8080", nil)
func NewHandler(parseCfg Config) *Handler {
	// Set defaults
	if parseCfg.ReadBufferSize == 0 {
		parseCfg.ReadBufferSize = parseDefaultWebSocketBufferSize
	}
	if parseCfg.WriteBufferSize == 0 {
		parseCfg.WriteBufferSize = parseDefaultWebSocketBufferSize
	}
	if parseCfg.BackendDialTimeout == 0 {
		parseCfg.BackendDialTimeout = parseDefaultBackendDialTimeout
	}
	if parseCfg.Logger == nil {
		parseCfg.Logger = defaultLogger{}
	}

	parseH := &Handler{
		config:      parseCfg,
		logger:      parseCfg.Logger,
		http2Server: &http2.Server{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:    parseCfg.ReadBufferSize,
			WriteBufferSize:   parseCfg.WriteBufferSize,
			WriteBufferPool:   buildWebSocketWriteBufferPool(parseCfg.WriteBufferSize),
			CheckOrigin:       parseCfg.CheckOrigin,
			EnableCompression: parseCfg.ShouldEnableCompression,
		},
		abuseGuard: buildHandlerAbuseGuard(parseCfg),
	}

	if parseErr := getHandlerConfigError(parseCfg); parseErr != nil {
		parseH.initErr = parseErr
		logBridgeEvent(parseH.logger, "WARN", "bridge_config_invalid", nil, parseErr, "Bridge configuration warning")
		return parseH
	}

	parseTargetURL, parseErr := parseBridgeTargetURL(parseCfg.TargetAddress)
	if parseErr != nil {
		parseH.initErr = parseErr
		logBridgeEvent(parseH.logger, "WARN", "bridge_config_invalid", nil, parseErr, "Bridge configuration warning")
		return parseH
	}
	if parseErr = getBridgeBackendTransportPolicyError(parseCfg, parseTargetURL); parseErr != nil {
		parseH.initErr = parseErr
		logBridgeEvent(parseH.logger, "ERROR", "backend_transport_policy_violation", nil, parseErr, "Bridge backend transport policy violation")
		return parseH
	}
	if shouldWarnBridgePlaintextBackend(parseTargetURL.Hostname()) {
		logBridgeEvent(
			parseH.logger,
			"WARN",
			"backend_plaintext_non_loopback",
			nil,
			nil,
			fmt.Sprintf(
				"Bridge security warning: TargetAddress %q uses plaintext h2c backend transport to non-loopback host %q. Ensure this hop is on a trusted private network or terminate TLS before the bridge.",
				parseCfg.TargetAddress,
				parseTargetURL.Hostname(),
			),
		)
	}

	parseBackendDialer := &net.Dialer{
		Timeout: parseCfg.BackendDialTimeout,
	}

	parseProxyBufferPool := &reverseProxyBufferPool{}

	// Create the reverse proxy
	parseH.proxy = &httputil.ReverseProxy{
		Director: func(parseReq *http.Request) {
			parseReq.URL.Scheme = parseTargetURL.Scheme
			parseReq.URL.Host = parseTargetURL.Host
			parseReq.Host = parseTargetURL.Host
		},
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(parseDialContext context.Context, parseNetwork string, parseAddr string, parseTLSConfig *tls.Config) (net.Conn, error) {
				return parseBackendDialer.DialContext(parseDialContext, parseNetwork, parseAddr)
			},
		},
		ErrorHandler: func(parseW http.ResponseWriter, parseR2 *http.Request, parseErr error) {
			logBridgeEvent(parseH.logger, "ERROR", "backend_proxy_error", parseR2, parseErr, "Proxy error")
			http.Error(parseW, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
		BufferPool: parseProxyBufferPool,
	}
	parseH.serveH2CHandler = h2c.NewHandler(parseH.proxy, parseH.http2Server)

	return parseH
}

// ServeHTTP implements http.Handler. This is called for each incoming HTTP request.
func (parseH *Handler) ServeHTTP(parseW http.ResponseWriter, parseR *http.Request) {
	if parseH.initErr != nil {
		logBridgeEvent(parseH.logger, "ERROR", "bridge_request_rejected", parseR, parseH.initErr, "Bridge request rejected due to configuration error")
		http.Error(parseW, parseH.initErr.Error(), http.StatusInternalServerError)
		return
	}

	if parseErr := parseH.abuseGuard.reserveHandlerConnection(parseR, time.Now()); parseErr != nil {
		logBridgeEvent(parseH.logger, "WARN", "ws_upgrade_rejected_abuse_control", parseR, parseErr, "WebSocket upgrade rejected by abuse controls")
		// Before http.Error, which writes the status: a header set after that is
		// a header nobody receives.
		setRetryAfterHeader(parseW, parseErr)
		http.Error(parseW, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	defer parseH.abuseGuard.clearHandlerConnection(parseR)

	// Upgrade to WebSocket
	parseWs, parseErr := parseH.upgrader.Upgrade(parseW, parseR, nil)
	if parseErr != nil {
		logBridgeEvent(parseH.logger, "WARN", "ws_upgrade_failed", parseR, parseErr, "WebSocket upgrade failed")
		return
	}
	logBridgeEvent(parseH.logger, "INFO", "ws_upgrade_succeeded", parseR, nil, "WebSocket upgrade succeeded")
	defer parseWs.Close()

	parseStopKeepalive, parseErr := applyHandlerConnectionSettings(parseWs, parseH.config)
	if parseErr != nil {
		logBridgeEvent(parseH.logger, "WARN", "ws_connection_setup_failed", parseR, parseErr, "WebSocket connection setup failed")
		return
	}
	defer parseStopKeepalive()

	// Call OnConnect callback
	if parseH.config.OnConnect != nil {
		parseH.config.OnConnect(parseR)
	}
	logBridgeEvent(parseH.logger, "INFO", "tunnel_connect", parseR, nil, "Tunnel connected")
	defer func() {
		logBridgeEvent(parseH.logger, "INFO", "tunnel_disconnect", parseR, nil, "Tunnel disconnected")
		if parseH.config.OnDisconnect != nil {
			parseH.config.OnDisconnect(parseR)
		}
	}()

	// Wrap WebSocket as net.Conn
	parseConn := NewWebSocketConn(parseWs)
	defer parseConn.Close()

	// Serve HTTP/2 over the WebSocket connection
	parseHTTP2Server := parseH.http2Server
	if parseHTTP2Server == nil {
		parseHTTP2Server = &http2.Server{}
	}
	parseServeH2CHandler := parseH.serveH2CHandler
	if parseServeH2CHandler == nil {
		parseServeH2CHandler = h2c.NewHandler(parseH.proxy, parseHTTP2Server)
	}
	parseHTTP2Server.ServeConn(parseConn, &http2.ServeConnOpts{
		Handler: parseServeH2CHandler,
	})
}

// parseBridgeTargetURL validates the configured backend address and returns a proxy target URL.
func parseBridgeTargetURL(parseTargetAddress string) (*url.URL, error) {
	parseTargetAddress = strings.TrimSpace(parseTargetAddress)
	if parseTargetAddress == "" {
		return nil, fmt.Errorf("bridge: target address is required")
	}
	if strings.Contains(parseTargetAddress, "://") {
		parseConfiguredTargetURL, parseErr := url.Parse(parseTargetAddress)
		if parseErr != nil {
			return nil, fmt.Errorf("bridge: invalid target address %q: %w", parseTargetAddress, parseErr)
		}
		if parseConfiguredTargetURL.Scheme != "http" {
			return nil, fmt.Errorf("bridge: unsupported target scheme %q; use host:port or http://host:port", parseConfiguredTargetURL.Scheme)
		}
		if parseConfiguredTargetURL.Host == "" {
			return nil, fmt.Errorf("bridge: invalid target address %q", parseTargetAddress)
		}
		if (parseConfiguredTargetURL.Path != "" && parseConfiguredTargetURL.Path != "/") ||
			parseConfiguredTargetURL.RawQuery != "" ||
			parseConfiguredTargetURL.Fragment != "" {
			return nil, fmt.Errorf("bridge: target address %q must not include path, query, or fragment", parseTargetAddress)
		}
		parseTargetAddress = parseConfiguredTargetURL.Host
	}

	parseTargetURL, parseErr := url.Parse("http://" + parseTargetAddress)
	if parseErr != nil {
		return nil, fmt.Errorf("bridge: invalid target address %q: %w", parseTargetAddress, parseErr)
	}
	if parseTargetURL.Host == "" {
		return nil, fmt.Errorf("bridge: invalid target address %q", parseTargetAddress)
	}
	if (parseTargetURL.Path != "" && parseTargetURL.Path != "/") ||
		parseTargetURL.RawQuery != "" ||
		parseTargetURL.Fragment != "" {
		return nil, fmt.Errorf("bridge: target address %q must not include path, query, or fragment", parseTargetAddress)
	}
	return parseTargetURL, nil
}

// shouldWarnBridgePlaintextBackend reports whether plaintext backend transport should emit a warning.
func shouldWarnBridgePlaintextBackend(parseHost string) bool {
	parseHost = strings.TrimSpace(parseHost)
	if parseHost == "" {
		return true
	}
	if strings.EqualFold(parseHost, "localhost") {
		return false
	}
	parseIP := net.ParseIP(parseHost)
	if parseIP != nil {
		return !parseIP.IsLoopback()
	}
	return true
}

// getBridgeBackendTransportPolicyError validates strict backend transport policy for non-loopback plaintext targets.
func getBridgeBackendTransportPolicyError(parseConfig Config, parseTargetURL *url.URL) error {
	if !parseConfig.ShouldRequireLoopbackBackend || parseTargetURL == nil {
		return nil
	}
	if !shouldWarnBridgePlaintextBackend(parseTargetURL.Hostname()) {
		return nil
	}
	return fmt.Errorf(
		"bridge: TargetAddress %q violates backend transport policy; non-loopback plaintext backend targets are not allowed when ShouldRequireLoopbackBackend is true",
		parseConfig.TargetAddress,
	)
}

// getHandlerConfigError validates optional bridge handler websocket settings.
func getHandlerConfigError(parseConfig Config) error {
	if parseConfig.ReadBufferSize < 0 {
		return fmt.Errorf("bridge: ReadBufferSize must be >= 0")
	}
	if parseConfig.WriteBufferSize < 0 {
		return fmt.Errorf("bridge: WriteBufferSize must be >= 0")
	}
	if parseConfig.ReadLimitBytes < 0 {
		return fmt.Errorf("bridge: ReadLimitBytes must be >= 0")
	}
	if parseConfig.ShouldDisableReadLimit && parseConfig.ReadLimitBytes > 0 {
		return fmt.Errorf("bridge: ReadLimitBytes cannot be set when ShouldDisableReadLimit is true")
	}
	if parseConfig.PingInterval < 0 {
		return fmt.Errorf("bridge: PingInterval must be >= 0")
	}
	if parseConfig.IdleTimeout < 0 {
		return fmt.Errorf("bridge: IdleTimeout must be >= 0")
	}
	if parseConfig.BackendDialTimeout < 0 {
		return fmt.Errorf("bridge: BackendDialTimeout must be >= 0")
	}
	if parseConfig.IdleTimeout > 0 && parseConfig.PingInterval <= 0 {
		return fmt.Errorf("bridge: PingInterval must be > 0 when IdleTimeout is set")
	}
	if parseConfig.IdleTimeout > 0 && parseConfig.PingInterval >= parseConfig.IdleTimeout {
		return fmt.Errorf("bridge: PingInterval must be less than IdleTimeout")
	}
	if parseConfig.MaxActiveConnections < 0 {
		return fmt.Errorf("bridge: MaxActiveConnections must be >= 0")
	}
	if parseConfig.MaxConnectionsPerClient < 0 {
		return fmt.Errorf("bridge: MaxConnectionsPerClient must be >= 0")
	}
	if parseConfig.MaxUpgradesPerClientPerMinute < 0 {
		return fmt.Errorf("bridge: MaxUpgradesPerClientPerMinute must be >= 0")
	}
	return nil
}

// applyHandlerConnectionSettings applies optional websocket limits and keepalive behavior.
func applyHandlerConnectionSettings(parseWebSocket *websocket.Conn, parseConfig Config) (func(), error) {
	parseReadLimitBytes := getHandlerReadLimitBytes(parseConfig)
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
	parseWriteTimeout := buildHandlerPingWriteTimeout(parseConfig)
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

// getHandlerReadLimitBytes resolves websocket read-size guarding for bridge handlers.
func getHandlerReadLimitBytes(parseConfig Config) int64 {
	if parseConfig.ShouldDisableReadLimit {
		return 0
	}
	if parseConfig.ReadLimitBytes > 0 {
		return parseConfig.ReadLimitBytes
	}
	return parseDefaultReadLimitBytes
}

// buildHandlerPingWriteTimeout derives a deadline for websocket ping control frames.
func buildHandlerPingWriteTimeout(parseConfig Config) time.Duration {
	parseWriteTimeout := parseConfig.PingInterval
	if parseConfig.IdleTimeout > 0 && (parseWriteTimeout <= 0 || parseWriteTimeout > parseConfig.IdleTimeout) {
		parseWriteTimeout = parseConfig.IdleTimeout
	}
	if parseWriteTimeout <= 0 {
		parseWriteTimeout = time.Second
	}
	return parseWriteTimeout
}
