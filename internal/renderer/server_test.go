package renderer_test

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"

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

func TestServer_Render_ValidationErrors(t *testing.T) {
	srv := newTestServer(t)

	tests := []struct {
		name       string
		req        *rendererv1.RenderRequest
		wantCode   grpccodes.Code
		wantErrSub string
		needsTypst bool
	}{
		{
			name: "missing entrypoint",
			req: &rendererv1.RenderRequest{
				Entrypoint: "",
				Files:      map[string][]byte{"main.typ": []byte("= Test")},
			},
			wantCode:   grpccodes.InvalidArgument,
			wantErrSub: "entrypoint is required",
		},
		{
			name: "entrypoint not in files",
			req: &rendererv1.RenderRequest{
				Entrypoint: "other.typ",
				Files:      map[string][]byte{"main.typ": []byte("= Test")},
			},
			wantCode:   grpccodes.InvalidArgument,
			wantErrSub: "not found in files",
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
			wantCode:   grpccodes.InvalidArgument,
			wantErrSub: "invalid file path",
		},
		{
			name: "typst compile error",
			req: &rendererv1.RenderRequest{
				Entrypoint: "main.typ",
				Files: map[string][]byte{
					"main.typ": []byte("#invalid_syntax{{{"),
				},
			},
			wantCode:   grpccodes.InvalidArgument,
			wantErrSub: "compilation failed",
			needsTypst: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.needsTypst {
				requireTypst(t)
			}

			_, err := srv.Render(context.Background(), tt.req)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			st := status.Convert(err)
			if st.Code() != tt.wantCode {
				t.Errorf("expected code %v, got %v (msg: %s)", tt.wantCode, st.Code(), st.Message())
			}

			if !strings.Contains(st.Message(), tt.wantErrSub) {
				t.Errorf("error %q should contain %q", st.Message(), tt.wantErrSub)
			}
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
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st := status.Convert(err)
	if st.Code() != grpccodes.ResourceExhausted {
		t.Errorf("expected ResourceExhausted, got %v", st.Code())
	}
}

func TestServer_Render_ConcurrencyLimit(t *testing.T) {
	srv := newTestServer(t, func(c *renderer.Config) {
		c.MaxConcurrency = 1
	})

	req := &rendererv1.RenderRequest{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte("= Concurrent Test"),
		},
	}

	var wg sync.WaitGroup

	var errExhausted bool

	var mu sync.Mutex

	for range 5 {
		wg.Go(func() {
			_, err := srv.Render(context.Background(), req)
			if err != nil {
				st := status.Convert(err)
				if st.Code() == grpccodes.ResourceExhausted {
					mu.Lock()
					errExhausted = true
					mu.Unlock()
				}
			}
		})
	}

	wg.Wait()

	_ = errExhausted
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
