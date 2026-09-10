package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// Capacity-aware utilization thresholds (percent of a node's own capacity).
const (
	utilIdlePercent      = 10.0
	utilSaturatedPercent = 85.0
	// Rightsizing uses sustained (windowed) averages, not momentary usage.
	rightsizeWindow        = 24 * time.Hour
	rightsizeReclaimCPU    = 10.0
	rightsizeReclaimMemory = 15.0
	rightsizeScalePercent  = 85.0
	// Forecast fits a linear trend over recent history and projects when fleet
	// utilization reaches the headroom-exhaustion line.
	forecastWindow    = 24 * time.Hour
	forecastBucket    = time.Hour
	forecastThreshold = utilSaturatedPercent
)

func inventoryNumber(data map[string]any, key string) float64 {
	if data == nil {
		return 0
	}
	switch value := data[key].(type) {
	// Inventory decodes with UseNumber, so a value straight off the wire is a
	// json.Number; one reloaded from a persisted document is a float64.
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0
		}
		return parsed
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case uint64:
		return float64(value)
	}
	return 0
}

func maxFloat(values ...float64) float64 {
	m := 0.0
	for _, v := range values {
		if v > m {
			m = v
		}
	}
	return m
}

func percent(used, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return used / total * 100
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// linearFit returns the least-squares slope and intercept for y = intercept +
// slope*x, plus the coefficient of determination (r2) as a fit-quality signal.
func linearFit(xs, ys []float64) (slope, intercept, r2 float64) {
	n := float64(len(xs))
	if n < 2 {
		return 0, 0, 0
	}
	var sx, sy, sxx, sxy, syy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
		syy += ys[i] * ys[i]
	}
	denom := n*sxx - sx*sx
	if denom == 0 {
		return 0, sy / n, 0
	}
	slope = (n*sxy - sx*sy) / denom
	intercept = (sy - slope*sx) / n
	ssTot := syy - sy*sy/n
	if ssTot <= 0 {
		return slope, intercept, 0
	}
	var ssRes float64
	for i := range xs {
		d := ys[i] - (intercept + slope*xs[i])
		ssRes += d * d
	}
	return slope, intercept, 1 - ssRes/ssTot
}

type forecastPoint struct {
	Timestamp time.Time `json:"timestamp"`
	CPU       float64   `json:"cpu"`
	Memory    float64   `json:"memory"`
	Disk      float64   `json:"disk"`
	Network   float64   `json:"network"`
}

type utilizationForecast struct {
	Metric       string  `json:"metric"`
	Current      float64 `json:"current"`
	SlopePerDay  float64 `json:"slopePerDay"`
	Projected7d  float64 `json:"projected7d"`
	Projected30d float64 `json:"projected30d"`
	// DaysToFull is the projected days until the metric reaches the threshold at
	// the current trend; -1 when the trend is flat or declining (no exhaustion).
	DaysToFull float64 `json:"daysToFull"`
	Threshold  float64 `json:"threshold"`
	Confidence string  `json:"confidence"`
	Points     int     `json:"points"`
}

func buildForecast(metric string, series []forecastPoint, valueOf func(forecastPoint) float64) utilizationForecast {
	base := series[0].Timestamp
	xs := make([]float64, len(series))
	ys := make([]float64, len(series))
	for i, p := range series {
		xs[i] = p.Timestamp.Sub(base).Hours() / 24
		ys[i] = valueOf(p)
	}
	slope, intercept, r2 := linearFit(xs, ys)
	current := clampPercent(intercept + slope*xs[len(xs)-1])
	daysToFull := -1.0
	if slope > 0.01 {
		if current >= forecastThreshold {
			daysToFull = 0
		} else {
			daysToFull = (forecastThreshold - current) / slope
		}
	}
	confidence := "low"
	if len(series) >= 6 && r2 >= 0.4 {
		confidence = "medium"
	}
	if len(series) >= 12 && r2 >= 0.7 {
		confidence = "high"
	}
	return utilizationForecast{
		Metric: metric, Current: current, SlopePerDay: slope,
		Projected7d: clampPercent(current + slope*7), Projected30d: clampPercent(current + slope*30),
		DaysToFull: daysToFull, Threshold: forecastThreshold, Confidence: confidence, Points: len(series),
	}
}

