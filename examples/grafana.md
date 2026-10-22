# Grafana

Two ways to get LANLeecher data into Grafana.

## Prometheus metrics (live)

Scrape `/api/v1/metrics` with a token that has the scope `read:rankings`:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: lanleecher
    scrape_interval: 30s
    metrics_path: /api/v1/metrics
    scheme: https
    authorization:
      credentials: llk_…
    static_configs:
      - targets: ["lanleecher.example"]
```

Useful panels:

| Panel | Query |
| --- | --- |
| Throughput (time series, unit bits/sec) | `lanleecher_throughput_bits_per_second` (legend: `{{direction}}`) |
| Event traffic (stat, unit bytes SI) | `lanleecher_event_bytes_total{direction="down"}` |
| Download rate from the totals | `rate(lanleecher_event_bytes_total{direction="down"}[5m]) * 8` |
| Clients (stat) | `lanleecher_event_clients` |
| Switches down (stat, alert) | `count(lanleecher_switch_up == 0)` |

## Rankings via JSON (Infinity data source)

Install the [Infinity](https://grafana.com/grafana/plugins/yesoreyeram-infinity-datasource/) data
source, configure it with base URL `https://lanleecher.example` and an `Authorization: Bearer llk_…`
header (custom HTTP header in the data source settings), then add a table panel:

- Type: JSON, Parser: Backend
- URL: `/api/v1/rankings/download?limit=20`
- Rows / root: `entries`
- Columns: `rank` (number), `name` (string), `bytes_down` (number, unit bytes SI),
  `bytes_up` (number, unit bytes SI)

For the beamer-like view without client details, use `/api/v1/board` with root `top_download`
and a token with scope `read:board`.
