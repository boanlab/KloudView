package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Memory struct {
	// Held apart from everything below because it is never persisted: a log
	// read's answer is evidence while someone is looking at it, not state.
	reports *reportStore

	mu                     sync.RWMutex
	agents                 map[string]domain.Agent
	resources              map[string]domain.Resource
	groups                 map[string]domain.Group
	relations              map[string]domain.Relation
	members                map[string]domain.GroupMembership
	metrics                map[string][]domain.MetricSample
	alerts                 map[string]domain.Alert
	incidents              map[string]domain.Incident
	incidentEvents         map[string][]domain.IncidentEvent
	operations             map[string]domain.Operation
	alertRules             map[string]domain.AlertRule
	alertSilences          map[string]domain.AlertSilence
	alertInhibitions       map[string]domain.AlertInhibition
	notificationChannels   map[string]domain.NotificationChannel
	notificationRoutes     map[string]domain.NotificationRoute
	notificationDeliveries map[string]domain.NotificationDelivery
	runbooks               map[string]domain.Runbook
	executions             map[string]domain.RunbookExecution
	terminals              map[string]domain.TerminalSession
	terminalCommands       map[string]domain.TerminalCommand
	terminalRecordings     map[string]domain.TerminalRecording
	enrollmentTokens       map[string]domain.EnrollmentToken
	audit                  []domain.AuditEvent
	inventories            map[string]domain.AgentInventory
	users                  map[string]domain.User
	teams                  map[string]domain.Team
	pendingMetrics         []domain.MetricSample
	droppedMetrics         int
	logLines               map[string][]domain.LogLine
	logCounters            map[string][]domain.LogCounters
	// Resources are persisted incrementally rather than inside the state
	// document, so what changed since the last save is tracked here.
	dirtyResources   map[string]bool
	deletedResources map[string]bool
	dirtyInventories map[string]bool
}

func NewMemory() *Memory {
	return &Memory{reports: newReportStore(), agents: map[string]domain.Agent{}, resources: map[string]domain.Resource{}, groups: map[string]domain.Group{}, relations: map[string]domain.Relation{}, members: map[string]domain.GroupMembership{}, metrics: map[string][]domain.MetricSample{}, alerts: map[string]domain.Alert{}, incidents: map[string]domain.Incident{}, incidentEvents: map[string][]domain.IncidentEvent{}, operations: map[string]domain.Operation{}, alertRules: map[string]domain.AlertRule{}, alertSilences: map[string]domain.AlertSilence{}, alertInhibitions: map[string]domain.AlertInhibition{}, notificationChannels: map[string]domain.NotificationChannel{}, notificationRoutes: map[string]domain.NotificationRoute{}, notificationDeliveries: map[string]domain.NotificationDelivery{}, runbooks: map[string]domain.Runbook{}, executions: map[string]domain.RunbookExecution{}, terminals: map[string]domain.TerminalSession{}, terminalCommands: map[string]domain.TerminalCommand{}, terminalRecordings: map[string]domain.TerminalRecording{}, enrollmentTokens: map[string]domain.EnrollmentToken{}, inventories: map[string]domain.AgentInventory{}, users: map[string]domain.User{}, teams: map[string]domain.Team{}, logLines: map[string][]domain.LogLine{}, logCounters: map[string][]domain.LogCounters{}, dirtyResources: map[string]bool{}, deletedResources: map[string]bool{}, dirtyInventories: map[string]bool{}}
}

func (s *Memory) UpsertAgent(agent domain.Agent) domain.Agent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.agents[agent.ID]; ok {
		agent.CreatedAt = previous.CreatedAt
	} else {
		agent.CreatedAt = time.Now().UTC()
	}
	s.agents[agent.ID] = agent
	return agent
}

func (s *Memory) ListAgents() []domain.Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.Agent, 0, len(s.agents))
	for _, item := range s.agents {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Hostname < items[j].Hostname })
	return items
}

func (s *Memory) HasAgent(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.agents[id]
	return ok
}

func (s *Memory) Agent(id string) (domain.Agent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	agent, ok := s.agents[id]
	return agent, ok
}

func (s *Memory) DeleteAgent(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	agent, ok := s.agents[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.agents, id)
	delete(s.inventories, id)
	now := time.Now().UTC()
	for sessionID, session := range s.terminals {
		if session.TargetID != agent.NodeID || session.Status == "closed" {
			continue
		}
		session.Status = "closed"
		session.ClosedAt = &now
		s.terminals[sessionID] = session
		for commandID, command := range s.terminalCommands {
			if command.SessionID == sessionID && command.Status == "queued" {
				command.Status = "canceled"
				command.FinishedAt = &now
				s.terminalCommands[commandID] = command
			}
		}
	}
	for operationID, operation := range s.operations {
		if !contains(operation.TargetIDs, agent.NodeID) || operation.Status == "succeeded" || operation.Status == "failed" {
			continue
		}
		operation.Status = "failed"
		operation.Error = "owning agent removed"
		operation.UpdatedAt = now
		operation.FinishedAt = &now
		operation.LeaseUntil = nil
		s.operations[operationID] = operation
		if execution, ok := s.executions[operation.ExecutionID]; ok {
			execution.Status = "failed"
			execution.UpdatedAt = now
			execution.FinishedAt = &now
			s.executions[execution.ID] = execution
		}
	}
	removed := map[string]bool{agent.NodeID: true}
	for resourceID, resource := range s.resources {
		if resource.AgentID == id || resourceID == agent.NodeID {
			removed[resourceID] = true
			delete(s.resources, resourceID)
			delete(s.metrics, resourceID)
			s.markResourceDeleted(resourceID)
		}
	}
	for relationID, relation := range s.relations {
		if removed[relation.SourceID] || removed[relation.TargetID] {
			delete(s.relations, relationID)
		}
	}
	for membershipID, membership := range s.members {
		if removed[membership.ResourceID] {
			delete(s.members, membershipID)
		}
	}
	return nil
}

// How long a stopped VM or container stays queryable. Each is a named,
// deliberately created thing whose stopping is an event worth a month of
// history.
const terminatedResourceRetention = 30 * 24 * time.Hour

