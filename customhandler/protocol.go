package customhandler

import "encoding/json"

// InvokeRequest is the JSON envelope the Functions host POSTs to a custom
// handler for each invocation when enableForwardingHttpRequest is not set. Its
// shape is documented at
// https://learn.microsoft.com/azure/azure-functions/functions-custom-handlers.
//
// Data is keyed by binding name: the trigger binding (e.g. "req" for HTTP,
// "timer" for a timer) plus any input bindings. Metadata carries host-supplied
// trigger metadata (blob name, queue insertion time, HTTP route params, and a
// "sys" object).
type InvokeRequest struct {
	Data     map[string]json.RawMessage `json:"Data"`
	Metadata map[string]json.RawMessage `json:"Metadata"`
}

// InvokeResponse is the JSON envelope a custom handler returns. Outputs carries
// output-binding values keyed by binding name (an HTTP response uses "res");
// ReturnValue carries the function's $return value; Logs are surfaced by the
// host as function logs.
type InvokeResponse struct {
	Outputs     map[string]any `json:"Outputs,omitempty"`
	Logs        []string       `json:"Logs,omitempty"`
	ReturnValue any            `json:"ReturnValue,omitempty"`
}

// httpRequestPayload is the shape the host serializes an HTTP trigger's request
// into, delivered under InvokeRequest.Data[<triggerName>] (conventionally
// "req") when enableForwardingHttpRequest is false.
type httpRequestPayload struct {
	URL     string              `json:"Url"`
	Method  string              `json:"Method"`
	Query   map[string]string   `json:"Query"`
	Headers map[string][]string `json:"Headers"`
	Params  map[string]string   `json:"Params"`
	Body    json.RawMessage     `json:"Body"`
}

// httpResponsePayload is the shape returned under InvokeResponse.Outputs["res"]
// for an HTTP-triggered function in envelope mode.
type httpResponsePayload struct {
	StatusCode int               `json:"statusCode"`
	Body       string            `json:"body"`
	Headers    map[string]string `json:"headers,omitempty"`
}
