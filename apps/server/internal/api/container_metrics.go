package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// containerMetricsRequest carries one cgroup reading per container. The agent
// sends runtime IDs; the resource ID is derived here, the same way
// reconcileInventory derives it when it creates the resource.
type containerMetricsRequest struct {
	NodeID    string    `json:"nodeId"`
	Timestamp time.Time `json:"timestamp"`
	Items     []struct {
		ID               string  `json:"id"`
		CPUPercent       float64 `json:"cpuPercent"`
		MemoryBytes      uint64  `json:"memoryBytes"`
		MemoryLimitBytes uint64  `json:"memoryLimitBytes"`
		MemoryPercent    float64 `json:"memoryPercent"`
		DiskReadBytes    uint64  `json:"diskReadBytes"`
		DiskWriteBytes   uint64  `json:"diskWriteBytes"`
		HostMemoryBytes  uint64  `json:"hostMemoryBytes"`
		Processes        int     `json:"processes"`
		OOMKills         uint64  `json:"oomKills"`
		ThrottledUsec    uint64  `json:"throttledUsec"`
		ThrottledCount   uint64  `json:"throttledCount"`
	} `json:"items"`
}

func (s *Server) ingestContainerMetrics(w http.ResponseWriter, r *http.Request) {
	var request containerMetricsRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	agentID := r.PathValue("id")
	node, ok := s.store.Resource(request.NodeID)
	if !ok {
		writeError(w, http.StatusNotFound, "resource_not_found", "node is not registered")
		return
	}
	if node.AgentID != agentID {
		writeError(w, http.StatusForbidden, "resource_ownership_mismatch", "node is managed by another agent")
		return
	}
	timestamp := request.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	accepted := 0
	for _, item := range request.Items {
		if item.ID == "" {
			continue
		}
		resourceID := stableID(string(domain.ResourceContainer), request.NodeID+"-"+item.ID)
		resource, found := s.store.Resource(resourceID)
		// A container the inventory has not reported yet has no resource to
		// attach a sample to; the next inventory creates it.
		if !found || resource.AgentID != agentID {
			continue
		}
		// Only CPU and memory are percentages. cgroup block IO is a byte
		// counter, not a share of capacity, so it stays an attribute rather
		// than being passed off as the disk metric.
		sample := domain.MetricSample{
			ResourceID: resourceID,
			Timestamp:  timestamp,
			CPU:        clampPercent(item.CPUPercent),
			Memory:     clampPercent(item.MemoryPercent),
			// Counters the kernel keeps, carried as readings rather than
			// attributes so they can be charted and alerted on. An OOM kill
			// used to reach the server only as kernel prose.
			Values: map[string]float64{
				"oom_kills":       float64(item.OOMKills),
				"throttled_usec":  float64(item.ThrottledUsec),
				"throttled_count": float64(item.ThrottledCount),
			},
		}
		if err := validateMetricSample(sample, time.Now().UTC()); err != nil {
			continue
		}
		s.store.AddMetric(sample)
		s.evaluateRules(sample, s.store.NetworkRate(resourceID))
		// Absolute figures belong on the resource, where the console reads
		// them next to the image and state.
		if resource.Attributes == nil {
			resource.Attributes = map[string]string{}
		}
		// memoryUsedBytes is what is in use and memoryBytes is what that is a
		// share of, the same way the VM path writes them. They were the other
		// way round here, and since the console reads both under the VM's
		// meaning a container's meter read "0 B / 8.2 MB" -- no usage, and the
		// usage sitting in the total's place.
		//
		// A container with no limit of its own is measured against the
		// machine, so that is what goes in the denominator; the agent sends
		// which basis it used rather than leaving the console to guess.
		setAttribute(resource.Attributes, "memoryUsedBytes", item.MemoryBytes)
		basis := item.MemoryLimitBytes
		if basis == 0 {
			basis = item.HostMemoryBytes
		}
		setAttribute(resource.Attributes, "memoryBytes", basis)
		setAttribute(resource.Attributes, "memoryLimitBytes", item.MemoryLimitBytes)
		setAttribute(resource.Attributes, "hostMemoryBytes", item.HostMemoryBytes)
		setAttribute(resource.Attributes, "diskReadBytes", item.DiskReadBytes)
		setAttribute(resource.Attributes, "diskWriteBytes", item.DiskWriteBytes)
		setAttribute(resource.Attributes, "processes", uint64(item.Processes))
		s.store.UpsertResource(resource)
		accepted++
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": accepted})
}

// setAttribute omits a zero rather than writing one, so an unlimited container
// does not appear to be capped at zero bytes.
func setAttribute(attributes map[string]string, key string, value uint64) {
	if value == 0 {
		delete(attributes, key)
		return
	}
	attributes[key] = strconv.FormatUint(value, 10)
}
