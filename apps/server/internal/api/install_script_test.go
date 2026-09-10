package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kloudview/kloudview/apps/server/internal/store"
)

// installScriptBody serves the script the way an operator would fetch it.
func installScriptBody(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kloudview-agent-linux-amd64"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := New(store.NewMemory(), "token", "").WithAgentReleases(dir, "1.0.0")
	recorder := httptest.NewRecorder()
	server.agentInstallScript(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/agent-install.sh", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	return recorder.Body.String()
}

func TestAgentInstallScript(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kloudview-agent-linux-amd64"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := New(store.NewMemory(), "token", "").WithAgentReleases(dir, "1.0.0")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent-install.sh", nil)
	request.Host = "kloudview.internal"
	request.Header.Set("X-Forwarded-Proto", "https")
	server.agentInstallScript(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"https://kloudview.internal/api/v1/agent-releases/$arch",
		"KLOUDVIEW_SERVER_URL=https://kloudview.internal",
		"amd64) want=",
		"checksum mismatch",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("script is missing %q", want)
		}
	}
	// The token is an argument, never baked into the served script.
	if strings.Contains(body, "KLOUDVIEW_ENROLLMENT_TOKEN=token") {
		t.Error("the script embeds a token")
	}
}

func TestAgentInstallScriptNeedsReleases(t *testing.T) {
	server := New(store.NewMemory(), "token", "")
	recorder := httptest.NewRecorder()
	server.agentInstallScript(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/agent-install.sh", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when no build is available", recorder.Code)
	}
}

// The agent runs unprivileged, so what it can read is decided by group
// membership. Without these the agent starts, reports counters of zero, and
// looks like a quiet node rather than a blind one.
func TestAgentInstallScriptGrantsCollectionAccess(t *testing.T) {
	body := installScriptBody(t)
	for _, want := range []string{
		"systemd-journal",
		"adm",
		"docker",
		"SupplementaryGroups=$extra",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("install script is missing %q", want)
		}
	}
	// The unit heredoc must expand $extra, so it cannot be quoted.
	if strings.Contains(body, "<<'UNIT'") {
		t.Error("the unit heredoc is quoted, so SupplementaryGroups stays literal")
	}
	// systemd parses SupplementaryGroups= as a space-separated list. A comma
	// separated one is read as a single group name and the unit dies 216/GROUP.
	if strings.Contains(body, "paste -sd, -") {
		t.Error("the group list is comma separated, which systemd rejects")
	}
}

func TestAgentInstallScriptIsValidShell(t *testing.T) {
	body := installScriptBody(t)
	path := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("sh", "-n", path).CombinedOutput()
	if err != nil {
		t.Fatalf("generated script is not valid shell: %v\n%s", err, output)
	}
}

// A redeploy re-runs the installer against a host already running the agent.
// Both steps that break on a second run are pinned here.
func TestAgentInstallScriptIsRerunnable(t *testing.T) {
	body := installScriptBody(t)
	// Overwriting a running executable in place fails with ETXTBSY.
	if !strings.Contains(body, "kloudview-agent.new") ||
		!strings.Contains(body, "mv -f /var/lib/kloudview/bin/kloudview-agent.new /var/lib/kloudview/bin/kloudview-agent") {
		t.Error("the binary is written in place rather than renamed over")
	}
	// enable --now leaves an already-active unit running the previous binary.
	if strings.Contains(body, "systemctl enable --now") {
		t.Error("a re-run would not pick up the new binary")
	}
	if !strings.Contains(body, "systemctl restart kloudview-agent") {
		t.Error("the unit is never restarted")
	}
	// The stored identity is what keeps a re-run from registering a second agent.
	for _, forbidden := range []string{
		"rm -f /var/lib/kloudview/agent.json",
		"rm /var/lib/kloudview/agent.json",
		"> /var/lib/kloudview/agent.json",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the installer destroys the stored identity: %q", forbidden)
		}
	}
}

// Self-update lets the server replace the binary on a host, so the installer
// grants it only when asked. The generated unit file must reflect the flag.
func TestInstallScriptLeavesAutoUpdateOffUnlessRequested(t *testing.T) {
	script := installScriptBody(t)
	if !strings.Contains(script, "want_auto_update=0") {
		t.Error("auto-update does not default to off")
	}
	if !strings.Contains(script, `--auto-update) want_auto_update=1`) {
		t.Error("no --auto-update flag to turn it on")
	}
	if strings.Contains(script, "KLOUDVIEW_AUTO_UPDATE=true\n") {
		t.Error("auto-update is written unconditionally")
	}
	if !strings.Contains(script, `KLOUDVIEW_AUTO_UPDATE=$([ "$want_auto_update" = 1 ]`) {
		t.Error("auto-update is not driven by the flag")
	}
	if !strings.Contains(script, "[--auto-update]") {
		t.Error("usage does not mention the flag")
	}
}
