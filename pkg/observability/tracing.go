// Package observability bootstraps the OpenTelemetry tracer provider (F1-B):
// HTTP → pgx → outbound-webapi spans exported over OTLP/HTTP to a lightweight
// self-hosted backend (Tempo).
package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const traceBatchTimeout = 5 * time.Second

// TracingConfig carries everything InitTracing needs; zero Enabled = no-op.
type TracingConfig struct {
	Enabled     bool
	Endpoint    string // OTLP/HTTP base, e.g. http://tempo:4318
	SampleRatio float64
	ServiceName string
	Environment string
	Version     string
}

// InitTracing installs the global tracer provider and returns its shutdown
// hook. Disabled config returns a no-op shutdown and leaves the default
// (no-op) provider in place, so instrumentation stays zero-cost.
func InitTracing(ctx context.Context, cfg *TracingConfig) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(cfg.Endpoint))
	if err != nil {
		return nil, fmt.Errorf("observability - InitTracing - exporter: %w", err)
	}

	// Schemaless on purpose: merging with resource.Default() fails whenever
	// the SDK's schema version differs from the semconv import (bit us live:
	// "conflicting Schema URL 1.41.0 vs 1.26.0" crash-looped the app).
	res := resource.NewSchemaless(
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.Version),
		semconv.DeploymentEnvironment(cfg.Environment),
	)

	ratio := cfg.SampleRatio
	if ratio <= 0 || ratio > 1 {
		ratio = 1
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(traceBatchTimeout)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	return provider.Shutdown, nil
}
