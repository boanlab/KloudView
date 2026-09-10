package api

import (
	"net/http"
	"regexp"

	"github.com/kloudview/kloudview/apps/server/internal/access"
)

// Log lines are stored as the node produced them so an investigation is not
// missing the one value it needed. Secret material is masked on the way out
// instead, for every reader without the raw grant.
//
// The separator must be an assignment, not bare whitespace, or "Failed password
// for invalid user admin" loses its "for" and the most common brute-force line
// becomes unreadable.
var secretPatterns = []struct {
	pattern  *regexp.Regexp
	template string
}{
	{regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key)(["']?\s*[=:]\s*["']?)([^\s"',;]+)`), "${1}${2}[REDACTED]"},
	{regexp.MustCompile(`(?i)(authorization:\s*\w+\s+|bearer\s+)([A-Za-z0-9._~+/=-]{8,})`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(\b[A-Za-z0-9._%+-]+:)[^\s@]{6,}@`), "${1}[REDACTED]@"},
}

// redactSecrets masks credential values only. Usernames, source addresses,
// ports, ttys, and commands are what attributes a failure to someone and are
// deliberately left intact.
func redactSecrets(line string) string {
	for _, rule := range secretPatterns {
		line = rule.pattern.ReplaceAllString(line, rule.template)
	}
	return line
}

// rawLogAction is granted by the wildcard permission that only role-admin
// holds, so raw lines stay admin-only until a role is given it explicitly.
const rawLogResource, rawLogAction = "logs", "read-raw"

// canReadRawLogs reports whether this subject sees unmasked lines.
func (s *Server) canReadRawLogs(r *http.Request) bool {
	subject := s.subjectFromRequest(r)
	if subject == "" {
		return false
	}
	return s.access.Evaluate(access.Request{SubjectID: subject, Resource: rawLogResource, Action: rawLogAction}).Allowed
}
