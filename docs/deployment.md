# Deployment guide

A step-by-step walkthrough for anyone deploying and configuring KloudView for the first time. It covers everything from a local bring-up to monitoring real production nodes.

## 1. Prerequisites

- **Docker** and **Docker Compose v2** (no Go/Node installation required on the host — builds and tests all run in containers)
- Ports that must be open: console/API **8080**, and internal-only PostgreSQL 5432
- Managed nodes must have **outbound** access to the Server on port 8080 (the agent connects to the Server).

## 2. Configuration (`.env`)

```bash
tools/gen-env.sh --url https://kloudview.example.com
```

This writes `.env` mode 0600 with random secrets and prints the generated admin
password once. It refuses to overwrite an existing `.env`: replacing the secrets
on a running deployment locks out the database, which keeps the password it was
initialised with, and every enrolled agent.

`cp .env.example .env` also works, but every value then has to be replaced by
hand — the placeholders are published here, and the server refuses to start on
them.

| Key | Change required | Description |
|---|---|---|
| `KLOUDVIEW_ENROLLMENT_TOKEN` | ✅ | Agent initial enrollment token. **At least 16 characters.** Server and agent use the same value |
| `KLOUDVIEW_AGENT_CREDENTIAL_KEY` | ✅ | **Server-only** key for binding agent credentials. **At least 32 characters, must differ from the token above** (the Server refuses to start if they match). Not distributed to the agent |
| `KLOUDVIEW_POSTGRES_PASSWORD` | ✅ | PostgreSQL password |
| `KLOUDVIEW_ADMIN_PASSWORD` | ✅ (production) | Initial `admin` console account password. Default `admin` |

> `.env` is included in `.gitignore` and is not committed. Do not push secrets to the repository.

Once the fleet is cleared, agents take the build across a ten-minute window rather than
at once, each waiting out an offset derived from its own id. `GET /api/v1/agents/rollout`
reports how many are on the target and names any whose turn has passed without taking
it; the Agents page leads with the same count.
If the Server needs a separate Web origin, specify the single origin to allow in `KLOUDVIEW_CORS_ORIGIN`. The default is same-origin only.
If `KLOUDVIEW_DATABASE_URL` is set, the Server persists state and metrics to PostgreSQL. The default Compose configuration uses the PostgreSQL adapter. Only when no connection string is present does it fall back to JSON snapshots at `KLOUDVIEW_STATE_PATH` and `KLOUDVIEW_ACCESS_STATE_PATH`.
After enrollment, the agent uses a per-ID runtime credential. Production deployments should add Server certificate verification, one-time bootstrap, mTLS, and per-credential rotation.
The Server and agent must specify the same `KLOUDVIEW_ENROLLMENT_TOKEN`, which must be at least 16 characters. The Compose default is for local development only, so always change it to a random value in externally exposed environments.
`KLOUDVIEW_AGENT_CREDENTIAL_KEY` is a separate random value of at least 32 characters provided only to the Server. If you use the same value as the enrollment token, the Server refuses to start. Do not distribute this value to agent containers or hosts.

### Every server value

Compose reads these from `.env`. A default in this table is what Compose sets unless
the description says otherwise; the server binary on its own has no web root or state
path until one is given.

| Environment variable | Default | Description |
|---|---|---|
| `KLOUDVIEW_ADDR` | `:8080` | Server listen address |
| `KLOUDVIEW_ENROLLMENT_TOKEN` | (change required) | Agent initial enrollment token, at least 16 characters |
| `KLOUDVIEW_AGENT_CREDENTIAL_KEY` | (change required) | Server-only key for binding agent credentials, at least 32 characters, must differ from the token |
| `KLOUDVIEW_DATABASE_URL` | Compose PostgreSQL | PostgreSQL if set, otherwise JSON snapshot |
| `KLOUDVIEW_STATE_PATH` | `/var/lib/kloudview/state.json` | JSON snapshot path used when no database is configured |
| `KLOUDVIEW_ACCESS_STATE_PATH` | `/var/lib/kloudview/access.json` | Roles, scopes and bindings, alongside the state snapshot |
| `KLOUDVIEW_WEB_ROOT` | `/opt/kloudview/web` | Directory the console is served from |
| `KLOUDVIEW_DEV_HEADER_AUTH` | `false` | Accepts `X-KloudView-Subject` in place of a session. Local development and tests only |
| `KLOUDVIEW_ADMIN_PASSWORD` | `admin` | Initial `admin` account password (change before production) |
| `KLOUDVIEW_CORS_ORIGIN` | same-origin | Specify a single origin to allow a separate Web origin |
| `KLOUDVIEW_AGENT_RELEASE_PATH` | `/opt/kloudview/releases` | Directory of `kloudview-agent-linux-*` builds offered to agents |
| `KLOUDVIEW_AGENT_TARGET_VERSION` | unset | Version agents should run; no update is advertised while unset |

