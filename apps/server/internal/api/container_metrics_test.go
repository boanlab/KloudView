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
	// memoryUsedBytes is what is in use; memoryBytes is what that is a share
	// of -- here the container's own limit, because it has one.
	for key, want := range map[string]string{
		"memoryUsedBytes": "68141056", "memoryBytes": "134217728", "memoryLimitBytes": "134217728",
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
	if refreshed.Attributes["memoryUsedBytes"] != "68141056" || refreshed.Attributes["processes"] != "38" {
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

// A container and a VM must store their memory the same way, because one
// console reads both. The container path wrote usage under memoryBytes while
// the VM path wrote the allowance there, so a container's meter rendered
// "0 B / 8.2 MB" -- nothing used, and the usage standing in for the total.
func TestAGuestReportsUsageAndItsBasisTheSameWayWhicheverKindItIs(t *testing.T) {
	runtimeID := "d4a3340c5323"
	handler, agentID, nodeID, credential := containerHandler(t, runtimeID)
	body := `{"nodeId":"` + nodeID + `","items":[{"id":"` + runtimeID +
		`","cpuPercent":5,"memoryBytes":8331264,"memoryLimitBytes":0,"hostMemoryBytes":16777216000,"memoryPercent":0.05}]}`
	if recorder := postContainerMetrics(t, handler, agentID, credential, body); recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	resource := getBody(t, handler, "/api/v1/resources/"+stableID(string(domain.ResourceContainer), nodeID+"-"+runtimeID))
	if !strings.Contains(resource, `"memoryUsedBytes":"8331264"`) {
		t.Errorf("usage is not under memoryUsedBytes: %s", resource)
	}
	// No limit of its own, so the machine is what the percentage is a share of
	// and the meter has to say so.
	if !strings.Contains(resource, `"memoryBytes":"16777216000"`) {
		t.Errorf("the basis the percentage was taken against is missing: %s", resource)
	}
	if strings.Contains(resource, `"memoryLimitBytes"`) {
		t.Errorf("an unlimited container was given a limit: %s", resource)
	}
}

func TestALimitedContainerIsMeasuredAgainstItsLimit(t *testing.T) {
	runtimeID := "d4a3340c5323"
	handler, agentID, nodeID, credential := containerHandler(t, runtimeID)
	body := `{"nodeId":"` + nodeID + `","items":[{"id":"` + runtimeID +
		`","cpuPercent":5,"memoryBytes":33554432,"memoryLimitBytes":67108864,"hostMemoryBytes":16777216000,"memoryPercent":50}]}`
	postContainerMetrics(t, handler, agentID, credential, body)
	resource := getBody(t, handler, "/api/v1/resources/"+stableID(string(domain.ResourceContainer), nodeID+"-"+runtimeID))
	if !strings.Contains(resource, `"memoryBytes":"67108864"`) {
		t.Errorf("a limited container is not measured against its limit: %s", resource)
	}
	if !strings.Contains(resource, `"memoryUsedBytes":"33554432"`) {
		t.Errorf("usage missing: %s", resource)
	}
}
