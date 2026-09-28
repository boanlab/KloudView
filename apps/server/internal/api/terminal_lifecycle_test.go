package api

import (
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func activeSession(t *testing.T, server *Server) domain.TerminalSession {
	t.Helper()
	started := time.Now().UTC()
	return server.store.PutTerminal(domain.TerminalSession{
		ID: "terminal-1", TargetID: "node-01", Status: "active",
		RequestedBy: "admin", ApprovedBy: "admin", Reason: "test",
		CreatedAt: started, StartedAt: &started,
	})
}

// A session that ends leaves a record saying so. Without it a session nobody is
// attached to still reads as open, and the audit trail has no time against it.
func TestEndingASessionRecordsThatItEnded(t *testing.T) {
	server := New(store.NewMemory(), "token", "")
	session := activeSession(t, server)
	server.store.PutTerminalCommand(domain.TerminalCommand{
		ID: "cmd-1", SessionID: session.ID, Status: "queued",
	})

	ended := server.endTerminalSession(session, "console disconnected\n")
	if ended.Status != "closed" {
		t.Fatalf("status = %q, want closed", ended.Status)
	}
	if ended.ClosedAt == nil {
		t.Fatal("no time recorded against the session")
	}
	stored, ok := server.store.Terminal(session.ID)
	if !ok || stored.Status != "closed" || stored.ClosedAt == nil {
		t.Fatalf("stored session = %+v", stored)
	}
	// Whatever was queued for a session that is over will never run.
	commands := server.store.TerminalCommands(session.ID)
	for _, command := range commands {
		if command.Status == "queued" {
			t.Errorf("command %s is still queued on a closed session", command.ID)
		}
	}
	recording, ok := server.store.TerminalRecording(session.ID, time.Now().UTC())
	if !ok {
		t.Fatal("no recording kept for the session")
	}
	found := false
	for _, event := range recording.Events {
		if event.Data == "console disconnected\n" {
			found = true
		}
	}
	if !found {
		t.Errorf("the recording does not say the session ended: %+v", recording.Events)
	}
}

// Ending is once. A second pass must not move the time it already recorded.
func TestEndingAClosedSessionLeavesItsTimeAlone(t *testing.T) {
	server := New(store.NewMemory(), "token", "")
	session := activeSession(t, server)
	first := server.endTerminalSession(session, "session closed\n")
	again := server.endTerminalSession(first, "console disconnected\n")
	if !again.ClosedAt.Equal(*first.ClosedAt) {
		t.Errorf("closedAt moved from %s to %s", first.ClosedAt, again.ClosedAt)
	}
}

// A reload opens a new connection before the old one finishes leaving. The
// departing connection must not end a session that is already serving its
// replacement, which is what removeBrowser returning nil is for.
func TestAReplacedConsoleDoesNotEndTheSession(t *testing.T) {
	hub := newTerminalHub()
	first := &browserTerminal{socket: &terminalSocket{}, targetID: "node-01"}
	second := &browserTerminal{socket: &terminalSocket{}, targetID: "node-01"}

	hub.registerBrowser("terminal-1", first)
	hub.registerBrowser("terminal-1", second)
	if peer := hub.removeBrowser("terminal-1", first); peer != nil {
		t.Error("the replaced connection was treated as the session leaving")
	}
	if hub.browser("terminal-1") != second {
		t.Error("the replacement was dropped when the old connection left")
	}
}
