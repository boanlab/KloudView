package inventory

import (
	"errors"
	"testing"
)

// A failure that costs nothing must not be announced. The containerd socket is
// root-owned with no group to join, so an unprivileged agent is always refused
// there; on a host where docker already listed the same containers that is not
// news, and a warning repeated at every start is how an operator learns to
// ignore the warnings that matter.
func TestOnlyTheFailuresThatCostSomethingAreReported(t *testing.T) {
	reportedFailuresMu.Lock()
	reportedFailures = map[string]bool{}
	reportedFailuresMu.Unlock()

	// A reading nothing else covers is worth a word.
	commandOutput("false")
	reportedFailuresMu.Lock()
	loud := reportedFailures["false"]
	reportedFailuresMu.Unlock()
	if !loud {
		t.Error("a failure that leaves the agent with nothing went unreported")
	}

	reportedFailuresMu.Lock()
	reportedFailures = map[string]bool{}
	reportedFailuresMu.Unlock()

	commandOutputQuiet("false")
	reportedFailuresMu.Lock()
	quiet := reportedFailures["false"]
	reportedFailuresMu.Unlock()
	if quiet {
		t.Error("a failure another source already covered was announced anyway")
	}
}

// Each command says so once. Collection repeats every beat, and the same line
// forever is noise rather than a signal.
func TestACommandIsReportedOnce(t *testing.T) {
	reportedFailuresMu.Lock()
	reportedFailures = map[string]bool{}
	reportedFailuresMu.Unlock()

	reportCommandFailure("thing", errors.New("first"))
	reportedFailuresMu.Lock()
	first := reportedFailures["thing"]
	reportedFailuresMu.Unlock()
	if !first {
		t.Fatal("the first failure was not recorded")
	}
	// Recording it again must not clear the mark that keeps it quiet.
	reportCommandFailure("thing", errors.New("second"))
	reportedFailuresMu.Lock()
	still := reportedFailures["thing"]
	reportedFailuresMu.Unlock()
	if !still {
		t.Error("the command would be reported again on the next beat")
	}
}

// A command that is not installed is not a failure: a host without a
// hypervisor or a container runtime is simply a host without one.
func TestAMissingCommandIsNotAFailure(t *testing.T) {
	reportedFailuresMu.Lock()
	reportedFailures = map[string]bool{}
	reportedFailuresMu.Unlock()

	if out := commandOutput("kloudview-no-such-command"); out != "" {
		t.Errorf("output = %q", out)
	}
	reportedFailuresMu.Lock()
	reported := len(reportedFailures)
	reportedFailuresMu.Unlock()
	if reported != 0 {
		t.Error("a command the host does not have was reported as a failure")
	}
}
