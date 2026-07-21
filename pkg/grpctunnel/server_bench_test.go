//go:build !js && !wasm

package grpctunnel

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// buildForwardBenchHeaders returns the forward-header set a typical bridge
// session produces (request id, correlation id, and trace context).
func buildForwardBenchHeaders() http.Header {
	forward := make(http.Header)
	forward.Set(bridgeRequestIDMetadataKey, "req-12345")
	forward.Set(bridgeCorrelationIDMetadataKey, "corr-12345")
	forward.Set(bridgeTraceParentMetadataKey, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	return forward
}

// BenchmarkForwardMetadataHandler_Inject measures the common case: tunneled
// requests carry none of the forwarded headers, so all must be injected.
func BenchmarkForwardMetadataHandler_Inject(b *testing.B) {
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	handler := wrapBridgeForwardMetadataHandler(inner, buildForwardBenchHeaders())
	request := httptest.NewRequest(http.MethodPost, "/todo.TodoService/CreateTodo", nil)
	request.Header.Set("Content-Type", "application/grpc")
	recorder := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(recorder, request)
	}
}

// BenchmarkForwardMetadataHandler_AllPresent measures the skip case: the
// tunneled request already carries every forwarded header.
func BenchmarkForwardMetadataHandler_AllPresent(b *testing.B) {
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	handler := wrapBridgeForwardMetadataHandler(inner, buildForwardBenchHeaders())
	request := httptest.NewRequest(http.MethodPost, "/todo.TodoService/CreateTodo", nil)
	request.Header.Set("Content-Type", "application/grpc")
	for key, values := range buildForwardBenchHeaders() {
		request.Header[key] = values
	}
	recorder := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(recorder, request)
	}
}

// BenchmarkForwardMetadataHandler_NoForwardHeaders measures sessions that
// produced no forwardable headers at all.
func BenchmarkForwardMetadataHandler_NoForwardHeaders(b *testing.B) {
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	handler := wrapBridgeForwardMetadataHandler(inner, make(http.Header))
	request := httptest.NewRequest(http.MethodPost, "/todo.TodoService/CreateTodo", nil)
	recorder := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(recorder, request)
	}
}
