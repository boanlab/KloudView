package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

const terminalTicketLifetime = 30 * time.Second

type terminalMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId,omitempty"`
	Data      []byte `json:"data,omitempty"`
	Cols      uint16 `json:"cols,omitempty"`
	Rows      uint16 `json:"rows,omitempty"`
	Code      int    `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

type terminalTicket struct {
	SessionID string
	Subject   string
	Writable  bool
	ExpiresAt time.Time
}

type terminalSocket struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *terminalSocket) write(ctx context.Context, message terminalMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return s.conn.Write(ctx, websocket.MessageText, payload)
}

type browserTerminal struct {
	socket   *terminalSocket
	targetID string
	writable bool
}

type terminalHub struct {
	mu       sync.Mutex
	agents   map[string]*terminalSocket
	browsers map[string]*browserTerminal
	tickets  map[string]terminalTicket
	// What the shell has echoed back on the current line, per session. The
	// agent stream writes it and the browser stream reads it, so it lives
	// here rather than on either side.
	lines map[string]*promptLine
}

func newTerminalHub() *terminalHub {
	return &terminalHub{agents: map[string]*terminalSocket{}, browsers: map[string]*browserTerminal{}, tickets: map[string]terminalTicket{}}
}

func (h *terminalHub) issue(ticket terminalTicket) (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(value)
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for key, current := range h.tickets {
		if now.After(current.ExpiresAt) {
			delete(h.tickets, key)
		}
	}
	h.tickets[token] = ticket
	return token, nil
}

func (h *terminalHub) consume(token, sessionID string) (terminalTicket, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ticket, ok := h.tickets[token]
	delete(h.tickets, token)
	return ticket, ok && ticket.SessionID == sessionID && time.Now().Before(ticket.ExpiresAt)
}

func (h *terminalHub) registerAgent(targetID string, socket *terminalSocket) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agents[targetID] = socket
	items := []string{}
	for sessionID, browser := range h.browsers {
		if browser.targetID == targetID {
			items = append(items, sessionID)
		}
	}
	return items
}

func (h *terminalHub) removeAgent(targetID string, socket *terminalSocket) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.agents[targetID] == socket {
		delete(h.agents, targetID)
	}
}

func (h *terminalHub) registerBrowser(sessionID string, browser *browserTerminal) *terminalSocket {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.browsers[sessionID] = browser
	return h.agents[browser.targetID]
}

func (h *terminalHub) removeBrowser(sessionID string, browser *browserTerminal) *terminalSocket {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.browsers[sessionID] == browser {
		delete(h.browsers, sessionID)
		return h.agents[browser.targetID]
	}
	return nil
}

func (h *terminalHub) browser(sessionID string) *browserTerminal {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.browsers[sessionID]
}

func (h *terminalHub) closeSession(sessionID string) {
	h.mu.Lock()
	browser := h.browsers[sessionID]
	delete(h.browsers, sessionID)
	var agent *terminalSocket
	if browser != nil {
		agent = h.agents[browser.targetID]
	}
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if agent != nil {
		_ = agent.write(ctx, terminalMessage{Type: "close", SessionID: sessionID})
	}
	if browser != nil {
		_ = browser.socket.conn.Close(websocket.StatusNormalClosure, "session closed")
	}
}

func (s *Server) createTerminalStreamTicket(w http.ResponseWriter, r *http.Request) {
	session, ok := s.store.Terminal(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "terminal session not found")
		return
	}
	if session.Status != "active" {
		writeError(w, http.StatusConflict, "inactive_session", "terminal session is not active")
		return
	}
	if !s.authorizeResourceTarget(r, "terminal", "read", session.TargetID) {
		writeError(w, http.StatusForbidden, "access_denied", "terminal target scope is not assigned")
		return
	}
	writable := s.authorizeResourceTarget(r, "terminal", "create", session.TargetID)
	ticket, err := s.terminal.issue(terminalTicket{SessionID: session.ID, Subject: s.subjectFromRequest(r), Writable: writable, ExpiresAt: time.Now().Add(terminalTicketLifetime)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ticket_failed", "terminal stream ticket could not be created")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ticket": ticket, "expiresInSeconds": int(terminalTicketLifetime.Seconds()), "writable": writable})
}

// terminalKeystrokeLimit caps one keystroke message. A burst of typing or a
// paste is small; anything larger is not a person at a keyboard.
const terminalKeystrokeLimit = 4096

// terminalEchoSettle is how long the echo is given to come back before the
// prompt line is read. A Return arrives while the last few characters are
// still in flight, so the line is read once the output has gone quiet.
const (
	terminalEchoSettle   = 20 * time.Millisecond
	terminalEchoMinimum  = 15 * time.Millisecond
	terminalEchoDeadline = 250 * time.Millisecond
)

// cutAtReturn splits a keystroke burst at the first Return.
func cutAtReturn(data []byte) (before, after []byte, found bool) {
	index := bytes.IndexAny(data, "\r\n")
	if index < 0 {
		return data, nil, false
	}
	return data[:index], data[index+1:], true
}

// returnAllowed reads the line the shell has echoed and puts it to the deny
// policy. A full-screen program owns the terminal outright, and there is no
// command line inside one to judge.
func (s *Server) returnAllowed(session domain.TerminalSession) bool {
	line := s.terminal.promptLine(session.ID)
	if line == nil {
		return true
	}
	time.Sleep(terminalEchoMinimum)
	deadline := time.Now().Add(terminalEchoDeadline)
	for line.quiet() < terminalEchoSettle && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	text, alternate := line.snapshot()
	if alternate || strings.TrimSpace(text) == "" {
		return true
	}
	for _, candidate := range commandCandidates(text) {
		if !s.terminalCommandAllowed(candidate) {
			return false
		}
	}
	return true
}

// forwardTerminal hands a message to the agent holding this session.
func (s *Server) forwardTerminal(ctx context.Context, socket *terminalSocket, session domain.TerminalSession, message terminalMessage) {
	message.SessionID = session.ID
	s.terminal.mu.Lock()
	agent := s.terminal.agents[session.TargetID]
	s.terminal.mu.Unlock()
	if agent == nil || agent.write(ctx, message) != nil {
		_ = socket.write(ctx, terminalMessage{Type: "status", Message: "agent disconnected"})
	}
}

func (s *Server) terminalBrowserStream(w http.ResponseWriter, r *http.Request) {
	session, ok := s.store.Terminal(r.PathValue("id"))
	if !ok || session.Status != "active" {
		writeError(w, http.StatusNotFound, "inactive_session", "active terminal session not found")
		return
	}
	ticket, ok := s.terminal.consume(r.URL.Query().Get("ticket"), session.ID)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "valid terminal stream ticket is required")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	socket := &terminalSocket{conn: conn}
	browser := &browserTerminal{socket: socket, targetID: session.TargetID, writable: ticket.Writable}
	s.store.AppendTerminalRecording(session.ID, session.TargetID, "control", "session connected\n", time.Now())
	agent := s.terminal.registerBrowser(session.ID, browser)
	ctx := r.Context()
	if agent == nil {
		_ = socket.write(ctx, terminalMessage{Type: "status", SessionID: session.ID, Message: "waiting for agent"})
	} else {
		_ = agent.write(ctx, terminalMessage{Type: "open", SessionID: session.ID, Cols: 120, Rows: 32})
	}
	defer func() {
		if peer := s.terminal.removeBrowser(session.ID, browser); peer != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = peer.write(closeCtx, terminalMessage{Type: "close", SessionID: session.ID})
		}
		_ = conn.Close(websocket.StatusNormalClosure, "stream closed")
	}()
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var message terminalMessage
		if json.Unmarshal(payload, &message) != nil {
			continue
		}
		switch message.Type {
		case "input":
			if !browser.writable {
				_ = socket.write(ctx, terminalMessage{Type: "error", Message: "terminal is read-only"})
				continue
			}
			masked := maskTerminalData(string(message.Data))
			if !s.terminalInputAllowed(string(message.Data)) {
				s.store.AppendTerminalRecording(session.ID, session.TargetID, "blocked", "[BLOCKED] "+masked, time.Now())
				_ = socket.write(ctx, terminalMessage{Type: "error", Message: "command blocked by terminal policy"})
				continue
			}
			s.store.AppendTerminalRecording(session.ID, session.TargetID, "input", masked, time.Now())
		case "keys":
			if !browser.writable {
				_ = socket.write(ctx, terminalMessage{Type: "error", Message: "terminal is read-only"})
				continue
			}
			if len(message.Data) > terminalKeystrokeLimit {
				_ = socket.write(ctx, terminalMessage{Type: "error", Message: "terminal input limit exceeded"})
				continue
			}
			// Keystrokes are not written to the recording. Out of the
			// program's context they say nothing -- "j j x" is not an act
			// anyone can review -- while the output recording already holds
			// every screen they produced. Keeping them out also keeps a
			// password typed at an unechoed prompt out of the recording.
			//
			// A Return is where the deny policy gets its say: everything
			// before it has been echoed back, so the line it is about to run
			// can be read off the screen.
			if before, after, found := cutAtReturn(message.Data); found {
				if len(before) > 0 {
					s.forwardTerminal(ctx, socket, session, terminalMessage{Type: "keys", Data: before})
				}
				if !s.returnAllowed(session) {
					s.store.AppendTerminalRecording(session.ID, session.TargetID, "blocked", "[BLOCKED] the command on the prompt line is refused by terminal policy\n", time.Now())
					_ = socket.write(ctx, terminalMessage{Type: "error", Message: "command blocked by terminal policy"})
					// The Return is dropped and the line stays on the prompt
					// for the operator to see and change.
					continue
				}
				message.Data = append([]byte{'\r'}, after...)
			}
		case "reply":
			// A terminal's answer to a question the program asked, not
			// something the operator typed -- so it crosses in either mode.
			// It is let through only because its shape is checked: a cursor
			// report or a device attribute and nothing else, which leaves no
			// room to smuggle a command past the deny policy.
			if !browser.writable {
				continue
			}
			if !terminalReportPattern.Match(message.Data) {
				_ = socket.write(ctx, terminalMessage{Type: "error", Message: "malformed terminal report"})
				continue
			}

		case "resize":
			if message.Cols > 0 && message.Rows > 0 {
				// The geometry belongs in the recording: a full-screen program
				// draws by absolute position, so a replay at the wrong width
				// puts its rows in the wrong place.
				s.store.AppendTerminalRecording(session.ID, session.TargetID, "control",
					fmt.Sprintf("screen %dx%d\n", message.Cols, message.Rows), time.Now())
			}
		default:
			continue
		}
		message.SessionID = session.ID
		s.terminal.mu.Lock()
		agent = s.terminal.agents[session.TargetID]
		s.terminal.mu.Unlock()
		if agent == nil || agent.write(ctx, message) != nil {
			_ = socket.write(ctx, terminalMessage{Type: "status", Message: "agent disconnected"})
		}
	}
}

// promptLine is the echo tracker for a session, created when the first output
// arrives and dropped with the session.
func (h *terminalHub) promptLine(sessionID string) *promptLine {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lines[sessionID]
}

func (h *terminalHub) trackOutput(sessionID string, data []byte) {
	h.mu.Lock()
	line := h.lines[sessionID]
	if line == nil {
		if h.lines == nil {
			h.lines = map[string]*promptLine{}
		}
		line = newPromptLine()
		h.lines[sessionID] = line
	}
	h.mu.Unlock()
	line.feed(data)
}

func (h *terminalHub) forgetLine(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.lines, sessionID)
}

func (s *Server) terminalAgentStream(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.store.Agent(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "agent_not_found", "agent enrollment required")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	socket := &terminalSocket{conn: conn}
	sessions := s.terminal.registerAgent(agent.NodeID, socket)
	ctx := r.Context()
	for _, sessionID := range sessions {
		_ = socket.write(ctx, terminalMessage{Type: "open", SessionID: sessionID, Cols: 120, Rows: 32})
	}
	defer func() {
		s.terminal.removeAgent(agent.NodeID, socket)
		_ = conn.Close(websocket.StatusNormalClosure, "agent stream closed")
	}()
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var message terminalMessage
		if json.Unmarshal(payload, &message) != nil || message.SessionID == "" {
			continue
		}
		browser := s.terminal.browser(message.SessionID)
		if browser != nil && browser.targetID == agent.NodeID {
			if message.Type == "output" {
				s.store.AppendTerminalRecording(message.SessionID, agent.NodeID, "output", maskTerminalData(string(message.Data)), time.Now())
				// The same bytes the operator is about to see are what the
				// deny policy will read when a Return arrives.
				s.terminal.trackOutput(message.SessionID, message.Data)
			}
			if message.Type == "exit" {
				s.terminal.forgetLine(message.SessionID)
			}
			_ = browser.socket.write(ctx, message)
		}
	}
}
