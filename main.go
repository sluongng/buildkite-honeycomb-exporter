package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/buildkite/go-buildkite/v3/buildkite"
)

var (
	ServiceVersion   = "v0.0.1"
	ServiceName      = "BuildKiteExporter"
	ServiceCachePath = "/tmp/buildkite-id-cache.txt"

	BuildKiteApiToken      = os.Getenv("BUILDKITE_TOKEN")
	BuildKiteOrgName       = os.Getenv("BUILDKITE_ORG")
	BuildKitePipelineName  = os.Getenv("BUILDKITE_PIPELINE")
	BuildKiteMaxPagination = 100

	HoneycombEndPoint = "api.honeycomb.io:443"
	HoneycombHeaders  = map[string]string{
		"x-honeycomb-team":    os.Getenv("HONEYCOMB_API_KEY"),
		"x-honeycomb-dataset": os.Getenv("HONEYCOMB_DATASET"),
	}
	HoneycombMaxRetention = 60 * 24 * time.Hour
)

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

	tracer, shutdown := initOtel(ctx, ServiceName)
	defer shutdown()

	sleepDuration := 15 * time.Minute

	pipelines := strings.Split(BuildKitePipelineName, ",")

	NewDaemon(tracer, bk, pipelines, sleepDuration, ServiceCachePath).Exec(ctx)
}
