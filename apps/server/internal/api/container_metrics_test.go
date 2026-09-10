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

// containerHandler enrols an agent and reports one container through the
// inventory, which is what creates the resource a sample can attach to.
func containerHandler(t *testing.T, runtimeID string) (http.Handler, string, string, string) {
	t.Helper()
	memory := store.NewMemory()
	handler := New(memory, "test-token", "").Handler()
	enrolled := enrolAgent(t, handler)
	inventory := `{"hostname":"node-01","containers":[{"id":"` + runtimeID + `","name":"web","image":"nginx","state":"running","runtime":"docker"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+enrolled.Agent.ID+"/inventory", strings.NewReader(inventory))
	request.Header.Set("Authorization", "Bearer "+enrolled.Credential)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("inventory status %d: %s", recorder.Code, recorder.Body.String())
	}
	return handler, enrolled.Agent.ID, enrolled.Agent.NodeID, enrolled.Credential
}

func postContainerMetrics(t *testing.T, handler http.Handler, agentID, credential, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agentID+"/container-metrics", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+credential)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestContainerMetricsAttachToTheContainerResource(t *testing.T) {
	runtimeID := "d4a3340c5323"
	handler, agentID, nodeID, credential := containerHandler(t, runtimeID)
	body := `{"nodeId":"` + nodeID + `","timestamp":"` + time.Now().UTC().Format(time.RFC3339) +
		`","items":[{"id":"` + runtimeID + `","cpuPercent":12.5,"memoryBytes":68141056,"memoryLimitBytes":134217728,"memoryPercent":50.8,"diskReadBytes":1536,"diskWriteBytes":2304,"processes":38}]}`
	recorder := postContainerMetrics(t, handler, agentID, credential, body)
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"accepted":1`) {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}

	resourceID := stableID(string(domain.ResourceContainer), nodeID+"-"+runtimeID)
	metrics := getBody(t, handler, "/api/v1/resources/"+resourceID+"/metrics")
	if !strings.Contains(metrics, `"cpu":12.5`) || !strings.Contains(metrics, `"memory":50.8`) {
		t.Fatalf("container samples missing: %s", metrics)
	}
	// Block IO is a byte counter, so it must not arrive dressed as a percentage.
	if strings.Contains(metrics, "1536") || strings.Contains(metrics, "2304") {
		t.Fatalf("byte counters leaked into the percentage series: %s", metrics)
	}

	var resource domain.Resource
	if err := json.Unmarshal([]byte(getBody(t, handler, "/api/v1/resources/"+resourceID)), &resource); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"memoryBytes": "68141056", "memoryLimitBytes": "134217728",
		"diskReadBytes": "1536", "diskWriteBytes": "2304", "processes": "38",
	} {
		if resource.Attributes[key] != want {
			t.Errorf("attribute %s = %q, want %q", key, resource.Attributes[key], want)
		}
	}
	// The inventory's own fields survive the metric update.
	if resource.Attributes["image"] != "nginx" {
		t.Errorf("inventory attributes lost: %+v", resource.Attributes)
	}

	// Inventory runs on a slower cycle; a refresh must not erase the readings.
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agentID+"/inventory",
		strings.NewReader(`{"hostname":"node-01","containers":[{"id":"`+runtimeID+`","name":"web","image":"nginx","state":"running","runtime":"docker"}]}`))
	request.Header.Set("Authorization", "Bearer "+credential)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	var refreshed domain.Resource
	if err := json.Unmarshal([]byte(getBody(t, handler, "/api/v1/resources/"+resourceID)), &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.Attributes["memoryBytes"] != "68141056" || refreshed.Attributes["processes"] != "38" {
		t.Fatalf("an inventory refresh erased the cgroup readings: %+v", refreshed.Attributes)
	}
}

func TestContainerMetricsIgnoreUnknownContainers(t *testing.T) {
	handler, agentID, nodeID, credential := containerHandler(t, "d4a3340c5323")
	body := `{"nodeId":"` + nodeID + `","items":[{"id":"not-reported-yet","cpuPercent":5}]}`
	recorder := postContainerMetrics(t, handler, agentID, credential, body)
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"accepted":0`) {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestContainerMetricsRejectAnotherAgentsNode(t *testing.T) {
	handler, agentID, _, credential := containerHandler(t, "d4a3340c5323")
	recorder := postContainerMetrics(t, handler, agentID, credential, `{"nodeId":"node-elsewhere","items":[]}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown node status %d, want 404", recorder.Code)
	}
}
