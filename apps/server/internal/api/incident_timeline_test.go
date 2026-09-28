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

func TestAGuestIncidentShowsWhatWasDoneOnItsHost(t *testing.T) {
	memory := store.NewMemory()
	server := New(memory, "test-token", "")
	now := time.Now().UTC()
	memory.UpsertResource(domain.Resource{ID: "node-1", Name: "node-1", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "container-1", Name: "api", Type: domain.ResourceContainer})
	// What the inventory writes when it discovers a guest.
	memory.PutRelation(domain.Relation{ID: "relation-1", SourceID: "node-1", TargetID: "container-1", Type: "runs"})
	incident := memory.PutIncident(domain.Incident{ID: "incident-1", Title: "api is down", Severity: "critical", Status: "investigating", ResourceIDs: []string{"container-1"}})
	// The work happens on the node: a guest runs no agent of its own.
	started, finished := now.Add(-2*time.Minute), now.Add(-time.Minute)
	memory.PutOperation(domain.Operation{ID: "operation-1", Type: "service.restart", Status: "succeeded", TargetIDs: []string{"node-1"}, RequestedBy: "admin", StartedAt: &started, FinishedAt: &finished})
	memory.PutTerminal(domain.TerminalSession{ID: "terminal-1", TargetID: "node-1", Status: "active", RequestedBy: "admin"})

	events := readTimeline(t, server, incident.ID, "admin")
	kinds := map[string]bool{}
	for _, event := range events {
		kinds[event.Type] = true
	}
	if !kinds[domain.EventOperation] || !kinds[domain.EventTerminal] {
		t.Fatalf("a guest incident showed nothing about its host: %+v", kinds)
	}
	// Unrelated hosts stay out: the relation is what ties them, not the clock.
	memory.UpsertResource(domain.Resource{ID: "node-2", Name: "node-2", Type: domain.ResourceNode})
	memory.PutOperation(domain.Operation{ID: "operation-elsewhere", Type: "service.restart", Status: "succeeded", TargetIDs: []string{"node-2"}, RequestedBy: "admin", FinishedAt: &finished})
	for _, event := range readTimeline(t, server, incident.ID, "admin") {
		if event.Metadata["resourceId"] == "node-2" {
			t.Fatalf("an unrelated host leaked into the timeline: %+v", event)
		}
	}
}

// A shell session is one entry with its steps inside, not a row per stage: a
// response that opens eight shells is eight lines, not twenty-four.
func TestAShellSessionIsOneEntryWithItsStepsInside(t *testing.T) {
	started := time.Date(2026, 9, 19, 1, 20, 0, 0, time.UTC)
	closed := started.Add(4 * time.Minute)
	session := domain.TerminalSession{
		ID: "terminal-1", TargetID: "node-01", Status: "closed",
		RequestedBy: "admin", ApprovedBy: "approver",
		CreatedAt: started.Add(-30 * time.Second), StartedAt: &started, ClosedAt: &closed,
	}

	events := terminalEvents("incident-1", session, time.Now().UTC(), 3)
	if len(events) != 1 {
		t.Fatalf("one session produced %d rows", len(events))
	}
	entry := events[0]
	// Stamped when it was asked for, so it holds its place in the story.
	if !entry.CreatedAt.Equal(session.CreatedAt) {
		t.Errorf("entry is stamped %v, want the moment it was requested", entry.CreatedAt)
	}
	for key, want := range map[string]string{
		"approvedBy": "approver",
		"heldFor":    "4m0s",
		"commands":   "3",
	} {
		if entry.Metadata[key] != want {
			t.Errorf("%s = %q, want %q", key, entry.Metadata[key], want)
		}
	}
	if entry.Metadata["closedAt"] == "" {
		t.Error("the close is not recorded inside the entry")
	}
}

// A session that is still open must not climb back to the top of the timeline
// every time the page refreshes, shouldering aside the notes someone wrote.
func TestAnOpenSessionKeepsItsPlace(t *testing.T) {
	started := time.Date(2026, 9, 19, 7, 0, 0, 0, time.UTC)
	session := domain.TerminalSession{
		ID: "terminal-2", TargetID: "node-01", Status: "active",
		RequestedBy: "admin", CreatedAt: started, StartedAt: &started,
	}

	first := terminalEvents("incident-1", session, started.Add(time.Minute), 0)
	later := terminalEvents("incident-1", session, started.Add(time.Hour), 0)
	if !first[0].CreatedAt.Equal(later[0].CreatedAt) {
		t.Fatalf("an open session re-dated itself: %v then %v", first[0].CreatedAt, later[0].CreatedAt)
	}
	if first[0].Metadata["closedAt"] != "" {
		t.Error("an open session reported a close")
	}
	// It still says how long it has been held, which is the reason to notice
	// it at all.
	if later[0].Metadata["heldFor"] != "1h0m0s" {
		t.Errorf("heldFor = %q", later[0].Metadata["heldFor"])
	}
}
