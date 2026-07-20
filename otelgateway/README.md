# `otelgateway` — Azure logs → OTLP gateway for the Functions Go worker

A small library that turns an Azure Functions Go worker into a telemetry
gateway: it decodes Azure logs arriving on an **Event Hub trigger** into
OpenTelemetry pdata and exports them over **OTLP** to any collector.

It is the reusable core behind the `eventHubOtelGateway*` samples — the
worker-based counterpart to the upstream OpenTelemetry Collector
[`azurefunctionsreceiver`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/receiver/azurefunctionsreceiver).
Where that receiver is a custom-handler component configured in collector YAML,
this is a library a normal Functions app imports — so the app code stays thin.

## Quickstart

```go
package main

import (
    "github.com/azure/azure-functions-golang-worker/otelgateway"
    "github.com/azure/azure-functions-golang-worker/sdk"
    "github.com/azure/azure-functions-golang-worker/worker"
)

func main() {
    gw, err := otelgateway.New(otelgateway.DefaultOTLPEndpoint)
    if err != nil { panic(err) }

    app := sdk.FunctionApp()
    app.EventHub("logs", gw.Handler(otelgateway.EncodingResourceLogs),
        sdk.WithEventHubName("insights-logs"),
        sdk.WithConnection("EventHubConnection"),
        sdk.WithConsumerGroup("$Default"),
    )

    worker.Start(app)
}
```

That's the whole gateway. The handler decodes each message and exports it over
OTLP to whatever listens at the endpoint.

## Decode is reused, not reimplemented

The built-in `azureresourcelogs` encoding is the canonical contrib decode
([`pkg/translator/azure`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/pkg/translator/azure)) —
the **same** `plog.Unmarshaler` the upstream receiver loads as its
`azureresourcelogs` encoding extension. You are reusing Elastic's decode, by
import.

## Any collector (OTLP transport)

Export is OTLP, a stable wire protocol, so a gateway works with **any** collector
it is pointed at — any vendor, any version:

- **Embedded** — point at [`otelcollector`](../otelcollector/README.md)'s bundled
  OTLP receiver on `localhost:4317` and run the collector in-process via
  `otelcollector.WithCollector` on `worker.Start`. The worker owns the
  collector's lifecycle.
- **External** — point `New` at a sidecar or remote collector; the worker
  manages no collector.

The gateway never manages a collector itself — embedding one is the caller's
choice.

## Parity with the receiver (as code, not YAML)

| Receiver feature | `otelgateway` |
|---|---|
| Multiple log bindings | one `Handler(encoding)` per `app.EventHub(...)` binding |
| Per-binding encoding | the encoding registry: `EncodingResourceLogs`, `EncodingRaw`, plus `WithEncoding(name, u)` |
| `include_metadata` | `WithMetadataEnrichment()` folds Event Hub system properties onto resource attributes |
| Processors / exporters / auth | the embedded (or external) collector pipeline |

```go
gw, _ := otelgateway.New(otelgateway.DefaultOTLPEndpoint,
    otelgateway.WithMetadataEnrichment(),
    otelgateway.WithEncoding("myformat", myUnmarshaler),
)
app.EventHub("logs",    gw.Handler(otelgateway.EncodingResourceLogs), /* ... */)
app.EventHub("rawLogs", gw.Handler(otelgateway.EncodingRaw),          /* ... */)
```

The difference from the receiver is **config vs code**: it selects these in
collector YAML; the library selects them in Go.

## API

| Symbol | Purpose |
|---|---|
| `New(endpoint, opts...) (*Gateway, error)` | Dial the OTLP endpoint (empty → `DefaultOTLPEndpoint`). |
| `Gateway.Handler(encoding) sdk.EventHubHandler` | Handler for a named encoding; panics at setup if unknown. |
| `Gateway.Close() error` | Release the OTLP connection. |
| `WithEncoding(name, plog.Unmarshaler)` | Register/override an encoding. |
| `WithMetadataEnrichment()` | Enable invoke-metadata enrichment. |
| `EncodingResourceLogs`, `EncodingRaw` | Built-in encoding names. |
| `RawLogsUnmarshaler` | The raw passthrough encoding, exported for composition. |

## Notes

- **Opt-in deps.** Like `otelfunc`/`otelcollector`, this is a separate module, so
  apps that never import it pull in no OTLP/collector packages.
- **Singular Event Hub handler.** The worker's typed Event Hub handler is one
  message per invocation (no `cardinality: many` form today). A single Azure
  Monitor message still fans out to many records via its `records` array.
- **No `otelfunc`.** A gateway should not mix the worker's own invocation
  telemetry into the forwarded customer data; don't register `otelfunc` on the
  data path.
