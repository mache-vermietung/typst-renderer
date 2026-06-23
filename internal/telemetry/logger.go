package telemetry

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

// TraceContextHandler is a slog.Handler that extracts trace_id and span_id from context.Context.
type TraceContextHandler struct {
	slog.Handler
}

// NewTraceContextHandler wraps an existing slog.Handler.
func NewTraceContextHandler(next slog.Handler) *TraceContextHandler {
	return &TraceContextHandler{Handler: next}
}

// Handle appends the current trace ID and span ID to the log record if they are valid.
func (h *TraceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx == nil {
		return h.Handler.Handle(ctx, r)
	}

	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}

	return h.Handler.Handle(ctx, r)
}

// InitLogger initializes slog as the default logger using structured output and trace correlation.
func InitLogger() {
	level := slog.LevelInfo

	if lvlStr := os.Getenv("LOG_LEVEL"); lvlStr != "" {
		var l slog.Level
		if err := l.UnmarshalText([]byte(lvlStr)); err == nil {
			level = l
		}
	}

	var baseHandler slog.Handler

	opts := &slog.HandlerOptions{Level: level}

	if os.Getenv("LOG_FORMAT") == "text" {
		baseHandler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		baseHandler = slog.NewJSONHandler(os.Stdout, opts)
	}

	handler := NewTraceContextHandler(baseHandler)
	logger := slog.New(handler)
	slog.SetDefault(logger)
}
