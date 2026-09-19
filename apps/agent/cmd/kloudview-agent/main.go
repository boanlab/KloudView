package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kloudview/kloudview/apps/agent/internal/client"
	"github.com/kloudview/kloudview/apps/agent/internal/config"
	"github.com/kloudview/kloudview/apps/agent/internal/executor"
	"github.com/kloudview/kloudview/apps/agent/internal/identity"
	"github.com/kloudview/kloudview/apps/agent/internal/inventory"
	"github.com/kloudview/kloudview/apps/agent/internal/logstream"
	"github.com/kloudview/kloudview/apps/agent/internal/metrics"
	"github.com/kloudview/kloudview/apps/agent/internal/selfupdate"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	cfg := config.Load()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	host := inventory.Collect()
	api := client.New(cfg.ServerURL, cfg.EnrollmentToken, version, cfg.TerminalEnabled, cfg.LogStreamEnabled)
	runner := executor.New(cfg.AllowedServices).WithTerminalUser(cfg.TerminalUser)
	collector := metrics.NewCollector()
	agentID, nodeID := establishIdentity(ctx, api, host, cfg)
	if cfg.TerminalEnabled {
		go maintainTerminalStream(ctx, api, runner, agentID)
	}
	if err := api.Inventory(agentID, host); err != nil {
		slog.Warn("inventory failed", "error", err)
	}
	logs := logstream.NewCollector(logstream.DefaultLimits, nil)
	cursorPath := journalCursorPath(cfg.StatePath)
	if cfg.LogStreamEnabled {
		go logstream.Run(ctx, logs, readJournalCursor(cursorPath))
	}
	lastInventory := time.Now()
	lastLogReport := time.Now()
	metricReady := false
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		beat, err := api.Heartbeat(agentID, nodeID, host)
		if err == nil {
			adoptCredential(cfg.StatePath, api, agentID, nodeID, beat)
			applyUpdate(cfg, api, beat)
		} else {
			slog.Warn("heartbeat failed", "error", err)
			if client.RequiresEnrollment(err) {
				if cfg.EnrollmentToken == "" {
					slog.Error("re-enrollment token required")
				} else {
					agentID, nodeID = enroll(ctx, api, host)
					persistIdentity(cfg.StatePath, api, agentID, nodeID)
					lastInventory = time.Time{}
				}
			}
		}
		if metricReady {
			if err := api.Metric(agentID, nodeID, collector.Collect()); err != nil {
				slog.Warn("metric failed", "error", err)
			}
		} else {
			metricReady = true
		}
		if operation, err := api.ClaimOperation(agentID); err != nil {
			slog.Warn("operation claim failed", "error", err)
		} else if operation != nil {
			result, runErr := runner.Run(*operation)
			status, operationError := "succeeded", ""
			if runErr != nil {
				status, operationError = "failed", runErr.Error()
			}
			if err := api.CompleteOperation(agentID, operation.ID, status, result, operationError); err != nil {
				slog.Warn("operation completion failed", "error", err)
			}
		}
		if cfg.TerminalEnabled {
			if command, err := api.ClaimTerminalCommand(agentID); err != nil {
				slog.Warn("terminal claim failed", "error", err)
			} else if command != nil {
				output, runErr := runner.RunTerminal(command.Command)
				status, commandError := "succeeded", ""
				if runErr != nil {
					status, commandError = "failed", runErr.Error()
				}
				if err := api.CompleteTerminalCommand(agentID, command.ID, status, output, commandError); err != nil {
					slog.Warn("terminal completion failed", "error", err)
				}
			}
		}
		if ids := containerIDs(host); len(ids) > 0 {
			if err := api.ContainerMetrics(agentID, nodeID, inventory.ContainerCgroupStats(ids)); err != nil {
				slog.Warn("container metrics failed", "error", err)
			}
		}
		// Guests are read every tick, not on the inventory's five-minute cycle:
		// libvirt reports counters, and a rate needs readings close enough
		// together to mean something.
		if len(host.VMs) > 0 {
			if err := api.VirtualMachineMetrics(agentID, nodeID, inventory.VirtualMachineStats()); err != nil {
				slog.Warn("vm metrics failed", "error", err)
			}
		}
		// Processes are read every tick for the same reason guests are: /proc
		// reports CPU as a counter, and only two readings close together make
		// a rate. Not every process gets one — the collector ships the
		// heaviest, and the console says so for the rest.
		if stats := inventory.ProcessStatistics(host.MemoryBytes); len(stats) > 0 {
			if err := api.ProcessMetrics(agentID, nodeID, stats); err != nil {
				slog.Warn("process metrics failed", "error", err)
			}
		}
		// A window holding lines goes out on the next tick; an empty one waits
		// out the interval, because all it carries is counters.
		if cfg.LogStreamEnabled && (logs.Pending() > 0 || time.Since(lastLogReport) >= time.Minute) {
			batch := logs.Flush(time.Now().UTC())
			if err := api.Logs(agentID, nodeID, batch); err != nil {
				slog.Warn("log report failed", "error", err)
			} else {
				// Only after the server has the window: the cursor is where the
				// next reader resumes, so recording it early would drop lines.
				writeJournalCursor(cursorPath, batch.Cursor)
			}
			lastLogReport = time.Now()
		}
		if time.Since(lastInventory) >= 5*time.Minute {
			host = inventory.Collect()
			if err := api.Inventory(agentID, host); err != nil {
				slog.Warn("inventory failed", "error", err)
			} else {
				lastInventory = time.Now()
			}
		}
		select {
		case <-ctx.Done():
			slog.Info("agent stopped")
			return
		case <-ticker.C:
		}
	}
}

