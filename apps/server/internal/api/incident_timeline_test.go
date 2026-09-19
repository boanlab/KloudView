package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/access"
	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func timelineFixture(t *testing.T) (*store.Memory, *Server, domain.Incident) {
	t.Helper()
	memory := store.NewMemory()
	now := time.Now().UTC()
	memory.UpsertResource(domain.Resource{ID: "node-1", Name: "node-1", Type: domain.ResourceNode, Health: domain.HealthCritical})
	alert := memory.PutAlert(domain.Alert{ID: "alert-1", Name: "CPU saturation", Severity: "critical", Status: "firing", ResourceID: "node-1", StartedAt: now.Add(-20 * time.Minute), UpdatedAt: now})
	// Declared after someone had already tried to fix it.
	incident := memory.PutIncident(domain.Incident{ID: "incident-1", Title: "node-1 saturated", Severity: "critical", Status: "investigating", ResourceIDs: []string{"node-1"}, AlertIDs: []string{alert.ID}})
	started, finished := now.Add(-10*time.Minute), now.Add(-9*time.Minute)
	memory.PutOperation(domain.Operation{
		ID: "operation-1", Type: "service.restart", Status: "succeeded", TargetIDs: []string{"node-1"},
		RequestedBy: "admin", ApprovedBy: "approver", StartedAt: &started, FinishedAt: &finished,
	})
	memory.PutTerminal(domain.TerminalSession{ID: "terminal-1", TargetID: "node-1", Status: "active", RequestedBy: "admin", CreatedAt: now.Add(-5 * time.Minute)})
	// What a person typed into the incident, which must still be there.
	memory.AddIncidentEvent(domain.IncidentEvent{ID: "incident-event-1", IncidentID: incident.ID, Type: "declared", Actor: "admin", Message: "Incident declared", CreatedAt: incident.CreatedAt})
	return memory, New(memory, "test-token", ""), incident
}

func readTimeline(t *testing.T, server *Server, incidentID string, subject string, scope ...string) []domain.IncidentEvent {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/incidents/"+incidentID+"/timeline", nil)
	request.Header.Set("X-KloudView-Subject", subject)
	if len(scope) > 0 {
		request.Header.Set("X-KloudView-Scope", scope[0])
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("timeline = %d %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Items []domain.IncidentEvent `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Items
}

func TestIncidentTimelineCarriesWhatWasActuallyDone(t *testing.T) {
	_, server, incident := timelineFixture(t)
	events := readTimeline(t, server, incident.ID, "admin")

	types := map[string]int{}
	for _, event := range events {
		types[event.Type]++
	}
	// The stored "declared" line, plus the action lines nobody typed in.
	for _, want := range []string{"declared", domain.EventAlert, domain.EventOperation, domain.EventApproval, domain.EventTerminal} {
		if types[want] == 0 {
			t.Fatalf("timeline has no %s entry: %+v", want, types)
		}
	}
	// Oldest first: the alert that started it comes before the incident record.
	if !events[0].CreatedAt.Before(events[len(events)-1].CreatedAt) {
		t.Fatalf("timeline is not in time order")
	}
	if events[0].Type != domain.EventAlert {
		t.Fatalf("first entry = %s, want the alert that started it", events[0].Type)
	}
	for _, event := range events {
		if event.Source != domain.EventRecorded && event.Source != domain.EventDerived {
			t.Fatalf("entry %s has no source", event.ID)
		}
		if event.Source == domain.EventDerived && event.Metadata["resourceId"] == "" {
			t.Fatalf("derived entry %s cannot be linked back: %+v", event.ID, event.Metadata)
		}
	}
}

func TestIncidentTimelineKeepsActionsTakenBeforeItWasDeclared(t *testing.T) {
	memory, server, incident := timelineFixture(t)
	// An operation run before the incident record existed, inside the window
	// the linked alert opens. Deriving lines rather than storing them is what
	// makes it appear at all.
	early := incident.CreatedAt.Add(-12 * time.Minute)
	memory.PutOperation(domain.Operation{ID: "operation-early", Type: "service.status", Status: "succeeded", TargetIDs: []string{"node-1"}, RequestedBy: "admin", FinishedAt: &early})
	found := false
	for _, event := range readTimeline(t, server, incident.ID, "admin") {
		if event.Metadata["operationId"] == "operation-early" {
			found = true
		}
	}
	if !found {
		t.Fatal("an action taken before the incident was declared is missing from its timeline")
	}
}

func TestIncidentTimelineStaysWithinTheCallersScope(t *testing.T) {
	memory, server, incident := timelineFixture(t)
	// Two resources on one incident, in two scopes.
	production := memory.PutGroup(domain.Group{ID: "group-production", Name: "production", Type: "environment", Path: "production"})
	secret := memory.PutGroup(domain.Group{ID: "group-secret", Name: "secret", Type: "environment", Path: "secret"})
	memory.PutMembership(domain.GroupMembership{ID: "membership-1", GroupID: production.ID, ResourceID: "node-1"})
	memory.UpsertResource(domain.Resource{ID: "node-secret", Name: "node-secret", Type: domain.ResourceNode})
	memory.PutMembership(domain.GroupMembership{ID: "membership-2", GroupID: secret.ID, ResourceID: "node-secret"})
	memory.PutOperation(domain.Operation{ID: "operation-secret", Type: "service.restart", Status: "succeeded", TargetIDs: []string{"node-secret"}, RequestedBy: "admin"})
	incident.ResourceIDs = []string{"node-1", "node-secret"}
	memory.PutIncident(incident)

	// A viewer who may read production and nothing else.
	server.access.PutRole(access.Role{ID: "role-view", Name: "view", Permissions: []access.Permission{{Resource: "*", Action: "read"}}})
	server.access.PutScope(access.Scope{ID: "scope-production", Name: "production", Paths: []string{"production"}})
	server.access.PutBinding(access.Binding{ID: "binding-view", SubjectID: "viewer", RoleID: "role-view", ScopeID: "scope-production"})

	// The incident itself is readable because one of its resources is in scope.
	events := readTimeline(t, server, incident.ID, "viewer", "production")
	if len(events) == 0 {
		t.Fatal("viewer should still see the part of the timeline it may read")
	}
	for _, event := range events {
		if event.Metadata["resourceId"] == "node-secret" {
			t.Fatalf("timeline leaked an action outside the caller's scope: %+v", event)
		}
	}
	// And the action is genuinely there for someone who may read it.
	seen := false
	for _, event := range readTimeline(t, server, incident.ID, "admin") {
		if event.Metadata["operationId"] == "operation-secret" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("admin should see the action the viewer was denied")
	}
}
