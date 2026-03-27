//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const parseBridgeObservabilityScope = "github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
const parseBridgeRequestSpanName = "grpctunnel.bridge.request"
const parseBridgeSessionSpanName = "grpctunnel.bridge.session"

const parseBridgeConnectionsActiveMetric = "bridge_connections_active"
const parseBridgeConnectionsTotalMetric = "bridge_connections_total"
const parseBridgeUpgradeFailuresTotalMetric = "bridge_upgrade_failures_total"
const parseBridgeUpgradeLatencyMetric = "bridge_request_latency_ms"

const parseBridgeMetricResultSuccess = "success"
const parseBridgeMetricResultFailure = "failure"

// bridgeObservability stores OTel tracer and metrics handles for bridge runtime signals.
type bridgeObservability struct {
	getBridgeTracer               trace.Tracer
	getBridgeConnectionsActive    metric.Int64UpDownCounter
	getBridgeConnectionsTotal     metric.Int64Counter
	getBridgeUpgradeFailuresTotal metric.Int64Counter
	getBridgeUpgradeLatencyMS     metric.Float64Histogram
}

// buildBridgeObservability creates a bridge observability handle backed by the global OTel providers.
func buildBridgeObservability() *bridgeObservability {
	parseMeter := otel.Meter(parseBridgeObservabilityScope)
	parseTracer := otel.Tracer(parseBridgeObservabilityScope)

	parseConnectionsActive, _ := parseMeter.Int64UpDownCounter(
		parseBridgeConnectionsActiveMetric,
		metric.WithDescription("Current active websocket tunnel connections"),
	)
	parseConnectionsTotal, _ := parseMeter.Int64Counter(
		parseBridgeConnectionsTotalMetric,
		metric.WithDescription("Total accepted websocket tunnel connections"),
	)
	parseUpgradeFailuresTotal, _ := parseMeter.Int64Counter(
		parseBridgeUpgradeFailuresTotalMetric,
		metric.WithDescription("Total websocket upgrade failures"),
	)
	parseUpgradeLatencyMS, _ := parseMeter.Float64Histogram(
		parseBridgeUpgradeLatencyMetric,
		metric.WithUnit("ms"),
		metric.WithDescription("Websocket upgrade request latency in milliseconds"),
	)

	return &bridgeObservability{
		getBridgeTracer:               parseTracer,
		getBridgeConnectionsActive:    parseConnectionsActive,
		getBridgeConnectionsTotal:     parseConnectionsTotal,
		getBridgeUpgradeFailuresTotal: parseUpgradeFailuresTotal,
		getBridgeUpgradeLatencyMS:     parseUpgradeLatencyMS,
	}
}

// getBridgeMetricContext returns a non-nil context for OTel metric operations.
func getBridgeMetricContext(parseContext context.Context) context.Context {
	if parseContext == nil {
		return context.Background()
	}
	return parseContext
}

// buildBridgeMetricAttributes builds stable metric attributes from an HTTP request and result state.
func buildBridgeMetricAttributes(parseRequest *http.Request, parseResult string) []attribute.KeyValue {
	parseAttributes := []attribute.KeyValue{
		attribute.String("component", "grpctunnel.bridge"),
	}
	if parseResult != "" {
		parseAttributes = append(parseAttributes, attribute.String("result", parseResult))
	}
	if parseRequest == nil {
		return parseAttributes
	}
	parseMethod := strings.TrimSpace(parseRequest.Method)
	if parseMethod != "" {
		parseAttributes = append(parseAttributes, attribute.String("method", parseMethod))
	}
	if parseRequest.URL != nil {
		parsePath := strings.TrimSpace(parseRequest.URL.Path)
		if parsePath != "" {
			parseAttributes = append(parseAttributes, attribute.String("path", parsePath))
		}
	}
	return parseAttributes
}

