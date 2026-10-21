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

// An alias is the operator's name for a resource the agent names. The agent
// reports the VM again every few minutes; the alias has to outlast that.
func TestAliasSurvivesTheNextInventory(t *testing.T) {
	const credentialKey = "server-only-runtime-credential-key-123456"
	handler := New(store.NewMemory(), "test-token", "").WithAgentCredentialKey(credentialKey).Handler()
	call := func(method, path, body string, header func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		header(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	recorder := call(http.MethodPost, "/api/v1/agents/enroll", `{"token":"test-token","hostname":"node-02","version":"0.1.0","protocolVersion":"1"}`, func(*http.Request) {})
	var enrolled struct {
		Agent      domain.Agent `json:"agent"`
		Credential string       `json:"credential"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &enrolled) != nil {
		t.Fatalf("enroll = %s", recorder.Body.String())
	}
	asAgent := func(request *http.Request) { request.Header.Set("Authorization", "Bearer "+enrolled.Credential) }
	inventory := `{"hostname":"node-02","vms":[{"name":"vm-0042","state":"running"}]}`
	report := func() {
		t.Helper()
		if recorder := call(http.MethodPost, "/api/v1/agents/"+enrolled.Agent.ID+"/inventory", inventory, asAgent); recorder.Code != http.StatusOK {
			t.Fatalf("inventory = %d %s", recorder.Code, recorder.Body.String())
		}
	}
	report()

	recorder = call(http.MethodGet, "/api/v1/resources?type=vm", "", authorize)
	var listed struct {
		Items []domain.Resource `json:"items"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &listed) != nil || len(listed.Items) != 1 {
		t.Fatalf("vms = %s", recorder.Body.String())
	}
	vm := listed.Items[0]

	// The form sends every field; only the alias and the tags may land on an
	// agent's resource.
	body := `{"name":"renamed","alias":"  Billing DB  ","type":"node","health":"healthy","tags":{"owner":"storage"}}`
	if recorder := call(http.MethodPut, "/api/v1/resources/"+vm.ID, body, authorize); recorder.Code != http.StatusOK {
		t.Fatalf("update = %d %s", recorder.Code, recorder.Body.String())
	}
	report()

	recorder = call(http.MethodGet, "/api/v1/resources/"+vm.ID, "", authorize)
	var got domain.Resource
	if json.Unmarshal(recorder.Body.Bytes(), &got) != nil {
		t.Fatalf("get = %s", recorder.Body.String())
	}
	if got.Alias != "Billing DB" || got.Name != "vm-0042" || got.Type != domain.ResourceVM || got.Tags["owner"] != "storage" {
		t.Fatalf("after the next inventory: %+v", got)
	}

	recorder = call(http.MethodGet, "/api/v1/resources?q=billing", "", authorize)
	if json.Unmarshal(recorder.Body.Bytes(), &listed) != nil || len(listed.Items) != 1 {
		t.Fatalf("search by alias = %s", recorder.Body.String())
	}
	recorder = call(http.MethodGet, "/api/v1/overview?types=vm", "", authorize)
	if !strings.Contains(recorder.Body.String(), `"name":"Billing DB"`) {
		t.Fatalf("overview cell does not carry the alias: %s", recorder.Body.String())
	}
}
