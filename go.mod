module github.com/azure/azure-functions-golang-worker

go 1.24.0

require (
	github.com/azure/azure-functions-golang-worker/sdk v0.0.0
	google.golang.org/grpc v1.80.0
	google.golang.org/protobuf v1.36.11
)

require (
	go.opentelemetry.io/otel v1.41.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260120221211-b8f7ae30c516 // indirect
)

// The sdk package is a separate, dependency-free module (standard library
// only) so alternative transports such as ./customhandler can depend on the
// programming model without inheriting this module's gRPC/protobuf graph.
// It is resolved locally here until it is published independently.
replace github.com/azure/azure-functions-golang-worker/sdk => ./sdk

require (
	github.com/spf13/pflag v1.0.6
	golang.org/x/net v0.50.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)
