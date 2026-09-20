package executor

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/creack/pty"
)

// startTestPTY builds the same shape startPTY does, without the credential
// handling, so the test can run as whoever is running the suite.
func startTestPTY(t *testing.T, script string) *ptySession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	file, err := pty.StartWithSize(command, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		cancel()
		t.Skipf("no pty available: %v", err)
	}
	return &ptySession{file: file, command: command, cancel: cancel}
}

// zombie reports whether the pid is present and reaped-pending. A reaped child
// is gone from the table entirely, so "not found" is the passing answer.
func zombie(pid int) bool {
	data, err := os.ReadFile("/proc/" + itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	fields := string(data)
	// State is the field after the last ')': "pid (comm) S ..."
	for index := len(fields) - 1; index >= 0; index-- {
		if fields[index] == ')' {
			rest := fields[index+1:]
			for _, char := range rest {
				if char == ' ' {
					continue
				}
				return char == 'Z'
			}
		}
	}
	return false
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func TestClosingASessionLeavesNoZombie(t *testing.T) {
	session := startTestPTY(t, "sleep 30")
	pid := session.command.Process.Pid
	session.close()
	if zombie(pid) {
		t.Fatalf("shell %d was killed but never reaped", pid)
	}
	// Closing again must not panic or wait a second time.
	session.close()
}

func TestClosingASessionTakesItsBackgroundJobsWithIt(t *testing.T) {
	// A login shell on a terminal, as the agent starts one. That shell turns on
	// job control, so a background job gets a process group of its own and a
	// group signal never reaches it — the session is the only unit that holds
	// them together. A shell started with -c has no job control and would pass
	// this test without the fix.
	session := startLoginPTY(t)
	shell := session.command.Process.Pid
	if _, err := session.file.Write([]byte("sleep 120 &\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	child := 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && child == 0 {
		time.Sleep(100 * time.Millisecond)
		child = findChild(shell, "sleep")
	}
	if child == 0 {
		t.Skip("the shell never started the background job")
	}
	if group := processGroup(child); group == shell {
		t.Skip("this shell has no job control, so the test proves nothing")
	}
	session.close()
	if zombie(shell) {
		t.Fatalf("shell %d was not reaped", shell)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(child, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("background job %d outlived its session and is now orphaned", child)
}

// startLoginPTY starts the shell the way the agent does.
func startLoginPTY(t *testing.T) *ptySession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "/bin/sh", "-l")
	command.Env = append(os.Environ(), "TERM=dumb")
	file, err := pty.StartWithSize(command, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		cancel()
		t.Skipf("no pty available: %v", err)
	}
	// Drain, or the shell blocks once the pty buffer fills.
	go func() {
		buffer := make([]byte, 4096)
		for {
			if _, err := file.Read(buffer); err != nil {
				return
			}
		}
	}()
	return &ptySession{file: file, command: command, cancel: cancel}
}

// findChild returns a process in the shell's session whose name matches.
func findChild(sid int, name string) int {
	for _, pid := range sessionMembers(sid) {
		if pid == sid {
			continue
		}
		data, err := os.ReadFile("/proc/" + itoa(pid) + "/comm")
		if err == nil && strings.TrimSpace(string(data)) == name {
			return pid
		}
	}
	return 0
}

func processGroup(pid int) int {
	data, err := os.ReadFile("/proc/" + itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	end := strings.LastIndex(string(data), ")")
	if end < 0 {
		return 0
	}
	fields := strings.Fields(string(data)[end+1:])
	if len(fields) < 3 {
		return 0
	}
	group, _ := strconv.Atoi(fields[2])
	return group
}

func TestClosingAnAlreadyExitedSessionReapsIt(t *testing.T) {
	session := startTestPTY(t, "exit 0")
	pid := session.command.Process.Pid
	// Wait for it to exit on its own; until something reaps it, it is a zombie.
	time.Sleep(300 * time.Millisecond)
	session.close()
	if zombie(pid) {
		t.Fatalf("exited shell %d was left unreaped", pid)
	}
}

func TestTerminalEnvironmentKeepsPagersOutOfTheWay(t *testing.T) {
	env := terminalEnv("/usr/bin/bash")
	have := map[string]string{}
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok {
			have[key] = value
		}
	}
	// The console renders a screen and takes keys one at a time, so the
	// session says so and pagers are left alone.
	if have["TERM"] != "xterm-256color" {
		t.Errorf("TERM = %q, want a terminal that can be addressed", have["TERM"])
	}
	for _, key := range []string{"PAGER", "SYSTEMD_PAGER", "GIT_PAGER"} {
		if _, pinned := have[key]; pinned {
			t.Errorf("%s is pinned to %q; a session that can page should page", key, have[key])
		}
	}
	// -F so short output prints and returns, -R so colour survives. Not -X:
	// that stops less using the alternate screen, which turns every page turn
	// into another few rows appended to the session.
	if !strings.Contains(have["LESS"], "F") || !strings.Contains(have["LESS"], "R") {
		t.Errorf("LESS = %q, want -F and -R", have["LESS"])
	}
	if strings.Contains(have["LESS"], "X") {
		t.Errorf("LESS = %q still has -X; paging would accumulate in the scrollback", have["LESS"])
	}
	// bash re-evaluates this before every prompt, so the login profile cannot
	// overwrite it the way it overwrites PS1.
	if !strings.Contains(have["PROMPT_COMMAND"], `\w`) {
		t.Errorf("PROMPT_COMMAND = %q, want the working directory in it", have["PROMPT_COMMAND"])
	}
	// A shell without PROMPT_COMMAND still gets a prompt worth reading.
	for _, entry := range terminalEnv("/bin/sh") {
		if strings.HasPrefix(entry, "PROMPT_COMMAND=") {
			t.Error("/bin/sh does not honour PROMPT_COMMAND; setting it is misleading")
		}
	}
	if !strings.Contains(have["PS1"], `\h`) {
		t.Errorf("PS1 = %q, want the host in it", have["PS1"])
	}
}

func TestLoginShellPrefersBash(t *testing.T) {
	shell, args := loginShell()
	if len(args) != 1 || args[0] != "-l" {
		t.Fatalf("args = %v, want a login shell", args)
	}
	// The host running the suite decides which exists; either answer is a
	// shell, and only bash is promised the prompt.
	if !strings.HasSuffix(shell, "bash") && shell != "/bin/sh" {
		t.Fatalf("shell = %q", shell)
	}
}

// A pty pair standing in for a session, so the two input paths can be driven
// without a WebSocket on either end.
func sessionOnAPty(t *testing.T) (*ptySession, *os.File) {
	t.Helper()
	primary, secondary, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	t.Cleanup(func() { _ = primary.Close(); _ = secondary.Close() })
	// A tty is line-buffered until someone turns that off, and that is exactly
	// what a full-screen program does when it takes the terminal. Without it
	// the far end would not see a keystroke until a newline arrived, which is
	// the very behaviour keyboard mode exists to escape.
	makeRaw(t, secondary)
	return &ptySession{file: primary}, secondary
}

// makeRaw is what vi does to the terminal the moment it starts: line
// discipline off, echo off, one byte is enough to satisfy a read. It goes
// through SyscallConn rather than Fd, because Fd takes the file out of Go's
// poller and a read on it would then block past its own deadline.
func makeRaw(t *testing.T, file *os.File) {
	t.Helper()
	conn, err := file.SyscallConn()
	if err != nil {
		t.Skipf("no syscall access to the pty: %v", err)
	}
	var ioctlErr syscall.Errno
	controlErr := conn.Control(func(fd uintptr) {
		var settings syscall.Termios
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&settings))); e != 0 {
			ioctlErr = e
			return
		}
		settings.Lflag &^= syscall.ICANON | syscall.ECHO
		settings.Cc[syscall.VMIN] = 1
		settings.Cc[syscall.VTIME] = 0
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&settings))); e != 0 {
			ioctlErr = e
		}
	})
	if controlErr != nil {
		t.Skipf("cannot reach the pty descriptor: %v", controlErr)
	}
	if ioctlErr != 0 {
		t.Skipf("cannot put the pty in raw mode: %v", ioctlErr)
	}
}

