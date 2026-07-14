package customhandler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/azure/azure-functions-golang-worker/sdk"
)

// customHandlerPortEnvVar is the environment variable the Functions host sets
// to tell a custom handler which port to listen on.
const customHandlerPortEnvVar = "FUNCTIONS_CUSTOMHANDLER_PORT"

// serveShutdownTimeout bounds graceful shutdown so a stuck middleware shutdown
// cannot delay process exit indefinitely.
const serveShutdownTimeout = 10 * time.Second

// hostJSONFileName is the host.json the custom handler runs alongside.
const hostJSONFileName = "host.json"

// validateHostConfig reports a deployment inconsistency when the app's
// HTTP-forwarding intent (WithForwardedHTTP) disagrees with the
// enableForwardingHttpRequest setting in host.json — the two must match or HTTP
// triggers misbehave. It is best-effort: it does nothing when the app has no
// HTTP triggers, or when host.json is absent or unreadable (for example when the
// handler is mounted inside another program via [Handler] rather than run
// standalone). It turns silent config drift into a loud, self-explaining
// startup error, so a forgotten EmitConfig fails fast instead of misbehaving.
func validateHostConfig(app *sdk.App, cfg *config, hostJSONPath string) error {
	if !appHasHTTPFunctions(app) {
		return nil
	}
	data, err := os.ReadFile(hostJSONPath)
	if err != nil {
		return nil
	}
	var hj struct {
		CustomHandler struct {
			EnableForwardingHTTPRequest bool `json:"enableForwardingHttpRequest"`
		} `json:"customHandler"`
	}
	if err := json.Unmarshal(data, &hj); err != nil {
		return nil
	}
	if hj.CustomHandler.EnableForwardingHTTPRequest != cfg.forwardHTTP {
		return fmt.Errorf("customhandler: host.json enableForwardingHttpRequest=%t but the app was built with HTTP forwarding=%t; "+
			"make them consistent (WithForwardedHTTP in code and enableForwardingHttpRequest in host.json), or re-run EmitConfig",
			hj.CustomHandler.EnableForwardingHTTPRequest, cfg.forwardHTTP)
	}
	return nil
}

// Serve runs the app as a standalone custom handler. It builds
// Handler(app, opts...), listens on FUNCTIONS_CUSTOMHANDLER_PORT (falling back
// to 8080), and blocks until the process receives SIGINT or SIGTERM, then
// gracefully drains the HTTP server and runs middleware shutdowns.
//
// Serve is the only entry point in this package that owns process-level
// concerns (reading the port env var, trapping signals). Embedders that host
// their own server — for example an OpenTelemetry Collector receiver — should
// mount [Handler] instead so none of that ownership leaks into their process.
func Serve(app *sdk.App, opts ...Option) error {
	if err := validateHostConfig(app, resolveConfig(opts...), hostJSONFileName); err != nil {
		return err
	}

	port := os.Getenv(customHandlerPortEnvVar)
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:              "127.0.0.1:" + port,
		Handler:           Handler(app, opts...),
		ReadHeaderTimeout: 30 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), serveShutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	return app.RunShutdowns(shutdownCtx)
}
