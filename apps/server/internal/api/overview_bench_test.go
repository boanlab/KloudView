package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// seedFleet builds the shape a real fleet has: every node sits in a rack under an
// environment, and carries the containers and processes its inventory reported.
// Sub-resources reach their group through the node's relation, never a
// membership of their own.
func seedFleet(nodes, perNode int) *store.Memory {
	memory := store.NewMemory()
	now := time.Now().UTC()
	memory.PutGroup(domain.Group{ID: "env-prod", Name: "production", Type: "environment"})
	for n := range nodes {
		nodeID := fmt.Sprintf("node-%03d", n)
		rackID := fmt.Sprintf("rack-%02d", n/10)
		memory.PutGroup(domain.Group{ID: rackID, Name: rackID, Type: "rack", ParentID: "env-prod"})
		memory.UpsertResource(domain.Resource{ID: nodeID, Name: nodeID, Type: domain.ResourceNode, Health: domain.HealthHealthy, LastSeenAt: now})
		memory.PutMembership(domain.GroupMembership{ID: "m-" + nodeID, GroupID: rackID, ResourceID: nodeID})
		for i := range perNode {
			childID := fmt.Sprintf("%s-proc-%04d", nodeID, i)
			memory.UpsertResource(domain.Resource{ID: childID, Name: childID, Type: domain.ResourceProcess, Health: domain.HealthHealthy, LastSeenAt: now})
			memory.PutRelation(domain.Relation{ID: "r-" + childID, SourceID: nodeID, TargetID: childID, Type: "runs"})
		}
	}
	return memory
}

// benchmarkOverview measures the cache miss. The response is cached for two
// seconds, so a loop that reuses one query measures the cache instead of the
// work, and the miss is what decides whether the page ever stalls.
func benchmarkOverview(b *testing.B, nodes, perNode int, query string) {
	handler := New(seedFleet(nodes, perNode), "test-token", "").Handler()
	separator := "?"
	if query != "" {
		separator = "&"
	}
	miss := 0
	b.ResetTimer()
	for b.Loop() {
		miss++
		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/overview%s%scacheBust=%d", query, separator, miss), nil)
		request.Header.Set("X-KloudView-Subject", "admin")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			b.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("X-KloudView-Cache") != "miss" {
			b.Fatal("measured a cache hit rather than the work")
		}
	}
}

// A small fleet.
func BenchmarkOverviewFourNodes(b *testing.B) { benchmarkOverview(b, 4, 450, "") }

// A large fleet.
func BenchmarkOverviewFiftyNodes(b *testing.B) { benchmarkOverview(b, 50, 450, "") }

// The console asks for nodes only; the cost of the other 22,000 resources still
// lands on this request because they are all walked before the filter applies.
func BenchmarkOverviewFiftyNodesTypeFiltered(b *testing.B) {
	benchmarkOverview(b, 50, 450, "?types=node")
}

func BenchmarkResourceGroupIDsFiftyNodes(b *testing.B) {
	memory := seedFleet(50, 450)
	b.ResetTimer()
	for b.Loop() {
		memory.ResourceGroupIDs("node-007-proc-0100")
	}
}
