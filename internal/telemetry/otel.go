package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// InitTracerForService initializes tracing for the given service name. The OTLP exporter and the
// resource are configured through the standard OTEL_* environment variables.
func InitTracerForService(ctx context.Context, serviceName string) (*sdktrace.TracerProvider, error) {
	// The exporter reads OTEL_EXPORTER_OTLP_ENDPOINT and related variables itself.
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}

	// The resource honours OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES.
	res, err := serviceResource(ctx, serviceName)
	if err != nil {
		return nil, err
	}

	// Assemble the provider.
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
