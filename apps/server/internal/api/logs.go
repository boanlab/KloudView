package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// logBatchLimits bound one report, so a misbehaving or hostile agent cannot
// use the endpoint to fill server memory.
const (
	logBatchMaxLines  = 2000
	logBatchMaxLength = 4000
	logWindowMaxAge   = 24 * time.Hour
	// Sized to the caps above so a full batch parses; the agent's own limits
	// are well inside it.
	logBatchMaxBody = 8 << 20
)

// logBatchRequest is what the agent posts each reporting window.
type logBatchRequest struct {
	NodeID   string         `json:"nodeId"`
	From     time.Time      `json:"from"`
	To       time.Time      `json:"to"`
	Counters map[string]int `json:"counters"`
	Dropped  int            `json:"dropped"`
	Lines    []struct {
		At        time.Time `json:"at"`
		Priority  int       `json:"priority"`
		Unit      string    `json:"unit"`
		Message   string    `json:"message"`
		Repeat    int       `json:"repeat"`
		Container string    `json:"container"`
	} `json:"lines"`
}

func (s *Server) ingestLogs(w http.ResponseWriter, r *http.Request) {
	var request logBatchRequest
	if err := readJSONLimit(r, &request, logBatchMaxBody); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	agentID := r.PathValue("id")
	resource, ok := s.store.Resource(request.NodeID)
	if !ok {
		writeError(w, http.StatusNotFound, "resource_not_found", "node is not registered")
		return
	}
	if resource.AgentID != agentID {
		writeError(w, http.StatusForbidden, "resource_ownership_mismatch", "node is managed by another agent")
		return
	}
	now := time.Now().UTC()
	if request.To.IsZero() {
		request.To = now
	}
	if request.To.Before(now.Add(-logWindowMaxAge)) || request.To.After(now.Add(5*time.Minute)) {
		writeError(w, http.StatusBadRequest, "invalid_window", "window is outside the accepted range")
		return
	}
	if len(request.Lines) > logBatchMaxLines {
		request.Lines = request.Lines[:logBatchMaxLines]
	}
	lines := make([]domain.LogLine, 0, len(request.Lines))
	for _, item := range request.Lines {
		if item.Priority < 0 || item.Priority > 7 {
			continue
		}
		message := strings.TrimSpace(item.Message)
		if message == "" {
			continue
		}
		if len(message) > logBatchMaxLength {
			message = message[:logBatchMaxLength]
		}
		lines = append(lines, domain.LogLine{At: item.At, Priority: item.Priority, Unit: item.Unit, Message: message, Repeat: item.Repeat, Container: item.Container})
	}
	counters := domain.LogCounters{From: request.From, To: request.To, Counts: request.Counters, Dropped: request.Dropped}
	s.store.AddLogBatch(request.NodeID, agentID, counters, lines)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) listLogLines(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("nodeId")
	if nodeID != "" && !s.authorizeResourceTarget(r, "resources", "read", nodeID) {
		writeError(w, http.StatusForbidden, "access_denied", "node scope is not assigned")
		return
	}
	since, limit := logQuery(r)
	raw := s.canReadRawLogs(r)
	// The scope check decides which nodes are read, rather than filtering the
	// result afterwards, so the store never assembles lines the caller cannot
	// see and the page limit is honest.
	items := s.store.LogLines(s.authorizedLogNodes(r, nodeID), since, limit)
	if !raw {
		for index := range items {
			items[index].Message = redactSecrets(items[index].Message)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "raw": raw})
}

func (s *Server) listLogCounters(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("nodeId")
	if nodeID != "" && !s.authorizeResourceTarget(r, "resources", "read", nodeID) {
		writeError(w, http.StatusForbidden, "access_denied", "node scope is not assigned")
		return
	}
	since, _ := logQuery(r)
	items := s.store.LogCounterWindows(s.authorizedLogNodes(r, nodeID), since)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// authorizedLogNodes is the set of nodes this request may read. A named node
// is already checked by the caller; without one every reporting node is
// checked once, here, instead of once per line.
func (s *Server) authorizedLogNodes(r *http.Request, nodeID string) map[string]bool {
	if nodeID != "" {
		return map[string]bool{nodeID: true}
	}
	allowed := map[string]bool{}
	for _, agent := range s.store.ListAgents() {
		if agent.NodeID != "" && s.authorizeResourceTarget(r, "resources", "read", agent.NodeID) {
			allowed[agent.NodeID] = true
		}
	}
	return allowed
}

func logQuery(r *http.Request) (time.Time, int) {
	minutes := 60
	if value, err := strconv.Atoi(r.URL.Query().Get("minutes")); err == nil && value > 0 {
		minutes = min(value, 24*60)
	}
	limit := 500
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 {
		limit = min(value, 2000)
	}
	return time.Now().UTC().Add(-time.Duration(minutes) * time.Minute), limit
}
