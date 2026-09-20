package store

import (
	"slices"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// What was done to a set of resources over a window. An incident timeline asks
// this question, and so does a resource's own history; both would otherwise
// have to pull every operation ever run and filter it in the handler.

// ActivityFilter selects records that touch one of the named resources and
// overlap the window. An empty ResourceIDs matches every resource, and a zero
// From or To leaves that end of the window open.
type ActivityFilter struct {
	ResourceIDs []string
	From        time.Time
	To          time.Time
}

func (f ActivityFilter) matchesResource(id string) bool {
	return len(f.ResourceIDs) == 0 || slices.Contains(f.ResourceIDs, id)
}

func (f ActivityFilter) matchesAnyResource(ids []string) bool {
	if len(f.ResourceIDs) == 0 {
		return true
	}
	for _, id := range ids {
		if slices.Contains(f.ResourceIDs, id) {
			return true
		}
	}
	return false
}

// overlaps compares the record's own interval with the window. A record that
// has not finished is treated as running up to now, so an operation still
// executing when the window opens counts — during a response, the action
// someone started and has not finished is the one that matters most.
func (f ActivityFilter) overlaps(start time.Time, end *time.Time) bool {
	finish := time.Now().UTC()
	if end != nil {
		finish = *end
	}
	if finish.Before(start) {
		finish = start
	}
	if !f.From.IsZero() && finish.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && start.After(f.To) {
		return false
	}
	return true
}

// OperationsTouching returns the operations aimed at the filter's resources,
// oldest first, which is the order a timeline reads in.
func (s *Memory) OperationsTouching(filter ActivityFilter) []domain.Operation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.Operation{}
	for _, item := range s.operations {
		if filter.matchesAnyResource(item.TargetIDs) && filter.overlaps(item.CreatedAt, item.FinishedAt) {
			items = append(items, item)
		}
	}
	sortByCreation(items, func(item domain.Operation) time.Time { return item.CreatedAt })
	return items
}

// TerminalsTouching returns the terminal sessions opened against the filter's
// resources, oldest first.
func (s *Memory) TerminalsTouching(filter ActivityFilter) []domain.TerminalSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.TerminalSession{}
	for _, item := range s.terminals {
		if filter.matchesResource(item.TargetID) && filter.overlaps(item.CreatedAt, item.ClosedAt) {
			items = append(items, item)
		}
	}
	sortByCreation(items, func(item domain.TerminalSession) time.Time { return item.CreatedAt })
	return items
}

func sortByCreation[T any](items []T, at func(T) time.Time) {
	slices.SortFunc(items, func(a, b T) int { return at(a).Compare(at(b)) })
}
