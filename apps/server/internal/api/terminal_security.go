package api

import (
	"net/http"
	"regexp"
	"strings"
	"time"
)

var terminalKeySecretPattern = regexp.MustCompile(`(?i)(password|passwd|token|secret)(\s*[:=]\s*)([^\s'";]+)`)
var terminalBearerSecretPattern = regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)([^\s]+)`)

func defaultTerminalDenyPatterns() []*regexp.Regexp {
	values := []string{
		`(?i)(^|[;&|]\s*)rm\s+(-[^\s]*r[^\s]*f|-[^\s]*f[^\s]*r)\s+(/|/\*|--no-preserve-root)`,
		`(?i)(^|[;&|]\s*)(mkfs(\.[a-z0-9]+)?|wipefs)(\s|$)`,
		`(?i)(^|[;&|]\s*)dd\s+.*\bof=/dev/`,
		`(?i)(^|[;&|]\s*)(shutdown|reboot|poweroff|halt)(\s|$)`,
	}
	patterns := make([]*regexp.Regexp, 0, len(values))
	for _, value := range values {
		patterns = append(patterns, regexp.MustCompile(value))
	}
	return patterns
}

// terminalReportPattern is every answer the console's emulator is allowed to
// give: a cursor position report, a device status report, a device attributes
// reply, or "I do not know that capability" to an XTGETTCAP query. Nothing
// here can carry a shell command.
var terminalReportPattern = regexp.MustCompile(`^(\x1b\[[?>]?[0-9;]{0,24}[Rnc]|\x1bP0\+r\x1b\\)$`)

func maskTerminalData(value string) string {
	value = terminalKeySecretPattern.ReplaceAllString(value, `${1}${2}[REDACTED]`)
	value = terminalBearerSecretPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	return value
}

func (s *Server) terminalInputAllowed(value string) bool {
	if len(value) > 4096 {
		return false
	}
	for _, pattern := range s.terminalDenyPatterns {
		if pattern.MatchString(value) {
			return false
		}
	}
	return true
}

// terminalCommandAllowed screens a full, possibly multi-line queued command
// against the deny policy. Each line is checked independently so a destructive
// statement on a line other than the first cannot slip past the line-anchored
// patterns before the agent claims the command.
func (s *Server) terminalCommandAllowed(command string) bool {
	if len(command) > 4096 {
		return false
	}
	for _, line := range strings.Split(command, "\n") {
		if !s.terminalInputAllowed(line + "\n") {
			return false
		}
	}
	return true
}

func (s *Server) getTerminalRecording(w http.ResponseWriter, r *http.Request) {
	session, ok := s.store.Terminal(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "terminal session not found")
		return
	}
	if !s.authorizeResourceTarget(r, "terminal", "read", session.TargetID) {
		writeError(w, http.StatusForbidden, "access_denied", "terminal target scope is not assigned")
		return
	}
	recording, ok := s.store.TerminalRecording(session.ID, time.Now())
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "terminal recording not found")
		return
	}
	writeJSON(w, http.StatusOK, recording)
}

func (s *Server) deleteTerminalRecording(w http.ResponseWriter, r *http.Request) {
	session, ok := s.store.Terminal(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "terminal session not found")
		return
	}
	if !s.authorizeResourceTarget(r, "terminal", "close", session.TargetID) {
		writeError(w, http.StatusForbidden, "access_denied", "terminal target scope is not assigned")
		return
	}
	if err := s.store.DeleteTerminalRecording(session.ID); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "terminal recording not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
