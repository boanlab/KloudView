package logstream

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func journalJSON(priority, message, identifier string) []byte {
	return []byte(`{"PRIORITY":"` + priority + `","MESSAGE":"` + message + `","SYSLOG_IDENTIFIER":"` + identifier + `","__REALTIME_TIMESTAMP":"1757000000000000"}`)
}

func TestEverythingIsCountedButOnlyTroubleIsShipped(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	collector.Observe(journalJSON("3", "disk read error", "kernel"))
	collector.Observe(journalJSON("4", "high memory", "systemd"))
	collector.Observe(journalJSON("5", "session opened", "cron"))
	collector.Observe(journalJSON("6", "started unit", "systemd"))
	collector.Observe(journalJSON("7", "socket poll returned", "systemd"))

	batch := collector.Flush(time.Now().UTC())
	// Every severity is counted, including the ones that stay on the host, so
	// the console can show how loud a node is without carrying the lines.
	if batch.Counters["err"] != 1 || batch.Counters["warning"] != 1 ||
		batch.Counters["notice"] != 1 || batch.Counters["info"] != 1 ||
		batch.Counters["debug"] != 1 {
		t.Fatalf("counters = %+v", batch.Counters)
	}
	// Routine activity is the overwhelming majority of what a host logs and
	// says nothing while it is going well, so it waits for a journal read.
	if len(batch.Lines) != 2 {
		t.Fatalf("shipped %d lines, want the error and the warning only: %+v", len(batch.Lines), batch.Lines)
	}
	for _, line := range batch.Lines {
		if line.Priority > PriorityWarning {
			t.Fatalf("routine line was streamed: %+v", line)
		}
	}
}

// Severity is set by whoever wrote the program, and most of them are careless
// about it: a host logs every sudo session at info, and a kernel line that
// says a process crashed comes through below warning. Both must cross anyway.
func TestAccessAndKernelCrossBelowTheFloor(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	collector.Observe(journalJSON("6", "session opened for user root by ubuntu", "sudo"))
	collector.Observe(journalJSON("6", "new group: name=deploy", "groupadd"))
	collector.Observe(journalJSON("6", "traps: fwupdmgr trap int3", "kernel"))
	collector.Observe(journalJSON("6", "Starting sysstat-collect.service", "systemd"))

	batch := collector.Flush(time.Now().UTC())
	shipped := map[string]bool{}
	for _, line := range batch.Lines {
		shipped[line.Unit] = true
	}
	for _, unit := range []string{"sudo", "groupadd", "kernel"} {
		if !shipped[unit] {
			t.Errorf("%q was withheld at info; its evidence never arrives live", unit)
		}
	}
	if shipped["systemd"] {
		t.Error("a routine service start was streamed; that is the volume this change exists to stop")
	}
}

// A container's own output arrives on the same journal under its name, so
// without this the console reads a container's error as a host service's.
func TestAContainersOutputSaysWhichContainer(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	collector.Observe([]byte(`{"PRIORITY":"3","MESSAGE":"upstream timed out","SYSLOG_IDENTIFIER":"kv-api","CONTAINER_NAME":"kv-api","__REALTIME_TIMESTAMP":"1757000000000000"}`))
	collector.Observe(journalJSON("3", "disk read error", "kernel"))

	batch := collector.Flush(time.Now().UTC())
	byUnit := map[string]Line{}
	for _, line := range batch.Lines {
		byUnit[line.Unit] = line
	}
	if byUnit["kv-api"].Container != "kv-api" {
		t.Errorf("container line = %+v, want the container named", byUnit["kv-api"])
	}
	if byUnit["kernel"].Container != "" {
		t.Errorf("a host line claimed a container: %+v", byUnit["kernel"])
	}
}

func TestCollectorShipsAuthBelowTheShipThreshold(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	collector.Observe(journalJSON("7", "Accepted publickey for boan", "sshd"))
	collector.Observe(journalJSON("7", "socket poll returned", "systemd"))

	batch := collector.Flush(time.Now().UTC())
	if len(batch.Lines) != 1 || batch.Lines[0].Unit != "sshd" {
		t.Fatalf("login activity is withheld at debug: %+v", batch.Lines)
	}
}

// The reason routine lines have a budget of their own: a host that chatters at
// info must not be able to fill the window and drop the errors behind it.
func TestRoutineChatterDoesNotCrowdOutWarnings(t *testing.T) {
	collector := NewCollector(Limits{MaxLines: 3, MaxRoutineLines: 2, MaxMessage: 100}, nil)
	// Routine lines from a unit that ships at any severity: these are the only
	// ones that can still crowd a window now that the floor is at warning.
	for i := range 20 {
		collector.Observe(journalJSON("6", "session opened "+strconv.Itoa(i), "sudo"))
	}
	collector.Observe(journalJSON("3", "disk read error", "kernel"))
	collector.Observe(journalJSON("4", "high memory", "systemd"))

	batch := collector.Flush(time.Now().UTC())
	severe := 0
	for _, line := range batch.Lines {
		if line.Priority <= PriorityWarning {
			severe++
		}
	}
	if severe != 2 {
		t.Fatalf("shipped %d severe lines after routine chatter, want both: %+v", severe, batch.Lines)
	}
	if batch.Dropped != 18 {
		t.Fatalf("dropped = %d, want the routine overflow counted", batch.Dropped)
	}
	// And the chatter spent the routine budget, not the one held for trouble.
	routine := 0
	for _, line := range batch.Lines {
		if line.Priority > PriorityWarning {
			routine++
		}
	}
	if routine != 2 {
		t.Fatalf("%d routine lines kept, want the routine cap of 2: %+v", routine, batch.Lines)
	}
}