// keepsTerminatedRecord reports whether a type outlives its own inventory.
//
// A process does not. Inventory samples every few minutes while a host churns
// hundreds of processes an hour, so the ones caught between two samples are an
// arbitrary fraction of what ran: a list that cannot be complete is not
// evidence of anything, and it buries the stopped containers that are. A
// process missing from the latest inventory is dropped outright.
func keepsTerminatedRecord(resourceType domain.ResourceType) bool {
	return resourceType != domain.ResourceProcess
}

// PruneAgentResources marks children absent from the latest inventory as
// terminated, keeping them queryable, and removes those whose retention has
// passed. Deleting a VM or container on the first missing report would erase
// the evidence of what was running when something failed.
func (s *Memory) PruneAgentResources(agentID, nodeID string, retained map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	removed := map[string]bool{}
	for resourceID, resource := range s.resources {
		if resource.AgentID != agentID || resourceID == nodeID || retained[resourceID] {
			continue
		}
		if keepsTerminatedRecord(resource.Type) {
			if resource.TerminatedAt == nil {
				terminated := now
				resource.TerminatedAt = &terminated
				resource.UpdatedAt = now
				s.resources[resourceID] = resource
				s.markResourceDirty(resourceID)
				continue
			}
			if now.Sub(*resource.TerminatedAt) < terminatedResourceRetention {
				continue
			}
		}
		removed[resourceID] = true
		delete(s.resources, resourceID)
		delete(s.metrics, resourceID)
		s.markResourceDeleted(resourceID)
	}
	for relationID, relation := range s.relations {
		if removed[relation.SourceID] || removed[relation.TargetID] {
			delete(s.relations, relationID)
		}
	}
	for membershipID, membership := range s.members {
		if removed[membership.ResourceID] {
			delete(s.members, membershipID)
		}
	}
}

func (s *Memory) UpsertResource(resource domain.Resource) domain.Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if previous, ok := s.resources[resource.ID]; ok {
		resource.CreatedAt = previous.CreatedAt
	} else {
		resource.CreatedAt = now
	}
	resource.UpdatedAt = now
	s.resources[resource.ID] = resource
	s.markResourceDirty(resource.ID)
	return resource
}

// markResourceDirty records a change for the next incremental save. Callers
// hold the write lock.
func (s *Memory) markResourceDirty(id string) {
	if s.dirtyResources == nil {
		s.dirtyResources = map[string]bool{}
		s.deletedResources = map[string]bool{}
	}
	s.dirtyResources[id] = true
	delete(s.deletedResources, id)
}

func (s *Memory) markResourceDeleted(id string) {
	if s.dirtyResources == nil {
		s.dirtyResources = map[string]bool{}
		s.deletedResources = map[string]bool{}
	}
	s.deletedResources[id] = true
	delete(s.dirtyResources, id)
}

// PendingResourceChanges returns what to write and what to remove, without
// clearing them; the caller acknowledges once the write has committed.
func (s *Memory) PendingResourceChanges() ([]domain.Resource, []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	changed := make([]domain.Resource, 0, len(s.dirtyResources))
	for id := range s.dirtyResources {
		if resource, ok := s.resources[id]; ok {
			changed = append(changed, resource)
		}
	}
	removed := make([]string, 0, len(s.deletedResources))
	for id := range s.deletedResources {
		removed = append(removed, id)
	}
	return changed, removed
}

func (s *Memory) acknowledgeResourceChanges(changed []domain.Resource, removed []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, resource := range changed {
		if current, ok := s.resources[resource.ID]; ok && current.UpdatedAt.Equal(resource.UpdatedAt) {
			delete(s.dirtyResources, resource.ID)
		}
	}
	for _, id := range removed {
		delete(s.deletedResources, id)
	}
}

// markAllResourcesDirty schedules every resource for the next save, used when
// they are carried in from a store that held them another way.
func (s *Memory) markAllResourcesDirty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirtyResources == nil {
		s.dirtyResources = map[string]bool{}
		s.deletedResources = map[string]bool{}
	}
	for id := range s.resources {
		s.dirtyResources[id] = true
	}
}

// ReplaceResources loads a set read from storage without marking it dirty.
func (s *Memory) ReplaceResources(items []domain.Resource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources = make(map[string]domain.Resource, len(items))
	for _, item := range items {
		s.resources[item.ID] = item
	}
	s.dirtyResources = map[string]bool{}
	s.deletedResources = map[string]bool{}
}

// ListLiveResources excludes terminated records. Live views -- health totals,
// the heatmap, utilization, metric scoping -- describe what is running now, so
// a stopped container must not go on counting against them.
func (s *Memory) ListLiveResources() []domain.Resource {
	items := s.ListResources()
	live := items[:0]
	for _, item := range items {
		if item.TerminatedAt == nil {
			live = append(live, item)
		}
	}
	return live
}

func (s *Memory) ListResources() []domain.Resource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.Resource, 0, len(s.resources))
	for _, item := range s.resources {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if ri, rj := resourceTypeRank(items[i].Type), resourceTypeRank(items[j].Type); ri != rj {
			return ri < rj
		}
		if items[i].Name == items[j].Name {
			return items[i].ID < items[j].ID
		}
		return items[i].Name < items[j].Name
	})
	return items
}

// resourceTypeRank orders resources from most to least operationally significant
// so hosts and hypervisors surface ahead of the many discovered processes.
func resourceTypeRank(t domain.ResourceType) int {
	switch t {
	case domain.ResourceNode:
		return 0
	case domain.ResourceHypervisor:
		return 1
	case domain.ResourceVM:
		return 2
	case domain.ResourceContainer:
		return 3
	case domain.ResourceProcess:
		return 4
	default:
		return 5
	}
}

func (s *Memory) HasResource(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.resources[id]
	return ok
}

func (s *Memory) Resource(id string) (domain.Resource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resource, ok := s.resources[id]
	return resource, ok
}

func (s *Memory) DeleteResource(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.resources[id]; !ok {
		return ErrNotFound
	}
	delete(s.resources, id)
	delete(s.metrics, id)
	s.markResourceDeleted(id)
	for relationID, relation := range s.relations {
		if relation.SourceID == id || relation.TargetID == id {
			delete(s.relations, relationID)
		}
	}
	for membershipID, membership := range s.members {
		if membership.ResourceID == id {
			delete(s.members, membershipID)
		}
	}
	return nil
}

