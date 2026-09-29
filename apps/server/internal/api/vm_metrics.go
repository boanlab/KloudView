package api

import (
	"net/http"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// Per-domain samples, derived by the agent each tick from the raw counters
// libvirt reports and posted here, as container readings are. Inventory facts
// arrive on the five-minute cycle and carry no rate.

// vmMetricsRequest carries one reading per domain. The agent sends domain
// names; the resource ID is derived here the same way reconcileInventory
// derives it when it creates the resource.
type vmMetricsRequest struct {
	NodeID    string    `json:"nodeId"`
	Timestamp time.Time `json:"timestamp"`
	Items     []struct {
		Name             string  `json:"name"`
		State            string  `json:"state"`
		VCPUs            int     `json:"vcpus"`
		CPUPercent       float64 `json:"cpuPercent"`
		MemoryBytes      uint64  `json:"memoryBytes"`
		MemoryLimitBytes uint64  `json:"memoryLimitBytes"`
		MemoryPercent    float64 `json:"memoryPercent"`
		HostMemoryBytes  uint64  `json:"hostMemoryBytes"`
		DiskReadBytes    uint64  `json:"diskReadBytes"`
		DiskWriteBytes   uint64  `json:"diskWriteBytes"`
		NetworkRxRate    float64 `json:"networkRxRate"`
		NetworkTxRate    float64 `json:"networkTxRate"`
	} `json:"items"`
}

func (s *Server) ingestVMMetrics(w http.ResponseWriter, r *http.Request) {
	var request vmMetricsRequest
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
		if item.Name == "" {
			continue
		}
		resourceID := stableID(string(domain.ResourceVM), request.NodeID+"-"+item.Name)
		resource, found := s.store.Resource(resourceID)
		// A domain the inventory has not reported yet has no resource to
		// attach a sample to; the next inventory creates it.
		if !found || resource.AgentID != agentID {
			continue
		}
		// CPU and memory are shares of what the guest was given. Disk stays an
		// attribute: libvirt counts bytes moved, which is not a share of
		// capacity, and the host's own disk total is not this guest's.
		sample := domain.MetricSample{
			ResourceID: resourceID,
			Timestamp:  timestamp,
			CPU:        clampPercent(item.CPUPercent),
			Memory:     clampPercent(item.MemoryPercent),
			NetworkRx:  uint64(max(item.NetworkRxRate, 0)),
			NetworkTx:  uint64(max(item.NetworkTxRate, 0)),
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
		setAttribute(resource.Attributes, "memoryBytes", item.MemoryLimitBytes)
		setAttribute(resource.Attributes, "hostMemoryBytes", item.HostMemoryBytes)
		setAttribute(resource.Attributes, "diskReadBytes", item.DiskReadBytes)
		setAttribute(resource.Attributes, "diskWriteBytes", item.DiskWriteBytes)
		setAttribute(resource.Attributes, "vcpus", uint64(max(item.VCPUs, 0)))
		if item.State != "" {
			resource.Attributes["state"] = item.State
		}
		s.store.UpsertResource(resource)
		accepted++
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": accepted})
}
