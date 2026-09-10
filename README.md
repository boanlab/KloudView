# KloudView

KloudView is an open-source infrastructure operations platform for monitoring and
operating a hierarchy of bare-metal nodes, hypervisors, VMs, containers, and host
processes. It answers two questions: what is failing or saturated right now, and what
was done to the machine that got it there.

## Components

- `apps/server`: Central API, state aggregation, and operations coordination
- `apps/agent`: A single Go binary installed on managed targets
- Web Console (`apps/web`): Monitoring, hierarchy navigation, and resource management UI
- `deploy`: systemd and deployment assets
- `docs`: Architecture, ADRs, developer, and user documentation

Start with the [Deployment guide](docs/deployment.md) and the [Console guide](docs/console.md).
Also available: [HTTP API](docs/api.md), [Alert routing](docs/alert-routing.md),
[PostgreSQL storage](docs/postgresql.md), [Terminal security](docs/terminal-security.md),
[Agent installation](docs/agent-installation.md), [OpenAPI document](docs/openapi.json),
the [Roadmap](docs/roadmap.md), and the [open decisions](docs/open-decisions.md).

## Quick start

All you need is Docker and Docker Compose (no Go/Node required on the host).

```bash
# 1) Generate .env with random secrets (prints the admin password once)
tools/gen-env.sh --url https://kloudview.example.com

# 2) Start PostgreSQL and the Server
docker compose up --build -d
```

- **Web Console**: `http://localhost:8080` — log in as `admin` with the password `tools/gen-env.sh` printed
- **Health**: `http://localhost:8080/healthz`
- **PostgreSQL**: Compose-internal only at `postgres:5432`

Shut down:

```bash
docker compose down       # stop the stack (data preserved)
docker compose down -v    # stop and wipe data
```

Images build as version `dev`. Stamp a release instead with `make build VERSION=x.y.z`;
the value is reported by `GET /healthz` and `GET /api/v1/system/info`.

For detailed deployment, configuration, and production transition, see [docs/deployment.md](docs/deployment.md).

## Console

The console is a dependency-free ES-module SPA served by the server. Every view has
its own URL (`/utilization`, `/resources/{id}?tab=hardware`), so a view can be
reloaded, bookmarked, and shared.

| Section | Pages |
|---|---|
| — | Dashboard |
| Issues | Alerts, Incidents |
| Infrastructure | Infrastructure Map, Resources, Utilization, Logs, Agents |
| Operations | Remote shell, Automations, Task history |
| Settings | User Management, Group Management, Unmanaged, Alerting, Audit log |

Interface language switches between English and Korean from the topbar. See
[docs/console.md](docs/console.md) for what each page is for.

## Authentication

Access to the Web Console is protected by **session login**. On first bring-up, only the administrator account is seeded.

- `admin`, with the password from `KLOUDVIEW_ADMIN_PASSWORD`. The code default is `admin`; `.env.example` ships a placeholder to replace

Users, teams, roles, scopes, and bindings are managed under **User Management** in the console. `admin` is the only account seeded; create the rest there.

## Testing

```bash
make test
```

Server and Agent tests run on Docker without installing Go on the host.

Web Console unit tests:

```bash
cd apps/web
npm test
```

## Agent configuration

Build static Linux amd64 and arm64 binaries, with checksums and the version the
server checks a release directory against:

```bash
make verify-agent-binaries VERSION=0.1.0
./dist/kloudview-agent-linux-amd64 --version
```

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
| `KLOUDVIEW_AUTO_UPDATE` | `false` | Install the agent build the server advertises, after verifying its checksum |

## Server environment variables

Compose reads the following values from `.env`.

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
| `KLOUDVIEW_AGENT_CANARY` | unset | Node taking a new agent build first; the rest follow after its soak. Unset updates every agent at once |
| `KLOUDVIEW_AGENT_CANARY_SOAK` | `10m` | How long the canary must hold a build before the fleet is offered it |
| `KLOUDVIEW_PUBLIC_URL` | unset | Address operators and agents reach the server on; the startup hardening check confirms it is `https://` |
| `KLOUDVIEW_METRIC_RAW_DAYS` | `30` | Days of full-resolution samples to keep |
| `KLOUDVIEW_METRIC_ROLLUP_DAYS` | `400` | Days of rolled-up samples to keep |
| `KLOUDVIEW_METRIC_ROLLUP_SECONDS` | `60` | Rollup bucket size |

If the Server needs a separate Web origin, specify the single origin to allow in `KLOUDVIEW_CORS_ORIGIN`. The default is same-origin only.

If `KLOUDVIEW_DATABASE_URL` is set, the Server persists state and metrics to PostgreSQL. The default Compose configuration uses the PostgreSQL adapter. Only when no connection string is present does it fall back to JSON snapshots at `KLOUDVIEW_STATE_PATH` and `KLOUDVIEW_ACCESS_STATE_PATH`.

After enrollment, the agent uses a per-ID runtime credential. Production deployments should add Server certificate verification, one-time bootstrap, mTLS, and per-credential rotation.

The Server and agent must specify the same `KLOUDVIEW_ENROLLMENT_TOKEN`, which must be at least 16 characters. The Compose default is for local development only, so always change it to a random value in externally exposed environments.

`KLOUDVIEW_AGENT_CREDENTIAL_KEY` is a separate random value of at least 32 characters provided only to the Server. If you use the same value as the enrollment token, the Server refuses to start. Do not distribute this value to agent containers or hosts.

## API scope

- Agent enrollment, heartbeat, and inventory
- Resource, group, membership, and relation queries and CRUD
- CPU, memory, disk, and network ingestion, aggregation, and per-resource history
- Capacity and rightsizing analysis
- Alert rules, alerts, silences, inhibitions, notification routing, and incidents
- Approval-gated operations, runbooks, and terminal sessions
- Users, teams, roles, scopes, bindings, and audit

Every endpoint is listed in [docs/api.md](docs/api.md) and `docs/openapi.json`.

## Contributing

Please review [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md). The project is distributed under the Apache License 2.0.
