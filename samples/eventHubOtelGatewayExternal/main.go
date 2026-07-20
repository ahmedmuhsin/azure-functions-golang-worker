// Command eventHubOtelGatewayExternal is a worker-based Azure logs gateway that
// exports to an OpenTelemetry Collector running ELSEWHERE.
//
// It is the external-collector counterpart of ../eventHubOtelGatewayEmbedded.
// Because export is OTLP — a stable wire protocol — the gateway works with any
// collector it is pointed at: a sidecar, a remote collector, or a third-party
// one (any vendor, any version). The worker manages no collector; it only
// decodes and exports.
//
// Set OTEL_EXPORTER_OTLP_ENDPOINT to the collector's OTLP/gRPC address. All
// decode-and-export logic lives in the otelgateway library; this app is wiring.
package main

import (
	"log/slog"
	"os"

	"github.com/azure/azure-functions-golang-worker/otelgateway"
	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/worker"
)

func main() {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		slog.Error("set OTEL_EXPORTER_OTLP_ENDPOINT to your collector's OTLP/gRPC address")
		os.Exit(1)
	}

	gw, err := otelgateway.New(endpoint)
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

	// No embedded collector: the worker just exports to the external endpoint.
	worker.Start(app)
}
