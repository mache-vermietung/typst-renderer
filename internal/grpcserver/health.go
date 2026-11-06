package grpcserver

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// ErrNotServing is returned by CheckHealth when the server answers but does not report SERVING.
var ErrNotServing = errors.New("grpc service is not serving")

// CheckHealth calls the standard grpc.health.v1 Check RPC for the whole server (empty service
// name) on conn and returns nil only for SERVING.
func CheckHealth(ctx context.Context, conn grpc.ClientConnInterface) error {
	resp, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("grpc health check: %w", err)
	}

	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("%w: %s", ErrNotServing, resp.GetStatus())
	}

	return nil
}

// ProbeHealth dials addr without TLS (cluster-internal) and runs CheckHealth. It is meant for
// container healthchecks of distroless images that have no shell or grpc_health_probe binary.
func ProbeHealth(ctx context.Context, addr string) error {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("grpc health probe dial %q: %w", addr, err)
	}

	defer func() { _ = conn.Close() }()

	return CheckHealth(ctx, conn)
}
