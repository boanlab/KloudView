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

// terminalCloseGrace is how long the shell's process group is given to act on
// SIGHUP before the context's SIGKILL takes over.
const terminalCloseGrace = 2 * time.Second

type ptySession struct {
	file    *os.File
	command *exec.Cmd
	cancel  context.CancelFunc
	closed  sync.Once
	total   int
	input   []byte
}

// close ends a session and collects what it started.
//
// Cancelling the context kills the shell and nothing else, and never reaps it:
// the shell stays in the process table as a zombie, and whatever it was
// running — an editor, a pager — is orphaned onto PID 1 and keeps running. On
// a node with the agent installed both are visible, because the agent's own
// inventory finds them and reports them as processes on the host. The agent
// was filing its own leftovers as a problem with the machine.
//
// pty.StartWithSize makes the shell a session and process-group leader, so a
// negative pid reaches everything it started. SIGHUP first, which is what a
// shell and its children expect when a terminal goes away, then the context's
// SIGKILL if that was not enough, and Wait either way.
func (s *ptySession) close() {
	s.closed.Do(func() {
		defer s.cancel()
		if s.command == nil || s.command.Process == nil {
			_ = s.file.Close()
			return
		}
		// The shell is the session leader, so its pid is the session id.
		sid := s.command.Process.Pid
		signalSession(sid, syscall.SIGHUP)
		_ = s.file.Close()
		reaped := make(chan struct{})
		go func() {
			defer close(reaped)
			_ = s.command.Wait()
		}()
		select {
		case <-reaped:
		case <-time.After(terminalCloseGrace):
			signalSession(sid, syscall.SIGKILL)
			<-reaped
		}
		// A session id outlives its leader, so anything that ignored the
		// hang-up or was started in a group of its own is still reachable.
		signalSession(sid, syscall.SIGKILL)
	})
}

// signalSession sends sig to every process in the session the shell leads.
//
// Signalling the process group is not enough. A shell with a terminal turns on
// job control and puts each background job in a group of its own, so
// "sleep 300 &" survives a group signal and is left running, reparented to PID
// 1, after the session is closed. The session is the unit that holds
// everything the shell started, and /proc is the only way to enumerate it.
func signalSession(sid int, sig syscall.Signal) {
	_ = syscall.Kill(-sid, sig)
	for _, pid := range sessionMembers(sid) {
		if pid != sid {
			_ = syscall.Kill(pid, sig)
		}
	}
}

func sessionMembers(sid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	members := []int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if processSession(pid) == sid {
			members = append(members, pid)
		}
	}
	return members
}

