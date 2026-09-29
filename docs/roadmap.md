# Roadmap

What KloudView does today is described in the [README](../README.md), the
[Console guide](console.md), and the [HTTP API](api.md). This page lists what it does
not do yet, and what "done" means for each item.

| Priority | Item | Scope | Done when |
|---|---|---|---|
| P0 | External identity | OIDC/OAuth2 login, session expiry and refresh, external user and group sync | OIDC login verified end to end alongside the existing local accounts |
| P0 | Agent trust | Per-agent mTLS credentials, certificate issuance, rotation, revocation | Forged agent connections rejected; certificates rotate without downtime |
| P0 | Server high availability | Multiple servers, PostgreSQL lease or advisory lock, shared WebSocket routing | One server fails without duplicated operations or notifications |
| P0 | Operation durability | Operation lease, retry policy, idempotency key, dead-letter state | No operation lost or duplicated across agent and server restarts |
| P1 | Observability | Structured server and agent logs, Prometheus metrics, trace correlation | A request, operation, and terminal session share one correlation ID |
| P1 | Notification channels | Channel types beyond the generic webhook - email, PagerDuty - and per-channel rate limiting | A channel type fails without affecting the others, with its delivery result recorded |
| P1 | Large fleets | Server-side filtering and pagination everywhere, virtual scrolling, aggregation cache | Initial render and filter targets met at 1,000–10,000 nodes |
| P1 | Hierarchy and permissions | Group inheritance exceptions, per-role field masking, break-glass approval | Permission boundary tests and matching audit events |
| P1 | Agent distribution | Package repository, signing and verification, automatic rollback | Install and upgrade automated on major amd64 and arm64 distributions |
| P2 | Storage scaling | PostgreSQL partitioning, retention jobs, long-term metric store | Audit and metric retention policies applied automatically |
| P2 | Security verification | SAST, dependency scan, container scan, fuzz and property tests | CI security gates and a documented vulnerability response |
| P2 | Project operations | CODEOWNERS, release process, issue templates, SBOM | A new contributor goes from local run to verified PR unaided |

Decisions blocking work right now are listed separately in
[open-decisions.md](open-decisions.md).

## Known limitations

- Operations and runbook executions target a single agent-managed node; multi-node
  fan-out and result aggregation are not implemented.
- Terminal I/O is relayed in memory and recorded per session; there is no shared
  recording store across servers.
- Alert rule evaluation runs in a single server process.
