package otelgateway

import (
	"encoding/json"

	"github.com/azure/azure-functions-golang-worker/sdk/bindings"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// enrichResourceAttributes folds Event Hub invoke metadata onto every
// ResourceLogs resource — the worker-side equivalent of the receiver's
// include_metadata option, reading the typed trigger fields directly.
func enrichResourceAttributes(ld plog.Logs, msg bindings.EventHubMessage) {
	rl := ld.ResourceLogs()
	for i := 0; i < rl.Len(); i++ {
		attrs := rl.At(i).Resource().Attributes()
		putStr(attrs, "azure.eventhub.partition_key", msg.PartitionKey)
		putStr(attrs, "azure.eventhub.offset", msg.Offset)
		putStr(attrs, "azure.eventhub.enqueued_time", msg.EnqueuedTimeUtc)
		if msg.SequenceNumber != 0 {
			attrs.PutInt("azure.eventhub.sequence_number", msg.SequenceNumber)
		}
		for k, v := range msg.SystemProperties {
			setAny(attrs, "azure.eventhub.system."+k, v)
		}
	}
}

func putStr(m pcommon.Map, k, v string) {
	if v != "" {
		m.PutStr(k, v)
	}
}

func setAny(m pcommon.Map, k string, v any) {
	switch val := v.(type) {
	case string:
		m.PutStr(k, val)
	case bool:
		m.PutBool(k, val)
	case float64:
		m.PutDouble(k, val)
	case int64:
		m.PutInt(k, val)
	case int:
		m.PutInt(k, int64(val))
	default:
		if b, err := json.Marshal(v); err == nil {
			m.PutStr(k, string(b))
		}
	}
}
