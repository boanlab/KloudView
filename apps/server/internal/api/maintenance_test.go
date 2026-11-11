package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func maintenanceFleet(t *testing.T) (http.Handler, *store.Memory) {
	t.Helper()
	memory := store.NewMemory()
	for _, id := range []string{"node-01", "node-02"} {
		memory.UpsertResource(domain.Resource{ID: id, Name: id, Type: domain.ResourceNode, AgentID: "agent-" + id, Health: domain.HealthHealthy})
	}
	handler := New(memory, "test-token", "").Handler()
	rule := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", strings.NewReader(`{"name":"CPU high","metric":"cpu","operator":">","threshold":80,"duration":"0s","severity":"critical","enabled":true}`))
	authorize(rule)
	handler.ServeHTTP(httptest.NewRecorder(), rule)
	return handler, memory
}

func postSilence(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alert-silences", strings.NewReader(body))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func window() (string, string) {
	now := time.Now().UTC()
	return now.Add(-time.Minute).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339)
}

func TestSilenceNamingAResourceQuietsOnlyThatResource(t *testing.T) {
	handler, memory := maintenanceFleet(t)
	for _, id := range []string{"node-01", "node-02"} {
		metric := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-"+id+"/metrics", strings.NewReader(fmt.Sprintf(`{"resourceId":%q,"cpu":95}`, id)))
		agentAuthorize(metric)
		handler.ServeHTTP(httptest.NewRecorder(), metric)
	}
	start, end := window()
	if recorder := postSilence(t, handler, fmt.Sprintf(`{"name":"Patch node-01","resourceIds":["node-01"],"startsAt":%q,"endsAt":%q}`, start, end)); recorder.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", recorder.Code, recorder.Body.String())
	}
	statuses := map[string]string{}
	for _, alert := range memory.ListAlerts() {
		statuses[alert.ResourceID] = alert.Status
	}
	if statuses["node-01"] != "silenced" || statuses["node-02"] != "firing" {
		t.Fatalf("statuses = %v, want node-01 silenced and node-02 still firing", statuses)
	}

	for id, want := range map[string]domain.Health{"node-01": domain.HealthMaintenance, "node-02": domain.HealthCritical} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/resources/"+id, nil)
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var resource domain.Resource
		_ = json.Unmarshal(recorder.Body.Bytes(), &resource)
		if resource.Health != want {
			t.Errorf("%s health = %q, want %q", id, resource.Health, want)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources?q=node", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !strings.Contains(recorder.Body.String(), `"health":"maintenance"`) {
		t.Errorf("resource list does not show maintenance: %s", recorder.Body.String())
	}
}

func TestSilenceResourceListIsValidated(t *testing.T) {
	handler, _ := maintenanceFleet(t)
	start, end := window()
	for _, test := range []struct {
		ids  string
		want int
	}{
		{`["node-missing"]`, http.StatusBadRequest},
		{`["node-01","node-01"]`, http.StatusBadRequest},
		{`[""]`, http.StatusBadRequest},
		{`["node-01","node-02"]`, http.StatusCreated},
	} {
		recorder := postSilence(t, handler, fmt.Sprintf(`{"name":"Patch","resourceIds":%s,"startsAt":%q,"endsAt":%q}`, test.ids, start, end))
		if recorder.Code != test.want {
			t.Errorf("resourceIds %s = %d, want %d (%s)", test.ids, recorder.Code, test.want, recorder.Body.String())
		}
	}
}

func TestStaleHostUnderMaintenanceIsNotUnknown(t *testing.T) {
	resource := domain.Resource{ID: "node-01", AgentID: "agent-node-01", Health: domain.HealthHealthy}
	health, stale := effectiveResourceHealth(resource, map[string]domain.Health{"node-01": domain.HealthCritical}, map[string]bool{}, map[string]bool{"node-01": true})
	if health != domain.HealthMaintenance || !stale {
		t.Fatalf("health = %q stale = %v, want maintenance and still flagged stale", health, stale)
	}
	resource.Health = domain.HealthMaintenance
	if health, _ := effectiveResourceHealth(resource, map[string]domain.Health{"node-01": domain.HealthCritical}, map[string]bool{"agent-node-01": true}, nil); health != domain.HealthMaintenance {
		t.Fatalf("manual maintenance overridden by alert health: %q", health)
	}
}
