package domain

import "time"

type Health string

const (
	HealthHealthy     Health = "healthy"
	HealthWarning     Health = "warning"
	HealthCritical    Health = "critical"
	HealthUnknown     Health = "unknown"
	HealthMaintenance Health = "maintenance"
)

type ResourceType string

const (
	ResourceNode       ResourceType = "node"
	ResourceHypervisor ResourceType = "hypervisor"
	ResourceVM         ResourceType = "vm"
	ResourceContainer  ResourceType = "container"
	ResourceProcess    ResourceType = "process"
)

type Resource struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       ResourceType      `json:"type"`
	Health     Health            `json:"health"`
	AgentID    string            `json:"agentId,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
	// Lifecycle of agent-discovered children. LastSeenAt is the last inventory
	// that still listed it; TerminatedAt is set when one no longer does, so a
	// container that stopped is a record rather than a row that quietly ages.
	LastSeenAt   time.Time  `json:"lastSeenAt,omitzero"`
	TerminatedAt *time.Time `json:"terminatedAt,omitempty"`
}

type Relation struct {
	ID        string    `json:"id"`
	SourceID  string    `json:"sourceId"`
	TargetID  string    `json:"targetId"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"createdAt"`
}

type GroupMembership struct {
	ID         string    `json:"id"`
	GroupID    string    `json:"groupId"`
	ResourceID string    `json:"resourceId"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Group struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	ParentID    string            `json:"parentId,omitempty"`
	Path        string            `json:"path,omitempty"`
	Mode        string            `json:"mode,omitempty"`
	Selector    map[string]string `json:"selector,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type Agent struct {
	ID           string            `json:"id"`
	NodeID       string            `json:"nodeId"`
	Hostname     string            `json:"hostname"`
	Version      string            `json:"version"`
	Protocol     string            `json:"protocolVersion"`
	Status       string            `json:"status"`
	Capabilities []string          `json:"capabilities"`
	Labels       map[string]string `json:"labels,omitempty"`
	LastSeenAt   time.Time         `json:"lastSeenAt"`
	CreatedAt    time.Time         `json:"createdAt"`
	// When this agent first reported its current Version. A staged rollout
	// soaks the canary against this, so it must be the moment the version
	// changed, not the moment of the last heartbeat.
	VersionSince time.Time `json:"versionSince,omitzero"`
	// Runtime credential, stored as a hash. PreviousHash keeps the value the
	// agent held before a rotation and is cleared once the new one is used, so
	// an agent that fails to persist a rotation is never locked out.
	CredentialHash     string    `json:"-"`
	PreviousHash       string    `json:"-"`
	CredentialIssuedAt time.Time `json:"credentialIssuedAt,omitzero"`
}

type MetricSample struct {
	ResourceID string    `json:"resourceId"`
	Timestamp  time.Time `json:"timestamp"`
	CPU        float64   `json:"cpu"`
	Memory     float64   `json:"memory"`
	Disk       float64   `json:"disk"`
	NetworkRx  uint64    `json:"networkRx"`
	NetworkTx  uint64    `json:"networkTx"`
	// Values carries every reading that is not one of the five above.
	//
	// Those five were the whole vocabulary, so a host could be measured for
	// utilization and nothing else. Anything that did not fit — an OOM kill,
	// a throttled container, pressure, a swap figure, the usage of a mount
	// that is not "/" — was either a five-place schema change or was dropped
	// into Resource.Attributes as a last value with no history and no way to
	// alert on it. Several already were.
	//
	// Some of these have no fixed cardinality either: a host has as many
	// filesystems as it has, so there is no column count that would have
	// covered them.
	Values map[string]float64 `json:"values,omitempty"`
}

// Value returns a reading by name, whether it is one of the five that have a
// field of their own or one carried in Values.
func (m MetricSample) Value(name string) (float64, bool) {
	switch name {
	case "cpu":
		return m.CPU, true
	case "memory":
		return m.Memory, true
	case "disk":
		return m.Disk, true
	}
	value, ok := m.Values[name]
	return value, ok
}

type MetricSummary struct {
	ResourceID    string    `json:"resourceId,omitempty"`
	Count         int       `json:"count"`
	CPUAvg        float64   `json:"cpuAvg"`
	CPUMax        float64   `json:"cpuMax"`
	MemoryAvg     float64   `json:"memoryAvg"`
	MemoryMax     float64   `json:"memoryMax"`
	DiskAvg       float64   `json:"diskAvg"`
	DiskMax       float64   `json:"diskMax"`
	NetworkRx     uint64    `json:"networkRx"`
	NetworkTx     uint64    `json:"networkTx"`
	NetworkRxRate float64   `json:"networkRxRate"`
	NetworkTxRate float64   `json:"networkTxRate"`
	ObservedAt    time.Time `json:"observedAt"`
}

type NetworkRate struct {
	Rx float64 `json:"rx"`
	Tx float64 `json:"tx"`
}

type MetricPoint struct {
	Timestamp     time.Time `json:"timestamp"`
	Count         int       `json:"count"`
	CPU           float64   `json:"cpu"`
	Memory        float64   `json:"memory"`
	Disk          float64   `json:"disk"`
	NetworkRxRate float64   `json:"networkRxRate"`
	NetworkTxRate float64   `json:"networkTxRate"`
}

type Alert struct {
	ID          string     `json:"id"`
	RuleID      string     `json:"ruleId,omitempty"`
	Name        string     `json:"name"`
	Severity    string     `json:"severity"`
	Status      string     `json:"status"`
	ResourceID  string     `json:"resourceId"`
	Summary     string     `json:"summary,omitempty"`
	Assignee    string     `json:"assignee,omitempty"`
	StartedAt   time.Time  `json:"startedAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
	Inhibited   bool       `json:"inhibited,omitempty"`
	InhibitedBy []string   `json:"inhibitedBy,omitempty"`
}

