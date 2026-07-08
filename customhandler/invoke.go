package customhandler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"reflect"

	"github.com/azure/azure-functions-golang-worker/sdk"
)

var (
	ctxType            = reflect.TypeOf((*context.Context)(nil)).Elem()
	responseWriterType = reflect.TypeOf((*http.ResponseWriter)(nil)).Elem()
	requestType        = reflect.TypeOf((*http.Request)(nil))
	errorType          = reflect.TypeOf((*error)(nil)).Elem()
)

// loadedFn precomputes the reflection metadata needed to dispatch to a
// registered function, so ServeHTTP does no per-request type discovery. It is
// the custom-handler analogue of the worker's LoadedFunction.
type loadedFn struct {
	rf          *sdk.RegisteredFunction
	ft          reflect.Type
	triggerName string
	isHTTP      bool

	ctxIdx     []int // positions of context.Context arguments
	writerIdx  int   // position of http.ResponseWriter, or -1
	requestIdx int   // position of *http.Request, or -1
	dataIdx    int   // position of the typed trigger data / client argument, or -1
}

func newLoadedFn(rf *sdk.RegisteredFunction) *loadedFn {
	ft := reflect.TypeOf(rf.Func)
	lf := &loadedFn{
		rf:         rf,
		ft:         ft,
		isHTTP:     isHTTPTrigger(rf),
		writerIdx:  -1,
		requestIdx: -1,
		dataIdx:    -1,
	}
	if b := rf.TriggerBinding(); b != nil {
		lf.triggerName = b.Name
	}
	for i := 0; i < ft.NumIn(); i++ {
		switch t := ft.In(i); {
		case t.Implements(ctxType):
			lf.ctxIdx = append(lf.ctxIdx, i)
		case t == responseWriterType:
			lf.writerIdx = i
		case t == requestType:
			lf.requestIdx = i
		case lf.dataIdx == -1:
			lf.dataIdx = i
		}
	}
	return lf
}