func (s *Memory) PutGroup(group domain.Group) domain.Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if previous, ok := s.groups[group.ID]; ok {
		group.CreatedAt = previous.CreatedAt
	} else {
		group.CreatedAt = now
	}
	group.UpdatedAt = now
	s.groups[group.ID] = group
	if group.Mode == "dynamic" {
		for id, member := range s.members {
			if member.GroupID == group.ID {
				delete(s.members, id)
			}
		}
	}
	return group
}

func (s *Memory) ListGroups() []domain.Group {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.Group, 0, len(s.groups))
	for _, item := range s.groups {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (s *Memory) Group(id string) (domain.Group, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.groups[id]
	return item, ok
}
func (s *Memory) HasChildGroups(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, group := range s.groups {
		if group.ParentID == id {
			return true
		}
	}
	return false
}
func (s *Memory) WouldCreateGroupCycle(id, parentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if id == parentID {
		return true
	}
	seen := map[string]bool{id: true}
	current := parentID
	for current != "" {
		if seen[current] {
			return true
		}
		seen[current] = true
		group, ok := s.groups[current]
		if !ok {
			return false
		}
		current = group.ParentID
	}
	return false
}
func (s *Memory) EntityExists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, resourceOK := s.resources[id]
	_, groupOK := s.groups[id]
	return resourceOK || groupOK
}

func (s *Memory) DeleteGroup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.groups[id]; !ok {
		return ErrNotFound
	}
	delete(s.groups, id)
	for key, member := range s.members {
		if member.GroupID == id {
			delete(s.members, key)
		}
	}
	return nil
}

func (s *Memory) PutRelation(relation domain.Relation) domain.Relation {
	s.mu.Lock()
	defer s.mu.Unlock()
	if relation.CreatedAt.IsZero() {
		relation.CreatedAt = time.Now().UTC()
	}
	s.relations[relation.ID] = relation
	return relation
}

func (s *Memory) ListRelations() []domain.Relation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.Relation, 0, len(s.relations))
	for _, item := range s.relations {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (s *Memory) DeleteRelation(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.relations[id]; !ok {
		return ErrNotFound
	}
	delete(s.relations, id)
	return nil
}

func (s *Memory) Relation(id string) (domain.Relation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	relation, ok := s.relations[id]
	return relation, ok
}

func (s *Memory) WouldCreateRelationCycle(sourceID, targetID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	queue := []string{targetID}
	visited := map[string]bool{}
	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]
		if currentID == sourceID {
			return true
		}
		if visited[currentID] {
			continue
		}
		visited[currentID] = true
		for _, relation := range s.relations {
			if relation.SourceID == currentID && scopeInheritingRelation(relation.Type) {
				queue = append(queue, relation.TargetID)
			}
		}
	}
	return false
}

func (s *Memory) PutMembership(member domain.GroupMembership) domain.GroupMembership {
	s.mu.Lock()
	defer s.mu.Unlock()
	if member.CreatedAt.IsZero() {
		member.CreatedAt = time.Now().UTC()
	}
	s.members[member.ID] = member
	return member
}

