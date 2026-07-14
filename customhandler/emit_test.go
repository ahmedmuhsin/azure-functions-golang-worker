package customhandler

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/sdk/bindings"
)

func TestEmitFunctions_GeneratesFunctionJSON(t *testing.T) {
	app := sdk.FunctionApp()
	app.HTTP("hello", func(_ http.ResponseWriter, _ *http.Request) {}, sdk.WithMethods("GET", "POST"))
	app.Timer("cron", func(_ context.Context, _ bindings.TimerInfo) error { return nil }, sdk.WithSchedule("0 */5 * * * *"))

	out := t.TempDir()
	if err := EmitFunctions(app, out); err != nil {
		t.Fatalf("EmitFunctions: %v", err)
	}

	// HTTP function.json: httpTrigger in + http out rewritten to "res".
	httpBindings := readBindings(t, filepath.Join(out, "hello", "function.json"))
	assertBinding(t, httpBindings, "httpTrigger", "in", "req")
	res := assertBinding(t, httpBindings, "http", "out", "res")
	if res["name"] == "$return" {
		t.Error("http out binding should be rewritten from $return to res")
	}

	// Timer function.json: timerTrigger with the schedule preserved.
	timerBindings := readBindings(t, filepath.Join(out, "cron", "function.json"))
	trigger := assertBinding(t, timerBindings, "timerTrigger", "in", "timer")
	if trigger["schedule"] != "0 */5 * * * *" {
		t.Errorf("timer schedule = %v, want the registered cron", trigger["schedule"])
	}
}

func TestEmitFunctions_DoesNotWriteHostJSON(t *testing.T) {
	app := sdk.FunctionApp()
	app.HTTP("hello", func(_ http.ResponseWriter, _ *http.Request) {})

	out := t.TempDir()
	if err := EmitFunctions(app, out); err != nil {
		t.Fatalf("EmitFunctions: %v", err)
	}

	// host.json is user/template-owned; EmitFunctions must not create it.
	if _, err := os.Stat(filepath.Join(out, "host.json")); !os.IsNotExist(err) {
		t.Errorf("host.json should not be written by EmitFunctions (stat err = %v)", err)
	}
}

// readBindings reads a function.json and returns its bindings array as maps.
func readBindings(t *testing.T, path string) []map[string]any {
	t.Helper()
	var doc struct {
		Bindings []map[string]any `json:"bindings"`
	}
	readJSON(t, path, &doc)
	return doc.Bindings
}

// assertBinding asserts that a binding with the given type/direction/name
// exists and returns it.
func assertBinding(t *testing.T, all []map[string]any, typ, dir, name string) map[string]any {
	t.Helper()
	for _, b := range all {
		if b["type"] == typ && b["direction"] == dir && b["name"] == name {
			return b
		}
	}
	t.Fatalf("no binding {type:%s direction:%s name:%s} in %+v", typ, dir, name, all)
	return nil
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}
