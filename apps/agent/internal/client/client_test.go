package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kloudview/kloudview/apps/agent/internal/inventory"
)

func TestEnrollmentIncludesProtocolVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["protocolVersion"] != protocolVersion {
			t.Fatalf("protocolVersion = %v", payload["protocolVersion"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agent":{"id":"agent-node-01","nodeId":"node-node-01"},"credential":"credential"}`))
	}))
	defer server.Close()

	client := New(server.URL, "test-token", "0.1.0", false, false)
	agentID, nodeID, err := client.Enroll(inventory.Host{Hostname: "node-01"})
	if err != nil {
		t.Fatal(err)
	}
	if agentID != "agent-node-01" || nodeID != "node-node-01" {
		t.Fatalf("agent = %s, node = %s", agentID, nodeID)
	}
}

// The fleet view reports these as what the node is collecting, so every
// enabled collection has to appear.
func TestCapabilitiesNameEveryRunningCollection(t *testing.T) {
	for _, want := range []struct {
		terminal, logs bool
		capabilities   []string
	}{
		{false, false, []string{"inventory", "metrics"}},
		{true, false, []string{"inventory", "metrics", "terminal"}},
		{false, true, []string{"inventory", "metrics", "logs"}},
		{true, true, []string{"inventory", "metrics", "terminal", "logs"}},
	} {
		got := New("http://localhost", "token", "0.1.0", want.terminal, want.logs).capabilities()
		if len(got) != len(want.capabilities) {
			t.Fatalf("terminal=%v logs=%v: capabilities = %v, want %v", want.terminal, want.logs, got, want.capabilities)
		}
		for i, name := range want.capabilities {
			if got[i] != name {
				t.Fatalf("terminal=%v logs=%v: capabilities = %v, want %v", want.terminal, want.logs, got, want.capabilities)
			}
		}
	}
}

func TestRequiresEnrollment(t *testing.T) {
	if !RequiresEnrollment(&HTTPError{StatusCode: http.StatusUnauthorized}) {
		t.Fatal("unauthorized response must require enrollment")
	}
	if RequiresEnrollment(&HTTPError{StatusCode: http.StatusInternalServerError}) {
		t.Fatal("server failure must preserve the current identity")
	}
	if RequiresEnrollment(errors.New("network unavailable")) {
		t.Fatal("network failure must preserve the current identity")
	}
}
