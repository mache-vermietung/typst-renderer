package renderer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tobima/domio/internal/infrastructure/typst"
	rendererv1 "github.com/tobima/domio/internal/renderer/proto/renderer/v1"
)

const tracerName = "domio/renderer"

var (
	// ErrEntrypointRequired is returned when the entrypoint file path is empty.
	ErrEntrypointRequired = errors.New("entrypoint is required")
	// ErrInvalidEntrypointPath is returned when the entrypoint path traverses directories or is absolute.
	ErrInvalidEntrypointPath = errors.New("invalid entrypoint path")
	// ErrEntrypointNotFound is returned when the entrypoint file is not in the provided file map.
	ErrEntrypointNotFound = errors.New("entrypoint not found in files")
	// ErrInvalidFilePath is returned when a bundle file path traverses directories or is absolute.
	ErrInvalidFilePath = errors.New("invalid file path")
)

// Server implements the gRPC RendererService.
type Server struct {
	rendererv1.UnimplementedRendererServiceServer
	config  Config
	sem     chan struct{}
	tracer  trace.Tracer
	metrics *Metrics
}

// NewServer creates a new renderer gRPC Server instance.
func NewServer(cfg Config) (*Server, error) {
	metrics, err := NewMetrics()
	if err != nil {
		slog.Warn("failed to initialize renderer metrics", "error", err)
	}

	concurrency := cfg.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	return &Server{
		config:  cfg,
		sem:     make(chan struct{}, concurrency),
		tracer:  otel.Tracer(tracerName),
		metrics: metrics,
	}, nil
}

// Render compiles the requested template bundle and data into PDF bytes.
func (s *Server) Render(ctx context.Context, req *rendererv1.RenderRequest) (*rendererv1.RenderResponse, error) {
	start := time.Now()

	ctx, span := s.tracer.Start(ctx, "Renderer.Render")
	defer span.End()

	inBytes := s.calculateRequestBytes(req.GetEntrypoint(), req.GetFiles(), req.GetDataJson())

	resp, err := s.handleRender(ctx, req, inBytes)
	durationSec := time.Since(start).Seconds()

	st := status.Convert(err)

	var outBytes int64
	if resp != nil {
		outBytes = int64(len(resp.GetPdf()))
	}

	s.metrics.recordRequest(ctx, "Render", st.Code().String(), durationSec, inBytes, outBytes)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, st.Message())
		slog.ErrorContext(ctx, "render failed",
			slog.String("code", st.Code().String()),
			slog.String("error", st.Message()),
			slog.Int64("request_bytes", inBytes),
			slog.Duration("duration", time.Since(start)),
		)

		return nil, err
	}

	slog.InfoContext(ctx, "rendered document successfully",
		slog.Int64("request_bytes", inBytes),
		slog.Int64("response_bytes", outBytes),
		slog.Duration("duration", time.Since(start)),
	)

	return resp, nil
}

// Query executes a Typst query against the template and data.
func (s *Server) Query(ctx context.Context, req *rendererv1.QueryRequest) (*rendererv1.QueryResponse, error) {
	start := time.Now()

	ctx, span := s.tracer.Start(ctx, "Renderer.Query")
	defer span.End()

	inBytes := s.calculateRequestBytes(req.GetEntrypoint(), req.GetFiles(), req.GetDataJson())

	resp, err := s.handleQuery(ctx, req, inBytes)
	durationSec := time.Since(start).Seconds()

	st := status.Convert(err)

	var outBytes int64
	if resp != nil {
		outBytes = int64(len(resp.GetDataJson()))
	}

	s.metrics.recordRequest(ctx, "Query", st.Code().String(), durationSec, inBytes, outBytes)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, st.Message())
		slog.ErrorContext(ctx, "query failed",
			slog.String("code", st.Code().String()),
			slog.String("error", st.Message()),
			slog.Int64("request_bytes", inBytes),
			slog.Duration("duration", time.Since(start)),
		)

		return nil, err
	}

	return resp, nil
}

func (s *Server) handleRender(ctx context.Context, req *rendererv1.RenderRequest, inBytes int64) (*rendererv1.RenderResponse, error) {
	if s.config.MaxRequestBytes > 0 && inBytes > s.config.MaxRequestBytes {
		return nil, status.Errorf(grpccodes.ResourceExhausted, "request size %d exceeds limit of %d bytes", inBytes, s.config.MaxRequestBytes)
	}

	if err := s.validateBundle(req.GetEntrypoint(), req.GetFiles()); err != nil {
		return nil, status.Errorf(grpccodes.InvalidArgument, "%s", err.Error())
	}

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return nil, status.Errorf(grpccodes.ResourceExhausted, "concurrency limit exceeded (max: %d)", s.config.MaxConcurrency)
	}

	timeout := s.calculateTimeout(ctx)

	compileCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opts := typst.CompileOptions{
		BinaryPath:  s.config.TypstBinaryPath,
		PackagePath: s.config.PackagePath,
		FontPath:    s.config.FontPath,
		Timeout:     timeout,
	}

	in := typst.Input{
		Entrypoint: req.GetEntrypoint(),
		Files:      req.GetFiles(),
		DataJSON:   req.GetDataJson(),
	}

	pdfBytes, err := typst.Compile(compileCtx, opts, in)
	if err != nil {
		return nil, s.mapTypstError(err)
	}

	return &rendererv1.RenderResponse{Pdf: pdfBytes}, nil
}

