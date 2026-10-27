// Package typst provides hardened execution of the Typst CLI compiler in an isolated sandbox.
package typst

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultCompileTimeout is the default compilation timeout.
	DefaultCompileTimeout = 20 * time.Second
	// DefaultBinaryName is the default binary name on PATH.
	DefaultBinaryName = "typst"
)

var (
	// ErrCompileFailed indicates that typst compilation failed.
	ErrCompileFailed = errors.New("typst compile failed")
	// ErrBinaryNotFound indicates that the typst binary was not found or is not executable.
	ErrBinaryNotFound = errors.New("typst binary not found")
	// ErrInvalidInput indicates that input parameters or file paths are invalid.
	ErrInvalidInput = errors.New("invalid typst input")
)

// CompileOptions configures the typst compiler execution.
type CompileOptions struct {
	BinaryPath  string
	PackagePath string
	FontPath    string
	Timeout     time.Duration
}

// Input represents the template bundle and data to compile.
type Input struct {
	Entrypoint string
	Files      map[string][]byte
	DataJSON   []byte
}

// CheckBinary verifies that the typst binary is executable and returns its version string.
func CheckBinary(ctx context.Context, binaryPath string) (string, error) {
	if binaryPath == "" {
		binaryPath = DefaultBinaryName
	}

	cmd := exec.CommandContext(ctx, binaryPath, "--version")

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrBinaryNotFound, binaryPath, err)
	}

	return strings.TrimSpace(string(out)), nil
}

// Compile compiles a template bundle to PDF using the Typst CLI.
// It writes files to a temporary directory, sets the sandbox root to that directory,
// executes Typst with minimal environment variables, and removes all temporary files.
func Compile(ctx context.Context, opts CompileOptions, input Input) ([]byte, error) {
	if input.Entrypoint == "" {
		return nil, fmt.Errorf("%w: entrypoint is required", ErrInvalidInput)
	}

	if input.Files == nil {
		return nil, fmt.Errorf("%w: files map is required", ErrInvalidInput)
	}

	if _, ok := input.Files[input.Entrypoint]; !ok {
		return nil, fmt.Errorf("%w: entrypoint %q not found in files", ErrInvalidInput, input.Entrypoint)
	}

	tmpDir, err := os.MkdirTemp("", "domio-typst-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	if err := writeBundleFiles(tmpDir, input); err != nil {
		return nil, err
	}

	outPDF := filepath.Join(tmpDir, "out.pdf")
	args := buildCompileArgs(tmpDir, input.Entrypoint, outPDF, opts)

	binary := opts.BinaryPath
	if binary == "" {
		binary = DefaultBinaryName
	}

	if err := runTypstCommand(ctx, binary, args, opts.Timeout); err != nil {
		return nil, err
	}

	// #nosec G304 -- outPDF is located in the freshly created isolated temporary directory
	pdfBytes, err := os.ReadFile(outPDF)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read generated PDF: %w", ErrCompileFailed, err)
	}

	return pdfBytes, nil
}

func writeBundleFiles(tmpDir string, input Input) error {
	for relPath, content := range input.Files {
		if err := writeSingleFile(tmpDir, relPath, content); err != nil {
			return err
		}
	}

	if len(input.DataJSON) > 0 {
		dataPath := filepath.Join(tmpDir, "data.json")
		if err := os.WriteFile(dataPath, input.DataJSON, 0o600); err != nil {
			return fmt.Errorf("failed to write data.json: %w", err)
		}
	}

	return nil
}

func writeSingleFile(tmpDir, relPath string, content []byte) error {
	cleaned := filepath.Clean(relPath)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") || cleaned == "." {
		return fmt.Errorf("%w: invalid file path %q", ErrInvalidInput, relPath)
	}

	targetPath := filepath.Join(tmpDir, cleaned)

	relToTmp, err := filepath.Rel(tmpDir, targetPath)
	if err != nil || strings.HasPrefix(relToTmp, "..") {
		return fmt.Errorf("%w: file path escapes root %q", ErrInvalidInput, relPath)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return fmt.Errorf("failed to create parent directory for %q: %w", relPath, err)
	}

	if err := os.WriteFile(targetPath, content, 0o600); err != nil {
		return fmt.Errorf("failed to write file %q: %w", relPath, err)
	}

	return nil
}

func buildCompileArgs(tmpDir, entrypoint, outPDF string, opts CompileOptions) []string {
	args := []string{"compile", "--root", tmpDir}

	if opts.PackagePath != "" {
		absPkgPath, err := filepath.Abs(opts.PackagePath)
		if err == nil {
			args = append(args, "--package-path", absPkgPath)
		} else {
			args = append(args, "--package-path", opts.PackagePath)
		}
	}

	args = append(args, "--ignore-system-fonts")

	if opts.FontPath != "" {
		absFontPath, err := filepath.Abs(opts.FontPath)
		if err == nil {
			args = append(args, "--font-path", absFontPath)
		} else {
			args = append(args, "--font-path", opts.FontPath)
		}
	}

	args = append(args, filepath.Join(tmpDir, filepath.Clean(entrypoint)), outPDF)

	return args
}

func runTypstCommand(ctx context.Context, binary string, args []string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultCompileTimeout
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- binary name and arguments are strictly controlled by the compiler options and temp sandbox
	cmd := exec.CommandContext(cmdCtx, binary, args...)

	if envPath := os.Getenv("PATH"); envPath != "" {
		cmd.Env = []string{"PATH=" + envPath}
	} else {
		cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	}

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w: compilation timed out: %w", ErrCompileFailed, cmdCtx.Err())
		}

		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("%w: binary %s not found: %w", ErrBinaryNotFound, binary, err)
		}

		diag := strings.TrimSpace(stderr.String())
		if diag != "" {
			return fmt.Errorf("%w: %s", ErrCompileFailed, diag)
		}

		return fmt.Errorf("%w: %w", ErrCompileFailed, err)
	}

	return nil
}
