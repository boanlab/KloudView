// Package auth provides local password hashing and in-memory session tokens
// for console (human) authentication. Sessions are intentionally ephemeral:
// a server restart invalidates them and users simply log in again.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword returns a bcrypt hash suitable for storage.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword reports whether password matches the stored bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

type Session struct {
	Token     string
	UserID    string
	Username  string
	ExpiresAt time.Time
}

type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]Session
	ttl      time.Duration
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{sessions: map[string]Session{}, ttl: ttl}
}

func (s *SessionStore) TTL() time.Duration { return s.ttl }

// Create issues a new session for the user and returns it. now must be supplied
// by the caller so behavior stays deterministic and testable.
func (s *SessionStore) Create(userID, username string, now time.Time) Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := Session{Token: randomToken(), UserID: userID, Username: username, ExpiresAt: now.Add(s.ttl)}
	s.sessions[session.Token] = session
	return session
}

// Get returns the session for a token if it exists and has not expired.
func (s *SessionStore) Get(token string, now time.Time) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[token]
	if !ok {
		return Session{}, false
	}
	if now.After(session.ExpiresAt) {
		delete(s.sessions, token)
		return Session{}, false
	}
	return session, true
}

func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// DeleteByUser removes every session belonging to a user (used on disable/delete).
func (s *SessionStore) DeleteByUser(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, session := range s.sessions {
		if session.UserID == userID {
			delete(s.sessions, token)
		}
	}
}

// DeleteByUserExcept revokes a user's sessions except keepToken (used on
// self password change so a changed credential logs out every other device).
func (s *SessionStore) DeleteByUserExcept(userID, keepToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, session := range s.sessions {
		if session.UserID == userID && token != keepToken {
			delete(s.sessions, token)
		}
	}
}

func randomToken() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