func (s *Server) handleQuery(ctx context.Context, req *rendererv1.QueryRequest, inBytes int64) (*rendererv1.QueryResponse, error) {
	if s.config.MaxRequestBytes > 0 && inBytes > s.config.MaxRequestBytes {
		return nil, status.Errorf(grpccodes.ResourceExhausted, "request size %d exceeds limit of %d bytes", inBytes, s.config.MaxRequestBytes)
	}

	if err := s.validateBundle(req.GetEntrypoint(), req.GetFiles()); err != nil {
		return nil, status.Errorf(grpccodes.InvalidArgument, "%s", err.Error())
	}

	if strings.TrimSpace(req.GetSelector()) == "" {
		return nil, status.Error(grpccodes.InvalidArgument, "selector is required")
	}

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return nil, status.Errorf(grpccodes.ResourceExhausted, "concurrency limit exceeded (max: %d)", s.config.MaxConcurrency)
	}

	timeout := s.calculateTimeout(ctx)

	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opts := typst.CompileOptions{
		BinaryPath:  s.config.TypstBinaryPath,
		PackagePath: s.config.PackagePath,
		FontPath:    s.config.FontPath,
		Timeout:     timeout,
	}

	in := typst.Input{
		Entrypoint: req.GetEntrypoint(),
		Files:      req.GetFiles(),
		DataJSON:   req.GetDataJson(),
	}

	dataBytes, err := typst.Query(queryCtx, opts, in, req.GetSelector())
	if err != nil {
		return nil, s.mapTypstError(err)
	}

	return &rendererv1.QueryResponse{DataJson: dataBytes}, nil
}

func (s *Server) calculateRequestBytes(entrypoint string, files map[string][]byte, dataJSON []byte) int64 {
	total := int64(len(entrypoint) + len(dataJSON))
	for k, v := range files {
		total += int64(len(k) + len(v))
	}

	return total
}

func (s *Server) validateBundle(entrypoint string, files map[string][]byte) error {
	entrypoint = strings.TrimSpace(entrypoint)
	if entrypoint == "" {
		return ErrEntrypointRequired
	}

	if filepath.IsAbs(entrypoint) || strings.HasPrefix(entrypoint, "../") || strings.Contains(entrypoint, "/../") {
		return fmt.Errorf("%w: %q", ErrInvalidEntrypointPath, entrypoint)
	}

	if _, ok := files[entrypoint]; !ok {
		return fmt.Errorf("%w: %q", ErrEntrypointNotFound, entrypoint)
	}

	for path := range files {
		if filepath.IsAbs(path) || strings.HasPrefix(path, "../") || strings.Contains(path, "/../") {
			return fmt.Errorf("%w: %q", ErrInvalidFilePath, path)
		}
	}

	return nil
}

func (s *Server) calculateTimeout(ctx context.Context) time.Duration {
	maxTimeout := time.Duration(s.config.MaxTimeoutSeconds) * time.Second
	if maxTimeout <= 0 {
		maxTimeout = 20 * time.Second
	}

	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < maxTimeout {
			return remaining
		}
	}

	return maxTimeout
}

func (s *Server) mapTypstError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, typst.ErrCompileTimeout) {
		return status.Errorf(grpccodes.DeadlineExceeded, "rendering timed out: %v", err)
	}

	if errors.Is(err, typst.ErrOutputTooLarge) {
		return status.Errorf(grpccodes.ResourceExhausted, "%v", err)
	}

	if errors.Is(err, typst.ErrInvalidInput) {
		return status.Errorf(grpccodes.InvalidArgument, "%v", err)
	}

	if errors.Is(err, typst.ErrCompileFailed) {
		return status.Errorf(grpccodes.InvalidArgument, "compilation failed: %v", err)
	}

	if errors.Is(err, typst.ErrBinaryNotFound) {
		return status.Errorf(grpccodes.Internal, "typst binary not found: %v", err)
	}

	return status.Errorf(grpccodes.Internal, "rendering failed: %v", err)
}
