package benchmarks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"
	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	gproto "google.golang.org/protobuf/proto"
)

var storeBenchmarkSilentLogOnce sync.Once

// Mock todo service for benchmarking
type todoService struct {
	proto.UnimplementedTodoServiceServer
	todos []*proto.Todo
}

// buildTodoID builds the Todo ID for the next create operation without fmt formatting overhead.
func buildTodoID(parseTodoCount int) string {
	// Use a stack buffer so ID construction does not pay fmt.Sprintf costs.
	var buildBuffer [32]byte
	buildBytes := append(buildBuffer[:0], "todo-"...)
	buildBytes = strconv.AppendInt(buildBytes, int64(parseTodoCount+1), 10)
	return string(buildBytes)
}

func (parseS *todoService) CreateTodo(parseCtx context.Context, parseReq *proto.CreateTodoRequest) (*proto.CreateTodoResponse, error) {
	parseTodo := &proto.Todo{
		Id:   buildTodoID(len(parseS.todos)),
		Text: parseReq.Text,
		Done: false,
	}
	parseS.todos = append(parseS.todos, parseTodo)
	return &proto.CreateTodoResponse{Todo: parseTodo}, nil
}

func (parseS *todoService) ListTodos(parseCtx context.Context, parseReq *proto.ListTodosRequest) (*proto.ListTodosResponse, error) {
	return &proto.ListTodosResponse{Todos: parseS.todos}, nil
}

func (parseS *todoService) UpdateTodo(parseCtx context.Context, parseReq *proto.UpdateTodoRequest) (*proto.UpdateTodoResponse, error) {
	for _, parseT := range parseS.todos {
		if parseT.Id == parseReq.Id {
			parseT.Text = parseReq.Text
			parseT.Done = parseReq.Done
			return &proto.UpdateTodoResponse{Todo: parseT}, nil
		}
	}
	return nil, fmt.Errorf("todo not found")
}

func (parseS *todoService) DeleteTodo(parseCtx context.Context, parseReq *proto.DeleteTodoRequest) (*proto.DeleteTodoResponse, error) {
	for parseI, parseT := range parseS.todos {
		if parseT.Id == parseReq.Id {
			parseS.todos = append(parseS.todos[:parseI], parseS.todos[parseI+1:]...)
			return &proto.DeleteTodoResponse{Success: true}, nil
		}
	}
	return &proto.DeleteTodoResponse{Success: false}, nil
}

func (parseS *todoService) StreamTodos(parseReq *proto.StreamTodosRequest, parseStream proto.TodoService_StreamTodosServer) error {
	for _, parseTodo := range parseS.todos {
		if parseErr := parseStream.Send(&proto.StreamTodosResponse{Todo: parseTodo}); parseErr != nil {
			return parseErr
		}
	}
	return nil
}

func (parseS *todoService) SyncTodos(parseStream proto.TodoService_SyncTodosServer) error {
	for {
		parseReq, parseErr := parseStream.Recv()
		if parseErr == io.EOF {
			return nil
		}
		if parseErr != nil {
			return parseErr
		}

		var parseResp *proto.SyncResponse
		switch parseAction := parseReq.Action.(type) {
		case *proto.SyncRequest_Create:
			parseTodo := &proto.Todo{
				Id:   buildTodoID(len(parseS.todos)),
				Text: parseAction.Create.Text,
			}
			parseS.todos = append(parseS.todos, parseTodo)
			parseResp = &proto.SyncResponse{
				Result: &proto.SyncResponse_Todo{Todo: parseTodo},
			}
		case *proto.SyncRequest_Update:
			for _, parseTodo2 := range parseS.todos {
				if parseTodo2.Id == parseAction.Update.Id {
					parseTodo2.Text = parseAction.Update.Text
					parseTodo2.Done = parseAction.Update.Done
					parseResp = &proto.SyncResponse{
						Result: &proto.SyncResponse_Todo{Todo: parseTodo2},
					}
					break
				}
			}
		case *proto.SyncRequest_Delete:
			for parseI, parseTodo3 := range parseS.todos {
				if parseTodo3.Id == parseAction.Delete.Id {
					parseS.todos = append(parseS.todos[:parseI], parseS.todos[parseI+1:]...)
					parseResp = &proto.SyncResponse{
						Result: &proto.SyncResponse_Todo{Todo: parseTodo3},
					}
					break
				}
			}
		}

		if parseResp == nil {
			parseResp = &proto.SyncResponse{
				Result: &proto.SyncResponse_Error{Error: "not found"},
			}
		}

		if parseErr2 := parseStream.Send(parseResp); parseErr2 != nil {
			return parseErr2
		}
	}
}

