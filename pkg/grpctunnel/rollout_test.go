//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestBackpressure_SlowReaderBoundsInFlightData verifies HTTP/2 flow control
// stops a fast sender when the peer does not read: a client that never
// receives must NOT be able to push an unbounded volume through the tunnel.
func TestBackpressure_SlowReaderBoundsInFlightData(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)
	server := httptest.NewServer(Wrap(grpcServer, WithOriginCheck(func(*http.Request) bool { return true })))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := DialContext(ctx, "ws"+server.URL[4:], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	stream, err := proto.NewTodoServiceClient(conn).SyncTodos(ctx)
	if err != nil {
		t.Fatalf("stream open failed: %v", err)
	}

	// The mock service echoes every create; with no client Recv, both
	// directions' windows fill and sends must stall well before this cap.
	const chunkBytes = 64 * 1024
	const chunkCap = 512 // 32 MiB — must NOT all fit in flight
	chunk := strings.Repeat("b", chunkBytes)

	var sent atomic.Int64
	sendDone := make(chan error, 1)
	go func() {
		for i := 0; i < chunkCap; i++ {
			if err := stream.Send(&proto.SyncRequest{
				Action: &proto.SyncRequest_Create{Create: &proto.CreateTodoRequest{Text: chunk}},
			}); err != nil {
				sendDone <- err
				return
			}
			sent.Add(1)
		}
		sendDone <- nil
	}()

	// Wait for the sender to stall: no progress for one full second.
	deadline := time.Now().Add(15 * time.Second)
	stalled := false
	for time.Now().Before(deadline) {
		before := sent.Load()
		time.Sleep(time.Second)
		if sent.Load() == before {
			stalled = true
			break
		}
	}
	if !stalled {
		t.Fatalf("sender never stalled; flow control did not engage (sent %d chunks)", sent.Load())
	}
	if sent.Load() >= chunkCap {
		t.Fatalf("all %d chunks (%d MiB) fit in flight with no reader; backpressure is broken", chunkCap, chunkCap*chunkBytes>>20)
	}
	t.Logf("sender stalled after %d chunks (~%d KiB in flight)", sent.Load(), sent.Load()*chunkBytes/1024)

	// Draining the responses must release the sender and complete the stream.
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				return
			}
		}
	}()
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("sender failed after drain: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("sender did not complete after responses were drained")
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close send failed: %v", err)
	}
}

