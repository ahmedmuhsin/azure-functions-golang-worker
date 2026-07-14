package sdk

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoTransportImports enforces that the sdk module — the transport-neutral
// programming model — depends only on the standard library and its own
// sdk/bindings subpackage. It is the machine-checked guard behind the
// module-boundary guarantee: a transport type (gRPC, protobuf, the worker
// package, HTTP-envelope code, and so on) must never leak into the programming
// model. If this test fails, the separation between the programming model and
// its transports has been broken.
func TestNoTransportImports(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if !importAllowed(p) {
				t.Errorf("%s imports %q; the sdk package must import only the standard library and sdk/bindings", path, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk sdk sources: %v", err)
	}
}

// importAllowed reports whether an import path is permitted in the sdk module.
// Standard-library paths (whose first segment contains no ".") and the module's
// own packages are allowed; everything else is a transport/third-party leak.
func importAllowed(path string) bool {
	if first, _, _ := strings.Cut(path, "/"); !strings.Contains(first, ".") {
		return true // standard library
	}
	return strings.HasPrefix(path, "github.com/azure/azure-functions-golang-worker/sdk")
}
