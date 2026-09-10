package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/access"
	"github.com/kloudview/kloudview/apps/server/internal/auth"
	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

type Server struct {
	store                *store.Memory
	access               *access.Engine
	enrollmentKey        string
	credentialKey        string
	webRoot              string
	alertMu              sync.Mutex
	breaches             map[string]time.Time
	activeAlerts         map[string]string
	terminal             *terminalHub
	storageHealth        func(context.Context) error
	metricHistory        func(context.Context, string, time.Time, time.Time) ([]domain.MetricSample, error)
	notificationClient   *http.Client
	overviewCacheMu      sync.RWMutex
	overviewCache        map[string]overviewCacheEntry
	terminalDenyPatterns []*regexp.Regexp
	version              string
	sessions             *auth.SessionStore
	devHeaderAuth        bool
	silentAgents         map[string]time.Time
	releaseDir           string
	releaseVersion       string
	canary               string
	canarySoak           time.Duration
	releases             releaseCache
}

// WithAgentReleases serves agent builds from dir and advertises version as the
// target for connected agents.
func (s *Server) WithAgentReleases(dir, version string) *Server {
	s.releaseDir, s.releaseVersion = dir, version
	return s
}

// WithAgentRollout stages self-update behind one node. An empty canary offers a
// new target to every agent at once.
func (s *Server) WithAgentRollout(canary string, soak time.Duration) *Server {
	s.canary, s.canarySoak = canary, soak
	return s
}

type overviewCacheEntry struct {
	value     map[string]any
	expiresAt time.Time
}

const agentProtocolVersion = "1"

func (s *Server) WithVersion(version string) *Server {
	s.version = version
	return s
}

// WithDevHeaderAuth toggles the X-KloudView-Subject header fallback. When false,
// only a valid session cookie authenticates a human subject (production posture).
func (s *Server) WithDevHeaderAuth(enabled bool) *Server {
	s.devHeaderAuth = enabled
	return s
}

func (s *Server) WithAgentCredentialKey(key string) *Server {
	s.credentialKey = key
	return s
}

func (s *Server) WithStorageHealth(check func(context.Context) error) *Server {
	s.storageHealth = check
	return s
}

// WithMetricHistory supplies the reader for windows older than the in-memory
// one. Without it the metric endpoint only reaches back as far as memory holds.
func (s *Server) WithMetricHistory(read func(context.Context, string, time.Time, time.Time) ([]domain.MetricSample, error)) *Server {
	s.metricHistory = read
	return s
}

func New(memory *store.Memory, enrollmentKey, webRoot string) *Server {
	return NewWithAccess(memory, access.NewEngine(), enrollmentKey, webRoot)
}

func NewWithAccess(memory *store.Memory, accessEngine *access.Engine, enrollmentKey, webRoot string) *Server {
	server := &Server{store: memory, access: accessEngine, enrollmentKey: enrollmentKey, credentialKey: enrollmentKey, webRoot: webRoot, breaches: map[string]time.Time{}, activeAlerts: map[string]string{}, terminal: newTerminalHub(), notificationClient: &http.Client{Timeout: 10 * time.Second}, overviewCache: map[string]overviewCacheEntry{}, terminalDenyPatterns: defaultTerminalDenyPatterns(), sessions: auth.NewSessionStore(24 * time.Hour), devHeaderAuth: true}
	for _, alert := range memory.ListAlerts() {
		if alert.Status == "firing" && alert.RuleID != "" {
			key := alert.RuleID + ":" + alert.ResourceID
			server.activeAlerts[key] = alert.ID
			server.breaches[key] = alert.StartedAt
		}
	}
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerCoreRoutes(mux)
	s.registerIncidentRoutes(mux)
	s.registerExecutionRoutes(mux)
	s.registerAccessRoutes(mux)
	if s.webRoot != "" {
		mux.Handle("/", s.spaHandler())
	}
	return s.requestLog(securityHeaders(cors(mux)))
}

// spaHandler serves the console's static files and falls back to index.html for
// deep links. A missing asset still 404s.
func (s *Server) spaHandler() http.Handler {
	files := http.FileServer(http.Dir(s.webRoot))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			files.ServeHTTP(w, r)
			return
		}
		clean := path.Clean(r.URL.Path)
		if strings.Contains(clean, "..") {
			http.NotFound(w, r)
			return
		}
		// Requests with an extension are assets; the file server answers them.
		if path.Ext(clean) != "" || clean == "/" {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := os.Stat(filepath.Join(s.webRoot, filepath.FromSlash(clean))); err == nil {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(s.webRoot, "index.html"))
	})
}

type enrollmentRequest struct {
	Token        string            `json:"token"`
	Hostname     string            `json:"hostname"`
	Version      string            `json:"version"`
	Protocol     string            `json:"protocolVersion"`
	Capabilities []string          `json:"capabilities"`
	Labels       map[string]string `json:"labels"`
}

func (s *Server) enrollAgent(w http.ResponseWriter, r *http.Request) {
	var input enrollmentRequest
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// A console-issued token is accepted while it is inside its window and its
	// bindings match the presenting host; the configured bootstrap token stays
	// valid for unattended installs.
	issued, issuedOK := s.store.FindEnrollmentToken(input.Token)
	switch {
	case issuedOK && !enrollmentAllowed(issued, input.Hostname, r.RemoteAddr):
		writeError(w, http.StatusForbidden, "token_host_mismatch", "enrollment token is not valid for this host")
		return
	case !issuedOK && (s.enrollmentKey == "" || input.Token != s.enrollmentKey):
		writeError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired enrollment token")
		return
	}
	if input.Hostname == "" {
		writeError(w, http.StatusBadRequest, "hostname_required", "hostname is required")
		return
	}
	if input.Protocol == "" {
		input.Protocol = agentProtocolVersion
	}
	if err := validateAgentMetadata(input.Hostname, input.Version, input.Protocol, input.Capabilities, input.Labels); err != nil {
		if input.Protocol != agentProtocolVersion {
			writeError(w, http.StatusConflict, "unsupported_agent_protocol", "agent protocol version is not supported")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_agent_metadata", err.Error())
		}
		return
	}
	id := stableID("agent", input.Hostname)
	nodeID := stableID("node", input.Hostname)
	if current, ok := s.store.Agent(id); ok && !strings.EqualFold(strings.TrimSpace(current.Hostname), strings.TrimSpace(input.Hostname)) {
		writeError(w, http.StatusConflict, "agent_identity_collision", "hostname resolves to an existing agent identity")
		return
	}
	now := time.Now().UTC()
	agent := s.store.UpsertAgent(domain.Agent{ID: id, NodeID: nodeID, Hostname: input.Hostname, Version: input.Version, Protocol: input.Protocol, Status: "online", Capabilities: input.Capabilities, Labels: input.Labels, LastSeenAt: now})
	s.store.UpsertResource(domain.Resource{ID: nodeID, Name: input.Hostname, Type: domain.ResourceNode, Health: domain.HealthHealthy, AgentID: id, Attributes: map[string]string{"agentVersion": input.Version}, Tags: input.Labels})
	if issuedOK {
		s.store.UseEnrollmentToken(issued.ID)
	}
	credential, err := newCredential()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential_failed", "could not issue an agent credential")
		return
	}
	agent.CredentialHash = hashCredential(credential)
	agent.CredentialIssuedAt = time.Now().UTC()
	agent = s.store.UpsertAgent(agent)
	writeJSON(w, http.StatusCreated, map[string]any{"agent": agent, "credential": credential, "heartbeatIntervalSeconds": 10, "protocolVersion": agentProtocolVersion})
}