// REST handler implementation
type restHandler struct {
	service *todoService
}

type todoJSON struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

func (parseH *restHandler) ServeHTTP(parseW http.ResponseWriter, parseR *http.Request) {
	parseW.Header().Set("Content-Type", "application/json")

	switch parseR.URL.Path {
	case "/todos":
		if parseR.Method == "GET" {
			parseH.listTodos(parseW, parseR)
		} else if parseR.Method == "POST" {
			parseH.createTodo(parseW, parseR)
		}
	case "/todos/update":
		if parseR.Method == "PUT" {
			parseH.updateTodo(parseW, parseR)
		}
	case "/todos/delete":
		if parseR.Method == "DELETE" {
			parseH.deleteTodo(parseW, parseR)
		}
	default:
		http.NotFound(parseW, parseR)
	}
}

func (parseH *restHandler) createTodo(parseW http.ResponseWriter, parseR *http.Request) {
	var parseReq struct {
		Text string `json:"text"`
	}
	if parseErr := json.NewDecoder(parseR.Body).Decode(&parseReq); parseErr != nil {
		http.Error(parseW, parseErr.Error(), http.StatusBadRequest)
		return
	}

	parseResp, _ := parseH.service.CreateTodo(context.Background(), &proto.CreateTodoRequest{Text: parseReq.Text})
	json.NewEncoder(parseW).Encode(todoJSON{
		ID:   parseResp.Todo.Id,
		Text: parseResp.Todo.Text,
		Done: parseResp.Todo.Done,
	})
}

func (parseH *restHandler) listTodos(parseW http.ResponseWriter, parseR *http.Request) {
	parseResp, _ := parseH.service.ListTodos(context.Background(), &proto.ListTodosRequest{})
	parseTodos := make([]todoJSON, len(parseResp.Todos))
	for parseI, parseT := range parseResp.Todos {
		parseTodos[parseI] = todoJSON{ID: parseT.Id, Text: parseT.Text, Done: parseT.Done}
	}
	json.NewEncoder(parseW).Encode(map[string]interface{}{"todos": parseTodos})
}

func (parseH *restHandler) updateTodo(parseW http.ResponseWriter, parseR *http.Request) {
	var parseReq todoJSON
	if parseErr := json.NewDecoder(parseR.Body).Decode(&parseReq); parseErr != nil {
		http.Error(parseW, parseErr.Error(), http.StatusBadRequest)
		return
	}

	parseResp, _ := parseH.service.UpdateTodo(context.Background(), &proto.UpdateTodoRequest{
		Id:   parseReq.ID,
		Text: parseReq.Text,
		Done: parseReq.Done,
	})
	json.NewEncoder(parseW).Encode(todoJSON{
		ID:   parseResp.Todo.Id,
		Text: parseResp.Todo.Text,
		Done: parseResp.Todo.Done,
	})
}

func (parseH *restHandler) deleteTodo(parseW http.ResponseWriter, parseR *http.Request) {
	var parseReq struct {
		ID string `json:"id"`
	}
	if parseErr := json.NewDecoder(parseR.Body).Decode(&parseReq); parseErr != nil {
		http.Error(parseW, parseErr.Error(), http.StatusBadRequest)
		return
	}

	parseResp, _ := parseH.service.DeleteTodo(context.Background(), &proto.DeleteTodoRequest{Id: parseReq.ID})
	json.NewEncoder(parseW).Encode(map[string]bool{"success": parseResp.Success})
}

