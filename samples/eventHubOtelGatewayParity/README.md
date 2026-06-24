# `eventHubOtelGatewayParity` — receiver feature parity, as code

Demonstrates feature parity with the upstream
[`azurefunctionsreceiver`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/receiver/azurefunctionsreceiver),
expressed as Go via the [`otelgateway`](../../otelgateway/README.md) library
instead of collector YAML.

The receiver is a configurable, multi-binding, multi-encoding decode router.
This sample shows the same capabilities as thin app code:

| Receiver feature | Here |
|---|---|
| Multiple log bindings (`/logs`, `/raw_logs`) | two Event Hub functions: `logs` and `rawLogs` |
| Per-binding encoding | `gw.Handler(EncodingResourceLogs)` vs `gw.Handler(EncodingRaw)` |
| `include_metadata` | `otelgateway.WithMetadataEnrichment()` |
| Processors / exporters / auth | the embedded collector pipeline |

The difference is **config vs code**: the receiver selects these in collector
YAML; the library selects them in Go. Same result.

## Pipeline

```
host Event Hub triggers (insights-logs, raw-logs)
  -> gRPC -> otelgateway: per-binding decode + metadata enrichment + OTLP export
  -> embedded collector (localhost:4317, worker-managed)
  -> processors / exporters -> Azure Monitor (OTEL_DCE_LOGS_ENDPOINT)
```

## Notes

- Adding an encoding is a library call (`WithEncoding(name, unmarshaler)`), not a
  new collector component.
- The `azureresourcelogs` decode is the canonical contrib
  `pkg/translator/azure` unmarshaler — the same one the receiver loads as its
  `azureresourcelogs` encoding extension.
- This sample embeds the collector (like
  [`eventHubOtelGatewayEmbedded`](../eventHubOtelGatewayEmbedded)); point it at an
  external collector instead by dropping `otelcollector.WithCollector` and
  passing an endpoint to `otelgateway.New` (see
  [`eventHubOtelGatewayExternal`](../eventHubOtelGatewayExternal)).

## Running

Build with the workspace file disabled (resolves local modules via `replace`):

```pwsh
$env:GOWORK = "off"
go build ./...
```

End to end needs a real Event Hub (Azure Monitor diagnostic settings streaming
resource logs to `insights-logs-*` and a `raw-logs` hub) and
`OTEL_DCE_LOGS_ENDPOINT` set.
