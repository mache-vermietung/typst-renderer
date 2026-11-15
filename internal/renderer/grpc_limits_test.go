package renderer_test

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/mache-vermietung/typst-renderer/internal/grpcserver"
	"github.com/mache-vermietung/typst-renderer/internal/renderer"
	rendererv1 "github.com/mache-vermietung/typst-renderer/proto/renderer/v1"
)

// newBufconnClient serves srv through grpcserver with the renderer's message size options, so the
// transport limits are exercised exactly as in cmd/renderer.
func newBufconnClient(t *testing.T, cfg renderer.Config, srv *renderer.Server) rendererv1.RendererServiceClient {
	t.Helper()

	lis := bufconn.Listen(1 << 20)

	grpcSrv := grpcserver.New("", renderer.GRPCServerOptions(cfg)...)
	rendererv1.RegisterRendererServiceServer(grpcSrv.GRPCServer(), srv)

	go func() {
		_ = grpcSrv.GRPCServer().Serve(lis)
	}()

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(64<<20)),
	)
	if err != nil {
		t.Fatalf("failed to dial bufnet: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()

		grpcSrv.GRPCServer().Stop()
	})

	return rendererv1.NewRendererServiceClient(conn)
}

// paddedTemplate returns a valid template of roughly size bytes (the padding is a Typst comment).
func paddedTemplate(size int) []byte {
	var b bytes.Buffer

	b.WriteString("= Padded\n")

	for b.Len() < size {
		b.WriteString("// " + strings.Repeat("x", 1<<16) + "\n")
	}

	return b.Bytes()
}

func TestServer_GRPC_RequestAboveDefaultMessageLimit(t *testing.T) {
	requireTypst(t)

	cfg := testConfig()
	cfg.MaxRequestBytes = 8 << 20
	client := newBufconnClient(t, cfg, newTestServer(t, func(c *renderer.Config) { *c = cfg }))

	// 5 MiB: above gRPC's 4 MiB default, below the configured limit.
	resp, err := client.Render(context.Background(), &rendererv1.RenderRequest{
		Entrypoint: "main.typ",
		Files:      map[string][]byte{"main.typ": paddedTemplate(5 << 20)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.HasPrefix(resp.GetPdf(), []byte("%PDF-")) {
		t.Errorf("expected PDF output")
	}
}

func TestServer_GRPC_RequestAboveConfiguredLimit(t *testing.T) {
	cfg := testConfig()
	cfg.MaxRequestBytes = 6 << 20
	client := newBufconnClient(t, cfg, newTestServer(t, func(c *renderer.Config) { *c = cfg }))

	_, err := client.Render(context.Background(), &rendererv1.RenderRequest{
		Entrypoint: "main.typ",
		Files:      map[string][]byte{"main.typ": paddedTemplate(6<<20 + 1)},
	})

	st := status.Convert(err)
	if st.Code() != grpccodes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted, got %v (%s)", st.Code(), st.Message())
	}

	// The handler's own check answers, not the transport limit.
	if !strings.Contains(st.Message(), "exceeds limit") {
		t.Errorf("expected the renderer's size limit message, got %q", st.Message())
	}
}
