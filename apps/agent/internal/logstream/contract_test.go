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

type logContract struct {
	AuthIdentifiers []string `json:"logStreamAuthIdentifiers"`
	AlwaysShip      []string `json:"logStreamAlwaysShip"`
	ShipPriority    int      `json:"logStreamShipPriority"`
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

// The console tabs login activity by this list. A unit the agent treats as an
// access event but the contract omits lands in the wrong tab there, with
// nothing to catch it.
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

// Everything that crosses regardless of severity. The agent's set is the union
// of the two contract lists, because the console needs the access half on its
// own for the login tab while the agent needs both to decide what to ship.
func TestAlwaysShipMatchesTheContract(t *testing.T) {
	contract := readContract(t)
	expected := map[string]bool{}
	for _, name := range contract.AuthIdentifiers {
		expected[name] = true
	}
	for _, name := range contract.AlwaysShip {
		expected[name] = true
	}
	if len(expected) != len(alwaysShip) {
		t.Fatalf("contract names %d units that ship at any severity, agent has %d", len(expected), len(alwaysShip))
	}
	for name := range expected {
		if !alwaysShip[name] {
			t.Errorf("contract says %q ships at any severity but the agent does not", name)
		}
	}
}

// Severity alone is not a filter for importance. A host logs every sudo
// session and every account change at info, and most kernel lines -- process
// crashes among them -- sit below warning. If the floor ever rises above one
// of these without the unit being named, that evidence stops arriving and
// nothing says so.
func TestAccessAndKernelSurviveTheSeverityFloor(t *testing.T) {
	for _, unit := range []string{"sshd", "sudo", "useradd", "groupadd", "kernel"} {
		if !alwaysShip[unit] {
			t.Errorf("%q is subject to the severity floor; its lines are mostly below it", unit)
		}
	}
	if shipPriority > PriorityWarning {
		t.Errorf("ship priority %d is above warning; routine activity would stream again", shipPriority)
	}
}
