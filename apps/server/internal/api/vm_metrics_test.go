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

// vmFixture enrols an agent and gives it a node with one VM on it, the way an
// inventory report would.
func vmFixture(t *testing.T) (*store.Memory, http.Handler, string) {
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
	memory.UpsertResource(domain.Resource{
		ID:   stableID(string(domain.ResourceVM), enrolled.Agent.NodeID+"-web-01"),
		Name: "web-01", Type: domain.ResourceVM, AgentID: enrolled.Agent.ID,
		Attributes: map[string]string{"name": "web-01"},
	})
	return memory, handler, enrolled.Credential
}

func postVMMetrics(t *testing.T, handler http.Handler, credential, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/vm-metrics", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+credential)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestVMMetricsBecomeSamplesTheConsoleCanChart(t *testing.T) {
	memory, handler, credential := vmFixture(t)
	body := `{"nodeId":"node-node-01","items":[{"name":"web-01","state":"running","vcpus":2,"cpuPercent":37.5,"memoryBytes":101711872,"memoryLimitBytes":268435456,"memoryPercent":37.9,"hostMemoryBytes":551931904,"diskReadBytes":1500,"diskWriteBytes":2750,"networkRxRate":2048,"networkTxRate":1024}]}`
	if recorder := postVMMetrics(t, handler, credential, body); recorder.Code != http.StatusAccepted {
		t.Fatalf("ingest = %d %s", recorder.Code, recorder.Body.String())
	}
	resourceID := stableID(string(domain.ResourceVM), "node-node-01-web-01")
	samples := memory.Metrics(resourceID)
	if len(samples) != 1 {
		t.Fatalf("samples = %d, want the reading to be chartable", len(samples))
	}
	sample := samples[0]
	if sample.CPU != 37.5 || sample.Memory != 37.9 {
		t.Fatalf("sample = %+v", sample)
	}
	if sample.NetworkRx != 2048 || sample.NetworkTx != 1024 {
		t.Fatalf("network = %d/%d", sample.NetworkRx, sample.NetworkTx)
	}
	// Absolute figures stay on the resource, where the console reads them next
	// to the state. The guest's use and the emulator's cost are separate.
	resource, _ := memory.Resource(resourceID)
	for key, want := range map[string]string{
		"memoryUsedBytes": "101711872",
		"memoryBytes":     "268435456",
		"hostMemoryBytes": "551931904",
		"vcpus":           "2",
		"state":           "running",
	} {
		if resource.Attributes[key] != want {
			t.Errorf("attribute %s = %q, want %q", key, resource.Attributes[key], want)
		}
	}
}

func TestVMMetricsForAnUnknownDomainAreDropped(t *testing.T) {
	memory, handler, credential := vmFixture(t)
	body := `{"nodeId":"node-node-01","items":[{"name":"not-reported-yet","cpuPercent":10}]}`
	recorder := postVMMetrics(t, handler, credential, body)
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"accepted":0`) {
		t.Fatalf("ingest = %d %s", recorder.Code, recorder.Body.String())
	}
	// It must not invent a resource: the inventory owns what exists.
	if _, found := memory.Resource(stableID(string(domain.ResourceVM), "node-node-01-not-reported-yet")); found {
		t.Fatal("a metric reading created a resource the inventory never reported")
	}
}

func TestVMMetricsRejectAnotherAgentsNode(t *testing.T) {
	_, handler, credential := vmFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-somebody-else/vm-metrics",
		strings.NewReader(`{"nodeId":"node-node-01","items":[{"name":"web-01","cpuPercent":10}]}`))
	request.Header.Set("Authorization", "Bearer "+credential)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusAccepted {
		t.Fatalf("an agent posted readings for a node it does not own: %d", recorder.Code)
	}
}
