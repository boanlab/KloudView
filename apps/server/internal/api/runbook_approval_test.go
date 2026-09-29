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

// Separation of duties on a high-risk runbook, with the escape the terminal path
// already has. Enforced by identity alone it deadlocks: the only operator in a
// small deployment asks for the run and then cannot release it, so the most
// dangerous class of operation is the one that can never happen.
func TestAHighRiskRunbookNeedsASecondPersonUnlessYouAreAnAdministrator(t *testing.T) {
	setup := func() (http.Handler, *store.Memory) {
		memory := store.NewMemory()
		memory.UpsertAgent(domain.Agent{ID: "agent-1", Hostname: "node-1", NodeID: "node-1"})
		memory.UpsertResource(domain.Resource{ID: "node-1", Name: "node-1", Type: "node", AgentID: "agent-1"})
		memory.PutRunbook(domain.Runbook{
			ID: "runbook-1", Name: "restart everything", Risk: "high",
			Steps: []domain.RunbookStep{{Name: "refresh", Operation: "inventory.refresh"}},
		})
		engine := access.NewEngine()
		engine.PutScope(access.Scope{ID: "everything", Paths: []string{"*"}})
		engine.PutRole(access.Role{ID: "boss", Permissions: []access.Permission{{Resource: "*", Action: "*"}}})
		// An operator who may run and approve, but holds no approve-self grant.
		engine.PutRole(access.Role{ID: "hands", Permissions: []access.Permission{
			{Resource: "runbooks", Action: "read"}, {Resource: "runbooks", Action: "execute"},
			{Resource: "runbooks", Action: "approve"}, {Resource: "resources", Action: "read"},
		}})
		engine.PutBinding(access.Binding{ID: "b-boss", SubjectID: "admin", RoleID: "boss", ScopeID: "everything"})
		engine.PutBinding(access.Binding{ID: "b-one", SubjectID: "operator-one", RoleID: "hands", ScopeID: "everything"})
		engine.PutBinding(access.Binding{ID: "b-two", SubjectID: "operator-two", RoleID: "hands", ScopeID: "everything"})
		return NewWithAccess(memory, engine, "test-token", "").Handler(), memory
	}
	call := func(handler http.Handler, subject, method, path, body string) (int, string) {
		var reader *strings.Reader
		if body == "" {
			reader = strings.NewReader("")
		} else {
			reader = strings.NewReader(body)
		}
		request := httptest.NewRequest(method, path, reader)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-KloudView-Subject", subject)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	execute := `{"targetIds":["node-1"],"reason":"a reason"}`

	// An ordinary operator still needs a colleague.
	handler, _ := setup()
	code, body := call(handler, "operator-one", http.MethodPost, "/api/v1/runbooks/runbook-1/execute", execute)
	if code != http.StatusCreated {
		t.Fatalf("execute = %d %s", code, body)
	}
	if !strings.Contains(body, "awaiting_approval") {
		t.Errorf("a high-risk runbook ran without approval: %s", body)
	}
	id := executionIDFrom(t, body)
	if code, body := call(handler, "operator-one", http.MethodPost, "/api/v1/runbook-executions/"+id+"/approve", ""); code != http.StatusConflict {
		t.Errorf("the requester approved their own run: %d %s", code, body)
	}
	if code, body := call(handler, "operator-two", http.MethodPost, "/api/v1/runbook-executions/"+id+"/approve", ""); code != http.StatusOK {
		t.Errorf("a colleague could not approve it: %d %s", code, body)
	}

	// An administrator holds approve-self through *:*, so a deployment with one
	// operator is not stuck.
	handler2, _ := setup()
	code, body = call(handler2, "admin", http.MethodPost, "/api/v1/runbooks/runbook-1/execute", execute)
	if code != http.StatusCreated {
		t.Fatalf("execute as admin = %d %s", code, body)
	}
	adminID := executionIDFrom(t, body)
	if code, body := call(handler2, "admin", http.MethodPost, "/api/v1/runbook-executions/"+adminID+"/approve", ""); code != http.StatusOK {
		t.Errorf("an administrator could not release their own run: %d %s", code, body)
	}
}

func executionIDFrom(t *testing.T, body string) string {
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
