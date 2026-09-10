package api

import "net/http"

func (s *Server) registerAccessRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/audit-events", s.require("audit", "read", http.HandlerFunc(s.listAuditEvents)))
	mux.HandleFunc("POST /api/v1/access/evaluate", s.evaluateAccess)
	mux.Handle("GET /api/v1/roles", s.require("roles", "read", http.HandlerFunc(s.listRoles)))
	mux.Handle("POST /api/v1/roles", s.require("roles", "create", http.HandlerFunc(s.createRole)))
	mux.Handle("PUT /api/v1/roles/{id}", s.require("roles", "update", http.HandlerFunc(s.updateRole)))
	mux.Handle("DELETE /api/v1/roles/{id}", s.require("roles", "delete", http.HandlerFunc(s.deleteRole)))
	mux.Handle("GET /api/v1/scopes", s.require("scopes", "read", http.HandlerFunc(s.listScopes)))
	mux.Handle("POST /api/v1/scopes", s.require("scopes", "create", http.HandlerFunc(s.createScope)))
	mux.Handle("PUT /api/v1/scopes/{id}", s.require("scopes", "update", http.HandlerFunc(s.updateScope)))
	mux.Handle("DELETE /api/v1/scopes/{id}", s.require("scopes", "delete", http.HandlerFunc(s.deleteScope)))
	mux.Handle("GET /api/v1/role-bindings", s.require("bindings", "read", http.HandlerFunc(s.listBindings)))
	mux.Handle("POST /api/v1/role-bindings", s.require("bindings", "create", http.HandlerFunc(s.createBinding)))
	mux.Handle("PUT /api/v1/role-bindings/{id}", s.require("bindings", "update", http.HandlerFunc(s.updateBinding)))
	mux.Handle("DELETE /api/v1/role-bindings/{id}", s.require("bindings", "delete", http.HandlerFunc(s.deleteBinding)))
}
