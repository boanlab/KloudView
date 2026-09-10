package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/auth"
	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// A session owner must prove the current password to change their own; an
// administrator resetting another account is not asked for it.
func TestSelfPasswordChangeRequiresTheCurrentPassword(t *testing.T) {
	memory := store.NewMemory()
	hash, err := auth.HashPassword("original-password")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// admin holds the administrator binding the server seeds.
	memory.PutUser(domain.User{ID: "user-self", Username: "admin", PasswordHash: hash, Status: "active", CreatedAt: now})
	memory.PutUser(domain.User{ID: "user-other", Username: "other", PasswordHash: hash, Status: "active", CreatedAt: now})

	server := New(memory, "token", "")
	handler := server.Handler()

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"admin","password":"original-password"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()

	put := func(id, body string) int {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/users/"+id, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}

	if code := put("user-self", `{"password":"brand-new-password"}`); code != http.StatusUnauthorized {
		t.Fatalf("self change without the current password = %d, want 401", code)
	}
	if code := put("user-self", `{"password":"brand-new-password","currentPassword":"wrong"}`); code != http.StatusUnauthorized {
		t.Fatalf("self change with a wrong current password = %d, want 401", code)
	}
	if code := put("user-other", `{"password":"reset-by-admin"}`); code != http.StatusOK {
		t.Fatalf("admin reset of another account = %d, want 200", code)
	}
	// Last: a successful self-change ends the session it was made from.
	if code := put("user-self", `{"password":"brand-new-password","currentPassword":"original-password"}`); code != http.StatusOK {
		t.Fatalf("self change with the current password = %d, want 200", code)
	}
}
