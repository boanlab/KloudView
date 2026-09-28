package api

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/access"
	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func validateAgentMetadata(hostname, version, protocol string, capabilities []string, labels map[string]string) error {
	if strings.TrimSpace(hostname) == "" || len(hostname) > 253 || strings.ContainsAny(hostname, "/\\ \t\r\n") {
		return fmt.Errorf("invalid hostname")
	}
	if len(version) > 64 || strings.ContainsAny(version, "\r\n\t") {
		return fmt.Errorf("invalid agent version")
	}
	if protocol != agentProtocolVersion {
		return fmt.Errorf("unsupported agent protocol")
	}
	if len(capabilities) > 16 || len(labels) > 32 {
		return fmt.Errorf("agent metadata exceeds supported limits")
	}
	// Every capability an agent can advertise. A missing name fails the whole
	// heartbeat, freezing the agent record while its other reports continue.
	allowedCapabilities := map[string]bool{"inventory": true, "metrics": true, "terminal": true, "logs": true}
	seen := map[string]bool{}
	for _, capability := range capabilities {
		if !allowedCapabilities[capability] || seen[capability] {
			return fmt.Errorf("invalid agent capability")
		}
		seen[capability] = true
	}
	for key, value := range labels {
		if strings.TrimSpace(key) == "" || len(key) > 64 || len(value) > 256 || strings.ContainsAny(key, "=, \t\r\n") || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("invalid agent label")
		}
	}
	return nil
}

func validateRole(role access.Role) error {
	if strings.TrimSpace(role.Name) == "" || len(role.Permissions) == 0 {
		return fmt.Errorf("name and permissions are required")
	}
	if len(role.Name) > 128 || len(role.Permissions) > 100 {
		return fmt.Errorf("role exceeds supported limits")
	}
	allowedActions := map[string]bool{
		"*": true, "read": true, "create": true, "update": true, "delete": true,
		"execute": true, "approve": true, "close": true,
		// Elevated grant that lets a subject approve its own terminal request.
		"approve-self": true,
	}
	seen := map[string]bool{}
	for _, permission := range role.Permissions {
		resource := strings.TrimSpace(permission.Resource)
		action := strings.TrimSpace(permission.Action)
		if resource == "" || len(resource) > 128 || !allowedActions[action] {
			return fmt.Errorf("invalid role permission")
		}
		key := resource + "\x00" + action
		if seen[key] {
			return fmt.Errorf("duplicate role permission")
		}
		seen[key] = true
	}
	return nil
}

// What a rule may be written against: shares of capacity and rates first, then
// the counters the kernel keeps.
var alertMetrics = map[string]bool{
	"cpu": true, "memory": true, "disk": true,
	"network_rx_rate": true, "network_tx_rate": true,
	"oom_kills": true, "throttled_usec": true, "throttled_count": true,
}

// The subset measured out of 100, and so the only ones a threshold above 100
// is meaningless for.
var percentMetrics = map[string]bool{"cpu": true, "memory": true, "disk": true}

func validateAlertRule(rule domain.AlertRule) error {
	if rule.Name == "" || rule.Metric == "" || rule.Operator == "" || rule.Duration == "" {
		return fmt.Errorf("name, metric, operator and duration are required")
	}
	if !alertMetrics[rule.Metric] {
		return fmt.Errorf("unsupported metric")
	}
	if rule.Operator != ">" && rule.Operator != ">=" && rule.Operator != "<" && rule.Operator != "<=" && rule.Operator != "==" {
		return fmt.Errorf("unsupported operator")
	}
	duration, err := time.ParseDuration(rule.Duration)
	if err != nil || duration < 0 || duration > 30*24*time.Hour {
		return fmt.Errorf("invalid duration")
	}
	if rule.Severity != "warning" && rule.Severity != "critical" {
		return fmt.Errorf("severity must be warning or critical")
	}
	// A share cannot exceed 100. A rate or a counter has no such ceiling: "the
	// kernel has killed something twice" and "held off the CPU for 4,000,000
	// microseconds" are both ordinary numbers.
	if rule.Threshold < 0 || (percentMetrics[rule.Metric] && rule.Threshold > 100) {
		return fmt.Errorf("threshold is outside the metric range")
	}
	for key, value := range rule.Selector {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return fmt.Errorf("selector keys and values are required")
		}
	}
	return nil
}

