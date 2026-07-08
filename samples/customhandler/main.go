// Command customhandler-sample is a runnable Azure Functions app that serves
// the Go worker programming model over the custom-handler protocol instead of
// the native gRPC worker. The function code is identical to what a native
// worker app would write; only the entry point differs.
//
// Build and run under the Functions host:
//
//	go build -o handler .            # Linux/macOS (use handler.exe on Windows)
//	./handler --emit-config .        # generate host.json + <func>/function.json
//	func start                       # the host launches ./handler as a custom handler
//
// With no arguments the binary runs customhandler.Serve, listening on the port
// the host supplies via FUNCTIONS_CUSTOMHANDLER_PORT. The --emit-config step is
// build-time indexing: the Functions contract is generated from the same
// in-code registrations, so there is no hand-written function.json to drift.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

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

	// Build-time indexing path: `handler --emit-config <dir>` writes host.json
	// and one function.json per registered function, generated from the
	// registry above.
	if len(os.Args) > 2 && os.Args[1] == "--emit-config" {
		if err := customhandler.EmitConfig(app, os.Args[2],
			customhandler.WithExecutable(executableName()),
			customhandler.WithForwardingConfig(true),
		); err != nil {
			fmt.Fprintln(os.Stderr, "emit-config:", err)
			os.Exit(1)
		}
		fmt.Println("wrote host.json and function.json to", os.Args[2])
		return
	}

	// Runtime path: serve the app over the custom-handler HTTP protocol. With
	// forwarding enabled, HTTP triggers receive the raw request/response.
	if err := customhandler.Serve(app, customhandler.WithForwardedHTTP()); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
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

// executableName reports the running binary's file name so the generated
// host.json points defaultExecutablePath at whatever the binary was built as.
func executableName() string {
	if len(os.Args) > 0 && os.Args[0] != "" {
		return filepath.Base(os.Args[0])
	}
	return "handler"
}
