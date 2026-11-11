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

func sortFleet(t *testing.T) http.Handler {
	t.Helper()
	memory := store.NewMemory()
	now := time.Now().UTC()
	for _, r := range []struct {
		id, name string
		tags     map[string]string
		cpu      float64
		measured bool
	}{
		{"node-a", "alpha", map[string]string{"env": "prod", "rack": "7"}, 20, true},
		{"node-b", "bravo", map[string]string{"env": "production"}, 90, true},
		{"node-c", "charlie", map[string]string{"env": "prod", "rack": "8"}, 55, true},
		{"node-d", "delta", map[string]string{"env": "prod", "rack": "7"}, 0, false},
	} {
		memory.UpsertResource(domain.Resource{ID: r.id, Name: r.name, Type: domain.ResourceNode, Tags: r.tags})
		if r.measured {
			memory.AddMetric(domain.MetricSample{ResourceID: r.id, Timestamp: now, CPU: r.cpu, Memory: 10, Disk: 10})
		}
	}
	return New(memory, "test-token", "").Handler()
}

func listNames(t *testing.T, handler http.Handler, query string) (int, []string, map[string]json.RawMessage) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources?"+query, nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var body struct {
		Items   []domain.Resource          `json:"items"`
		Metrics map[string]json.RawMessage `json:"metrics"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	names := []string{}
	for _, item := range body.Items {
		names = append(names, item.Name)
	}
	return recorder.Code, names, body.Metrics
}

func TestResourceListSortsByLoadWithUnmeasuredLast(t *testing.T) {
	handler := sortFleet(t)
	for query, want := range map[string]string{
		"sort=cpu":                          "bravo,charlie,alpha,delta",
		"sort=cpu&order=asc":                "alpha,charlie,bravo,delta",
		"sort=name&order=desc":              "delta,charlie,bravo,alpha",
		"tag=env=prod":                      "alpha,charlie,delta",
		"tag=env=prod&tag=rack=7&sort=name": "alpha,delta",
	} {
		code, names, _ := listNames(t, handler, query)
		if code != http.StatusOK || strings.Join(names, ",") != want {
			t.Errorf("?%s = %d %v, want %s", query, code, names, want)
		}
	}
	_, _, metrics := listNames(t, handler, "sort=cpu&limit=1")
	if !strings.Contains(string(metrics["node-b"]), `"cpu":90`) {
		t.Errorf("metrics for the page = %v", metrics)
	}
}

func TestResourceListRejectsBadSortAndTag(t *testing.T) {
	handler := sortFleet(t)
	for _, query := range []string{"sort=owner", "sort=cpu&order=up", "tag=env", "tag==prod", "sort=cpu&cursor=abc"} {
		if code, _, _ := listNames(t, handler, query); code != http.StatusBadRequest {
			t.Errorf("?%s = %d, want 400", query, code)
		}
	}
}