// runConcurrentStreamingWorkload pushes streams MiB concurrently from many clients.
func runConcurrentStreamingWorkload(t *testing.T, clients int, chunksPerClient int, opts ...ServerOption) {
	t.Helper()
	grpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(grpcServer, &mockService{})
	defer grpcServer.Stop()

	allOpts := append([]ServerOption{WithOriginCheck(func(*http.Request) bool { return true })}, opts...)
	server := httptest.NewServer(Wrap(grpcServer, allOpts...))
	defer server.Close()
	wsURL := "ws" + server.URL[4:]
	chunk := strings.Repeat("c", 64*1024)

	var wg sync.WaitGroup
	errs := make(chan error, clients)
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			conn, err := DialContext(ctx, wsURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				errs <- fmt.Errorf("client %d dial: %w", id, err)
				return
			}
			defer conn.Close()
			stream, err := proto.NewTodoServiceClient(conn).BulkCreateTodos(ctx)
			if err != nil {
				errs <- fmt.Errorf("client %d open: %w", id, err)
				return
			}
			for i := 0; i < chunksPerClient; i++ {
				if err := stream.Send(&proto.BulkCreateRequest{Text: chunk}); err != nil {
					errs <- fmt.Errorf("client %d send %d: %w", id, i, err)
					return
				}
			}
			response, err := stream.CloseAndRecv()
			if err != nil {
				errs <- fmt.Errorf("client %d close: %w", id, err)
				return
			}
			if int(response.GetCreatedCount()) != chunksPerClient {
				errs <- fmt.Errorf("client %d count = %d, want %d", id, response.GetCreatedCount(), chunksPerClient)
			}
		}(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestConcurrentStreaming_HandlerMode runs 12 clients x 4 MiB simultaneously.
func TestConcurrentStreaming_HandlerMode(t *testing.T) {
	runConcurrentStreamingWorkload(t, 12, 64)
}

// TestConcurrentStreaming_NativeMode runs 12 clients x 4 MiB simultaneously.
func TestConcurrentStreaming_NativeMode(t *testing.T) {
	runConcurrentStreamingWorkload(t, 12, 64, WithNativeGRPCTransport())
}

// TestReconnectStorm_AllClientsRecoverAfterOutage verifies that a fleet of
// clients dropped by a bridge outage all reconnect once it returns.
func TestReconnectStorm_AllClientsRecoverAfterOutage(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)
	options := []ServerOption{WithOriginCheck(func(*http.Request) bool { return true })}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	addr := listener.Addr().String()
	firstServer := NewServer(addr, grpcServer, options...)
	go firstServer.Serve(listener)

	const fleet = 16
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conns := make([]*grpc.ClientConn, fleet)
	for i := range conns {
		conns[i], err = DialContext(ctx, "ws://"+addr,
			WithReconnectPolicy(ReconnectConfig{InitialDelay: 50 * time.Millisecond, MaxDelay: 500 * time.Millisecond}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			t.Fatalf("client %d dial failed: %v", i, err)
		}
		defer conns[i].Close()
		if _, err := proto.NewTodoServiceClient(conns[i]).CreateTodo(ctx, &proto.CreateTodoRequest{Text: "pre"}); err != nil {
			t.Fatalf("client %d pre-outage RPC failed: %v", i, err)
		}
	}

	// Outage: hard-close the bridge, dropping every tunnel at once.
	_ = firstServer.Close()

	// Recovery on the same address.
	var restartListener net.Listener
	for attempt := 0; attempt < 50; attempt++ {
		restartListener, err = net.Listen("tcp", addr)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("re-listen failed: %v", err)
	}
	secondServer := NewServer(addr, grpcServer, options...)
	defer secondServer.Close()
	go secondServer.Serve(restartListener)

	// The whole fleet storms back concurrently.
	var wg sync.WaitGroup
	errs := make(chan error, fleet)
	for i, clientConn := range conns {
		wg.Add(1)
		go func(id int, cc *grpc.ClientConn) {
			defer wg.Done()
			if _, err := proto.NewTodoServiceClient(cc).CreateTodo(ctx, &proto.CreateTodoRequest{Text: "post"}, grpc.WaitForReady(true)); err != nil {
				errs <- fmt.Errorf("client %d post-outage RPC failed: %w", id, err)
			}
		}(i, clientConn)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// runGracefulDrainTest verifies grpcServer.GracefulStop waits for in-flight
// tunneled streams and rejects new work, in the given transport mode.
func runGracefulDrainTest(t *testing.T, opts ...ServerOption) {
	t.Helper()
	grpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(grpcServer, &mockService{})

	allOpts := append([]ServerOption{WithOriginCheck(func(*http.Request) bool { return true })}, opts...)
	server := httptest.NewServer(Wrap(grpcServer, allOpts...))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := DialContext(ctx, "ws"+server.URL[4:], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	stream, err := proto.NewTodoServiceClient(conn).SyncTodos(ctx)
	if err != nil {
		t.Fatalf("stream open failed: %v", err)
	}
	if err := stream.Send(&proto.SyncRequest{Action: &proto.SyncRequest_Create{Create: &proto.CreateTodoRequest{Text: "drain"}}}); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("recv failed: %v", err)
	}

	stopDone := make(chan struct{})
	stopPanic := make(chan interface{}, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				stopPanic <- recovered
				return
			}
			close(stopDone)
		}()
		grpcServer.GracefulStop()
	}()
	select {
	case recovered := <-stopPanic:
		t.Fatalf("GracefulStop panicked: %v", recovered)
	case <-time.After(200 * time.Millisecond):
	}

	// GracefulStop must wait for the active stream.
	select {
	case <-stopDone:
		t.Fatal("GracefulStop returned while a stream was active")
	case <-time.After(500 * time.Millisecond):
	}

	// Finish the stream; drain must then complete.
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close send failed: %v", err)
	}
	for {
		if _, err := stream.Recv(); err != nil {
			break
		}
	}
	select {
	case <-stopDone:
	case <-time.After(10 * time.Second):
		t.Fatal("GracefulStop did not return after the stream finished")
	}
}

