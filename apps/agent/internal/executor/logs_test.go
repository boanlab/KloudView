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
		key := strings.Join(journalArgs(spec.dmesg, spec.facilities, spec.matches, "", "", "", 100), " ")
		if other, clash := seen[key]; clash {
			t.Fatalf("sources %q and %q read the same thing (%s); a source with no selector of its own returns the whole journal", other, name, key)
		}
		seen[key] = name
	}
}

func TestAuthSourceSelectsLoginFacilities(t *testing.T) {
	spec := logSources["auth"]
	args := strings.Join(journalArgs(spec.dmesg, spec.facilities, spec.matches, "", "", "", 100), " ")
	if !strings.Contains(args, "--facility=auth,authpriv") {
		t.Fatalf("auth args = %q, want the auth facilities rather than the whole journal", args)
	}
	// Debian writes auth.log, RHEL writes secure; both are tried in turn.
	if len(spec.files) != 2 {
		t.Fatalf("auth fallbacks = %v, want both distributions covered", spec.files)
	}
}

// A read is the only way to reach what the live view does not carry, and on a
// working host that is almost everything. Reading "everything" answers with an
// application's traffic and buries the host in it, so the two are separable.
func TestAReadCanSeparateTheHostFromItsApplications(t *testing.T) {
	host := strings.Join(journalArgs(false, logSources["host"].facilities, logSources["host"].matches, "", "", "", 100), " ")
	if !strings.Contains(host, "--facility=") || strings.Contains(host, "_COMM=") {
		t.Fatalf("host read = %q, want a facility selector", host)
	}
	container := strings.Join(journalArgs(false, logSources["container"].facilities, logSources["container"].matches, "", "", "", 100), " ")
	if !strings.HasSuffix(container, "_COMM=conmon") {
		t.Fatalf("container read = %q, want the monitor every container's output passes through", container)
	}
}

// Severity is something only a read can select on: the live view carries named
// senders rather than a severity range, so this is how "what went wrong" is
// asked of anything outside that list.
func TestAReadCanAskForASeverityBand(t *testing.T) {
	spec := logSources["journal"]
	args := strings.Join(journalArgs(spec.dmesg, spec.facilities, spec.matches, logPriorities["error"], "", "", 100), " ")
	if !strings.Contains(args, "--priority=0..3") {
		t.Fatalf("error read = %q", args)
	}
	if _, err := (&Executor{}).CaptureLogs("journal", "", "", "urgent-ish", 10); err == nil {
		t.Fatal("an unknown severity band was accepted")
	}
}

// Field matches are positional and journalctl requires them after every
// option; put one in the middle and it reads as a filename.
func TestFieldMatchesComeLast(t *testing.T) {
	args := journalArgs(false, "daemon", []string{"_COMM=conmon"}, "0..3", "yesterday", "now", 50)
	if args[len(args)-1] != "_COMM=conmon" {
		t.Fatalf("args = %v, want the match last", args)
	}
}
