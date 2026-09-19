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
