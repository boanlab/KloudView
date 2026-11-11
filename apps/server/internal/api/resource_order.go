package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

type resourceLoad struct {
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"`
	Disk   float64 `json:"disk"`
}

// parseTagFilter reads repeated tag=key=value parameters. Each is an exact
// match, ANDed: the free-text search already covers "contains".
func parseTagFilter(values []string) (map[string]string, error) {
	tags := map[string]string{}
	for _, value := range values {
		key, tagValue, ok := strings.Cut(value, "=")
		key, tagValue = strings.TrimSpace(key), strings.TrimSpace(tagValue)
		if !ok || key == "" || tagValue == "" {
			return nil, fmt.Errorf("tag must be key=value")
		}
		tags[key] = tagValue
	}
	return tags, nil
}

func tagsMatch(have, want map[string]string) bool {
	for key, value := range want {
		if have[key] != value {
			return false
		}
	}
	return true
}

// cursorSafe reports whether name-and-id cursors still page this order.
func (o resourceOrder) cursorSafe() bool {
	return o.field == "" || o.field == "name" && !o.descending
}

type resourceOrder struct {
	field      string
	descending bool
}

var resourceSortFields = map[string]bool{"name": true, "updated": true, "cpu": true, "memory": true, "disk": true}

// parseResourceOrder reads sort and order. With no sort the list keeps the
// store's order, hosts ahead of what runs on them. Name reads naturally A to
// Z; every other field is asked for to find the largest, so it defaults to
// descending.
func parseResourceOrder(field, direction string) (resourceOrder, error) {
	if field == "" && direction == "" {
		return resourceOrder{}, nil
	}
	if field == "" {
		field = "name"
	}
	if !resourceSortFields[field] {
		return resourceOrder{}, fmt.Errorf("sort must be name, updated, cpu, memory, or disk")
	}
	order := resourceOrder{field: field, descending: field != "name"}
	switch direction {
	case "":
	case "asc":
		order.descending = false
	case "desc":
		order.descending = true
	default:
		return resourceOrder{}, fmt.Errorf("order must be asc or desc")
	}
	return order, nil
}

// sort orders resources in place. A resource with no reading sorts after every
// one that has one, whichever way the order runs: "no data" is not "idle".
// Ties fall back to name then id, which keeps paging stable.
func (o resourceOrder) sort(items []domain.Resource, latest map[string]domain.MetricSample) {
	if o.field == "" {
		return
	}
	byName := func(a, b domain.Resource) bool {
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	}
	value := func(resource domain.Resource) (float64, bool) {
		sample, ok := latest[resource.ID]
		switch o.field {
		case "cpu":
			return sample.CPU, ok
		case "memory":
			return sample.Memory, ok
		case "disk":
			return sample.Disk, ok
		case "updated":
			return float64(resource.UpdatedAt.UnixNano()), true
		}
		return 0, false
	}
	sort.SliceStable(items, func(i, j int) bool {
		if o.field == "name" {
			if o.descending {
				return byName(items[j], items[i])
			}
			return byName(items[i], items[j])
		}
		vi, oki := value(items[i])
		vj, okj := value(items[j])
		if oki != okj {
			return oki
		}
		if vi != vj {
			if o.descending {
				return vi > vj
			}
			return vi < vj
		}
		return byName(items[i], items[j])
	})
}
