package identity

import (
	"os"
	"strings"
)

// machineIDPaths hold an id the distribution generates once when the system is
// installed. It outlives the agent, so a host that is set up again is still
// recognisably the same host, and it differs between two machines that happen
// to share a hostname.
var machineIDPaths = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

// Machine returns this host's machine id, or "" where the system does not keep
// one. Empty is not an error: it means the server cannot tell this host apart
// from another of the same name, which is what it did before any host reported
// one at all.
func Machine() string {
	for _, path := range machineIDPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if id := strings.TrimSpace(string(data)); id != "" {
			return id
		}
	}
	return ""
}
