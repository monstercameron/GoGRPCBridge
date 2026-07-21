//go:build !js && !wasm

package grpctunnel

import (
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// GetToolingConfigError validates additive tooling helper configuration.
func GetToolingConfigError(cfg ToolingConfig) error {
	if cfg.DebugPathPrefix != "" {
		if !strings.HasPrefix(cfg.DebugPathPrefix, "/") {
			return fmt.Errorf("grpctunnel: DebugPathPrefix must start with /")
		}
		if !strings.HasSuffix(cfg.DebugPathPrefix, "/") {
			return fmt.Errorf("grpctunnel: DebugPathPrefix must end with /")
		}
	}
	return nil
}

// BuildToolingHandler builds an optional direct gRPC tooling handler for grpcurl, grpcui, and pprof.
func BuildToolingHandler(grpcServer *grpc.Server, cfg ToolingConfig) (http.Handler, *health.Server, error) {
	if grpcServer == nil {
		return nil, nil, fmt.Errorf("grpctunnel: grpc server is required")
	}
	if err := GetToolingConfigError(cfg); err != nil {
		return nil, nil, err
	}

	healthServer := ensureToolingServices(grpcServer, cfg)
	warnToolingExposure(cfg)
	mux := http.NewServeMux()
	if cfg.ShouldEnablePprof {
		registerToolingPprofHandlers(mux, cfg)
	}
	mux.Handle("/", h2c.NewHandler(grpcServer, &http2.Server{}))
	return mux, healthServer, nil
}

// warnToolingExposure logs security-sensitive tooling exposure warnings.
func warnToolingExposure(cfg ToolingConfig) {
	logGrpctunnelEvent("grpctunnel.tooling", "WARN", "tooling_exposure", nil, nil, "Tooling handler exposes direct gRPC access; bind tooling endpoints to trusted networks only")
	if cfg.ShouldEnableReflection {
		logGrpctunnelEvent("grpctunnel.tooling", "WARN", "tooling_reflection_enabled", nil, nil, "gRPC reflection is enabled on tooling handler")
	}
	if cfg.ShouldEnablePprof {
		logGrpctunnelEvent("grpctunnel.tooling", "WARN", "tooling_pprof_enabled", nil, nil, "pprof is enabled on tooling handler")
	}
}

// ListenAndServeTooling starts an additive direct gRPC tooling server on a separate address.
func ListenAndServeTooling(addr string, grpcServer *grpc.Server, cfg ToolingConfig) error {
	if err := getToolingListenAddressError(addr, cfg); err != nil {
		return err
	}
	if shouldWarnToolingNonLoopbackBind(addr, cfg) {
		logGrpctunnelEvent(
			"grpctunnel.tooling",
			"WARN",
			"tooling_bind_non_loopback",
			nil,
			nil,
			fmt.Sprintf(
				"Tooling server with reflection/pprof is bound to non-loopback address %q; restrict access with trusted network boundaries and auth controls",
				addr,
			),
		)
	}

	handler, _, err := BuildToolingHandler(grpcServer, cfg)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return server.ListenAndServe()
}

// ensureToolingServices registers optional reflection and health services when absent.
func ensureToolingServices(grpcServer *grpc.Server, cfg ToolingConfig) *health.Server {
	serviceInfo := grpcServer.GetServiceInfo()
	if cfg.ShouldEnableReflection && !hasToolingService(serviceInfo, "grpc.reflection.v1alpha.ServerReflection") {
		reflection.Register(grpcServer)
	}

	if cfg.ShouldEnableHealthService && !hasToolingService(serviceInfo, grpc_health_v1.Health_ServiceDesc.ServiceName) {
		healthServer := health.NewServer()
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
		grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
		return healthServer
	}

	return nil
}

// hasToolingService reports whether the gRPC server already exposes a named service.
func hasToolingService(services map[string]grpc.ServiceInfo, serviceName string) bool {
	_, hasService := services[serviceName]
	return hasService
}

// registerToolingPprofHandlers mounts pprof handlers under the configured path prefix.
func registerToolingPprofHandlers(mux *http.ServeMux, cfg ToolingConfig) {
	debugPathPrefix := cfg.DebugPathPrefix
	if debugPathPrefix == "" {
		debugPathPrefix = "/debug/pprof/"
	}

	debugPathBase := strings.TrimSuffix(debugPathPrefix, "/")
	mux.Handle(debugPathPrefix, http.HandlerFunc(pprof.Index))
	mux.Handle(debugPathBase+"/cmdline", http.HandlerFunc(pprof.Cmdline))
	mux.Handle(debugPathBase+"/profile", http.HandlerFunc(pprof.Profile))
	mux.Handle(debugPathBase+"/symbol", http.HandlerFunc(pprof.Symbol))
	mux.Handle(debugPathBase+"/trace", http.HandlerFunc(pprof.Trace))
}

// getToolingListenAddressError blocks wildcard tooling binds when reflection/pprof are enabled.
func getToolingListenAddressError(addr string, cfg ToolingConfig) error {
	if !cfg.ShouldEnableReflection && !cfg.ShouldEnablePprof {
		return nil
	}
	if !shouldWarnToolingWildcardBind(addr) {
		return nil
	}
	return fmt.Errorf(
		"grpctunnel: refusing tooling listen address %q with reflection/pprof enabled; bind to loopback (127.0.0.1 or ::1) or disable introspection features",
		addr,
	)
}

// shouldWarnToolingWildcardBind reports whether a listen address resolves to wildcard interfaces.
func shouldWarnToolingWildcardBind(addr string) bool {
	trimmedAddress := strings.TrimSpace(addr)
	if trimmedAddress == "" || strings.HasPrefix(trimmedAddress, ":") {
		return true
	}

	host, _, err := net.SplitHostPort(trimmedAddress)
	if err != nil {
		return false
	}
	host = strings.TrimSpace(host)
	return host == "" || host == "0.0.0.0" || host == "::" || host == "[::]"
}

// shouldWarnToolingNonLoopbackBind reports whether a tooling listen address is non-loopback.
func shouldWarnToolingNonLoopbackBind(addr string, cfg ToolingConfig) bool {
	if !cfg.ShouldEnableReflection && !cfg.ShouldEnablePprof {
		return false
	}
	if shouldWarnToolingWildcardBind(addr) {
		return false
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return false
	}

	hostIP := net.ParseIP(host)
	if hostIP != nil {
		return !hostIP.IsLoopback()
	}

	return host != ""
}