func (s *Memory) ListMemberships(groupID string) []domain.GroupMembership {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.GroupMembership{}
	for _, item := range s.members {
		if groupID == "" || item.GroupID == groupID {
			items = append(items, item)
		}
	}
	for id, group := range s.groups {
		if group.Mode != "dynamic" || (groupID != "" && groupID != id) {
			continue
		}
		for _, resource := range s.resources {
			if tagsMatch(resource.Tags, group.Selector) {
				items = append(items, domain.GroupMembership{ID: "dynamic-" + id + "-" + resource.ID, GroupID: id, ResourceID: resource.ID, CreatedAt: group.CreatedAt})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (s *Memory) ResourceMatchesScope(resourceID, scopePath string, selector map[string]string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if (scopePath == "" || scopePath == "*") && len(selector) == 0 {
		return true
	}
	resource, ok := s.resources[resourceID]
	if !ok {
		return false
	}
	if !tagsMatch(resource.Tags, selector) {
		return false
	}
	if scopePath == "" || scopePath == "*" {
		return true
	}
	normalized := normalizePath(scopePath)
	for _, groupID := range s.resourceGroupIDsLocked(resourceID) {
		if group, exists := s.groups[groupID]; exists {
			path := groupPath(group, s.groups)
			if path == normalized || strings.HasPrefix(path, normalized+"/") {
				return true
			}
		}
	}
	return false
}

func (s *Memory) ResourceGroupIDs(resourceID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resourceGroupIDsLocked(resourceID)
}

func (s *Memory) resourceGroupIDsLocked(resourceID string) []string {
	return s.resourceGroupIDsWith(s.scopeIndexLocked(), resourceID)
}

func (s *Memory) resourceGroupIDsWith(index *scopeIndex, resourceID string) []string {
	groupIDs := map[string]bool{}
	queue := []string{resourceID}
	visited := map[string]bool{}
	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]
		if visited[currentID] {
			continue
		}
		visited[currentID] = true
		current := s.resources[currentID]
		for _, groupID := range index.membershipsOf(currentID) {
			groupIDs[groupID] = true
		}
		for _, group := range index.dynamic {
			if tagsMatch(current.Tags, group.Selector) {
				groupIDs[group.ID] = true
			}
		}
		for _, parentID := range index.parentsOf(currentID) {
			if !visited[parentID] {
				queue = append(queue, parentID)
			}
		}
	}
	// A group grants its scope to everything under it, so every ancestor of a
	// matched group counts as matched too.
	seeds := make([]string, 0, len(groupIDs))
	for groupID := range groupIDs {
		seeds = append(seeds, groupID)
	}
	for _, groupID := range seeds {
		for _, ancestor := range s.ancestorsOf(index, groupID) {
			groupIDs[ancestor] = true
		}
	}
	items := make([]string, 0, len(groupIDs))
	for groupID := range groupIDs {
		items = append(items, groupID)
	}
	sort.Strings(items)
	return items
}

func scopeInheritingRelation(relationType string) bool {
	return relationType == "hosts" || relationType == "runs" || relationType == "contains"
}

func (s *Memory) ResourceAccessContext(resourceID string) ([]string, map[string]string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resource, ok := s.resources[resourceID]
	if !ok {
		return nil, nil, false
	}
	return s.accessPathsWith(s.scopeIndexLocked(), resourceID), cloneMap(resource.Tags), true
}

func (s *Memory) accessPathsWith(index *scopeIndex, resourceID string) []string {
	return s.pathsOfGroups(index, s.resourceGroupIDsWith(index, resourceID))
}

// pathsOfGroups is the scope path set a resource in these groups is matched
// against. A resource with no group still gets one empty path, so a binding
// that names no path matches it.
func (s *Memory) pathsOfGroups(index *scopeIndex, groupIDs []string) []string {
	paths := []string{}
	seen := map[string]bool{}
	for _, groupID := range groupIDs {
		path, exists := s.groupPathOf(index, groupID)
		if !exists || seen[path] {
			continue
		}
		paths, seen[path] = append(paths, path), true
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		paths = append(paths, "")
	}
	return paths
}

func (s *Memory) GroupAccessContext(groupID string) (string, map[string]string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, ok := s.groups[groupID]
	if !ok {
		return "", nil, false
	}
	return groupPath(group, s.groups), cloneMap(group.Tags), true
}

func cloneMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func tagsMatch(tags, selector map[string]string) bool {
	for key, value := range selector {
		if tags[key] != value {
			return false
		}
	}
	return true
}
func normalizePath(value string) string {
	value = strings.ToLower(strings.Trim(value, " /"))
	return strings.ReplaceAll(value, " ", "-")
}
func groupPath(group domain.Group, groups map[string]domain.Group) string {
	if group.Path != "" {
		return normalizePath(group.Path)
	}
	parts := []string{normalizePath(group.Name)}
	seen := map[string]bool{group.ID: true}
	parentID := group.ParentID
	for parentID != "" && !seen[parentID] {
		parent, ok := groups[parentID]
		if !ok {
			break
		}
		seen[parentID] = true
		parts = append([]string{normalizePath(parent.Name)}, parts...)
		parentID = parent.ParentID
	}
	return strings.Join(parts, "/")
}

func (s *Memory) DeleteMembership(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.members[id]; !ok {
		return ErrNotFound
	}
	delete(s.members, id)
	return nil
}

func (s *Memory) Membership(id string) (domain.GroupMembership, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	membership, ok := s.members[id]
	return membership, ok
}

// pendingMetricLimit bounds the samples waiting to be written. Everything else
// held in memory has a ceiling — the audit log is trimmed, log lines and
// counters age out — and this queue drains only when a save commits, so a
// storage fault that lasts hours would otherwise grow it until the process
// dies. Past the limit the oldest samples go: the newest readings are the ones
// the console and the rules are asking about.
const pendingMetricLimit = 100000

// pendingMetricBatchLimit bounds one save's worth. A backlog is drained over
// several saves rather than in a single transaction large enough to hold a
// lock for minutes.
const pendingMetricBatchLimit = 10000

func (s *Memory) AddMetric(sample domain.MetricSample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := append(s.metrics[sample.ResourceID], sample)
	s.metrics[sample.ResourceID] = compactMetrics(items)
	s.pendingMetrics = append(s.pendingMetrics, sample)
	if excess := len(s.pendingMetrics) - pendingMetricLimit; excess > 0 {
		s.pendingMetrics = append([]domain.MetricSample(nil), s.pendingMetrics[excess:]...)
		s.droppedMetrics += excess
	}
}

// DroppedMetrics reports how many samples were discarded because the write
// queue was full, so a caller can say so rather than lose them silently.
func (s *Memory) DroppedMetrics() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.droppedMetrics
}

func (s *Memory) pendingMetricBatch() ([]domain.MetricSample, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	size := len(s.pendingMetrics)
	if size > pendingMetricBatchLimit {
		size = pendingMetricBatchLimit
	}
	items := append([]domain.MetricSample(nil), s.pendingMetrics[:size]...)
	return items, len(items)
}

func (s *Memory) acknowledgeMetricBatch(count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if count > len(s.pendingMetrics) {
		count = len(s.pendingMetrics)
	}
	s.pendingMetrics = append([]domain.MetricSample(nil), s.pendingMetrics[count:]...)
}

func compactMetrics(items []domain.MetricSample) []domain.MetricSample {
	const (
		retention     = 24 * time.Hour
		rawRetention  = time.Hour
		archiveBucket = 5 * time.Minute
		maxRawSamples = 720
	)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Timestamp.Before(items[j].Timestamp) })
	deduplicated := make([]domain.MetricSample, 0, len(items))
	for _, sample := range items {
		if len(deduplicated) > 0 && sample.Timestamp.Equal(deduplicated[len(deduplicated)-1].Timestamp) {
			deduplicated[len(deduplicated)-1] = sample
			continue
		}
		deduplicated = append(deduplicated, sample)
	}
	if len(deduplicated) == 0 {
		return deduplicated
	}
	newest := deduplicated[len(deduplicated)-1].Timestamp
	retentionStart := newest.Add(-retention)
	rawStart := newest.Add(-rawRetention)
	archived := make([]domain.MetricSample, 0, 288)
	recent := make([]domain.MetricSample, 0, maxRawSamples)
	var archiveKey int64 = -1
	for _, sample := range deduplicated {
		if sample.Timestamp.Before(retentionStart) {
			continue
		}
		if sample.Timestamp.Before(rawStart) {
			key := sample.Timestamp.UnixNano() / archiveBucket.Nanoseconds()
			if len(archived) > 0 && key == archiveKey {
				archived[len(archived)-1] = sample
			} else {
				archived = append(archived, sample)
				archiveKey = key
			}
			continue
		}
		recent = append(recent, sample)
	}
	if len(recent) > maxRawSamples {
		recent = recent[len(recent)-maxRawSamples:]
	}
	return append(archived, recent...)
}

func (s *Memory) Metrics(resourceID string) []domain.MetricSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]domain.MetricSample(nil), s.metrics[resourceID]...)
}

