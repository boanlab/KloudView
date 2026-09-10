package executor

import "testing"

func TestRunTerminalCapturesOutput(t *testing.T) {
	executor := New(nil)
	output, err := executor.RunTerminal("printf kloudview")
	if err != nil || output != "kloudview" {
		t.Fatalf("output = %q, error = %v", output, err)
	}
}

func TestRunTerminalEnforcesDenyPolicy(t *testing.T) {
	executor := New(nil)
	output, err := executor.RunTerminal("rm -rf / --no-preserve-root")
	if err == nil {
		t.Fatalf("destructive command was not blocked, output = %q", output)
	}
	if output != "" {
		t.Fatalf("blocked command produced output = %q", output)
	}
}

func TestRunTerminalBlocksDestructiveLineAfterFirst(t *testing.T) {
	executor := New(nil)
	if _, err := executor.RunTerminal("uptime\npoweroff"); err == nil {
		t.Fatal("destructive command on a later line was not blocked")
	}
}

func TestTerminalCommandAllowed(t *testing.T) {
	if terminalCommandAllowed("mkfs.ext4 /dev/sda1") {
		t.Fatal("mkfs command allowed")
	}
	if !terminalCommandAllowed("df -h\nfree -m") {
		t.Fatal("diagnostic command blocked")
	}
}

func TestLimitedBufferCapsOutput(t *testing.T) {
	buffer := &limitedBuffer{limit: 4}
	if written, err := buffer.Write([]byte("terminal")); err != nil || written != 8 {
		t.Fatalf("written = %d, error = %v", written, err)
	}
	if buffer.String() != "term" {
		t.Fatalf("output = %q", buffer.String())
	}
}