// readSoon returns what the far end of the pty received, or "" if nothing
// arrived before the deadline.
func readSoon(t *testing.T, file *os.File) string {
	t.Helper()
	_ = file.SetReadDeadline(time.Now().Add(750 * time.Millisecond))
	defer func() { _ = file.SetReadDeadline(time.Time{}) }()
	buffer := make([]byte, 256)
	n, err := file.Read(buffer)
	if err != nil && n == 0 {
		return ""
	}
	return string(buffer[:n])
}

func TestKeystrokesReachThePtyWithoutWaitingForANewline(t *testing.T) {
	session, far := sessionOnAPty(t)

	// An arrow key is not a line and never becomes one. If the agent waited
	// for a newline the way the command path does, vi would never see it.
	session.acceptKeys([]byte("\x1b[A"))
	if got := readSoon(t, far); got != "\x1b[A" {
		t.Fatalf("pty saw %q, want the arrow-up sequence", got)
	}
}

func TestTheDenyPolicyStillScreensTheCommandLine(t *testing.T) {
	session, far := sessionOnAPty(t)

	// A partial line waits: the policy must never see half a command.
	refused, overflow := session.acceptInput([]byte("echo saf"))
	if refused != 0 || overflow {
		t.Fatalf("a partial line was judged: refused=%d overflow=%v", refused, overflow)
	}
	if got := readSoon(t, far); got != "" {
		t.Fatalf("an unterminated line reached the pty as %q", got)
	}

	if refused, _ := session.acceptInput([]byte("e\n")); refused != 0 {
		t.Fatalf("a permitted command was refused %d times", refused)
	}
	if got := readSoon(t, far); got != "echo safe\n" {
		t.Fatalf("pty saw %q, want the completed command", got)
	}

}

func TestTheCommandPathStillHasAnInputLimit(t *testing.T) {
	session, _ := sessionOnAPty(t)
	if _, overflow := session.acceptInput(make([]byte, 5000)); !overflow {
		t.Fatal("a command longer than the buffer was not refused")
	}
	if len(session.input) != 0 {
		t.Fatal("the overflowing buffer was kept")
	}
}
