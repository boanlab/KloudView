package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

const (
	// An agent is considered silent once it misses this much of its heartbeat.
	agentSilenceAfter = 45 * time.Second
	// Agents that fall silent within this window of each other are reported as
	// one event: a rack losing power looks different from one node dying.
	silenceCorrelationWindow = 90 * time.Second
	connectivitySweepEvery   = 15 * time.Second
)

// WatchConnectivity records an alert when an agent stops reporting and resolves
// it when the agent returns. The transition is the only evidence available when
// a host is cut off or loses power, so it is captured with the last values seen.
func (s *Server) WatchConnectivity(ctx context.Context) {
	ticker := time.NewTicker(connectivitySweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepConnectivity(time.Now().UTC())
		}
	}
}

func (s *Server) sweepConnectivity(now time.Time) {
	silent := map[string]domain.Agent{}
	for _, agent := range s.store.ListAgents() {
		if now.Sub(agent.LastSeenAt) > agentSilenceAfter {
			silent[agent.ID] = agent
		}
	}

	s.alertMu.Lock()
	defer s.alertMu.Unlock()
	if s.silentAgents == nil {
		s.silentAgents = map[string]time.Time{}
	}

	// Returned: resolve the alert raised when it went quiet.
	for agentID, since := range s.silentAgents {
		if _, stillSilent := silent[agentID]; stillSilent {
			continue
		}
		delete(s.silentAgents, agentID)
		s.resolveConnectivityAlert(agentID, now.Sub(since))
	}

	// silentAgents is in-process state, so reconciliation against stored alerts
	// covers an alert raised before a restart: its agent returns, is not silent,
	// and would otherwise never be reconsidered.
	for _, alert := range s.store.ListAlerts() {
		agentID, ok := connectivityAlertAgent(alert)
		if !ok || alert.Status == "resolved" {
			continue
		}
		if _, stillSilent := silent[agentID]; stillSilent {
			continue
		}
		if _, tracked := s.silentAgents[agentID]; tracked {
			continue
		}
		s.resolveConnectivityAlert(agentID, now.Sub(alert.StartedAt))
	}

	for agentID, agent := range silent {
		if _, known := s.silentAgents[agentID]; known {
			continue
		}
		s.silentAgents[agentID] = agent.LastSeenAt
		s.raiseConnectivityAlert(agent, silent, now)
	}
}

// connectivityAlertAgent recovers the agent an offline alert was raised for.
// The ID is the only link back, since the alert points at the node resource.
func connectivityAlertAgent(alert domain.Alert) (string, bool) {
	rest, ok := strings.CutPrefix(alert.ID, "alert-offline-")
	if !ok {
		return "", false
	}
	// The suffix is the timestamp the alert was keyed with.
	cut := strings.LastIndex(rest, "-")
	if cut <= 0 {
		return "", false
	}
	return rest[:cut], true
}

// raiseConnectivityAlert records the disconnection with the last metrics the
// node reported and how many peers went silent at the same time.
func (s *Server) raiseConnectivityAlert(agent domain.Agent, silent map[string]domain.Agent, now time.Time) {
	together := 0
	for id, peer := range silent {
		if id == agent.ID {
			continue
		}
		if absDuration(peer.LastSeenAt.Sub(agent.LastSeenAt)) <= silenceCorrelationWindow {
			together++
		}
	}
	summary := fmt.Sprintf("last seen %s", agent.LastSeenAt.Format(time.RFC3339))
	if metric, ok := s.store.LatestMetrics()[agent.NodeID]; ok {
		summary += fmt.Sprintf(", final cpu %.1f%% memory %.1f%% disk %.1f%%",
			metric.CPU, metric.Memory, metric.Disk)
	}
	// The distinction that matters first: one node, or a shared dependency.
	if together > 0 {
		summary += fmt.Sprintf("; %d other nodes went silent within %s — suspect shared power or network",
			together, silenceCorrelationWindow)
	} else {
		summary += "; no other node went silent — suspect this host"
	}
	s.store.PutAlert(domain.Alert{
		ID:         fmt.Sprintf("alert-offline-%s-%d", agent.ID, agent.LastSeenAt.UnixNano()),
		Name:       "Agent stopped reporting: " + agent.Hostname,
		Severity:   "critical",
		Status:     "firing",
		ResourceID: agent.NodeID,
		Summary:    summary,
		StartedAt:  agent.LastSeenAt,
		UpdatedAt:  now,
	})
}

func (s *Server) resolveConnectivityAlert(agentID string, silentFor time.Duration) {
	prefix := fmt.Sprintf("alert-offline-%s-", agentID)
	now := time.Now().UTC()
	for _, alert := range s.store.ListAlerts() {
		if !strings.HasPrefix(alert.ID, prefix) || alert.Status == "resolved" {
			continue
		}
		alert.Status = "resolved"
		alert.UpdatedAt = now
		alert.ResolvedAt = &now
		alert.Summary += fmt.Sprintf("; reporting resumed after %s", silentFor.Round(time.Second))
		s.store.PutAlert(alert)
	}
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
