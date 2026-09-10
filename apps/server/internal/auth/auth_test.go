package auth

import (
	"testing"
	"time"
)

func TestSessionStoreDeleteByUserExcept(t *testing.T) {
	store := NewSessionStore(time.Hour)
	now := time.Now()
	keep := store.Create("user-1", "u1", now)
	other := store.Create("user-1", "u1", now)
	elsewhere := store.Create("user-2", "u2", now)

	store.DeleteByUserExcept("user-1", keep.Token)

	if _, ok := store.Get(keep.Token, now); !ok {
		t.Fatal("current session must be kept")
	}
	if _, ok := store.Get(other.Token, now); ok {
		t.Fatal("other session for the same user must be revoked")
	}
	if _, ok := store.Get(elsewhere.Token, now); !ok {
		t.Fatal("another user's session must be untouched")
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !CheckPassword(hash, "correct-horse") {
		t.Fatal("correct password must verify")
	}
	if CheckPassword(hash, "wrong") {
		t.Fatal("wrong password must not verify")
	}
}
