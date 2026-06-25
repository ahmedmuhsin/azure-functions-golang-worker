package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Minimal Azure Functions custom handler used to capture exactly what the host
// POSTs for each invocation. It appends every request body to CAPTURE_FILE and
// echoes to stderr, then returns a best-effort valid custom-handler response.
// The capture happens before responding, so the payload is recorded regardless
// of whether the host likes the response.
func main() {
	port := os.Getenv("FUNCTIONS_CUSTOMHANDLER_PORT")
	if port == "" {
		port = "8080"
	}
	capturePath := os.Getenv("CAPTURE_FILE")
	if capturePath == "" {
		capturePath = "captured.log"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		entry := fmt.Sprintf("\n===== %s %s @ %s =====\n%s\n",
			r.Method, r.URL.Path, time.Now().Format(time.RFC3339Nano), string(body))
		fmt.Fprint(os.Stderr, entry)
		if f, err := os.OpenFile(capturePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			_, _ = f.WriteString(entry)
			_ = f.Sync()
			_ = f.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"Outputs":{},"Logs":[],"ReturnValue":null}`)
	})

	_ = http.ListenAndServe("127.0.0.1:"+port, nil)
}
