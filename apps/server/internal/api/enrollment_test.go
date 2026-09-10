package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func TestEnrollmentTokenBindings(t *testing.T) {
	future := time.Now().UTC().Add(time.Minute)
	cases := []struct {
		name     string
		token    domain.EnrollmentToken
		hostname string
		addr     string
		allowed  bool
	}{
		{"unbound accepts any host", domain.EnrollmentToken{ExpiresAt: future}, "node-9", "10.20.0.5:52000", true},
		{"hostname matches", domain.EnrollmentToken{Hostname: "ryzen1", ExpiresAt: future}, "RYZEN1", "10.20.0.5:1", true},
		{"hostname mismatch", domain.EnrollmentToken{Hostname: "ryzen1", ExpiresAt: future}, "attacker", "10.20.0.5:1", false},
		{"cidr contains", domain.EnrollmentToken{AllowedCIDR: "10.20.0.0/16", ExpiresAt: future}, "n", "10.20.3.9:44", true},
		{"cidr excludes", domain.EnrollmentToken{AllowedCIDR: "10.20.0.0/16", ExpiresAt: future}, "n", "192.0.2.9:44", false},
		{"invalid cidr denies", domain.EnrollmentToken{AllowedCIDR: "nonsense", ExpiresAt: future}, "n", "10.20.0.1:1", false},
	}
	for _, test := range cases {
		if got := enrollmentAllowed(test.token, test.hostname, test.addr); got != test.allowed {
			t.Errorf("%s: allowed = %v, want %v", test.name, got, test.allowed)
		}
	}
}

func TestIssuedEnrollmentTokenEnrolsOnceWithinItsWindow(t *testing.T) {
	memory := store.NewMemory()
	// No bootstrap token configured: only an issued token can enrol.
	handler := New(memory, "", "").Handler()
	now := time.Now().UTC()
	memory.PutEnrollmentToken(domain.EnrollmentToken{
		ID: "t1", Hash: store.HashEnrollmentToken("issued-value"), Hostname: "node-01",
		MaxUses: 1, CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	})

	enrol := func(hostname, token string) int {
		body := `{"token":"` + token + `","hostname":"` + hostname + `","version":"0.1.0","protocolVersion":"1"}`
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll", strings.NewReader(body)))
		return recorder.Code
	}

	if code := enrol("other-host", "issued-value"); code != http.StatusForbidden {
		t.Fatalf("bound token accepted another host: %d", code)
	}
	if code := enrol("node-01", "issued-value"); code != http.StatusCreated {
		t.Fatalf("bound token refused its own host: %d", code)
	}
	if code := enrol("node-01", "issued-value"); code != http.StatusUnauthorized {
		t.Fatalf("token reused beyond its limit: %d", code)
	}
}

func TestExpiredEnrollmentTokenIsRefused(t *testing.T) {
	memory := store.NewMemory()
	handler := New(memory, "", "").Handler()
	now := time.Now().UTC()
	memory.PutEnrollmentToken(domain.EnrollmentToken{
		ID: "old", Hash: store.HashEnrollmentToken("stale"),
		CreatedAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(-time.Second),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/agents/enroll",
		strings.NewReader(`{"token":"stale","hostname":"node-01","version":"0.1.0","protocolVersion":"1"}`)))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expired token accepted: %d", recorder.Code)
	}
}
