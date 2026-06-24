package otelgateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azure/azure-functions-golang-worker/sdk/bindings"
	"go.opentelemetry.io/collector/pdata/plog"
)

func TestBuiltinEncodings_Present(t *testing.T) {
	enc := builtinEncodings()
	for _, name := range []string{EncodingResourceLogs, EncodingRaw} {
		if _, ok := enc[name]; !ok {
			t.Errorf("built-in encoding %q missing", name)
		}
	}
}

func TestRawLogsUnmarshaler_BodyPreserved(t *testing.T) {
	const payload = `{"hello":"world"}`
	ld, err := RawLogsUnmarshaler{}.UnmarshalLogs([]byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ld.LogRecordCount() != 1 {
		t.Fatalf("expected 1 record, got %d", ld.LogRecordCount())
	}
	got := ld.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Str()
	if got != payload {
		t.Errorf("body = %q, want %q", got, payload)
	}
}

func TestResourceLogsEncoding_DecodesRecords(t *testing.T) {
	// Minimal Azure Monitor resource-logs envelope: a "records" array.
	body := []byte(`{"records":[{"time":"2026-01-01T00:00:00Z","resourceId":"/sub/r","category":"Audit","operationName":"op"}]}`)
	u := builtinEncodings()[EncodingResourceLogs]
	ld, err := u.UnmarshalLogs(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ld.LogRecordCount() != 1 {
		t.Fatalf("expected 1 record, got %d", ld.LogRecordCount())
	}
	res := ld.ResourceLogs().At(0).Resource().Attributes()
	if v, ok := res.Get("azure.resource.id"); !ok || v.Str() != "/sub/r" {
		t.Errorf("expected azure.resource.id=/sub/r, got %v (present=%v)", v.AsString(), ok)
	}
}

func TestEnrichResourceAttributes_AddsMetadata(t *testing.T) {
	ld, err := RawLogsUnmarshaler{}.UnmarshalLogs([]byte("x"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := bindings.EventHubMessage{
		Body:             json.RawMessage(`"x"`),
		PartitionKey:     "pk",
		Offset:           "1024",
		EnqueuedTimeUtc:  "2026-01-01T00:00:00Z",
		SequenceNumber:   42,
		SystemProperties: map[string]any{"x-opt-partition-id": "3"},
	}
	enrichResourceAttributes(ld, msg)

	attrs := ld.ResourceLogs().At(0).Resource().Attributes()
	if v, ok := attrs.Get("azure.eventhub.partition_key"); !ok || v.Str() != "pk" {
		t.Errorf("partition_key missing/wrong: %v (present=%v)", v.AsString(), ok)
	}
	if v, ok := attrs.Get("azure.eventhub.sequence_number"); !ok || v.Int() != 42 {
		t.Errorf("sequence_number missing/wrong: %v (present=%v)", v.AsString(), ok)
	}
	if v, ok := attrs.Get("azure.eventhub.system.x-opt-partition-id"); !ok || v.Str() != "3" {
		t.Errorf("system property missing/wrong: %v (present=%v)", v.AsString(), ok)
	}
}

// recordingConsumer captures logs pushed via the consumer sink.
type recordingConsumer struct {
	calls int
	last  plog.Logs
}

func (c *recordingConsumer) ConsumeLogs(_ context.Context, ld plog.Logs) error {
	c.calls++
	c.last = ld
	return nil
}

func TestNewWithConsumer_HandlerPushesToConsumer(t *testing.T) {
	rc := &recordingConsumer{}
	gw := NewWithConsumer(rc)

	h := gw.Handler(EncodingRaw)
	if err := h(context.Background(), bindings.EventHubMessage{Body: json.RawMessage(`hello`)}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if rc.calls != 1 {
		t.Fatalf("consumer calls = %d, want 1", rc.calls)
	}
	if rc.last.LogRecordCount() != 1 {
		t.Errorf("pushed record count = %d, want 1", rc.last.LogRecordCount())
	}
	// Close is a no-op (no OTLP conn) and must not panic.
	if err := gw.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestHandler_UnknownEncodingPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic for unknown encoding")
		}
	}()
	NewWithConsumer(&recordingConsumer{}).Handler("nope")
}
