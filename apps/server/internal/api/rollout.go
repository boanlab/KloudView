package api

import (
	"crypto/sha256"
	"encoding/binary"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// Staged rollout of agent builds. A named canary takes a new build first; the
// rest are offered the version they already run until it has held the new one
// for the soak period. Any canary failure holds the fleet, so the failure mode
// is "nothing updates" rather than "the bad build lands everywhere".
const defaultCanarySoak = 10 * time.Minute

// rolloutWindow is how long the fleet takes to be offered a released build.
// Each agent is given a fixed offset inside it, derived from its own id, so a
// hundred agents fetch a build across the window instead of in the same
// second. The canary is exempt: staging exists to put one machine on the build
// first.
const rolloutWindow = 10 * time.Minute

// canarySilence is how long the canary may go quiet before it is taken as
// proof the build broke it. Longer than the console's own threshold on
// purpose: the console reporting a node as quiet costs an operator a glance,
// whereas this holds every other agent on its old build, and a fleet large
// enough to queue behind one slow save would otherwise stall its own rollout
// on a beat that merely arrived late.
const canarySilence = 3 * time.Minute

// canaryReporting is agentOnline with that longer patience.
func canaryReporting(agent domain.Agent, now time.Time) bool {
	return !agent.LastSeenAt.IsZero() && !agent.LastSeenAt.Before(now.Add(-canarySilence))
}

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
	// ReleasedAt is when the fleet was cleared, and the point each agent's
	// offset into the rollout window is measured from. Absent until it is.
	ReleasedAt *time.Time `json:"releasedAt,omitempty"`
	// Total and OnTarget count the fleet against the target.
	Total    int `json:"total"`
	OnTarget int `json:"onTarget"`
	// Stalled names the agents whose turn in the window has passed and which
	// are still not running the target.
	Stalled []string `json:"stalled,omitempty"`
	// Withheld marks a target no build matches, which no agent can reach.
	Withheld bool `json:"withheld,omitempty"`
	// Built is the version the published binaries carry, when it disagrees.
	Built string `json:"built,omitempty"`
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

// evaluateRollout decides whether the fleet may follow the canary onto target,
// and reports how much of the fleet actually did.
func evaluateRollout(agents []domain.Agent, target, canary string, published time.Time, soak time.Duration, now time.Time) rolloutState {
	return withFleetProgress(decideRollout(agents, target, canary, published, soak, now), agents, now)
}

// withFleetProgress counts what came of the decision. Released says the fleet
// may take the build, not that any of it did: an agent installs one only where
// the host turned self-update on, so a rollout can report released, name a
// canary that held it, and go no further for hours. Nothing about the decision
// shows that. The count does.
func withFleetProgress(state rolloutState, agents []domain.Agent, now time.Time) rolloutState {
	if state.Target == "" {
		return state
	}
	for _, agent := range agents {
		state.Total++
		if agent.Version == state.Target {
			state.OnTarget++
			continue
		}
		// An agent still inside its offset is waiting by design. One whose turn
		// has passed was offered the build and did not take it.
		if state.Released && rolloutReached(agent, state, now) {
			name := agent.Hostname
			if name == "" {
				name = agent.ID
			}
			state.Stalled = append(state.Stalled, name)
		}
	}
	sort.Strings(state.Stalled)
	if len(state.Stalled) > 0 {
		state.Reason += "; " + strconv.Itoa(len(state.Stalled)) + " of " + strconv.Itoa(state.Total) +
			" have not taken " + state.Target + " and their turn has passed"
	}
	return state
}

// decideRollout answers whether the fleet may follow the canary onto target.
func decideRollout(agents []domain.Agent, target, canary string, published time.Time, soak time.Duration, now time.Time) rolloutState {
	state := rolloutState{Target: target, Canary: strings.TrimSpace(canary)}
	if target == "" {
		state.Reason = "no target version configured"
		return state
	}
	if state.Canary == "" {
		state.Released, state.Reason = true, "no canary configured; the fleet updates together"
		state.ReleasedAt = releasedAt(published, published)
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
		if !canaryReporting(agent, now) {
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
		state.ReleasedAt = releasedAt(agent.VersionSince.Add(soak), published)
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
func targetVersionFor(agent domain.Agent, state rolloutState, now time.Time) string {
	if state.Target == "" {
		return ""
	}
	if isCanary(agent, state.Canary) {
		return state.Target
	}
	if state.Released && rolloutReached(agent, state, now) {
		return state.Target
	}
	return agent.Version
}

// rolloutReached reports whether this agent's turn inside the rollout window
// has come. The offset is a hash of the agent id, so it is stable across
// heartbeats and restarts and needs nothing stored: an agent that was told to
// wait is told the same thing next beat, and is admitted at the same moment
// whichever server instance answers it.
func rolloutReached(agent domain.Agent, state rolloutState, now time.Time) bool {
	// Nothing to count from means nothing to wait for.
	if state.ReleasedAt == nil {
		return true
	}
	// A window shorter than the second the offsets are measured in leaves
	// nothing to spread over.
	span := uint64(rolloutWindow / time.Second)
	if span == 0 {
		return true
	}
	digest := sha256.Sum256([]byte(agent.ID))
	offset := time.Duration(binary.BigEndian.Uint64(digest[:8])%span) * time.Second
	return !now.Before(state.ReleasedAt.Add(offset))
}

// releasedAt is the later of the two instants that can clear a fleet, as a
// value the response omits when there is none.
func releasedAt(cleared, published time.Time) *time.Time {
	at := cleared
	if published.After(at) {
		at = published
	}
	if at.IsZero() {
		return nil
	}
	return &at
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
	manifest := s.agentReleases()
	state := evaluateRollout(s.store.ListAgents(), manifest.Version, s.canary, manifest.PublishedAt, soak, now)
	if manifest.Version != "" && manifest.Built != "" && manifest.Built != manifest.Version {
		state.Withheld = true
		state.Built = manifest.Built
	}
	return state
}