type Incident struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Severity    string     `json:"severity"`
	Status      string     `json:"status"`
	Commander   string     `json:"commander,omitempty"`
	Description string     `json:"description,omitempty"`
	AlertIDs    []string   `json:"alertIds,omitempty"`
	ResourceIDs []string   `json:"resourceIds,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
}

// EnrollmentToken is a time-boxed credential that lets any agent enrol during
// its window. Only the hash is stored; the value is shown once at creation.
type EnrollmentToken struct {
	ID     string `json:"id"`
	Prefix string `json:"prefix"`
	Hash   string `json:"-"`
	Note   string `json:"note,omitempty"`
	// Optional bindings. A token that carries them is refused when presented by
	// a host that does not match, so a stolen value cannot enrol another node.
	Hostname    string     `json:"hostname,omitempty"`
	AllowedCIDR string     `json:"allowedCidr,omitempty"`
	MaxUses     int        `json:"maxUses"`
	Uses        int        `json:"uses"`
	CreatedBy   string     `json:"createdBy"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
}

// IncidentEvent is one line of an incident's timeline. Types declared, status,
// note and resource are written by people and stored; operation, terminal,
// approval and alert are derived at read time from what was already recorded
// elsewhere, and Source says which kind a line is. Derived lines are not
// stored, so they appear for actions taken before the incident was declared —
// which is most of them, since an incident is usually declared after the first
// few attempts to fix it.
type IncidentEvent struct {
	ID         string            `json:"id"`
	IncidentID string            `json:"incidentId"`
	Type       string            `json:"type"`
	Actor      string            `json:"actor"`
	Message    string            `json:"message"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	CreatedAt  time.Time         `json:"createdAt"`
	// Source is "recorded" or "derived"; empty on stored events written before
	// this field existed, which the API fills in as "recorded".
	Source string `json:"source,omitempty"`
}

// Event sources and the derived event types.
const (
	EventRecorded  = "recorded"
	EventDerived   = "derived"
	EventOperation = "operation"
	EventTerminal  = "terminal"
	EventApproval  = "approval"
	EventAlert     = "alert"
)

type Operation struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Status      string            `json:"status"`
	TargetIDs   []string          `json:"targetIds"`
	Parameters  map[string]string `json:"parameters,omitempty"`
	RequestedBy string            `json:"requestedBy"`
	Reason      string            `json:"reason"`
	ApprovedBy  string            `json:"approvedBy,omitempty"`
	Result      string            `json:"result,omitempty"`
	Error       string            `json:"error,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
	StartedAt   *time.Time        `json:"startedAt,omitempty"`
	FinishedAt  *time.Time        `json:"finishedAt,omitempty"`
	LeaseUntil  *time.Time        `json:"leaseUntil,omitempty"`
	Attempts    int               `json:"attempts"`
	ExecutionID string            `json:"executionId,omitempty"`
	StepIndex   int               `json:"stepIndex,omitempty"`
}

