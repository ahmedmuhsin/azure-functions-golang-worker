module github.com/azure/azure-functions-golang-worker/customhandler

go 1.24.0

require github.com/azure/azure-functions-golang-worker/sdk v0.0.0

// customhandler depends only on the dependency-free sdk module, never on the
// root worker module, so an embedder (e.g. an OpenTelemetry Collector receiver)
// does not inherit gRPC/protobuf into its module graph. Resolved locally until
// sdk is published independently.
replace github.com/azure/azure-functions-golang-worker/sdk => ../sdk
