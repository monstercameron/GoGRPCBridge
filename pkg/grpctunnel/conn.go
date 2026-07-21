//go:build !js && !wasm

package grpctunnel

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// webSocketConn adapts a WebSocket connection to net.Conn interface.
// This is needed because gRPC expects a net.Conn but browsers only have WebSocket.
type webSocketConn struct {
	ws         *websocket.Conn
	reader     io.Reader
	readMu     sync.Mutex
	closeOnce  sync.Once
	isClosed   atomic.Bool
	writeMu    sync.Mutex
	deadlineMu sync.Mutex // Protects deadline operations
}

func newWebSocketConn(ws *websocket.Conn) net.Conn {
	return &webSocketConn{ws: ws}
}

// Read reads binary payload bytes from the underlying WebSocket stream.
func (c *webSocketConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	if c.isClosed.Load() {
		return 0, io.EOF
	}
	if c.ws == nil {
		return 0, io.EOF
	}

	for {
		if c.reader == nil {
			messageType, reader, err := c.ws.NextReader()
			if err != nil {
				return 0, err
			}
			if messageType != websocket.BinaryMessage {
				// gRPC frames are always binary; a text frame is a protocol
				// violation, not a clean end of stream.
				return 0, fmt.Errorf("grpctunnel: unexpected websocket message type %d (want binary)", messageType)
			}
			c.reader = reader
		}

		n, err := c.reader.Read(p)
		if err == io.EOF {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			// Empty frame: continue draining subsequent frames until payload arrives.
			continue
		}
		return n, err
	}
}

// Write writes one binary WebSocket message from the provided byte slice.
func (c *webSocketConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if c.isClosed.Load() {
		return 0, io.ErrClosedPipe
	}
	if c.ws == nil {
		return 0, io.ErrClosedPipe
	}

	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close closes the adapted connection and underlying WebSocket exactly once.
func (c *webSocketConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.isClosed.Store(true)
		if c.ws == nil {
			return
		}
		err = c.ws.Close()
	})
	return err
}

// LocalAddr returns the local network address for this connection.
func (c *webSocketConn) LocalAddr() net.Addr {
	if c.ws == nil {
		return nil
	}
	return c.ws.LocalAddr()
}

// RemoteAddr returns the remote network address for this connection.
func (c *webSocketConn) RemoteAddr() net.Addr {
	if c.ws == nil {
		return nil
	}
	return c.ws.RemoteAddr()
}

// SetDeadline sets read and write deadlines on the underlying WebSocket.
func (c *webSocketConn) SetDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if c.ws == nil {
		return net.ErrClosed
	}
	if err := c.ws.SetReadDeadline(t); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(t)
}

// SetReadDeadline sets the read deadline on the underlying WebSocket.
func (c *webSocketConn) SetReadDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if c.ws == nil {
		return net.ErrClosed
	}
	return c.ws.SetReadDeadline(t)
}

// SetWriteDeadline sets the write deadline on the underlying WebSocket.
func (c *webSocketConn) SetWriteDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if c.ws == nil {
		return net.ErrClosed
	}
	return c.ws.SetWriteDeadline(t)
}
