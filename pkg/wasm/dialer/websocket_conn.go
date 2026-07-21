//go:build js && wasm

package dialer

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"
)

const (
	// JavaScript API names
	jsGlobalWebSocket  = "WebSocket"
	jsGlobalUint8Array = "Uint8Array"
	jsGlobalObject     = "Object"

	// WebSocket event handlers
	jsEventOnMessage = "onmessage"
	jsEventOnError   = "onerror"
	jsEventOnClose   = "onclose"

	// WebSocket methods
	jsMethodSend  = "send"
	jsMethodClose = "close"

	// WebSocket properties
	jsPropertyBinaryType = "binaryType"
	jsPropertyData       = "data"
	jsPropertyLength     = "length"

	// WebSocket binary type values
	jsBinaryTypeArrayBuffer = "arraybuffer"

	// Network type constants
	networkTypeWebSocket = "websocket"
	addressLocal         = "local"
	addressRemote        = "remote"

	// limitConnectionQueuedMessages caps queued inbound frames so slow readers
	// cannot cause unbounded memory growth in the browser process.
	limitConnectionQueuedMessages = 256
	// limitConnectionQueuedBytes caps total queued inbound payload bytes.
	limitConnectionQueuedBytes = 16 << 20
)

// browserWebSocketConnection implements net.Conn over browser WebSocket APIs.
type browserWebSocketConnection struct {
	browserWebSocket js.Value
	// cacheConnectionUint8Array holds the constructor so hot-path callbacks
	// do not repeatedly resolve it from the JS global object.
	cacheConnectionUint8Array js.Value

	// incomingMessagesChannel is a signal channel that tells Read() new data is queued.
	incomingMessagesChannel chan struct{}
	// incomingErrorsChannel receives socket errors and close notifications.
	incomingErrorsChannel chan error

	// readMessageBuffer stores remaining bytes when Read() receives a message
	// larger than the destination buffer.
	readMessageBuffer []byte
	// queuedMessages stores complete WebSocket messages until Read() consumes them.
	// The queue is bounded by limitConnectionQueuedMessages.
	queuedMessages  [][]byte
	queuedBytesSize int
	shiftQueueStart int

	queueMu sync.Mutex
	readMu  sync.Mutex
	// errorChannelMu serializes error-channel sends vs close to avoid send-on-closed-channel panics.
	errorChannelMu sync.RWMutex

	closeOnce sync.Once
	isClosed  atomic.Bool

	messageHandler *js.Func
	errorHandler   *js.Func
	closeHandler   *js.Func
}

// NewWebSocketConn creates a net.Conn adapter for a browser WebSocket.
func NewWebSocketConn(ws js.Value) net.Conn {
	c := &browserWebSocketConnection{
		browserWebSocket:          ws,
		cacheConnectionUint8Array: js.Global().Get(jsGlobalUint8Array),
		incomingMessagesChannel:   make(chan struct{}, 1),
		incomingErrorsChannel:     make(chan error, 4),
		queuedMessages:            make([][]byte, 0, 8),
	}

	msgHandler := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		messageEvent := args[0]
		messageData := messageEvent.Get(jsPropertyData)

		if messageData.Type() != js.TypeObject {
			c.storeConnectionError(fmt.Errorf("WASM: unsupported websocket data type: %s", messageData.Type().String()))
			return nil
		}

		uint8Array := c.cacheConnectionUint8Array.New(messageData)
		arrayLength := uint8Array.Get(jsPropertyLength).Int()
		messageBytes := make([]byte, arrayLength)
		if arrayLength > 0 {
			js.CopyBytesToGo(messageBytes, uint8Array)
		}

		c.storeIncomingMessage(messageBytes)
		return nil
	})
	c.messageHandler = &msgHandler
	ws.Set(jsEventOnMessage, msgHandler)

	errHandler := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		c.storeConnectionError(net.ErrClosed)
		return nil
	})
	c.errorHandler = &errHandler
	ws.Set(jsEventOnError, errHandler)

	closeFn := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		c.closeChannels()
		return nil
	})
	c.closeHandler = &closeFn
	ws.Set(jsEventOnClose, closeFn)

	return c
}

