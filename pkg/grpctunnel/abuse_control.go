//go:build !js && !wasm

package grpctunnel

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const bridgeAbuseWindowDuration = time.Minute

// storeBridgeAbuseRateWindow tracks one client's fixed-window upgrade attempt counts.
type storeBridgeAbuseRateWindow struct {
	getWindowStartedAt time.Time
	getWindowCount     int
}

// bridgeAbuseGuard stores in-memory counters used to enforce websocket abuse controls.
type bridgeAbuseGuard struct {
	setGuardLock sync.Mutex
	setConfig    BridgeConfig

	getActiveConnections       int
	storeClientConnections     map[string]int
	storeClientUpgradeAttempts map[string]storeBridgeAbuseRateWindow
	lastWindowSweepAt          time.Time
}

// buildBridgeAbuseGuard creates an abuse guard for bridge runtime controls.
func buildBridgeAbuseGuard(cfg BridgeConfig) *bridgeAbuseGuard {
	return &bridgeAbuseGuard{
		setConfig:                  cfg,
		storeClientConnections:     map[string]int{},
		storeClientUpgradeAttempts: map[string]storeBridgeAbuseRateWindow{},
	}
}

// reserveBridgeConnection validates abuse controls and reserves one connection slot.
func (g *bridgeAbuseGuard) reserveBridgeConnection(r *http.Request, now time.Time) error {
	if g == nil {
		return nil
	}

	clientKey := buildBridgeClientKey(r)
	if clientKey == "" {
		clientKey = "unknown"
	}

	g.setGuardLock.Lock()
	defer g.setGuardLock.Unlock()

	if g.setConfig.MaxUpgradesPerClientPerMinute > 0 {
		g.sweepExpiredUpgradeWindows(now)
		window := g.storeClientUpgradeAttempts[clientKey]
		if window.getWindowStartedAt.IsZero() || now.Sub(window.getWindowStartedAt) >= bridgeAbuseWindowDuration {
			window = storeBridgeAbuseRateWindow{
				getWindowStartedAt: now,
				getWindowCount:     0,
			}
		}
		if window.getWindowCount >= g.setConfig.MaxUpgradesPerClientPerMinute {
			return fmt.Errorf("upgrade rate exceeded for client %q", clientKey)
		}
		window.getWindowCount++
		g.storeClientUpgradeAttempts[clientKey] = window
	}

	if g.setConfig.MaxActiveConnections > 0 && g.getActiveConnections >= g.setConfig.MaxActiveConnections {
		return fmt.Errorf("active connection cap exceeded")
	}

	clientConnections := g.storeClientConnections[clientKey]
	if g.setConfig.MaxConnectionsPerClient > 0 && clientConnections >= g.setConfig.MaxConnectionsPerClient {
		return fmt.Errorf("per-client connection cap exceeded for client %q", clientKey)
	}

	g.getActiveConnections++
	g.storeClientConnections[clientKey] = clientConnections + 1
	return nil
}

// sweepExpiredUpgradeWindows drops expired rate windows so the per-client map
// cannot grow without bound as distinct client addresses churn.
// Callers must hold setGuardLock.
func (g *bridgeAbuseGuard) sweepExpiredUpgradeWindows(now time.Time) {
	if now.Sub(g.lastWindowSweepAt) < bridgeAbuseWindowDuration {
		return
	}
	g.lastWindowSweepAt = now
	for clientKey, window := range g.storeClientUpgradeAttempts {
		if now.Sub(window.getWindowStartedAt) >= bridgeAbuseWindowDuration {
			delete(g.storeClientUpgradeAttempts, clientKey)
		}
	}
}

// clearBridgeConnection releases one reserved connection slot for abuse controls.
func (g *bridgeAbuseGuard) clearBridgeConnection(r *http.Request) {
	if g == nil {
		return
	}

	clientKey := buildBridgeClientKey(r)
	if clientKey == "" {
		clientKey = "unknown"
	}

	g.setGuardLock.Lock()
	defer g.setGuardLock.Unlock()

	if g.getActiveConnections > 0 {
		g.getActiveConnections--
	}

	clientConnections := g.storeClientConnections[clientKey]
	if clientConnections <= 1 {
		delete(g.storeClientConnections, clientKey)
		return
	}
	g.storeClientConnections[clientKey] = clientConnections - 1
}

// buildBridgeClientKey derives a stable client key for abuse controls from request remote address.
func buildBridgeClientKey(r *http.Request) string {
	if r == nil {
		return ""
	}

	remoteAddress := strings.TrimSpace(r.RemoteAddr)
	if remoteAddress == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return strings.TrimSpace(host)
	}
	return remoteAddress
}
