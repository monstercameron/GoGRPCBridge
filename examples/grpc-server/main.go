package main

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto" // Adjust to your module path
)

const parsePersistSignalBufferSize = 1
const parsePersistDebounceDuration = 40 * time.Millisecond

var shouldLogTodoOperations = os.Getenv("GOGRPCBRIDGE_VERBOSE_TODO_LOGS") == "1"

// loadTodos reads from ../_shared/data/todos.json
func loadTodos() ([]*proto.Todo, error) {
	const filePath = "../_shared/data/todos.json"

	if _, parseErr := os.Stat(filePath); os.IsNotExist(parseErr) {
		log.Printf("No proto.json found; creating new at %s\n", filePath)
		if parseErr2 := os.WriteFile(filePath, []byte("[]"), 0600); parseErr2 != nil {
			return nil, fmt.Errorf("failed to create proto.json: %w", parseErr2)
		}
	}

	parseData, parseErr3 := os.ReadFile(filePath)
	if parseErr3 != nil {
		return nil, fmt.Errorf("failed to read proto.json: %w", parseErr3)
	}
	if len(parseData) == 0 {
		return []*proto.Todo{}, nil
	}

	var parseTs []*proto.Todo
	if parseErr4 := json.Unmarshal(parseData, &parseTs); parseErr4 != nil {
		log.Printf("Invalid JSON; resetting file. Error: %v\n", parseErr4)
		_ = os.WriteFile(filePath, []byte("[]"), 0600)
		return []*proto.Todo{}, nil
	}
	return parseTs, nil
}

// saveTodos saveTodos writes the in-memory slice back to ../_shared/data/todos.json.
func saveTodos(parseTodosSlice []*proto.Todo) error {
	const filePath = "../_shared/data/todos.json"
	parseOut, parseErr := json.Marshal(parseTodosSlice)
	if parseErr != nil {
		return parseErr
	}
	return os.WriteFile(filePath, parseOut, 0600)
}

// buildTodoStore buildTodoStore initializes insertion-order todo storage and ID lookups.
func buildTodoStore(parseTodosSlice []*proto.Todo) (*list.List, map[string]*list.Element) {
	parseTodoOrder := list.New()
	parseTodoByID := make(map[string]*list.Element, len(parseTodosSlice))

	for _, parseTodo := range parseTodosSlice {
		if parseTodo == nil {
			continue
		}
		if parseTodo.Id == "" {
			log.Printf("WARN: skipping todo with empty ID during startup")
			continue
		}
		if _, hasTodo := parseTodoByID[parseTodo.Id]; hasTodo {
			log.Printf("WARN: skipping duplicate todo ID during startup: %s", parseTodo.Id)
			continue
		}
		parseTodoByID[parseTodo.Id] = parseTodoOrder.PushBack(parseTodo)
	}

	return parseTodoOrder, parseTodoByID
}

// buildTodosSnapshot buildTodosSnapshot copies todos in insertion order so callers can release locks safely.
func buildTodosSnapshot(parseTodoOrder *list.List, parseTodoByID map[string]*list.Element) []*proto.Todo {
	parseTodosSnapshot := make([]*proto.Todo, 0, len(parseTodoByID))
	for parseTodoNode := parseTodoOrder.Front(); parseTodoNode != nil; parseTodoNode = parseTodoNode.Next() {
		parseTodo, parseIsTodo := parseTodoNode.Value.(*proto.Todo)
		if !parseIsTodo || parseTodo == nil {
			continue
		}
		parseTodosSnapshot = append(parseTodosSnapshot, parseTodo)
	}
	return parseTodosSnapshot
}

// handleTodosPersistResult records persistence failures and warns on slow writes.
func handleTodosPersistResult(parseAction string, parseTodosSlice []*proto.Todo, parsePersistStarted time.Time, parsePersistErr error) {
	if parsePersistErr != nil {
		log.Printf("ERROR: failed to persist todos after %s: %v", parseAction, parsePersistErr)
		return
	}

	parsePersistDuration := time.Since(parsePersistStarted)
	if parsePersistDuration > 150*time.Millisecond {
		log.Printf("WARN: persisting todos after %s took %s for %d records", parseAction, parsePersistDuration, len(parseTodosSlice))
	}
}

// todoServer implements the gRPC methods for TodoService
type todoServer struct {
	proto.UnimplementedTodoServiceServer
	mu               sync.RWMutex
	store            *list.List
	storeByID        map[string]*list.Element
	persistSignal    chan struct{}
	persistActionLog string
}

// signalTodosPersistLocked signalTodosPersistLocked marks the store dirty and schedules one persistence run.
func (parseS *todoServer) signalTodosPersistLocked(parseAction string) {
	parseS.persistActionLog = parseAction
	select {
	case parseS.persistSignal <- struct{}{}:
	default:
	}
}

