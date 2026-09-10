package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

const terminalOutputLimit = 10 << 20

type terminalMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId,omitempty"`
	Data      []byte `json:"data,omitempty"`
	Cols      uint16 `json:"cols,omitempty"`
	Rows      uint16 `json:"rows,omitempty"`
	Message   string `json:"message,omitempty"`
}

type ptySession struct {
	file   *os.File
	cancel context.CancelFunc
	total  int
	input  []byte
}

type terminalWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *terminalWriter) send(ctx context.Context, message terminalMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return w.conn.Write(ctx, websocket.MessageText, payload)
}

func (e *Executor) ServeTerminalStream(ctx context.Context, conn *websocket.Conn) error {
	writer := &terminalWriter{conn: conn}
	sessions := map[string]*ptySession{}
	var mu sync.Mutex
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, session := range sessions {
			session.cancel()
			_ = session.file.Close()
		}
	}()
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var message terminalMessage
		if json.Unmarshal(payload, &message) != nil || message.SessionID == "" {
			continue
		}
		mu.Lock()
		session := sessions[message.SessionID]
		mu.Unlock()
		switch message.Type {
		case "open":
			if session != nil {
				continue
			}
			terminal, err := e.startPTY(ctx, message.Cols, message.Rows)
			if err != nil {
				_ = writer.send(ctx, terminalMessage{Type: "error", SessionID: message.SessionID, Message: err.Error()})
				continue
			}
			mu.Lock()
			sessions[message.SessionID] = terminal
			mu.Unlock()
			go relayPTY(ctx, message.SessionID, terminal, writer, func() {
				mu.Lock()
				delete(sessions, message.SessionID)
				mu.Unlock()
			})
		case "input":
			if session != nil {
				session.input = append(session.input, message.Data...)
				if len(session.input) > 4096 {
					session.input = nil
					_ = writer.send(ctx, terminalMessage{Type: "error", SessionID: message.SessionID, Message: "terminal input limit exceeded"})
					continue
				}
				for {
					index := bytes.IndexByte(session.input, '\n')
					if index < 0 {
						break
					}
					line := append([]byte(nil), session.input[:index+1]...)
					session.input = session.input[index+1:]
					if !terminalInputAllowed(string(line)) {
						_ = writer.send(ctx, terminalMessage{Type: "error", SessionID: message.SessionID, Message: "command blocked by terminal policy"})
						continue
					}
					_, _ = session.file.Write(line)
				}
			}
		case "resize":
			if session != nil && message.Cols > 0 && message.Rows > 0 {
				_ = pty.Setsize(session.file, &pty.Winsize{Cols: message.Cols, Rows: message.Rows})
			}
		case "close":
			if session != nil {
				session.cancel()
				_ = session.file.Close()
			}
		}
	}
}

func (e *Executor) startPTY(parent context.Context, cols, rows uint16) (*ptySession, error) {
	if cols == 0 {
		cols = 120
	}
	if rows == 0 {
		rows = 32
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	command := exec.CommandContext(ctx, "/bin/sh", "-l")
	command.Env = append(os.Environ(), "TERM=dumb", "PS1=kloudview\\$ ")
	attr, extraEnv, err := e.terminalCredential()
	if err != nil {
		cancel()
		return nil, err
	}
	if attr != nil {
		command.SysProcAttr = attr
		command.Env = append(command.Env, extraEnv...)
	}
	file, err := pty.StartWithSize(command, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		cancel()
		return nil, err
	}
	return &ptySession{file: file, cancel: cancel}, nil
}

var terminalDenyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[;&|]\s*)rm\s+(-[^\s]*r[^\s]*f|-[^\s]*f[^\s]*r)\s+(/|/\*|--no-preserve-root)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(mkfs(\.[a-z0-9]+)?|wipefs)(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)dd\s+.*\bof=/dev/`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(shutdown|reboot|poweroff|halt)(\s|$)`),
}

func terminalInputAllowed(value string) bool {
	for _, pattern := range terminalDenyPatterns {
		if pattern.MatchString(value) {
			return false
		}
	}
	return true
}

// terminalCommandAllowed screens a full, possibly multi-line command against the
// deny policy. Each line is checked independently so that a destructive statement
// on a line other than the first cannot slip past the line-anchored patterns.
func terminalCommandAllowed(command string) bool {
	for _, line := range strings.Split(command, "\n") {
		if !terminalInputAllowed(line + "\n") {
			return false
		}
	}
	return true
}

// canDropTo reports whether a process running at euid may execute as uid: only
// root can become another account. Separate from the syscall path so the rule
// is testable whatever the test process runs as.
func canDropTo(euid int, uid uint64) bool {
	return euid == 0 || uint64(euid) == uid
}

// terminalCredential resolves the optional privilege-drop credential and login
// environment for the configured terminal user. It returns nil when no user is
// configured, meaning the command inherits the agent's own identity.
func (e *Executor) terminalCredential() (*syscall.SysProcAttr, []string, error) {
	if e.terminalUser == "" {
		return nil, nil, nil
	}
	account, err := user.Lookup(e.terminalUser)
	if err != nil {
		return nil, nil, err
	}
	uid, uidErr := strconv.ParseUint(account.Uid, 10, 32)
	gid, gidErr := strconv.ParseUint(account.Gid, 10, 32)
	if uidErr != nil || gidErr != nil {
		return nil, nil, errors.New("terminal user identity is invalid")
	}
	if !canDropTo(os.Geteuid(), uid) {
		return nil, nil, errors.New("terminal user privilege drop is unavailable")
	}
	attr := &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), NoSetGroups: true}}
	env := []string{"HOME=" + account.HomeDir, "USER=" + account.Username, "LOGNAME=" + account.Username}
	return attr, env, nil
}

func relayPTY(ctx context.Context, sessionID string, session *ptySession, writer *terminalWriter, done func()) {
	defer done()
	defer session.cancel()
	defer session.file.Close()
	buffer := make([]byte, 32<<10)
	for {
		count, err := session.file.Read(buffer)
		if count > 0 {
			session.total += count
			if session.total > terminalOutputLimit {
				_ = writer.send(ctx, terminalMessage{Type: "error", SessionID: sessionID, Message: "terminal output limit exceeded"})
				return
			}
			if writer.send(ctx, terminalMessage{Type: "output", SessionID: sessionID, Data: append([]byte(nil), buffer[:count]...)}) != nil {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				_ = writer.send(ctx, terminalMessage{Type: "error", SessionID: sessionID, Message: err.Error()})
			}
			_ = writer.send(ctx, terminalMessage{Type: "exit", SessionID: sessionID})
			return
		}
	}
}
