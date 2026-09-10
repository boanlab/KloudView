package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A TLS-terminating proxy reaches the server over plain HTTP, so r.TLS is nil
// while the browser's Origin says https. Comparing against r.TLS alone refuses
// every same-origin request in that topology.
func TestSameOriginBehindATLSTerminatingProxy(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/src/app.js", nil)
	request.Host = "kloudview.example.com"
	request.Header.Set("X-Forwarded-Proto", "https")
	if !sameOrigin("https://kloudview.example.com", request) {
		t.Fatal("a same-origin request behind a TLS proxy was rejected")
	}
	// The scheme still has to match what the browser reports.
	if sameOrigin("http://kloudview.example.com", request) {
		t.Error("an http origin matched an https-forwarded request")
	}
	// A different host is still cross-origin.
	if sameOrigin("https://evil.example.com", request) {
		t.Error("a different host was treated as same-origin")
	}
}

// A comma-separated chain names the client's scheme first.
func TestForwardedProtoTakesTheFirstValue(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Forwarded-Proto", "https, http")
	if got := requestScheme(request); got != "https" {
		t.Fatalf("scheme = %q, want https", got)
	}
}

// Without the header the scheme comes from the connection.
func TestRequestSchemeFallsBackToTheConnection(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := requestScheme(request); got != "http" {
		t.Fatalf("scheme = %q, want http", got)
	}
	request.Host = "kloudview.example.com"
	if !sameOrigin("http://kloudview.example.com", request) {
		t.Error("plain http same-origin was rejected")
	}
}
