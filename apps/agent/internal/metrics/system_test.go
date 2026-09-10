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
