package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// A target with no matching build on disk is served to nobody, so the fleet can
// never reach it. Reporting only that no agent has taken it describes the
// symptom of a cause the server already knows, and sends an operator to look at
// hosts that are behaving correctly.
func TestARolloutSaysWhenNoBuildIsBeingServed(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"kloudview-agent-linux-amd64", "kloudview-agent-linux-arm64"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("binary"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("0.1.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The target the operator named is not the one that was built.
	server := New(store.NewMemory(), "test-token", "").WithAgentReleases(dir, "9.9.9")
	state := server.rollout(time.Now().UTC())
	if !state.Withheld {
		t.Error("a target with no matching build is not reported as withheld")
	}
	if state.Built != "0.1.8" {
		t.Errorf("built = %q, want the version on disk", state.Built)
	}

	// The same directory, with the target it was actually built for.
	agreed := New(store.NewMemory(), "test-token", "").WithAgentReleases(dir, "0.1.8")
	if agreedState := agreed.rollout(time.Now().UTC()); agreedState.Withheld {
		t.Errorf("a matching build is reported as withheld: %+v", agreedState)
	}

	// And with no target named at all there is nothing to disagree with.
	none := New(store.NewMemory(), "test-token", "").WithAgentReleases(dir, "")
	if noneState := none.rollout(time.Now().UTC()); noneState.Withheld {
		t.Errorf("no target is reported as withheld: %+v", noneState)
	}
}
