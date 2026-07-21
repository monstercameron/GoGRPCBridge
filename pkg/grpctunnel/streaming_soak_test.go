//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const soakChunkBytes = 64 * 1024
const soakChunkCount = 512 // 32 MiB total per run

// runStreamingSoak pushes soakChunkCount chunks of soakChunkBytes through a
// client stream and verifies the server observed every one.
func runStreamingSoak(t *testing.T, opts ...ServerOption) {
	t.Helper()
	grpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(grpcServer, &mockService{})
	defer grpcServer.Stop()

	allOpts := append([]ServerOption{
		WithOriginCheck(func(*http.Request) bool { return true }),
		WithKeepalive(time.Second, 10*time.Second),
	}, opts...)
	server := httptest.NewServer(Wrap(grpcServer, allOpts...))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn, err := DialContext(ctx, "ws"+server.URL[4:], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	stream, err := proto.NewTodoServiceClient(conn).BulkCreateTodos(ctx)
	if err != nil {
		t.Fatalf("stream open failed: %v", err)
	}
	chunk := strings.Repeat("v", soakChunkBytes)
	for i := 0; i < soakChunkCount; i++ {
		if err := stream.Send(&proto.BulkCreateRequest{Text: chunk}); err != nil {
			t.Fatalf("send %d failed: %v", i, err)
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if response.GetCreatedCount() != soakChunkCount {
		t.Fatalf("server received %d chunks, want %d", response.GetCreatedCount(), soakChunkCount)
	}
}

// TestStreamingSoak_HandlerMode pushes 32 MiB through the handler transport.
func TestStreamingSoak_HandlerMode(t *testing.T) {
	runStreamingSoak(t)
}

// TestStreamingSoak_NativeMode pushes 32 MiB through the native transport.
func TestStreamingSoak_NativeMode(t *testing.T) {
	runStreamingSoak(t, WithNativeGRPCTransport())
}

// runTransportThroughputBench measures sustained client-stream throughput.
func runTransportThroughputBench(b *testing.B, opts ...ServerOption) {
	grpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(grpcServer, &mockService{})
	defer grpcServer.Stop()

	allOpts := append([]ServerOption{WithOriginCheck(func(*http.Request) bool { return true })}, opts...)
	server := httptest.NewServer(Wrap(grpcServer, allOpts...))
	defer server.Close()

	conn, err := Dial("ws"+server.URL[4:], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		b.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	stream, err := proto.NewTodoServiceClient(conn).BulkCreateTodos(context.Background())
	if err != nil {
		b.Fatalf("stream open failed: %v", err)
	}
	chunk := strings.Repeat("v", soakChunkBytes)

	b.SetBytes(soakChunkBytes)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := stream.Send(&proto.BulkCreateRequest{Text: chunk}); err != nil {
			b.Fatalf("send failed: %v", err)
		}
	}
	b.StopTimer()
	if _, err := stream.CloseAndRecv(); err != nil {
		b.Fatalf("close failed: %v", err)
	}
}

func BenchmarkThroughputHandler_64KBChunks(b *testing.B) {
	runTransportThroughputBench(b)
}

func BenchmarkThroughputNative_64KBChunks(b *testing.B) {
	runTransportThroughputBench(b, WithNativeGRPCTransport())
}