func validateAlertSilence(silence domain.AlertSilence) error {
	if strings.TrimSpace(silence.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if silence.StartsAt.IsZero() || silence.EndsAt.IsZero() || !silence.EndsAt.After(silence.StartsAt) {
		return fmt.Errorf("valid start and end times are required")
	}
	if silence.EndsAt.Sub(silence.StartsAt) > 30*24*time.Hour {
		return fmt.Errorf("silence duration cannot exceed 30 days")
	}
	for key, value := range silence.Selector {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return fmt.Errorf("selector keys and values are required")
		}
	}
	return nil
}

func (s *Server) authorizeResourceTarget(r *http.Request, resourceName, action, targetID string) bool {
	paths, tags, ok := s.store.ResourceAccessContext(targetID)
	return s.authorizeWithContext(s.subjectFromRequest(r), resourceName, action, store.AccessContext{Paths: paths, Tags: tags}, ok)
}

// resourceAuthorizer answers for one resource at a time but resolves the whole
// fleet's scopes once up front. Handlers that filter a collection must use this
// rather than authorizeResourceTarget in a loop: that call scans every
// membership and relation, so per-row it is quadratic in the fleet size.
func (s *Server) resourceAuthorizer(r *http.Request, resourceName, action string) func(string) bool {
	return s.resourceAuthorizerWith(r, resourceName, action, s.store.ResourceScopes())
}

func (s *Server) resourceAuthorizerWith(r *http.Request, resourceName, action string, scopes map[string]store.ResourceScope) func(string) bool {
	subject := s.subjectFromRequest(r)
	return func(resourceID string) bool {
		scope, known := scopes[resourceID]
		return s.authorizeWithContext(subject, resourceName, action, scope.AccessContext, known)
	}
}

// anyTargetAuthorizer is the bulk form of authorizeAnyTarget, for lists whose
// rows each name several targets.
func (s *Server) anyTargetAuthorizer(r *http.Request, resourceName, action string) func([]string) bool {
	allowed := s.resourceAuthorizer(r, resourceName, action)
	return func(targetIDs []string) bool {
		return s.anyTargetAllowed(r, resourceName, action, targetIDs, allowed)
	}
}

// anyTargetAllowed applies an already-prepared authorizer to a row's targets,
// keeping authorizeAnyTarget's rule that a row naming nothing falls back to the
// subject's configured scope.
func (s *Server) anyTargetAllowed(r *http.Request, resourceName, action string, targetIDs []string, allowed func(string) bool) bool {
	if len(targetIDs) == 0 {
		return s.authorizeConfiguredScope(r, resourceName, action, "", nil)
	}
	return slices.ContainsFunc(targetIDs, allowed)
}

// entityAuthorizer is resourceAuthorizer for collections that name resources and
// groups alike, such as relations. Decisions are memoized because a relation
// list references the same few hosts over and over.
func (s *Server) entityAuthorizer(r *http.Request, resourceName, action string) func(string) bool {
	subject := s.subjectFromRequest(r)
	scopes := s.store.ResourceScopes()
	decided := map[string]bool{}
	return func(entityID string) bool {
		if allowed, ok := decided[entityID]; ok {
			return allowed
		}
		allowed := false
		if scope, isResource := scopes[entityID]; isResource {
			allowed = s.authorizeWithContext(subject, resourceName, action, scope.AccessContext, true)
		} else {
			allowed = s.authorizeGroupTarget(r, resourceName, action, entityID)
		}
		decided[entityID] = allowed
		return allowed
	}
}

