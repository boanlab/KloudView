package store

import (
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func TestActivityWindowKeepsWhatWasRunning(t *testing.T) {
	base := time.Now().UTC().Add(-time.Hour)
	at := func(offset time.Duration) *time.Time { moment := base.Add(offset); return &moment }
	window := ActivityFilter{From: base.Add(5 * time.Minute), To: base.Add(20 * time.Minute)}
	for _, test := range []struct {
		name  string
		start time.Time
		end   *time.Time
		want  bool
	}{
		{"wholly inside", base.Add(10 * time.Minute), at(12 * time.Minute), true},
		{"ends before it opens", base, at(time.Minute), false},
		{"starts after it closes", base.Add(25 * time.Minute), at(26 * time.Minute), false},
		{"spans it", base, at(30 * time.Minute), true},
		{"ends on the opening edge", base, at(5 * time.Minute), true},
		// Nothing finished it, so it is still running: the action someone
		// started and has not finished is the one a response cares about.
		{"never finished", base, nil, true},
	} {
		if got := window.overlaps(test.start, test.end); got != test.want {
			t.Errorf("%s: overlaps = %v, want %v", test.name, got, test.want)
		}
	}
	// An open-ended window is bounded on one side only.
	if !(ActivityFilter{From: base.Add(5 * time.Minute)}).overlaps(base.Add(time.Hour), nil) {
		t.Error("an open end should accept anything after From")
	}
	if (ActivityFilter{To: base}).overlaps(base.Add(time.Minute), at(2*time.Minute)) {
		t.Error("an open start should still reject what begins after To")
	}
}

func TestActivityFilterSelectsByResource(t *testing.T) {
	memory := NewMemory()
	memory.PutOperation(domain.Operation{ID: "op-here", TargetIDs: []string{"node-1", "node-3"}})
	memory.PutOperation(domain.Operation{ID: "op-elsewhere", TargetIDs: []string{"node-2"}})
	closed := time.Now().UTC().Add(-2 * time.Hour)
	memory.PutTerminal(domain.TerminalSession{ID: "session-old", TargetID: "node-1", CreatedAt: closed, ClosedAt: &closed})
	memory.PutTerminal(domain.TerminalSession{ID: "session-here", TargetID: "node-1"})
	memory.PutTerminal(domain.TerminalSession{ID: "session-elsewhere", TargetID: "node-2"})

	operations := memory.OperationsTouching(ActivityFilter{ResourceIDs: []string{"node-3"}})
	if len(operations) != 1 || operations[0].ID != "op-here" {
		t.Fatalf("operations = %+v", operations)
	}
	// A window that opens after the old session closed leaves it out.
	sessions := memory.TerminalsTouching(ActivityFilter{ResourceIDs: []string{"node-1"}, From: time.Now().UTC().Add(-time.Hour)})
	if len(sessions) != 1 || sessions[0].ID != "session-here" {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestActivityFilterWithoutResourcesMatchesEverything(t *testing.T) {
	memory := NewMemory()
	memory.PutOperation(domain.Operation{ID: "op-1", TargetIDs: []string{"node-1"}})
	memory.PutOperation(domain.Operation{ID: "op-2", TargetIDs: []string{"node-2"}})
	if got := memory.OperationsTouching(ActivityFilter{}); len(got) != 2 {
		t.Fatalf("expected both operations, got %d", len(got))
	}
}
