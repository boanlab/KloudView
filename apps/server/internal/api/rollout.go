package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// Staged rollout of agent builds. A named canary takes a new build first; the
// rest are offered the version they already run until it has held the new one
// for the soak period. Any canary failure holds the fleet, so the failure mode
// is "nothing updates" rather than "the bad build lands everywhere".
const defaultCanarySoak = 10 * time.Minute

type rolloutState struct {
	// Target is the version the operator named.
	Target string `json:"target"`
	// Canary is the node or host the new build goes to first; empty means
	// staging is off and every agent takes the target at once.
	Canary string `json:"canary,omitempty"`
	// Released reports whether the rest of the fleet may take the target.
	Released bool `json:"released"`
	// Reason states why, in the words the console and the logs should use.
	Reason string `json:"reason,omitempty"`
	// SoakRemaining counts down while the canary is proving the build.
	SoakRemaining string `json:"soakRemaining,omitempty"`
}

// isCanary matches on either identifier so an operator can name the node the
// way the console shows it.
func isCanary(agent domain.Agent, canary string) bool {
	canary = strings.TrimSpace(canary)
	if canary == "" {
		return false
	}
	return strings.EqualFold(agent.ID, canary) ||
		strings.EqualFold(agent.NodeID, canary) ||
		strings.EqualFold(agent.Hostname, canary)
}

// evaluateRollout decides whether the fleet may follow the canary onto target.
func evaluateRollout(agents []domain.Agent, target, canary string, soak time.Duration, now time.Time) rolloutState {
	state := rolloutState{Target: target, Canary: strings.TrimSpace(canary)}
	if target == "" {
		state.Reason = "no target version configured"
		return state
	}
	if state.Canary == "" {
		state.Released, state.Reason = true, "no canary configured; the fleet updates together"
		return state
	}
	for _, agent := range agents {
		if !isCanary(agent, state.Canary) {
			continue
		}
		if agent.Version != target {
			state.Reason = "canary " + agent.Hostname + " has not taken " + target + " yet"
			return state
		}
		if !agentOnline(agent, now) {
			state.Reason = "canary " + agent.Hostname + " is not reporting on " + target
			return state
		}
		// LastSeenAt only proves it is alive now; VersionSince is when this
		// version arrived.
		since := now.Sub(agent.VersionSince)
		if agent.VersionSince.IsZero() {
			state.Reason = "canary " + agent.Hostname + " soak has not started"
			return state
		}
		if since < soak {
			state.Reason = "canary " + agent.Hostname + " is soaking " + target
			state.SoakRemaining = (soak - since).Round(time.Second).String()
			return state
		}
		state.Released = true
		state.Reason = "canary " + agent.Hostname + " held " + target + " for " + soak.String()
		return state
	}
	// A canary was named and is not among the enrolled agents. Holding is the
	// safe reading: a typo stops updates rather than releasing them unchecked.
	state.Reason = "canary " + state.Canary + " is not enrolled; the fleet is held"
	return state
}

// targetVersionFor is the version this particular agent should be running. An
// agent that is not yet cleared returns its own version, which it compares
// equal to and therefore leaves alone.
func targetVersionFor(agent domain.Agent, state rolloutState) string {
	if state.Target == "" {
		return ""
	}
	if state.Released || isCanary(agent, state.Canary) {
		return state.Target
	}
	return agent.Version
}

// getRollout reports why the fleet is or is not taking the current target. A
// held rollout is invisible otherwise: agents simply keep their version, which
// looks identical to nothing having been released.
func (s *Server) getRollout(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.rollout(time.Now().UTC()))
}

// rollout evaluates the current staging state against the enrolled agents.
func (s *Server) rollout(now time.Time) rolloutState {
	soak := s.canarySoak
	if soak <= 0 {
		soak = defaultCanarySoak
	}
	return evaluateRollout(s.store.ListAgents(), s.agentReleases().Version, s.canary, soak, now)
}
