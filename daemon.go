package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/buildkite/go-buildkite/v3/buildkite"
	"go.opentelemetry.io/otel/trace"
)

// Honeycomb has max data retention of 60 days
// so we should not need to send more than that worth of data
//
// For subsequent runs, only query from when last run left off
var lastFinishedAt = time.Now().AddDate(0, -2, 0)

const CachePath = "/tmp/buildkite-id-cache.txt"

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
