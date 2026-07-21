//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestApplyBridgeKeepaliveDefaults verifies keepalive defaulting rules.
func TestApplyBridgeKeepaliveDefaults(t *testing.T) {
	defaulted := applyBridgeKeepaliveDefaults(BridgeConfig{})
	if defaulted.PingInterval != defaultBridgePingInterval || defaulted.IdleTimeout != defaultBridgeIdleTimeout {
		t.Fatalf("defaults = (%v, %v), want (%v, %v)",
			defaulted.PingInterval, defaulted.IdleTimeout, defaultBridgePingInterval, defaultBridgeIdleTimeout)
	}

	explicit := applyBridgeKeepaliveDefaults(BridgeConfig{PingInterval: time.Second, IdleTimeout: 3 * time.Second})
	if explicit.PingInterval != time.Second || explicit.IdleTimeout != 3*time.Second {
		t.Fatalf("explicit keepalive was overridden: (%v, %v)", explicit.PingInterval, explicit.IdleTimeout)
	}

	disabled := applyBridgeKeepaliveDefaults(BridgeConfig{ShouldDisableKeepalive: true})
	if disabled.PingInterval != 0 || disabled.IdleTimeout != 0 {
		t.Fatalf("disabled keepalive still set probing: (%v, %v)", disabled.PingInterval, disabled.IdleTimeout)
	}
}

// TestGetBridgeConfigError_KeepaliveDisabledConflict verifies conflicting keepalive settings are rejected.
func TestGetBridgeConfigError_KeepaliveDisabledConflict(t *testing.T) {
	err := GetBridgeConfigError(BridgeConfig{
		ShouldDisableKeepalive: true,
		PingInterval:           time.Second,
		IdleTimeout:            2 * time.Second,
	})
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}

