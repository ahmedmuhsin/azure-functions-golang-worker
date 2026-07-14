package customhandler

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/sdk/bindings"
)

// config holds the resolved options for a [Handler] or [Serve] call.
type config struct {
	forwardHTTP bool
	logger      *slog.Logger
}

// Option configures [Handler] and [Serve].
type Option func(*config)

// WithForwardedHTTP makes HTTP-triggered functions receive the raw forwarded
// HTTP request and write directly to the real response, matching a host.json
// with "enableForwardingHttpRequest": true. Non-HTTP triggers still use the
// JSON envelope. Without this option, HTTP triggers use the envelope too: the
// request is reconstructed from the payload and the response is serialized
// under Outputs["res"].
//
// This is the option a component such as an OpenTelemetry Collector receiver
// wants when it needs the raw request (headers, body) delivered to its
// handler. Keep it consistent with the enableForwardingHttpRequest value
// [EmitConfig] writes into host.json.
func WithForwardedHTTP() Option {
	return func(c *config) { c.forwardHTTP = true }
}

// WithLogger sets the logger used for dispatch diagnostics (panics, decode
// failures). Defaults to slog.Default. Passing an explicit logger keeps the
// handler from writing to the process-global logger — appropriate when the
// handler is embedded in another program.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

// resolveConfig applies opts onto a default config.
func resolveConfig(opts ...Option) *config {
	cfg := &config{logger: slog.Default()}
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

// Handler builds an [http.Handler] that serves the app's registered functions
// over the custom-handler protocol. It owns nothing at process scope, so it is
// safe to mount on a listener the caller already owns.
//
// Routing:
//   - GET / is answered 200 (the host's startup readiness probe).
//   - POST /{functionName} is an envelope invocation for any trigger type.
//   - With [WithForwardedHTTP], a request whose path matches an HTTP function's
//     route (e.g. /api/hello) is dispatched to that handler with the raw
//     request and response.
func Handler(app *sdk.App, opts ...Option) http.Handler {
	cfg := resolveConfig(opts...)

	d := &dispatcher{
		app:     app,
		cfg:     cfg,
		byName:  make(map[string]*loadedFn),
		byRoute: make(map[string]*loadedFn),
	}
	app.GetRegisteredFunctions().Range(func(_, v any) bool {
		rf := v.(*sdk.RegisteredFunction)
		lf := newLoadedFn(rf)
		d.byName[strings.ToLower(rf.FuncName)] = lf
		if lf.isHTTP && cfg.forwardHTTP {
			d.byRoute[routePath(rf)] = lf
		}
		return true
	})
	return d
}

// dispatcher is the http.Handler returned by [Handler]. The route tables are
// built once at construction and read-only thereafter, so ServeHTTP needs no
// locking.
type dispatcher struct {
	app     *sdk.App
	cfg     *config
	byName  map[string]*loadedFn // keyed by lowercased function name (envelope path)
	byRoute map[string]*loadedFn // keyed by lowercased route path (forwarded HTTP)
}

func (d *dispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Startup readiness probe: the host GETs / before sending invocations.
	if r.Method == http.MethodGet && r.URL.Path == "/" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Forwarded-HTTP mode: match the original route and hand the raw request
	// and response to the user handler with no envelope in between.
	if d.cfg.forwardHTTP {
		if lf, ok := d.byRoute[strings.ToLower(r.URL.Path)]; ok {
			d.serveForwardedHTTP(lf, w, r)
			return
		}
	}

	// Envelope mode: the host POSTs to /{functionName}.
	name := strings.ToLower(strings.Trim(r.URL.Path, "/"))
	lf, ok := d.byName[name]
	if !ok {
		http.Error(w, "no function named "+name, http.StatusNotFound)
		return
	}
	d.serveEnvelope(lf, w, r)
}

// routePath returns the path the host forwards an HTTP trigger's requests to,
// i.e. the route prefix plus the trigger's route (defaulting to the function
// name). Only the default "api" prefix is modeled here.
func routePath(rf *sdk.RegisteredFunction) string {
	route := rf.FuncName
	if b := rf.TriggerBinding(); b != nil && b.HTTPBinding != nil && b.HTTPBinding.Route != "" {
		route = b.HTTPBinding.Route
	}
	return "/api/" + strings.ToLower(strings.Trim(route, "/"))
}

// isHTTPTrigger reports whether rf is registered as an HTTP trigger.
func isHTTPTrigger(rf *sdk.RegisteredFunction) bool {
	return rf.TriggerType == string(bindings.HTTPTriggerType)
}

// appHasHTTPFunctions reports whether the app registered any HTTP trigger.
func appHasHTTPFunctions(app *sdk.App) bool {
	found := false
	app.GetRegisteredFunctions().Range(func(_, v any) bool {
		if isHTTPTrigger(v.(*sdk.RegisteredFunction)) {
			found = true
			return false
		}
		return true
	})
	return found
}
