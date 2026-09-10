package store

import "github.com/kloudview/kloudview/apps/server/internal/domain"

// AccessContext is what a scope decision needs about one resource: the group
// paths it sits under, and its tags.
type AccessContext struct {
	Paths []string
	Tags  map[string]string
}

// scopeIndex is the reverse lookup scope resolution walks: which groups claim a
// resource directly, and which resources a scope-inheriting relation parents.
//
// Built per call from the live maps under the same lock, never cached: a stale
// scope is a wrong authorization decision.
//
// Inverting the maps costs a pass over all of them and only pays off across many
// resources, so a single lookup scans instead and leaves them unbuilt.
type scopeIndex struct {
	memory      *Memory
	inverted    bool
	memberships map[string][]string // resourceID -> groups naming it
	parents     map[string][]string // resourceID -> resources it inherits scope from
	dynamic     []domain.Group
	ancestors   map[string][]string // groupID -> itself and its ancestors
	paths       map[string]string   // groupID -> scope path
}

func (s *Memory) scopeIndexLocked() *scopeIndex {
	index := &scopeIndex{memory: s, ancestors: map[string][]string{}, paths: map[string]string{}}
	// Dynamic groups are matched against every resource visited, and there are
	// few of them, so this one is always worth collecting up front.
	for _, group := range s.groups {
		if group.Mode == "dynamic" {
			index.dynamic = append(index.dynamic, group)
		}
	}
	return index
}

func (s *Memory) invertedScopeIndexLocked() *scopeIndex {
	index := s.scopeIndexLocked()
	index.inverted = true
	index.memberships = make(map[string][]string, len(s.members))
	index.parents = make(map[string][]string, len(s.relations))
	for _, member := range s.members {
		index.memberships[member.ResourceID] = append(index.memberships[member.ResourceID], member.GroupID)
	}
	for _, relation := range s.relations {
		if scopeInheritingRelation(relation.Type) {
			index.parents[relation.TargetID] = append(index.parents[relation.TargetID], relation.SourceID)
		}
	}
	return index
}

func (i *scopeIndex) membershipsOf(resourceID string) []string {
	if i.inverted {
		return i.memberships[resourceID]
	}
	groupIDs := []string{}
	for _, member := range i.memory.members {
		if member.ResourceID == resourceID {
			groupIDs = append(groupIDs, member.GroupID)
		}
	}
	return groupIDs
}

func (i *scopeIndex) parentsOf(resourceID string) []string {
	if i.inverted {
		return i.parents[resourceID]
	}
	parents := []string{}
	for _, relation := range i.memory.relations {
		if relation.TargetID == resourceID && scopeInheritingRelation(relation.Type) {
			parents = append(parents, relation.SourceID)
		}
	}
	return parents
}

// ancestorsOf returns a group and every group above it, memoized so a deep tree
// is climbed once rather than once per resource beneath it.
func (s *Memory) ancestorsOf(index *scopeIndex, groupID string) []string {
	if cached, ok := index.ancestors[groupID]; ok {
		return cached
	}
	chain := []string{}
	seen := map[string]bool{}
	for current := groupID; current != "" && !seen[current]; {
		seen[current] = true
		chain = append(chain, current)
		group, ok := s.groups[current]
		if !ok {
			break
		}
		current = group.ParentID
	}
	index.ancestors[groupID] = chain
	return chain
}

func (s *Memory) groupPathOf(index *scopeIndex, groupID string) (string, bool) {
	if cached, ok := index.paths[groupID]; ok {
		return cached, true
	}
	group, exists := s.groups[groupID]
	if !exists {
		return "", false
	}
	path := groupPath(group, s.groups)
	index.paths[groupID] = path
	return path, true
}

// ResourceScope is everything a fleet-wide read needs to place one resource:
// the groups it belongs to, and the context an authorization decision takes.
type ResourceScope struct {
	GroupIDs []string
	AccessContext
}

// ResourceScopes resolves every resource in one pass, so a caller that needs
// the whole fleet builds the reverse index once instead of once per resource.
// Groups and paths come out together because the paths are derived from the
// groups; asking for them separately would walk the fleet twice.
func (s *Memory) ResourceScopes() map[string]ResourceScope {
	s.mu.RLock()
	defer s.mu.RUnlock()
	index := s.invertedScopeIndexLocked()
	all := make(map[string]ResourceScope, len(s.resources))
	for id, resource := range s.resources {
		groupIDs := s.resourceGroupIDsWith(index, id)
		all[id] = ResourceScope{
			GroupIDs:      groupIDs,
			AccessContext: AccessContext{Paths: s.pathsOfGroups(index, groupIDs), Tags: cloneMap(resource.Tags)},
		}
	}
	return all
}
