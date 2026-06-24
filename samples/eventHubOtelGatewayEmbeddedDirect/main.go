// Command eventHubOtelGatewayEmbeddedDirect is the lowest-latency gateway: it
// pushes decoded logs straight into an embedded collector's pipeline in-memory,
// with no OTLP serialization and no loopback hop.
//
// It is the in-memory counterpart of ../eventHubOtelGatewayEmbedded (which
// reaches the same embedded collector over OTLP loopback). The difference is the
// ingress seam:
//
//   - Embedded (OTLP):  handler -> marshal -> localhost:4317 -> otlp receiver -> pipeline
//   - EmbeddedDirect:   handler -> ConsumeLogs -> pipeline        (this sample)
//
// It uses otelinprocessreceiver — a custom receiver that exposes the pipeline's
// head consumer — registered into the embedded collector via WithFactories. The
// gateway is built with otelgateway.NewWithConsumer, so its handler calls
// ConsumeLogs directly. This matches the in-memory speed the upstream
// custom-handler receiver gets by being a pipeline stage itself.
//
// DEMONSTRATION: this proves the worker can reach receiver-class ingress
// latency. For most gateways the OTLP variants are simpler; prefer this only
// when per-event serialization cost matters.
package main

import (
	"log/slog"
	"os"

	"github.com/azure/azure-functions-golang-worker/otelcollector"
	"github.com/azure/azure-functions-golang-worker/otelgateway"
	"github.com/azure/azure-functions-golang-worker/otelinprocessreceiver"
	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/worker"
)

// collectorConfigYAML is a logs-only pipeline fed by the in-process receiver
// (no otlp receiver — ingress is the direct ConsumeLogs handoff).
const collectorConfigYAML = `
receivers:
  inprocess: {}
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
      receivers: [inprocess]
      processors: [batch]
      exporters: [otlphttp/azuremonitor]
`

func main() {
	// The in-process receiver exposes the pipeline's head consumer. It doubles
	// as the gateway's sink (it implements ConsumeLogs, blocking until the
	// embedded pipeline is built).
	recv := otelinprocessreceiver.New()

	gw := otelgateway.NewWithConsumer(recv)

	// Register the in-process receiver into the embedded collector's factories.
	factories, err := otelcollector.DefaultFactories()
	if err != nil {
		slog.Error("failed to build collector factories", "error", err)
		os.Exit(1)
	}
	factories.Receivers[otelinprocessreceiver.Type] = recv.Factory()

	app := sdk.FunctionApp()
	app.EventHub("logs", gw.Handler(otelgateway.EncodingResourceLogs),
		sdk.WithEventHubName("insights-logs"),
		sdk.WithConnection("EventHubConnection"),
		sdk.WithConsumerGroup("$Default"),
	)

	// Worker-managed embedded collector; the inprocess receiver is its ingress.
	worker.Start(app, otelcollector.WithCollector(
		otelcollector.WithFactories(factories),
		otelcollector.WithConfigYAML(collectorConfigYAML),
	))
}
