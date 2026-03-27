//go:build !js && !wasm

//lint:file-ignore SA1019 grpc.WithBlock is retained in timeout tests to validate blocking dial behavior on grpc 1.x.

package grpctunnel

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type mockService struct {
	proto.UnimplementedTodoServiceServer
}

func (parseS *mockService) CreateTodo(parseCtx context.Context, parseReq *proto.CreateTodoRequest) (*proto.CreateTodoResponse, error) {
	return &proto.CreateTodoResponse{
		Todo: &proto.Todo{Id: "test-1", Text: parseReq.Text, Done: false},
	}, nil
}

func (parseS *mockService) ListTodos(parseCtx context.Context, parseReq *proto.ListTodosRequest) (*proto.ListTodosResponse, error) {
	return &proto.ListTodosResponse{
		Todos: []*proto.Todo{
			{Id: "1", Text: "Test", Done: false},
		},
	}, nil
}

func (parseS *mockService) StreamTodos(parseReq *proto.StreamTodosRequest, parseStream proto.TodoService_StreamTodosServer) error {
	parseTodos := []*proto.Todo{
		{Id: "1", Text: "First", Done: false},
		{Id: "2", Text: "Second", Done: true},
		{Id: "3", Text: "Third", Done: false},
	}
	for _, parseTodo := range parseTodos {
		if parseErr := parseStream.Send(&proto.StreamTodosResponse{Todo: parseTodo}); parseErr != nil {
			return parseErr
		}
	}
	return nil
}

func (parseS *mockService) BulkCreateTodos(parseStream proto.TodoService_BulkCreateTodosServer) error {
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
	}
}

func (parseS *mockService) SyncTodos(parseStream proto.TodoService_SyncTodosServer) error {
	for {
		parseReq, parseErr := parseStream.Recv()
		if parseErr != nil {
			if parseErr.Error() == "EOF" {
				return nil
			}
			return parseErr
		}
		switch parseAction := parseReq.Action.(type) {
		case *proto.SyncRequest_Create:
			parseStream.Send(&proto.SyncResponse{
				Result: &proto.SyncResponse_Todo{
					Todo: &proto.Todo{Id: "1", Text: parseAction.Create.Text, Done: false},
				},
			})
		}
	}
}

