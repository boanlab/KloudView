package store

import (
	"sync"
	"time"
)

// Where a log read's answer lives.
//
// An operation's result is a field in the state document, rewritten whole every
// few seconds and bounded at four kilobytes, which suits a command's output but
// not hours of logs. The answer is kept here instead: in memory, never
// persisted, for as long as someone is likely to be looking at it, with the
// operation keeping a one-line summary. Evidence while it is wanted, not an
// archive - the same rule the streamed logs follow.
const (
	reportRetention = 30 * time.Minute
	reportMaxBytes  = 2 << 20
	reportsKept     = 40
)

type report struct {
	Text string
	At   time.Time
}

type reportStore struct {
	mu      sync.RWMutex
	reports map[string]report
	order   []string
}

func newReportStore() *reportStore {
	return &reportStore{reports: map[string]report{}}
}

// Put records one operation's full output, oldest evicted first.
func (r *reportStore) Put(operationID, text string) {
	if operationID == "" || text == "" {
		return
	}
	if len(text) > reportMaxBytes {
		text = text[:reportMaxBytes] + "\n[truncated at 2 MB]"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.reports[operationID]; !exists {
		r.order = append(r.order, operationID)
	}
	r.reports[operationID] = report{Text: text, At: time.Now().UTC()}
	r.evict()
}

// Get returns one operation's output and whether it is still held.
func (r *reportStore) Get(operationID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	held, ok := r.reports[operationID]
	if !ok || time.Since(held.At) > reportRetention {
		return "", false
	}
	return held.Text, true
}

// evict drops what has aged out and what no longer fits. The caller holds the
// lock.
func (r *reportStore) evict() {
	cutoff := time.Now().UTC().Add(-reportRetention)
	kept := r.order[:0]
	for _, id := range r.order {
		held, exists := r.reports[id]
		if !exists || held.At.Before(cutoff) {
			delete(r.reports, id)
			continue
		}
		kept = append(kept, id)
	}
	for len(kept) > reportsKept {
		delete(r.reports, kept[0])
		kept = kept[1:]
	}
	r.order = kept
}
