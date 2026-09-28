package inventory

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Host struct {
	Hostname    string             `json:"hostname"`
	MachineID   string             `json:"machineId,omitempty"`
	OS          string             `json:"os"`
	Kernel      string             `json:"kernel"`
	Arch        string             `json:"arch"`
	CPUCount    int                `json:"cpuCount"`
	MemoryBytes uint64             `json:"memoryBytes"`
	Disks       []Disk             `json:"disks"`
	Interfaces  []NetworkInterface `json:"interfaces"`
	Services    []Service          `json:"services"`
	VMs         []VirtualMachine   `json:"vms"`
	Containers  []Container        `json:"containers"`
	Processes   []Process          `json:"processes"`
}

type Disk struct {
	Name      string `json:"name"`
	Model     string `json:"model,omitempty"`
	SizeBytes uint64 `json:"sizeBytes"`
}
type NetworkInterface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac,omitempty"`
	Addresses []string `json:"addresses"`
	MTU       int      `json:"mtu"`
	Flags     string   `json:"flags"`
}
type Service struct {
	Name   string `json:"name"`
	Load   string `json:"load"`
	Active string `json:"active"`
	Sub    string `json:"sub"`
}

// VirtualMachine holds the hypervisor's view of a guest. MemoryBytes is what
// the host assigned; MemoryUsedBytes is what the guest reports through the
// balloon driver and is absent without it. Guest filesystem usage and guest
// processes are not visible from here at all.
type VirtualMachine struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	VCPUs        int    `json:"vcpus,omitempty"`
	CPUTimeNanos uint64 `json:"cpuTimeNanos,omitempty"`
	// MemoryBytes is what the host assigned the guest, and MemoryUsedBytes what
	// the guest reports using. The second needs a balloon driver in the guest;
	// without one it stays zero rather than being filled with a number that
	// means something else.
	MemoryBytes     uint64 `json:"memoryBytes,omitempty"`
	MemoryUsedBytes uint64 `json:"memoryUsedBytes,omitempty"`
	// HostMemoryBytes is the emulator's resident size on the host, which counts
	// the emulator itself and can exceed the memory the guest was given. It is
	// what the VM costs the host, not what the guest is using.
	HostMemoryBytes uint64 `json:"hostMemoryBytes,omitempty"`
	DiskReadBytes   uint64 `json:"diskReadBytes,omitempty"`
	DiskWriteBytes  uint64 `json:"diskWriteBytes,omitempty"`
	NetworkRxBytes  uint64 `json:"networkRxBytes,omitempty"`
	NetworkTxBytes  uint64 `json:"networkTxBytes,omitempty"`
}
type Container struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Image   string `json:"image,omitempty"`
	State   string `json:"state"`
	Runtime string `json:"runtime"`
}
type Process struct {
	PID         int      `json:"pid"`
	Name        string   `json:"name"`
	State       string   `json:"state"`
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
	Environment []EnvVar `json:"environment,omitempty"`
	RSSBytes    uint64   `json:"rssBytes"`
	// What the process belongs to, read from its cgroup. Only a few dozen
	// processes on a host get their own metrics, so the rest need somewhere
	// to send an operator looking for a trend.
	Unit        string `json:"unit,omitempty"`
	ContainerID string `json:"containerId,omitempty"`
}

func Collect() Host {
	hostname, _ := os.Hostname()
	return Host{Hostname: hostname, MachineID: readTrimmed("/etc/machine-id"), OS: osName(), Kernel: kernelVersion(), Arch: runtime.GOARCH, CPUCount: runtime.NumCPU(), MemoryBytes: totalMemory(), Disks: disks(), Interfaces: interfaces(), Services: services(), VMs: virtualMachines(), Containers: containers(), Processes: processes(processLimit)}
}

func osName() string {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok && key == "PRETTY_NAME" {
			return strings.Trim(value, `"`)
		}
	}
	return runtime.GOOS
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func kernelVersion() string {
	var info syscall.Utsname
	if syscall.Uname(&info) != nil {
		return ""
	}
	bytes := make([]byte, 0, len(info.Release))
	for _, value := range info.Release {
		if value == 0 {
			break
		}
		bytes = append(bytes, byte(value))
	}
	return string(bytes)
}

func totalMemory() uint64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			value, _ := strconv.ParseUint(fields[1], 10, 64)
			return value * 1024
		}
	}
	return 0
}

func disks() []Disk {
	paths, _ := filepath.Glob("/sys/block/*")
	items := []Disk{}
	for _, path := range paths {
		name := filepath.Base(path)
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
			continue
		}
		sectors, _ := strconv.ParseUint(readTrimmed(filepath.Join(path, "size")), 10, 64)
		items = append(items, Disk{Name: name, Model: readTrimmed(filepath.Join(path, "device/model")), SizeBytes: sectors * 512})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func interfaces() []NetworkInterface {
	values, err := net.Interfaces()
	if err != nil {
		return nil
	}
	items := make([]NetworkInterface, 0, len(values))
	for _, value := range values {
		addresses, _ := value.Addrs()
		list := make([]string, 0, len(addresses))
		for _, address := range addresses {
			list = append(list, address.String())
		}
		items = append(items, NetworkInterface{Name: value.Name, MAC: value.HardwareAddr.String(), Addresses: list, MTU: value.MTU, Flags: value.Flags.String()})
	}
	return items
}

// virshArgs prefixes a virsh invocation with the connection to use. An agent
// runs as its own unprivileged account, and virsh with no connection named
// picks qemu:///session — a per-user daemon that has never been asked to run
// anything. It answers "no domains" successfully, so a host full of virtual
// machines reads as a host with none. The system daemon is the one that has
// them. LIBVIRT_DEFAULT_URI still wins where an operator has set it.
func virshArgs(args ...string) []string {
	if os.Getenv("LIBVIRT_DEFAULT_URI") != "" {
		return args
	}
	return append([]string{"--connect", "qemu:///system"}, args...)
}

func commandOutput(name string, args ...string) string {
	if _, err := exec.LookPath(name); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		// A command that is installed and then fails is a host the agent
		// cannot read, which looks exactly like a host with nothing on it once
		// the output is empty. Saying so once per reason is the difference
		// between an empty list and an empty list nobody knew was wrong.
		reportCommandFailure(name, err)
		return ""
	}
	return string(output)
}

