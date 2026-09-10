package api

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

const (
	enrollmentTokenMinTTL = time.Minute
	enrollmentTokenMaxTTL = 24 * time.Hour
)

type enrollmentTokenRequest struct {
	TTLSeconds  int    `json:"ttlSeconds"`
	MaxUses     int    `json:"maxUses"`
	Note        string `json:"note"`
	Hostname    string `json:"hostname"`
	AllowedCIDR string `json:"allowedCidr"`
}

// enrollmentAllowed reports whether a presenting host satisfies the token's
// bindings.
func enrollmentAllowed(token domain.EnrollmentToken, hostname, remoteAddr string) bool {
	if token.Hostname != "" && !strings.EqualFold(token.Hostname, hostname) {
		return false
	}
	if token.AllowedCIDR == "" {
		return true
	}
	_, network, err := net.ParseCIDR(token.AllowedCIDR)
	if err != nil {
		return false
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && network.Contains(ip)
}

func (s *Server) listEnrollmentTokens(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.store.EnrollmentTokens()})
}

// createEnrollmentToken issues a value that is returned once and stored only as
// a hash. Any agent may enrol with it until it expires or reaches its use cap.
func (s *Server) createEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	var input enrollmentTokenRequest
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ttl := time.Duration(input.TTLSeconds) * time.Second
	if ttl < enrollmentTokenMinTTL || ttl > enrollmentTokenMaxTTL {
		writeError(w, http.StatusBadRequest, "invalid_ttl", "ttl must be between 1 minute and 24 hours")
		return
	}
	if input.MaxUses < 0 || input.MaxUses > 1000 {
		writeError(w, http.StatusBadRequest, "invalid_max_uses", "maxUses must be between 0 and 1000")
		return
	}
	if len(input.Note) > 200 || len(input.Hostname) > 253 {
		writeError(w, http.StatusBadRequest, "invalid_request", "note or hostname is too long")
		return
	}
	if input.AllowedCIDR != "" {
		if _, _, err := net.ParseCIDR(input.AllowedCIDR); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cidr", "allowedCidr must be a CIDR such as 10.20.0.0/16")
			return
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "token_generation_failed", "could not generate a token")
		return
	}
	value := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().UTC()
	token := s.store.PutEnrollmentToken(domain.EnrollmentToken{
		ID:          fmt.Sprintf("enroll-%d", now.UnixNano()),
		Prefix:      value[:6],
		Hash:        store.HashEnrollmentToken(value),
		Note:        input.Note,
		Hostname:    strings.TrimSpace(input.Hostname),
		AllowedCIDR: strings.TrimSpace(input.AllowedCIDR),
		MaxUses:     input.MaxUses,
		CreatedBy:   s.subjectFromRequest(r),
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "value": value})
}

func (s *Server) revokeEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	token, ok := s.store.RevokeEnrollmentToken(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "enrollment token not found")
		return
	}
	writeJSON(w, http.StatusOK, token)
}
