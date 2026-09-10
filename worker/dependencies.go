package worker

import (
	"encoding/json"
	"runtime/debug"
	"slices"
)

// This bounds the UTF-8 JSON property itself, not the enclosing protobuf/JSON
// message. Host ingestion limits must also account for the other metadata.
const dependencyInventoryMaxBytes = 8 * 1024

type dependencyModule struct {
	Path        string                 `json:"path"`
	Version     string                 `json:"version,omitempty"`
	Replacement *dependencyReplacement `json:"replacement,omitempty"`
}

type dependencyReplacement struct {
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
}

type dependencyInventory struct {
	SchemaVersion int               `json:"schema_version"`
	Status        string            `json:"status"`
	Total         int               `json:"total"`
	Reported      int               `json:"reported"`
	Truncated     bool              `json:"truncated"`
	Modules       []json.RawMessage `json:"modules"`
}

func buildDependencyInventory(bi *debug.BuildInfo) string {
	inventory := dependencyInventory{
		SchemaVersion: 1,
		Status:        "unavailable",
		Modules:       []json.RawMessage{},
	}
	var entries []string
	if bi != nil {
		inventory.Status = "available"
		for _, dep := range bi.Deps {
			if dep == nil || dep.Path == "" {
				continue
			}
			module := dependencyModule{Path: dep.Path, Version: dep.Version}
			if replacement := dep.Replace; replacement != nil {
				module.Replacement = &dependencyReplacement{}
				if replacement.Version == "" || replacement.Version == "(devel)" {
					// Local replacements have no recorded effective version. Do
					// not send their directory or imply the requested pin ran.
					module.Version = ""
				} else {
					module.Replacement.Path = replacement.Path
					module.Replacement.Version = replacement.Version
				}
			}
			// Only strings and a nonrecursive struct are serialized here, so
			// Marshal cannot encounter unsupported types or reference cycles.
			encoded, _ := json.Marshal(module)
			entries = append(entries, string(encoded))
		}
	}
	// Sort and deduplicate the normalized records, not the caller's BuildInfo.
	// Distinct versions/replacements for the same path remain separate records.
	slices.Sort(entries)
	entries = slices.Compact(entries)
	inventory.Total = len(entries)
	inventory.Truncated = inventory.Total > 0

	// Measure each candidate prefix using its exact envelope (including count
	// digit changes and JSON escapes). Keep Modules empty until the final encode
	// so we do not repeatedly marshal an increasingly large array.
	entryBytes := 0
	for i, entry := range entries {
		candidate := inventory
		candidate.Reported = i + 1
		candidate.Truncated = candidate.Reported < candidate.Total
		envelope, _ := json.Marshal(candidate)
		if len(envelope)+entryBytes+len(entry)+i > dependencyInventoryMaxBytes {
			break
		}
		entryBytes += len(entry)
		inventory.Reported = candidate.Reported
		inventory.Truncated = candidate.Truncated
	}
	for _, entry := range entries[:inventory.Reported] {
		inventory.Modules = append(inventory.Modules, json.RawMessage(entry))
	}
	encoded, _ := json.Marshal(inventory)
	return string(encoded)
}