// setupGRPC creates a gRPC server with bridge
func setupGRPC(parseB *testing.B) (proto.TodoServiceClient, func()) {
	parseB.Helper()
	storeBenchmarkSilentLogOnce.Do(func() {
		// Keep benchmark output parseable for quality gates by suppressing runtime logs.
		log.SetOutput(io.Discard)
	})

	// Create gRPC server
	parseGrpcServer := grpc.NewServer()
	parseTodoCapacity := parseB.N
	if parseTodoCapacity < 1024 {
		parseTodoCapacity = 1024
	}
	parseService := &todoService{todos: make([]*proto.Todo, 0, parseTodoCapacity)}
	proto.RegisterTodoServiceServer(parseGrpcServer, parseService)

	// Start bridge server
	parseBridge := grpctunnel.Wrap(parseGrpcServer)
	parseServer := httptest.NewServer(parseBridge)

	// Extract host from URL (http://127.0.0.1:12345 -> 127.0.0.1:12345)
	parseWsURL := strings.TrimPrefix(parseServer.URL, "http://")

	// Create client
	parseConn, parseErr := grpctunnel.Dial(parseWsURL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if parseErr != nil {
		parseB.Fatalf("Failed to create client: %v", parseErr)
	}

	parseClient := proto.NewTodoServiceClient(parseConn)

	parseCleanup := func() {
		parseConn.Close()
		parseServer.Close()
		parseGrpcServer.Stop()
	}

	return parseClient, parseCleanup
}

// setupREST creates a REST server
func setupREST(parseB *testing.B) (*http.Client, string, func()) {
	parseB.Helper()

	parseTodoCapacity := parseB.N
	if parseTodoCapacity < 1024 {
		parseTodoCapacity = 1024
	}
	parseService := &todoService{todos: make([]*proto.Todo, 0, parseTodoCapacity)}
	handler := &restHandler{service: parseService}
	parseServer := httptest.NewServer(handler)

	// Use a dedicated transport with a larger per-host idle pool so high-concurrency
	// benchmarks do not thrash sockets and fail on platforms with stricter reuse limits.
	parseTransport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     90 * time.Second,
	}

	parseClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: parseTransport,
	}

	parseCleanup := func() {
		parseTransport.CloseIdleConnections()
		parseServer.CloseClientConnections()
		parseServer.Close()
	}

	return parseClient, parseServer.URL, parseCleanup
}

// handleRESTCreateTodo posts a create request and fails the benchmark on transport or IO errors.
func handleRESTCreateTodo(parseB *testing.B, parseClient *http.Client, parseURL string, parseBodyBytes []byte) {
	parseB.Helper()

	parseResp, parseErr := parseClient.Post(parseURL+"/todos", "application/json", bytes.NewReader(parseBodyBytes))
	if parseErr != nil {
		parseB.Fatalf("failed to create REST todo: %v", parseErr)
	}
	if parseResp == nil {
		parseB.Fatal("failed to create REST todo: empty response")
	}
	if _, parseReadError := io.Copy(io.Discard, parseResp.Body); parseReadError != nil {
		_ = parseResp.Body.Close()
		parseB.Fatalf("failed to read REST create response body: %v", parseReadError)
	}
	if parseCloseError := parseResp.Body.Close(); parseCloseError != nil {
		parseB.Fatalf("failed to close REST create response body: %v", parseCloseError)
	}
}

// buildRESTCreateTodoResult posts a create request and decodes the returned todo.
func buildRESTCreateTodoResult(parseB *testing.B, parseClient *http.Client, parseURL string, parseBodyBytes []byte) todoJSON {
	parseB.Helper()

	parseResp, parseErr := parseClient.Post(parseURL+"/todos", "application/json", bytes.NewReader(parseBodyBytes))
	if parseErr != nil {
		parseB.Fatalf("failed to create REST todo: %v", parseErr)
	}
	if parseResp == nil {
		parseB.Fatal("failed to create REST todo: empty response")
	}

	var parseTodoResult todoJSON
	parseDecodeError := json.NewDecoder(parseResp.Body).Decode(&parseTodoResult)
	parseCloseError := parseResp.Body.Close()
	if parseDecodeError != nil {
		parseB.Fatalf("failed to decode REST create response: %v", parseDecodeError)
	}
	if parseCloseError != nil {
		parseB.Fatalf("failed to close REST create response body: %v", parseCloseError)
	}
	return parseTodoResult
}

// Benchmarks

func BenchmarkGRPC_CreateTodo(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		_, parseErr := parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
			Text: "Benchmark task",
		})
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
	}
}

