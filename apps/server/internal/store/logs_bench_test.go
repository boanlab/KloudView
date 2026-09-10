package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// seedLogs fills the store the way a fleet of that size would over 24 hours.
func seedLogs(b *testing.B, nodes, linesPerNode int) *Memory {
	b.Helper()
	memory := NewMemory()
	now := time.Now().UTC()
	for n := range nodes {
		nodeID := fmt.Sprintf("node-%03d", n)
		lines := make([]domain.LogLine, 0, linesPerNode)
		for i := range linesPerNode {
			lines = append(lines, domain.LogLine{
				At:       now.Add(-time.Duration(linesPerNode-i) * time.Second),
				Priority: 4,
				Unit:     "sshd",
				Message:  "Failed password for invalid user admin from 203.0.113.9 port 40222 ssh2",
			})
		}
		memory.AddLogBatch(nodeID, "agent", domain.LogCounters{To: now, Counts: map[string]int{"warning": linesPerNode}}, lines)
	}
	return memory
}

func BenchmarkLogLinesOneNodeOfFifty(b *testing.B) {
	memory := seedLogs(b, 50, 20000)
	since := time.Now().UTC().Add(-time.Hour)
	b.ResetTimer()
	for range b.N {
		if got := memory.LogLines(map[string]bool{"node-007": true}, since, 500); len(got) == 0 {
			b.Fatal("no lines")
		}
	}
}

func BenchmarkLogLinesWholeFleet(b *testing.B) {
	memory := seedLogs(b, 50, 20000)
	since := time.Now().UTC().Add(-time.Hour)
	b.ResetTimer()
	for range b.N {
		if got := memory.LogLines(nil, since, 500); len(got) == 0 {
			b.Fatal("no lines")
		}
	}
}

func BenchmarkAddLogBatch(b *testing.B) {
	memory := seedLogs(b, 50, 20000)
	now := time.Now().UTC()
	batch := make([]domain.LogLine, 100)
	for i := range batch {
		batch[i] = domain.LogLine{At: now, Priority: 4, Unit: "sshd", Message: "line"}
	}
	b.ResetTimer()
	for range b.N {
		memory.AddLogBatch("node-007", "agent", domain.LogCounters{To: now}, batch)
	}
}
