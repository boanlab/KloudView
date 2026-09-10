package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/access"
	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func TestAgentEnrollment(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	body := `{"token":"test-token","hostname":"node-01","version":"0.1.0","capabilities":["metrics"]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentCredentialUsesServerOnlyKey(t *testing.T) {
	const credentialKey = "server-only-runtime-credential-key-123456"
	handler := New(store.NewMemory(), "test-token", "").WithAgentCredentialKey(credentialKey).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"test-token","hostname":"node-01","version":"0.1.0","protocolVersion":"1","capabilities":["metrics"]}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("enrollment status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Agent      domain.Agent `json:"agent"`
		Credential string       `json:"credential"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}

	heartbeatBody := `{"nodeId":"node-node-01","hostname":"node-01","version":"0.1.0","protocolVersion":"1","capabilities":["metrics"]}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+response.Agent.ID+"/heartbeat", strings.NewReader(heartbeatBody))
	agentAuthorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap-derived credential status = %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+response.Agent.ID+"/heartbeat", strings.NewReader(heartbeatBody))
	request.Header.Set("Authorization", "Bearer "+response.Credential)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("runtime credential status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentProtocolCompatibility(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"test-token","hostname":"node-01","version":"0.1.0","protocolVersion":"2"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "unsupported_agent_protocol") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"agentProtocolVersions":["1"]`) {
		t.Fatalf("system info status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Header().Get("Content-Security-Policy") == "" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers missing: %v", recorder.Header())
	}
}

func TestResourceCursorAndOverviewCache(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-a", Name: "node", Type: domain.ResourceNode, Health: domain.HealthHealthy})
	memory.UpsertResource(domain.Resource{ID: "node-b", Name: "node", Type: domain.ResourceNode, Health: domain.HealthHealthy})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources?limit=1", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var first struct {
		Items      []domain.Resource `json:"items"`
		NextCursor string            `json:"nextCursor"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &first) != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %s", recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/resources?limit=1&cursor="+first.NextCursor, nil)
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var second struct {
		Items []domain.Resource `json:"items"`
	}
	if json.Unmarshal(recorder.Body.Bytes(), &second) != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second page = %s", recorder.Body.String())
	}
	for index := 0; index < 2; index++ {
		request = httptest.NewRequest(http.MethodGet, "/api/v1/overview?groupType=rack", nil)
		authorize(request)
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
	}
	if recorder.Header().Get("X-KloudView-Cache") != "hit" {
		t.Fatalf("cache = %s", recorder.Header().Get("X-KloudView-Cache"))
	}
}

func TestInhibitionDefersWebhookUntilSourceResolves(t *testing.T) {
	received := make(chan struct{}, 1)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, Health: domain.HealthHealthy})
	memory.PutAlertInhibition(domain.AlertInhibition{ID: "inhibition-01", Name: "critical suppresses warning", SourceSeverity: "critical", TargetSeverity: "warning", Enabled: true})
	memory.PutNotificationChannel(domain.NotificationChannel{ID: "channel-01", Name: "webhook", Type: "webhook", URL: webhook.URL, Enabled: true})
	memory.PutNotificationRoute(domain.NotificationRoute{ID: "route-01", Name: "all firing", ChannelIDs: []string{"channel-01"}, Events: []string{"firing"}, Enabled: true})
	server := New(memory, "test-token", "")
	source := memory.PutAlert(domain.Alert{ID: "source", Name: "critical", Severity: "critical", Status: "firing", ResourceID: "node-01"})
	target := memory.PutAlert(domain.Alert{ID: "target", Name: "warning", Severity: "warning", Status: "firing", ResourceID: "node-01"})
	server.processAlertEvent(target, "firing")
	if current, _ := memory.Alert(target.ID); !current.Inhibited {
		t.Fatal("target was not inhibited")
	}
	select {
	case <-received:
		t.Fatal("inhibited notification delivered")
	case <-time.After(100 * time.Millisecond):
	}
	source.Status = "resolved"
	source = memory.PutAlert(source)
	server.processAlertEvent(source, "resolved")
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("notification was not delivered")
	}
}

func TestHealthReportsStorageFailure(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").WithStorageHealth(func(context.Context) error {
		return errors.New("database unavailable")
	}).Handler()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestReadAPIRequiresIdentity(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous read status = %d", recorder.Code)
	}
}

func TestCrossOriginRequestDeniedByDefault(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/resources", nil)
	request.Header.Set("Origin", "https://untrusted.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", recorder.Code)
	}
}

func TestSameOriginModuleRequestAllowed(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", t.TempDir()).Handler()
	request := httptest.NewRequest(http.MethodGet, "http://console.example/src/app.js", nil)
	request.Header.Set("Origin", "http://console.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusForbidden {
		t.Fatalf("same-origin request denied: %s", recorder.Body.String())
	}
}

func TestStaleAgentIsReportedOffline(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertAgent(domain.Agent{ID: "agent-stale", Hostname: "stale", Status: "online", LastSeenAt: time.Now().UTC().Add(-time.Minute)})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"offline"`) {
		t.Fatalf("stale status missing: %s", recorder.Body.String())
	}
}

func TestStaleAgentPropagatesUnknownHealthAndStaleMetrics(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertAgent(domain.Agent{ID: "agent-stale", NodeID: "node-01", Hostname: "stale", Status: "online", LastSeenAt: time.Now().UTC().Add(-time.Minute)})
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, Health: domain.HealthHealthy, AgentID: "agent-stale"})
	memory.UpsertResource(domain.Resource{ID: "container-01", Name: "container-01", Type: domain.ResourceContainer, Health: domain.HealthHealthy, AgentID: "agent-stale"})
	memory.PutRelation(domain.Relation{ID: "runs-container", SourceID: "node-01", TargetID: "container-01", Type: "runs"})
	memory.PutGroup(domain.Group{ID: "rack-01", Name: "Rack-01", Type: "rack", Path: "production/rack-01"})
	memory.PutMembership(domain.GroupMembership{ID: "member-01", GroupID: "rack-01", ResourceID: "node-01"})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", CPU: 90, Timestamp: time.Now().UTC()})
	handler := New(memory, "test-token", "").Handler()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview?groupType=rack", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"unknown":2`) || !strings.Contains(body, `"metricsStale":true`) || !strings.Contains(body, `"count":0`) {
		t.Fatalf("overview = %s", body)
	}

	memory.PutAlert(domain.Alert{ID: "alert-01", Name: "Runtime failure", ResourceID: "container-01", Severity: "critical", Status: "firing"})
	request = httptest.NewRequest(http.MethodGet, "/api/v1/resources/container-01", nil)
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"health":"critical"`) {
		t.Fatalf("alert health = %s", recorder.Body.String())
	}
}

func TestOverviewAggregatesGroupsAndMetrics(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, Health: domain.HealthHealthy})
	memory.PutGroup(domain.Group{ID: "rack-01", Name: "Rack-01", Type: "rack", Path: "production/rack-01"})
	memory.PutMembership(domain.GroupMembership{ID: "member-01", GroupID: "rack-01", ResourceID: "node-01"})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-01", CPU: 71, Memory: 62, Disk: 53, Timestamp: time.Now().UTC()})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview?groupType=rack", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"name":"Rack-01"`) || !strings.Contains(body, `"cpu":71`) || !strings.Contains(body, `"healthy":1`) {
		t.Fatalf("overview mismatch: %s", body)
	}
}