// processSession reads the session id out of /proc/<pid>/stat. The comm field
// is parenthesised and may itself contain spaces and brackets, so the fields
// are counted from the last ')' rather than split from the start.
func processSession(pid int) int {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	end := strings.LastIndex(string(data), ")")
	if end < 0 {
		return 0
	}
	fields := strings.Fields(string(data)[end+1:])
	// state, ppid, pgrp, session
	if len(fields) < 4 {
		return 0
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil {
		return 0
	}
	return session
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

// acceptInput is the screened path: nothing reaches the pty until a newline
// arrives, so the deny policy always sees a whole line. It reports how many
// lines it refused and whether the buffer overflowed before one ever arrived.
func (s *ptySession) acceptInput(data []byte) (refused int, overflow bool) {
	s.input = append(s.input, data...)
	if len(s.input) > 4096 {
		s.input = nil
		return 0, true
	}
	for {
		index := bytes.IndexByte(s.input, '\n')
		if index < 0 {
			return refused, false
		}
		line := append([]byte(nil), s.input[:index+1]...)
		s.input = s.input[index+1:]
		if !terminalInputAllowed(string(line)) {
			refused++
			continue
		}
		_, _ = s.file.Write(line)
	}
}

// acceptKeys writes raw bytes through without waiting for a newline, because
// an arrow key is not a line and never becomes one. Nothing is screened here:
// the server reads the shell's echo and gets its say when a Return arrives,
// which is the only point at which a command exists to judge.
func (s *ptySession) acceptKeys(data []byte) {
	if len(data) > 0 {
		_, _ = s.file.Write(data)
	}
}

func (e *Executor) ServeTerminalStream(ctx context.Context, conn *websocket.Conn) error {
	writer := &terminalWriter{conn: conn}
	sessions := map[string]*ptySession{}
	// A size that arrived before the pty it describes. The browser reports its
	// size a round trip after the session opens, so the order is usually open
	// then resize — but not always, and a size dropped here leaves the shell
	// drawing into a pane of a different height.
	pending := map[string]pty.Winsize{}
	var mu sync.Mutex
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, session := range sessions {
			session.close()
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
			cols, rows := message.Cols, message.Rows
			mu.Lock()
			if size, waiting := pending[message.SessionID]; waiting {
				// The browser already said how big it is; the open was only
				// ever a guess.
				cols, rows = size.Cols, size.Rows
				delete(pending, message.SessionID)
			}
			mu.Unlock()
			terminal, err := e.startPTY(ctx, cols, rows)
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
			if session == nil {
				continue
			}
			refused, overflow := session.acceptInput(message.Data)
			if overflow {
				_ = writer.send(ctx, terminalMessage{Type: "error", SessionID: message.SessionID, Message: "terminal input limit exceeded"})
				continue
			}
			for range refused {
				_ = writer.send(ctx, terminalMessage{Type: "error", SessionID: message.SessionID, Message: "command blocked by terminal policy"})
			}
		case "keys":
			if session != nil {
				session.acceptKeys(message.Data)
			}
		case "reply":
			// The console's emulator answering a question the program asked.
			// It crosses in either mode because it is not something anyone
			// typed; the server has already checked its shape.
			if session != nil && len(message.Data) > 0 {
				_, _ = session.file.Write(message.Data)
			}
		case "resize":
			if message.Cols == 0 || message.Rows == 0 {
				continue
			}
			if session == nil {
				// The pty is not open yet; hold the size for it.
				mu.Lock()
				pending[message.SessionID] = pty.Winsize{Cols: message.Cols, Rows: message.Rows}
				mu.Unlock()
				continue
			}
			_ = pty.Setsize(session.file, &pty.Winsize{Cols: message.Cols, Rows: message.Rows})
		case "close":
			mu.Lock()
			delete(pending, message.SessionID)
			mu.Unlock()
			if session != nil {
				// Reaping waits on the grace period; the connection keeps
				// serving its other sessions meanwhile.
				go session.close()
			}
		}
	}
}

// loginShell prefers bash. A login shell sources the system profile, and the
// profile sets PS1 — so a PS1 handed to /bin/sh is overwritten before the
// operator sees it, leaving a prompt that says neither which node this is nor
// which directory they are in. bash re-evaluates PROMPT_COMMAND before every
// prompt, which the profile cannot undo.
func loginShell() (string, []string) {
	if path, err := exec.LookPath("bash"); err == nil {
		return path, []string{"-l"}
	}
	return "/bin/sh", []string{"-l"}
}

// terminalEnv is the environment an approved session runs in.
func terminalEnv(shell string) []string {
	env := []string{
		// The console renders a screen now — a grid, a cursor, a scroll region
		// and colour — so the session may say what it is. A program that asks
		// for the cursor gets it, and its output arrives drawn rather than as
		// the escape codes that would have drawn it.
		"TERM=xterm-256color",
		// Input goes byte by byte, so pagers are left to page: `systemctl
		// status` and `git log` behave as they do everywhere else.
		//
		// -F quits when the output fits one screen, so short output prints and
		// returns. -R keeps colour. Not -X: without the alternate screen every
		// page turn is appended to the session instead of redrawn in place.
		"LESS=-FR",
		`PS1=\u@\h:\w\$ `,
	}
	if strings.HasSuffix(shell, "bash") {
		env = append(env, `PROMPT_COMMAND=PS1='\u@\h:\w\$ '`)
	}
	return env
}

func (e *Executor) startPTY(parent context.Context, cols, rows uint16) (*ptySession, error) {
	if cols == 0 {
		cols = 120
	}
	if rows == 0 {
		rows = 32
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	shell, args := loginShell()
	command := exec.CommandContext(ctx, shell, args...)
	command.Env = append(os.Environ(), terminalEnv(shell)...)
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
	return &ptySession{file: file, command: command, cancel: cancel}, nil
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
	defer session.close()
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
