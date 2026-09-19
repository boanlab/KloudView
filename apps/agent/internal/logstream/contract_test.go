package logstream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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

func readContract(t *testing.T) struct {
	AuthIdentifiers []string `json:"logStreamAuthIdentifiers"`
	ShipPriority    int      `json:"logStreamShipPriority"`
} {
	t.Helper()
	var contract struct {
		AuthIdentifiers []string `json:"logStreamAuthIdentifiers"`
		ShipPriority    int      `json:"logStreamShipPriority"`
	}
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

func TestShipPriorityMatchesTheContract(t *testing.T) {
	if contract := readContract(t); contract.ShipPriority != shipPriority {
		t.Fatalf("contract ship priority %d, agent ships at %d", contract.ShipPriority, shipPriority)
	}
}
