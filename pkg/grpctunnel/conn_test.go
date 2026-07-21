//go:build !js && !wasm

package grpctunnel

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestWebSocketConn_ConcurrentWriteSafety verifies concurrent Write calls do not panic.
func TestWebSocketConn_ConcurrentWriteSafety(parseT *testing.T) {
	parseUpgrader := websocket.Upgrader{
		CheckOrigin: func(parseR *http.Request) bool { return true },
	}

	parseServer := httptest.NewServer(http.HandlerFunc(func(parseW http.ResponseWriter, parseR *http.Request) {
		parseWs, parseErr := parseUpgrader.Upgrade(parseW, parseR, nil)
		if parseErr != nil {
			return
		}
		defer parseWs.Close()

		for {
			if _, _, parseErr = parseWs.ReadMessage(); parseErr != nil {
				return
			}
		}
	}))
	defer parseServer.Close()

	parseWsURL := "ws" + strings.TrimPrefix(parseServer.URL, "http")
	parseClientWs, _, parseErr := websocket.DefaultDialer.Dial(parseWsURL, nil)
	if parseErr != nil {
		parseT.Fatalf("Dial failed: %v", parseErr)
	}
	defer parseClientWs.Close()

	parseConn := newWebSocketConn(parseClientWs)
	defer parseConn.Close()

	parsePanicChannel := make(chan interface{}, 2)
	parseErrorChannel := make(chan error, 2)

	// Run overlapping write loops to exercise concurrent writer behavior.
	parseRunWriter := func() {
		defer func() {
			if parseRecovered := recover(); parseRecovered != nil {
				parsePanicChannel <- parseRecovered
			}
		}()

		for parseIndex := 0; parseIndex < 300; parseIndex++ {
			if _, parseWriteErr := parseConn.Write([]byte("x")); parseWriteErr != nil {
				parseErrorChannel <- parseWriteErr
				return
			}
		}
	}

	var parseWg sync.WaitGroup
	parseWg.Add(2)
	go func() {
		defer parseWg.Done()
		parseRunWriter()
	}()
	go func() {
		defer parseWg.Done()
		parseRunWriter()
	}()
	parseWg.Wait()

	select {
	case parseRecovered := <-parsePanicChannel:
		parseT.Fatalf("Concurrent write panicked: %v", parseRecovered)
	default:
	}

	select {
	case parseWriteErr := <-parseErrorChannel:
		parseT.Fatalf("Concurrent write returned error: %v", parseWriteErr)
	default:
	}
}

// TestWebSocketConn_ReadSkipsEmptyBinaryFrames verifies Read does not return a spurious 0,nil on empty frames.
func TestWebSocketConn_ReadSkipsEmptyBinaryFrames(parseT *testing.T) {
	parseUpgrader := websocket.Upgrader{
		CheckOrigin: func(parseR *http.Request) bool { return true },
	}

	parseServer := httptest.NewServer(http.HandlerFunc(func(parseW http.ResponseWriter, parseR *http.Request) {
		parseWs, parseErr := parseUpgrader.Upgrade(parseW, parseR, nil)
		if parseErr != nil {
			return
		}
		defer parseWs.Close()

		if parseErr = parseWs.WriteMessage(websocket.BinaryMessage, []byte{}); parseErr != nil {
			return
		}
		if parseErr = parseWs.WriteMessage(websocket.BinaryMessage, []byte("abc")); parseErr != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}))
	defer parseServer.Close()

	parseWsURL := "ws" + strings.TrimPrefix(parseServer.URL, "http")
	parseClientWs, _, parseErr := websocket.DefaultDialer.Dial(parseWsURL, nil)
	if parseErr != nil {
		parseT.Fatalf("Dial failed: %v", parseErr)
	}
	defer parseClientWs.Close()

	parseConn := newWebSocketConn(parseClientWs)
	defer parseConn.Close()

	parseBuffer := make([]byte, 3)
	parseReadCount, parseReadErr := parseConn.Read(parseBuffer)
	if parseReadErr != nil {
		parseT.Fatalf("Read failed: %v", parseReadErr)
	}
	if parseReadCount != 3 {
		parseT.Fatalf("Read count = %d, want 3", parseReadCount)
	}
	if string(parseBuffer) != "abc" {
		parseT.Fatalf("Read payload = %q, want %q", string(parseBuffer), "abc")
	}
}

