package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// logSources are the only sources an agent will read. Each is a fixed selector
// or path, so a request cannot name an arbitrary file.
//
// A source with no selector reads the whole journal, so every source needs one
// of its own or the tabs built on them all return the same text.
var logSources = map[string]struct {
	dmesg      bool     // kernel ring buffer
	facilities string   // syslog facilities, comma separated
	files      []string // fallbacks, in order, when journald is absent
}{
	// Everything a host logs except the login activity `auth` covers, which is
	// what /var/log/syslog holds on a distribution that still writes one.
	"syslog": {
		facilities: "kern,user,mail,daemon,syslog,lpr,news,uucp,cron",
		files:      []string{"/var/log/syslog", "/var/log/messages"},
	},
	// Logins, sudo, and PAM. Debian calls the file auth.log and RHEL secure.
	"auth": {
		facilities: "auth,authpriv",
		files:      []string{"/var/log/auth.log", "/var/log/secure"},
	},
	"kernel":  {dmesg: true, files: []string{"/var/log/kern.log"}},
	"journal": {},
}

const (
	logMaxLines = 5000
	logMaxBytes = 2 << 20
	logTimeout  = 20 * time.Second
)

// CaptureLogs returns lines from one allowed source within a time window. It
// reads only; nothing on the host is modified.
func (e *Executor) CaptureLogs(source, since, until string, lines int) (string, error) {
	spec, ok := logSources[source]
	if !ok {
		return "", fmt.Errorf("log source %q is not available", source)
	}
	if lines <= 0 || lines > logMaxLines {
		lines = logMaxLines
	}
	ctx, cancel := context.WithTimeout(context.Background(), logTimeout)
	defer cancel()

	if output, err := e.captureJournal(ctx, spec.dmesg, spec.facilities, since, until, lines); err == nil {
		return output, nil
	}
	for _, path := range spec.files {
		if output, err := captureFile(path, lines); err == nil {
			return output, nil
		}
	}
	return "", errors.New("journald is unavailable and no fallback file for this source could be read")
}

// journalArgs is separate from running it so a source's selector can be
// asserted. Two sources that build the same arguments are one source wearing
// two names, which is how the console ends up with tabs that agree.
func journalArgs(dmesg bool, facilities, since, until string, lines int) []string {
	args := []string{"--no-pager", "--output=short-iso", "--lines=" + strconv.Itoa(lines)}
	if dmesg {
		args = append(args, "--dmesg")
	}
	if facilities != "" {
		args = append(args, "--facility="+facilities)
	}
	if since != "" {
		args = append(args, "--since="+since)
	}
	if until != "" {
		args = append(args, "--until="+until)
	}
	return args
}

func (e *Executor) captureJournal(ctx context.Context, dmesg bool, facilities, since, until string, lines int) (string, error) {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return "", err
	}
	args := journalArgs(dmesg, facilities, since, until, lines)
	output, err := exec.CommandContext(ctx, "journalctl", args...).Output()
	if err != nil {
		return "", err
	}
	return redactAndCap(string(output)), nil
}

func captureFile(path string, lines int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	ring := make([]string, 0, lines)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if len(ring) == lines {
			ring = ring[1:]
		}
		ring = append(ring, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return redactAndCap(strings.Join(ring, "\n")), nil
}

func redactAndCap(output string) string {
	var builder strings.Builder
	for _, line := range strings.Split(output, "\n") {
		if builder.Len() >= logMaxBytes {
			builder.WriteString("\n[truncated]")
			break
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}
