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

The installer turns this on, because a fleet that does not take the build it is
offered reports a released rollout and stays where it was. Install with
`--no-auto-update` to pin a host instead; it then takes a new build when the install
command is run on it again.

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

| Environment variable | Default | Description |
|---|---|---|
| `KLOUDVIEW_SERVER_URL` | `http://127.0.0.1:8080` | Central Server address |
| `KLOUDVIEW_ENROLLMENT_TOKEN` | none | Initial enrollment token |
| `KLOUDVIEW_INTERVAL` | `10s` | Heartbeat and metric interval |
| `KLOUDVIEW_STATE_PATH` | `/var/lib/kloudview/agent.json` | Storage path for issued credentials |
| `KLOUDVIEW_ALLOWED_SERVICES` | none | Allowlist of services permitted for `service.status/restart` (comma-separated) |
| `KLOUDVIEW_TERMINAL_ENABLED` | `false` | Whether to run approved terminal sessions |
| `KLOUDVIEW_TERMINAL_USER` | none | Account to drop privileges to when running terminals/commands |
| `KLOUDVIEW_LOG_STREAM` | `true` | Stream warning-and-worse journal lines plus login activity |
| `KLOUDVIEW_AUTO_UPDATE` | `false` | Install the agent build the server advertises, after verifying its checksum. The installer writes `true`; `--no-auto-update` pins a host instead |

An agent's identity comes from its hostname, so **hostnames must be unique across the
fleet**; a second machine enrolling under a name another holds is refused. Installing
and removing are one line each, served by the server:

```bash
curl -fsSL <server>/api/v1/agent-install.sh   | sudo sh -s -- <enrollment-token>
curl -fsSL <server>/api/v1/agent-uninstall.sh | sudo sh
```

Removal takes everything off the host, identity included; the console keeps the node
and its history until it is removed there too. See
[docs/agent-installation.md](docs/agent-installation.md).


Installing by hand needs the same group membership the installer grants:

```bash
sudo useradd --system --home /var/lib/kloudview --shell /usr/sbin/nologin kloudview
sudo usermod -aG systemd-journal,adm,docker kloudview
sudo install -o kloudview -g kloudview -m 0755 kloudview-agent /var/lib/kloudview/bin/kloudview-agent
sudo systemctl daemon-reload
sudo systemctl enable --now kloudview-agent
sudo systemctl status kloudview-agent
```

The installer writes the groups it granted into the unit's `SupplementaryGroups`, but
systemd applies those together with whatever the `kloudview` account belongs to, so
either is enough. Adding a group to the account takes effect on the next start of the
service, not on the next beat: a running process cannot be given one.

## Adding a runtime later

A hypervisor or container runtime installed after the agent is found on the next beat —
the agent looks for `virsh` and `docker` every time it collects — but the group that
lets it read them is granted at install time, and the group does not exist yet on a
host that has neither. The agent is then refused, and refuses quietly in the case of a
hypervisor, whose `virsh` answers "no domains" rather than failing when it is pointed
at a daemon it can reach and nobody has used.

After installing KVM or a container runtime on a host that already has the agent,
restart the agent:

```bash
sudo systemctl restart kloudview-agent
```

The unit grants the collection groups that exist at that moment before the agent
starts, so the one the new runtime brought with it is picked up. The list it works
from is fixed at install time by the collections that were chosen, so a host
installed with `--no-vms` stays without the hypervisor group. Re-running the install
command also works and is what changes that list.

The agent logs the first failure of each collection command, so a host in this state
says `collection command failed` with what the command reported, once, rather than
reporting an empty list.

## Removal

```bash
curl -fsSL https://kloudview.example.com/api/v1/agent-uninstall.sh | sudo sh
```

Stops and disables the unit, removes it, `/etc/kloudview`, `/var/lib/kloudview`, and
the `kloudview` account with the group memberships that came with it. It is safe to
run on a host that never had the agent, and safe to run twice.

Nothing is kept, identity included. `--dry-run` prints what it would remove and
changes nothing.

Setting the host up again is the install command again. An agent id is derived from
the hostname, so the host enrols into the record it had before and keeps its history;
there is nothing on disk worth carrying across a removal. Re-running the install
command is also how a pinned host takes a new build.

The script touches only the host it runs on. The console keeps the node, its
resources and their history; the agent stops reporting and goes offline. Removing the
agent in the console as well discards that node, every resource under it, and all of
their metrics — do that only when the history is meant to go too.

## Permission boundary

The default unit runs as the unprivileged `kloudview` user with the supplementary
groups its enabled collections need (see [Collection options](#collection-options)). Inventory and Metrics collect only readable kernel and runtime information. `service.restart` should succeed only on hosts where both the allowlist and a separate least-privilege policy are configured. Root privileges are not granted to the entire Agent process.

The terminal relay is disabled by default. Enabling it runs the shell commands of an approved session with the Agent user's privileges, so a separate sandbox and least-privilege policy must be configured first. Compose leaves it enabled for local development only.

The enrollment token is used only for bootstrap, and a per-Agent runtime credential is issued in the enrollment response. The credential hash and the previous one are persisted with the agent record, so a server restart does not invalidate a rotated credential. The credential is bound to the Agent ID with the server-only `KLOUDVIEW_AGENT_CREDENTIAL_KEY`, which is not deployed to the Agent. If this key is changed, existing Agents fail authentication and re-enroll with the bootstrap token. A production deployment must add one-time bootstrap, mTLS, individual revocation, and automatic rotation.

An agent's identity is its hostname: the Agent ID and node ID are derived from it, so
**hostnames must be unique across the fleet**. A second machine enrolling under a name
another machine already holds is refused with `agent_hostname_taken`, told apart by the
host's own machine id (`/etc/machine-id`). The same machine re-enrolling reports the
same id and takes its record back, which is why a full uninstall loses nothing. A host
whose system keeps no machine id still enrols, and cannot be told apart from another of
the same name.

The issued Agent ID, node ID, and runtime credential are stored atomically at `/var/lib/kloudview/agent.json` with `0600` permissions by default. On restart, this identity is used first, and re-enrollment with the bootstrap token happens only when the server rejects it with `401` or `404`. systemd's `StateDirectory` prepares the ownership and mode of the storage directory.
