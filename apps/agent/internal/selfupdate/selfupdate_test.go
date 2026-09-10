package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlan(t *testing.T) {
	here := []Release{{Arch: runtime.GOARCH, SHA256: "abc"}}
	elsewhere := []Release{{Arch: "s390x", SHA256: "abc"}}
	cases := []struct {
		name            string
		current, target string
		releases        []Release
		want            bool
	}{
		{"same version", "1.0", "1.0", here, false},
		{"no target advertised", "1.0", "", here, false},
		{"no build for this arch", "1.0", "1.1", elsewhere, false},
		{"missing checksum", "1.0", "1.1", []Release{{Arch: runtime.GOARCH}}, false},
		{"update available", "1.0", "1.1", here, true},
	}
	for _, test := range cases {
		if _, ok := Plan(test.current, test.target, test.releases); ok != test.want {
			t.Errorf("%s: ok = %v, want %v", test.name, ok, test.want)
		}
	}
}

func TestApplyRejectsAWrongChecksum(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(binary, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := "replacement"
	err := applyTo(binary, Release{SHA256: "wrong", Size: int64(len(payload))}, strings.NewReader(payload))
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v, want a checksum mismatch", err)
	}
	if data, _ := os.ReadFile(binary); string(data) != "original" {
		t.Fatalf("binary = %q, want it untouched", data)
	}
}

func TestApplyRejectsAShortDownload(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(binary, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := "replacement"
	sum := sha256.Sum256([]byte(payload))
	err := applyTo(binary, Release{SHA256: hex.EncodeToString(sum[:]), Size: 9999}, strings.NewReader(payload))
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("error = %v, want a size mismatch", err)
	}
	if data, _ := os.ReadFile(binary); string(data) != "original" {
		t.Fatalf("binary = %q, want it untouched", data)
	}
}

func TestApplyReplacesAndKeepsThePreviousBuild(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(binary, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := "replacement"
	sum := sha256.Sum256([]byte(payload))
	release := Release{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload))}
	if err := applyTo(binary, release, strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(binary); string(data) != payload {
		t.Fatalf("binary = %q, want the replacement", data)
	}
	if data, _ := os.ReadFile(binary + ".previous"); string(data) != "original" {
		t.Fatalf("previous = %q, want the original build", data)
	}
	info, err := os.Stat(binary)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("replacement is not executable: %v %v", err, info.Mode())
	}
}
