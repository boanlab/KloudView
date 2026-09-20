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

func TestEverythingIsCountedAndOnlyNamedSendersAreStreamed(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	collector.Observe(journalJSON("3", "disk read error", "kernel"))
	collector.Observe(journalJSON("4", "high memory", "systemd"))
	collector.Observe(journalJSON("5", "session opened", "sudo"))
	collector.Observe(journalJSON("6", "started unit", "systemd"))
	collector.Observe(journalJSON("7", "socket poll returned", "systemd"))

	batch := collector.Flush(time.Now().UTC())
	// Counting is whole. It is what lets the console say how much is waiting
	// on the node without carrying any of it.
	if batch.Counters["err"] != 1 || batch.Counters["warning"] != 1 ||
		batch.Counters["notice"] != 1 || batch.Counters["info"] != 1 ||
		batch.Counters["debug"] != 1 {
		t.Fatalf("counters = %+v", batch.Counters)
	}
	// Streaming is not. Only the senders the live view is for cross, whatever
	// severity they were written at -- and systemd's warning does not, which
	// is the trade: the live view is access and the kernel, and everything
	// else is a read away.
	units := map[string]bool{}
	for _, line := range batch.Lines {
		units[line.Unit] = true
	}
	if len(batch.Lines) != 2 || !units["kernel"] || !units["sudo"] {
		t.Fatalf("streamed %+v, want the kernel line and the sudo session", batch.Lines)
	}
}

// Login activity is mostly written at notice and info, and a host that logged
// a session at debug would still be a host someone logged into.
func TestLoginActivityCrossesAtAnySeverity(t *testing.T) {
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
	// Routine lines from a sender the live view carries: these are the only
	// ones that can crowd a window now that nothing else streams.
	for i := range 20 {
		collector.Observe(journalJSON("6", "session opened "+strconv.Itoa(i), "sudo"))
	}
	collector.Observe(journalJSON("3", "disk read error", "kernel"))
	collector.Observe(journalJSON("4", "kernel: high memory", "kernel"))

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

// An application's output never reaches the live view, and it does not need a
// rule of its own to be kept out: it is simply not one of the senders the view
// is for. This is the volume the whole design turns on -- 41,105 of 41,178
// journal entries in twenty minutes were one container's access log.
func TestApplicationOutputIsNotStreamed(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	collector.Observe(journalJSON("6", "GET / HTTP/1.1 200", "kv-api"))
	// nginx writes its startup banner to stderr, which the container runtime
	// labels an error however plainly the text says "[notice]". Severity would
	// not have kept this out; naming the senders does.
	collector.Observe(journalJSON("3", "[notice] start worker process 24", "kv-web"))
	collector.Observe(journalJSON("6", "session opened for root", "sudo"))

	batch := collector.Flush(time.Now().UTC())
	if len(batch.Lines) != 1 || batch.Lines[0].Unit != "sudo" {
		t.Fatalf("streamed %+v, want the sudo session alone", batch.Lines)
	}
	// It is counted, though, so the console can say what is waiting to be read.
	if batch.Counters["info"] != 2 || batch.Counters["err"] != 1 {
		t.Fatalf("counters = %+v, want the whole journal's volume", batch.Counters)
	}
}

// What the live view keeps of a container failing, and what it does not.
//
// Killing a container writes three kinds of line: podman's own event, the
// scope systemd closes, and the network the kernel tears down. Only the last
// is a sender the live view carries, so a container dying shows up there as a
// network interface going away and nothing more. The rest is a read away, and
// the container's own health and metrics say it more directly than any log.
func TestOnlyTheKernelHalfOfAContainerFailureIsStreamed(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	collector.Observe(journalJSON("6", "container died f858cbab0b", "podman"))
	collector.Observe(journalJSON("6", "libpod-f858cb.scope: Consumed 30min CPU time.", "systemd"))
	collector.Observe(journalJSON("6", "podman1: port 2(veth1) entered disabled state", "kernel"))

	batch := collector.Flush(time.Now().UTC())
	if len(batch.Lines) != 1 || batch.Lines[0].Unit != "kernel" {
		t.Fatalf("streamed %+v, want the kernel line alone", batch.Lines)
	}
}

// The console's volume chips have to name numbers a read can reproduce.
//
// A single total is dominated by whichever application talks most: the live
// view showed "8,477 info" while a read of the host returned nineteen lines,
// because the two were counting different things. Counting them apart makes
// each chip answer to a read someone can actually make.
func TestHostAndContainerVolumeAreCountedApart(t *testing.T) {
	collector := NewCollector(DefaultLimits, nil)
	container := func(priority, message, name string) []byte {
		return []byte(`{"PRIORITY":"` + priority + `","MESSAGE":"` + message +
			`","SYSLOG_IDENTIFIER":"` + name + `","CONTAINER_NAME":"` + name +
			`","__REALTIME_TIMESTAMP":"1757000000000000"}`)
	}
	for i := range 50 {
		collector.Observe(container("6", "GET / HTTP/1.1 200 "+strconv.Itoa(i), "kv-api"))
	}
	collector.Observe(journalJSON("6", "Starting sysstat-collect.service", "systemd"))
	collector.Observe(journalJSON("3", "disk read error", "kernel"))

	batch := collector.Flush(time.Now().UTC())
	if batch.Counters["info"] != 1 || batch.Counters["err"] != 1 {
		t.Fatalf("host counters = %+v, want this host's own volume", batch.Counters)
	}
	if batch.Containers["info"] != 50 {
		t.Fatalf("container counters = %+v, want the application's volume", batch.Containers)
	}
	// And a container's output is still no part of the live view.
	if len(batch.Lines) != 1 || batch.Lines[0].Unit != "kernel" {
		t.Fatalf("streamed %+v, want the kernel line alone", batch.Lines)
	}
}
