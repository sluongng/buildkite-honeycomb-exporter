package main

import (
	"context"
	"log"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/credentials"
)

func newExporter(ctx context.Context) (*otlptrace.Exporter, error) {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(HoneycombEndPoint),
		otlptracegrpc.WithHeaders(HoneycombHeaders),
		otlptracegrpc.WithTLSCredentials(credentials.NewClientTLSFromCert(nil, "")),
	}

	client := otlptracegrpc.NewClient(opts...)
	return otlptrace.New(ctx, client)
}

// newTraceProvider create a trace provider
func newTraceProvider(exp *otlptrace.Exporter) *sdktrace.TracerProvider {
	// The service.name attribute is required.
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(ServiceName),
		semconv.ServiceVersionKey.String(ServiceVersion),
	)

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
}

// newDebugTracerProvider creates a trace provider that will print all traces as
// JSON to stdout.  Intended for development purposes only.
//
// User can simply replace newTraceProvider() call with this.
func newDebugTracerProvider() *sdktrace.TracerProvider {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatal("Could not init debug exporter", err)
	}

	return sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
}

// initOtel returns a tracer object and a function that help handler graceful shutdown
func initOtel(ctx context.Context, serviceName string) (trace.Tracer, func()) {
	// Init otel
	exporter, err := newExporter(ctx)
	if err != nil {
		log.Fatalf("failed to initialize exporter: %v\n", err)
	}

	tp := newTraceProvider(exporter)

	return tp.Tracer(serviceName), func() { _ = tp.Shutdown(ctx) }
}
