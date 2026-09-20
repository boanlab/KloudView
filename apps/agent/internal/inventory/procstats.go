package inventory

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A process's resource use has to be derived, not read.
//
// The inventory reports what a process is — its name, its command line, how
// much memory it holds right now — on the five-minute cycle. None of that is a
// rate, and nothing turned any of it into samples, so every process detail
// page showed a flat zero and a trend that said there were not enough samples.
// There were none at all.
//
// /proc gives CPU as a counter of ticks burnt since the process started, so a
// percentage only exists between two readings. This reads them every tick, the
// way the container and VM paths already do.

// processSampleLimit bounds what is shipped. A host runs hundreds of processes
// and the inventory will report five thousand; sending a sample for each of
// them every tick would cost more than the answer is worth. The heaviest by
// CPU and the heaviest by memory are the ones anyone opens, so those are the
// ones that get a trend — and a process outside the set is told it is outside
// the set rather than shown a zero.
const processSampleLimit = 25

// ProcessStats is one process's reading, in the shape the server ingests.
type ProcessStats struct {
	PID           int     `json:"pid"`
	Name          string  `json:"name,omitempty"`
	State         string  `json:"state,omitempty"`
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryBytes   uint64  `json:"memoryBytes,omitempty"`
	MemoryPercent float64 `json:"memoryPercent,omitempty"`
	// What the percentage is a share of. A process has no allowance of its
	// own, so the meter's denominator is the machine's installed memory.
	HostMemoryBytes uint64 `json:"hostMemoryBytes,omitempty"`
	Threads         int    `json:"threads,omitempty"`
	StartedAt       string `json:"startedAt,omitempty"`
}

// processReading is the previous counter set for one process. The start time is
// part of the key, not the value: Linux reuses process IDs, and comparing a new
// process's counters against a dead one's would invent a rate out of two
// unrelated programs.
type processReading struct {
	ticks uint64
	at    time.Time
}

type processKey struct {
	pid       int
	startTime uint64
}

var (
	procMu   sync.Mutex
	lastProc = map[processKey]processReading{}
	procRoot = "/proc"
	procNow  = time.Now
)

// hostCores is the denominator for a process's CPU share. runtime.NumCPU
// reports what this process may use, which is what the host's own metrics
// already measure against.
func hostCores() int {
	if cores := runtime.NumCPU(); cores > 0 {
		return cores
	}
	return 1
}

// userHZ is the unit /proc reports CPU time in. It is 100 on every Linux
// architecture regardless of the kernel's own tick rate, which is why this can
// be a constant rather than a sysconf call the agent cannot make without cgo.
const userHZ = 100

// ProcessStatistics reads every process and turns its counters into rates,
// returning the heaviest by CPU and by memory. The first reading of a process
// has nothing to compare against, so its CPU is zero rather than a number
// invented from a single sample.
func ProcessStatistics(totalMemoryBytes uint64) []ProcessStats {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	now := procNow()
	// Read once per collection rather than once per process: it is the same
	// answer for every one of them and it comes off disk.
	boot := readBootTime()
	procMu.Lock()
	defer procMu.Unlock()

	items := make([]ProcessStats, 0, len(entries))
	seen := make(map[processKey]bool, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		raw, ok := readProcessStat(pid)
		if !ok {
			continue
		}
		key := processKey{pid: pid, startTime: raw.startTime}
		seen[key] = true
		item := ProcessStats{
			PID:         pid,
			Name:        raw.name,
			State:       raw.state,
			MemoryBytes: raw.rssBytes,
			Threads:     raw.threads,
			StartedAt:   processStartedAt(boot, raw.startTime),
		}
		if totalMemoryBytes > 0 {
			item.MemoryPercent = float64(raw.rssBytes) / float64(totalMemoryBytes) * 100
			item.HostMemoryBytes = totalMemoryBytes
		}
		if previous, found := lastProc[key]; found {
			elapsed := now.Sub(previous.at).Seconds()
			// A counter cannot go backwards for the same process, but a clock
			// that did not move gives no rate either way.
			if elapsed > 0 && raw.ticks >= previous.ticks {
				burnt := float64(raw.ticks-previous.ticks) / userHZ
				// Share of the whole machine, so a process's CPU adds up
				// against its host's the way a container's already does.
				item.CPUPercent = burnt / (elapsed * float64(hostCores())) * 100
			}
		}
		lastProc[key] = processReading{ticks: raw.ticks, at: now}
		items = append(items, item)
	}

	// A process that is gone should not keep its counters: its ID will be
	// handed to something else, and this map is the only thing standing
	// between that and a fabricated rate.
	for key := range lastProc {
		if !seen[key] {
			delete(lastProc, key)
		}
	}
	return heaviest(items)
}

