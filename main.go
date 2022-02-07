package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/buildkite/go-buildkite/v3/buildkite"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
	"go.opentelemetry.io/otel/trace"

	"google.golang.org/grpc/credentials"
)

var (
	ServiceVersion = "v0.0.1"
	ServiceName    = "BuildKiteExporter"

	BuildKiteApiToken      = os.Getenv("BUILDKITE_TOKEN")
	BuildKiteOrgName       = os.Getenv("BUILDKITE_ORG")
	BuildKitePipelineName  = os.Getenv("BUILDKITE_PIPELINE")
	BuildKiteMaxPagination = 100

	HoneycombEndPoint = "api.honeycomb.io:443"
	HoneycombHeaders  = map[string]string{
		"x-honeycomb-team":    os.Getenv("HONEYCOMB_API_KEY"),
		"x-honeycomb-dataset": os.Getenv("HONEYCOMB_DATASET"),
	}
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

// initOtel returns a tracer object and a function that help handler graceful shutdown
func initOtel(ctx context.Context) (trace.Tracer, func()) {
	// Init otel
	exporter, err := newExporter(ctx)
	if err != nil {
		log.Fatalf("failed to initialize exporter: %v\n", err)
	}

	tp := newTraceProvider(exporter)

	return tp.Tracer(ServiceName), func() { _ = tp.Shutdown(ctx) }
}

// init buildkite client
func initBuildKiteClient() *buildkite.Client {
	config, err := buildkite.NewTokenConfig(BuildKiteApiToken, false)
	if err != nil {
		log.Fatalf("failed to init BuildKite client: %v\n", err)
	}

	return buildkite.NewClient(config.Client())
}

func main() {
	// init bk client
	ctx := context.Background()

	bk := initBuildKiteClient()

	tracer, shutdown := initOtel(ctx)
	defer shutdown()

	sleepDuration := 15 * time.Minute
	for {
		// TODO: implement graceful shutdown when SIGTERM/SIGKILL
		processBuildKite(ctx, bk, tracer)

		log.Printf("sleeping for %s", sleepDuration)
		time.Sleep(sleepDuration)
	}
}
