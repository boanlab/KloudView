package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	expected := State{AgentID: "agent-01", NodeID: "node-01", Credential: "secret"}
	if err := Save(path, expected); err != nil {
		t.Fatal(err)
	}
	actual, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("state = %#v", actual)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}
