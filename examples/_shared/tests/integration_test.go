//go:build !js && !wasm

package tests

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/helpers"
	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"
	"github.com/monstercameron/GoGRPCBridge/pkg/bridge"
	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// mockTodoService implements a simple in-memory TodoService for testing
type mockTodoService struct {
	proto.UnimplementedTodoServiceServer
}

func (parseS *mockTodoService) CreateTodo(parseCtx context.Context, parseReq *proto.CreateTodoRequest) (*proto.CreateTodoResponse, error) {
	return &proto.CreateTodoResponse{
		Todo: &proto.Todo{
			Id:   "test-123",
			Text: parseReq.Text,
			Done: false,
		},
	}, nil
}

func (parseS *mockTodoService) ListTodos(parseCtx context.Context, parseReq *proto.ListTodosRequest) (*proto.ListTodosResponse, error) {
	return &proto.ListTodosResponse{
		Todos: []*proto.Todo{
			{Id: "1", Text: "Test Todo", Done: false},
		},
	}, nil
}

func (parseS *mockTodoService) StreamTodos(parseReq *proto.StreamTodosRequest, parseStream proto.TodoService_StreamTodosServer) error {
	parseTodos := []*proto.Todo{
		{Id: "1", Text: "First Todo", Done: false},
		{Id: "2", Text: "Second Todo", Done: true},
		{Id: "3", Text: "Third Todo", Done: false},
	}

	for _, parseTodo := range parseTodos {
		if parseErr := parseStream.Send(&proto.StreamTodosResponse{Todo: parseTodo}); parseErr != nil {
			return parseErr
		}
		time.Sleep(10 * time.Millisecond) // Simulate streaming delay
	}
	return nil
}

func (parseS *mockTodoService) BulkCreateTodos(parseStream proto.TodoService_BulkCreateTodosServer) error {
	parseCount := int32(0)
	for {
		_, parseErr := parseStream.Recv()
		if parseErr != nil {
			if parseErr.Error() == "EOF" {
				return parseStream.SendAndClose(&proto.BulkCreateResponse{CreatedCount: parseCount})
			}
			return parseErr
		}
		parseCount++
		// Simulate processing
		time.Sleep(5 * time.Millisecond)
	}
}

func (parseS *mockTodoService) SyncTodos(parseStream proto.TodoService_SyncTodosServer) error {
	for {
		parseReq, parseErr := parseStream.Recv()
		if parseErr != nil {
			if parseErr.Error() == "EOF" {
				return nil
			}
			return parseErr
		}

		// Echo back results
		switch parseAction := parseReq.Action.(type) {
		case *proto.SyncRequest_Create:
			parseStream.Send(&proto.SyncResponse{
				Result: &proto.SyncResponse_Todo{
					Todo: &proto.Todo{Id: "sync-1", Text: parseAction.Create.Text, Done: false},
				},
			})
		case *proto.SyncRequest_Update:
			parseStream.Send(&proto.SyncResponse{
				Result: &proto.SyncResponse_Todo{
					Todo: &proto.Todo{Id: parseAction.Update.Id, Text: parseAction.Update.Text, Done: parseAction.Update.Done},
				},
			})
		case *proto.SyncRequest_Delete:
			parseStream.Send(&proto.SyncResponse{
				Result: &proto.SyncResponse_Error{Error: "deleted"},
			})
		}
	}
}

// TestIntegration_FullRoundtrip tests a complete client-server roundtrip
func TestIntegration_FullRoundtrip(parseT *testing.T) {
	// Create gRPC server with mock service
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	// Create bridge handler
	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer: parseGrpcServer,
	})

	// Start HTTP test server
	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	// Convert http:// to ws://
	parseWsURL := "ws" + parseServer.URL[4:]

	// Create gRPC client using bridge bridge.DialOption
	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}
	defer parseConn.Close()

	// Create client
	parseClient := proto.NewTodoServiceClient(parseConn)

	// Test CreateTodo
	parseCreateResp, parseErr := parseClient.CreateTodo(parseCtx, &proto.CreateTodoRequest{
		Text: "Integration test todo",
	})
	if parseErr != nil {
		parseT.Fatalf("CreateTodo failed: %v", parseErr)
	}

	if parseCreateResp.GetTodo().GetId() != "test-123" {
		parseT.Errorf("Expected ID 'test-123', got '%s'", parseCreateResp.GetTodo().GetId())
	}

	if parseCreateResp.GetTodo().GetText() != "Integration test todo" {
		parseT.Errorf("Expected text 'Integration test todo', got '%s'", parseCreateResp.GetTodo().GetText())
	}

	// Test ListTodos
	parseListResp, parseErr := parseClient.ListTodos(parseCtx, &proto.ListTodosRequest{})
	if parseErr != nil {
		parseT.Fatalf("ListTodos failed: %v", parseErr)
	}

	if len(parseListResp.GetTodos()) != 1 {
		parseT.Errorf("Expected 1 todo, got %d", len(parseListResp.GetTodos()))
	}
}

