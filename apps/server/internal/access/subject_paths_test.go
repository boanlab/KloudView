package access

import (
	"reflect"
	"testing"
	"time"
)

// A client names the scope it is asking about on every request, and only a scope
// the identity is bound to is accepted. Without being told which those are, a
// client guesses, and a wrong guess is refused on every call.
func TestASubjectLearnsWhichScopesItHolds(t *testing.T) {
	engine := NewEngine()
	engine.PutRole(Role{ID: "reader", Permissions: []Permission{{Resource: "*", Action: "read"}}})
	engine.PutScope(Scope{ID: "one", Paths: []string{"region-b"}})
	engine.PutScope(Scope{ID: "two", Paths: []string{"region-a", "region-b"}})
	engine.PutScope(Scope{ID: "everything", Paths: []string{"*"}})
	engine.PutBinding(Binding{ID: "b1", SubjectID: "scoped", RoleID: "reader", ScopeID: "one"})
	engine.PutBinding(Binding{ID: "b2", SubjectID: "scoped", RoleID: "reader", ScopeID: "two"})
	engine.PutBinding(Binding{ID: "b3", SubjectID: "boss", RoleID: "reader", ScopeID: "everything"})

	if got, want := engine.PathsForSubject("scoped"), []string{"region-a", "region-b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("scoped holds %v, want %v", got, want)
	}
	// The unrestricted path comes first, so a client picking the first one gets
	// the widest scope it is entitled to.
	if got, want := engine.PathsForSubject("boss"), []string{"*"}; !reflect.DeepEqual(got, want) {
		t.Errorf("boss holds %v, want %v", got, want)
	}
	if got := engine.PathsForSubject("nobody"); len(got) != 0 {
		t.Errorf("an unbound subject holds %v, want nothing", got)
	}

	// An expired binding grants nothing, so it names nothing either.
	past := time.Now().UTC().Add(-time.Hour)
	engine.PutBinding(Binding{ID: "b4", SubjectID: "former", RoleID: "reader", ScopeID: "one", ExpiresAt: &past})
	if got := engine.PathsForSubject("former"); len(got) != 0 {
		t.Errorf("an expired binding still names %v", got)
	}

	// Every path it reports is one the engine will actually accept.
	for _, path := range engine.PathsForSubject("scoped") {
		if !engine.Evaluate(Request{SubjectID: "scoped", Resource: "resources", Action: "read", ResourcePath: path}).Allowed {
			t.Errorf("path %q was reported but is refused", path)
		}
	}
}
