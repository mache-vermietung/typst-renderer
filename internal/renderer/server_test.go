package renderer_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tobima/domio/internal/renderer"
	rendererv1 "github.com/tobima/domio/internal/renderer/proto/renderer/v1"
)

// requireTypst skips tests that run the real compiler when no typst binary is installed.
func requireTypst(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("typst"); err != nil {
		t.Skip("typst binary not found in PATH")
	}
}

func testConfig() renderer.Config {
	return renderer.Config{
		MaxRequestBytes:   1024 * 1024,
		MaxConcurrency:    2,
		MaxTimeoutSeconds: 5,
		TypstBinaryPath:   "typst",
		PackagePath:       "../document/packages",
	}
}

func newTestServer(t *testing.T, modifyCfg ...func(*renderer.Config)) *renderer.Server {
	t.Helper()

	cfg := testConfig()

	for _, m := range modifyCfg {
		m(&cfg)
	}

	srv, err := renderer.NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	return srv
}

func TestServer_Render_Success(t *testing.T) {
	requireTypst(t)

	srv := newTestServer(t)

	req := &rendererv1.RenderRequest{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte("#let data = json(\"data.json\")\n= Hello [ #data.name ]\n"),
		},
		DataJson: []byte(`{"name":"World"}`),
	}

	resp, err := srv.Render(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.GetPdf()) == 0 {
		t.Fatal("expected non-empty PDF bytes")
	}

	if !strings.HasPrefix(string(resp.GetPdf()[:5]), "%PDF-") {
		t.Errorf("expected PDF header %%PDF-, got %q", string(resp.GetPdf()[:5]))
	}
}

// assertStatus checks code and ErrorReason of err and that the message contains wantSub.
func assertStatus(t *testing.T, err error, wantCode grpccodes.Code, wantReason rendererv1.ErrorReason, wantSub string) {
	t.Helper()

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st := status.Convert(err)
	if st.Code() != wantCode {
		t.Errorf("expected code %v, got %v (msg: %s)", wantCode, st.Code(), st.Message())
	}

	if reason := rendererv1.ReasonOf(st); reason != wantReason {
		t.Errorf("expected reason %v, got %v", wantReason, reason)
	}

	if !strings.Contains(st.Message(), wantSub) {
		t.Errorf("error %q should contain %q", st.Message(), wantSub)
	}
}

func TestServer_Render_ValidationErrors(t *testing.T) {
	srv := newTestServer(t)

	invalidBundle := rendererv1.ErrorReason_ERROR_REASON_INVALID_BUNDLE

	tests := []struct {
		name       string
		req        *rendererv1.RenderRequest
		wantReason rendererv1.ErrorReason
		wantErrSub string
		needsTypst bool
	}{
		{
			name: "missing entrypoint",
			req: &rendererv1.RenderRequest{
				Entrypoint: "",
				Files:      map[string][]byte{"main.typ": []byte("= Test")},
			},
			wantReason: invalidBundle,
			wantErrSub: "entrypoint is required",
		},
		{
			name: "entrypoint not in files",
			req: &rendererv1.RenderRequest{
				Entrypoint: "other.typ",
				Files:      map[string][]byte{"main.typ": []byte("= Test")},
			},
			wantReason: invalidBundle,
			wantErrSub: "not found in files",
		},
		{
			name: "entrypoint with surrounding whitespace is not looked up trimmed",
			req: &rendererv1.RenderRequest{
				Entrypoint: " main.typ",
				Files:      map[string][]byte{"main.typ": []byte("= Test")},
			},
			wantReason: invalidBundle,
			wantErrSub: "not found in files",
		},
		{
			name: "absolute entrypoint",
			req: &rendererv1.RenderRequest{
				Entrypoint: "/main.typ",
				Files:      map[string][]byte{"/main.typ": []byte("= Test")},
			},
			wantReason: invalidBundle,
			wantErrSub: "invalid entrypoint path",
		},
		{
			name: "path traversal in file key",
			req: &rendererv1.RenderRequest{
				Entrypoint: "main.typ",
				Files: map[string][]byte{
					"main.typ":      []byte("= Test"),
					"../secret.txt": []byte("bad"),
				},
			},
			wantReason: invalidBundle,
			wantErrSub: "invalid file path",
		},
		{
			name: "nested path traversal in file key",
			req: &rendererv1.RenderRequest{
				Entrypoint: "main.typ",
				Files: map[string][]byte{
					"main.typ":           []byte("= Test"),
					"assets/../../x.txt": []byte("bad"),
				},
			},
			wantReason: invalidBundle,
			wantErrSub: "invalid file path",
		},
		{
			name: "unsupported pdf standard",
			req: &rendererv1.RenderRequest{
				Entrypoint:  "main.typ",
				Files:       map[string][]byte{"main.typ": []byte("= Test")},
				PdfStandard: "--input=x",
			},
			wantReason: invalidBundle,
			wantErrSub: "unsupported pdf standard",
		},
		{
			name: "typst compile error",
			req: &rendererv1.RenderRequest{
				Entrypoint: "main.typ",
				Files: map[string][]byte{
					"main.typ": []byte("#invalid_syntax{{{"),
				},
			},
			wantReason: rendererv1.ErrorReason_ERROR_REASON_COMPILE_FAILED,
			wantErrSub: "typst compile failed",
			needsTypst: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.needsTypst {
				requireTypst(t)
			}

			_, err := srv.Render(context.Background(), tt.req)
			assertStatus(t, err, grpccodes.InvalidArgument, tt.wantReason, tt.wantErrSub)
		})
	}
}

