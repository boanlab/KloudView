package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

const (
	batchTargetLimit = 500
	batchSizeLimit   = 100
)

func (s *Server) validateBatch(batch domain.OperationBatch) error {
	if batch.Type == "" || batch.Reason == "" {
		return fmt.Errorf("type, targetIds and reason are required")
	}
	if !supportedOperationTypes[batch.Type] {
		return fmt.Errorf("operation type is not supported")
	}
	if err := validateOperationParameters(batch.Type, batch.Parameters); err != nil {
		return err
	}
	if len(batch.TargetIDs) == 0 || len(batch.TargetIDs) > batchTargetLimit {
		return fmt.Errorf("between 1 and %d targets are required", batchTargetLimit)
	}
	seen := map[string]bool{}
	for _, id := range batch.TargetIDs {
		if strings.TrimSpace(id) == "" || seen[id] {
			return fmt.Errorf("target ids must be non-empty and unique")
		}
		seen[id] = true
		if err := s.validateExecutionTarget([]string{id}); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}
	if batch.BatchSize < 1 || batch.BatchSize > batchSizeLimit {
		return fmt.Errorf("batchSize must be between 1 and %d", batchSizeLimit)
	}
	if batch.MaxFailures < 0 || batch.MaxFailures > len(batch.TargetIDs) {
		return fmt.Errorf("maxFailures must be between 0 and the number of targets")
	}
	return nil
}

func (s *Server) createOperationBatch(w http.ResponseWriter, r *http.Request) {
	var batch domain.OperationBatch
	if err := readJSON(r, &batch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.validateBatch(batch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_batch", err.Error())
		return
	}
	if !s.authorizeAllTargets(r, "operations", "create", batch.TargetIDs) {
		writeError(w, http.StatusForbidden, "access_denied", "a target resource scope is not assigned")
		return
	}
	batch.ID = fmt.Sprintf("batch-%d", time.Now().UnixNano())
	batch.RequestedBy = s.subjectFromRequest(r)
	batch.ApprovedBy = ""
	batch.FinishedAt = nil
	created := s.store.CreateBatch(batch, operationNeedsApproval(batch.Type))
	s.store.AdvanceBatch(created.ID)
	current, _ := s.store.Batch(created.ID)
	writeJSON(w, http.StatusCreated, current)
}

func (s *Server) listOperationBatches(w http.ResponseWriter, r *http.Request) {
	allowed := s.anyTargetAuthorizer(r, "operations", "read")
	items := []domain.OperationBatch{}
	for _, batch := range s.store.ListBatches() {
		if allowed(batch.TargetIDs) {
			items = append(items, batch)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getOperationBatch(w http.ResponseWriter, r *http.Request) {
	batch, ok := s.store.Batch(r.PathValue("id"))
	if !ok || !s.anyTargetAuthorizer(r, "operations", "read")(batch.TargetIDs) {
		writeError(w, http.StatusNotFound, "not_found", "batch not found")
		return
	}
	writeJSON(w, http.StatusOK, batch)
}

func (s *Server) approveOperationBatch(w http.ResponseWriter, r *http.Request) {
	current, ok := s.store.Batch(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "batch not found")
		return
	}
	if !s.authorizeAllTargets(r, "operations", "approve", current.TargetIDs) {
		writeError(w, http.StatusForbidden, "access_denied", "batch target scope is not assigned")
		return
	}
	approver := s.subjectFromRequest(r)
	allowSelf := s.authorizeAllTargets(r, "operations", "approve-self", current.TargetIDs)
	batch, err := s.store.ApproveBatch(current.ID, approver, allowSelf)
	if err != nil {
		writeError(w, http.StatusConflict, "batch_not_approved", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, batch)
}

// Stopping a batch is the safe direction, so anyone who could have started
// it on these hosts can stop it.
func (s *Server) abortOperationBatch(w http.ResponseWriter, r *http.Request) {
	current, ok := s.store.Batch(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "batch not found")
		return
	}
	if !s.authorizeAllTargets(r, "operations", "create", current.TargetIDs) {
		writeError(w, http.StatusForbidden, "access_denied", "batch target scope is not assigned")
		return
	}
	batch, err := s.store.AbortBatch(current.ID)
	if err != nil {
		writeError(w, http.StatusConflict, "batch_not_aborted", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, batch)
}
