package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The server half of docs/contracts/agent-server.json. The agent half is in
// apps/agent/internal/client/contract_test.go. Neither module can import the
// other, so the file is the only thing holding the two sides together.
type agentContract struct {
	ProtocolVersion   string   `json:"protocolVersion"`
	Capabilities      []string `json:"capabilities"`
	OperationTypes    []string `json:"operationTypes"`
	LogCaptureSources []string `json:"logCaptureSources"`
	TerminalMessages  []string `json:"terminalMessageTypes"`
}

// contractPath finds docs/contracts/agent-server.json by walking up from the
// working directory, which locates it under both the repository layout and the
// per-module Docker build context.
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

// Every capability the agent may advertise has to survive validation; a missing
// one fails the whole heartbeat.
func TestServerAcceptsEveryContractCapability(t *testing.T) {
	contract := loadContract(t)
	if len(contract.Capabilities) == 0 {
		t.Fatal("contract lists no capabilities")
	}
	// Each on its own, so a failure names the offending capability.
	for _, capability := range contract.Capabilities {
		if err := validateAgentMetadata("node-01", "0.2.4", contract.ProtocolVersion, []string{capability}, nil); err != nil {
			t.Errorf("capability %q rejected: %v", capability, err)
		}
	}
	// And all together, which is what a fully-featured agent actually sends.
	if err := validateAgentMetadata("node-01", "0.2.4", contract.ProtocolVersion, contract.Capabilities, nil); err != nil {
		t.Errorf("full capability set rejected: %v", err)
	}
}

// The allowlist must stay an allowlist.
func TestServerRejectsCapabilitiesOutsideTheContract(t *testing.T) {
	contract := loadContract(t)
	if err := validateAgentMetadata("node-01", "0.2.4", contract.ProtocolVersion, []string{"root-shell"}, nil); err == nil {
		t.Error("an unlisted capability was accepted")
	}
}

// A protocol bump on one side alone rejects every heartbeat from the other.
func TestServerProtocolVersionMatchesTheContract(t *testing.T) {
	if contract := loadContract(t); contract.ProtocolVersion != agentProtocolVersion {
		t.Fatalf("server speaks %q, contract says %q", agentProtocolVersion, contract.ProtocolVersion)
	}
}

// logCaptureSources is documented as mirroring the agent's list. A source the
// server accepts but the agent does not know is an operation that reaches a
// host and fails there; one the agent knows but the server refuses is a
// capture the console can never request.
func TestServerLogCaptureSourcesMatchTheContract(t *testing.T) {
	contract := loadContract(t)
	if len(contract.LogCaptureSources) != len(logCaptureSources) {
		t.Fatalf("server has %d sources, contract lists %d", len(logCaptureSources), len(contract.LogCaptureSources))
	}
	for _, source := range contract.LogCaptureSources {
		if !logCaptureSources[source] {
			t.Errorf("server does not accept log source %q", source)
		}
	}
}

// An operation the agent cannot execute is one the console should not offer:
// it would be claimed, fail on the host, and report back as an error.
func TestContractOperationTypesPassParameterValidation(t *testing.T) {
	contract := loadContract(t)
	if len(contract.OperationTypes) == 0 {
		t.Fatal("contract lists no operation types")
	}
	parameters := map[string]map[string]string{
		"service.status":  {"service": "containerd.service"},
		"service.restart": {"service": "containerd.service"},
		"logs.capture":    {"source": contract.LogCaptureSources[0]},
	}
	for _, operationType := range contract.OperationTypes {
		// Accepted for creation, or the console offers a button that 400s.
		if !supportedOperationTypes[operationType] {
			t.Errorf("operation %q is in the contract but not accepted for creation", operationType)
		}
		if err := validateOperationParameters(operationType, parameters[operationType]); err != nil {
			t.Errorf("operation %q rejected its own parameters: %v", operationType, err)
		}
	}
	if len(supportedOperationTypes) != len(contract.OperationTypes) {
		t.Errorf("server accepts %d operation types, contract lists %d",
			len(supportedOperationTypes), len(contract.OperationTypes))
	}
}

// The terminal WebSocket carries message types as bare strings on both sides of
// a module boundary neither can import across. A type the server sends that the
// agent does not know is silently dropped, and the operator sees a shell that
// ignores them -- so the names live in the contract and both sides check it.
func TestTerminalMessageTypesAreInTheContract(t *testing.T) {
	contract := loadContract(t)
	listed := map[string]bool{}
	for _, name := range contract.TerminalMessages {
		listed[name] = true
	}
	// Every type this side puts on the wire or reads off it.
	for _, name := range []string{"open", "input", "keys", "reply", "resize", "close", "output", "exit", "status", "error"} {
		if !listed[name] {
			t.Errorf("terminal message %q is not in docs/contracts/agent-server.json", name)
		}
	}
}
