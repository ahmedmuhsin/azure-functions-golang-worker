package otelgateway

import (
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/translator/azure"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

// Built-in encoding names usable with [Gateway.Handler].
const (
	// EncodingResourceLogs decodes the Azure Monitor resource-logs schema
	// (a "records" array) using the canonical contrib unmarshaler — the same
	// one the upstream receiver loads as its azureresourcelogs encoding.
	EncodingResourceLogs = "azureresourcelogs"

	// EncodingRaw stores the undecoded message body as a single log record.
	EncodingRaw = "raw"
)

// scopeVersion is reported as the instrumentation scope version on decoded logs.
const scopeVersion = "otelgateway"

// builtinEncodings returns the encodings every Gateway starts with.
func builtinEncodings() map[string]plog.Unmarshaler {
	return map[string]plog.Unmarshaler{
		EncodingResourceLogs: &azure.ResourceLogsUnmarshaler{
			Version: scopeVersion,
			Logger:  zap.NewNop(),
		},
		EncodingRaw: RawLogsUnmarshaler{},
	}
}

// RawLogsUnmarshaler turns an undecoded Event Hub message body into a single
// log record whose body is the raw payload. Exported so callers can reference
// it when composing their own encoding sets.
type RawLogsUnmarshaler struct{}

// UnmarshalLogs implements plog.Unmarshaler.
func (RawLogsUnmarshaler) UnmarshalLogs(buf []byte) (plog.Logs, error) {
	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty().
		ScopeLogs().AppendEmpty().
		LogRecords().AppendEmpty().
		Body().SetStr(string(buf))
	return logs, nil
}
