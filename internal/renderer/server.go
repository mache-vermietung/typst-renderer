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
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tobima/domio/internal/infrastructure/typst"
	rendererv1 "github.com/tobima/domio/internal/renderer/proto/renderer/v1"
)

const (
	tracerName = "domio/renderer"
	// messageOverheadBytes is the headroom on top of the payload limits for the protobuf framing
	// (field tags, lengths, map keys).
	messageOverheadBytes = 1 << 20
)

// GRPCServerOptions aligns the gRPC message size limits with the configured request limit and the
// typst output limit. Without them gRPC rejects every message above 4 MiB before the handler runs.
func GRPCServerOptions(cfg Config) []grpc.ServerOption {
	maxRequest := cfg.MaxRequestBytes
	if maxRequest <= 0 {
		maxRequest = DefaultMaxRequestBytes
	}

	return []grpc.ServerOption{
		grpc.MaxRecvMsgSize(int(maxRequest + messageOverheadBytes)),
		grpc.MaxSendMsgSize(int(typst.DefaultMaxOutputBytes + messageOverheadBytes)),
	}
}

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

	var outBytes int64
	if resp != nil {
		outBytes = int64(len(resp.GetPdf()))
	}

	s.finish(ctx, span, "Render", start, inBytes, outBytes, err)

	if err != nil {
		return nil, err
	}

	return resp, nil
}

// Query executes a Typst query against the template and data.
func (s *Server) Query(ctx context.Context, req *rendererv1.QueryRequest) (*rendererv1.QueryResponse, error) {
	start := time.Now()

	ctx, span := s.tracer.Start(ctx, "Renderer.Query")
	defer span.End()

	inBytes := s.calculateRequestBytes(req.GetEntrypoint(), req.GetFiles(), req.GetDataJson())

	resp, err := s.handleQuery(ctx, req, inBytes)

	var outBytes int64
	if resp != nil {
		outBytes = int64(len(resp.GetDataJson()))
	}

	s.finish(ctx, span, "Query", start, inBytes, outBytes, err)

	if err != nil {
		return nil, err
	}

	return resp, nil
}

// finish records metrics, span and the log line of a request. Typst diagnostics can quote template
// source and data (personal data): like the local adapter they stay in the span's error event and
// the status message for the caller, but not in the log or the span status.
func (s *Server) finish(ctx context.Context, span trace.Span, method string, start time.Time, inBytes, outBytes int64, err error) {
	duration := time.Since(start)
	st := status.Convert(err)
	reason := rendererv1.ReasonOf(st)

	s.metrics.recordRequest(ctx, method, st.Code().String(), duration.Seconds(), inBytes, outBytes)

	if err == nil {
		slog.InfoContext(ctx, "renderer request succeeded",
			slog.String("method", method),
			slog.Int64("request_bytes", inBytes),
			slog.Int64("response_bytes", outBytes),
			slog.Duration("duration", duration),
		)

		return
	}

	message := st.Message()
	if reason == rendererv1.ErrorReason_ERROR_REASON_COMPILE_FAILED {
		message = "typst compilation failed"
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, message)
	slog.ErrorContext(ctx, "renderer request failed",
		slog.String("method", method),
		slog.String("code", st.Code().String()),
		slog.String("reason", reason.String()),
		slog.String("error", message),
		slog.Int64("request_bytes", inBytes),
		slog.Duration("duration", duration),
	)
}

func (s *Server) handleRender(ctx context.Context, req *rendererv1.RenderRequest, inBytes int64) (*rendererv1.RenderResponse, error) {
	release, err := s.admit(req.GetEntrypoint(), req.GetFiles(), inBytes)
	if err != nil {
		return nil, err
	}
	defer release()

	timeout := s.calculateTimeout(ctx)

	compileCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opts := s.compileOptions(timeout)
	opts.PDFStandard = req.GetPdfStandard()

	in := typst.Input{
		Entrypoint: req.GetEntrypoint(),
		Files:      req.GetFiles(),
		DataJSON:   req.GetDataJson(),
	}

	pdfBytes, err := typst.Compile(compileCtx, opts, in)
	if err != nil {
		return nil, s.mapTypstError(ctx, err)
	}

	return &rendererv1.RenderResponse{Pdf: pdfBytes}, nil
}

