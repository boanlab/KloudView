package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func TestResourceListHidesTerminatedUntilAsked(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node", Type: domain.ResourceNode, AgentID: "agent-01"})
	created := memory.UpsertResource(domain.Resource{ID: "container-gone", Name: "gone", Type: domain.ResourceContainer, AgentID: "agent-01"})
	stopped := created.CreatedAt.Add(2 * time.Hour)
	created.TerminatedAt = &stopped
	memory.UpsertResource(created)
	handler := New(memory, "test-token", "").Handler()

	body := getBody(t, handler, "/api/v1/resources")
	if strings.Contains(body, "container-gone") {
		t.Fatalf("terminated resource listed by default: %s", body)
	}
	if body = getBody(t, handler, "/api/v1/resources?lifecycle=terminated"); !strings.Contains(body, "container-gone") || strings.Contains(body, "node-01") {
		t.Fatalf("terminated-only listing mismatch: %s", body)
	}
	if body = getBody(t, handler, "/api/v1/resources?lifecycle=all"); !strings.Contains(body, "container-gone") || !strings.Contains(body, "node-01") {
		t.Fatalf("combined listing mismatch: %s", body)
	}
	// One hour before it stopped it was still running.
	at := stopped.Add(-time.Hour).Format(time.RFC3339Nano)
	if body = getBody(t, handler, "/api/v1/resources?at="+at); !strings.Contains(body, "container-gone") {
		t.Fatalf("point-in-time query missed a running resource: %s", body)
	}
	// One hour after, it was not.
	at = stopped.Add(time.Hour).Format(time.RFC3339Nano)
	if body = getBody(t, handler, "/api/v1/resources?at="+at); strings.Contains(body, "container-gone") {
		t.Fatalf("point-in-time query returned a stopped resource: %s", body)
	}
}

func TestMetricWindowReadsHistory(t *testing.T) {
	memory := store.NewMemory()
	now := time.Now().UTC()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node", Type: domain.ResourceNode})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", CPU: 11, Timestamp: now})
	var asked struct{ from, to time.Time }
	server := New(memory, "test-token", "").WithMetricHistory(
		func(_ context.Context, resourceID string, from, to time.Time) ([]domain.MetricSample, error) {
			asked.from, asked.to = from, to
			return []domain.MetricSample{{ResourceID: resourceID, CPU: 77, Timestamp: from}}, nil
		})
	handler := server.Handler()

	// No window: the live in-memory series.
	if body := getBody(t, handler, "/api/v1/resources/node-01/metrics"); !strings.Contains(body, `"cpu":11`) {
		t.Fatalf("live metrics missing: %s", body)
	}
	// A window reaches the history reader instead.
	from := now.Add(-90 * 24 * time.Hour)
	body := getBody(t, handler, "/api/v1/resources/node-01/metrics?from="+from.Format(time.RFC3339)+"&to="+now.Format(time.RFC3339))
	if !strings.Contains(body, `"cpu":77`) || !strings.Contains(body, `"source":"history"`) {
		t.Fatalf("history metrics missing: %s", body)
	}
	if asked.from.Sub(from).Abs() > time.Second || asked.to.Sub(now).Abs() > time.Second {
		t.Fatalf("window not forwarded: %+v", asked)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources/node-01/metrics?from="+now.Format(time.RFC3339)+"&to="+from.Format(time.RFC3339), nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("reversed window status %d", recorder.Code)
	}
}

func TestMetricWindowWithoutHistoryFiltersMemory(t *testing.T) {
	memory := store.NewMemory()
	now := time.Now().UTC()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node", Type: domain.ResourceNode})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", CPU: 11, Timestamp: now.Add(-5 * time.Minute)})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", CPU: 22, Timestamp: now.Add(-30 * time.Second)})
	handler := New(memory, "test-token", "").Handler()
	body := getBody(t, handler, "/api/v1/resources/node-01/metrics?from="+now.Add(-time.Minute).Format(time.RFC3339))
	if strings.Contains(body, `"cpu":11`) || !strings.Contains(body, `"cpu":22`) {
		t.Fatalf("memory window not applied: %s", body)
	}
	var payload struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil || payload.Source != "memory" {
		t.Fatalf("source = %q (%v)", payload.Source, err)
	}
}

func getBody(t *testing.T, handler http.Handler, target string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s -> %d: %s", target, recorder.Code, recorder.Body.String())
	}
	return recorder.Body.String()
}

// Live views describe what is running now. A terminated record must not keep
// counting toward health totals, the heatmap, or the attention list.
func TestTerminatedResourcesLeaveTheLiveViews(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node", Type: domain.ResourceNode, AgentID: "agent-01", Health: domain.HealthHealthy})
	gone := memory.UpsertResource(domain.Resource{ID: "process-gone", Name: "sh", Type: domain.ResourceProcess, AgentID: "agent-01", Health: domain.HealthCritical})
	if body := getBody(t, New(memory, "test-token", "").Handler(), "/api/v1/overview"); !strings.Contains(body, "process-gone") {
		t.Fatalf("a live resource is missing from the overview: %s", body)
	}

	stopped := gone.CreatedAt.Add(time.Minute)
	gone.TerminatedAt = &stopped
	memory.UpsertResource(gone)
	// The overview is cached for two seconds, so read it through a fresh server.
	body := getBody(t, New(memory, "test-token", "").Handler(), "/api/v1/overview")
	if strings.Contains(body, "process-gone") {
		t.Fatalf("a terminated resource still counts as live: %s", body)
	}
	if !strings.Contains(body, `"total":1`) {
		t.Fatalf("the terminated resource still counts toward the totals: %s", body)
	}
}

// A zombie is a child the parent has not reaped, which clears on its own.
func TestZombieProcessIsAWarningNotCritical(t *testing.T) {
	if got := inventoryHealth(map[string]any{"state": "Z (zombie)"}); got != domain.HealthWarning {
		t.Fatalf("zombie health = %q, want warning", got)
	}
	if got := inventoryHealth(map[string]any{"state": "dead"}); got != domain.HealthCritical {
		t.Fatalf("dead health = %q, want critical", got)
	}
}
