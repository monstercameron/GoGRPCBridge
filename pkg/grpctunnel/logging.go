//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

const defaultGrpctunnelLogLevel = "INFO"
const defaultGrpctunnelLogEvent = "grpctunnel_event"
const defaultGrpctunnelLogComponent = "grpctunnel"

// logGrpctunnelEvent emits one structured grpctunnel log event line.
func logGrpctunnelEvent(component string, level string, event string, r *http.Request, err error, msg string) {
	log.Printf("%s", buildGrpctunnelLogLine(component, level, event, r, err, msg))
}

// buildGrpctunnelLogLine builds a structured grpctunnel log line with optional request and OTel context fields.
func buildGrpctunnelLogLine(component string, level string, event string, r *http.Request, err error, msg string) string {
	component = strings.TrimSpace(component)
	if component == "" {
		component = defaultGrpctunnelLogComponent
	}
	level = strings.TrimSpace(level)
	if level == "" {
		level = defaultGrpctunnelLogLevel
	}
	event = strings.TrimSpace(event)
	if event == "" {
		event = defaultGrpctunnelLogEvent
	}

	b := strings.Builder{}
	appendGrpctunnelLogField(&b, "component", component)
	appendGrpctunnelLogField(&b, "level", level)
	appendGrpctunnelLogField(&b, "event", event)
	appendGrpctunnelLogField(&b, "msg", msg)

	if r != nil {
		appendGrpctunnelLogField(&b, "request_id", getGrpctunnelRequestID(r))
		appendGrpctunnelLogField(&b, "remote_addr", strings.TrimSpace(r.RemoteAddr))
		appendGrpctunnelLogField(&b, "origin", strings.TrimSpace(r.Header.Get("Origin")))
		appendGrpctunnelLogField(&b, "method", strings.TrimSpace(r.Method))
		if r.URL != nil {
			appendGrpctunnelLogField(&b, "path", strings.TrimSpace(r.URL.Path))
		}

		traceID, spanID := getGrpctunnelTraceSpanIDs(r.Context())
		appendGrpctunnelLogField(&b, "trace_id", traceID)
		appendGrpctunnelLogField(&b, "span_id", spanID)
	}

	if err != nil {
		appendGrpctunnelLogField(&b, "error", strings.TrimSpace(err.Error()))
	}

	return b.String()
}

// appendGrpctunnelLogField appends one key/value field to a structured grpctunnel log line.
func appendGrpctunnelLogField(b *strings.Builder, key string, value string) {
	if b == nil {
		return
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(strconv.Quote(value))
}

// getGrpctunnelRequestID resolves a correlation/request identifier from common ingress headers.
func getGrpctunnelRequestID(r *http.Request) string {
	if r == nil {
		return ""
	}

	requestIDHeaders := []string{
		"X-Request-Id",
		"X-Request-ID",
		"X-Correlation-Id",
		"X-Correlation-ID",
	}
	for _, headerName := range requestIDHeaders {
		headerValue := strings.TrimSpace(r.Header.Get(headerName))
		if headerValue != "" {
			return headerValue
		}
	}
	return ""
}

// getGrpctunnelTraceSpanIDs returns OTel trace/span identifiers from context when available.
func getGrpctunnelTraceSpanIDs(ctx context.Context) (string, string) {
	if ctx == nil {
		return "", ""
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return "", ""
	}
	return spanContext.TraceID().String(), spanContext.SpanID().String()
}
