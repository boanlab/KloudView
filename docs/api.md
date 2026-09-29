# HTTP API

The API prefix is `/api/v1`. The console authenticates with a session cookie from
`POST /api/v1/auth/login`. `X-KloudView-Subject` and `X-KloudView-Scope` headers select
an identity and scope for local development and tests only; they are not a production
authentication mechanism.

A path under `/api/` that matches no route answers `404` with a JSON error body, never the console's HTML.

The machine-readable OpenAPI 3.1 document is `docs/openapi.json`. Use `make openapi` to regenerate it from the registered routes and `make openapi-check` to detect method/path gaps between the implementation and the specification.

APIs that mutate or act on target resources do not trust the request headers alone; they re-evaluate the scope using the actual static and dynamic group paths and the resource tags.

## Scope enforcement

`POST /api/v1/access/evaluate` returns the decision for a subject, resource, action, and
scope path. The console's access simulator calls it directly.

List APIs return only authorized targets, while mutation, deletion, and approval APIs re-verify the scope of the actual targets. VM, Container, and Process traverse parent relationships such as `hosts` in reverse to inherit the group path of their physical node or hypervisor. A mutation that spans multiple targets requires permission on every target. Alert, Incident, Operation, Runbook execution, and Terminal session apply the same rules based on their linked resources.

Scope and Role binding management APIs also evaluate the actual scope paths specified in the payload. A scope administrator can only view, create, modify, and delete scopes and bindings that are fully contained within all of their permitted paths. Composite scopes for which only some paths are permitted are not exposed.

| Area | Endpoint | Function |
|---|---|---|
| Health | `GET /healthz` | Health and build version |
| Session | `/api/v1/auth/login`, `/logout`, `/me` | Session login, logout, current identity and profile |
| Compatibility | `GET /api/v1/system/info` | Supported API and Agent protocol versions |
| Monitoring | `GET /api/v1/overview` | Group health, heatmap cells, latest resource aggregates |
| Resources | `/api/v1/resources` | Unmanaged resource CRUD, search, type/health filters, offset pagination |
| Hierarchy | `/api/v1/groups`, `/memberships`, `/relations` | Group and relationship management |
| Metrics | `/api/v1/agents/{id}/metrics`, `/metrics/summary`, `/metrics/timeseries`, `/resources/{id}/metrics` | Agent ingestion, latest aggregates, bucketed time series, per-resource history |
| Capacity | `GET /api/v1/utilization` | Headroom, idle and saturated nodes, rightsizing, and forecast |
| Alerting | `/api/v1/alerts`, `/alert-rules`, `/alert-silences`, `/alert-inhibitions`, `/incidents` | Alerts, silences, inhibitions, and Incident lifecycle |
| Notifications | `/api/v1/notification-channels`, `/notification-routes`, `/notification-deliveries` | Webhook channels, routing, and delivery results |
| Operations | `/api/v1/operations`, `/runbooks`, `/runbook-executions` | Agent operations and approved executions |
| Terminal | `/api/v1/terminal-sessions`, `/terminal-sessions/{id}/stream` | Request, independent approval, WebSocket PTY relay, termination |
| Agent | `/api/v1/agents`, `/inventories` | Enrollment, heartbeat, inventory, and fleet management |
| Access | `/api/v1/users`, `/teams`, `/roles`, `/scopes`, `/role-bindings`, `/access/evaluate` | Accounts, RBAC, hierarchical scopes, and decision lookup |
| Audit | `GET /api/v1/audit-events` | Mutation and approval audit records |

## Resource queries

`GET /api/v1/resources` query:

| Name | Default | Description |
|---|---:|---|
| `q` | none | Partial match on name, ID, type, or tag |
| `type` | none | `node`, `hypervisor`, `vm`, `container`, `process` |
| `health` | none | `healthy`, `warning`, `critical`, `unknown` |
| `limit` | 200 | Up to 1000 |
| `offset` | 0 | Starting position |

