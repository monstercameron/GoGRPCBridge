package helpers

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/monstercameron/GoGRPCBridge/pkg/bridge"

	"github.com/gorilla/websocket"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const parseDefaultBackendDialTimeout = 10 * time.Second

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

func (defaultLogger) Printf(format string, parseV ...interface{}) {
	log.Printf(format, parseV...)
}

// Handler is the gRPC-over-WebSocket bridge handler.
type Handler struct {
	config         Config
	upgrader       websocket.Upgrader
	proxy          *httputil.ReverseProxy
	logger         Logger
	http2Server    *http2.Server
	serveH2CHandle http.Handler
	initErr        error
}

// NewHandler creates a new gRPC-over-WebSocket bridge handler.
// This is the main entry point for integrating the bridge into your application.
//
// Example:
//
//	handler := helpers.NewHandler(helpers.Config{
//	    TargetAddress: "localhost:50051",
//	})
//	http.Handle("/", handler)
//	http.ListenAndServe(":8080", nil)
func NewHandler(parseCfg Config) *Handler {
	// Set defaults
	if parseCfg.ReadBufferSize == 0 {
		parseCfg.ReadBufferSize = parseDefaultWebSocketBufferSize
	}
	if parseCfg.WriteBufferSize == 0 {
		parseCfg.WriteBufferSize = parseDefaultWebSocketBufferSize
	}
	if parseCfg.Logger == nil {
		parseCfg.Logger = defaultLogger{}
	}

	parseH := &Handler{
		config:      parseCfg,
		logger:      parseCfg.Logger,
		http2Server: &http2.Server{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  parseCfg.ReadBufferSize,
			WriteBufferSize: parseCfg.WriteBufferSize,
			WriteBufferPool: buildWebSocketWriteBufferPool(parseCfg.WriteBufferSize),
			CheckOrigin:     parseCfg.CheckOrigin,
		},
	}

	parseTargetURL, parseErr := parseProxyTargetURL(parseCfg.TargetAddress)
	if parseErr != nil {
		parseH.initErr = parseErr
		parseH.logger.Printf("Bridge configuration warning: %v", parseErr)
		return parseH
	}
	if shouldWarnProxyPlaintextBackend(parseTargetURL.Hostname()) {
		parseH.logger.Printf(
			"Bridge security warning: TargetAddress %q uses plaintext h2c backend transport to non-loopback host %q. Ensure this hop is on a trusted private network or terminate TLS before the bridge.",
			parseCfg.TargetAddress,
			parseTargetURL.Hostname(),
		)
	}

	// Create the reverse proxy
	parseH.proxy = &httputil.ReverseProxy{
		Director: func(parseReq *http.Request) {
			parseReq.URL.Scheme = parseTargetURL.Scheme
			parseReq.URL.Host = parseTargetURL.Host
			parseReq.Host = parseTargetURL.Host
		},
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLS: func(parseNetwork, parseAddr string, parseCfg2 *tls.Config) (net.Conn, error) {
				return net.DialTimeout(parseNetwork, parseAddr, parseDefaultBackendDialTimeout)
			},
		},
		ErrorHandler: func(parseW http.ResponseWriter, parseR2 *http.Request, parseErr error) {
			parseH.logger.Printf("Proxy error: %v", parseErr)
			http.Error(parseW, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
	}
	parseH.serveH2CHandle = h2c.NewHandler(parseH.proxy, parseH.http2Server)

	return parseH
}

// ServeHTTP implements http.Handler. This is called for each incoming HTTP request.
func (parseH *Handler) ServeHTTP(parseW http.ResponseWriter, parseR *http.Request) {
	if parseH.initErr != nil {
		parseH.logger.Printf("Bridge request rejected due to configuration error: %v", parseH.initErr)
		http.Error(parseW, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// Upgrade to WebSocket
	parseWs, parseErr := parseH.upgrader.Upgrade(parseW, parseR, nil)
	if parseErr != nil {
		parseH.logger.Printf("WebSocket upgrade failed: %v", parseErr)
		return
	}
	defer parseWs.Close()

	// Call OnConnect callback
	if parseH.config.OnConnect != nil {
		parseH.config.OnConnect(parseR)
	}
	defer func() {
		if parseH.config.OnDisconnect != nil {
			parseH.config.OnDisconnect(parseR)
		}
	}()

	// Wrap WebSocket as net.Conn
	parseConn := bridge.NewWebSocketConn(parseWs)
	defer parseConn.Close()

	// Serve HTTP/2 over the WebSocket connection
	parseHTTP2Server := parseH.http2Server
	parseServeH2CHandle := parseH.serveH2CHandle
	if parseHTTP2Server == nil {
		parseHTTP2Server = &http2.Server{}
		parseServeH2CHandle = h2c.NewHandler(parseH.proxy, parseHTTP2Server)
	}
	parseHTTP2Server.ServeConn(parseConn, &http2.ServeConnOpts{
		Handler: parseServeH2CHandle,
	})
}

// parseProxyTargetURL validates the backend target and returns an HTTP URL.
func parseProxyTargetURL(parseTargetAddress string) (*url.URL, error) {
	if parseTargetAddress == "" {
		return nil, fmt.Errorf("helpers: target address is required")
	}

	parseTargetURL, parseErr := url.Parse("http://" + parseTargetAddress)
	if parseErr != nil {
		return nil, fmt.Errorf("helpers: invalid target address %q: %w", parseTargetAddress, parseErr)
	}
	if parseTargetURL.Host == "" {
		return nil, fmt.Errorf("helpers: invalid target address %q", parseTargetAddress)
	}
	return parseTargetURL, nil
}

// shouldWarnProxyPlaintextBackend reports whether plaintext backend transport should emit a warning.
func shouldWarnProxyPlaintextBackend(parseHost string) bool {
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
