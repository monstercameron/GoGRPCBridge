// Custom router example - compatibility wiring for existing HTTP servers.
// For new integrations, prefer the typed pkg/grpctunnel API surface.
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/helpers"
)

func main() {
	parseMux := http.NewServeMux()

	// Health check endpoint
	parseMux.HandleFunc("/health", func(parseW http.ResponseWriter, parseR *http.Request) {
		parseW.WriteHeader(http.StatusOK)
		if _, parseErr := parseW.Write([]byte("OK")); parseErr != nil {
			log.Printf("Failed to write health response: %v", parseErr)
		}
	})

	// Metrics endpoint
	parseMux.HandleFunc("/metrics", func(parseW2 http.ResponseWriter, parseR2 *http.Request) {
		// Your metrics implementation
		if _, parseErr2 := parseW2.Write([]byte("# Metrics here\n")); parseErr2 != nil {
			log.Printf("Failed to write metrics response: %v", parseErr2)
		}
	})

	// gRPC bridge on specific path
	parseMux.Handle("/grpc", helpers.NewHandler(helpers.Config{
		TargetAddress: "localhost:50051",
	}))

	// Another bridge for a different service
	parseMux.Handle("/api/v2/grpc", helpers.NewHandler(helpers.Config{
		TargetAddress: "localhost:50052",
	}))

	parseServer := &http.Server{
		Addr:         ":8080",
		Handler:      parseMux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Println("Server with multiple endpoints listening on :8080")
	log.Println("  /health       - Health check")
	log.Println("  /metrics      - Prometheus metrics")
	log.Println("  /grpc         - gRPC bridge to :50051")
	log.Println("  /api/v2/grpc  - gRPC bridge to :50052")
	log.Fatal(parseServer.ListenAndServe())
}