// authorizeWithContext is authorizeResourceTarget with the scope context already
// resolved. A fleet-wide read resolves every context in one pass and calls this,
// rather than paying a full scope resolution per row.
func (s *Server) authorizeWithContext(subject, resourceName, action string, context store.AccessContext, known bool) bool {
	if !known {
		return s.access.Evaluate(access.Request{SubjectID: subject, Resource: resourceName, Action: action, ResourcePath: ""}).Allowed
	}
	for _, path := range context.Paths {
		decision := s.access.Evaluate(access.Request{SubjectID: subject, Resource: resourceName, Action: action, ResourcePath: path, Tags: context.Tags})
		if decision.Allowed {
			return true
		}
	}
	return false
}

func (s *Server) authorizeGroupTarget(r *http.Request, resourceName, action, targetID string) bool {
	path, tags, ok := s.store.GroupAccessContext(targetID)
	if !ok {
		return false
	}
	return s.access.Evaluate(access.Request{SubjectID: s.subjectFromRequest(r), Resource: resourceName, Action: action, ResourcePath: path, Tags: tags}).Allowed
}

func (s *Server) authorizeEntityTarget(r *http.Request, resourceName, action, targetID string) bool {
	if s.store.HasResource(targetID) {
		return s.authorizeResourceTarget(r, resourceName, action, targetID)
	}
	return s.authorizeGroupTarget(r, resourceName, action, targetID)
}

func (s *Server) visibleResourceIDs(r *http.Request, resourceName string) map[string]bool {
	return s.visibleResourceIDsWith(r, resourceName, s.store.ResourceScopes())
}

func (s *Server) visibleResourceIDsWith(r *http.Request, resourceName string, scopes map[string]store.ResourceScope) map[string]bool {
	subject := s.subjectFromRequest(r)
	visible := map[string]bool{}
	authorize := func(resourceID string) {
		if visible[resourceID] {
			return
		}
		scope, known := scopes[resourceID]
		if s.authorizeWithContext(subject, resourceName, "read", scope.AccessContext, known) {
			visible[resourceID] = true
		}
	}
	for _, resource := range s.store.ListLiveResources() {
		authorize(resource.ID)
	}
	// A sample can outlive the resource it came from, so metrics are a second
	// source of ids rather than a subset of the first.
	for resourceID := range s.store.LatestMetrics() {
		authorize(resourceID)
	}
	return visible
}

func (s *Server) authorizeAnyTarget(r *http.Request, resourceName, action string, targetIDs []string) bool {
	if len(targetIDs) == 0 {
		return s.authorizeConfiguredScope(r, resourceName, action, "", nil)
	}
	for _, targetID := range targetIDs {
		if s.authorizeResourceTarget(r, resourceName, action, targetID) {
			return true
		}
	}
	return false
}

func (s *Server) authorizeAllTargets(r *http.Request, resourceName, action string, targetIDs []string) bool {
	if len(targetIDs) == 0 {
		return s.authorizeConfiguredScope(r, resourceName, action, "", nil)
	}
	for _, targetID := range targetIDs {
		if !s.authorizeResourceTarget(r, resourceName, action, targetID) {
			return false
		}
	}
	return true
}

