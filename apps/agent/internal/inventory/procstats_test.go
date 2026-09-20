package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeProcess lays out the parts of /proc this reads, for one process.
func writeProcess(t *testing.T, root string, pid int, name string, ticks, startTime, rssPages uint64) {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprint(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}
	// Fields after the name: state, ppid, pgrp, session, tty, tpgid, flags,
	// minflt, cminflt, majflt, cmajflt, utime, stime, cutime, cstime,
	// priority, nice, num_threads, itrealvalue, starttime, vsize, rss.
	fields := []any{
		"S", 1, 1, 1, 0, -1, 0, 0, 0, 0, 0,
		ticks, 0, 0, 0,
		20, 0, 4, 0,
		startTime, 0, rssPages,
	}
	line := fmt.Sprintf("%d (%s)", pid, name)
	for _, field := range fields {
		line += fmt.Sprintf(" %v", field)
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("write stat: %v", err)
	}
}

func useFakeProc(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stat"), []byte("btime 1700000000\n"), 0o644); err != nil {
		t.Fatalf("write /proc/stat: %v", err)
	}
	previousRoot, previousNow := procRoot, procNow
	procRoot = root
	t.Cleanup(func() {
		procRoot, procNow = previousRoot, previousNow
		procMu.Lock()
		lastProc = map[processKey]processReading{}
		procMu.Unlock()
	})
	procMu.Lock()
	lastProc = map[processKey]processReading{}
	procMu.Unlock()
	return root
}

func find(items []ProcessStats, pid int) (ProcessStats, bool) {
	for _, item := range items {
		if item.PID == pid {
			return item, true
		}
	}
	return ProcessStats{}, false
}

// The executable name sits in parentheses and is whatever the program called
// itself. Splitting the line on spaces would put the CPU counters in the wrong
// columns for any process with a space or a bracket in its name.
func TestAProcessNamedWithSpacesAndBracketsStillParses(t *testing.T) {
	root := useFakeProc(t)
	writeProcess(t, root, 42, "my (odd) name", 500, 1000, 256)

	items := ProcessStatistics(0)
	item, ok := find(items, 42)
	if !ok {
		t.Fatal("the process was not read at all")
	}
	if item.Name != "my (odd) name" {
		t.Errorf("name = %q", item.Name)
	}
	if item.State != "S" {
		t.Errorf("state = %q, want the field after the name", item.State)
	}
	if item.Threads != 4 {
		t.Errorf("threads = %d, want 4", item.Threads)
	}
	if want := 256 * uint64(os.Getpagesize()); item.MemoryBytes != want {
		t.Errorf("memory = %d, want %d", item.MemoryBytes, want)
	}
}

func TestCPUNeedsTwoReadings(t *testing.T) {
	root := useFakeProc(t)
	base := time.Unix(1700001000, 0)
	procNow = func() time.Time { return base }
	writeProcess(t, root, 7, "busy", 1000, 50, 10)

	first, _ := find(ProcessStatistics(0), 7)
	if first.CPUPercent != 0 {
		t.Fatalf("a single sample produced %.2f%%, which was invented", first.CPUPercent)
	}

	// One second later, one second of CPU burnt.
	procNow = func() time.Time { return base.Add(time.Second) }
	writeProcess(t, root, 7, "busy", 1000+userHZ, 50, 10)
	second, _ := find(ProcessStatistics(0), 7)
	want := 100.0 / float64(hostCores())
	if diff := second.CPUPercent - want; diff > 0.01 || diff < -0.01 {
		t.Fatalf("cpu = %.2f%%, want %.2f%% (one core's worth, shared across %d)", second.CPUPercent, want, hostCores())
	}
}

