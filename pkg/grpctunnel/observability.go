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

const bridgeObservabilityScope = "github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
const bridgeRequestSpanName = "grpctunnel.bridge.request"
const bridgeSessionSpanName = "grpctunnel.bridge.session"

const bridgeConnectionsActiveMetric = "bridge_connections_active"
const bridgeConnectionsTotalMetric = "bridge_connections_total"
const bridgeUpgradeFailuresTotalMetric = "bridge_upgrade_failures_total"
const bridgeUpgradeLatencyMetric = "bridge_request_latency_ms"

const bridgeMetricResultSuccess = "success"
const bridgeMetricResultFailure = "failure"

// bridgeObservability stores OTel tracer and metrics handles for bridge runtime signals.
type bridgeObservability struct {
	tracer               trace.Tracer
	connectionsActive    metric.Int64UpDownCounter
	connectionsTotal     metric.Int64Counter
	upgradeFailuresTotal metric.Int64Counter
	upgradeLatencyMS     metric.Float64Histogram
}

// buildBridgeObservability creates a bridge observability handle backed by the global OTel providers.
func buildBridgeObservability() *bridgeObservability {
	meter := otel.Meter(bridgeObservabilityScope)
	tracer := otel.Tracer(bridgeObservabilityScope)

	connectionsActive, _ := meter.Int64UpDownCounter(
		bridgeConnectionsActiveMetric,
		metric.WithDescription("Current active websocket tunnel connections"),
	)
	connectionsTotal, _ := meter.Int64Counter(
		bridgeConnectionsTotalMetric,
		metric.WithDescription("Total accepted websocket tunnel connections"),
	)
	upgradeFailuresTotal, _ := meter.Int64Counter(
		bridgeUpgradeFailuresTotalMetric,
		metric.WithDescription("Total websocket upgrade failures"),
	)
	upgradeLatencyMS, _ := meter.Float64Histogram(
		bridgeUpgradeLatencyMetric,
		metric.WithUnit("ms"),
		metric.WithDescription("Websocket upgrade request latency in milliseconds"),
	)

	return &bridgeObservability{
		tracer:               tracer,
		connectionsActive:    connectionsActive,
		connectionsTotal:     connectionsTotal,
		upgradeFailuresTotal: upgradeFailuresTotal,
		upgradeLatencyMS:     upgradeLatencyMS,
	}
}

// getBridgeMetricContext returns a non-nil context for OTel metric operations.
func getBridgeMetricContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// buildBridgeMetricAttributes builds stable metric attributes from an HTTP request and result state.
func buildBridgeMetricAttributes(r *http.Request, result string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("component", "grpctunnel.bridge"),
	}
	if result != "" {
		attrs = append(attrs, attribute.String("result", result))
	}
	if r == nil {
		return attrs
	}
	method := strings.TrimSpace(r.Method)
	if method != "" {
		attrs = append(attrs, attribute.String("method", method))
	}
	if r.URL != nil {
		path := strings.TrimSpace(r.URL.Path)
		if path != "" {
			attrs = append(attrs, attribute.String("path", path))
		}
	}
	return attrs
}

// storeBridgeUpgradeResult records websocket upgrade latency with result labels.
func (o *bridgeObservability) storeBridgeUpgradeResult(ctx context.Context, duration time.Duration, r *http.Request, result string) {
	if o == nil || o.upgradeLatencyMS == nil {
		return
	}
	attrs := buildBridgeMetricAttributes(r, result)
	o.upgradeLatencyMS.Record(
		getBridgeMetricContext(ctx),
		float64(duration)/float64(time.Millisecond),
		metric.WithAttributes(attrs...),
	)
}

// storeBridgeUpgradeFailure records a websocket upgrade failure counter increment and latency sample.
func (o *bridgeObservability) storeBridgeUpgradeFailure(ctx context.Context, duration time.Duration, r *http.Request) {
	o.storeBridgeUpgradeResult(ctx, duration, r, bridgeMetricResultFailure)
	if o == nil || o.upgradeFailuresTotal == nil {
		return
	}
	attrs := buildBridgeMetricAttributes(r, bridgeMetricResultFailure)
	o.upgradeFailuresTotal.Add(
		getBridgeMetricContext(ctx),
		1,
		metric.WithAttributes(attrs...),
	)
}

// storeBridgeUpgradeSuccess records a websocket upgrade success latency sample.
func (o *bridgeObservability) storeBridgeUpgradeSuccess(ctx context.Context, duration time.Duration, r *http.Request) {
	o.storeBridgeUpgradeResult(ctx, duration, r, bridgeMetricResultSuccess)
}

// storeBridgeConnectionDelta updates active and total connection metrics from connect/disconnect events.
func (o *bridgeObservability) storeBridgeConnectionDelta(ctx context.Context, r *http.Request, delta int64) {
	if o == nil {
		return
	}
	attrs := buildBridgeMetricAttributes(r, "")
	if o.connectionsActive != nil {
		o.connectionsActive.Add(
			getBridgeMetricContext(ctx),
			delta,
			metric.WithAttributes(attrs...),
		)
	}
	if delta > 0 && o.connectionsTotal != nil {
		o.connectionsTotal.Add(
			getBridgeMetricContext(ctx),
			delta,
			metric.WithAttributes(attrs...),
		)
	}
}

// startBridgeRequestSpan starts the server span used for one websocket upgrade request.
func (o *bridgeObservability) startBridgeRequestSpan(ctx context.Context, r *http.Request) (context.Context, trace.Span) {
	ctx = getBridgeMetricContext(ctx)
	if o == nil || o.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	attrs := buildBridgeMetricAttributes(r, "")
	return o.tracer.Start(
		ctx,
		bridgeRequestSpanName,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)
}

// startBridgeSessionSpan starts the session span for a tunneled websocket lifecycle.
func (o *bridgeObservability) startBridgeSessionSpan(ctx context.Context, r *http.Request) (context.Context, trace.Span) {
	ctx = getBridgeMetricContext(ctx)
	if o == nil || o.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	attrs := buildBridgeMetricAttributes(r, "")
	return o.tracer.Start(
		ctx,
		bridgeSessionSpanName,
		trace.WithAttributes(attrs...),
	)
}
