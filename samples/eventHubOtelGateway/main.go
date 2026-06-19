// Command eventHubOtelGateway is a spike: the worker-based equivalent of the
// upstream OpenTelemetry Collector "azurefunctionsreceiver", built on native
// Functions triggers + the embedded collector instead of a custom handler.
//
// Pipeline:
//
//	host Event Hub trigger -> gRPC -> this handler decodes Azure resource logs
//	to pdata -> ConsumeLogs (in-memory) -> embedded collector's logs pipeline
//	-> processors/exporters -> backend (Azure Monitor by default).
//
// Ingress is in-process: a small custom "inproc" receiver (see inproc.go),
// registered through otelcollector.WithFactories, captures the consumer at the
// head of the logs pipeline. The handler calls ConsumeLogs directly — the same
// in-memory handoff the upstream receiver does, with no OTLP serialization and
// no loopback network hop.
//
// Deliberately NO otelfunc: the only telemetry on the collector's pipeline is
// the forwarded customer data. otelfunc would add the worker's own invocation
// spans/logs onto the same collector, which a gateway does not want
// interleaved with forwarded data. The handler owns the pdata shape end to
// end, so "control the log shape ourselves" holds by construction.
package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/azure/azure-functions-golang-worker/otelcollector"
	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/sdk/bindings"
	"github.com/azure/azure-functions-golang-worker/worker"

	"go.opentelemetry.io/collector/consumer"
)

// collectorConfigYAML is a logs-only pipeline fed by the in-process receiver.
// Batching, retry, and the exporter are the collector's job; the handler only
// produces pdata and calls ConsumeLogs.
const collectorConfigYAML = `
receivers:
  inproc: {}
processors:
  batch:
extensions:
  azureauth:
    use_default: true
    scopes:
      - https://monitor.azure.com/.default
exporters:
  otlphttp/azuremonitor:
    logs_endpoint: ${env:OTEL_DCE_LOGS_ENDPOINT}
    auth:
      authenticator: azureauth
service:
  extensions: [azureauth]
  pipelines:
    logs:
      receivers: [inproc]
      processors: [batch]
      exporters: [otlphttp/azuremonitor]
`

// gateway decodes Azure resource-logs messages and pushes them straight into
// the collector's logs pipeline. The head consumer is captured lazily on the
// first invocation (blocking until the collector has started) and cached for
// the rest of the worker's life.
type gateway struct {
	tap *logsTap

	mu   sync.Mutex
	logs consumer.Logs
}

// logsConsumer returns the pipeline head consumer, fetching it once from the
// tap and caching it. The first call blocks until the collector's pipeline is
// built (or ctx is cancelled).
func (g *gateway) logsConsumer(ctx context.Context) (consumer.Logs, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.logs != nil {
		return g.logs, nil
	}
	c, err := g.tap.consumer(ctx)
	if err != nil {
		return nil, err
	}
	g.logs = c
	return c, nil
}

// Handle is the entire decode-and-forward shim: decode one Event Hub message
// (an Azure Monitor resource-logs batch) to pdata, then push it into the
// pipeline. A ConsumeLogs error fails the invocation so the host retries.
//
// Signature note: sdk.EventHubHandler is singular (one message per
// invocation). The upstream receiver consumes Event Hub batches; the typed Go
// handler has no "many" cardinality form today (only CosmosDB exposes a slice
// handler). A single Azure Monitor message still carries many records via the
// "records" array, so one invocation already fans out to many log records.
func (g *gateway) Handle(ctx context.Context, msg bindings.EventHubMessage) error {
	logs, err := g.logsConsumer(ctx)
	if err != nil {
		return err
	}
	ld, err := decodeResourceLogs(msg.Body)
	if err != nil {
		return err
	}
	if ld.LogRecordCount() == 0 {
		return nil
	}
	slog.InfoContext(ctx, "forwarding resource logs to embedded collector",
		"records", ld.LogRecordCount(),
		"resources", ld.ResourceLogs().Len(),
	)
	return logs.ConsumeLogs(ctx, ld)
}

func main() {
	tap := newLogsTap()

	// Register the in-process receiver alongside the bundled components, then
	// point the logs pipeline at it via the config YAML.
	factories, err := otelcollector.DefaultFactories()
	if err != nil {
		slog.Error("failed to build collector factories", "error", err)
		return
	}
	tf := tap.factory()
	factories.Receivers[tf.Type()] = tf

	gw := &gateway{tap: tap}

	app := sdk.FunctionApp()

	app.EventHub("resourceLogs", gw.Handle,
		sdk.WithEventHubName("insights-logs"),
		sdk.WithConnection("EventHubConnection"),
		sdk.WithConsumerGroup("$Default"),
	)

	worker.Start(app, otelcollector.WithCollector(
		otelcollector.WithFactories(factories),
		otelcollector.WithConfigYAML(collectorConfigYAML),
	))
}
