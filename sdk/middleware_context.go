// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package sdk

import (
	"context"
	"sync"
)

// MiddlewareContext carries per-invocation state that flows through the
// worker's middleware chain. It is the framework / middleware-integration
// layer's working space: things the worker and middleware need to
// coordinate but that user-facing handlers don't normally interact with.
//
// MiddlewareContext embeds [*InvocationContext], so the trigger-side
// fields (InvocationID, FunctionName, TraceContext, etc.) are promoted
// and reachable directly:
//
//	mc, ok := sdk.MiddlewareContextFrom(ctx)
//	if ok {
//	    mc.SetOutboundTraceAttribute("tenant", tenant) // framework method
//	    fmt.Println(mc.FunctionName)                    // promoted from InvocationContext
//	}
//
// Authors of custom middleware can use the wrapper to reach state the
// worker dispatcher reads (e.g. outbound trace attributes). User code
// should call the standard observability APIs (slog, span.SetAttributes)
// instead; middleware/otelfunc coordinates through MiddlewareContext on
// the user's behalf.
type MiddlewareContext struct {
	*InvocationContext

	mu                 sync.Mutex
	outboundTraceAttrs map[string]string
	outputs            map[string]any
}

// ContextWithMiddleware returns a context that carries the given
// MiddlewareContext. The name mirrors context.WithValue / context.WithCancel:
// a derived context.Context carrying the supplied value.
//
// The worker dispatcher calls this once per invocation, before the
// middleware chain runs. Most tests should use [NewContext] instead —
// it wraps the given InvocationContext in a fresh MiddlewareContext
// implicitly.
func ContextWithMiddleware(parent context.Context, mc *MiddlewareContext) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithValue(parent, invocationContextKey{}, mc)
}

// MiddlewareContextFrom returns the *MiddlewareContext stored in ctx by
// the worker dispatcher, if any. The boolean is false when ctx was not
// produced by the dispatcher.
//
// Intended for middleware and framework integration code (e.g. the
// worker dispatcher reading recorded outbound trace attributes, or
// middleware/otelfunc writing them after harvest). User code that needs
// trigger metadata should use [FromContext] instead — it returns the
// embedded *InvocationContext directly.
func MiddlewareContextFrom(ctx context.Context) (*MiddlewareContext, bool) {
	if ctx == nil {
		return nil, false
	}
	mc, ok := ctx.Value(invocationContextKey{}).(*MiddlewareContext)
	return mc, ok && mc != nil
}

// SetOutboundTraceAttribute records a key/value pair for the active transport
// to forward to the host. The gRPC worker sends them on
// InvocationResponse.TraceContextAttributes, and the host applies each entry as
// a tag on its parent activity via Activity.AddTag(k, v), surfacing them on the
// host-emitted "request" record in Application Insights. A transport with no
// slot for outbound trace attributes (for example the custom handler) records
// them but does not forward them.
//
// Intended for middleware integration. User code that wants to tag the
// host's parent span should call span.SetAttributes on the worker
// invocation span instead and let middleware/otelfunc auto-harvest.
//
// Safe to call from multiple goroutines.
func (mc *MiddlewareContext) SetOutboundTraceAttribute(key, value string) {
	if mc == nil {
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if mc.outboundTraceAttrs == nil {
		mc.outboundTraceAttrs = make(map[string]string, 4)
	}
	mc.outboundTraceAttrs[key] = value
}

// OutboundTraceAttributes returns the recorded outbound trace
// attributes, or nil when none have been recorded. The returned map is
// the live backing store; callers needing an immutable snapshot should
// copy it.
//
// Intended for the worker dispatcher's response builder.
func (mc *MiddlewareContext) OutboundTraceAttributes() map[string]string {
	if mc == nil {
		return nil
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	return mc.outboundTraceAttrs
}

// SetOutput records a value for a named output binding. The active transport
// encodes recorded outputs into its invocation response — the custom handler
// into InvokeResponse.Outputs; a transport that does not surface named outputs
// (today, the gRPC worker path) ignores them.
//
// This lets a handler acknowledge an invocation as successful while still
// routing data to an output binding — for example dead-lettering an
// unprocessable message instead of failing the invocation.
//
// Safe to call from multiple goroutines.
func (mc *MiddlewareContext) SetOutput(name string, value any) {
	if mc == nil {
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if mc.outputs == nil {
		mc.outputs = make(map[string]any, 2)
	}
	mc.outputs[name] = value
}

// Outputs returns the recorded named output bindings, or nil when none have
// been set. The returned map is the live backing store; callers needing an
// immutable snapshot should copy it. Intended for the active transport's
// response builder.
func (mc *MiddlewareContext) Outputs() map[string]any {
	if mc == nil {
		return nil
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	return mc.outputs
}
