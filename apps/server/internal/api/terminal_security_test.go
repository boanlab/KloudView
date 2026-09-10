package api

import "testing"

func TestTerminalSecretMasking(t *testing.T) {
	masked := maskTerminalData("password=hunter2 token:abc123 Authorization: Bearer xyz")
	if masked != "password=[REDACTED] token:[REDACTED] Authorization: Bearer [REDACTED]" {
		t.Fatalf("unexpected masked value: %q", masked)
	}
}

func TestTerminalCommandPolicy(t *testing.T) {
	server := &Server{terminalDenyPatterns: defaultTerminalDenyPatterns()}
	for _, command := range []string{"rm -rf /\n", "mkfs.ext4 /dev/sdb\n", "dd if=/dev/zero of=/dev/sda\n", "reboot\n"} {
		if server.terminalInputAllowed(command) {
			t.Fatalf("dangerous command allowed: %q", command)
		}
	}
	if !server.terminalInputAllowed("journalctl -n 100\n") {
		t.Fatal("diagnostic command blocked")
	}
}

func TestTerminalCommandAllowedScreensEveryLine(t *testing.T) {
	server := &Server{terminalDenyPatterns: defaultTerminalDenyPatterns()}
	if server.terminalCommandAllowed("uptime\nshutdown -h now") {
		t.Fatal("destructive command on a later line was allowed")
	}
	if server.terminalCommandAllowed("wipefs /dev/sdb") {
		t.Fatal("wipefs command allowed")
	}
	if !server.terminalCommandAllowed("df -h\nfree -m\njournalctl -n 50") {
		t.Fatal("diagnostic multi-line command blocked")
	}
}
