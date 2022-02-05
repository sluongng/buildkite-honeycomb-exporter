# BuilKite Honeycomb Exporter

A quick scraper program that let you export builds on BuildKite as OpenTelemetry data
and then send them to honeycomb.io for slice-n-dice high cardinality analysis.

Worth to note that this program is using 100% OpenTelemetry SDK so Honeycomb can be swapped out
with any Tracing/Metrics/APM providers that support OpenTelemetry protocol (DataDog, Grafana Tempo etc...)

This was built as a quick POC / MVP for my daily use cases but PRs/Issues are more than welcome.
