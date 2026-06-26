package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
)

// invoke is the subset of the Azure Functions custom-handler invoke payload we
// care about.
type invoke struct {
	Data     map[string]json.RawMessage `json:"Data"`
	Metadata map[string]json.RawMessage `json:"Metadata"`
}

// Custom handler that, for each invocation, captures the raw payload AND then
// demonstrates building a blob client from the deferred-binding reference and
// doing a ranged read, proving the handler can act on the reference without the
// host loading the full blob.
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
		writeLog(capturePath, fmt.Sprintf("\n===== %s %s @ %s =====\n%s",
			r.Method, r.URL.Path, time.Now().Format(time.RFC3339Nano), string(body)))

		// Only the function invocations (POST /<FunctionName>) carry bindings;
		// the startup GET / does not.
		if r.Method == http.MethodPost {
			writeLog(capturePath, "--- handler reaction ---\n"+react(r.Context(), body))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"Outputs":{},"Logs":[],"ReturnValue":null}`)
	})

	_ = http.ListenAndServe("127.0.0.1:"+port, nil)
}

// react inspects the "myblob" binding and, when it is a deferred reference,
// builds a client and does a ranged read.
func react(ctx context.Context, body []byte) string {
	var inv invoke
	if err := json.Unmarshal(body, &inv); err != nil {
		return "could not parse invoke body: " + err.Error()
	}

	raw, ok := inv.Data["myblob"]
	if !ok {
		return "no myblob binding in this invocation"
	}

	// A string value means the host already loaded the full content inline
	// (non-deferred). An object means a deferred ParameterBindingData reference.
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "{") {
		var content string
		_ = json.Unmarshal(raw, &content)
		return fmt.Sprintf("NON-DEFERRED: host delivered the full content inline (%d bytes): %q",
			len(content), content)
	}

	var ref struct {
		Source string `json:"Source"`
	}
	_ = json.Unmarshal(raw, &ref)

	// Find a blob URL to build a client from. The trigger payload carries it in
	// Metadata.Uri; an input binding does not expose it (the reference Content
	// is serialized as a descriptor over the custom-handler boundary).
	uri := blobURI(inv)
	if uri == "" {
		return fmt.Sprintf("DEFERRED reference (Source=%q), but no blob URL in the payload. "+
			"The trigger exposes Metadata.Uri; an input binding does not, so a handler here would "+
			"reconstruct the path from its binding template plus the app's AzureWebJobsStorage connection.", ref.Source)
	}

	// Build a client for exactly the blob the host pointed us at. The blob path
	// comes from the host-supplied reference (Metadata.Uri); the connection comes
	// from the app's AzureWebJobsStorage setting, the same way a binding resolves
	// it under the hood. No account or key is hard-coded here.
	connStr := os.Getenv("AzureWebJobsStorage")
	if connStr == "" {
		return "DEFERRED reference, but AzureWebJobsStorage is not set in the environment"
	}
	parts, err := blob.ParseURL(uri)
	if err != nil {
		return "parse blob url: " + err.Error()
	}
	client, err := blob.NewClientFromConnectionString(connStr, parts.ContainerName, parts.BlobName, nil)
	if err != nil {
		return "build blob client: " + err.Error()
	}

	// One metadata call for the full size (no content download).
	props, err := client.GetProperties(ctx, nil)
	if err != nil {
		return "get properties: " + err.Error()
	}
	full := int64(0)
	if props.ContentLength != nil {
		full = *props.ContentLength
	}

	// Ranged read of just the first 5 bytes.
	const want = 5
	dl, err := client.DownloadStream(ctx, &blob.DownloadStreamOptions{
		Range: blob.HTTPRange{Offset: 0, Count: want},
	})
	if err != nil {
		return "ranged download: " + err.Error()
	}
	head, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()

	return fmt.Sprintf("DEFERRED reference (Source=%q): built a blob client from the host-supplied "+
		"reference using the app's AzureWebJobsStorage connection, read the first %d of %d bytes via a "+
		"range request (%q). The full blob was never downloaded into the invocation.",
		ref.Source, len(head), full, string(head))
}

// blobURI extracts a blob URL from the invoke metadata. The blob trigger puts
// the full URL in Metadata.Uri (a JSON string whose value is itself quoted).
func blobURI(inv invoke) string {
	raw, ok := inv.Metadata["Uri"]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.Trim(s, "\"")
}

func writeLog(path, msg string) {
	fmt.Fprintln(os.Stderr, msg)
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		_, _ = f.WriteString(msg + "\n")
		_ = f.Sync()
		_ = f.Close()
	}
}
