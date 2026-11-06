package grpcserver_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/tobima/domio/internal/infrastructure/grpcserver"
)

func startServer(t *testing.T) *grpcserver.Server {
	t.Helper()

	srv := grpcserver.New("127.0.0.1:0")

	go func() { _ = srv.Start() }()

	require.Eventually(t, func() bool { return srv.Addr() != "127.0.0.1:0" }, 2*time.Second, 10*time.Millisecond)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_ = srv.Shutdown(ctx)
	})

	return srv
}

func probeCtx(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	return ctx
}

func TestProbeHealth(t *testing.T) {
	t.Parallel()

	t.Run("serving", func(t *testing.T) {
		t.Parallel()

		srv := startServer(t)
		require.NoError(t, grpcserver.ProbeHealth(probeCtx(t), srv.Addr()))
	})

	t.Run("not serving", func(t *testing.T) {
		t.Parallel()

		srv := startServer(t)
		srv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

		err := grpcserver.ProbeHealth(probeCtx(t), srv.Addr())
		require.True(t, errors.Is(err, grpcserver.ErrNotServing), "err = %v", err)
	})

	t.Run("unreachable", func(t *testing.T) {
		t.Parallel()

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		addr := lis.Addr().String()
		require.NoError(t, lis.Close())

		shortCtx, shortCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer shortCancel()

		require.Error(t, grpcserver.ProbeHealth(shortCtx, addr))
	})
}
