package inventory

import (
	"strconv"
	"strings"
)

// parseDomstats reads `virsh domstats --raw` output into per-domain figures.
// Everything here is the hypervisor's view: how much the host spends on a
// guest. It says nothing about the guest's own filesystem or processes.
func parseDomstats(output string) map[string]VirtualMachine {
	domains := map[string]VirtualMachine{}
	unused := map[string]uint64{}
	current := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if name, ok := strings.CutPrefix(line, "Domain: "); ok {
			current = strings.Trim(name, "'\"")
			domains[current] = VirtualMachine{Name: current, State: "unknown"}
			continue
		}
		if current == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vm := domains[current]
		number, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		switch {
		case key == "state.state":
			vm.State = domainState(number)
		case key == "cpu.time":
			// Nanoseconds of host CPU burnt on this guest since it started.
			vm.CPUTimeNanos = number
		case key == "vcpu.current":
			vm.VCPUs = int(number)
		case key == "balloon.current":
			// KiB the host has assigned to the guest.
			vm.MemoryBytes = number * 1024
		case key == "balloon.unused":
			// What the guest says it is not using. Guest usage is the
			// assignment less this, and libvirt reports it only when the guest
			// runs a balloon driver.
			unused[current] = number * 1024
		case key == "balloon.rss":
			// The emulator's resident size on the host. This was being read as
			// the guest's memory use, which it is not: it includes the
			// emulator and routinely exceeds the memory the guest was given,
			// so a gauge built on it read over 100%.
			vm.HostMemoryBytes = number * 1024
		case strings.HasPrefix(key, "block.") && strings.HasSuffix(key, ".rd.bytes"):
			vm.DiskReadBytes += number
		case strings.HasPrefix(key, "block.") && strings.HasSuffix(key, ".wr.bytes"):
			vm.DiskWriteBytes += number
		case strings.HasPrefix(key, "net.") && strings.HasSuffix(key, ".rx.bytes"):
			vm.NetworkRxBytes += number
		case strings.HasPrefix(key, "net.") && strings.HasSuffix(key, ".tx.bytes"):
			vm.NetworkTxBytes += number
		}
		domains[current] = vm
	}
	for name, free := range unused {
		vm := domains[name]
		if vm.MemoryBytes > free {
			vm.MemoryUsedBytes = vm.MemoryBytes - free
			domains[name] = vm
		}
	}
	return domains
}

// domainState maps libvirt's numeric domain state.
func domainState(value uint64) string {
	switch value {
	case 1:
		return "running"
	case 3:
		return "paused"
	case 4:
		return "shutting down"
	case 5:
		return "shut off"
	case 6:
		return "crashed"
	case 7:
		return "suspended"
	default:
		return "unknown"
	}
}
