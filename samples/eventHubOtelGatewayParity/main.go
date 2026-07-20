// Command eventHubOtelGatewayParity demonstrates feature parity with the
// upstream OpenTelemetry Collector "azurefunctionsreceiver", expressed as Go
// using the otelgateway library rather than as collector YAML.
//
// It shows the receiver's three configurable behaviors as thin app code:
//
//   - Multiple log bindings (the receiver's example exposes /logs and
//     /raw_logs): two Event Hub functions, "logs" and "rawLogs".
//   - Per-binding encoding: each binding passes a different encoding name to
//     gw.Handler — the resource-logs decode vs a raw passthrough.
//   - include_metadata: otelgateway.WithMetadataEnrichment folds Event Hub
//     invoke metadata onto resource attributes.
//
// Like the embedded sample it runs a worker-managed embedded collector. The
// difference from the receiver is config vs code: the receiver selects these in
// YAML; here they are library calls.
package main

import (
	"log/slog"
	"os"

	"github.com/azure/azure-functions-golang-worker/otelcollector"
	"github.com/azure/azure-functions-golang-worker/otelgateway"
	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/worker"
)

// collectorConfigYAML is a logs-only pipeline fed by the bundled otlp receiver
// the gateway exports to.
const collectorConfigYAML = `
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: localhost:4317
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
      receivers: [otlp]
      processors: [batch]
      exporters: [otlphttp/azuremonitor]
`

func main() {
	gw, err := otelgateway.New(otelgateway.DefaultOTLPEndpoint,
		otelgateway.WithMetadataEnrichment(), // the receiver's include_metadata
	)
	if err != nil {
		slog.Error("failed to build gateway", "error", err)
		os.Exit(1)
	}

	app := sdk.FunctionApp()

	// Multiple bindings, each with its own encoding — the receiver's
	// multi-binding multiplex, as two Event Hub functions.
	app.EventHub("logs", gw.Handler(otelgateway.EncodingResourceLogs),
		sdk.WithEventHubName("insights-logs"),
		sdk.WithConnection("EventHubConnection"),
		sdk.WithConsumerGroup("$Default"),
	)
	app.EventHub("rawLogs", gw.Handler(otelgateway.EncodingRaw),
		sdk.WithEventHubName("raw-logs"),
		sdk.WithConnection("EventHubConnection"),
		sdk.WithConsumerGroup("$Default"),
	)

	worker.Start(app, otelcollector.WithCollector(
		otelcollector.WithConfigYAML(collectorConfigYAML),
	))
}
