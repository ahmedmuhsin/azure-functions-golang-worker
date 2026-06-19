package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// azureResourceLogsEnvelope is the shape Azure Monitor uses when it streams
// platform resource logs to an Event Hub: a single message carries a batch of
// records under a top-level "records" array.
type azureResourceLogsEnvelope struct {
	Records []json.RawMessage `json:"records"`
}

// azureResourceLogRecord captures the common top-level fields of the Azure
// Monitor resource-log schema. Anything not promoted here stays in the record
// body verbatim, so no information is lost.
type azureResourceLogRecord struct {
	Time          string `json:"time"`
	ResourceID    string `json:"resourceId"`
	OperationName string `json:"operationName"`
	Category      string `json:"category"`
	Level         string `json:"level"`
	ResultType    string `json:"resultType"`
	CorrelationID string `json:"correlationId"`
	Location      string `json:"location"`
}

// decodeResourceLogs turns one Event Hub message body (an Azure Monitor
// resource-logs batch) into pdata. Records are grouped by resourceId so each
// Azure resource becomes its own ResourceLogs, mirroring what the upstream
// azureresourcelogs encoding extension produces.
//
// This is the half of the shim that the upstream receiver implements as an
// encoding extension. Everything downstream (batching, retry, export) is the
// embedded collector's job, not ours.
func decodeResourceLogs(body []byte) (plog.Logs, error) {
	var env azureResourceLogsEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return plog.Logs{}, fmt.Errorf("parse resource-logs envelope: %w", err)
	}

	logs := plog.NewLogs()
	byResource := make(map[string]plog.ScopeLogs, len(env.Records))

	for _, raw := range env.Records {
		var rec azureResourceLogRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return plog.Logs{}, fmt.Errorf("parse resource-log record: %w", err)
		}

		sl, ok := byResource[rec.ResourceID]
		if !ok {
			rl := logs.ResourceLogs().AppendEmpty()
			res := rl.Resource().Attributes()
			res.PutStr("cloud.provider", "azure")
			putNonEmpty(res, "cloud.resource_id", rec.ResourceID)
			putNonEmpty(res, "cloud.region", rec.Location)

			sl = rl.ScopeLogs().AppendEmpty()
			sl.Scope().SetName("azureresourcelogs")
			byResource[rec.ResourceID] = sl
		}

		lr := sl.LogRecords().AppendEmpty()
		if ts, err := time.Parse(time.RFC3339Nano, rec.Time); err == nil {
			lr.SetTimestamp(pcommon.NewTimestampFromTime(ts))
		}
		lr.SetSeverityNumber(severityFromAzureLevel(rec.Level))
		lr.SetSeverityText(rec.Level)

		attrs := lr.Attributes()
		putNonEmpty(attrs, "azure.category", rec.Category)
		putNonEmpty(attrs, "azure.operation_name", rec.OperationName)
		putNonEmpty(attrs, "azure.result_type", rec.ResultType)
		putNonEmpty(attrs, "azure.correlation_id", rec.CorrelationID)

		// Preserve the full record as the log body so nothing is dropped.
		setBodyFromJSON(lr.Body(), raw)
	}

	return logs, nil
}

// severityFromAzureLevel maps the Azure resource-log "level" string onto an
// OTel severity number.
func severityFromAzureLevel(level string) plog.SeverityNumber {
	switch strings.ToLower(level) {
	case "verbose", "debug", "trace":
		return plog.SeverityNumberDebug
	case "informational", "information", "info":
		return plog.SeverityNumberInfo
	case "warning", "warn":
		return plog.SeverityNumberWarn
	case "error":
		return plog.SeverityNumberError
	case "critical":
		return plog.SeverityNumberFatal
	default:
		return plog.SeverityNumberUnspecified
	}
}

// setBodyFromJSON stores the record as a structured map body when possible,
// falling back to the raw string so a record is never dropped on a parse miss.
func setBodyFromJSON(body pcommon.Value, raw []byte) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		if err := body.SetEmptyMap().FromRaw(m); err == nil {
			return
		}
	}
	body.SetStr(string(raw))
}

func putNonEmpty(m pcommon.Map, k, v string) {
	if v != "" {
		m.PutStr(k, v)
	}
}
