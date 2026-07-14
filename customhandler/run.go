package customhandler

import (
	"fmt"
	"os"

	"github.com/azure/azure-functions-golang-worker/sdk"
)

// emitConfigFlag is the process argument [Run] recognizes to switch from
// serving to build-time function.json generation.
const emitConfigFlag = "--emit-config"

// Run is the one-line entry point for a standalone custom-handler app, the
// custom-handler analogue of worker.Start for the gRPC worker. It dispatches on
// the process arguments:
//
//   - "<binary> --emit-config [dir]" writes the function.json files (via
//     [EmitFunctions]) to dir, defaulting to the current directory, then
//     returns. This is the build/publish indexing step.
//   - otherwise it runs [Serve], blocking until the host terminates the process.
//
// Run owns process-level concerns (argument parsing and os.Exit on error), so
// it is only appropriate for a standalone main(). Embedders that host their own
// server should mount [Handler]; callers that want a different CLI should call
// [EmitFunctions] and [Serve] directly.
//
// Run does not write host.json: that file is user/template-owned, as in any
// Functions app.
func Run(app *sdk.App, opts ...Option) {
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] != emitConfigFlag {
			continue
		}
		dir := "."
		if i+1 < len(os.Args) {
			dir = os.Args[i+1]
		}
		if err := EmitFunctions(app, dir); err != nil {
			fmt.Fprintln(os.Stderr, "customhandler: emit-config:", err)
			os.Exit(1)
		}
		return
	}

	if err := Serve(app, opts...); err != nil {
		fmt.Fprintln(os.Stderr, "customhandler: serve:", err)
		os.Exit(1)
	}
}