type AlertRule struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Metric    string            `json:"metric"`
	Operator  string            `json:"operator"`
	Threshold float64           `json:"threshold"`
	Duration  string            `json:"duration"`
	Severity  string            `json:"severity"`
	ScopePath string            `json:"scopePath,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Selector  map[string]string `json:"selector,omitempty"`
	Enabled   bool              `json:"enabled"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

type AlertSilence struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	ScopePath string            `json:"scopePath,omitempty"`
	Selector  map[string]string `json:"selector,omitempty"`
	StartsAt  time.Time         `json:"startsAt"`
	EndsAt    time.Time         `json:"endsAt"`
	CreatedBy string            `json:"createdBy"`
	CreatedAt time.Time         `json:"createdAt"`
}

type AlertInhibition struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	SourceSeverity string            `json:"sourceSeverity"`
	TargetSeverity string            `json:"targetSeverity"`
	ScopePath      string            `json:"scopePath,omitempty"`
	Selector       map[string]string `json:"selector,omitempty"`
	EqualLabels    []string          `json:"equalLabels,omitempty"`
	Enabled        bool              `json:"enabled"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

type NotificationChannel struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	// BodyTemplate lets the receiver decide the payload shape. Empty sends the
	// default body. See api.renderNotificationBody.
	BodyTemplate string    `json:"bodyTemplate,omitempty"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type NotificationRoute struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	ChannelIDs []string          `json:"channelIds"`
	Severities []string          `json:"severities,omitempty"`
	Events     []string          `json:"events,omitempty"`
	ScopePath  string            `json:"scopePath,omitempty"`
	Selector   map[string]string `json:"selector,omitempty"`
	Continue   bool              `json:"continue"`
	Enabled    bool              `json:"enabled"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

type NotificationDelivery struct {
	ID          string     `json:"id"`
	RouteID     string     `json:"routeId"`
	ChannelID   string     `json:"channelId"`
	AlertID     string     `json:"alertId"`
	Event       string     `json:"event"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	StatusCode  int        `json:"statusCode,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	DeliveredAt *time.Time `json:"deliveredAt,omitempty"`
}

type RunbookStep struct {
	Name       string            `json:"name"`
	Operation  string            `json:"operation"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

type Runbook struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Risk        string        `json:"risk"`
	Steps       []RunbookStep `json:"steps"`
	CreatedAt   time.Time     `json:"createdAt"`
	UpdatedAt   time.Time     `json:"updatedAt"`
}

