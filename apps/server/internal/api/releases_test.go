package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/store"
)

func releaseDir(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	for _, arch := range []string{"amd64", "arm64"} {
		if err := os.WriteFile(filepath.Join(dir, releaseFilePrefix+arch), []byte("binary-"+arch), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if version != "" {
		if err := os.WriteFile(filepath.Join(dir, releaseVersionFile), []byte(version+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReleasesAreOfferedWhenTheBuildMatchesTheTarget(t *testing.T) {
	server := New(store.NewMemory(), "token", "").WithAgentReleases(releaseDir(t, "0.2.5"), "0.2.5")
	manifest := server.agentReleases()
	if manifest.Version != "0.2.5" || len(manifest.Releases) != 2 {
		t.Fatalf("manifest = %+v", manifest)
	}
}

// Filenames carry only the architecture. A build disagreeing with the target
// leaves the agent unmatched after installing it, and downloading again.
func TestAMismatchedBuildIsWithheldRatherThanLooped(t *testing.T) {
	server := New(store.NewMemory(), "token", "").WithAgentReleases(releaseDir(t, "0.2.5"), "0.2.4")
	manifest := server.agentReleases()
	if len(manifest.Releases) != 0 {
		t.Fatalf("a 0.2.5 build was offered as 0.2.4: %+v", manifest.Releases)
	}
	// The target still travels, so an agent already on it is left alone.
	if manifest.Version != "0.2.4" {
		t.Errorf("version = %q, want the configured target", manifest.Version)
	}
}

// A directory populated before the build recorded versions still works.
func TestAnUnversionedReleaseDirectoryIsStillOffered(t *testing.T) {
	server := New(store.NewMemory(), "token", "").WithAgentReleases(releaseDir(t, ""), "0.2.4")
	if manifest := server.agentReleases(); len(manifest.Releases) != 2 {
		t.Fatalf("releases = %+v", manifest.Releases)
	}
}

// Trailing whitespace from the shell that wrote the file must not read as a
// different version.
func TestRecordedVersionIsTrimmed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, releaseFilePrefix+"amd64"), []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, releaseVersionFile), []byte("  0.2.5 \n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := New(store.NewMemory(), "token", "").WithAgentReleases(dir, "0.2.5")
	if manifest := server.agentReleases(); len(manifest.Releases) != 1 {
		t.Fatalf("whitespace in VERSION withheld a matching build: %+v", manifest)
	}
}

// With no target configured no update is advertised, so a recorded build version
// has nothing to disagree with. Withholding there breaks first-time installs,
// which need the binaries the install script downloads.
func TestReleasesAreServedWhenNoTargetIsConfigured(t *testing.T) {
	server := New(store.NewMemory(), "token", "").WithAgentReleases(releaseDir(t, "0.1.0"), "")
	manifest := server.agentReleases()
	if len(manifest.Releases) != 2 {
		t.Fatalf("releases withheld with no target: %+v", manifest)
	}
	if manifest.Version != "" {
		t.Errorf("version = %q, want empty so no agent updates", manifest.Version)
	}
}
