# Agent installation

## Enrollment tokens

Issue a token from **Agents → Install agent**. It is shown once, expires on its own
(1 minute to 1 hour), and can be bound to a hostname, a CIDR, or both; a bound token
presented by another host is refused with `403 token_host_mismatch`.

`KLOUDVIEW_ENROLLMENT_TOKEN` on the server remains valid for unattended installs. Leave
it unset to accept only console-issued tokens.

After enrolling, the agent stores a credential of its own at
`KLOUDVIEW_STATE_PATH`, so it reconnects after a reboot or a network outage without a
new token. The enrollment token bounds the *first* connection only.

## One-line install

Issue a token from **Agents → Install agent** and run the command it shows on the
target host:

```bash
curl -fsSL https://kloudview.example.com/api/v1/agent-install.sh | sudo sh -s -- <token>
```

The script picks the build for the host's architecture, verifies its SHA-256 against
the digest the server published, creates the unprivileged `kloudview` account,
installs the binary under `/var/lib/kloudview/bin`, writes `/etc/kloudview/agent.env`,
and enables the systemd unit. The token is an argument, so it never appears in the
URL or in a proxy log.

Behind a TLS ingress, set `X-Forwarded-Proto` and `X-Forwarded-Host`; the script's
URLs follow them. Over plain HTTP the script can be replaced in transit by anyone on
the network path, and it is piped into a root shell — serve the console over HTTPS
before using this on a network you do not control.

### Collection options

The agent runs unprivileged, so what it can read is decided by group membership. Each
collection is enabled by default and can be turned off, in the console's install
dialog or as an argument:

All are on by default; each option turns one off.

| Option | Turns off |
|---|---|
| `--no-logs` | Journal and `/var/log` reads, and log streaming. Skips `systemd-journal` and `adm` |
| `--auto-update` | Lets the server replace the agent binary. Off unless given |
| `--no-containers` | Container discovery and cgroup usage. Skips the container runtime group |
| `--no-terminal` | Approval-gated shell sessions |

```bash
curl -fsSL https://kloudview.example.com/api/v1/agent-install.sh | sudo sh -s -- <token> --no-logs
```

Skipping a group is not cosmetic: an agent without `systemd-journal` starts normally
and reports severity counters of zero, which reads as a quiet node rather than a blind
one. Without the container runtime group, a host running containers reports none. The
installer prints the groups it actually granted and warns about any that the host does
not have.

Log collection resumes where it stopped. The agent stores the journal cursor of
each window the server accepted, in `journal-cursor` beside its identity, and seeks
past it on the next start, so a restart -- a self-update, a crash, a reboot -- leaves
no gap and re-sends nothing. A cursor the journal no longer holds is discarded and
collection starts at the present.

The installer is safe to re-run, which is how an agent is upgraded. It renames the new
binary over the old one rather than writing in place, restarts the unit so the new
build takes effect, and leaves `/var/lib/kloudview/agent.json` alone, so the host keeps
its registration and the fresh token goes unused.

The binary lives under the state directory rather than `/usr/local/bin` so the agent
can replace it during a self-update without running as root.

## Virtual machines

Where libvirt is present, the agent reads `virsh domstats` and reports the
hypervisor's view of each guest: state, vCPU count, host CPU time, assigned memory,
and cumulative disk and network I/O. Guest memory usage appears only when the guest
runs the VirtIO balloon driver.

This is deliberately the host side of the boundary. A guest's filesystem usage, its
processes, and its logs are not visible from the hypervisor at any collection depth —
a guest filesystem filling up produces no signal here. Install an agent inside the
guest to monitor it as a node.

libvirt's official Go binding needs cgo, which would end the single static binary, so
the agent shells out to `virsh` as it already does for `docker` and `systemctl`.

## Automatic updates

Build the binaries with `make verify-agent-binaries VERSION=x.y.z`, which also records
the version the server checks the release directory against, and point the server at
them with `KLOUDVIEW_AGENT_RELEASE_PATH` (Compose mounts `./dist`). Set
`KLOUDVIEW_AGENT_TARGET_VERSION` to the version agents should run; while it is unset
no update is advertised.