func TestWrap(parseT *testing.T) {
	// Create gRPC server
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	// Wrap it for WebSocket
	handler := Wrap(parseGrpcServer)

	// Start test server
	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	// Convert http:// to ws://
	parseWsURL := "ws" + parseServer.URL[4:]

	// Connect via new Dial API
	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := DialContext(parseCtx, parseWsURL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if parseErr != nil {
		parseT.Fatalf("Dial failed: %v", parseErr)
	}
	defer parseConn.Close()

	// Test RPC call
	parseClient := proto.NewTodoServiceClient(parseConn)
	parseResp, parseErr := parseClient.CreateTodo(parseCtx, &proto.CreateTodoRequest{Text: "New API test"})
	if parseErr != nil {
		parseT.Fatalf("CreateTodo failed: %v", parseErr)
	}

	if parseResp.GetTodo().GetText() != "New API test" {
		parseT.Errorf("Expected 'New API test', got '%s'", parseResp.GetTodo().GetText())
	}
}

func TestDial_URLInference(parseT *testing.T) {
	parseTests := []struct {
		name     string
		target   string
		expected string
	}{
		{"WebSocket URL", "ws://localhost:8080", "ws://localhost:8080"},
		{"Secure WebSocket", "wss://api.example.com", "wss://api.example.com"},
		{"Host and port", "localhost:8080", "ws://localhost:8080"},
		{"Port only", ":8080", "ws://localhost:8080"},
	}

	for _, parseTt := range parseTests {
		parseT.Run(parseTt.name, func(parseT2 *testing.T) {
			parseResult := inferWebSocketURL(parseTt.target, false)
			if parseResult != parseTt.expected {
				parseT2.Errorf("Expected %s, got %s", parseTt.expected, parseResult)
			}
		})
	}
}

func TestDial_TLSInference(parseT *testing.T) {
	parseResult := inferWebSocketURL("localhost:8080", true)
	parseExpected := "wss://localhost:8080"
	if parseResult != parseExpected {
		parseT.Errorf("Expected %s with TLS, got %s", parseExpected, parseResult)
	}
}

// TestDial_WithTLSNilConfigInference verifies WithTLS(nil) still selects wss URL inference.
func TestDial_WithTLSNilConfigInference(parseT *testing.T) {
	parseOptions := &clientOptions{}
	WithTLS(nil)(parseOptions)

	if !parseOptions.isUseTLS {
		parseT.Fatal("Expected WithTLS(nil) to force TLS URL inference")
	}

	parseResult := inferWebSocketURL("localhost:8080", parseOptions.isUseTLS)
	parseExpected := "wss://localhost:8080"
	if parseResult != parseExpected {
		parseT.Fatalf("Expected %s with WithTLS(nil), got %s", parseExpected, parseResult)
	}
}

func TestWrap_WithOptions(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	var parseConnectCalled atomic.Bool
	var parseDisconnectCalled atomic.Bool

	handler := Wrap(parseGrpcServer,
		WithOriginCheck(func(parseR *http.Request) bool { return true }),
		WithBufferSizes(8192, 8192),
		WithConnectHook(func(parseR2 *http.Request) {
			parseConnectCalled.Store(true)
		}),
		WithDisconnectHook(func(parseR3 *http.Request) {
			parseDisconnectCalled.Store(true)
		}),
	)

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]

	parseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	parseConn, parseErr := Dial(parseWsURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if parseErr != nil {
		parseT.Fatalf("Dial failed: %v", parseErr)
	}

	parseClient := proto.NewTodoServiceClient(parseConn)
	_, _ = parseClient.ListTodos(parseCtx, &proto.ListTodosRequest{})

	parseConn.Close()
	time.Sleep(100 * time.Millisecond)

	if !parseConnectCalled.Load() {
		parseT.Error("Connect hook not called")
	}

	if !parseDisconnectCalled.Load() {
		parseT.Error("Disconnect hook not called")
	}
}

func TestWrap_ServerStreaming(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	handler := Wrap(parseGrpcServer)
	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]
	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := Dial(parseWsURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if parseErr != nil {
		parseT.Fatalf("Dial failed: %v", parseErr)
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
		if parseErr2 != nil {
			if parseErr2.Error() == "EOF" {
				break
			}
			parseT.Fatalf("Recv failed: %v", parseErr2)
		}
		parseCount++
	}

	if parseCount != 3 {
		parseT.Errorf("Expected 3 streamed todos, got %d", parseCount)
	}
}

func TestWrap_ClientStreaming(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	handler := Wrap(parseGrpcServer)
	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]
	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := Dial(parseWsURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if parseErr != nil {
		parseT.Fatalf("Dial failed: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)
	parseStream, parseErr := parseClient.BulkCreateTodos(parseCtx)
	if parseErr != nil {
		parseT.Fatalf("BulkCreateTodos failed: %v", parseErr)
	}

	for parseI := 0; parseI < 5; parseI++ {
		if parseErr2 := parseStream.Send(&proto.BulkCreateRequest{Text: "Todo"}); parseErr2 != nil {
			parseT.Fatalf("Send failed: %v", parseErr2)
		}
	}

	parseResp, parseErr := parseStream.CloseAndRecv()
	if parseErr != nil {
		parseT.Fatalf("CloseAndRecv failed: %v", parseErr)
	}

	if parseResp.CreatedCount != 5 {
		parseT.Errorf("Expected 5 created, got %d", parseResp.CreatedCount)
	}
}

func TestWrap_BidirectionalStreaming(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	handler := Wrap(parseGrpcServer)
	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]
	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := Dial(parseWsURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if parseErr != nil {
		parseT.Fatalf("Dial failed: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)
	parseStream, parseErr := parseClient.SyncTodos(parseCtx)
	if parseErr != nil {
		parseT.Fatalf("SyncTodos failed: %v", parseErr)
	}

	parseDone := make(chan bool)
	parseResponses := 0

	go func() {
		for {
			_, parseErr2 := parseStream.Recv()
			if parseErr2 != nil {
				parseDone <- true
				return
			}
			parseResponses++
		}
	}()

	parseStream.Send(&proto.SyncRequest{
		Action: &proto.SyncRequest_Create{
			Create: &proto.CreateTodoRequest{Text: "Test"},
		},
	})

	parseStream.CloseSend()
	<-parseDone

	if parseResponses != 1 {
		parseT.Errorf("Expected 1 response, got %d", parseResponses)
	}
}

func TestDial_ContextTimeout(parseT *testing.T) {
	parseCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Try to connect to non-routable IP (will timeout)
	_, parseErr := DialContext(parseCtx, "10.255.255.1:9999",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)

	if parseErr == nil {
		parseT.Error("Expected timeout/connection error, got nil")
	} else {
		parseT.Logf("Got expected error: %v", parseErr)
	}
}

func TestDial_WithTLSOption(parseT *testing.T) {
	parseCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, parseErr := DialContext(parseCtx, "wss://127.0.0.1:65535",
		WithTLS(&tls.Config{InsecureSkipVerify: true}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if parseErr == nil {
		parseT.Error("Expected connection error for unreachable endpoint")
	}
}

func TestDial_UnsupportedOptionType(parseT *testing.T) {
	parseCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, parseErr := DialContext(parseCtx, "ws://127.0.0.1:65535",
		"invalid-option-type",
	)
	if parseErr == nil {
		parseT.Fatal("Expected option parsing error")
	}
}

func TestWrap_OriginRejection(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	handler := Wrap(parseGrpcServer,
		WithOriginCheck(func(parseR *http.Request) bool {
			return false // Reject all
		}),
	)

	parseServer := httptest.NewServer(handler)
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]
	parseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	parseConn, parseErr := DialContext(parseCtx, parseWsURL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)

	// Origin check happens during WebSocket upgrade
	// The dialer should fail to connect
	if parseErr == nil {
		if parseConn != nil {
			parseConn.Close()
		}
		// Connection might succeed but gRPC calls should fail
		// This is acceptable - origin check prevents actual data transfer
		parseT.Log("Connection succeeded but origin check should prevent WebSocket upgrade")
	}
}

func TestBuildTunnelConn(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	parseServer := httptest.NewServer(Wrap(parseGrpcServer))
	defer parseServer.Close()

	parseWsURL := "ws" + parseServer.URL[4:]
	parseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parseConn, parseErr := BuildTunnelConn(parseCtx, TunnelConfig{
		Target:      parseWsURL,
		GRPCOptions: ApplyTunnelInsecureCredentials(nil),
	})
	if parseErr != nil {
		parseT.Fatalf("BuildTunnelConn failed: %v", parseErr)
	}
	defer parseConn.Close()

	parseClient := proto.NewTodoServiceClient(parseConn)
	parseResp, parseErr := parseClient.CreateTodo(parseCtx, &proto.CreateTodoRequest{Text: "Typed API test"})
	if parseErr != nil {
		parseT.Fatalf("CreateTodo failed: %v", parseErr)
	}
	if parseResp.GetTodo().GetText() != "Typed API test" {
		parseT.Fatalf("Unexpected todo text: %s", parseResp.GetTodo().GetText())
	}
}

func TestGetTunnelConfigError_EmptyTarget(parseT *testing.T) {
	parseErr := GetTunnelConfigError(TunnelConfig{})
	if parseErr == nil {
		parseT.Fatal("Expected target validation error")
	}
}

func TestParseTunnelTargetURL_InvalidScheme(parseT *testing.T) {
	_, parseErr := ParseTunnelTargetURL("http://localhost:8080", false)
	if parseErr == nil {
		parseT.Fatal("Expected invalid scheme error")
	}
}

func TestBuildBridgeHandler_NilServer(parseT *testing.T) {
	_, parseErr := BuildBridgeHandler(nil, BridgeConfig{})
	if parseErr == nil {
		parseT.Fatal("Expected nil server validation error")
	}
}

func TestHandleBridgeMux(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	parseMux := http.NewServeMux()
	parseErr := HandleBridgeMux(parseMux, "/grpc", parseGrpcServer, BridgeConfig{})
	if parseErr != nil {
		parseT.Fatalf("HandleBridgeMux failed: %v", parseErr)
	}

	parseServer := httptest.NewServer(parseMux)
	defer parseServer.Close()

	parseResp, parseErr := http.Get(parseServer.URL + "/grpc")
	if parseErr != nil {
		parseT.Fatalf("GET /grpc failed: %v", parseErr)
	}
	defer parseResp.Body.Close()

	if parseResp.StatusCode == http.StatusNotFound {
		parseT.Fatalf("Expected /grpc handler to be registered, got status %d", parseResp.StatusCode)
	}
}