func (s *Server) systemInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion":               "v1",
		"serverVersion":            s.version,
		"agentProtocolVersions":    []string{agentProtocolVersion},
		"heartbeatIntervalSeconds": 10,
	})
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, ok := s.store.Agent(id)
	if !ok {
		writeError(w, http.StatusNotFound, "agent_not_found", "agent enrollment required")
		return
	}
	var input domain.Agent
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.NodeID != "" && input.NodeID != current.NodeID || input.Hostname != "" && !strings.EqualFold(strings.TrimSpace(input.Hostname), strings.TrimSpace(current.Hostname)) {
		writeError(w, http.StatusConflict, "agent_identity_mismatch", "heartbeat identity does not match enrollment")
		return
	}
	if input.Protocol == "" {
		input.Protocol = current.Protocol
	}
	if err := validateAgentMetadata(current.Hostname, input.Version, input.Protocol, input.Capabilities, input.Labels); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_agent_metadata", err.Error())
		return
	}
	// The soak clock starts when the version changes, not on every heartbeat.
	if current.Version != input.Version || current.VersionSince.IsZero() {
		current.VersionSince = time.Now().UTC()
	}
	current.Version = input.Version
	current.Protocol = input.Protocol
	current.Capabilities = input.Capabilities
	current.Labels = input.Labels
	current.Status = "online"
	current.LastSeenAt = time.Now().UTC()
	updated := s.store.UpsertAgent(current)
	if resource, exists := s.store.Resource(current.NodeID); exists {
		if resource.Attributes == nil {
			resource.Attributes = map[string]string{}
		}
		if resource.Tags == nil {
			resource.Tags = map[string]string{}
		}
		resource.Attributes["agentVersion"] = current.Version
		for key, value := range current.Labels {
			resource.Tags[key] = value
		}
		s.store.UpsertResource(resource)
	}
	// The agent compares targetVersion with its own and updates itself when the
	// two differ and self-update is enabled on the host.
	manifest := s.agentReleases()
	// Staged: the canary is offered the new build first, and everyone else is
	// offered the version they already run until it has soaked.
	state := s.rollout(time.Now().UTC())
	match := s.matchAgentCredential(updated, updated.ID, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	rotated, credential := s.rotateAgentCredential(updated, match)
	response := map[string]any{
		"agent":         rotated,
		"targetVersion": targetVersionFor(updated, state),
		"releases":      manifest.Releases,
	}
	// Present only when a rotation happened; an older agent ignores it and keeps
	// using the credential it holds, which stays valid until it uses the new one.
	if credential != "" {
		response["credential"] = credential
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	agents := s.store.ListAgents()
	visible := agents[:0]
	for index := range agents {
		if !s.authorizeResourceTarget(r, "resources", "read", agents[index].NodeID) {
			continue
		}
		if !agentOnline(agents[index], time.Now().UTC()) {
			agents[index].Status = "offline"
		}
		visible = append(visible, agents[index])
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": visible})
}

func agentOnline(agent domain.Agent, now time.Time) bool {
	return !agent.LastSeenAt.IsZero() && !agent.LastSeenAt.Before(now.Add(-30*time.Second))
}

func (s *Server) onlineAgentIDs(now time.Time) map[string]bool {
	online := map[string]bool{}
	for _, agent := range s.store.ListAgents() {
		if agentOnline(agent, now) {
			online[agent.ID] = true
		}
	}
	return online
}

func (s *Server) currentMetricResourceIDs(r *http.Request, now time.Time) map[string]bool {
	return s.currentMetricResourceIDsWith(r, now, s.store.ResourceScopes())
}

func (s *Server) currentMetricResourceIDsWith(r *http.Request, now time.Time, scopes map[string]store.ResourceScope) map[string]bool {
	visible := s.visibleResourceIDsWith(r, "metrics", scopes)
	online := s.onlineAgentIDs(now)
	for _, resource := range s.store.ListLiveResources() {
		if resource.AgentID != "" && !online[resource.AgentID] {
			delete(visible, resource.ID)
		}
	}
	return visible
}

func effectiveResourceHealth(resource domain.Resource, alertHealth map[string]domain.Health, onlineAgents map[string]bool) (domain.Health, bool) {
	metricsStale := resource.AgentID != "" && !onlineAgents[resource.AgentID]
	if health, ok := alertHealth[resource.ID]; ok {
		return health, metricsStale
	}
	if metricsStale || resource.Health == "" {
		return domain.HealthUnknown, metricsStale
	}
	return resource.Health, false
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.store.Agent(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	if !s.authorizeResourceTarget(r, "agents", "delete", agent.NodeID) {
		writeError(w, http.StatusForbidden, "access_denied", "agent scope is not assigned")
		return
	}
	if err := s.store.DeleteAgent(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) putInventory(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.store.Agent(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "agent_not_found", "agent enrollment required")
		return
	}
	var data map[string]any
	if err := readJSONNumbers(r, &data, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	inventory := domain.AgentInventory{AgentID: agent.ID, NodeID: agent.NodeID, Data: data, ObservedAt: time.Now().UTC()}
	s.reconcileInventory(inventory)
	writeJSON(w, http.StatusOK, s.store.PutInventory(inventory))
}

func (s *Server) getInventory(w http.ResponseWriter, r *http.Request) {
	inventory, ok := s.store.Inventory(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "inventory not found")
		return
	}
	if !s.authorizeResourceTarget(r, "resources", "read", inventory.NodeID) {
		writeError(w, http.StatusForbidden, "access_denied", "resource scope is not assigned")
		return
	}
	writeJSON(w, http.StatusOK, inventory)
}
func (s *Server) listInventories(w http.ResponseWriter, r *http.Request) {
	items := []domain.AgentInventory{}
	for _, inventory := range s.store.Inventories() {
		if s.authorizeResourceTarget(r, "resources", "read", inventory.NodeID) {
			items = append(items, inventory)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) reconcileInventory(inventory domain.AgentInventory) {
	observed := inventory.ObservedAt
	if observed.IsZero() {
		observed = time.Now().UTC()
	}
	retained := map[string]bool{}
	for _, kind := range []struct {
		key          string
		resourceType domain.ResourceType
		idField      string
		relationType string
	}{{"vms", domain.ResourceVM, "name", "hosts"}, {"containers", domain.ResourceContainer, "id", "runs"}, {"processes", domain.ResourceProcess, "pid", "runs"}} {
		values, ok := inventory.Data[kind.key].([]any)
		if !ok {
			continue
		}
		for _, raw := range values {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			// Same normalization as the attribute map, or the resource ID slug
			// and the pid attribute would disagree for the same process.
			identity := attributeString(item[kind.idField])
			if identity == "<nil>" {
				identity = ""
			}
			name, _ := item["name"].(string)
			if identity == "" {
				identity = name
			}
			if identity == "" {
				continue
			}
			resourceID := stableID(string(kind.resourceType), inventory.NodeID+"-"+identity)
			retained[resourceID] = true
			attributes := inventoryAttributes(item)
			attributes["discoveredBy"] = inventory.AgentID
			// Inventory runs on a slower cycle than cgroup readings, so keys
			// the metric path owns are carried over instead of being erased
			// every five minutes.
			if previous, found := s.store.Resource(resourceID); found {
				for _, key := range metricOwnedAttributes {
					if value, ok := previous.Attributes[key]; ok {
						attributes[key] = value
					}
				}
			}
			s.store.UpsertResource(domain.Resource{ID: resourceID, Name: name, Type: kind.resourceType, Health: inventoryHealth(item), AgentID: inventory.AgentID, Attributes: attributes, LastSeenAt: observed})
			s.store.PutRelation(domain.Relation{ID: stableID("relation", inventory.NodeID+"-"+kind.relationType+"-"+resourceID), SourceID: inventory.NodeID, TargetID: resourceID, Type: kind.relationType})
		}
	}
	s.store.PruneAgentResources(inventory.AgentID, inventory.NodeID, retained)
	if node, found := s.store.Resource(inventory.NodeID); found {
		node.Type = domain.ResourceNode
		if values, ok := inventory.Data["vms"].([]any); ok && len(values) > 0 {
			node.Type = domain.ResourceHypervisor
		}
		if node.Attributes == nil {
			node.Attributes = map[string]string{}
		}
		primary, all := nodeAddresses(inventory.Data["interfaces"])
		if primary != "" {
			node.Attributes["address"] = primary
		}
		if all != "" {
			node.Attributes["addresses"] = all
		}
		if hostname, ok := inventory.Data["hostname"].(string); ok && hostname != "" {
			node.Attributes["hostname"] = hostname
		}
		s.store.UpsertResource(node)
	}
}

// virtualInterfaces are host-side ends of container and VM networking. Their
// addresses belong to the bridge, not to the node an operator connects to, so
// they are excluded from the address a node is identified by.
var virtualInterfaces = []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "kube", "tun", "tap"}

// nodeAddresses picks the address a node is reached on and lists the rest.
// The agent reports every interface in CIDR form; loopback and link-local
// identify no host, and the first global IPv4 on a physical interface is what
// an operator recognises the machine by.
func nodeAddresses(raw any) (primary, all string) {
	values, ok := raw.([]any)
	if !ok {
		return "", ""
	}
	collected := []string{}
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, _ := item["name"].(string)
		virtual := false
		for _, prefix := range virtualInterfaces {
			if strings.HasPrefix(name, prefix) {
				virtual = true
				break
			}
		}
		addresses, ok := item["addresses"].([]any)
		if !ok {
			continue
		}
		for _, entry := range addresses {
			text, _ := entry.(string)
			ip, _, err := net.ParseCIDR(text)
			if err != nil {
				if ip = net.ParseIP(text); ip == nil {
					continue
				}
			}
			if !ip.IsGlobalUnicast() {
				continue
			}
			collected = append(collected, ip.String())
			if primary == "" && !virtual && ip.To4() != nil {
				primary = ip.String()
			}
		}
	}
	// A node reachable only over IPv6, or only through a bridge, still needs an
	// address rather than none.
	if primary == "" && len(collected) > 0 {
		primary = collected[0]
	}
	return primary, strings.Join(collected, ", ")
}

// supportedOperationTypes are the operations an agent executor implements. Must
// match docs/contracts/agent-server.json: a type accepted here that the agent
// cannot run is claimed, sent to a host and failed there, and one missing here
// is a console button that always returns 400.
var supportedOperationTypes = map[string]bool{
	"inventory.refresh": true,
	"service.status":    true,
	"service.restart":   true,
	"logs.capture":      true,
}

// attentionLimit caps the unhealthy resources the overview names outright. The
// full count travels alongside as attentionTotal.
const attentionLimit = 8

// metricOwnedAttributes are written by the container metric path, not by the
// inventory that creates the resource.
var metricOwnedAttributes = []string{"memoryBytes", "memoryLimitBytes", "diskReadBytes", "diskWriteBytes", "processes"}

func inventoryAttributes(item map[string]any) map[string]string {
	attributes := map[string]string{}
	for key, value := range item {
		if value != nil {
			attributes[key] = attributeString(value)
		}
	}
	return attributes
}

// attributeString renders an inventory value for storage. The PUT path decodes
// numbers as json.Number, but inventory reloaded from the database comes back
// through float64, where fmt.Sprint switches to scientific notation at 1e6.
// A whole number is written in full so a PID or byte count reads as itself.
func attributeString(value any) string {
	if f, ok := value.(float64); ok && f == math.Trunc(f) && math.Abs(f) < 1e18 {
		return strconv.FormatInt(int64(f), 10)
	}
	return fmt.Sprint(value)
}

// healthReason answers why a resource is on the attention list, in the order
// effectiveResourceHealth decides it: a firing alert, then a stale agent, then
// the state the inventory last reported.
func healthReason(resource domain.Resource, alertHealth map[string]domain.Health, metricsStale bool) string {
	if _, ok := alertHealth[resource.ID]; ok {
		return "active alert"
	}
	if metricsStale {
		return "agent not reporting"
	}
	// The state string is what inventoryHealth graded, so it is the reason.
	if state := strings.TrimSpace(resource.Attributes["state"]); state != "" {
		return state
	}
	return ""
}

// inventoryHealth grades a reported state. A zombie is a child the parent has
// not reaped yet, ordinary on Linux and self-clearing, so it is a warning;
// critical is reserved for a runtime reporting a workload as dead.
func inventoryHealth(item map[string]any) domain.Health {
	state := strings.ToLower(fmt.Sprint(item["state"]))
	if strings.Contains(state, "zombie") || strings.HasPrefix(state, "z ") {
		return domain.HealthWarning
	}
	if strings.Contains(state, "dead") {
		return domain.HealthCritical
	}
	if strings.Contains(state, "stopped") || strings.Contains(state, "shut") || strings.Contains(state, "exited") || strings.Contains(state, "paused") {
		return domain.HealthWarning
	}
	return domain.HealthHealthy
}
func (s *Server) listResources(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 {
		limit = min(value, 1000)
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	resourceType := r.URL.Query().Get("type")
	health := r.URL.Query().Get("health")
	lifetime, err := parseLifetimeFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_time", err.Error())
		return
	}
	alertHealth := s.activeAlertHealth()
	onlineAgents := s.onlineAgentIDs(time.Now().UTC())
	allowed := s.resourceAuthorizer(r, "resources", "read")
	filtered := make([]domain.Resource, 0)
	for _, resource := range s.store.ListResources() {
		if !allowed(resource.ID) {
			continue
		}
		if !lifetime.match(resource) {
			continue
		}
		resource.Health, _ = effectiveResourceHealth(resource, alertHealth, onlineAgents)
		searchable := strings.ToLower(resource.Name + " " + resource.ID + " " + string(resource.Type))
		for key, value := range resource.Tags {
			searchable += " " + strings.ToLower(key+"="+value+" "+key+" "+value)
		}
		if query != "" && !strings.Contains(searchable, query) {
			continue
		}
		if resourceType != "" && string(resource.Type) != resourceType {
			continue
		}
		if health != "" && string(resource.Health) != health {
			continue
		}
		filtered = append(filtered, resource)
	}
	total := len(filtered)
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		parts, valid := decodeCursor(cursor, 2)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
			return
		}
		offset = sort.Search(len(filtered), func(index int) bool {
			return filtered[index].Name > parts[0] || filtered[index].Name == parts[0] && filtered[index].ID > parts[1]
		})
	}
	if offset > total {
		offset = total
	}
	end := min(offset+limit, total)
	response := map[string]any{"items": filtered[offset:end], "total": total, "limit": limit, "offset": offset}
	if end < total {
		response["nextOffset"] = end
		last := filtered[end-1]
		response["nextCursor"] = encodeCursor(last.Name, last.ID)
	}
	writeJSON(w, http.StatusOK, response)
}

// lifetimeFilter selects resources by when they existed. `lifecycle` is live
// (default), terminated, or all; `at` answers what was running at a past moment.
type lifetimeFilter struct {
	at        *time.Time
	lifecycle string
}

func parseLifetimeFilter(r *http.Request) (lifetimeFilter, error) {
	filter := lifetimeFilter{lifecycle: r.URL.Query().Get("lifecycle")}
	switch filter.lifecycle {
	case "", "live", "terminated", "all":
	default:
		return filter, errors.New("lifecycle must be live, terminated, or all")
	}
	raw := strings.TrimSpace(r.URL.Query().Get("at"))
	if raw == "" {
		return filter, nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return filter, errors.New("at must be an RFC3339 timestamp")
	}
	at = at.UTC()
	filter.at = &at
	return filter, nil
}

func (f lifetimeFilter) match(resource domain.Resource) bool {
	if f.at != nil {
		if !resource.CreatedAt.IsZero() && resource.CreatedAt.After(*f.at) {
			return false
		}
		return resource.TerminatedAt == nil || resource.TerminatedAt.After(*f.at)
	}
	switch f.lifecycle {
	case "terminated":
		return resource.TerminatedAt != nil
	case "all":
		return true
	default:
		return resource.TerminatedAt == nil
	}
}

func (s *Server) getResource(w http.ResponseWriter, r *http.Request) {
	resource, ok := s.store.Resource(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if !s.authorizeResourceTarget(r, "resources", "read", resource.ID) {
		writeError(w, http.StatusForbidden, "access_denied", "resource scope is not assigned")
		return
	}
	resource.Health, _ = effectiveResourceHealth(resource, s.activeAlertHealth(), s.onlineAgentIDs(time.Now().UTC()))
	writeJSON(w, http.StatusOK, resource)
}

func (s *Server) createResource(w http.ResponseWriter, r *http.Request) {
	var resource domain.Resource
	if err := readJSON(r, &resource); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateResource(&resource); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_resource", err.Error())
		return
	}
	resource.ID = fmt.Sprintf("resource-%d", time.Now().UnixNano())
	resource.AgentID = ""
	writeJSON(w, http.StatusCreated, s.store.UpsertResource(resource))
}

func (s *Server) updateResource(w http.ResponseWriter, r *http.Request) {
	current, ok := s.store.Resource(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if !s.authorizeResourceTarget(r, "resources", "update", current.ID) {
		writeError(w, http.StatusForbidden, "access_denied", "resource scope is not assigned")
		return
	}
	var resource domain.Resource
	if err := readJSON(r, &resource); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateResource(&resource); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_resource", err.Error())
		return
	}
	resource.ID = current.ID
	resource.AgentID = current.AgentID
	writeJSON(w, http.StatusOK, s.store.UpsertResource(resource))
}

func (s *Server) deleteResource(w http.ResponseWriter, r *http.Request) {
	resource, ok := s.store.Resource(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if !s.authorizeResourceTarget(r, "resources", "delete", resource.ID) {
		writeError(w, http.StatusForbidden, "access_denied", "resource scope is not assigned")
		return
	}
	if resource.AgentID != "" {
		writeError(w, http.StatusConflict, "agent_managed_resource", "remove the owning agent from Agent Fleet")
		return
	}
	if err := s.store.DeleteResource(resource.ID); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type overviewGroup struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Path     string `json:"path,omitempty"`
	Total    int    `json:"total"`
	Healthy  int    `json:"healthy"`
	Warning  int    `json:"warning"`
	Critical int    `json:"critical"`
	Unknown  int    `json:"unknown"`
}

type overviewCell struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	Type    domain.ResourceType `json:"type"`
	Health  domain.Health       `json:"health"`
	GroupID string              `json:"groupId,omitempty"`
	CPU     float64             `json:"cpu"`
	Memory  float64             `json:"memory"`
	Disk    float64             `json:"disk"`
	Network float64             `json:"network"`
	// Split rates; Network is their sum, kept for the single-value views.
	NetworkRx float64 `json:"networkRx"`
	NetworkTx float64 `json:"networkTx"`
	Stale     bool    `json:"metricsStale,omitempty"`
	// Set only on the attention list. Health alone says a resource needs
	// looking at; these say why, and on which host.
	Reason string `json:"reason,omitempty"`
	Host   string `json:"host,omitempty"`
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	cacheKey := s.subjectFromRequest(r) + "\x00" + r.Header.Get("X-KloudView-Scope") + "\x00" + r.URL.RawQuery
	s.overviewCacheMu.RLock()
	cached, found := s.overviewCache[cacheKey]
	s.overviewCacheMu.RUnlock()
	if found && time.Now().Before(cached.expiresAt) {
		w.Header().Set("X-KloudView-Cache", "hit")
		writeJSON(w, http.StatusOK, cached.value)
		return
	}
	// Scopes for the whole fleet resolve in one pass; asking per resource rescans
	// every membership and relation each time, quadratic in resource count.
	subject := s.subjectFromRequest(r)
	scopes := s.store.ResourceScopes()
	resources := []domain.Resource{}
	for _, resource := range s.store.ListLiveResources() {
		scope, known := scopes[resource.ID]
		if s.authorizeWithContext(subject, "resources", "read", scope.AccessContext, known) {
			resources = append(resources, resource)
		}
	}
	groups := s.store.ListGroups()
	groupType := r.URL.Query().Get("groupType")
	selectedGroup := r.URL.Query().Get("groupId")
	selectedHealth := r.URL.Query().Get("health")
	// The heatmap shows one kind at a time. Without this the cell cap is
	// reached by whatever is most numerous -- processes -- and the nodes the
	// caller asked for never reach the response.
	selectedTypes := map[string]bool{}
	for _, value := range strings.Split(r.URL.Query().Get("types"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			selectedTypes[value] = true
		}
	}
	anomaliesOnly := r.URL.Query().Get("anomalies") == "true"
	cellLimit := 1000
	if value, err := strconv.Atoi(r.URL.Query().Get("cellLimit")); err == nil && value > 0 {
		cellLimit = min(value, 5000)
	}
	groupIndex := map[string]*overviewGroup{}
	for _, group := range groups {
		if groupType != "" && group.Type != groupType {
			continue
		}
		if !s.authorizeGroupTarget(r, "resources", "read", group.ID) {
			continue
		}
		groupIndex[group.ID] = &overviewGroup{ID: group.ID, Name: group.Name, Type: group.Type, Path: group.Path}
	}
	resourceGroupSets := map[string]map[string]bool{}
	for _, resource := range resources {
		for _, groupID := range scopes[resource.ID].GroupIDs {
			if _, ok := groupIndex[groupID]; !ok {
				continue
			}
			if resourceGroupSets[resource.ID] == nil {
				resourceGroupSets[resource.ID] = map[string]bool{}
			}
			resourceGroupSets[resource.ID][groupID] = true
		}
	}
	resourceGroups := map[string][]string{}
	for resourceID, groupSet := range resourceGroupSets {
		for groupID := range groupSet {
			resourceGroups[resourceID] = append(resourceGroups[resourceID], groupID)
		}
		sort.Strings(resourceGroups[resourceID])
	}
	health := s.activeAlertHealth()
	onlineAgents := s.onlineAgentIDs(time.Now().UTC())
	latestMetrics := s.store.LatestMetrics()
	networkRates := s.store.NetworkRates()
	cells := make([]overviewCell, 0, len(resources))
	// Collected separately from cells: cells carry the caller's display filters,
	// while this answers "what needs looking at" across the whole authorized set.
	attention := make([]overviewCell, 0, attentionLimit)
	counts := map[string]int{}
	for _, resource := range resources {
		resourceHealth, metricsStale := effectiveResourceHealth(resource, health, onlineAgents)
		counts[string(resourceHealth)]++
		metric := latestMetrics[resource.ID]
		network := networkRates[resource.ID]
		groupIDs := resourceGroups[resource.ID]
		groupID := ""
		if len(groupIDs) > 0 {
			groupID = groupIDs[0]
		}
		included := selectedGroup == "" || slices.Contains(groupIDs, selectedGroup)
		included = included && (len(selectedTypes) == 0 || selectedTypes[string(resource.Type)])
		included = included && (selectedHealth == "" || string(resourceHealth) == selectedHealth)
		included = included && (!anomaliesOnly || (resourceHealth != domain.HealthHealthy && resourceHealth != domain.HealthMaintenance))
		cell := overviewCell{ID: resource.ID, Name: resource.Name, Type: resource.Type, Health: resourceHealth, GroupID: groupID, CPU: metric.CPU, Memory: metric.Memory, Disk: metric.Disk, Network: network.Rx + network.Tx, NetworkRx: network.Rx, NetworkTx: network.Tx, Stale: metricsStale}
		if resourceHealth != domain.HealthHealthy && resourceHealth != domain.HealthMaintenance {
			attention = append(attention, cell)
		}
		if included {
			if selectedGroup != "" {
				cell.GroupID = selectedGroup
			}
			cells = append(cells, cell)
		}
		for _, id := range groupIDs {
			group := groupIndex[id]
			group.Total++
			switch resourceHealth {
			case domain.HealthHealthy:
				group.Healthy++
			case domain.HealthWarning:
				group.Warning++
			case domain.HealthCritical:
				group.Critical++
			default:
				group.Unknown++
			}
		}
	}
	groupItems := make([]overviewGroup, 0, len(groupIndex))
	for _, group := range groupIndex {
		groupItems = append(groupItems, *group)
	}
	sort.Slice(groupItems, func(i, j int) bool { return groupItems[i].Name < groupItems[j].Name })
	priority := map[domain.Health]int{domain.HealthCritical: 0, domain.HealthWarning: 1, domain.HealthUnknown: 2, domain.HealthMaintenance: 3, domain.HealthHealthy: 4}
	sort.Slice(cells, func(i, j int) bool {
		if priority[cells[i].Health] != priority[cells[j].Health] {
			return priority[cells[i].Health] < priority[cells[j].Health]
		}
		return cells[i].Name < cells[j].Name
	})
	cellTotal := len(cells)
	if len(cells) > cellLimit {
		cells = cells[:cellLimit]
	}
	sort.Slice(attention, func(i, j int) bool {
		if priority[attention[i].Health] != priority[attention[j].Health] {
			return priority[attention[i].Health] < priority[attention[j].Health]
		}
		return attention[i].Name < attention[j].Name
	})
	attentionTotal := len(attention)
	if len(attention) > attentionLimit {
		attention = attention[:attentionLimit]
	}
	// Only the entries that survive the cap are enriched, so the lookups are
	// bounded however large the fleet's unhealthy set is.
	for i := range attention {
		resource, ok := s.store.Resource(attention[i].ID)
		if !ok {
			continue
		}
		attention[i].Reason = healthReason(resource, health, attention[i].Stale)
		if agent, found := s.store.Agent(resource.AgentID); found {
			attention[i].Host = agent.Hostname
		}
	}
	activeIncidents := 0
	incidentAllowed := s.resourceAuthorizerWith(r, "incidents", "read", scopes)
	for _, incident := range s.store.ListIncidents() {
		if incident.Status != "resolved" && s.anyTargetAllowed(r, "incidents", "read", incident.ResourceIDs, incidentAllowed) {
			activeIncidents++
		}
	}
	response := map[string]any{"total": len(resources), "healthy": counts[string(domain.HealthHealthy)], "warning": counts[string(domain.HealthWarning)], "critical": counts[string(domain.HealthCritical)], "unknown": counts[string(domain.HealthUnknown)], "activeIncidents": activeIncidents, "groups": groupItems, "cells": cells, "cellTotal": cellTotal, "cellLimit": cellLimit, "cellsTruncated": cellTotal > len(cells), "attention": attention, "attentionTotal": attentionTotal, "metrics": s.store.MetricSummaryFor("", s.currentMetricResourceIDsWith(r, time.Now().UTC(), scopes))}
	s.overviewCacheMu.Lock()
	if len(s.overviewCache) > 256 {
		s.overviewCache = map[string]overviewCacheEntry{}
	}
	s.overviewCache[cacheKey] = overviewCacheEntry{value: response, expiresAt: time.Now().Add(2 * time.Second)}
	s.overviewCacheMu.Unlock()
	w.Header().Set("X-KloudView-Cache", "miss")
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) activeAlertHealth() map[string]domain.Health {
	health := map[string]domain.Health{}
	for _, alert := range s.store.ListAlerts() {
		if alert.Status == "resolved" || alert.Status == "silenced" {
			continue
		}
		candidate := domain.HealthWarning
		if alert.Severity == "critical" {
			candidate = domain.HealthCritical
		}
		if health[alert.ResourceID] != domain.HealthCritical {
			health[alert.ResourceID] = candidate
		}
	}
	return health
}
func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	items := []domain.Group{}
	for _, group := range s.store.ListGroups() {
		if s.authorizeGroupTarget(r, "groups", "read", group.ID) {
			items = append(items, group)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var group domain.Group
	if err := readJSON(r, &group); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if code, err := validateGroup(&group); err != nil {
		writeError(w, http.StatusBadRequest, code, err.Error())
		return
	}
	if group.ParentID == "" && !s.authorizeConfiguredScope(r, "groups", "create", group.Path, group.Tags) {
		writeError(w, http.StatusForbidden, "access_denied", "group scope is not assigned")
		return
	}
	if group.ParentID != "" {
		if _, ok := s.store.Group(group.ParentID); !ok {
			writeError(w, http.StatusBadRequest, "parent_not_found", "parent group not found")
			return
		}
		if !s.authorizeGroupTarget(r, "groups", "create", group.ParentID) {
			writeError(w, http.StatusForbidden, "access_denied", "parent group scope is not assigned")
			return
		}
	}
	group.ID = fmt.Sprintf("grp-%d", time.Now().UnixNano())
	writeJSON(w, http.StatusCreated, s.store.PutGroup(group))
}

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	var group domain.Group
	if err := readJSON(r, &group); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	group.ID = r.PathValue("id")
	if _, ok := s.store.Group(group.ID); !ok {
		writeError(w, http.StatusNotFound, "not_found", "group not found")
		return
	}
	if !s.authorizeGroupTarget(r, "groups", "update", group.ID) {
		writeError(w, http.StatusForbidden, "access_denied", "group scope is not assigned")
		return
	}
	if code, err := validateGroup(&group); err != nil {
		writeError(w, http.StatusBadRequest, code, err.Error())
		return
	}
	if group.ParentID != "" {
		if _, ok := s.store.Group(group.ParentID); !ok {
			writeError(w, http.StatusBadRequest, "parent_not_found", "parent group not found")
			return
		}
		if !s.authorizeGroupTarget(r, "groups", "update", group.ParentID) {
			writeError(w, http.StatusForbidden, "access_denied", "parent group scope is not assigned")
			return
		}
	} else if !s.authorizeConfiguredScope(r, "groups", "update", group.Path, group.Tags) {
		writeError(w, http.StatusForbidden, "access_denied", "group scope is not assigned")
		return
	}
	if s.store.WouldCreateGroupCycle(group.ID, group.ParentID) {
		writeError(w, http.StatusConflict, "hierarchy_cycle", "parent would create a hierarchy cycle")
		return
	}
	writeJSON(w, http.StatusOK, s.store.PutGroup(group))
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.store.Group(r.PathValue("id")); !ok {
		writeError(w, http.StatusNotFound, "not_found", "group not found")
		return
	}
	if !s.authorizeGroupTarget(r, "groups", "delete", r.PathValue("id")) {
		writeError(w, http.StatusForbidden, "access_denied", "group scope is not assigned")
		return
	}
	if s.store.HasChildGroups(r.PathValue("id")) {
		writeError(w, http.StatusConflict, "group_not_empty", "move or delete child groups first")
		return
	}
	if err := s.store.DeleteGroup(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "group not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createMembership(w http.ResponseWriter, r *http.Request) {
	var member domain.GroupMembership
	if err := readJSON(r, &member); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if member.GroupID == "" || member.ResourceID == "" {
		writeError(w, http.StatusBadRequest, "fields_required", "groupId and resourceId are required")
		return
	}
	group, ok := s.store.Group(member.GroupID)
	if !ok {
		writeError(w, http.StatusBadRequest, "group_not_found", "group not found")
		return
	}
	if group.Mode == "dynamic" {
		writeError(w, http.StatusConflict, "dynamic_group", "dynamic group membership is selector-managed")
		return
	}
	if !s.store.HasResource(member.ResourceID) {
		writeError(w, http.StatusBadRequest, "resource_not_found", "resource not found")
		return
	}
	if !s.authorizeGroupTarget(r, "groups", "update", member.GroupID) || !s.authorizeResourceTarget(r, "groups", "update", member.ResourceID) {
		writeError(w, http.StatusForbidden, "access_denied", "membership scope is not assigned")
		return
	}
	member.ID = stableID("membership", member.GroupID+"-"+member.ResourceID)
	writeJSON(w, http.StatusCreated, s.store.PutMembership(member))
}

func (s *Server) listMemberships(w http.ResponseWriter, r *http.Request) {
	allowed := s.resourceAuthorizer(r, "groups", "read")
	items := []domain.GroupMembership{}
	for _, membership := range s.store.ListMemberships(r.URL.Query().Get("groupId")) {
		if allowed(membership.ResourceID) && s.authorizeGroupTarget(r, "groups", "read", membership.GroupID) {
			items = append(items, membership)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) deleteMembership(w http.ResponseWriter, r *http.Request) {
	membership, ok := s.store.Membership(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "membership not found")
		return
	}
	if !s.authorizeGroupTarget(r, "groups", "update", membership.GroupID) || !s.authorizeResourceTarget(r, "groups", "update", membership.ResourceID) {
		writeError(w, http.StatusForbidden, "access_denied", "membership scope is not assigned")
		return
	}
	if err := s.store.DeleteMembership(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "membership not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createRelation(w http.ResponseWriter, r *http.Request) {
	var relation domain.Relation
	if err := readJSON(r, &relation); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if relation.SourceID == "" || relation.TargetID == "" || relation.Type == "" {
		writeError(w, http.StatusBadRequest, "fields_required", "sourceId, targetId and type are required")
		return
	}
	if relation.SourceID == relation.TargetID {
		writeError(w, http.StatusBadRequest, "self_relation", "relation source and target must differ")
		return
	}
	if !map[string]bool{"hosts": true, "runs": true, "contains": true, "depends_on": true}[relation.Type] {
		writeError(w, http.StatusBadRequest, "invalid_relation_type", "unsupported relation type")
		return
	}
	source, sourceOK := s.store.Resource(relation.SourceID)
	target, targetOK := s.store.Resource(relation.TargetID)
	if !sourceOK || !targetOK {
		writeError(w, http.StatusBadRequest, "resource_not_found", "relation source and target resources must exist")
		return
	}
	if err := validateRelationTypes(relation, source, target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_relation_hierarchy", err.Error())
		return
	}
	if scopeInheritingRelationType(relation.Type) && s.store.WouldCreateRelationCycle(relation.SourceID, relation.TargetID) {
		writeError(w, http.StatusConflict, "relation_cycle", "relation would create a hierarchy cycle")
		return
	}
	if !s.authorizeEntityTarget(r, "relations", "create", relation.SourceID) || !s.authorizeEntityTarget(r, "relations", "create", relation.TargetID) {
		writeError(w, http.StatusForbidden, "access_denied", "relation scope is not assigned")
		return
	}
	relation.ID = stableID("relation", relation.SourceID+"-"+relation.Type+"-"+relation.TargetID)
	writeJSON(w, http.StatusCreated, s.store.PutRelation(relation))
}

func (s *Server) listRelations(w http.ResponseWriter, r *http.Request) {
	allowed := s.entityAuthorizer(r, "relations", "read")
	items := []domain.Relation{}
	for _, relation := range s.store.ListRelations() {
		if allowed(relation.SourceID) && allowed(relation.TargetID) {
			items = append(items, relation)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) deleteRelation(w http.ResponseWriter, r *http.Request) {
	relation, ok := s.store.Relation(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "relation not found")
		return
	}
	if !s.authorizeEntityTarget(r, "relations", "delete", relation.SourceID) || !s.authorizeEntityTarget(r, "relations", "delete", relation.TargetID) {
		writeError(w, http.StatusForbidden, "access_denied", "relation scope is not assigned")
		return
	}
	if err := s.store.DeleteRelation(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "relation not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ingestMetric(w http.ResponseWriter, r *http.Request) {
	var sample domain.MetricSample
	if err := readJSON(r, &sample); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if sample.ResourceID == "" {
		writeError(w, http.StatusBadRequest, "resource_required", "resourceId is required")
		return
	}
	resource, ok := s.store.Resource(sample.ResourceID)
	if !ok {
		writeError(w, http.StatusNotFound, "resource_not_found", "resource is not registered")
		return
	}
	if resource.AgentID != r.PathValue("id") {
		writeError(w, http.StatusForbidden, "resource_ownership_mismatch", "resource is managed by another agent")
		return
	}
	if err := validateMetricSample(sample, time.Now().UTC()); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_metric", err.Error())
		return
	}
	if sample.Timestamp.IsZero() {
		sample.Timestamp = time.Now().UTC()
	}
	s.store.AddMetric(sample)
	s.evaluateRules(sample, s.store.NetworkRate(sample.ResourceID))
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) evaluateRules(sample domain.MetricSample, network domain.NetworkRate) {
	s.alertMu.Lock()
	defer s.alertMu.Unlock()
	now := time.Now().UTC()
	for _, rule := range s.store.ListAlertRules() {
		if !rule.Enabled {
			continue
		}
		if !s.store.ResourceMatchesScope(sample.ResourceID, rule.ScopePath, rule.Selector) {
			continue
		}
		if s.alertSilenced(sample.ResourceID, now) {
			continue
		}
		value, ok := metricValue(rule.Metric, sample, network)
		if !ok {
			continue
		}
		key := rule.ID + ":" + sample.ResourceID
		breached := compareMetric(value, rule.Operator, rule.Threshold)
		if !breached {
			delete(s.breaches, key)
			if alertID, exists := s.activeAlerts[key]; exists {
				if alert, found := s.store.Alert(alertID); found {
					alert.Status = "resolved"
					alert = s.store.PutAlert(alert)
					s.processAlertEvent(alert, "resolved")
				}
				delete(s.activeAlerts, key)
			}
			continue
		}
		started, exists := s.breaches[key]
		if !exists {
			s.breaches[key] = now
			started = now
		}
		duration, err := time.ParseDuration(rule.Duration)
		if err != nil {
			continue
		}
		if now.Sub(started) < duration {
			continue
		}
		if _, exists := s.activeAlerts[key]; exists {
			continue
		}
		alert := domain.Alert{ID: fmt.Sprintf("alert-%d", time.Now().UnixNano()), RuleID: rule.ID, Name: rule.Name, Severity: rule.Severity, Status: "firing", ResourceID: sample.ResourceID, Summary: fmt.Sprintf("%s %s %.2f, observed %.2f", rule.Metric, rule.Operator, rule.Threshold, value), StartedAt: started}
		alert = s.store.PutAlert(alert)
		s.activeAlerts[key] = alert.ID
		s.processAlertEvent(alert, "firing")
	}
}

func metricValue(metric string, sample domain.MetricSample, network domain.NetworkRate) (float64, bool) {
	switch metric {
	case "cpu":
		return sample.CPU, true
	case "memory":
		return sample.Memory, true
	case "disk":
		return sample.Disk, true
	case "network_rx_rate":
		return network.Rx, true
	case "network_tx_rate":
		return network.Tx, true
	default:
		return 0, false
	}
}
func compareMetric(value float64, operator string, threshold float64) bool {
	switch operator {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	default:
		return false
	}
}

func (s *Server) listMetrics(w http.ResponseWriter, r *http.Request) {
	resourceID := r.PathValue("id")
	if !s.authorizeResourceTarget(r, "metrics", "read", resourceID) {
		writeError(w, http.StatusForbidden, "access_denied", "metric resource scope is not assigned")
		return
	}
	from, to, ranged, err := parseMetricWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_time", err.Error())
		return
	}
	if !ranged {
		writeJSON(w, http.StatusOK, map[string]any{"items": s.store.Metrics(resourceID)})
		return
	}
	if s.metricHistory == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": filterSamples(s.store.Metrics(resourceID), from, to), "from": from, "to": to, "source": "memory"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	items, err := s.metricHistory(ctx, resourceID, from, to)
	if err != nil {
		writeError(w, http.StatusBadGateway, "history_unavailable", "metric history could not be read")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "from": from, "to": to, "source": "history"})
}

// parseMetricWindow reads from/to. Absent both, the caller serves the live
// window; to alone defaults to an hour before it.
func parseMetricWindow(r *http.Request) (time.Time, time.Time, bool, error) {
	rawFrom := strings.TrimSpace(r.URL.Query().Get("from"))
	rawTo := strings.TrimSpace(r.URL.Query().Get("to"))
	if rawFrom == "" && rawTo == "" {
		return time.Time{}, time.Time{}, false, nil
	}
	to := time.Now().UTC()
	if rawTo != "" {
		parsed, err := time.Parse(time.RFC3339, rawTo)
		if err != nil {
			return time.Time{}, time.Time{}, false, errors.New("to must be an RFC3339 timestamp")
		}
		to = parsed.UTC()
	}
	from := to.Add(-time.Hour)
	if rawFrom != "" {
		parsed, err := time.Parse(time.RFC3339, rawFrom)
		if err != nil {
			return time.Time{}, time.Time{}, false, errors.New("from must be an RFC3339 timestamp")
		}
		from = parsed.UTC()
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, false, errors.New("from must precede to")
	}
	if to.Sub(from) > 400*24*time.Hour {
		return time.Time{}, time.Time{}, false, errors.New("window must not exceed 400 days")
	}
	return from, to, true, nil
}

func filterSamples(items []domain.MetricSample, from, to time.Time) []domain.MetricSample {
	window := make([]domain.MetricSample, 0, len(items))
	for _, item := range items {
		if !item.Timestamp.Before(from) && !item.Timestamp.After(to) {
			window = append(window, item)
		}
	}
	return window
}
func (s *Server) metricSummary(w http.ResponseWriter, r *http.Request) {
	resourceID := r.URL.Query().Get("resourceId")
	if resourceID != "" && !s.authorizeResourceTarget(r, "metrics", "read", resourceID) {
		writeError(w, http.StatusForbidden, "access_denied", "metric resource scope is not assigned")
		return
	}
	writeJSON(w, http.StatusOK, s.store.MetricSummaryFor(resourceID, s.currentMetricResourceIDs(r, time.Now().UTC())))
}

func (s *Server) metricTimeseries(w http.ResponseWriter, r *http.Request) {
	minutes := 60
	if value, err := strconv.Atoi(r.URL.Query().Get("minutes")); err == nil && value > 0 {
		minutes = min(value, 24*60)
	}
	bucketSeconds := 60
	if value, err := strconv.Atoi(r.URL.Query().Get("bucketSeconds")); err == nil && value >= 10 {
		bucketSeconds = min(value, 3600)
	}
	since := time.Now().UTC().Add(-time.Duration(minutes) * time.Minute)
	writeJSON(w, http.StatusOK, map[string]any{"items": s.store.AggregatedMetricsFor(since, time.Duration(bucketSeconds)*time.Second, s.visibleResourceIDs(r, "metrics"))})
}

func (s *Server) createAlert(w http.ResponseWriter, r *http.Request) {
	var alert domain.Alert
	if err := readJSON(r, &alert); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if alert.Name == "" || alert.ResourceID == "" {
		writeError(w, http.StatusBadRequest, "fields_required", "name and resourceId are required")
		return
	}
	if !s.authorizeResourceTarget(r, "alerts", "create", alert.ResourceID) {
		writeError(w, http.StatusForbidden, "access_denied", "alert resource scope is not assigned")
		return
	}
	if alert.Severity == "" {
		alert.Severity = "warning"
	}
	if alert.Status == "" {
		alert.Status = "firing"
	}
	if alert.Severity != "warning" && alert.Severity != "critical" {
		writeError(w, http.StatusBadRequest, "invalid_severity", "severity must be warning or critical")
		return
	}
	if alert.Status != "firing" && alert.Status != "acknowledged" && alert.Status != "resolved" && alert.Status != "silenced" {
		writeError(w, http.StatusBadRequest, "invalid_status", "unsupported alert status")
		return
	}
	alert.ID = fmt.Sprintf("alert-%d", time.Now().UnixNano())
	alert = s.store.PutAlert(alert)
	s.processAlertEvent(alert, "firing")
	writeJSON(w, http.StatusCreated, alert)
}

func (s *Server) updateAlert(w http.ResponseWriter, r *http.Request) {
	previous, ok := s.store.Alert(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "alert not found")
		return
	}
	var alert domain.Alert
	if err := readJSON(r, &alert); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if alert.Name == "" || alert.ResourceID == "" {
		writeError(w, http.StatusBadRequest, "fields_required", "name and resourceId are required")
		return
	}
	if !s.authorizeResourceTarget(r, "alerts", "update", previous.ResourceID) {
		writeError(w, http.StatusForbidden, "access_denied", "alert resource scope is not assigned")
		return
	}
	if alert.Status != "firing" && alert.Status != "acknowledged" && alert.Status != "resolved" && alert.Status != "silenced" {
		writeError(w, http.StatusBadRequest, "invalid_status", "unsupported alert status")
		return
	}
	if alert.Severity != "warning" && alert.Severity != "critical" {
		writeError(w, http.StatusBadRequest, "invalid_severity", "severity must be warning or critical")
		return
	}
	alert.ID = r.PathValue("id")
	alert.RuleID = previous.RuleID
	alert.ResourceID = previous.ResourceID
	alert = s.store.PutAlert(alert)
	if alert.Status == "resolved" || alert.Status == "silenced" {
		s.clearAlertTracking(previous)
	}
	if alert.Status != previous.Status && (alert.Status == "firing" || alert.Status == "resolved") {
		s.processAlertEvent(alert, alert.Status)
	} else {
		s.recomputeInhibitions()
	}
	writeJSON(w, http.StatusOK, alert)
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	items := []domain.Alert{}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	status, severity := r.URL.Query().Get("status"), r.URL.Query().Get("severity")
	allowed := s.resourceAuthorizer(r, "alerts", "read")
	for _, alert := range s.store.ListAlerts() {
		if allowed(alert.ResourceID) &&
			(query == "" || strings.Contains(strings.ToLower(alert.Name+" "+alert.ResourceID+" "+alert.Summary), query)) &&
			(status == "" || alert.Status == status) && (severity == "" || alert.Severity == severity) {
			items = append(items, alert)
		}
	}
	limit := 200
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 {
		limit = min(value, 1000)
	}
	start := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		parts, valid := decodeCursor(cursor, 2)
		if !valid {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
			return
		}
		for start < len(items) && (items[start].UpdatedAt.Format(time.RFC3339Nano) > parts[0] || items[start].UpdatedAt.Format(time.RFC3339Nano) == parts[0] && items[start].ID >= parts[1]) {
			start++
		}
	}
	end := min(start+limit, len(items))
	response := map[string]any{"items": items[start:end], "total": len(items), "limit": limit}
	if end < len(items) {
		last := items[end-1]
		response["nextCursor"] = encodeCursor(last.UpdatedAt.Format(time.RFC3339Nano), last.ID)
	}
	writeJSON(w, http.StatusOK, response)
}
func (s *Server) deleteAlert(w http.ResponseWriter, r *http.Request) {
	alert, ok := s.store.Alert(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "alert not found")
		return
	}
	if !s.authorizeResourceTarget(r, "alerts", "delete", alert.ResourceID) {
		writeError(w, http.StatusForbidden, "access_denied", "alert resource scope is not assigned")
		return
	}
	if err := s.store.DeleteAlert(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "alert not found")
		return
	}
	s.clearAlertTracking(alert)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createIncident(w http.ResponseWriter, r *http.Request) {
	var incident domain.Incident
	if err := readJSON(r, &incident); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.validateIncident(&incident); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_incident", err.Error())
		return
	}
	if !s.authorizeAllTargets(r, "incidents", "create", incident.ResourceIDs) {
		writeError(w, http.StatusForbidden, "access_denied", "incident resource scope is not assigned")
		return
	}
	incident.ID = fmt.Sprintf("incident-%d", time.Now().UnixNano())
	incident = s.store.PutIncident(incident)
	s.store.AddIncidentEvent(domain.IncidentEvent{ID: fmt.Sprintf("incident-event-%d", time.Now().UnixNano()), IncidentID: incident.ID, Type: "declared", Actor: s.subjectFromRequest(r), Message: "Incident declared", CreatedAt: time.Now().UTC()})
	writeJSON(w, http.StatusCreated, incident)
}