func TestServer_Render_RequestTooLarge(t *testing.T) {
	srv := newTestServer(t, func(c *renderer.Config) {
		c.MaxRequestBytes = 50
	})

	req := &rendererv1.RenderRequest{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte("= This template content exceeds the limit of 50 bytes by far"),
		},
	}

	_, err := srv.Render(context.Background(), req)
	assertStatus(t, err, grpccodes.ResourceExhausted, rendererv1.ErrorReason_ERROR_REASON_REQUEST_TOO_LARGE, "exceeds limit")
}

func TestServer_Render_ConcurrencyLimit(t *testing.T) {
	srv := newTestServer(t, func(c *renderer.Config) {
		c.MaxConcurrency = 1
	})

	req := &rendererv1.RenderRequest{
		Entrypoint: "main.typ",
		Files:      map[string][]byte{"main.typ": []byte("= Concurrent Test")},
	}

	release := srv.OccupySlots(1)

	_, err := srv.Render(context.Background(), req)
	assertStatus(t, err, grpccodes.ResourceExhausted, rendererv1.ErrorReason_ERROR_REASON_CONCURRENCY_LIMIT, "concurrency limit")

	_, err = srv.Query(context.Background(), &rendererv1.QueryRequest{Entrypoint: "main.typ", Files: req.GetFiles(), Selector: "<x>"})
	assertStatus(t, err, grpccodes.ResourceExhausted, rendererv1.ErrorReason_ERROR_REASON_CONCURRENCY_LIMIT, "concurrency limit")

	release()

	// The slot of a finished (here: rejected) request is free again.
	requireTypst(t)

	if _, err := srv.Render(context.Background(), req); err != nil {
		t.Fatalf("expected render after release to succeed, got %v", err)
	}
}

func TestServer_Render_TimeoutLeavesNoWorkDirectory(t *testing.T) {
	requireTypst(t)

	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	srv := newTestServer(t, func(c *renderer.Config) {
		c.MaxTimeoutSeconds = 1
	})

	req := &rendererv1.RenderRequest{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte("#let x = 0\n#for i in range(1000000000) { x += 1 }\n#x\n"),
		},
	}

	start := time.Now()

	_, err := srv.Render(context.Background(), req)
	assertStatus(t, err, grpccodes.DeadlineExceeded, rendererv1.ErrorReason_ERROR_REASON_TIMEOUT, "timed out")

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("expected the compiler to be killed shortly after the timeout, took %s", elapsed)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("failed to read temp dir: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("expected no leftover work directories, found %d entries", len(entries))
	}
}

func TestServer_Render_GRPCDeadlineBoundsTimeout(t *testing.T) {
	requireTypst(t)

	srv := newTestServer(t, func(c *renderer.Config) {
		c.MaxTimeoutSeconds = 30
	})

	req := &rendererv1.RenderRequest{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte("#let x = 0\n#for i in range(1000000000) { x += 1 }\n#x\n"),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()

	_, err := srv.Render(ctx, req)
	assertStatus(t, err, grpccodes.DeadlineExceeded, rendererv1.ErrorReason_ERROR_REASON_TIMEOUT, "timed out")

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("expected the caller's deadline to bound the compilation, took %s", elapsed)
	}
}

func TestServer_Render_CanceledByCaller(t *testing.T) {
	requireTypst(t)

	srv := newTestServer(t)

	req := &rendererv1.RenderRequest{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte("#let x = 0\n#for i in range(1000000000) { x += 1 }\n#x\n"),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)

	_, err := srv.Render(ctx, req)
	if code := status.Code(err); code != grpccodes.Canceled {
		t.Fatalf("expected Canceled, got %v (%v)", code, err)
	}
}

// A package that is not vendored must not be downloaded: the renderer has no egress, and typst's
// HTTP(S) traffic is routed to an unreachable proxy.
func TestServer_Render_NonVendoredPackageFails(t *testing.T) {
	requireTypst(t)

	srv := newTestServer(t)

	req := &rendererv1.RenderRequest{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte("#import \"@preview/domio-not-vendored:0.1.0\": *\n= Test\n"),
		},
	}

	_, err := srv.Render(context.Background(), req)
	assertStatus(t, err, grpccodes.InvalidArgument, rendererv1.ErrorReason_ERROR_REASON_COMPILE_FAILED, "download")
}

func TestServer_Render_PDFStandard(t *testing.T) {
	requireTypst(t)

	srv := newTestServer(t)

	resp, err := srv.Render(context.Background(), &rendererv1.RenderRequest{
		Entrypoint:  "main.typ",
		Files:       map[string][]byte{"main.typ": []byte("= Archiv")},
		PdfStandard: "a-2b",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(string(resp.GetPdf()), "pdfaid") {
		t.Errorf("expected PDF/A identification in the output")
	}
}

func TestServer_Query(t *testing.T) {
	requireTypst(t)

	srv := newTestServer(t)

	req := &rendererv1.QueryRequest{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte("[#metadata(\"custom-value\") <meta-key>]"),
		},
		Selector: "<meta-key>",
	}

	resp, err := srv.Query(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(string(resp.GetDataJson()), "custom-value") {
		t.Errorf("expected result containing 'custom-value', got %q", string(resp.GetDataJson()))
	}
}

func TestServer_Query_EmptySelector(t *testing.T) {
	srv := newTestServer(t)

	req := &rendererv1.QueryRequest{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte("= Test"),
		},
		Selector: "",
	}

	_, err := srv.Query(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st := status.Convert(err)
	if st.Code() != grpccodes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}
