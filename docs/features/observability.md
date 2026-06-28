# Observability for mcpproxy

MCPProxy can export operational metrics and distributed traces so you can run it
as a first-class service in Kubernetes or any monitored environment. Two
exporters are available, both **disabled by default** and enabled via config:

- **Prometheus** — a `/metrics` scrape endpoint on the existing HTTP listener.
- **OpenTelemetry (OTLP)** — trace export for tool calls and upstream hops over
  OTLP/HTTP or OTLP/gRPC.

> Anonymous product telemetry is a separate, unrelated feature — see
> [Anonymous Telemetry](telemetry.md). The exporters described here send data
> only to **your** monitoring stack.

## Quick start

Add an `observability` block to `~/.mcpproxy/mcp_config.json`:

```json
{
  "listen": "127.0.0.1:8080",
  "observability": {
    "metrics": {
      "enabled": true
    },
    "tracing": {
      "enabled": true,
      "protocol": "http",
      "endpoint": "localhost:4318",
      "sample_rate": 0.1
    }
  }
}
```

Restart the daemon. `/metrics` is now served on the same address as the HTTP API
(`http://127.0.0.1:8080/metrics`), and traces are exported to the configured
OTLP collector.

## Configuration reference

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `observability.metrics.enabled` | bool | `false` | Expose the Prometheus `/metrics` endpoint. |
| `observability.tracing.enabled` | bool | `false` | Enable OTLP trace export. |
| `observability.tracing.protocol` | string | `"http"` | OTLP transport: `http` or `grpc`. |
| `observability.tracing.endpoint` | string | `localhost:4318` (http) / `localhost:4317` (grpc) | Collector address as `host:port` (no scheme). |
| `observability.tracing.sample_rate` | float | `0.1` | Head-based trace sampling ratio in `[0,1]`. |

Invalid values are repaired on load (unknown protocol → `http`, out-of-range
sample rate → `0.1`), so a partial block never breaks startup.

> **Note:** `/metrics` is served on the same listener as the REST API. The
> REST API requires an API key, but the `/metrics` endpoint follows the same
> rules as the MCP endpoints. Keep `listen` bound to a trusted interface (the
> default is localhost) or scrape via a sidecar/network policy in clustered
> deployments.

## Prometheus scrape config

```yaml
scrape_configs:
  - job_name: mcpproxy
    metrics_path: /metrics
    static_configs:
      - targets: ["mcpproxy:8080"]
```

### Exported metrics

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `mcpproxy_uptime_seconds` | gauge | — | Time since process start. |
| `mcpproxy_http_requests_total` | counter | `method`, `path`, `status` | HTTP requests served. |
| `mcpproxy_http_request_duration_seconds` | histogram | `method`, `path`, `status` | HTTP request latency. |
| `mcpproxy_tool_calls_total` | counter | `server`, `tool`, `status` | Upstream tool calls. |
| `mcpproxy_tool_call_duration_seconds` | histogram | `server`, `tool`, `status` | Tool-call latency. |
| `mcpproxy_servers_total` | gauge | — | Configured upstream servers. |
| `mcpproxy_servers_connected` | gauge | — | Connected upstream servers. |
| `mcpproxy_servers_quarantined` | gauge | — | Quarantined upstream servers. |
| `mcpproxy_tools_total` | gauge | — | Indexed tools. |
| `mcpproxy_docker_containers_active` | gauge | — | Running isolated Docker containers. |
| `mcpproxy_quarantine_events_total` | counter | `scope`, `action` | Quarantine state changes (`scope` = `server`/`tool`). |
| `mcpproxy_oauth_refresh_total` | counter | `server`, `result` | OAuth token-refresh attempts/outcomes. |
| `mcpproxy_oauth_refresh_duration_seconds` | histogram | `server`, `result` | OAuth token-refresh latency. |

Go runtime and process collectors (`go_*`, `process_*`) are exported too.

> Counter series with labels (e.g. `mcpproxy_tool_calls_total`) only appear on
> a scrape **after** the first matching event — this is normal Prometheus
> behaviour for label vectors.

## OpenTelemetry tracing

When tracing is enabled, MCPProxy creates a span for each tool call
(`tool.call`, attributes `tool.server` and `tool.name`) that wraps the upstream
hop, so latency and errors are visible per call. Incoming HTTP requests are also
traced and W3C `traceparent` context is propagated.

Run a local collector to receive spans, for example the OpenTelemetry Collector:

```yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: 0.0.0.0:4318
      grpc:
        endpoint: 0.0.0.0:4317
exporters:
  debug:
service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [debug]
```

Set `observability.tracing.protocol` to `grpc` and `endpoint` to
`localhost:4317` to use the gRPC transport instead of HTTP.

## Editions

Both the **personal** and **server** editions export the same core metrics and
traces. The **server edition** additionally annotates tool-call spans with the
authenticated `user_id` and the active `profile` slug, so multi-tenant operators
can slice traces by user/profile. These are added as span attributes (not
Prometheus labels) to keep metric cardinality bounded.

## Grafana dashboard

A reference dashboard is committed at
[`contrib/grafana/mcpproxy-dashboard.json`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/contrib/grafana/mcpproxy-dashboard.json).
Import it via **Grafana → Dashboards → New → Import → Upload JSON file**, then
select your Prometheus data source.

The dashboard includes panels for:

- **Overview** — uptime, connected/configured/quarantined upstreams, indexed
  tools, active Docker containers.
- **Tool calls** — call rate by status and p95 latency by server (with a
  `server` template variable).
- **HTTP & security** — HTTP request rate, OAuth refresh outcomes, and
  quarantine events.

<!-- TODO(MCP-32): add a rendered screenshot of the imported dashboard once a
     reference Grafana instance is available. -->