var (
	reportedFailures   = map[string]bool{}
	reportedFailuresMu sync.Mutex
)

// reportCommandFailure logs the first failure of each command, with whatever
// the command wrote to stderr. Every beat repeats the same collection, so the
// log would otherwise be the same line forever.
func reportCommandFailure(name string, err error) {
	detail := err.Error()
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		detail = strings.TrimSpace(string(exit.Stderr))
	}
	reportedFailuresMu.Lock()
	seen := reportedFailures[name]
	reportedFailures[name] = true
	reportedFailuresMu.Unlock()
	if !seen {
		slog.Warn("collection command failed; what it reads will look empty", "command", name, "error", detail)
	}
}

func services() []Service {
	output := commandOutput("systemctl", "list-units", "--type=service", "--all", "--no-legend", "--no-pager", "--plain")
	items := []Service{}
	for _, line := range strings.Split(output, "\n") {
		if service, ok := parseServiceLine(line); ok {
			items = append(items, service)
		}
	}
	return items
}

// parseServiceLine reads one list-units row. systemctl marks failed and
// not-found units with a leading glyph, which shifts every column and leaves
// the unit nameless -- exactly the units worth reading. --plain suppresses it;
// this drops it too, for systemd versions that lack the flag.
func parseServiceLine(line string) (Service, bool) {
	fields := strings.Fields(line)
	if len(fields) > 0 && !strings.Contains(fields[0], ".") {
		fields = fields[1:]
	}
	if len(fields) < 4 {
		return Service{}, false
	}
	return Service{Name: fields[0], Load: fields[1], Active: fields[2], Sub: fields[3]}, true
}

func virtualMachines() []VirtualMachine {
	// domstats carries the resource figures; fall back to the name list when
	// libvirt is present but stats are unavailable.
	if stats := parseDomstats(commandOutput("virsh", virshArgs("domstats", "--raw")...)); len(stats) > 0 {
		items := make([]VirtualMachine, 0, len(stats))
		for _, vm := range stats {
			items = append(items, vm)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		return items
	}
	items := []VirtualMachine{}
	for _, name := range strings.Split(commandOutput("virsh", virshArgs("list", "--all", "--name")...), "\n") {
		if name = strings.TrimSpace(name); name != "" {
			items = append(items, VirtualMachine{Name: name, State: "unknown"})
		}
	}
	return items
}

func containers() []Container {
	items := []Container{}
	seen := map[string]bool{}
	for _, runtimeName := range []string{"docker", "podman", "nerdctl"} {
		output := commandOutput(runtimeName, "ps", "-a", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.State}}")
		for _, item := range parseFormattedContainers(output, runtimeName) {
			if !seen[item.ID] {
				items = append(items, item)
				seen[item.ID] = true
			}
		}
	}
	output := commandOutput("ctr", "containers", "list", "-q")
	for _, id := range strings.Split(output, "\n") {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			items = append(items, Container{ID: id, Name: id, Runtime: "containerd", State: "discovered"})
			seen[id] = true
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func parseFormattedContainers(output, runtimeName string) []Container {
	items := []Container{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 4 && strings.TrimSpace(fields[0]) != "" {
			items = append(items, Container{ID: fields[0], Name: fields[1], Image: fields[2], State: fields[3], Runtime: runtimeName})
		}
	}
	return items
}

// processLimit guards against a runaway process table; below it the whole
// table is reported.
const processLimit = 5000

func processes(limit int) []Process {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	items := []Process{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		process, ok := readProcess(pid)
		if ok {
			items = append(items, process)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].RSSBytes > items[j].RSSBytes })
	// A limit of zero reports every process. The cap is a guard against a
	// runaway host, not a sampling policy; heaviest first, so a truncated
	// report still carries what matters.
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func readProcess(pid int) (Process, bool) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return Process{}, false
	}
	process := Process{PID: pid}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Name":
			process.Name = value
		case "State":
			process.State = value
		case "VmRSS":
			fields := strings.Fields(value)
			if len(fields) > 0 {
				rss, _ := strconv.ParseUint(fields[0], 10, 64)
				process.RSSBytes = rss * 1024
			}
		}
	}
	process.Unit, process.ContainerID = processUnit(pid)
	command, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	process.Command = executableFromCmdline(command)
	process.Args = readCmdline(pid)
	if enabled, allowed := envPolicy.snapshot(); enabled {
		process.Environment = readEnviron(pid, allowed)
	}
	return process, true
}

func executableFromCmdline(command []byte) string {
	executable, _, _ := strings.Cut(string(command), "\x00")
	return strings.TrimSpace(executable)
}
