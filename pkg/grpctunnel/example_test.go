//go:build !js && !wasm

package grpctunnel_test

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ExampleWrap shows the middleware-style integration: wrap an existing
// grpc.Server and mount it anywhere an http.Handler fits.
func ExampleWrap() {
	grpcServer := grpc.NewServer()
	// proto.RegisterYourServiceServer(grpcServer, &yourImpl{})

	mux := http.NewServeMux()
	mux.Handle("/grpc", grpctunnel.Wrap(grpcServer))

	// log.Fatal(http.ListenAndServe(":8080", mux))
	_ = mux
}

// ExampleNewServer shows a shutdown-capable bridge server: the caller owns
// the http.Server and can stop it gracefully on SIGTERM.
func ExampleNewServer() {
	grpcServer := grpc.NewServer()

	srv := grpctunnel.NewServer(":8080", grpcServer,
		grpctunnel.WithAllowedOrigins("https://app.example.com"),
		grpctunnel.WithKeepalive(30*time.Second, 2*time.Minute),
	)
	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Println(err)
		}
	}()

	// ... on shutdown signal:
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// ExampleWithAuthorize shows pre-upgrade request authorization: failing
// requests are rejected with 403 before any tunnel resources are allocated.
func ExampleWithAuthorize() {
	grpcServer := grpc.NewServer()

	handler := grpctunnel.Wrap(grpcServer,
		grpctunnel.WithAuthorize(func(r *http.Request) error {
			if r.Header.Get("Authorization") == "" {
				return errors.New("missing bearer token")
			}
			return nil // validate the token for real deployments
		}),
	)
	_ = handler
}

// ExampleWithAllowedOrigins shows declarative origin allowlisting, including
// subdomain wildcards. Requests without an Origin header (non-browser
// clients) are allowed, matching browser-only origin-policy convention.
func ExampleWithAllowedOrigins() {
	grpcServer := grpc.NewServer()

	handler := grpctunnel.Wrap(grpcServer,
		grpctunnel.WithAllowedOrigins(
			"https://app.example.com",
			"https://*.staging.example.com",
		),
	)
	_ = handler
}

// ExampleWithTunnelKeepalive shows a resilient client: keepalive probing
// detects dead connections and reconnect backoff re-establishes the tunnel.
func ExampleWithTunnelKeepalive() {
	conn, err := grpctunnel.Dial("wss://api.example.com/grpc",
		grpctunnel.WithTunnelKeepalive(30*time.Second, 20*time.Second),
		grpctunnel.WithReconnectPolicy(grpctunnel.ReconnectConfig{
			MaxDelay: 30 * time.Second,
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()
}

// ExampleWithNativeGRPCTransport shows the cheapest serving path: sessions go
// through gRPC's own HTTP/2 transport (−47% memory per RPC). Upgrade-request
// headers are not forwarded into per-RPC metadata in this mode.
func ExampleWithNativeGRPCTransport() {
	grpcServer := grpc.NewServer() // no transport credentials in native mode

	handler := grpctunnel.Wrap(grpcServer,
		grpctunnel.WithNativeGRPCTransport(),
		grpctunnel.WithAllowedOrigins("https://app.example.com"),
	)
	_ = handler
}

// ExampleDial shows the client side: dial the bridge like any gRPC target.
// ws://, wss://, http://, https://, host:port, and :port all work.
func ExampleDial() {
	conn, err := grpctunnel.Dial("localhost:8080",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()

	// client := proto.NewYourServiceClient(conn)
}

// ExampleBuildTunnelConn shows the typed client configuration surface,
// including reconnect backoff tuning.
func ExampleBuildTunnelConn() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpctunnel.BuildTunnelConn(ctx, grpctunnel.TunnelConfig{
		Target: "wss://api.example.com/grpc",
		ReconnectConfig: &grpctunnel.ReconnectConfig{
			InitialDelay: 500 * time.Millisecond,
			MaxDelay:     30 * time.Second,
		},
		GRPCOptions: grpctunnel.ApplyTunnelInsecureCredentials(nil),
	})
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()
}