// heaviest picks the processes worth a trend: the top by CPU and the top by
// memory, as one set. Either list alone would miss a kind of trouble — a busy
// process that holds nothing, or a leak that never burns a cycle.
func heaviest(items []ProcessStats) []ProcessStats {
	if len(items) <= processSampleLimit {
		return items
	}
	chosen := map[int]bool{}
	picked := make([]ProcessStats, 0, processSampleLimit*2)
	take := func(less func(a, b ProcessStats) bool) {
		sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
		for i := 0; i < len(items) && i < processSampleLimit; i++ {
			if chosen[items[i].PID] {
				continue
			}
			chosen[items[i].PID] = true
			picked = append(picked, items[i])
		}
	}
	take(func(a, b ProcessStats) bool { return a.CPUPercent > b.CPUPercent })
	take(func(a, b ProcessStats) bool { return a.MemoryBytes > b.MemoryBytes })
	return picked
}

type rawProcessStat struct {
	name      string
	state     string
	ticks     uint64
	startTime uint64
	rssBytes  uint64
	threads   int
}

// readProcessStat parses /proc/<pid>/stat.
//
// The second field is the executable name in parentheses and may itself
// contain spaces and parentheses, so the fields after it are found from the
// last closing parenthesis rather than by splitting the whole line.
func readProcessStat(pid int) (rawProcessStat, bool) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return rawProcessStat{}, false
	}
	line := string(data)
	opened := strings.IndexByte(line, '(')
	closed := strings.LastIndexByte(line, ')')
	if opened < 0 || closed < opened {
		return rawProcessStat{}, false
	}
	stat := rawProcessStat{name: line[opened+1 : closed]}
	fields := strings.Fields(line[closed+1:])
	// Indices counted from the field after the name: state is stat field 3,
	// utime 14, stime 15, num_threads 20, starttime 22, rss 24.
	const (
		fieldState     = 0
		fieldUtime     = 11
		fieldStime     = 12
		fieldThreads   = 17
		fieldStartTime = 19
		fieldRSS       = 21
	)
	if len(fields) <= fieldRSS {
		return rawProcessStat{}, false
	}
	stat.state = fields[fieldState]
	utime, _ := strconv.ParseUint(fields[fieldUtime], 10, 64)
	stime, _ := strconv.ParseUint(fields[fieldStime], 10, 64)
	stat.ticks = utime + stime
	stat.threads, _ = strconv.Atoi(fields[fieldThreads])
	stat.startTime, _ = strconv.ParseUint(fields[fieldStartTime], 10, 64)
	pages, _ := strconv.ParseUint(fields[fieldRSS], 10, 64)
	stat.rssBytes = pages * uint64(os.Getpagesize())
	return stat, true
}

// processStartedAt turns the kernel's "ticks since boot" into a wall clock
// time, which is what makes a restart visible as a restart.
func processStartedAt(boot time.Time, startTime uint64) string {
	if boot.IsZero() {
		return ""
	}
	return boot.Add(time.Duration(startTime) / userHZ * time.Second).UTC().Format(time.RFC3339)
}

func readBootTime() time.Time {
	data, err := os.ReadFile(filepath.Join(procRoot, "stat"))
	if err != nil {
		return time.Time{}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if seconds, ok := strings.CutPrefix(line, "btime "); ok {
			value, err := strconv.ParseInt(strings.TrimSpace(seconds), 10, 64)
			if err != nil {
				return time.Time{}
			}
			return time.Unix(value, 0)
		}
	}
	return time.Time{}
}

// processUnit is the systemd unit or container a process belongs to, read from
// its cgroup. A process outside the sampled set still has somewhere to send
// the operator for a trend, and this is what names it.
func processUnit(pid int) (unit, containerID string) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		// cgroup v2 is "0::/path"; v1 lines are "id:controller:/path" and the
		// last segment names the same thing.
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || parts[2] == "" {
			continue
		}
		// Walk the path from the leaf up. Rootless podman nests a plain
		// "container" directory inside the scope, so the segment that names
		// the thing is not always the last one.
		segments := strings.Split(strings.Trim(parts[2], "/"), "/")
		for i := len(segments) - 1; i >= 0; i-- {
			segment := segments[i]
			if segment == "" {
				continue
			}
			if id, ok := containerScopeID(segment); ok {
				return "", id
			}
			// A slice is a grouping, not a unit anyone runs; it is only worth
			// naming if nothing more specific is in the path.
			for _, suffix := range []string{".service", ".scope", ".socket", ".mount"} {
				if strings.HasSuffix(segment, suffix) {
					return segment, ""
				}
			}
		}
	}
	return "", ""
}

// containerScopeID recognises the scope names podman and docker give a
// container, so a process inside one is pointed at the container rather than
// at a unit name nobody recognises.
func containerScopeID(segment string) (string, bool) {
	for _, prefix := range []string{"libpod-", "docker-", "crio-", "cri-containerd-"} {
		if rest, ok := strings.CutPrefix(segment, prefix); ok {
			// The monitor process lives beside the container in
			// libpod-conmon-<id>.scope and is not the container itself.
			if strings.HasPrefix(rest, "conmon-") {
				return "", false
			}
			return strings.TrimSuffix(rest, ".scope"), true
		}
	}
	return "", false
}
