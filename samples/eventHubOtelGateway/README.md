# `eventHubOtelGateway` — worker-based Azure resource-logs gateway (spike)

A spike exploring the worker-based equivalent of the upstream OpenTelemetry
Collector [`azurefunctionsreceiver`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/receiver/azurefunctionsreceiver).
Instead of deploying a collector binary **as** a Functions custom handler, this
uses a **native Event Hub trigger** plus the embedded
[`otelcollector`](../../otelcollector/README.md).

## Pipeline

```
host Event Hub trigger
  -> gRPC -> handler decodes Azure resource logs to pdata (plog.Logs)
              reusing contrib pkg/translator/azure (no reimplementation)
  -> ConsumeLogs (in-memory, via the inproc receiver)
  -> embedded collector's logs pipeline
  -> processors / exporters -> backend (Azure Monitor by default)
```

The worker-side code is just **transport glue + forward** ([main.go](main.go)):
the decode is not reimplemented. The handler reuses the same `plog.Unmarshaler`
(`pkg/translator/azure`) that the upstream receiver loads as its
`azureresourcelogs` encoding extension. Batching, retries, and the exporter are
the collector's job. Ingress is the custom in-process receiver in
[inproc.go](inproc.go).

## Why no `otelfunc`

There are two independent planes of telemetry:

- **Data plane** — the resource logs arriving over Event Hub. This is the
  gateway's product; the handler builds the pdata, so its shape is 100% ours.
- **Control plane** — spans/logs about *this function executing*. That is what
  `otelfunc` produces.

`otelfunc` exports the worker's own invocation telemetry into the embedded
collector. On a gateway that would interleave the worker's `function <name>`
spans with the forwarded customer data on the same pipeline. So the data path
runs **without** `otelfunc`. If you want to observe the gateway's own health,
give it a *separate* pipeline or exporter — don't mix it into the forwarded
path.

## The ingress seam

Upstream, the receiver *is* a pipeline stage and calls `ConsumeLogs` directly.
This spike gets the same in-memory handoff without being a built-in receiver: a
small custom `inproc` receiver ([inproc.go](inproc.go)) registered through
`otelcollector.WithFactories` captures the consumer at the head of the logs
pipeline at graph-build time, and the handler calls `ConsumeLogs` on it. No
OTLP serialization, no loopback hop — and no change to the `otelcollector`
package, since the factory map it already exposes is the extensibility point.

Two things come with feeding a consumer directly (inherent, not specific to this
spike): the consumer only exists after the collector starts, so the first
invocation blocks until the pipeline is built; and `ConsumeLogs` is called
concurrently across invocations, with a returned error being the signal to fail
the invocation so the host retries.

## What this spike shows

- The worker-side code is small and reuses the canonical decode: a ~30-line
  handler plus a ~60-line in-process receiver, with **zero** decode
  reimplementation — the Azure resource-logs schema is handled by the contrib
  `pkg/translator/azure` unmarshaler (the exact code the upstream receiver runs
  via its `azureresourcelogs` encoding extension), imported as a library.
- This is the key reuse point: the receiver's *own* code is the custom-handler
  HTTP transport, which is exactly what the native trigger replaces. The part
  worth keeping — the decode — was already factored out of the receiver into
  `pkg/translator/azure`, so the worker reuses it by import rather than by
  embedding the receiver component. Reusing the receiver *factory* via
  `WithFactories` would instead pull in its HTTP transport, which a worker app
  never drives.
- Version note: `pkg/translator/azure@v0.132.0` lines up with the collector
  core (`v1.38.0`) that `otelcollector` pins, so it drops in with no version
  juggling — the alignment that an `ocb` build would otherwise manage.
- Gap surfaced: the typed `sdk.EventHubHandler` is **singular** — there is no
  batch ("many") cardinality form for Event Hub today (only CosmosDB exposes a
  slice handler). A real gateway wants batches for throughput. A single Azure
  Monitor message still fans out to many records via its `records` array, so
  this remains useful, but per-invocation batching is a missing piece.

## Running

This is a build-first spike (it compiles against the local worker and embedded
collector via `replace` directives). Build it like the other standalone
samples, with the workspace file disabled:

```pwsh
$env:GOWORK = "off"
go build ./...
```

To exercise it end to end you need a real Event Hub (Azure Monitor diagnostic
settings streaming resource logs to `insights-logs-*`) and the three
`OTEL_DCE_*` endpoints in `local.settings.json`. For purely local inspection,
swap the default collector config for a logs-only pipeline with a `debug`
exporter via `otelcollector.WithConfigYAML(...)` + `WithFactories(...)`.
