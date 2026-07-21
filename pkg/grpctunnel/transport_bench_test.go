//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// buildTransportBenchClient starts a bridge in the requested transport mode
// and returns a connected client.
func buildTransportBenchClient(b *testing.B, opts ...ServerOption) (proto.TodoServiceClient, func()) {
	b.Helper()
	grpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(grpcServer, &mockService{})

	allOpts := append([]ServerOption{WithOriginCheck(func(*http.Request) bool { return true })}, opts...)
	server := httptest.NewServer(Wrap(grpcServer, allOpts...))

	conn, err := Dial("ws"+server.URL[4:], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		server.Close()
		grpcServer.Stop()
		b.Fatalf("dial failed: %v", err)
	}
	cleanup := func() {
		conn.Close()
		server.Close()
		grpcServer.Stop()
	}
	return proto.NewTodoServiceClient(conn), cleanup
}

// runTransportUnaryBench measures one unary RPC per iteration.
func runTransportUnaryBench(b *testing.B, client proto.TodoServiceClient) {
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.CreateTodo(ctx, &proto.CreateTodoRequest{Text: "bench"}); err != nil {
			b.Fatalf("RPC failed: %v", err)
		}
	}
}

// runTransportStreamBench measures one full server-stream drain per iteration.
func runTransportStreamBench(b *testing.B, client proto.TodoServiceClient) {
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stream, err := client.StreamTodos(ctx, &proto.StreamTodosRequest{})
		if err != nil {
			b.Fatalf("stream open failed: %v", err)
		}
		for {
			_, recvErr := stream.Recv()
			if recvErr == io.EOF {
				break
			}
			if recvErr != nil {
				b.Fatalf("stream recv failed: %v", recvErr)
			}
		}
	}
}

func BenchmarkTransportHandler_Unary(b *testing.B) {
	client, cleanup := buildTransportBenchClient(b)
	defer cleanup()
	runTransportUnaryBench(b, client)
}

func BenchmarkTransportNative_Unary(b *testing.B) {
	client, cleanup := buildTransportBenchClient(b, WithNativeGRPCTransport())
	defer cleanup()
	runTransportUnaryBench(b, client)
}

func BenchmarkTransportHandler_ServerStream(b *testing.B) {
	client, cleanup := buildTransportBenchClient(b)
	defer cleanup()
	runTransportStreamBench(b, client)
}

func BenchmarkTransportNative_ServerStream(b *testing.B) {
	client, cleanup := buildTransportBenchClient(b, WithNativeGRPCTransport())
	defer cleanup()
	runTransportStreamBench(b, client)
}
