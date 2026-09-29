package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// A target no build matches is served to nobody, so the rollout reports the
// cause rather than only the symptom.
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

	// Target and built version disagree.
	server := New(store.NewMemory(), "test-token", "").WithAgentReleases(dir, "9.9.9")
	state := server.rollout(time.Now().UTC())
	if !state.Withheld {
		t.Error("a target with no matching build is not reported as withheld")
	}
	if state.Built != "0.1.8" {
		t.Errorf("built = %q, want the version on disk", state.Built)
	}

	// The same directory with its own target.
	agreed := New(store.NewMemory(), "test-token", "").WithAgentReleases(dir, "0.1.8")
	if agreedState := agreed.rollout(time.Now().UTC()); agreedState.Withheld {
		t.Errorf("a matching build is reported as withheld: %+v", agreedState)
	}

	// No target named.
	none := New(store.NewMemory(), "test-token", "").WithAgentReleases(dir, "")
	if noneState := none.rollout(time.Now().UTC()); noneState.Withheld {
		t.Errorf("no target is reported as withheld: %+v", noneState)
	}
}
