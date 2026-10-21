# Deploying with Ansible

[한국어](ansible-deployment.ko.md)

The playbooks in [`deploy/ansible`](../deploy/ansible) put the server on one host
and an agent on every managed host, and take both through later upgrades. They
drive what the repository already ships — `docker-compose.yml` for the server,
the server's own installer for agents — rather than reimplementing it, so what
this guide does is what [deployment.md](deployment.md) and
[agent-installation.md](agent-installation.md) describe, run from one place.

## 1. Topology

```text
 Control node (operator workstation)
 ┌───────────────────────────────────────┐
 │ git checkout of this repository       │
 │ deploy/ansible   playbooks and roles  │
 │ dist/            agent builds         │
 │ ~/.ansible/kloudview-vault-pass       │
 └──────────────┬────────────────────────┘
                │ SSH (Ansible)
       ┌────────┴──────────────────────────────────────┐
       ▼                                               ▼
 Server host (kloudview_server)                  Managed hosts (kloudview_agents)
 ┌───────────────────────────────────────┐      ┌──────────────────────────────┐
 │ TLS reverse proxy :443  (yours)       │◀─────│ kloudview-agent (systemd,    │
 │        │ X-Forwarded-Proto/Host       │ HTTPS│ unprivileged kloudview user) │
 │        ▼                              │ out- │                              │
 │ Docker Compose project "kloudview"    │ bound│ installed by the server's    │
 │ ┌───────────────┐   ┌───────────────┐ │      │ /api/v1/agent-install.sh     │
 │ │ server :8080  │──▶│ postgres 17   │ │      └──────────────────────────────┘
 │ │ console + API │   │ internal only │ │
 │ └───────────────┘   └───────────────┘ │
 └───────────────────────────────────────┘
```

- Ansible reaches every machine over SSH; nothing on the managed hosts connects
  back to the control node.
- Agents open **outbound** connections to the server's public address. They
  fetch the installer, the binary, and later updates from it, and report to it.
