package access

import (
	"testing"
	"time"
)

func TestAdministratorHasGlobalAccess(t *testing.T) {
	engine := NewEngine()
	decision := engine.Evaluate(Request{SubjectID: "admin", Resource: "groups", Action: "delete", ResourcePath: "production/dc-1/rack-07"})
	if !decision.Allowed {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestTerminalSelfApprovalRequiresElevation(t *testing.T) {
	engine := NewEngine()
	// A delegated approver may approve terminals but not self-approve.
	engine.PutRole(Role{ID: "role-approver", Name: "Terminal Approver", Permissions: []Permission{{Resource: "terminal", Action: "approve"}}})
	engine.PutBinding(Binding{ID: "b-approver", SubjectID: "approver-01", RoleID: "role-approver", ScopeID: "scope-global"})
	if !engine.Evaluate(Request{SubjectID: "approver-01", Resource: "terminal", Action: "approve", ResourcePath: "production"}).Allowed {
		t.Fatal("delegated approver should hold terminal:approve")
	}
	if engine.Evaluate(Request{SubjectID: "approver-01", Resource: "terminal", Action: "approve-self", ResourcePath: "production"}).Allowed {
		t.Fatal("delegated approver must not hold terminal:approve-self")
	}
	// Acting alone is never inherited: a role holding every action still does
	// not hold approve-self, because "may approve my own request" is a
	// statement about one person rather than a degree of access.
	if engine.Evaluate(Request{SubjectID: "admin", Resource: "terminal", Action: "approve-self", ResourcePath: "production"}).Allowed {
		t.Fatal("*:* must not carry terminal:approve-self")
	}
	// It is held only where somebody wrote it down.
	engine.PutRole(Role{ID: "role-alone", Name: "Break glass", Permissions: []Permission{
		{Resource: "terminal", Action: "approve"},
		{Resource: "terminal", Action: "approve-self"},
	}})
	engine.PutBinding(Binding{ID: "b-alone", SubjectID: "lone-operator", RoleID: "role-alone", ScopeID: "scope-global"})
	if !engine.Evaluate(Request{SubjectID: "lone-operator", Resource: "terminal", Action: "approve-self", ResourcePath: "production"}).Allowed {
		t.Fatal("an explicit terminal:approve-self should be held")
	}
	// And it says nothing about any other resource.
	if engine.Evaluate(Request{SubjectID: "lone-operator", Resource: "runbooks", Action: "approve-self", ResourcePath: "production"}).Allowed {
		t.Fatal("terminal:approve-self must not carry runbooks:approve-self")
	}
}

func TestScopeLimitsOperator(t *testing.T) {
	engine := NewEngine()
	engine.PutBinding(Binding{ID: "operator", SubjectID: "operator-01", RoleID: "role-operator", ScopeID: "scope-production"})
	allowed := engine.Evaluate(Request{SubjectID: "operator-01", Resource: "operations", Action: "create", ResourcePath: "production/dc-1"})
	denied := engine.Evaluate(Request{SubjectID: "operator-01", Resource: "operations", Action: "create", ResourcePath: "staging/dc-1"})
	if !allowed.Allowed || denied.Allowed {
		t.Fatalf("allowed = %+v, denied = %+v", allowed, denied)
	}
}

func TestExpiredBindingIsDenied(t *testing.T) {
	engine := NewEngine()
	expired := time.Now().Add(-time.Minute)
	engine.PutBinding(Binding{ID: "expired", SubjectID: "viewer-01", RoleID: "role-viewer", ScopeID: "scope-global", ExpiresAt: &expired})
	decision := engine.Evaluate(Request{SubjectID: "viewer-01", Resource: "resources", Action: "read"})
	if decision.Allowed {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestBindingReferencesAndProtectedDeletes(t *testing.T) {
	engine := NewEngine()
	role := engine.PutRole(Role{ID: "role-custom", Name: "Custom", Permissions: []Permission{{Resource: "groups", Action: "read"}}})
	scope := engine.PutScope(Scope{ID: "scope-custom", Name: "Custom", Paths: []string{"production"}})

	if _, err := engine.CreateBinding(Binding{ID: "invalid", SubjectID: "operator", RoleID: "missing", ScopeID: scope.ID}); err == nil {
		t.Fatal("missing role accepted")
	}
	binding, err := engine.CreateBinding(Binding{ID: "binding-custom", SubjectID: "operator", RoleID: role.ID, ScopeID: scope.ID})
	if err != nil || binding.CreatedAt.IsZero() {
		t.Fatalf("binding creation failed: %v", err)
	}
	if err := engine.DeleteRole(role.ID); err == nil {
		t.Fatal("referenced role deleted")
	}
	if err := engine.DeleteScope(scope.ID); err == nil {
		t.Fatal("referenced scope deleted")
	}
	if err := engine.DeleteBinding(binding.ID); err != nil {
		t.Fatal(err)
	}
	if err := engine.DeleteRole(role.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAccessUpdates(t *testing.T) {
	engine := NewEngine()
	engine.PutRole(Role{ID: "role-custom", Name: "Before", Permissions: []Permission{{Resource: "groups", Action: "read"}}})
	engine.PutScope(Scope{ID: "scope-custom", Name: "Before"})
	binding, err := engine.CreateBinding(Binding{ID: "binding-custom", SubjectID: "one", RoleID: "role-custom", ScopeID: "scope-custom"})
	if err != nil {
		t.Fatal(err)
	}
	updatedRole, err := engine.UpdateRole(Role{ID: "role-custom", Name: "After", Permissions: []Permission{{Resource: "groups", Action: "*"}}})
	if err != nil || updatedRole.Name != "After" {
		t.Fatalf("role update failed: %v", err)
	}
	updatedScope, err := engine.UpdateScope(Scope{ID: "scope-custom", Name: "After", Paths: []string{"production"}})
	if err != nil || updatedScope.Name != "After" {
		t.Fatalf("scope update failed: %v", err)
	}
	binding.SubjectID = "two"
	updatedBinding, err := engine.UpdateBinding(binding)
	if err != nil || updatedBinding.SubjectID != "two" || !updatedBinding.CreatedAt.Equal(binding.CreatedAt) {
		t.Fatalf("binding update failed: %v", err)
	}
	if _, err := engine.UpdateRole(Role{ID: "role-admin", Name: "Changed", Permissions: []Permission{{Resource: "*", Action: "*"}}}); err == nil {
		t.Fatal("system role updated")
	}
	if _, err := engine.UpdateScope(Scope{ID: "scope-global", Name: "Changed"}); err == nil {
		t.Fatal("system scope updated")
	}
	if err := engine.DeleteScope("scope-global"); err == nil {
		t.Fatal("system scope deleted")
	}
}
