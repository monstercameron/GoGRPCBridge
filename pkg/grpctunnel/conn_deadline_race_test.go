//go:build !js && !wasm

package grpctunnel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestSetWriteDeadlineConcurrentWithWrite pins the pairing grpc-go performs
// by design and gorilla's concurrency model forbids: http2Client.Close
// calls SetWriteDeadline(now) from one goroutine specifically to kick a
// loopyWriter that is mid-Write on another. Before the fix,
// SetWriteDeadline reached gorilla's Conn.SetWriteDeadline — a plain field
// write that every frame flush reads unsynchronized — and the race
// detector fired on exactly this test's shape (found live by a downstream
// CI's -race run, 2026-08-15). Run under -race; without it this only
// proves the calls don't error or deadlock.
func TestSetWriteDeadlineConcurrentWithWrite(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			if _, _, err = ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientWs, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer clientWs.Close()

	conn := newWebSocketConn(clientWs)
	defer conn.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// The writer: a loopyWriter stand-in, hammering frames.
	wg.Add(1)
	go func() {
		defer wg.Done()
		payload := make([]byte, 512)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := conn.Write(payload); err != nil {
				return
			}
		}
	}()

	// The closer: SetWriteDeadline from another goroutine, over and over —
	// each call must be race-free against the in-flight writes, and a
	// FUTURE deadline must never kill the healthy connection.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		}
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	time.Sleep(200 * time.Millisecond)
	close(stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: SetWriteDeadline blocked behind an in-flight write (or vice versa)")
	}
}