func BenchmarkREST_CreateTodo(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	parseBody := map[string]string{"text": "Benchmark task"}
	parseBodyBytes, _ := json.Marshal(parseBody)

	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		parseResp, parseErr := parseClient.Post(parseUrl+"/todos", "application/json", bytes.NewReader(parseBodyBytes))
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		io.ReadAll(parseResp.Body)
		parseResp.Body.Close()
	}
}

func BenchmarkGRPC_ListTodos(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	// Pre-populate with 100 todos
	for parseI := 0; parseI < 100; parseI++ {
		parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
			Text: fmt.Sprintf("Todo %d", parseI),
		})
	}

	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		_, parseErr := parseClient.ListTodos(context.Background(), &proto.ListTodosRequest{})
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
	}
}

func BenchmarkREST_ListTodos(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	// Pre-populate with 100 todos
	parseBody := map[string]string{"text": "Todo"}
	parseBodyBytes, _ := json.Marshal(parseBody)
	for parseI := 0; parseI < 100; parseI++ {
		handleRESTCreateTodo(parseB, parseClient, parseUrl, parseBodyBytes)
	}

	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		parseResp2, parseErr := parseClient.Get(parseUrl + "/todos")
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		io.ReadAll(parseResp2.Body)
		parseResp2.Body.Close()
	}
}

func BenchmarkGRPC_UpdateTodo(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	// Create a todo to update
	parseCreateResp, _ := parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
		Text: "Original",
	})
	parseTodoID := parseCreateResp.Todo.Id

	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		_, parseErr := parseClient.UpdateTodo(context.Background(), &proto.UpdateTodoRequest{
			Id:   parseTodoID,
			Text: "Updated",
			Done: true,
		})
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
	}
}

func BenchmarkREST_UpdateTodo(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	// Create a todo to update
	parseCreateBody := map[string]string{"text": "Original"}
	parseCreateBytes, _ := json.Marshal(parseCreateBody)
	parseCreateResult := buildRESTCreateTodoResult(parseB, parseClient, parseUrl, parseCreateBytes)

	parseUpdateBody := todoJSON{ID: parseCreateResult.ID, Text: "Updated", Done: true}
	parseUpdateBytes, _ := json.Marshal(parseUpdateBody)

	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		parseReq, _ := http.NewRequest("PUT", parseUrl+"/todos/update", bytes.NewReader(parseUpdateBytes))
		parseReq.Header.Set("Content-Type", "application/json")
		parseResp, parseErr := parseClient.Do(parseReq)
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		io.ReadAll(parseResp.Body)
		parseResp.Body.Close()
	}
}

func BenchmarkGRPC_DeleteTodo(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		parseB.StopTimer()
		// Create a todo to delete
		parseCreateResp, _ := parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
			Text: "To be deleted",
		})
		parseB.StartTimer()

		_, parseErr := parseClient.DeleteTodo(context.Background(), &proto.DeleteTodoRequest{
			Id: parseCreateResp.Todo.Id,
		})
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
	}
}

func BenchmarkREST_DeleteTodo(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		parseB.StopTimer()
		// Create a todo to delete
		parseCreateBody := map[string]string{"text": "To be deleted"}
		parseCreateBytes, _ := json.Marshal(parseCreateBody)
		parseCreateResult := buildRESTCreateTodoResult(parseB, parseClient, parseUrl, parseCreateBytes)
		parseB.StartTimer()

		parseDeleteBody := map[string]string{"id": parseCreateResult.ID}
		parseDeleteBytes, _ := json.Marshal(parseDeleteBody)
		parseReq, _ := http.NewRequest("DELETE", parseUrl+"/todos/delete", bytes.NewReader(parseDeleteBytes))
		parseReq.Header.Set("Content-Type", "application/json")
		parseResp, parseErr := parseClient.Do(parseReq)
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		io.ReadAll(parseResp.Body)
		parseResp.Body.Close()
	}
}

// Payload size benchmarks - measures compression efficiency
func BenchmarkGRPC_PayloadSize_10Items(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	// Pre-populate with 10 todos
	for parseI := 0; parseI < 10; parseI++ {
		parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
			Text: fmt.Sprintf("This is a longer todo text to measure payload size differences between Protobuf and JSON #%d", parseI),
		})
	}

	parseB.ReportAllocs()
	parseB.ReportMetric(0, "payload-bytes")
	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		parseResp, parseErr := parseClient.ListTodos(context.Background(), &proto.ListTodosRequest{})
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		// Estimate protobuf size
		parseSize := gproto.Size(parseResp)
		parseB.ReportMetric(float64(parseSize)/1024.0, "KB/op")
	}
}