// TestIntegration_LifecycleHooks tests that hooks are called
func TestIntegration_LifecycleHooks(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	var isConnectCalled, isDisconnectCalled bool

	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer: parseGrpcServer,
		OnConnect: func(parseR *http.Request) {
			isConnectCalled = true
		},
		OnDisconnect: func(parseR2 *http.Request) {
			isDisconnectCalled = true
		},
	})

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}

	parseClient := proto.NewTodoServiceClient(parseConn)
	_, _ = parseClient.ListTodos(parseCtx, &proto.ListTodosRequest{})

	parseConn.Close()

	// Give time for disconnect hook
	time.Sleep(100 * time.Millisecond)

	if !isConnectCalled {
		parseT.Error("OnConnect hook was not called")
	}

	if !isDisconnectCalled {
		parseT.Error("OnDisconnect hook was not called")
	}
}

// TestIntegration_CustomBufferSizes tests with custom buffer sizes
func TestIntegration_CustomBufferSizes(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer:      parseGrpcServer,
		ReadBufferSize:  8192,
		WriteBufferSize: 8192,
	})

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)
	_, parseErr = parseClient.CreateTodo(parseCtx, &proto.CreateTodoRequest{Text: "Test"})
	if parseErr != nil {
		parseT.Fatalf("CreateTodo failed: %v", parseErr)
	}
}

// TestServe_WithListener tests the Serve convenience function
func TestServe_WithListener(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	// Create listener
	parseListener, parseErr := net.Listen("tcp", "127.0.0.1:0")
	if parseErr != nil {
		parseT.Fatalf("Failed to create listener: %v", parseErr)
	}
	defer parseListener.Close()

	// Start server in background
	go func() {
		_ = helpers.Serve(parseListener, parseGrpcServer)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Connect to it
	parseAddr := parseListener.Addr().String()
	parseWsURL := "ws://" + parseAddr

	parseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseAddr,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)
	parseResp, parseErr := parseClient.ListTodos(parseCtx, &proto.ListTodosRequest{})
	if parseErr != nil {
		parseT.Fatalf("ListTodos failed: %v", parseErr)
	}

	if len(parseResp.GetTodos()) == 0 {
		parseT.Error("Expected at least one todo")
	}
}

// TestIntegration_MultipleRequests tests multiple sequential requests
func TestIntegration_MultipleRequests(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer: parseGrpcServer,
	})

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)

	// Make multiple requests over the same connection
	for parseI := 0; parseI < 5; parseI++ {
		_, parseErr2 := parseClient.ListTodos(parseCtx, &proto.ListTodosRequest{})
		if parseErr2 != nil {
			parseT.Fatalf("Request %d failed: %v", parseI, parseErr2)
		}
	}
}

// TestIntegration_OriginCheck tests origin validation
func TestIntegration_OriginCheck(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer: parseGrpcServer,
		CheckOrigin: func(parseR *http.Request) bool {
			// Reject all origins
			return false
		},
	})

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Should fail due to origin check
	_, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)

	// We expect a connection error due to rejected origin
	if parseErr == nil {
		parseT.Error("Expected error due to origin rejection, got nil")
	}
}

// TestIntegration_ServerStreaming tests server-to-client streaming over WebSocket
func TestIntegration_ServerStreaming(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer: parseGrpcServer,
	})

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)

	parseStream, parseErr := parseClient.StreamTodos(parseCtx, &proto.StreamTodosRequest{})
	if parseErr != nil {
		parseT.Fatalf("StreamTodos failed: %v", parseErr)
	}

	parseReceivedTodos := []*proto.Todo{}
	for {
		parseResp, parseErr2 := parseStream.Recv()
		if parseErr2 != nil {
			if parseErr2.Error() == "EOF" {
				break
			}
			parseT.Fatalf("Failed to receive: %v", parseErr2)
		}
		parseReceivedTodos = append(parseReceivedTodos, parseResp.Todo)
	}

	if len(parseReceivedTodos) != 3 {
		parseT.Errorf("Expected 3 streamed todos, got %d", len(parseReceivedTodos))
	}

	if parseReceivedTodos[0].Text != "First Todo" {
		parseT.Errorf("Expected 'First Todo', got '%s'", parseReceivedTodos[0].Text)
	}
}

