package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// A timeline carries what was typed into the incident and what was done to its
// resources: operations run, shells opened, approvals given, alerts firing and
// clearing.
//
// The second half is derived when the timeline is read, not written when the
// action happens, which makes it retroactive. An incident is usually declared
// after the first attempts to fix it, and those attempts still appear; adding
// a resource later pulls in that resource's history too.

// timelineLeadIn extends the window back from the incident's own start, so the
// action taken a moment before someone declared it is not cut off.
const timelineLeadIn = 15 * time.Minute

func (s *Server) incidentTimeline(w http.ResponseWriter, r *http.Request) {
	incident, ok := s.store.Incident(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "incident not found")
		return
	}
	if !s.authorizeAnyTarget(r, "incidents", "read", incident.ResourceIDs) {
		writeError(w, http.StatusForbidden, "access_denied", "incident resource scope is not assigned")
		return
	}
	events := []domain.IncidentEvent{}
	for _, event := range s.store.IncidentEvents(incident.ID) {
		if event.Source == "" {
			event.Source = domain.EventRecorded
		}
		events = append(events, event)
	}
	events = append(events, s.derivedIncidentEvents(r, incident)...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].CreatedAt.Before(events[j].CreatedAt) })
	writeJSON(w, http.StatusOK, map[string]any{"items": events})
}

// derivedIncidentEvents reads the actions taken against the incident's
// resources during its life and turns them into timeline lines.
func (s *Server) derivedIncidentEvents(r *http.Request, incident domain.Incident) []domain.IncidentEvent {
	alerts := s.incidentAlerts(incident)
	// A guest runs no agent of its own, so the work done about it happens on
	// the node that runs it: the restart, the shell someone opened to look.
	// An incident about a container whose timeline only matched the container
	// showed nothing at all, however much was being done about it.
	resources := s.withHosts(incidentResources(incident, alerts))
	filter := store.ActivityFilter{
		ResourceIDs: resources,
		From:        timelineStart(incident, alerts),
		To:          timelineEnd(incident),
	}
	if len(filter.ResourceIDs) == 0 {
		// Nothing identifies what this incident is about, and an unbounded
		// filter would sweep in the whole fleet's activity.
		return nil
	}
	now := time.Now().UTC()
	events := []domain.IncidentEvent{}
	// Each row is checked against the caller's own scope. Without this the
	// incident page would report actions on resources the caller may not read.
	allowedOperation := s.resourceAuthorizer(r, "operations", "read")
	for _, operation := range s.store.OperationsTouching(filter) {
		if !s.anyTargetAllowed(r, "operations", "read", operation.TargetIDs, allowedOperation) {
			continue
		}
		events = append(events, operationEvents(incident.ID, operation, now)...)
	}
	allowedTerminal := s.resourceAuthorizer(r, "terminal", "read")
	for _, session := range s.store.TerminalsTouching(filter) {
		if !allowedTerminal(session.TargetID) {
			continue
		}
		events = append(events, terminalEvents(incident.ID, session, now, s.commandsTyped(session.ID))...)
	}
	allowedAlert := s.resourceAuthorizer(r, "alerts", "read")
	for _, alert := range alerts {
		if !allowedAlert(alert.ResourceID) {
			continue
		}
		events = append(events, alertEvents(incident.ID, alert)...)
	}
	return events
}

// withHosts adds the node behind every guest in the list, following the
// relation the inventory writes when it discovers the guest.
func (s *Server) withHosts(resourceIDs []string) []string {
	if len(resourceIDs) == 0 {
		return resourceIDs
	}
	wanted := map[string]bool{}
	for _, id := range resourceIDs {
		wanted[id] = true
	}
	hosts := []string{}
	for _, relation := range s.store.ListRelations() {
		if !wanted[relation.TargetID] || wanted[relation.SourceID] {
			continue
		}
		if relation.Type != "hosts" && relation.Type != "runs" {
			continue
		}
		wanted[relation.SourceID] = true
		hosts = append(hosts, relation.SourceID)
	}
	return append(resourceIDs, hosts...)
}

