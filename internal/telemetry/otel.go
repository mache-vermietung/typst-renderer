package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// InitTracer initializes tracing for the "domio-api" service.
func InitTracer(ctx context.Context) (*sdktrace.TracerProvider, error) {
	return InitTracerForService(ctx, "domio-api")
}

// InitTracerForService initializes tracing for the given service name, so that
// additional binaries (e.g. cmd/portal-bff) show up distinctly in traces.
func InitTracerForService(ctx context.Context, serviceName string) (*sdktrace.TracerProvider, error) {
	// 1. Der Exporter liest automatisch OTEL_EXPORTER_OTLP_ENDPOINT etc
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Resource liest automatisch OTEL_SERVICE_NAME und OTEL_RESOURCE_ATTRIBUTES
	res, err := serviceResource(ctx, serviceName)
	if err != nil {
		return nil, err
	}

	// 3. Provider zusammenbauen
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp, nil
}