// TestKeepalive_DetectsDeadPeer verifies the default server keepalive reclaims
// connections whose peer stops responding.
func TestKeepalive_DetectsDeadPeer(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)

	disconnected := make(chan struct{}, 1)
	handler, err := BuildBridgeHandler(grpcServer, BridgeConfig{
		CheckOrigin:  func(*http.Request) bool { return true },
		PingInterval: 100 * time.Millisecond,
		IdleTimeout:  400 * time.Millisecond,
		OnDisconnect: func(*http.Request) {
			select {
			case disconnected <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("BuildBridgeHandler failed: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	// Raw TCP client that completes the websocket upgrade and then goes
	// silent: it never answers pings, simulating a dead peer.
	rawConn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("raw dial failed: %v", err)
	}
	defer rawConn.Close()
	upgrade := "GET / HTTP/1.1\r\nHost: bridge\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := rawConn.Write([]byte(upgrade)); err != nil {
		t.Fatalf("upgrade write failed: %v", err)
	}

	select {
	case <-disconnected:
		// Idle timeout fired and the bridge reclaimed the session.
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not reclaim dead connection within 5s")
	}
}

// TestReconnect_SurvivesServerRestart verifies the documented reconnect story:
// a client with reconnect + keepalive policies transparently redials after the
// bridge goes away and comes back on the same address.
func TestReconnect_SurvivesServerRestart(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)
	options := []ServerOption{WithOriginCheck(func(*http.Request) bool { return true })}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	addr := listener.Addr().String()
	firstServer := NewServer(addr, grpcServer, options...)
	go firstServer.Serve(listener)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := DialContext(ctx, "ws://"+addr,
		WithReconnectPolicy(ReconnectConfig{InitialDelay: 50 * time.Millisecond, MaxDelay: 250 * time.Millisecond}),
		WithTunnelKeepalive(10*time.Second, 5*time.Second),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	client := proto.NewTodoServiceClient(conn)
	if _, err := client.CreateTodo(ctx, &proto.CreateTodoRequest{Text: "before restart"}); err != nil {
		t.Fatalf("RPC before restart failed: %v", err)
	}

	// Tear the bridge down; in-flight tunnels drop.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	_ = firstServer.Shutdown(shutdownCtx)
	_ = firstServer.Close()

	// Bring a new bridge up on the same address; retry binding while the OS
	// releases the port.
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

	// WaitForReady blocks the RPC until the channel reconnects.
	if _, err := client.CreateTodo(ctx, &proto.CreateTodoRequest{Text: "after restart"}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("RPC after restart failed: %v", err)
	}
}

// TestKeepaliveOptionsAndPolicy covers option plumbing and keepalive policy validation.
func TestKeepaliveOptionsAndPolicy(t *testing.T) {
	options := buildServerOptions(WithKeepaliveDisabled(), WithNativeGRPCTransport())
	if !options.shouldDisableKeepalive {
		t.Fatal("WithKeepaliveDisabled did not set shouldDisableKeepalive")
	}
	if !options.shouldUseNativeTransport {
		t.Fatal("WithNativeGRPCTransport did not set shouldUseNativeTransport")
	}

	if err := GetKeepaliveConfigError(KeepaliveConfig{Interval: -1}); err == nil {
		t.Fatal("negative Interval accepted")
	}
	if err := GetKeepaliveConfigError(KeepaliveConfig{Timeout: -1}); err == nil {
		t.Fatal("negative Timeout accepted")
	}

	if _, err := ApplyTunnelKeepalivePolicy(nil, KeepaliveConfig{Interval: -1}); err == nil {
		t.Fatal("ApplyTunnelKeepalivePolicy accepted invalid config")
	}
	dialOptions, err := ApplyTunnelKeepalivePolicy(nil, KeepaliveConfig{})
	if err != nil {
		t.Fatalf("ApplyTunnelKeepalivePolicy failed on defaults: %v", err)
	}
	if len(dialOptions) != 1 {
		t.Fatalf("dial options = %d, want 1", len(dialOptions))
	}

	if _, err := BuildTunnelConn(context.Background(), TunnelConfig{
		Target:          "ws://localhost:1",
		KeepaliveConfig: &KeepaliveConfig{Interval: -1},
	}); err == nil {
		t.Fatal("BuildTunnelConn accepted invalid keepalive config")
	}
}

// TestBridgeConnListener covers listener address identity and closed-listener behavior.
func TestBridgeConnListener(t *testing.T) {
	listener := newBridgeConnListener()
	if listener.Addr().Network() != "grpctunnel" || listener.Addr().String() != "grpctunnel-bridge" {
		t.Fatalf("unexpected listener addr: %s/%s", listener.Addr().Network(), listener.Addr().String())
	}

	if err := listener.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
	if _, err := listener.Accept(); err != net.ErrClosed {
		t.Fatalf("Accept after close = %v, want net.ErrClosed", err)
	}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	if listener.deliver(server) {
		t.Fatal("deliver succeeded on closed listener")
	}
}

// TestNativeTransport_EndToEnd verifies unary, server-streaming, and
// bidirectional RPCs through gRPC's native transport mode.
func TestNativeTransport_EndToEnd(t *testing.T) {
	grpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(grpcServer, &mockService{})
	defer grpcServer.Stop()

	handler := Wrap(grpcServer,
		WithNativeGRPCTransport(),
		WithOriginCheck(func(*http.Request) bool { return true }),
	)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsURL := "ws" + server.URL[4:]

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := DialContext(ctx, wsURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()
	client := proto.NewTodoServiceClient(conn)

	// Unary.
	created, err := client.CreateTodo(ctx, &proto.CreateTodoRequest{Text: "native"})
	if err != nil {
		t.Fatalf("unary RPC failed: %v", err)
	}
	if created.GetTodo().GetText() != "native" {
		t.Fatalf("unary response text = %q, want %q", created.GetTodo().GetText(), "native")
	}

	// Server streaming.
	stream, err := client.StreamTodos(ctx, &proto.StreamTodosRequest{})
	if err != nil {
		t.Fatalf("stream open failed: %v", err)
	}
	streamed := 0
	for {
		_, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatalf("stream recv failed: %v", recvErr)
		}
		streamed++
	}
	if streamed != 3 {
		t.Fatalf("streamed %d todos, want 3", streamed)
	}

	// Bidirectional.
	sync, err := client.SyncTodos(ctx)
	if err != nil {
		t.Fatalf("bidi open failed: %v", err)
	}
	if err := sync.Send(&proto.SyncRequest{Action: &proto.SyncRequest_Create{Create: &proto.CreateTodoRequest{Text: "bidi"}}}); err != nil {
		t.Fatalf("bidi send failed: %v", err)
	}
	if _, err := sync.Recv(); err != nil {
		t.Fatalf("bidi recv failed: %v", err)
	}
	if err := sync.CloseSend(); err != nil {
		t.Fatalf("bidi close failed: %v", err)
	}
}

// TestNativeTransport_ConcurrentClients verifies concurrent tunnels through
// the native transport listener.
func TestNativeTransport_ConcurrentClients(t *testing.T) {
	grpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(grpcServer, &mockService{})
	defer grpcServer.Stop()

	handler := Wrap(grpcServer,
		WithNativeGRPCTransport(),
		WithOriginCheck(func(*http.Request) bool { return true }),
	)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsURL := "ws" + server.URL[4:]

	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			errs <- dialHardeningTestTunnel(t, wsURL)
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent native tunnel %d failed: %v", i, err)
		}
	}
}
