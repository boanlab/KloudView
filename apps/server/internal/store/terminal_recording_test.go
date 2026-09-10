package store

import (
	"strings"
	"testing"
	"time"
)

func TestTerminalRecordingRetentionAndLimit(t *testing.T) {
	memory := NewMemory()
	now := time.Now()
	recording := memory.AppendTerminalRecording("session-1", "node-1", "output", strings.Repeat("x", terminalRecordingLimit+1), now)
	if !recording.Truncated || recording.Bytes != terminalRecordingLimit {
		t.Fatalf("recording limit not enforced: %+v", recording)
	}
	if _, ok := memory.TerminalRecording("session-1", now.Add(31*24*time.Hour)); ok {
		t.Fatal("expired recording returned")
	}
}
