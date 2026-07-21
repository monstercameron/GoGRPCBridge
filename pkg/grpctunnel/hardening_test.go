//go:build !js && !wasm

package grpctunnel

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monstercameron/GoGRPCBridge/examples/_shared/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// buildHardeningTestBackend returns a registered gRPC server for handler tests.
func buildHardeningTestBackend(t *testing.T) *grpc.Server {
	t.Helper()
	grpcServer := grpc.NewServer()
	proto.RegisterTodoServiceServer(grpcServer, &mockService{})
	t.Cleanup(grpcServer.Stop)
	return grpcServer
}

// dialHardeningTestTunnel dials the bridge and performs one unary RPC.
func dialHardeningTestTunnel(t *testing.T, wsURL string, opts ...interface{}) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dialOpts := append([]interface{}{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, opts...)
	conn, err := DialContext(ctx, wsURL, dialOpts...)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := proto.NewTodoServiceClient(conn)
	_, err = client.CreateTodo(ctx, &proto.CreateTodoRequest{Text: "hardening"})
	return err
}

// TestWithAuthorize_RejectsAndAllows verifies the pre-upgrade authorization hook.
func TestWithAuthorize_RejectsAndAllows(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)

	handler := Wrap(grpcServer,
		WithOriginCheck(func(*http.Request) bool { return true }),
		WithAuthorize(func(r *http.Request) error {
			if r.Header.Get("X-Bridge-Token") != "letmein" {
				return fmt.Errorf("missing or invalid token")
			}
			return nil
		}),
	)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsURL := "ws" + server.URL[4:]

	if err := dialHardeningTestTunnel(t, wsURL); err == nil {
		t.Fatal("expected unauthorized dial to fail, got success")
	}

	if err := dialHardeningTestTunnel(t, wsURL, WithHeader("X-Bridge-Token", "letmein")); err != nil {
		t.Fatalf("authorized dial failed: %v", err)
	}
}

