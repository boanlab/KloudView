package store

import (
	"sync"
	"time"
)

// Where a log read's answer lives.
//
// An operation's result is a field in the state document, which is rewritten
// whole every few seconds, so it is bounded at four kilobytes -- and rightly:
// a command's output belongs there, and "systemctl status" fits. A log read
// does not. Asked for two hours of one host it answers with 576 lines and
// eighty-six kilobytes, of which the operator saw twelve, ending mid-word on
// a note pointing at a resource that does not exist.
//
// So the answer is kept here instead: in memory, never persisted, for as long
// as someone is likely to still be looking at it. The same reasoning the
// streamed logs already follow -- evidence while it is wanted, not an archive
// -- and the operation keeps a one-line summary so the state document stays
// the size it was designed to be.
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
