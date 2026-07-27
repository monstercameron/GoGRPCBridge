//go:build !js && !wasm

package grpctunnel

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Telling a refused client WHEN to come back.
//
// A bare 429 is an instruction to guess. The client backs off on a schedule of
// its own invention, which is either too eager — hammering a door that stays
// shut for another forty seconds — or too patient, sitting out a window that
// reopened long ago. Neither is a decision the client is equipped to make: the
// server is the only party that knows when the cap lifts.
//
// # Only when the answer is actually known
//
// The upgrade-rate cap is a fixed window, so the moment it reopens is arithmetic
// and this says so exactly. The CONNECTION caps are not: a slot frees when some
// other client disconnects, which is not on a schedule and cannot be predicted
// from here. Those keep the bare 429, deliberately — a Retry-After invented for
// them would be a number with no meaning behind it, and a client that trusted it
// would be worse off than one that backed off on its own.

// RetryAfterError is an error that knows when the caller may return.
//
// Exported so an embedding server can honour it in its own refusal paths rather
// than reimplementing the header — this package is not the only place that can
// decide to say no.
type RetryAfterError interface {
	error
	// RetryAfter reports how long the caller should wait. Zero means the answer
	// is not known, which is different from "come back immediately".
	RetryAfter() time.Duration
}

// abuseRefusal is a refusal carrying the wait, when one is known.
type abuseRefusal struct {
	reason string
	wait   time.Duration
}

func (parseErr abuseRefusal) Error() string { return parseErr.reason }

// RetryAfter implements RetryAfterError.
func (parseErr abuseRefusal) RetryAfter() time.Duration { return parseErr.wait }

// setRetryAfter writes the header when the error knows the answer.
//
// Seconds, rounded UP and floored at one: `Retry-After: 0` invites an immediate
// retry into a door that is still shut, and a fractional second is not a value
// the header can carry. Rounding up means a client that obeys it is never early,
// which is the only rounding direction that reduces load rather than moving it.
func setRetryAfter(parseW http.ResponseWriter, parseErr error) {
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
