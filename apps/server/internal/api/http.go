package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/access"
	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func stableID(prefix, value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer(" ", "-", ".", "-", "_", "-").Replace(value)
	return prefix + "-" + value
}

func readJSON(r *http.Request, target any) error {
	return readJSONLimit(r, target, 1<<20)
}

// readJSONLimit is for endpoints that legitimately carry bulk. A body past the
// limit fails to parse rather than being silently truncated.
func readJSONLimit(r *http.Request, target any, limit int64) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, limit))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// readJSONNumbers decodes into an untyped map without routing numbers through
// float64. Inventory identifiers and byte counters are only ever stringified,
// and float64 formats values at or above 1e6 in scientific notation and loses
// precision above 2^53. json.Number keeps the literal as sent.
func readJSONNumbers(r *http.Request, target any, limit int64) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, limit))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (s *Server) require(resource, action string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject := s.subjectFromRequest(r)
		if subject == "" {
			writeError(w, http.StatusUnauthorized, "subject_required", "authentication is required")
			return
		}
		decision := s.access.Evaluate(access.Request{SubjectID: subject, Resource: resource, Action: action, ResourcePath: r.Header.Get("X-KloudView-Scope")})
		if !decision.Allowed {
			writeError(w, http.StatusForbidden, "access_denied", decision.Reason)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAgentKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agentID := r.PathValue("id")
		credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if agentID == "" {
			writeError(w, http.StatusUnauthorized, "agent_authentication_failed", "valid agent credential is required")
			return
		}
		agent, _ := s.store.Agent(agentID)
		if s.matchAgentCredential(agent, agentID, credential) == credentialNone {
			writeError(w, http.StatusUnauthorized, "agent_authentication_failed", "valid agent credential is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) agentCredential(agentID string) string {
	mac := hmac.New(sha256.New, []byte(s.credentialKey))
	mac.Write([]byte(agentID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func cors(next http.Handler) http.Handler {
	allowedOrigin := os.Getenv("KLOUDVIEW_CORS_ORIGIN")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin != allowedOrigin && !sameOrigin(origin, r) {
			writeError(w, http.StatusForbidden, "origin_denied", "cross-origin request is not allowed")
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-KloudView-Subject, X-KloudView-Scope")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(origin string, r *http.Request) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host != r.Host {
		return false
	}
	return strings.EqualFold(parsed.Scheme, requestScheme(r))
}

// requestScheme is the scheme the browser used, which is not the scheme the
// server was reached on when a proxy terminates TLS in front of it. Reading
// r.TLS alone rejects every same-origin request behind such a proxy.
//
// A cross-origin caller cannot exploit the header: setting it makes the request
// preflighted, and the preflight carries the real Origin and is refused.
func requestScheme(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme, _, _ := strings.Cut(forwarded, ",")
		return strings.ToLower(strings.TrimSpace(scheme))
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; connect-src 'self' http: ws: wss:; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		writer := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(writer, r)
		if shouldAudit(r, writer.status) {
			actor := s.subjectFromRequest(r)
			metadata := map[string]string{"status": fmt.Sprintf("%d", writer.status)}
			if actor == "" {
				actor = "agent"
				if agent, ok := s.store.Agent(r.PathValue("id")); ok {
					metadata["resourceId"] = agent.NodeID
				}
			} else if writer.status != http.StatusUnauthorized && writer.status != http.StatusForbidden {
				metadata["scope"] = r.Header.Get("X-KloudView-Scope")
			}
			result := "succeeded"
			if writer.status >= 400 {
				result = "failed"
			}
			s.store.AddAudit(domain.AuditEvent{ID: fmt.Sprintf("audit-%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(), Actor: actor, Action: r.Method, Target: r.URL.Path, Result: result, SourceIP: r.RemoteAddr, Metadata: metadata})
		}
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func shouldAudit(r *http.Request, status int) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodOptions || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		return false
	}
	if strings.HasSuffix(r.URL.Path, "/metrics") || strings.HasSuffix(r.URL.Path, "/heartbeat") {
		return false
	}
	if strings.HasSuffix(r.URL.Path, "/operations/claim") && status == http.StatusNoContent {
		return false
	}
	return true
}
