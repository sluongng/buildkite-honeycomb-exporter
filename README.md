# BuilKite Honeycomb Exporter

A quick scraper program that let you export builds on BuildKite as OpenTelemetry data
and then send them to honeycomb.io for slice-n-dice high cardinality analysis.

This program is using 100% OpenTelemetry SDK so Honeycomb can be swapped out with any
Tracing/Metrics/APM providers that support OpenTelemetry protocol (DataDog, Grafana Tempo etc...)

![image](https://user-images.githubusercontent.com/26684313/152633754-e83b05f1-f552-4afd-b7bd-1c0405d7839a.png)

This was built as a quick POC / MVP for my daily use cases but PRs/Issues are more than welcome.

Totally inspired by https://github.com/zoidbergwill/gitlab-honeycomb-buildevents-webhooks-sink
