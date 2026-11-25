# Grafana dashboard and alert rules

goldie ships a Grafana dashboard and Prometheus alert rules for the metrics in
[Metrics goldie emits](configuration.md#metrics-goldie-emits). Both are files in the repository:

- `deploy/grafana/goldie-dashboard.json`: the dashboard.
- `deploy/grafana/goldie-alerts.yaml`: the alert rules.

A Go test (`internal/app/grafana_dashboards_test.go`) runs a server, reads its OTLP metric export and fails
when the dashboard or an alert queries a metric or label goldie does not emit.

## Prerequisites

Metrics reach Prometheus through an OpenTelemetry Collector or Prometheus's OTLP receiver. The queries use the
Prometheus names: dots become underscores, counters get `_total` and the histogram `_seconds_bucket`
(`goldie.item.uses` is `goldie_item_uses_total`).

## Dashboard

Import the JSON in Grafana (Dashboards, New, Import). It uses two variables:

- `datasource`: your Prometheus data source.
- `ui_url`: the address of the goldie UI, for example `https://goldie.example.com`. The data links are built from it.

Panels: Item uses by kind, client (`via`), Project and principal, the most used Items, searches and lookups,
Items served, skill gets, Source polls and their duration, and MCP tool calls. The panels link to the UI:
Item uses to Activity (`/ops/activity`), Items to Assets (`/assets`), Source polls to Sources (`/ops/sources`).

## Alert rules

| Alert | Severity | Fires when |
|---|---|---|
| `GoldieSourcePollFailing` | warning | A Source had failed polls for 15 minutes. |
| `GoldieSourceNotPolled` | critical | No poll of a Source finished `ok` in the last hour. |
| `GoldieSourcePollSlow` | warning | The p95 poll duration is above 30 seconds for 15 minutes. |
| `GoldieMcpToolErrors` | warning | More than 20% of MCP tool calls return an error for 15 minutes. |
| `GoldieNoItemsServed` | critical | `goldie.items` sums to zero for 10 minutes. |

Load the file with `rule_files` in `prometheus.yml`, or copy the rules into a `PrometheusRule`. Thresholds are
starting points; tune them to your Source count and poll interval.
