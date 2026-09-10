package store

import (
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// Reads take the newest lines per node and merge, so the window has to stay
// ordered whatever order the agent sent it in.
func TestLogLinesReturnTheNewestAcrossNodes(t *testing.T) {
	memory := NewMemory()
	now := time.Now().UTC()
	for _, node := range []string{"node-a", "node-b"} {
		memory.AddLogBatch(node, "agent", domain.LogCounters{To: now}, []domain.LogLine{
			// Deliberately out of order.
			{At: now.Add(-30 * time.Second), Priority: 4, Message: node + "-mid"},
			{At: now.Add(-90 * time.Second), Priority: 4, Message: node + "-old"},
			{At: now.Add(-5 * time.Second), Priority: 4, Message: node + "-new"},
		})
	}

	got := memory.LogLines(nil, now.Add(-time.Minute), 4)
	if len(got) != 4 {
		t.Fatalf("returned %d lines, want the 4 newest inside the window", len(got))
	}
	for index := 1; index < len(got); index++ {
		if got[index].At.After(got[index-1].At) {
			t.Fatalf("not newest first: %v then %v", got[index-1].At, got[index].At)
		}
	}
	for _, line := range got {
		if line.Message == "node-a-old" || line.Message == "node-b-old" {
			t.Fatal("a line outside the window was returned")
		}
	}

	// A scope that names one node never reads the other.
	only := memory.LogLines(map[string]bool{"node-a": true}, now.Add(-time.Hour), 10)
	for _, line := range only {
		if line.NodeID != "node-a" {
			t.Fatalf("read outside the allowed set: %+v", line)
		}
	}
	if len(only) != 3 {
		t.Fatalf("allowed node returned %d lines, want 3", len(only))
	}
}
