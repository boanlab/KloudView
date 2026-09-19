package inventory

import (
	"testing"
	"time"
)

func withDomains(t *testing.T, sets ...map[string]VirtualMachine) func() map[string]VMStats {
	t.Helper()
	index := 0
	original := vmStats
	vmStats = func() map[string]VirtualMachine {
		set := sets[min(index, len(sets)-1)]
		index++
		return set
	}
	t.Cleanup(func() {
		vmStats = original
		vmMu.Lock()
		lastVM = map[string]vmReading{}
		vmMu.Unlock()
	})
	vmMu.Lock()
	lastVM = map[string]vmReading{}
	vmMu.Unlock()
	return VirtualMachineStats
}

func TestFirstReadingInventsNoRate(t *testing.T) {
	read := withDomains(t, map[string]VirtualMachine{
		"one": {Name: "one", State: "running", VCPUs: 1, CPUTimeNanos: 5e9, NetworkRxBytes: 1000},
	})
	stats := read()
	// One counter reading says nothing about a rate; a number here would be
	// invented from the guest's whole lifetime.
	if stats["one"].CPUPercent != 0 || stats["one"].NetworkRxRate != 0 {
		t.Fatalf("first reading produced rates: %+v", stats["one"])
	}
}

func TestRatesComeFromTheChangeBetweenReadings(t *testing.T) {
	base := map[string]VirtualMachine{"one": {Name: "one", VCPUs: 2, CPUTimeNanos: 0, NetworkRxBytes: 0}}
	// 100ms of host CPU across two vCPUs. Over the ~200ms between readings
	// that is about a quarter of the guest's two cores; wall-clock jitter
	// makes the exact figure unstable, so the test asserts the band.
	next := map[string]VirtualMachine{"one": {Name: "one", VCPUs: 2, CPUTimeNanos: 1e8, NetworkRxBytes: 2048}}
	read := withDomains(t, base, next)
	read()
	time.Sleep(200 * time.Millisecond)
	stats := read()
	got := stats["one"]
	if got.CPUPercent < 10 || got.CPUPercent > 45 {
		t.Fatalf("cpu = %.1f, want roughly a quarter of two vCPUs", got.CPUPercent)
	}
	if got.NetworkRxRate <= 0 {
		t.Fatalf("network rate = %.1f, want bytes per second", got.NetworkRxRate)
	}
}

func TestMemoryIsWhatTheGuestUsesNotWhatTheEmulatorCosts(t *testing.T) {
	read := withDomains(t, map[string]VirtualMachine{
		// 256 MiB assigned, 97 MiB used by the guest, 526 MiB resident on the
		// host because the emulator counts too.
		"one": {Name: "one", MemoryBytes: 268435456, MemoryUsedBytes: 101711872, HostMemoryBytes: 551931904},
	})
	got := read()["one"]
	if got.MemoryLimitBytes != 268435456 || got.MemoryBytes != 101711872 {
		t.Fatalf("memory = %+v", got)
	}
	if got.MemoryPercent <= 0 || got.MemoryPercent >= 100 {
		t.Fatalf("memory percent = %.1f, want a share of the assignment", got.MemoryPercent)
	}
	// The host cost is kept, but separately: it is not the guest's usage.
	if got.HostMemoryBytes != 551931904 {
		t.Fatalf("host memory = %d", got.HostMemoryBytes)
	}
}

func TestARestartedGuestDoesNotProduceANegativeRate(t *testing.T) {
	before := map[string]VirtualMachine{"one": {Name: "one", VCPUs: 1, CPUTimeNanos: 9e9, NetworkRxBytes: 9000}}
	after := map[string]VirtualMachine{"one": {Name: "one", VCPUs: 1, CPUTimeNanos: 1e6, NetworkRxBytes: 10}}
	read := withDomains(t, before, after)
	read()
	time.Sleep(100 * time.Millisecond)
	got := read()["one"]
	if got.CPUPercent != 0 || got.NetworkRxRate != 0 {
		t.Fatalf("counters that went backwards produced a rate: %+v", got)
	}
}

func TestADomainThatDisappearsIsForgotten(t *testing.T) {
	read := withDomains(t,
		map[string]VirtualMachine{"one": {Name: "one", CPUTimeNanos: 5e9}},
		map[string]VirtualMachine{},
	)
	read()
	read()
	vmMu.Lock()
	remembered := len(lastVM)
	vmMu.Unlock()
	if remembered != 0 {
		t.Fatalf("a gone domain kept %d readings; its counters restart if it returns", remembered)
	}
}