func TestOverviewFiltersAndLimitsCellsBySeverity(t *testing.T) {
	memory := store.NewMemory()
	memory.PutGroup(domain.Group{ID: "rack-01", Name: "Rack-01", Type: "rack", Path: "production/rack-01"})
	for index, resource := range []domain.Resource{
		{ID: "node-critical", Name: "critical", Type: domain.ResourceNode, Health: domain.HealthCritical},
		{ID: "node-warning", Name: "warning", Type: domain.ResourceNode, Health: domain.HealthWarning},
		{ID: "node-healthy", Name: "healthy", Type: domain.ResourceNode, Health: domain.HealthHealthy},
	} {
		memory.UpsertResource(resource)
		memory.PutMembership(domain.GroupMembership{ID: fmt.Sprintf("member-%d", index), GroupID: "rack-01", ResourceID: resource.ID})
	}
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview?groupType=rack&groupId=rack-01&anomalies=true&cellLimit=1", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"cellTotal":2`) || !strings.Contains(body, `"cellsTruncated":true`) || !strings.Contains(body, `"id":"node-critical"`) || strings.Contains(body, `"id":"node-healthy"`) {
		t.Fatalf("overview filtering mismatch: %s", body)
	}
}

// Attention spans the whole authorized set; cells carry the display filters.
func TestOverviewAttentionIgnoresTheDisplayFilters(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-1", Name: "node-1", Type: domain.ResourceNode, Health: domain.HealthHealthy})
	memory.UpsertResource(domain.Resource{ID: "process-zombie", Name: "sh", Type: domain.ResourceProcess, Health: domain.HealthWarning})
	handler := New(memory, "test-token", "").Handler()

	// Exactly what the console asks for while the heatmap is showing nodes.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview?groupType=rack&types=node", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("overview status = %d", recorder.Code)
	}

	var payload struct {
		Warning        int `json:"warning"`
		AttentionTotal int `json:"attentionTotal"`
		Attention      []struct {
			ID     string `json:"id"`
			Health string `json:"health"`
		} `json:"attention"`
		Cells []struct {
			ID string `json:"id"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	// The type filter still applies to cells, so the two must disagree here.
	if len(payload.Cells) != 1 || payload.Cells[0].ID != "node-1" {
		t.Fatalf("cells should hold only the node: %+v", payload.Cells)
	}
	if payload.Warning != 1 || payload.AttentionTotal != 1 {
		t.Fatalf("warning = %d, attentionTotal = %d, want 1 and 1", payload.Warning, payload.AttentionTotal)
	}
	if len(payload.Attention) != 1 || payload.Attention[0].ID != "process-zombie" {
		t.Fatalf("attention should name the warning process: %+v", payload.Attention)
	}
}

func TestOverviewGroupIncludesHostedDescendants(t *testing.T) {
	memory := store.NewMemory()
	memory.PutGroup(domain.Group{ID: "rack-01", Name: "Rack 01", Type: "rack", Path: "production/dc-1/rack-01"})
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, Health: domain.HealthHealthy})
	memory.UpsertResource(domain.Resource{ID: "vm-01", Name: "vm-01", Type: domain.ResourceVM, Health: domain.HealthHealthy})
	memory.UpsertResource(domain.Resource{ID: "container-01", Name: "container-01", Type: domain.ResourceContainer, Health: domain.HealthWarning})
	memory.PutMembership(domain.GroupMembership{ID: "member", GroupID: "rack-01", ResourceID: "node-01"})
	memory.PutRelation(domain.Relation{ID: "hosts", SourceID: "node-01", TargetID: "vm-01", Type: "hosts"})
	memory.PutRelation(domain.Relation{ID: "runs", SourceID: "vm-01", TargetID: "container-01", Type: "runs"})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview?groupType=rack&groupId=rack-01", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"total":3,"healthy":2,"warning":1`) || !strings.Contains(body, `"cellTotal":3`) {
		t.Fatalf("inherited group overview = %s", body)
	}
}

func TestResourceFilteringAndPagination(t *testing.T) {
	memory := store.NewMemory()
	for _, resource := range []domain.Resource{
		{ID: "node-01", Name: "api-node", Type: domain.ResourceNode, Health: domain.HealthHealthy},
		{ID: "node-02", Name: "db-node", Type: domain.ResourceNode, Health: domain.HealthWarning},
		{ID: "vm-01", Name: "api-vm", Type: domain.ResourceVM, Health: domain.HealthHealthy},
	} {
		memory.UpsertResource(resource)
	}
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources?q=api&type=node&limit=1", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"total":1`) || !strings.Contains(body, "api-node") || strings.Contains(body, "api-vm") {
		t.Fatalf("resource filtering mismatch: %s", body)
	}
}

