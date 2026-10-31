package renderer

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "domio/renderer"

// Metrics holds the OpenTelemetry metrics instruments for the renderer service.
type Metrics struct {
	requestsTotal          metric.Int64Counter
	requestDurationSeconds metric.Float64Histogram
	requestSizeBytes       metric.Int64Histogram
	responseSizeBytes      metric.Int64Histogram
}

// NewMetrics initializes and registers OpenTelemetry metrics instruments for the renderer.
func NewMetrics() (*Metrics, error) {
	meter := otel.GetMeterProvider().Meter(meterName)

	reqTotal, err := meter.Int64Counter(
		"renderer_requests_total",
		metric.WithDescription("Total number of renderer requests handled"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, err
	}

	reqDuration, err := meter.Float64Histogram(
		"renderer_request_duration_seconds",
		metric.WithDescription("Duration of document rendering requests in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	reqSize, err := meter.Int64Histogram(
		"renderer_request_size_bytes",
		metric.WithDescription("Size of incoming renderer requests in bytes"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	respSize, err := meter.Int64Histogram(
		"renderer_response_size_bytes",
		metric.WithDescription("Size of generated renderer responses in bytes"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	return &Metrics{
		requestsTotal:          reqTotal,
		requestDurationSeconds: reqDuration,
		requestSizeBytes:       reqSize,
		responseSizeBytes:      respSize,
	}, nil
}

func (m *Metrics) recordRequest(ctx context.Context, method, code string, durationSec float64, inBytes, outBytes int64) {
	if m == nil {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.String("method", method),
		attribute.String("status_code", code),
	}

	m.requestsTotal.Add(ctx, 1, metric.WithAttributes(attrs...))
	m.requestDurationSeconds.Record(ctx, durationSec, metric.WithAttributes(attrs...))
	m.requestSizeBytes.Record(ctx, inBytes, metric.WithAttributes(attribute.String("method", method)))

	if outBytes > 0 {
		m.responseSizeBytes.Record(ctx, outBytes, metric.WithAttributes(attribute.String("method", method)))
	}
}