// TestWebSocketConn_ReadRejectsTextFrame verifies non-binary protocol frames terminate reads.
func TestWebSocketConn_ReadRejectsTextFrame(parseT *testing.T) {
	parseUpgrader := websocket.Upgrader{
		CheckOrigin: func(parseR *http.Request) bool { return true },
	}

	parseServer := httptest.NewServer(http.HandlerFunc(func(parseW http.ResponseWriter, parseR *http.Request) {
		parseWs, parseErr := parseUpgrader.Upgrade(parseW, parseR, nil)
		if parseErr != nil {
			return
		}
		defer parseWs.Close()

		if parseErr = parseWs.WriteMessage(websocket.TextMessage, []byte("invalid-frame")); parseErr != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}))
	defer parseServer.Close()

	parseWsURL := "ws" + strings.TrimPrefix(parseServer.URL, "http")
	parseClientWs, _, parseErr := websocket.DefaultDialer.Dial(parseWsURL, nil)
	if parseErr != nil {
		parseT.Fatalf("Dial failed: %v", parseErr)
	}
	defer parseClientWs.Close()

	parseConn := newWebSocketConn(parseClientWs)
	defer parseConn.Close()

	parseBuffer := make([]byte, 16)
	parseReadCount, parseReadErr := parseConn.Read(parseBuffer)
	if parseReadCount != 0 {
		parseT.Fatalf("Read count = %d, want 0", parseReadCount)
	}
	if parseReadErr == nil || errors.Is(parseReadErr, io.EOF) {
		parseT.Fatalf("Read error = %v, want explicit non-EOF protocol error", parseReadErr)
	}
	if !strings.Contains(parseReadErr.Error(), "unexpected websocket message type") {
		parseT.Fatalf("Read error = %v, want message-type protocol error", parseReadErr)
	}
}

// TestWebSocketConn_ReadReturnsProtocolCloseError verifies protocol-close frames surface close metadata to callers.
func TestWebSocketConn_ReadReturnsProtocolCloseError(parseT *testing.T) {
	parseUpgrader := websocket.Upgrader{
		CheckOrigin: func(parseR *http.Request) bool { return true },
	}

	parseServer := httptest.NewServer(http.HandlerFunc(func(parseW http.ResponseWriter, parseR *http.Request) {
		parseWs, parseErr := parseUpgrader.Upgrade(parseW, parseR, nil)
		if parseErr != nil {
			return
		}
		defer parseWs.Close()

		parseClosePayload := websocket.FormatCloseMessage(websocket.CloseProtocolError, "malformed-frame")
		if parseErr = parseWs.WriteControl(websocket.CloseMessage, parseClosePayload, time.Now().Add(time.Second)); parseErr != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}))
	defer parseServer.Close()

	parseWsURL := "ws" + strings.TrimPrefix(parseServer.URL, "http")
	parseClientWs, _, parseErr := websocket.DefaultDialer.Dial(parseWsURL, nil)
	if parseErr != nil {
		parseT.Fatalf("Dial failed: %v", parseErr)
	}
	defer parseClientWs.Close()

	parseConn := newWebSocketConn(parseClientWs)
	defer parseConn.Close()

	parseBuffer := make([]byte, 8)
	_, parseReadErr := parseConn.Read(parseBuffer)
	if parseReadErr == nil {
		parseT.Fatal("Read() expected protocol close error, got nil")
	}

	var parseCloseErr *websocket.CloseError
	if !errors.As(parseReadErr, &parseCloseErr) {
		parseT.Fatalf("Read error = %T, want *websocket.CloseError", parseReadErr)
	}
	if parseCloseErr.Code != websocket.CloseProtocolError {
		parseT.Fatalf("Close code = %d, want %d", parseCloseErr.Code, websocket.CloseProtocolError)
	}
}
