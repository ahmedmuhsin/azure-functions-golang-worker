// Package sdk is the transport-neutral programming model for the Azure
// Functions Go worker: the function-app registry, typed trigger handlers,
// binding metadata, the middleware chain, and the shared invocation core.
//
// The programming model is deliberately independent of how invocations reach
// the process. It is consumed by two transports, each in its own module:
//
//   - worker — the native gRPC transport. The host launches the binary and
//     drives it over the FunctionRpc bidirectional stream.
//   - customhandler — the Azure Functions custom-handler HTTP transport. The
//     host forwards invocations to an HTTP server, which may run standalone or
//     be mounted inside another program.
//
// Neither transport's types appear in this package. sdk imports only the
// standard library and sdk/bindings; the module boundary (sdk/go.mod declares
// no third-party requires) structurally prevents a transport dependency from
// leaking in, and imports_test.go asserts it.
//
// # What lives here vs. in a transport
//
// The programming model defines WHAT a function is — a typed handler, its
// binding metadata, and the middleware wrapped around it. A transport defines
// HOW invocations arrive and results leave: wire-format conversion, process
// lifecycle, response encoding, and function indexing. The shared execution
// seam is [RunInvocation]: each transport builds a transport-specific inner
// [Handler] (binding arguments from its own wire format) and hands it to
// RunInvocation for middleware composition and panic recovery.
//
// # Middleware extensibility
//
// The [Middleware] interface (Wrap(next Handler) -> Handler) is deliberately
// minimal, matching the shape established by net/http (Handler/HandlerFunc)
// and gRPC interceptors. It supports the full range of cross-cutting concerns:
// distributed tracing, structured logging, authentication, retry policies,
// panic recovery, and request/response validation. Middleware that wants to
// replace function execution entirely (e.g. orchestration replay) can
// short-circuit the chain by skipping next().
//
// # Transport capability matrix
//
// The programming model does not force parity: each transport implements as
// much of it as makes sense. Optional contracts declared here — for example
// [CapabilityProvider] and [MiddlewareContext.SetOutboundTraceAttribute] — are
// honored by transports that can and ignored by those that cannot. A "no" or
// "limited" below is a deliberate property of the leaner transport, not a
// defect.
//
//	Capability                         gRPC worker      customhandler
//	---------------------------------  ---------------  ------------------------
//	Typed triggers + middleware        yes              yes (shared)
//	Function indexing                  runtime (gRPC)   build-time (EmitFunctions)
//	HTTP streaming                     HttpUri proxy    forwarded HTTP
//	Embeddable as http.Handler         no               yes
//	Runs on a stock host               no               yes
//	Warm start / specialization        yes              no
//	Capability negotiation             yes (WorkerInit) static (host.json)
//	Native OTel worker-mode            yes              no
//	Structured logs to host            RpcLog stream    envelope / stderr
//	Outbound trace attrs -> host span  yes              no
//	Named output bindings              yes              limited
package sdk