type RunbookExecution struct {
	ID        string `json:"id"`
	RunbookID string `json:"runbookId"`
	// Recorded at execution time. An execution outlives the runbook it ran, and
	// the ID alone cannot say what was executed once that runbook is deleted.
	RunbookName  string     `json:"runbookName,omitempty"`
	Status       string     `json:"status"`
	TargetIDs    []string   `json:"targetIds"`
	OperationIDs []string   `json:"operationIds"`
	RequestedBy  string     `json:"requestedBy"`
	ApprovedBy   string     `json:"approvedBy,omitempty"`
	Reason       string     `json:"reason"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
}

type TerminalSession struct {
	ID          string     `json:"id"`
	TargetID    string     `json:"targetId"`
	Status      string     `json:"status"`
	RequestedBy string     `json:"requestedBy"`
	ApprovedBy  string     `json:"approvedBy,omitempty"`
	Reason      string     `json:"reason"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	ClosedAt    *time.Time `json:"closedAt,omitempty"`
}

type TerminalCommand struct {
	ID          string     `json:"id"`
	SessionID   string     `json:"sessionId"`
	TargetID    string     `json:"targetId"`
	Command     string     `json:"command"`
	Status      string     `json:"status"`
	Output      string     `json:"output,omitempty"`
	Error       string     `json:"error,omitempty"`
	RequestedBy string     `json:"requestedBy"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
}

type TerminalRecordingEvent struct {
	Sequence  int       `json:"sequence"`
	Direction string    `json:"direction"`
	Data      string    `json:"data"`
	Timestamp time.Time `json:"timestamp"`
}

type TerminalRecording struct {
	SessionID string                   `json:"sessionId"`
	TargetID  string                   `json:"targetId"`
	Events    []TerminalRecordingEvent `json:"events"`
	Bytes     int                      `json:"bytes"`
	Truncated bool                     `json:"truncated"`
	CreatedAt time.Time                `json:"createdAt"`
	UpdatedAt time.Time                `json:"updatedAt"`
	ExpiresAt time.Time                `json:"expiresAt"`
}

type AuditEvent struct {
	ID        string            `json:"id"`
	Timestamp time.Time         `json:"timestamp"`
	Actor     string            `json:"actor"`
	Action    string            `json:"action"`
	Target    string            `json:"target"`
	Result    string            `json:"result"`
	SourceIP  string            `json:"sourceIp,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// LogLine is one line the agent shipped continuously: warning and worse, plus
// authentication activity at any severity. Repeat folds identical messages in
// one reporting window.
type LogLine struct {
	ID       string    `json:"id"`
	NodeID   string    `json:"nodeId"`
	AgentID  string    `json:"agentId"`
	At       time.Time `json:"at"`
	Priority int       `json:"priority"`
	Unit     string    `json:"unit"`
	Message  string    `json:"message"`
	Repeat   int       `json:"repeat,omitempty"`
}

// LogCounters is one node's log volume by severity over a reporting window.
// Counting every severity is what makes "is this normal" answerable; Dropped
// records lines the agent's rate cap refused, so a short window is visibly
// short.
type LogCounters struct {
	NodeID string         `json:"nodeId"`
	From   time.Time      `json:"from"`
	To     time.Time      `json:"to"`
	Counts map[string]int `json:"counts"`
	// Containers is the same measurement for the applications running on the
	// node, kept apart because the two are read apart. A single total is
	// dominated by whichever application talks most and matches no read
	// anyone can make.
	Containers map[string]int `json:"containers,omitempty"`
	Dropped    int            `json:"dropped,omitempty"`
}

type AgentInventory struct {
	AgentID    string         `json:"agentId"`
	NodeID     string         `json:"nodeId"`
	Data       map[string]any `json:"data"`
	ObservedAt time.Time      `json:"observedAt"`
}

// User is a local account that can authenticate to the console. PasswordHash is
// persisted (internal state snapshot) but must be cleared before the record is
// returned from any API handler; see Public.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"displayName"`
	PasswordHash string    `json:"passwordHash,omitempty"`
	Status       string    `json:"status"` // active | disabled
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Public returns a copy safe to serialize in API responses (no password hash).
func (u User) Public() User {
	u.PasswordHash = ""
	return u
}

// Team groups users for ownership and organization.
type Team struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	MemberIDs   []string  `json:"memberIds"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