func (s *Server) validateIncident(incident *domain.Incident) error {
	if strings.TrimSpace(incident.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if incident.Severity != "warning" && incident.Severity != "critical" {
		return fmt.Errorf("severity must be warning or critical")
	}
	if incident.Status == "" {
		incident.Status = "declared"
	}
	validStatus := map[string]bool{"declared": true, "investigating": true, "mitigating": true, "monitoring": true, "resolved": true}
	if !validStatus[incident.Status] {
		return fmt.Errorf("unsupported incident status")
	}
	resourceIDs := map[string]bool{}
	for _, resourceID := range incident.ResourceIDs {
		if !s.store.HasResource(resourceID) {
			return fmt.Errorf("resource %s not found", resourceID)
		}
		resourceIDs[resourceID] = true
	}
	for _, alertID := range incident.AlertIDs {
		alert, ok := s.store.Alert(alertID)
		if !ok {
			return fmt.Errorf("alert %s not found", alertID)
		}
		resourceIDs[alert.ResourceID] = true
	}
	if len(resourceIDs) == 0 {
		return fmt.Errorf("at least one resource or alert is required")
	}
	incident.ResourceIDs = incident.ResourceIDs[:0]
	for resourceID := range resourceIDs {
		incident.ResourceIDs = append(incident.ResourceIDs, resourceID)
	}
	sort.Strings(incident.ResourceIDs)
	return nil
}

func (s *Server) authorizeConfiguredScope(r *http.Request, resourceName, action, path string, tags map[string]string) bool {
	return s.access.Evaluate(access.Request{SubjectID: s.subjectFromRequest(r), Resource: resourceName, Action: action, ResourcePath: path, Tags: tags}).Allowed
}

func (s *Server) authorizeScopeDefinition(r *http.Request, resourceName, action string, scope access.Scope) bool {
	paths := scope.Paths
	if len(paths) == 0 {
		paths = []string{""}
	}
	for _, path := range paths {
		if !s.authorizeConfiguredScope(r, resourceName, action, path, scope.Tags) {
			return false
		}
	}
	return true
}

func (s *Server) authorizeBindingScope(r *http.Request, action string, binding access.Binding) bool {
	scope, ok := s.access.Scope(binding.ScopeID)
	return ok && s.authorizeScopeDefinition(r, "bindings", action, scope)
}

func validateGroup(group *domain.Group) (string, error) {
	if group.Name == "" || group.Type == "" {
		return "fields_required", fmt.Errorf("name and type are required")
	}
	if group.Mode == "" {
		group.Mode = "static"
	}
	if group.Mode != "static" && group.Mode != "dynamic" {
		return "invalid_mode", fmt.Errorf("mode must be static or dynamic")
	}
	if group.Mode == "dynamic" && len(group.Selector) == 0 {
		return "selector_required", fmt.Errorf("dynamic group requires selector")
	}
	for key, value := range group.Selector {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return "invalid_selector", fmt.Errorf("selector keys and values are required")
		}
	}
	return "", nil
}

func validateResource(resource *domain.Resource) error {
	if strings.TrimSpace(resource.Name) == "" {
		return fmt.Errorf("name is required")
	}
	switch resource.Type {
	case domain.ResourceNode, domain.ResourceHypervisor, domain.ResourceVM, domain.ResourceContainer, domain.ResourceProcess:
	default:
		return fmt.Errorf("unsupported resource type")
	}
	if resource.Health == "" {
		resource.Health = domain.HealthUnknown
	}
	switch resource.Health {
	case domain.HealthHealthy, domain.HealthWarning, domain.HealthCritical, domain.HealthUnknown, domain.HealthMaintenance:
	default:
		return fmt.Errorf("unsupported health")
	}
	return nil
}

// A named reading is bounded in count and in name, because the map is written
// by whatever the agent sends and lands in every stored sample. A misbehaving
// or compromised agent should cost one rejected sample, not an unbounded row
// repeated every ten seconds.
const (
	metricValueLimit    = 64
	metricNameMaxLength = 48
)

var metricNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func validateMetricSample(sample domain.MetricSample, now time.Time) error {
	for name, value := range map[string]float64{"cpu": sample.CPU, "memory": sample.Memory, "disk": sample.Disk} {
		if value < 0 || value > 100 {
			return fmt.Errorf("%s must be between 0 and 100", name)
		}
	}
	if len(sample.Values) > metricValueLimit {
		return fmt.Errorf("a sample carries %d named readings; at most %d", len(sample.Values), metricValueLimit)
	}
	for name, value := range sample.Values {
		if len(name) > metricNameMaxLength || !metricNamePattern.MatchString(name) {
			return fmt.Errorf("metric name %q is not a lowercase identifier of at most %d characters", name, metricNameMaxLength)
		}
		// A rule compares a number; NaN compares false against everything and
		// an infinity compares true against everything, so neither is a
		// reading anyone can act on.
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("metric %q is not a finite number", name)
		}
	}
	if !sample.Timestamp.IsZero() && (sample.Timestamp.After(now.Add(5*time.Minute)) || sample.Timestamp.Before(now.Add(-24*time.Hour))) {
		return fmt.Errorf("timestamp is outside the accepted clock window")
	}
	return nil
}

