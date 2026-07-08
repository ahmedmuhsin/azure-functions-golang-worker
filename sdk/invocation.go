package sdk

import (
	"context"
	"runtime/debug"
)

// RunInvocation composes app's middleware chain around inner, executes the
// resulting Handler, and converts a panic into a recovered value plus stack
// trace instead of letting it unwind the caller's goroutine.
//
// It is the transport-neutral execution core shared by every worker driver:
// the gRPC worker (worker.Start) and the custom-handler transport
// (customhandler) both build a transport-specific inner Handler — which binds
// arguments from the wire format and performs the reflective call — and hand
// it to RunInvocation so middleware composition and panic handling behave
// identically regardless of transport.
//
// The contract:
//
//   - If inner panics, recovered carries the recovered value and stack the
//     formatted stack trace captured at recover time; err is nil.
//   - If inner (or any middleware) returns an error, it is returned as err.
//   - On success, all three return values are zero.
//
// app may be nil (a driver with no registered middleware may pass nil); in
// that case inner is invoked directly without composition.
func RunInvocation(ctx context.Context, mc *MiddlewareContext, app *App, inner Handler) (recovered any, stack string, err error) {
	defer func() {
		if r := recover(); r != nil {
			recovered = r
			stack = string(debug.Stack())
		}
	}()

	if app == nil {
		return nil, "", inner(ctx, mc)
	}
	return nil, "", app.Compose(inner)(ctx, mc)
}
