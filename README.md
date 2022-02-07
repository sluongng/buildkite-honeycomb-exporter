# BuilKite Honeycomb Exporter

A quick scraper program that let you export builds on BuildKite as OpenTelemetry data
and then send them to honeycomb.io for slice-n-dice high cardinality analysis.

This program is using 100% OpenTelemetry SDK so Honeycomb can be swapped out with any
Tracing/Metrics/APM providers that support OpenTelemetry protocol (DataDog, Grafana Tempo etc...)

![image](https://user-images.githubusercontent.com/26684313/152633754-e83b05f1-f552-4afd-b7bd-1c0405d7839a.png)

![image](https://user-images.githubusercontent.com/26684313/152633824-804545e6-3e18-4193-b4f3-bff8202842b6.png)

This was built as a quick POC / MVP for my daily use cases but PRs/Issues are more than welcome.

## Push vs Pull

It's definitely more efficient to push traces on each pipeline run than
to poll for build result and creating cache manually each run.  There are
several BuildKite plugins that enable generating traces via different hooks.

However, I picked polling approach instead for these reasons:

1. Keep It Simple and Stupid(KISS):
   This allows us to keep the complexity of creating traces away from our pipeline logic
   and thus, reduce the complexity in maintaining an already complex system.

2. Being able to get past/old data:
   This polling approach allow us to send traces for old data as well as new.
   Having old data is valuable to use as base line of comparision.

3. Being able to create traces without wasting CI compute power:
   In the future, we might want to analyze and add our profiling artifacts as traces.
   These artifacts can be heavy and take time to analyze.
   Decoupling the creation of profile-traces way from CI pipeline keep the pipeline fast and performance.

There are definitely some cons to this approach:

1. Developers cannot freely add more traces to the pipeline.

2. Require separate compute to run this exporter.

Feel free to pick the tradeoffs that is right for your use case.

## Credits

Totally inspired by https://github.com/zoidbergwill/gitlab-honeycomb-buildevents-webhooks-sink
