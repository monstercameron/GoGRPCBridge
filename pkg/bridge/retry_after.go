package bridge

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Telling a refused client WHEN to come back — the handler-side copy of the
// policy documented in pkg/grpctunnel/retry_after.go.
//
// A bare 429 is an instruction to guess, and the client is the party least
// equipped to guess: only the server knows when the cap lifts. The upgrade-rate
// cap is a fixed window, so its reopening is arithmetic and gets said exactly.
// The connection caps are not — a slot frees when somebody else disconnects,
// which is not a schedule — so they keep the bare 429 rather than carry an
// invented number.

// RetryAfterError is an error that knows when the caller may return.
type RetryAfterError interface {
	error
	// RetryAfter reports how long to wait. Zero means unknown, which is not the
	// same as "come back now".
	RetryAfter() time.Duration
}

// parseAbuseRefusal is a refusal carrying the wait, when one is known.
type parseAbuseRefusal struct {
	setReason string
	setWait   time.Duration
}

func (parseErr parseAbuseRefusal) Error() string { return parseErr.setReason }

// RetryAfter implements RetryAfterError.
func (parseErr parseAbuseRefusal) RetryAfter() time.Duration { return parseErr.setWait }

// setRetryAfterHeader writes the header when the error knows the answer.
//
// Seconds, rounded UP and floored at one: the header carries no fractions, and
// rounding down would send a client back before the door opens — which moves
// load rather than reducing it.
func setRetryAfterHeader(parseW http.ResponseWriter, parseErr error) {
	var parseRetryable RetryAfterError
	if !errors.As(parseErr, &parseRetryable) {
		return
	}
	parseWait := parseRetryable.RetryAfter()
	if parseWait <= 0 {
		return
	}
	parseSeconds := int(math.Ceil(parseWait.Seconds()))
	if parseSeconds < 1 {
		parseSeconds = 1
	}
	parseW.Header().Set("Retry-After", strconv.Itoa(parseSeconds))
}
