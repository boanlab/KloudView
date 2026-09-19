package logstream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// contractPath walks up to the repository root: the file is shared by two
// modules, so neither can hold it.
func contractPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		candidate := filepath.Join(dir, "docs", "contracts", "agent-server.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("agent-server.json not found above the working directory")
	return ""
}

type logContract struct {
	AuthIdentifiers []string `json:"logStreamAuthIdentifiers"`
	AlsoStreamed    []string `json:"logStreamAlsoStreamed"`
}

func readContract(t *testing.T) logContract {
	t.Helper()
	var contract logContract
	raw, err := os.ReadFile(contractPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	return contract
}

// The console groups streamed lines into system, login, and kernel activity
// using this list. A unit the agent treats as login activity but the contract
// omits lands in the wrong tab there, with nothing to catch it.
func TestAuthIdentifiersMatchTheContract(t *testing.T) {
	contract := readContract(t)
	if len(contract.AuthIdentifiers) != len(authIdentifiers) {
		t.Fatalf("contract lists %d auth identifiers, agent has %d", len(contract.AuthIdentifiers), len(authIdentifiers))
	}
	for _, name := range contract.AuthIdentifiers {
		if !authIdentifiers[name] {
			t.Fatalf("contract lists %q as login activity but the agent does not", name)
		}
	}
}

// The live view is an allowlist, and the two halves of it are listed
// separately because the console needs the access half on its own: a kernel
// line in the login view would be wrong.
func TestTheStreamedSendersMatchTheContract(t *testing.T) {
	contract := readContract(t)
	expected := map[string]bool{}
	for _, name := range contract.AuthIdentifiers {
		expected[name] = true
	}
	for _, name := range contract.AlsoStreamed {
		expected[name] = true
	}
	if len(expected) != len(streamed) {
		t.Fatalf("contract names %d streamed senders, agent has %d", len(expected), len(streamed))
	}
	for name := range expected {
		if !streamed[name] {
			t.Errorf("contract streams %q but the agent does not", name)
		}
	}
}

// Severity is not the filter, and must not quietly become one again. Every
// sender here logs mostly below warning -- sudo sessions at info, a kernel
// line saying a process crashed at info -- so a floor reintroduced anywhere
// would take the evidence with it.
func TestTheStreamDoesNotFilterOnSeverity(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	for _, unit := range []string{"sshd", "sudo", "useradd", "groupadd", "kernel"} {
		collector.Observe(journalJSON("7", "written at debug on purpose", unit))
	}
	batch := collector.Flush(time.Now().UTC())
	if len(batch.Lines) != 5 {
		t.Fatalf("shipped %d of 5 senders at debug: %+v", len(batch.Lines), batch.Lines)
	}
}
