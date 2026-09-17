package grpcserver_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/tobima/domio/internal/infrastructure/grpcserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func ctxWithPeer(ip string) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 12345},
	})
}

func okHandler(_ context.Context, _ any) (any, error) {
	return "ok", nil
}

func TestUnaryRateLimitInterceptor_LimitsProtectedMethod(t *testing.T) {
	protected := map[string]bool{"/svc/Protected": true}
	unary, _ := grpcserver.NewRateLimitInterceptors(1, time.Minute, protected)

	info := &grpc.UnaryServerInfo{FullMethod: "/svc/Protected"}
	ctx := ctxWithPeer("203.0.113.1")

	if _, err := unary(ctx, nil, info, okHandler); err != nil {
		t.Fatalf("first call: unexpected error %v", err)
	}

	_, err := unary(ctx, nil, info, okHandler)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second call: got err %v, want ResourceExhausted", err)
	}
}

func TestUnaryRateLimitInterceptor_UnprotectedMethodIsNeverLimited(t *testing.T) {
	protected := map[string]bool{"/svc/Protected": true}
	unary, _ := grpcserver.NewRateLimitInterceptors(1, time.Minute, protected)

	info := &grpc.UnaryServerInfo{FullMethod: "/svc/Unprotected"}
	ctx := ctxWithPeer("203.0.113.1")

	for i := range 5 {
		if _, err := unary(ctx, nil, info, okHandler); err != nil {
			t.Fatalf("call %d: unexpected error %v", i+1, err)
		}
	}
}

func TestUnaryRateLimitInterceptor_LimitIsPerPeerIP(t *testing.T) {
	protected := map[string]bool{"/svc/Protected": true}
	unary, _ := grpcserver.NewRateLimitInterceptors(1, time.Minute, protected)

	info := &grpc.UnaryServerInfo{FullMethod: "/svc/Protected"}

	if _, err := unary(ctxWithPeer("203.0.113.1"), nil, info, okHandler); err != nil {
		t.Fatalf("first IP: unexpected error %v", err)
	}

	if _, err := unary(ctxWithPeer("198.51.100.7"), nil, info, okHandler); err != nil {
		t.Fatalf("second, distinct IP: unexpected error %v", err)
	}
}

type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeServerStream) Context() context.Context {
	return f.ctx
}

func TestStreamRateLimitInterceptor_LimitsProtectedMethod(t *testing.T) {
	protected := map[string]bool{"/svc/ProtectedStream": true}
	_, stream := grpcserver.NewRateLimitInterceptors(1, time.Minute, protected)

	info := &grpc.StreamServerInfo{FullMethod: "/svc/ProtectedStream"}
	ss := &fakeServerStream{ctx: ctxWithPeer("203.0.113.1")}

	okStream := func(_ any, _ grpc.ServerStream) error { return nil }

	if err := stream(nil, ss, info, okStream); err != nil {
		t.Fatalf("first call: unexpected error %v", err)
	}

	err := stream(nil, ss, info, okStream)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second call: got err %v, want ResourceExhausted", err)
	}
}
