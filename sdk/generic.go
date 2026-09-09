package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"github.com/azure/azure-functions-golang-worker/sdk/bindings"
)

// GenericTrigger registers a host-provided non-HTTP trigger without a typed
// trigger package. The handler must be func(context.Context, T) error or
// func(context.Context, T) (R, error). It runs through the normal middleware
// chain. Registration panics on invalid metadata or handler signatures, like
// the other App registration methods. Caller-owned properties are snapshotted.
//
// Strings and byte slices receive the host payload unchanged, including JSON
// quoting. Other data types use JSON decoding. Host collections bind to slices;
// Cardinality "many" must be explicitly requested when the extension supports
// batching. SDK clients, HTTP, and trigger-specific execution protocols require
// their existing adapters. This method does not select a ClientFactory.
//
// A return value is only useful when consumed by the host trigger extension or
// a declared $return output binding. Named output bindings are not supported.
// Trigger-specific options such as WithQueueName do not apply;
// put their host configuration in trigger.Properties instead.
func (app *App) GenericTrigger(name string, f any, trigger *bindings.GenericTrigger, opts ...Option) *RegisteredFunction {
	return app.RegisterFunction(name, f, trigger, opts...)
}

var genericBindingName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

func validateGenericHandler(f any, cardinality string) {
	fail := func(message string) { panic("GenericTrigger: " + message) }
	ft := reflect.TypeOf(f)
	if ft == nil || ft.Kind() != reflect.Func || reflect.ValueOf(f).IsNil() {
		fail("handler must be a non-nil function")
	}
	if ft.IsVariadic() || ft.NumIn() != 2 {
		fail("handler must accept exactly 2 arguments (context.Context, payload)")
	}
	if ft.In(0) != reflect.TypeFor[context.Context]() {
		fail("handler first argument must be context.Context")
	}
	if ft.NumOut() < 1 || ft.NumOut() > 2 || ft.Out(ft.NumOut()-1) != reflect.TypeFor[error]() {
		fail("handler must return error or (result, error)")
	}
	if ft.NumOut() == 2 && ft.Out(0).Implements(reflect.TypeFor[error]()) {
		fail("handler result must not be another error")
	}
	pt := ft.In(1)
	if pt.Implements(reflect.TypeFor[context.Context]()) || pt.Implements(reflect.TypeFor[http.ResponseWriter]()) {
		fail("payload must be data, not a context or HTTP response writer")
	}
	seenPointers := make(map[reflect.Type]bool)
	for pt.Kind() == reflect.Ptr {
		if seenPointers[pt] {
			fail("payload cannot be a recursive pointer type")
		}
		seenPointers[pt] = true
		pt = pt.Elem()
	}
	switch pt.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.Struct, reflect.Map, reflect.Slice, reflect.Interface:
	default:
		fail(fmt.Sprintf("unsupported payload type %s; use text, bytes, or a JSON model", pt))
	}
	if cardinality == "many" && (pt.Kind() != reflect.Slice || pt.Elem().Kind() == reflect.Uint8) {
		fail("cardinality many requires a slice payload such as []Order or [][]byte, not a single []byte")
	}
}

// The low-level RegisterFunction path shares validation with GenericTrigger so
// a library can implement Bind without bypassing the generic payload contract.
func validateGenericRegistration(rf *RegisteredFunction) {
	fail := func(message string) { panic(fmt.Sprintf("GenericTrigger %q: %s", rf.FuncName, message)) }
	trigger := rf.RawBindings[0]
	if strings.TrimSpace(trigger.Type) == "" {
		fail("type must not be empty")
	}
	if strings.TrimSpace(trigger.Type) != trigger.Type || !strings.HasSuffix(strings.ToLower(trigger.Type), "trigger") {
		fail("type must end in Trigger and match an installed host extension")
	}
	if strings.EqualFold(trigger.Type, "httpTrigger") {
		fail("use App.HTTP for HTTP triggers")
	}
	if trigger.Type != rf.TriggerType || trigger.Direction != "in" {
		fail("trigger type and direction must match registration")
	}
	if rf.ClientFactory != nil {
		fail("SDK client injection requires a typed trigger adapter")
	}
	validateGenericHandler(rf.Func, trigger.GenericBinding.Cardinality)
	names := make(map[string]bool, len(rf.RawBindings))
	for i := range rf.RawBindings {
		b := &rf.RawBindings[i]
		key := strings.ToLower(b.Name)
		isReturn := b.Name == "$return" && b.Direction == "out"
		if (!isReturn && !genericBindingName.MatchString(b.Name)) || names[key] {
			fail("binding name must be valid and unique: " + b.Name)
		}
		names[key] = true
		if strings.TrimSpace(b.Type) == "" {
			fail("binding type must not be empty")
		}
		if i > 0 && strings.HasSuffix(strings.ToLower(b.Type), "trigger") {
			fail("only one trigger is allowed")
		}
		if b.Direction != "in" && !isReturn {
			fail("only input and $return output bindings are supported")
		}
		if _, err := json.Marshal(b); err != nil {
			fail(err.Error())
		}
		if g := b.GenericBinding; g != nil {
			// Preserve large integer configuration values while breaking every
			// caller-owned map/slice alias, including nested values.
			data, err := json.Marshal(g.Properties)
			if err != nil {
				fail(err.Error())
			}
			var properties map[string]any
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			if err := decoder.Decode(&properties); err != nil {
				fail(err.Error())
			}
			b.GenericBinding = &bindings.GenericBinding{DataType: g.DataType, Cardinality: g.Cardinality, Properties: properties}
		}
	}
}