Pass the `nextCursor` from the previous response as `cursor`. When a cursor is present it takes precedence over offset and resumes after the composite name-and-ID sort key. Offset pagination remains available. The alert list also supports `q`, `status`, `severity`, `limit`, and `cursor`.

The `nextOffset` field is provided only when a next page exists. Agent-owned resources allow metadata edits only and cannot be deleted directly. Removing such a node and its auto-discovered child resources is done through the agent deletion API. Deleting an unmanaged resource also deletes its relationships, group memberships, and metrics.

`GET /api/v1/resources/{id}` lazily fetches targets already included in list pages and the heatmap. The server verifies the effective health reflecting the actual target scope and alerts before returning the details, so the Web Console does not need to keep the entire fleet list for the detail view.

## Monitoring aggregation

`GET /api/v1/overview?groupType=rack` computes group health and cell data on the server so the browser does not re-aggregate the full detail data. `groupType` accepts a group type such as `rack`, `service`, or `cluster`.

Overview results for the same identity, scope, and query are reused on the server for 2 seconds. The response's `X-KloudView-Cache` value is `hit` or `miss`. Cache entries are limited to 256.

The heatmap payload can be reduced server-side with `groupId`, `health`, `anomalies=true`, and `cellLimit`. The default cell limit is 1000 and the maximum is 5000; the response's `cellTotal`, `cellLimit`, and `cellsTruncated` indicate the displayed range. When limited, cells are returned in priority order: Critical, Warning, Unknown, Maintenance, then Healthy. KPIs and group aggregates are based on the full fleet regardless of cell filters and limits.

`attention` names up to 8 resources that are neither healthy nor in maintenance, in the same priority order, with `attentionTotal` carrying the full count. Each entry adds `reason` -- the reported state, `agent not reporting`, or `active alert` -- and `host`, so the list says why a resource needs looking at and where it runs. Like the KPIs, it is drawn from the whole authorized fleet and ignores the cell filters, so it stays consistent with the health counts even while the heatmap is narrowed to one resource type.

Group health includes direct members, the members of all descendant groups, and child resources linked through `hosts`, `runs`, and `contains` relations, without duplication. As a result, VM and Container failures propagate to the health of the parent environment, data center, and rack. `depends_on` is not an ownership hierarchy, so it does not propagate group membership or scope.

`GET /api/v1/metrics/timeseries?minutes=60&bucketSeconds=60` returns the average CPU, Memory, and Disk per bucket for the specified interval. The network summary provides the counter delta of the last two samples as bytes/sec.

`GET /api/v1/resources/{id}/metrics` returns the live in-memory series. Adding `from` and `to` (RFC3339, up to a 400-day window) reads the retained history instead, raw inside the raw-retention window and one-minute peaks beyond it; the response's `source` is `memory` or `history`.

`GET /api/v1/resources` hides terminated resources. `lifecycle=terminated` returns only records of VMs, containers, and processes that stopped being reported, `lifecycle=all` returns both, and `at=<RFC3339>` returns what existed at that moment.

In memory the server retains the last hour of samples at their original resolution and compresses everything older, up to 24 hours, into 5-minute representative values. It replaces samples with the same timestamp using the latest received value and orders out-of-order arrivals, preserving the network counter computation order while limiting memory and snapshot size as the node count grows.

Metric ingestion is allowed only for enrolled resources owned by the credential's Agent. CPU, Memory, and Disk are validated to the 0–100 range, and sample timestamps are accepted only from 24 hours in the past to 5 minutes in the future relative to the server, preventing arbitrary resources and abnormal values from polluting monitoring and alert state.

`POST /api/v1/agents/{id}/logs` accepts one reporting window from an agent: severity
counters for all eight syslog priorities, and the lines worth keeping — warning and
worse, plus authentication activity at any severity. `GET /api/v1/logs/lines` and
`GET /api/v1/logs/counters` read them back over `minutes`, optionally for one
`nodeId`. A read decides which nodes the caller may see first, then merges the
newest lines from each of those windows, so its cost follows the page size and
the node count rather than the volume retained. Lines are stored as the node produced them; secret values are masked on
read for every subject without the `logs:read-raw` permission, which only role-admin
holds. Usernames, source addresses, ports, ttys, and commands are never masked. The
window is held in memory for 24 hours and is not persisted.

