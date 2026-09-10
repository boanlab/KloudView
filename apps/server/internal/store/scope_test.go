package store

import (
	"reflect"
	"slices"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// scopeFixture exercises every path scope resolution can take: a direct
// membership, a group nested under another, inheritance across a relation
// chain, a dynamic group matched by tags, a relation type that does not carry
// scope, and a relation cycle.
func scopeFixture() *Memory {
	memory := NewMemory()
	memory.PutGroup(domain.Group{ID: "env", Name: "Production", Type: "environment"})
	memory.PutGroup(domain.Group{ID: "rack", Name: "Rack 01", Type: "rack", ParentID: "env"})
	memory.PutGroup(domain.Group{ID: "tagged", Name: "Database Tier", Type: "tier", Mode: "dynamic", Selector: map[string]string{"tier": "db"}})

	memory.UpsertResource(domain.Resource{ID: "node", Name: "node", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "container", Name: "container", Type: domain.ResourceContainer})
	memory.UpsertResource(domain.Resource{ID: "process", Name: "process", Type: domain.ResourceProcess})
	memory.UpsertResource(domain.Resource{ID: "db", Name: "db", Type: domain.ResourceVM, Tags: map[string]string{"tier": "db"}})
	memory.UpsertResource(domain.Resource{ID: "orphan", Name: "orphan", Type: domain.ResourceVM})
	memory.UpsertResource(domain.Resource{ID: "peer", Name: "peer", Type: domain.ResourceVM})

	memory.PutMembership(domain.GroupMembership{ID: "m1", GroupID: "rack", ResourceID: "node"})
	memory.PutRelation(domain.Relation{ID: "r1", SourceID: "node", TargetID: "container", Type: "runs"})
	memory.PutRelation(domain.Relation{ID: "r2", SourceID: "container", TargetID: "process", Type: "runs"})
	// depends_on does not carry scope, so peer stays outside the rack.
	memory.PutRelation(domain.Relation{ID: "r3", SourceID: "node", TargetID: "peer", Type: "depends_on"})
	// A cycle must terminate rather than hang or drop groups.
	memory.PutRelation(domain.Relation{ID: "r4", SourceID: "process", TargetID: "node", Type: "contains"})
	return memory
}

func TestResourceGroupIDsResolvesEveryScopePath(t *testing.T) {
	memory := scopeFixture()
	for resourceID, want := range map[string][]string{
		"node":      {"env", "rack"},
		"container": {"env", "rack"},
		// Two relations deep, and a group's parent is inherited with it.
		"process": {"env", "rack"},
		"db":      {"tagged"},
		"orphan":  {},
		"peer":    {},
	} {
		if got := memory.ResourceGroupIDs(resourceID); !slices.Equal(got, want) {
			t.Errorf("%s groups = %v, want %v", resourceID, got, want)
		}
	}
}

// The bulk form exists only to avoid resolving one resource at a time. If it
// ever disagrees with the single-resource form, a fleet-wide read authorizes
// differently from the detail page for the same resource.
func TestResourceScopesMatchResolvingOneAtATime(t *testing.T) {
	memory := scopeFixture()
	scopes := memory.ResourceScopes()
	if len(scopes) != 6 {
		t.Fatalf("resolved %d resources, want every one", len(scopes))
	}
	for resourceID, scope := range scopes {
		if want := memory.ResourceGroupIDs(resourceID); !slices.Equal(scope.GroupIDs, want) {
			t.Errorf("%s groups: bulk %v, single %v", resourceID, scope.GroupIDs, want)
		}
	}
}

func TestResourceScopesMatchTheSingleResourceAccessContext(t *testing.T) {
	memory := scopeFixture()
	contexts := memory.ResourceScopes()
	for resourceID, context := range contexts {
		wantPaths, wantTags, ok := memory.ResourceAccessContext(resourceID)
		if !ok {
			t.Fatalf("%s is missing from the single-resource form", resourceID)
		}
		if !slices.Equal(context.Paths, wantPaths) {
			t.Errorf("%s paths = %v, want %v", resourceID, context.Paths, wantPaths)
		}
		if !reflect.DeepEqual(context.Tags, wantTags) {
			t.Errorf("%s tags = %v, want %v", resourceID, context.Tags, wantTags)
		}
	}
	// The paths a scope is matched against are the groups', not the resource's,
	// and a binding on the parent environment has to match as well as one on the
	// rack itself.
	if paths := contexts["process"].Paths; !slices.Equal(paths, []string{"production", "production/rack-01"}) {
		t.Errorf("inherited paths = %v", paths)
	}
	// A resource in no group still gets one entry, so an unscoped binding matches.
	if paths := contexts["orphan"].Paths; !slices.Equal(paths, []string{""}) {
		t.Errorf("ungrouped path = %v", paths)
	}
}

// Tags decide dynamic membership, so the index must be derived from live state
// rather than carried across a change.
func TestScopeFollowsATagChange(t *testing.T) {
	memory := scopeFixture()
	memory.UpsertResource(domain.Resource{ID: "orphan", Name: "orphan", Type: domain.ResourceVM, Tags: map[string]string{"tier": "db"}})
	if got := memory.ResourceGroupIDs("orphan"); !slices.Equal(got, []string{"tagged"}) {
		t.Fatalf("groups = %v, want the dynamic group to pick it up", got)
	}
	if got := memory.ResourceScopes()["orphan"].GroupIDs; !slices.Equal(got, []string{"tagged"}) {
		t.Fatalf("bulk groups = %v", got)
	}
}