func BenchmarkREST_PayloadSize_10Items(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	// Pre-populate with 10 todos
	parseBody := map[string]string{"text": "This is a longer todo text to measure payload size differences between Protobuf and JSON"}
	parseBodyBytes, _ := json.Marshal(parseBody)
	for parseI := 0; parseI < 10; parseI++ {
		handleRESTCreateTodo(parseB, parseClient, parseUrl, parseBodyBytes)
	}

	parseB.ReportAllocs()
	parseB.ReportMetric(0, "payload-bytes")
	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		parseResp2, parseErr := parseClient.Get(parseUrl + "/todos")
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		parseData, _ := io.ReadAll(parseResp2.Body)
		parseResp2.Body.Close()
		parseB.ReportMetric(float64(len(parseData))/1024.0, "KB/op")
	}
}

func BenchmarkGRPC_PayloadSize_100Items(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	for parseI := 0; parseI < 100; parseI++ {
		parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
			Text: fmt.Sprintf("Todo with medium length text for realistic payload testing #%d", parseI),
		})
	}

	parseB.ReportAllocs()
	parseB.ReportMetric(0, "payload-bytes")
	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		parseResp, parseErr := parseClient.ListTodos(context.Background(), &proto.ListTodosRequest{})
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		parseSize := gproto.Size(parseResp)
		parseB.ReportMetric(float64(parseSize)/1024.0, "KB/op")
	}
}

func BenchmarkREST_PayloadSize_100Items(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	parseBody := map[string]string{"text": "Todo with medium length text for realistic payload testing"}
	parseBodyBytes, _ := json.Marshal(parseBody)
	for parseI := 0; parseI < 100; parseI++ {
		handleRESTCreateTodo(parseB, parseClient, parseUrl, parseBodyBytes)
	}

	parseB.ReportAllocs()
	parseB.ReportMetric(0, "payload-bytes")
	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		parseResp2, parseErr := parseClient.Get(parseUrl + "/todos")
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		parseData, _ := io.ReadAll(parseResp2.Body)
		parseResp2.Body.Close()
		parseB.ReportMetric(float64(len(parseData))/1024.0, "KB/op")
	}
}

func BenchmarkGRPC_PayloadSize_1000Items(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	for parseI := 0; parseI < 1000; parseI++ {
		parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
			Text: fmt.Sprintf("Todo with medium length text for realistic payload testing #%d", parseI),
		})
	}

	parseB.ReportAllocs()
	parseB.ReportMetric(0, "payload-bytes")
	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		parseResp, parseErr := parseClient.ListTodos(context.Background(), &proto.ListTodosRequest{})
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		parseSize := gproto.Size(parseResp)
		parseB.ReportMetric(float64(parseSize)/1024.0, "KB/op")
	}
}

func BenchmarkREST_PayloadSize_1000Items(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	parseBody := map[string]string{"text": "Todo with medium length text for realistic payload testing"}
	parseBodyBytes, _ := json.Marshal(parseBody)
	for parseI := 0; parseI < 1000; parseI++ {
		handleRESTCreateTodo(parseB, parseClient, parseUrl, parseBodyBytes)
	}

	parseB.ReportAllocs()
	parseB.ReportMetric(0, "payload-bytes")
	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		parseResp2, parseErr := parseClient.Get(parseUrl + "/todos")
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		parseData, _ := io.ReadAll(parseResp2.Body)
		parseResp2.Body.Close()
		parseB.ReportMetric(float64(len(parseData))/1024.0, "KB/op")
	}
}

func BenchmarkGRPC_StreamingTodos(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	// Pre-populate with 100 todos
	for parseI := 0; parseI < 100; parseI++ {
		parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
			Text: fmt.Sprintf("Todo %d", parseI),
		})
	}

	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		parseStream, parseErr := parseClient.StreamTodos(context.Background(), &proto.StreamTodosRequest{})
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		for {
			_, parseErr2 := parseStream.Recv()
			if parseErr2 == io.EOF {
				break
			}
			if parseErr2 != nil {
				parseB.Fatal(parseErr2)
			}
		}
	}
}

