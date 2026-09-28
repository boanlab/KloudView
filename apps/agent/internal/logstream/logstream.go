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

// Severities are syslog priorities, used for counting and for display.
const (
	PriorityEmergency = 0
	PriorityAlert     = 1
	PriorityCritical  = 2
	PriorityError     = 3
	PriorityWarning   = 4
	PriorityNotice    = 5
	PriorityInfo      = 6
	PriorityDebug     = 7
)

// PriorityNames index by priority; used for counter keys and display.
var PriorityNames = [8]string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}

// authIdentifiers is login and account activity: who got in, who became root,
// who was added or removed.
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

// streamed is everything the live view carries: an allowlist of senders, not a
// severity floor.
//
// The program writing a line chooses its severity and they are careless in both
// directions — sudo sessions and account changes at info, kernel crashes below
// warning, a startup banner as err because stderr maps to err. Naming the
// senders says what is meant. Access and the kernel are worth interrupting
// someone for; everything else waits on the node for a read.
var streamed = func() map[string]bool {
	units := map[string]bool{"kernel": true}
	for unit := range authIdentifiers {
		units[unit] = true
	}
	return units
}()

// Batch is one reporting window: what happened, and how much of it.
type Batch struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Counted separately because they are read separately. A host's own logs
	// and its applications' output are two different questions with two
	// different answers, and a single total is neither: it is dominated by
	// whichever application talks most, and matches no read anyone can make.
	Counters   map[string]int `json:"counters"`
	Containers map[string]int `json:"containers,omitempty"`
	Lines      []Line         `json:"lines"`
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
	containers map[string]int
	mu         sync.Mutex
	limits     Limits
	redact     Redactor
	counters   map[string]int
	lines      map[string]*Line
	order      []string
	routine    int
	dropped    int
	since      time.Time
	cursor     string
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
	return &Collector{limits: limits, redact: redact, counters: map[string]int{}, containers: map[string]int{}, lines: map[string]*Line{}, since: time.Now().UTC()}
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

// Observe records one journal entry. Every entry is counted; only the senders
// the live view carries are kept as lines.
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
	// Every entry is counted, including the ones that stay on the node. That
	// is what lets the console say how much is waiting to be read without
	// carrying any of it, and what makes the live view honest about being a
	// slice rather than a summary.
	//
	// A container's output is counted apart from the host's, because the two
	// are read apart: a chip saying 8,477 that answers to no read anyone can
	// make is worse than no chip at all.
	if item.Container != "" {
		c.containers[PriorityNames[priority]]++
		return
	}
	c.counters[PriorityNames[priority]]++
	if !streamed[unit] {
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
	// whether it was allowed to cross. A unit on the list still chatters at
	// info -- a host opens sudo sessions all day -- and letting that chatter
	// spend the budget held for trouble is exactly the crowding the two
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
	c.lines[key] = &Line{At: entryTime(item.Realtime), Priority: priority, Unit: unit, Message: message}
	c.order = append(c.order, key)
}

// Flush returns the accumulated window and starts a new one. An empty window
// still reports its counters, because zero errors is itself the measurement.
func (c *Collector) Flush(now time.Time) Batch {
	c.mu.Lock()
	defer c.mu.Unlock()
	batch := Batch{From: c.since, To: now, Counters: c.counters, Containers: c.containers, Dropped: c.dropped, Cursor: c.cursor, Lines: make([]Line, 0, len(c.order))}
	for _, key := range c.order {
		batch.Lines = append(batch.Lines, *c.lines[key])
	}
	sort.Slice(batch.Lines, func(i, j int) bool { return batch.Lines[i].At.Before(batch.Lines[j].At) })
	c.counters = map[string]int{}
	c.containers = map[string]int{}
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
