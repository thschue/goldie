# Grafana dashboard and alert rules

goldie ships a Grafana dashboard and Prometheus alert rules for the metrics in
[Metrics goldie emits](configuration.md#metrics-goldie-emits). Both are files in the repository:

- `docs/grafana/goldie-dashboard.json`: the dashboard.
- `docs/grafana/goldie-alerts.yaml`: the alert rules.

They are reference assets you import by hand; nothing in goldie deploys them. The queries are checked in CI
against the metrics a running server exports, so a renamed metric cannot silently break them.

## Prerequisites

Metrics reach Prometheus through an OpenTelemetry Collector or Prometheus's OTLP receiver. The queries use the
Prometheus names: dots become underscores, counters get `_total` and the histogram `_seconds_bucket`
(`goldie.item.uses` is `goldie_item_uses_total`).

## Dashboard

Import the JSON in Grafana (Dashboards, New, Import). It uses two variables:

- `datasource`: your Prometheus data source.
- `ui_url`: the address of the goldie UI, for example `https://goldie.example.com`, with no trailing slash. The data
  links are built from it. Set it in the dashboard's variables, then save the dashboard so the value stays.

Panels: Item uses by kind, surface (`mcp`, `cli` or `other`), Project and principal, the most used Items,
searches and lookups, Items served, skill gets, Source polls and their duration, and MCP tool calls. The panels
link to the UI: Item uses to Activity (`/ops/activity`), Items to Assets (`/assets`), Source polls to Sources
(`/ops/sources`). The `/ops/*` pages need an operator session, so the link asks you to sign in first.

To link the other way, set `GOLDIE_GRAFANA_URL` on the server to this dashboard's URL. The Activity page then
shows a "Trends in Grafana" link to it (see [configuration](configuration.md)).

## Alert rules

| Alert | Severity | Fires when |
|---|---|---|
| `GoldieSourcePollFailing` | warning | A Source had failed polls and no successful poll for 15 minutes. |
| `GoldieSourceNotPolled` | critical | No poll of a Source finished `ok` in the last hour. |
| `GoldieSourcePollSlow` | warning | The p95 poll duration is above 30 seconds for 15 minutes. |
| `GoldieMcpToolErrors` | warning | More than 20% of MCP tool calls return an error for 15 minutes. |
| `GoldieNoItemsServed` | critical | `goldie.items` sums to zero for 10 minutes. |

`GoldieSourceNotPolled` and `GoldieNoItemsServed` stay silent when the metric is absent, for example when the
server is down or metrics export is off; alert on `up` or the Collector for that.

Load the file with `rule_files` in `prometheus.yml`, or copy the rules into a `PrometheusRule`. Thresholds are
starting points; tune them to your Source count and poll interval.
