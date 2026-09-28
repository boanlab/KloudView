package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func enrol(t *testing.T, server *Server, hostname, machineID string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"token":"token","hostname":"` + hostname + `","machineId":"` + machineID +
		`","version":"0.1.0","protocolVersion":"1","capabilities":["metrics"]}`
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(
		http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(body)))
	return recorder
}

// An agent id is the hostname, so two machines called the same thing would
// enrol as one: each overwriting the other's inventory and credential while the
// console showed a single node flickering between two hosts. The machine id is
// what tells them apart.
func TestASecondMachineWithTheSameHostnameIsRefused(t *testing.T) {
	server := New(store.NewMemory(), "token", "")

	if code := enrol(t, server, "node-01", "machine-aaa").Code; code != http.StatusCreated {
		t.Fatalf("first enrolment = %d, want 201", code)
	}
	second := enrol(t, server, "node-01", "machine-bbb")
	if second.Code != http.StatusConflict {
		t.Fatalf("second machine enrolled as the same host: %d %s", second.Code, second.Body.String())
	}
	var failure struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &failure); err != nil {
		t.Fatalf("unreadable error: %v", err)
	}
	if failure.Error.Code != "agent_hostname_taken" {
		t.Errorf("error code = %q", failure.Error.Code)
	}
	// The host that was there first keeps its record.
	agents := server.store.ListAgents()
	if len(agents) != 1 || agents[0].MachineID != "machine-aaa" {
		t.Errorf("fleet = %+v, want only the first machine", agents)
	}
}

// The same host set up again reports the same machine id and takes its own
// record back, which is what makes a full uninstall safe to reinstall from.
func TestTheSameMachineReenrolsIntoItsOwnRecord(t *testing.T) {
	server := New(store.NewMemory(), "token", "")
	enrol(t, server, "node-01", "machine-aaa")
	if code := enrol(t, server, "node-01", "machine-aaa").Code; code != http.StatusCreated {
		t.Fatalf("re-enrolment = %d, want 201", code)
	}
	if agents := server.store.ListAgents(); len(agents) != 1 {
		t.Errorf("re-enrolling made %d agents", len(agents))
	}
}

// A host whose system keeps no machine id, and every agent enrolled before they
// reported one, must still be able to enrol: the check is a refinement of the
// identity, not a new requirement for it.
func TestAHostWithoutAMachineIDStillEnrols(t *testing.T) {
	server := New(store.NewMemory(), "token", "")
	if code := enrol(t, server, "node-01", "").Code; code != http.StatusCreated {
		t.Fatalf("enrolment without a machine id = %d, want 201", code)
	}
	// And one that starts reporting an id adopts it rather than being locked out.
	if code := enrol(t, server, "node-01", "machine-aaa").Code; code != http.StatusCreated {
		t.Fatalf("adopting a machine id = %d, want 201", code)
	}
	agents := server.store.ListAgents()
	if len(agents) != 1 || agents[0].MachineID != "machine-aaa" {
		t.Errorf("fleet = %+v, want the id adopted", agents)
	}
}
