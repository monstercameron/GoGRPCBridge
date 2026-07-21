//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// waitForGoroutineSettle polls until the goroutine count drops to at most
// baseline+slack, tolerating runtime and library background goroutines.
func waitForGoroutineSettle(t *testing.T, baseline int, slack int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var current int
	for time.Now().Before(deadline) {
		runtime.GC()
		current = runtime.NumGoroutine()
		if current <= baseline+slack {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	t.Fatalf("goroutines did not settle: baseline=%d current=%d (slack %d)\n%s", baseline, current, slack, buf[:n])
}

// runLeakCycles opens, exercises, and closes full tunnels against wsURL.
func runLeakCycles(t *testing.T, wsURL string, cycles int) {
	t.Helper()
	for i := 0; i < cycles; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn, err := DialContext(ctx, wsURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			cancel()
			t.Fatalf("cycle %d dial failed: %v", i, err)
		}
		client := proto.NewTodoServiceClient(conn)
		if _, err := client.CreateTodo(ctx, &proto.CreateTodoRequest{Text: fmt.Sprintf("cycle-%d", i)}); err != nil {
			conn.Close()
			cancel()
			t.Fatalf("cycle %d RPC failed: %v", i, err)
		}
		conn.Close()
		cancel()
	}
}

// TestGoroutineLeak_HandlerMode verifies repeated connect/RPC/disconnect
// cycles through the handler transport release all per-session goroutines.
func TestGoroutineLeak_HandlerMode(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)
	server := httptest.NewServer(Wrap(grpcServer,
		WithOriginCheck(func(*http.Request) bool { return true }),
		WithKeepalive(time.Second, 5*time.Second),
	))
	defer server.Close()

	runLeakCycles(t, "ws"+server.URL[4:], 2) // warm lazy machinery
	runtime.GC()
	baseline := runtime.NumGoroutine()

	runLeakCycles(t, "ws"+server.URL[4:], 25)
	waitForGoroutineSettle(t, baseline, 8)
}

// TestGoroutineLeak_NativeMode verifies the native transport path (listener
// delivery + session blocking) also releases all per-session goroutines.
func TestGoroutineLeak_NativeMode(t *testing.T) {
	grpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(grpcServer, &mockService{})
	defer grpcServer.Stop()

	server := httptest.NewServer(Wrap(grpcServer,
		WithNativeGRPCTransport(),
		WithOriginCheck(func(*http.Request) bool { return true }),
		WithKeepalive(time.Second, 5*time.Second),
	))
	defer server.Close()

	runLeakCycles(t, "ws"+server.URL[4:], 2)
	runtime.GC()
	baseline := runtime.NumGoroutine()

	runLeakCycles(t, "ws"+server.URL[4:], 25)
	waitForGoroutineSettle(t, baseline, 8)
}

// TestGoroutineLeak_RejectedUpgrades verifies rejection paths (authorization
// and abuse controls) allocate nothing that outlives the request.
func TestGoroutineLeak_RejectedUpgrades(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)
	handler := Wrap(grpcServer,
		WithAuthorize(func(r *http.Request) error {
			if r.Header.Get("X-Token") == "" {
				return fmt.Errorf("no token")
			}
			return nil
		}),
		WithMaxUpgradesPerClientPerMinute(10),
	)
	server := httptest.NewServer(handler)
	defer server.Close()

	runtime.GC()
	baseline := runtime.NumGoroutine()

	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 50; i++ {
		resp, err := client.Get(server.URL)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
		// Mix of 403 (no token) and, once rate-limited, 429.
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("request %d status = %d, want 403 or 429", i, resp.StatusCode)
		}
	}
	client.CloseIdleConnections()
	waitForGoroutineSettle(t, baseline, 8)
}

// TestAbuseGuard_ReleasesSlotsAfterSessions verifies connection accounting
// returns to zero after sessions end, so caps cannot wedge permanently.
func TestAbuseGuard_ReleasesSlotsAfterSessions(t *testing.T) {
	guard := buildBridgeAbuseGuard(BridgeConfig{MaxActiveConnections: 4, MaxConnectionsPerClient: 2})
	requests := make([]*http.Request, 4)
	for i := range requests {
		requests[i] = httptest.NewRequest(http.MethodGet, "/grpc", nil)
		requests[i].RemoteAddr = fmt.Sprintf("10.1.0.%d:999", i/2)
		if err := guard.reserveBridgeConnection(requests[i], time.Now()); err != nil {
			t.Fatalf("reserve %d failed: %v", i, err)
		}
	}
	// At the cap now: one more from any client must fail.
	extra := httptest.NewRequest(http.MethodGet, "/grpc", nil)
	extra.RemoteAddr = "10.1.0.9:999"
	if err := guard.reserveBridgeConnection(extra, time.Now()); err == nil {
		t.Fatal("reserve above global cap succeeded")
	}
	for _, r := range requests {
		guard.clearBridgeConnection(r)
	}
	if guard.getActiveConnections != 0 {
		t.Fatalf("active connections = %d after clear, want 0", guard.getActiveConnections)
	}
	if len(guard.storeClientConnections) != 0 {
		t.Fatalf("client connection map size = %d after clear, want 0", len(guard.storeClientConnections))
	}
	if err := guard.reserveBridgeConnection(extra, time.Now()); err != nil {
		t.Fatalf("reserve after release failed: %v", err)
	}
	guard.clearBridgeConnection(extra)
}

// TestReadLimit_BreachTerminatesSession verifies an oversized inbound message
// ends the session instead of buffering it.
func TestReadLimit_BreachTerminatesSession(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)
	disconnected := make(chan struct{}, 1)
	handler, err := BuildBridgeHandler(grpcServer, BridgeConfig{
		CheckOrigin:    func(*http.Request) bool { return true },
		ReadLimitBytes: 1024,
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

	ws, _, err := websocket.DefaultDialer.Dial("ws"+server.URL[4:], nil)
	if err != nil {
		t.Fatalf("ws dial failed: %v", err)
	}
	defer ws.Close()

	// The write may fail locally when the server tears the connection down
	// mid-message — that is the desired enforcement, not a test failure.
	oversized := make([]byte, 64*1024)
	_ = ws.WriteMessage(websocket.BinaryMessage, oversized)

	select {
	case <-disconnected:
		// Session terminated by the read limit.
	case <-time.After(5 * time.Second):
		t.Fatal("read-limit breach did not terminate the session within 5s")
	}
}
