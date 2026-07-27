package bridge

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const parseHandlerAbuseWindowDuration = time.Minute

// storeHandlerAbuseRateWindow tracks one client's fixed-window upgrade attempt counts.
type storeHandlerAbuseRateWindow struct {
	getWindowStartedAt time.Time
	getWindowCount     int
}

// handlerAbuseGuard stores in-memory counters used to enforce websocket abuse controls.
type handlerAbuseGuard struct {
	setGuardLock sync.Mutex
	setConfig    Config

	getActiveConnections       int
	storeClientConnections     map[string]int
	storeClientUpgradeAttempts map[string]storeHandlerAbuseRateWindow
}

// buildHandlerAbuseGuard creates an abuse guard for bridge runtime controls.
func buildHandlerAbuseGuard(parseConfig Config) *handlerAbuseGuard {
	return &handlerAbuseGuard{
		setConfig:                  parseConfig,
		storeClientConnections:     map[string]int{},
		storeClientUpgradeAttempts: map[string]storeHandlerAbuseRateWindow{},
	}
}

// reserveHandlerConnection validates abuse controls and reserves one connection slot.
func (parseGuard *handlerAbuseGuard) reserveHandlerConnection(parseRequest *http.Request, parseNow time.Time) error {
	if parseGuard == nil {
		return nil
	}

	parseClientKey := buildHandlerClientKey(parseRequest)
	if parseClientKey == "" {
		parseClientKey = "unknown"
	}

	parseGuard.setGuardLock.Lock()
	defer parseGuard.setGuardLock.Unlock()

	if parseGuard.setConfig.MaxUpgradesPerClientPerMinute > 0 {
		parseWindow := parseGuard.storeClientUpgradeAttempts[parseClientKey]
		if parseWindow.getWindowStartedAt.IsZero() || parseNow.Sub(parseWindow.getWindowStartedAt) >= parseHandlerAbuseWindowDuration {
			parseWindow = storeHandlerAbuseRateWindow{
				getWindowStartedAt: parseNow,
				getWindowCount:     0,
			}
		}
		if parseWindow.getWindowCount >= parseGuard.setConfig.MaxUpgradesPerClientPerMinute {
			// The one refusal whose end is knowable: a fixed window reopens at a
			// computable instant, so the client is told rather than left to guess.
			return parseAbuseRefusal{
				setReason: fmt.Sprintf("upgrade rate exceeded for client %q", parseClientKey),
				setWait:   parseWindow.getWindowStartedAt.Add(parseHandlerAbuseWindowDuration).Sub(parseNow),
			}
		}
		parseWindow.getWindowCount++
		parseGuard.storeClientUpgradeAttempts[parseClientKey] = parseWindow
	}

	// No Retry-After on either connection cap, deliberately: a slot frees when
	// some other client disconnects, which is not a schedule anybody here can
	// compute. See pkg/bridge/retry_after.go.
	if parseGuard.setConfig.MaxActiveConnections > 0 && parseGuard.getActiveConnections >= parseGuard.setConfig.MaxActiveConnections {
		return fmt.Errorf("active connection cap exceeded")
	}

	parseClientConnections := parseGuard.storeClientConnections[parseClientKey]
	if parseGuard.setConfig.MaxConnectionsPerClient > 0 && parseClientConnections >= parseGuard.setConfig.MaxConnectionsPerClient {
		return fmt.Errorf("per-client connection cap exceeded for client %q", parseClientKey)
	}

	parseGuard.getActiveConnections++
	parseGuard.storeClientConnections[parseClientKey] = parseClientConnections + 1
	return nil
}

// clearHandlerConnection releases one reserved connection slot for abuse controls.
func (parseGuard *handlerAbuseGuard) clearHandlerConnection(parseRequest *http.Request) {
	if parseGuard == nil {
		return
	}

	parseClientKey := buildHandlerClientKey(parseRequest)
	if parseClientKey == "" {
		parseClientKey = "unknown"
	}

	parseGuard.setGuardLock.Lock()
	defer parseGuard.setGuardLock.Unlock()

	if parseGuard.getActiveConnections > 0 {
		parseGuard.getActiveConnections--
	}

	parseClientConnections := parseGuard.storeClientConnections[parseClientKey]
	if parseClientConnections <= 1 {
		delete(parseGuard.storeClientConnections, parseClientKey)
		return
	}
	parseGuard.storeClientConnections[parseClientKey] = parseClientConnections - 1
}

// buildHandlerClientKey derives a stable client key for abuse controls from request remote address.
func buildHandlerClientKey(parseRequest *http.Request) string {
	if parseRequest == nil {
		return ""
	}

	parseRemoteAddress := strings.TrimSpace(parseRequest.RemoteAddr)
	if parseRemoteAddress == "" {
		return ""
	}

	parseHost, _, parseErr := net.SplitHostPort(parseRemoteAddress)
	if parseErr == nil {
		return strings.TrimSpace(parseHost)
	}
	return parseRemoteAddress
}
