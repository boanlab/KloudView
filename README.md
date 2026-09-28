# KloudView

KloudView is an open-source infrastructure operations platform for monitoring and
operating a hierarchy of bare-metal nodes, hypervisors, VMs, containers, and host
processes. It answers two questions: what is failing or saturated right now, and what
was done to the machine that got it there.

A server holds the fleet's state and serves the console; an agent, one static Go
binary, runs on each managed host and reports to it.

| Guide | For |
|---|---|
| [Deployment](docs/deployment.md) | Running it, configuring it, and taking it to production |
| [Console](docs/console.md) | What each page is for |
| [Agent installation](docs/agent-installation.md) | Putting an agent on a host, and taking it off |
| [HTTP API](docs/api.md) · [OpenAPI](docs/openapi.json) | The API, and its generated reference |
| [Alert routing](docs/alert-routing.md) · [Terminal security](docs/terminal-security.md) · [PostgreSQL](docs/postgresql.md) | One subsystem each |
| [Roadmap](docs/roadmap.md) · [Open decisions](docs/open-decisions.md) | What is not built, and what is waiting on a call |

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

Interface language switches between English and Korean from the topbar.

## Authentication

Session login. `admin` is the only account seeded, with the password from
`KLOUDVIEW_ADMIN_PASSWORD`; users, teams, roles, scopes and bindings are created under
**User Management** in the console.

## Testing

```bash
make test        # Server and Agent, with a PostgreSQL the target starts and removes
make test-e2e    # the console in a browser, against a stack and two agents of its own
```

Both run on Docker; neither needs Go or Node on the host. `make test` starts a
PostgreSQL for the store tests, which skip themselves without one.

Console unit tests on their own:

```bash
cd apps/web
npm test
```

## Agents

An agent is one static Go binary on the managed host. Its identity comes from its
hostname, so **hostnames must be unique across the fleet**. Installing and removing are
one line each, served by the server:

```bash
curl -fsSL <server>/api/v1/agent-install.sh   | sudo sh -s -- <enrollment-token>
curl -fsSL <server>/api/v1/agent-uninstall.sh | sudo sh
```

The installer verifies the build's checksum, creates an unprivileged account with the
groups its collections need, and enrols. Removal takes everything off the host; the
console keeps the node and its history until it is removed there too.

Publish the builds the server offers with `make verify-agent-binaries VERSION=x.y.z`,
then name that version in `KLOUDVIEW_AGENT_TARGET_VERSION`.

## API scope

- Agent enrollment, heartbeat, and inventory
- Resource, group, membership, and relation queries and CRUD
- CPU, memory, disk, and network ingestion, aggregation, and per-resource history
- Capacity and rightsizing analysis
- Alert rules, alerts, silences, inhibitions, notification routing, and incidents
- Approval-gated operations, runbooks, and terminal sessions
- Users, teams, roles, scopes, bindings, and audit

## Contributing

Please review [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md). The project is distributed under the Apache License 2.0.
