package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/store"
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

func TestConsoleAssetsAreRevalidated(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<title>console</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("export const version = 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := New(store.NewMemory(), "test-token", root).Handler()
	// The console has no fingerprinted filenames, so a browser left to guess
	// how long app.js stays fresh serves the previous console after an upgrade.
	for _, path := range []string{"/app.js", "/", "/index.html"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s Cache-Control = %q, want no-cache", path, got)
		}
	}
	// no-cache is revalidation, not "never cache": an unchanged file still
	// answers 304 with no body.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	modified := recorder.Header().Get("Last-Modified")
	if modified == "" {
		t.Fatal("no Last-Modified to revalidate against")
	}
	conditional := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	conditional.Header.Set("If-Modified-Since", modified)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, conditional)
	if recorder.Code != http.StatusNotModified {
		t.Fatalf("unchanged asset = %d, want 304", recorder.Code)
	}
}
