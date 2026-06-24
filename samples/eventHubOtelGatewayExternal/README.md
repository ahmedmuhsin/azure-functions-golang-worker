# `eventHubOtelGatewayExternal` — Azure logs gateway to any external collector

A worker-based Azure logs gateway that exports to an OpenTelemetry Collector
running **elsewhere**. The external-collector counterpart of
[`eventHubOtelGatewayEmbedded`](../eventHubOtelGatewayEmbedded).

Because export is **OTLP** — a stable wire protocol — the gateway works with any
collector it is pointed at (any vendor, any version): a sidecar, a remote
collector, or a third-party one. The worker manages no collector; it only
decodes and exports. All decode-and-export logic lives in the
[`otelgateway`](../../otelgateway/README.md) library.

## Pipeline

```
host Event Hub trigger
  -> gRPC -> otelgateway: decode (contrib pkg/translator/azure) + OTLP export
  -> OTEL_EXPORTER_OTLP_ENDPOINT  (any external collector)
  -> that collector's processors / exporters -> backend
```

## How it works

- `OTEL_EXPORTER_OTLP_ENDPOINT` names the external collector's OTLP/gRPC address.
- `otelgateway.New(endpoint)` builds a gateway exporting there.
- `gw.Handler(EncodingResourceLogs)` decodes each Event Hub message with the
  canonical contrib unmarshaler and exports it over OTLP.
- `worker.Start(app)` — no `otelcollector.WithCollector`, so the worker runs no
  collector of its own.

This is the variant that demonstrates **collector independence**: the same app
binary targets whatever OTLP endpoint you give it, no recompile.

## Running

Build with the workspace file disabled (resolves local modules via `replace`):

```pwsh
$env:GOWORK = "off"
go build ./...
```

End to end needs a real Event Hub (Azure Monitor diagnostic settings streaming
resource logs to `insights-logs-*`) and a reachable collector at
`OTEL_EXPORTER_OTLP_ENDPOINT`. For local inspection, run any collector with a
`debug` exporter and point the endpoint at it.