// Linux hands process IDs out again. Two unrelated programs sharing an ID must
// not be joined into one counter, or the second one appears to have burnt
// everything the first one ever did.
func TestAReusedProcessIDStartsOver(t *testing.T) {
	root := useFakeProc(t)
	base := time.Unix(1700002000, 0)
	procNow = func() time.Time { return base }
	writeProcess(t, root, 9, "first", 900000, 100, 10)
	ProcessStatistics(0)

	// Same ID, different start time: a different program.
	procNow = func() time.Time { return base.Add(time.Second) }
	writeProcess(t, root, 9, "second", 5, 999, 10)
	again, _ := find(ProcessStatistics(0), 9)
	if again.CPUPercent != 0 {
		t.Fatalf("a reused process ID reported %.2f%% inherited from its predecessor", again.CPUPercent)
	}
}

func TestMemoryIsAShareOfTheHost(t *testing.T) {
	root := useFakeProc(t)
	pages := uint64(1024)
	writeProcess(t, root, 11, "holder", 0, 1, pages)
	total := pages * uint64(os.Getpagesize()) * 4 // the process holds a quarter
	item, _ := find(ProcessStatistics(total), 11)
	if diff := item.MemoryPercent - 25; diff > 0.01 || diff < -0.01 {
		t.Fatalf("memory = %.2f%%, want 25%%", item.MemoryPercent)
	}
}

// Either list alone misses a kind of trouble: a busy process that holds
// nothing, or a leak that never burns a cycle.
func TestTheHeaviestByCPUAndByMemoryBothSurvive(t *testing.T) {
	items := make([]ProcessStats, 0, processSampleLimit*4)
	for i := 0; i < processSampleLimit*4; i++ {
		items = append(items, ProcessStats{PID: i, CPUPercent: float64(i), MemoryBytes: uint64(processSampleLimit*4 - i)})
	}
	picked := heaviest(items)
	if _, ok := find(picked, processSampleLimit*4-1); !ok {
		t.Error("the busiest process was dropped")
	}
	if _, ok := find(picked, 0); !ok {
		t.Error("the largest process was dropped")
	}
	if len(picked) > processSampleLimit*2 {
		t.Errorf("picked %d, more than the two lists can hold", len(picked))
	}
}

func TestAProcessKnowsWhatItBelongsTo(t *testing.T) {
	root := useFakeProc(t)
	write := func(pid int, cgroup string) {
		dir := filepath.Join(root, fmt.Sprint(pid))
		_ = os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte(cgroup), 0o644); err != nil {
			t.Fatalf("write cgroup: %v", err)
		}
	}
	write(1, "0::/system.slice/getty@tty1.service\n")
	if unit, container := processUnit(1); unit != "getty@tty1.service" || container != "" {
		t.Errorf("unit = %q container = %q", unit, container)
	}
	write(2, "0::/machine.slice/libpod-abc123def456.scope\n")
	if unit, container := processUnit(2); container != "abc123def456" || unit != "" {
		t.Errorf("unit = %q container = %q", unit, container)
	}
	// Rootless podman nests a plain "container" directory inside the scope, so
	// the segment that names the container is not the last one in the path.
	write(5, "0::/user.slice/user-1000.slice/user@1000.service/user.slice/libpod-3b5d128fcaa5.scope/container\n")
	if unit, container := processUnit(5); container != "3b5d128fcaa5" || unit != "" {
		t.Errorf("rootless container: unit = %q container = %q", unit, container)
	}
	// The monitor beside a container is not the container.
	write(3, "0::/machine.slice/libpod-conmon-abc123.scope\n")
	if _, container := processUnit(3); container != "" {
		t.Errorf("the container monitor was mistaken for the container: %q", container)
	}
	write(4, "")
	if unit, container := processUnit(4); unit != "" || container != "" {
		t.Errorf("an empty cgroup produced unit %q container %q", unit, container)
	}
}

func TestStartTimeBecomesAWallClock(t *testing.T) {
	// A restart is only visible as a restart if the kernel's "ticks since
	// boot" is turned back into a time of day.
	boot := time.Unix(1700000000, 0)
	if got := processStartedAt(boot, 200); got != "2023-11-14T22:13:22Z" {
		t.Errorf("startedAt = %q, want two seconds after boot", got)
	}
	if got := processStartedAt(time.Time{}, 200); got != "" {
		t.Errorf("an unknown boot time produced %q rather than nothing", got)
	}
}
