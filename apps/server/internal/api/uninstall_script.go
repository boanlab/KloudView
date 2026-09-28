package api

import (
	"fmt"
	"net/http"
)

// agentUninstallScript returns a POSIX shell uninstaller, the mirror of the
// installer. It carries no token because it enrols nothing: everything it
// touches is on the host it runs on.
//
// It leaves nothing behind, identity included. An agent id is derived from the
// hostname, so a host that is set up again enrols into the record it had
// before and keeps its history; there is nothing on disk worth carrying across
// a removal.
//
// It stops at the edge of the host. Removing an agent in the console takes its
// node, every resource under it and all of their history with it, which is a
// separate decision from taking the service off a machine and one an operator
// should make while looking at what they are about to lose.
func (s *Server) agentUninstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, uninstallScriptTemplate)
}

const uninstallScriptTemplate = `#!/bin/sh
# KloudView agent uninstaller. Usage:
#   curl -fsSL <server>/api/v1/agent-uninstall.sh | sudo sh
#
# Takes the agent off this host and nothing else. The console keeps the node,
# its resources and their history; the agent simply stops reporting and goes
# offline. Remove it there as well only when you mean to discard that history.
#
# Nothing is kept, identity included. Setting the host up again enrols into the
# same record, because an agent id comes from the hostname.
#
#   --dry-run  print what would be removed and change nothing
set -eu

dry_run=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) dry_run=1 ;;
    -*) echo "unknown option: $arg" >&2; exit 2 ;;
    *) echo "unexpected argument: $arg" >&2; exit 2 ;;
  esac
done
[ "$(id -u)" = "0" ] || { echo "run as root" >&2; exit 2; }

run() {
  if [ "$dry_run" = 1 ]; then
    echo "would: $*"
  else
    "$@"
  fi
}

found=0
[ -f /etc/systemd/system/kloudview-agent.service ] && found=1
[ -d /var/lib/kloudview ] && found=1
[ -d /etc/kloudview ] && found=1
id -u kloudview >/dev/null 2>&1 && found=1
if [ "$found" = 0 ]; then
  echo "no kloudview agent on this host"
  exit 0
fi

if command -v systemctl >/dev/null 2>&1; then
  if systemctl list-unit-files kloudview-agent.service >/dev/null 2>&1; then
    # Stopping before anything is removed: a unit whose binary disappears
    # underneath it restarts into a file that is no longer there.
    run systemctl disable --now kloudview-agent 2>/dev/null || true
  fi
fi
# The unit file goes whether or not this host can run systemctl. An install
# that got as far as writing it and no further leaves one behind, and a
# uninstaller that skipped it would report a clean host while the file sat
# there waiting for the next boot that did have systemd.
run rm -f /etc/systemd/system/kloudview-agent.service
if command -v systemctl >/dev/null 2>&1; then
  run systemctl daemon-reload
fi

run rm -rf /etc/kloudview

# The binary, and the identity and credential beside it.
run rm -rf /var/lib/kloudview

# The account owns nothing once its directory is gone, and its group
# memberships go with it.
if id -u kloudview >/dev/null 2>&1; then
  if command -v userdel >/dev/null 2>&1; then
    run userdel kloudview 2>/dev/null || true
  elif command -v deluser >/dev/null 2>&1; then
    run deluser kloudview 2>/dev/null || true
  fi
fi
if getent group kloudview >/dev/null 2>&1; then
  if command -v groupdel >/dev/null 2>&1; then
    run groupdel kloudview 2>/dev/null || true
  fi
fi

if [ "$dry_run" = 1 ]; then
  echo "dry run: nothing was changed"
  exit 0
fi
echo "kloudview-agent removed from $(hostname)"
echo "nothing kloudview is left on this host"
echo "the console still lists this node; remove it there to discard its history"
`
