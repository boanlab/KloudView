package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
