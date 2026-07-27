//go:build !js && !wasm

package grpctunnel

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// A refused client is told when to come back, when that is knowable.
//
// The window is fixed, so its reopening is arithmetic. Without the header the
// client picks a schedule unrelated to the one the server is enforcing — which
// is either a retry into a door that stays shut for another forty seconds, or a
// wait long past the moment it opened.
func TestUpgradeRateRefusalSaysWhenToComeBack(parseT *testing.T) {
	parseGuard := buildBridgeAbuseGuard(BridgeConfig{MaxUpgradesPerClientPerMinute: 2})
	parseRequest := httptest.NewRequest(http.MethodGet, "/grpc", nil)
	parseRequest.RemoteAddr = "203.0.113.7:5555"

	parseStart := time.Now()
	for parseI := range 2 {
		if parseErr := parseGuard.reserveBridgeConnection(parseRequest, parseStart); parseErr != nil {
			parseT.Fatalf("attempt %d was refused inside the cap: %v", parseI+1, parseErr)
		}
	}

	// Twenty seconds into the window, so the answer is a specific forty rather
	// than the whole window length — a test that passed for either would not be
	// checking that the remaining time is what is reported.
	parseAt := parseStart.Add(20 * time.Second)
	parseErr := parseGuard.reserveBridgeConnection(parseRequest, parseAt)
	if parseErr == nil {
		parseT.Fatal("the third upgrade inside one minute was allowed")
	}

	parseRecorder := httptest.NewRecorder()
	setRetryAfter(parseRecorder, parseErr)
	parseHeader := parseRecorder.Header().Get("Retry-After")
	if parseHeader == "" {
		parseT.Fatal("no Retry-After on a refusal whose end is computable")
	}
	parseSeconds, parseConvErr := strconv.Atoi(parseHeader)
	if parseConvErr != nil {
		parseT.Fatalf("Retry-After = %q, which is not delay-seconds", parseHeader)
	}
	if parseSeconds != 40 {
		parseT.Errorf("Retry-After = %d, want 40 — the window opened %v ago and lasts %v",
			parseSeconds, parseAt.Sub(parseStart), bridgeAbuseWindowDuration)
	}
}

// And obeying it works: a client that waits exactly as long as it was told gets
// in. A header that undershoots would send everybody back into the same wall.
func TestWaitingTheAdvertisedTimeGetsYouIn(parseT *testing.T) {
	parseGuard := buildBridgeAbuseGuard(BridgeConfig{MaxUpgradesPerClientPerMinute: 1})
	parseRequest := httptest.NewRequest(http.MethodGet, "/grpc", nil)
	parseRequest.RemoteAddr = "203.0.113.9:5555"

	parseStart := time.Now()
	if parseErr := parseGuard.reserveBridgeConnection(parseRequest, parseStart); parseErr != nil {
		parseT.Fatalf("first upgrade refused: %v", parseErr)
	}
	parseGuard.clearBridgeConnection(parseRequest)

	parseErr := parseGuard.reserveBridgeConnection(parseRequest, parseStart.Add(time.Second))
	if parseErr == nil {
		parseT.Fatal("the second upgrade inside the window was allowed")
	}
	parseRetryable, parseOK := parseErr.(RetryAfterError)
	if !parseOK {
		parseT.Fatalf("refusal %T does not carry a retry time", parseErr)
	}

	parseWhen := parseStart.Add(time.Second).Add(parseRetryable.RetryAfter())
	if parseErr := parseGuard.reserveBridgeConnection(parseRequest, parseWhen); parseErr != nil {
		parseT.Errorf("a client that waited exactly as long as it was told was refused again: %v", parseErr)
	}
}

// The connection caps get NO header, and that is the decision rather than an
// omission: a slot frees when somebody else disconnects, which is not a
// schedule. A number invented for it would be worse than silence, because a
// client that trusted it would return at a time nothing was promised about.
func TestConnectionCapsCarryNoRetryAfter(parseT *testing.T) {
	parseCases := []struct {
		parseName   string
		parseConfig BridgeConfig
	}{
		{"active connections", BridgeConfig{MaxActiveConnections: 1}},
		{"per client", BridgeConfig{MaxConnectionsPerClient: 1}},
	}
	for _, parseCase := range parseCases {
		parseT.Run(parseCase.parseName, func(parseT *testing.T) {
			parseGuard := buildBridgeAbuseGuard(parseCase.parseConfig)
			parseRequest := httptest.NewRequest(http.MethodGet, "/grpc", nil)
			parseRequest.RemoteAddr = "203.0.113.11:5555"

			parseNow := time.Now()
			if parseErr := parseGuard.reserveBridgeConnection(parseRequest, parseNow); parseErr != nil {
				parseT.Fatalf("first connection refused: %v", parseErr)
			}
			parseErr := parseGuard.reserveBridgeConnection(parseRequest, parseNow)
			if parseErr == nil {
				parseT.Fatal("the second connection was allowed past the cap")
			}

			parseRecorder := httptest.NewRecorder()
			setRetryAfter(parseRecorder, parseErr)
			if parseGot := parseRecorder.Header().Get("Retry-After"); parseGot != "" {
				parseT.Errorf("Retry-After = %q on a refusal with no knowable end", parseGot)
			}
		})
	}
}

// A sub-second remainder still has to be a usable instruction. Rounding down
// produces `Retry-After: 0`, which invites an immediate retry into a door that
// is still shut.
func TestARemainderUnderASecondRoundsUpToOne(parseT *testing.T) {
	parseRecorder := httptest.NewRecorder()
	setRetryAfter(parseRecorder, abuseRefusal{reason: "x", wait: 200 * time.Millisecond})
	if parseGot := parseRecorder.Header().Get("Retry-After"); parseGot != "1" {
		parseT.Errorf("Retry-After = %q, want 1", parseGot)
	}
}

// An ordinary error carries nothing, so unrelated refusals are unaffected.
func TestAnErrorWithNoDeadlineSetsNoHeader(parseT *testing.T) {
	parseRecorder := httptest.NewRecorder()
	setRetryAfter(parseRecorder, http.ErrHandlerTimeout)
	if parseGot := parseRecorder.Header().Get("Retry-After"); parseGot != "" {
		parseT.Errorf("Retry-After = %q on an error that knows nothing about time", parseGot)
	}
}
