package customhandler_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/azure/azure-functions-golang-worker/customhandler"
	"github.com/azure/azure-functions-golang-worker/sdk"
)

// ExampleHandler shows the embedding pattern: an application (for example an
// OpenTelemetry Collector receiver) mounts the custom-handler dispatcher on a
// server it already owns, instead of surrendering process ownership the way the
// gRPC worker's blocking Start would require. The same typed programming model
// and middleware chain run; only the transport is HTTP.
func ExampleHandler() {
	app := sdk.FunctionApp()
	app.Use(sdk.MiddlewareFunc(func(next sdk.Handler) sdk.Handler {
		return next // a real middleware would trace/log here
	}))
	app.HTTP("ingest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "received %s", r.URL.Query().Get("payload"))
	})

	// The component owns the listener; customhandler.Handler is just mounted.
	mux := http.NewServeMux()
	mux.Handle("/", customhandler.Handler(app, customhandler.WithForwardedHTTP()))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/ingest?payload=hi")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))
	// Output: received hi
}
