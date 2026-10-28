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
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultCompileTimeout is the default compilation timeout.
	DefaultCompileTimeout = 20 * time.Second
	// DefaultBinaryName is the default binary name on PATH.
	DefaultBinaryName = "typst"
	// unreachableProxy is set as HTTP(S) proxy for the typst process so that it cannot
	// download packages that are not vendored.
	unreachableProxy = "http://127.0.0.1:9"
	// processWaitDelay bounds how long Wait blocks for I/O after the process was killed.
	processWaitDelay = 2 * time.Second
	// DefaultMaxOutputBytes is the default size limit of the generated PDF/PNG or query output.
	DefaultMaxOutputBytes int64 = 64 << 20
	// maxDiagnosticBytes bounds the stderr diagnostics kept for error messages.
	maxDiagnosticBytes int64 = 64 << 10
	// dataFileName is the file below the sandbox root that receives Input.DataJSON.
	dataFileName = "data.json"
)

var (
	// ErrCompileFailed indicates that typst compilation failed.
	ErrCompileFailed = errors.New("typst compile failed")
	// ErrCompileTimeout indicates that typst compilation exceeded its timeout.
	ErrCompileTimeout = errors.New("typst compile timed out")
	// ErrBinaryNotFound indicates that the typst binary was not found or is not executable.
	ErrBinaryNotFound = errors.New("typst binary not found")
	// ErrInvalidInput indicates that input parameters or file paths are invalid.
	ErrInvalidInput = errors.New("invalid typst input")
	// ErrOutputTooLarge indicates that the generated output exceeds CompileOptions.MaxOutputBytes.
	ErrOutputTooLarge = errors.New("typst output too large")
)

// CompileOptions configures the typst compiler execution.
type CompileOptions struct {
	BinaryPath  string
	PackagePath string
	FontPath    string
	Timeout     time.Duration
	// MaxOutputBytes limits the generated output; 0 means DefaultMaxOutputBytes.
	MaxOutputBytes int64
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
// Packages are resolved only from opts.PackagePath: the package cache is an empty
// per-call directory and all HTTP(S) traffic goes to an unreachable proxy, so imports
// of packages that are not vendored fail instead of being downloaded.
func Compile(ctx context.Context, opts CompileOptions, input Input) ([]byte, error) {
	return runInSandbox(ctx, opts, input, func(s sandbox) ([]string, string) {
		outPDF := filepath.Join(s.workDir, "out.pdf")

		return append(s.baseArgs("compile"), s.entrypoint, outPDF), outPDF
	})
}

// CompilePNG compiles a specific 1-based page of a template bundle to a PNG image.
func CompilePNG(ctx context.Context, opts CompileOptions, input Input, page int) ([]byte, error) {
	if page < 1 {
		return nil, fmt.Errorf("%w: page must be >= 1", ErrInvalidInput)
	}

	return runInSandbox(ctx, opts, input, func(s sandbox) ([]string, string) {
		outTemplate := filepath.Join(s.workDir, "out-{p}.png")
		args := append(s.baseArgs("compile"), "--format", "png", "--pages", strconv.Itoa(page), s.entrypoint, outTemplate)

		return args, filepath.Join(s.workDir, fmt.Sprintf("out-%d.png", page))
	})
}

// Query runs typst query on a template bundle and returns the matching elements in JSON format.
func Query(ctx context.Context, opts CompileOptions, input Input, selector string) ([]byte, error) {
	if selector == "" {
		return nil, fmt.Errorf("%w: selector is required", ErrInvalidInput)
	}

	return runInSandbox(ctx, opts, input, func(s sandbox) ([]string, string) {
		return append(s.baseArgs("query"), s.entrypoint, selector, "--field", "value"), ""
	})
}

// sandbox describes the isolated per-call working directory of a typst invocation.
type sandbox struct {
	workDir    string
	rootDir    string
	cacheDir   string
	entrypoint string
	opts       CompileOptions
}

func (s sandbox) baseArgs(subcommand string) []string {
	return buildBaseArgs(subcommand, s.rootDir, s.cacheDir, s.opts)
}

// runInSandbox validates the input, writes the bundle into a fresh temporary sandbox, runs typst
// with the arguments returned by buildArgs and removes the sandbox afterwards. The result is the
// content of outputFile, or typst's stdout if outputFile is empty; both are size-limited.
func runInSandbox(
	ctx context.Context,
	opts CompileOptions,
	input Input,
	buildArgs func(s sandbox) (args []string, outputFile string),
) ([]byte, error) {
	if err := validateInput(input); err != nil {
		return nil, err
	}

	workDir, err := os.MkdirTemp("", "domio-typst-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	defer func() {
		_ = os.RemoveAll(workDir)
	}()

	rootDir, cacheDir, err := createWorkDirs(workDir)
	if err != nil {
		return nil, err
	}

	if err := writeBundleFiles(rootDir, input); err != nil {
		return nil, err
	}

	args, outputFile := buildArgs(sandbox{
		workDir:    workDir,
		rootDir:    rootDir,
		cacheDir:   cacheDir,
		entrypoint: filepath.Join(rootDir, filepath.Clean(input.Entrypoint)),
		opts:       opts,
	})

	binary := opts.BinaryPath
	if binary == "" {
		binary = DefaultBinaryName
	}

	maxOutput := opts.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputBytes
	}

	stdout, err := runTypstCommand(ctx, binary, args, opts.Timeout, maxOutput)
	if err != nil || outputFile == "" {
		return stdout, err
	}

	return readOutputFile(outputFile, maxOutput)
}