func (s *Memory) LatestMetrics() map[string]domain.MetricSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make(map[string]domain.MetricSample, len(s.metrics))
	for resourceID, samples := range s.metrics {
		if len(samples) > 0 {
			items[resourceID] = samples[len(samples)-1]
		}
	}
	return items
}

// ResourceAveragesSince returns the mean CPU/Memory/Disk per resource over
// samples at or after `since`, for judging sustained rather than momentary usage.
func (s *Memory) ResourceAveragesSince(since time.Time) map[string]domain.MetricSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	averages := make(map[string]domain.MetricSample, len(s.metrics))
	for resourceID, samples := range s.metrics {
		var cpu, memory, disk float64
		count := 0
		for i := range samples {
			if samples[i].Timestamp.Before(since) {
				continue
			}
			cpu += samples[i].CPU
			memory += samples[i].Memory
			disk += samples[i].Disk
			count++
		}
		if count > 0 {
			averages[resourceID] = domain.MetricSample{ResourceID: resourceID, CPU: cpu / float64(count), Memory: memory / float64(count), Disk: disk / float64(count)}
		}
	}
	return averages
}

func (s *Memory) NetworkRates() map[string]domain.NetworkRate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make(map[string]domain.NetworkRate, len(s.metrics))
	for resourceID, samples := range s.metrics {
		if rate, ok := networkRate(samples); ok {
			items[resourceID] = rate
		}
	}
	return items
}

func (s *Memory) NetworkRate(resourceID string) domain.NetworkRate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rate, _ := networkRate(s.metrics[resourceID])
	return rate
}

func networkRate(samples []domain.MetricSample) (domain.NetworkRate, bool) {
	if len(samples) < 2 {
		return domain.NetworkRate{}, false
	}
	previous, latest := samples[len(samples)-2], samples[len(samples)-1]
	seconds := latest.Timestamp.Sub(previous.Timestamp).Seconds()
	if seconds <= 0 {
		return domain.NetworkRate{}, false
	}
	rate := domain.NetworkRate{}
	if latest.NetworkRx >= previous.NetworkRx {
		rate.Rx = float64(latest.NetworkRx-previous.NetworkRx) / seconds
	}
	if latest.NetworkTx >= previous.NetworkTx {
		rate.Tx = float64(latest.NetworkTx-previous.NetworkTx) / seconds
	}
	return rate, true
}

func (s *Memory) AggregatedMetrics(since time.Time, bucket time.Duration) []domain.MetricPoint {
	return s.AggregatedMetricsFor(since, bucket, nil)
}

func (s *Memory) AggregatedMetricsFor(since time.Time, bucket time.Duration, allowed map[string]bool) []domain.MetricPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if bucket <= 0 {
		bucket = time.Minute
	}
	points := map[int64]*domain.MetricPoint{}
	// Network is a counter, so its rate is derived per resource from consecutive
	// samples, averaged within a bucket, then summed across resources to give the
	// fleet-total throughput (consistent with the live network KPI).
	type netAcc struct {
		rx, tx float64
		n      int
	}
	bucketNet := map[int64]map[string]*netAcc{}
	for resourceID, samples := range s.metrics {
		if allowed != nil && !allowed[resourceID] {
			continue
		}
		var previous *domain.MetricSample
		for i := range samples {
			sample := samples[i]
			if !sample.Timestamp.Before(since) {
				key := sample.Timestamp.UnixNano() / bucket.Nanoseconds()
				point, ok := points[key]
				if !ok {
					point = &domain.MetricPoint{Timestamp: time.Unix(0, key*bucket.Nanoseconds()).UTC()}
					points[key] = point
				}
				point.Count++
				point.CPU += sample.CPU
				point.Memory += sample.Memory
				point.Disk += sample.Disk
				if previous != nil {
					seconds := sample.Timestamp.Sub(previous.Timestamp).Seconds()
					if seconds > 0 && sample.NetworkRx >= previous.NetworkRx && sample.NetworkTx >= previous.NetworkTx {
						if bucketNet[key] == nil {
							bucketNet[key] = map[string]*netAcc{}
						}
						acc := bucketNet[key][resourceID]
						if acc == nil {
							acc = &netAcc{}
							bucketNet[key][resourceID] = acc
						}
						acc.rx += float64(sample.NetworkRx-previous.NetworkRx) / seconds
						acc.tx += float64(sample.NetworkTx-previous.NetworkTx) / seconds
						acc.n++
					}
				}
			}
			previous = &samples[i]
		}
	}
	for key, resources := range bucketNet {
		point := points[key]
		if point == nil {
			continue
		}
		for _, acc := range resources {
			if acc.n > 0 {
				point.NetworkRxRate += acc.rx / float64(acc.n)
				point.NetworkTxRate += acc.tx / float64(acc.n)
			}
		}
	}
	items := make([]domain.MetricPoint, 0, len(points))
	for _, point := range points {
		point.CPU /= float64(point.Count)
		point.Memory /= float64(point.Count)
		point.Disk /= float64(point.Count)
		items = append(items, *point)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Timestamp.Before(items[j].Timestamp) })
	return items
}

func (s *Memory) MetricSummary(resourceID string) domain.MetricSummary {
	return s.MetricSummaryFor(resourceID, nil)
}

func (s *Memory) MetricSummaryFor(resourceID string, allowed map[string]bool) domain.MetricSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	summary := domain.MetricSummary{ResourceID: resourceID}
	for id, samples := range s.metrics {
		if resourceID != "" && id != resourceID {
			continue
		}
		if allowed != nil && !allowed[id] {
			continue
		}
		if len(samples) == 0 {
			continue
		}
		sample := samples[len(samples)-1]
		summary.Count++
		summary.CPUAvg += sample.CPU
		summary.MemoryAvg += sample.Memory
		summary.DiskAvg += sample.Disk
		if sample.CPU > summary.CPUMax {
			summary.CPUMax = sample.CPU
		}
		if sample.Memory > summary.MemoryMax {
			summary.MemoryMax = sample.Memory
		}
		if sample.Disk > summary.DiskMax {
			summary.DiskMax = sample.Disk
		}
		summary.NetworkRx += sample.NetworkRx
		summary.NetworkTx += sample.NetworkTx
		if sample.Timestamp.After(summary.ObservedAt) {
			summary.ObservedAt = sample.Timestamp
		}
		if len(samples) > 1 {
			previous := samples[len(samples)-2]
			seconds := sample.Timestamp.Sub(previous.Timestamp).Seconds()
			if seconds > 0 {
				if sample.NetworkRx >= previous.NetworkRx {
					summary.NetworkRxRate += float64(sample.NetworkRx-previous.NetworkRx) / seconds
				}
				if sample.NetworkTx >= previous.NetworkTx {
					summary.NetworkTxRate += float64(sample.NetworkTx-previous.NetworkTx) / seconds
				}
			}
		}
	}
	if summary.Count > 0 {
		divisor := float64(summary.Count)
		summary.CPUAvg /= divisor
		summary.MemoryAvg /= divisor
		summary.DiskAvg /= divisor
	}
	return summary
}

