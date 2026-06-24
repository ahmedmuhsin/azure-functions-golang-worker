// Package otelgateway turns an Azure Functions Go worker into a telemetry
// gateway: it decodes Azure logs delivered over an Event Hub trigger into
// OpenTelemetry pdata and exports them over OTLP to any collector.
//
// It is the reusable core behind the eventHubOtelGateway* samples — the
// worker-based counterpart to the upstream OpenTelemetry Collector
// "azurefunctionsreceiver". Where that receiver is a custom-handler component
// configured in collector YAML, this is a library a normal Functions app
// imports, so the app code stays thin:
//
//	gw, _ := otelgateway.New(otelgateway.DefaultOTLPEndpoint)
//	app := sdk.FunctionApp()
//	app.EventHub("logs", gw.Handler(otelgateway.EncodingResourceLogs),
//	    sdk.WithEventHubName("insights-logs"),
//	    sdk.WithConnection("EventHubConnection"),
//	)
//	worker.Start(app)
//
// # Decode
//
// The built-in azureresourcelogs encoding is the canonical contrib decode
// (go-translator pkg/translator/azure) — the same plog.Unmarshaler the upstream
// receiver loads as its azureresourcelogs encoding extension. So the decode is
// reused, not reimplemented. Register additional encodings with [WithEncoding].
//
// # Transport
//
// Export is OTLP, a stable wire protocol, so a gateway works with any collector
// it is pointed at — an embedded collector on loopback (see otelcollector), a
// sidecar, or a remote/third-party collector (any vendor, any version). The
// gateway itself never manages a collector; embedding one is the caller's
// choice via otelcollector.WithCollector on worker.Start.
//
// # Parity with the receiver
//
// The receiver's capabilities map onto this library as code rather than YAML:
// multiple Event Hub bindings each with their own encoding (call [Gateway.Handler]
// per binding), and optional invoke-metadata enrichment ([WithMetadataEnrichment],
// mirroring the receiver's include_metadata).
package otelgateway
