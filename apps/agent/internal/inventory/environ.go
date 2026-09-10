package inventory

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// procPathFunc is replaceable in tests; /proc entries cannot be fabricated.
var procPathFunc = func(pid int, name string) string {
	return filepath.Join("/proc", strconv.Itoa(pid), name)
}

func procPath(pid int, name string) string { return procPathFunc(pid, name) }

// commonEnvKeys are reported with their value. They describe how a process was
// set up rather than what it may access, so they carry no credentials on a
// conventional Linux host.
var commonEnvKeys = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"PWD": true, "OLDPWD": true, "LANG": true, "LC_ALL": true, "TERM": true,
	"TZ": true, "HOSTNAME": true, "SHLVL": true, "EDITOR": true, "PAGER": true,
	"JAVA_HOME": true, "GOPATH": true, "GOROOT": true, "VIRTUAL_ENV": true,
	"PYTHONPATH": true, "NODE_ENV": true, "LD_LIBRARY_PATH": true,
	"XDG_RUNTIME_DIR": true, "SYSTEMD_EXEC_PID": true, "INVOCATION_ID": true,
}

// EnvVar is one environment entry. Value is empty when only the name is
// reported, which Redacted then marks.
type EnvVar struct {
	Key      string `json:"key"`
	Value    string `json:"value,omitempty"`
	Redacted bool   `json:"redacted,omitempty"`
}

// readEnviron reports a process's environment under a fixed policy: values are
// included only for keys known to be innocuous or explicitly allowed for this
// node. Every other key is reported by name alone, because an environment
// routinely holds database passwords, API tokens, and cloud keys, and a
// monitoring system must not become the place they are all collected.
func readEnviron(pid int, extraAllowed map[string]bool) []EnvVar {
	raw, err := os.ReadFile(procPath(pid, "environ"))
	if err != nil {
		return nil
	}
	items := []EnvVar{}
	seen := map[string]bool{}
	for _, entry := range strings.Split(string(raw), "\x00") {
		if entry == "" {
			continue
		}
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if commonEnvKeys[key] || extraAllowed[key] {
			items = append(items, EnvVar{Key: key, Value: value})
			continue
		}
		items = append(items, EnvVar{Key: key, Redacted: true})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return items
}

// readCmdline returns a process's full argument vector.
func readCmdline(pid int) []string {
	raw, err := os.ReadFile(procPath(pid, "cmdline"))
	if err != nil {
		return nil
	}
	args := []string{}
	for _, arg := range strings.Split(string(raw), "\x00") {
		if arg != "" {
			args = append(args, arg)
		}
	}
	return args
}