func (s *Memory) PutAlert(alert domain.Alert) domain.Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if previous, ok := s.alerts[alert.ID]; ok {
		alert.StartedAt = previous.StartedAt
	} else if alert.StartedAt.IsZero() {
		alert.StartedAt = now
	}
	alert.UpdatedAt = now
	if alert.Status == "resolved" && alert.ResolvedAt == nil {
		alert.ResolvedAt = &now
	} else if alert.Status != "resolved" {
		alert.ResolvedAt = nil
	}
	s.alerts[alert.ID] = alert
	return alert
}

func (s *Memory) ListAlerts() []domain.Alert {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.Alert, 0, len(s.alerts))
	for _, item := range s.alerts {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items
}

func (s *Memory) Alert(id string) (domain.Alert, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.alerts[id]
	return item, ok
}

func (s *Memory) DeleteAlert(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.alerts[id]; !ok {
		return ErrNotFound
	}
	delete(s.alerts, id)
	return nil
}

func (s *Memory) PutIncident(incident domain.Incident) domain.Incident {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if previous, ok := s.incidents[incident.ID]; ok {
		incident.CreatedAt = previous.CreatedAt
	} else {
		incident.CreatedAt = now
	}
	incident.UpdatedAt = now
	if incident.Status == "resolved" && incident.ResolvedAt == nil {
		incident.ResolvedAt = &now
	} else if incident.Status != "resolved" {
		incident.ResolvedAt = nil
	}
	s.incidents[incident.ID] = incident
	return incident
}

func (s *Memory) ListIncidents() []domain.Incident {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.Incident, 0, len(s.incidents))
	for _, item := range s.incidents {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items
}
func (s *Memory) Incident(id string) (domain.Incident, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.incidents[id]
	return item, ok
}
func (s *Memory) AddIncidentEvent(event domain.IncidentEvent) domain.IncidentEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.incidentEvents[event.IncidentID] = append(s.incidentEvents[event.IncidentID], event)
	return event
}
func (s *Memory) IncidentEvents(incidentID string) []domain.IncidentEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := append([]domain.IncidentEvent(nil), s.incidentEvents[incidentID]...)
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func (s *Memory) DeleteIncident(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.incidents[id]; !ok {
		return ErrNotFound
	}
	delete(s.incidents, id)
	delete(s.incidentEvents, id)
	return nil
}

// Operations live in the state document, which is rewritten whole on every
// save, and an inventory refresh answers with the whole host report -- already
// stored as the inventory. Both are bounded so the duplicate stays small.
const (
	operationResultLimit = 4 << 10
	operationHistory     = 500
)

// Report returns one operation's full output while it is still held.
func (s *Memory) Report(operationID string) (string, bool) {
	return s.reports.Get(operationID)
}

// summariseReport is what goes in the state document instead of the report.
// A trailing newline ends the last line rather than starting another one.
func summariseReport(result string) string {
	lines := strings.Count(strings.TrimSuffix(result, "\n"), "\n") + 1
	return fmt.Sprintf("%d lines, %d bytes", lines, len(result))
}

func truncateResult(result string) string {
	if len(result) <= operationResultLimit {
		return result
	}
	return result[:operationResultLimit] + "\n… truncated; the full report is on the resource it describes"
}

func (s *Memory) PutOperation(operation domain.Operation) domain.Operation {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if previous, ok := s.operations[operation.ID]; ok {
		operation.CreatedAt = previous.CreatedAt
	} else {
		operation.CreatedAt = now
	}
	operation.UpdatedAt = now
	operation.Result = truncateResult(operation.Result)
	s.operations[operation.ID] = operation
	s.pruneOperations()
	return operation
}

// pruneOperations keeps the most recent finished operations. Callers hold the
// write lock; anything unfinished is kept whatever its age.
func (s *Memory) pruneOperations() {
	if len(s.operations) <= operationHistory {
		return
	}
	finished := make([]domain.Operation, 0, len(s.operations))
	for _, operation := range s.operations {
		if operation.FinishedAt != nil {
			finished = append(finished, operation)
		}
	}
	if len(finished) <= operationHistory {
		return
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].UpdatedAt.After(finished[j].UpdatedAt) })
	for _, operation := range finished[operationHistory:] {
		delete(s.operations, operation.ID)
	}
}

func (s *Memory) ListOperations() []domain.Operation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.Operation, 0, len(s.operations))
	for _, item := range s.operations {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items
}

func (s *Memory) Operation(id string) (domain.Operation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	operation, ok := s.operations[id]
	return operation, ok
}

