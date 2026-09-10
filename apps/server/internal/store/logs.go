package store

import (
	"sort"
	"strconv"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// Streamed logs are held in memory only. They exist so the evidence for a
// node-side failure is on the server before the node goes quiet, not as an
// archive; a window worth keeping is captured on demand instead.
const (
	logRetention      = 24 * time.Hour
	logLinesPerNode   = 20000
	logWindowsPerNode = 24 * 60
)

// AddLogBatch records one reporting window from a node.
func (s *Memory) AddLogBatch(nodeID, agentID string, counters domain.LogCounters, lines []domain.LogLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logLines == nil {
		s.logLines = map[string][]domain.LogLine{}
		s.logCounters = map[string][]domain.LogCounters{}
	}
	cutoff := time.Now().UTC().Add(-logRetention)
	// Reads walk a node's window backwards and stop at the retention edge, so
	// the window has to be ordered. The batch arrives over HTTP, so the order
	// is established here rather than assumed of the sender.
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].At.Before(lines[j].At) })
	for index, line := range lines {
		line.NodeID = nodeID
		line.AgentID = agentID
		if line.At.IsZero() {
			line.At = counters.To
		}
		line.ID = nodeID + "-" + strconv.FormatInt(line.At.UnixNano(), 36) + "-" + strconv.Itoa(index)
		s.logLines[nodeID] = append(s.logLines[nodeID], line)
	}
	s.logLines[nodeID] = trimLogLines(s.logLines[nodeID], cutoff)

	counters.NodeID = nodeID
	s.logCounters[nodeID] = append(s.logCounters[nodeID], counters)
	windows := s.logCounters[nodeID]
	kept := windows[:0]
	for _, window := range windows {
		if window.To.After(cutoff) {
			kept = append(kept, window)
		}
	}
	if len(kept) > logWindowsPerNode {
		kept = kept[len(kept)-logWindowsPerNode:]
	}
	s.logCounters[nodeID] = append([]domain.LogCounters(nil), kept...)
}

// trimLogLines drops what has aged out. Lines arrive in time order, so the
// expired ones are a prefix and the rest is re-sliced rather than copied.
func trimLogLines(items []domain.LogLine, cutoff time.Time) []domain.LogLine {
	drop := 0
	for drop < len(items) && !items[drop].At.After(cutoff) {
		drop++
	}
	items = items[drop:]
	if len(items) > logLinesPerNode {
		items = items[len(items)-logLinesPerNode:]
	}
	// Re-slicing keeps the original array alive; compact once it is mostly
	// dead weight.
	if cap(items) > 2*logLinesPerNode && cap(items) > 2*len(items) {
		items = append(make([]domain.LogLine, 0, len(items)+logLinesPerNode/4), items...)
	}
	return items
}

// LogLines returns streamed lines newest first for the nodes in allowed, or
// every node when allowed is nil.
//
// Only the newest `limit` of each node can reach the answer, so that many are
// taken from each tail and merged rather than sorting the whole window under
// the read lock.
func (s *Memory) LogLines(allowed map[string]bool, since time.Time, limit int) []domain.LogLine {
	if limit <= 0 {
		limit = logLinesPerNode
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	// One cursor per node, walking its window backwards. Each step takes the
	// newest line still available, so the cost is the page size times the node
	// count rather than the size of everything retained.
	windows := make([][]domain.LogLine, 0, len(s.logLines))
	for id, lines := range s.logLines {
		if allowed != nil && !allowed[id] {
			continue
		}
		if len(lines) > 0 {
			windows = append(windows, lines)
		}
	}
	cursors := make([]int, len(windows))
	for index := range cursors {
		cursors[index] = len(windows[index]) - 1
	}
	items := make([]domain.LogLine, 0, min(limit, 256))
	for len(items) < limit {
		newest := -1
		for index, cursor := range cursors {
			if cursor < 0 || !windows[index][cursor].At.After(since) {
				continue
			}
			if newest < 0 || windows[index][cursor].At.After(windows[newest][cursors[newest]].At) {
				newest = index
			}
		}
		if newest < 0 {
			break
		}
		items = append(items, windows[newest][cursors[newest]])
		cursors[newest]--
	}
	return items
}

// LogCounterWindows returns a node's severity counts newest last, so a chart
// reads left to right.
func (s *Memory) LogCounterWindows(allowed map[string]bool, since time.Time) []domain.LogCounters {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.LogCounters{}
	for id, windows := range s.logCounters {
		if allowed != nil && !allowed[id] {
			continue
		}
		for _, window := range windows {
			if window.To.After(since) {
				items = append(items, window)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].To.Before(items[j].To) })
	return items
}
