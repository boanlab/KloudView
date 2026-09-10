package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func TestUtilizationCapacityAndFlags(t *testing.T) {
	memory := store.NewMemory()
	now := time.Now().UTC()
	memory.UpsertResource(domain.Resource{ID: "node-busy", Name: "busy", Type: domain.ResourceNode, AgentID: "agent-busy", Health: domain.HealthHealthy})
	memory.UpsertResource(domain.Resource{ID: "node-idle", Name: "idle", Type: domain.ResourceNode, AgentID: "agent-idle", Health: domain.HealthHealthy})
	memory.PutInventory(domain.AgentInventory{AgentID: "agent-busy", NodeID: "node-busy", Data: map[string]any{"cpuCount": float64(10), "memoryBytes": float64(1000)}, ObservedAt: now})
	memory.PutInventory(domain.AgentInventory{AgentID: "agent-idle", NodeID: "node-idle", Data: map[string]any{"cpuCount": float64(4), "memoryBytes": float64(2000)}, ObservedAt: now})
	memory.PutGroup(domain.Group{ID: "rack-01", Name: "Rack-01", Type: "rack"})
	memory.PutMembership(domain.GroupMembership{ID: "m1", GroupID: "rack-01", ResourceID: "node-busy"})
	memory.PutMembership(domain.GroupMembership{ID: "m2", GroupID: "rack-01", ResourceID: "node-idle"})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-busy", CPU: 90, Memory: 50, Disk: 20, Timestamp: now})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-idle", CPU: 2, Memory: 3, Disk: 1, Timestamp: now})

	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/utilization", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, body)
	}
	// One host is saturated (cpu 90 >= 85), one is idle (all metrics < 10).
	if !strings.Contains(body, `"saturated":1`) || !strings.Contains(body, `"idle":1`) {
		t.Fatalf("idle/saturated flags mismatch: %s", body)
	}
	// Capacity totals come from inventory (10 + 4 cores).
	if !strings.Contains(body, `"coresTotal":14`) {
		t.Fatalf("cores total mismatch: %s", body)
	}
	// The saturated host is ranked and flagged.
	if !strings.Contains(body, `"state":"saturated"`) {
		t.Fatalf("state mismatch: %s", body)
	}
	// Rightsizing: the sustained-idle host is a reclaim candidate, the
	// sustained-high host a scale candidate.
	if !strings.Contains(body, `"reclaim":1`) || !strings.Contains(body, `"scale":1`) {
		t.Fatalf("rightsizing counts mismatch: %s", body)
	}
	if !strings.Contains(body, `"recommendation":"reclaim"`) || !strings.Contains(body, `"recommendation":"scale"`) {
		t.Fatalf("recommendation mismatch: %s", body)
	}
}

func TestUtilizationForecastProjectsExhaustion(t *testing.T) {
	memory := store.NewMemory()
	now := time.Now().UTC()
	memory.UpsertResource(domain.Resource{ID: "node-1", Name: "n1", Type: domain.ResourceNode, AgentID: "agent-1", Health: domain.HealthHealthy})
	memory.PutInventory(domain.AgentInventory{AgentID: "agent-1", NodeID: "node-1", Data: map[string]any{"cpuCount": float64(8), "memoryBytes": float64(1000)}, ObservedAt: now})
	// A steadily rising CPU trend across hourly samples must yield a positive
	// slope and a finite projection to the headroom-exhaustion line.
	for i := 0; i < 12; i++ {
		ts := now.Add(-time.Duration(11-i) * time.Hour)
		memory.AddMetric(domain.MetricSample{ResourceID: "node-1", CPU: float64(10 + i*2), Memory: 5, Disk: 1, Timestamp: ts})
	}

	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/utilization", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}

	var payload struct {
		Forecast []struct {
			Metric      string  `json:"metric"`
			SlopePerDay float64 `json:"slopePerDay"`
			DaysToFull  float64 `json:"daysToFull"`
		} `json:"forecast"`
		Series []struct {
			CPU float64 `json:"cpu"`
		} `json:"series"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Series) < 6 {
		t.Fatalf("expected multi-bucket series, got %d", len(payload.Series))
	}
	var cpu *struct {
		Metric      string  `json:"metric"`
		SlopePerDay float64 `json:"slopePerDay"`
		DaysToFull  float64 `json:"daysToFull"`
	}
	for i := range payload.Forecast {
		if payload.Forecast[i].Metric == "cpu" {
			cpu = &payload.Forecast[i]
		}
	}
	if cpu == nil {
		t.Fatalf("no cpu forecast: %s", recorder.Body.String())
	}
	if cpu.SlopePerDay <= 0 {
		t.Fatalf("expected rising slope, got %v", cpu.SlopePerDay)
	}
	if cpu.DaysToFull <= 0 {
		t.Fatalf("expected finite exhaustion projection, got %v", cpu.DaysToFull)
	}
}

// Inventory decodes with UseNumber, so hardware figures arrive as json.Number.
// Reading only float64 silently reported every node as having no capacity,
// which zeroes core counts and hides the capacity summary.
func TestInventoryNumberReadsEveryNumericShape(t *testing.T) {
	for name, data := range map[string]map[string]any{
		"json.Number": {"cpuCount": json.Number("24")},
		"float64":     {"cpuCount": float64(24)},
		"int":         {"cpuCount": 24},
		"int64":       {"cpuCount": int64(24)},
		"uint64":      {"cpuCount": uint64(24)},
	} {
		if got := inventoryNumber(data, "cpuCount"); got != 24 {
			t.Errorf("%s: inventoryNumber = %v, want 24", name, got)
		}
	}
	for name, data := range map[string]map[string]any{
		"missing key": {},
		"nil map":     nil,
		"unparseable": {"cpuCount": json.Number("not-a-number")},
		"wrong type":  {"cpuCount": "24"},
	} {
		if got := inventoryNumber(data, "cpuCount"); got != 0 {
			t.Errorf("%s: inventoryNumber = %v, want 0", name, got)
		}
	}
}
