package api

import "net/http"

func (s *Server) registerExecutionRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/operations", s.require("operations", "read", http.HandlerFunc(s.listOperations)))
	mux.Handle("POST /api/v1/operations", s.require("operations", "create", http.HandlerFunc(s.createOperation)))
	mux.Handle("POST /api/v1/operations/{id}/approve", s.require("operations", "approve", http.HandlerFunc(s.approveOperation)))
	mux.Handle("GET /api/v1/runbooks", s.require("runbooks", "read", http.HandlerFunc(s.listRunbooks)))
	mux.Handle("POST /api/v1/runbooks", s.require("runbooks", "create", http.HandlerFunc(s.createRunbook)))
	mux.Handle("PUT /api/v1/runbooks/{id}", s.require("runbooks", "update", http.HandlerFunc(s.updateRunbook)))
	mux.Handle("DELETE /api/v1/runbooks/{id}", s.require("runbooks", "delete", http.HandlerFunc(s.deleteRunbook)))
	mux.Handle("POST /api/v1/runbooks/{id}/execute", s.require("runbooks", "execute", http.HandlerFunc(s.executeRunbook)))
	mux.Handle("GET /api/v1/runbook-executions", s.require("runbooks", "read", http.HandlerFunc(s.listRunbookExecutions)))
	mux.Handle("POST /api/v1/runbook-executions/{id}/approve", s.require("runbooks", "approve", http.HandlerFunc(s.approveRunbookExecution)))
	mux.Handle("GET /api/v1/terminal-sessions", s.require("terminal", "read", http.HandlerFunc(s.listTerminalSessions)))
	mux.Handle("POST /api/v1/terminal-sessions", s.require("terminal", "create", http.HandlerFunc(s.createTerminalSession)))
	mux.Handle("POST /api/v1/terminal-sessions/{id}/approve", s.require("terminal", "approve", http.HandlerFunc(s.approveTerminalSession)))
	mux.Handle("POST /api/v1/terminal-sessions/{id}/close", s.require("terminal", "close", http.HandlerFunc(s.closeTerminalSession)))
	mux.Handle("POST /api/v1/terminal-sessions/{id}/stream-ticket", s.require("terminal", "read", http.HandlerFunc(s.createTerminalStreamTicket)))
	mux.Handle("GET /api/v1/terminal-sessions/{id}/stream", http.HandlerFunc(s.terminalBrowserStream))
	mux.Handle("GET /api/v1/terminal-sessions/{id}/recording", s.require("terminal", "read", http.HandlerFunc(s.getTerminalRecording)))
	mux.Handle("DELETE /api/v1/terminal-sessions/{id}/recording", s.require("terminal", "close", http.HandlerFunc(s.deleteTerminalRecording)))
	mux.Handle("GET /api/v1/terminal-sessions/{id}/commands", s.require("terminal", "read", http.HandlerFunc(s.listTerminalCommands)))
	mux.Handle("POST /api/v1/terminal-sessions/{id}/commands", s.require("terminal", "create", http.HandlerFunc(s.createTerminalCommand)))
	mux.Handle("POST /api/v1/agents/{id}/terminal/claim", s.requireAgentKey(http.HandlerFunc(s.claimTerminalCommand)))
	mux.Handle("PUT /api/v1/agents/{id}/terminal/commands/{commandId}", s.requireAgentKey(http.HandlerFunc(s.completeTerminalCommand)))
	mux.Handle("GET /api/v1/agents/{id}/terminal/stream", s.requireAgentKey(http.HandlerFunc(s.terminalAgentStream)))
}
