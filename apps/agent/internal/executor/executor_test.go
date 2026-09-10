package executor

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/kloudview/kloudview/apps/agent/internal/client"
)

// The service allowlist is what stands between the console and systemctl on a
// production host. Everything else about an operation is checked server-side,
// where a compromised or buggy server is exactly the thing being defended
// against, so it has to hold here too.
func TestServiceOperationsRefuseAnythingOffTheAllowlist(t *testing.T) {
	executor := New([]string{"containerd.service"})
	for _, operationType := range []string{"service.status", "service.restart"} {
		for _, service := range []string{
			"sshd.service",         // plausible, and not granted
			"",                     // omitted parameter
			"containerd",           // near miss: the allowlist is exact
			"containerd.service ",  // trailing space
			"containerd.service;x", // no shell is involved, but still not a match
		} {
			_, err := executor.Run(client.Operation{
				Type:       operationType,
				Parameters: map[string]string{"service": service},
			})
			if err == nil || err.Error() != "service not allowed" {
				t.Errorf("%s on %q: err = %v, want \"service not allowed\"", operationType, service, err)
			}
		}
	}
}

// An agent installed without any service grants can restart nothing at all.
func TestEmptyAllowlistGrantsNoService(t *testing.T) {
	executor := New(nil)
	_, err := executor.Run(client.Operation{
		Type:       "service.restart",
		Parameters: map[string]string{"service": "containerd.service"},
	})
	if err == nil || err.Error() != "service not allowed" {
		t.Fatalf("err = %v, want \"service not allowed\"", err)
	}
}

// The counterpart: a granted service must get past the guard. It then reaches
// systemctl, which is absent in the test container, so the test asserts on
// which failure occurred rather than on success.
func TestAllowlistedServiceReachesSystemctl(t *testing.T) {
	executor := New([]string{"containerd.service"})
	_, err := executor.Run(client.Operation{
		Type:       "service.status",
		Parameters: map[string]string{"service": "containerd.service"},
	})
	if err != nil && err.Error() == "service not allowed" {
		t.Fatal("an allowlisted service was refused by the guard")
	}
}

func TestUnknownOperationIsRefused(t *testing.T) {
	if _, err := New(nil).Run(client.Operation{Type: "rm.rf"}); err == nil || err.Error() != "operation not supported" {
		t.Fatalf("err = %v, want \"operation not supported\"", err)
	}
}

// lines comes off the wire, so every shape a caller can send has to land on a
// sane bound rather than on zero or a negative buffer.
func TestAtoiOrFallsBackOnAnythingUnusable(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  int
	}{
		{"500", 500},
		{"", 42},
		{"0", 42},
		{"-1", 42},
		{"abc", 42},
		{"1e6", 42},
		{" 7", 42},
	} {
		if got := atoiOr(testCase.value, 42); got != testCase.want {
			t.Errorf("atoiOr(%q) = %d, want %d", testCase.value, got, testCase.want)
		}
	}
}

// A privilege drop that cannot be performed must fail closed. Running the
// command as the agent's own identity instead would silently hand the session
// more privilege than the operator configured. Exercised through canDropTo so
// the rule is checked whatever the test process runs as.
func TestPrivilegeDropIsRefusedWithoutThePrivilegeToDoIt(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		euid  int
		uid   uint64
		allow bool
	}{
		{"root may become anyone", 0, 1000, true},
		{"root may become root", 0, 0, true},
		{"a user may stay themselves", 1000, 1000, true},
		{"a user may not become another", 1000, 1001, false},
		{"a user may not become root", 1000, 0, false},
		{"a user may not become a system account", 1000, 33, false},
	} {
		if got := canDropTo(testCase.euid, testCase.uid); got != testCase.allow {
			t.Errorf("%s: canDropTo(%d, %d) = %v, want %v", testCase.name, testCase.euid, testCase.uid, got, testCase.allow)
		}
	}
}

// No configured user means the command runs as the agent, which is the
// documented default and must not be reported as an error.
func TestTerminalCredentialIsAbsentWithoutAConfiguredUser(t *testing.T) {
	attr, env, err := New(nil).terminalCredential()
	if err != nil || attr != nil || env != nil {
		t.Fatalf("attr = %v, env = %v, err = %v; want all empty", attr, env, err)
	}
}

func TestTerminalCredentialRejectsAnUnknownUser(t *testing.T) {
	executor := New(nil).WithTerminalUser("kloudview-no-such-account-" + strconv.Itoa(os.Getpid()))
	if _, _, err := executor.terminalCredential(); err == nil {
		t.Fatal("an account that does not exist was accepted")
	}
}

// Capture output is bounded before it is sent, so one runaway log cannot use
// the whole message budget.
func TestRedactAndCapTruncatesBeyondTheLimit(t *testing.T) {
	line := strings.Repeat("x", 512) + "\n"
	oversized := strings.Repeat(line, (logMaxBytes/512)+16)
	capped := redactAndCap(oversized)
	if len(capped) > logMaxBytes+len("\n[truncated]")+512 {
		t.Fatalf("output not capped: %d bytes", len(capped))
	}
	if !strings.HasSuffix(capped, "[truncated]") {
		t.Error("a truncated capture must say so")
	}
	// Short input passes through, minus the trailing newline.
	if got := redactAndCap("one\ntwo\n"); got != "one\ntwo" {
		t.Errorf("redactAndCap(short) = %q", got)
	}
}
