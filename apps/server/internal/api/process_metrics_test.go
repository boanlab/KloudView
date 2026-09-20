package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// processFixture enrols an agent and gives its node one process, the way an
// inventory report would.
func processFixture(t *testing.T) (*store.Memory, http.Handler, string, string) {
	t.Helper()
	memory := store.NewMemory()
	handler := New(memory, "test-token", "").Handler()
	enroll := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"test-token","hostname":"node-01"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, enroll)
	var enrolled struct {
		Agent      domain.Agent `json:"agent"`
		Credential string       `json:"credential"`
	}
	json.Unmarshal(recorder.Body.Bytes(), &enrolled)
	resourceID := stableID(string(domain.ResourceProcess), enrolled.Agent.NodeID+"-1084")
	memory.UpsertResource(domain.Resource{
		ID: resourceID, Name: "agetty", Type: domain.ResourceProcess, AgentID: enrolled.Agent.ID,
		Attributes: map[string]string{"pid": "1084"},
	})
	return memory, handler, enrolled.Credential, resourceID
}

func postProcessMetrics(t *testing.T, handler http.Handler, credential, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/process-metrics", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+credential)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestProcessReadingsBecomeSamplesTheConsoleCanChart(t *testing.T) {
	memory, handler, credential, resourceID := processFixture(t)
	body := `{"nodeId":"node-node-01","items":[{"pid":1084,"name":"agetty","state":"S","cpuPercent":3.5,"memoryBytes":2097152,"memoryPercent":1.25,"hostMemoryBytes":16777216000,"threads":4,"startedAt":"2026-09-19T10:00:00Z"}]}`
	if recorder := postProcessMetrics(t, handler, credential, body); recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	samples := memory.Metrics(resourceID)
	if len(samples) != 1 {
		t.Fatalf("got %d samples, want the one that was posted", len(samples))
	}
	if samples[0].CPU != 3.5 || samples[0].Memory != 1.25 {
		t.Fatalf("sample = %+v", samples[0])
	}
	resource, _ := memory.Resource(resourceID)
	for key, want := range map[string]string{
		"memoryUsedBytes": "2097152",
		"hostMemoryBytes": "16777216000",
		"threads":         "4",
		"state":           "S",
		"startedAt":       "2026-09-19T10:00:00Z",
	} {
		if resource.Attributes[key] != want {
			t.Errorf("%s = %q, want %q", key, resource.Attributes[key], want)
		}
	}
	// Without this the console cannot tell a process that was measured and
	// found idle from one that was never measured at all.
	if resource.Attributes["metricsSampledAt"] == "" {
		t.Error("nothing recorded that this process is being sampled")
	}
}

func TestAProcessTheInventoryHasNotSeenGetsNoSample(t *testing.T) {
	memory, handler, credential, _ := processFixture(t)
	body := `{"nodeId":"node-node-01","items":[{"pid":9999,"cpuPercent":50}]}`
	recorder := postProcessMetrics(t, handler, credential, body)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d", recorder.Code)
	}
	var result struct {
		Accepted int `json:"accepted"`
	}
	json.Unmarshal(recorder.Body.Bytes(), &result)
	if result.Accepted != 0 {
		t.Fatalf("accepted %d readings for a process with no resource", result.Accepted)
	}
	if memory.HasResource(stableID(string(domain.ResourceProcess), "node-node-01-9999")) {
		t.Error("a metric reading conjured a resource the inventory never reported")
	}
}

func TestProcessMetricsStayWithTheirOwnAgent(t *testing.T) {
	_, handler, _, _ := processFixture(t)
	// A second agent on a second node must not be able to write samples onto
	// the first node's processes.
	enroll := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"test-token","hostname":"node-02"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, enroll)
	var intruder struct {
		Credential string `json:"credential"`
	}
	json.Unmarshal(recorder.Body.Bytes(), &intruder)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-02/process-metrics",
		strings.NewReader(`{"nodeId":"node-node-01","items":[{"pid":1084,"cpuPercent":99}]}`))
	request.Header.Set("Authorization", "Bearer "+intruder.Credential)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want a refusal", response.Code)
	}
}

func TestAnImpossibleProcessReadingIsDropped(t *testing.T) {
	memory, handler, credential, resourceID := processFixture(t)
	body := `{"nodeId":"node-node-01","items":[{"pid":1084,"cpuPercent":-5,"memoryPercent":900}]}`
	if recorder := postProcessMetrics(t, handler, credential, body); recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d", recorder.Code)
	}
	// Clamped rather than refused: a reading slightly out of range is still a
	// reading, and the gauge has to mean something.
	samples := memory.Metrics(resourceID)
	if len(samples) != 1 {
		t.Fatalf("got %d samples", len(samples))
	}
	if samples[0].CPU < 0 || samples[0].Memory > 100 {
		t.Fatalf("sample was not clamped: %+v", samples[0])
	}
}
