package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// persistedAgent carries the credential hashes that domain.Agent hides from API
// responses. Without them a restart leaves the server unable to recognise any
// agent that has rotated, and the derived-value fallback only covers the first
// restart after enrolment -- the second locks every agent out.
type persistedAgent struct {
	domain.Agent
	CredentialHash string `json:"credentialHash,omitempty"`
	PreviousHash   string `json:"previousHash,omitempty"`
}

func persistAgents(agents map[string]domain.Agent) map[string]persistedAgent {
	items := make(map[string]persistedAgent, len(agents))
	for id, agent := range agents {
		items[id] = persistedAgent{Agent: agent, CredentialHash: agent.CredentialHash, PreviousHash: agent.PreviousHash}
	}
	return items
}

func restoreAgents(agents map[string]persistedAgent) map[string]domain.Agent {
	items := make(map[string]domain.Agent, len(agents))
	for id, stored := range agents {
		agent := stored.Agent
		agent.CredentialHash = stored.CredentialHash
		agent.PreviousHash = stored.PreviousHash
		items[id] = agent
	}
	return items
}

// persistedEnrollmentToken carries the hash that domain.EnrollmentToken hides
// from API responses. Without it a restart leaves every issued token present
// but unmatchable, so no agent can ever enrol with one.
type persistedEnrollmentToken struct {
	domain.EnrollmentToken
	Hash string `json:"hash"`
}

func (t persistedEnrollmentToken) token() domain.EnrollmentToken {
	restored := t.EnrollmentToken
	restored.Hash = t.Hash
	return restored
}

func persistEnrollmentTokens(tokens map[string]domain.EnrollmentToken) map[string]persistedEnrollmentToken {
	items := make(map[string]persistedEnrollmentToken, len(tokens))
	for id, token := range tokens {
		items[id] = persistedEnrollmentToken{EnrollmentToken: token, Hash: token.Hash}
	}
	return items
}

func restoreEnrollmentTokens(tokens map[string]persistedEnrollmentToken) map[string]domain.EnrollmentToken {
	items := make(map[string]domain.EnrollmentToken, len(tokens))
	for id, token := range tokens {
		items[id] = token.token()
	}
	return items
}

type snapshot struct {
	Agents                 map[string]persistedAgent              `json:"agents"`
	Resources              map[string]domain.Resource             `json:"resources"`
	Groups                 map[string]domain.Group                `json:"groups"`
	Relations              map[string]domain.Relation             `json:"relations"`
	Members                map[string]domain.GroupMembership      `json:"members"`
	Metrics                map[string][]domain.MetricSample       `json:"metrics"`
	Alerts                 map[string]domain.Alert                `json:"alerts"`
	Incidents              map[string]domain.Incident             `json:"incidents"`
	IncidentEvents         map[string][]domain.IncidentEvent      `json:"incidentEvents"`
	Operations             map[string]domain.Operation            `json:"operations"`
	AlertRules             map[string]domain.AlertRule            `json:"alertRules"`
	AlertSilences          map[string]domain.AlertSilence         `json:"alertSilences"`
	AlertInhibitions       map[string]domain.AlertInhibition      `json:"alertInhibitions"`
	NotificationChannels   map[string]domain.NotificationChannel  `json:"notificationChannels"`
	NotificationRoutes     map[string]domain.NotificationRoute    `json:"notificationRoutes"`
	NotificationDeliveries map[string]domain.NotificationDelivery `json:"notificationDeliveries"`
	Runbooks               map[string]domain.Runbook              `json:"runbooks"`
	Executions             map[string]domain.RunbookExecution     `json:"executions"`
	Terminals              map[string]domain.TerminalSession      `json:"terminals"`
	TerminalCommands       map[string]domain.TerminalCommand      `json:"terminalCommands"`
	TerminalRecordings     map[string]domain.TerminalRecording    `json:"terminalRecordings"`
	EnrollmentTokens       map[string]persistedEnrollmentToken    `json:"enrollmentTokens"`
	Audit                  []domain.AuditEvent                    `json:"audit"`
	Inventories            map[string]domain.AgentInventory       `json:"inventories"`
	Users                  map[string]domain.User                 `json:"users"`
	Teams                  map[string]domain.Team                 `json:"teams"`
}

func NewPersistent(path string) (*Memory, error) {
	memory := NewMemory()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return memory, nil
	}
	if err != nil {
		return nil, err
	}
	var state snapshot
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	memory.restore(state)
	return memory, nil
}

