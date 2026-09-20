package store

import (
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// A log read answers with far more than the state document should carry. The
// operation kept twelve lines of five hundred and seventy-six, cut mid-word,
// and the operator had no way to know the rest existed.
func TestALogReadKeepsItsWholeAnswerOutOfTheStateDocument(t *testing.T) {
	memory := NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode})
	operation := memory.PutOperation(domain.Operation{
		ID: "op-read", Type: "logs.capture", TargetIDs: []string{"node-01"}, Status: "running",
	})
	answer := strings.Repeat("2026-09-19T12:00:00+00:00 node-01 grafana[1208]: a line of it\n", 900)
	if len(answer) <= operationResultLimit {
		t.Fatalf("the fixture is %d bytes; it has to be bigger than the %d-byte field", len(answer), operationResultLimit)
	}

	if _, ok := memory.CompleteOperation(operation.ID, "node-01", "succeeded", answer, ""); !ok {
		t.Fatal("the operation would not complete")
	}

	// The operation says how much there is, and stays small.
	stored, _ := memory.Operation(operation.ID)
	if len(stored.Result) > 200 {
		t.Fatalf("operation result is %d bytes; the state document is rewritten whole", len(stored.Result))
	}
	if !strings.Contains(stored.Result, "900 lines") {
		t.Errorf("result = %q, want it to say how much is there", stored.Result)
	}

	// And the answer itself is whole.
	text, held := memory.Report(operation.ID)
	if !held {
		t.Fatal("the read's answer was not kept")
	}
	if text != answer {
		t.Fatalf("held %d bytes of %d", len(text), len(answer))
	}
}

// Everything else keeps its output where it always was: a command's result is
// short, and it belongs with the operation that produced it.
func TestOtherOperationsKeepTheirResultInPlace(t *testing.T) {
	memory := NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode})
	operation := memory.PutOperation(domain.Operation{
		ID: "op-status", Type: "service.status", TargetIDs: []string{"node-01"}, Status: "running",
	})
	memory.CompleteOperation(operation.ID, "node-01", "succeeded", "active (running)", "")

	stored, _ := memory.Operation(operation.ID)
	if stored.Result != "active (running)" {
		t.Fatalf("result = %q", stored.Result)
	}
	if _, held := memory.Report(operation.ID); held {
		t.Error("a short command's output was diverted; it belongs with its operation")
	}
}

// The answer is evidence while someone is looking at it, not an archive, so it
// is held in memory and let go.
func TestAReportIsLetGoOfOnceItIsStale(t *testing.T) {
	reports := newReportStore()
	reports.Put("op-old", "lines")
	reports.mu.Lock()
	reports.reports["op-old"] = report{Text: "lines", At: time.Now().UTC().Add(-2 * reportRetention)}
	reports.mu.Unlock()

	if _, held := reports.Get("op-old"); held {
		t.Error("a stale report was still served")
	}
	// And the newest are what survive a flood of them.
	for i := range reportsKept * 2 {
		reports.Put("op-"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune('0'+i%10)), "text")
	}
	reports.mu.RLock()
	defer reports.mu.RUnlock()
	if len(reports.reports) > reportsKept {
		t.Fatalf("holding %d reports, want at most %d", len(reports.reports), reportsKept)
	}
}
