package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func TestAgentListFilters(t *testing.T) {
	memory := store.NewMemory()
	now := time.Now().UTC()
	for _, agent := range []domain.Agent{
		{ID: "agent-web-1", Hostname: "web-1", NodeID: "node-web-1", Version: "1.2.0", LastSeenAt: now, Labels: map[string]string{"zone": "seoul-a"}},
		{ID: "agent-web-2", Hostname: "web-2", NodeID: "node-web-2", Version: "1.1.0", LastSeenAt: now.Add(-2 * time.Minute), Labels: map[string]string{"zone": "busan-a"}},
		{ID: "agent-db-1", Hostname: "db-1", NodeID: "node-db-1", Version: "1.1.0", LastSeenAt: now.Add(-72 * time.Hour), Labels: map[string]string{"zone": "seoul-a"}},
	} {
		memory.UpsertAgent(agent)
	}
	handler := New(memory, "test-token", "").Handler()
	list := func(query string) (int, []string) {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/agents?"+query, nil)
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var body struct {
			Items []domain.Agent `json:"items"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		names := []string{}
		for _, agent := range body.Items {
			names = append(names, agent.Hostname)
		}
		sort.Strings(names)
		return recorder.Code, names
	}
	for _, test := range []struct {
		query string
		want  []string
	}{
		{"", []string{"db-1", "web-1", "web-2"}},
		{"status=online", []string{"web-1"}},
		{"status=offline", []string{"db-1", "web-2"}},
		{"version=1.1.0", []string{"db-1", "web-2"}},
		{"silentFor=1h", []string{"db-1"}},
		{"silentFor=1m&status=offline", []string{"db-1", "web-2"}},
		{"q=WEB", []string{"web-1", "web-2"}},
		{"q=zone=seoul-a", []string{"db-1", "web-1"}},
		{"q=seoul&version=1.2.0", []string{"web-1"}},
	} {
		code, got := list(test.query)
		if code != http.StatusOK || len(got) != len(test.want) {
			t.Errorf("GET ?%s = %d %v, want %v", test.query, code, got, test.want)
			continue
		}
		for i := range got {
			if got[i] != test.want[i] {
				t.Errorf("GET ?%s = %v, want %v", test.query, got, test.want)
				break
			}
		}
	}
	for _, query := range []string{"status=dead", "silentFor=soon", "silentFor=-1h"} {
		if code, _ := list(query); code != http.StatusBadRequest {
			t.Errorf("GET ?%s = %d, want 400", query, code)
		}
	}
}