func TestReadAPIsFilterResourcesByAssignedScope(t *testing.T) {
	memory := store.NewMemory()
	memory.PutGroup(domain.Group{ID: "production", Name: "Production", Type: "environment", Path: "production"})
	memory.PutGroup(domain.Group{ID: "staging", Name: "Staging", Type: "environment", Path: "staging"})
	for _, resource := range []domain.Resource{
		{ID: "node-production", Name: "production-node", Type: domain.ResourceNode, Health: domain.HealthHealthy},
		{ID: "vm-production", Name: "production-vm", Type: domain.ResourceVM, Health: domain.HealthHealthy},
		{ID: "node-staging", Name: "staging-node", Type: domain.ResourceNode, Health: domain.HealthHealthy},
	} {
		memory.UpsertResource(resource)
	}
	memory.PutMembership(domain.GroupMembership{ID: "production-member", GroupID: "production", ResourceID: "node-production"})
	memory.PutMembership(domain.GroupMembership{ID: "staging-member", GroupID: "staging", ResourceID: "node-staging"})
	memory.PutRelation(domain.Relation{ID: "hosts", SourceID: "node-production", TargetID: "vm-production", Type: "hosts"})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-production", CPU: 11, Timestamp: time.Now().UTC()})
	memory.AddMetric(domain.MetricSample{ResourceID: "node-staging", CPU: 99, Timestamp: time.Now().UTC()})
	server := New(memory, "test-token", "")
	bindOperator(server)
	handler := server.Handler()

	for _, path := range []string{"/api/v1/resources", "/api/v1/overview?groupType=environment"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-KloudView-Subject", "test-operator")
		request.Header.Set("X-KloudView-Scope", "production")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		body := recorder.Body.String()
		if recorder.Code != http.StatusOK || !strings.Contains(body, "production-node") || !strings.Contains(body, "production-vm") || strings.Contains(body, "staging-node") {
			t.Fatalf("%s scope response = %s", path, body)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/summary", nil)
	request.Header.Set("X-KloudView-Subject", "test-operator")
	request.Header.Set("X-KloudView-Scope", "production")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"count":1`) || !strings.Contains(recorder.Body.String(), `"cpuAvg":11`) {
		t.Fatalf("metric scope response = %s", recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/resources/vm-production", nil)
	request.Header.Set("X-KloudView-Subject", "test-operator")
	request.Header.Set("X-KloudView-Scope", "production")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "production-vm") {
		t.Fatalf("resource detail response = %s", recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/resources/node-staging", nil)
	request.Header.Set("X-KloudView-Subject", "test-operator")
	request.Header.Set("X-KloudView-Scope", "production")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-scope resource detail status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestOperationalRecordsRespectAssignedScope(t *testing.T) {
	memory := store.NewMemory()
	memory.PutGroup(domain.Group{ID: "production", Name: "Production", Path: "production"})
	memory.PutGroup(domain.Group{ID: "staging", Name: "Staging", Path: "staging"})
	memory.UpsertResource(domain.Resource{ID: "node-production", Name: "production-node", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "node-staging", Name: "staging-node", Type: domain.ResourceNode})
	memory.PutMembership(domain.GroupMembership{ID: "production-member", GroupID: "production", ResourceID: "node-production"})
	memory.PutMembership(domain.GroupMembership{ID: "staging-member", GroupID: "staging", ResourceID: "node-staging"})
	memory.PutAlert(domain.Alert{ID: "alert-production", Name: "production-alert", ResourceID: "node-production", Status: "firing"})
	memory.PutAlert(domain.Alert{ID: "alert-staging", Name: "staging-alert", ResourceID: "node-staging", Status: "firing"})
	memory.PutOperation(domain.Operation{ID: "operation-production", Type: "inventory.refresh", TargetIDs: []string{"node-production"}, Status: "pending"})
	memory.PutOperation(domain.Operation{ID: "operation-staging", Type: "inventory.refresh", TargetIDs: []string{"node-staging"}, Status: "pending"})
	memory.PutIncident(domain.Incident{ID: "incident-production", Title: "production-incident", Status: "open", ResourceIDs: []string{"node-production"}})
	memory.PutIncident(domain.Incident{ID: "incident-staging", Title: "staging-incident", Status: "open", ResourceIDs: []string{"node-staging"}})

	accessEngine := access.NewEngine()
	accessEngine.PutRole(access.Role{ID: "scoped-reader", Name: "Scoped reader", Permissions: []access.Permission{{Resource: "resources", Action: "read"}, {Resource: "alerts", Action: "read"}, {Resource: "operations", Action: "read"}, {Resource: "incidents", Action: "read"}, {Resource: "metrics", Action: "read"}}})
	accessEngine.PutScope(access.Scope{ID: "production-only", Name: "Production", Paths: []string{"production"}})
	accessEngine.PutBinding(access.Binding{ID: "scoped-binding", SubjectID: "scoped-user", RoleID: "scoped-reader", ScopeID: "production-only"})
	handler := NewWithAccess(memory, accessEngine, "test-token", "").Handler()

	for _, path := range []string{"/api/v1/alerts", "/api/v1/operations"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-KloudView-Subject", "scoped-user")
		request.Header.Set("X-KloudView-Scope", "production")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		body := recorder.Body.String()
		if recorder.Code != http.StatusOK || !strings.Contains(body, "production") || strings.Contains(body, "staging") {
			t.Fatalf("%s scope response = %s", path, body)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	request.Header.Set("X-KloudView-Subject", "scoped-user")
	request.Header.Set("X-KloudView-Scope", "production")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"activeIncidents":1`) {
		t.Fatalf("overview incident scope response = %s", recorder.Body.String())
	}
}

func TestAccessGovernanceRespectsManagedScope(t *testing.T) {
	memory := store.NewMemory()
	accessEngine := access.NewEngine()
	accessEngine.PutRole(access.Role{ID: "scope-manager", Name: "Scope manager", Permissions: []access.Permission{{Resource: "scopes", Action: "*"}, {Resource: "bindings", Action: "*"}}})
	accessEngine.PutScope(access.Scope{ID: "managed-production", Name: "Managed production", Paths: []string{"production"}})
	accessEngine.PutScope(access.Scope{ID: "managed-staging", Name: "Managed staging", Paths: []string{"staging"}})
	accessEngine.PutBinding(access.Binding{ID: "manager-binding", SubjectID: "scope-manager-user", RoleID: "scope-manager", ScopeID: "managed-production"})
	accessEngine.PutBinding(access.Binding{ID: "staging-binding", SubjectID: "staging-user", RoleID: "scope-manager", ScopeID: "managed-staging"})
	handler := NewWithAccess(memory, accessEngine, "test-token", "").Handler()

	for _, path := range []string{"/api/v1/scopes", "/api/v1/role-bindings"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-KloudView-Subject", "scope-manager-user")
		request.Header.Set("X-KloudView-Scope", "production")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		body := recorder.Body.String()
		if recorder.Code != http.StatusOK || !strings.Contains(body, "managed-production") || strings.Contains(body, "managed-staging") || strings.Contains(body, "scope-global") {
			t.Fatalf("%s governance scope response = %s", path, body)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/scopes", strings.NewReader(`{"name":"Forbidden staging","paths":["staging"]}`))
	request.Header.Set("X-KloudView-Subject", "scope-manager-user")
	request.Header.Set("X-KloudView-Scope", "production")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-scope creation status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestGroupLifecycle(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(`{"name":"Rack-01","type":"rack"}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d", recorder.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil)
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !strings.Contains(recorder.Body.String(), "Rack-01") {
		t.Fatalf("missing group: %s", recorder.Body.String())
	}
}

func TestEnrollmentRejectsInvalidToken(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"wrong","hostname":"node-01"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestAgentCredentialIsBoundToAgent(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-02", Name: "node-02", Type: domain.ResourceNode, AgentID: "agent-node-02"})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-02/metrics", strings.NewReader(`{"resourceId":"node-02","cpu":10}`))
	wrongCredential := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/metrics", nil)
	agentAuthorize(wrongCredential)
	request.Header.Set("Authorization", wrongCredential.Header.Get("Authorization"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("cross-agent credential status = %d", recorder.Code)
	}
}

func TestAgentCannotWriteAnotherResourceMetrics(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-02", Name: "node-02", Type: domain.ResourceNode, AgentID: "agent-node-02"})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/metrics", strings.NewReader(`{"resourceId":"node-02","cpu":10}`))
	agentAuthorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("resource ownership status = %d", recorder.Code)
	}
}

func TestMetricIngestionRejectsUnknownResourceAndInvalidValues(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertAgent(domain.Agent{ID: "agent-node-01", NodeID: "node-01", Hostname: "node-01"})
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-node-01"})
	handler := New(memory, "test-token", "").Handler()
	for _, item := range []struct {
		body string
		code int
	}{
		{`{"resourceId":"unknown","cpu":10}`, http.StatusNotFound},
		{`{"resourceId":"node-01","cpu":101}`, http.StatusBadRequest},
		{`{"resourceId":"node-01","memory":-1}`, http.StatusBadRequest},
		{`{"resourceId":"node-01","disk":101}`, http.StatusBadRequest},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/metrics", strings.NewReader(item.body))
		agentAuthorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != item.code {
			t.Fatalf("body = %s status = %d response = %s", item.body, recorder.Code, recorder.Body.String())
		}
	}
	if len(memory.Metrics("node-01")) != 0 {
		t.Fatal("invalid metrics must not be stored")
	}
}

func TestHeartbeatRequiresEnrollment(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/unknown/heartbeat", strings.NewReader(`{"hostname":"node-01"}`))
	agentAuthorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHeartbeatPreservesEnrolledIdentityAndMergesMetadata(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertAgent(domain.Agent{ID: "agent-node-01", NodeID: "node-node-01", Hostname: "node-01", Version: "old", Protocol: "1", Capabilities: []string{"metrics"}, LastSeenAt: time.Now().UTC()})
	memory.UpsertResource(domain.Resource{ID: "node-node-01", Name: "node-01", Type: domain.ResourceNode, Health: domain.HealthHealthy, AgentID: "agent-node-01", Tags: map[string]string{"environment": "production"}})
	handler := New(memory, "test-token", "").Handler()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/heartbeat", strings.NewReader(`{"nodeId":"node-other","hostname":"other","version":"new","protocolVersion":"1","capabilities":["metrics"]}`))
	agentAuthorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("identity mismatch status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/heartbeat", strings.NewReader(`{"nodeId":"node-node-01","hostname":"node-01","version":"new","protocolVersion":"1","capabilities":["inventory","metrics"],"labels":{"os":"linux","arch":"amd64"}}`))
	agentAuthorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	agent, _ := memory.Agent("agent-node-01")
	resource, _ := memory.Resource("node-node-01")
	if agent.NodeID != "node-node-01" || agent.Hostname != "node-01" || agent.Version != "new" || resource.Tags["environment"] != "production" || resource.Tags["os"] != "linux" || resource.Attributes["agentVersion"] != "new" {
		t.Fatalf("agent = %+v, resource = %+v", agent, resource)
	}
}

func TestAgentEnrollmentRejectsIdentityCollisionAndInvalidCapabilities(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"test-token","hostname":"node-01","protocolVersion":"1","capabilities":["metrics"]}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("initial enrollment status = %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"test-token","hostname":"node.01","protocolVersion":"1","capabilities":["metrics"]}`))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("collision status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"test-token","hostname":"node-02","protocolVersion":"1","capabilities":["root-shell"]}`))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("capability status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestMembershipAndRelationLifecycle(t *testing.T) {
	memory := store.NewMemory()
	memory.PutGroup(domain.Group{ID: "rack-01", Name: "Rack-01", Type: "rack", Mode: "static"})
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "vm-01", Name: "vm-01", Type: domain.ResourceVM})
	handler := New(memory, "test-token", "").Handler()
	cases := []struct{ path, body string }{
		{"/api/v1/memberships", `{"groupId":"rack-01","resourceId":"node-01"}`},
		{"/api/v1/relations", `{"sourceId":"node-01","targetId":"vm-01","type":"hosts"}`},
	}
	for _, item := range cases {
		request := httptest.NewRequest(http.MethodPost, item.path, strings.NewReader(item.body))
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("%s status = %d, body = %s", item.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestRelationRejectsReverseHierarchy(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode})
	memory.UpsertResource(domain.Resource{ID: "vm-01", Name: "vm-01", Type: domain.ResourceVM})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/relations", strings.NewReader(`{"sourceId":"vm-01","targetId":"node-01","type":"hosts"}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_relation_hierarchy") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestGroupUpdateValidatesDynamicSelector(t *testing.T) {
	memory := store.NewMemory()
	memory.PutGroup(domain.Group{ID: "group-01", Name: "Rack", Type: "rack", Mode: "static"})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/groups/group-01", strings.NewReader(`{"name":"Database","type":"service","mode":"dynamic"}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "selector_required") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestDynamicGroupRejectsEmptySelectorValues(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(`{"name":"Unsafe","type":"service","mode":"dynamic","selector":{"role":""}}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_selector") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestOverviewAggregatesNestedGroupMembers(t *testing.T) {
	memory := store.NewMemory()
	memory.PutGroup(domain.Group{ID: "production", Name: "Production", Type: "environment", Mode: "static"})
	memory.PutGroup(domain.Group{ID: "rack-01", Name: "Rack 01", Type: "rack", ParentID: "production", Mode: "static"})
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, Health: domain.HealthCritical})
	memory.PutMembership(domain.GroupMembership{ID: "member", GroupID: "rack-01", ResourceID: "node-01"})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview?groupType=environment", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"id":"production"`) || !strings.Contains(body, `"total":1`) || !strings.Contains(body, `"critical":1`) || !strings.Contains(body, `"groupId":"production"`) {
		t.Fatalf("overview = %s", body)
	}
}

func TestMetricSummary(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertAgent(domain.Agent{ID: "agent-node-01", NodeID: "node-01", Hostname: "node-01", Status: "online", LastSeenAt: time.Now().UTC()})
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-node-01"})
	memory.UpsertResource(domain.Resource{ID: "node-02", Name: "node-02", Type: domain.ResourceVM, AgentID: "agent-node-01"})
	handler := New(memory, "test-token", "").Handler()
	for _, body := range []string{
		`{"resourceId":"node-01","cpu":40,"memory":50,"disk":60,"networkRx":100,"networkTx":200}`,
		`{"resourceId":"node-02","cpu":80,"memory":70,"disk":40,"networkRx":300,"networkTx":400}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/metrics", strings.NewReader(body))
		agentAuthorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("ingest status = %d", recorder.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/summary", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"cpuAvg":60`) {
		t.Fatalf("summary = %s", recorder.Body.String())
	}
}

func TestResourcePaginationReturnsNextOffset(t *testing.T) {
	memory := store.NewMemory()
	for index := 0; index < 3; index++ {
		id := fmt.Sprintf("node-%d", index)
		memory.UpsertResource(domain.Resource{ID: id, Name: id, Type: domain.ResourceNode})
	}
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources?limit=2", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"nextOffset":2`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestResourceSearchIncludesTags(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "host", Type: domain.ResourceNode, Tags: map[string]string{"owner": "database"}})
	memory.UpsertResource(domain.Resource{ID: "node-02", Name: "host", Type: domain.ResourceNode, Tags: map[string]string{"owner": "platform"}})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources?q=owner%3Ddatabase", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "node-01") || strings.Contains(recorder.Body.String(), "node-02") {
		t.Fatalf("search = %s", recorder.Body.String())
	}
}

