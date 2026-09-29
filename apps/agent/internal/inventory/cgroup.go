package inventory

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Container resource use is read from the cgroup filesystem: cheaper than a
// runtime CLI, and runtime-independent, since docker, podman, containerd and
// kubelet all end up as directories under the same hierarchy.

// cgroupRoot is replaceable in tests; a cgroup hierarchy cannot be fabricated
// in a temporary directory otherwise.
var cgroupRoot = "/sys/fs/cgroup"

// ContainerStats is one container's cgroup reading. Counters are cumulative;
// CPUPercent is derived from the change since the previous reading.
type ContainerStats struct {
	CPUPercent       float64 `json:"cpuPercent"`
	MemoryBytes      uint64  `json:"memoryBytes,omitempty"`
	MemoryLimitBytes uint64  `json:"memoryLimitBytes,omitempty"`
	// What MemoryPercent is a share of. A container with a limit is measured
	// against it; one without is measured against the machine, and the console
	// has no other way to tell which denominator it is looking at.
	HostMemoryBytes uint64  `json:"hostMemoryBytes,omitempty"`
	MemoryPercent   float64 `json:"memoryPercent,omitempty"`
	DiskReadBytes   uint64  `json:"diskReadBytes,omitempty"`
	DiskWriteBytes  uint64  `json:"diskWriteBytes,omitempty"`
	Processes       int     `json:"processes,omitempty"`
	// OOMKills is how many times the kernel has killed something in this
	// container: a number that can be charted and alerted on, where the
	// kernel's own log prose cannot.
	OOMKills uint64 `json:"oomKills,omitempty"`
	// ThrottledUsec is how long the kernel has held this container off the CPU
	// for exceeding its quota, and ThrottledCount how many periods that
	// happened in. A throttled container reads as comfortable -- low usage,
	// because being stopped is not usage -- and this is the only number that
	// says otherwise.
	ThrottledUsec  uint64 `json:"throttledUsec,omitempty"`
	ThrottledCount uint64 `json:"throttledCount,omitempty"`
}

// cpuReading is the previous CPU counter for one container, kept so a rate can
// be derived without asking the runtime.
type cpuReading struct {
	usec uint64
	at   time.Time
}

var (
	cpuMu      sync.Mutex
	lastCPU    = map[string]cpuReading{}
	hostMemory uint64
)

// ContainerCgroupStats reads the cgroup for each container ID. IDs that have no
// cgroup directory -- a stopped container, or a runtime that hides it -- are
// absent from the result rather than reported as zero.
func ContainerCgroupStats(ids []string) map[string]ContainerStats {
	paths := cgroupPaths(ids)
	if len(paths) == 0 {
		return nil
	}
	if hostMemory == 0 {
		hostMemory = totalMemory()
	}
	now := time.Now()
	stats := make(map[string]ContainerStats, len(paths))
	for id, dir := range paths {
		stat, ok := readCgroup(dir, id, now)
		if ok {
			stats[id] = stat
		}
	}
	return stats
}

