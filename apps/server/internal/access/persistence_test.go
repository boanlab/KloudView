package access

import (
	"path/filepath"
	"testing"
)

func TestPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.json")
	engine := NewEngine()
	engine.PutRole(Role{ID: "custom", Name: "Custom", Permissions: []Permission{{Resource: "groups", Action: "read"}}})
	engine.PutScope(Scope{ID: "dc-1", Name: "DC-1", Paths: []string{"production/dc-1"}})
	engine.PutBinding(Binding{ID: "binding", SubjectID: "user", RoleID: "custom", ScopeID: "dc-1"})
	if err := engine.Save(path); err != nil {
		t.Fatal(err)
	}
	restored, err := NewPersistent(path)
	if err != nil {
		t.Fatal(err)
	}
	decision := restored.Evaluate(Request{SubjectID: "user", Resource: "groups", Action: "read", ResourcePath: "production/dc-1/rack-01"})
	if !decision.Allowed {
		t.Fatalf("decision = %+v", decision)
	}
}
