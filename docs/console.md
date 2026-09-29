# Console guide

The console is a dependency-free ES-module single-page app in `apps/web`, served by
the server from the same origin. There is no build step: `index.html` loads
`src/app.js`, which renders every page and binds every handler.

Every view is a URL, so it can be reloaded, bookmarked, and opened in a second tab.
A deep link visited while signed out returns to that link after login.

| Path | View |
|---|---|
| `/` | Dashboard |
| `/{page}` | A named page, e.g. `/utilization`, `/jobs` |
| `/resources/{id}?tab=` | Resource detail; `tab` is `overview`, `sub`, `hardware`, or `runtime` |
| `/agents/{id}` | Agent inventory |
| `/incidents/{id}` | Incident detail |

## Pages

**Dashboard** — fleet verdict banner, KPIs, group health, and the attention list:
active alerts plus unhealthy resources that have no alert, so a degraded node is
never invisible.

**Alerts / Incidents** — the alert lifecycle (acknowledge, resolve, delete) and
incident response (timeline, notes, status, linked alerts). The KPI tiles on both
pages are status filters. Ticking the alerts one outage is showing through declares
them as a single incident, whose timeline carries what was done to its resources as
well as what was written into it.

**Infrastructure Map** — every node boxed by cluster, rack, or label and colored by
load, with per-node CPU, memory, disk, and network. Expand a node for its VMs and
containers. Filter by name or by load state.

**Resources** — the inventory: type, health, group, agent, and last update. Search
matches name, id, type, and tags on the server. A row opens the resource detail. The
*Terminated* chip shows VMs, containers, and processes that stopped being reported;
those records are kept for 30 days and are read-only.

**Resource detail** — a status band of four capacity meters (each with its
denominator and a saturation tick) plus health and alert count beside the name, then
four tabs:

- *Overview* — sparklines of the resource's own metric history over a live, 24-hour,
  7-day, or 30-day window, its active alerts, the tasks and shell sessions run
  against it, and its facts
- *Sub-resources* — descendants only, filtered by type and text. A manually
  registered resource is attached here so it appears under its host and inherits
  scope through the link; agent-discovered children are attached by their own
  inventory and cannot be detached
- *Hardware* — disks and network interfaces
- *Services* — systemd units, failed ones first, searchable and filterable by
  state. Containers, VMs, and processes are resources with their own pages, so
  they are not repeated here

**Utilization** — capacity-aware usage: headroom, idle and saturated nodes,
rightsizing candidates, and a linear-regression capacity forecast. The *Workloads*
tab covers what runs on the nodes — VMs, containers, and processes — with the usage
each type reports; a resource that reports none shows a dash rather than zero.

**Logs** — the *Live stream* tab shows what agents ship continuously: warnings and
worse, plus authentication activity at any severity, with severity counts across
every priority so ordinary volume is visible. The remaining tabs read a wider window
from a node on demand. Secret values are masked for anyone without the
`logs:read-raw` permission; usernames, source addresses, and commands are not.

**Agents** — enrolled agents, their inventory, and the per-agent hardware and
runtime detail. The page leads with what the fleet is running against the version it
was told to run, and names any agent whose turn in the rollout has passed without
taking the build.

**Remote shell** — approval-gated terminal sessions. A session is requested with a
reason, approved by another user (administrators may self-approve through the
`terminal:approve-self` grant), and every session is recorded. The recording dialog
replays the session as a terminal screen.

The session is a screen rather than a transcript, so full-screen programs draw
normally, and its panel follows the operator between pages instead of ending when the
shell page is left. A session ends when the console holding it goes away, and the row
records when.

**Automations** — runbooks and their executions. An execution shows each step's
outcome with the agent's raw payload behind a one-line summary.

**Task history** — every operation run through an agent, filterable by status from
the KPI tiles and by task, target, requester, or reason.

**Settings** — User Management (users, teams, roles, scopes, role bindings), Group
Management (node groups, dynamic groups, tags), Unmanaged resources, Alerting (rules
and delivery), and the Audit log.

## Behaviour

- **Auto-refresh** — every 10 seconds, in place. Skipped while a dialog is open, the
  tab is hidden, or a field has focus, and run once more as soon as a hidden tab
  comes back to the front.
- **Sessions** — a session that expires or is revoked returns the tab to the sign-in
  screen saying so, rather than counting the refused requests as an outage.
- **Destructive actions** — a delete confirms first and names the record it is about
  to remove, since it is offered from one row among many that look alike.
- **Pagination** — page size is derived from the viewport, so a table fills the
  window without a long scroll and re-fits on resize.
- **Permissions** — a control whose action the identity lacks is disabled rather than
  failing on submit. The mapping lives in `src/policy.js`.
- **Scope** — taken at sign-in from the paths `auth/me` reports for the identity, and
  sent on every request. A remembered choice is kept while it is still one of them.
- **LIVE / OFFLINE** — reachability, not permission: the badge turns only when nothing
  answers. A page this identity may not read shows its own empty state instead.
- **Silenced alerts** — a silence stops the paging, not the condition. Resource health
  leaves silenced alerts out, so a node under planned work stops colouring the map red;
  the dashboard verdict counts them instead of reading all-clear.
- **Language** — English and Korean, toggled in the topbar. Translation happens at
  the single `setHTML` choke point in `src/ui.js`, so every view, dialog, and toast
  is covered by the dictionary in `src/i18n.js`.

## Layout of the source

| File | Contents |
|---|---|
| `src/app.js` | Pages, the action dispatcher, `bind()`, routing, data loading |
| `src/components.js` | Shell, navigation, page head, KPI tiles, cards, icons |
| `src/state.js` | The single mutable state object |
| `src/policy.js` | Action-to-permission mapping |
| `src/api.js` | Fetch wrapper and cursor-following collection loader |
| `src/i18n.js` | Korean dictionary and dynamic patterns |
| `src/navigation.js` | Sidebar sections and their pages |
| `src/theme.js` | Light and dark selection, persisted per browser |
| `src/ui.js` | `setHTML`, escaping, label-to-field pairing, formatting helpers |
| `styles.css` | All styling, including the light theme |

## Tests

```bash
cd apps/web
npm test          # unit tests for components, policy, and helpers
```

End-to-end tests need a server at `http://127.0.0.1:8080`; override with
`KLOUDVIEW_WEB_URL`. They create and delete their own objects, and a global teardown
removes anything left behind by a run that failed midway.
