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

// The build has been on the server since before any of these cases start.
var published = time.Now().UTC().Add(-24 * time.Hour)

// With no canary, naming a target releases it to the whole fleet at once.
func TestNoCanaryReleasesTheFleetTogether(t *testing.T) {
	now := time.Now().UTC()
	fleet := []domain.Agent{
		agentAt("a", "0.2.3", now, now.Add(-time.Hour)),
		agentAt("b", "0.2.3", now, now.Add(-time.Hour)),
	}
	state := evaluateRollout(fleet, "0.2.4", "", published, soak, now)
	if !state.Released {
		t.Fatalf("state = %+v", state)
	}
	for _, agent := range fleet {
		if got := targetVersionFor(agent, state, now); got != "0.2.4" {
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
	state := evaluateRollout([]domain.Agent{canary, rest}, "0.2.4", "a", published, soak, now)

	if state.Released {
		t.Fatalf("fleet released before the canary took the build: %+v", state)
	}
	if got := targetVersionFor(canary, state, now); got != "0.2.4" {
		t.Errorf("canary offered %q, want 0.2.4", got)
	}
	if got := targetVersionFor(rest, state, now); got != "0.2.3" {
		t.Errorf("fleet offered %q, want its own 0.2.3 so it does not update", got)
	}
}

// Reaching the new version is not enough; it has to stay up on it.
func TestFleetHoldsWhileTheCanaryIsStillSoaking(t *testing.T) {
	now := time.Now().UTC()
	canary := agentAt("a", "0.2.4", now, now.Add(-2*time.Minute))
	rest := agentAt("b", "0.2.3", now, now.Add(-time.Hour))
	state := evaluateRollout([]domain.Agent{canary, rest}, "0.2.4", "a", published, soak, now)

	if state.Released {
		t.Fatalf("released after only two minutes: %+v", state)
	}
	if state.SoakRemaining != "8m0s" {
		t.Errorf("soakRemaining = %q, want 8m0s", state.SoakRemaining)
	}
	if got := targetVersionFor(rest, state, now); got != "0.2.3" {
		t.Errorf("fleet offered %q during the soak", got)
	}
}

func TestFleetFollowsOnceTheCanaryHasSoaked(t *testing.T) {
	now := time.Now().UTC()
	canary := agentAt("a", "0.2.4", now, now.Add(-11*time.Minute))
	rest := agentAt("b", "0.2.3", now, now.Add(-time.Hour))
	state := evaluateRollout([]domain.Agent{canary, rest}, "0.2.4", "a", published, soak, now)

	if !state.Released {
		t.Fatalf("state = %+v", state)
	}
	// Released, but each agent waits out its own offset, so the answer a
	// minute in depends on which agent is asking. By the end of the window
	// every one of them has been offered the build.
	if got := targetVersionFor(rest, state, now.Add(rolloutWindow)); got != "0.2.4" {
		t.Errorf("fleet offered %q after a clean soak and a full window", got)
	}
}

// A released build is handed out across the window rather than to everyone at
// once: a hundred agents fetching the same binary in the same second is the
// server's problem, not theirs.
func TestAReleasedFleetIsSpreadAcrossTheWindow(t *testing.T) {
	now := time.Now().UTC()
	fleet := []domain.Agent{agentAt("canary", "0.2.4", now, now.Add(-11*time.Minute))}
	for index := 0; index < 100; index++ {
		fleet = append(fleet, agentAt("n"+itoa(index), "0.2.3", now, now.Add(-time.Hour)))
	}
	state := evaluateRollout(fleet, "0.2.4", "canary", published, soak, now)
	if !state.Released || state.ReleasedAt == nil {
		t.Fatalf("state = %+v", state)
	}

	offered := func(at time.Time) int {
		count := 0
		for _, agent := range fleet[1:] {
			if targetVersionFor(agent, state, at) == "0.2.4" {
				count++
			}
		}
		return count
	}
	if immediate := offered(*state.ReleasedAt); immediate > 30 {
		t.Errorf("%d of 100 agents were offered the build in the first instant", immediate)
	}
	if half := offered(state.ReleasedAt.Add(rolloutWindow / 2)); half == 0 || half == 100 {
		t.Errorf("half way through the window %d of 100 had it; want a partial rollout", half)
	}
	if all := offered(state.ReleasedAt.Add(rolloutWindow)); all != 100 {
		t.Errorf("%d of 100 agents had the build after a full window, want all", all)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
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
	state := evaluateRollout(append([]domain.Agent{canary}, rest...), "0.2.4", "a", published, soak, now)

	if state.Released {
		t.Fatalf("a canary that stopped reporting released the fleet: %+v", state)
	}
	for _, agent := range rest {
		if got := targetVersionFor(agent, state, now); got != "0.2.3" {
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
	state := evaluateRollout([]domain.Agent{canary, rest}, "0.2.4", "a", published, soak, now)

	if state.Released {
		t.Fatalf("state = %+v", state)
	}
	if got := targetVersionFor(rest, state, now); got != "0.2.3" {
		t.Errorf("fleet offered %q", got)
	}
}

// A typo in the canary name must stop updates, not wave them through.
func TestAnUnknownCanaryHoldsTheFleet(t *testing.T) {
	now := time.Now().UTC()
	fleet := []domain.Agent{agentAt("a", "0.2.3", now, now.Add(-time.Hour))}
	state := evaluateRollout(fleet, "0.2.4", "ryzen-typo", published, soak, now)

	if state.Released {
		t.Fatalf("an unenrolled canary released the fleet: %+v", state)
	}
	if got := targetVersionFor(fleet[0], state, now); got != "0.2.3" {
		t.Errorf("fleet offered %q", got)
	}
}

// No target means no update is on offer at all, canary or not.
func TestNoTargetOffersNothing(t *testing.T) {
	now := time.Now().UTC()
	agent := agentAt("a", "0.2.3", now, now.Add(-time.Hour))
	state := evaluateRollout([]domain.Agent{agent}, "", "a", published, soak, now)
	if state.Released {
		t.Fatal("released with no target configured")
	}
	if got := targetVersionFor(agent, state, now); got != "" {
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
	if state := evaluateRollout([]domain.Agent{canary}, "0.2.4", "a", published, soak, now); state.Released {
		t.Fatal("a fresh version with a recent heartbeat must still soak")
	}
}

// Without a canary the fleet is cleared the moment the build is published, and
// the window still applies: "together" means nobody waits on a canary, not
// that a hundred agents fetch at once.
func TestAFreshBuildWithNoCanaryIsStillSpread(t *testing.T) {
	now := time.Now().UTC()
	fleet := []domain.Agent{}
	for index := 0; index < 100; index++ {
		fleet = append(fleet, agentAt("n"+itoa(index), "0.2.3", now, now.Add(-time.Hour)))
	}
	state := evaluateRollout(fleet, "0.2.4", "", now, soak, now)
	if !state.Released || state.ReleasedAt == nil {
		t.Fatalf("state = %+v", state)
	}
	offered := 0
	for _, agent := range fleet {
		if targetVersionFor(agent, state, now) == "0.2.4" {
			offered++
		}
	}
	if offered > 30 {
		t.Errorf("%d of 100 agents were offered a just-published build at once", offered)
	}
}

// A canary that was already running the version before it became the target
// has a soak that finished in the past. The window has to count from the
// publish, or every offset has elapsed before the fleet hears about it.
func TestAPreSoakedCanaryDoesNotCollapseTheWindow(t *testing.T) {
	now := time.Now().UTC()
	// On the build since last week; published as the target just now.
	canary := agentAt("canary", "0.2.4", now, now.Add(-7*24*time.Hour))
	fleet := []domain.Agent{canary}
	for index := 0; index < 100; index++ {
		fleet = append(fleet, agentAt("n"+itoa(index), "0.2.3", now, now.Add(-time.Hour)))
	}
	state := evaluateRollout(fleet, "0.2.4", "canary", now, soak, now)
	if !state.Released {
		t.Fatalf("state = %+v", state)
	}
	offered := 0
	for _, agent := range fleet[1:] {
		if targetVersionFor(agent, state, now) == "0.2.4" {
			offered++
		}
	}
	if offered > 30 {
		t.Errorf("%d of 100 agents were offered the build at once on a week-old soak", offered)
	}
}

// A beat that arrived late is not a broken build. The console calls this
// canary quiet; the rollout does not stall the fleet on it.
func TestACanaryThatMissedAFewBeatsDoesNotHoldTheFleet(t *testing.T) {
	now := time.Now().UTC()
	quiet := now.Add(-45 * time.Second)
	canary := agentAt("a", "0.2.4", quiet, now.Add(-11*time.Minute))
	rest := agentAt("b", "0.2.3", now, now.Add(-time.Hour))
	if agentOnline(canary, now) {
		t.Fatal("the console would still call this canary online; the case proves nothing")
	}
	state := evaluateRollout([]domain.Agent{canary, rest}, "0.2.4", "a", published, soak, now)
	if !state.Released {
		t.Fatalf("a single late beat held the fleet: %+v", state)
	}
}
