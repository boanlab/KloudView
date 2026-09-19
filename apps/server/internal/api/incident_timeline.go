package api

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// An incident's timeline used to hold only what someone typed into it:
// declared, a status change, a note. What was actually done — operations run,
// shells opened, approvals given, alerts firing and clearing — was recorded in
// Task history, Audit and Alerts, and never reached the page people watch
// during a response. Nobody stops to write notes mid-incident, so the timeline
// was empty exactly when it was needed.
//
// These lines are therefore derived when the timeline is read, not written
// when the action happens. Deriving them is what makes the timeline
// retroactive: an incident is usually declared after the first few attempts to
// fix it, and those attempts still appear. Adding a resource to an incident
// later pulls in that resource's history too.

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
	filter := store.ActivityFilter{
		ResourceIDs: incidentResources(incident, alerts),
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
		events = append(events, terminalEvents(incident.ID, session, now)...)
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
// "still running" and "still open" describe the present, not a past moment, so
// they are stamped now and sort to the end. Stamping them with the record's
// start put a shell opened an hour earlier at the top of the timeline, which
// read as the beginning of the story rather than as something in progress.
func operationEvents(incidentID string, operation domain.Operation, now time.Time) []domain.IncidentEvent {
	meta := map[string]string{"operationId": operation.ID, "status": operation.Status}
	if len(operation.TargetIDs) > 0 {
		meta["resourceId"] = operation.TargetIDs[0]
	}
	targets := describeTargets(operation.TargetIDs)
	events := []domain.IncidentEvent{
		derived(incidentID, domain.EventOperation, "requested", operation.ID, operation.RequestedBy,
			fmt.Sprintf("%s requested on %s", operation.Type, targets), operation.CreatedAt, meta),
	}
	if operation.ApprovedBy != "" && operation.StartedAt != nil {
		events = append(events, derived(incidentID, domain.EventApproval, "approved", operation.ID, operation.ApprovedBy,
			fmt.Sprintf("%s approved on %s", operation.Type, targets), *operation.StartedAt, meta))
	}
	switch {
	case operation.FinishedAt != nil:
		message := fmt.Sprintf("%s %s on %s", operation.Type, operation.Status, targets)
		if operation.Error != "" {
			message += " — " + operation.Error
		}
		events = append(events, derived(incidentID, domain.EventOperation, "finished", operation.ID, operation.RequestedBy,
			message, *operation.FinishedAt, meta))
	case operation.StartedAt != nil:
		events = append(events, derived(incidentID, domain.EventOperation, "running", operation.ID, operation.RequestedBy,
			fmt.Sprintf("%s still running on %s", operation.Type, targets), now, meta))
	}
	return events
}

func terminalEvents(incidentID string, session domain.TerminalSession, now time.Time) []domain.IncidentEvent {
	meta := map[string]string{"sessionId": session.ID, "resourceId": session.TargetID, "status": session.Status}
	events := []domain.IncidentEvent{
		derived(incidentID, domain.EventTerminal, "opened", session.ID, session.RequestedBy,
			"Shell session requested on "+session.TargetID, session.CreatedAt, meta),
	}
	if session.ApprovedBy != "" && session.StartedAt != nil {
		events = append(events, derived(incidentID, domain.EventApproval, "session-approved", session.ID, session.ApprovedBy,
			"Shell session approved on "+session.TargetID, *session.StartedAt, meta))
	}
	if session.ClosedAt != nil {
		events = append(events, derived(incidentID, domain.EventTerminal, "closed", session.ID, session.RequestedBy,
			"Shell session closed on "+session.TargetID, *session.ClosedAt, meta))
	} else {
		events = append(events, derived(incidentID, domain.EventTerminal, "open", session.ID, session.RequestedBy,
			"Shell session still open on "+session.TargetID, now, meta))
	}
	return events
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
