package helpers

import (
	"net"
	"net/http"
	"time"

	"github.com/monstercameron/GoGRPCBridge/pkg/bridge"

	"github.com/gorilla/websocket"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
)

// ServerConfig holds configuration for the server-side bridge.
type ServerConfig struct {
	// GRPCServer is the gRPC server to serve over WebSocket
	GRPCServer *grpc.Server

	// CheckOrigin validates websocket upgrade origins.
	// If nil, gorilla/websocket applies its default same-origin policy.
	CheckOrigin func(r *http.Request) bool

	// ReadBufferSize for WebSocket (default: 4096)
	ReadBufferSize int

	// WriteBufferSize for WebSocket (default: 4096)
	WriteBufferSize int

	// OnConnect is called when a client connects
	OnConnect func(r *http.Request)

	// OnDisconnect is called when a client disconnects
	OnDisconnect func(r *http.Request)
}

// ServeHandler creates an http.Handler that serves a gRPC server over WebSocket.
// Use this on the server side to accept gRPC connections via WebSocket.
//
// Example:
//
//	grpcServer := grpc.NewServer()
//	proto.RegisterYourServiceServer(grpcServer, &yourImpl{})
//
//	http.Handle("/grpc", bridge.ServeHandler(bridge.ServerConfig{
//	    GRPCServer: grpcServer,
//	}))
//	http.ListenAndServe(":8080", nil)
func ServeHandler(parseCfg ServerConfig) http.Handler {
	// Set defaults
	if parseCfg.ReadBufferSize == 0 {
		parseCfg.ReadBufferSize = parseDefaultWebSocketBufferSize
	}
	if parseCfg.WriteBufferSize == 0 {
		parseCfg.WriteBufferSize = parseDefaultWebSocketBufferSize
	}

	parseUpgrader := websocket.Upgrader{
		ReadBufferSize:  parseCfg.ReadBufferSize,
		WriteBufferSize: parseCfg.WriteBufferSize,
		WriteBufferPool: buildWebSocketWriteBufferPool(parseCfg.WriteBufferSize),
		CheckOrigin:     parseCfg.CheckOrigin,
	}
	parseHTTP2Server := &http2.Server{}
	parseServeH2CHandler := h2c.NewHandler(parseCfg.GRPCServer, parseHTTP2Server)

	return http.HandlerFunc(func(parseW http.ResponseWriter, parseR2 *http.Request) {
		// Upgrade to WebSocket
		parseWs, parseErr := parseUpgrader.Upgrade(parseW, parseR2, nil)
		if parseErr != nil {
			return
		}
		defer parseWs.Close()

		// Lifecycle hooks
		if parseCfg.OnConnect != nil {
			parseCfg.OnConnect(parseR2)
		}
		defer func() {
			if parseCfg.OnDisconnect != nil {
				parseCfg.OnDisconnect(parseR2)
			}
		}()

		// Wrap WebSocket as net.Conn
		parseConn := bridge.NewWebSocketConn(parseWs)
		defer parseConn.Close()

		// Serve gRPC over HTTP/2 on the WebSocket connection
		parseHTTP2Server.ServeConn(parseConn, &http2.ServeConnOpts{
			Handler: parseServeH2CHandler,
		})
	})
}

// Serve accepts WebSocket connections and serves gRPC over them.
// This is a convenience wrapper around ServeHandler for simple cases.
//
// Example:
//
//	grpcServer := grpc.NewServer()
//	proto.RegisterYourServiceServer(grpcServer, &yourImpl{})
//
//	lis, _ := net.Listen("tcp", ":8080")
//	bridge.Serve(lis, grpcServer)
func Serve(parseListener net.Listener, parseGrpcServer *grpc.Server) error {
	handler := ServeHandler(ServerConfig{
		GRPCServer: parseGrpcServer,
	})
	parseServer := &http.Server{
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return parseServer.Serve(parseListener)
}
