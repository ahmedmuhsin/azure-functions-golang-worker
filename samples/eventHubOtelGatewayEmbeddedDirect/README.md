# `eventHubOtelGatewayEmbeddedDirect` — in-memory gateway (no loopback)

The lowest-latency gateway variant: it pushes decoded logs **straight into** an
embedded collector's pipeline in-memory — no OTLP serialization, no loopback
hop. The in-memory counterpart of
[`eventHubOtelGatewayEmbedded`](../eventHubOtelGatewayEmbedded), which reaches the
same embedded collector over OTLP loopback.

## The difference is one seam

```
Embedded (OTLP):   handler -> marshal -> localhost:4317 -> otlp receiver -> pipeline
EmbeddedDirect:    handler -> ConsumeLogs ----------------------------> pipeline
```

It uses [`otelinprocessreceiver`](../../otelinprocessreceiver/README.md) — a
custom receiver that exposes the pipeline's head consumer — registered into the
embedded collector via `otelcollector.WithFactories`. The gateway is built with
`otelgateway.NewWithConsumer(recv)`, so its handler calls `ConsumeLogs` directly.

This matches the in-memory speed the upstream custom-handler `azurefunctionsreceiver`
gets by *being* a pipeline stage. It is the proof that the worker can reach
receiver-class ingress latency.

## Why this is a demonstration, not the default

- **Serialization is the real cost.** A loopback OTLP hop pays marshal +
  localhost round-trip; this removes both. But for low-to-moderate log volume
  that cost is negligible, and the OTLP variants are simpler.
- **It adds moving parts.** A custom receiver in the pipeline, a config that must
  list `inprocess`, and first-invocation blocking (the consumer exists only after
  the collector starts; `ConsumeLogs` waits until then).
- **No upstream precedent.** No core/contrib receiver accepts host-pushed pdata;
  `otelinprocessreceiver` is worker-repo-only. See its README.

Prefer [`eventHubOtelGatewayEmbedded`](../eventHubOtelGatewayEmbedded) (OTLP
loopback) or [`eventHubOtelGatewayExternal`](../eventHubOtelGatewayExternal)
unless per-event serialization cost is proven to matter.

## Running

```pwsh
$env:GOWORK = "off"
go build ./...
```

End to end needs a real Event Hub (Azure Monitor diagnostic settings streaming
resource logs to `insights-logs-*`) and `OTEL_DCE_LOGS_ENDPOINT` set.