func TestCollectorFoldsRepeatsAndCapsWindow(t *testing.T) {
	collector := NewCollector(Limits{MaxLines: 2, MaxMessage: 100}, nil)
	for range 50 {
		collector.Observe(journalJSON("4", "Failed password for invalid user admin", "sshd"))
	}
	collector.Observe(journalJSON("4", "second distinct message", "sshd"))
	collector.Observe(journalJSON("4", "third distinct message", "sshd"))

	batch := collector.Flush(time.Now().UTC())
	if len(batch.Lines) != 2 {
		t.Fatalf("cap not applied: %+v", batch.Lines)
	}
	if batch.Dropped != 1 {
		t.Fatalf("dropped = %d, want the line the cap refused", batch.Dropped)
	}
	// A brute-force burst costs one line and a count, not fifty lines.
	if batch.Lines[0].Repeat != 49 {
		t.Fatalf("repeat = %d", batch.Lines[0].Repeat)
	}
	if batch.Counters["warning"] != 52 {
		t.Fatalf("counters lost the capped lines: %+v", batch.Counters)
	}
}

func TestCollectorRedactsAndTruncates(t *testing.T) {
	collector := NewCollector(Limits{MaxLines: 10, MaxMessage: 20}, func(line string) string {
		return strings.ReplaceAll(line, "hunter2", "[REDACTED]")
	})
	collector.Observe(journalJSON("3", "auth failed password=hunter2", "sshd"))
	batch := collector.Flush(time.Now().UTC())
	if len(batch.Lines) != 1 {
		t.Fatalf("lines = %+v", batch.Lines)
	}
	if strings.Contains(batch.Lines[0].Message, "hunter2") {
		t.Fatalf("credential left the host: %q", batch.Lines[0].Message)
	}
	if len([]rune(batch.Lines[0].Message)) > 21 {
		t.Fatalf("message not truncated: %q", batch.Lines[0].Message)
	}
}

func TestFlushResetsTheWindow(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	collector.Observe(journalJSON("3", "first", "kernel"))
	first := collector.Flush(time.Now().UTC())
	second := collector.Flush(time.Now().UTC())
	if len(first.Lines) != 1 || len(second.Lines) != 0 {
		t.Fatalf("window not reset: %d then %d", len(first.Lines), len(second.Lines))
	}
	// An empty window still reports, because zero errors is a measurement.
	if second.Counters == nil {
		t.Fatal("empty window reported no counters")
	}
	if !second.From.Equal(first.To) {
		t.Fatalf("windows are not contiguous: %v then %v", first.To, second.From)
	}
}

func TestJournalArgsResumeFromACursor(t *testing.T) {
	fresh := strings.Join(journalArgs(""), " ")
	if !strings.Contains(fresh, "--since=now") || strings.Contains(fresh, "--after-cursor") {
		t.Fatalf("a first run should start at the present: %s", fresh)
	}
	resumed := strings.Join(journalArgs("s=abc;i=1;b=2"), " ")
	if !strings.Contains(resumed, "--after-cursor=s=abc;i=1;b=2") || strings.Contains(resumed, "--since=now") {
		t.Fatalf("a restart should resume from the cursor: %s", resumed)
	}
}

// The cursor addresses the last entry the window observed, including entries
// that were only counted. Resuming after it is what closes the restart gap
// without re-sending anything.
func TestBatchCarriesTheCursorOfTheLastEntryObserved(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	if collector.Cursor() != "" {
		t.Fatal("a collector reports a cursor before reading anything")
	}
	collector.Observe([]byte(`{"__CURSOR":"c1","PRIORITY":"3","MESSAGE":"shipped","SYSLOG_IDENTIFIER":"kernel"}`))
	// Counted only: below the ship threshold and not an auth identifier.
	collector.Observe([]byte(`{"__CURSOR":"c2","PRIORITY":"7","MESSAGE":"counted","SYSLOG_IDENTIFIER":"systemd"}`))

	batch := collector.Flush(time.Now().UTC())
	if batch.Cursor != "c2" {
		t.Fatalf("cursor = %q, want the last entry observed", batch.Cursor)
	}
	if len(batch.Lines) != 1 || batch.Counters["debug"] != 1 {
		t.Fatalf("batch = %+v", batch)
	}
	// A flush does not rewind the cursor; the next window continues from here.
	collector.Observe([]byte(`{"__CURSOR":"c3","PRIORITY":"4","MESSAGE":"next","SYSLOG_IDENTIFIER":"sshd"}`))
	if next := collector.Flush(time.Now().UTC()); next.Cursor != "c3" {
		t.Fatalf("cursor = %q after a second window", next.Cursor)
	}
}

func TestPendingTracksTheOpenWindow(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	if collector.Pending() != 0 {
		t.Fatal("a fresh collector reports pending lines")
	}
	collector.Observe(journalJSON("3", "disk read error", "kernel"))
	// A repeat folds into the line already held, so it is not a second one.
	collector.Observe(journalJSON("3", "disk read error", "kernel"))
	if collector.Pending() != 1 {
		t.Fatalf("pending = %d, want the one distinct line", collector.Pending())
	}
	// Debug is counted, never shipped, so it leaves nothing to report.
	collector.Observe(journalJSON("7", "socket poll returned", "systemd"))
	if collector.Pending() != 1 {
		t.Fatalf("pending = %d after a counted-only entry", collector.Pending())
	}
	collector.Flush(time.Now().UTC())
	if collector.Pending() != 0 {
		t.Fatalf("pending = %d after a flush", collector.Pending())
	}
}