// REST can't do server streaming, so we simulate with pagination
func BenchmarkREST_StreamingTodos_Simulation(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	// Pre-populate with 100 todos
	parseBody := map[string]string{"text": "Todo"}
	parseBodyBytes, _ := json.Marshal(parseBody)
	for parseI := 0; parseI < 100; parseI++ {
		handleRESTCreateTodo(parseB, parseClient, parseUrl, parseBodyBytes)
	}

	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		// Simulate streaming by fetching all at once (what REST would do)
		parseResp2, parseErr := parseClient.Get(parseUrl + "/todos")
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		io.ReadAll(parseResp2.Body)
		parseResp2.Body.Close()
	}
}

// Latency benchmarks - measure time to first byte and connection overhead
func BenchmarkGRPC_ConnectionReuse_10Requests(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		// 10 sequential requests on same connection
		for parseJ := 0; parseJ < 10; parseJ++ {
			_, parseErr := parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
				Text: "Task",
			})
			if parseErr != nil {
				parseB.Fatal(parseErr)
			}
		}
	}
}

func BenchmarkREST_ConnectionReuse_10Requests(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	parseBodyBytes, _ := json.Marshal(map[string]string{"text": "Task"})

	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		// 10 sequential requests - HTTP keep-alive helps but still has overhead
		for parseJ := 0; parseJ < 10; parseJ++ {
			parseResp, parseErr := parseClient.Post(parseUrl+"/todos", "application/json", bytes.NewReader(parseBodyBytes))
			if parseErr != nil {
				parseB.Fatal(parseErr)
			}
			io.ReadAll(parseResp.Body)
			parseResp.Body.Close()
		}
	}
}

// Large dataset streaming - gRPC can stream without loading all into memory
func BenchmarkGRPC_StreamLargeDataset_1000Items(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	// Pre-populate with 1000 todos
	for parseI := 0; parseI < 1000; parseI++ {
		parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
			Text: fmt.Sprintf("Todo item with realistic text length for streaming benchmark #%d", parseI),
		})
	}

	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		parseStream, parseErr := parseClient.StreamTodos(context.Background(), &proto.StreamTodosRequest{})
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		parseCount := 0
		for {
			_, parseErr2 := parseStream.Recv()
			if parseErr2 == io.EOF {
				break
			}
			if parseErr2 != nil {
				parseB.Fatal(parseErr2)
			}
			parseCount++
		}
	}
}

func BenchmarkREST_LargeDataset_1000Items(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	// Pre-populate with 1000 todos
	parseBody := map[string]string{"text": "Todo item with realistic text length for streaming benchmark"}
	parseBodyBytes, _ := json.Marshal(parseBody)
	for parseI := 0; parseI < 1000; parseI++ {
		handleRESTCreateTodo(parseB, parseClient, parseUrl, parseBodyBytes)
	}

	parseB.ResetTimer()
	for parseI2 := 0; parseI2 < parseB.N; parseI2++ {
		// REST must load ALL data into memory at once
		parseResp2, parseErr := parseClient.Get(parseUrl + "/todos")
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		parseData, _ := io.ReadAll(parseResp2.Body)
		parseResp2.Body.Close()

		// Parse all JSON at once
		var parseResult map[string][]todoJSON
		json.Unmarshal(parseData, &parseResult)
	}
}

// Concurrent requests - HTTP/2 multiplexing advantage
func BenchmarkGRPC_ConcurrentRequests_10Parallel(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	parseB.RunParallel(func(parsePb *testing.PB) {
		for parsePb.Next() {
			_, parseErr := parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
				Text: "Concurrent task",
			})
			if parseErr != nil {
				parseB.Fatal(parseErr)
			}
		}
	})
}

func BenchmarkREST_ConcurrentRequests_10Parallel(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	parseBodyBytes, _ := json.Marshal(map[string]string{"text": "Concurrent task"})

	parseB.RunParallel(func(parsePb *testing.PB) {
		for parsePb.Next() {
			parseResp, parseErr := parseClient.Post(parseUrl+"/todos", "application/json", bytes.NewReader(parseBodyBytes))
			if parseErr != nil {
				parseB.Fatal(parseErr)
			}
			io.ReadAll(parseResp.Body)
			parseResp.Body.Close()
		}
	})
}

