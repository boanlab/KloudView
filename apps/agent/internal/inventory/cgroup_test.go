package inventory

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// writeCgroup builds one container directory in the layout systemd's cgroup
// driver produces.
func writeCgroup(t *testing.T, root, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, "system.slice", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, content := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func withRoot(t *testing.T, root string) {
	t.Helper()
	previous := cgroupRoot
	cgroupRoot = root
	t.Cleanup(func() {
		cgroupRoot = previous
		cpuMu.Lock()
		lastCPU = map[string]cpuReading{}
		cpuMu.Unlock()
	})
}

func TestCgroupStatsReadV2(t *testing.T) {
	root := t.TempDir()
	withRoot(t, root)
	full := "d4a3340c5323d2dcc21cb2326595b9cfdacc9b83a9ff8d2c90b1abc653d08d12"
	writeCgroup(t, root, "docker-"+full+".scope", map[string]string{
		"cpu.stat":       "usage_usec 2287970\nuser_usec 1728621\nsystem_usec 559349\n",
		"memory.current": "68141056\n",
		"memory.max":     "134217728\n",
		"io.stat":        "8:0 rbytes=1024 wbytes=2048 rios=3 wios=4\n8:16 rbytes=512 wbytes=256\n",
		"pids.current":   "38\n",
	})

	// The runtime reports a short ID; the cgroup carries the full one.
	stats := ContainerCgroupStats([]string{full[:12]})
	stat, ok := stats[full[:12]]
	if !ok {
		t.Fatalf("short id did not match the full cgroup name: %+v", stats)
	}
	if stat.MemoryBytes != 68141056 || stat.MemoryLimitBytes != 134217728 {
		t.Fatalf("memory = %+v", stat)
	}
	if want := 68141056.0 / 134217728.0 * 100; stat.MemoryPercent < want-0.01 || stat.MemoryPercent > want+0.01 {
		t.Fatalf("memory percent = %v, want %v", stat.MemoryPercent, want)
	}
	// Every device is summed, matching how the node's own disk counters read.
	if stat.DiskReadBytes != 1536 || stat.DiskWriteBytes != 2304 {
		t.Fatalf("io = %d read, %d write", stat.DiskReadBytes, stat.DiskWriteBytes)
	}
	if stat.Processes != 38 {
		t.Fatalf("processes = %d", stat.Processes)
	}
	// A cumulative counter has no rate until there is something to compare to.
	if stat.CPUPercent != 0 {
		t.Fatalf("first reading produced a rate: %v", stat.CPUPercent)
	}
}

func TestCgroupCPUPercentUsesTheDelta(t *testing.T) {
	root := t.TempDir()
	withRoot(t, root)
	id := "aaaaaaaaaaaa0000"
	writeCgroup(t, root, "docker-"+id+".scope", map[string]string{"cpu.stat": "usage_usec 0\n"})

	now := time.Now()
	if got := cpuPercent(id, 0, now); got != 0 {
		t.Fatalf("first reading = %v", got)
	}
	// One core fully busy for one second, expressed as a share of the host.
	cores := float64(runtime.NumCPU())
	got := cpuPercent(id, 1_000_000, now.Add(time.Second))
	if want := 100 / cores; got < want-0.5 || got > want+0.5 {
		t.Fatalf("cpu percent = %v, want about %v", got, want)
	}
	// A counter that went backwards means the container restarted.
	if got := cpuPercent(id, 10, now.Add(2*time.Second)); got != 0 {
		t.Fatalf("counter reset produced a rate: %v", got)
	}
}

func TestCgroupStatsReadV1(t *testing.T) {
	root := t.TempDir()
	withRoot(t, root)
	id := "bbbbbbbbbbbb1111"
	writeCgroup(t, root, id, map[string]string{
		"cpuacct.usage":                   "5000000000\n",
		"memory.usage_in_bytes":           "1048576\n",
		"memory.limit_in_bytes":           "9223372036854771712\n",
		"blkio.throttle.io_service_bytes": "8:0 Read 100\n8:0 Write 200\n8:0 Sync 300\nTotal 600\n",
	})

	stats := ContainerCgroupStats([]string{id})
	stat, ok := stats[id]
	if !ok {
		t.Fatalf("v1 layout not read: %+v", stats)
	}
	if stat.MemoryBytes != 1048576 {
		t.Fatalf("memory = %d", stat.MemoryBytes)
	}
	// The v1 "unlimited" sentinel is a limit near the word size, not a real cap.
	if stat.MemoryLimitBytes != 0 {
		t.Fatalf("unlimited container reported a limit of %d", stat.MemoryLimitBytes)
	}
	if stat.DiskReadBytes != 100 || stat.DiskWriteBytes != 200 {
		t.Fatalf("io = %d read, %d write", stat.DiskReadBytes, stat.DiskWriteBytes)
	}
}

func TestCgroupStatsOmitsContainersWithoutACgroup(t *testing.T) {
	root := t.TempDir()
	withRoot(t, root)
	writeCgroup(t, root, "docker-cccccccccccc.scope", map[string]string{"cpu.stat": "usage_usec 1\n"})

	stats := ContainerCgroupStats([]string{"cccccccccccc", "dddddddddddd"})
	if _, ok := stats["dddddddddddd"]; ok {
		t.Fatal("a container with no cgroup was reported as zero rather than omitted")
	}
	if _, ok := stats["cccccccccccc"]; !ok {
		t.Fatalf("the present container was dropped: %+v", stats)
	}
}
