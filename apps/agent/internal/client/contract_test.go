package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The agent half of docs/contracts/agent-server.json. The server half is in
// apps/server/internal/api/contract_test.go. Neither module can import the
// other, so the file is the only thing holding the two sides together.
type agentContract struct {
	ProtocolVersion  string   `json:"protocolVersion"`
	Capabilities     []string `json:"capabilities"`
	TerminalMessages []string `json:"terminalMessageTypes"`
}

// contractPath finds docs/contracts/agent-server.json by walking up from the
// working directory. The Docker builds run these tests with only their own
// module copied in, so a fixed relative path resolves differently there than it
// does in the repository; both layouts put the file on the way up.
func contractPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for range 8 {
		candidate := filepath.Join(dir, "docs", "contracts", "agent-server.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Never skip: an unfindable contract means the two sides are unchecked.
	t.Fatal("docs/contracts/agent-server.json not found; the agent and server contract is unverified")
	return ""
}

func loadContract(t *testing.T) agentContract {
	t.Helper()
	path := contractPath(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var contract agentContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	return contract
}

// Whatever this agent can put on the wire has to be something the contract
// declares -- and therefore something the server was tested to accept. A
// capability added here and nowhere else fails every heartbeat.
func TestEveryAdvertisedCapabilityIsInTheContract(t *testing.T) {
	contract := loadContract(t)
	// All four combinations capabilities() can produce.
	for _, flags := range []struct{ terminal, logs bool }{
		{false, false}, {true, false}, {false, true}, {true, true},
	} {
		client := New("http://localhost", "token", "0.0.0", flags.terminal, flags.logs)
		for _, capability := range client.capabilities() {
			if !slices.Contains(contract.Capabilities, capability) {
				t.Errorf("terminal=%v logs=%v advertises %q, which the contract does not list",
					flags.terminal, flags.logs, capability)
			}
		}
	}
}

// The reverse direction: a capability declared and then never wired up is a
// server accepting something no agent sends, which hides a missed rollout.
func TestEveryContractCapabilityIsReachable(t *testing.T) {
	contract := loadContract(t)
	reachable := map[string]bool{}
	for _, flags := range []struct{ terminal, logs bool }{
		{false, false}, {true, false}, {false, true}, {true, true},
	} {
		for _, capability := range New("http://localhost", "token", "0.0.0", flags.terminal, flags.logs).capabilities() {
			reachable[capability] = true
		}
	}
	for _, capability := range contract.Capabilities {
		if !reachable[capability] {
			t.Errorf("contract lists %q, but no configuration of this agent advertises it", capability)
		}
	}
}

// A protocol bump on one side alone rejects every heartbeat from the other.
func TestAgentProtocolVersionMatchesTheContract(t *testing.T) {
	if contract := loadContract(t); contract.ProtocolVersion != protocolVersion {
		t.Fatalf("agent speaks %q, contract says %q", protocolVersion, contract.ProtocolVersion)
	}
}

// The terminal WebSocket carries message types as bare strings across a module
// boundary neither side can import. A type the server sends that the executor
// does not know is silently dropped -- the operator sees a shell that ignores
// their keys -- so the names live in the contract and both sides check it.
func TestTerminalMessageTypesAreInTheContract(t *testing.T) {
	contract := loadContract(t)
	// Every type the agent reads off the wire or puts on it.
	for _, name := range []string{"open", "input", "keys", "reply", "resize", "close", "output", "exit", "error"} {
		if !slices.Contains(contract.TerminalMessages, name) {
			t.Errorf("terminal message %q is not in docs/contracts/agent-server.json", name)
		}
	}
}
