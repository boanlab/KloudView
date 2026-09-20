package api

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) registerCoreRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if s.storageHealth != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := s.storageHealth(ctx); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "version": s.version})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.version})
	})
	// Authentication (public) and user management (RBAC-gated).
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("GET /api/v1/auth/me", s.me)
	mux.HandleFunc("PUT /api/v1/auth/me", s.updateMe)
	mux.Handle("GET /api/v1/users", s.require("users", "read", http.HandlerFunc(s.listUsers)))
	mux.Handle("POST /api/v1/users", s.require("users", "create", http.HandlerFunc(s.createUser)))
	mux.Handle("PUT /api/v1/users/{id}", s.require("users", "update", http.HandlerFunc(s.updateUser)))
	mux.Handle("DELETE /api/v1/users/{id}", s.require("users", "delete", http.HandlerFunc(s.deleteUser)))
	mux.Handle("GET /api/v1/teams", s.require("teams", "read", http.HandlerFunc(s.listTeams)))
	mux.Handle("POST /api/v1/teams", s.require("teams", "create", http.HandlerFunc(s.createTeam)))
	mux.Handle("PUT /api/v1/teams/{id}", s.require("teams", "update", http.HandlerFunc(s.updateTeam)))
	mux.Handle("DELETE /api/v1/teams/{id}", s.require("teams", "delete", http.HandlerFunc(s.deleteTeam)))
	mux.Handle("GET /api/v1/system/info", s.require("resources", "read", http.HandlerFunc(s.systemInfo)))
	mux.HandleFunc("POST /api/v1/agents/enroll", s.enrollAgent)
	mux.Handle("POST /api/v1/agents/{id}/heartbeat", s.requireAgentKey(http.HandlerFunc(s.heartbeat)))
	mux.Handle("POST /api/v1/agents/{id}/inventory", s.requireAgentKey(http.HandlerFunc(s.putInventory)))
	mux.Handle("POST /api/v1/agents/{id}/operations/claim", s.requireAgentKey(http.HandlerFunc(s.claimOperation)))
	mux.Handle("PUT /api/v1/agents/{id}/operations/{operationId}", s.requireAgentKey(http.HandlerFunc(s.completeOperation)))
	mux.Handle("GET /api/v1/agents", s.require("resources", "read", http.HandlerFunc(s.listAgents)))
	mux.Handle("GET /api/v1/agents/rollout", s.require("agents", "read", http.HandlerFunc(s.getRollout)))
	mux.Handle("DELETE /api/v1/agents/{id}", s.require("agents", "delete", http.HandlerFunc(s.deleteAgent)))
	mux.Handle("GET /api/v1/inventories", s.require("resources", "read", http.HandlerFunc(s.listInventories)))
	mux.Handle("GET /api/v1/agent-releases", s.require("agents", "read", http.HandlerFunc(s.listAgentReleases)))
	mux.HandleFunc("GET /api/v1/agent-releases/{arch}", s.downloadAgentRelease)
	mux.HandleFunc("GET /api/v1/agent-install.sh", s.agentInstallScript)
	mux.Handle("GET /api/v1/enrollment-tokens", s.require("agents", "read", http.HandlerFunc(s.listEnrollmentTokens)))
	mux.Handle("POST /api/v1/enrollment-tokens", s.require("agents", "create", http.HandlerFunc(s.createEnrollmentToken)))
	mux.Handle("DELETE /api/v1/enrollment-tokens/{id}", s.require("agents", "delete", http.HandlerFunc(s.revokeEnrollmentToken)))
	mux.Handle("GET /api/v1/agents/{id}/inventory", s.require("resources", "read", http.HandlerFunc(s.getInventory)))
	mux.Handle("GET /api/v1/resources", s.require("resources", "read", http.HandlerFunc(s.listResources)))
	mux.Handle("GET /api/v1/resources/{id}", s.require("resources", "read", http.HandlerFunc(s.getResource)))
	mux.Handle("POST /api/v1/resources", s.require("resources", "create", http.HandlerFunc(s.createResource)))
	mux.Handle("PUT /api/v1/resources/{id}", s.require("resources", "update", http.HandlerFunc(s.updateResource)))
	mux.Handle("DELETE /api/v1/resources/{id}", s.require("resources", "delete", http.HandlerFunc(s.deleteResource)))
	mux.Handle("GET /api/v1/overview", s.require("resources", "read", http.HandlerFunc(s.overview)))
	mux.Handle("GET /api/v1/utilization", s.require("resources", "read", http.HandlerFunc(s.utilization)))
	mux.Handle("GET /api/v1/groups", s.require("groups", "read", http.HandlerFunc(s.listGroups)))
	mux.Handle("POST /api/v1/groups", s.require("groups", "create", http.HandlerFunc(s.createGroup)))
	mux.Handle("PUT /api/v1/groups/{id}", s.require("groups", "update", http.HandlerFunc(s.updateGroup)))
	mux.Handle("DELETE /api/v1/groups/{id}", s.require("groups", "delete", http.HandlerFunc(s.deleteGroup)))
	mux.Handle("GET /api/v1/memberships", s.require("groups", "read", http.HandlerFunc(s.listMemberships)))
	mux.Handle("POST /api/v1/memberships", s.require("groups", "update", http.HandlerFunc(s.createMembership)))
	mux.Handle("DELETE /api/v1/memberships/{id}", s.require("groups", "update", http.HandlerFunc(s.deleteMembership)))
	mux.Handle("GET /api/v1/relations", s.require("relations", "read", http.HandlerFunc(s.listRelations)))
	mux.Handle("POST /api/v1/relations", s.require("relations", "create", http.HandlerFunc(s.createRelation)))
	mux.Handle("DELETE /api/v1/relations/{id}", s.require("relations", "delete", http.HandlerFunc(s.deleteRelation)))
	mux.Handle("POST /api/v1/agents/{id}/metrics", s.requireAgentKey(http.HandlerFunc(s.ingestMetric)))
	mux.Handle("POST /api/v1/agents/{id}/container-metrics", s.requireAgentKey(http.HandlerFunc(s.ingestContainerMetrics)))
	mux.Handle("POST /api/v1/agents/{id}/vm-metrics", s.requireAgentKey(http.HandlerFunc(s.ingestVMMetrics)))
	mux.Handle("POST /api/v1/agents/{id}/process-metrics", s.requireAgentKey(http.HandlerFunc(s.ingestProcessMetrics)))
	mux.Handle("POST /api/v1/agents/{id}/logs", s.requireAgentKey(http.HandlerFunc(s.ingestLogs)))
	mux.Handle("GET /api/v1/logs/lines", s.require("resources", "read", http.HandlerFunc(s.listLogLines)))
	mux.Handle("GET /api/v1/logs/counters", s.require("resources", "read", http.HandlerFunc(s.listLogCounters)))
	mux.Handle("GET /api/v1/metrics/summary", s.require("metrics", "read", http.HandlerFunc(s.metricSummary)))
	mux.Handle("GET /api/v1/metrics/timeseries", s.require("metrics", "read", http.HandlerFunc(s.metricTimeseries)))
	mux.Handle("GET /api/v1/resources/{id}/metrics", s.require("metrics", "read", http.HandlerFunc(s.listMetrics)))
}
