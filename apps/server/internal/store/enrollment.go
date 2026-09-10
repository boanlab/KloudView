package store

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// HashEnrollmentToken derives the stored form of a token value.
func HashEnrollmentToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *Memory) PutEnrollmentToken(token domain.EnrollmentToken) domain.EnrollmentToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enrollmentTokens[token.ID] = token
	return token
}

// EnrollmentTokens returns every token, newest first, dropping ones that
// expired more than a day ago.
func (s *Memory) EnrollmentTokens() []domain.EnrollmentToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	items := make([]domain.EnrollmentToken, 0, len(s.enrollmentTokens))
	for id, token := range s.enrollmentTokens {
		if token.ExpiresAt.Before(cutoff) {
			delete(s.enrollmentTokens, id)
			continue
		}
		items = append(items, token)
	}
	sortByCreatedDesc(items)
	return items
}

func sortByCreatedDesc(items []domain.EnrollmentToken) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].CreatedAt.After(items[j-1].CreatedAt); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func (s *Memory) RevokeEnrollmentToken(id string) (domain.EnrollmentToken, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.enrollmentTokens[id]
	if !ok {
		return domain.EnrollmentToken{}, false
	}
	if token.RevokedAt == nil {
		now := time.Now().UTC()
		token.RevokedAt = &now
		s.enrollmentTokens[id] = token
	}
	return token, true
}

// FindEnrollmentToken returns a token that is unexpired, unrevoked, and still
// within its use limit. The use is not recorded; call UseEnrollmentToken after
// the caller has checked the token's bindings.
func (s *Memory) FindEnrollmentToken(value string) (domain.EnrollmentToken, bool) {
	if value == "" {
		return domain.EnrollmentToken{}, false
	}
	hash := HashEnrollmentToken(value)
	now := time.Now().UTC()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, token := range s.enrollmentTokens {
		if subtle.ConstantTimeCompare([]byte(token.Hash), []byte(hash)) != 1 {
			continue
		}
		if token.RevokedAt != nil || now.After(token.ExpiresAt) {
			return domain.EnrollmentToken{}, false
		}
		if token.MaxUses > 0 && token.Uses >= token.MaxUses {
			return domain.EnrollmentToken{}, false
		}
		return token, true
	}
	return domain.EnrollmentToken{}, false
}

// UseEnrollmentToken records one successful enrolment against a token.
func (s *Memory) UseEnrollmentToken(id string) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.enrollmentTokens[id]
	if !ok {
		return
	}
	token.Uses++
	token.LastUsedAt = &now
	s.enrollmentTokens[id] = token
}
