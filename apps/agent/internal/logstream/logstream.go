// Package logstream follows the node's journal continuously so a failure that
// only shows up in logs is on the server before the node goes quiet.
//
// Two things leave the host. Counters cover every severity, because "is this
// normal" needs a denominator and a count costs nothing. Lines are shipped only
// for warning and worse, plus authentication activity at any severity, because
// those are the ones worth reading later.
package logstream

import (
	"bufio"
	"context"
	"encoding/json"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Severities are syslog priorities. Anything at or below shipPriority is sent
// as a line; the rest is counted only.
const (
	PriorityEmergency = 0
	PriorityAlert     = 1
	PriorityCritical  = 2
	PriorityError     = 3
	PriorityWarning   = 4
	PriorityNotice    = 5
	PriorityInfo      = 6
	PriorityDebug     = 7

	// Anything at or below shipPriority is sent as a line; the rest is counted
	// and left on the host for a journal read to fetch.
	//
	// Measured on a working host: of 402,582 journal entries in a day, 402,489
	// were info -- 99.98% of them, and 98.8% of the total was one container's
	// access log. Shipping that continuously costs a great deal and tells an
	// operator nothing. Warning is the line where a log stops describing
	// normal operation.
	shipPriority = PriorityWarning
)

// PriorityNames index by priority; used for counter keys and display.
var PriorityNames = [8]string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}

// authIdentifiers is login and account activity: who got in, who became root,
// who was added or removed. The console tabs on this list, so it holds only
// what an operator would call an access event.
//
// lastlog, wtmp, and btmp are binary databases rather than logs, so session
// history is taken from these journal identifiers instead.
var authIdentifiers = map[string]bool{
	"sshd": true, "sudo": true, "su": true, "login": true, "systemd-logind": true,
	"polkitd": true, "gdm-password": true, "sshd-session": true, "audit": true,
	"auditd": true, "useradd": true, "usermod": true, "passwd": true,
	"groupadd": true, "groupmod": true, "groupdel": true, "userdel": true,
	"chfn": true, "chsh": true, "newgrp": true,
}

// alwaysShip names what crosses regardless of severity, because severity is a
// poor proxy for importance: the program writing the line decides it, and most
// of them are careless about it.
//
// Measured on a working host over a day: every sudo session and every account
// change was logged at info, and of 39 kernel lines 30 were below warning --
// including "traps: fwupdmgr[...] trap int3", a process crash. A severity
// floor alone would have dropped all of it.
var alwaysShip = func() map[string]bool {
	units := map[string]bool{"kernel": true}
	for unit := range authIdentifiers {
		units[unit] = true
	}
	return units
}()

// Batch is one reporting window: what happened, and how much of it.
type Batch struct {
	From     time.Time      `json:"from"`
	To       time.Time      `json:"to"`
	Counters map[string]int `json:"counters"`
	Lines    []Line         `json:"lines"`
	// Cursor addresses the last journal entry this window observed. Stored
	// after the batch is accepted, it is where the next reader resumes, so an
	// agent restart leaves no gap and re-sends nothing.
	Cursor string `json:"-"`
	// Dropped counts lines the rate cap discarded, so a truncated window is
	// visibly truncated rather than quietly short.
	Dropped int `json:"dropped"`
}

// Line is one shipped log line. Repeat folds identical messages in a window.
type Line struct {
	At       time.Time `json:"at"`
	Priority int       `json:"priority"`
	Unit     string    `json:"unit"`
	Message  string    `json:"message"`
	Repeat   int       `json:"repeat,omitempty"`
	// Container is set when the line is a container's own output rather than
	// the host's. Without it a container's error reads as a host service's,
	// and the console cannot tell the two apart -- they arrive on the same
	// journal under the container's name as the syslog identifier.
	Container string `json:"container,omitempty"`
}

// Limits bound what one window can cost.
type Limits struct {
	// MaxLines is the cap per window before Dropped starts counting.
	MaxLines int
	// MaxRoutineLines caps notice and info separately. Sharing one budget lets
	// a host that chatters at info fill the window and drop the warnings and
	// errors that arrive after it, which is the opposite of what a cap is for.
	MaxRoutineLines int
	// MaxMessage truncates a single line; a stack trace in one journal entry
	// can be megabytes.
	MaxMessage int
}

// DefaultLimits keeps a brute-force burst from filling the window while leaving
// room for a genuine incident.
var DefaultLimits = Limits{MaxLines: 500, MaxRoutineLines: 500, MaxMessage: 2000}

// Redactor can transform a line before it leaves the host. Lines are shipped
// unmodified by default; secret material is masked on the server for readers
// without the raw grant, so an investigation is never missing a value that
// only existed on the node.
type Redactor func(string) string

// Collector accumulates a window. It is safe for concurrent use: Run writes,
// Flush reads and resets.
type Collector struct {
	mu       sync.Mutex
	limits   Limits
	redact   Redactor
	counters map[string]int
	lines    map[string]*Line
	order    []string
	routine  int
	dropped  int
	since    time.Time
	cursor   string
}

func NewCollector(limits Limits, redact Redactor) *Collector {
	if limits.MaxLines <= 0 {
		limits.MaxLines = DefaultLimits.MaxLines
	}
	if limits.MaxRoutineLines <= 0 {
		limits.MaxRoutineLines = DefaultLimits.MaxRoutineLines
	}
	if limits.MaxMessage <= 0 {
		limits.MaxMessage = DefaultLimits.MaxMessage
	}
	if redact == nil {
		redact = func(line string) string { return line }
	}
	return &Collector{limits: limits, redact: redact, counters: map[string]int{}, lines: map[string]*Line{}, since: time.Now().UTC()}
}

