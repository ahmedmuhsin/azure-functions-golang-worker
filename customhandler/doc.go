// Package customhandler serves an [sdk.App] over the Azure Functions
// custom-handler HTTP protocol, as an alternative transport to the native
// gRPC worker (worker.Start).
//
// The programming model — typed trigger registrations (app.HTTP, app.Timer,
// …), the middleware chain (app.Use / App.Compose), and worker-driven
// function metadata — is transport-agnostic: an *sdk.App is just a registry.
// worker.Start binds that registry to the host's FunctionRpc gRPC stream;
// this package binds the same registry to the custom-handler protocol, in
// which the host launches any executable that runs an HTTP server and POSTs a
// JSON envelope per invocation.
//
// # Why a second transport
//
// The native gRPC worker is a process entrypoint: it owns main(), parses
// gRPC launch flags, traps signals, and blocks. That shape cannot be embedded
// as a component inside another program (for example an OpenTelemetry
// Collector receiver) without that program surrendering its process launch.
// The custom-handler protocol is just an HTTP server, which reduces cleanly to
// an [http.Handler] — a mountable component. This package therefore exposes
// these surfaces at increasing levels of process ownership:
//
//   - [Handler] returns an http.Handler. It owns nothing at process scope: no
//     os.Exit, no signal handling, no flag parsing, no global logger. Mount it
//     on a listener you already own (an embedded collector receiver, a test
//     server, an existing mux).
//   - [Serve] is a thin convenience wrapper for standalone apps: it reads
//     FUNCTIONS_CUSTOMHANDLER_PORT, listens, and blocks until SIGINT/SIGTERM,
//     then drains middleware shutdowns.
//   - [Run] is the one-line standalone entry point (the custom-handler analogue
//     of worker.Start): invoked as "<binary> --emit-config [dir]" it writes the
//     function.json files, otherwise it serves. It owns process-level concerns
//     (argument parsing, os.Exit).
//   - [EmitFunctions] writes the function.json files from the registry, so the
//     binding metadata is derived from code instead of hand-written. host.json
//     is not generated — it is a user/template-owned file (extension bundle,
//     logging, the customHandler executable and port), authored as in any
//     Functions app.
//
// # Programming model over custom handlers
//
//	app := sdk.FunctionApp()
//	app.Use(otelfunc.Middleware())
//	app.HTTP("hello", hello, sdk.WithMethods("GET"))
//
//	// Standalone:
//	customhandler.Run(app)
//
//	// Embedded in another server (e.g. a collector receiver):
//	mux.Handle("/", customhandler.Handler(app))
//
// The same hello handler runs through the same App.Compose middleware chain it
// would under the gRPC worker; only the transport differs.
//
// # Build-time indexing
//
// Custom handlers are indexed by the host from function.json on disk, not by a
// runtime metadata request (the custom-handler protocol has no equivalent of
// FunctionsMetadataRequest). [EmitFunctions] closes that gap by serializing the
// registry to disk at build time — the same RawBindings the gRPC worker sends
// to the host, written as files:
//
//	app --emit-config ./out   // publish step: writes one function.json per function
//	app                        // runtime (customhandler.Run serves)
//
// Because both the gRPC metadata response and the generated function.json
// derive from the same registrations, the two deployment modes describe
// identical functions. Only function.json is generated; host.json is supplied
// from a template, as in any Functions app.
//
// # Scope and fidelity
//
// This is a prototype. It is dependency-clean by construction: it imports only
// sdk and the standard library, never the worker/gRPC/protobuf packages, so an
// embedder does not inherit the native worker's launch model. The
// custom-handler transport is intentionally less capable than the gRPC worker:
// there is no placeholder/specialization warm-start, no streaming RpcLog
// pipeline, and richer protobuf TypedData flattens to JSON. Treat it as a
// portable compatibility mode, not a replacement for the native worker.
package customhandler
