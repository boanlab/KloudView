package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureFileReturnsTheTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syslog")
	var builder strings.Builder
	for i := 0; i < 100; i++ {
		builder.WriteString("line ")
		builder.WriteString(strings.Repeat("x", 3))
		builder.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureFile(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(out, "\n")); got != 10 {
		t.Fatalf("lines = %d, want the last 10", got)
	}
}

func TestCaptureLogsRejectsAnUnknownSource(t *testing.T) {
	executor := &Executor{}
	if _, err := executor.CaptureLogs("../../etc/shadow", "", "", "", 10); err == nil {
		t.Fatal("an arbitrary path was accepted as a log source")
	}
}

func TestEveryLogSourceSelectsSomethingDifferent(t *testing.T) {
	seen := map[string]string{}
	for name, spec := range logSources {
		key := strings.Join(journalArgs(spec.dmesg, spec.facilities, "", "", "", 100), " ")
		if other, clash := seen[key]; clash {
			t.Fatalf("sources %q and %q read the same thing (%s); a source with no selector of its own returns the whole journal", other, name, key)
		}
		seen[key] = name
	}
}

func TestAuthSourceSelectsLoginFacilities(t *testing.T) {
	spec := logSources["auth"]
	args := strings.Join(journalArgs(spec.dmesg, spec.facilities, "", "", "", 100), " ")
	if !strings.Contains(args, "--facility=auth,authpriv") {
		t.Fatalf("auth args = %q, want the auth facilities rather than the whole journal", args)
	}
	// Debian writes auth.log, RHEL writes secure; both are tried in turn.
	if len(spec.files) != 2 {
		t.Fatalf("auth fallbacks = %v, want both distributions covered", spec.files)
	}
}

// The stream carries warning and worse; everything below it stays on the host.
// A read that cannot ask for that band leaves it unreachable, which would make
// the severity floor a deletion rather than a deferral.
func TestAReadCanAskForWhatTheStreamLeavesBehind(t *testing.T) {
	spec := logSources["journal"]
	args := strings.Join(journalArgs(spec.dmesg, spec.facilities, logPriorities["routine"], "", "", 100), " ")
	if !strings.Contains(args, "--priority=5..7") {
		t.Fatalf("routine read = %q, want the notice-to-debug band", args)
	}
	errors := strings.Join(journalArgs(spec.dmesg, spec.facilities, logPriorities["error"], "", "", 100), " ")
	if !strings.Contains(errors, "--priority=0..3") {
		t.Fatalf("error read = %q", errors)
	}
	// An unnamed band is not a silent "everything"; it is refused.
	if _, err := (&Executor{}).CaptureLogs("journal", "", "", "urgent-ish", 10); err == nil {
		t.Fatal("an unknown severity was accepted")
	}
}

// Narrowing by severity is a journald feature. A plain log file has no
// severity field, so answering from one would return everything while
// appearing to have filtered.
func TestASeverityReadIsNotAnsweredFromAPlainFile(t *testing.T) {
	executor := &Executor{}
	_, err := executor.CaptureLogs("auth", "", "", "error", 10)
	if err == nil {
		return // journald answered it, which is the normal path
	}
	if !strings.Contains(err.Error(), "severity filter") {
		t.Fatalf("error = %v, want the reason the file fallback was refused", err)
	}
}