The heartbeat response's `targetVersion` is per agent, not fleet-wide. With
`KLOUDVIEW_AGENT_CANARY` naming a node (by hostname, node ID or agent ID), that node
is offered a new build first and every other agent is offered the version it already
runs, so it leaves itself alone. The fleet is offered the new build only once the
canary has been reporting on it for `KLOUDVIEW_AGENT_CANARY_SOAK` (default 10m). A
canary that goes silent, never takes the build, or is not enrolled holds the fleet —
the failure mode is "nothing updates", never "the bad build lands everywhere".

Once cleared, agents take the build across a ten-minute window, each waiting out an
offset derived from its own id, rather than fetching it in the same second. The window
counts from when the build was published, so a canary already on the version before it
became the target does not hand the fleet a window that has expired.

`GET /api/v1/agents/rollout` reports whether the fleet is released and why, how many
agents are on the target, and names any whose turn has passed and are still not running
it. Released means the build was offered: an agent installs one only where self-update
is on, so the count is what says whether anything took it.

`GET /api/v1/agent-install.sh` and `GET /api/v1/agent-uninstall.sh` return shell scripts
that put an agent on a host and take it off again. Neither needs authentication: the
installer takes the enrollment token as an argument, so the URL carries no secret, and
the uninstaller touches only the host it runs on.

An agent's ID and node ID are derived from its hostname, so hostnames must be unique
across the fleet. Enrollment carries the host's own machine ID, and a second machine
claiming a name another holds is refused with `agent_hostname_taken`; the same machine
re-enrolling takes its record back.

`POST /api/v1/agents/{id}/container-metrics` accepts cgroup readings per container.
CPU and memory percentages become metric samples on the container's own resource, so
containers carry a trend and can be alerted on like a node; byte counters and process
count are stored as resource attributes rather than being passed off as percentages.

Alert rule metrics support `cpu`, `memory`, `disk`, `network_rx_rate`, and `network_tx_rate`. The network rate threshold unit is bytes/sec.

Rule duration is allowed from 0 up to 30 days, and an alert is created only after the condition holds continuously for that duration. CPU, Memory, and Disk thresholds must be 0–100, the Network threshold must be 0 or greater, and severity is `warning` or `critical`. When a rule is modified, disabled, or deleted, existing active alerts are resolved and the evaluation duration is reset. When a user resolves or deletes an alert, the tracking state is also reset so that later conditions are re-evaluated with a fresh duration.

`/api/v1/alert-silences` suppresses alerts based on a time window of up to 30 days, a hierarchical scope, and a tag selector. When an active window is created, existing matching alerts transition to `silenced`, and new matching alerts are not created within the window.

An Incident must include at least one resource or an existing alert. The resources of linked alerts are automatically merged into the Incident's `resourceIds`, and creation, modification, status changes, and timeline queries are validated against the actual scope of those targets. The supported states are `declared`, `investigating`, `mitigating`, `monitoring`, and `resolved`.

## Audit queries

`GET /api/v1/audit-events` supports `limit` and `offset`. The default limit is 200 and the maximum is 1000; the response includes `total`, `offset`, `limit`, and an optional `nextOffset`.

User mutation events record the approved request scope, and Agent events record the enrolled node ID. On query, each event is re-evaluated against the current user's `audit:read` permission and scope. Events carrying neither scope nor resource information can only be queried under the Global scope.

An actor is the signed-in subject, `agent` when an agent credential was accepted, and `anonymous` otherwise - a request that proved nothing is never attributed to the fleet.

Reads are not recorded, and neither is a work poll that found nothing: an agent claims terminal and operation work every few seconds, and recording the empty answers would push what people did out of the log.

## Agent authentication

