package api

import (
	"fmt"
	"net/http"
	"strings"
)

// agentInstallScript returns a POSIX shell installer. The token is taken as an
// argument rather than baked in, so the URL that fetches this script carries no
// secret and cannot leak one through a proxy or access log.
func (s *Server) agentInstallScript(w http.ResponseWriter, r *http.Request) {
	origin := requestOrigin(r)
	var digests strings.Builder
	for _, release := range s.agentReleases().Releases {
		fmt.Fprintf(&digests, "  %s) want=%s ;;\n", release.Arch, release.SHA256)
	}
	if digests.Len() == 0 {
		writeError(w, http.StatusServiceUnavailable, "no_releases", "the server has no agent builds to install")
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, installScriptTemplate, digests.String(), origin, origin)
}

// requestOrigin reconstructs the address the caller used, honouring the proxy
// headers a reverse proxy sets.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host
}

const installScriptTemplate = `#!/bin/sh
# KloudView agent installer. Usage:
#   curl -fsSL <server>/api/v1/agent-install.sh | sudo sh -s -- <enrollment-token> [options]
#
# The agent runs unprivileged, so each kind of collection needs a specific
# group. Options turn a collection off; the installer then neither grants its
# group nor enables it.
#
#   --no-logs        skip journal and /var/log access, and stop log streaming
#   --no-containers  skip container runtime access
#   --no-terminal    do not offer approval-gated shell sessions
#   --auto-update    let the server replace this binary; off unless asked for
set -eu

token=""
want_logs=1
want_containers=1
want_terminal=1
want_auto_update=0
for arg in "$@"; do
  case "$arg" in
    --no-logs) want_logs=0 ;;
    --no-containers) want_containers=0 ;;
    --no-terminal) want_terminal=0 ;;
    --terminal) want_terminal=1 ;;
    --auto-update) want_auto_update=1 ;;
    -*) echo "unknown option: $arg" >&2; exit 2 ;;
    *) token="$arg" ;;
  esac
done
[ -n "$token" ] || token="${KLOUDVIEW_ENROLLMENT_TOKEN:-}"
[ -n "$token" ] || { echo "usage: sh -s -- <enrollment-token> [--no-logs] [--no-containers] [--no-terminal] [--auto-update]" >&2; exit 2; }
[ "$(id -u)" = "0" ] || { echo "run as root" >&2; exit 2; }

arch=$(uname -m)
case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
case "$arch" in
%s  *) echo "no agent build for $arch" >&2; exit 1 ;;
esac

if ! id -u kloudview >/dev/null 2>&1; then
  if command -v useradd >/dev/null 2>&1; then
    useradd --system --home-dir /var/lib/kloudview --shell /usr/sbin/nologin kloudview
  elif command -v adduser >/dev/null 2>&1; then
    adduser -S -H -h /var/lib/kloudview -s /sbin/nologin kloudview
  else
    echo "cannot create the kloudview account on this system" >&2; exit 1
  fi
fi
install -d -o kloudview -g kloudview -m 0755 /var/lib/kloudview/bin

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
curl -fsSL -o "$tmp" "%s/api/v1/agent-releases/$arch"
got=$(sha256sum "$tmp" | cut -d' ' -f1)
[ "$got" = "$want" ] || { echo "checksum mismatch: $got != $want" >&2; exit 1; }
# Rename rather than overwrite: replacing a running executable in place fails
# with ETXTBSY, which is what a re-run hits.
install -o kloudview -g kloudview -m 0755 "$tmp" /var/lib/kloudview/bin/kloudview-agent.new
mv -f /var/lib/kloudview/bin/kloudview-agent.new /var/lib/kloudview/bin/kloudview-agent

# Group membership is what decides whether the agent can read anything: the
# system journal is root:systemd-journal 0640, /var/log is adm-readable, and
# container discovery goes through the runtime socket. Only the groups for the
# enabled collections are granted.
wanted=""
[ "$want_logs" = 1 ] && wanted="systemd-journal adm"
[ "$want_containers" = 1 ] && wanted="$wanted docker"

for group in $wanted; do
  getent group "$group" >/dev/null 2>&1 || continue
  if command -v usermod >/dev/null 2>&1; then
    usermod -aG "$group" kloudview 2>/dev/null || true
  elif command -v addgroup >/dev/null 2>&1; then
    addgroup kloudview "$group" 2>/dev/null || true
  fi
done
extra=$(id -nG kloudview | tr ' ' '\n' | grep -E '^(systemd-journal|adm|docker)$' | paste -sd' ' -)

# A group that does not exist on this host is skipped, which would otherwise
# leave the agent collecting nothing with no sign that it is doing so.
missing=""
for group in $wanted; do
  case " $extra " in *" $group "*) ;; *) missing="$missing $group" ;; esac
done

install -d -m 0755 /etc/kloudview
cat > /etc/kloudview/agent.env <<ENV
KLOUDVIEW_SERVER_URL=%s
KLOUDVIEW_ENROLLMENT_TOKEN=$token
KLOUDVIEW_AUTO_UPDATE=$([ "$want_auto_update" = 1 ] && echo true || echo false)
KLOUDVIEW_LOG_STREAM=$([ "$want_logs" = 1 ] && echo true || echo false)
KLOUDVIEW_TERMINAL_ENABLED=$([ "$want_terminal" = 1 ] && echo true || echo false)
ENV
chown root:kloudview /etc/kloudview/agent.env
chmod 0640 /etc/kloudview/agent.env

cat > /etc/systemd/system/kloudview-agent.service <<UNIT
[Unit]
Description=KloudView Infrastructure Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=kloudview
Group=kloudview
StateDirectory=kloudview
StateDirectoryMode=0700
EnvironmentFile=-/etc/kloudview/agent.env
SupplementaryGroups=$extra
ExecStart=/var/lib/kloudview/bin/kloudview-agent
Restart=always
RestartSec=5s
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable kloudview-agent
# restart rather than enable --now: on a re-run the unit is already active and
# would keep running the previous binary.
systemctl restart kloudview-agent

# The identity at /var/lib/kloudview/agent.json is left alone, so a re-run keeps
# the agent's existing registration and the new token simply goes unused.
sleep 2
systemctl --no-pager --lines=5 status kloudview-agent
echo "kloudview-agent $(/var/lib/kloudview/bin/kloudview-agent --version) installed"
echo "groups granted: ${extra:-none}"
[ "$want_terminal" = 1 ] && echo "terminal: enabled"
if [ -n "$missing" ]; then
  echo "WARNING: no such group on this host:$missing" >&2
  case "$missing" in
    *systemd-journal*|*adm*) echo "WARNING: the agent cannot read the journal, so log collection will report nothing" >&2 ;;
  esac
  case "$missing" in
    *docker*) echo "WARNING: the agent cannot reach the container runtime, so containers will not be discovered" >&2 ;;
  esac
fi
`
