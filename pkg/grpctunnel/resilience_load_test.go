//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestWrap_ReconnectBurst verifies repeated concurrent dial and disconnect cycles remain stable.
func TestWrap_ReconnectBurst(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	parseServer := httptest.NewServer(Wrap(parseGrpcServer))
	defer parseServer.Close()

	parseWebSocketURL := "ws" + parseServer.URL[4:]
	var parseSuccessCount atomic.Int64
	var parseFailureCount atomic.Int64

	var parseWaitGroup sync.WaitGroup
	for parseWorkerIndex := 0; parseWorkerIndex < 8; parseWorkerIndex++ {
		parseWaitGroup.Add(1)
		go func() {
			defer parseWaitGroup.Done()
			for parseAttemptIndex := 0; parseAttemptIndex < 5; parseAttemptIndex++ {
				parseDialContext, clearDial := context.WithTimeout(context.Background(), 3*time.Second)
				parseConnection, parseErr := DialContext(parseDialContext, parseWebSocketURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
				clearDial()
				if parseErr != nil {
					parseFailureCount.Add(1)
					continue
				}
				parseSuccessCount.Add(1)
				_ = parseConnection.Close()
			}
		}()
	}
	parseWaitGroup.Wait()

	if parseSuccessCount.Load() == 0 {
		parseT.Fatal("Reconnect burst expected successful connections, got zero")
	}
	if parseSuccessCount.Load() < 30 {
		parseT.Fatalf("Reconnect burst successes = %d, want >= %d", parseSuccessCount.Load(), 30)
	}
	if parseFailureCount.Load() > 10 {
		parseT.Fatalf("Reconnect burst failures = %d, want <= %d", parseFailureCount.Load(), 10)
	}
}

// TestWrap_CancellationBurst verifies concurrent canceled RPCs fail fast instead of hanging.
func TestWrap_CancellationBurst(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(parseGrpcServer, &mockService{})
	defer parseGrpcServer.Stop()

	parseServer := httptest.NewServer(Wrap(parseGrpcServer))
	defer parseServer.Close()

	parseWebSocketURL := "ws" + parseServer.URL[4:]
	parseDialContext, clearDial := context.WithTimeout(context.Background(), 5*time.Second)
	defer clearDial()
	parseConnection, parseErr := DialContext(parseDialContext, parseWebSocketURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if parseErr != nil {
		parseT.Fatalf("DialContext() error: %v", parseErr)
	}
	defer parseConnection.Close()

	parseClient := proto.NewTodoServiceClient(parseConnection)
	var parseFailureCount atomic.Int64

	var parseWaitGroup sync.WaitGroup
	for parseRequestIndex := 0; parseRequestIndex < 50; parseRequestIndex++ {
		parseWaitGroup.Add(1)
		go func() {
			defer parseWaitGroup.Done()
			parseCanceledContext, clearCanceled := context.WithCancel(context.Background())
			clearCanceled()
			_, parseCallErr := parseClient.ListTodos(parseCanceledContext, &proto.ListTodosRequest{})
			if parseCallErr != nil {
				parseFailureCount.Add(1)
			}
		}()
	}
	parseWaitGroup.Wait()

	if parseFailureCount.Load() != 50 {
		parseT.Fatalf("cancellation failure count = %d, want %d", parseFailureCount.Load(), 50)
	}
}

// TestBuildBridgeHandler_MalformedUpgradeBurst verifies malformed upgrade bursts do not panic and are rejected.
func TestBuildBridgeHandler_MalformedUpgradeBurst(parseT *testing.T) {
	parseGrpcServer := grpc.NewServer()
	defer parseGrpcServer.Stop()

	parseHandler, parseErr := BuildBridgeHandler(parseGrpcServer, BridgeConfig{})
	if parseErr != nil {
		parseT.Fatalf("BuildBridgeHandler() error: %v", parseErr)
	}

	parseServer := httptest.NewServer(parseHandler)
	defer parseServer.Close()

	var parseRejectedCount atomic.Int64
	var parseWaitGroup sync.WaitGroup
	for parseRequestIndex := 0; parseRequestIndex < 50; parseRequestIndex++ {
		parseWaitGroup.Add(1)
		go func() {
			defer parseWaitGroup.Done()
			parseResponse, parseRequestErr := http.Get(parseServer.URL + "/grpc")
			if parseRequestErr != nil {
				return
			}
			defer parseResponse.Body.Close()
			_, _ = io.Copy(io.Discard, parseResponse.Body)
			if parseResponse.StatusCode != http.StatusSwitchingProtocols {
				parseRejectedCount.Add(1)
			}
		}()
	}
	parseWaitGroup.Wait()

	if parseRejectedCount.Load() != 50 {
		parseT.Fatalf("malformed burst rejected count = %d, want %d", parseRejectedCount.Load(), 50)
	}
}
