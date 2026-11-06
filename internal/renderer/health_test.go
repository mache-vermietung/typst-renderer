package renderer_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/tobima/domio/internal/renderer"
	rendererv1 "github.com/tobima/domio/internal/renderer/proto/renderer/v1"
)

func TestProbeAddr(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		":9100":          "127.0.0.1:9100",
		"0.0.0.0:9100":   "127.0.0.1:9100",
		"[::]:9100":      "127.0.0.1:9100",
		"10.0.0.5:9100":  "10.0.0.5:9100",
		"localhost:9100": "localhost:9100",
	}

	for in, want := range tests {
		if got := renderer.ProbeAddr(in); got != want {
			t.Errorf("ProbeAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGRPCServer_ImplementsHealthService(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.ListenAddr = "127.0.0.1:0"

	grpcSrv := renderer.NewGRPCServer(cfg, newTestServer(t))

	go func() { _ = grpcSrv.Start() }()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_ = grpcSrv.Shutdown(ctx)
	})

	deadline := time.Now().Add(2 * time.Second)
	for grpcSrv.Addr() == cfg.ListenAddr && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg.ListenAddr = grpcSrv.Addr()
	if err := renderer.RunHealthProbe(ctx, cfg); err != nil {
		t.Fatalf("RunHealthProbe: %v", err)
	}

	conn, err := grpc.NewClient(grpcSrv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	resp, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: rendererv1.RendererService_ServiceDesc.ServiceName,
	})
	if err != nil {
		t.Fatalf("health check for RendererService: %v", err)
	}

	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("RendererService status = %v, want SERVING", resp.GetStatus())
	}
}
