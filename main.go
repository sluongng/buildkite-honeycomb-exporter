package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/buildkite/go-buildkite/v3/buildkite"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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

	HoneycombEndPoint    = "api.honeycomb.io:443"
	HoneycombDataSetName = "buildkite-pipelines"
	HoneycombHeaders     = map[string]string{
		"x-honeycomb-team":    os.Getenv("HONEYCOMB_API_KEY"),
		"x-honeycomb-dataset": HoneycombDataSetName,
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
	resource :=
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(ServiceName),
			semconv.ServiceVersionKey.String(ServiceVersion),
		)

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resource),
	)
}

// init opentelemetry sdk setup
func initOtel(ctx context.Context) trace.Tracer {
	// Init otel
	exporter, err := newExporter(ctx)
	if err != nil {
		log.Fatalf("failed to initialize exporter: %v", err)
	}
	tp := newTraceProvider(exporter)
	// Handle shutdown for Trace Provider
	defer func() { _ = tp.Shutdown(ctx) }()

	return tp.Tracer("BuildKiteExporter")
}

// init buildkite client
func initBuildKiteClient() *buildkite.Client {
	config, err := buildkite.NewTokenConfig(BuildKiteApiToken, false)
	if err != nil {
		log.Fatal(err)
	}

	return buildkite.NewClient(config.Client())
}

func main() {
	// init bk client
	ctx := context.Background()

	bk := initBuildKiteClient()
	tracer := initOtel(ctx)

	// BuildKite pagination loop
	wg := &sync.WaitGroup{}
	page := 1
	for {
		buildListOptions := &buildkite.BuildsListOptions{
			// Honeycomb has max data retention of 60 days
			// we should not need to send more than that worth of data
			FinishedFrom: time.Now().AddDate(0, -2, 0),
			// Possible values are: running, scheduled, passed, failed, canceled, skipped and not_run.
			// filters for only 'finished' states
			State: []string{"passed", "failed", "canceled", "skipped"},
			ListOptions: buildkite.ListOptions{
				Page:    page,
				PerPage: BuildKiteMaxPagination,
			},
		}

		log.Println("Calling API on page", page)
		builds, resp, err := bk.Builds.ListByPipeline(BuildKiteOrgName, BuildKitePipelineName, buildListOptions)
		if err != nil {
			log.Fatal(err)
		}
		for _, b := range builds {
			wg.Add(1)
			go processBuild(ctx, tracer, b, wg)
		}

		// use buildkite response header to determine next page
		if resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}

	// ensure all workers are finished
	wg.Wait()
}

func processBuild(ctx context.Context, tracer trace.Tracer, b buildkite.Build, wg *sync.WaitGroup) {
	defer wg.Done()

	log.Printf("processing build %d finished at %s", *b.Number, b.FinishedAt)

	if b.StartedAt == nil || b.FinishedAt == nil {
		return
	}

	// create build span
	buildCtx, buildSpan := tracer.Start(ctx, fmt.Sprintf("%d", *b.Number), trace.WithTimestamp(b.StartedAt.Time))

	// build timing
	buildSpan.AddEvent("created", trace.WithTimestamp(b.StartedAt.Time))
	if b.ScheduledAt != nil {
		buildSpan.AddEvent("scheduled", trace.WithTimestamp(b.ScheduledAt.Time))
	}

	// build state
	if b.State != nil {
		buildSpan.SetAttributes(attribute.String("state", *b.State))
		switch *b.State {
		case "failed":
			buildSpan.SetStatus(codes.Error, *b.State)
		case "passed", "finished":
			buildSpan.SetStatus(codes.Ok, *b.State)
		default:
			buildSpan.SetStatus(codes.Unset, *b.State)
		}
	}

	// build metadata
	if b.Commit != nil {
		buildSpan.SetAttributes(attribute.String("commit", *b.Commit))
	}
	if b.Branch != nil {
		buildSpan.SetAttributes(attribute.String("branch", *b.Branch))
	}
	if b.Author != nil {
		buildSpan.SetAttributes(attribute.String("author", b.Author.Email))
	}

	// create job spans
	for _, j := range b.Jobs {
		processJob(buildCtx, tracer, j)
	}

	buildSpan.End(trace.WithTimestamp(b.FinishedAt.Time))
}

func processJob(ctx context.Context, tracer trace.Tracer, j *buildkite.Job) {
	if j.StartedAt == nil || j.FinishedAt == nil {
		return
	}

	// agent timing
	_, jSpan := tracer.Start(ctx, *j.Name, trace.WithTimestamp(j.StartedAt.Time))
	jSpan.AddEvent("created", trace.WithTimestamp(j.CreatedAt.Time))
	if j.ScheduledAt != nil {
		jSpan.AddEvent("scheduled", trace.WithTimestamp(j.ScheduledAt.Time))
	}
	if j.RunnableAt != nil {
		jSpan.AddEvent("runnable", trace.WithTimestamp(j.RunnableAt.Time))
	}

	// agent state
	if j.State != nil {
		jSpan.SetAttributes(attribute.String("state", *j.State))
		switch *j.State {
		case "failed":
			jSpan.SetStatus(codes.Error, *j.State)
		case "passed", "finished":
			jSpan.SetStatus(codes.Ok, *j.State)
		default:
			jSpan.SetStatus(codes.Unset, *j.State)
		}
	}

	// agent data
	if j.Agent.IPAddress != nil {
		jSpan.SetAttributes(attribute.String("agent_ip", *j.Agent.IPAddress))
	}
	if j.Agent.Version != nil {
		jSpan.SetAttributes(attribute.String("agent_version", *j.Agent.Version))
	}

	jSpan.End(trace.WithTimestamp(j.FinishedAt.Time))
}
