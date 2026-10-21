package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// Listings put nodes ahead of VMs and processes whatever their names, so a
// node named node-01 comes before a VM named instance-01. Following the cursor
// has to visit each resource once.
func TestResourceCursorVisitsEachResourceOnce(t *testing.T) {
	memory := store.NewMemory()
	for index := range 3 {
		memory.UpsertResource(domain.Resource{ID: fmt.Sprintf("node-%d", index), Name: fmt.Sprintf("r1n%d", 10+index), Type: domain.ResourceNode})
		memory.UpsertResource(domain.Resource{ID: fmt.Sprintf("vm-%d", index), Name: fmt.Sprintf("instance-%02d", index), Type: domain.ResourceVM})
		memory.UpsertResource(domain.Resource{ID: fmt.Sprintf("process-%d", index), Name: fmt.Sprintf("a-process-%d", index), Type: domain.ResourceProcess})
	}
	handler := New(memory, "test-token", "").Handler()

	seen := map[string]int{}
	cursor := ""
	for page := 0; page < 20; page++ {
		path := "/api/v1/resources?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var body struct {
			Items      []domain.Resource `json:"items"`
			Total      int               `json:"total"`
			NextCursor string            `json:"nextCursor"`
		}
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &body) != nil {
			t.Fatalf("page %d: status = %d, body = %s", page, recorder.Code, recorder.Body.String())
		}
		for _, item := range body.Items {
			seen[item.ID]++
		}
		if body.NextCursor == "" {
			break
		}
		cursor = body.NextCursor
	}
	if len(seen) != 9 {
		t.Fatalf("visited %d resources, want 9: %v", len(seen), seen)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("%s visited %d times", id, count)
		}
	}
}

func TestListingsLeaveOutExcludedTypes(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-1", Name: "node-01", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "vm-1", Name: "instance-01", Type: domain.ResourceVM})
	memory.UpsertResource(domain.Resource{ID: "process-1", Name: "sshd", Type: domain.ResourceProcess})
	memory.PutRelation(domain.Relation{ID: "rel-vm", SourceID: "node-1", TargetID: "vm-1", Type: "hosts"})
	memory.PutRelation(domain.Relation{ID: "rel-process", SourceID: "node-1", TargetID: "process-1", Type: "runs"})
	memory.PutInventory(domain.AgentInventory{AgentID: "agent-1", NodeID: "node-1", Data: map[string]any{"os": "linux", "processes": []any{map[string]any{"pid": 1.0}}}})
	handler := New(memory, "test-token", "").Handler()

	get := func(path string, into any) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), into) != nil {
			t.Fatalf("%s: status = %d, body = %s", path, recorder.Code, recorder.Body.String())
		}
	}
	var resources struct {
		Items []domain.Resource `json:"items"`
	}
	get("/api/v1/resources?excludeTypes=process", &resources)
	if len(resources.Items) != 2 {
		t.Fatalf("resources = %+v", resources.Items)
	}
	var relations struct {
		Items []domain.Relation `json:"items"`
	}
	get("/api/v1/relations?excludeTypes=process", &relations)
	if len(relations.Items) != 1 || relations.Items[0].ID != "rel-vm" {
		t.Fatalf("relations = %+v", relations.Items)
	}
	var inventories struct {
		Items []domain.AgentInventory `json:"items"`
	}
	get("/api/v1/inventories?summary=true", &inventories)
	if len(inventories.Items) != 1 || inventories.Items[0].Data["processes"] != nil || inventories.Items[0].Data["os"] != "linux" {
		t.Fatalf("summary = %+v", inventories.Items)
	}
	get("/api/v1/inventories", &inventories)
	if inventories.Items[0].Data["processes"] == nil {
		t.Fatal("the full report lost its processes to the summary")
	}
}

// The heatmap draws machines; a fleet of processes must not crowd them out
// of the capped cell list.
func TestOverviewCellsKeepMachinesUnderTheCap(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-1", Name: "node-01", Type: domain.ResourceNode})
	for index := range 5 {
		memory.UpsertResource(domain.Resource{ID: fmt.Sprintf("process-%d", index), Name: fmt.Sprintf("a-%d", index), Type: domain.ResourceProcess})
	}
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview?cellLimit=2", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var body struct {
		Cells []struct {
			ID string `json:"id"`
		} `json:"cells"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &body) != nil || len(body.Cells) == 0 || body.Cells[0].ID != "node-1" {
		t.Fatalf("cells = %s", recorder.Body.String())
	}
}

func TestAccessBatchAnswersForTheCaller(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	body := `{"requests":[{"subjectId":"someone-else","resource":"resources","action":"read"},{"resource":"roles","action":"update"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate-batch", strings.NewReader(body))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var response struct {
		Items []struct {
			Allowed bool `json:"allowed"`
		} `json:"items"`
	}
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &response) != nil || len(response.Items) != 2 || !response.Items[0].Allowed {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
