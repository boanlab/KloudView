package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// logHandler enrols a real agent, because the ingest endpoint authenticates
// with an issued credential rather than the enrollment token.
func logHandler(t *testing.T) (http.Handler, string, string, string) {
	t.Helper()
	memory := store.NewMemory()
	server := New(memory, "test-token", "")
	bindViewer(server)
	handler := server.Handler()
	enrolled := enrolAgent(t, handler)
	memory.UpsertResource(domain.Resource{ID: "node-02", Name: "other", Type: domain.ResourceNode, AgentID: "agent-other"})
	return handler, enrolled.Agent.ID, enrolled.Agent.NodeID, enrolled.Credential
}

func postLogs(t *testing.T, handler http.Handler, agentID, credential, body string) int {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agentID+"/logs", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+credential)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code
}

func TestLogIngestStoresLinesAndCounters(t *testing.T) {
	handler, agentID, nodeID, credential := logHandler(t)
	now := time.Now().UTC()
	body := `{"nodeId":"` + nodeID + `","from":"` + now.Add(-time.Minute).Format(time.RFC3339) + `","to":"` + now.Format(time.RFC3339) +
		`","counters":{"err":2,"warning":1,"info":900},"dropped":3,"lines":[{"at":"` + now.Format(time.RFC3339) +
		`","priority":3,"unit":"kernel","message":"I/O error on sda","repeat":4}]}`
	if code := postLogs(t, handler, agentID, credential, body); code != http.StatusAccepted {
		t.Fatalf("ingest status %d", code)
	}

	lines := getBody(t, handler, "/api/v1/logs/lines?minutes=60")
	if !strings.Contains(lines, "I/O error on sda") || !strings.Contains(lines, `"repeat":4`) {
		t.Fatalf("lines missing: %s", lines)
	}
	counters := getBody(t, handler, "/api/v1/logs/counters?minutes=60")
	// Every severity is retained, including the ones never shipped as lines.
	if !strings.Contains(counters, `"info":900`) || !strings.Contains(counters, `"dropped":3`) {
		t.Fatalf("counters missing: %s", counters)
	}
}

func TestLogIngestRejectsAnotherAgentsNode(t *testing.T) {
	handler, agentID, _, credential := logHandler(t)
	body := `{"nodeId":"node-02","counters":{"err":1},"lines":[]}`
	if code := postLogs(t, handler, agentID, credential, body); code != http.StatusForbidden {
		t.Fatalf("cross-agent ingest status %d, want 403", code)
	}
	if code := postLogs(t, handler, agentID, credential, `{"nodeId":"node-missing"}`); code != http.StatusNotFound {
		t.Fatalf("unknown node status %d, want 404", code)
	}
}

func TestLogIngestBoundsBatchSize(t *testing.T) {
	handler, agentID, nodeID, credential := logHandler(t)
	now := time.Now().UTC()
	lines := make([]map[string]any, 0, logBatchMaxLines+10)
	for index := range logBatchMaxLines + 10 {
		lines = append(lines, map[string]any{
			"at": now, "priority": 4, "unit": "sshd",
			"message": "Failed password for user " + strconv.Itoa(index),
		})
	}
	// One over-long line proves the per-message cap independently of the count.
	lines[0]["message"] = strings.Repeat("x", logBatchMaxLength+50)
	payload, err := json.Marshal(map[string]any{"nodeId": nodeID, "to": now, "counters": map[string]int{"warning": 1}, "lines": lines})
	if err != nil {
		t.Fatal(err)
	}
	if code := postLogs(t, handler, agentID, credential, string(payload)); code != http.StatusAccepted {
		t.Fatalf("ingest status %d", code)
	}

	var response struct {
		Items []domain.LogLine `json:"items"`
	}
	if err := json.Unmarshal([]byte(getBody(t, handler, "/api/v1/logs/lines?limit=2000")), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) > logBatchMaxLines {
		t.Fatalf("stored %d lines, want at most %d", len(response.Items), logBatchMaxLines)
	}
	for _, line := range response.Items {
		if len(line.Message) > logBatchMaxLength {
			t.Fatalf("message not truncated: %d bytes", len(line.Message))
		}
	}
}

func TestLogIngestRejectsStaleWindow(t *testing.T) {
	handler, agentID, nodeID, credential := logHandler(t)
	stale := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	if code := postLogs(t, handler, agentID, credential, `{"nodeId":"`+nodeID+`","to":"`+stale+`"}`); code != http.StatusBadRequest {
		t.Fatalf("stale window status %d, want 400", code)
	}
}

// Lines are stored as the node produced them; only role-admin reads them that
// way. Everyone else gets the secret masked, and nobody loses attribution.
func TestRawLogLinesAreAdminOnly(t *testing.T) {
	handler, agentID, nodeID, credential := logHandler(t)
	now := time.Now().UTC()
	body := `{"nodeId":"` + nodeID + `","to":"` + now.Format(time.RFC3339) +
		`","counters":{"warning":1},"lines":[{"at":"` + now.Format(time.RFC3339) +
		`","priority":4,"unit":"sshd","message":"Failed password for admin from 203.0.113.9 db_password=hunter2"}]}`
	if code := postLogs(t, handler, agentID, credential, body); code != http.StatusAccepted {
		t.Fatalf("ingest status %d", code)
	}

	// test-viewer holds a global scope, so what differs here is the role, not
	// which nodes each identity can see.
	for _, test := range []struct {
		subject string
		wantRaw bool
	}{{"admin", true}, {"test-viewer", false}} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/logs/lines?minutes=60", nil)
		request.Header.Set("X-KloudView-Subject", test.subject)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status %d", test.subject, recorder.Code)
		}
		got := recorder.Body.String()
		if strings.Contains(got, "hunter2") != test.wantRaw {
			t.Fatalf("%s: raw secret visible = %v, want %v", test.subject, !test.wantRaw, test.wantRaw)
		}
		// Attribution survives for every role, or the log is useless.
		if !strings.Contains(got, "admin") || !strings.Contains(got, "203.0.113.9") {
			t.Fatalf("%s: attribution lost: %s", test.subject, got)
		}
	}
}