// storeIncomingMessage queues an inbound WebSocket message and signals readers.
func (c *browserWebSocketConnection) storeIncomingMessage(message []byte) {
	if c.isConnectionClosed() {
		return
	}

	var queueDepth int
	var queueBytes int
	isQueueOverflow := false

	c.queueMu.Lock()
	queueDepth = len(c.queuedMessages) - c.shiftQueueStart
	queueBytes = c.queuedBytesSize
	availableQueueBytes := limitConnectionQueuedBytes - len(message)
	if queueDepth >= limitConnectionQueuedMessages ||
		availableQueueBytes < 0 ||
		queueBytes > availableQueueBytes {
		isQueueOverflow = true
	} else {
		c.clearQueuedMessagesHeadLocked()
		c.queuedMessages = append(c.queuedMessages, message)
		c.queuedBytesSize += len(message)
	}
	c.queueMu.Unlock()

	if isQueueOverflow {
		c.handleConnectionQueueOverflow(queueDepth, queueBytes, len(message))
		return
	}

	// Signal is edge-triggered: one token is enough to wake readers, even if many
	// messages were queued while the reader was busy.
	select {
	case c.incomingMessagesChannel <- struct{}{}:
	default:
	}
}

// clearQueuedMessagesHeadLocked compacts the queue when consumed head space is large.
func (c *browserWebSocketConnection) clearQueuedMessagesHeadLocked() {
	if c.shiftQueueStart == 0 {
		return
	}
	if c.shiftQueueStart < len(c.queuedMessages)/2 &&
		len(c.queuedMessages) < cap(c.queuedMessages) {
		return
	}

	copy(c.queuedMessages, c.queuedMessages[c.shiftQueueStart:])
	keepCount := len(c.queuedMessages) - c.shiftQueueStart
	for i := keepCount; i < len(c.queuedMessages); i++ {
		c.queuedMessages[i] = nil
	}
	c.queuedMessages = c.queuedMessages[:keepCount]
	c.shiftQueueStart = 0
}

// handleConnectionQueueOverflow aborts the socket when inbound buffering exceeds
// the queue safety limit.
func (c *browserWebSocketConnection) handleConnectionQueueOverflow(queueDepth int, queueBytes int, incomingMessageBytes int) {
	overflowErr := fmt.Errorf(
		"WASM: incoming websocket backlog exceeded safety limits (messages: %d/%d, bytes: %d/%d)",
		queueDepth+1,
		limitConnectionQueuedMessages,
		queueBytes+incomingMessageBytes,
		limitConnectionQueuedBytes,
	)
	c.storeConnectionError(overflowErr)
	c.closeChannels()
	c.browserWebSocket.Call(jsMethodClose)
}

// storeConnectionError sends a connection error to waiting readers.
func (c *browserWebSocketConnection) storeConnectionError(err error) {
	if c.isConnectionClosed() {
		return
	}
	c.errorChannelMu.RLock()
	defer c.errorChannelMu.RUnlock()
	if c.isConnectionClosed() {
		return
	}

	// Error delivery is best-effort and non-blocking so browser callbacks never
	// stall the JS event loop.
	select {
	case c.incomingErrorsChannel <- err:
	default:
	}
}

// shiftQueuedMessage pops the next queued message.
func (c *browserWebSocketConnection) shiftQueuedMessage() ([]byte, bool) {
	c.queueMu.Lock()
	defer c.queueMu.Unlock()

	if c.shiftQueueStart >= len(c.queuedMessages) {
		return nil, false
	}

	message := c.queuedMessages[c.shiftQueueStart]
	c.queuedMessages[c.shiftQueueStart] = nil
	c.queuedBytesSize -= len(message)
	if c.queuedBytesSize < 0 {
		c.queuedBytesSize = 0
	}
	c.shiftQueueStart++
	if c.shiftQueueStart >= len(c.queuedMessages) {
		c.queuedMessages = c.queuedMessages[:0]
		c.shiftQueueStart = 0
	} else {
		c.clearQueuedMessagesHeadLocked()
	}
	return message, true
}

// isConnectionClosed reports whether the connection is already closed.
func (c *browserWebSocketConnection) isConnectionClosed() bool {
	return c.isClosed.Load()
}

