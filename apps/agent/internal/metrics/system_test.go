package metrics

import "testing"

func TestPercentFromCounters(t *testing.T) {
	value := percentFromCounters(1000, 700, 1200, 820)
	if value != 40 {
		t.Fatalf("cpu percent = %.1f", value)
	}
	if value := percentFromCounters(0, 0, 100, 20); value != 0 {
		t.Fatalf("first sample = %.1f", value)
	}
}

func TestMemoryPercentUsesAvailableAndLegacyFallback(t *testing.T) {
	if value := memoryPercentFromData("MemTotal: 1000 kB\nMemAvailable: 250 kB\n"); value != 75 {
		t.Fatalf("available memory percent = %.1f", value)
	}
	legacy := "MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 200 kB\nSReclaimable: 30 kB\nShmem: 10 kB\n"
	if value := memoryPercentFromData(legacy); value != 63 {
		t.Fatalf("legacy memory percent = %.1f", value)
	}
}

// A host blocked on its disks is not a host at rest.
//
// iowait sat in the idle column, so a machine that could not get a read
// through reported low CPU usage — the one number an operator would have
// looked at said everything was fine.
func TestTimeBlockedOnIOIsNotIdle(t *testing.T) {
	// user nice system idle iowait irq softirq steal
	const line = "cpu  100 0 100 700 100 0 0 0"
	total, idle := parseCPULine(line)
	if total != 1000 {
		t.Fatalf("total = %d, want every column summed", total)
	}
	if idle != 700 {
		t.Fatalf("idle = %d, want the idle column alone — iowait is not rest", idle)
	}
	// Which is to say: a host spending a tenth of its time waiting on a disk
	// reports that tenth as busy.
	if busy := percentFromCounters(0, 0, total, idle); busy != 0 {
		t.Fatalf("a first reading invented %v%%", busy)
	}
	if busy := percentFromCounters(500, 350, total, idle); busy != 30 {
		t.Fatalf("busy = %v%%, want 30 — 20%% of work plus 10%% blocked on I/O", busy)
	}
}

func TestAMalformedCPULineIsRefusedRatherThanGuessed(t *testing.T) {
	for _, line := range []string{"", "cpu", "cpu 1 2 3", "intr 100 0 0 0 0 0 0 0", "cpu a b c d e f g h"} {
		if total, idle := parseCPULine(line); total != 0 || idle != 0 {
			t.Errorf("parseCPULine(%q) = %d, %d, want nothing", line, total, idle)
		}
	}
}