// serveEnvelope handles a POST /{functionName} invocation carrying a JSON
// InvokeRequest and returns a JSON InvokeResponse.
func (d *dispatcher) serveEnvelope(lf *loadedFn, w http.ResponseWriter, r *http.Request) {
	var req InvokeRequest
	if r.Body != nil {
		// A malformed or empty body is tolerated: triggers with no payload
		// (e.g. a timer firing) still invoke with zero-valued data.
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	ic := buildInvocationContext(lf.rf, req.Metadata, r.Header)
	mc := &sdk.MiddlewareContext{InvocationContext: ic}
	ctx := sdk.ContextWithMiddleware(r.Context(), mc)

	args, capture, err := d.bindEnvelopeArgs(ctx, lf, ic, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	returnVal, invokeErr, recovered, stack := d.runInvocation(ctx, mc, lf, args)
	if recovered != nil {
		d.cfg.logger.LogAttrs(ctx, slog.LevelError, "custom handler: user function panicked",
			slog.String("function", lf.rf.FuncName),
			slog.Any("panic", recovered),
			slog.String("stack", stack),
		)
	}

	resp := InvokeResponse{}
	switch {
	case lf.isHTTP && capture != nil:
		resp.Outputs = map[string]any{"res": capture.toPayload()}
	case returnVal != nil:
		resp.ReturnValue = returnVal
	}

	writeJSON(w, statusForResult(recovered, invokeErr), resp)
}

// serveForwardedHTTP handles a raw forwarded HTTP request: the user handler
// runs against the real response writer, so streaming and native net/http
// semantics are preserved.
func (d *dispatcher) serveForwardedHTTP(lf *loadedFn, w http.ResponseWriter, r *http.Request) {
	ic := buildInvocationContext(lf.rf, nil, r.Header)
	mc := &sdk.MiddlewareContext{InvocationContext: ic}
	ctx := sdk.ContextWithMiddleware(r.Context(), mc)

	args := make([]reflect.Value, lf.ft.NumIn())
	for i := 0; i < lf.ft.NumIn(); i++ {
		args[i] = zeroArg(lf.ft.In(i))
	}
	if lf.writerIdx >= 0 {
		args[lf.writerIdx] = reflect.ValueOf(w)
	}
	if lf.requestIdx >= 0 {
		args[lf.requestIdx] = reflect.ValueOf(r)
	}

	_, _, recovered, stack := d.runInvocation(ctx, mc, lf, args)
	if recovered != nil {
		d.cfg.logger.LogAttrs(ctx, slog.LevelError, "custom handler: user function panicked",
			slog.String("function", lf.rf.FuncName),
			slog.Any("panic", recovered),
			slog.String("stack", stack),
		)
		tryWriteError(w, http.StatusInternalServerError)
	}
}

// bindEnvelopeArgs builds the reflected argument list for an envelope
// invocation. For HTTP triggers it reconstructs the request and returns a
// response capture; for typed triggers it decodes the trigger payload; for
// extension triggers it invokes the registered ClientFactory.
func (d *dispatcher) bindEnvelopeArgs(ctx context.Context, lf *loadedFn, ic *sdk.InvocationContext, req InvokeRequest) ([]reflect.Value, *responseCapture, error) {
	args := make([]reflect.Value, lf.ft.NumIn())
	for i := 0; i < lf.ft.NumIn(); i++ {
		args[i] = zeroArg(lf.ft.In(i))
	}
	for _, i := range lf.ctxIdx {
		args[i] = reflect.ValueOf(ctx)
	}

	if lf.isHTTP {
		hr, err := reconstructRequest(ctx, req.Data[lf.triggerName])
		if err != nil {
			return nil, nil, err
		}
		capture := newResponseCapture()
		if lf.writerIdx >= 0 {
			args[lf.writerIdx] = reflect.ValueOf(http.ResponseWriter(capture))
		}
		if lf.requestIdx >= 0 {
			args[lf.requestIdx] = reflect.ValueOf(hr)
		}
		return args, capture, nil
	}

	if lf.dataIdx >= 0 {
		if lf.rf.ClientFactory != nil {
			client, err := lf.rf.ClientFactory(triggerConfig(lf.rf), ic.TriggerMetadata)
			if err != nil {
				return nil, nil, err
			}
			args[lf.dataIdx] = reflect.ValueOf(client)
		} else {
			v, err := decodeInto(lf.ft.In(lf.dataIdx), req.Data[lf.triggerName])
			if err != nil {
				return nil, nil, err
			}
			args[lf.dataIdx] = v
		}
	}
	return args, nil, nil
}

// runInvocation builds the transport-specific inner Handler (ctx re-injection,
// reflective call, return-value capture) and hands it to [sdk.RunInvocation],
// which owns middleware composition and panic recovery — the same execution
// core the gRPC worker uses. The contract mirrors the worker's path so both
// transports behave identically.
func (d *dispatcher) runInvocation(ctx context.Context, mc *sdk.MiddlewareContext, lf *loadedFn, args []reflect.Value) (returnVal any, err error, recovered any, stack string) {
	inner := func(ctx context.Context, _ *sdk.MiddlewareContext) error {
		// Re-inject the (possibly middleware-enriched) ctx into the argument
		// list right before the call, matching the worker's contract.
		for _, i := range lf.ctxIdx {
			args[i] = reflect.ValueOf(ctx)
		}
		if lf.requestIdx >= 0 {
			if r, ok := args[lf.requestIdx].Interface().(*http.Request); ok && r != nil {
				args[lf.requestIdx] = reflect.ValueOf(r.WithContext(ctx))
			}
		}
		results := reflect.ValueOf(lf.rf.Func).Call(args)
		for _, res := range results {
			if res.Type().Implements(errorType) {
				if !res.IsNil() {
					return res.Interface().(error)
				}
				continue
			}
			if returnVal == nil {
				returnVal = res.Interface()
			}
		}
		return nil
	}

	recovered, stack, err = sdk.RunInvocation(ctx, mc, d.app, inner)
	return returnVal, err, recovered, stack
}