type utilizationNode struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	CPU            float64 `json:"cpu"`
	Memory         float64 `json:"memory"`
	Disk           float64 `json:"disk"`
	Network        float64 `json:"network"`
	NetworkRx      float64 `json:"networkRx"`
	NetworkTx      float64 `json:"networkTx"`
	Cores          float64 `json:"cores"`
	MemoryBytes    float64 `json:"memoryBytes"`
	CoresUsed      float64 `json:"coresUsed"`
	MemUsedBytes   float64 `json:"memUsedBytes"`
	State          string  `json:"state"`
	AvgCPU         float64 `json:"avgCpu"`
	AvgMemory      float64 `json:"avgMemory"`
	Recommendation string  `json:"recommendation"`
}

type utilizationGroup struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	Nodes         int     `json:"nodes"`
	CoresTotal    float64 `json:"coresTotal"`
	CoresUsed     float64 `json:"coresUsed"`
	CPU           float64 `json:"cpu"`
	MemTotalBytes float64 `json:"memTotalBytes"`
	MemUsedBytes  float64 `json:"memUsedBytes"`
	Memory        float64 `json:"memory"`
	Disk          float64 `json:"disk"`
	Idle          int     `json:"idle"`
	Saturated     int     `json:"saturated"`
	diskSum       float64
}

