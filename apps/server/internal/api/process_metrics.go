package api

import (
	"net/http"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// A process's resource use arrived only as inventory facts, on the five-minute
// cycle, and only as the memory it held at that instant. Nothing turned any of
// it into samples, so every process detail page showed a flat zero and a trend
// that said there were not enough samples — which was true: there were none.
//
// The agent now derives a rate each tick for the processes heavy enough to be
// worth one, and posts them here, the way container and VM readings already
// worked. A process outside that set gets no sample at all, which is the
// honest answer and what the console shows instead of a zero.

// processMetricsRequest carries one reading per sampled process. The agent
// sends process IDs; the resource ID is derived here the same way
// reconcileInventory derives it when it creates the resource.
type processMetricsRequest struct {
	NodeID    string    `json:"nodeId"`
	Timestamp time.Time `json:"timestamp"`
	Items     []struct {
		PID             int     `json:"pid"`
		Name            string  `json:"name"`
		State           string  `json:"state"`
		CPUPercent      float64 `json:"cpuPercent"`
		MemoryBytes     uint64  `json:"memoryBytes"`
		MemoryPercent   float64 `json:"memoryPercent"`
		HostMemoryBytes uint64  `json:"hostMemoryBytes"`
		Threads         int     `json:"threads"`
		StartedAt       string  `json:"startedAt"`
	} `json:"items"`
}

func (s *Server) ingestProcessMetrics(w http.ResponseWriter, r *http.Request) {
	var request processMetricsRequest
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
		if item.PID <= 0 {
			continue
		}
		resourceID := stableID(string(domain.ResourceProcess), request.NodeID+"-"+attributeString(item.PID))
		resource, found := s.store.Resource(resourceID)
		// A process the inventory has not reported yet has no resource to
		// attach a sample to; the next inventory creates it.
		if !found || resource.AgentID != agentID {
			continue
		}
		// CPU is a share of the whole machine, so a process's reading adds up
		// against its host's the way a container's does. Memory is a share of
		// installed memory for the same reason: a process has no allocation of
		// its own to be a share of.
		sample := domain.MetricSample{
			ResourceID: resourceID,
			Timestamp:  timestamp,
			CPU:        clampPercent(item.CPUPercent),
			Memory:     clampPercent(item.MemoryPercent),
		}
		if err := validateMetricSample(sample, time.Now().UTC()); err != nil {
			continue
		}
		s.store.AddMetric(sample)
		s.evaluateRules(sample, s.store.NetworkRate(resourceID))
		if resource.Attributes == nil {
			resource.Attributes = map[string]string{}
		}
		setAttribute(resource.Attributes, "memoryUsedBytes", item.MemoryBytes)
		setAttribute(resource.Attributes, "hostMemoryBytes", item.HostMemoryBytes)
		setAttribute(resource.Attributes, "threads", uint64(max(item.Threads, 0)))
		if item.State != "" {
			resource.Attributes["state"] = item.State
		}
		if item.StartedAt != "" {
			resource.Attributes["startedAt"] = item.StartedAt
		}
		// The console needs to tell "sampled and idle" from "never sampled",
		// and the only difference visible to it is this.
		resource.Attributes["metricsSampledAt"] = timestamp.UTC().Format(time.RFC3339)
		s.store.UpsertResource(resource)
		accepted++
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": accepted})
}
