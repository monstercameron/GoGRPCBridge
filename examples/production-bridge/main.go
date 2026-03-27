// Production bridge example - compatibility-oriented full configuration.
// For new integrations, prefer the typed pkg/grpctunnel API surface.
package main

import (
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/helpers"
)

type customLogger struct{}

func (parseL *customLogger) Printf(format string, parseV ...interface{}) {
	log.Printf("[Bridge] "+format, parseV...)
}

func main() {
	parseAddr := flag.String("addr", ":8443", "Bridge listen address")
	parseTarget := flag.String("target", "localhost:50051", "gRPC server address")
	parseCert := flag.String("cert", "cert.pem", "TLS certificate")
	parseKey := flag.String("key", "key.pem", "TLS private key")
	parseAllowedOrigins := flag.String("origins", "https://example.com", "Comma-separated allowed origins")
	flag.Parse()

	parseOrigins := strings.Split(*parseAllowedOrigins, ",")

	// Create bridge with production config
	handler := helpers.NewHandler(helpers.Config{
		TargetAddress: *parseTarget,

		// Custom origin validation
		CheckOrigin: func(parseR *http.Request) bool {
			parseOrigin := parseR.Header.Get("Origin")
			for _, parseAllowed := range parseOrigins {
				if parseOrigin == strings.TrimSpace(parseAllowed) {
					return true
				}
			}
			log.Printf("Rejected connection from origin: %s", parseOrigin)
			return false
		},

		// Larger buffers for production
		ReadBufferSize:  8192,
		WriteBufferSize: 8192,

		// Custom logger
		Logger: &customLogger{},

		// Connection lifecycle hooks
		OnConnect: func(parseR2 *http.Request) {
			log.Printf("Client connected: %s (Origin: %s, User-Agent: %s)",
				parseR2.RemoteAddr,
				parseR2.Header.Get("Origin"),
				parseR2.Header.Get("User-Agent"))
		},

		OnDisconnect: func(parseR3 *http.Request) {
			log.Printf("Client disconnected: %s", parseR3.RemoteAddr)
		},
	})

	parseServer := &http.Server{
		Addr:         *parseAddr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("Production bridge starting on %s", *parseAddr)
	log.Printf("Proxying to gRPC server at %s", *parseTarget)
	log.Printf("Allowed origins: %v", parseOrigins)
	log.Fatal(parseServer.ListenAndServeTLS(*parseCert, *parseKey))
}
