package customhandler

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/azure/azure-functions-golang-worker/sdk"
)

// defaultExtensionBundle is the extension-bundle range written into a generated
// host.json. It matches the range the host samples ship with.
const defaultExtensionBundle = "[4.*, 5.0.0)"

// emitConfig holds resolved [EmitConfig] options.
type emitConfig struct {
	executable  string
	arguments   []string
	forwardHTTP bool
	bundle      string
}

// EmitOption configures [EmitConfig].
type EmitOption func(*emitConfig)

// WithExecutable sets host.json customHandler.description.defaultExecutablePath
// — the compiled handler binary the host launches. Defaults to "handler".
func WithExecutable(path string) EmitOption {
	return func(c *emitConfig) { c.executable = path }
}

// WithArguments sets host.json customHandler.description.arguments.
func WithArguments(args ...string) EmitOption {
	return func(c *emitConfig) { c.arguments = args }
}

// WithForwardingConfig sets host.json customHandler.enableForwardingHttpRequest.
// Keep it consistent with whether the handler was built with
// [WithForwardedHTTP].
func WithForwardingConfig(enabled bool) EmitOption {
	return func(c *emitConfig) { c.forwardHTTP = enabled }
}

// WithExtensionBundle overrides the extension-bundle version range in host.json.
func WithExtensionBundle(version string) EmitOption {
	return func(c *emitConfig) { c.bundle = version }
}

// EmitConfig writes the on-disk Functions configuration a custom-handler
// deployment needs, generated from the app's in-code registrations:
//
//   - <outDir>/<functionName>/function.json for each registered function
//   - <outDir>/host.json with the customHandler section
//
// It is the build-time analogue of the worker's runtime worker-driven indexing:
// the same RawBindings the gRPC worker sends to the host in a
// FunctionMetadataResponse are serialized to the function.json files the stock
// host reads from disk. Because both derive from the same registrations, the
// gRPC and custom-handler deployments describe identical functions — the
// on-disk JSON cannot drift from the code.
//
// Typical use is a build/publish step invoked from the same binary that serves
// the app (so the registry is the single source of truth):
//
//	if len(os.Args) > 1 && os.Args[1] == "--emit-config" {
//	    _ = customhandler.EmitConfig(app, os.Args[2])
//	    return
//	}
//	customhandler.Serve(app)
func EmitConfig(app *sdk.App, outDir string, opts ...EmitOption) error {
	cfg := &emitConfig{
		executable: "handler",
		bundle:     defaultExtensionBundle,
	}
	for _, o := range opts {
		o(cfg)
	}

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
	if emitErr != nil {
		return emitErr
	}

	return writeHostJSON(outDir, cfg)
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

// writeHostJSON writes the host.json with the customHandler section.
func writeHostJSON(outDir string, cfg *emitConfig) error {
	description := map[string]any{
		"defaultExecutablePath": cfg.executable,
	}
	if len(cfg.arguments) > 0 {
		description["arguments"] = cfg.arguments
	}

	doc := map[string]any{
		"version": "2.0",
		"extensionBundle": map[string]any{
			"id":      "Microsoft.Azure.Functions.ExtensionBundle",
			"version": cfg.bundle,
		},
		"customHandler": map[string]any{
			"description":                 description,
			"enableForwardingHttpRequest": cfg.forwardHTTP,
		},
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "host.json"), append(data, '\n'), 0o644)
}
