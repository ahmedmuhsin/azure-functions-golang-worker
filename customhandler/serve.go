package customhandler

import (
	"context"
	"errors"
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