// TestIntegration_ClientStreaming tests client-to-server streaming over WebSocket
func TestIntegration_ClientStreaming(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer: parseGrpcServer,
	})

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)

	parseStream, parseErr := parseClient.BulkCreateTodos(parseCtx)
	if parseErr != nil {
		parseT.Fatalf("BulkCreateTodos failed: %v", parseErr)
	}

	// Send multiple todos
	parseTodos := []string{"Todo 1", "Todo 2", "Todo 3", "Todo 4", "Todo 5"}
	for _, parseText := range parseTodos {
		if parseErr2 := parseStream.Send(&proto.BulkCreateRequest{Text: parseText}); parseErr2 != nil {
			parseT.Fatalf("Failed to send: %v", parseErr2)
		}
	}

	parseResp, parseErr := parseStream.CloseAndRecv()
	if parseErr != nil {
		parseT.Fatalf("Failed to close and receive: %v", parseErr)
	}

	if parseResp.CreatedCount != int32(len(parseTodos)) {
		parseT.Errorf("Expected %d created todos, got %d", len(parseTodos), parseResp.CreatedCount)
	}
}

// TestIntegration_BidirectionalStreaming tests full-duplex streaming over WebSocket
func TestIntegration_BidirectionalStreaming(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer: parseGrpcServer,
	})

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)

	parseStream, parseErr := parseClient.SyncTodos(parseCtx)
	if parseErr != nil {
		parseT.Fatalf("SyncTodos failed: %v", parseErr)
	}

	// Test bidirectional streaming by sending and receiving concurrently
	parseDone := make(chan bool)
	parseResponses := []*proto.SyncResponse{}

	// Receiver goroutine
	go func() {
		for {
			parseResp, parseErr2 := parseStream.Recv()
			if parseErr2 != nil {
				parseDone <- true
				return
			}
			parseResponses = append(parseResponses, parseResp)
		}
	}()

	// Send multiple operations
	parseStream.Send(&proto.SyncRequest{
		Action: &proto.SyncRequest_Create{
			Create: &proto.CreateTodoRequest{Text: "Bidirectional test"},
		},
	})

	parseStream.Send(&proto.SyncRequest{
		Action: &proto.SyncRequest_Update{
			Update: &proto.UpdateTodoRequest{Id: "123", Text: "Updated", Done: true},
		},
	})

	parseStream.Send(&proto.SyncRequest{
		Action: &proto.SyncRequest_Delete{
			Delete: &proto.DeleteTodoRequest{Id: "456"},
		},
	})

	parseStream.CloseSend()
	<-parseDone

	if len(parseResponses) != 3 {
		parseT.Errorf("Expected 3 responses, got %d", len(parseResponses))
	}

	// Verify create response
	if parseResponses[0].GetTodo() == nil {
		parseT.Error("Expected todo in first response")
	}
}

// TestIntegration_Metadata tests that gRPC metadata (headers) are preserved
func TestIntegration_Metadata(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer: parseGrpcServer,
	})

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)

	// Send metadata with request
	parseMd := metadata.Pairs(
		"authorization", "Bearer test-token",
		"custom-header", "custom-value",
	)
	parseCtx = metadata.NewOutgoingContext(parseCtx, parseMd)

	var parseHeader metadata.MD
	_, parseErr = parseClient.CreateTodo(parseCtx, &proto.CreateTodoRequest{Text: "Test"}, grpc.Header(&parseHeader))
	if parseErr != nil {
		parseT.Fatalf("CreateTodo with metadata failed: %v", parseErr)
	}

	// Note: This tests that metadata doesn't break the connection
	// Full metadata round-trip verification would require server-side inspection
	parseT.Log("Metadata test passed - request succeeded with headers")
}

// TestIntegration_Trailers tests that gRPC trailers are preserved
func TestIntegration_Trailers(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer: parseGrpcServer,
	})

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)

	var parseTrailer metadata.MD
	_, parseErr = parseClient.ListTodos(parseCtx, &proto.ListTodosRequest{}, grpc.Trailer(&parseTrailer))
	if parseErr != nil {
		parseT.Fatalf("ListTodos with trailer failed: %v", parseErr)
	}

	// Note: This tests that trailer handling doesn't break the connection
	parseT.Log("Trailer test passed - request succeeded with trailer capture")
}

