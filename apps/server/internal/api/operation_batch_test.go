package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

type batchHarness struct {
	t       *testing.T
	handler http.Handler
}

func newBatchHarness(t *testing.T, hosts int) batchHarness {
	t.Helper()
	server := New(store.NewMemory(), "test-token", "")
	bindApprover(server)
	handler := server.Handler()
	for i := 1; i <= hosts; i++ {
		enroll := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(fmt.Sprintf(`{"token":"test-token","hostname":"node-%02d","version":"0.1.0"}`, i)))
		handler.ServeHTTP(httptest.NewRecorder(), enroll)
	}
	return batchHarness{t: t, handler: handler}
}

func (h batchHarness) do(request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	return recorder
}

func (h batchHarness) admin(method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	authorize(request)
	return h.do(request)
}

// claim asks host i for work and returns the operation id it got, or "".
func (h batchHarness) claim(i int) string {
	request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/agents/agent-node-%02d/operations/claim", i), nil)
	agentAuthorize(request)
	recorder := h.do(request)
	if recorder.Code != http.StatusOK {
		return ""
	}
	var operation domain.Operation
	_ = json.Unmarshal(recorder.Body.Bytes(), &operation)
	return operation.ID
}

func (h batchHarness) complete(i int, id, status string) {
	request := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/agents/agent-node-%02d/operations/%s", i, id), strings.NewReader(fmt.Sprintf(`{"status":%q}`, status)))
	agentAuthorize(request)
	if recorder := h.do(request); recorder.Code != http.StatusOK {
		h.t.Fatalf("complete %s = %d %s", id, recorder.Code, recorder.Body.String())
	}
}

func (h batchHarness) batch(id string) domain.OperationBatch {
	recorder := h.admin(http.MethodGet, "/api/v1/operation-batches/"+id, "")
	var batch domain.OperationBatch
	_ = json.Unmarshal(recorder.Body.Bytes(), &batch)
	return batch
}

func (h batchHarness) working() map[int]string {
	got := map[int]string{}
	for i := 1; i <= 5; i++ {
		if id := h.claim(i); id != "" {
			got[i] = id
		}
	}
	return got
}

func targets(n int) string {
	ids := []string{}
	for i := 1; i <= n; i++ {
		ids = append(ids, fmt.Sprintf(`"node-node-%02d"`, i))
	}
	return "[" + strings.Join(ids, ",") + "]"
}

func TestBatchReleasesWavesAndHaltsPastItsTolerance(t *testing.T) {
	h := newBatchHarness(t, 5)
	recorder := h.admin(http.MethodPost, "/api/v1/operation-batches", `{"type":"inventory.refresh","reason":"refresh","targetIds":`+targets(5)+`,"batchSize":2,"maxFailures":1}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", recorder.Code, recorder.Body.String())
	}
	var created domain.OperationBatch
	_ = json.Unmarshal(recorder.Body.Bytes(), &created)

	first := h.working()
	if len(first) != 2 || first[1] == "" || first[2] == "" {
		t.Fatalf("first wave = %v, want hosts 1 and 2 only", first)
	}
	h.complete(1, first[1], "failed")
	h.complete(2, first[2], "succeeded")
	second := h.working()
	if len(second) != 2 || second[3] == "" || second[4] == "" {
		t.Fatalf("second wave = %v, want hosts 3 and 4", second)
	}
	// A second failure passes the tolerance of one: host 5 is never started.
	h.complete(3, second[3], "failed")
	if batch := h.batch(created.ID); batch.Status != "halted" || batch.Targets[4].Status != "canceled" {
		t.Fatalf("after two failures: status %q, host 5 %q", batch.Status, batch.Targets[4].Status)
	}
	if extra := h.claim(5); extra != "" {
		t.Fatalf("host 5 was handed %s after the halt", extra)
	}
	h.complete(4, second[4], "succeeded")
	batch := h.batch(created.ID)
	if batch.Status != "halted" || batch.FinishedAt == nil {
		t.Fatalf("final = %q finished %v", batch.Status, batch.FinishedAt)
	}
}

func TestBatchSucceedsAndAbortCancelsWhatIsQueued(t *testing.T) {
	h := newBatchHarness(t, 3)
	recorder := h.admin(http.MethodPost, "/api/v1/operation-batches", `{"type":"inventory.refresh","reason":"refresh","targetIds":`+targets(3)+`,"batchSize":1,"maxFailures":0}`)
	var created domain.OperationBatch
	_ = json.Unmarshal(recorder.Body.Bytes(), &created)
	id := h.claim(1)
	h.complete(1, id, "succeeded")
	if second := h.claim(2); second == "" {
		t.Fatal("host 2 was not released after host 1 finished")
	} else if recorder := h.admin(http.MethodPost, "/api/v1/operation-batches/"+created.ID+"/abort", ""); recorder.Code != http.StatusOK {
		t.Fatalf("abort = %d %s", recorder.Code, recorder.Body.String())
	} else {
		h.complete(2, second, "succeeded")
	}
	batch := h.batch(created.ID)
	if batch.Status != "aborted" || batch.Targets[2].Status != "canceled" || batch.FinishedAt == nil {
		t.Fatalf("after abort: %+v", batch)
	}
}

func TestBatchNeedingApprovalWaitsForASecondIdentity(t *testing.T) {
	h := newBatchHarness(t, 2)
	recorder := h.admin(http.MethodPost, "/api/v1/operation-batches", `{"type":"service.restart","parameters":{"service":"sshd.service"},"reason":"patch","targetIds":`+targets(2)+`,"batchSize":2,"maxFailures":0}`)
	var created domain.OperationBatch
	_ = json.Unmarshal(recorder.Body.Bytes(), &created)
	if created.Status != "awaiting_approval" {
		t.Fatalf("status = %q", created.Status)
	}
	if id := h.claim(1); id != "" {
		t.Fatalf("host 1 claimed %s before approval", id)
	}
	if recorder := h.admin(http.MethodPost, "/api/v1/operation-batches/"+created.ID+"/approve", ""); recorder.Code != http.StatusConflict {
		t.Fatalf("self approval = %d %s, want 409", recorder.Code, recorder.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/operation-batches/"+created.ID+"/approve", nil)
	request.Header.Set("X-KloudView-Subject", "test-approver")
	if recorder := h.do(request); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"approvedBy":"test-approver"`) {
		t.Fatalf("independent approval = %d %s", recorder.Code, recorder.Body.String())
	}
	if h.claim(1) == "" || h.claim(2) == "" {
		t.Fatal("approved batch did not release both hosts")
	}
}

func TestBatchRejectsMalformedRequests(t *testing.T) {
	h := newBatchHarness(t, 2)
	for _, body := range []string{
		`{"type":"inventory.refresh","reason":"r","targetIds":[],"batchSize":1}`,
		`{"type":"inventory.refresh","reason":"r","targetIds":["node-node-01","node-node-01"],"batchSize":1}`,
		`{"type":"inventory.refresh","reason":"r","targetIds":["node-missing"],"batchSize":1}`,
		`{"type":"inventory.refresh","reason":"r","targetIds":` + targets(2) + `,"batchSize":0}`,
		`{"type":"inventory.refresh","reason":"r","targetIds":` + targets(2) + `,"batchSize":1,"maxFailures":3}`,
		`{"type":"shell.exec","reason":"r","targetIds":` + targets(2) + `,"batchSize":1}`,
		`{"type":"inventory.refresh","targetIds":` + targets(2) + `,"batchSize":1}`,
	} {
		if recorder := h.admin(http.MethodPost, "/api/v1/operation-batches", body); recorder.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, recorder.Code)
		}
	}
}