// TestGracefulDrain_NativeMode verifies deployment draining in native mode:
// GracefulStop waits for in-flight tunneled streams before returning.
func TestGracefulDrain_NativeMode(t *testing.T) {
	runGracefulDrainTest(t, WithNativeGRPCTransport())
}

// TestGracefulDrain_HandlerModeLimitation pins the documented handler-mode
// limitation: grpc-go's ServeHTTP transport has an unimplemented Drain(), so
// GracefulStop PANICS while handler-mode tunnels are active. Deployments in
// handler mode must drain by stopping new upgrades and bounding session
// lifetime instead (see CONNECTION_LIFECYCLE.md). If a future grpc-go
// implements Drain for ServeHTTP transports, this test fails so the docs and
// guidance get updated.
func TestGracefulDrain_HandlerModeLimitation(t *testing.T) {
	grpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(grpcServer, &mockService{})
	defer grpcServer.Stop()

	server := httptest.NewServer(Wrap(grpcServer, WithOriginCheck(func(*http.Request) bool { return true })))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := DialContext(ctx, "ws"+server.URL[4:], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()
	stream, err := proto.NewTodoServiceClient(conn).SyncTodos(ctx)
	if err != nil {
		t.Fatalf("stream open failed: %v", err)
	}
	if err := stream.Send(&proto.SyncRequest{Action: &proto.SyncRequest_Create{Create: &proto.CreateTodoRequest{Text: "drain"}}}); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("recv failed: %v", err)
	}

	panicked := make(chan interface{}, 1)
	go func() {
		defer func() {
			panicked <- recover()
		}()
		grpcServer.GracefulStop()
	}()
	select {
	case recovered := <-panicked:
		if recovered == nil {
			t.Fatal("GracefulStop no longer panics in handler mode — grpc-go implemented Drain(); update CONNECTION_LIFECYCLE.md guidance and promote handler-mode draining")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("GracefulStop neither panicked nor returned")
	}
}

// TestSessionMaxLifetime_ForcesReauthorization verifies expired sessions are
// closed and clients transparently reconnect through the Authorize hook.
func TestSessionMaxLifetime_ForcesReauthorization(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)

	var authorizations atomic.Int64
	disconnected := make(chan struct{}, 4)
	handler := Wrap(grpcServer,
		WithOriginCheck(func(*http.Request) bool { return true }),
		WithSessionMaxLifetime(400*time.Millisecond),
		WithAuthorize(func(*http.Request) error {
			authorizations.Add(1)
			return nil
		}),
		WithDisconnectHook(func(*http.Request) {
			select {
			case disconnected <- struct{}{}:
			default:
			}
		}),
	)
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := DialContext(ctx, "ws"+server.URL[4:],
		WithReconnectPolicy(ReconnectConfig{InitialDelay: 50 * time.Millisecond, MaxDelay: 250 * time.Millisecond}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()
	client := proto.NewTodoServiceClient(conn)

	if _, err := client.CreateTodo(ctx, &proto.CreateTodoRequest{Text: "first session"}); err != nil {
		t.Fatalf("first RPC failed: %v", err)
	}

	// The lifetime bound must terminate the session.
	select {
	case <-disconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("session was not closed at max lifetime")
	}

	// The client reconnects and re-passes authorization.
	if _, err := client.CreateTodo(ctx, &proto.CreateTodoRequest{Text: "second session"}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("post-expiry RPC failed: %v", err)
	}
	if got := authorizations.Load(); got < 2 {
		t.Fatalf("authorizations = %d, want >= 2 (reconnect must re-authorize)", got)
	}
}

// TestGetBridgeConfigError_NegativeSessionLifetime verifies validation.
func TestGetBridgeConfigError_NegativeSessionLifetime(t *testing.T) {
	if err := GetBridgeConfigError(BridgeConfig{SessionMaxLifetime: -time.Second}); err == nil {
		t.Fatal("negative SessionMaxLifetime accepted")
	}
}
