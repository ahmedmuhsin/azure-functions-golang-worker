package customhandler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/azure/azure-functions-golang-worker/sdk"
)

// reconstructRequest rebuilds an *http.Request from the host's serialized HTTP
// trigger payload so a net/http handler runs unchanged in envelope mode.
func reconstructRequest(ctx context.Context, raw json.RawMessage) (*http.Request, error) {
	var p httpRequestPayload
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
	}

	method := p.Method
	if method == "" {
		method = http.MethodPost
	}
	url := p.URL
	if url == "" {
		url = "/"
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(decodeBody(p.Body)))
	if err != nil {
		return nil, err
	}
	for k, vals := range p.Headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	// If the URL carried no query string, fold in the separate Query map so
	// r.URL.Query() reflects the host-supplied values.
	if req.URL.RawQuery == "" && len(p.Query) > 0 {
		q := req.URL.Query()
		for k, v := range p.Query {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}
	return req, nil
}

// decodeBody normalizes the host's Body field. The host may serialize the body
// as a JSON string (the common case) or as raw JSON; either is returned as the
// underlying bytes.
func decodeBody(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []byte(s)
	}
	return raw
}

// responseCapture is a minimal http.ResponseWriter that records status,
// headers, and body so an envelope-mode HTTP response can be serialized under
// Outputs["res"]. It avoids importing net/http/httptest into library code.
type responseCapture struct {
	header http.Header
	buf    bytes.Buffer
	status int
	wrote  bool
}

func newResponseCapture() *responseCapture {
	return &responseCapture{header: make(http.Header), status: http.StatusOK}
}

func (c *responseCapture) Header() http.Header { return c.header }

func (c *responseCapture) WriteHeader(status int) {
	if !c.wrote {
		c.status = status
		c.wrote = true
	}
}

func (c *responseCapture) Write(b []byte) (int, error) {
	c.wrote = true
	return c.buf.Write(b)
}

func (c *responseCapture) toPayload() httpResponsePayload {
	headers := make(map[string]string, len(c.header))
	for k := range c.header {
		headers[k] = c.header.Get(k)
	}
	return httpResponsePayload{
		StatusCode: c.status,
		Body:       c.buf.String(),
		Headers:    headers,
	}
}

// buildInvocationContext assembles the per-invocation sdk.InvocationContext
// from host metadata and request headers.
func buildInvocationContext(rf *sdk.RegisteredFunction, metadata map[string]json.RawMessage, header http.Header) *sdk.InvocationContext {
	ic := &sdk.InvocationContext{
		FunctionID:      rf.FuncId,
		FunctionName:    rf.FuncName,
		TriggerType:     rf.TriggerType,
		TriggerMetadata: flattenMetadata(metadata),
		InvocationID:    invocationID(metadata, header),
	}
	if tp := header.Get("traceparent"); tp != "" {
		ic.TraceContext = sdk.TraceContext{
			TraceParent: tp,
			TraceState:  header.Get("tracestate"),
		}
	}
	return ic
}

// flattenMetadata reduces the host's Metadata map to string values, matching
// how the gRPC worker exposes TriggerMetadata. JSON string values are unquoted;
// non-string values keep their compact JSON encoding.
func flattenMetadata(metadata map[string]json.RawMessage) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for k, raw := range metadata {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			out[k] = s
			continue
		}
		out[k] = string(raw)
	}
	return out
}

// invocationID resolves an invocation id from the correlation header, then the
// host "sys" metadata, then a generated fallback so telemetry always has a
// stable key for the invocation.
func invocationID(metadata map[string]json.RawMessage, header http.Header) string {
	if id := header.Get("x-ms-invocation-id"); id != "" {
		return id
	}
	if raw, ok := metadata["sys"]; ok {
		var sys struct {
			RandGuid string `json:"RandGuid"`
		}
		if err := json.Unmarshal(raw, &sys); err == nil && sys.RandGuid != "" {
			return sys.RandGuid
		}
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// triggerConfig marshals the trigger binding into the map form a ClientFactory
// expects, mirroring the worker's extraction from RawBindings[0].
func triggerConfig(rf *sdk.RegisteredFunction) map[string]any {
	config := make(map[string]any)
	if b := rf.TriggerBinding(); b != nil {
		if raw, err := json.Marshal(b); err == nil {
			_ = json.Unmarshal(raw, &config)
		}
	}
	return config
}

// zeroArg produces a usable zero value for a handler argument type: a fresh
// allocation for pointers (so decoding can populate it) and the zero value
// otherwise. Mirrors the worker's pre-allocation.
func zeroArg(t reflect.Type) reflect.Value {
	if t.Kind() == reflect.Ptr {
		return reflect.New(t.Elem())
	}
	return reflect.Zero(t)
}

// decodeInto unmarshals a trigger payload into a new value of type t.
func decodeInto(t reflect.Type, raw json.RawMessage) (reflect.Value, error) {
	ptr := reflect.New(t)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
			return reflect.Value{}, err
		}
	}
	return ptr.Elem(), nil
}

// writeJSON serializes v as the invocation response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// statusForResult maps an invocation outcome to the envelope HTTP status: a
// panic or a middleware/handler error is a 500 (the host treats a non-2xx
// response as a failed invocation); success is 200.
func statusForResult(recovered any, err error) int {
	if recovered != nil || err != nil {
		return http.StatusInternalServerError
	}
	return http.StatusOK
}

// tryWriteError best-effort writes an error status if the handler has not
// already committed a response.
func tryWriteError(w http.ResponseWriter, status int) {
	defer func() { _ = recover() }()
	w.WriteHeader(status)
}
