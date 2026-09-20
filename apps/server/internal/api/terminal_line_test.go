package api

import (
	"strings"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/store"
)

const prompt = "ubuntu@node:/tmp$ "

func echo(t *testing.T, parts ...string) *promptLine {
	t.Helper()
	line := newPromptLine()
	for _, part := range parts {
		line.feed([]byte(part))
	}
	return line
}

func TestThePromptLineIsWhatTheShellEchoed(t *testing.T) {
	// A shell echoes one character at a time as it is typed.
	line := echo(t, prompt)
	for _, ch := range "systemctl status cron" {
		line.feed([]byte(string(ch)))
	}
	got, alternate := line.snapshot()
	if alternate {
		t.Fatal("a plain prompt should not look like a full-screen program")
	}
	if got != prompt+"systemctl status cron" {
		t.Fatalf("line = %q", got)
	}
}

func TestBackspaceTakesACharacterBackOffTheLine(t *testing.T) {
	line := echo(t, prompt, "rm -rf /x")
	// readline rubs a character out by stepping back, printing a space and
	// stepping back again.
	line.feed([]byte("\b \b"))
	got, _ := line.snapshot()
	if got != prompt+"rm -rf /" {
		t.Fatalf("line = %q", got)
	}
}

// The case a keystroke buffer cannot see: the operator presses the up arrow
// and the shell writes a whole command onto the line that was never typed.
func TestACommandRecalledFromHistoryIsOnTheLine(t *testing.T) {
	line := echo(t, prompt)
	line.feed([]byte("\x1b[A"))          // the arrow itself echoes nothing
	line.feed([]byte("rm -rf /var/tmp")) // the shell writes the recalled command
	got, _ := line.snapshot()
	if !strings.Contains(got, "rm -rf /var/tmp") {
		t.Fatalf("a recalled command was not on the line: %q", got)
	}
}

func TestRubbingOutARecalledCommandClearsIt(t *testing.T) {
	// Pressing up twice: the shell returns to the start of the line, erases to
	// the end, and writes the older command.
	line := echo(t, prompt, "mkfs.ext4 /dev/sdb")
	line.feed([]byte("\r"))
	line.feed([]byte(prompt))
	line.feed([]byte("\x1b[K")) // erase from here to the end of the line
	line.feed([]byte("uptime"))
	got, _ := line.snapshot()
	if got != prompt+"uptime" {
		t.Fatalf("line = %q, want the older command alone", got)
	}
}

func TestAFullScreenProgramLeavesNoCommandLine(t *testing.T) {
	line := echo(t, prompt, "vi /etc/hosts")
	line.feed([]byte("\x1b[?1049h")) // vi takes the terminal
	line.feed([]byte("\x1b[5;1Hrm -rf / typed inside the editor"))
	_, alternate := line.snapshot()
	if !alternate {
		t.Fatal("the alternate screen was not noticed")
	}
	line.feed([]byte("\x1b[?1049l")) // and gives it back
	if _, alternate := line.snapshot(); alternate {
		t.Fatal("leaving the alternate screen was not noticed")
	}
}

func TestColourAndTitlesAreNotPartOfTheCommand(t *testing.T) {
	line := echo(t,
		"\x1b]0;ubuntu@node: /tmp\x07", // the window title
		"\x1b[32m", prompt, "\x1b[0m",  // a coloured prompt
		"uptime",
	)
	got, _ := line.snapshot()
	if got != prompt+"uptime" {
		t.Fatalf("line = %q", got)
	}
}

func TestTheDenyPolicyReachesPastThePrompt(t *testing.T) {
	server := New(store.NewMemory(), "test-token", "")
	// The patterns anchor on the start of a line, and the echo puts the prompt
	// in front of everything. Without stripping it, this would be allowed.
	refused := 0
	for _, candidate := range commandCandidates(prompt + "rm -rf /") {
		if !server.terminalCommandAllowed(candidate) {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("a destructive command behind a prompt was not refused")
	}
}

func TestQuotedTextIsStillAllowedBehindAPrompt(t *testing.T) {
	server := New(store.NewMemory(), "test-token", "")
	for _, candidate := range commandCandidates(prompt + `echo "rm -rf /"`) {
		if !server.terminalCommandAllowed(candidate) {
			t.Fatalf("a quoted mention was refused as a command: %q", candidate)
		}
	}
}

func TestASeparatorLateInTheLineIsStillJudged(t *testing.T) {
	server := New(store.NewMemory(), "test-token", "")
	refused := false
	for _, candidate := range commandCandidates(prompt + "cd /tmp ; mkfs.ext4 /dev/sdb") {
		if !server.terminalCommandAllowed(candidate) {
			refused = true
		}
	}
	if !refused {
		t.Fatal("a destructive command after a separator was not refused")
	}
}

func TestADeviceControlStringIsNotPartOfTheCommand(t *testing.T) {
	// vim opens by asking what the arrow keys send. The payload is not text,
	// and if it landed on the tracked line the deny policy would be reading
	// something nobody typed.
	line := echo(t, prompt, "\x1bP+q6b75;6b64\x1b\\", "uptime")
	got, _ := line.snapshot()
	if got != prompt+"uptime" {
		t.Fatalf("line = %q", got)
	}
}

func TestTheEmulatorsAnswersAreTheOnlyRepliesAllowed(t *testing.T) {
	allowed := []string{"\x1b[5;12R", "\x1b[?1;2c", "\x1b[>0;276;0c", "\x1b[0n", "\x1bP0+r\x1b\\"}
	for _, reply := range allowed {
		if !terminalReportPattern.MatchString(reply) {
			t.Errorf("the emulator's own reply %q was refused", reply)
		}
	}
	// A reply channel that accepted anything would be a way around the policy.
	for _, smuggled := range []string{"rm -rf /\n", "\x1b[0n; rm -rf /", "\x1bP0+r\x1b\\rm -rf /", ""} {
		if terminalReportPattern.MatchString(smuggled) {
			t.Errorf("%q was accepted as a terminal report", smuggled)
		}
	}
}
