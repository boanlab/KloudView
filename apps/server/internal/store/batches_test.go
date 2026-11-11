package store

import (
	"encoding/json"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func TestBatchApprovalNeedsASecondIdentity(t *testing.T) {
	memory := NewMemory()
	memory.CreateBatch(domain.OperationBatch{ID: "batch-1", Type: "service.restart", TargetIDs: []string{"node-a"}, BatchSize: 1, RequestedBy: "alice"}, true)
	if _, err := memory.ApproveBatch("batch-1", "alice", false); err == nil {
		t.Fatal("the requester approved their own batch")
	}
	batch, err := memory.ApproveBatch("batch-1", "bob", false)
	if err != nil || batch.Status != "running" || batch.Targets[0].Status != "pending" {
		t.Fatalf("approved by bob: %+v %v", batch, err)
	}
	if operation, _ := memory.Operation(batch.Targets[0].OperationID); operation.ApprovedBy != "bob" || operation.Status != "pending" {
		t.Fatalf("child = %+v", operation)
	}
}

func TestBatchSurvivesASaveAndLoad(t *testing.T) {
	memory := NewMemory()
	memory.CreateBatch(domain.OperationBatch{ID: "batch-1", Type: "inventory.refresh", TargetIDs: []string{"node-a", "node-b"}, BatchSize: 1}, false)
	memory.AdvanceBatch("batch-1")
	data, err := memory.marshalState(false, true, true)
	if err != nil {
		t.Fatal(err)
	}
	var state snapshot
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	restored := NewMemory()
	restored.restore(state)
	batch, ok := restored.Batch("batch-1")
	if !ok || len(batch.Targets) != 2 || batch.Targets[0].Status != "pending" || batch.Targets[1].Status != "queued" {
		t.Fatalf("restored batch = %+v %v", batch, ok)
	}
}
