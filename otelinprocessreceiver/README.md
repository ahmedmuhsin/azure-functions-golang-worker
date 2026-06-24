# `otelinprocessreceiver` — push pdata into an embedded collector, in-process

An OpenTelemetry Collector receiver that the **embedding application pushes into
directly**, with no serialization and no loopback network hop.

Every stock receiver owns its ingestion — it scrapes, listens on a socket, or
polls a queue. This one inverts that: it exposes the consumer at the head of its
pipeline so host code can call `ConsumeLogs` in-process. It is the
"collector as a library" ingress — when you embed a collector in your own
process (see [`otelcollector`](../otelcollector/README.md)) and already hold the
pdata, this avoids the OTLP-loopback (marshal + localhost round-trip) an `otlp`
receiver would otherwise require.

## Usage

```go
r := otelinprocessreceiver.New()

factories, _ := otelcollector.DefaultFactories()
factories.Receivers[otelinprocessreceiver.Type] = r.Factory()

worker.Start(app, otelcollector.WithCollector(
    otelcollector.WithFactories(factories),
    otelcollector.WithConfigYAML(`
receivers: { inprocess: {} }
exporters: { otlphttp: { logs_endpoint: ${env:OTEL_DCE_LOGS_ENDPOINT} } }
service:
  pipelines:
    logs: { receivers: [inprocess], exporters: [otlphttp] }`),
))

// r implements ConsumeLogs; call it from a handler once the collector runs.
_ = r.ConsumeLogs(ctx, ld)
```

`r` doubles as the receiver factory provider **and** the `ConsumeLogs` entry
point. `ConsumeLogs` blocks until the collector has built the pipeline (the
consumer is captured during component creation at collector start), then
delegates — so an early call waits rather than failing. Safe for concurrent
callers.

## Notes

- **Config must list it.** The pipeline must include `inprocess` under
  `receivers:`, or the consumer is never captured and `ConsumeLogs` blocks until
  its context is cancelled.
- **Dependency-clean.** Imports only collector core
  (`receiver`/`consumer`/`component`/`pdata`) — no worker, gateway, or vendor
  deps — so it composes with any embedded collector.
- **No upstream equivalent.** The collector component model assumes receivers
  own ingestion, so neither core nor contrib has a host-push receiver. This is an
  *upstream candidate* if the collector-as-a-library pattern is standardized;
  until then it lives here. Kept generic so a future move is a relocation, not a
  rewrite.
- **Scaling.** This optimizes latency *within* an embedding process; it does not
  make collectors shared across instances. Each embedding process still runs its
  own collector.
