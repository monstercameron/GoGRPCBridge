//go:build js && wasm

package dialer

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"syscall/js"
	"testing"
	"time"
)

// buildDialerTestSocket creates a mock browser WebSocket object for WASM tests.
func buildDialerTestSocket(parseT *testing.T, parseReadyState int, parseCloseFn func(), parseSendFn func()) (js.Value, func()) {
	parseT.Helper()

	parseSocket := js.Global().Get(jsGlobalObject).New()
	parseSocket.Set(jsPropertyReadyState, parseReadyState)

	parseCloseHandler := js.FuncOf(func(parseThis js.Value, parseArgs []js.Value) interface{} {
		if parseCloseFn != nil {
			parseCloseFn()
		}
		return nil
	})
	parseSendHandler := js.FuncOf(func(parseThis js.Value, parseArgs []js.Value) interface{} {
		if parseSendFn != nil {
			parseSendFn()
		}
		return nil
	})

	parseSocket.Set(jsMethodClose, parseCloseHandler)
	parseSocket.Set(jsMethodSend, parseSendHandler)

	return parseSocket, func() {
		parseCloseHandler.Release()
		parseSendHandler.Release()
	}
}

// storeDialerTestWebSocketConstructor swaps the global WebSocket constructor for one test.
func storeDialerTestWebSocketConstructor(parseT *testing.T, parseSocket js.Value) func() {
	parseT.Helper()

	parsePreviousConstructor := js.Global().Get(jsGlobalWebSocket)
	parseConstructor := js.FuncOf(func(parseThis js.Value, parseArgs []js.Value) interface{} {
		return parseSocket
	})
	js.Global().Set(jsGlobalWebSocket, parseConstructor)

	return func() {
		js.Global().Set(jsGlobalWebSocket, parsePreviousConstructor)
		parseConstructor.Release()
	}
}

