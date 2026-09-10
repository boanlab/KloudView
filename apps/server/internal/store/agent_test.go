package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func TestDeleteAgentCascadesDiscoveredState(t *testing.T) {
	memory := NewMemory()
	memory.UpsertAgent(domain.Agent{ID: "agent-01", NodeID: "node-01", Hostname: "node-01"})
	memory.UpsertResource(domain.Resource{ID: "node-01", AgentID: "agent-01", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "vm-01", AgentID: "agent-01", Type: domain.ResourceVM})
	memory.PutRelation(domain.Relation{ID: "relation-01", SourceID: "node-01", TargetID: "vm-01"})
	memory.PutMembership(domain.GroupMembership{ID: "membership-01", GroupID: "group-01", ResourceID: "node-01"})
	memory.PutInventory(domain.AgentInventory{AgentID: "agent-01", NodeID: "node-01"})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", CPU: 10})

	if err := memory.DeleteAgent("agent-01"); err != nil {
		t.Fatal(err)
	}
	if memory.HasAgent("agent-01") || memory.HasResource("node-01") || memory.HasResource("vm-01") {
		t.Fatal("agent resources retained")
	}
	if len(memory.ListRelations()) != 0 || len(memory.ListMemberships("")) != 0 || len(memory.Inventories()) != 0 || len(memory.Metrics("node-01")) != 0 {
		t.Fatal("agent dependent state retained")
	}
}

func TestNetworkRateSummary(t *testing.T) {
	memory := NewMemory()
	start := time.Now().UTC()
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", Timestamp: start, NetworkRx: 1000, NetworkTx: 2000})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", Timestamp: start.Add(10 * time.Second), NetworkRx: 3000, NetworkTx: 5000})
	summary := memory.MetricSummary("node-01")
	if summary.NetworkRxRate != 200 || summary.NetworkTxRate != 300 {
		t.Fatalf("network rate = %.1f/%.1f", summary.NetworkRxRate, summary.NetworkTxRate)
	}
	rate := memory.NetworkRates()["node-01"]
	if rate.Rx != 200 || rate.Tx != 300 {
		t.Fatalf("network map rate = %.1f/%.1f", rate.Rx, rate.Tx)
	}
}

func TestMetricRetentionKeepsRawAndArchivedWindows(t *testing.T) {
	memory := NewMemory()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for index := 0; index <= 25*60*6; index++ {
		memory.AddMetric(domain.MetricSample{ResourceID: "node-01", Timestamp: start.Add(time.Duration(index) * 10 * time.Second), CPU: float64(index % 100)})
	}
	items := memory.Metrics("node-01")
	if len(items) > 1008 || len(items) < 500 {
		t.Fatalf("retained samples = %d", len(items))
	}
	newest := start.Add(25 * time.Hour)
	if items[0].Timestamp.Before(newest.Add(-24*time.Hour)) || !items[len(items)-1].Timestamp.Equal(newest) {
		t.Fatalf("retention range = %s to %s", items[0].Timestamp, items[len(items)-1].Timestamp)
	}
}

func TestMetricOrderingProtectsNetworkRate(t *testing.T) {
	memory := NewMemory()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", Timestamp: start.Add(10 * time.Second), NetworkRx: 3000})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", Timestamp: start, NetworkRx: 1000})
	if rate := memory.NetworkRate("node-01"); rate.Rx != 200 {
		t.Fatalf("rx rate = %f", rate.Rx)
	}
}

func TestAggregatedMetrics(t *testing.T) {
	memory := NewMemory()
	start := time.Now().UTC().Truncate(time.Minute)
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", Timestamp: start, CPU: 20, Memory: 40, Disk: 60})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-02", Timestamp: start.Add(10 * time.Second), CPU: 40, Memory: 60, Disk: 80})
	items := memory.AggregatedMetrics(start.Add(-time.Second), time.Minute)
	if len(items) != 1 || items[0].Count != 2 || items[0].CPU != 30 || items[0].Memory != 50 || items[0].Disk != 70 {
		t.Fatalf("aggregated metrics = %+v", items)
	}
}

