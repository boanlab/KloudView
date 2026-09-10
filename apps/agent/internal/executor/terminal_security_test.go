package executor

import "testing"

func TestTerminalInputPolicy(t *testing.T) {
	if terminalInputAllowed("poweroff\n") {
		t.Fatal("poweroff command allowed")
	}
	if !terminalInputAllowed("uptime\n") {
		t.Fatal("diagnostic command blocked")
	}
}
