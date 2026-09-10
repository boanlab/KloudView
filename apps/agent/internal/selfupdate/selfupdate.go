// Package selfupdate replaces the running agent binary with a build offered by
// the server, after verifying its checksum.
package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// Release is one build advertised by the server.
type Release struct {
	Arch   string `json:"arch"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	URL    string `json:"url"`
}

// Plan reports the release to install, or false when the agent is already at
// the target version, the target is unknown, or no build matches this machine.
func Plan(currentVersion, targetVersion string, releases []Release) (Release, bool) {
	if targetVersion == "" || currentVersion == targetVersion {
		return Release{}, false
	}
	for _, release := range releases {
		if release.Arch == runtime.GOARCH && release.SHA256 != "" {
			return release, true
		}
	}
	return Release{}, false
}

// Apply replaces the running binary with body. The caller exits afterwards so
// the service manager starts the new build.
func Apply(release Release, body io.Reader) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	return applyTo(executable, release, body)
}

// applyTo stages the download beside the binary, verifies its size and digest,
// and swaps it in, keeping the previous build for a manual revert.
func applyTo(executable string, release Release, body io.Reader) error {
	staged := executable + ".new"
	file, err := os.OpenFile(staged, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, digest), body)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(staged)
		return err
	}
	if release.Size > 0 && written != release.Size {
		os.Remove(staged)
		return fmt.Errorf("size mismatch: got %d, want %d", written, release.Size)
	}
	if sum := hex.EncodeToString(digest.Sum(nil)); sum != release.SHA256 {
		os.Remove(staged)
		return fmt.Errorf("checksum mismatch: got %s, want %s", sum, release.SHA256)
	}
	if err := os.Rename(executable, executable+".previous"); err != nil {
		os.Remove(staged)
		return err
	}
	if err := os.Rename(staged, executable); err != nil {
		os.Rename(executable+".previous", executable)
		return err
	}
	return nil
}
