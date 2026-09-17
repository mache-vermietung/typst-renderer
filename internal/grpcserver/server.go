// Package grpcserver provides a gRPC server wrapper with telemetry, health check, and reflection.
package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Server wraps a grpc.Server with health check, reflection, and lifecycle management.
type Server struct {
	addr         string
	server       *grpc.Server
	healthServer *health.Server
	listener     net.Listener
	mu           sync.Mutex
	running      bool
}

// New creates a new gRPC Server instance configured with OpenTelemetry and health checks.
func New(addr string, extraOpts ...grpc.ServerOption) *Server {
	opts := make([]grpc.ServerOption, 0, 1+len(extraOpts))
	opts = append(opts, grpc.StatsHandler(otelgrpc.NewServerHandler()))
	opts = append(opts, extraOpts...)

	grpcServer := grpc.NewServer(opts...)

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	reflection.Register(grpcServer)

	return &Server{
		addr:         addr,
		server:       grpcServer,
		healthServer: healthServer,
	}
}

// GRPCServer returns the underlying grpc.Server to allow registering services.
func (s *Server) GRPCServer() *grpc.Server {
	return s.server
}

// SetServingStatus sets the health status for a specific service or all services.
func (s *Server) SetServingStatus(service string, status grpc_health_v1.HealthCheckResponse_ServingStatus) {
	s.healthServer.SetServingStatus(service, status)
}

// Addr returns the bound address, especially useful when listening on ":0".
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener != nil {
		return s.listener.Addr().String()
	}

	return s.addr
}

// Start starts listening on the configured address and serving incoming RPCs.
// It blocks until the server stops or encounters an error.
func (s *Server) Start() error {
	s.mu.Lock()

	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("grpcserver: failed to listen on %s: %w", s.addr, err)
	}

	s.listener = lis
	s.running = true
	s.mu.Unlock()

	slog.Info("gRPC server listening", "addr", lis.Addr().String())

	if err := s.server.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("grpcserver: serve error: %w", err)
	}

	return nil
}

// Shutdown gracefully stops the gRPC server. If ctx is cancelled before graceful stop completes,
// it stops the server immediately.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}

	s.running = false
	s.mu.Unlock()

	s.healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	stopped := make(chan struct{})

	go func() {
		s.server.GracefulStop()
		close(stopped)
	}()

	select {
	case <-ctx.Done():
		s.server.Stop()
		return ctx.Err()
	case <-stopped:
		return nil
	}
}
