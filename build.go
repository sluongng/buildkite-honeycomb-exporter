package main

import (
	"context"
	"fmt"
	"log"

	"github.com/buildkite/go-buildkite/v3/buildkite"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func (d *daemon) processBuild(ctx context.Context, b buildkite.Build) {
	defer d.wg.Done()

	log.Printf("processing build %d finished at %s", *b.Number, b.FinishedAt)

	if b.StartedAt == nil || b.FinishedAt == nil {
		return
	}

	// create build span
	buildCtx, buildSpan := d.tracer.Start(ctx, fmt.Sprintf("%d", *b.Number), trace.WithTimestamp(b.StartedAt.Time))

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
		switch m := b.MetaData.(type) {
		// this cannot be casted directly to map[string]string
		case map[string]interface{}:
			for k, v := range m {
				switch val := v.(type) {
				case string:
					buildSpan.SetAttributes(attribute.String("build_"+k, val))
				default:
				}
			}
		default:
		}
	}

	// create job spans
	for _, j := range b.Jobs {
		d.processJob(buildCtx, *b.ID, j)
	}

	buildSpan.End(trace.WithTimestamp(b.FinishedAt.Time))
}