// journalCursorPath sits beside the agent identity, in the state directory the
// unit already creates with restricted ownership.
func journalCursorPath(statePath string) string {
	if statePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(statePath), "journal-cursor")
}

func readJournalCursor(path string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func writeJournalCursor(path, cursor string) {
	if path == "" || cursor == "" {
		return
	}
	if err := os.WriteFile(path, []byte(cursor), 0o600); err != nil {
		slog.Warn("journal cursor save failed", "error", err)
	}
}

// containerIDs are the runtime IDs from the last inventory; cgroup readings are
// taken every tick while the container list refreshes on the slower cycle.
func containerIDs(host inventory.Host) []string {
	ids := make([]string, 0, len(host.Containers))
	for _, container := range host.Containers {
		if container.ID != "" {
			ids = append(ids, container.ID)
		}
	}
	return ids
}

func maintainTerminalStream(ctx context.Context, api *client.Client, runner *executor.Executor, agentID string) {
	for ctx.Err() == nil {
		if err := api.TerminalStream(ctx, agentID, runner.ServeTerminalStream); err != nil && ctx.Err() == nil {
			slog.Warn("terminal stream disconnected", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// adoptCredential stores a credential the server rotated to. The previous one
// stays valid server-side until this one is used, so a failed write is not
// fatal: the next heartbeat simply presents the old value again.
func adoptCredential(statePath string, api *client.Client, agentID, nodeID string, beat client.HeartbeatResponse) {
	if beat.Credential == "" || beat.Credential == api.Credential() {
		return
	}
	if err := identity.Save(statePath, identity.State{AgentID: agentID, NodeID: nodeID, Credential: beat.Credential}); err != nil {
		slog.Warn("credential rotation not persisted", "error", err)
		return
	}
	api.SetCredential(beat.Credential)
	slog.Info("agent credential rotated")
}

// applyUpdate replaces this binary when the server advertises a different
// build and KLOUDVIEW_AUTO_UPDATE is on. The process exits so the service
// manager restarts it; the previous build is kept beside the new one.
func applyUpdate(cfg config.Config, api *client.Client, beat client.HeartbeatResponse) {
	if !cfg.AutoUpdate {
		return
	}
	release, ok := selfupdate.Plan(version, beat.TargetVersion, beat.Releases)
	if !ok {
		return
	}
	slog.Info("agent update available", "from", version, "to", beat.TargetVersion, "arch", release.Arch)
	body, err := api.DownloadRelease(release)
	if err != nil {
		slog.Warn("agent update download failed", "error", err)
		return
	}
	defer body.Close()
	if err := selfupdate.Apply(release, body); err != nil {
		slog.Warn("agent update rejected", "error", err)
		return
	}
	slog.Info("agent updated, restarting", "version", beat.TargetVersion)
	os.Exit(0)
}

func establishIdentity(ctx context.Context, api *client.Client, host inventory.Host, cfg config.Config) (string, string) {
	state, err := identity.Load(cfg.StatePath)
	if err == nil {
		api.SetCredential(state.Credential)
		if _, heartbeatErr := api.Heartbeat(state.AgentID, state.NodeID, host); heartbeatErr == nil || !client.RequiresEnrollment(heartbeatErr) {
			slog.Info("agent identity restored", "agent", state.AgentID, "node", state.NodeID)
			return state.AgentID, state.NodeID
		}
		slog.Warn("stored agent identity rejected")
	} else if !os.IsNotExist(err) {
		slog.Warn("agent identity load failed", "error", err)
	}
	if cfg.EnrollmentToken == "" {
		slog.Error("enrollment token required")
		os.Exit(1)
	}
	agentID, nodeID := enroll(ctx, api, host)
	persistIdentity(cfg.StatePath, api, agentID, nodeID)
	return agentID, nodeID
}

func persistIdentity(path string, api *client.Client, agentID, nodeID string) {
	if err := identity.Save(path, identity.State{AgentID: agentID, NodeID: nodeID, Credential: api.Credential()}); err != nil {
		slog.Error("agent identity save failed", "error", err)
	}
}

func enroll(ctx context.Context, api *client.Client, host inventory.Host) (string, string) {
	delay := time.Second
	for {
		agentID, nodeID, err := api.Enroll(host)
		if err == nil {
			slog.Info("agent enrolled", "agent", agentID, "node", nodeID)
			return agentID, nodeID
		}
		slog.Warn("enrollment failed", "error", err, "retry", delay)
		select {
		case <-ctx.Done():
			os.Exit(0)
		case <-time.After(delay + time.Duration(rand.IntN(500))*time.Millisecond):
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
}
