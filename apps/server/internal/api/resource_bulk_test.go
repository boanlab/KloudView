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

func bulkFleet(t *testing.T) (http.Handler, *store.Memory) {
	t.Helper()
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-a", Name: "a", Type: domain.ResourceNode, Tags: map[string]string{"owner": "ops", "env": "dev"}, Attributes: map[string]string{"address": "10.0.0.1"}})
	memory.UpsertResource(domain.Resource{ID: "node-b", Name: "b", Type: domain.ResourceNode})
	memory.PutGroup(domain.Group{ID: "grp-static", Name: "rack-7", Type: "rack"})
	memory.PutGroup(domain.Group{ID: "grp-dynamic", Name: "prod", Type: "env", Mode: "dynamic", Selector: map[string]string{"env": "prod"}})
	return New(memory, "test-token", "").Handler(), memory
}

func postBulk(handler http.Handler, body string) (int, map[string]json.RawMessage) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/resources/bulk", strings.NewReader(body))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	out := map[string]json.RawMessage{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &out)
	return recorder.Code, out
}

func TestBulkPatchesTagsAndKeepsTheRestOfTheResource(t *testing.T) {
	handler, memory := bulkFleet(t)
	code, out := postBulk(handler, `{"resourceIds":["node-a","node-b","node-gone"],"setTags":{"env":"prod"},"removeTags":["owner"],"addToGroupId":"grp-static"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d %v", code, out)
	}
	if string(out["updated"]) != `["node-a","node-b"]` || !strings.Contains(string(out["failed"]), `"node-gone"`) {
		t.Fatalf("updated = %s failed = %s", out["updated"], out["failed"])
	}
	a, _ := memory.Resource("node-a")
	if a.Tags["env"] != "prod" || a.Tags["owner"] != "" || a.Attributes["address"] != "10.0.0.1" || a.Name != "a" {
		t.Fatalf("node-a after bulk = %+v", a)
	}
	members := 0
	for _, membership := range memory.ListMemberships("grp-static") {
		if membership.ResourceID == "node-a" || membership.ResourceID == "node-b" {
			members++
		}
	}
	if members != 2 {
		t.Fatalf("rack-7 members = %d, want 2", members)
	}
}

func TestBulkRejectsMalformedRequests(t *testing.T) {
	handler, _ := bulkFleet(t)
	for body, want := range map[string]int{
		`{"resourceIds":[],"setTags":{"a":"b"}}`:                            http.StatusBadRequest,
		`{"resourceIds":["node-a","node-a"],"setTags":{"a":"b"}}`:           http.StatusBadRequest,
		`{"resourceIds":["node-a"]}`:                                        http.StatusBadRequest,
		`{"resourceIds":["node-a"],"setTags":{"a":" "}}`:                    http.StatusBadRequest,
		`{"resourceIds":["node-a"],"setTags":{"a":"b"},"removeTags":["a"]}`: http.StatusBadRequest,
		`{"resourceIds":["node-a"],"addToGroupId":"grp-missing"}`:           http.StatusBadRequest,
		`{"resourceIds":["node-a"],"addToGroupId":"grp-dynamic"}`:           http.StatusConflict,
	} {
		if code, out := postBulk(handler, body); code != want {
			t.Errorf("%s = %d %v, want %d", body, code, out, want)
		}
	}
}
