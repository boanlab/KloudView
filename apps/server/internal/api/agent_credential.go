package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// credentialRotationAge is how long an agent may keep one runtime credential.
// The clock only advances while the agent is checking in, so a host that was
// off for a week still authenticates with what it stored.
const credentialRotationAge = 24 * time.Hour

func hashCredential(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func newCredential() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// credentialMatch reports which stored credential a presented value matches.
type credentialMatch int

const (
	credentialNone credentialMatch = iota
	credentialCurrent
	credentialPrevious
	credentialDerived
)

// matchAgentCredential compares a presented value against the agent's current
// and previous credentials, then against the value derived from its ID.
func (s *Server) matchAgentCredential(agent domain.Agent, agentID, presented string) credentialMatch {
	hashed := hashCredential(presented)
	if agent.CredentialHash != "" &&
		subtle.ConstantTimeCompare([]byte(agent.CredentialHash), []byte(hashed)) == 1 {
		return credentialCurrent
	}
	if agent.PreviousHash != "" &&
		subtle.ConstantTimeCompare([]byte(agent.PreviousHash), []byte(hashed)) == 1 {
		return credentialPrevious
	}
	if subtle.ConstantTimeCompare([]byte(presented), []byte(s.agentCredential(agentID))) == 1 {
		return credentialDerived
	}
	return credentialNone
}

// rotateAgentCredential issues a new credential when the current one is old
// enough, keeping the previous value valid until the agent uses the new one.
// It returns the value to hand back, or "" when nothing changed.
func (s *Server) rotateAgentCredential(agent domain.Agent, match credentialMatch) (domain.Agent, string) {
	now := time.Now().UTC()
	// The agent proved it holds the new credential: retire the old one.
	if match == credentialCurrent && agent.PreviousHash != "" {
		agent.PreviousHash = ""
		agent = s.store.UpsertAgent(agent)
	}
	fresh := agent.CredentialHash != "" && now.Sub(agent.CredentialIssuedAt) < credentialRotationAge
	if fresh || match == credentialPrevious {
		return agent, ""
	}
	value, err := newCredential()
	if err != nil {
		return agent, ""
	}
	// Only supersede a credential the agent actually holds.
	if match == credentialCurrent {
		agent.PreviousHash = agent.CredentialHash
	}
	agent.CredentialHash = hashCredential(value)
	agent.CredentialIssuedAt = now
	return s.store.UpsertAgent(agent), value
}