`POST /api/v1/agents/enroll` validates the bootstrap token and returns a runtime credential bound to the Agent ID. Subsequent heartbeat, inventory, metrics, and operation claim/completion use this credential as a Bearer token. It cannot be used for a different Agent ID or for resources owned by another Agent.

The runtime credential is generated by signing the Agent ID with `KLOUDVIEW_AGENT_CREDENTIAL_KEY`, which exists only on the server. This key must be at least 32 characters and must differ from the bootstrap enrollment token. As a result, the bootstrap token deployed to an Agent alone cannot compute another Agent's runtime credential. If the signing key is rotated, existing Agents fail authentication and re-enroll through the bootstrap procedure.

The Agent ID, node ID, and hostname fixed at enrollment cannot be changed via heartbeat. Enrolling a different hostname that normalizes to the same result is also rejected as an identity collision. Heartbeat updates only the supported protocol, version, the `inventory`, `metrics`, and `terminal` capabilities, and a limited set of labels, merging Agent metadata while preserving the operator-assigned tags on the node.

Agents send `protocolVersion` in both enrollment and heartbeat. The currently supported version is `1`, and incompatible enrollments are rejected with `409 unsupported_agent_protocol`. An omitted value is treated as `1`.

When the last heartbeat exceeds 30 seconds, the Agent is computed as `offline` in API responses. The nodes, VMs, Containers, and Processes owned by that Agent are evaluated as `unknown` regardless of their stored prior state and propagate into the Overview group aggregates. The CPU, Memory, Disk, and Network heatmaps mark the last value as `stale` rather than displaying it as a normal value. However, if a warning or critical alert has already fired, the alert severity takes precedence over `unknown` to preserve the failure signal.

## Connectivity events

A sweep every 15 seconds records an alert when an agent stops reporting for longer
than 45 seconds, and resolves it when the agent returns, noting how long the gap was.

The alert carries the evidence that survives a host being cut off: the last metric
sample the node sent, and whether other nodes fell silent within 90 seconds. One node
alone points at that host; several at once point at shared power or network. This is
the only diagnosis available when a host loses power, since nothing can be fetched
from it afterwards.

## Agent credential rotation

Enrolment returns a random credential; the server stores only its SHA-256. A
heartbeat issues a replacement once the current one is older than 24 hours, and the
value it replaces stays valid until the agent authenticates with the new one, so an
agent that cannot persist the rotation is never locked out.

The clock advances only while the agent checks in. A host that was powered off for a
week authenticates with what it stored and rotates from there.

Agents enrolled before rotation existed present a credential derived from
`KLOUDVIEW_AGENT_CREDENTIAL_KEY`. That value is still accepted and the agent is moved
to a stored credential on its next heartbeat, so the server can be deployed before or
after the agents are updated.

## Terminal relay

Only an approved, active session can connect to the PTY. The browser obtains a single-use stream ticket valid for 30 seconds through an authenticated HTTP request and connects to a same-origin WebSocket. The server relays between the outbound WebSocket the Agent establishes first and the browser connection, keyed by the target node and session ID.

The Agent runs `/bin/sh` in a real PTY and handles input, output, and terminal resize. The session limit is 30 minutes and the output cap is 10 MiB. If the connection drops or the session ends, the PTY is terminated as well. Session request, approval, ticket issuance, and termination are recorded in the mutation audit. Session I/O is recorded per session up to 1 MiB and read back through `GET /api/v1/terminal-sessions/{id}/recording`, which the console replays; `DELETE` on the same path discards it. A production deployment additionally requires Agent mTLS and isolated execution.

`/terminal-sessions/{id}/commands` and the Agent command claim API serve clients that do not use the PTY stream. The Web Console uses the stream.

## Operation execution

Operations and Runbook executions currently target a single Agent-managed node. A claim is granted a 45-second lease and, if no response is received, is reassigned up to 3 times. A completion request can be submitted only by the Agent that owns the node. Batch execution across multiple nodes will be supported after adding per-execution fan-out and result aggregation.
