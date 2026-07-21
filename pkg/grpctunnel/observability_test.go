//go:build !js && !wasm

package grpctunnel

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
)

// TestBuildBridgeHandler_RecordsUpgradeFailureMetrics verifies bridge observability records OTel metrics for upgrade failures.
func TestBuildBridgeHandler_RecordsUpgradeFailureMetrics(parseT *testing.T) {
	parseReader := sdkmetric.NewManualReader()
	parseMeterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(parseReader))
	parseOriginalMeterProvider := otel.GetMeterProvider()
	otel.SetMeterProvider(parseMeterProvider)
	defer otel.SetMeterProvider(parseOriginalMeterProvider)
	defer func() {
		_ = parseMeterProvider.Shutdown(context.Background())
	}()

	parseGrpcServer := grpc.NewServer()
	defer parseGrpcServer.Stop()

	parseHandler, parseErr := BuildBridgeHandler(parseGrpcServer, BridgeConfig{})
	if parseErr != nil {
		parseT.Fatalf("BuildBridgeHandler() error: %v", parseErr)
	}

	parseReq := httptest.NewRequest(http.MethodGet, "/grpc", nil)
	parseW := httptest.NewRecorder()
	parseHandler.ServeHTTP(parseW, parseReq)

	parseResourceMetrics := metricdata.ResourceMetrics{}
	if parseCollectErr := parseReader.Collect(context.Background(), &parseResourceMetrics); parseCollectErr != nil {
		parseT.Fatalf("Collect() error: %v", parseCollectErr)
	}

	parseFailureCount, hasFailureCount := getBridgeInt64SumMetricValue(parseResourceMetrics, bridgeUpgradeFailuresTotalMetric)
	if !hasFailureCount {
		parseT.Fatalf("missing metric %q", bridgeUpgradeFailuresTotalMetric)
	}
	if parseFailureCount < 1 {
		parseT.Fatalf("%s = %d, want >= 1", bridgeUpgradeFailuresTotalMetric, parseFailureCount)
	}

	parseLatencyCount, hasLatencyCount := getBridgeFloat64HistogramCount(parseResourceMetrics, bridgeUpgradeLatencyMetric)
	if !hasLatencyCount {
		parseT.Fatalf("missing metric %q", bridgeUpgradeLatencyMetric)
	}
	if parseLatencyCount < 1 {
		parseT.Fatalf("%s count = %d, want >= 1", bridgeUpgradeLatencyMetric, parseLatencyCount)
	}
}

// TestBuildBridgeHandler_LogsUpgradeFailureWithTraceIDsWhenTracerConfigured verifies bridge logs include trace IDs when a tracer provider is configured.
func TestBuildBridgeHandler_LogsUpgradeFailureWithTraceIDsWhenTracerConfigured(parseT *testing.T) {
	parseTracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	parseOriginalTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(parseTracerProvider)
	defer otel.SetTracerProvider(parseOriginalTracerProvider)
	defer func() {
		_ = parseTracerProvider.Shutdown(context.Background())
	}()

	parseGrpcServer := grpc.NewServer()
	defer parseGrpcServer.Stop()

	parseHandler, parseErr := BuildBridgeHandler(parseGrpcServer, BridgeConfig{})
	if parseErr != nil {
		parseT.Fatalf("BuildBridgeHandler() error: %v", parseErr)
	}

	var parseLogBuffer bytes.Buffer
	parseOriginalWriter := log.Writer()
	log.SetOutput(&parseLogBuffer)
	defer log.SetOutput(parseOriginalWriter)

	parseReq := httptest.NewRequest(http.MethodGet, "/grpc", nil)
	parseW := httptest.NewRecorder()
	parseHandler.ServeHTTP(parseW, parseReq)

	parseLogOutput := parseLogBuffer.String()
	if !strings.Contains(parseLogOutput, `event="ws_upgrade_failed"`) {
		parseT.Fatalf("expected ws_upgrade_failed log, got %q", parseLogOutput)
	}
	if !strings.Contains(parseLogOutput, `trace_id="`) {
		parseT.Fatalf("expected trace_id in log output, got %q", parseLogOutput)
	}
	if !strings.Contains(parseLogOutput, `span_id="`) {
		parseT.Fatalf("expected span_id in log output, got %q", parseLogOutput)
	}
}

// getBridgeInt64SumMetricValue returns the summed value for one int64 sum metric name.
func getBridgeInt64SumMetricValue(parseResourceMetrics metricdata.ResourceMetrics, parseMetricName string) (int64, bool) {
	for _, parseScopeMetrics := range parseResourceMetrics.ScopeMetrics {
		for _, parseMetric := range parseScopeMetrics.Metrics {
			if parseMetric.Name != parseMetricName {
				continue
			}
			parseSum, parseOK := parseMetric.Data.(metricdata.Sum[int64])
			if !parseOK {
				return 0, false
			}
			var parseTotal int64
			for _, parseDataPoint := range parseSum.DataPoints {
				parseTotal += parseDataPoint.Value
			}
			return parseTotal, true
		}
	}
	return 0, false
}

// getBridgeFloat64HistogramCount returns the datapoint count total for one float64 histogram metric name.
func getBridgeFloat64HistogramCount(parseResourceMetrics metricdata.ResourceMetrics, parseMetricName string) (uint64, bool) {
	for _, parseScopeMetrics := range parseResourceMetrics.ScopeMetrics {
		for _, parseMetric := range parseScopeMetrics.Metrics {
			if parseMetric.Name != parseMetricName {
				continue
			}
			parseHistogram, parseOK := parseMetric.Data.(metricdata.Histogram[float64])
			if !parseOK {
				return 0, false
			}
			var parseTotal uint64
			for _, parseDataPoint := range parseHistogram.DataPoints {
				parseTotal += parseDataPoint.Count
			}
			return parseTotal, true
		}
	}
	return 0, false
}
