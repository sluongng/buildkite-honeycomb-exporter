package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/buildkite/go-buildkite/v3/buildkite"
	"go.opentelemetry.io/otel/trace"
)

// daemon contains all the info needed by the goroutines inside the long-lived process
type daemon struct {
	lastFinishedAt time.Time
	tracer         trace.Tracer
	buildKite      *buildkite.Client
	wg             *sync.WaitGroup
	cacheFilePath  string
	sleepDuration  time.Duration
}

// NewDaemon produce daemon struct that can be executed as a long-lived process
func NewDaemon(
	tracer trace.Tracer,
	buildKite *buildkite.Client,
	sleepDuration time.Duration,
	cacheFilePath string,
) *daemon {
	wg := &sync.WaitGroup{}

	// Default to HoneycombMaxRetention on initial run
	// should be updated on subsequent runs
	lastFinishedAt := time.Now().Add(-1 * HoneycombMaxRetention)

	return &daemon{
		lastFinishedAt: lastFinishedAt,
		tracer:         tracer,
		buildKite:      buildKite,
		wg:             wg,
		sleepDuration:  sleepDuration,
		cacheFilePath:  cacheFilePath,
	}
}

// Exec execute the daemon as a long-lived process
func (d *daemon) Exec(ctx context.Context) {
	// TODO: implement graceful shutdown when SIGTERM/SIGKILL
	for {
		d.processBuildKite(ctx)

		log.Printf("sleeping for %s", d.sleepDuration)
		time.Sleep(d.sleepDuration)
	}
}

// BuildKite pagination loop
func (d *daemon) processBuildKite(ctx context.Context) {
	cache := NewCache(d.cacheFilePath)
	defer cache.fileStore.Close()

	cachedBuildIDs := cache.loadCache()

	buildListOptions := &buildkite.BuildsListOptions{
		// Only query from last run's cut off point to limit the number of
		// requests needed on subsequent runs.
		FinishedFrom: d.lastFinishedAt,
		// Possible values are: running, scheduled, passed, failed, canceled, skipped and not_run.
		// filters for only 'finished' states
		State: []string{"passed", "failed", "canceled", "skipped"},
		// Pagination options
		ListOptions: buildkite.ListOptions{
			Page:    1,
			PerPage: BuildKiteMaxPagination,
		},
	}
	for {
		log.Println("Calling API on page", buildListOptions.Page)
		builds, resp, err := d.buildKite.Builds.ListByPipeline(BuildKiteOrgName, BuildKitePipelineName, buildListOptions)
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

			if b.FinishedAt != nil && b.FinishedAt.After(d.lastFinishedAt) {
				d.lastFinishedAt = b.FinishedAt.Time
			}

			d.wg.Add(1)
			go d.processBuild(ctx, b)
		}

		// use buildkite response header to determine next page
		if resp.NextPage == 0 {
			break
		}

		buildListOptions.Page = resp.NextPage
	}

	// store all build IDs each run into cache
	err := cache.writeCache(cachedBuildIDs)
	if err != nil {
		log.Fatalf("error writing cache: %v", err)
	}

	// ensure all workers are finished
	d.wg.Wait()
}