func (s *Memory) ClaimOperation(nodeID string) (domain.Operation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	var selected domain.Operation
	found := false
	for id, operation := range s.operations {
		expired := operation.Status == "running" && operation.LeaseUntil != nil && operation.LeaseUntil.Before(now)
		if !contains(operation.TargetIDs, nodeID) || operation.Status != "pending" && !expired {
			continue
		}
		if expired && (operation.Attempts >= 3 || operation.Type == "service.restart") {
			operation.Status = "failed"
			operation.Error = "operation lease expired after maximum attempts"
			if operation.Type == "service.restart" {
				operation.Error = "service restart completion unknown; automatic retry disabled"
			}
			operation.UpdatedAt = now
			operation.FinishedAt = &now
			operation.LeaseUntil = nil
			s.operations[id] = operation
			if execution, ok := s.executions[operation.ExecutionID]; ok {
				execution.Status = "failed"
				execution.UpdatedAt = now
				execution.FinishedAt = &now
				s.executions[execution.ID] = execution
			}
			continue
		}
		if !found || operation.CreatedAt.Before(selected.CreatedAt) {
			selected = operation
			found = true
		}
	}
	if !found {
		return domain.Operation{}, false
	}
	leaseUntil := now.Add(45 * time.Second)
	selected.Status = "running"
	selected.StartedAt = &now
	selected.LeaseUntil = &leaseUntil
	selected.Attempts++
	selected.UpdatedAt = now
	s.operations[selected.ID] = selected
	return selected, true
}

func (s *Memory) CompleteOperation(id, nodeID, status, result, operationError string) (domain.Operation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.operations[id]
	if !ok || operation.Status != "running" || !contains(operation.TargetIDs, nodeID) {
		return domain.Operation{}, false
	}
	now := time.Now().UTC()
	operation.Status = status
	// A log read answers with more than the state document should carry, so
	// the text is held aside and the operation keeps a line saying what is
	// there. Everything else keeps its output where it always was.
	if operation.Type == "logs.capture" && result != "" {
		s.reports.Put(id, result)
		operation.Result = summariseReport(result)
	} else {
		operation.Result = truncateResult(result)
	}
	operation.Error = operationError
	operation.UpdatedAt = now
	operation.FinishedAt = &now
	operation.LeaseUntil = nil
	s.operations[id] = operation
	if operation.ExecutionID != "" {
		if execution, exists := s.executions[operation.ExecutionID]; exists {
			execution.UpdatedAt = now
			if status == "failed" {
				execution.Status = "failed"
				execution.FinishedAt = &now
			} else {
				nextFound := false
				for _, operationID := range execution.OperationIDs {
					next := s.operations[operationID]
					if next.StepIndex == operation.StepIndex+1 {
						next.Status = "pending"
						next.UpdatedAt = now
						s.operations[operationID] = next
						nextFound = true
						break
					}
				}
				if !nextFound {
					execution.Status = "succeeded"
					execution.FinishedAt = &now
				}
			}
			s.executions[execution.ID] = execution
		}
	}
	return operation, true
}

// allowSelf carries the caller's approve-self grant, which is never inherited
// from a wildcard. The refusal stays here so no path can route around it; the
// permission can only be read a layer up.
func (s *Memory) ApproveOperation(id, approver string, allowSelf bool) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.operations[id]
	if !ok {
		return domain.Operation{}, ErrNotFound
	}
	if operation.Status != "awaiting_approval" {
		return domain.Operation{}, errors.New("operation is not awaiting approval")
	}
	if operation.RequestedBy == approver && !allowSelf {
		return domain.Operation{}, errors.New("the requester cannot approve their own operation; this needs a second identity, or a role holding operations:approve-self")
	}
	now := time.Now().UTC()
	operation.ApprovedBy = approver
	operation.Status = "pending"
	operation.UpdatedAt = now
	s.operations[id] = operation
	return operation, nil
}

func (s *Memory) PutAlertRule(rule domain.AlertRule) domain.AlertRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if previous, ok := s.alertRules[rule.ID]; ok {
		rule.CreatedAt = previous.CreatedAt
	} else {
		rule.CreatedAt = now
	}
	rule.UpdatedAt = now
	s.alertRules[rule.ID] = rule
	return rule
}
func (s *Memory) ListAlertRules() []domain.AlertRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.AlertRule, 0, len(s.alertRules))
	for _, item := range s.alertRules {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}
func (s *Memory) AlertRule(id string) (domain.AlertRule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.alertRules[id]
	return item, ok
}
func (s *Memory) DeleteAlertRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.alertRules[id]; !ok {
		return ErrNotFound
	}
	delete(s.alertRules, id)
	return nil
}

func (s *Memory) PutAlertSilence(silence domain.AlertSilence) domain.AlertSilence {
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.alertSilences[silence.ID]; ok {
		silence.CreatedAt = previous.CreatedAt
	} else if silence.CreatedAt.IsZero() {
		silence.CreatedAt = time.Now().UTC()
	}
	s.alertSilences[silence.ID] = silence
	return silence
}

func (s *Memory) ListAlertSilences() []domain.AlertSilence {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.AlertSilence, 0, len(s.alertSilences))
	for _, item := range s.alertSilences {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].StartsAt.After(items[j].StartsAt) })
	return items
}

func (s *Memory) AlertSilence(id string) (domain.AlertSilence, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.alertSilences[id]
	return item, ok
}

func (s *Memory) DeleteAlertSilence(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.alertSilences[id]; !ok {
		return ErrNotFound
	}
	delete(s.alertSilences, id)
	return nil
}

func (s *Memory) PutRunbook(runbook domain.Runbook) domain.Runbook {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if previous, ok := s.runbooks[runbook.ID]; ok {
		runbook.CreatedAt = previous.CreatedAt
	} else {
		runbook.CreatedAt = now
	}
	runbook.UpdatedAt = now
	s.runbooks[runbook.ID] = runbook
	return runbook
}
func (s *Memory) ListRunbooks() []domain.Runbook {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.Runbook, 0, len(s.runbooks))
	for _, item := range s.runbooks {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}
func (s *Memory) DeleteRunbook(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runbooks[id]; !ok {
		return ErrNotFound
	}
	delete(s.runbooks, id)
	return nil
}

func (s *Memory) Runbook(id string) (domain.Runbook, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.runbooks[id]
	return item, ok
}

func (s *Memory) CreateRunbookExecution(execution domain.RunbookExecution, operations []domain.Operation) domain.RunbookExecution {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executions[execution.ID] = execution
	for _, operation := range operations {
		s.operations[operation.ID] = operation
	}
	return execution
}
func (s *Memory) ListRunbookExecutions() []domain.RunbookExecution {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.RunbookExecution, 0, len(s.executions))
	for _, item := range s.executions {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items
}

func (s *Memory) RunbookExecution(id string) (domain.RunbookExecution, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	execution, ok := s.executions[id]
	return execution, ok
}