// storeBridgeUpgradeResult records websocket upgrade latency with result labels.
func (parseObservability *bridgeObservability) storeBridgeUpgradeResult(parseContext context.Context, parseDuration time.Duration, parseRequest *http.Request, parseResult string) {
	if parseObservability == nil || parseObservability.getBridgeUpgradeLatencyMS == nil {
		return
	}
	parseAttributes := buildBridgeMetricAttributes(parseRequest, parseResult)
	parseObservability.getBridgeUpgradeLatencyMS.Record(
		getBridgeMetricContext(parseContext),
		float64(parseDuration)/float64(time.Millisecond),
		metric.WithAttributes(parseAttributes...),
	)
}

// storeBridgeUpgradeFailure records a websocket upgrade failure counter increment and latency sample.
func (parseObservability *bridgeObservability) storeBridgeUpgradeFailure(parseContext context.Context, parseDuration time.Duration, parseRequest *http.Request) {
	parseObservability.storeBridgeUpgradeResult(parseContext, parseDuration, parseRequest, parseBridgeMetricResultFailure)
	if parseObservability == nil || parseObservability.getBridgeUpgradeFailuresTotal == nil {
		return
	}
	parseAttributes := buildBridgeMetricAttributes(parseRequest, parseBridgeMetricResultFailure)
	parseObservability.getBridgeUpgradeFailuresTotal.Add(
		getBridgeMetricContext(parseContext),
		1,
		metric.WithAttributes(parseAttributes...),
	)
}

// storeBridgeUpgradeSuccess records a websocket upgrade success latency sample.
func (parseObservability *bridgeObservability) storeBridgeUpgradeSuccess(parseContext context.Context, parseDuration time.Duration, parseRequest *http.Request) {
	parseObservability.storeBridgeUpgradeResult(parseContext, parseDuration, parseRequest, parseBridgeMetricResultSuccess)
}

// storeBridgeConnectionDelta updates active and total connection metrics from connect/disconnect events.
func (parseObservability *bridgeObservability) storeBridgeConnectionDelta(parseContext context.Context, parseRequest *http.Request, parseDelta int64) {
	if parseObservability == nil {
		return
	}
	parseAttributes := buildBridgeMetricAttributes(parseRequest, "")
	if parseObservability.getBridgeConnectionsActive != nil {
		parseObservability.getBridgeConnectionsActive.Add(
			getBridgeMetricContext(parseContext),
			parseDelta,
			metric.WithAttributes(parseAttributes...),
		)
	}
	if parseDelta > 0 && parseObservability.getBridgeConnectionsTotal != nil {
		parseObservability.getBridgeConnectionsTotal.Add(
			getBridgeMetricContext(parseContext),
			parseDelta,
			metric.WithAttributes(parseAttributes...),
		)
	}
}

// startBridgeRequestSpan starts the server span used for one websocket upgrade request.
func (parseObservability *bridgeObservability) startBridgeRequestSpan(parseContext context.Context, parseRequest *http.Request) (context.Context, trace.Span) {
	parseContext = getBridgeMetricContext(parseContext)
	if parseObservability == nil || parseObservability.getBridgeTracer == nil {
		return parseContext, trace.SpanFromContext(parseContext)
	}
	parseAttributes := buildBridgeMetricAttributes(parseRequest, "")
	return parseObservability.getBridgeTracer.Start(
		parseContext,
		parseBridgeRequestSpanName,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(parseAttributes...),
	)
}

// startBridgeSessionSpan starts the session span for a tunneled websocket lifecycle.
func (parseObservability *bridgeObservability) startBridgeSessionSpan(parseContext context.Context, parseRequest *http.Request) (context.Context, trace.Span) {
	parseContext = getBridgeMetricContext(parseContext)
	if parseObservability == nil || parseObservability.getBridgeTracer == nil {
		return parseContext, trace.SpanFromContext(parseContext)
	}
	parseAttributes := buildBridgeMetricAttributes(parseRequest, "")
	return parseObservability.getBridgeTracer.Start(
		parseContext,
		parseBridgeSessionSpanName,
		trace.WithAttributes(parseAttributes...),
	)
}
