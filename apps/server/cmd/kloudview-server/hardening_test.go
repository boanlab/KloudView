package main

import (
	"strings"
	"testing"
)

func hardened() hardeningConfig {
	return hardeningConfig{
		AdminPassword: "a-long-enough-admin-secret",
		DevHeaderAuth: false,
		PublicURL:     "https://kloudview.internal",
	}
}

func TestAHardenedConfigurationWarnsAboutNothing(t *testing.T) {
	if warnings := hardeningWarnings(hardened()); len(warnings) != 0 {
		t.Fatalf("warnings on a hardened configuration: %v", warnings)
	}
}

// Default admin password and plain HTTP.
func TestAnUnhardenedConfigurationWarnsOnEveryItem(t *testing.T) {
	warnings := hardeningWarnings(hardeningConfig{
		AdminPassword: "admin",
		DevHeaderAuth: false,
		PublicURL:     "",
	})
	if len(warnings) != 2 {
		t.Fatalf("want 2 warnings, got %d: %v", len(warnings), warnings)
	}
	joined := strings.Join(warnings, "\n")
	for _, expected := range []string{"admin/admin", "cleartext"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("warnings do not mention %q:\n%s", expected, joined)
		}
	}
}

func TestAdminPasswordWarnings(t *testing.T) {
	for _, testCase := range []struct {
		password string
		warns    bool
	}{
		{"admin", true},
		{"", true},
		{"short", true},
		{"eleven-char", true}, // 11
		{"twelve-chars", false},
		{"a-long-enough-admin-secret", false},
	} {
		config := hardened()
		config.AdminPassword = testCase.password
		warnings := hardeningWarnings(config)
		if got := len(warnings) > 0; got != testCase.warns {
			t.Errorf("password %q: warned=%v, want %v (%v)", testCase.password, got, testCase.warns, warnings)
		}
	}
}

// A missing declaration and an http:// one are both "not confirmed as TLS", and
// both have to say so rather than passing quietly.
func TestPublicURLMustDeclareTLS(t *testing.T) {
	for _, url := range []string{"", "   ", "http://10.20.0.1:8080", "HTTP://kloudview"} {
		config := hardened()
		config.PublicURL = url
		if warnings := hardeningWarnings(config); len(warnings) != 1 {
			t.Errorf("PublicURL %q gave %v, want exactly one warning", url, warnings)
		}
	}
	for _, url := range []string{"https://kloudview.internal", "HTTPS://kloudview.internal"} {
		config := hardened()
		config.PublicURL = url
		if warnings := hardeningWarnings(config); len(warnings) != 0 {
			t.Errorf("PublicURL %q warned: %v", url, warnings)
		}
	}
}

func TestDevHeaderAuthWarns(t *testing.T) {
	config := hardened()
	config.DevHeaderAuth = true
	if warnings := hardeningWarnings(config); len(warnings) != 1 || !strings.Contains(warnings[0], "without a session") {
		t.Errorf("dev header auth: %v", warnings)
	}
}

// Long enough to pass the admin-password length rule, and published.
func TestPlaceholderAdminPasswordWarns(t *testing.T) {
	config := hardened()
	config.AdminPassword = "change-me-admin-password"
	warnings := hardeningWarnings(config)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "placeholder") {
		t.Fatalf("warnings = %v", warnings)
	}
}
