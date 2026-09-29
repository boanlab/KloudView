package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/auth"
	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

const sessionCookie = "kv_session"

func (s *Server) sessionFromRequest(r *http.Request) (auth.Session, bool) {
	if s.sessions == nil {
		return auth.Session{}, false
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.Session{}, false
	}
	return s.sessions.Get(cookie.Value, time.Now().UTC())
}

// subjectFromRequest resolves the human subject: a valid session cookie takes
// precedence; the X-KloudView-Subject header is only honored in dev-header-auth.
func (s *Server) subjectFromRequest(r *http.Request) string {
	if session, ok := s.sessionFromRequest(r); ok {
		return session.Username
	}
	if s.devHeaderAuth {
		return r.Header.Get("X-KloudView-Subject")
	}
	return ""
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	user, ok := s.store.UserByUsername(input.Username)
	if !ok || user.Status != "active" || !auth.CheckPassword(user.PasswordHash, input.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}
	session := s.sessions.Create(user.ID, user.Username, time.Now().UTC())
	s.setSessionCookie(w, session.Token, session.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{"user": user.Public()})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.Delete(cookie.Value)
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	if session, ok := s.sessionFromRequest(r); ok {
		user, _ := s.store.User(session.UserID)
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated": true,
			"source":        "session",
			"subject":       session.Username,
			"user":          user.Public(),
			"headerAuth":    s.devHeaderAuth,
			"scopePaths":    s.access.PathsForSubject(session.Username),
		})
		return
	}
	if s.devHeaderAuth {
		if subject := r.Header.Get("X-KloudView-Subject"); subject != "" {
			writeJSON(w, http.StatusOK, map[string]any{
				"authenticated": true,
				"source":        "header",
				"subject":       subject,
				"headerAuth":    true,
				"scopePaths":    s.access.PathsForSubject(subject),
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false, "headerAuth": s.devHeaderAuth})
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	items := s.store.ListUsers()
	safe := make([]domain.User, len(items))
	for i, u := range items {
		safe[i] = u.Public()
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": safe})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		Password    string `json:"password"`
		Status      string `json:"status"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" || len(input.Password) < 6 {
		writeError(w, http.StatusBadRequest, "invalid_user", "username is required and password must be at least 6 characters")
		return
	}
	if _, exists := s.store.UserByUsername(input.Username); exists {
		writeError(w, http.StatusConflict, "username_taken", "username already exists")
		return
	}
	hash, err := auth.HashPassword(input.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash_failed", "could not hash password")
		return
	}
	status := input.Status
	if status != "disabled" {
		status = "active"
	}
	now := time.Now().UTC()
	display := input.DisplayName
	if display == "" {
		display = input.Username
	}
	user := domain.User{ID: fmt.Sprintf("user-%d", now.UnixNano()), Username: input.Username, DisplayName: display, PasswordHash: hash, Status: status, CreatedAt: now, UpdatedAt: now}
	writeJSON(w, http.StatusCreated, s.store.PutUser(user).Public())
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	user, ok := s.store.User(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	var input struct {
		DisplayName     *string `json:"displayName"`
		Password        *string `json:"password"`
		CurrentPassword string  `json:"currentPassword"`
		Status          *string `json:"status"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// Changing your own password requires proving you know it, so a hijacked
	// session cannot lock the owner out. An administrator resetting somebody
	// else's password cannot supply it and is not asked for it.
	if input.Password != nil {
		if session, ok := s.sessionFromRequest(r); ok && session.UserID == user.ID {
			if !auth.CheckPassword(user.PasswordHash, input.CurrentPassword) {
				writeError(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect")
				return
			}
		}
	}
	if input.Status != nil && *input.Status == "disabled" {
		if session, ok := s.sessionFromRequest(r); ok && session.UserID == user.ID {
			writeError(w, http.StatusConflict, "self_disable", "you cannot disable your own account")
			return
		}
	}
	if input.DisplayName != nil {
		user.DisplayName = strings.TrimSpace(*input.DisplayName)
	}
	if input.Password != nil {
		if len(*input.Password) < 6 {
			writeError(w, http.StatusBadRequest, "invalid_user", "password must be at least 6 characters")
			return
		}
		hash, err := auth.HashPassword(*input.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "hash_failed", "could not hash password")
			return
		}
		user.PasswordHash = hash
	}
	if input.Status != nil && (*input.Status == "active" || *input.Status == "disabled") {
		user.Status = *input.Status
	}
	user.UpdatedAt = time.Now().UTC()
	updated := s.store.PutUser(user).Public()
	// A disabled account or an admin-forced password reset revokes active sessions.
	if user.Status == "disabled" || input.Password != nil {
		s.sessions.DeleteByUser(user.ID)
	}
	writeJSON(w, http.StatusOK, updated)
}

// updateMe is self-service: the session owner edits their own display name and
// password. A password change requires the correct current password.
func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in to update your profile")
		return
	}
	user, ok := s.store.User(session.UserID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	var input struct {
		DisplayName     *string `json:"displayName"`
		CurrentPassword string  `json:"currentPassword"`
		NewPassword     string  `json:"newPassword"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.DisplayName != nil {
		name := strings.TrimSpace(*input.DisplayName)
		if name == "" {
			writeError(w, http.StatusBadRequest, "invalid_user", "display name cannot be empty")
			return
		}
		user.DisplayName = name
	}
	if input.NewPassword != "" {
		if !auth.CheckPassword(user.PasswordHash, input.CurrentPassword) {
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect")
			return
		}
		if len(input.NewPassword) < 6 {
			writeError(w, http.StatusBadRequest, "invalid_user", "new password must be at least 6 characters")
			return
		}
		if input.NewPassword == input.CurrentPassword {
			writeError(w, http.StatusBadRequest, "invalid_user", "new password must differ from the current password")
			return
		}
		hash, err := auth.HashPassword(input.NewPassword)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "hash_failed", "could not hash password")
			return
		}
		user.PasswordHash = hash
	}
	user.UpdatedAt = time.Now().UTC()
	updated := s.store.PutUser(user).Public()
	if input.NewPassword != "" {
		s.sessions.DeleteByUserExcept(user.ID, session.Token)
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if session, ok := s.sessionFromRequest(r); ok && session.UserID == id {
		writeError(w, http.StatusConflict, "self_delete", "you cannot delete your own account")
		return
	}
	if err := s.store.DeleteUser(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	s.sessions.DeleteByUser(id)
	w.WriteHeader(http.StatusNoContent)
}
