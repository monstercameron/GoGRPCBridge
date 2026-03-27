// Simple bridge server example - minimal compatibility configuration.
// For new integrations, prefer the typed pkg/grpctunnel API surface.
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/helpers"
)

func main() {
	// Create bridge with minimal config
	handler := helpers.NewHandler(helpers.Config{
		TargetAddress: "localhost:50051",
	})

	parseServer := &http.Server{
		Addr:         ":8080",
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Println("Bridge listening on :8080")
	log.Println("Proxying to gRPC server at localhost:50051")
	log.Fatal(parseServer.ListenAndServe())
}
