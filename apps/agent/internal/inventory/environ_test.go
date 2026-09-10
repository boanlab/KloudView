package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProcEnviron fakes /proc/<pid>/environ by pointing procPath at a temp dir.
func TestReadEnvironRedactsEverythingNotAllowed(t *testing.T) {
	dir := t.TempDir()
	entries := []string{
		"PATH=/usr/bin:/bin",
		"LANG=en_US.UTF-8",
		"DATABASE_PASSWORD=hunter2",
		"AWS_SECRET_ACCESS_KEY=abcd1234",
		"MY_APP_TIER=frontend",
	}
	if err := os.WriteFile(filepath.Join(dir, "environ"), []byte(strings.Join(entries, "\x00")), 0o600); err != nil {
		t.Fatal(err)
	}
	original := procPathFunc
	procPathFunc = func(_ int, name string) string { return filepath.Join(dir, name) }
	defer func() { procPathFunc = original }()

	items := readEnviron(1, map[string]bool{"MY_APP_TIER": true})
	byKey := map[string]EnvVar{}
	for _, item := range items {
		byKey[item.Key] = item
	}

	if got := byKey["PATH"]; got.Value != "/usr/bin:/bin" || got.Redacted {
		t.Errorf("PATH = %+v, want its value", got)
	}
	if got := byKey["LANG"]; got.Value != "en_US.UTF-8" {
		t.Errorf("LANG = %+v", got)
	}
	// Node-specific allowance applies.
	if got := byKey["MY_APP_TIER"]; got.Value != "frontend" {
		t.Errorf("MY_APP_TIER = %+v, want the node's allowance to apply", got)
	}
	for _, secret := range []string{"DATABASE_PASSWORD", "AWS_SECRET_ACCESS_KEY"} {
		got := byKey[secret]
		if got.Key != secret {
			t.Errorf("%s is missing; its name should still be reported", secret)
		}
		if got.Value != "" || !got.Redacted {
			t.Errorf("%s leaked a value: %+v", secret, got)
		}
	}
	// The whole environment is still enumerated, names and all.
	if len(items) != len(entries) {
		t.Errorf("items = %d, want %d", len(items), len(entries))
	}
}

func TestReadCmdlineSplitsArguments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("nginx\x00-g\x00daemon off;\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := procPathFunc
	procPathFunc = func(_ int, name string) string { return filepath.Join(dir, name) }
	defer func() { procPathFunc = original }()

	args := readCmdline(1)
	if len(args) != 3 || args[2] != "daemon off;" {
		t.Fatalf("args = %q", args)
	}
}

func TestReadEnvironOnAMissingProcess(t *testing.T) {
	original := procPathFunc
	procPathFunc = func(_ int, name string) string { return filepath.Join(t.TempDir(), name) }
	defer func() { procPathFunc = original }()
	if got := readEnviron(1, nil); got != nil {
		t.Fatalf("items = %v, want none", got)
	}
}
