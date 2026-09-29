package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/access"
	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// Acting alone on something that waits for a second person is granted by name or
// not at all. A role holding every action still does not hold it, so an
// administrator is not quietly exempt from the separation everybody else has.
func TestActingAloneIsGrantedByNameOrNotAtAll(t *testing.T) {
	newServer := func(grants ...access.Permission) (http.Handler, *store.Memory) {
		memory := store.NewMemory()
		memory.UpsertAgent(domain.Agent{ID: "agent-1", Hostname: "node-1", NodeID: "node-1"})
		memory.UpsertResource(domain.Resource{ID: "node-1", Name: "node-1", Type: domain.ResourceNode, AgentID: "agent-1"})
		memory.PutRunbook(domain.Runbook{ID: "safe", Name: "safe", Risk: "high",
			Steps: []domain.RunbookStep{{Name: "look", Operation: "inventory.refresh"}}})
		memory.PutRunbook(domain.Runbook{ID: "restarts", Name: "restarts", Risk: "high",
			Steps: []domain.RunbookStep{{Name: "bounce", Operation: "service.restart", Parameters: map[string]string{"service": "cron"}}}})
		engine := access.NewEngine()
		engine.PutScope(access.Scope{ID: "everything", Paths: []string{"*"}})
		engine.PutRole(access.Role{ID: "boss", Permissions: append(
			[]access.Permission{{Resource: "*", Action: "*"}}, grants...)})
		engine.PutBinding(access.Binding{ID: "b", SubjectID: "solo", RoleID: "boss", ScopeID: "everything"})
		return NewWithAccess(memory, engine, "test-token", "").Handler(), memory
	}
	call := func(handler http.Handler, method, path, body string) (int, string) {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-KloudView-Subject", "solo")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	restart := `{"type":"service.restart","targetIds":["node-1"],"parameters":{"service":"cron"},"reason":"a reason"}`
	run := `{"targetIds":["node-1"],"reason":"a reason"}`

	// A wildcard carries everything except this.
	handler, _ := newServer()
	_, body := call(handler, http.MethodPost, "/api/v1/operations", restart)
	if code, body := call(handler, http.MethodPost, "/api/v1/operations/"+idFrom(t, body)+"/approve", ""); code != http.StatusConflict {
		t.Errorf("*:* let the requester approve their own operation: %d %s", code, body)
	}
	_, body = call(handler, http.MethodPost, "/api/v1/runbooks/safe/execute", run)
	if code, body := call(handler, http.MethodPost, "/api/v1/runbook-executions/"+idFrom(t, body)+"/approve", ""); code != http.StatusConflict {
		t.Errorf("*:* let the requester approve their own execution: %d %s", code, body)
	}

	// Named, it is held - and only for what was named.
	handler, _ = newServer(access.Permission{Resource: "operations", Action: "approve-self"})
	_, body = call(handler, http.MethodPost, "/api/v1/operations", restart)
	if code, body := call(handler, http.MethodPost, "/api/v1/operations/"+idFrom(t, body)+"/approve", ""); code != http.StatusOK {
		t.Errorf("an explicit operations:approve-self was refused: %d %s", code, body)
	}
	_, body = call(handler, http.MethodPost, "/api/v1/runbooks/safe/execute", run)
	if code, body := call(handler, http.MethodPost, "/api/v1/runbook-executions/"+idFrom(t, body)+"/approve", ""); code != http.StatusConflict {
		t.Errorf("operations:approve-self leaked into runbooks: %d %s", code, body)
	}

	// A runbook grant releases a runbook of its own.
	handler, _ = newServer(access.Permission{Resource: "runbooks", Action: "approve-self"})
	_, body = call(handler, http.MethodPost, "/api/v1/runbooks/safe/execute", run)
	if code, body := call(handler, http.MethodPost, "/api/v1/runbook-executions/"+idFrom(t, body)+"/approve", ""); code != http.StatusOK {
		t.Errorf("an explicit runbooks:approve-self was refused: %d %s", code, body)
	}

	// But it is not a way to restart a service nobody may restart alone: the
	// execution approves its own first operation, so the operation's grant is
	// required too.
	_, body = call(handler, http.MethodPost, "/api/v1/runbooks/restarts/execute", run)
	if code, body := call(handler, http.MethodPost, "/api/v1/runbook-executions/"+idFrom(t, body)+"/approve", ""); code != http.StatusConflict {
		t.Errorf("a high-risk runbook laundered a service restart: %d %s", code, body)
	}

	// With both, the same runbook goes through.
	handler, _ = newServer(
		access.Permission{Resource: "runbooks", Action: "approve-self"},
		access.Permission{Resource: "operations", Action: "approve-self"},
	)
	_, body = call(handler, http.MethodPost, "/api/v1/runbooks/restarts/execute", run)
	if code, body := call(handler, http.MethodPost, "/api/v1/runbook-executions/"+idFrom(t, body)+"/approve", ""); code != http.StatusOK {
		t.Errorf("both grants together were still refused: %d %s", code, body)
	}
}

func idFrom(t *testing.T, body string) string {
	t.Helper()
	const key = `"id":"`
	start := strings.Index(body, key)
	if start < 0 {
		t.Fatalf("no id in %s", body)
	}
	rest := body[start+len(key):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("unterminated id in %s", body)
	}
	return rest[:end]
}