// cgroupPaths locates each container's directory. Layouts differ by cgroup
// driver and runtime, so the hierarchy is walked once and matched by ID prefix
// instead of guessing a path per runtime.
func cgroupPaths(ids []string) map[string]string {
	wanted := make(map[string]string, len(ids))
	for _, id := range ids {
		if len(id) >= 12 {
			wanted[id] = ""
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	found := map[string]string{}
	_ = filepath.WalkDir(cgroupRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		name := entry.Name()
		for id := range wanted {
			if !strings.Contains(name, id) {
				continue
			}
			// A container id appears in more than one directory: rootless
			// podman adds libpod-conmon-<id>.scope beside libpod-<id>.scope,
			// and the monitor's usage is not the container's. The container's
			// own directory is the shortest name carrying the id, since every
			// companion adds a word to it.
			if previous, seen := found[id]; !seen || len(name) < len(filepath.Base(previous)) {
				found[id] = path
			}
		}
		return nil
	})
	return found
}

func readCgroup(dir, id string, now time.Time) (ContainerStats, bool) {
	stat := ContainerStats{}
	ok := false
	if usec, found := cpuUsageMicros(dir); found {
		stat.CPUPercent = cpuPercent(id, usec, now)
		ok = true
	}
	if value, found := readUint(filepath.Join(dir, "memory.current")); found {
		stat.MemoryBytes, ok = value, true
	} else if value, found := readUint(filepath.Join(dir, "memory.usage_in_bytes")); found {
		stat.MemoryBytes, ok = value, true
	}
	stat.MemoryLimitBytes = memoryLimit(dir)
	// A container with no limit is measured against the host, which is what it
	// can actually consume.
	basis := stat.MemoryLimitBytes
	if basis == 0 {
		basis = hostMemory
	}
	stat.HostMemoryBytes = hostMemory
	if basis > 0 && stat.MemoryBytes > 0 {
		stat.MemoryPercent = float64(stat.MemoryBytes) / float64(basis) * 100
	}
	stat.OOMKills = oomKills(dir)
	if cpu, ok := cpuStat(dir); ok {
		stat.ThrottledUsec = cpu["throttled_usec"]
		stat.ThrottledCount = cpu["nr_throttled"]
	}
	stat.DiskReadBytes, stat.DiskWriteBytes = blockIO(dir)
	if value, found := readUint(filepath.Join(dir, "pids.current")); found {
		stat.Processes = int(value)
	}
	return stat, ok
}

// cpuUsageMicros reads cumulative CPU time, cgroup v2 first then v1.
func cpuUsageMicros(dir string) (uint64, bool) {
	if stat, ok := cpuStat(dir); ok {
		if usec, found := stat["usage_usec"]; found {
			return usec, true
		}
	}
	// cgroup v1 reports nanoseconds.
	if nanos, found := readUint(filepath.Join(dir, "cpuacct.usage")); found {
		return nanos / 1000, true
	}
	return 0, false
}

// cpuStat reads the whole of cpu.stat rather than stopping at the first key.
// Throttling is three lines below usage in the same file: the reason a
// container can be at its limit and still look idle, since time the kernel
// holds it off the CPU is not time it spent on the CPU.
func cpuStat(dir string) (map[string]uint64, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "cpu.stat"))
	if err != nil {
		return nil, false
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		if parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64); err == nil {
			values[key] = parsed
		}
	}
	return values, len(values) > 0
}

// oomKills is the count the kernel keeps in the cgroup itself, beside the
// memory figures already being read. A counter can be charted and alerted on;
// the log line it replaces could be neither.
func oomKills(dir string) uint64 {
	raw, err := os.ReadFile(filepath.Join(dir, "memory.events"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, found := strings.Cut(line, " ")
		if found && key == "oom_kill" {
			if count, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64); err == nil {
				return count
			}
		}
	}
	return 0
}

// cpuPercent converts the counter to a share of one host's worth of CPU, so it
// reads the same way as the node's own CPU figure.
func cpuPercent(id string, usec uint64, now time.Time) float64 {
	cpuMu.Lock()
	defer cpuMu.Unlock()
	previous, seen := lastCPU[id]
	lastCPU[id] = cpuReading{usec: usec, at: now}
	if !seen || !now.After(previous.at) || usec < previous.usec {
		return 0 // first reading, or a counter reset after a restart
	}
	elapsed := now.Sub(previous.at).Microseconds()
	if elapsed <= 0 {
		return 0
	}
	cores := float64(runtime.NumCPU())
	if cores <= 0 {
		cores = 1
	}
	percent := float64(usec-previous.usec) / float64(elapsed) / cores * 100
	return min(percent, 100)
}

func memoryLimit(dir string) uint64 {
	if raw, err := os.ReadFile(filepath.Join(dir, "memory.max")); err == nil {
		value := strings.TrimSpace(string(raw))
		if value == "max" {
			return 0
		}
		if limit, err := strconv.ParseUint(value, 10, 64); err == nil {
			return limit
		}
	}
	// cgroup v1 writes an unlimited container as a value near the word size.
	if limit, found := readUint(filepath.Join(dir, "memory.limit_in_bytes")); found && limit < 1<<62 {
		return limit
	}
	return 0
}

// blockIO sums every device, matching how the node's own disk counters read.
func blockIO(dir string) (uint64, uint64) {
	var read, write uint64
	if raw, err := os.ReadFile(filepath.Join(dir, "io.stat")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			for _, field := range strings.Fields(line) {
				key, value, found := strings.Cut(field, "=")
				if !found {
					continue
				}
				parsed, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					continue
				}
				switch key {
				case "rbytes":
					read += parsed
				case "wbytes":
					write += parsed
				}
			}
		}
		return read, write
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "blkio.throttle.io_service_bytes")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			parsed, err := strconv.ParseUint(fields[2], 10, 64)
			if err != nil {
				continue
			}
			switch fields[1] {
			case "Read":
				read += parsed
			case "Write":
				write += parsed
			}
		}
	}
	return read, write
}

func readUint(path string) (uint64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
