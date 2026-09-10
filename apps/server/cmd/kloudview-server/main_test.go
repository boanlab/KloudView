package main

import (
	"path/filepath"
	"testing"
)

// A path that does not exist yet is a new store, not an error.
func TestLoadStateReadsBackWhatItWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	memory, err := loadState(path)
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	if err := memory.Save(path); err != nil {
		t.Fatal(err)
	}

	restored, err := loadState(path)
	if err != nil {
		t.Fatalf("restored state: %v", err)
	}
	if len(restored.ListResources()) != 0 {
		t.Fatal("persisted empty state was modified")
	}
}

// No path configured means state lives only in memory.
func TestLoadStateWithoutPersistenceUsesFreshStore(t *testing.T) {
	memory, err := loadState("")
	if err != nil || memory == nil {
		t.Fatalf("memory = %v, error = %v", memory, err)
	}
}

func TestValidateServerSecrets(t *testing.T) {
	validToken := "enrollment-token-long-enough"
	validKey := "independent-runtime-credential-key-123456"
	for _, item := range []struct {
		name  string
		token string
		key   string
		valid bool
	}{
		{name: "valid", token: validToken, key: validKey, valid: true},
		{name: "short enrollment token", token: "short", key: validKey},
		{name: "short credential key", token: validToken, key: "short"},
		{name: "shared secret", token: validKey, key: validKey},
	} {
		t.Run(item.name, func(t *testing.T) {
			err := validateServerSecrets(item.token, item.key)
			if (err == nil) != item.valid {
				t.Fatalf("error = %v, valid = %v", err, item.valid)
			}
		})
	}
}

// The placeholders in .env.example clear the length rules, so a deployment that
// copied it unedited would otherwise start on secrets published in the
// repository.
func TestServerRefusesPublishedPlaceholderSecrets(t *testing.T) {
	validKey := "independent-runtime-credential-key-123456"
	validToken := "enrollment-token-long-enough"
	for _, token := range []string{
		"replace-with-random-bootstrap-token",
		"local-development-token-change-me",
	} {
		if err := validateServerSecrets(token, validKey); err == nil {
			t.Errorf("token %q was accepted", token)
		}
	}
	for _, key := range []string{
		"replace-with-independent-random-server-key",
		"local-development-agent-credential-key-change-me",
	} {
		if err := validateServerSecrets(validToken, key); err == nil {
			t.Errorf("key %q was accepted", key)
		}
	}
	if err := validateServerSecrets(validToken, validKey); err != nil {
		t.Errorf("a pair of real secrets was rejected: %v", err)
	}
}