// TestIntegration_Cancellation tests context cancellation propagation
func TestIntegration_Cancellation(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer: parseGrpcServer,
	})

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)

	// Start a server streaming call
	parseStreamCtx, parseStreamCancel := context.WithCancel(parseCtx)
	parseStream, parseErr := parseClient.StreamTodos(parseStreamCtx, &proto.StreamTodosRequest{})
	if parseErr != nil {
		parseT.Fatalf("StreamTodos failed: %v", parseErr)
	}

	// Receive one message
	_, parseErr = parseStream.Recv()
	if parseErr != nil {
		parseT.Fatalf("First recv failed: %v", parseErr)
	}

	// Cancel the context
	parseStreamCancel()

	// Next recv should fail with context canceled
	_, parseErr = parseStream.Recv()
	if parseErr == nil {
		parseT.Error("Expected error after context cancellation, got nil")
	}

	if parseErr != nil && parseErr != context.Canceled && !isContextCanceledError(parseErr) {
		parseT.Logf("Got error after cancellation: %v (type: %T)", parseErr, parseErr)
	}
}

// Helper to check for context cancellation errors
func isContextCanceledError(parseErr error) bool {
	if parseErr == context.Canceled {
		return true
	}
	return parseErr.Error() == "context canceled" || parseErr.Error() == "rpc error: code = Canceled desc = context canceled"
}

// TestIntegration_Backpressure tests flow control in streaming
func TestIntegration_Backpressure(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	handler := helpers.ServeHandler(helpers.ServerConfig{
		GRPCServer: parseGrpcServer,
	})

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	parseConn, parseErr := grpc.DialContext(
		parseCtx,
		parseServer.URL,
		bridge.DialOption(parseWsURL),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr != nil {
		parseT.Fatalf("Failed to dial: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)

	// Test backpressure with slow receiver
	parseStream, parseErr := parseClient.StreamTodos(parseCtx, &proto.StreamTodosRequest{})
	if parseErr != nil {
		parseT.Fatalf("StreamTodos failed: %v", parseErr)
	}

	parseReceivedCount := 0
	for {
		_, parseErr2 := parseStream.Recv()
		if parseErr2 == io.EOF {
			break
		}
		if parseErr2 != nil {
			parseT.Fatalf("Recv failed: %v", parseErr2)
		}
		parseReceivedCount++

		// Simulate slow consumer
		time.Sleep(50 * time.Millisecond)
	}

	if parseReceivedCount != 3 {
		parseT.Errorf("Expected 3 messages with backpressure, got %d", parseReceivedCount)
	}

	parseT.Log("Backpressure test passed - slow receiver handled correctly")
}

// TestIntegration_GrpcTunnel_ServerStreaming tests new grpctunnel API with server streaming
func TestIntegration_GrpcTunnel_ServerStreaming(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	// Use new grpctunnel.Wrap API
	handler := grpctunnel.Wrap(parseGrpcServer)
	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use new grpctunnel.Dial API
	parseConn, parseErr := grpctunnel.DialContext(parseCtx, parseWsURL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if parseErr != nil {
		parseT.Fatalf("grpctunnel.Dial failed: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)
	parseStream, parseErr := parseClient.StreamTodos(parseCtx, &proto.StreamTodosRequest{})
	if parseErr != nil {
		parseT.Fatalf("StreamTodos failed: %v", parseErr)
	}

	parseCount := 0
	for {
		_, parseErr2 := parseStream.Recv()
		if parseErr2 == io.EOF {
			break
		}
		if parseErr2 != nil {
			parseT.Fatalf("Recv failed: %v", parseErr2)
		}
		parseCount++
	}

	if parseCount != 3 {
		parseT.Errorf("Expected 3 todos, got %d", parseCount)
	}
}

// TestIntegration_GrpcTunnel_Unary tests new grpctunnel API with unary calls
func TestIntegration_GrpcTunnel_Unary(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockTodoService{})
	defer parseGrpcServer.Stop()

	handler := grpctunnel.Wrap(parseGrpcServer)
	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := grpctunnel.Dial(parseWsURL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if parseErr != nil {
		parseT.Fatalf("grpctunnel.Dial failed: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)

	parseResp, parseErr := parseClient.CreateTodo(parseCtx, &proto.CreateTodoRequest{Text: "Test with grpctunnel"})
	if parseErr != nil {
		parseT.Fatalf("CreateTodo failed: %v", parseErr)
	}

	if parseResp.GetTodo().GetText() != "Test with grpctunnel" {
		parseT.Errorf("Expected 'Test with grpctunnel', got '%s'", parseResp.GetTodo().GetText())
	}
}
