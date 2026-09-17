package grpcserver

import (
	"context"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/tobima/domio/internal/shared/ratelimit"
)

// NewRateLimitInterceptors builds a matching pair of unary/stream server interceptors that
// enforce a per-peer-IP request limit on the given set of full gRPC method names (e.g.
// "/applicant.v1.ApplicantSubmissionService/ValidateUploadLink"), independently of any
// rate limiting performed upstream (e.g. by a BFF) - so the core backend stays protected even
// if a caller talks to it directly. Methods not in protectedMethods are never limited.
func NewRateLimitInterceptors(
	limit int, window time.Duration, protectedMethods map[string]bool,
) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	limiter := ratelimit.New(limit, window)

	unary := func(
		ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		if protectedMethods[info.FullMethod] && !limiter.Allow(peerKey(ctx)) {
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}

		return handler(ctx, req)
	}

	stream := func(
		srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler,
	) error {
		if protectedMethods[info.FullMethod] && !limiter.Allow(peerKey(ss.Context())) {
			return status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}

		return handler(srv, ss)
	}

	return unary, stream
}

// peerKey extracts the caller's IP address from ctx, falling back to the raw peer address
// string if it cannot be split into host/port (e.g. a non-TCP or already-host-only address).
func peerKey(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return "unknown"
	}

	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return p.Addr.String()
	}

	return host
}