func TestManualResourceLifecycle(t *testing.T) {
	memory := store.NewMemory()
	handler := New(memory, "test-token", "").Handler()
	create := httptest.NewRequest(http.MethodPost, "/api/v1/resources", strings.NewReader(`{"name":"legacy-node","type":"node","health":"unknown","tags":{"environment":"production"}}`))
	authorize(create)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, create)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var created domain.Resource
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	update := httptest.NewRequest(http.MethodPut, "/api/v1/resources/"+created.ID, strings.NewReader(`{"name":"legacy-node-01","type":"node","health":"maintenance"}`))
	authorize(update)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, update)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "legacy-node-01") {
		t.Fatalf("update status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	remove := httptest.NewRequest(http.MethodDelete, "/api/v1/resources/"+created.ID, nil)
	authorize(remove)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, remove)
	if recorder.Code != http.StatusNoContent || memory.HasResource(created.ID) {
		t.Fatalf("delete status = %d", recorder.Code)
	}
}

func TestAgentManagedResourceCannotBeDeletedDirectly(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-01"})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/resources/node-01", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestTerminalRejectsAgentChildResource(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertAgent(domain.Agent{ID: "agent-01", NodeID: "node-01", Hostname: "node-01"})
	memory.UpsertResource(domain.Resource{ID: "process-01", Name: "worker", Type: domain.ResourceProcess, AgentID: "agent-01"})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions", strings.NewReader(`{"targetId":"process-01","reason":"Test"}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "agent host node") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAlertIncidentAndOperationCreation(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-01"})
	handler := New(memory, "test-token", "").Handler()
	cases := []struct{ path, body string }{
		{"/api/v1/alerts", `{"name":"CPU saturation","resourceId":"node-01","severity":"critical"}`},
		{"/api/v1/incidents", `{"title":"Payment degradation","severity":"critical","resourceIds":["node-01"]}`},
		{"/api/v1/operations", `{"type":"service.restart","targetIds":["node-01"],"parameters":{"service":"containerd.service"},"reason":"Incident mitigation","requestedBy":"operator-01","approvedBy":"commander-01"}`},
	}
	for _, item := range cases {
		request := httptest.NewRequest(http.MethodPost, item.path, strings.NewReader(item.body))
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("%s status = %d, body = %s", item.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestServiceOperationRequiresValidService(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-node-01"})
	handler := New(memory, "test-token", "").Handler()
	for _, service := range []string{"", "../../unsafe"} {
		body := fmt.Sprintf(`{"type":"service.status","targetIds":["node-01"],"parameters":{"service":%q},"reason":"Diagnosis"}`, service)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/operations", strings.NewReader(body))
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("service = %q status = %d body = %s", service, recorder.Code, recorder.Body.String())
		}
	}
}

func TestMutationRequiresSubject(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(`{"name":"Rack-01","type":"rack"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestAgentOperationWorkflow(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	enroll := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"test-token","hostname":"node-01","version":"0.1.0"}`))
	enrollRecorder := httptest.NewRecorder()
	handler.ServeHTTP(enrollRecorder, enroll)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/operations", strings.NewReader(`{"type":"inventory.refresh","targetIds":["node-node-01"],"reason":"Inventory reconciliation","requestedBy":"operator-01"}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d", recorder.Code)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/operations/claim", nil)
	agentAuthorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), created.ID) {
		t.Fatalf("claim status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-node-01/operations/"+created.ID, strings.NewReader(`{"status":"succeeded","result":"ok","error":""}`))
	agentAuthorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"succeeded"`) {
		t.Fatalf("complete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestOperationRequesterComesFromIdentity(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-01"})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/operations", strings.NewReader(`{"type":"inventory.refresh","targetIds":["node-01"],"reason":"Test","requestedBy":"spoofed"}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"requestedBy":"admin"`) || strings.Contains(recorder.Body.String(), "spoofed") {
		t.Fatalf("requester identity mismatch: %s", recorder.Body.String())
	}
}

func TestOperationApprovalRequiresIndependentIdentity(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-01"})
	server := New(memory, "test-token", "")
	bindApprover(server)
	bindOperator(server)
	handler := server.Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/operations", strings.NewReader(`{"type":"service.restart","targetIds":["node-01"],"parameters":{"service":"containerd.service"},"reason":"Incident mitigation"}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var operation domain.Operation
	if err := json.Unmarshal(recorder.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	if operation.Status != "awaiting_approval" {
		t.Fatalf("status = %s", operation.Status)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/operations/"+operation.ID+"/approve", nil)
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("self approval status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/operations/"+operation.ID+"/approve", nil)
	request.Header.Set("X-KloudView-Subject", "test-approver")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"pending"`) || !strings.Contains(recorder.Body.String(), `"approvedBy":"test-approver"`) {
		t.Fatalf("independent approval status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestOperationRejectsMultipleTargets(t *testing.T) {
	memory := store.NewMemory()
	for _, id := range []string{"node-01", "node-02"} {
		memory.UpsertResource(domain.Resource{ID: id, Name: id, Type: domain.ResourceNode, AgentID: "agent-" + id})
	}
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/operations", strings.NewReader(`{"type":"inventory.refresh","targetIds":["node-01","node-02"],"reason":"Test"}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "exactly one target") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestOperationUsesActualResourceScope(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-01", Tags: map[string]string{"environment": "staging"}})
	memory.PutGroup(domain.Group{ID: "staging", Name: "Staging", Type: "environment", Path: "staging", Mode: "static"})
	memory.PutMembership(domain.GroupMembership{ID: "member", GroupID: "staging", ResourceID: "node-01"})
	server := New(memory, "test-token", "")
	bindOperator(server)
	handler := server.Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/operations", strings.NewReader(`{"type":"inventory.refresh","targetIds":["node-01"],"reason":"Test"}`))
	request.Header.Set("X-KloudView-Subject", "test-operator")
	request.Header.Set("X-KloudView-Scope", "production")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "target resource scope") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAlertRuleRunbookTerminalAndAudit(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertAgent(domain.Agent{ID: "agent-node-01", NodeID: "node-01", Hostname: "node-01"})
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-node-01"})
	handler := New(memory, "test-token", "").Handler()
	for _, item := range []struct{ path, body string }{
		{"/api/v1/alert-rules", `{"name":"CPU saturation","metric":"cpu","operator":">","threshold":90,"duration":"5m","severity":"critical","enabled":true}`},
		{"/api/v1/runbooks", `{"name":"Refresh inventory","risk":"low","steps":[{"name":"Collect","operation":"inventory.refresh"}]}`},
	} {
		request := httptest.NewRequest(http.MethodPost, item.path, strings.NewReader(item.body))
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("%s status = %d, body = %s", item.path, recorder.Code, recorder.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions", strings.NewReader(`{"targetId":"node-01","reason":"Incident diagnosis"}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("terminal status = %d", recorder.Code)
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	// admin is an administrator (*:*), so it holds terminal:approve-self and
	// may approve its own session; the session becomes active.
	request = httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions/"+session.ID+"/approve", nil)
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin self approval status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions/"+session.ID+"/commands", strings.NewReader(`{"command":"uptime"}`))
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("command status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var command domain.TerminalCommand
	if err := json.Unmarshal(recorder.Body.Bytes(), &command); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/terminal/claim", nil)
	agentAuthorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"command":"uptime"`) {
		t.Fatalf("claim status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-node-01/terminal/commands/"+command.ID, strings.NewReader(`{"status":"succeeded","output":"up 10 days"}`))
	agentAuthorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "up 10 days") {
		t.Fatalf("completion status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/audit-events", nil)
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "/approve") {
		t.Fatalf("audit status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestMetricRuleFiresAndResolvesAlert(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, Health: domain.HealthHealthy, AgentID: "agent-node-01"})
	handler := New(memory, "test-token", "").Handler()
	rule := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", strings.NewReader(`{"name":"CPU high","metric":"cpu","operator":">","threshold":80,"duration":"0s","severity":"critical","enabled":true}`))
	authorize(rule)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, rule)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("rule status = %d", recorder.Code)
	}
	metric := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/metrics", strings.NewReader(`{"resourceId":"node-01","cpu":95}`))
	agentAuthorize(metric)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, metric)
	list := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	authorize(list)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, list)
	if !strings.Contains(recorder.Body.String(), `"status":"firing"`) {
		t.Fatalf("firing alert missing: %s", recorder.Body.String())
	}
	resources := httptest.NewRequest(http.MethodGet, "/api/v1/resources?health=critical", nil)
	authorize(resources)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, resources)
	if !strings.Contains(recorder.Body.String(), `"id":"node-01"`) || !strings.Contains(recorder.Body.String(), `"health":"critical"`) {
		t.Fatalf("effective resource health missing: %s", recorder.Body.String())
	}
	metric = httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/metrics", strings.NewReader(`{"resourceId":"node-01","cpu":20}`))
	agentAuthorize(metric)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, metric)
	list = httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	authorize(list)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, list)
	if !strings.Contains(recorder.Body.String(), `"status":"resolved"`) {
		t.Fatalf("resolved alert missing: %s", recorder.Body.String())
	}
}

func TestDeletingAlertRuleResolvesActiveAlert(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-node-01"})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", strings.NewReader(`{"name":"CPU high","metric":"cpu","operator":">","threshold":80,"duration":"0s","severity":"critical","enabled":true}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var rule domain.AlertRule
	if err := json.Unmarshal(recorder.Body.Bytes(), &rule); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/metrics", strings.NewReader(`{"resourceId":"node-01","cpu":95}`))
	agentAuthorize(request)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	request = httptest.NewRequest(http.MethodDelete, "/api/v1/alert-rules/"+rule.ID, nil)
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete rule status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	alerts := memory.ListAlerts()
	if len(alerts) != 1 || alerts[0].Status != "resolved" {
		t.Fatalf("alerts after rule deletion = %+v", alerts)
	}
}

func TestAlertRuleRejectsInvalidRanges(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	for _, body := range []string{
		`{"name":"Negative duration","metric":"cpu","operator":">","threshold":80,"duration":"-1m","severity":"critical","enabled":true}`,
		`{"name":"CPU range","metric":"cpu","operator":">","threshold":101,"duration":"1m","severity":"critical","enabled":true}`,
		`{"name":"Severity","metric":"cpu","operator":">","threshold":80,"duration":"1m","severity":"emergency","enabled":true}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", strings.NewReader(body))
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("rule = %s status = %d body = %s", body, recorder.Code, recorder.Body.String())
		}
	}
}

func TestAlertSilenceLifecycleAndSuppression(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-node-01"})
	memory.UpsertResource(domain.Resource{ID: "node-02", Name: "node-02", Type: domain.ResourceNode, AgentID: "agent-node-02"})
	handler := New(memory, "test-token", "").Handler()
	rule := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", strings.NewReader(`{"name":"CPU high","metric":"cpu","operator":">","threshold":80,"duration":"0s","severity":"critical","enabled":true}`))
	authorize(rule)
	handler.ServeHTTP(httptest.NewRecorder(), rule)

	metric := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/metrics", strings.NewReader(`{"resourceId":"node-01","cpu":95}`))
	agentAuthorize(metric)
	handler.ServeHTTP(httptest.NewRecorder(), metric)

	start := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	end := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	body := fmt.Sprintf(`{"name":"Maintenance","startsAt":%q,"endsAt":%q}`, start, end)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alert-silences", strings.NewReader(body))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create silence status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var silence domain.AlertSilence
	if err := json.Unmarshal(recorder.Body.Bytes(), &silence); err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	authorize(list)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, list)
	if !strings.Contains(recorder.Body.String(), `"status":"silenced"`) {
		t.Fatalf("silenced alert missing: %s", recorder.Body.String())
	}

	metric = httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-02/metrics", strings.NewReader(`{"resourceId":"node-02","cpu":99}`))
	agentAuthorize(metric)
	handler.ServeHTTP(httptest.NewRecorder(), metric)
	list = httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	authorize(list)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, list)
	if strings.Contains(recorder.Body.String(), `"resourceId":"node-02"`) {
		t.Fatalf("alert created during silence: %s", recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/v1/alert-silences/"+silence.ID, nil)
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete silence status = %d", recorder.Code)
	}
}

func TestNetworkRateRuleFiresAlert(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-node-01"})
	handler := New(memory, "test-token", "").Handler()
	rule := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", strings.NewReader(`{"name":"Network receive high","metric":"network_rx_rate","operator":">","threshold":50,"duration":"0s","severity":"warning","enabled":true}`))
	authorize(rule)
	handler.ServeHTTP(httptest.NewRecorder(), rule)
	base := time.Now().UTC().Add(-30 * time.Second)
	for _, body := range []string{
		fmt.Sprintf(`{"resourceId":"node-01","timestamp":%q,"networkRx":1000,"networkTx":1000}`, base.Format(time.RFC3339)),
		fmt.Sprintf(`{"resourceId":"node-01","timestamp":%q,"networkRx":2000,"networkTx":1100}`, base.Add(10*time.Second).Format(time.RFC3339)),
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/metrics", strings.NewReader(body))
		agentAuthorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("metric status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Network receive high") || !strings.Contains(recorder.Body.String(), "observed 100.00") {
		t.Fatalf("alerts = %s", recorder.Body.String())
	}
}

func TestAgentInventoryCreatesRuntimeResources(t *testing.T) {
	memory := store.NewMemory()
	handler := New(memory, "test-token", "").Handler()
	enroll := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"test-token","hostname":"node-01","version":"0.1.0"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, enroll)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/inventory", strings.NewReader(`{"hostname":"node-01","vms":[{"name":"vm-01","state":"running"}],"containers":[{"id":"abc","name":"api","state":"running","runtime":"containerd"}],"processes":[{"pid":42,"name":"worker"}]}`))
	agentAuthorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("inventory status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	resources := httptest.NewRequest(http.MethodGet, "/api/v1/resources", nil)
	authorize(resources)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, resources)
	body := recorder.Body.String()
	if !strings.Contains(body, "vm-01") || !strings.Contains(body, "api") || !strings.Contains(body, "worker") || !strings.Contains(body, `"type":"hypervisor"`) || !strings.Contains(body, `"type":"process"`) {
		t.Fatalf("runtime resources missing: %s", body)
	}
	vmID := stableID("vm", "node-node-01-vm-01")
	containerID := stableID("container", "node-node-01-abc")
	relations := memory.ListRelations()
	if len(relations) != 3 {
		t.Fatalf("runtime relations = %+v", relations)
	}
	if relation, ok := memory.Relation(stableID("relation", "node-node-01-hosts-"+vmID)); !ok || relation.Type != "hosts" {
		t.Fatalf("vm relation = %+v", relation)
	}
	container, ok := memory.Resource(containerID)
	if !ok || container.Attributes["runtime"] != "containerd" || container.Attributes["state"] != "running" {
		t.Fatalf("container attributes = %+v", container.Attributes)
	}
}

// Inventory identifiers and byte counters survive decode without float64
// rounding or exponent formatting, in the attribute and in the derived ID.
func TestAgentInventoryKeepsLargeNumbersIntact(t *testing.T) {
	memory := store.NewMemory()
	handler := New(memory, "test-token", "").Handler()
	enroll := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"test-token","hostname":"node-01","version":"0.1.0"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, enroll)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/inventory",
		strings.NewReader(`{"hostname":"node-01","processes":[{"pid":3093176,"name":"sh","rssBytes":3948544}]}`))
	agentAuthorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("inventory status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	id := stableID("process", "node-node-01-3093176")
	process, ok := memory.Resource(id)
	if !ok {
		t.Fatalf("process %s not found; ids = %+v", id, resourceIDs(memory))
	}
	if process.Attributes["pid"] != "3093176" {
		t.Errorf("pid attribute = %q, want %q", process.Attributes["pid"], "3093176")
	}
	if process.Attributes["rssBytes"] != "3948544" {
		t.Errorf("rssBytes attribute = %q, want %q", process.Attributes["rssBytes"], "3948544")
	}
}

func resourceIDs(memory *store.Memory) []string {
	var ids []string
	for _, resource := range memory.ListResources() {
		ids = append(ids, resource.ID)
	}
	return ids
}

// The agent's advertised set and this allowlist live in different modules and
// must agree; a missing name fails every heartbeat.
func TestAgentCapabilityAllowlistCoversWhatTheAgentAdvertises(t *testing.T) {
	// Mirrors capabilities() in apps/agent/internal/client/client.go.
	for _, capabilities := range [][]string{
		{"inventory", "metrics"},
		{"inventory", "metrics", "terminal"},
		{"inventory", "metrics", "logs"},
		{"inventory", "metrics", "terminal", "logs"},
	} {
		if err := validateAgentMetadata("node-01", "0.2.4", agentProtocolVersion, capabilities, nil); err != nil {
			t.Errorf("capabilities %v rejected: %v", capabilities, err)
		}
	}
	if err := validateAgentMetadata("node-01", "0.2.4", agentProtocolVersion, []string{"root-shell"}, nil); err == nil {
		t.Error("an unknown capability must still be rejected")
	}
}

func TestRunbookExecutionReleasesStepsSequentially(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	enroll := httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(`{"token":"test-token","hostname":"node-01"}`))
	handler.ServeHTTP(httptest.NewRecorder(), enroll)
	create := httptest.NewRequest(http.MethodPost, "/api/v1/runbooks", strings.NewReader(`{"name":"Two steps","risk":"low","steps":[{"name":"First","operation":"inventory.refresh"},{"name":"Second","operation":"inventory.refresh"}]}`))
	authorize(create)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, create)
	var runbook struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &runbook); err != nil {
		t.Fatal(err)
	}
	execute := httptest.NewRequest(http.MethodPost, "/api/v1/runbooks/"+runbook.ID+"/execute", strings.NewReader(`{"targetIds":["node-node-01"],"reason":"Test"}`))
	authorize(execute)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, execute)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("execute status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	claim := func() string {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/operations/claim", nil)
		agentAuthorize(request)
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != http.StatusOK {
			t.Fatalf("claim status = %d", result.Code)
		}
		var operation struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(result.Body.Bytes(), &operation); err != nil {
			t.Fatal(err)
		}
		return operation.ID
	}
	complete := func(id string) {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-node-01/operations/"+id, strings.NewReader(`{"status":"succeeded","result":"ok"}`))
		agentAuthorize(request)
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != http.StatusOK {
			t.Fatalf("complete status = %d", result.Code)
		}
	}
	first := claim()
	complete(first)
	second := claim()
	if first == second {
		t.Fatal("same operation claimed twice")
	}
	complete(second)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/runbook-executions", nil)
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !strings.Contains(recorder.Body.String(), `"status":"succeeded"`) {
		t.Fatalf("execution not succeeded: %s", recorder.Body.String())
	}
}

func TestRunbookRejectsInvalidRiskAndUnnamedSteps(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	tests := []string{
		`{"name":"Invalid risk","risk":"urgent","steps":[{"name":"Collect","operation":"inventory.refresh"}]}`,
		`{"name":"Unnamed step","risk":"low","steps":[{"operation":"inventory.refresh"}]}`,
	}
	for _, body := range tests {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/runbooks", strings.NewReader(body))
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body = %s, status = %d, response = %s", body, recorder.Code, recorder.Body.String())
		}
	}
}

func TestAlertRuleUsesHierarchyScope(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode, AgentID: "agent-node-01", Tags: map[string]string{"environment": "production"}})
	memory.PutGroup(domain.Group{ID: "production", Name: "Production", Type: "environment", Path: "production"})
	memory.PutMembership(domain.GroupMembership{ID: "member", GroupID: "production", ResourceID: "node-01"})
	handler := New(memory, "test-token", "").Handler()
	for _, scope := range []string{"staging", "production"} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", strings.NewReader(`{"name":"CPU `+scope+`","metric":"cpu","operator":">","threshold":80,"duration":"0s","severity":"critical","scopePath":"`+scope+`","selector":{"environment":"production"},"enabled":true}`))
		authorize(request)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	metric := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-node-01/metrics", strings.NewReader(`{"resourceId":"node-01","cpu":95}`))
	agentAuthorize(metric)
	handler.ServeHTTP(httptest.NewRecorder(), metric)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/alerts", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if !strings.Contains(body, "CPU production") || strings.Contains(body, "CPU staging") {
		t.Fatalf("scope mismatch: %s", body)
	}
}

func TestIncidentTimeline(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", strings.NewReader(`{"title":"Database latency","severity":"critical","resourceIds":["node-01"]}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var incident struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &incident); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+incident.ID+"/events", strings.NewReader(`{"type":"note","message":"Failover started"}`))
	authorize(request)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	request = httptest.NewRequest(http.MethodGet, "/api/v1/incidents/"+incident.ID+"/events", nil)
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if !strings.Contains(body, "Incident declared") || !strings.Contains(body, "Failover started") {
		t.Fatalf("timeline missing: %s", body)
	}
}

func TestIncidentDerivesResourceFromRelatedAlert(t *testing.T) {
	memory := store.NewMemory()
	memory.UpsertResource(domain.Resource{ID: "node-01", Name: "node-01", Type: domain.ResourceNode})
	memory.PutAlert(domain.Alert{ID: "alert-01", Name: "CPU high", Severity: "critical", Status: "firing", ResourceID: "node-01"})
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/incidents", strings.NewReader(`{"title":"Compute impact","severity":"critical","alertIds":["alert-01"]}`))
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"resourceIds":["node-01"]`) {
		t.Fatalf("derived incident = %d %s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/incidents", strings.NewReader(`{"title":"Invalid alert","severity":"warning","alertIds":["missing"]}`))
	authorize(request)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing alert status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAccessControlCRUD(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	requestJSON := func(method, path, body string) (int, []byte) {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.Bytes()
	}
	status, body := requestJSON(http.MethodPost, "/api/v1/roles", `{"name":"Rack Operator","permissions":[{"resource":"groups","action":"read"}]}`)
	if status != http.StatusCreated {
		t.Fatalf("role create status = %d, body = %s", status, body)
	}
	var role struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &role); err != nil {
		t.Fatal(err)
	}
	status, body = requestJSON(http.MethodPut, "/api/v1/roles/"+role.ID, `{"name":"Rack Maintainer","permissions":[{"resource":"groups","action":"*"}]}`)
	if status != http.StatusOK || !strings.Contains(string(body), "Rack Maintainer") {
		t.Fatalf("role update status = %d, body = %s", status, body)
	}
	status, body = requestJSON(http.MethodPost, "/api/v1/scopes", `{"name":"Rack 07","paths":["production/rack-07"]}`)
	var scope struct {
		ID string `json:"id"`
	}
	if status != http.StatusCreated || json.Unmarshal(body, &scope) != nil {
		t.Fatalf("scope create status = %d, body = %s", status, body)
	}
	status, body = requestJSON(http.MethodPost, "/api/v1/role-bindings", `{"subjectId":"operator-01","roleId":"`+role.ID+`","scopeId":"`+scope.ID+`"}`)
	var binding struct {
		ID string `json:"id"`
	}
	if status != http.StatusCreated || json.Unmarshal(body, &binding) != nil {
		t.Fatalf("binding create status = %d, body = %s", status, body)
	}
	status, _ = requestJSON(http.MethodDelete, "/api/v1/roles/"+role.ID, "")
	if status != http.StatusConflict {
		t.Fatalf("referenced role delete status = %d", status)
	}
	status, body = requestJSON(http.MethodPut, "/api/v1/role-bindings/"+binding.ID, `{"subjectId":"operator-02","roleId":"`+role.ID+`","scopeId":"`+scope.ID+`"}`)
	if status != http.StatusOK || !strings.Contains(string(body), "operator-02") {
		t.Fatalf("binding update status = %d, body = %s", status, body)
	}
	if status, _ = requestJSON(http.MethodDelete, "/api/v1/role-bindings/"+binding.ID, ""); status != http.StatusNoContent {
		t.Fatalf("binding delete status = %d", status)
	}
	if status, _ = requestJSON(http.MethodDelete, "/api/v1/roles/"+role.ID, ""); status != http.StatusNoContent {
		t.Fatalf("role delete status = %d", status)
	}
	if status, _ = requestJSON(http.MethodDelete, "/api/v1/scopes/"+scope.ID, ""); status != http.StatusNoContent {
		t.Fatalf("scope delete status = %d", status)
	}
}

func TestRoleRejectsInvalidAndDuplicatePermissions(t *testing.T) {
	handler := New(store.NewMemory(), "test-token", "").Handler()
	for _, body := range []string{
		`{"name":"Invalid","permissions":[{"resource":"resources","action":"grant"}]}`,
		`{"name":"Duplicate","permissions":[{"resource":"groups","action":"read"},{"resource":"groups","action":"read"}]}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/roles", strings.NewReader(body))
		authorize(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body = %s, status = %d, response = %s", body, recorder.Code, recorder.Body.String())
		}
	}
}

func TestAuditEventsSupportBoundedPagination(t *testing.T) {
	memory := store.NewMemory()
	for index := 0; index < 3; index++ {
		memory.AddAudit(domain.AuditEvent{ID: fmt.Sprintf("audit-%d", index), Timestamp: time.Date(2026, 1, 1, 0, 0, index, 0, time.UTC), Actor: "operator"})
	}
	handler := New(memory, "test-token", "").Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/audit-events?limit=2&offset=1", nil)
	authorize(request)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"total":3`) || !strings.Contains(recorder.Body.String(), `"offset":1`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		Items []domain.AuditEvent `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 || result.Items[0].ID != "audit-1" {
		t.Fatalf("items = %+v", result.Items)
	}
}

func TestAuditEventsAreFilteredByRecordedScope(t *testing.T) {
	memory := store.NewMemory()
	now := time.Now().UTC()
	memory.UpsertResource(domain.Resource{ID: "node-production", Name: "node-production", Type: domain.ResourceNode})
	memory.PutGroup(domain.Group{ID: "production", Name: "Production", Type: "environment", Path: "production"})
	memory.PutMembership(domain.GroupMembership{ID: "member-production", GroupID: "production", ResourceID: "node-production"})
	memory.AddAudit(domain.AuditEvent{ID: "production", Timestamp: now, Actor: "operator", Metadata: map[string]string{"scope": "production/rack-01"}})
	memory.AddAudit(domain.AuditEvent{ID: "agent-production", Timestamp: now.Add(-time.Second), Actor: "agent", Metadata: map[string]string{"resourceId": "node-production"}})
	memory.AddAudit(domain.AuditEvent{ID: "staging", Timestamp: now.Add(-2 * time.Second), Actor: "operator", Metadata: map[string]string{"scope": "staging"}})
	memory.AddAudit(domain.AuditEvent{ID: "unscoped", Timestamp: now.Add(-3 * time.Second), Actor: "agent"})
	server := New(memory, "test-token", "")
	bindOperator(server)
	handler := server.Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/audit-events", nil)
	request.Header.Set("X-KloudView-Subject", "test-operator")
	request.Header.Set("X-KloudView-Scope", "production")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"id":"production"`) || !strings.Contains(body, `"id":"agent-production"`) || strings.Contains(body, `"id":"staging"`) || strings.Contains(body, `"id":"unscoped"`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, body)
	}
}

func authorize(request *http.Request) {
	request.Header.Set("X-KloudView-Subject", "admin")
}

func agentAuthorize(request *http.Request) {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	agentID := ""
	for index, part := range parts {
		if part == "agents" && index+1 < len(parts) {
			agentID = parts[index+1]
			break
		}
	}
	mac := hmac.New(sha256.New, []byte("test-token"))
	mac.Write([]byte(agentID))
	request.Header.Set("Authorization", "Bearer "+base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
}

// bindTestSubject grants a subject a role over a scope. The server seeds only
// "admin"; the restricted identities below exist to exercise scope limits and
// independent-approval rules.
func bindTestSubject(server *Server, subject, roleID, scopeID string) {
	server.access.PutBinding(access.Binding{
		ID: "binding-" + subject, SubjectID: subject, RoleID: roleID, ScopeID: scopeID,
	})
}

func bindOperator(server *Server) {
	bindTestSubject(server, "test-operator", "role-operator", "scope-production")
}
func bindViewer(server *Server) {
	bindTestSubject(server, "test-viewer", "role-viewer", "scope-global")
}
func bindApprover(server *Server) {
	bindTestSubject(server, "test-approver", "role-admin", "scope-global")
}

func TestNodeAddressesPrefersPhysicalIPv4(t *testing.T) {
	interfaces := []any{
		map[string]any{"name": "lo", "addresses": []any{"127.0.0.1/8", "::1/128"}},
		map[string]any{"name": "docker0", "addresses": []any{"172.17.0.1/16"}},
		map[string]any{"name": "enp5s0", "addresses": []any{"10.20.0.23/16", "fe80::8ac9:b3ff:febe:a11/64"}},
	}
	primary, all := nodeAddresses(interfaces)
	if primary != "10.20.0.23" {
		t.Fatalf("primary = %q, want the address on the physical interface", primary)
	}
	// Loopback and link-local identify no host, so they are not listed either.
	if all != "172.17.0.1, 10.20.0.23" {
		t.Fatalf("all = %q", all)
	}
}

func TestNodeAddressesFallsBackWhenOnlyVirtual(t *testing.T) {
	primary, _ := nodeAddresses([]any{
		map[string]any{"name": "lo", "addresses": []any{"127.0.0.1/8"}},
		map[string]any{"name": "br-4b58", "addresses": []any{"172.18.0.1/16"}},
	})
	if primary != "172.18.0.1" {
		t.Fatalf("primary = %q, want the bridge address rather than none", primary)
	}
}

func TestNodeAddressesHandlesMissingInterfaces(t *testing.T) {
	if primary, all := nodeAddresses(nil); primary != "" || all != "" {
		t.Fatalf("nodeAddresses(nil) = %q, %q", primary, all)
	}
}
