package typst_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tobima/domio/internal/infrastructure/typst"
)

func TestCheckBinary(t *testing.T) {
	ctx := context.Background()

	version, err := typst.CheckBinary(ctx, "typst")
	if err != nil {
		t.Fatalf("unexpected error checking typst binary: %v", err)
	}

	if len(version) == 0 {
		t.Errorf("expected non-empty version string")
	}

	_, err = typst.CheckBinary(ctx, "non_existent_binary_xyz_123")
	if !errors.Is(err, typst.ErrBinaryNotFound) {
		t.Errorf("expected ErrBinaryNotFound, got %v", err)
	}
}

func TestCompile_MinimalBundleWithJSON(t *testing.T) {
	ctx := context.Background()

	input := typst.Input{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte(`#let data = json("data.json")
#set page(paper: "a4")
= Mietvertrag
Mieter: #data.tenantName
`),
		},
		DataJSON: []byte(`{"tenantName": "Max Mustermann"}`),
	}

	opts := typst.CompileOptions{
		Timeout: 10 * time.Second,
	}

	pdf, err := typst.Compile(ctx, opts, input)
	if err != nil {
		t.Fatalf("expected successful compilation, got: %v", err)
	}

	if len(pdf) == 0 {
		t.Fatalf("expected non-empty PDF bytes")
	}

	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Errorf("expected PDF header %%PDF-, got %q", string(pdf[:5]))
	}
}

func TestCompile_VendoredPackage(t *testing.T) {
	ctx := context.Background()

	pkgPath := filepath.Join("..", "..", "document", "packages")
	if _, err := os.Stat(pkgPath); os.IsNotExist(err) {
		t.Skipf("vendored packages path not found: %s", pkgPath)
	}

	input := typst.Input{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte(`#import "@preview/cmarker:0.1.10": render
#render("*Fetter Text aus Markdown*", raw-typst: false)
`),
		},
	}

	opts := typst.CompileOptions{
		PackagePath: pkgPath,
		Timeout:     10 * time.Second,
	}

	pdf, err := typst.Compile(ctx, opts, input)
	if err != nil {
		t.Fatalf("expected successful compilation with vendored cmarker, got: %v", err)
	}

	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Errorf("expected valid PDF header, got %q", string(pdf[:5]))
	}
}

func TestCompile_NonVendoredPackageFails(t *testing.T) {
	ctx := context.Background()

	pkgPath := filepath.Join("..", "..", "document", "packages")

	// tablex exists in the public registry; the import must fail because downloads are blocked.
	input := typst.Input{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte(`#import "@preview/tablex:0.0.9": tablex
Hello
`),
		},
	}

	opts := typst.CompileOptions{
		PackagePath: pkgPath,
		Timeout:     5 * time.Second,
	}

	_, err := typst.Compile(ctx, opts, input)
	if err == nil {
		t.Fatalf("expected error for non-vendored package, got nil")
	}

	if !errors.Is(err, typst.ErrCompileFailed) {
		t.Errorf("expected ErrCompileFailed, got %v", err)
	}

	if !strings.Contains(err.Error(), "failed to download package") {
		t.Errorf("expected blocked package download, got %v", err)
	}
}

func TestCompile_SandboxEscapeFails(t *testing.T) {
	ctx := context.Background()

	input := typst.Input{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte(`#read("../escape.txt")`),
		},
	}

	opts := typst.CompileOptions{
		Timeout: 5 * time.Second,
	}

	_, err := typst.Compile(ctx, opts, input)
	if err == nil {
		t.Fatalf("expected error for escaping project root, got nil")
	}

	if !errors.Is(err, typst.ErrCompileFailed) {
		t.Errorf("expected ErrCompileFailed, got %v", err)
	}
}

func TestCompile_InvalidFilePathFails(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		filePath string
	}{
		{"Absolute path", "/etc/passwd"},
		{"Parent dir traversal", "../secret.txt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := typst.Input{
				Entrypoint: "main.typ",
				Files: map[string][]byte{
					"main.typ":  []byte("Hello"),
					tc.filePath: []byte("Forbidden"),
				},
			}

			_, err := typst.Compile(ctx, typst.CompileOptions{}, input)
			if !errors.Is(err, typst.ErrInvalidInput) {
				t.Errorf("expected ErrInvalidInput for path %q, got: %v", tc.filePath, err)
			}
		})
	}
}

func TestCompile_MissingEntrypointFails(t *testing.T) {
	ctx := context.Background()

	input := typst.Input{
		Entrypoint: "missing.typ",
		Files: map[string][]byte{
			"main.typ": []byte("Hello"),
		},
	}

	_, err := typst.Compile(ctx, typst.CompileOptions{}, input)
	if !errors.Is(err, typst.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for missing entrypoint, got: %v", err)
	}
}

func TestCompile_SyntaxErrorFails(t *testing.T) {
	ctx := context.Background()

	input := typst.Input{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte(`#let x = { incomplete`),
		},
	}

	_, err := typst.Compile(ctx, typst.CompileOptions{}, input)
	if !errors.Is(err, typst.ErrCompileFailed) {
		t.Errorf("expected ErrCompileFailed for syntax error, got: %v", err)
	}
}

func TestCompile_TimeoutHandling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	time.Sleep(2 * time.Millisecond) // ensure context is already expired

	input := typst.Input{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte("Hello"),
		},
	}

	_, err := typst.Compile(ctx, typst.CompileOptions{}, input)
	if !errors.Is(err, typst.ErrCompileTimeout) {
		t.Errorf("expected ErrCompileTimeout on timeout, got: %v", err)
	}
}

func TestCompile_LongRunningProcessIsKilled(t *testing.T) {
	ctx := context.Background()

	input := typst.Input{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte(`#let x = 0
#for i in range(1000000000) { x += 1 }
#x
`),
		},
	}

	start := time.Now()

	_, err := typst.Compile(ctx, typst.CompileOptions{Timeout: 500 * time.Millisecond}, input)
	if !errors.Is(err, typst.ErrCompileTimeout) {
		t.Fatalf("expected ErrCompileTimeout, got: %v", err)
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("expected compile to be aborted shortly after the timeout, took %s", elapsed)
	}
}

func TestCompile_RemovesTemporaryDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	input := typst.Input{
		Entrypoint: "main.typ",
		Files: map[string][]byte{
			"main.typ": []byte(`#let x = { incomplete`),
		},
	}

	if _, err := typst.Compile(context.Background(), typst.CompileOptions{}, input); err == nil {
		t.Fatalf("expected compile error")
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("failed to read temp dir: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("expected temporary directory to be removed, found %d entries", len(entries))
	}
}
