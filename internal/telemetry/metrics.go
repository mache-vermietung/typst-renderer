package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Environment variables that enable the metric export.
const (
	envOTLPEndpoint        = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPMetricsEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
)

// ShutdownFunc flushes and stops a provider.
type ShutdownFunc func(ctx context.Context) error

// InitMeterProvider sets up the global MeterProvider with an OTLP/HTTP exporter for the given
// service. The exporter is configured through the standard OTEL_* environment variables
// (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_METRICS_ENDPOINT); the export interval follows
// OTEL_METRIC_EXPORT_INTERVAL (milliseconds, SDK default 60000). Without an endpoint metrics stay
// a no-op and the daemon starts unchanged.
func InitMeterProvider(ctx context.Context, serviceName string) (ShutdownFunc, error) {
	if os.Getenv(envOTLPEndpoint) == "" && os.Getenv(envOTLPMetricsEndpoint) == "" {
		otel.SetMeterProvider(noop.NewMeterProvider())
		slog.InfoContext(ctx, "OTLP endpoint not set, metrics export disabled")

		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}

	res, err := serviceResource(ctx, serviceName)
	if err != nil {
		return nil, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	return mp.Shutdown, nil
}

func serviceResource(ctx context.Context, serviceName string) (*resource.Resource, error) {
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithAttributes(semconv.ServiceNameKey.String(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("create telemetry resource: %w", err)
	}

	return res, nil
}
