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

// Empty work polls are not events: recorded, they push what people did out of
// the ring buffer.
func TestAnEmptyPollIsNotAuditable(t *testing.T) {
	cases := []struct {
		path   string
		status int
		want   bool
	}{
		{"/api/v1/agents/agent-1/terminal/claim", http.StatusNoContent, false},
		{"/api/v1/agents/agent-1/operations/claim", http.StatusNoContent, false},
		// A poll that handed out work is the start of something.
		{"/api/v1/agents/agent-1/terminal/claim", http.StatusOK, true},
		{"/api/v1/agents/agent-1/operations/claim", http.StatusOK, true},
		// Actions are always recorded.
		{"/api/v1/terminal-sessions/session-1/approve", http.StatusOK, true},
		{"/api/v1/resources", http.StatusCreated, true},
		{"/api/v1/resources/resource-1", http.StatusForbidden, true},
	}
	for _, test := range cases {
		request, err := http.NewRequest(http.MethodPost, test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := shouldAudit(request, test.status); got != test.want {
			t.Errorf("shouldAudit(POST %s, %d) = %v, want %v", test.path, test.status, got, test.want)
		}
	}
}

// An unauthenticated request is anonymous, not the fleet.
func TestAnUnauthenticatedRequestIsNotCalledAnAgent(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertAgent(domain.Agent{ID: "agent-1", Hostname: "node-1", NodeID: "node-1"})
	handler := NewWithAccess(memory, access.NewEngine(), "test-token", "").Handler()

	// A sign-in attempt from nobody.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"nobody","password":"wrong-password"}`)))

	events := memory.Audit()
	if len(events) == 0 {
		t.Fatal("the attempt was not recorded at all")
	}
	for _, event := range events {
		if event.Actor == "agent" {
			t.Errorf("an unauthenticated %s %s was recorded as an agent", event.Action, event.Target)
		}
	}
	if events[0].Actor != "anonymous" {
		t.Errorf("actor = %q, want anonymous", events[0].Actor)
	}

	// An agent path without a credential.
	memory2 := store.NewMemory()
	memory2.UpsertAgent(domain.Agent{ID: "agent-1", Hostname: "node-1", NodeID: "node-1"})
	handler2 := NewWithAccess(memory2, access.NewEngine(), "test-token", "").Handler()
	recorder2 := httptest.NewRecorder()
	handler2.ServeHTTP(recorder2, httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-1/terminal/claim", nil))
	for _, event := range memory2.Audit() {
		if event.Actor == "agent" {
			t.Errorf("a claim with no credential was recorded as an agent: %+v", event)
		}
	}
}
