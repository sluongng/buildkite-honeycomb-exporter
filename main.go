package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
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

func loadCache(cacheFile *os.File) map[string]struct{} {
	result := make(map[string]struct{})
	scanner := bufio.NewScanner(cacheFile)
	for scanner.Scan() {
		result[scanner.Text()] = struct{}{}
	}

	fmt.Printf("loading cache: %d lines\n", len(result))

	return result
}

func writeCache(cacheBuildIDs map[string]struct{}, cacheFile *os.File) error {
	err := cacheFile.Truncate(0)
	if err != nil {
		return fmt.Errorf("error truncating cache: %v", err)
	}
	_, err = cacheFile.Seek(0, 0)
	if err != nil {
		return fmt.Errorf("error seeking cache: %v", err)
	}

	w := bufio.NewWriter(cacheFile)
	defer w.Flush()

	for k := range cacheBuildIDs {
		_, err := w.WriteString(k + "\n")
		if err != nil {
			return fmt.Errorf("error writing cache: %v", err)
		}
	}

	return nil
}

// Honeycomb has max data retention of 60 days
// so we should not need to send more than that worth of data
//
// For subsequent runs, only query from when last run left off
var lastFinishedAt = time.Now().AddDate(0, -2, 0)

const CachePath = "/tmp/buildkite-id-cache.txt"

// BuildKite pagination loop
func processBuildKite(ctx context.Context, bk *buildkite.Client, tracer trace.Tracer) {
	// load cache file on each run
	f, err := os.OpenFile(CachePath, os.O_RDWR|os.O_CREATE, 0775)
	if err != nil {
		log.Fatalf("could not open cache file: %v\n", err)
	}
	defer f.Close()

	cachedBuildIDs := loadCache(f)

	// track what is the latest build finish time
	runFinishedAt := lastFinishedAt

	wg := &sync.WaitGroup{}
	page := 1
	for {
		buildListOptions := &buildkite.BuildsListOptions{
			FinishedFrom: lastFinishedAt,
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
			log.Printf("Issues calling BuildKite API: %v\n", err)
			// TODO: backoff retry with retry limit?
			continue
		}
		for _, b := range builds {
			if _, ok := cachedBuildIDs[*b.ID]; ok {
				// build ID is in cache, skip processing
				log.Println("Skipping build:", *b.ID)
				continue
			}

			// add build ID to cache
			cachedBuildIDs[*b.ID] = struct{}{}

			if b.FinishedAt != nil && b.FinishedAt.After(runFinishedAt) {
				runFinishedAt = b.FinishedAt.Time
			}

			wg.Add(1)
			go processBuild(ctx, tracer, b, wg)
		}

		// use buildkite response header to determine next page
		if resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}

	// store all build IDs each run into cache
	err = writeCache(cachedBuildIDs, f)
	if err != nil {
		log.Fatalf("error writing cache: %v", err)
	}

	// Update future runs query starting point so that
	// we don't have to read 2 months worth each time.
	lastFinishedAt = runFinishedAt

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
	if b.WebURL != nil {
		buildSpan.SetAttributes(attribute.String("url", *b.WebURL))
	}
	// TODO: allow filtering metadata keys
	if b.MetaData != nil {
		if metadata, ok := b.MetaData.(map[string]string); ok {
			for k, v := range metadata {
				buildSpan.SetAttributes(attribute.String("build_"+k, v))
			}
		}
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

	// job metadata
	jSpan.SetAttributes(attribute.Int("retry_count", j.RetriesCount))
	jSpan.SetAttributes(attribute.Bool("retried", j.Retried))
	jSpan.SetAttributes(attribute.Bool("soft_failed", j.SoftFailed))
	if j.LogsURL != nil {
		jSpan.SetAttributes(attribute.String("url", *j.LogsURL))
	}
	if j.StepKey != nil {
		jSpan.SetAttributes(attribute.String("step_key", *j.StepKey))
	}
	if j.ExitStatus != nil {
		jSpan.SetAttributes(attribute.Int("exit_status", *j.ExitStatus))
	}

	// agent data
	if j.Agent.IPAddress != nil {
		jSpan.SetAttributes(attribute.String("agent_ip", *j.Agent.IPAddress))
	}
	if j.Agent.Version != nil {
		jSpan.SetAttributes(attribute.String("agent_version", *j.Agent.Version))
	}
	// TODO: allow filtering metadata keys
	for _, m := range j.Agent.Metadata {
		// Assuming that agent metadata are kv pairs separated by '='
		token := strings.Split(m, "=")
		if len(token) != 2 {
			continue
		}
		jSpan.SetAttributes(attribute.String("agent_"+token[0], token[1]))
	}

	jSpan.End(trace.WithTimestamp(j.FinishedAt.Time))
}
