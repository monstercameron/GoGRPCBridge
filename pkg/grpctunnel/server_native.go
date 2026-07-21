//go:build !js && !wasm

package grpctunnel

import (
	"net"
	"sync"
)

// bridgeListenerAddr is the synthetic listener address reported for
// websocket-fed gRPC listeners.
type bridgeListenerAddr struct{}

// Network returns the synthetic network name for bridged connections.
func (bridgeListenerAddr) Network() string { return "grpctunnel" }

// String returns the synthetic address string for bridged connections.
func (bridgeListenerAddr) String() string { return "grpctunnel-bridge" }

// bridgeConnListener adapts upgraded websocket connections into a
// net.Listener so grpc.Server.Serve applies its native HTTP/2 server
// transport to tunneled sessions.
type bridgeConnListener struct {
	conns     chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

// newBridgeConnListener creates a listener fed by websocket upgrades.
func newBridgeConnListener() *bridgeConnListener {
	return &bridgeConnListener{
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

// Accept returns the next tunneled connection or net.ErrClosed after Close.
func (l *bridgeConnListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Close releases Accept callers. grpc.Server.Serve calls this on Stop and
// GracefulStop.
func (l *bridgeConnListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
	})
	return nil
}

// Addr returns the synthetic bridge address.
func (l *bridgeConnListener) Addr() net.Addr { return bridgeListenerAddr{} }

// deliver hands one connection to Accept, or reports false when the listener
// is closed (server stopped) so the caller can reject the session.
func (l *bridgeConnListener) deliver(conn net.Conn) bool {
	select {
	case l.conns <- conn:
		return true
	case <-l.closed:
		return false
	}
}

// notifyCloseConn wraps a net.Conn and signals when it is closed, letting the
// upgrade handler block until the gRPC transport finishes with the session.
type notifyCloseConn struct {
	net.Conn
	done      chan struct{}
	closeOnce sync.Once
}

// newNotifyCloseConn wraps conn with close notification.
func newNotifyCloseConn(conn net.Conn) *notifyCloseConn {
	return &notifyCloseConn{Conn: conn, done: make(chan struct{})}
}

// Close closes the underlying connection and signals waiters exactly once.
func (c *notifyCloseConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(func() {
		close(c.done)
	})
	return err
}
