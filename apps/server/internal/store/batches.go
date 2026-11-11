package store

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// CreateBatch records a batch and one queued operation per target. Nothing is
// claimable yet: a batch that needs approval waits for it, and one that does
// not is released by the first AdvanceBatch.
func (s *Memory) CreateBatch(batch domain.OperationBatch, needsApproval bool) domain.OperationBatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	batch.CreatedAt, batch.UpdatedAt = now, now
	batch.Status = "running"
	if needsApproval {
		batch.Status = "awaiting_approval"
	}
	batch.Targets = make([]domain.BatchTarget, 0, len(batch.TargetIDs))
	for index, target := range batch.TargetIDs {
		operation := domain.Operation{
			ID:          fmt.Sprintf("%s-%04d", batch.ID, index),
			Type:        batch.Type,
			Status:      "queued",
			TargetIDs:   []string{target},
			Parameters:  batch.Parameters,
			RequestedBy: batch.RequestedBy,
			Reason:      batch.Reason,
			BatchID:     batch.ID,
			// Spaced by a nanosecond so a claim, which takes the oldest
			// pending operation, follows the order the targets were given in.
			CreatedAt: now.Add(time.Duration(index)),
			UpdatedAt: now,
		}
		s.operations[operation.ID] = operation
		batch.Targets = append(batch.Targets, domain.BatchTarget{ResourceID: target, OperationID: operation.ID, Status: "queued"})
	}
	s.batches[batch.ID] = batch
	return copyBatch(batch)
}

func (s *Memory) Batch(id string) (domain.OperationBatch, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	batch, ok := s.batches[id]
	return copyBatch(batch), ok
}

// copyBatch detaches the targets, which the advance path rewrites in place.
func copyBatch(batch domain.OperationBatch) domain.OperationBatch {
	batch.Targets = append([]domain.BatchTarget(nil), batch.Targets...)
	return batch
}

func (s *Memory) ListBatches() []domain.OperationBatch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.OperationBatch, 0, len(s.batches))
	for _, batch := range s.batches {
		items = append(items, copyBatch(batch))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items
}

// ApproveBatch approves every target at once, under the same rule as a single
// operation: the requester needs a second identity or approve-self.
func (s *Memory) ApproveBatch(id, approver string, allowSelf bool) (domain.OperationBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch, ok := s.batches[id]
	if !ok {
		return domain.OperationBatch{}, ErrNotFound
	}
	if batch.Status != "awaiting_approval" {
		return domain.OperationBatch{}, errors.New("batch is not awaiting approval")
	}
	if batch.RequestedBy == approver && !allowSelf {
		return domain.OperationBatch{}, errors.New("the requester cannot approve their own batch; this needs a second identity, or a role holding operations:approve-self")
	}
	now := time.Now().UTC()
	batch.ApprovedBy = approver
	batch.Status = "running"
	batch.UpdatedAt = now
	for _, target := range batch.Targets {
		if operation, exists := s.operations[target.OperationID]; exists {
			operation.ApprovedBy = approver
			s.operations[target.OperationID] = operation
		}
	}
	s.batches[id] = batch
	s.advanceBatchLocked(id, now)
	return copyBatch(s.batches[id]), nil
}

// AbortBatch cancels every target that has not been released. Hosts already
// working finish what they were given; stopping them halfway through a
// restart would leave them in a worse state than either outcome.
func (s *Memory) AbortBatch(id string) (domain.OperationBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch, ok := s.batches[id]
	if !ok {
		return domain.OperationBatch{}, ErrNotFound
	}
	if batch.FinishedAt != nil || batch.Status == "aborted" || batch.Status == "halted" {
		return domain.OperationBatch{}, errors.New("batch has already stopped")
	}
	now := time.Now().UTC()
	s.cancelQueuedLocked(&batch, now, "batch aborted")
	batch.Status = "aborted"
	batch.UpdatedAt = now
	s.batches[id] = batch
	s.advanceBatchLocked(id, now)
	return copyBatch(s.batches[id]), nil
}

// AdvanceBatches moves every unfinished batch on. The completion path calls
// AdvanceBatch for the batch it touched; this sweep covers what reaches a
// final state some other way, such as a lease that expired.
func (s *Memory) AdvanceBatches() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for id, batch := range s.batches {
		if batch.FinishedAt == nil {
			s.advanceBatchLocked(id, now)
		}
	}
}

func (s *Memory) AdvanceBatch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advanceBatchLocked(id, time.Now().UTC())
}

func finishedStatus(status string) bool {
	return status == "succeeded" || status == "failed" || status == "canceled"
}

// advanceBatchLocked copies each target's operation state onto the batch,
// stops the batch once failures pass its tolerance, releases the next wave,
// and closes the batch when nothing is left to wait on.
func (s *Memory) advanceBatchLocked(id string, now time.Time) {
	batch, ok := s.batches[id]
	if !ok || batch.FinishedAt != nil || batch.Status == "awaiting_approval" {
		return
	}
	inFlight, failed, queued := 0, 0, 0
	for index, target := range batch.Targets {
		if operation, exists := s.operations[target.OperationID]; exists {
			batch.Targets[index].Status = operation.Status
			batch.Targets[index].Error = operation.Error
		} else if !finishedStatus(target.Status) {
			// Pruned before its result was copied: nothing to wait on.
			batch.Targets[index].Status = "failed"
			batch.Targets[index].Error = "operation record is gone"
		}
		switch batch.Targets[index].Status {
		case "queued":
			queued++
		case "pending", "running", "awaiting_approval":
			inFlight++
		case "failed":
			failed++
		}
	}
	if batch.Status == "running" && failed > batch.MaxFailures && queued > 0 {
		s.cancelQueuedLocked(&batch, now, fmt.Sprintf("batch halted after %d failures", failed))
		batch.Status = "halted"
		queued = 0
	}
	if batch.Status == "running" {
		for index := range batch.Targets {
			if inFlight >= batch.BatchSize || queued == 0 {
				break
			}
			target := batch.Targets[index]
			if target.Status != "queued" {
				continue
			}
			operation := s.operations[target.OperationID]
			operation.Status = "pending"
			operation.UpdatedAt = now
			s.operations[target.OperationID] = operation
			batch.Targets[index].Status = "pending"
			inFlight++
			queued--
		}
	}
	if inFlight == 0 && queued == 0 {
		switch batch.Status {
		case "running":
			batch.Status = "succeeded"
			if failed > 0 {
				batch.Status = "partial"
			}
		}
		batch.FinishedAt = &now
	}
	batch.UpdatedAt = now
	s.batches[id] = batch
}

func (s *Memory) cancelQueuedLocked(batch *domain.OperationBatch, now time.Time, reason string) {
	for index, target := range batch.Targets {
		operation, exists := s.operations[target.OperationID]
		if !exists || operation.Status != "queued" {
			continue
		}
		operation.Status = "canceled"
		operation.Error = reason
		operation.UpdatedAt = now
		operation.FinishedAt = &now
		s.operations[target.OperationID] = operation
		batch.Targets[index].Status = "canceled"
		batch.Targets[index].Error = reason
	}
}
