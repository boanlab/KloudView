package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func TestEnrollmentTokenLifecycle(t *testing.T) {
	memory := NewMemory()
	now := time.Now().UTC()
	memory.PutEnrollmentToken(domain.EnrollmentToken{
		ID: "t1", Hash: HashEnrollmentToken("secret"), MaxUses: 1,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	})

	if _, ok := memory.FindEnrollmentToken("wrong"); ok {
		t.Fatal("a wrong value matched")
	}
	if _, ok := memory.FindEnrollmentToken("secret"); !ok {
		t.Fatal("a valid value did not match")
	}
	// Finding does not spend the use.
	if _, ok := memory.FindEnrollmentToken("secret"); !ok {
		t.Fatal("find consumed the token")
	}
	memory.UseEnrollmentToken("t1")
	if _, ok := memory.FindEnrollmentToken("secret"); ok {
		t.Fatal("token accepted beyond its use limit")
	}
}

func TestEnrollmentTokenExpiryAndRevocation(t *testing.T) {
	memory := NewMemory()
	now := time.Now().UTC()
	memory.PutEnrollmentToken(domain.EnrollmentToken{
		ID: "expired", Hash: HashEnrollmentToken("old"),
		CreatedAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(-time.Minute),
	})
	memory.PutEnrollmentToken(domain.EnrollmentToken{
		ID: "live", Hash: HashEnrollmentToken("new"),
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	if _, ok := memory.FindEnrollmentToken("old"); ok {
		t.Fatal("an expired token was accepted")
	}
	if _, ok := memory.RevokeEnrollmentToken("live"); !ok {
		t.Fatal("revoke reported no token")
	}
	if _, ok := memory.FindEnrollmentToken("new"); ok {
		t.Fatal("a revoked token was accepted")
	}
	if len(memory.EnrollmentTokens()) != 2 {
		t.Fatalf("tokens = %d, want both listed until the retention cutoff", len(memory.EnrollmentTokens()))
	}
}

// The hash is hidden from API responses with `json:"-"`, and persistence uses
// the same struct. Without a carrier for it a restart leaves every issued token
// listed and unexpired but permanently unmatchable, so no agent can enrol.
func TestEnrollmentTokenSurvivesAReload(t *testing.T) {
	memory := NewMemory()
	value := "enrollment-token-value-0123456789"
	now := time.Now().UTC()
	memory.PutEnrollmentToken(domain.EnrollmentToken{
		ID: "enroll-1", Prefix: "enro", Hash: HashEnrollmentToken(value),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	if _, ok := memory.FindEnrollmentToken(value); !ok {
		t.Fatal("the token does not match before a reload")
	}

	data, err := memory.MarshalState(false)
	if err != nil {
		t.Fatal(err)
	}
	var state snapshot
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	reloaded := NewMemory()
	reloaded.restore(state)

	if _, ok := reloaded.FindEnrollmentToken(value); !ok {
		t.Fatal("the token no longer matches after a reload")
	}
	// The hash still stays out of an API response.
	listed, err := json.Marshal(reloaded.EnrollmentTokens())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(listed), HashEnrollmentToken(value)) {
		t.Fatal("the hash is exposed in the API representation")
	}
}

// The credential hashes are hidden from API responses the same way the token
// hash is. Losing them on a restart locks out every agent that has rotated,
// because the derived-value fallback only covers the first restart.
func TestAgentCredentialSurvivesAReload(t *testing.T) {
	memory := NewMemory()
	memory.UpsertAgent(domain.Agent{
		ID: "agent-01", NodeID: "node-01", Hostname: "node-01",
		CredentialHash: "current-hash", PreviousHash: "previous-hash",
	})

	data, err := memory.MarshalState(false)
	if err != nil {
		t.Fatal(err)
	}
	var state snapshot
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	reloaded := NewMemory()
	reloaded.restore(state)

	agent, ok := reloaded.Agent("agent-01")
	if !ok {
		t.Fatal("the agent did not survive the reload")
	}
	if agent.CredentialHash != "current-hash" || agent.PreviousHash != "previous-hash" {
		t.Fatalf("credential hashes lost: %+v", agent)
	}
	listed, err := json.Marshal(reloaded.ListAgents())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(listed), "current-hash") {
		t.Fatal("the credential hash is exposed in the API representation")
	}
}
