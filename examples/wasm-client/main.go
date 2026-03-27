//go:build js && wasm

package main

import (
	"context"
	"log"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"
	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
)

func main() {
	log.Println("WASM: Starting new gRPC client...")
	const parseBridgeWebSocketURL = "ws://127.0.0.1:5000/"

	// Establish a gRPC connection via the WebSocket tunnel
	parseDialContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseGrpcConnection, parseDialError := grpctunnel.BuildTunnelConn(parseDialContext, grpctunnel.TunnelConfig{
		Target:      parseBridgeWebSocketURL,
		GRPCOptions: grpctunnel.ApplyTunnelInsecureCredentials(nil),
	})
	if parseDialError != nil {
		log.Fatalf("WASM: Failed to connect to gRPC server via WebSocket: %v", parseDialError)
	}
	defer func() {
		if parseCloseError := parseGrpcConnection.Close(); parseCloseError != nil {
			log.Printf("WASM: Error closing gRPC connection: %v", parseCloseError)
		}
	}()

	parseTodoServiceClient := proto.NewTodoServiceClient(parseGrpcConnection)

	// Make a simple CreateTodo RPC call
	parseCreateRequest := &proto.CreateTodoRequest{Text: "Learn gRPC-over-WebSocket"}
	parseContextWithTimeout, parseContextCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer parseContextCancel()

	parseCreateResponse, parseCreateError := parseTodoServiceClient.CreateTodo(parseContextWithTimeout, parseCreateRequest)
	if parseCreateError != nil {
		log.Fatalf("WASM: Failed to create todo: %v", parseCreateError)
	}
	log.Printf("WASM: Created new todo: ID=%s, Text=%s, Done=%t",
		parseCreateResponse.GetTodo().GetId(),
		parseCreateResponse.GetTodo().GetText(),
		parseCreateResponse.GetTodo().GetDone(),
	)

	// Keep the WASM runtime alive
	select {}
}
