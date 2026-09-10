package api

import (
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func silentServer(t *testing.T, agents ...domain.Agent) (*Server, *store.Memory) {
	t.Helper()
	memory := store.NewMemory()
	for _, agent := range agents {
		memory.UpsertAgent(agent)
		memory.UpsertResource(domain.Resource{ID: agent.NodeID, Name: agent.Hostname, Type: domain.ResourceNode, AgentID: agent.ID})
	}
	return New(memory, "token", ""), memory
}

func firing(memory *store.Memory) []domain.Alert {
	out := []domain.Alert{}
	for _, alert := range memory.ListAlerts() {
		if strings.HasPrefix(alert.ID, "alert-offline-") && alert.Status == "firing" {
			out = append(out, alert)
		}
	}
	return out
}

// One node silent while its peers keep reporting points at that host.
func TestLoneSilenceBlamesTheHost(t *testing.T) {
	now := time.Now().UTC()
	server, memory := silentServer(t,
		domain.Agent{ID: "a1", NodeID: "node-1", Hostname: "n1", LastSeenAt: now.Add(-5 * time.Minute)},
		domain.Agent{ID: "a2", NodeID: "node-2", Hostname: "n2", LastSeenAt: now},
	)
	server.sweepConnectivity(now)

	alerts := firing(memory)
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want one for the silent node", len(alerts))
	}
	if !strings.Contains(alerts[0].Summary, "suspect this host") {
		t.Fatalf("summary = %q", alerts[0].Summary)
	}
	if alerts[0].ResourceID != "node-1" {
		t.Fatalf("alert points at %q", alerts[0].ResourceID)
	}
}

// Several nodes falling silent together points at something they share.
func TestSimultaneousSilenceBlamesASharedDependency(t *testing.T) {
	now := time.Now().UTC()
	quiet := now.Add(-2 * time.Minute)
	server, memory := silentServer(t,
		domain.Agent{ID: "a1", NodeID: "node-1", Hostname: "n1", LastSeenAt: quiet},
		domain.Agent{ID: "a2", NodeID: "node-2", Hostname: "n2", LastSeenAt: quiet.Add(3 * time.Second)},
		domain.Agent{ID: "a3", NodeID: "node-3", Hostname: "n3", LastSeenAt: quiet.Add(-5 * time.Second)},
	)
	server.sweepConnectivity(now)

	alerts := firing(memory)
	if len(alerts) != 3 {
		t.Fatalf("alerts = %d, want one per silent node", len(alerts))
	}
	for _, alert := range alerts {
		if !strings.Contains(alert.Summary, "shared power or network") {
			t.Fatalf("summary = %q, want the correlated diagnosis", alert.Summary)
		}
	}
}

// The alert carries the last values the node reported before it went quiet.
func TestSilenceAlertKeepsTheFinalMetrics(t *testing.T) {
	now := time.Now().UTC()
	server, memory := silentServer(t,
		domain.Agent{ID: "a1", NodeID: "node-1", Hostname: "n1", LastSeenAt: now.Add(-3 * time.Minute)},
	)
	memory.AddMetric(domain.MetricSample{ResourceID: "node-1", Timestamp: now.Add(-4 * time.Minute), CPU: 97.5, Memory: 88.25, Disk: 41})
	server.sweepConnectivity(now)

	alerts := firing(memory)
	if len(alerts) != 1 || !strings.Contains(alerts[0].Summary, "final cpu 97.5%") {
		t.Fatalf("summary = %q, want the last sample", alerts[0].Summary)
	}
}

// Returning resolves the alert and records how long the gap was.
func TestReturnResolvesTheSilenceAlert(t *testing.T) {
	now := time.Now().UTC()
	server, memory := silentServer(t,
		domain.Agent{ID: "a1", NodeID: "node-1", Hostname: "n1", LastSeenAt: now.Add(-3 * time.Minute)},
	)
	server.sweepConnectivity(now)
	if len(firing(memory)) != 1 {
		t.Fatal("no alert was raised")
	}

	memory.UpsertAgent(domain.Agent{ID: "a1", NodeID: "node-1", Hostname: "n1", LastSeenAt: now})
	server.sweepConnectivity(now)

	if len(firing(memory)) != 0 {
		t.Fatal("the alert stayed firing after the agent returned")
	}
	resolved := memory.ListAlerts()[0]
	if !strings.Contains(resolved.Summary, "reporting resumed after") {
		t.Fatalf("summary = %q", resolved.Summary)
	}
}

// silentAgents is in-process state, so a fresh server must still resolve an
// alert an earlier process raised.
func TestReturnResolvesAnAlertRaisedByAnEarlierProcess(t *testing.T) {
	now := time.Now().UTC()
	server, memory := silentServer(t,
		domain.Agent{ID: "a1", NodeID: "node-1", Hostname: "n1", LastSeenAt: now.Add(-3 * time.Minute)},
	)
	server.sweepConnectivity(now)
	if len(firing(memory)) != 1 {
		t.Fatal("no alert was raised")
	}

	// A new process over the same store: the alert survives, the tracking does not.
	restarted := New(memory, "token", "")
	memory.UpsertAgent(domain.Agent{ID: "a1", NodeID: "node-1", Hostname: "n1", LastSeenAt: now})
	restarted.sweepConnectivity(now)

	if remaining := firing(memory); len(remaining) != 0 {
		t.Fatalf("alert outlived the process that raised it: %+v", remaining)
	}
}

// A node that stays silent must not raise a new alert on every sweep.
func TestSilenceIsReportedOnce(t *testing.T) {
	now := time.Now().UTC()
	server, memory := silentServer(t,
		domain.Agent{ID: "a1", NodeID: "node-1", Hostname: "n1", LastSeenAt: now.Add(-3 * time.Minute)},
	)
	server.sweepConnectivity(now)
	server.sweepConnectivity(now.Add(time.Minute))
	if got := len(firing(memory)); got != 1 {
		t.Fatalf("alerts = %d, want one", got)
	}
}