// allowSelf carries the caller's approve-self grant, which is never inherited
// from a wildcard. The refusal stays here so no path can route around it; the
// permission can only be read a layer up.
func (s *Memory) ApproveRunbookExecution(id, approver string, allowSelf bool) (domain.RunbookExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	execution, ok := s.executions[id]
	if !ok {
		return domain.RunbookExecution{}, ErrNotFound
	}
	if execution.Status != "awaiting_approval" {
		return domain.RunbookExecution{}, errors.New("execution is not awaiting approval")
	}
	if execution.RequestedBy == approver && !allowSelf {
		return domain.RunbookExecution{}, errors.New("the requester cannot approve their own execution; this needs a second identity, or a role holding runbooks:approve-self")
	}
	now := time.Now().UTC()
	execution.ApprovedBy = approver
	execution.Status = "running"
	execution.UpdatedAt = now
	if len(execution.OperationIDs) > 0 {
		operation := s.operations[execution.OperationIDs[0]]
		operation.Status = "pending"
		operation.ApprovedBy = approver
		operation.UpdatedAt = now
		s.operations[operation.ID] = operation
	}
	s.executions[id] = execution
	return execution, nil
}

func (s *Memory) PutTerminal(session domain.TerminalSession) domain.TerminalSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.terminals[session.ID]; ok {
		session.CreatedAt = previous.CreatedAt
	}
	s.terminals[session.ID] = session
	return session
}
func (s *Memory) ListTerminals() []domain.TerminalSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.TerminalSession, 0, len(s.terminals))
	for _, item := range s.terminals {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items
}
func (s *Memory) Terminal(id string) (domain.TerminalSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.terminals[id]
	return item, ok
}

func (s *Memory) PutTerminalCommand(command domain.TerminalCommand) domain.TerminalCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminalCommands[command.ID] = command
	return command
}

func (s *Memory) TerminalCommands(sessionID string) []domain.TerminalCommand {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []domain.TerminalCommand{}
	for _, command := range s.terminalCommands {
		if command.SessionID == sessionID {
			items = append(items, command)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func (s *Memory) ClaimTerminalCommand(targetID string) (domain.TerminalCommand, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var selected domain.TerminalCommand
	found := false
	now := time.Now().UTC()
	for id, command := range s.terminalCommands {
		if command.Status == "running" && command.StartedAt != nil && command.StartedAt.Before(now.Add(-45*time.Second)) {
			command.Status = "failed"
			command.Error = "terminal command completion unknown after execution timeout"
			command.FinishedAt = &now
			s.terminalCommands[id] = command
			continue
		}
		if command.TargetID != targetID || command.Status != "queued" {
			continue
		}
		session, ok := s.terminals[command.SessionID]
		if !ok || session.Status != "active" {
			continue
		}
		if !found || command.CreatedAt.Before(selected.CreatedAt) {
			selected, found = command, true
		}
	}
	if !found {
		return domain.TerminalCommand{}, false
	}
	selected.Status = "running"
	selected.StartedAt = &now
	s.terminalCommands[selected.ID] = selected
	return selected, true
}

func (s *Memory) CompleteTerminalCommand(id, targetID, status, output, commandError string) (domain.TerminalCommand, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	command, ok := s.terminalCommands[id]
	if !ok || command.Status != "running" || command.TargetID != targetID {
		return domain.TerminalCommand{}, false
	}
	now := time.Now().UTC()
	command.Status = status
	command.Output = output
	command.Error = commandError
	command.FinishedAt = &now
	s.terminalCommands[id] = command
	return command, true
}

func (s *Memory) CancelTerminalCommands(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for id, command := range s.terminalCommands {
		if command.SessionID != sessionID || command.Status != "queued" {
			continue
		}
		command.Status = "canceled"
		command.FinishedAt = &now
		s.terminalCommands[id] = command
	}
}

func (s *Memory) AddAudit(event domain.AuditEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, event)
	if len(s.audit) > 10000 {
		s.audit = s.audit[len(s.audit)-10000:]
	}
}
func (s *Memory) Audit() []domain.AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := append([]domain.AuditEvent(nil), s.audit...)
	sort.Slice(items, func(i, j int) bool { return items[i].Timestamp.After(items[j].Timestamp) })
	return items
}

func (s *Memory) PutInventory(inventory domain.AgentInventory) domain.AgentInventory {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inventories[inventory.AgentID] = inventory
	if s.dirtyInventories == nil {
		s.dirtyInventories = map[string]bool{}
	}
	s.dirtyInventories[inventory.AgentID] = true
	return inventory
}

// PendingInventories returns the reports changed since the last save.
func (s *Memory) PendingInventories() []domain.AgentInventory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.AgentInventory, 0, len(s.dirtyInventories))
	for agentID := range s.dirtyInventories {
		if inventory, ok := s.inventories[agentID]; ok {
			items = append(items, inventory)
		}
	}
	return items
}

func (s *Memory) acknowledgeInventories(items []domain.AgentInventory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		if current, ok := s.inventories[item.AgentID]; ok && current.ObservedAt.Equal(item.ObservedAt) {
			delete(s.dirtyInventories, item.AgentID)
		}
	}
}

// ReplaceInventories loads reports read from storage without marking them dirty.
func (s *Memory) ReplaceInventories(items []domain.AgentInventory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inventories = make(map[string]domain.AgentInventory, len(items))
	for _, item := range items {
		s.inventories[item.AgentID] = item
	}
	s.dirtyInventories = map[string]bool{}
}

// markAllInventoriesDirty schedules every report for the next save, used when
// they are carried in from a store that held them another way.
func (s *Memory) markAllInventoriesDirty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirtyInventories == nil {
		s.dirtyInventories = map[string]bool{}
	}
	for agentID := range s.inventories {
		s.dirtyInventories[agentID] = true
	}
}
func (s *Memory) Inventory(agentID string) (domain.AgentInventory, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.inventories[agentID]
	return item, ok
}
func (s *Memory) Inventories() []domain.AgentInventory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.AgentInventory, 0, len(s.inventories))
	for _, item := range s.inventories {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ObservedAt.After(items[j].ObservedAt) })
	return items
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