- PostgreSQL is reachable only inside the Compose network.
- The TLS proxy is **not** part of the playbooks. Port 8080 (`kloudview_server_http_port`)
  is published on the server host; put a proxy in front of it that passes `X-Forwarded-Proto` and
  `X-Forwarded-Host` ([why](deployment.md#behind-a-tls-ingress)).

### What ends up where

| Machine | Path | Contents |
|---|---|---|
| Server | `/opt/kloudview/src/` | The deployed commit, exported with `git archive` |
| Server | `/opt/kloudview/src/.env` | Secrets and settings, mode `0600`, written from the vault |
| Server | `/opt/kloudview/src/dist/` | Agent builds offered to the fleet (mounted read-only) |
| Server | volumes `kloudview_kloudview-postgres-data`, `kloudview_kloudview-data` | Database and server state. Survive every redeploy |
| Agent | `/var/lib/kloudview/bin/kloudview-agent` | The binary |
| Agent | `/var/lib/kloudview/agent.json` | The host's enrolled identity and credential |
| Agent | `/etc/kloudview/agent.env`, `/etc/systemd/system/kloudview-agent.service` | Configuration and unit, written by the installer |

## 2. What the playbooks do

| Playbook | Hosts |
|---|---|
| `server.yml` | `kloudview_server` |
| `agents.yml` | `kloudview_agents` |
| `site.yml` | both, server first |

**`server.yml`** (role `kloudview_server`), in order:

1. **Preflight.** The secrets meet the server's own rules (token ≥ 16 characters,
   credential key ≥ 32, the two differ). Agent builds exist in `dist/`, since the
   installer answers `503` without them. If a target agent version is set, it
   matches the version `dist/VERSION` was stamped with.
2. **Docker.** Installs `docker.io`, `docker-compose-v2` and `rsync` from the
   distribution (Ubuntu/Debian). Skip with `kloudview_server_install_docker: false`.
3. **Source.** Exports `kloudview_server_git_ref` (default `HEAD`) from the
   control node's checkout with `git archive` and rsyncs it to
   `/opt/kloudview/src`. Uncommitted changes and untracked files are not
   deployed. `.env` and `dist/` on the server are left out of the sync.
4. **Agent builds.** Rsyncs `dist/` to the server.
5. **`.env`.** Renders `templates/env.j2` from the vault and group variables.
6. **Compose.** `docker compose up` for project `kloudview`. The server image is
   rebuilt only when the source changed. Compose recreates a container whose
   configuration changed, so a changed `.env` also takes effect. The build
   runs the server's test suite, so a broken commit fails here.
7. **Checks.** Waits for `/healthz`, then prints any `not production hardened`
   warning the server logged at startup.

**`agents.yml`** (role `kloudview_agent`):

1. Refuses to run if two hosts report the same hostname, because an agent's
   identity is its hostname.
2. Installs `curl` and downloads `<public_url>/api/v1/agent-install.sh`.
3. Runs it with the enrollment token passed through the environment. That keeps
   the token out of the process list and the Ansible output. A host that
   already has `/var/lib/kloudview/agent.json` is skipped unless
   `kloudview_agent_reinstall=true` is set.
4. Confirms `kloudview-agent` is running and enabled.

With `kloudview_agent_state=absent`, it runs the server's uninstaller instead.

Every run is idempotent. Rerunning with nothing changed reports `changed=0`.

## 3. Secrets

`deploy/ansible/inventory/group_vars/all/vault.yml` holds the enrollment token,
the agent credential key, the PostgreSQL password and the initial admin
password, encrypted with `ansible-vault`.

The key that opens it is `~/.ansible/kloudview-vault-pass`, which
`deploy/ansible/ansible.cfg` points at. It lives outside the repository so it
cannot be committed. Give it to other operators over a password manager, never
through the repository or chat.

**Neither the vault nor `inventory/hosts.yml` belongs in this repository.**
Both describe one deployment — its secrets, its addresses — and this repository
is the project every deployment starts from. A vault committed here would be
one that nobody else can open, and every other deployment would have to
overwrite a tracked file to create its own. Keep a deployment's
`vault.yml` and `hosts.yml` in a private repository of their own, or back them
up next to the password file.

**Back the password file up.** Without it the vault cannot be opened, and the
values in it cannot simply be regenerated:

- the database keeps the password it was initialised with,
- enrolled agents hold credentials bound to the credential key,
- the admin password is seeded on the first start only.

To create both files on a control node that has neither:

```bash
deploy/ansible/gen-vault.sh
```

It refuses to replace either file without `--force`. Replacing the secrets of a
running deployment locks out its database and every enrolled agent.

**Moving to another control node** takes both files. `vault.yml` comes with
the repository, but the password file does not, and Ansible looks for it under
the home directory of whoever runs it. Without it, every command stops with
`The vault password file … was not found`:

```bash
# on the new control node
mkdir -p ~/.ansible && chmod 700 ~/.ansible
scp <old-control-node>:.ansible/kloudview-vault-pass ~/.ansible/
chmod 600 ~/.ansible/kloudview-vault-pass
ansible-vault view deploy/ansible/inventory/group_vars/all/vault.yml >/dev/null && echo ok
```

```bash
cd deploy/ansible
ansible-vault view inventory/group_vars/all/vault.yml   # e.g. the first admin password
ansible-vault edit inventory/group_vars/all/vault.yml
```

## 4. First deployment

### 4.1 Control node

```bash
python3 -m venv ~/.venvs/ansible
~/.venvs/ansible/bin/pip install ansible-core
export PATH=~/.venvs/ansible/bin:$PATH
ansible-galaxy collection install community.docker community.general ansible.posix
```

Also needed: `git`, `rsync`, and Docker to build the agent binaries.

### 4.2 Inventory and settings

- `deploy/ansible/inventory/hosts.yml`, copied from `hosts.example.yml`, which
  explains each name and the `--limit` patterns: the server under `kloudview_server`, and
  managed hosts under `kloudview_agents`.
- `deploy/ansible/inventory/group_vars/all/main.yml`: `kloudview_public_url`, the
  `https://` address operators and agents reach the server on.
- Optional overrides, in the same file or in `group_vars/kloudview_server.yml`:

| Variable | Default | Meaning |
|---|---|---|
| `kloudview_server_git_ref` | `HEAD` | Commit, tag or branch to deploy |
| `kloudview_server_version` | `dev` | Version reported by `/healthz` |
| `kloudview_server_agent_target_version` | empty | Agent version the fleet should run; empty advertises no update |
| `kloudview_server_agent_canary` | empty | Hostname that takes a new agent build first |
| `kloudview_server_agent_canary_soak` | `10m` | How long the canary holds a build before the rest follow |
| `kloudview_server_install_dir` | `/opt/kloudview` | Install location on the server |
| `kloudview_server_http_port` | `8080` | Host port the server is published on, or `127.0.0.1:8080` to keep it to the host for a proxy there |
| `kloudview_agent_options` | `[]` | Installer options: `--no-logs`, `--no-containers`, `--no-vms`, `--no-terminal`, `--no-auto-update` |

The installer turns on approval-gated shell sessions unless `--no-terminal` is
given.

### 4.3 Deploy

```bash
make verify-agent-binaries VERSION=0.1.0          # repository root: builds dist/

cd deploy/ansible
./gen-vault.sh                                    # once: password file and vault (section 3)
ansible all -m ping                               # SSH and sudo reach every host
ansible-playbook server.yml -e kloudview_server_version=0.1.0
ansible-playbook agents.yml --limit node01.example.com   # one host first
ansible-playbook agents.yml
```

Then log in at `kloudview_public_url` as `admin` with the vault's
`vault_kloudview_admin_password`, and change it. If one person runs the fleet,
see [working without a colleague](deployment.md#working-without-a-colleague).

### 4.4 Adding and removing hosts

```bash
# add the host to kloudview_agents in hosts.yml, then
ansible-playbook agents.yml --limit node03.example.com

# remove the agent from a host (the console keeps the node until removed there)
ansible-playbook agents.yml --limit node03.example.com -e kloudview_agent_state=absent
```

## 5. Upgrading and migrating

### 5.1 How the server migrates its database

Schema migrations are part of the server binary. On every start the server takes
a PostgreSQL advisory lock and applies every migration newer than the version
recorded in `kloudview_schema_migrations`, **all in one transaction**. So:

- A schema upgrade needs no separate step. Starting the new server performs it.
- A failed migration rolls back completely, and the server does not start. The
  `Start PostgreSQL and the server` task then fails and the database is
  unchanged.
- Migrations only go forward; there are no down migrations. An older server
  started on a newer schema does not refuse to run. It runs against tables it
  does not know, so **rolling back across a migration means restoring the
  backup taken before the upgrade**, not just redeploying the old commit.

### 5.2 Read the release first

On the control node, before deploying, compare what is running with what is
about to run:

```bash
git fetch --tags
git log --oneline <deployed>..<new>
git diff <deployed>..<new> -- docker-compose.yml .env.example docs/deployment.md apps/server/internal/store/postgres.go
```

| If the diff shows… | Then |
|---|---|
| A new key in `.env.example` or a new `environment:` entry in `docker-compose.yml` | Add it to `deploy/ansible/roles/kloudview_server/templates/env.j2`, and its value to `group_vars` or the vault. The template writes only the keys it lists, so a new key would otherwise stay unset |
| A new entry in `postgresMigrations` (`apps/server/internal/store/postgres.go`) | A schema change. The backup in 5.3 is your only way back |
| A changed `postgres:` image **major** version | Follow 5.6. The new major refuses the old data directory |
| A change to the agent/server contract (`docs/contracts/agent-server.json`) | Upgrade the server first, then agents (5.4), which is the order below anyway |

### 5.3 Upgrade the server

```bash
cd deploy/ansible

# 1. back up the database on the server
ansible kloudview_server -b -m ansible.builtin.shell -a \
  'cd /opt/kloudview/src && docker compose -p kloudview exec -T postgres pg_dump -U kloudview -Fc kloudview > /opt/kloudview/pre-upgrade-$(date +%Y%m%d%H%M).dump'

# 2. deploy the new commit
ansible-playbook server.yml -e kloudview_server_git_ref=v0.2.0 -e kloudview_server_version=0.2.0

# 3. confirm
curl -s https://kloudview.example.com/healthz         # {"status":"ok","version":"0.2.0"}
```

The playbook rebuilds the image because the source changed, recreates the
server container, and the server migrates the schema as it starts. PostgreSQL
and both volumes are untouched. Expect the console to be unavailable for the
seconds the container takes to restart.

Copy the dump off the server
(`ansible kloudview_server -b -m ansible.builtin.fetch -a 'src=/opt/kloudview/<file> dest=backups/ flat=true'`)
if the server's disk is not itself backed up.

To pin deployments to releases rather than whatever is checked out, set
`kloudview_server_git_ref` and `kloudview_server_version` in `group_vars`
instead of on the command line, and change them in the same commit that bumps
them.

### 5.4 Upgrade the agents

First build the new agent version on the control node:

```bash
make verify-agent-binaries VERSION=0.2.0
```

How it reaches the hosts depends on how they were installed.

**Auto-update (the installer's default).** The server hands the build out, and
the agents install it themselves:

```bash
ansible-playbook server.yml \
  -e kloudview_server_agent_target_version=0.2.0 \
  -e kloudview_server_agent_canary=node01        # optional: one host first
```

The server publishes the new build and target. The canary takes it first, and
the rest follow once it has held the build for the soak, spread over ten
minutes. Follow it on the console's **Agents** page or at
`GET /api/v1/agents/rollout`. If the target does not match `dist/VERSION`, the
preflight stops the run, because a mismatched pair is offered to nobody.

**Pinned (`--no-auto-update`).** Ansible owns the version:

```bash
ansible-playbook server.yml                                 # publish the new build
ansible-playbook agents.yml -e kloudview_agent_reinstall=true --limit node01
ansible-playbook agents.yml -e kloudview_agent_reinstall=true
```

Reinstalling keeps each host's identity and history.

Changing `kloudview_agent_options` on hosts already enrolled also needs
`kloudview_agent_reinstall=true`.

### 5.5 Roll back

If no migration was added between the two commits, redeploying the old one is
enough:

```bash
ansible-playbook server.yml -e kloudview_server_git_ref=v0.1.0 -e kloudview_server_version=0.1.0
```

If one was added, restore the dump taken before the upgrade as well, on the
server host:

```bash
cd /opt/kloudview/src
P="docker compose -p kloudview"
$P stop server
$P exec -T postgres dropdb   -U kloudview kloudview
$P exec -T postgres createdb -U kloudview kloudview
$P exec -T postgres pg_restore -U kloudview -d kloudview --no-owner < /opt/kloudview/pre-upgrade-<stamp>.dump
```

Then run `server.yml` with the old ref, which starts the server again.
Anything recorded after the backup is lost.

Agents roll back the same way they upgrade: set the target back (auto-update)
or reinstall from the old `dist/` (pinned).

### 5.6 PostgreSQL major version upgrade

When `docker-compose.yml` moves `postgres:` to a new major (for example 17 → 18),
the new server cannot read the old data directory. Dump, recreate, restore:

```bash
# on the server host, still on the old image
cd /opt/kloudview/src
docker compose -p kloudview exec -T postgres pg_dump -U kloudview -Fc kloudview > /opt/kloudview/pg-major.dump
docker compose -p kloudview down                                  # keeps volumes
docker volume rm kloudview_kloudview-postgres-data                # only after the dump is verified

# from the control node: deploys the new compose file, starts an empty database
ansible-playbook server.yml -e kloudview_server_git_ref=<new>
```

The new PostgreSQL creates the `kloudview` role from `KLOUDVIEW_POSTGRES_PASSWORD`,
and the server creates an empty schema on its first start. Replace it with the
dump exactly as in 5.5:

```bash
cd /opt/kloudview/src
P="docker compose -p kloudview"
$P stop server
$P exec -T postgres dropdb   -U kloudview kloudview
$P exec -T postgres createdb -U kloudview kloudview
$P exec -T postgres pg_restore -U kloudview -d kloudview --no-owner < /opt/kloudview/pg-major.dump
$P start server
```

Keep `KLOUDVIEW_POSTGRES_PASSWORD` unchanged across the move.

### 5.7 Changing the port

The server listens on 8080 inside its container; `kloudview_server_http_port`
picks the host port it is published on. Change both values together, in
`group_vars/all/main.yml`:

```yaml
kloudview_server_http_port: "9090"
kloudview_public_url: http://kloudview.example.com:9090
```

```bash
ansible-playbook server.yml
ansible-playbook agents.yml -e kloudview_agent_reinstall=true
```

The second run is needed once agents are enrolled. Each agent keeps the address
it was installed with in `/etc/kloudview/agent.env`. Without the reinstall, it
goes on calling the old port and drops offline. Reinstalling rewrites that
address and keeps the host's identity and history.

Behind a TLS proxy on the same host, `127.0.0.1:8080` publishes the server to
the proxy alone. `kloudview_public_url` is then the proxy's `https://` address,
and agents need no change when the internal port moves.

### 5.8 Changing secrets

| Secret | Effect of changing it in the vault |
|---|---|
| `vault_kloudview_admin_password` | None after the first start. Change the password in the console |
| `vault_kloudview_enrollment_token` | New installs need the new value. Enrolled agents are unaffected, since they use their own credential |
| `vault_kloudview_agent_credential_key` | Every enrolled agent fails authentication and re-enrols with the token in its `agent.env`. Rotate the token in the same change only if you then reinstall every agent |
| `vault_kloudview_postgres_password` | The database keeps the old one. Run `ALTER ROLE kloudview PASSWORD '…'` in PostgreSQL first, then deploy |

## 6. Troubleshooting

| Symptom | Look at |
|---|---|
| `Require agent builds` fails | `dist/` is empty. Run `make verify-agent-binaries VERSION=…` |
| `Check the target matches the builds` fails | `kloudview_server_agent_target_version` differs from `dist/VERSION` |
| `Start PostgreSQL and the server` fails after an upgrade | `docker compose -p kloudview logs server` on the server. A failed migration is reported there, and the schema is unchanged |
| `Fetch the installer` fails with 503 | The server has no agent builds. Redeploy `server.yml` with `dist/` populated |
| Agent installs but never appears | `journalctl -u kloudview-agent` on the host, reachability of `kloudview_public_url` from it, and duplicate hostnames |
| `Report unmet hardening items` prints warnings | Each one names a [hardening item](deployment.md#6-production-hardening-checklist) |

## Appendix: verification record

The playbooks were exercised on 2026-10-20 against an Ubuntu 24.04 machine acting
as both server and agent (`ansible_connection: local`). The run used a separate
install directory and Compose project so nothing collided with a real
deployment, and was torn down afterwards. Nothing from it is still running.

```bash
# agent builds (no make on the machine: the Makefile recipe, run directly)
docker run --rm --user 1000:1000 -v $PWD:/src -w /src/apps/agent golang:1.24-alpine \
  sh -ec 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags netgo,osusergo \
          -ldflags "-s -w -X main.version=0.0.1-ansibletest" -o /src/dist/kloudview-agent-linux-amd64 ./cmd/kloudview-agent'

cd deploy/ansible
E="-i inventory/hosts.yml -i <test-inventory.yml> --limit kvtest"
ansible-playbook $E server.yml                                   # deployed, /healthz ok
ansible-playbook $E server.yml                                   # rerun: changed=0
ansible-playbook $E agents.yml -e kloudview_public_url=http://127.0.0.1:8080   # "agent enrolled"
ansible-playbook $E agents.yml -e kloudview_public_url=http://127.0.0.1:8080   # rerun: changed=0
ansible-playbook $E agents.yml -e ... -e kloudview_agent_state=absent          # removed; rerun changed=0

# upgrade and rollback path
ansible-playbook $E server.yml -e kloudview_server_git_ref=HEAD~2 -e kloudview_server_version=old
pg_dump (as in 5.3)                                              # schema version 6
ansible-playbook $E server.yml -e kloudview_server_version=new   # rebuilt, recreated, /healthz "new"
stop server, dropdb, createdb, pg_restore (as in 5.5), start     # restored, server healthy
```

Not exercised: a deployment over SSH to separate machines, a TLS proxy, an
auto-update rollout to agents, and a PostgreSQL major upgrade (5.6).
