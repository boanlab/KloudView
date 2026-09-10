package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/store"
)

type enrollResponse struct {
	Agent      struct{ ID, NodeID string } `json:"agent"`
	Credential string                      `json:"credential"`
}

type beatResponse struct {
	Credential string `json:"credential"`
}

func enrolAgent(t *testing.T, handler http.Handler) enrollResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll",
		strings.NewReader(`{"token":"test-token","hostname":"node-01","version":"0.1.0","protocolVersion":"1"}`)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("enrol status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response enrollResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func heartbeat(t *testing.T, handler http.Handler, agentID, nodeID, credential string) (int, string) {
	t.Helper()
	body := `{"nodeId":"` + nodeID + `","hostname":"node-01","version":"0.1.0","protocolVersion":"1"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agentID+"/heartbeat", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+credential)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var response beatResponse
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	return recorder.Code, response.Credential
}

func TestEnrollmentIssuesAStoredCredential(t *testing.T) {
	memory := store.NewMemory()
	server := New(memory, "test-token", "")
	enrolled := enrolAgent(t, server.Handler())

	if enrolled.Credential == "" {
		t.Fatal("no credential returned")
	}
	if enrolled.Credential == server.agentCredential(enrolled.Agent.ID) {
		t.Fatal("the credential is the derived value, not a stored secret")
	}
	agent, _ := memory.Agent(enrolled.Agent.ID)
	if agent.CredentialHash != hashCredential(enrolled.Credential) {
		t.Fatal("the store does not hold the hash of the issued credential")
	}
	if code, _ := heartbeat(t, server.Handler(), enrolled.Agent.ID, enrolled.Agent.NodeID, enrolled.Credential); code != http.StatusOK {
		t.Fatalf("heartbeat with the issued credential = %d", code)
	}
	if code, _ := heartbeat(t, server.Handler(), enrolled.Agent.ID, enrolled.Agent.NodeID, "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("heartbeat with a wrong credential = %d", code)
	}
}

// Rotation is driven by the agent's own check-ins: a fresh credential is left
// alone, an aged one is replaced, and the value it replaces keeps working until
// the agent proves it stored the new one.
func TestCredentialRotationKeepsThePreviousValueUntilTheNewOneIsUsed(t *testing.T) {
	memory := store.NewMemory()
	server := New(memory, "test-token", "")
	handler := server.Handler()
	enrolled := enrolAgent(t, handler)

	code, rotated := heartbeat(t, handler, enrolled.Agent.ID, enrolled.Agent.NodeID, enrolled.Credential)
	if code != http.StatusOK || rotated != "" {
		t.Fatalf("a fresh credential rotated: code = %d, rotated = %v", code, rotated != "")
	}

	// Age the credential past the rotation window.
	agent, _ := memory.Agent(enrolled.Agent.ID)
	agent.CredentialIssuedAt = time.Now().UTC().Add(-2 * credentialRotationAge)
	memory.UpsertAgent(agent)

	code, rotated = heartbeat(t, handler, enrolled.Agent.ID, enrolled.Agent.NodeID, enrolled.Credential)
	if code != http.StatusOK || rotated == "" {
		t.Fatalf("an aged credential did not rotate: code = %d", code)
	}
	if rotated == enrolled.Credential {
		t.Fatal("rotation returned the same value")
	}

	// The agent may have failed to persist the new value; the old one still works.
	if code, _ := heartbeat(t, handler, enrolled.Agent.ID, enrolled.Agent.NodeID, enrolled.Credential); code != http.StatusOK {
		t.Fatalf("the previous credential stopped working immediately: %d", code)
	}
	// Once it uses the new one, the old one is retired.
	if code, _ := heartbeat(t, handler, enrolled.Agent.ID, enrolled.Agent.NodeID, rotated); code != http.StatusOK {
		t.Fatalf("the rotated credential was refused: %d", code)
	}
	if code, _ := heartbeat(t, handler, enrolled.Agent.ID, enrolled.Agent.NodeID, enrolled.Credential); code != http.StatusUnauthorized {
		t.Fatalf("the previous credential still works after the new one was used: %d", code)
	}
}

// An agent holding only the derived value authenticates with it, and is moved
// to a stored credential on its next check-in.
func TestDerivedCredentialStillAuthenticatesAndMigrates(t *testing.T) {
	memory := store.NewMemory()
	server := New(memory, "test-token", "").WithAgentCredentialKey("legacy-server-only-credential-key-000")
	handler := server.Handler()
	enrolled := enrolAgent(t, handler)

	// Simulate an agent from before the change: no stored credential.
	agent, _ := memory.Agent(enrolled.Agent.ID)
	agent.CredentialHash, agent.PreviousHash = "", ""
	memory.UpsertAgent(agent)

	derived := server.agentCredential(enrolled.Agent.ID)
	code, rotated := heartbeat(t, handler, enrolled.Agent.ID, enrolled.Agent.NodeID, derived)
	if code != http.StatusOK {
		t.Fatalf("the derived credential was refused: %d", code)
	}
	if rotated == "" {
		t.Fatal("the agent was not migrated to a stored credential")
	}
	if code, _ := heartbeat(t, handler, enrolled.Agent.ID, enrolled.Agent.NodeID, rotated); code != http.StatusOK {
		t.Fatalf("the migrated credential was refused: %d", code)
	}
}
