package renderer

import (
	"context"
	"net"
	"time"

	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/mache-vermietung/typst-renderer/internal/grpcserver"
	rendererv1 "github.com/mache-vermietung/typst-renderer/proto/renderer/v1"
)

// HealthProbeCommand is the argument that turns the renderer binary into a one-shot health
// probe (`/bin/renderer healthcheck`), used by Docker healthchecks of the distroless image.
const HealthProbeCommand = "healthcheck"

// healthProbeTimeout bounds the container health probe.
const healthProbeTimeout = 2 * time.Second

// NewGRPCServer builds the daemon's gRPC server: message limits, the RendererService and the
// standard grpc.health.v1 health service, which reports SERVING for the whole server ("") and
// for the RendererService. The API (/readyz) and container healthchecks use the same mechanism.
func NewGRPCServer(cfg Config, srv *Server) *grpcserver.Server {
	grpcSrv := grpcserver.New(cfg.ListenAddr, GRPCServerOptions(cfg)...)
	rendererv1.RegisterRendererServiceServer(grpcSrv.GRPCServer(), srv)
	grpcSrv.SetServingStatus(rendererv1.RendererService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)

	return grpcSrv
}

// ProbeAddr derives the loopback address the container health probe dials from the listen
// address (":9100" → "127.0.0.1:9100").
func ProbeAddr(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return listenAddr
	}

	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	return net.JoinHostPort(host, port)
}

// RunHealthProbe checks the renderer listening on cfg.ListenAddr via grpc.health.v1.
func RunHealthProbe(ctx context.Context, cfg Config) error {
	ctx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()

	return grpcserver.ProbeHealth(ctx, ProbeAddr(cfg.ListenAddr))
}