// waitDialerTestCondition waits for a condition needed by an asynchronous WASM test.
func waitDialerTestCondition(parseT *testing.T, parseMessage string, parseCondition func() bool) {
	parseT.Helper()

	parseDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(parseDeadline) {
		if parseCondition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	parseT.Fatalf("Timed out waiting for %s", parseMessage)
}

// isDialerTestValueEqual reports whether two JavaScript values are strictly equal.
func isDialerTestValueEqual(parseLeft, parseRight js.Value) bool {
	return js.Global().Get(jsGlobalObject).Call("is", parseLeft, parseRight).Bool()
}

// storeDialerTestEnvironmentState swaps navigator and document metadata for online and visibility tests.
func storeDialerTestEnvironmentState(parseT *testing.T, isBrowserOnline bool, parseVisibilityState string) func() {
	parseT.Helper()

	parsePreviousNavigator := js.Global().Get(jsGlobalNavigator)
	parsePreviousDocument := js.Global().Get(jsGlobalDocument)

	parseNavigator := js.Global().Get(jsGlobalObject).New()
	parseNavigator.Set(jsPropertyOnLine, isBrowserOnline)
	parseDocument := js.Global().Get(jsGlobalObject).New()
	parseDocument.Set(jsPropertyVisibilityState, parseVisibilityState)

	// Node.js defines globalThis.navigator as an accessor property, so a plain
	// assignment is silently ignored; defineProperty replaces it reliably.
	setDialerTestGlobal(jsGlobalNavigator, parseNavigator)
	setDialerTestGlobal(jsGlobalDocument, parseDocument)

	return func() {
		setDialerTestGlobal(jsGlobalNavigator, parsePreviousNavigator)
		setDialerTestGlobal(jsGlobalDocument, parsePreviousDocument)
	}
}

// setDialerTestGlobal overrides one globalThis property even when it is accessor-defined.
func setDialerTestGlobal(parseName string, parseValue js.Value) {
	parseDescriptor := js.Global().Get(jsGlobalObject).New()
	parseDescriptor.Set("value", parseValue)
	parseDescriptor.Set("configurable", true)
	parseDescriptor.Set("writable", true)
	js.Global().Get(jsGlobalObject).Call("defineProperty", js.Global(), parseName, parseDescriptor)
}

// TestBuildDialerEnvironmentState_OnlineVisible verifies diagnostics include online and visible states.
func TestBuildDialerEnvironmentState_OnlineVisible(parseT *testing.T) {
	parseRestoreEnvironment := storeDialerTestEnvironmentState(parseT, true, "visible")
	defer parseRestoreEnvironment()

	parseState := buildDialerEnvironmentState()
	if parseState != "online,visibility=visible" {
		parseT.Fatalf("buildDialerEnvironmentState() = %q, want %q", parseState, "online,visibility=visible")
	}
}

// TestBuildDialerEnvironmentState_OfflineHidden verifies diagnostics include offline and hidden states.
func TestBuildDialerEnvironmentState_OfflineHidden(parseT *testing.T) {
	parseRestoreEnvironment := storeDialerTestEnvironmentState(parseT, false, "hidden")
	defer parseRestoreEnvironment()

	parseState := buildDialerEnvironmentState()
	if parseState != "offline,visibility=hidden" {
		parseT.Fatalf("buildDialerEnvironmentState() = %q, want %q", parseState, "offline,visibility=hidden")
	}
}

// TestBuildDialerEnvironmentState_ReflectsTransitions verifies online and visibility transitions are reflected immediately.
func TestBuildDialerEnvironmentState_ReflectsTransitions(parseT *testing.T) {
	parseRestoreEnvironment := storeDialerTestEnvironmentState(parseT, true, "visible")
	defer parseRestoreEnvironment()

	parseInitialState := buildDialerEnvironmentState()
	if parseInitialState != "online,visibility=visible" {
		parseT.Fatalf("initial buildDialerEnvironmentState() = %q, want %q", parseInitialState, "online,visibility=visible")
	}

	js.Global().Get(jsGlobalNavigator).Set(jsPropertyOnLine, false)
	js.Global().Get(jsGlobalDocument).Set(jsPropertyVisibilityState, "hidden")

	parseTransitionedState := buildDialerEnvironmentState()
	if parseTransitionedState != "offline,visibility=hidden" {
		parseT.Fatalf("transitioned buildDialerEnvironmentState() = %q, want %q", parseTransitionedState, "offline,visibility=hidden")
	}
}

// TestBrowserWebSocketConnection_ReadBuffersQueuedMessages verifies stream-style reads over queued frames.
func TestBrowserWebSocketConnection_ReadBuffersQueuedMessages(parseT *testing.T) {
	parseSocket, parseSocketCleanup := buildDialerTestSocket(parseT, 1, nil, nil)
	defer parseSocketCleanup()

	parseConnection := NewWebSocketConn(parseSocket).(*browserWebSocketConnection)
	defer parseConnection.closeChannels()

	parseConnection.storeIncomingMessage([]byte("abcdef"))
	parseConnection.storeIncomingMessage([]byte("ghi"))

	parseBuffer := make([]byte, 4)

	parseBytesRead, parseErr := parseConnection.Read(parseBuffer)
	if parseErr != nil {
		parseT.Fatalf("Read() unexpected error: %v", parseErr)
	}
	if parseBytesRead != 4 {
		parseT.Fatalf("Read() bytes = %d, want 4", parseBytesRead)
	}
	if string(parseBuffer[:parseBytesRead]) != "abcd" {
		parseT.Fatalf("Read() payload = %q, want %q", string(parseBuffer[:parseBytesRead]), "abcd")
	}

	parseBytesRead, parseErr = parseConnection.Read(parseBuffer)
	if parseErr != nil {
		parseT.Fatalf("Read() unexpected second error: %v", parseErr)
	}
	if parseBytesRead != 2 {
		parseT.Fatalf("Read() second bytes = %d, want 2", parseBytesRead)
	}
	if string(parseBuffer[:parseBytesRead]) != "ef" {
		parseT.Fatalf("Read() second payload = %q, want %q", string(parseBuffer[:parseBytesRead]), "ef")
	}

	parseBytesRead, parseErr = parseConnection.Read(parseBuffer)
	if parseErr != nil {
		parseT.Fatalf("Read() unexpected third error: %v", parseErr)
	}
	if parseBytesRead != 3 {
		parseT.Fatalf("Read() third bytes = %d, want 3", parseBytesRead)
	}
	if string(parseBuffer[:parseBytesRead]) != "ghi" {
		parseT.Fatalf("Read() third payload = %q, want %q", string(parseBuffer[:parseBytesRead]), "ghi")
	}
}

// TestBrowserWebSocketConnection_QueueByteLimit verifies oversized queued payload totals close the connection.
func TestBrowserWebSocketConnection_QueueByteLimit(parseT *testing.T) {
	parseCloseCount := 0
	parseSocket, parseSocketCleanup := buildDialerTestSocket(parseT, 1, func() {
		parseCloseCount++
	}, nil)
	defer parseSocketCleanup()

	parseConnection := NewWebSocketConn(parseSocket).(*browserWebSocketConnection)
	defer parseConnection.closeChannels()

	parseConnection.storeIncomingMessage(make([]byte, limitConnectionQueuedBytes))
	if parseConnection.isConnectionClosed() {
		parseT.Fatal("connection should remain open at exact queued-byte limit")
	}

	parseConnection.storeIncomingMessage([]byte{1})
	if !parseConnection.isConnectionClosed() {
		parseT.Fatal("connection should close when queued-byte limit is exceeded")
	}
	if parseCloseCount != 1 {
		parseT.Fatalf("close() count = %d, want 1", parseCloseCount)
	}
}

// TestBrowserWebSocketConnection_CloseChannelsClearsBufferedPayload verifies close teardown releases queued payload memory.
func TestBrowserWebSocketConnection_CloseChannelsClearsBufferedPayload(parseT *testing.T) {
	parseSocket, parseSocketCleanup := buildDialerTestSocket(parseT, 1, nil, nil)
	defer parseSocketCleanup()

	parseConnection := NewWebSocketConn(parseSocket).(*browserWebSocketConnection)
	parseConnection.storeIncomingMessage([]byte("buffered"))
	parseConnection.readMessageBuffer = []byte("tail")

	parseConnection.closeChannels()

	if parseConnection.queuedMessages != nil {
		parseT.Fatal("closeChannels() expected queuedMessages to be nil")
	}
	if parseConnection.queuedBytesSize != 0 {
		parseT.Fatalf("closeChannels() queuedBytesSize = %d, want 0", parseConnection.queuedBytesSize)
	}
	if parseConnection.shiftQueueStart != 0 {
		parseT.Fatalf("closeChannels() shiftQueueStart = %d, want 0", parseConnection.shiftQueueStart)
	}
	if parseConnection.readMessageBuffer != nil {
		parseT.Fatal("closeChannels() expected readMessageBuffer to be nil")
	}
}

// TestBrowserWebSocketConnection_StoreConnectionErrorConcurrentClose verifies close and error delivery do not panic.
func TestBrowserWebSocketConnection_StoreConnectionErrorConcurrentClose(parseT *testing.T) {
	for parseIteration := 0; parseIteration < 200; parseIteration++ {
		parseSocket, parseSocketCleanup := buildDialerTestSocket(parseT, 1, nil, nil)
		parseConnection := NewWebSocketConn(parseSocket).(*browserWebSocketConnection)

		parsePanicChannel := make(chan interface{}, 2)
		var parseWg sync.WaitGroup
		parseWg.Add(2)

		go func() {
			defer parseWg.Done()
			defer func() {
				if parseRecovered := recover(); parseRecovered != nil {
					parsePanicChannel <- parseRecovered
				}
			}()
			parseConnection.storeConnectionError(io.ErrClosedPipe)
		}()
		go func() {
			defer parseWg.Done()
			defer func() {
				if parseRecovered := recover(); parseRecovered != nil {
					parsePanicChannel <- parseRecovered
				}
			}()
			parseConnection.closeChannels()
		}()

		parseWg.Wait()
		parseSocketCleanup()

		select {
		case parseRecovered := <-parsePanicChannel:
			parseT.Fatalf("concurrent close/error panicked on iteration %d: %v", parseIteration, parseRecovered)
		default:
		}
	}
}

// TestBrowserWebSocketConnection_ReadReturnsStoredError verifies queued connection errors surface to readers.
func TestBrowserWebSocketConnection_ReadReturnsStoredError(parseT *testing.T) {
	parseSocket, parseSocketCleanup := buildDialerTestSocket(parseT, 1, nil, nil)
	defer parseSocketCleanup()

	parseConnection := NewWebSocketConn(parseSocket).(*browserWebSocketConnection)
	defer parseConnection.closeChannels()

	parseConnection.storeConnectionError(io.ErrClosedPipe)

	parseBytesRead, parseErr := parseConnection.Read(make([]byte, 8))
	if parseBytesRead != 0 {
		parseT.Fatalf("Read() bytes = %d, want 0", parseBytesRead)
	}
	if !errors.Is(parseErr, io.ErrClosedPipe) {
		parseT.Fatalf("Read() error = %v, want %v", parseErr, io.ErrClosedPipe)
	}
}

// TestBrowserWebSocketConnection_ReadReturnsUnsupportedDataError verifies invalid JS payloads are reported.
func TestBrowserWebSocketConnection_ReadReturnsUnsupportedDataError(parseT *testing.T) {
	parseSocket, parseSocketCleanup := buildDialerTestSocket(parseT, 1, nil, nil)
	defer parseSocketCleanup()

	parseConnection := NewWebSocketConn(parseSocket).(*browserWebSocketConnection)
	defer parseConnection.closeChannels()

	parseEvent := js.Global().Get(jsGlobalObject).New()
	parseEvent.Set(jsPropertyData, "not-binary")
	parseSocket.Get(jsEventOnMessage).Invoke(parseEvent)

	parseBytesRead, parseErr := parseConnection.Read(make([]byte, 8))
	if parseBytesRead != 0 {
		parseT.Fatalf("Read() bytes = %d, want 0", parseBytesRead)
	}
	if parseErr == nil || !strings.Contains(parseErr.Error(), "unsupported websocket data type") {
		parseT.Fatalf("Read() error = %v, want unsupported data type error", parseErr)
	}
}

// TestBrowserWebSocketConnection_WriteRecoversPanic verifies JS send failures become Go errors.
func TestBrowserWebSocketConnection_WriteRecoversPanic(parseT *testing.T) {
	parseSocket, parseSocketCleanup := buildDialerTestSocket(parseT, 1, nil, func() {
		panic("boom")
	})
	defer parseSocketCleanup()

	parseConnection := NewWebSocketConn(parseSocket).(*browserWebSocketConnection)
	defer parseConnection.closeChannels()

	parseBytesWritten, parseErr := parseConnection.Write([]byte("payload"))
	if parseBytesWritten != 0 {
		parseT.Fatalf("Write() bytes = %d, want 0", parseBytesWritten)
	}
	if parseErr == nil || !strings.Contains(parseErr.Error(), "websocket write failed") || !strings.Contains(parseErr.Error(), "boom") {
		parseT.Fatalf("Write() error = %v, want recovered panic error", parseErr)
	}
}

// TestNewBrowserWebSocketDialer_CancelsConnectingSocket verifies cancellation cleans up temporary handlers.
func TestNewBrowserWebSocketDialer_CancelsConnectingSocket(parseT *testing.T) {
	parseCloseCount := 0
	parseSocket, parseSocketCleanup := buildDialerTestSocket(parseT, 0, func() {
		parseCloseCount++
	}, nil)
	defer parseSocketCleanup()

	parseRestoreConstructor := storeDialerTestWebSocketConstructor(parseT, parseSocket)
	defer parseRestoreConstructor()

	parseDialContext, cancel := context.WithCancel(context.Background())
	cancel()

	_, parseErr := newBrowserWebSocketDialer("ws://example.test")(parseDialContext, "ignored")
	if !errors.Is(parseErr, context.Canceled) {
		parseT.Fatalf("Dial() error = %v, want %v", parseErr, context.Canceled)
	}
	if parseCloseCount != 1 {
		parseT.Fatalf("close() count = %d, want 1", parseCloseCount)
	}
	if parseSocket.Get(jsEventOnOpen).Truthy() {
		parseT.Fatal("temporary onopen handler was not cleared after cancellation")
	}
	if parseSocket.Get(jsEventOnError).Truthy() {
		parseT.Fatal("temporary onerror handler was not cleared after cancellation")
	}
}

// TestNewBrowserWebSocketDialer_CancelsOpenSocket verifies cancellation closes sockets that already reached OPEN.
func TestNewBrowserWebSocketDialer_CancelsOpenSocket(parseT *testing.T) {
	parseCloseCount := 0
	parseSocket, parseSocketCleanup := buildDialerTestSocket(parseT, 1, func() {
		parseCloseCount++
	}, nil)
	defer parseSocketCleanup()

	parseRestoreConstructor := storeDialerTestWebSocketConstructor(parseT, parseSocket)
	defer parseRestoreConstructor()

	parseDialContext, cancel := context.WithCancel(context.Background())
	cancel()

	_, parseErr := newBrowserWebSocketDialer("ws://example.test")(parseDialContext, "ignored")
	if !errors.Is(parseErr, context.Canceled) {
		parseT.Fatalf("Dial() error = %v, want %v", parseErr, context.Canceled)
	}
	if parseCloseCount != 1 {
		parseT.Fatalf("close() count = %d, want 1", parseCloseCount)
	}
}

// TestNewBrowserWebSocketDialer_ClearsTemporaryHandlersOnError verifies handshake failures detach temp handlers.
func TestNewBrowserWebSocketDialer_ClearsTemporaryHandlersOnError(parseT *testing.T) {
	parseCloseCount := 0
	parseSocket, parseSocketCleanup := buildDialerTestSocket(parseT, 0, func() {
		parseCloseCount++
	}, nil)
	defer parseSocketCleanup()

	parseRestoreConstructor := storeDialerTestWebSocketConstructor(parseT, parseSocket)
	defer parseRestoreConstructor()

	parseResultChannel := make(chan error, 1)
	go func() {
		_, parseErr := newBrowserWebSocketDialer("ws://example.test")(context.Background(), "ignored")
		parseResultChannel <- parseErr
	}()

	waitDialerTestCondition(parseT, "temporary error handler", func() bool {
		return parseSocket.Get(jsEventOnError).Truthy()
	})

	parseSocket.Get(jsEventOnError).Invoke(js.Global().Get(jsGlobalObject).New())

	select {
	case parseErr := <-parseResultChannel:
		if parseErr == nil || !strings.Contains(parseErr.Error(), "connection setup") {
			parseT.Fatalf("Dial() error = %v, want handshake error", parseErr)
		}
	case <-time.After(2 * time.Second):
		parseT.Fatal("Timed out waiting for dial error")
	}

	if parseCloseCount != 1 {
		parseT.Fatalf("close() count = %d, want 1", parseCloseCount)
	}
	if parseSocket.Get(jsEventOnOpen).Truthy() {
		parseT.Fatal("temporary onopen handler was not cleared after handshake error")
	}
	if parseSocket.Get(jsEventOnError).Truthy() {
		parseT.Fatal("temporary onerror handler was not cleared after handshake error")
	}
}

// TestNewBrowserWebSocketDialer_ReplacesTemporaryHandlersOnOpen verifies steady-state handlers replace dial handlers.
func TestNewBrowserWebSocketDialer_ReplacesTemporaryHandlersOnOpen(parseT *testing.T) {
	parseCloseCount := 0
	parseSocket, parseSocketCleanup := buildDialerTestSocket(parseT, 0, func() {
		parseCloseCount++
	}, nil)
	defer parseSocketCleanup()

	parseRestoreConstructor := storeDialerTestWebSocketConstructor(parseT, parseSocket)
	defer parseRestoreConstructor()

	parseConnectionChannel := make(chan net.Conn, 1)
	parseErrorChannel := make(chan error, 1)
	go func() {
		parseConnection, parseErr := newBrowserWebSocketDialer("ws://example.test")(context.Background(), "ignored")
		if parseErr != nil {
			parseErrorChannel <- parseErr
			return
		}
		parseConnectionChannel <- parseConnection
	}()

	waitDialerTestCondition(parseT, "temporary open handler", func() bool {
		return parseSocket.Get(jsEventOnOpen).Truthy() && parseSocket.Get(jsEventOnError).Truthy()
	})

	parseTemporaryErrorHandler := parseSocket.Get(jsEventOnError)
	parseSocket.Set(jsPropertyReadyState, 1)
	parseSocket.Get(jsEventOnOpen).Invoke(js.Global().Get(jsGlobalObject).New())

	var parseConnection *browserWebSocketConnection
	select {
	case parseErr := <-parseErrorChannel:
		parseT.Fatalf("Dial() unexpected error: %v", parseErr)
	case parseNetConn := <-parseConnectionChannel:
		parseConnection = parseNetConn.(*browserWebSocketConnection)
	case <-time.After(2 * time.Second):
		parseT.Fatal("Timed out waiting for dial success")
	}
	defer parseConnection.closeChannels()

	if parseSocket.Get(jsEventOnOpen).Truthy() {
		parseT.Fatal("temporary onopen handler was not cleared after open")
	}

	parseSteadyErrorHandler := parseSocket.Get(jsEventOnError)
	if !parseSteadyErrorHandler.Truthy() {
		parseT.Fatal("steady-state onerror handler was not installed after open")
	}
	if isDialerTestValueEqual(parseTemporaryErrorHandler, parseSteadyErrorHandler) {
		parseT.Fatal("temporary onerror handler was not replaced after open")
	}
	if parseCloseCount != 0 {
		parseT.Fatalf("close() count = %d, want 0", parseCloseCount)
	}
}
