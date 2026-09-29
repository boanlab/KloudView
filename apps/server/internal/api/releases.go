package api

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// agentRelease describes one downloadable agent build.
type agentRelease struct {
	Arch   string `json:"arch"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	URL    string `json:"url"`
}

type releaseManifest struct {
	Version  string         `json:"version"`
	Releases []agentRelease `json:"releases"`
	// Version stamped on the binaries on disk; nothing is served while it
	// disagrees with the target.
	Built string `json:"-"`
	// When this build was published, taken from the file the build stamped.
	// The rollout window counts from here rather than from anything an agent
	// reports, so a canary that was already on the version before it became
	// the target does not hand the fleet a window that has expired.
	PublishedAt time.Time `json:"-"`
}

const releaseFilePrefix = "kloudview-agent-linux-"

// releaseVersionFile records the version `make verify-agent-binaries` stamped
// into the binaries beside it.
const releaseVersionFile = "VERSION"

type releaseCache struct {
	mu       sync.Mutex
	loadedAt time.Time
	manifest releaseManifest
}

// agentReleases lists the builds present in the distribution directory. Version
// is empty unless a target was configured, so agents never update without an
// operator naming the version. The result is cached briefly so a heartbeat does
// not hash the binaries.
func (s *Server) agentReleases() releaseManifest {
	if s.releaseDir == "" {
		return releaseManifest{}
	}
	s.releases.mu.Lock()
	defer s.releases.mu.Unlock()
	if time.Since(s.releases.loadedAt) < 30*time.Second {
		return s.releases.manifest
	}
	manifest := releaseManifest{Version: s.releaseVersion}
	entries, err := os.ReadDir(s.releaseDir)
	if err != nil {
		s.releases.manifest, s.releases.loadedAt = manifest, time.Now()
		return manifest
	}
	// Filenames carry only the architecture, so the build records the version it
	// stamped. Serving a build that disagrees with the target leaves the agent
	// still unmatched after installing it, and downloading again every
	// heartbeat; a disagreement offers nothing instead.
	// Only when a target is named: with none configured no update is advertised,
	// so there is nothing for the build to disagree with, and the binaries are
	// still needed to install an agent for the first time.
	versionPath := filepath.Join(s.releaseDir, releaseVersionFile)
	if info, err := os.Stat(versionPath); err == nil {
		manifest.PublishedAt = info.ModTime().UTC()
	}
	if built, err := os.ReadFile(versionPath); err == nil {
		manifest.Built = strings.TrimSpace(string(built))
	}
	if s.releaseVersion != "" && manifest.Built != "" {
		if got := manifest.Built; got != s.releaseVersion {
			slog.Warn("agent releases withheld: built version does not match the configured target",
				"built", got, "target", s.releaseVersion)
			s.releases.manifest, s.releases.loadedAt = manifest, time.Now()
			return manifest
		}
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), releaseFilePrefix) {
			continue
		}
		arch := strings.TrimPrefix(entry.Name(), releaseFilePrefix)
		info, err := entry.Info()
		if err != nil {
			continue
		}
		sum, err := fileSHA256(filepath.Join(s.releaseDir, entry.Name()))
		if err != nil {
			continue
		}
		manifest.Releases = append(manifest.Releases, agentRelease{
			Arch:   arch,
			SHA256: sum,
			Size:   info.Size(),
			URL:    "/api/v1/agent-releases/" + arch,
		})
	}
	s.releases.manifest, s.releases.loadedAt = manifest, time.Now()
	return manifest
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (s *Server) listAgentReleases(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.agentReleases())
}

// downloadAgentRelease serves one build. The arch comes from the path and is
// matched against the manifest rather than joined into a filesystem path.
func (s *Server) downloadAgentRelease(w http.ResponseWriter, r *http.Request) {
	arch := r.PathValue("arch")
	for _, release := range s.agentReleases().Releases {
		if release.Arch != arch {
			continue
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-KloudView-Agent-SHA256", release.SHA256)
		http.ServeFile(w, r, filepath.Join(s.releaseDir, releaseFilePrefix+release.Arch))
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "no agent build for this architecture")
}