## Working without a colleague

A new deployment seeds one account, and three actions wait for a second person: a
terminal session, a high-risk runbook, and a service restart. `admin` holds every other
permission through `*:*` and still cannot approve its own request for these, by design.

Either create a second account to approve with - which is what the record is for - or,
where one person genuinely runs the fleet, create a role naming the grants that apply and
bind it to them under **User Management**:

| Grant | Lets them alone |
|---|---|
| `terminal:approve-self` | open a shell |
| `runbooks:approve-self` | release a high-risk runbook |
| `operations:approve-self` | restart a service, including as a runbook step |

The grant is visible in the role, and every approval names who gave it in the audit log.
| `KLOUDVIEW_AGENT_CANARY` | unset | Node taking a new agent build first; the rest follow after its soak. Unset updates every agent at once |
| `KLOUDVIEW_AGENT_CANARY_SOAK` | `10m` | How long the canary must hold a build before the fleet is offered it |

Once the fleet is cleared, agents take the build across a ten-minute window rather than
at once, each waiting out an offset derived from its own id. `GET /api/v1/agents/rollout`
reports how many are on the target and names any whose turn has passed without taking
it; the Agents page leads with the same count.
| `KLOUDVIEW_PUBLIC_URL` | unset | Address operators and agents reach the server on; the startup hardening check confirms it is `https://` |
| `KLOUDVIEW_METRIC_RAW_DAYS` | `30` | Days of full-resolution samples to keep |
| `KLOUDVIEW_METRIC_ROLLUP_DAYS` | `400` | Days of rolled-up samples to keep |
| `KLOUDVIEW_METRIC_ROLLUP_SECONDS` | `60` | Rollup bucket size |

If the Server needs a separate Web origin, specify the single origin to allow in `KLOUDVIEW_CORS_ORIGIN`. The default is same-origin only.

If `KLOUDVIEW_DATABASE_URL` is set, the Server persists state and metrics to PostgreSQL. The default Compose configuration uses the PostgreSQL adapter. Only when no connection string is present does it fall back to JSON snapshots at `KLOUDVIEW_STATE_PATH` and `KLOUDVIEW_ACCESS_STATE_PATH`.

After enrollment, the agent uses a per-ID runtime credential. Production deployments should add Server certificate verification, one-time bootstrap, mTLS, and per-credential rotation.

The Server and agent must specify the same `KLOUDVIEW_ENROLLMENT_TOKEN`, which must be at least 16 characters. The Compose default is for local development only, so always change it to a random value in externally exposed environments.

`KLOUDVIEW_AGENT_CREDENTIAL_KEY` is a separate random value of at least 32 characters provided only to the Server. If you use the same value as the enrollment token, the Server refuses to start. Do not distribute this value to agent containers or hosts.


## 3. Start

```bash
docker compose up --build -d
```

Startup proceeds in the order PostgreSQL → Server. Check status:

```bash
curl -s http://localhost:8080/healthz          # {"status":"ok",...}
docker compose ps
```

## 4. First login

Open `http://localhost:8080` in a browser → the **login screen** appears.

- Administrator: `admin` / `KLOUDVIEW_ADMIN_PASSWORD` (default `admin`)

After logging in, manage accounts and permissions under **User Management → Users / Teams / Roles / Scopes / Role bindings**. New users can be assigned a role at creation time (automatic binding) or granted one separately under Role bindings.

## 5. Installing the agent on real nodes

Publish the builds the server offers, then install from the server:

```bash
make verify-agent-binaries VERSION=0.1.0 # amd64+arm64, static-link check, SHA256SUMS, VERSION
```