func TestPruneAgentResourcesMarksVanishedChildrenTerminated(t *testing.T) {
	memory := NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", AgentID: "agent-01", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "vm-current", AgentID: "agent-01", Type: domain.ResourceVM})
	memory.UpsertResource(domain.Resource{ID: "vm-stopped", AgentID: "agent-01", Type: domain.ResourceVM})
	memory.PutRelation(domain.Relation{ID: "stopped-relation", SourceID: "node-01", TargetID: "vm-stopped"})
	memory.PruneAgentResources("agent-01", "node-01", map[string]bool{"vm-current": true})
	stopped, ok := memory.Resource("vm-stopped")
	if !ok || stopped.TerminatedAt == nil {
		t.Fatalf("stopped resource = %+v", stopped)
	}
	if current, _ := memory.Resource("vm-current"); current.TerminatedAt != nil {
		t.Fatal("reported resource marked terminated")
	}
	if len(memory.ListRelations()) != 1 {
		t.Fatal("relation dropped while the record is still queryable")
	}
	// A child that comes back is live again, not a tombstone.
	memory.UpsertResource(domain.Resource{ID: "vm-stopped", AgentID: "agent-01", Type: domain.ResourceVM})
	if revived, _ := memory.Resource("vm-stopped"); revived.TerminatedAt != nil {
		t.Fatal("restarted resource still terminated")
	}
}

func TestPruneAgentResourcesDropsExpiredTombstones(t *testing.T) {
	memory := NewMemory()
	expired := time.Now().UTC().Add(-terminatedResourceRetention - time.Hour)
	memory.UpsertResource(domain.Resource{ID: "node-01", AgentID: "agent-01", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "vm-old", AgentID: "agent-01", Type: domain.ResourceVM, TerminatedAt: &expired})
	memory.PutRelation(domain.Relation{ID: "old-relation", SourceID: "node-01", TargetID: "vm-old"})
	memory.PruneAgentResources("agent-01", "node-01", map[string]bool{})
	if memory.HasResource("vm-old") || len(memory.ListRelations()) != 0 {
		t.Fatal("expired tombstone retained")
	}
}

func TestOperationLeaseRetriesExpiredClaim(t *testing.T) {
	memory := NewMemory()
	memory.PutOperation(domain.Operation{ID: "operation-01", Type: "inventory.refresh", Status: "pending", TargetIDs: []string{"node-01"}})
	claimed, ok := memory.ClaimOperation("node-01")
	if !ok || claimed.Attempts != 1 || claimed.LeaseUntil == nil {
		t.Fatalf("first claim = %+v", claimed)
	}
	expired := time.Now().UTC().Add(-time.Second)
	claimed.LeaseUntil = &expired
	memory.PutOperation(claimed)
	retried, ok := memory.ClaimOperation("node-01")
	if !ok || retried.Attempts != 2 {
		t.Fatalf("retry claim = %+v", retried)
	}
	if _, ok := memory.CompleteOperation(retried.ID, "node-02", "succeeded", "", ""); ok {
		t.Fatal("another node completed operation")
	}
	if completed, ok := memory.CompleteOperation(retried.ID, "node-01", "succeeded", "ok", ""); !ok || completed.LeaseUntil != nil {
		t.Fatalf("completion = %+v", completed)
	}
}

func TestServiceRestartLeaseDoesNotRetry(t *testing.T) {
	memory := NewMemory()
	started := time.Now().UTC().Add(-time.Minute)
	lease := time.Now().UTC().Add(-time.Second)
	memory.PutOperation(domain.Operation{ID: "operation-01", Type: "service.restart", Status: "running", TargetIDs: []string{"node-01"}, Attempts: 1, StartedAt: &started, LeaseUntil: &lease})
	if operation, ok := memory.ClaimOperation("node-01"); ok {
		t.Fatalf("restart reclaimed = %+v", operation)
	}
	operation, _ := memory.Operation("operation-01")
	if operation.Status != "failed" || operation.Error != "service restart completion unknown; automatic retry disabled" {
		t.Fatalf("restart state = %+v", operation)
	}
}

func TestExpiredTerminalCommandBecomesFailed(t *testing.T) {
	memory := NewMemory()
	started := time.Now().UTC().Add(-time.Minute)
	memory.PutTerminal(domain.TerminalSession{ID: "session-01", TargetID: "node-01", Status: "active"})
	memory.PutTerminalCommand(domain.TerminalCommand{ID: "command-01", SessionID: "session-01", TargetID: "node-01", Status: "running", StartedAt: &started})
	if command, ok := memory.ClaimTerminalCommand("node-01"); ok {
		t.Fatalf("expired command reclaimed = %+v", command)
	}
	commands := memory.TerminalCommands("session-01")
	if len(commands) != 1 || commands[0].Status != "failed" || commands[0].FinishedAt == nil {
		t.Fatalf("terminal commands = %+v", commands)
	}
}

