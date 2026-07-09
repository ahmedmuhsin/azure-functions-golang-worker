// Command customhandler-sample is a runnable Azure Functions app that serves
// the Go worker programming model over the custom-handler protocol instead of
// the native gRPC worker. The function code is identical to what a native
// worker app would write; only the entry point differs.
//
// Build and run under the Functions host:
//
//	go build -o handler .            # Linux/macOS (use handler.exe on Windows)
//	./handler --emit-config .        # generate one function.json per function
//	func start                       # the host launches ./handler as a custom handler
//
// host.json is committed alongside this sample, as in any Functions app: it
// carries the customHandler executable/port and extension-bundle settings.
// customhandler.Run drives everything else — with no arguments it serves on the
// port the host supplies via FUNCTIONS_CUSTOMHANDLER_PORT, and with
// "--emit-config <dir>" it performs build-time indexing, generating the
// function.json files from the same in-code registrations so the binding
// metadata never drifts from the code.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/azure/azure-functions-golang-worker/customhandler"
	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/sdk/bindings"
)

func main() {
	app := sdk.FunctionApp()

	// Cross-cutting middleware composes around every invocation, exactly as it
	// would under the gRPC worker (this is the shared sdk.RunInvocation core).
	app.Use(sdk.MiddlewareFunc(func(next sdk.Handler) sdk.Handler {
		return func(ctx context.Context, mc *sdk.MiddlewareContext) error {
			slog.InfoContext(ctx, "invoking function", "function", mc.FunctionName, "trigger", mc.TriggerType)
			return next(ctx, mc)
		}
	}))

	// Typed trigger registrations — the same programming model as a native app.
	app.HTTP("hello", hello, sdk.WithMethods("GET", "POST"), sdk.WithAuth("anonymous"))
	app.Timer("heartbeat", heartbeat, sdk.WithSchedule("0 */5 * * * *"))

	// One-line entry point (the custom-handler analogue of worker.Start):
	// `handler --emit-config <dir>` writes the function.json files; otherwise it
	// serves over the custom-handler protocol. WithForwardedHTTP delivers the raw
	// request/response to HTTP handlers.
	customhandler.Run(app, customhandler.WithForwardedHTTP())
}

// hello is a standard net/http handler — unchanged from a native-worker app.
func hello(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "world"
	}
	fmt.Fprintf(w, "Hello, %s!", name)
}

// heartbeat is a typed timer handler.
func heartbeat(ctx context.Context, timer bindings.TimerInfo) error {
	slog.InfoContext(ctx, "heartbeat fired", "past_due", timer.IsPastDue)
	return nil
}
