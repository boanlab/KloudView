package store

import (
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

func TestDynamicGroupMembership(t *testing.T) {
	memory := NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, Tags: map[string]string{"role": "database"}})
	memory.UpsertResource(domain.Resource{ID: "node-02", Name: "node-02", Type: domain.ResourceNode, Tags: map[string]string{"role": "web"}})
	memory.PutGroup(domain.Group{ID: "database", Name: "Database", Type: "service", Mode: "dynamic", Selector: map[string]string{"role": "database"}})
	members := memory.ListMemberships("database")
	if len(members) != 1 || members[0].ResourceID != "node-01" {
		t.Fatalf("members = %+v", members)
	}
}

func TestDynamicGroupDropsStaticMemberships(t *testing.T) {
	memory := NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Tags: map[string]string{"role": "database"}})
	memory.UpsertResource(domain.Resource{ID: "node-02", Tags: map[string]string{"role": "compute"}})
	memory.PutGroup(domain.Group{ID: "service", Name: "Service", Type: "service", Mode: "static"})
	memory.PutMembership(domain.GroupMembership{ID: "manual", GroupID: "service", ResourceID: "node-02"})
	memory.PutGroup(domain.Group{ID: "service", Name: "Service", Type: "service", Mode: "dynamic", Selector: map[string]string{"role": "database"}})
	members := memory.ListMemberships("service")
	if len(members) != 1 || members[0].ResourceID != "node-01" || members[0].ID == "manual" {
		t.Fatalf("members = %+v", members)
	}
}

func TestNestedHierarchyScope(t *testing.T) {
	memory := NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, Tags: map[string]string{"environment": "production"}})
	memory.PutGroup(domain.Group{ID: "production", Name: "Production", Type: "environment"})
	memory.PutGroup(domain.Group{ID: "dc-1", Name: "DC-1", Type: "datacenter", ParentID: "production"})
	memory.PutGroup(domain.Group{ID: "rack-07", Name: "Rack-07", Type: "rack", ParentID: "dc-1"})
	memory.PutMembership(domain.GroupMembership{ID: "member", GroupID: "rack-07", ResourceID: "node-01"})
	if !memory.ResourceMatchesScope("node-01", "production/dc-1", map[string]string{"environment": "production"}) {
		t.Fatal("nested scope should match")
	}
	if memory.ResourceMatchesScope("node-01", "production/dc-2", nil) {
		t.Fatal("different scope should not match")
	}
}

func TestResourceAccessContextInheritsHostScope(t *testing.T) {
	memory := NewMemory()
	memory.PutGroup(domain.Group{ID: "production", Name: "Production", Type: "environment", Path: "production"})
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "vm-01", Name: "vm-01", Type: domain.ResourceVM})
	memory.UpsertResource(domain.Resource{ID: "container-01", Name: "container-01", Type: domain.ResourceContainer})
	memory.PutMembership(domain.GroupMembership{ID: "member", GroupID: "production", ResourceID: "node-01"})
	memory.PutRelation(domain.Relation{ID: "hosts", SourceID: "node-01", TargetID: "vm-01", Type: "hosts"})
	memory.PutRelation(domain.Relation{ID: "runs", SourceID: "vm-01", TargetID: "container-01", Type: "runs"})

	paths, _, ok := memory.ResourceAccessContext("container-01")
	if !ok || len(paths) != 1 || paths[0] != "production" {
		t.Fatalf("paths = %v", paths)
	}
	if !memory.ResourceMatchesScope("container-01", "production", nil) {
		t.Fatal("container should match inherited production scope")
	}
	memory.UpsertResource(domain.Resource{ID: "dependency-01", Name: "dependency-01", Type: domain.ResourceVM})
	memory.PutRelation(domain.Relation{ID: "dependency", SourceID: "node-01", TargetID: "dependency-01", Type: "depends_on"})
	dependencyPaths, _, ok := memory.ResourceAccessContext("dependency-01")
	if !ok || len(dependencyPaths) != 1 || dependencyPaths[0] != "" {
		t.Fatalf("dependency paths = %v", dependencyPaths)
	}
}

func TestHierarchyCycleDetection(t *testing.T) {
	memory := NewMemory()
	memory.PutGroup(domain.Group{ID: "root", Name: "Root"})
	memory.PutGroup(domain.Group{ID: "child", Name: "Child", ParentID: "root"})
	if !memory.WouldCreateGroupCycle("root", "child") {
		t.Fatal("cycle not detected")
	}
	if memory.WouldCreateGroupCycle("child", "") {
		t.Fatal("root move should be valid")
	}
}

func TestResourceRelationCycleDetection(t *testing.T) {
	memory := NewMemory()
	memory.PutRelation(domain.Relation{ID: "a-b", SourceID: "a", TargetID: "b", Type: "contains"})
	memory.PutRelation(domain.Relation{ID: "b-c", SourceID: "b", TargetID: "c", Type: "runs"})
	if !memory.WouldCreateRelationCycle("c", "a") {
		t.Fatal("resource hierarchy cycle not detected")
	}
	if memory.WouldCreateRelationCycle("c", "unrelated") {
		t.Fatal("unrelated hierarchy should remain valid")
	}
}

func TestDeleteResourceCascadesTopologyAndMetrics(t *testing.T) {
	memory := NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "vm-01", Name: "vm-01", Type: domain.ResourceVM})
	memory.PutGroup(domain.Group{ID: "rack-01", Name: "Rack", Type: "rack"})
	memory.PutMembership(domain.GroupMembership{ID: "member", GroupID: "rack-01", ResourceID: "node-01"})
	memory.PutRelation(domain.Relation{ID: "hosts", SourceID: "node-01", TargetID: "vm-01", Type: "hosts"})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", CPU: 10})
	if err := memory.DeleteResource("node-01"); err != nil {
		t.Fatal(err)
	}
	if len(memory.ListMemberships("")) != 0 || len(memory.ListRelations()) != 0 || len(memory.Metrics("node-01")) != 0 {
		t.Fatal("resource references not removed")
	}
}

func TestCloseCancelsQueuedTerminalCommands(t *testing.T) {
	memory := NewMemory()
	memory.PutTerminal(domain.TerminalSession{ID: "session-01", Status: "active"})
	memory.PutTerminalCommand(domain.TerminalCommand{ID: "command-01", SessionID: "session-01", Status: "queued"})
	memory.CancelTerminalCommands("session-01")
	commands := memory.TerminalCommands("session-01")
	if len(commands) != 1 || commands[0].Status != "canceled" || commands[0].FinishedAt == nil {
		t.Fatalf("commands = %+v", commands)
	}
}
