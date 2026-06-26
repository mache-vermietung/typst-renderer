package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InitTracer ...
func InitTracer(ctx context.Context) (*sdktrace.TracerProvider, error) {
	// 1. Der Exporter liest automatisch OTEL_EXPORTER_OTLP_ENDPOINT etc
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Resource liest automatisch OTEL_SERVICE_NAME und OTEL_RESOURCE_ATTRIBUTES
	res, err := resource.New(ctx,
		resource.WithFromEnv(), // Liest Umgebungsvariablen
		resource.WithAttributes(
			semconv.ServiceNameKey.String("domio-api"),
			semconv.ServiceVersionKey.String("0.1.0"),
		),
	)
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
