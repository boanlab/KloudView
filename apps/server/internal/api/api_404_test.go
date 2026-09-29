package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// An unrouted API path answers a JSON error, not the console under a 200.
func TestAnUnroutedAPIPathAnswersInJSON(t *testing.T) {
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html><title>console</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := New(store.NewMemory(), "test-token", web).Handler()

	for _, path := range []string{"/api/v1/bindings", "/api/v1/nonsense", "/api/v2/resources"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), "<!doctype") {
			t.Errorf("GET %s answered with the console instead of an error", path)
		}
		var payload struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Errorf("GET %s body is not JSON: %v", path, err)
			continue
		}
		if payload.Error.Code == "" {
			t.Errorf("GET %s carries no error code", path)
		}
	}

	// A console deep link is still the console.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/resources/node-1", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "<!doctype") {
		t.Errorf("deep link = %d %q, want the console", recorder.Code, recorder.Body.String())
	}
}
