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

func TestCollectorCountsEverythingAndShipsEverythingButDebug(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	collector.Observe(journalJSON("3", "disk read error", "kernel"))
	collector.Observe(journalJSON("4", "high memory", "systemd"))
	collector.Observe(journalJSON("5", "session opened", "cron"))
	collector.Observe(journalJSON("6", "started unit", "systemd"))
	collector.Observe(journalJSON("7", "socket poll returned", "systemd"))

	batch := collector.Flush(time.Now().UTC())
	// Every severity is counted, including debug, which is never shipped.
	if batch.Counters["err"] != 1 || batch.Counters["warning"] != 1 ||
		batch.Counters["notice"] != 1 || batch.Counters["info"] != 1 ||
		batch.Counters["debug"] != 1 {
		t.Fatalf("counters = %+v", batch.Counters)
	}
	if len(batch.Lines) != 4 {
		t.Fatalf("shipped %d lines, want everything but debug: %+v", len(batch.Lines), batch.Lines)
	}
	for _, line := range batch.Lines {
		if line.Priority > PriorityInfo {
			t.Fatalf("shipped a debug line: %+v", line)
		}
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
	for i := range 20 {
		collector.Observe(journalJSON("6", "routine "+strconv.Itoa(i), "systemd"))
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

// A container's output is the container's log. It arrives on the host's
// journal only because the runtime puts it there, and on a working host it is
// virtually all of it -- one web server's access log drowned every host event
// behind it and left every log tab showing the same thing.
func TestAContainersOutputIsNotTheHostsLog(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	container := func(priority, message, identifier, name string) []byte {
		return []byte(`{"PRIORITY":"` + priority + `","MESSAGE":"` + message +
			`","SYSLOG_IDENTIFIER":"` + identifier + `","CONTAINER_NAME":"` + name +
			`","__REALTIME_TIMESTAMP":"1757000000000000"}`)
	}
	collector.Observe(container("6", "GET / HTTP/1.1 200", "kv-api", "kv-api"))
	// nginx writes its startup banner to stderr, which the runtime labels an
	// error however plainly the text says otherwise. Severity would not have
	// saved us from this one; provenance does.
	collector.Observe(container("3", "[notice] start worker process 24", "kv-web", "kv-web"))
	collector.Observe(journalJSON("6", "started unit", "systemd"))

	batch := collector.Flush(time.Now().UTC())
	if len(batch.Lines) != 1 || batch.Lines[0].Unit != "systemd" {
		t.Fatalf("container output reached the host stream: %+v", batch.Lines)
	}
	// And it is not counted either: the counters say how loud this host is,
	// and a number dominated by output held elsewhere does not answer that.
	if batch.Counters["info"] != 1 || batch.Counters["err"] != 0 {
		t.Fatalf("counters = %+v, want the host's own volume only", batch.Counters)
	}
	// Nor is it a truncated window; nothing was dropped for want of room.
	if batch.Dropped != 0 {
		t.Fatalf("dropped = %d, want container output not counted as truncation", batch.Dropped)
	}
}

// What the host says about a container is the host talking, and it is exactly
// the news worth waking someone for. None of it carries CONTAINER_NAME.
func TestWhatTheHostSaysAboutAContainerStays(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	collector.Observe(journalJSON("6", "container died f858cbab0b", "podman"))
	collector.Observe(journalJSON("6", "libpod-f858cb.scope: Consumed 30min CPU time.", "systemd"))
	collector.Observe(journalJSON("6", "podman1: port 2(veth1) entered disabled state", "kernel"))

	batch := collector.Flush(time.Now().UTC())
	units := map[string]bool{}
	for _, line := range batch.Lines {
		units[line.Unit] = true
	}
	for _, unit := range []string{"podman", "systemd", "kernel"} {
		if !units[unit] {
			t.Errorf("%q was dropped; a container failing would be silent", unit)
		}
	}
}