// closeChannels marks the connection closed, detaches event handlers, and wakes readers.
func (c *browserWebSocketConnection) closeChannels() {
	c.closeOnce.Do(func() {
		c.isClosed.Store(true)

		// Detach JS callbacks first so no late event can write into Go-owned state
		// after teardown begins.
		c.browserWebSocket.Set(jsEventOnMessage, js.Null())
		c.browserWebSocket.Set(jsEventOnError, js.Null())
		c.browserWebSocket.Set(jsEventOnClose, js.Null())

		// Drop buffered payload references so closed sockets release memory promptly.
		c.queueMu.Lock()
		for i := range c.queuedMessages {
			c.queuedMessages[i] = nil
		}
		c.queuedMessages = nil
		c.shiftQueueStart = 0
		c.queuedBytesSize = 0
		c.queueMu.Unlock()
		if c.readMu.TryLock() {
			c.readMessageBuffer = nil
			c.readMu.Unlock()
		}

		c.errorChannelMu.Lock()
		close(c.incomingErrorsChannel)
		c.errorChannelMu.Unlock()
		c.releaseEventHandlers()
	})
}

// releaseEventHandlers releases js.Func handlers that were installed on the socket.
func (c *browserWebSocketConnection) releaseEventHandlers() {
	if c.messageHandler != nil {
		c.messageHandler.Release()
		c.messageHandler = nil
	}
	if c.errorHandler != nil {
		c.errorHandler.Release()
		c.errorHandler = nil
	}
	if c.closeHandler != nil {
		c.closeHandler.Release()
		c.closeHandler = nil
	}
}

// Read reads bytes from the WebSocket stream into dst.
func (c *browserWebSocketConnection) Read(dst []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	if c.isConnectionClosed() {
		return 0, net.ErrClosed
	}

	if len(c.readMessageBuffer) > 0 {
		n := copy(dst, c.readMessageBuffer)
		c.readMessageBuffer = c.readMessageBuffer[n:]
		return n, nil
	}

	for {
		// Preserve stream semantics across message boundaries by draining any queued
		// frame and buffering only the unread tail for the next Read call.
		if queuedMessage, ok := c.shiftQueuedMessage(); ok {
			n := copy(dst, queuedMessage)
			if n < len(queuedMessage) {
				c.readMessageBuffer = queuedMessage[n:]
			}
			return n, nil
		}

		select {
		case err, ok := <-c.incomingErrorsChannel:
			if !ok {
				return 0, net.ErrClosed
			}
			return 0, err
		case <-c.incomingMessagesChannel:
		}
	}
}

// Write writes src to the WebSocket as one binary message.
func (c *browserWebSocketConnection) Write(src []byte) (n int, err error) {
	if c.isConnectionClosed() {
		return 0, net.ErrClosed
	}

	defer func() {
		// JS interop can panic if the browser socket state is invalid; convert that
		// into a Go error so callers see a normal transport failure.
		if recovered := recover(); recovered != nil {
			n = 0
			err = fmt.Errorf("WASM: websocket write failed: %v", recovered)
		}
	}()

	uint8ArrayToSend := c.cacheConnectionUint8Array.New(len(src))
	js.CopyBytesToJS(uint8ArrayToSend, src)
	c.browserWebSocket.Call(jsMethodSend, uint8ArrayToSend)

	return len(src), nil
}

// Close closes the WebSocket connection.
func (c *browserWebSocketConnection) Close() error {
	c.closeChannels()
	c.browserWebSocket.Call(jsMethodClose)
	return nil
}

// LocalAddr returns a placeholder local address.
func (c *browserWebSocketConnection) LocalAddr() net.Addr {
	return &browserWebSocketAddr{networkTypeWebSocket, addressLocal}
}

// RemoteAddr returns a placeholder remote address.
func (c *browserWebSocketConnection) RemoteAddr() net.Addr {
	return &browserWebSocketAddr{networkTypeWebSocket, addressRemote}
}

// SetDeadline is a no-op in browser WebSocket environments.
func (c *browserWebSocketConnection) SetDeadline(deadline time.Time) error {
	return nil
}

// SetReadDeadline is a no-op in browser WebSocket environments.
func (c *browserWebSocketConnection) SetReadDeadline(deadline time.Time) error {
	return nil
}

// SetWriteDeadline is a no-op in browser WebSocket environments.
func (c *browserWebSocketConnection) SetWriteDeadline(deadline time.Time) error {
	return nil
}

// browserWebSocketAddr is a placeholder implementation of net.Addr.
type browserWebSocketAddr struct {
	networkType   string
	addressString string
}

// Network returns the placeholder network type.
func (a *browserWebSocketAddr) Network() string { return a.networkType }

// String returns the placeholder address string.
func (a *browserWebSocketAddr) String() string { return a.addressString }