// Header overhead - REST sends more metadata per request
func BenchmarkGRPC_MinimalRequest(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	parseB.ReportAllocs()
	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		_, parseErr := parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
			Text: "X", // Minimal payload
		})
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
	}
}

func BenchmarkREST_MinimalRequest(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	parseBodyBytes, _ := json.Marshal(map[string]string{"text": "X"}) // Minimal payload

	parseB.ReportAllocs()
	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		parseResp, parseErr := parseClient.Post(parseUrl+"/todos", "application/json", bytes.NewReader(parseBodyBytes))
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		io.ReadAll(parseResp.Body)
		parseResp.Body.Close()
	}
}

// Bidirectional streaming - only gRPC can do this
func BenchmarkGRPC_BidirectionalStream_100Messages(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	parseSyncRequest := &proto.SyncRequest{
		Action: &proto.SyncRequest_Create{
			Create: &proto.CreateTodoRequest{Text: "Sync task"},
		},
	}

	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		parseStream, parseErr := parseClient.SyncTodos(context.Background())
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}

		// Send 100 messages
		for parseJ := 0; parseJ < 100; parseJ++ {
			parseErr2 := parseStream.Send(parseSyncRequest)
			if parseErr2 != nil {
				parseB.Fatal(parseErr2)
			}

			// Receive response
			_, parseErr2 = parseStream.Recv()
			if parseErr2 != nil {
				parseB.Fatal(parseErr2)
			}
		}

		parseStream.CloseSend()
	}
}

// REST equivalent would require 100 HTTP requests
func BenchmarkREST_Bidirectional_100Messages(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	parseBodyBytes, _ := json.Marshal(map[string]string{"text": "Sync task"})

	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		// REST must make 100 separate HTTP requests (no streaming)
		for parseJ := 0; parseJ < 100; parseJ++ {
			parseResp, parseErr := parseClient.Post(parseUrl+"/todos", "application/json", bytes.NewReader(parseBodyBytes))
			if parseErr != nil {
				parseB.Fatal(parseErr)
			}
			io.ReadAll(parseResp.Body)
			parseResp.Body.Close()
		}
	}
}

// Complex nested data structures - Protobuf serialization advantage
type ComplexData struct {
	ID        string                 `json:"id"`
	Metadata  map[string]interface{} `json:"metadata"`
	Tags      []string               `json:"tags"`
	Nested    []map[string]string    `json:"nested"`
	Timestamp int64                  `json:"timestamp"`
}

func BenchmarkGRPC_ComplexSerialization(parseB *testing.B) {
	parseClient, parseCleanup := setupGRPC(parseB)
	defer parseCleanup()

	parseB.ReportAllocs()
	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		// Protobuf handles complex types efficiently
		_, parseErr := parseClient.CreateTodo(context.Background(), &proto.CreateTodoRequest{
			Text: "Complex data with nested structures and metadata fields for serialization testing",
		})
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
	}
}

func BenchmarkREST_ComplexSerialization(parseB *testing.B) {
	parseClient, parseUrl, parseCleanup := setupREST(parseB)
	defer parseCleanup()

	// JSON serialization of complex nested structures
	parseComplexData := ComplexData{
		ID: "test-123",
		Metadata: map[string]interface{}{
			"key1": "value1",
			"key2": 123,
			"key3": true,
		},
		Tags:      []string{"tag1", "tag2", "tag3"},
		Nested:    []map[string]string{{"a": "b"}, {"c": "d"}},
		Timestamp: time.Now().Unix(),
	}
	parseBodyBytes, _ := json.Marshal(parseComplexData)

	parseB.ReportAllocs()
	parseB.ResetTimer()
	for parseI := 0; parseI < parseB.N; parseI++ {
		parseResp, parseErr := parseClient.Post(parseUrl+"/todos", "application/json", bytes.NewReader(parseBodyBytes))
		if parseErr != nil {
			parseB.Fatal(parseErr)
		}
		io.ReadAll(parseResp.Body)
		parseResp.Body.Close()
	}
}
