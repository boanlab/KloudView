package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func TestResourceSearchFindsAHostByAddress(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-web-1", Name: "web-1", Type: domain.ResourceNode, Attributes: map[string]string{"address": "10.2.0.12", "addresses": "10.2.0.12, 192.168.7.4", "hostname": "web-1.prod.internal"}})
	memory.UpsertResource(domain.Resource{ID: "node-web-2", Name: "web-2", Type: domain.ResourceNode, Attributes: map[string]string{"address": "10.2.0.13", "kernel": "6.8.0-10.2.0.12"}})
	handler := New(memory, "test-token", "").Handler()
	for _, test := range []struct {
		query string
		want  string
	}{
		{"10.2.0.12", "web-1"},
		{"192.168.7.4", "web-1"},
		{"prod.internal", "web-1"},
		{"10.2.0.13", "web-2"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/resources?q="+test.query, nil)
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var body struct {
			Items []domain.Resource `json:"items"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Items) != 1 || body.Items[0].Name != test.want {
			names := []string{}
			for _, item := range body.Items {
				names = append(names, item.Name)
			}
			t.Errorf("q=%s matched %v, want only %s", test.query, names, test.want)
		}
	}
}