func (s *Server) updateIncident(w http.ResponseWriter, r *http.Request) {
	previous, ok := s.store.Incident(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "incident not found")
		return
	}
	var incident domain.Incident
	if err := readJSON(r, &incident); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.validateIncident(&incident); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_incident", err.Error())
		return
	}
	if !s.authorizeAllTargets(r, "incidents", "update", previous.ResourceIDs) || !s.authorizeAllTargets(r, "incidents", "update", incident.ResourceIDs) {
		writeError(w, http.StatusForbidden, "access_denied", "incident resource scope is not assigned")
		return
	}
	incident.ID = r.PathValue("id")
	incident = s.store.PutIncident(incident)
	if previous.Status != incident.Status {
		s.store.AddIncidentEvent(domain.IncidentEvent{ID: fmt.Sprintf("incident-event-%d", time.Now().UnixNano()), IncidentID: incident.ID, Type: "status", Actor: s.subjectFromRequest(r), Message: "Status changed to " + incident.Status, Metadata: map[string]string{"from": previous.Status, "to": incident.Status}, CreatedAt: time.Now().UTC()})
	}
	writeJSON(w, http.StatusOK, incident)
}

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	allowed := s.resourceAuthorizer(r, "incidents", "read")
	anyAllowed := s.anyTargetAuthorizer(r, "incidents", "read")
	items := []domain.Incident{}
	for _, incident := range s.store.ListIncidents() {
		if !anyAllowed(incident.ResourceIDs) {
			continue
		}
		visibleResources := []string{}
		for _, resourceID := range incident.ResourceIDs {
			if allowed(resourceID) {
				visibleResources = append(visibleResources, resourceID)
			}
		}
		incident.ResourceIDs = visibleResources
		items = append(items, incident)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) deleteIncident(w http.ResponseWriter, r *http.Request) {
	incident, ok := s.store.Incident(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "incident not found")
		return
	}
	if !s.authorizeAllTargets(r, "incidents", "delete", incident.ResourceIDs) {
		writeError(w, http.StatusForbidden, "access_denied", "incident resource scope is not assigned")
		return
	}
	if err := s.store.DeleteIncident(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "incident not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createIncidentEvent(w http.ResponseWriter, r *http.Request) {
	incident, ok := s.store.Incident(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "incident not found")
		return
	}
	if !s.authorizeAnyTarget(r, "incidents", "update", incident.ResourceIDs) {
		writeError(w, http.StatusForbidden, "access_denied", "incident resource scope is not assigned")
		return
	}
	var input struct {
		Type     string            `json:"type"`
		Message  string            `json:"message"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.Message == "" {
		writeError(w, http.StatusBadRequest, "message_required", "message is required")
		return
	}
	if input.Type == "" {
		input.Type = "note"
	}
	event := domain.IncidentEvent{ID: fmt.Sprintf("incident-event-%d", time.Now().UnixNano()), IncidentID: r.PathValue("id"), Type: input.Type, Actor: s.subjectFromRequest(r), Message: input.Message, Metadata: input.Metadata, CreatedAt: time.Now().UTC()}
	writeJSON(w, http.StatusCreated, s.store.AddIncidentEvent(event))
}
func (s *Server) listIncidentEvents(w http.ResponseWriter, r *http.Request) {
	incident, ok := s.store.Incident(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "incident not found")
		return
	}
	if !s.authorizeAnyTarget(r, "incidents", "read", incident.ResourceIDs) {
		writeError(w, http.StatusForbidden, "access_denied", "incident resource scope is not assigned")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.store.IncidentEvents(r.PathValue("id"))})
}

func (s *Server) createOperation(w http.ResponseWriter, r *http.Request) {
	var operation domain.Operation
	if err := readJSON(r, &operation); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if operation.Type == "" || len(operation.TargetIDs) == 0 || operation.Reason == "" {
		writeError(w, http.StatusBadRequest, "fields_required", "type, targetIds and reason are required")
		return
	}
	if err := s.validateExecutionTarget(operation.TargetIDs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_target", err.Error())
		return
	}
	if !s.authorizeResourceTarget(r, "operations", "create", operation.TargetIDs[0]) {
		writeError(w, http.StatusForbidden, "access_denied", "target resource scope is not assigned")
		return
	}
	if !supportedOperationTypes[operation.Type] {
		writeError(w, http.StatusBadRequest, "unsupported_operation", "operation type is not supported")
		return
	}
	if err := validateOperationParameters(operation.Type, operation.Parameters); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_parameters", err.Error())
		return
	}
	operation.ID = fmt.Sprintf("operation-%d", time.Now().UnixNano())
	operation.RequestedBy = s.subjectFromRequest(r)
	operation.ApprovedBy = ""
	operation.Status = "pending"
	if operation.Type == "service.restart" {
		operation.Status = "awaiting_approval"
		operation.ApprovedBy = ""
	}
	writeJSON(w, http.StatusCreated, s.store.PutOperation(operation))
}

func (s *Server) listOperations(w http.ResponseWriter, r *http.Request) {
	allowed := s.anyTargetAuthorizer(r, "operations", "read")
	items := []domain.Operation{}
	for _, operation := range s.store.ListOperations() {
		if allowed(operation.TargetIDs) {
			items = append(items, operation)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) approveOperation(w http.ResponseWriter, r *http.Request) {
	current, ok := s.store.Operation(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "operation not found")
		return
	}
	if !s.authorizeAllTargets(r, "operations", "approve", current.TargetIDs) {
		writeError(w, http.StatusForbidden, "access_denied", "operation target scope is not assigned")
		return
	}
	approver := s.subjectFromRequest(r)
	operation, err := s.store.ApproveOperation(r.PathValue("id"), approver)
	if err != nil {
		writeError(w, http.StatusConflict, "operation_not_approved", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (s *Server) createAlertRule(w http.ResponseWriter, r *http.Request) {
	var rule domain.AlertRule
	if err := readJSON(r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateAlertRule(rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_rule", err.Error())
		return
	}
	if !s.authorizeConfiguredScope(r, "alert-rules", "create", rule.ScopePath, rule.Selector) {
		writeError(w, http.StatusForbidden, "access_denied", "alert rule scope is not assigned")
		return
	}
	rule.ID = fmt.Sprintf("alert-rule-%d", time.Now().UnixNano())
	writeJSON(w, http.StatusCreated, s.store.PutAlertRule(rule))
}
func (s *Server) updateAlertRule(w http.ResponseWriter, r *http.Request) {
	previous, ok := s.store.AlertRule(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "alert rule not found")
		return
	}
	var rule domain.AlertRule
	if err := readJSON(r, &rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateAlertRule(rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_rule", err.Error())
		return
	}
	if !s.authorizeConfiguredScope(r, "alert-rules", "update", previous.ScopePath, previous.Selector) || !s.authorizeConfiguredScope(r, "alert-rules", "update", rule.ScopePath, rule.Selector) {
		writeError(w, http.StatusForbidden, "access_denied", "alert rule scope is not assigned")
		return
	}
	rule.ID = r.PathValue("id")
	s.deactivateAlertRule(rule.ID)
	writeJSON(w, http.StatusOK, s.store.PutAlertRule(rule))
}
func (s *Server) listAlertRules(w http.ResponseWriter, r *http.Request) {
	items := []domain.AlertRule{}
	for _, rule := range s.store.ListAlertRules() {
		if s.access.Evaluate(access.Request{SubjectID: s.subjectFromRequest(r), Resource: "alert-rules", Action: "read", ResourcePath: rule.ScopePath, Tags: rule.Selector}).Allowed {
			items = append(items, rule)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) deleteAlertRule(w http.ResponseWriter, r *http.Request) {
	rule, ok := s.store.AlertRule(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "alert rule not found")
		return
	}
	if !s.authorizeConfiguredScope(r, "alert-rules", "delete", rule.ScopePath, rule.Selector) {
		writeError(w, http.StatusForbidden, "access_denied", "alert rule scope is not assigned")
		return
	}
	if err := s.store.DeleteAlertRule(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "alert rule not found")
		return
	}
	s.deactivateAlertRule(rule.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearAlertTracking(alert domain.Alert) {
	if alert.RuleID == "" {
		return
	}
	s.alertMu.Lock()
	defer s.alertMu.Unlock()
	key := alert.RuleID + ":" + alert.ResourceID
	delete(s.activeAlerts, key)
	delete(s.breaches, key)
}

func (s *Server) deactivateAlertRule(ruleID string) {
	s.alertMu.Lock()
	defer s.alertMu.Unlock()
	for _, alert := range s.store.ListAlerts() {
		if alert.RuleID == ruleID && alert.Status != "resolved" && alert.Status != "silenced" {
			alert.Status = "resolved"
			s.store.PutAlert(alert)
		}
	}
	prefix := ruleID + ":"
	for key := range s.activeAlerts {
		if strings.HasPrefix(key, prefix) {
			delete(s.activeAlerts, key)
			delete(s.breaches, key)
		}
	}
}

func (s *Server) createAlertSilence(w http.ResponseWriter, r *http.Request) {
	var silence domain.AlertSilence
	if err := readJSON(r, &silence); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateAlertSilence(silence); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_silence", err.Error())
		return
	}
	if !s.authorizeConfiguredScope(r, "alert-rules", "create", silence.ScopePath, silence.Selector) {
		writeError(w, http.StatusForbidden, "access_denied", "alert silence scope is not assigned")
		return
	}
	silence.ID = fmt.Sprintf("alert-silence-%d", time.Now().UnixNano())
	silence.CreatedBy = s.subjectFromRequest(r)
	silence = s.store.PutAlertSilence(silence)
	s.applyAlertSilence(silence)
	writeJSON(w, http.StatusCreated, silence)
}

func (s *Server) updateAlertSilence(w http.ResponseWriter, r *http.Request) {
	previous, ok := s.store.AlertSilence(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "alert silence not found")
		return
	}
	var silence domain.AlertSilence
	if err := readJSON(r, &silence); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateAlertSilence(silence); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_silence", err.Error())
		return
	}
	if !s.authorizeConfiguredScope(r, "alert-rules", "update", previous.ScopePath, previous.Selector) || !s.authorizeConfiguredScope(r, "alert-rules", "update", silence.ScopePath, silence.Selector) {
		writeError(w, http.StatusForbidden, "access_denied", "alert silence scope is not assigned")
		return
	}
	silence.ID = previous.ID
	silence.CreatedBy = previous.CreatedBy
	silence = s.store.PutAlertSilence(silence)
	s.applyAlertSilence(silence)
	writeJSON(w, http.StatusOK, silence)
}

func (s *Server) listAlertSilences(w http.ResponseWriter, r *http.Request) {
	items := []domain.AlertSilence{}
	for _, silence := range s.store.ListAlertSilences() {
		if s.access.Evaluate(access.Request{SubjectID: s.subjectFromRequest(r), Resource: "alert-rules", Action: "read", ResourcePath: silence.ScopePath, Tags: silence.Selector}).Allowed {
			items = append(items, silence)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) deleteAlertSilence(w http.ResponseWriter, r *http.Request) {
	silence, ok := s.store.AlertSilence(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "alert silence not found")
		return
	}
	if !s.authorizeConfiguredScope(r, "alert-rules", "delete", silence.ScopePath, silence.Selector) {
		writeError(w, http.StatusForbidden, "access_denied", "alert silence scope is not assigned")
		return
	}
	if err := s.store.DeleteAlertSilence(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "alert silence not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) alertSilenced(resourceID string, now time.Time) bool {
	for _, silence := range s.store.ListAlertSilences() {
		if !now.Before(silence.StartsAt) && now.Before(silence.EndsAt) && s.store.ResourceMatchesScope(resourceID, silence.ScopePath, silence.Selector) {
			return true
		}
	}
	return false
}

func (s *Server) applyAlertSilence(silence domain.AlertSilence) {
	s.alertMu.Lock()
	defer s.alertMu.Unlock()
	now := time.Now().UTC()
	if now.Before(silence.StartsAt) || !now.Before(silence.EndsAt) {
		return
	}
	for _, alert := range s.store.ListAlerts() {
		if alert.Status == "resolved" || alert.Status == "silenced" || !s.store.ResourceMatchesScope(alert.ResourceID, silence.ScopePath, silence.Selector) {
			continue
		}
		alert.Status = "silenced"
		s.store.PutAlert(alert)
		delete(s.activeAlerts, alert.RuleID+":"+alert.ResourceID)
	}
}

func (s *Server) createRunbook(w http.ResponseWriter, r *http.Request) {
	var runbook domain.Runbook
	if err := readJSON(r, &runbook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateRunbook(runbook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_runbook", err.Error())
		return
	}
	runbook.ID = fmt.Sprintf("runbook-%d", time.Now().UnixNano())
	writeJSON(w, http.StatusCreated, s.store.PutRunbook(runbook))
}
func (s *Server) updateRunbook(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.store.Runbook(r.PathValue("id")); !ok {
		writeError(w, http.StatusNotFound, "not_found", "runbook not found")
		return
	}
	var runbook domain.Runbook
	if err := readJSON(r, &runbook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateRunbook(runbook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_runbook", err.Error())
		return
	}
	runbook.ID = r.PathValue("id")
	writeJSON(w, http.StatusOK, s.store.PutRunbook(runbook))
}
func (s *Server) listRunbooks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.store.ListRunbooks()})
}
func (s *Server) deleteRunbook(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRunbook(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "runbook not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) executeRunbook(w http.ResponseWriter, r *http.Request) {
	runbook, ok := s.store.Runbook(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "runbook not found")
		return
	}
	var input struct {
		TargetIDs []string `json:"targetIds"`
		Reason    string   `json:"reason"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(input.TargetIDs) == 0 || input.Reason == "" {
		writeError(w, http.StatusBadRequest, "fields_required", "targetIds and reason are required")
		return
	}
	if err := s.validateExecutionTarget(input.TargetIDs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_target", err.Error())
		return
	}
	if !s.authorizeResourceTarget(r, "runbooks", "execute", input.TargetIDs[0]) {
		writeError(w, http.StatusForbidden, "access_denied", "target resource scope is not assigned")
		return
	}
	now := time.Now().UTC()
	execution := domain.RunbookExecution{ID: fmt.Sprintf("execution-%d", time.Now().UnixNano()), RunbookID: runbook.ID, RunbookName: runbook.Name, Status: "running", TargetIDs: input.TargetIDs, RequestedBy: s.subjectFromRequest(r), Reason: input.Reason, CreatedAt: now, UpdatedAt: now}
	if runbook.Risk == "high" {
		execution.Status = "awaiting_approval"
	}
	operations := make([]domain.Operation, 0, len(runbook.Steps))
	for index, step := range runbook.Steps {
		status := "blocked"
		if index == 0 && execution.Status == "running" {
			status = "pending"
		}
		operation := domain.Operation{ID: fmt.Sprintf("operation-%d-%d", time.Now().UnixNano(), index), Type: step.Operation, Status: status, TargetIDs: input.TargetIDs, Parameters: step.Parameters, RequestedBy: execution.RequestedBy, Reason: input.Reason, CreatedAt: now, UpdatedAt: now, ExecutionID: execution.ID, StepIndex: index}
		operations = append(operations, operation)
		execution.OperationIDs = append(execution.OperationIDs, operation.ID)
	}
	writeJSON(w, http.StatusCreated, s.store.CreateRunbookExecution(execution, operations))
}
func (s *Server) listRunbookExecutions(w http.ResponseWriter, r *http.Request) {
	allowed := s.anyTargetAuthorizer(r, "runbooks", "read")
	items := []domain.RunbookExecution{}
	for _, execution := range s.store.ListRunbookExecutions() {
		if allowed(execution.TargetIDs) {
			items = append(items, execution)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) approveRunbookExecution(w http.ResponseWriter, r *http.Request) {
	current, ok := s.store.RunbookExecution(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "runbook execution not found")
		return
	}
	if !s.authorizeAllTargets(r, "runbooks", "approve", current.TargetIDs) {
		writeError(w, http.StatusForbidden, "access_denied", "runbook target scope is not assigned")
		return
	}
	execution, err := s.store.ApproveRunbookExecution(r.PathValue("id"), s.subjectFromRequest(r))
	if err != nil {
		writeError(w, http.StatusConflict, "execution_not_approved", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, execution)
}

func (s *Server) createTerminalSession(w http.ResponseWriter, r *http.Request) {
	var session domain.TerminalSession
	if err := readJSON(r, &session); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if session.TargetID == "" || session.Reason == "" {
		writeError(w, http.StatusBadRequest, "fields_required", "targetId and reason are required")
		return
	}
	target, ok := s.store.Resource(session.TargetID)
	if !ok || target.AgentID == "" {
		writeError(w, http.StatusBadRequest, "target_unavailable", "terminal target must be managed by an agent")
		return
	}
	agent, ok := s.store.Agent(target.AgentID)
	if !ok || agent.NodeID != target.ID {
		writeError(w, http.StatusBadRequest, "target_unavailable", "terminal target must be an agent host node")
		return
	}
	if !s.authorizeResourceTarget(r, "terminal", "create", session.TargetID) {
		writeError(w, http.StatusForbidden, "access_denied", "target resource scope is not assigned")
		return
	}
	session.ID = fmt.Sprintf("terminal-%d", time.Now().UnixNano())
	session.RequestedBy = s.subjectFromRequest(r)
	session.Status = "awaiting_approval"
	session.CreatedAt = time.Now().UTC()
	writeJSON(w, http.StatusCreated, s.store.PutTerminal(session))
}
func (s *Server) approveTerminalSession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.store.Terminal(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "terminal session not found")
		return
	}
	if !s.authorizeResourceTarget(r, "terminal", "approve", session.TargetID) {
		writeError(w, http.StatusForbidden, "access_denied", "terminal target scope is not assigned")
		return
	}
	approver := s.subjectFromRequest(r)
	// Separation of duties: self-approval requires the terminal:approve-self
	// grant, which administrators hold via *:*.
	if approver == session.RequestedBy &&
		!s.authorizeResourceTarget(r, "terminal", "approve-self", session.TargetID) {
		writeError(w, http.StatusConflict, "separation_required", "requester cannot approve terminal session")
		return
	}
	if session.Status != "awaiting_approval" {
		writeError(w, http.StatusConflict, "invalid_status", "session is not awaiting approval")
		return
	}
	now := time.Now().UTC()
	session.ApprovedBy = approver
	session.Status = "active"
	session.StartedAt = &now
	writeJSON(w, http.StatusOK, s.store.PutTerminal(session))
}
func (s *Server) closeTerminalSession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.store.Terminal(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "terminal session not found")
		return
	}
	if !s.authorizeResourceTarget(r, "terminal", "close", session.TargetID) {
		writeError(w, http.StatusForbidden, "access_denied", "terminal target scope is not assigned")
		return
	}
	if session.Status == "closed" {
		writeError(w, http.StatusConflict, "invalid_status", "session is already closed")
		return
	}
	now := time.Now().UTC()
	session.Status = "closed"
	session.ClosedAt = &now
	s.store.CancelTerminalCommands(session.ID)
	s.terminal.closeSession(session.ID)
	writeJSON(w, http.StatusOK, s.store.PutTerminal(session))
}
func (s *Server) listTerminalSessions(w http.ResponseWriter, r *http.Request) {
	allowed := s.resourceAuthorizer(r, "terminal", "read")
	items := []domain.TerminalSession{}
	for _, session := range s.store.ListTerminals() {
		if allowed(session.TargetID) {
			items = append(items, session)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createTerminalCommand(w http.ResponseWriter, r *http.Request) {
	session, ok := s.store.Terminal(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "terminal session not found")
		return
	}
	if !s.authorizeResourceTarget(r, "terminal", "create", session.TargetID) {
		writeError(w, http.StatusForbidden, "access_denied", "terminal target scope is not assigned")
		return
	}
	if session.Status != "active" {
		writeError(w, http.StatusConflict, "inactive_session", "terminal session is not active")
		return
	}
	var input struct {
		Command string `json:"command"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Command = strings.TrimSpace(input.Command)
	if input.Command == "" || len(input.Command) > 4096 {
		writeError(w, http.StatusBadRequest, "invalid_command", "command must contain 1 to 4096 characters")
		return
	}
	if !s.terminalCommandAllowed(input.Command) {
		writeError(w, http.StatusForbidden, "command_blocked", "command blocked by terminal policy")
		return
	}
	command := domain.TerminalCommand{ID: fmt.Sprintf("terminal-command-%d", time.Now().UnixNano()), SessionID: session.ID, TargetID: session.TargetID, Command: input.Command, Status: "queued", RequestedBy: s.subjectFromRequest(r), CreatedAt: time.Now().UTC()}
	writeJSON(w, http.StatusCreated, s.store.PutTerminalCommand(command))
}

func (s *Server) listTerminalCommands(w http.ResponseWriter, r *http.Request) {
	session, ok := s.store.Terminal(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "terminal session not found")
		return
	}
	if !s.authorizeResourceTarget(r, "terminal", "read", session.TargetID) {
		writeError(w, http.StatusForbidden, "access_denied", "terminal target scope is not assigned")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.store.TerminalCommands(r.PathValue("id"))})
}

func (s *Server) claimTerminalCommand(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.store.Agent(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "agent_not_found", "agent enrollment required")
		return
	}
	command, ok := s.store.ClaimTerminalCommand(agent.NodeID)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, command)
}

func (s *Server) completeTerminalCommand(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status string `json:"status"`
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.Status != "succeeded" && input.Status != "failed" {
		writeError(w, http.StatusBadRequest, "invalid_status", "status must be succeeded or failed")
		return
	}
	agent, agentOK := s.store.Agent(r.PathValue("id"))
	if !agentOK {
		writeError(w, http.StatusNotFound, "agent_not_found", "agent enrollment required")
		return
	}
	command, ok := s.store.CompleteTerminalCommand(r.PathValue("commandId"), agent.NodeID, input.Status, input.Output, input.Error)
	if !ok {
		writeError(w, http.StatusConflict, "command_not_running", "terminal command is not running")
		return
	}
	writeJSON(w, http.StatusOK, command)
}
func (s *Server) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 {
		limit = min(value, 1000)
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	items := make([]domain.AuditEvent, 0)
	subject := s.subjectFromRequest(r)
	allowed := s.resourceAuthorizer(r, "audit", "read")
	for _, event := range s.store.Audit() {
		if resourceID := event.Metadata["resourceId"]; resourceID != "" {
			if allowed(resourceID) {
				items = append(items, event)
			}
			continue
		}
		eventScope := event.Metadata["scope"]
		if eventScope == "" {
			eventScope = "*"
		}
		decision := s.access.Evaluate(access.Request{SubjectID: subject, Resource: "audit", Action: "read", ResourcePath: eventScope})
		if decision.Allowed {
			items = append(items, event)
		}
	}
	total := len(items)
	if offset > total {
		offset = total
	}
	end := min(offset+limit, total)
	response := map[string]any{"items": items[offset:end], "total": total, "limit": limit, "offset": offset}
	if end < total {
		response["nextOffset"] = end
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) claimOperation(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.store.Agent(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "agent_not_found", "agent enrollment required")
		return
	}
	operation, ok := s.store.ClaimOperation(agent.NodeID)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (s *Server) completeOperation(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.store.Agent(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "agent_not_found", "agent enrollment required")
		return
	}
	var input struct{ Status, Result, Error string }
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.Status != "succeeded" && input.Status != "failed" {
		writeError(w, http.StatusBadRequest, "invalid_status", "status must be succeeded or failed")
		return
	}
	operation, ok := s.store.CompleteOperation(r.PathValue("operationId"), agent.NodeID, input.Status, input.Result, input.Error)
	if !ok {
		writeError(w, http.StatusConflict, "operation_not_running", "operation is not running for this agent")
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (s *Server) evaluateAccess(w http.ResponseWriter, r *http.Request) {
	subject := s.subjectFromRequest(r)
	if subject == "" {
		writeError(w, http.StatusUnauthorized, "subject_required", "authenticated subject is required")
		return
	}
	var request access.Request
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.SubjectID != subject {
		decision := s.access.Evaluate(access.Request{SubjectID: subject, Resource: "roles", Action: "update", ResourcePath: r.Header.Get("X-KloudView-Scope")})
		if !decision.Allowed {
			writeError(w, http.StatusForbidden, "access_denied", "cross-subject evaluation requires role administration")
			return
		}
	}
	writeJSON(w, http.StatusOK, s.access.Evaluate(request))
}

func (s *Server) listRoles(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.access.Roles()})
}
func (s *Server) createRole(w http.ResponseWriter, r *http.Request) {
	var role access.Role
	if err := readJSON(r, &role); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateRole(role); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_role", err.Error())
		return
	}
	role.ID = fmt.Sprintf("role-%d", time.Now().UnixNano())
	writeJSON(w, http.StatusCreated, s.access.PutRole(role))
}
func (s *Server) updateRole(w http.ResponseWriter, r *http.Request) {
	var role access.Role
	if err := readJSON(r, &role); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateRole(role); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_role", err.Error())
		return
	}
	role.ID = r.PathValue("id")
	updated, err := s.access.UpdateRole(role)
	if err != nil {
		writeError(w, http.StatusConflict, "role_not_updated", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) {
	if err := s.access.DeleteRole(r.PathValue("id")); err != nil {
		writeError(w, http.StatusConflict, "role_not_deleted", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listScopes(w http.ResponseWriter, r *http.Request) {
	items := []access.Scope{}
	for _, scope := range s.access.Scopes() {
		if s.authorizeScopeDefinition(r, "scopes", "read", scope) {
			items = append(items, scope)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) createScope(w http.ResponseWriter, r *http.Request) {
	var scope access.Scope
	if err := readJSON(r, &scope); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if scope.Name == "" {
		writeError(w, http.StatusBadRequest, "name_required", "name is required")
		return
	}
	if !s.authorizeScopeDefinition(r, "scopes", "create", scope) {
		writeError(w, http.StatusForbidden, "access_denied", "scope paths are not assigned")
		return
	}
	scope.ID = fmt.Sprintf("scope-%d", time.Now().UnixNano())
	writeJSON(w, http.StatusCreated, s.access.PutScope(scope))
}
func (s *Server) updateScope(w http.ResponseWriter, r *http.Request) {
	previous, ok := s.access.Scope(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "scope_not_found", "scope not found")
		return
	}
	var scope access.Scope
	if err := readJSON(r, &scope); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if scope.Name == "" {
		writeError(w, http.StatusBadRequest, "name_required", "name is required")
		return
	}
	if !s.authorizeScopeDefinition(r, "scopes", "update", previous) || !s.authorizeScopeDefinition(r, "scopes", "update", scope) {
		writeError(w, http.StatusForbidden, "access_denied", "scope paths are not assigned")
		return
	}
	scope.ID = r.PathValue("id")
	updated, err := s.access.UpdateScope(scope)
	if err != nil {
		writeError(w, http.StatusNotFound, "scope_not_updated", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
func (s *Server) deleteScope(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.access.Scope(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "scope not found")
		return
	}
	if !s.authorizeScopeDefinition(r, "scopes", "delete", scope) {
		writeError(w, http.StatusForbidden, "access_denied", "scope paths are not assigned")
		return
	}
	if err := s.access.DeleteScope(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listBindings(w http.ResponseWriter, r *http.Request) {
	items := []access.Binding{}
	for _, binding := range s.access.Bindings() {
		if s.authorizeBindingScope(r, "read", binding) {
			items = append(items, binding)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) createBinding(w http.ResponseWriter, r *http.Request) {
	var binding access.Binding
	if err := readJSON(r, &binding); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if binding.SubjectID == "" || binding.RoleID == "" || binding.ScopeID == "" {
		writeError(w, http.StatusBadRequest, "fields_required", "subjectId, roleId and scopeId are required")
		return
	}
	if !s.authorizeBindingScope(r, "create", binding) {
		writeError(w, http.StatusForbidden, "access_denied", "binding scope is not assigned")
		return
	}
	binding.ID = fmt.Sprintf("binding-%d", time.Now().UnixNano())
	created, err := s.access.CreateBinding(binding)
	if err != nil {
		writeError(w, http.StatusBadRequest, "binding_not_created", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}
func (s *Server) updateBinding(w http.ResponseWriter, r *http.Request) {
	previous, ok := s.access.Binding(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "binding_not_found", "binding not found")
		return
	}
	var binding access.Binding
	if err := readJSON(r, &binding); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if binding.SubjectID == "" || binding.RoleID == "" || binding.ScopeID == "" {
		writeError(w, http.StatusBadRequest, "fields_required", "subjectId, roleId and scopeId are required")
		return
	}
	if !s.authorizeBindingScope(r, "update", previous) || !s.authorizeBindingScope(r, "update", binding) {
		writeError(w, http.StatusForbidden, "access_denied", "binding scope is not assigned")
		return
	}
	binding.ID = r.PathValue("id")
	updated, err := s.access.UpdateBinding(binding)
	if err != nil {
		writeError(w, http.StatusBadRequest, "binding_not_updated", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
func (s *Server) deleteBinding(w http.ResponseWriter, r *http.Request) {
	binding, ok := s.access.Binding(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "binding not found")
		return
	}
	if !s.authorizeBindingScope(r, "delete", binding) {
		writeError(w, http.StatusForbidden, "access_denied", "binding scope is not assigned")
		return
	}
	if err := s.access.DeleteBinding(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
