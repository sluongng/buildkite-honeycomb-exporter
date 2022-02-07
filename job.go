package main

import (
	"context"
	"strings"

	"github.com/buildkite/go-buildkite/v3/buildkite"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

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