func (s *Server) handleQuery(ctx context.Context, req *rendererv1.QueryRequest, inBytes int64) (*rendererv1.QueryResponse, error) {
	if strings.TrimSpace(req.GetSelector()) == "" {
		return nil, invalidBundle("selector is required")
	}

	release, err := s.admit(req.GetEntrypoint(), req.GetFiles(), inBytes)
	if err != nil {
		return nil, err
	}
	defer release()

	timeout := s.calculateTimeout(ctx)

	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	in := typst.Input{
		Entrypoint: req.GetEntrypoint(),
		Files:      req.GetFiles(),
		DataJSON:   req.GetDataJson(),
	}

	dataBytes, err := typst.Query(queryCtx, s.compileOptions(timeout), in, req.GetSelector())
	if err != nil {
		return nil, s.mapTypstError(ctx, err)
	}

	return &rendererv1.QueryResponse{DataJson: dataBytes}, nil
}

// admit checks the request size and the bundle and takes a concurrency slot; the returned release
// frees the slot.
func (s *Server) admit(entrypoint string, files map[string][]byte, inBytes int64) (release func(), err error) {
	if s.config.MaxRequestBytes > 0 && inBytes > s.config.MaxRequestBytes {
		return nil, rendererv1.NewStatusError(grpccodes.ResourceExhausted, rendererv1.ErrorReason_ERROR_REASON_REQUEST_TOO_LARGE,
			fmt.Sprintf("request size %d exceeds limit of %d bytes", inBytes, s.config.MaxRequestBytes))
	}

	if err := s.validateBundle(entrypoint, files); err != nil {
		return nil, invalidBundle(err.Error())
	}

	select {
	case s.sem <- struct{}{}:
		return func() { <-s.sem }, nil
	default:
		return nil, rendererv1.NewStatusError(grpccodes.ResourceExhausted, rendererv1.ErrorReason_ERROR_REASON_CONCURRENCY_LIMIT,
			fmt.Sprintf("concurrency limit exceeded (max: %d)", cap(s.sem)))
	}
}

func (s *Server) compileOptions(timeout time.Duration) typst.CompileOptions {
	return typst.CompileOptions{
		BinaryPath:  s.config.TypstBinaryPath,
		PackagePath: s.config.PackagePath,
		FontPath:    s.config.FontPath,
		Timeout:     timeout,
	}
}

func (s *Server) calculateRequestBytes(entrypoint string, files map[string][]byte, dataJSON []byte) int64 {
	total := int64(len(entrypoint) + len(dataJSON))
	for k, v := range files {
		total += int64(len(k) + len(v))
	}

	return total
}

func (s *Server) validateBundle(entrypoint string, files map[string][]byte) error {
	if strings.TrimSpace(entrypoint) == "" {
		return ErrEntrypointRequired
	}

	if !filepath.IsLocal(entrypoint) {
		return fmt.Errorf("%w: %q", ErrInvalidEntrypointPath, entrypoint)
	}

	if _, ok := files[entrypoint]; !ok {
		return fmt.Errorf("%w: %q", ErrEntrypointNotFound, entrypoint)
	}

	for path := range files {
		if !filepath.IsLocal(path) {
			return fmt.Errorf("%w: %q", ErrInvalidFilePath, path)
		}
	}

	return nil
}

func (s *Server) calculateTimeout(ctx context.Context) time.Duration {
	maxTimeout := time.Duration(s.config.MaxTimeoutSeconds) * time.Second
	if maxTimeout <= 0 {
		maxTimeout = DefaultMaxTimeoutSeconds * time.Second
	}

	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < maxTimeout {
			return remaining
		}
	}

	return maxTimeout
}

func invalidBundle(msg string) error {
	return rendererv1.NewStatusError(grpccodes.InvalidArgument, rendererv1.ErrorReason_ERROR_REASON_INVALID_BUNDLE, msg)
}

// mapTypstError translates a typst error into a status with code and ErrorReason. ctx is the
// request context: a caller that went away gets Canceled instead of a compile failure.
func (s *Server) mapTypstError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return status.Error(grpccodes.Canceled, "request canceled by the caller")
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, typst.ErrCompileTimeout):
		return rendererv1.NewStatusError(grpccodes.DeadlineExceeded, rendererv1.ErrorReason_ERROR_REASON_TIMEOUT, err.Error())
	case errors.Is(err, typst.ErrOutputTooLarge):
		return rendererv1.NewStatusError(grpccodes.ResourceExhausted, rendererv1.ErrorReason_ERROR_REASON_OUTPUT_TOO_LARGE, err.Error())
	case errors.Is(err, typst.ErrInvalidInput):
		return invalidBundle(err.Error())
	case errors.Is(err, typst.ErrCompileFailed):
		return rendererv1.NewStatusError(grpccodes.InvalidArgument, rendererv1.ErrorReason_ERROR_REASON_COMPILE_FAILED, err.Error())
	case errors.Is(err, typst.ErrBinaryNotFound):
		return status.Errorf(grpccodes.Internal, "typst binary not found: %v", err)
	default:
		return status.Errorf(grpccodes.Internal, "rendering failed: %v", err)
	}
}