// A process that leaves the inventory is gone, not terminated. Inventory
// samples on a cycle far slower than a process lives, so keeping the ones it
// happens to catch would list an arbitrary fraction of what ran and bury the
// stopped containers worth reading.
func TestAProcessLeavingTheInventoryIsRemovedNotRecorded(t *testing.T) {
	memory := NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", AgentID: "agent-01", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "process-gone", AgentID: "agent-01", Type: domain.ResourceProcess})
	memory.UpsertResource(domain.Resource{ID: "container-gone", AgentID: "agent-01", Type: domain.ResourceContainer})

	memory.PruneAgentResources("agent-01", "node-01", map[string]bool{})
	if memory.HasResource("process-gone") {
		t.Error("a process was kept as a terminated record")
	}
	container, ok := memory.Resource("container-gone")
	if !ok || container.TerminatedAt == nil {
		t.Fatalf("a stopped container was not recorded: %+v", container)
	}
}

// Records left by an earlier build are cleared by the same pass, so the filter
// is not permanently full of processes that stopped before the rule changed.
func TestExistingProcessRecordsAreClearedOnTheNextPrune(t *testing.T) {
	memory := NewMemory()
	recent := time.Now().UTC().Add(-time.Minute)
	memory.UpsertResource(domain.Resource{ID: "node-01", AgentID: "agent-01", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "process-old", AgentID: "agent-01", Type: domain.ResourceProcess, TerminatedAt: &recent})
	memory.UpsertResource(domain.Resource{ID: "container-recent", AgentID: "agent-01", Type: domain.ResourceContainer, TerminatedAt: &recent})

	memory.PruneAgentResources("agent-01", "node-01", map[string]bool{})
	if memory.HasResource("process-old") {
		t.Error("an existing process record survived the prune")
	}
	if !memory.HasResource("container-recent") {
		t.Error("a container inside its retention was removed")
	}
}

// The node sends the inventory, so its own absence from it means nothing. A
// decommission is an explicit removal and a silent node is an alert; treating
// either as termination would erase a live fleet during a partition.
func TestTheReportingNodeIsNeverTerminatedByItsOwnInventory(t *testing.T) {
	memory := NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", AgentID: "agent-01", Type: domain.ResourceNode})

	memory.PruneAgentResources("agent-01", "node-01", map[string]bool{})
	node, ok := memory.Resource("node-01")
	if !ok || node.TerminatedAt != nil {
		t.Fatalf("the reporting node was terminated: %+v", node)
	}
}

// An inventory refresh answers with the whole host report, already stored as
// the inventory, and the state document is rewritten on every save.
func TestOperationResultsAndHistoryAreBounded(t *testing.T) {
	memory := NewMemory()
	big := strings.Repeat("x", 40*1024)
	memory.PutOperation(domain.Operation{ID: "operation-big", Type: "inventory.refresh", Status: "pending", TargetIDs: []string{"node-01"}})
	if _, ok := memory.ClaimOperation("node-01"); !ok {
		t.Fatal("claim failed")
	}
	done, ok := memory.CompleteOperation("operation-big", "node-01", "succeeded", big, "")
	if !ok {
		t.Fatal("completion failed")
	}
	if len(done.Result) > operationResultLimit+128 {
		t.Fatalf("result kept %d bytes, want it truncated", len(done.Result))
	}
	if !strings.Contains(done.Result, "truncated") {
		t.Fatal("the truncation is not visible in the result")
	}

	finished := time.Now().UTC()
	for i := range operationHistory + 50 {
		id := fmt.Sprintf("operation-%04d", i)
		memory.PutOperation(domain.Operation{
			ID: id, Type: "inventory.refresh", Status: "succeeded",
			TargetIDs: []string{"node-01"}, FinishedAt: &finished,
		})
	}
	if got := len(memory.ListOperations()); got > operationHistory+2 {
		t.Fatalf("kept %d operations, want about %d", got, operationHistory)
	}

	// Anything still running is kept whatever the history limit says.
	memory.PutOperation(domain.Operation{ID: "operation-live", Type: "logs.capture", Status: "running", TargetIDs: []string{"node-01"}})
	found := false
	for _, operation := range memory.ListOperations() {
		if operation.ID == "operation-live" {
			found = true
		}
	}
	if !found {
		t.Fatal("an unfinished operation was pruned")
	}
}
