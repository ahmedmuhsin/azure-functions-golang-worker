# `eventHubOtelGatewayEmbedded` — Azure logs gateway with an embedded collector

A worker-based Azure logs gateway that runs its **own embedded** OpenTelemetry
Collector. The worker-based equivalent of the upstream
[`azurefunctionsreceiver`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/receiver/azurefunctionsreceiver),
deployed as a normal Functions app instead of a custom-handler collector.

All decode-and-export logic lives in the [`otelgateway`](../../otelgateway/README.md)
library — this app is just wiring.

## Pipeline

```
host Event Hub trigger
  -> gRPC -> otelgateway: decode (contrib pkg/translator/azure) + OTLP export
  -> embedded collector (localhost:4317, worker-managed lifecycle)
  -> processors / exporters -> Azure Monitor (OTEL_DCE_LOGS_ENDPOINT)
```

## How it works

- `otelgateway.New(DefaultOTLPEndpoint)` builds a gateway exporting to
  `localhost:4317`.
- `gw.Handler(EncodingResourceLogs)` is the Event Hub handler: decode each
  message with the canonical contrib unmarshaler, then export over OTLP.
- `otelcollector.WithCollector(...)` runs the collector in-process for the
  worker's lifetime (started before serving, flushed and shut down on teardown).

The collector config is a logs-only pipeline whose `otlp` receiver ingests what
the gateway exports.

## Embedded vs external

This sample embeds the collector. To export to a collector you run **elsewhere**
(sidecar, remote, third-party — any vendor/version, since OTLP is a stable wire
protocol), see [`eventHubOtelGatewayExternal`](../eventHubOtelGatewayExternal).
The only difference is whether `worker.Start` is given
`otelcollector.WithCollector`.

## Running

Build with the workspace file disabled (resolves local modules via `replace`):

```pwsh
$env:GOWORK = "off"
go build ./...
```

End to end needs a real Event Hub (Azure Monitor diagnostic settings streaming
resource logs to `insights-logs-*`) and `OTEL_DCE_LOGS_ENDPOINT` set. For local
inspection, point the collector config's exporter at a `debug` exporter.
