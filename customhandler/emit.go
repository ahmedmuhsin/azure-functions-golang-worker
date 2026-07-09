package customhandler

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/azure/azure-functions-golang-worker/sdk"
)

// EmitFunctions writes one <outDir>/<functionName>/function.json per registered
// function, generated from the app's in-code registrations.
//
// It is the build-time analogue of the worker's runtime worker-driven indexing:
// the same RawBindings the gRPC worker sends the host in a
// FunctionMetadataResponse are serialized to the function.json files the stock
// host reads from disk. Because both derive from the same registrations, the
// gRPC and custom-handler deployments describe identical functions — the
// on-disk metadata cannot drift from the code.
//
// EmitFunctions deliberately does not write host.json. That file carries
// user/deployment-owned settings (extension bundle, logging, and the
// customHandler executable and port) and is supplied from a template, exactly
// as host.json is authored by hand in non-custom-handler apps.
//
// It is typically invoked as a build/publish step from the same binary that
// serves the app — see [Run], which wires the "--emit-config" convention — or
// called directly:
//
//	if err := customhandler.EmitFunctions(app, "."); err != nil { ... }
func EmitFunctions(app *sdk.App, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	var emitErr error
	app.GetRegisteredFunctions().Range(func(_, v any) bool {
		rf := v.(*sdk.RegisteredFunction)
		if err := writeFunctionJSON(outDir, rf); err != nil {
			emitErr = err
			return false
		}
		return true
	})
	return emitErr
}

// writeFunctionJSON serializes one function's bindings to
// <outDir>/<functionName>/function.json. The implicit HTTP $return binding the
// worker appends is rewritten to the custom-handler "res" output binding.
func writeFunctionJSON(outDir string, rf *sdk.RegisteredFunction) error {
	rawBindings := make([]json.RawMessage, 0, len(rf.RawBindings))
	for _, b := range rf.RawBindings {
		if b.Type == "http" && b.Direction == "out" && b.Name == "$return" {
			b.Name = "res"
		}
		raw, err := json.Marshal(b)
		if err != nil {
			return err
		}
		rawBindings = append(rawBindings, raw)
	}

	doc := map[string]any{"bindings": rawBindings}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Join(outDir, rf.FuncName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "function.json"), append(data, '\n'), 0o644)
}