// runTodosPersistWorker runTodosPersistWorker coalesces mutation signals and persists snapshots asynchronously.
func (parseS *todoServer) runTodosPersistWorker() {
	for range parseS.persistSignal {
		// Debounce high-frequency write bursts so multiple mutations collapse into one file rewrite.
		parseDebounceTimer := time.NewTimer(parsePersistDebounceDuration)
		for {
			select {
			case <-parseS.persistSignal:
				if !parseDebounceTimer.Stop() {
					select {
					case <-parseDebounceTimer.C:
					default:
					}
				}
				parseDebounceTimer.Reset(parsePersistDebounceDuration)
			case <-parseDebounceTimer.C:
				goto runPersist
			}
		}

	runPersist:
		parseS.mu.RLock()
		parseStoreSnapshot := buildTodosSnapshot(parseS.store, parseS.storeByID)
		parseAction := parseS.persistActionLog
		parseS.mu.RUnlock()

		parsePersistStarted := time.Now()
		parsePersistErr := saveTodos(parseStoreSnapshot)
		handleTodosPersistResult(parseAction, parseStoreSnapshot, parsePersistStarted, parsePersistErr)
	}
}

func newTodoServer() (*todoServer, error) {
	parseT, parseErr := loadTodos()
	if parseErr != nil {
		return nil, parseErr
	}
	parseTodoOrder, parseTodoByID := buildTodoStore(parseT)
	parseServer := &todoServer{
		store:         parseTodoOrder,
		storeByID:     parseTodoByID,
		persistSignal: make(chan struct{}, parsePersistSignalBufferSize),
	}
	go parseServer.runTodosPersistWorker()
	return parseServer, nil
}

func (parseS *todoServer) CreateTodo(parseCtx context.Context, parseReq *proto.CreateTodoRequest) (*proto.CreateTodoResponse, error) {
	parseS.mu.Lock()

	parseNewID := uuid.New().String()
	parseNewTodo := &proto.Todo{
		Id:   parseNewID,
		Text: parseReq.Text,
		Done: false,
	}
	parseS.storeByID[parseNewID] = parseS.store.PushBack(parseNewTodo)
	parseS.signalTodosPersistLocked("create")
	parseS.mu.Unlock()

	if shouldLogTodoOperations {
		log.Printf("Created new todo: %s => %s", parseNewID, parseReq.Text)
	}
	return &proto.CreateTodoResponse{Todo: parseNewTodo}, nil
}

func (parseS *todoServer) ListTodos(parseCtx context.Context, parseReq *proto.ListTodosRequest) (*proto.ListTodosResponse, error) {
	parseS.mu.RLock()
	parseTodos := buildTodosSnapshot(parseS.store, parseS.storeByID)
	parseS.mu.RUnlock()

	if shouldLogTodoOperations {
		log.Printf("Listing %d todos", len(parseTodos))
	}
	return &proto.ListTodosResponse{Todos: parseTodos}, nil
}

func (parseS *todoServer) UpdateTodo(parseCtx context.Context, parseReq *proto.UpdateTodoRequest) (*proto.UpdateTodoResponse, error) {
	parseS.mu.Lock()
	parseTodoNode, hasTodo := parseS.storeByID[parseReq.Id]
	if !hasTodo {
		parseS.mu.Unlock()
		log.Printf("No todo found with ID: %s\n", parseReq.Id)
		return &proto.UpdateTodoResponse{}, nil
	}

	parseCurrentTodo, parseIsTodo := parseTodoNode.Value.(*proto.Todo)
	if !parseIsTodo || parseCurrentTodo == nil {
		delete(parseS.storeByID, parseReq.Id)
		parseS.store.Remove(parseTodoNode)
		parseS.mu.Unlock()
		log.Printf("WARN: dropped corrupt todo node while updating ID: %s", parseReq.Id)
		return &proto.UpdateTodoResponse{}, nil
	}

	parseUpdated := &proto.Todo{
		Id:   parseCurrentTodo.Id,
		Text: parseReq.Text,
		Done: parseReq.Done,
	}
	parseTodoNode.Value = parseUpdated
	parseS.signalTodosPersistLocked("update")
	parseS.mu.Unlock()

	if shouldLogTodoOperations {
		log.Printf("Updated todo: %s => %s, done=%v", parseUpdated.Id, parseUpdated.Text, parseUpdated.Done)
	}
	return &proto.UpdateTodoResponse{Todo: parseUpdated}, nil
}

func (parseS *todoServer) DeleteTodo(parseCtx context.Context, parseReq *proto.DeleteTodoRequest) (*proto.DeleteTodoResponse, error) {
	parseS.mu.Lock()
	parseTodoNode, hasTodo := parseS.storeByID[parseReq.Id]
	if !hasTodo {
		parseS.mu.Unlock()
		log.Printf("Delete: no todo found with ID %s\n", parseReq.Id)
		return &proto.DeleteTodoResponse{Success: false}, nil
	}

	parseS.store.Remove(parseTodoNode)
	delete(parseS.storeByID, parseReq.Id)
	parseS.signalTodosPersistLocked("delete")
	parseS.mu.Unlock()

	if shouldLogTodoOperations {
		log.Printf("Deleted todo with ID: %s", parseReq.Id)
	}
	return &proto.DeleteTodoResponse{Success: true}, nil
}

func main() {
	// 1. Create the gRPC server
	parseSrv, parseErr := newTodoServer()
	if parseErr != nil {
		log.Fatalf("Failed to create todoServer: %v\n", parseErr)
	}
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, parseSrv)

	parseListener, parseErr := net.Listen("tcp", "localhost:50051")
	if parseErr != nil {
		log.Fatalf("Failed to listen: %v", parseErr)
	}
	log.Printf("gRPC server listening on :50051")
	if parseErr2 := parseGrpcServer.Serve(parseListener); parseErr2 != nil {
		log.Fatalf("gRPC server failed: %v", parseErr2)
	}
}
