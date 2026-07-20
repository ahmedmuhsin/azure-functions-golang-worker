// Command eventHubOtelGatewayEmbedded is a worker-based Azure logs gateway that
// runs its own embedded OpenTelemetry Collector.
//
// It is the embedded-collector counterpart of ../eventHubOtelGatewayExternal,
// and the worker-based equivalent of the upstream OpenTelemetry Collector
// "azurefunctionsreceiver" — but as a normal Functions app instead of a
// custom-handler collector distribution.
//
// All the decode-and-export logic lives in the otelgateway library; this app is
// just wiring. The worker owns the embedded collector's lifecycle via
// otelcollector.WithCollector (start-before-serving, flush-and-shutdown-on-
// teardown). The gateway exports decoded logs over OTLP to that collector on
// loopback.
//
// No otelfunc: only forwarded customer data flows through the collector.
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
// the gateway exports to. Forwards to Azure Monitor via OTEL_DCE_LOGS_ENDPOINT.
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
	gw, err := otelgateway.New(otelgateway.DefaultOTLPEndpoint)
	if err != nil {
		slog.Error("failed to build gateway", "error", err)
		os.Exit(1)
	}

	app := sdk.FunctionApp()
	app.EventHub("logs", gw.Handler(otelgateway.EncodingResourceLogs),
		sdk.WithEventHubName("insights-logs"),
		sdk.WithConnection("EventHubConnection"),
		sdk.WithConsumerGroup("$Default"),
	)

	// Worker-managed embedded collector: started before serving, flushed and
	// shut down on teardown.
	worker.Start(app, otelcollector.WithCollector(
		otelcollector.WithConfigYAML(collectorConfigYAML),
	))
}