```bash
curl -fsSL <server>/api/v1/agent-install.sh | sudo sh -s -- <enrollment-token>
```

The script picks the build for the host's architecture, verifies its SHA-256 against the
digest the server published, creates the unprivileged `kloudview` account, grants it the
groups its collections need, writes `/etc/kloudview/agent.env` and the systemd unit, and
enrols. Collections are on by default and declined with `--no-logs`, `--no-containers`,
`--no-vms`, `--no-terminal`; updates with `--no-auto-update`.

Hostnames identify agents and must be unique across the fleet. Re-running the command on
a host keeps its identity, which is how a host takes a new build after being pinned, or
picks up a runtime installed after the agent.

Removing takes everything off the host:

```bash
curl -fsSL <server>/api/v1/agent-uninstall.sh | sudo sh
```

The console keeps the node and its history until it is removed there too.

After enrollment the agent appears under **Agents** and on the **Dashboard**, and collects
`/proc` and `/sys` along with `systemctl`, `docker` and `virsh` listings **read-only**. For
the permission boundary and a by-hand install, see
[agent-installation.md](agent-installation.md).

## 6. Production hardening checklist

The server checks the items it can see about itself at startup and logs each
unmet one as `WARN not production hardened`. Check `docker compose logs server`
after any configuration change: a deployment meeting none of this list looks
exactly as healthy as one meeting all of it. TLS cannot be detected from inside
the process, so set `KLOUDVIEW_PUBLIC_URL` to the address operators and agents
actually use and the check will confirm it is `https://`.

- [ ] Change `KLOUDVIEW_ADMIN_PASSWORD` to a strong value
- [ ] `KLOUDVIEW_ENROLLMENT_TOKEN` (≥16 chars) and `KLOUDVIEW_AGENT_CREDENTIAL_KEY` (≥32 chars) are random and differ from each other. `tools/gen-env.sh` produces both; the server refuses to start on the `.env.example` placeholders or the compose defaults, since both are published here
- [ ] Place a **TLS-terminating reverse proxy** (nginx/traefik, etc.) in front of 8080 and serve the console over HTTPS. The proxy must pass `X-Forwarded-Proto`, which is how the server knows the browser used HTTPS: without it every same-origin request is refused as cross-origin and the console loads blank
- [ ] Establish a PostgreSQL backup policy (state and metrics are persisted in the `kloudview` DB)
- [ ] Automate large-scale group management with **Dynamic groups → Auto-group by label** (minimize manual mapping)

## 7. Observability and data

- State and access control are stored in PostgreSQL JSONB documents, and metrics in time-series tables (BRIN index, downsampling, retention).
- Without `KLOUDVIEW_DATABASE_URL`, it operates on atomic JSON snapshots at `KLOUDVIEW_STATE_PATH`/`KLOUDVIEW_ACCESS_STATE_PATH` (single node).
- Logs: `docker compose logs -f server`.

## 8. Testing

```bash
make test                       # Server and Agent, with a PostgreSQL the target starts
make test-e2e                   # the console in a browser, on a stack and agents of its own
cd apps/web && npm test         # console unit tests on their own
```

## 9. Troubleshooting

- **Stuck at the login screen**: The `admin` password is `KLOUDVIEW_ADMIN_PASSWORD`. If it still fails after changing the value, it may still be the value from the initial seed, so log in with an admin account and reset the password under Users.
- **Agent not showing up**: On the target host, verify reachability with `curl http://<server-host>:8080/healthz`, confirm `KLOUDVIEW_ENROLLMENT_TOKEN` matches the Server, and check `journalctl -u kloudview-agent`.
- **All APIs return 401/403**: Session expired or permissions not granted. Log in again and check Role bindings.

## Behind a TLS ingress

Terminate TLS at the ingress and forward `X-Forwarded-Proto` and `X-Forwarded-Host`.

`X-Forwarded-Proto` is required, not optional. The server is reached over plain
HTTP behind the proxy, so it has no other way to know the browser used HTTPS;
without the header every same-origin request is refused as cross-origin and the
console renders blank. The agent install script also builds its URLs from these,
so the command shown in the console points at the public address rather than the
container's.