// TestWithAuthorize_RespondsForbidden verifies the raw HTTP status for rejected upgrades.
func TestWithAuthorize_RespondsForbidden(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)

	handler := Wrap(grpcServer, WithAuthorize(func(*http.Request) error {
		return fmt.Errorf("nope")
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/grpc", nil)
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

// TestBuildOriginAllowlistCheck covers exact, wildcard, and missing-origin matching.
func TestBuildOriginAllowlistCheck(t *testing.T) {
	tests := []struct {
		name          string
		allowed       []string
		requestOrigin string
		want          bool
	}{
		{"exact match", []string{"https://app.example.com"}, "https://app.example.com", true},
		{"exact match case-insensitive", []string{"https://App.Example.com"}, "https://app.example.com", true},
		{"trailing slash normalized", []string{"https://app.example.com/"}, "https://app.example.com", true},
		{"mismatch", []string{"https://app.example.com"}, "https://evil.example.net", false},
		{"scheme mismatch", []string{"https://app.example.com"}, "http://app.example.com", false},
		{"port mismatch", []string{"https://app.example.com:8443"}, "https://app.example.com", false},
		{"star allows all", []string{"*"}, "https://anything.example.com", true},
		{"subdomain wildcard match", []string{"https://*.example.com"}, "https://api.example.com", true},
		{"subdomain wildcard scheme mismatch", []string{"https://*.example.com"}, "http://api.example.com", false},
		{"subdomain wildcard other domain", []string{"https://*.example.com"}, "https://api.evil.net", false},
		{"missing origin allowed", []string{"https://app.example.com"}, "", true},
		{"empty allowlist rejects browsers", nil, "https://app.example.com", false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			check := BuildOriginAllowlistCheck(testCase.allowed...)
			request := httptest.NewRequest(http.MethodGet, "/grpc", nil)
			if testCase.requestOrigin != "" {
				request.Header.Set("Origin", testCase.requestOrigin)
			}
			if got := check(request); got != testCase.want {
				t.Fatalf("check(origin=%q, allowed=%v) = %v, want %v",
					testCase.requestOrigin, testCase.allowed, got, testCase.want)
			}
		})
	}
}

// TestWithAllowedOrigins_EndToEnd verifies the allowlist rejects and admits real upgrades.
func TestWithAllowedOrigins_EndToEnd(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)

	handler := Wrap(grpcServer, WithAllowedOrigins("https://app.example.com"))
	server := httptest.NewServer(handler)
	defer server.Close()
	wsURL := "ws" + server.URL[4:]

	if err := dialHardeningTestTunnel(t, wsURL, WithHeader("Origin", "https://evil.example.net")); err == nil {
		t.Fatal("expected disallowed origin dial to fail, got success")
	}
	if err := dialHardeningTestTunnel(t, wsURL, WithHeader("Origin", "https://app.example.com")); err != nil {
		t.Fatalf("allowed origin dial failed: %v", err)
	}
	// Non-browser clients without an Origin header pass the allowlist.
	if err := dialHardeningTestTunnel(t, wsURL); err != nil {
		t.Fatalf("origin-less dial failed: %v", err)
	}
}

// TestAbuseGuard_SweepsExpiredUpgradeWindows verifies stale rate windows are pruned.
func TestAbuseGuard_SweepsExpiredUpgradeWindows(t *testing.T) {
	guard := buildBridgeAbuseGuard(BridgeConfig{MaxUpgradesPerClientPerMinute: 10})
	start := time.Now()

	for clientIndex := 0; clientIndex < 100; clientIndex++ {
		request := httptest.NewRequest(http.MethodGet, "/grpc", nil)
		request.RemoteAddr = fmt.Sprintf("10.0.0.%d:1234", clientIndex)
		if err := guard.reserveBridgeConnection(request, start); err != nil {
			t.Fatalf("reserve %d failed: %v", clientIndex, err)
		}
		guard.clearBridgeConnection(request)
	}
	if got := len(guard.storeClientUpgradeAttempts); got != 100 {
		t.Fatalf("window map size = %d, want 100 before sweep", got)
	}

	lateRequest := httptest.NewRequest(http.MethodGet, "/grpc", nil)
	lateRequest.RemoteAddr = "10.0.1.1:1234"
	if err := guard.reserveBridgeConnection(lateRequest, start.Add(2*bridgeAbuseWindowDuration)); err != nil {
		t.Fatalf("late reserve failed: %v", err)
	}
	guard.clearBridgeConnection(lateRequest)

	if got := len(guard.storeClientUpgradeAttempts); got != 1 {
		t.Fatalf("window map size after sweep = %d, want 1", got)
	}
}

// TestNewServer_GracefulShutdown verifies callers can shut the bridge server down.
func TestNewServer_GracefulShutdown(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)

	server := NewServer("127.0.0.1:0", grpcServer, WithOriginCheck(func(*http.Request) bool { return true }))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()

	wsURL := "ws://" + listener.Addr().String()
	if err := dialHardeningTestTunnel(t, wsURL); err != nil {
		t.Fatalf("dial before shutdown failed: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	select {
	case serveErr := <-serveDone:
		if serveErr != http.ErrServerClosed {
			t.Fatalf("Serve returned %v, want %v", serveErr, http.ErrServerClosed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}
}

// TestServerOptions_AbuseControlSetters verifies abuse-control options reach the resolved config.
func TestServerOptions_AbuseControlSetters(t *testing.T) {
	options := buildServerOptions(
		WithMaxActiveConnections(11),
		WithMaxConnectionsPerClient(3),
		WithMaxUpgradesPerClientPerMinute(60),
	)
	if options.maxActiveConnections != 11 {
		t.Fatalf("maxActiveConnections = %d, want 11", options.maxActiveConnections)
	}
	if options.maxConnectionsPerClient != 3 {
		t.Fatalf("maxConnectionsPerClient = %d, want 3", options.maxConnectionsPerClient)
	}
	if options.maxUpgradesPerClient != 60 {
		t.Fatalf("maxUpgradesPerClient = %d, want 60", options.maxUpgradesPerClient)
	}
}

// TestWithMaxConnectionsPerClient_EndToEnd verifies the per-client cap rejects a second concurrent tunnel.
func TestWithMaxConnectionsPerClient_EndToEnd(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)

	handler := Wrap(grpcServer,
		WithOriginCheck(func(*http.Request) bool { return true }),
		WithMaxConnectionsPerClient(1),
	)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsURL := "ws" + server.URL[4:]

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstConn, err := DialContext(ctx, wsURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("first dial failed: %v", err)
	}
	defer firstConn.Close()
	// Hold the first tunnel open with a live RPC so its slot stays reserved.
	if _, err := proto.NewTodoServiceClient(firstConn).CreateTodo(ctx, &proto.CreateTodoRequest{Text: "hold"}); err != nil {
		t.Fatalf("first RPC failed: %v", err)
	}

	if err := dialHardeningTestTunnel(t, wsURL); err == nil {
		t.Fatal("expected second concurrent tunnel from same client to be rejected")
	}
}

// TestListenAndServeTLS_InvalidCerts verifies TLS startup surfaces certificate errors.
func TestListenAndServeTLS_InvalidCerts(t *testing.T) {
	grpcServer := buildHardeningTestBackend(t)
	if err := ListenAndServeTLS("127.0.0.1:0", "missing-cert.pem", "missing-key.pem", grpcServer); err == nil {
		t.Fatal("expected certificate load error, got nil")
	}
}
