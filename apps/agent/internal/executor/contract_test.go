package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kloudview/kloudview/apps/agent/internal/client"
)

// The executor's half of docs/contracts/agent-server.json. The server refuses
// an operation type or log source it does not recognise before it ever reaches
// a host, and this agent refuses one it does not implement after it arrives.
// Both lists have to say the same thing.
type executorContract struct {
	OperationTypes    []string `json:"operationTypes"`
	LogCaptureSources []string `json:"logCaptureSources"`
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

func loadContract(t *testing.T) executorContract {
	t.Helper()
	path := contractPath(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var contract executorContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	return contract
}

// An operation type the server can dispatch but this executor does not handle
// falls through to "operation not supported" -- claimed, sent to the host, and
// failed there, with the console reporting an error for a feature it offered.
func TestExecutorHandlesEveryContractOperationType(t *testing.T) {
	contract := loadContract(t)
	if len(contract.OperationTypes) == 0 {
		t.Fatal("contract lists no operation types")
	}
	executor := New(nil)
	for _, operationType := range contract.OperationTypes {
		// Run with parameters that fail their own checks: what matters is that
		// the type is recognised, not that it succeeds on this machine.
		_, err := executor.Run(client.Operation{Type: operationType})
		if err != nil && err.Error() == "operation not supported" {
			t.Errorf("operation %q is in the contract but the executor does not handle it", operationType)
		}
	}
	if _, err := executor.Run(client.Operation{Type: "definitely.not.real"}); err == nil || err.Error() != "operation not supported" {
		t.Errorf("an unlisted operation type should be refused, got %v", err)
	}
}

func TestExecutorLogSourcesMatchTheContract(t *testing.T) {
	contract := loadContract(t)
	if len(contract.LogCaptureSources) != len(logSources) {
		t.Fatalf("executor has %d sources, contract lists %d", len(logSources), len(contract.LogCaptureSources))
	}
	for _, source := range contract.LogCaptureSources {
		if _, ok := logSources[source]; !ok {
			t.Errorf("executor does not implement log source %q", source)
		}
	}
	for source := range logSources {
		if !slices.Contains(contract.LogCaptureSources, source) {
			t.Errorf("executor implements %q, which the contract does not list", source)
		}
	}
}