// utilization reports capacity-aware resource usage for node/hypervisor hosts:
// per-node utilization plus headroom, and rollups by group and fleet, with idle
// and saturated flags. Usage percentages come from the latest metric sample;
// absolute capacity (cores, memory) comes from the agent inventory.
func (s *Server) utilization(w http.ResponseWriter, r *http.Request) {
	latest := s.store.LatestMetrics()
	averages := s.store.ResourceAveragesSince(time.Now().Add(-rightsizeWindow))
	rates := s.store.NetworkRates()
	inventoryByAgent := map[string]domain.AgentInventory{}
	for _, inventory := range s.store.Inventories() {
		inventoryByAgent[inventory.AgentID] = inventory
	}

	groupIndex := map[string]*utilizationGroup{}
	for _, group := range s.store.ListGroups() {
		if !s.authorizeGroupTarget(r, "resources", "read", group.ID) {
			continue
		}
		groupIndex[group.ID] = &utilizationGroup{ID: group.ID, Name: group.Name, Type: group.Type}
	}

	nodes := []utilizationNode{}
	var coresTotal, coresUsed, memTotal, memUsed, diskSum float64
	var reclaimCores, reclaimMemBytes float64
	idle, saturated, reclaim, scale := 0, 0, 0, 0

	for _, resource := range s.store.ListLiveResources() {
		if resource.Type != "node" && resource.Type != "hypervisor" {
			continue
		}
		if !s.authorizeResourceTarget(r, "resources", "read", resource.ID) {
			continue
		}
		metric := latest[resource.ID]
		data := inventoryByAgent[resource.AgentID].Data
		cores := inventoryNumber(data, "cpuCount")
		memoryBytes := inventoryNumber(data, "memoryBytes")
		coresUsedNode := cores * metric.CPU / 100
		memUsedNode := memoryBytes * metric.Memory / 100

		state := "normal"
		if metric.CPU >= utilSaturatedPercent || metric.Memory >= utilSaturatedPercent || metric.Disk >= utilSaturatedPercent {
			state = "saturated"
			saturated++
		} else if maxFloat(metric.CPU, metric.Memory, metric.Disk) < utilIdlePercent {
			state = "idle"
			idle++
		}

		avg := averages[resource.ID]
		recommendation := "ok"
		if cores > 0 && avg.CPU < rightsizeReclaimCPU && avg.Memory < rightsizeReclaimMemory {
			recommendation = "reclaim"
			reclaim++
			reclaimCores += cores
			reclaimMemBytes += memoryBytes
		} else if avg.CPU >= rightsizeScalePercent || avg.Memory >= rightsizeScalePercent {
			recommendation = "scale"
			scale++
		}

		network := rates[resource.ID]
		nodes = append(nodes, utilizationNode{
			ID: resource.ID, Name: resource.Name, Type: string(resource.Type),
			CPU: metric.CPU, Memory: metric.Memory, Disk: metric.Disk, Network: network.Rx + network.Tx,
			NetworkRx: network.Rx, NetworkTx: network.Tx,
			Cores: cores, MemoryBytes: memoryBytes, CoresUsed: coresUsedNode, MemUsedBytes: memUsedNode, State: state,
			AvgCPU: avg.CPU, AvgMemory: avg.Memory, Recommendation: recommendation,
		})

		coresTotal += cores
		coresUsed += coresUsedNode
		memTotal += memoryBytes
		memUsed += memUsedNode
		diskSum += metric.Disk

		for _, groupID := range s.store.ResourceGroupIDs(resource.ID) {
			group := groupIndex[groupID]
			if group == nil {
				continue
			}
			group.Nodes++
			group.CoresTotal += cores
			group.CoresUsed += coresUsedNode
			group.MemTotalBytes += memoryBytes
			group.MemUsedBytes += memUsedNode
			group.diskSum += metric.Disk
			if state == "idle" {
				group.Idle++
			} else if state == "saturated" {
				group.Saturated++
			}
		}
	}

	sort.Slice(nodes, func(i, j int) bool {
		return maxFloat(nodes[i].CPU, nodes[i].Memory, nodes[i].Disk) >
			maxFloat(nodes[j].CPU, nodes[j].Memory, nodes[j].Disk)
	})

	groups := make([]utilizationGroup, 0, len(groupIndex))
	for _, group := range groupIndex {
		if group.Nodes == 0 {
			continue
		}
		group.CPU = percent(group.CoresUsed, group.CoresTotal)
		group.Memory = percent(group.MemUsedBytes, group.MemTotalBytes)
		group.Disk = group.diskSum / float64(group.Nodes)
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		return maxFloat(groups[i].CPU, groups[i].Memory) > maxFloat(groups[j].CPU, groups[j].Memory)
	})

	fleetDisk := 0.0
	if len(nodes) > 0 {
		fleetDisk = diskSum / float64(len(nodes))
	}

	series := []forecastPoint{}
	for _, point := range s.store.AggregatedMetricsFor(time.Now().Add(-forecastWindow), forecastBucket, s.visibleResourceIDs(r, "resources")) {
		if point.Count == 0 {
			continue
		}
		series = append(series, forecastPoint{Timestamp: point.Timestamp, CPU: point.CPU, Memory: point.Memory, Disk: point.Disk, Network: point.NetworkRxRate + point.NetworkTxRate})
	}
	forecasts := []utilizationForecast{}
	if len(series) >= 2 {
		forecasts = append(forecasts,
			buildForecast("cpu", series, func(p forecastPoint) float64 { return p.CPU }),
			buildForecast("memory", series, func(p forecastPoint) float64 { return p.Memory }))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"fleet": map[string]any{
			"nodes":           len(nodes),
			"coresTotal":      coresTotal,
			"coresUsed":       coresUsed,
			"cpu":             percent(coresUsed, coresTotal),
			"memTotalBytes":   memTotal,
			"memUsedBytes":    memUsed,
			"memory":          percent(memUsed, memTotal),
			"disk":            fleetDisk,
			"idle":            idle,
			"saturated":       saturated,
			"reclaim":         reclaim,
			"scale":           scale,
			"reclaimCores":    reclaimCores,
			"reclaimMemBytes": reclaimMemBytes,
		},
		"nodes":    nodes,
		"groups":   groups,
		"series":   series,
		"forecast": forecasts,
	})
}
