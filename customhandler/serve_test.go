package customhandler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/sdk/bindings"
)

func TestValidateHostConfig(t *testing.T) {
	writeHostJSON := func(t *testing.T, forwarding bool) string {
		t.Helper()
		body := `{"customHandler":{"enableForwardingHttpRequest":false}}`
		if forwarding {
			body = `{"customHandler":{"enableForwardingHttpRequest":true}}`
		}
		p := filepath.Join(t.TempDir(), "host.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	httpApp := func() *sdk.App {
		app := sdk.FunctionApp()
		app.HTTP("hello", func(http.ResponseWriter, *http.Request) {})
		return app
	}

	t.Run("mismatch is reported", func(t *testing.T) {
		cfg := &config{forwardHTTP: true}
		if err := validateHostConfig(httpApp(), cfg, writeHostJSON(t, false)); err == nil {
			t.Fatal("expected a mismatch error, got nil")
		}
	})

	t.Run("match is accepted", func(t *testing.T) {
		cfg := &config{forwardHTTP: true}
		if err := validateHostConfig(httpApp(), cfg, writeHostJSON(t, true)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing host.json is ignored", func(t *testing.T) {
		cfg := &config{forwardHTTP: true}
		if err := validateHostConfig(httpApp(), cfg, filepath.Join(t.TempDir(), "absent.json")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("no HTTP functions is ignored", func(t *testing.T) {
		app := sdk.FunctionApp()
		app.Timer("cron", func(context.Context, bindings.TimerInfo) error { return nil })
		cfg := &config{forwardHTTP: true}
		if err := validateHostConfig(app, cfg, writeHostJSON(t, false)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
