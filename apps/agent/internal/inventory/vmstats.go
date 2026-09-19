package inventory

import (
	"sync"
	"time"
)

// A virtual machine's resource use has to be derived, not read. libvirt reports
// counters — nanoseconds of host CPU burnt, bytes moved since boot — so a rate
// only exists relative to the previous reading. Nothing was doing that, which
// is why a VM's gauges sat at zero while its facts showed real numbers.

// VMStats is one domain's reading, in the shape the server ingests.
type VMStats struct {
	State            string  `json:"state,omitempty"`
	VCPUs            int     `json:"vcpus,omitempty"`
	CPUPercent       float64 `json:"cpuPercent"`
	MemoryBytes      uint64  `json:"memoryBytes,omitempty"`
	MemoryLimitBytes uint64  `json:"memoryLimitBytes,omitempty"`
	MemoryPercent    float64 `json:"memoryPercent,omitempty"`
	HostMemoryBytes  uint64  `json:"hostMemoryBytes,omitempty"`
	DiskReadBytes    uint64  `json:"diskReadBytes,omitempty"`
	DiskWriteBytes   uint64  `json:"diskWriteBytes,omitempty"`
	NetworkRxRate    float64 `json:"networkRxRate,omitempty"`
	NetworkTxRate    float64 `json:"networkTxRate,omitempty"`
}

// vmReading is the previous counter set for one domain.
type vmReading struct {
	cpuNanos uint64
	rxBytes  uint64
	txBytes  uint64
	at       time.Time
}

var (
	vmMu    sync.Mutex
	lastVM  = map[string]vmReading{}
	vmStats = func() map[string]VirtualMachine {
		return parseDomstats(commandOutput("virsh", "domstats", "--raw"))
	}
)

// VirtualMachineStats reads every domain and turns the counters into rates.
// The first reading of a domain has nothing to compare against, so its CPU and
// network rates are zero rather than a number invented from a single sample.
func VirtualMachineStats() map[string]VMStats {
	domains := vmStats()
	now := time.Now()
	vmMu.Lock()
	defer vmMu.Unlock()
	stats := make(map[string]VMStats, len(domains))
	seen := make(map[string]bool, len(domains))
	for name, vm := range domains {
		seen[name] = true
		item := VMStats{
			State:            vm.State,
			VCPUs:            vm.VCPUs,
			MemoryBytes:      vm.MemoryUsedBytes,
			MemoryLimitBytes: vm.MemoryBytes,
			HostMemoryBytes:  vm.HostMemoryBytes,
			DiskReadBytes:    vm.DiskReadBytes,
			DiskWriteBytes:   vm.DiskWriteBytes,
		}
		if vm.MemoryBytes > 0 && vm.MemoryUsedBytes > 0 {
			item.MemoryPercent = float64(vm.MemoryUsedBytes) / float64(vm.MemoryBytes) * 100
		}
		if previous, ok := lastVM[name]; ok {
			elapsed := now.Sub(previous.at).Seconds()
			if elapsed > 0 {
				// Host CPU nanoseconds over wall nanoseconds, shared across the
				// guest's vCPUs so a two-vCPU guest pinning one core reads 50%.
				if vcpus := max(vm.VCPUs, 1); vm.CPUTimeNanos >= previous.cpuNanos {
					burnt := float64(vm.CPUTimeNanos - previous.cpuNanos)
					item.CPUPercent = burnt / (elapsed * 1e9 * float64(vcpus)) * 100
				}
				item.NetworkRxRate = rate(vm.NetworkRxBytes, previous.rxBytes, elapsed)
				item.NetworkTxRate = rate(vm.NetworkTxBytes, previous.txBytes, elapsed)
			}
		}
		lastVM[name] = vmReading{cpuNanos: vm.CPUTimeNanos, rxBytes: vm.NetworkRxBytes, txBytes: vm.NetworkTxBytes, at: now}
		stats[name] = item
	}
	// A domain that is gone should not keep its counters: if it comes back its
	// counters restart, and comparing against the old ones invents a rate.
	// This runs even when nothing was read at all, which is the case that
	// leaves readings behind the longest.
	for name := range lastVM {
		if !seen[name] {
			delete(lastVM, name)
		}
	}
	if len(stats) == 0 {
		return nil
	}
	return stats
}

// rate turns two counter readings into bytes per second, treating a counter
// that went backwards — a restarted guest — as no reading at all.
func rate(current, previous uint64, elapsed float64) float64 {
	if current < previous || elapsed <= 0 {
		return 0
	}
	return float64(current-previous) / elapsed
}