func validateRelationTypes(relation domain.Relation, source, target domain.Resource) error {
	if relation.Type == "depends_on" {
		return nil
	}
	rank := map[domain.ResourceType]int{
		domain.ResourceNode: 0, domain.ResourceHypervisor: 1, domain.ResourceVM: 2,
		domain.ResourceContainer: 3, domain.ResourceProcess: 4,
	}
	sourceRank, sourceOK := rank[source.Type]
	targetRank, targetOK := rank[target.Type]
	if !sourceOK || !targetOK || sourceRank >= targetRank {
		return fmt.Errorf("relation must point from a parent resource to a lower hierarchy level")
	}
	if relation.Type == "hosts" && target.Type != domain.ResourceHypervisor && target.Type != domain.ResourceVM {
		return fmt.Errorf("hosts target must be a hypervisor or vm")
	}
	if relation.Type == "runs" && target.Type != domain.ResourceContainer && target.Type != domain.ResourceProcess {
		return fmt.Errorf("runs target must be a container or process")
	}
	return nil
}

func scopeInheritingRelationType(relationType string) bool {
	return relationType == "hosts" || relationType == "runs" || relationType == "contains"
}

func validateRunbook(runbook domain.Runbook) error {
	if runbook.Name == "" || len(runbook.Steps) == 0 {
		return fmt.Errorf("name and steps are required")
	}
	if runbook.Risk != "low" && runbook.Risk != "medium" && runbook.Risk != "high" {
		return fmt.Errorf("risk must be low, medium, or high")
	}
	if len(runbook.Steps) > 20 {
		return fmt.Errorf("runbook supports at most 20 steps")
	}
	for _, step := range runbook.Steps {
		if strings.TrimSpace(step.Name) == "" {
			return fmt.Errorf("step name is required")
		}
		if step.Operation != "inventory.refresh" && step.Operation != "service.status" && step.Operation != "service.restart" {
			return fmt.Errorf("unsupported operation")
		}
		if step.Operation == "service.restart" && runbook.Risk != "high" {
			return fmt.Errorf("service restart requires high risk")
		}
		if err := validateOperationParameters(step.Operation, step.Parameters); err != nil {
			return err
		}
	}
	return nil
}

// logCaptureSources mirrors the agent's allowlist so an unknown source is
// refused before it reaches a host.
var logCaptureSources = map[string]bool{
	"syslog": true, "auth": true, "kernel": true, "journal": true,
	"host": true, "container": true,
}

// logCapturePriorities mirrors the agent's allowlist of severity bands. Empty
// is the source's own range. The live view carries named senders rather than a
// severity range, so a band is something only a read can ask for.
var logCapturePriorities = map[string]bool{
	"": true, "error": true, "warning": true,
	"notice": true, "info": true, "debug": true, "routine": true,
}

func validateOperationParameters(operationType string, parameters map[string]string) error {
	if operationType == "logs.capture" {
		if !logCaptureSources[strings.TrimSpace(parameters["source"])] {
			return fmt.Errorf("unsupported log source")
		}
		// The band becomes a journalctl argument on the node, so it is refused
		// here rather than on the host.
		if !logCapturePriorities[strings.TrimSpace(parameters["priority"])] {
			return fmt.Errorf("unsupported log priority")
		}
		for _, key := range []string{"since", "until"} {
			value := strings.TrimSpace(parameters[key])
			if value == "" {
				continue
			}
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return fmt.Errorf("%s must be an RFC3339 timestamp", key)
			}
		}
		return nil
	}
	if operationType != "service.status" && operationType != "service.restart" {
		return nil
	}
	service := strings.TrimSpace(parameters["service"])
	if service == "" {
		return fmt.Errorf("service parameter is required")
	}
	if len(service) > 128 || strings.ContainsAny(service, "/\\ \t\r\n") {
		return fmt.Errorf("service parameter contains invalid characters")
	}
	return nil
}

func (s *Server) validateExecutionTarget(targetIDs []string) error {
	if len(targetIDs) != 1 {
		return fmt.Errorf("exactly one target is required per execution")
	}
	resource, ok := s.store.Resource(targetIDs[0])
	if !ok || resource.AgentID == "" {
		return fmt.Errorf("target must be managed by an agent")
	}
	return nil
}
