package api

import (
	"net/http"
	"testing"
)

// An agent polls for work every few seconds and usually finds none. Recorded,
// those empty answers push every human action out of the ring buffer: a hundred
// agents overwrite the whole log in minutes, and the audit page stays full of
// nothing but polling.
func TestAnEmptyPollIsNotAuditable(t *testing.T) {
	cases := []struct {
		path   string
		status int
		want   bool
	}{
		{"/api/v1/agents/agent-1/terminal/claim", http.StatusNoContent, false},
		{"/api/v1/agents/agent-1/operations/claim", http.StatusNoContent, false},
		// A poll that handed out work is the start of something someone did.
		{"/api/v1/agents/agent-1/terminal/claim", http.StatusOK, true},
		{"/api/v1/agents/agent-1/operations/claim", http.StatusOK, true},
		// And the things people do are always recorded.
		{"/api/v1/terminal-sessions/session-1/approve", http.StatusOK, true},
		{"/api/v1/resources", http.StatusCreated, true},
		{"/api/v1/resources/resource-1", http.StatusForbidden, true},
	}
	for _, test := range cases {
		request, err := http.NewRequest(http.MethodPost, test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := shouldAudit(request, test.status); got != test.want {
			t.Errorf("shouldAudit(POST %s, %d) = %v, want %v", test.path, test.status, got, test.want)
		}
	}
}