// entry is the subset of journalctl's JSON output that is requested.
type entry struct {
	Cursor     string `json:"__CURSOR"`
	Priority   string `json:"PRIORITY"`
	Message    string `json:"MESSAGE"`
	Identifier string `json:"SYSLOG_IDENTIFIER"`
	Unit       string `json:"_SYSTEMD_UNIT"`
	Comm       string `json:"_COMM"`
	Container  string `json:"CONTAINER_NAME"`
	Realtime   string `json:"__REALTIME_TIMESTAMP"`
}

// Observe records one journal entry. Every entry is counted; only shippable
// ones are kept as lines.
func (c *Collector) Observe(raw []byte) {
	var item entry
	if err := json.Unmarshal(raw, &item); err != nil {
		return
	}
	priority, err := strconv.Atoi(item.Priority)
	if err != nil || priority < 0 || priority > 7 {
		priority = PriorityInfo
	}
	unit := item.Identifier
	if unit == "" {
		unit = item.Comm
	}
	if unit == "" {
		unit = strings.TrimSuffix(item.Unit, ".service")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if item.Cursor != "" {
		c.cursor = item.Cursor
	}
	c.counters[PriorityNames[priority]]++
	// These ship at any severity, so access and kernel activity is never
	// withheld no matter where shipPriority is set.
	if priority > shipPriority && !alwaysShip[unit] {
		return
	}
	message := c.redact(item.Message)
	if len(message) > c.limits.MaxMessage {
		message = message[:c.limits.MaxMessage] + "…"
	}
	// Identical messages in one window fold into a repeat count, so a
	// brute-force burst costs one line rather than thousands.
	key := strconv.Itoa(priority) + "\x00" + unit + "\x00" + message
	if existing, ok := c.lines[key]; ok {
		existing.Repeat++
		return
	}
	// Which budget a line spends is decided by its severity alone, not by
	// whether it was allowed to cross. A unit on the always-ship list still
	// chatters at info -- a host opens sudo sessions all day -- and letting
	// that chatter spend the severe budget is exactly the crowding the two
	// budgets exist to prevent.
	if routine := priority > PriorityWarning; routine {
		if c.routine >= c.limits.MaxRoutineLines {
			c.dropped++
			return
		}
		c.routine++
	} else if len(c.lines)-c.routine >= c.limits.MaxLines {
		c.dropped++
		return
	}
	c.lines[key] = &Line{At: entryTime(item.Realtime), Priority: priority, Unit: unit, Message: message, Container: item.Container}
	c.order = append(c.order, key)
}

// Flush returns the accumulated window and starts a new one. An empty window
// still reports its counters, because zero errors is itself the measurement.
func (c *Collector) Flush(now time.Time) Batch {
	c.mu.Lock()
	defer c.mu.Unlock()
	batch := Batch{From: c.since, To: now, Counters: c.counters, Dropped: c.dropped, Cursor: c.cursor, Lines: make([]Line, 0, len(c.order))}
	for _, key := range c.order {
		batch.Lines = append(batch.Lines, *c.lines[key])
	}
	sort.Slice(batch.Lines, func(i, j int) bool { return batch.Lines[i].At.Before(batch.Lines[j].At) })
	c.counters = map[string]int{}
	c.lines = map[string]*Line{}
	c.order = nil
	c.routine = 0
	c.dropped = 0
	c.since = now
	return batch
}

// Pending is how many lines the open window holds. The reporting loop sends a
// window that has something in it on the next tick rather than waiting out the
// full interval, so an event reaches the console seconds after it happened.
func (c *Collector) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.lines)
}

// Cursor is the last journal entry observed, or empty before the first one.
func (c *Collector) Cursor() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cursor
}

// entryTime converts journald's microsecond epoch; a missing one falls back to
// arrival time rather than the zero instant.
func entryTime(value string) time.Time {
	micros, err := strconv.ParseInt(value, 10, 64)
	if err != nil || micros <= 0 {
		return time.Now().UTC()
	}
	return time.UnixMicro(micros).UTC()
}

// journalArgs requests only the fields the collector reads, which keeps the
// parse cost bounded on a host that logs heavily. __CURSOR is always included.
func journalArgs(cursor string) []string {
	args := []string{
		"--follow", "--output=json", "--no-pager", "--quiet",
		"--output-fields=PRIORITY,MESSAGE,SYSLOG_IDENTIFIER,_SYSTEMD_UNIT,_COMM,CONTAINER_NAME,__REALTIME_TIMESTAMP",
	}
	if cursor == "" {
		return append(args, "--since=now")
	}
	return append(args, "--after-cursor="+cursor)
}

// Run follows the journal from cursor until ctx is done, restarting the reader
// if it stops. An empty cursor starts at the present. A host without journalctl
// reports counters of zero rather than failing.
func Run(ctx context.Context, collector *Collector, cursor string) {
	for ctx.Err() == nil {
		err := follow(ctx, collector, cursor)
		if latest := collector.Cursor(); latest != "" {
			cursor = latest
		} else if err != nil && cursor != "" {
			// The journal no longer holds this entry -- rotated or vacuumed --
			// so seeking to it fails every time. Start at the present instead.
			cursor = ""
			continue
		}
		if err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-time.After(30 * time.Second):
			}
		}
	}
}

func follow(ctx context.Context, collector *Collector, cursor string) error {
	command := exec.CommandContext(ctx, "journalctl", journalArgs(cursor)...)
	output, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		collector.Observe(scanner.Bytes())
	}
	return command.Wait()
}