// readOutputFile reads the generated file, rejecting files above maxBytes.
func readOutputFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read generated output: %w", ErrCompileFailed, err)
	}

	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%w: %d bytes (limit %d)", ErrOutputTooLarge, info.Size(), maxBytes)
	}

	// #nosec G304 -- path is located in the freshly created isolated temporary directory
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read generated output: %w", ErrCompileFailed, err)
	}

	return content, nil
}

func validateInput(input Input) error {
	if input.Entrypoint == "" {
		return fmt.Errorf("%w: entrypoint is required", ErrInvalidInput)
	}

	if input.Files == nil {
		return fmt.Errorf("%w: files map is required", ErrInvalidInput)
	}

	if _, ok := input.Files[input.Entrypoint]; !ok {
		return fmt.Errorf("%w: entrypoint %q not found in files", ErrInvalidInput, input.Entrypoint)
	}

	if len(input.DataJSON) == 0 {
		return nil
	}

	for relPath := range input.Files {
		if filepath.Clean(relPath) == dataFileName {
			return fmt.Errorf("%w: bundle file %q collides with the data file", ErrInvalidInput, relPath)
		}
	}

	return nil
}

// createWorkDirs creates the sandbox root and the (always empty) package cache below workDir.
func createWorkDirs(workDir string) (rootDir, cacheDir string, err error) {
	rootDir = filepath.Join(workDir, "root")
	cacheDir = filepath.Join(workDir, "cache")

	for _, dir := range []string{rootDir, cacheDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", "", fmt.Errorf("failed to create directory %q: %w", dir, err)
		}
	}

	return rootDir, cacheDir, nil
}

func writeBundleFiles(tmpDir string, input Input) error {
	for relPath, content := range input.Files {
		if err := writeSingleFile(tmpDir, relPath, content); err != nil {
			return err
		}
	}

	if len(input.DataJSON) > 0 {
		dataPath := filepath.Join(tmpDir, dataFileName)
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

func buildBaseArgs(subcommand, rootDir, cacheDir string, opts CompileOptions) []string {
	args := []string{subcommand, "--root", rootDir, "--package-cache-path", cacheDir}

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

	return args
}

// runTypstCommand runs typst and returns its stdout, which is limited to maxStdout bytes.
// Diagnostics on stderr are kept up to maxDiagnosticBytes for the returned error.
func runTypstCommand(ctx context.Context, binary string, args []string, timeout time.Duration, maxStdout int64) ([]byte, error) {
	if timeout <= 0 {
		timeout = DefaultCompileTimeout
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- binary name and arguments are strictly controlled by the compiler options and temp sandbox
	cmd := exec.CommandContext(cmdCtx, binary, args...)

	cmd.Env = processEnv()
	cmd.WaitDelay = processWaitDelay

	stdout := &cappedBuffer{limit: maxStdout}
	stderr := &cappedBuffer{limit: maxDiagnosticBytes}

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %w", ErrCompileTimeout, cmdCtx.Err())
		}

		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%w: binary %s not found: %w", ErrBinaryNotFound, binary, err)
		}

		diag := strings.TrimSpace(stderr.String())
		if diag != "" {
			return nil, fmt.Errorf("%w: %s", ErrCompileFailed, diag)
		}

		return nil, fmt.Errorf("%w: %w", ErrCompileFailed, err)
	}

	if stdout.truncated {
		return nil, fmt.Errorf("%w: output exceeds %d bytes", ErrOutputTooLarge, maxStdout)
	}

	return stdout.buf.Bytes(), nil
}

// cappedBuffer keeps at most limit bytes and silently discards the rest, so that a chatty
// process cannot exhaust memory while it is still able to write (no broken pipe).
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	kept := p

	remaining := b.limit - int64(b.buf.Len())
	if int64(len(p)) > remaining {
		b.truncated = true
		kept = p[:max(remaining, 0)]
	}

	b.buf.Write(kept)

	// Report the full length: discarded bytes count as written, otherwise exec treats it as a
	// short write and fails the command.
	return len(p), nil
}

// String returns the kept content, marked as truncated if bytes were discarded.
func (b *cappedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + " … (truncated)"
	}

	return b.buf.String()
}

// processEnv returns the minimal environment for the typst process. Only PATH is passed
// through; the proxy variables point to an unreachable address to block package downloads.
func processEnv() []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}

	return []string{
		"PATH=" + path,
		"HTTPS_PROXY=" + unreachableProxy,
		"HTTP_PROXY=" + unreachableProxy,
		"https_proxy=" + unreachableProxy,
		"http_proxy=" + unreachableProxy,
	}
}