func (s *Memory) Save(path string) error {
	data, err := s.MarshalState(true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// MarshalStateWithout is MarshalState with parts the caller persists another
// way left out of the document.
func (s *Memory) MarshalStateWithout(includeMetrics, includeResources, includeInventories bool) ([]byte, error) {
	return s.marshalState(includeMetrics, includeResources, includeInventories)
}

func (s *Memory) MarshalState(includeMetrics bool) ([]byte, error) {
	return s.marshalState(includeMetrics, true, true)
}

func (s *Memory) marshalState(includeMetrics, includeResources, includeInventories bool) ([]byte, error) {
	s.mu.RLock()
	state := snapshot{Agents: persistAgents(s.agents), Resources: s.resources, Groups: s.groups, Relations: s.relations, Members: s.members, Metrics: s.metrics, Alerts: s.alerts, Incidents: s.incidents, IncidentEvents: s.incidentEvents, Operations: s.operations, AlertRules: s.alertRules, AlertSilences: s.alertSilences, AlertInhibitions: s.alertInhibitions, NotificationChannels: s.notificationChannels, NotificationRoutes: s.notificationRoutes, NotificationDeliveries: s.notificationDeliveries, Runbooks: s.runbooks, Executions: s.executions, Terminals: s.terminals, TerminalCommands: s.terminalCommands, TerminalRecordings: s.terminalRecordings, EnrollmentTokens: persistEnrollmentTokens(s.enrollmentTokens), Audit: s.audit, Inventories: s.inventories, Users: s.users, Teams: s.teams}
	if !includeMetrics {
		state.Metrics = nil
	}
	if !includeResources {
		state.Resources = nil
	}
	if !includeInventories {
		state.Inventories = nil
	}
	data, err := json.Marshal(state)
	s.mu.RUnlock()
	return data, err
}

func RestoreState(data []byte) (*Memory, error) {
	memory := NewMemory()
	var state snapshot
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	memory.restore(state)
	return memory, nil
}

func (s *Memory) restore(state snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state.Agents != nil {
		s.agents = restoreAgents(state.Agents)
	}
	if state.Resources != nil {
		s.resources = state.Resources
	}
	if state.Groups != nil {
		s.groups = state.Groups
	}
	if state.Relations != nil {
		s.relations = state.Relations
	}
	if state.Members != nil {
		s.members = state.Members
	}
	if state.Metrics != nil {
		s.metrics = state.Metrics
	}
	if state.Alerts != nil {
		s.alerts = state.Alerts
	}
	if state.Incidents != nil {
		s.incidents = state.Incidents
	}
	if state.IncidentEvents != nil {
		s.incidentEvents = state.IncidentEvents
	}
	if state.Operations != nil {
		s.operations = state.Operations
		// Re-bound on load, so a document carrying oversized results is trimmed
		// rather than kept as it was written.
		for id, operation := range s.operations {
			if bounded := truncateResult(operation.Result); bounded != operation.Result {
				operation.Result = bounded
				s.operations[id] = operation
			}
		}
		s.pruneOperations()
	}
	if state.AlertRules != nil {
		s.alertRules = state.AlertRules
	}
	if state.AlertSilences != nil {
		s.alertSilences = state.AlertSilences
	}
	if state.AlertInhibitions != nil {
		s.alertInhibitions = state.AlertInhibitions
	}
	if state.NotificationChannels != nil {
		s.notificationChannels = state.NotificationChannels
	}
	if state.NotificationRoutes != nil {
		s.notificationRoutes = state.NotificationRoutes
	}
	if state.NotificationDeliveries != nil {
		s.notificationDeliveries = state.NotificationDeliveries
	}
	if state.Runbooks != nil {
		s.runbooks = state.Runbooks
	}
	if state.Executions != nil {
		s.executions = state.Executions
	}
	if state.Terminals != nil {
		s.terminals = state.Terminals
	}
	if state.TerminalCommands != nil {
		s.terminalCommands = state.TerminalCommands
	}
	if state.EnrollmentTokens != nil {
		s.enrollmentTokens = restoreEnrollmentTokens(state.EnrollmentTokens)
	}
	if state.TerminalRecordings != nil {
		s.terminalRecordings = state.TerminalRecordings
	}
	if state.Audit != nil {
		s.audit = state.Audit
	}
	if state.Users != nil {
		s.users = state.Users
	}
	if state.Teams != nil {
		s.teams = state.Teams
	}
	if state.Inventories != nil {
		s.inventories = state.Inventories
	}
}
