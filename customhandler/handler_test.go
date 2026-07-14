package customhandler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azure/azure-functions-golang-worker/sdk"
	"github.com/azure/azure-functions-golang-worker/sdk/bindings"
)

// postEnvelope drives one envelope invocation against h and returns the
// decoded InvokeResponse and raw recorder.
func postEnvelope(t *testing.T, h http.Handler, fn string, req InvokeRequest) (InvokeResponse, *httptest.ResponseRecorder) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/"+fn, strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	var resp InvokeResponse
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response %q: %v", rec.Body.String(), err)
		}
	}
	return resp, rec
}

// httpEnvelope builds an InvokeRequest carrying an HTTP trigger payload under
// the "req" binding.
func httpEnvelope(method, url string) InvokeRequest {
	payload, _ := json.Marshal(httpRequestPayload{Method: method, URL: url})
	return InvokeRequest{Data: map[string]json.RawMessage{"req": payload}}
}

func TestHandler_HTTPEnvelope(t *testing.T) {
	app := sdk.FunctionApp()
	app.HTTP("hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "Hello, %s!", r.URL.Query().Get("name"))
	})

	resp, rec := postEnvelope(t, Handler(app), "hello", httpEnvelope(http.MethodGet, "http://localhost/api/hello?name=Ada"))

	if rec.Code != http.StatusOK {
		t.Fatalf("envelope status = %d, want 200", rec.Code)
	}
	res := decodeHTTPOutput(t, resp)
	if res.StatusCode != http.StatusCreated {
		t.Errorf("res.statusCode = %d, want 201", res.StatusCode)
	}
	if res.Body != "Hello, Ada!" {
		t.Errorf("res.body = %q, want %q", res.Body, "Hello, Ada!")
	}
	if got := res.Headers["Content-Type"]; got != "text/plain" {
		t.Errorf("res.headers[Content-Type] = %q, want text/plain", got)
	}
}

func TestHandler_TimerEnvelope(t *testing.T) {
	app := sdk.FunctionApp()
	var gotPastDue bool
	ran := false
	app.Timer("cron", func(_ context.Context, info bindings.TimerInfo) error {
		ran = true
		gotPastDue = info.IsPastDue
		return nil
	})

	timerPayload, _ := json.Marshal(bindings.TimerInfo{IsPastDue: true})
	req := InvokeRequest{Data: map[string]json.RawMessage{"timer": timerPayload}}
	_, rec := postEnvelope(t, Handler(app), "cron", req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !ran {
		t.Fatal("timer handler did not run")
	}
	if !gotPastDue {
		t.Error("timer handler did not receive decoded IsPastDue=true")
	}
}

func TestHandler_NamedOutputs(t *testing.T) {
	app := sdk.FunctionApp()
	app.Timer("cron", func(ctx context.Context, _ bindings.TimerInfo) error {
		if mc, ok := sdk.MiddlewareContextFrom(ctx); ok {
			mc.SetOutput("failedMessage", map[string]string{"error": "boom"})
		}
		return nil
	})

	timerPayload, _ := json.Marshal(bindings.TimerInfo{})
	req := InvokeRequest{Data: map[string]json.RawMessage{"timer": timerPayload}}
	resp, rec := postEnvelope(t, Handler(app), "cron", req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if _, ok := resp.Outputs["failedMessage"]; !ok {
		t.Errorf("response Outputs missing failedMessage: %+v", resp.Outputs)
	}
}

func TestHandler_ForwardedHTTP(t *testing.T) {
	app := sdk.FunctionApp()
	app.HTTP("hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hi %s", r.URL.Query().Get("who"))
	})

	h := Handler(app, WithForwardedHTTP())
	r := httptest.NewRequest(http.MethodGet, "/api/hello?who=there", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "hi there" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "hi there")
	}
}

func TestHandler_MiddlewareRunsAroundInvocation(t *testing.T) {
	app := sdk.FunctionApp()
	var order []string
	app.Use(sdk.MiddlewareFunc(func(next sdk.Handler) sdk.Handler {
		return func(ctx context.Context, mc *sdk.MiddlewareContext) error {
			order = append(order, "before:"+mc.FunctionName)
			err := next(ctx, mc)
			order = append(order, "after")
			return err
		}
	}))
	app.HTTP("hello", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		w.WriteHeader(http.StatusOK)
	})

	postEnvelope(t, Handler(app), "hello", httpEnvelope(http.MethodGet, "http://localhost/api/hello"))

	want := []string{"before:hello", "handler", "after"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("middleware order = %v, want %v", order, want)
	}
}

func TestHandler_StartupPing(t *testing.T) {
	app := sdk.FunctionApp()
	app.HTTP("hello", func(w http.ResponseWriter, r *http.Request) {})

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	Handler(app).ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("startup ping status = %d, want 200", rec.Code)
	}
}

func TestHandler_UnknownFunction(t *testing.T) {
	app := sdk.FunctionApp()
	app.HTTP("hello", func(w http.ResponseWriter, r *http.Request) {})

	r := httptest.NewRequest(http.MethodPost, "/missing", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	Handler(app).ServeHTTP(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandler_PanicBecomesFailure(t *testing.T) {
	app := sdk.FunctionApp()
	app.HTTP("boom", func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	})

	_, rec := postEnvelope(t, Handler(app, WithLogger(discardLogger())), "boom", httpEnvelope(http.MethodGet, "http://localhost/api/boom"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHandler_MiddlewareErrorBecomesFailure(t *testing.T) {
	app := sdk.FunctionApp()
	app.Use(sdk.MiddlewareFunc(func(next sdk.Handler) sdk.Handler {
		return func(ctx context.Context, mc *sdk.MiddlewareContext) error {
			return fmt.Errorf("denied")
		}
	}))
	app.HTTP("hello", func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not run when middleware short-circuits")
	})

	_, rec := postEnvelope(t, Handler(app, WithLogger(discardLogger())), "hello", httpEnvelope(http.MethodGet, "http://localhost/api/hello"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// decodeHTTPOutput extracts the Outputs["res"] payload from a response.
func decodeHTTPOutput(t *testing.T, resp InvokeResponse) httpResponsePayload {
	t.Helper()
	raw, ok := resp.Outputs["res"]
	if !ok {
		t.Fatalf("response has no Outputs[res]: %+v", resp)
	}
	// resp.Outputs values round-trip through any; re-marshal to decode.
	b, _ := json.Marshal(raw)
	var res httpResponsePayload
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatalf("decode res payload: %v", err)
	}
	return res
}