Each heartbeat returns the target version and the available builds with their SHA-256
digests. An agent started with `KLOUDVIEW_AUTO_UPDATE=true` downloads the build for
its architecture, verifies the size and digest before installing, keeps the previous
binary as `<binary>.previous`, and exits so systemd starts the new build. A digest
mismatch aborts the update and leaves the running binary untouched.

Auto-update is off unless the agent sets `KLOUDVIEW_AUTO_UPDATE=true`, since it lets
the server replace code on the host.

The Agent is a single Go binary that connects to the Server from the managed host using an outbound connection.

## Static binary

The release files are built with `CGO_ENABLED=0` and the `netgo` and `osusergo` build tags, so they do not depend on glibc, musl, or an ELF dynamic loader. `make verify-agent-binaries VERSION=0.1.0` builds the Linux amd64 and arm64 files and verifies static linking and the SHA-256 checksums.

You must select the file matching your CPU architecture; a Linux kernel with `/proc` and `/sys` is required. The Agent also runs on hosts without `systemctl`, `virsh`, `docker`, `podman`, `nerdctl`, or `ctr`, but the corresponding optional inventory items are empty. When multiple container runtimes are installed, their results are collected together and deduplicated by container ID.

Inventory VMs are reconciled as `hosts` child resources of the host, and Containers and Processes as `runs` child resources. Fields reported by the Agent, such as running state, image, runtime, PID, and RSS, are preserved in the resource attributes, and Agent-owned resources that disappear in the next inventory are cleaned up along with their relationships.

The process command collects only the executable path to avoid exposing sensitive execution arguments. Command-line arguments and environment variables are neither collected nor transmitted.

## Configuration

`/etc/kloudview/agent.env`:

```ini
KLOUDVIEW_SERVER_URL=https://kloudview.example.com
KLOUDVIEW_ENROLLMENT_TOKEN=replace-with-at-least-16-random-characters
KLOUDVIEW_INTERVAL=10s
KLOUDVIEW_STATE_PATH=/var/lib/kloudview/agent.json
KLOUDVIEW_ALLOWED_SERVICES=sshd,containerd,libvirtd
KLOUDVIEW_TERMINAL_ENABLED=false
KLOUDVIEW_LOG_STREAM=true
```

`KLOUDVIEW_LOG_STREAM` follows the journal continuously and reports severity counts
every minute. Set it to `false` to collect nothing; the installer does this for you
with `--no-logs`.

Installing by hand needs the same group membership the installer grants:

```bash
sudo useradd --system --home /var/lib/kloudview --shell /usr/sbin/nologin kloudview
sudo usermod -aG systemd-journal,adm,docker kloudview
sudo install -o kloudview -g kloudview -m 0755 kloudview-agent /var/lib/kloudview/bin/kloudview-agent
sudo systemctl daemon-reload
sudo systemctl enable --now kloudview-agent
sudo systemctl status kloudview-agent
```

The unit must carry `SupplementaryGroups=systemd-journal,adm,docker` for those groups
to take effect.

## Permission boundary

The default unit runs as the unprivileged `kloudview` user with the supplementary
groups its enabled collections need (see [Collection options](#collection-options)). Inventory and Metrics collect only readable kernel and runtime information. `service.restart` should succeed only on hosts where both the allowlist and a separate least-privilege policy are configured. Root privileges are not granted to the entire Agent process.

The terminal relay is disabled by default. Enabling it runs the shell commands of an approved session with the Agent user's privileges, so a separate sandbox and least-privilege policy must be configured first. Compose leaves it enabled for local development only.

The enrollment token is used only for bootstrap, and a per-Agent runtime credential is issued in the enrollment response. The credential hash and the previous one are persisted with the agent record, so a server restart does not invalidate a rotated credential. The credential is bound to the Agent ID with the server-only `KLOUDVIEW_AGENT_CREDENTIAL_KEY`, which is not deployed to the Agent. If this key is changed, existing Agents fail authentication and re-enroll with the bootstrap token. A production deployment must add one-time bootstrap, mTLS, individual revocation, and automatic rotation.

The issued Agent ID, node ID, and runtime credential are stored atomically at `/var/lib/kloudview/agent.json` with `0600` permissions by default. On restart, this identity is used first, and re-enrollment with the bootstrap token happens only when the server rejects it with `401` or `404`. systemd's `StateDirectory` prepares the ownership and mode of the storage directory.
