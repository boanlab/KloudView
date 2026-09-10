package api

import (
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func agentAt(id, version string, lastSeen, versionSince time.Time) domain.Agent {
	return domain.Agent{
		ID: "agent-" + id, NodeID: "node-" + id, Hostname: id,
		Version: version, LastSeenAt: lastSeen, VersionSince: versionSince,
	}
}

const soak = 10 * time.Minute

// With no canary, naming a target releases it to the whole fleet at once.
func TestNoCanaryReleasesTheFleetTogether(t *testing.T) {
	now := time.Now().UTC()
	fleet := []domain.Agent{
		agentAt("a", "0.2.3", now, now.Add(-time.Hour)),
		agentAt("b", "0.2.3", now, now.Add(-time.Hour)),
	}
	state := evaluateRollout(fleet, "0.2.4", "", soak, now)
	if !state.Released {
		t.Fatalf("state = %+v", state)
	}
	for _, agent := range fleet {
		if got := targetVersionFor(agent, state); got != "0.2.4" {
			t.Errorf("%s offered %q, want 0.2.4", agent.Hostname, got)
		}
	}
}

// The canary is offered the new build; nobody else is, so they compare equal to
// their own version and leave themselves alone.
func TestCanaryTakesTheBuildAloneWhileTheFleetHolds(t *testing.T) {
	now := time.Now().UTC()
	canary := agentAt("a", "0.2.3", now, now.Add(-time.Hour))
	rest := agentAt("b", "0.2.3", now, now.Add(-time.Hour))
	state := evaluateRollout([]domain.Agent{canary, rest}, "0.2.4", "a", soak, now)

	if state.Released {
		t.Fatalf("fleet released before the canary took the build: %+v", state)
	}
	if got := targetVersionFor(canary, state); got != "0.2.4" {
		t.Errorf("canary offered %q, want 0.2.4", got)
	}
	if got := targetVersionFor(rest, state); got != "0.2.3" {
		t.Errorf("fleet offered %q, want its own 0.2.3 so it does not update", got)
	}
}

// Reaching the new version is not enough; it has to stay up on it.
func TestFleetHoldsWhileTheCanaryIsStillSoaking(t *testing.T) {
	now := time.Now().UTC()
	canary := agentAt("a", "0.2.4", now, now.Add(-2*time.Minute))
	rest := agentAt("b", "0.2.3", now, now.Add(-time.Hour))
	state := evaluateRollout([]domain.Agent{canary, rest}, "0.2.4", "a", soak, now)

	if state.Released {
		t.Fatalf("released after only two minutes: %+v", state)
	}
	if state.SoakRemaining != "8m0s" {
		t.Errorf("soakRemaining = %q, want 8m0s", state.SoakRemaining)
	}
	if got := targetVersionFor(rest, state); got != "0.2.3" {
		t.Errorf("fleet offered %q during the soak", got)
	}
}

func TestFleetFollowsOnceTheCanaryHasSoaked(t *testing.T) {
	now := time.Now().UTC()
	canary := agentAt("a", "0.2.4", now, now.Add(-11*time.Minute))
	rest := agentAt("b", "0.2.3", now, now.Add(-time.Hour))
	state := evaluateRollout([]domain.Agent{canary, rest}, "0.2.4", "a", soak, now)

	if !state.Released {
		t.Fatalf("state = %+v", state)
	}
	if got := targetVersionFor(rest, state); got != "0.2.4" {
		t.Errorf("fleet offered %q after a clean soak", got)
	}
}

// The case the mechanism exists for: a build that stops the canary reporting.
func TestASilentCanaryHoldsTheFleet(t *testing.T) {
	now := time.Now().UTC()
	canary := agentAt("a", "0.2.4", now.Add(-5*time.Minute), now.Add(-11*time.Minute))
	rest := []domain.Agent{
		agentAt("b", "0.2.3", now, now.Add(-time.Hour)),
		agentAt("c", "0.2.3", now, now.Add(-time.Hour)),
		agentAt("d", "0.2.3", now, now.Add(-time.Hour)),
	}
	state := evaluateRollout(append([]domain.Agent{canary}, rest...), "0.2.4", "a", soak, now)

	if state.Released {
		t.Fatalf("a canary that stopped reporting released the fleet: %+v", state)
	}
	for _, agent := range rest {
		if got := targetVersionFor(agent, state); got != "0.2.3" {
			t.Errorf("%s offered %q while the canary was silent", agent.Hostname, got)
		}
	}
}

// A canary that never takes the build at all also holds, rather than timing out
// into a release.
func TestACanaryStuckOnTheOldVersionHoldsTheFleet(t *testing.T) {
	now := time.Now().UTC()
	canary := agentAt("a", "0.2.3", now, now.Add(-24*time.Hour))
	rest := agentAt("b", "0.2.3", now, now.Add(-24*time.Hour))
	state := evaluateRollout([]domain.Agent{canary, rest}, "0.2.4", "a", soak, now)

	if state.Released {
		t.Fatalf("state = %+v", state)
	}
	if got := targetVersionFor(rest, state); got != "0.2.3" {
		t.Errorf("fleet offered %q", got)
	}
}

// A typo in the canary name must stop updates, not wave them through.
func TestAnUnknownCanaryHoldsTheFleet(t *testing.T) {
	now := time.Now().UTC()
	fleet := []domain.Agent{agentAt("a", "0.2.3", now, now.Add(-time.Hour))}
	state := evaluateRollout(fleet, "0.2.4", "ryzen-typo", soak, now)

	if state.Released {
		t.Fatalf("an unenrolled canary released the fleet: %+v", state)
	}
	if got := targetVersionFor(fleet[0], state); got != "0.2.3" {
		t.Errorf("fleet offered %q", got)
	}
}

// No target means no update is on offer at all, canary or not.
func TestNoTargetOffersNothing(t *testing.T) {
	now := time.Now().UTC()
	agent := agentAt("a", "0.2.3", now, now.Add(-time.Hour))
	state := evaluateRollout([]domain.Agent{agent}, "", "a", soak, now)
	if state.Released {
		t.Fatal("released with no target configured")
	}
	if got := targetVersionFor(agent, state); got != "" {
		t.Errorf("offered %q with no target", got)
	}
}

// The operator should be able to name the node however the console shows it.
func TestCanaryMatchesAnyOfItsIdentifiers(t *testing.T) {
	now := time.Now().UTC()
	agent := agentAt("ryzen1", "0.2.4", now, now.Add(-time.Hour))
	for _, name := range []string{"ryzen1", "RYZEN1", "node-ryzen1", "agent-ryzen1", " ryzen1 "} {
		if !isCanary(agent, name) {
			t.Errorf("%q did not match the canary", name)
		}
	}
	for _, name := range []string{"", "ryzen", "ryzen11", "node-ryzen2"} {
		if isCanary(agent, name) {
			t.Errorf("%q matched the canary but should not have", name)
		}
	}
}

// Once the fleet has followed, the canary is not singled out again.
func TestSoakClockStartsAtTheVersionChangeNotTheHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	// Heartbeating constantly, but only two minutes on the new build.
	canary := agentAt("a", "0.2.4", now, now.Add(-2*time.Minute))
	if state := evaluateRollout([]domain.Agent{canary}, "0.2.4", "a", soak, now); state.Released {
		t.Fatal("a fresh version with a recent heartbeat must still soak")
	}
}
