package metrics

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"syscall"
)

type Sample struct {
	CPU, Memory, Disk    float64
	NetworkRx, NetworkTx uint64
}

type Collector struct {
	total uint64
	idle  uint64
}

func NewCollector() *Collector {
	total, idle := cpuCounters()
	return &Collector{total: total, idle: idle}
}

func (c *Collector) Collect() Sample {
	total, idle := cpuCounters()
	cpu := percentFromCounters(c.total, c.idle, total, idle)
	c.total, c.idle = total, idle
	return Sample{CPU: cpu, Memory: memoryPercent(), Disk: diskPercent(), NetworkRx: networkBytes(0), NetworkTx: networkBytes(8)}
}

func cpuCounters() (uint64, uint64) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(strings.SplitN(string(data), "\n", 2)[0])
	if len(fields) < 8 {
		return 0, 0
	}
	var total uint64
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, _ := strconv.ParseUint(field, 10, 64)
		values = append(values, value)
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return total, idle
}

func percentFromCounters(previousTotal, previousIdle, total, idle uint64) float64 {
	if previousTotal == 0 || total <= previousTotal || idle < previousIdle {
		return 0
	}
	totalDelta := total - previousTotal
	idleDelta := idle - previousIdle
	if idleDelta >= totalDelta {
		return 0
	}
	return float64(totalDelta-idleDelta) / float64(totalDelta) * 100
}

func memoryPercent() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	return memoryPercentFromData(string(data))
}

func memoryPercentFromData(data string) float64 {
	values := map[string]float64{}
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 {
			values[strings.TrimSuffix(fields[0], ":")], _ = strconv.ParseFloat(fields[1], 64)
		}
	}
	if values["MemTotal"] == 0 {
		return 0
	}
	available := values["MemAvailable"]
	if available == 0 {
		available = values["MemFree"] + values["Buffers"] + values["Cached"] + values["SReclaimable"] - values["Shmem"]
	}
	if available < 0 {
		available = 0
	}
	if available > values["MemTotal"] {
		available = values["MemTotal"]
	}
	return (values["MemTotal"] - available) / values["MemTotal"] * 100
}

func diskPercent() float64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil || stat.Blocks == 0 {
		return 0
	}
	return float64(stat.Blocks-stat.Bavail) / float64(stat.Blocks) * 100
}

func networkBytes(index int) uint64 {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0
	}
	var total uint64
	for _, line := range strings.Split(string(data), "\n")[2:] {
		fields := strings.Fields(strings.Replace(line, ":", " ", 1))
		if len(fields) > index+1 && fields[0] != "lo" {
			value, _ := strconv.ParseUint(fields[index+1], 10, 64)
			total += value
		}
	}
	return total
}
