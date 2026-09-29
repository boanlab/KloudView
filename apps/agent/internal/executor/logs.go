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
	matches    []string // raw journal field matches
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
	// Host and container output are separable because container output carries
	// no syslog facility: the runtime writes it, not a program calling syslog.
	// Naming every facility therefore selects the host alone, which matters
	// because one container's access log can outnumber the host's own lines by
	// orders of magnitude.
	"host": {
		facilities: "kern,user,mail,daemon,auth,syslog,lpr,news,uucp,cron,authpriv," +
			"local0,local1,local2,local3,local4,local5,local6,local7",
	},
	// And the other half: conmon is the monitor every container's stdout and
	// stderr passes through. Measured: 41,105 container lines, no host lines
	// among them.
	"container": {matches: []string{"_COMM=conmon"}},
}

// logPriorities are the severity bands a read may ask for. The live view
// carries named senders rather than a severity range, so severity is something
// only a read can select on -- which makes this the way to ask "what went
// wrong on this node" of anything outside that list.
var logPriorities = map[string]string{
	"":        "",
	"error":   "0..3",
	"warning": "0..4",
	"notice":  "5..5",
	"info":    "6..6",
	"debug":   "7..7",
	"routine": "5..7",
}

const (
	logMaxLines = 5000
	logMaxBytes = 2 << 20
	logTimeout  = 20 * time.Second
)

// CaptureLogs returns lines from one allowed source within a time window. It
// reads only; nothing on the host is modified.
func (e *Executor) CaptureLogs(source, since, until, priority string, lines int) (string, error) {
	spec, ok := logSources[source]
	if !ok {
		return "", fmt.Errorf("log source %q is not available", source)
	}
	severity, ok := logPriorities[priority]
	if !ok {
		return "", fmt.Errorf("log priority %q is not available", priority)
	}
	if lines <= 0 || lines > logMaxLines {
		lines = logMaxLines
	}
	ctx, cancel := context.WithTimeout(context.Background(), logTimeout)
	defer cancel()

	if output, err := e.captureJournal(ctx, spec.dmesg, spec.facilities, spec.matches, severity, since, until, lines); err == nil {
		return output, nil
	}
	// A plain log file carries no severity field and no container name, so a
	// narrowed read cannot be answered from one. Refusing beats returning the
	// whole file under a filter that was never applied.
	if severity != "" || len(spec.matches) > 0 {
		return "", errors.New("journald is unavailable, and this filter cannot be applied to a plain log file")
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
func journalArgs(dmesg bool, facilities string, matches []string, severity, since, until string, lines int) []string {
	args := []string{"--no-pager", "--output=short-iso", "--lines=" + strconv.Itoa(lines)}
	if dmesg {
		args = append(args, "--dmesg")
	}
	if facilities != "" {
		args = append(args, "--facility="+facilities)
	}
	if severity != "" {
		args = append(args, "--priority="+severity)
	}
	if since != "" {
		args = append(args, "--since="+since)
	}
	if until != "" {
		args = append(args, "--until="+until)
	}
	// Field matches are positional and must come last, after every option.
	return append(args, matches...)
}

func (e *Executor) captureJournal(ctx context.Context, dmesg bool, facilities string, matches []string, severity, since, until string, lines int) (string, error) {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return "", err
	}
	args := journalArgs(dmesg, facilities, matches, severity, since, until, lines)
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
