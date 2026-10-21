package api

import (
	"maps"
	"strings"
)

// typeSet reads a comma-separated list of resource types.
func typeSet(value string) map[string]bool {
	set := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			set[item] = true
		}
	}
	return set
}

// inventorySummary is a report without its process list, which is most of it:
// a host reports every process it runs, and each is also a resource of its own.
// The stored report is shared, so the summary is a copy.
func inventorySummary(data map[string]any) map[string]any {
	if _, ok := data["processes"]; !ok {
		return data
	}
	summary := maps.Clone(data)
	delete(summary, "processes")
	return summary
}