func (s *Server) incidentAlerts(incident domain.Incident) []domain.Alert {
	alerts := []domain.Alert{}
	for _, id := range incident.AlertIDs {
		if alert, ok := s.store.Alert(id); ok {
			alerts = append(alerts, alert)
		}
	}
	return alerts
}

// incidentResources is what the incident is about: the resources named on it
// plus the resource behind every alert linked to it.
func incidentResources(incident domain.Incident, alerts []domain.Alert) []string {
	seen := map[string]bool{}
	ids := []string{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, id := range incident.ResourceIDs {
		add(id)
	}
	for _, alert := range alerts {
		add(alert.ResourceID)
	}
	return ids
}

// timelineStart opens the window at whichever came first: the incident record
// or the earliest alert behind it, less a lead-in.
func timelineStart(incident domain.Incident, alerts []domain.Alert) time.Time {
	start := incident.CreatedAt
	for _, alert := range alerts {
		if alert.StartedAt.Before(start) {
			start = alert.StartedAt
		}
	}
	return start.Add(-timelineLeadIn)
}

func timelineEnd(incident domain.Incident) time.Time {
	if incident.ResolvedAt != nil {
		return *incident.ResolvedAt
	}
	return time.Now().UTC()
}

// derivedID is stable across reads so the console can key a row and link it to
// the record it came from.
func derivedID(kind, id, stage string) string {
	return fmt.Sprintf("derived:%s:%s:%s", kind, id, stage)
}

func derived(incidentID, kind, stage, id, actor, message string, at time.Time, metadata map[string]string) domain.IncidentEvent {
	return domain.IncidentEvent{
		ID:         derivedID(kind, id, stage),
		IncidentID: incidentID,
		Type:       kind,
		Actor:      actor,
		Message:    message,
		Metadata:   metadata,
		CreatedAt:  at,
		Source:     domain.EventDerived,
	}
}

// operationEvents splits one operation into the moments worth seeing: asked
// for, approved, and how it ended. An operation that started and has not
// finished stays visible as running, which is what stops two people restarting
// the same node.
// "still running" and "still open" describe the present, so they are stamped
// now and sort to the end rather than to the record's start.
// changesTheHost separates operations that alter a machine from ones that only
// look at it. A timeline leads with changes and keeps the checks behind them as
// evidence. An unknown type counts as a change: showing one that only looked is
// the cheaper mistake.
func changesTheHost(operationType string) bool {
	switch operationType {
	case "inventory.refresh", "service.status", "logs.capture":
		return false
	default:
		return true
	}
}

// operationEvents turns one operation into the moments worth seeing. An
// operation that ran and finished on its own is a single line: splitting it
// into "requested" and "succeeded" three seconds apart doubled the timeline
// without saying anything. It splits when something happened in between — an
// approval to record, or an execution that has not come back yet.
func operationEvents(incidentID string, operation domain.Operation, now time.Time) []domain.IncidentEvent {
	meta := map[string]string{"operationId": operation.ID, "status": operation.Status, "effect": "read"}
	if changesTheHost(operation.Type) {
		meta["effect"] = "change"
	}
	if len(operation.TargetIDs) > 0 {
		meta["resourceId"] = operation.TargetIDs[0]
	}
	targets := describeTargets(operation.TargetIDs)
	approved := operation.ApprovedBy != "" && operation.StartedAt != nil
	outcome := func() domain.IncidentEvent {
		message := fmt.Sprintf("%s %s on %s", operation.Type, operation.Status, targets)
		if operation.Error != "" {
			message += " — " + operation.Error
		}
		return derived(incidentID, domain.EventOperation, "finished", operation.ID, operation.RequestedBy,
			message, *operation.FinishedAt, meta)
	}
	if operation.FinishedAt != nil && !approved {
		return []domain.IncidentEvent{outcome()}
	}
	events := []domain.IncidentEvent{
		derived(incidentID, domain.EventOperation, "requested", operation.ID, operation.RequestedBy,
			fmt.Sprintf("%s requested on %s", operation.Type, targets), operation.CreatedAt, meta),
	}
	if approved {
		events = append(events, derived(incidentID, domain.EventApproval, "approved", operation.ID, operation.ApprovedBy,
			fmt.Sprintf("%s approved on %s", operation.Type, targets), *operation.StartedAt, meta))
	}
	switch {
	case operation.FinishedAt != nil:
		events = append(events, outcome())
	case operation.StartedAt != nil:
		events = append(events, derived(incidentID, domain.EventOperation, "running", operation.ID, operation.RequestedBy,
			fmt.Sprintf("%s still running on %s", operation.Type, targets), now, meta))
	}
	return events
}

// commandsTyped counts the commands sent as whole lines, so an entry can say
// how much was done without carrying the recording. Keystrokes are not
// recorded - a password at an unechoed prompt would be among them - so a
// session driven from the keyboard reports none rather than a wrong number.
func (s *Server) commandsTyped(sessionID string) int {
	recording, ok := s.store.TerminalRecording(sessionID, time.Now())
	if !ok {
		return 0
	}
	typed := 0
	for _, event := range recording.Events {
		if event.Direction == "input" || event.Direction == "blocked" {
			typed++
		}
	}
	return typed
}

// terminalEvents turns one shell session into one entry, its lifecycle as steps
// underneath. Stamped when the session was asked for, so a still-open one keeps
// its place instead of climbing on every poll.
func terminalEvents(incidentID string, session domain.TerminalSession, now time.Time, typed int) []domain.IncidentEvent {
	meta := map[string]string{
		"sessionId":  session.ID,
		"resourceId": session.TargetID,
		"status":     session.Status,
		// The steps, for the console to lay out beneath the entry.
		"requestedBy": session.RequestedBy,
		"requestedAt": session.CreatedAt.UTC().Format(time.RFC3339),
	}
	if session.ApprovedBy != "" && session.StartedAt != nil {
		meta["approvedBy"] = session.ApprovedBy
		meta["approvedAt"] = session.StartedAt.UTC().Format(time.RFC3339)
	}
	ended := now
	if session.ClosedAt != nil {
		meta["closedAt"] = session.ClosedAt.UTC().Format(time.RFC3339)
		ended = *session.ClosedAt
	}
	if started := session.StartedAt; started != nil && ended.After(*started) {
		meta["heldFor"] = ended.Sub(*started).Round(time.Second).String()
	}
	if typed > 0 {
		meta["commands"] = strconv.Itoa(typed)
	}
	message := "Shell session on " + session.TargetID
	if session.ClosedAt == nil {
		message += " — still open"
	}
	return []domain.IncidentEvent{
		derived(incidentID, domain.EventTerminal, "session", session.ID, session.RequestedBy,
			message, session.CreatedAt, meta),
	}
}

func alertEvents(incidentID string, alert domain.Alert) []domain.IncidentEvent {
	meta := map[string]string{"alertId": alert.ID, "resourceId": alert.ResourceID, "severity": alert.Severity}
	events := []domain.IncidentEvent{
		derived(incidentID, domain.EventAlert, "firing", alert.ID, alert.Severity,
			fmt.Sprintf("%s firing on %s", alert.Name, alert.ResourceID), alert.StartedAt, meta),
	}
	if alert.ResolvedAt != nil {
		events = append(events, derived(incidentID, domain.EventAlert, "resolved", alert.ID, alert.Severity,
			fmt.Sprintf("%s resolved on %s", alert.Name, alert.ResourceID), *alert.ResolvedAt, meta))
	}
	return events
}

func describeTargets(ids []string) string {
	switch len(ids) {
	case 0:
		return "no target"
	case 1:
		return ids[0]
	default:
		return fmt.Sprintf("%s and %d more", ids[0], len(ids)-1)
	}
}
