import { api, apiCollection } from "./api.js";
import { card, icon, kpi, pageHead, renderShell } from "./components.js";
import { nav } from "./navigation.js";
import { actionPermissions, hasPermission, permissionChecks } from "./policy.js";
import { getLang, setLang, t, translateLive } from "./i18n.js";
import { state } from "./state.js";
import { createTerminal, keyBytes, renderTerminal } from "./terminal.js";
import { applyTheme, getTheme, setTheme } from "./theme.js";
import {
  escapeHTML,
  alertPickNoun,
  chartMarks,
  failureCard,
  heatmapTier,
  incidentTitleFor,
  formatBytes,
  keyValues,
  localDateTime,
  setHTML,
  statusClass,
  terminalDockView,
} from "./ui.js";

const $ = (s) => document.querySelector(s);
let filterTimer;
let terminalSocket;
let terminalConnecting = false;
let terminalSessionId;
let terminalScreen = createTerminal(80, 24);
let terminalPaintPending = false;
let terminalScreenHadFocus = false;
// Whether a program currently owns the alternate screen. Tracked so the pane
// can be re-rendered the moment that changes rather than on the next refresh.
let terminalFullScreen = false;
// Whether this identity may type into the session at all. Keystrokes go
// straight to the pty; the deny policy gets its say on the server, which reads
// the line the shell echoes back when a Return arrives.
let terminalWritable = false;
// Draft command, preserved across re-renders: the input element is replaced
// each time and would otherwise lose what was half-typed in it.
let terminalDraft = "";
const terminalDecoder = new TextDecoder();
function can(resource, action) {
  return hasPermission(state.permissions, resource, action);
}
// Shimmer placeholders shown before the first data load completes.
function skeletonRows(rows, cols) {
  const cells = Array.from(
    { length: cols },
    (_, i) =>
      `<td class="${i >= 3 && i <= 6 ? "num" : ""}"><span class="sk sk-line"></span></td>`,
  ).join("");
  return Array.from(
    { length: rows },
    () => `<tr class="sk-row">${cells}</tr>`,
  ).join("");
}
function skeletonCells(n) {
  return `<div class="sk-cells">${'<span class="sk sk-cell"></span>'.repeat(n)}</div>`;
}

function overviewGroups() {
  let live = state.liveOverview?.groups || [];
  return live.length
    ? live.map((g) => ({
        id: g.id,
        name: g.name,
        count: g.total,
        ok: g.healthy,
        warn: g.warning,
        crit: g.critical,
        unknown: g.unknown,
        status: g.critical
          ? "Critical"
          : g.warning
            ? "Degraded"
            : g.unknown
              ? "Unknown"
              : "Healthy",
      }))
    : [];
}
function groupCards() {
  let cards = overviewGroups();
  if (!cards.length)
    return `<div class="card-body"><div class="empty">No groups configured</div></div>`;
  return `<div class="card-body"><div class="group-grid">${cards
    .map(
      (g) =>
        `<div class="group-card ${state.overviewGroup === g.id ? "selected" : ""}" data-overview-group="${g.id || "all"}"><div class="group-top"><div class="group-name">${g.name}</div><span class="status-pill ${statusClass(g.status)}">${g.status}</span></div><div class="group-count">${g.count}<span class="muted" style="font:11px Inter"> resources</span></div><div class="stack"><span class="s-ok" style="width:${g.count ? (g.ok / g.count) * 100 : 0}%"></span><span class="s-warn" style="width:${g.count ? (g.warn / g.count) * 100 : 0}%"></span><span class="s-crit" style="width:${g.count ? (g.crit / g.count) * 100 : 0}%"></span><span class="s-unknown" style="width:${g.count ? ((g.unknown || 0) / g.count) * 100 : 0}%"></span></div><div class="group-foot"><span><b class="ok">${g.ok}</b> healthy · <b class="warn">${g.warn}</b> warn · <b class="critical">${g.crit}</b> crit</span><span>${g.unknown || 0} unknown</span></div></div>`,
    )
    .join("")}</div></div>`;
}

function attention() {
  // Action list: active alerts plus unhealthy resources that have no alert.
  let alerts = state.apiOnline
    ? state.liveAlerts.filter(
        (a) => a.status !== "resolved" && a.status !== "silenced",
      )
    : [];
  let alertResourceIds = new Set(alerts.map((a) => a.resourceId).filter(Boolean));
  let sev = (s) => (s === "warning" || s === "Warning" ? "warn" : "critical");
  let items = alerts.map((a) => ({
    resource: a.resourceId || "",
    title: a.name,
    desc: `${a.resourceId || a.target || ""} · ${a.summary || a.status}`,
    sev: sev(a.severity || a.sev),
    time: a.updatedAt ? new Date(a.updatedAt).toLocaleTimeString() : a.time || "",
  }));
  // The server's attention list spans every authorized resource; cells carry
  // the heatmap's type filter.
  (state.liveOverview?.attention || [])
    .filter((c) => !alertResourceIds.has(c.id))
    .forEach((c) =>
      items.push({
        resource: c.id,
        // A process name alone repeats across rows; the state and host
        // distinguish them.
        title: `${c.name}${c.host ? ` on ${c.host}` : ""}`,
        desc: [c.reason || `${c.health} health`, c.type, c.id]
          .filter(Boolean)
          .join(" · "),
        sev: sev(c.health),
        time: "",
      }),
    );
  items = items.slice(0, 8);
  // Bounded like the other in-card lists.
  return `<div class="attention-list scroll-list">${
    items.length
      ? items
          .map(
            (it) =>
              `<div class="attention"${it.resource ? ` data-live-resource="${escapeHTML(it.resource)}"` : ""}><i class="sev ${it.sev === "warn" ? "warn" : ""}"></i><div><div class="att-title">${escapeHTML(it.title)}</div><div class="att-desc">${escapeHTML(it.desc)}</div></div><div class="att-time">${it.time}</div></div>`,
          )
          .join("")
      : '<div class="empty">All resources healthy · no active alerts</div>'
  }</div>`;
}

// The cells the heatmap is choosing between: everything the dashboard's
// filters allow, before the tier is picked. The tier buttons count these, so
// a tab reading 0 is telling the truth about this view rather than about the
// fleet -- "Containers 4" next to "Nodes 0" answers the question the empty
// square used to raise.
function heatmapCandidates() {
  let cells = state.liveOverview?.cells || [];
  if (state.overviewGroup !== "all")
    cells = cells.filter((cell) => cell.groupId === state.overviewGroup);
  if (state.anomaliesOnly)
    cells = cells.filter(
      (cell) => !["healthy", "maintenance"].includes(cell.health),
    );
  else if (state.overviewHealth !== "all")
    cells = cells.filter((cell) => cell.health === state.overviewHealth);
  return cells;
}

function heatmap(tier) {
  if (!(state.apiOnline && state.liveOverview))
    return `<div class="heatmap-wrap">${skeletonCells(48)}</div>`;
  // One tier at a time; per-process detail lives in the resource drill-down.
  const cells = heatmapCandidates().filter((cell) => cell.type === tier);
  let legend =
    state.metric === "health"
      ? '<span class="legend-item" style="--c:var(--green)">Healthy</span><span class="legend-item" style="--c:var(--amber)">Warning</span><span class="legend-item" style="--c:var(--red)">Critical</span><span class="legend-item" style="--c:#4e5a68">Unknown</span>'
      : state.metric === "network"
        ? '<span class="legend-item" style="--c:#28666a">Absolute throughput</span><span class="legend-item" style="--c:#4e5a68">Stale / idle</span>'
        : '<span class="legend-item" style="--c:#28666a">Below 75%</span><span class="legend-item" style="--c:var(--amber)">75–90%</span><span class="legend-item" style="--c:var(--red)">90%+</span><span class="legend-item" style="--c:#4e5a68">Stale</span>';
  let cellHTML = (cell) => {
    let value = Number(cell[state.metric] || 0),
      cls =
        state.metric !== "health" && cell.metricsStale
          ? "unknown"
          : state.metric === "health"
            ? cell.health === "warning"
              ? "warn"
              : cell.health
            : state.metric === "network"
              ? value > 0
                ? "busy"
                : "unknown"
              : value >= 90
                ? "critical"
                : value >= 75
                  ? "warn"
                  : value > 0
                    ? "busy"
                    : "unknown",
      displayValue =
        state.metric === "health"
          ? cell.health
          : cell.metricsStale
            ? "stale"
            : state.metric === "network"
              ? `${formatBytes(value)}/s`
              : `${value.toFixed(1)}%`,
      intensity =
        state.metric === "network" && value > 0 && !cell.metricsStale
          ? ` style="opacity:${(0.35 + Math.min(0.65, Math.log10(value + 1) * 0.08125)).toFixed(2)}"`
          : "";
    return `<span class="heat-cell ${cls}"${intensity} data-tip="${escapeHTML(cell.name)} · ${escapeHTML(displayValue)}" aria-label="${escapeHTML(cell.name)} · ${escapeHTML(displayValue)}" data-live-resource="${escapeHTML(cell.id)}"></span>`;
  };
  return `<div class="heatmap-wrap"><div class="heatmap-legend">${legend}<span style="margin-left:auto">${cells.length} shown</span></div>${
    cells.length
      ? `<div class="heat-cells">${cells.map(cellHTML).join("")}</div>`
      : '<div class="empty">Nothing of this kind matches the current filters</div>'
  }</div>`;
}

// The scale a plot is drawn against, as labels down its left edge.
//
// The gridlines sit at quarters of the plot area, so these are the values at
// those quarters. Without them a line is a shape with no magnitude: a chart
// reading 8% and one reading 80% look identical if neither says what the top
// of the box means.
function chartAxis(top, format) {
  return `<div class="chart-axis">${[1, 0.75, 0.5, 0.25, 0]
    .map((fraction) => `<span>${escapeHTML(format(top * fraction))}</span>`)
    .join("")}</div>`;
}

const percentTick = (value) => `${Math.round(value)}%`;
const rateTick = (value) => formatBytes(value).replace(" ", "");

function chart() {
  let data = state.liveMetricSeries;
  if (!data.length)
    return `<div class="card-body"><span class="sk sk-chart"></span></div>`;
  let path = (key) =>
      data
        .map((point, index) => {
          let x = data.length === 1 ? 400 : (index / (data.length - 1)) * 800;
          let y =
            118 - Math.max(0, Math.min(100, Number(point[key] || 0))) * 1.12;
          return `${index ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`;
        })
        .join(" "),
    labels = [data[0], data[Math.floor(data.length / 2)], data.at(-1)].map(
      (point) =>
        new Date(point.timestamp).toLocaleTimeString([], {
          hour: "2-digit",
          minute: "2-digit",
        }),
    ),
    latest = data.at(-1);
  return `<div class="card-body"><div class="metric-legend"><span><i style="background:var(--blue)"></i>CPU <b>${Number(latest.cpu).toFixed(1)}%</b></span><span><i style="background:var(--purple)"></i>Memory <b>${Number(latest.memory).toFixed(1)}%</b></span><span><i style="background:var(--amber)"></i>Disk <b>${Number(latest.disk).toFixed(1)}%</b></span><span class="muted" style="margin-left:auto">${latest.count} samples in bucket</span></div><div class="spark-area">${chartAxis(100, percentTick)}<div class="chart-grid"></div><svg viewBox="0 0 800 120" preserveAspectRatio="none"><path d="${path("cpu")}" fill="none" stroke="#5c9cf5" stroke-width="2"/><path d="${path("memory")}" fill="none" stroke="#9d85f5" stroke-width="2"/><path d="${path("disk")}" fill="none" stroke="#f2b84b" stroke-width="2"/></svg><div class="chart-labels">${labels.map((label) => `<span>${label}</span>`).join("")}</div></div></div>`;
}

// Throughput chart: bytes/s on its own axis, scaled to peak.
function networkChart() {
  let data = state.liveMetricSeries;
  if (!data.length)
    return `<div class="card-body"><span class="sk sk-chart"></span></div>`;
  let peak = Math.max(
    1,
    ...data.map((p) =>
      Math.max(Number(p.networkRxRate || 0), Number(p.networkTxRate || 0)),
    ),
  );
  let path = (key) =>
      data
        .map((point, index) => {
          let x = data.length === 1 ? 400 : (index / (data.length - 1)) * 800;
          let y = 118 - Math.min(112, (Number(point[key] || 0) / peak) * 112);
          return `${index ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`;
        })
        .join(" "),
    labels = [data[0], data[Math.floor(data.length / 2)], data.at(-1)].map(
      (point) =>
        new Date(point.timestamp).toLocaleTimeString([], {
          hour: "2-digit",
          minute: "2-digit",
        }),
    ),
    latest = data.at(-1);
  return `<div class="card-body"><div class="metric-legend"><span><i style="background:#5c9cf5"></i>RX <b>${formatBytes(Number(latest.networkRxRate || 0))}/s</b></span><span><i style="background:#f2b84b"></i>TX <b>${formatBytes(Number(latest.networkTxRate || 0))}/s</b></span><span class="muted" style="margin-left:auto">peak ${formatBytes(peak)}/s</span></div><div class="spark-area">${chartAxis(peak, rateTick)}<div class="chart-grid"></div><svg viewBox="0 0 800 120" preserveAspectRatio="none"><path d="${path("networkRxRate")}" fill="none" stroke="#5c9cf5" stroke-width="2"/><path d="${path("networkTxRate")}" fill="none" stroke="#f2b84b" stroke-width="2"/></svg><div class="chart-labels">${labels.map((label) => `<span>${label}</span>`).join("")}</div></div></div>`;
}

function liveResourceKpis() {
  let m = state.liveMetrics;
  if (!m || !m.count) return "";
  let fmt = (v) => Number(v || 0).toFixed(1) + "%";
  return `<div class="section-label">Live telemetry</div><div class="page-sub" style="margin:-9px 0 12px"><span class="ok"><i class="dot"></i>Live Agent data</span> · ${m.count} reporting resources · ${m.observedAt ? new Date(m.observedAt).toLocaleTimeString() : "waiting"}</div><div class="grid kpis">${/* These are the mean across reporting resources, which is a different number
      from the core-weighted figure in Capacity & usage further down the page.
      Both were labelled just "CPU", so the two disagreed with no explanation. */ ""}${kpi("LIVE CPU", fmt(m.cpuAvg), `Mean per resource · peak ${fmt(m.cpuMax)}`, m.cpuMax > 90 ? "critical" : "info")}${kpi("LIVE MEMORY", fmt(m.memoryAvg), `Mean per resource · peak ${fmt(m.memoryMax)}`, m.memoryMax > 90 ? "critical" : "")}${kpi("LIVE DISK", fmt(m.diskAvg), `Mean per resource · peak ${fmt(m.diskMax)}`, m.diskMax > 90 ? "critical" : "")}${kpi("NETWORK RX", formatBytes(m.networkRxRate) + "/s", "Current receive rate", "info")}${kpi("NETWORK TX", formatBytes(m.networkTxRate) + "/s", "Current transmit rate", "info")}</div>`;
}
// Verdict banner: current overall state.
function statusHero() {
  let o = state.liveOverview || {};
  let crit = o.critical || 0,
    warn = o.warning || 0,
    inc = o.activeIncidents || 0,
    unknown = o.unknown || 0,
    total = o.total || 0,
    healthy = o.healthy ?? Math.max(0, total - crit - warn - unknown),
    online = state.liveAgents.filter((a) => a.status === "online").length,
    agents = state.liveAgents.length,
    level = crit || inc ? "critical" : warn ? "warn" : "ok",
    glyph = level === "critical" ? "✕" : level === "warn" ? "!" : "✓",
    verdict =
      level === "critical"
        ? "Action required"
        : level === "warn"
          ? "Warnings to review"
          : "All systems operational",
    sub =
      level === "critical"
        ? `${crit} critical · ${inc} active incident${inc === 1 ? "" : "s"}`
        : level === "warn"
          ? // Any resource type can carry a warning -- today's are zombie
            // processes, not nodes, so the banner does not say "nodes".
            `${warn} warning${warn === 1 ? "" : "s"} across the fleet`
          : `${total} resources reporting`,
    // 100% is reserved for nothing unhealthy and 0% for nothing healthy; every
    // mixed state clamps to 1-99 rather than rounding into either.
    healthPct = !total
      ? 100
      : healthy === total
        ? 100
        : healthy === 0
          ? 0
          : Math.min(99, Math.max(1, Math.round((healthy / total) * 100))),
    // Banner target: incidents when open, else alerts.
    target = inc ? "incidents" : crit || warn ? "alerts" : null,
    stat = (value, label, cls = "") =>
      `<div class="hero-stat"><div class="hs-value ${cls}">${value}</div><div class="hs-label">${label}</div></div>`;
  return `<div class="status-hero ${level}${target ? " clickable" : ""}"${target ? ` data-page="${target}"` : ""}><div class="hero-verdict"><span class="hero-glyph">${glyph}</span><div><div class="hero-title">${verdict}${target ? ' <span class="hero-cta">→</span>' : ""}</div><div class="hero-sub">${sub}</div></div></div><div class="hero-stats">${stat(healthPct + "%", "Healthy")}${stat(warn, "Warning", warn ? "warn" : "")}${stat(crit, "Critical", crit ? "critical" : "")}${stat(`${online}/${agents}`, "Agents online")}${stat(inc, "Incidents", inc ? "critical" : "")}</div></div>`;
}

function capacitySummary() {
  const u = state.liveUtilization;
  if (!u || !u.fleet.coresTotal) return "";
  const f = u.fleet;
  const tone = (v) => (v >= 85 ? "critical" : v >= 60 ? "warn" : "ok");
  const bar = (pct, t) =>
    `<div class="cap-bar"><div class="cap-fill ${t}" style="width:${Math.min(100, pct).toFixed(1)}%"></div></div>`;
  const metricCell = (label, pct, sub) =>
    `<div class="cap-metric"><div class="cap-head"><span>${label}</span><b class="${tone(pct)}">${pct.toFixed(1)}%</b></div>${bar(pct, tone(pct))}<div class="muted mono">${sub}</div></div>`;
  return card(
    "Capacity & usage",
    // "of capacity" separates this from the per-resource mean in Live telemetry.
    `<div class="card-body cap-grid">${metricCell("CPU of capacity", f.cpu, `${f.coresUsed.toFixed(1)} / ${f.coresTotal} cores · ${(f.coresTotal - f.coresUsed).toFixed(0)} free`)}${metricCell("Memory of capacity", f.memory, `${formatBytes(f.memUsedBytes)} / ${formatBytes(f.memTotalBytes)}`)}<div class="cap-flags"><div><b class="${f.saturated ? "critical" : "ok"}">${f.saturated}</b><span class="muted">saturated</span></div><div><b class="${f.idle ? "warn" : ""}">${f.idle}</b><span class="muted">idle</span></div></div></div>`,
    `<button class="btn btn-sm" data-page="utilization">View utilization</button>`,
    "span-2",
  );
}

function overview() {
  let o = state.liveOverview,
    groupTypes = [
      ...new Set(
        (state.liveGroups.length
          ? state.liveGroups.map((group) => group.type)
          : ["rack", "service", "cluster"]
        ).filter(Boolean),
      ),
    ],
    seg = `<div class="seg">${groupTypes.map((type) => `<button data-group-type="${type}" class="${state.groupBy === type ? "active" : ""}">${type[0].toUpperCase() + type.slice(1)}</button>`).join("")}</div>`;
  // Tiers the payload actually carries, so the segment does not flicker as
  // the health chips narrow what is counted. A fleet with none of them at all
  // keeps the standard set rather than losing the control entirely.
  const allCells = state.liveOverview?.cells || [];
  const candidates = heatmapCandidates();
  const labels = [
    ["node", "Nodes"],
    ["hypervisor", "Hypervisors"],
    ["vm", "VMs"],
    ["container", "Containers"],
  ];
  const present = labels.filter(([type]) =>
    allCells.some((cell) => cell.type === type),
  );
  const heatTypes = (present.length ? present : labels).map(([type, label]) => ({
    type,
    label,
    count: candidates.filter((cell) => cell.type === type).length,
  }));
  const heatTier = heatmapTier(heatTypes, state.heatmapType, state.heatmapTypePinned);
  const typeSeg = `<div class="seg heat-type-seg">${heatTypes.map((tier) => `<button data-heatmap-type="${escapeHTML(tier.type)}" class="${heatTier === tier.type ? "active" : ""}">${tier.label} <span class="seg-count mono">${tier.count}</span></button>`).join("")}</div>`;
  let metric = `<div class="seg"><button data-metric="health" class="${state.metric === "health" ? "active" : ""}">Health</button><button data-metric="cpu" class="${state.metric === "cpu" ? "active" : ""}">CPU</button><button data-metric="memory" class="${state.metric === "memory" ? "active" : ""}">Memory</button><button data-metric="disk" class="${state.metric === "disk" ? "active" : ""}">Disk</button><button data-metric="network" class="${state.metric === "network" ? "active" : ""}">Network</button></div>`,
    fleetFilters = `<div class="filterbar"><button class="filter-chip ${state.overviewGroup === "all" ? "active" : ""}" data-overview-group="all">All groups</button>${["all", "healthy", "warning", "critical", "unknown"].map((health) => `<button class="filter-chip ${state.overviewHealth === health && !state.anomaliesOnly ? "active" : ""}" data-overview-health="${health}">${health === "all" ? "All states" : health}</button>`).join("")}<button class="filter-chip ${state.anomaliesOnly ? "active" : ""}" data-overview-anomalies="true">Anomalies first</button></div>`;
  return (
    pageHead(
      "Infrastructure overview",
      `${state.apiOnline ? "Real-time health, capacity, and alerts across your infrastructure" : "Server unavailable"}`,
      `<button class="btn" data-action="noc-mode">Fullscreen</button>`,
    ) +
    statusHero() +
    liveResourceKpis() +
    `${fleetFilters}<div class="grid dashboard-grid">${capacitySummary()}${/* Paired rather than stacked full-width: both are sparse on a small fleet,
      and two full-width rows of mostly empty card pushed the charts and the
      attention list below the fold. */ ""}${card("Group health", groupCards(), seg)}${card("Infrastructure heatmap", heatmap(heatTier), `${typeSeg}${metric}`)}${card("Resource utilization", chart(), `<select id="metric-window" class="time-select"><option value="60" ${state.metricMinutes === 60 ? "selected" : ""}>Last 1 hour</option><option value="360" ${state.metricMinutes === 360 ? "selected" : ""}>Last 6 hours</option><option value="1440" ${state.metricMinutes === 1440 ? "selected" : ""}>Last 24 hours</option></select>`)}${card("Network throughput", networkChart())}${card("Attention required", attention(), `<button class="btn btn-sm" data-page="alerts">View all</button>`, "span-2")}</div>`
  );
}

function liveResourceTable(data) {
  let metrics = new Map(
      (state.liveOverview?.cells || []).map((x) => [x.id, x]),
    ),
    groupNames = new Map(state.liveGroups.map((x) => [x.id, x.name]));
  return `<div class="table-wrap"><table class="table"><thead><tr><th>Resource</th><th>Type</th><th>Address</th><th>Health</th><th>Group</th><th>Agent</th><th>Updated</th><th>Actions</th></tr></thead><tbody>${data
    .map((r) => {
      let m = metrics.get(r.id) || {},
        membership = structuralGroup(r.id),
        health = m.health || r.health || "unknown";
      // A terminated resource is a record: its last health is history, not state.
      if (r.terminatedAt)
        return `<tr class="row-terminated" data-live-resource="${r.id}"><td><div class="resource"><span class="resource-icon">${String(r.type).slice(0, 2).toUpperCase()}</span><div>${escapeHTML(r.name)}<div class="muted mono" data-i18n-skip>${escapeHTML(resourceIdentity(r))}</div></div></div></td><td class="mono">${escapeHTML(t(TYPE_LABELS[r.type] || r.type))}</td><td class="mono">${escapeHTML(addressOf(r))}</td><td class="muted"><i class="dot"></i>Terminated</td><td>${escapeHTML(membership?.name || "Ungrouped")}</td><td class="mono">${r.agentId || "—"}</td><td class="mono muted">${formatWhen(r.terminatedAt)}</td><td><span class="muted">Read-only</span></td></tr>`;
      return `<tr data-live-resource="${r.id}"><td><div class="resource"><span class="resource-icon">${String(r.type).slice(0, 2).toUpperCase()}</span><div>${escapeHTML(r.name)}<div class="muted mono" data-i18n-skip>${escapeHTML(resourceIdentity(r))}</div></div></div></td><td class="mono">${escapeHTML(t(TYPE_LABELS[r.type] || r.type))}</td><td class="mono">${escapeHTML(addressOf(r))}</td><td class="${healthTone(health)}"><i class="dot"></i>${health}</td><td>${escapeHTML(membership?.name || "Ungrouped")}</td><td class="mono">${r.agentId || "—"}</td><td class="mono muted">${new Date(r.updatedAt).toLocaleTimeString()}</td><td><button class="btn btn-sm" data-action="edit-resource" data-resource-id="${r.id}">Edit</button> ${r.agentId ? "" : `<button class="btn btn-sm btn-danger" data-action="delete-resource" data-resource-id="${r.id}">Delete</button>`}</td></tr>`;
    })
    .join("") ||
    (!state.apiOnline
      ? skeletonRows(12, 8)
      : '<tr><td colspan="8"><div class="empty">No resources match the current filters</div></td></tr>')}</tbody></table></div>`;
}

function infrastructure() {
  // Server-side match covers name, id, type, and tags; no second filter here.
  let data = state.liveFilteredResources,
    pageStart = state.liveResourceTotal ? state.resourceOffset + 1 : 0,
    pageEnd = Math.min(
      state.resourceOffset + data.length,
      state.liveResourceTotal,
    );
  return (
    pageHead(
      "Resources",
      "Browse and manage every physical, virtual, and container resource",
      `<button class="btn" data-action="export">${icon("download")}Export</button>`,
    ) +
    `<div class="filterbar"><input id="resource-filter" placeholder="Filter resources…" value="${state.query}"><button class="filter-chip ${!state.resourceType ? "active" : ""}" data-resource-filter="all">All</button><button class="filter-chip ${state.resourceType === "node" ? "active" : ""}" data-resource-filter="node">Node</button><button class="filter-chip ${state.resourceType === "vm" ? "active" : ""}" data-resource-filter="vm">VM</button><button class="filter-chip ${state.resourceType === "container" ? "active" : ""}" data-resource-filter="container">Container</button><button class="filter-chip ${state.resourceType === "process" ? "active" : ""}" data-resource-filter="process">Process</button><span class="filter-divider"></span><button class="filter-chip ${state.resourceHealth === "critical" ? "active" : ""}" data-resource-filter="critical" title="Show only critical resources">Critical only</button><button class="filter-chip ${state.resourceLifecycle === "terminated" ? "active" : ""}" data-resource-filter="terminated" title="Show VMs, containers, and processes that stopped being reported">Terminated</button><span class="result-count">${pageStart}–${pageEnd} of ${state.liveResourceTotal} resources</span></div>${card("Resources", `${liveResourceTable(data)}<div class="pagination"><button class="btn btn-sm" data-resource-page="previous" ${state.resourceOffset === 0 ? "disabled" : ""}>Previous</button><span class="mono muted">Page ${Math.floor(state.resourceOffset / state.resourcePageSize) + 1} of ${Math.max(1, Math.ceil(state.liveResourceTotal / state.resourcePageSize))}</span><button class="btn btn-sm" data-resource-page="next" ${state.resourceOffset + state.resourcePageSize >= state.liveResourceTotal ? "disabled" : ""}>Next</button></div>`, "")}`
  );
}

// resourceBandMetric prefers the resource's own latest sample over the
// overview cell, which exists only when the dashboard's filters happen to
// include this resource's type.
// The meters on a detail page, taken from this resource's own last sample.
//
// The overview cell cannot stand in for a missing one. The server builds each
// cell by looking the resource up in a map of latest metrics, and a lookup
// that misses yields a zero sample — so a resource nobody has measured arrives
// carrying 0% for everything, which reads as "idle" when it means "unknown".
// This page has already asked for the resource's own samples; if there are
// none, that is the answer.
function resourceBandMetric(resource, cell) {
  const samples = (state.resourceMetrics || []).filter(
    (x) => !x.resourceId || x.resourceId === resource.id,
  );
  const latest = samples[samples.length - 1];
  if (!latest) return { health: cell?.health || resource.health };
  return {
    ...(cell || {}),
    cpu: latest.cpu,
    memory: latest.memory,
    disk: latest.disk,
    health: cell?.health || resource.health,
  };
}

// hostOfResource finds the node a guest runs on, following the relation the
// inventory writes when it discovers the guest.
function hostOfResource(resource) {
  if (!["vm", "container", "process"].includes(resource.type)) return "";
  const link = (state.liveRelations || []).find(
    (r) => r.targetId === resource.id && ["hosts", "runs"].includes(r.type),
  );
  return link?.sourceId || "";
}

function liveResourceDetailPage() {
  let resource =
    state.selectedResource ||
    state.liveResources.find((x) => x.id === state.selectedResourceId);
  if (!resource) {
    state.selectedResourceId = null;
    state.selectedResource = null;
    return infrastructure();
  }
  let cells = new Map((state.liveOverview?.cells || []).map((x) => [x.id, x])),
    // The overview payload carries the dashboard's own filters, and its heatmap
    // defaults to nodes — so a VM or container asking for its cell got nothing
    // and showed zeros on its own page. This resource's last sample is loaded
    // for the trend anyway; it is the honest source for the meters.
    metric = resourceBandMetric(resource, cells.get(resource.id)),
    resourcesById = new Map(state.liveResources.map((x) => [x.id, x]));
  // Sub-resources: descendants only (node > vm > container > process).
  const typeRank = { node: 0, hypervisor: 0, vm: 1, container: 2, process: 3 };
  const selfRank = typeRank[resource.type] ?? 0;
  const belowTypes = ["vm", "container", "process"].filter(
    (t) => (typeRank[t] ?? 9) > selfRank,
  );
  const peers = state.liveRelations
    .filter((x) => x.sourceId === resource.id || x.targetId === resource.id)
    .map((x) => {
      const peerId = x.sourceId === resource.id ? x.targetId : x.sourceId;
      return { peerId, peer: resourcesById.get(peerId), rel: x.type, relationId: x.id };
    })
    .filter((e) => e.peer && (typeRank[e.peer.type] ?? 9) > selfRank);
  const q = (state.subQuery || "").toLowerCase();
  const typeF = state.subFilter || "";
  const filteredPeers = peers.filter(
    (e) =>
      (!typeF || e.peer.type === typeF) &&
      (!q ||
        `${e.peer.name} ${e.peerId} ${e.peer.type}`.toLowerCase().includes(q)),
  );
  const subPage = pagedList(filteredPeers, "subresources");
  const subRows = filteredPeers.length
    ? subPage.slice
        .map((e) => {
          const m = cells.get(e.peerId) || {},
            health = m.health || e.peer.health || "unknown",
            pct = (key) =>
              m[key] != null ? Number(m[key]).toFixed(1) + "%" : "—";
          // Agent-discovered links are recreated by the next inventory, so
          // only a manually attached resource offers a detach.
          const detachable = !e.peer.agentId && can("relations", "delete");
          return `<tr data-live-resource="${e.peerId}"><td><div class="resource"><span class="resource-icon">${e.peer.type.slice(0, 2).toUpperCase()}</span><div>${escapeHTML(e.peer.name || e.peerId)}<div class="muted mono">${e.peerId}</div></div></div></td><td class="mono">${e.peer.type}</td><td class="mono">${escapeHTML(e.rel)}</td><td class="${healthTone(health)}"><i class="dot"></i>${health}</td><td class="mono num">${pct("cpu")}</td><td class="mono num">${pct("memory")}</td><td>${detachable ? `<button class="btn btn-sm btn-danger" data-action="detach-resource" data-relation-id="${escapeHTML(e.relationId)}" data-peer-name="${escapeHTML(e.peer.name || e.peerId)}">Detach</button>` : '<span class="muted">Agent managed</span>'}</td></tr>`;
        })
        .join("")
    : `<tr><td colspan="7"><div class="empty">${peers.length ? "No matching sub-resources" : "No sub-resources"}</div></td></tr>`;
  const subChip = (k, label) =>
    `<button class="filter-chip ${typeF === k ? "active" : ""}" data-sub-filter="${k}">${label} <span class="count-badge">${peers.filter((e) => e.peer.type === k).length}</span></button>`;
  const subFilterbar = belowTypes.length
    ? `<div class="filterbar"><input id="sub-filter" placeholder="Filter sub-resources…" value="${escapeHTML(state.subQuery || "")}"><button class="filter-chip ${!typeF ? "active" : ""}" data-sub-filter="all">All <span class="count-badge">${peers.length}</span></button>${belowTypes.map((t) => subChip(t, t.toUpperCase())).join("")}<span class="result-count">${filteredPeers.length} of ${peers.length}</span></div>`
    : "";
  const report = state.liveInventories.find(
    (x) => x.agentId === resource.agentId,
  );
  const inventory = report?.data || {};
  const hasSpecs = !!report && ["node", "hypervisor"].includes(resource.type);

  // Root-cause context: this resource's alerts, tasks, and shell sessions.
  const alerts = state.liveAlerts.filter(
    (a) => a.resourceId === resource.id && a.status !== "resolved",
  );
  // A guest runs no agent of its own, so everything done about it is done on
  // the host that runs it: the shell someone opened to look at this container
  // is a session on the node. Matching only this resource's own id left the
  // page empty at exactly the moment someone was working on it.
  const hostID = hostOfResource(resource),
    onHost = (id) => hostID && id === hostID,
    hostName = hostID
      ? state.liveResources.find((x) => x.id === hostID)?.name || hostID
      : "",
    via = (isHost) => (isHost ? ` · on ${hostName}` : ""),
    concerns = (ids) => ids.some((id) => id === resource.id || onHost(id));
  const activity = [
    ...state.liveOperations
      .filter((o) => concerns(o.targetIds || []))
      .map((o) => ({
        when: o.updatedAt,
        kind: "OP",
        title: o.type + via(!(o.targetIds || []).includes(resource.id)),
        status: statusText(o.status),
        who: o.requestedBy,
        tone: statusTone(o.status),
      })),
    ...state.liveTerminals
      .filter((t) => t.targetId === resource.id || onHost(t.targetId))
      .map((t) => ({
        when: t.closedAt || t.startedAt || t.createdAt,
        kind: "TS",
        title: "Terminal session" + via(t.targetId !== resource.id),
        status: statusText(t.status),
        who: t.requestedBy,
        tone: statusTone(t.status),
      })),
  ]
    .sort((a, b) => Date.parse(b.when || 0) - Date.parse(a.when || 0))
    .slice(0, 8);

  const attributes = Object.entries(resource.attributes || {});
  const groupName = structuralGroup(resource.id)?.name || "Ungrouped";

  const trendSection = resourceTrend(state.resourceMetrics, resource);
  const overviewTab =
    `${trendSection}<div style="height:12px"></div>` +
    `<div class="detail-layout"><div>${card(
      "Active alerts",
      `<div class="card-body">${
        alerts.length
          ? alerts
              .map(
                (a) =>
                  `<div class="relation-node"><span class="resource-icon">AL</span><div>${escapeHTML(a.name)}<div class="muted">${escapeHTML(a.summary || "")} · ${formatWhen(a.updatedAt)}</div></div><span class="status-pill ${a.severity === "critical" ? "critical" : "warn"}" style="margin-left:auto">${escapeHTML(a.severity)}</span></div>`,
              )
              .join("")
          : '<div class="empty">No active alert on this resource</div>'
      }</div>`,
      alerts.length ? `<span class="count-badge critical">${alerts.length}</span>` : "",
    )}<div style="height:12px"></div>${card(
      "Recent activity",
      `<div class="card-body scroll-list">${
        activity.length
          ? activity
              .map(
                (e) =>
                  `<div class="relation-node"><span class="resource-icon">${e.kind}</span><div>${escapeHTML(e.title)}<div class="muted"><span>${escapeHTML(e.status)}</span> · <span>${escapeHTML(e.who)}</span></div></div><span class="mono muted" style="margin-left:auto">${formatWhen(e.when)}</span></div>`,
              )
              .join("")
          : '<div class="empty">No task or session recorded for this resource</div>'
      }</div>`,
    )}</div><div>${card(
      "Resource facts",
      `<div class="spec-grid"><div class="spec"><label>Type</label><span class="mono">${escapeHTML(resource.type)}</span></div><div class="spec"><label>Group</label><span>${escapeHTML(groupName)}</span></div><div class="spec"><label>Agent</label>${resource.agentId ? `<button class="link mono" data-live-agent-id="${escapeHTML(resource.agentId)}">${escapeHTML(resource.agentId)}</button>` : '<span class="mono">—</span>'}</div><div class="spec"><label>Last update</label><span class="mono">${formatWhen(resource.updatedAt)}</span></div>${attributes.map(([k, v]) => `<div class="spec"><label>${escapeHTML(labelFromKey(k))}</label><span class="mono">${escapeHTML(String(v))}</span></div>`).join("")}</div>`,
    )}</div></div>`;

  const subTab = card(
    "Sub-resources",
    `${subFilterbar}<div class="table-wrap"><table class="table"><thead><tr><th>Resource</th><th>Type</th><th>Relation</th><th>Health</th><th class="num">CPU</th><th class="num">Memory</th><th>Actions</th></tr></thead><tbody>${subRows}</tbody></table></div>${subPage.bar}`,
    can("relations", "create")
      ? `<button class="btn btn-sm" data-action="attach-resource" data-host-id="${resource.id}">Attach resource</button>`
      : "",
  );

  // One tab per question; counts on the tab labels.
  const tabs = [
    ["overview", "Overview", ""],
    ["sub", "Sub-resources", peers.length],
    ...(hasSpecs
      ? [
          ["hardware", "Hardware", specCount(inventory, "hardware")],
          ["runtime", "Services", specCount(inventory, "runtime")],
        ]
      : []),
  ];
  let tab = state.detailTab;
  if (!tabs.some(([key]) => key === tab)) tab = "overview";
  const tabBar = `<div class="tabbar">${tabs
    .map(
      ([key, label, count]) =>
        `<button class="tab ${key === tab ? "active" : ""}" data-detail-tab="${key}">${label}${count === "" || count === undefined ? "" : ` <span class="count-badge">${count}</span>`}</button>`,
    )
    .join("")}</div>`;

  const body =
    tab === "sub"
      ? subTab
      : tab === "hardware"
        ? machineSpecs(inventory, "hardware")
        : tab === "runtime"
          ? machineSpecs(inventory, "runtime")
          : overviewTab;

  // A missing reading is not a reading of zero. A process nobody sampled and
  // a process sitting idle are different facts, and showing both as 0.0% said
  // the machine had been asked when it had not.
  const pct = (value) => (value == null ? "—" : Number(value).toFixed(1) + "%");
  const band = (value) =>
    Number(value || 0) >= 85 ? "critical" : Number(value || 0) >= 60 ? "warn" : "";
  const health = metric.health || resource.health || "unknown";
  // Meter sub-line: the denominator the percentage is a share of.
  const cap = (state.liveUtilization?.nodes || []).find(
    (x) => x.id === resource.id,
  );
  // Network counters are cumulative; the current rate is the delta over the
  // elapsed time between the last two samples.
  const rates = (() => {
    const samples = state.resourceMetrics || [];
    const [previous, latest] = samples.slice(-2);
    if (!previous || !latest) return null;
    const seconds =
      (Date.parse(latest.timestamp) - Date.parse(previous.timestamp)) / 1000;
    if (!(seconds > 0)) return null;
    const delta = (key) =>
      Math.max(0, Number(latest[key] || 0) - Number(previous[key] || 0)) /
      seconds;
    return { rx: delta("networkRx"), tx: delta("networkTx") };
  })();
  // A guest's disk is not a slice of the host's disks, and the inventory here
  // is the node's. Showing the host total under a VM's meter read as though a
  // 256 MiB guest had a terabyte.
  // A process is not a guest: it was given no cores and no memory allowance,
  // so its percentages are shares of the machine, the way a container's are.
  const isProcess = resource.type === "process";
  // Nor is a container, for CPU. The agent divides a container's usage by the
  // host's core count, while a VM's is divided by the vCPUs it was given, so
  // the same real load reads eight times smaller on a container here. The
  // meter cannot make them comparable, but it can stop claiming they are
  // measured the same way.
  const isContainer = resource.type === "container";
  const isGuest = ["vm", "container", "process"].includes(resource.type),
    diskTotal = isGuest
      ? 0
      : (inventory.disks || []).reduce(
          (total, x) => total + Number(x.sizeBytes || 0),
          0,
        );
  // What the guest was given, which is what its percentages are a share of.
  const guestCores = Number(resource.attributes?.vcpus || 0),
    guestMemory = Number(resource.attributes?.memoryBytes || 0),
    guestMemoryUsed = Number(resource.attributes?.memoryUsedBytes || 0);
  const ofTotal = (used, total, unit) =>
    total > 0
      ? unit === "cores"
        ? `${Number(used).toFixed(1)} / ${Number(total).toFixed(0)} cores`
        : `${formatBytes(used)} / ${formatBytes(total)}`
      : "";
  // Health and alert count sit beside the name, ahead of the meters.
  const headBadges = `<span class="status-pill ${healthTone(health)}">${escapeHTML(health)}</span>${alerts.length ? `<span class="status-pill critical">${alerts.length} active alerts</span>` : ""}`;
  return `<div class="breadcrumb"><button class="link" data-page="infrastructure">Infrastructure</button> / <span>${escapeHTML(resource.name)}</span></div>${pageHead(
    `${escapeHTML(resource.name)} ${headBadges}`,
    `${resource.type} · ${escapeHTML(groupName)} · ${resource.id}`,
    `${resource.agentId && ["node", "hypervisor"].includes(resource.type) ? '<button class="btn btn-primary" data-action="connect-terminal">Open terminal</button>' : ""}`,
  )}<div class="grid kpis">${kpi("CPU", pct(metric.cpu), isProcess || isContainer ? "Share of all cores" : isGuest ? (guestCores ? `Share of ${guestCores} vCPU` : "Share of its own cores") : ofTotal(cap?.coresUsed, cap?.cores, "cores") || "Share of all cores", band(metric.cpu), "", metric.cpu)}${kpi("MEMORY", pct(metric.memory), isProcess ? ofTotal(guestMemoryUsed, hostMemoryOf(resource)) || "Share of installed memory" : isGuest ? ofTotal(guestMemoryUsed, guestMemory) || (isContainer ? "Share of installed memory" : "Share of assigned memory") : ofTotal(cap?.memUsedBytes, cap?.memoryBytes) || "Share of installed memory", band(metric.memory), "", metric.memory)}${isGuest ? "" : kpi("DISK", pct(metric.disk), ofTotal((diskTotal * Number(metric.disk || 0)) / 100, diskTotal) || "Share of disk capacity", band(metric.disk), "", metric.disk)}${isProcess ? kpi("THREADS", resource.attributes?.threads || "—", "Running now") : kpi("NETWORK", rates ? `<span class="kpi-split"><span>↓ ${formatBytes(rates.rx)}/s</span><span>↑ ${formatBytes(rates.tx)}/s</span></span>` : formatBytes(metric.network || 0) + "/s", rates ? "Receive / transmit" : "Receive and transmit")}</div>${tabBar}${body}`;
}

// One incident can be the reason for several alerts.
//
// A node running out of memory fires memory, then swap, then a service that
// could not allocate — three alerts, one outage. Declaring them separately
// splits the response across three timelines, and declaring one and ignoring
// the rest leaves the others firing with nobody looking at them.
//
// The selection is held by alert id rather than by row, so it survives the
// filter chips, paging, and the ten-second refresh that rebuilds the table.
function alertPickBar(hasAlerts) {
  const picked = pickedAlerts();
  if (!picked.length) {
    if (!hasAlerts) return "";
    // Nothing is picked, and the row buttons are gone. Without this line the
    // page offers no way to declare anything and does not say where one is.
    return `<div class="filterbar attach-bar quiet"><span class="muted">Tick the alerts an outage is showing through, then declare them as one incident.</span></div>`;
  }
  const resources = new Set(picked.map((alert) => alert.resourceId).filter(Boolean));
  return `<div class="filterbar attach-bar"><span><b class="mono">${picked.length}</b> <span>${alertPickNoun(picked.length)}</span>${resources.size > 1 ? ` · <b class="mono">${resources.size}</b> <span>resources</span>` : ""}</span><button class="btn btn-primary" data-action="declare-from-alerts">Declare one incident</button><button class="btn" data-action="clear-alert-picks">Clear</button></div>`;
}

// The picked alerts, in the order the server lists them, skipping any that
// have gone away since they were picked.
function pickedAlerts() {
  return state.liveAlerts.filter((alert) => state.alertsPicked.includes(alert.id));
}

function alertsPage() {
  const all = state.liveAlerts;
  const filter = state.alertFilter || "all";
  const match = {
    firing: (a) => a.status === "firing",
    acknowledged: (a) => a.status === "acknowledged",
    critical: (a) => a.severity === "critical" && a.status !== "resolved",
    resolved: (a) => a.status === "resolved",
  };
  const data = filter === "all" ? all : all.filter(match[filter] || (() => true));
  const count = (key) => all.filter(match[key]).length;
  const tile = (key, label, value, sub, tone) =>
    `<button class="kpi-filter ${filter === key ? "active" : ""}" data-alert-filter="${key}">${kpi(label, value, sub, tone)}</button>`;
  const ruleNames = new Map(state.liveAlertRules.map((r) => [r.id, r.name]));
  const { slice, bar } = pagedList(data, "alerts");
  return (
    pageHead(
      "Alerts",
      "Monitor, acknowledge, and resolve infrastructure anomalies",
      `<button class="btn" data-action="silence">Silence rules</button><button class="btn btn-primary" data-action="new-rule">+ New alert rule</button>`,
    ) +
    `<div class="grid kpis">${tile("firing", "FIRING", String(count("firing")), "Active conditions", count("firing") ? "critical" : "calm")}${tile("acknowledged", "ACKNOWLEDGED", String(count("acknowledged")), "Under investigation", count("acknowledged") ? "warn" : "calm")}${tile("critical", "CRITICAL", String(count("critical")), "Immediate attention", count("critical") ? "critical" : "calm")}${tile("resolved", "RESOLVED", String(count("resolved")), "Historical alerts", "ok")}</div>${alertPickBar(all.length > 0)}<div class="card"><div class="table-wrap"><table class="table"><thead><tr><th class="pick-col"></th><th>Alert</th><th>Severity</th><th>Status</th><th>Condition</th><th>Updated</th><th>Actions</th></tr></thead><tbody>${data.length ? slice.map((a) => `<tr class="${state.alertsPicked.includes(a.id) ? "picked" : ""}"><td class="pick-col"><input type="checkbox" data-alert-pick="${escapeHTML(a.id)}" ${state.alertsPicked.includes(a.id) ? "checked" : ""}></td><td><div class="resource" data-live-resource="${escapeHTML(a.resourceId)}" title="Inspect resource"><span class="resource-icon">AL</span><div>${a.name}<div class="muted mono">${a.resourceId}</div></div></div></td><td class="${a.status === "resolved" ? "muted" : a.severity === "warning" ? "warn" : "critical"}">${a.severity}</td><td><span class="status-pill ${a.status === "firing" ? "critical" : a.status === "resolved" ? "ok" : "warn"}">${a.status}</span></td><td class="prose"><div class="clamp" title="${escapeHTML(summaryLabel(a.summary) || "Metric rule condition")}">${escapeHTML(summaryLabel(a.summary) || "Metric rule condition")}</div>${a.ruleId ? `<div class="muted">${escapeHTML(ruleNames.get(a.ruleId) || a.ruleId)}</div>` : ""}</td><td class="mono muted">${formatWhen(a.updatedAt)}</td><td>${a.status === "firing" ? `<button class="btn btn-sm" data-action="ack-alert" data-alert-id="${a.id}">Acknowledge</button> ` : ""}${a.status !== "resolved" ? `<button class="btn btn-sm" data-action="resolve-alert" data-alert-id="${a.id}">Resolve</button> ` : ""}<button class="btn btn-sm btn-danger" data-action="delete-alert" data-alert-id="${a.id}">Delete</button></td></tr>`).join("") : `<tr><td colspan="7"><div class="empty">${all.length ? "No alert matches this filter" : "No server alerts"}</div></td></tr>`}</tbody></table></div>${bar}</div>`
  );
}

function utilizationPage() {
  const u = state.liveUtilization;
  const head = pageHead(
    "Utilization",
    "Capacity-aware usage, headroom, and idle or saturated nodes",
  );
  if (!u)
    return (
      head +
      `<div class="card"><div class="${state.apiOnline ? "empty" : "sk sk-chart"}">${state.apiOnline ? "No capacity data yet — install agents to report hardware inventory" : ""}</div></div>`
    );
  const f = u.fleet;
  const filter = state.utilFilter || "all";
  const q = (state.utilQuery || "").toLowerCase();
  const filtered = u.nodes.filter(
    (n) =>
      (filter === "all" || n.state === filter) &&
      (!q || `${n.name} ${n.id}`.toLowerCase().includes(q)),
  );
  const chip = (key, label) =>
    `<button class="filter-chip ${filter === key ? "active" : ""}" data-util-filter="${key}">${label}</button>`;
  const filterbar = `<div class="filterbar"><input id="util-filter" placeholder="Filter nodes…" value="${escapeHTML(state.utilQuery || "")}">${chip("all", `All nodes ${u.nodes.length}`)}${chip("saturated", `Saturated ${f.saturated}`)}${chip("idle", `Idle ${f.idle}`)}</div>`;
  const tone = (v) => (v >= 85 ? "critical" : v >= 60 ? "warn" : "ok");
  // Same vocabulary as the map legend, so a state reads identically in both.
  const badge = (s) =>
    `<span class="status-pill ${s === "saturated" ? "critical" : s === "idle" ? "warn" : "ok"}">${LOAD_LABELS[s] || s}</span>`;
  const kpis = `<div class="grid kpis">${kpi("TOTAL CPU", `${f.cpu.toFixed(1)}%`, `${f.coresUsed.toFixed(1)} / ${f.coresTotal} cores`, tone(f.cpu))}${kpi("TOTAL MEMORY", `${f.memory.toFixed(1)}%`, `${formatBytes(f.memUsedBytes)} / ${formatBytes(f.memTotalBytes)}`, tone(f.memory))}${kpi("CPU HEADROOM", `${(f.coresTotal - f.coresUsed).toFixed(0)}`, "cores available")}${kpi("IDLE", String(f.idle), "under 10% used", f.idle ? "warn" : "")}${kpi("SATURATED", String(f.saturated), "over 85% used", f.saturated ? "critical" : "ok")}</div>`;
  const nodePage = pagedList(filtered, "utilNodes");
  const nodeRows = filtered.length
    ? nodePage.slice
        .map(
          (n) =>
            `<tr data-live-resource="${n.id}"><td><div class="resource"><span class="resource-icon">${String(n.type).slice(0, 2).toUpperCase()}</span><div>${escapeHTML(n.name)}<div class="muted mono">${n.cores ? `${n.cores} cores · ${formatBytes(n.memoryBytes)}` : "no inventory"}</div></div></div></td><td>${badge(n.state)}</td><td class="mono num">${n.cpu.toFixed(1)}%</td><td class="mono num">${n.memory.toFixed(1)}%</td><td class="mono num">${n.disk.toFixed(1)}%</td><td class="mono num">${n.cores ? `${n.coresUsed.toFixed(1)} / ${n.cores}` : "—"}</td><td class="mono num">${formatBytes(n.network)}/s</td></tr>`,
        )
        .join("")
    : '<tr><td colspan="7"><div class="empty">No nodes</div></td></tr>';
  const groupRows = u.groups.length
    ? u.groups
        .map(
          (g) =>
            `<tr><td><div class="resource"><span class="resource-icon">GR</span>${escapeHTML(g.name)}</div></td><td class="mono">${escapeHTML(g.type)}</td><td class="mono num">${g.nodes}</td><td class="mono num">${g.cpu.toFixed(1)}%</td><td class="mono num">${g.memory.toFixed(1)}%</td><td class="mono num">${g.disk.toFixed(1)}%</td><td class="mono num">${g.idle}</td><td class="mono num">${g.saturated}</td></tr>`,
        )
        .join("")
    : '<tr><td colspan="8"><div class="empty">No groups</div></td></tr>';
  const recRows = (list, extra) =>
    list.length
      ? `<div class="table-wrap"><table class="table"><thead><tr><th>Node</th><th class="num">Avg CPU</th><th class="num">Avg Mem</th>${extra.head}</tr></thead><tbody>${list
          .map(
            (n) =>
              `<tr data-live-resource="${n.id}"><td><div class="resource"><span class="resource-icon">${String(n.type).slice(0, 2).toUpperCase()}</span>${escapeHTML(n.name)}</div></td><td class="mono num">${n.avgCpu.toFixed(1)}%</td><td class="mono num">${n.avgMemory.toFixed(1)}%</td>${extra.cell(n)}</tr>`,
          )
          .join("")}</tbody></table></div>`
      : `<div class="empty">${extra.empty}</div>`;
  const reclaimNodes = u.nodes.filter((n) => n.recommendation === "reclaim");
  const scaleNodes = u.nodes.filter((n) => n.recommendation === "scale");
  const rightsizing =
    '<div class="section-label">Rightsizing</div>' +
    card(
      "Reclaim candidates",
      `${recRows(reclaimNodes, { head: '<th class="num">Reclaimable</th>', cell: (n) => `<td class="mono num">${n.cores} cores · ${formatBytes(n.memoryBytes)}</td>`, empty: "No sustained-idle nodes" })}`,
      `<span class="muted">${f.reclaim} nodes · ${f.reclaimCores.toFixed(0)} cores · ${formatBytes(f.reclaimMemBytes)}</span>`,
    ) +
    '<div style="height:12px"></div>' +
    card(
      "Scale candidates",
      `${recRows(scaleNodes, { head: '<th class="num">Cores</th>', cell: (n) => `<td class="mono num">${n.cores || "—"}</td>`, empty: "No sustained-saturated nodes" })}`,
      `<span class="muted">${f.scale} nodes</span>`,
    );
  const series = u.series || [];
  const forecastThresholdPct = (u.forecast || [])[0]?.threshold || 85;
  const clampPct = (v) => Math.max(0, Math.min(100, Number(v) || 0));
  const horizon = (fx) => {
    if (fx.daysToFull < 0)
      return { text: "No exhaustion projected", tone: "ok" };
    if (fx.daysToFull === 0) return { text: "At capacity now", tone: "critical" };
    const d = fx.daysToFull;
    const t =
      d < 1
        ? "< 1 day"
        : d < 90
          ? `~${Math.round(d)} days`
          : d < 730
            ? `~${Math.round(d / 30)} months`
            : "> 2 years";
    return { text: `Reaches ${fx.threshold}% in ${t}`, tone: d < 14 ? "critical" : d < 60 ? "warn" : "ok" };
  };
  const fcell = (label, metric) => {
    const fx = (u.forecast || []).find((x) => x.metric === metric);
    if (!fx) return "";
    const h = horizon(fx);
    const dir = fx.slopePerDay > 0.05 ? "up" : fx.slopePerDay < -0.05 ? "down" : "";
    const arrow = dir === "up" ? "▲" : dir === "down" ? "▼" : "▬";
    const sign = fx.slopePerDay >= 0 ? "+" : "";
    return `<div class="forecast-cell"><div class="forecast-head"><span class="muted">${label}</span><span class="status-pill ${h.tone}">${h.text}</span></div><div class="forecast-metrics"><span>Now <b>${fx.current.toFixed(1)}%</b></span><span>Trend <b class="trend-${dir || "flat"}">${arrow} ${sign}${fx.slopePerDay.toFixed(2)}%/day</b></span><span>In 30 days <b>${fx.projected30d.toFixed(1)}%</b></span><span class="muted">confidence ${fx.confidence}</span></div></div>`;
  };
  const netPeak = Math.max(1, ...series.map((p) => Number(p.network || 0)));
  const headroomY = (118 - forecastThresholdPct * 1.12).toFixed(1);
  const miniTrend = (key, label, color, isPct) => {
    const path = series
      .map((p, i) => {
        const x = series.length === 1 ? 400 : (i / (series.length - 1)) * 800;
        const v = Number(p[key] || 0);
        const y = isPct
          ? 118 - clampPct(v) * 1.12
          : 118 - Math.min(112, (v / netPeak) * 112);
        return `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(" ");
    const latest = series.at(-1) || {};
    const cur = isPct
      ? `${Number(latest[key] || 0).toFixed(1)}%`
      : `${formatBytes(Number(latest[key] || 0))}/s`;
    const headroom = isPct
      ? `<line x1="0" x2="800" y1="${headroomY}" y2="${headroomY}" stroke="var(--red)" stroke-width="1" stroke-dasharray="4 4" opacity="0.5"/>`
      : "";
    return `<div class="mini-trend"><div class="mini-trend-head"><span><i style="background:${color}"></i>${label}</span><b class="mono">${cur}</b></div><div class="spark-area"><div class="chart-grid"></div><svg viewBox="0 0 800 120" preserveAspectRatio="none">${headroom}<path d="${path}" fill="none" stroke="${color}" stroke-width="2"/></svg></div></div>`;
  };
  const trendChart =
    series.length >= 2
      ? `<div class="card-body"><div class="mini-trend-grid">${miniTrend("cpu", "CPU", "#629cf6", true)}${miniTrend("memory", "Memory", "#9d85f5", true)}${miniTrend("disk", "Disk", "#f2b84b", true)}${miniTrend("network", "Network", "#45d49b", false)}</div><div class="muted mono" style="margin-top:8px">last ${series.length}h · headroom line ${forecastThresholdPct}% (CPU / Memory / Disk)</div></div>`
      : `<div class="card-body"><div class="empty">Not enough history yet for a trend</div></div>`;
  const forecast = (u.forecast || []).length
    ? '<div class="section-label">Trends & Forecast</div>' +
      card(
        "Capacity forecast",
        `<div class="card-body"><div class="forecast-grid">${fcell("Total CPU", "cpu")}${fcell("Total memory", "memory")}</div></div>`,
        `<span class="muted">Linear projection from recent trend</span>`,
      ) +
      '<div style="height:12px"></div>' +
      card("Overall utilization trend", trendChart) +
      '<div style="height:12px"></div>'
    : "";
  // One tab per question, so a long node list never pushes the trends and the
  // rightsizing candidates off the page.
  const workloads = workloadUsage();
  const tabs = [
    ["nodes", "Nodes", u.nodes.length],
    ["workloads", "Workloads", workloads.length],
    ["trends", "Trends & forecast", ""],
    ["rightsizing", "Rightsizing", reclaimNodes.length + scaleNodes.length],
    ["groups", "Group rollup", u.groups.length],
  ];
  let tab = state.utilTab;
  if (!tabs.some(([key]) => key === tab)) tab = "nodes";
  const tabBar = `<div class="tabbar">${tabs
    .map(
      ([key, label, count]) =>
        `<button class="tab ${key === tab ? "active" : ""}" data-util-tab="${key}">${label}${count === "" ? "" : ` <span class="count-badge">${count}</span>`}</button>`,
    )
    .join("")}</div>`;

  const nodesTab =
    filterbar +
    card(
      "Nodes by usage",
      `<div class="table-wrap"><table class="table"><thead><tr><th>Node</th><th>State</th><th class="num">CPU</th><th class="num">Memory</th><th class="num">Disk</th><th class="num">Cores used</th><th class="num">Network</th></tr></thead><tbody>${nodeRows}</tbody></table></div>${nodePage.bar}`,
      `<span class="muted">${filtered.length} of ${u.nodes.length} nodes</span>`,
    );

  const workloadsTab = workloadsSection(workloads, tone, badge);

  const groupsTab = card(
    "Group rollup",
    `<div class="table-wrap"><table class="table"><thead><tr><th>Group</th><th>Type</th><th class="num">Nodes</th><th class="num">CPU</th><th class="num">Memory</th><th class="num">Disk</th><th class="num">Idle</th><th class="num">Saturated</th></tr></thead><tbody>${groupRows}</tbody></table></div>`,
  );

  return (
    head +
    kpis +
    tabBar +
    (tab === "workloads"
      ? workloadsTab
      : tab === "trends"
        ? forecast
        : tab === "rightsizing"
          ? rightsizing
          : tab === "groups"
            ? groupsTab
            : nodesTab)
  );
}

// What runs on the nodes, with the usage each type actually reports. The
// overview already carries a sample per resource, so this needs no extra call.
function workloadUsage() {
  const metrics = new Map(
    (state.liveOverview?.cells || []).map((cell) => [cell.id, cell]),
  );
  const nodeName = new Map(state.liveResources.map((r) => [r.id, r.name]));
  const parent = new Map(
    state.liveRelations
      .filter((relation) => ["hosts", "runs"].includes(relation.type))
      .map((relation) => [relation.targetId, relation.sourceId]),
  );
  return state.liveResources
    .filter((resource) => ["vm", "container", "process"].includes(resource.type))
    .map((resource) => {
      const cell = metrics.get(resource.id) || {};
      const attributes = resource.attributes || {};
      return {
        id: resource.id,
        name: resource.name,
        type: resource.type,
        health: cell.health || resource.health || "unknown",
        node: nodeName.get(parent.get(resource.id)) || "—",
        cpu: Number(cell.cpu || 0),
        memory: Number(cell.memory || 0),
        memoryBytes: Number(attributes.memoryBytes || 0),
        memoryLimitBytes: Number(attributes.memoryLimitBytes || 0),
        processes: Number(attributes.processes || 0),
        // A resource with no sample is reported as unmeasured rather than as
        // idle, which is a different statement.
        measured: metrics.has(resource.id) && (cell.cpu > 0 || cell.memory > 0),
      };
    })
    .sort(workloadOrder(state.workloadSort));
}

// Which question the ranking answers. It was hard-sorted by CPU, so during a
// memory alert a leak holding forty percent of the machine and burning no CPU
// sorted below every busy process -- the one ranked view in the product,
// ranked by the wrong number for half of all incidents.
function workloadOrder(by) {
  if (by === "memory") {
    return (a, b) => b.memory - a.memory || b.memoryBytes - a.memoryBytes || b.cpu - a.cpu;
  }
  return (a, b) => b.cpu - a.cpu || b.memory - a.memory;
}

function workloadsSection(workloads, tone, badge) {
  const type = state.workloadType || "all";
  const query = (state.workloadQuery || "").toLowerCase();
  const filtered = workloads.filter(
    (item) =>
      (type === "all" || item.type === type) &&
      (!query || `${item.name} ${item.id} ${item.node}`.toLowerCase().includes(query)),
  );
  const count = (key) =>
    key === "all"
      ? workloads.length
      : workloads.filter((item) => item.type === key).length;
  const chip = (key, label) =>
    `<button class="filter-chip ${type === key ? "active" : ""}" data-workload-type="${key}">${label} ${count(key)}</button>`;
  const filterbar = `<div class="filterbar"><input id="workload-filter" placeholder="Filter workloads…" value="${escapeHTML(state.workloadQuery || "")}">${chip("all", "All")}${chip("container", "Container")}${chip("vm", "VM")}${chip("process", "Process")}</div>`;

  const page = pagedList(filtered, "utilWorkloads");
  const rows = filtered.length
    ? page.slice
        .map(
          (item) =>
            `<tr data-live-resource="${item.id}"><td><div class="resource"><span class="resource-icon">${String(item.type).slice(0, 2).toUpperCase()}</span><div>${escapeHTML(item.name)}<div class="muted mono">${escapeHTML(item.node)}</div></div></div></td><td class="mono">${item.type}</td><td class="${healthTone(item.health)}"><i class="dot"></i>${item.health}</td>${
              item.measured
                ? `<td class="mono num ${tone(item.cpu)}">${item.cpu.toFixed(1)}%</td><td class="mono num ${tone(item.memory)}">${item.memory.toFixed(1)}%</td>`
                : `<td class="mono num muted" title="This resource type does not report usage yet">—</td><td class="mono num muted">—</td>`
            }<td class="mono num">${item.memoryBytes ? formatBytes(item.memoryBytes) : "—"}</td><td class="mono num">${item.memoryLimitBytes ? formatBytes(item.memoryLimitBytes) : "No limit"}</td><td class="mono num">${item.processes || "—"}</td></tr>`,
        )
        .join("")
    : '<tr><td colspan="8"><div class="empty">No workload matches the current filters</div></td></tr>';

  const unmeasured = workloads.filter((item) => !item.measured).length;
  // A count on its own reads as a failure. Most of these are processes
  // outside the sampled set, which is a deliberate bound rather than
  // something broken, and saying so is the difference between "the tool is
  // not working" and "this is what it measures".
  const note = unmeasured
    ? `<div class="page-sub" style="margin:-4px 0 10px">${unmeasured} of these are not sampled: only the heaviest processes by CPU and by memory are measured each tick. Containers and VMs are always measured.</div>`
    : "";
  return (
    filterbar +
    note +
    card(
      "Workloads by usage",
      `<div class="table-wrap"><table class="table"><thead><tr><th>Workload</th><th>Type</th><th>Health</th><th class="num">CPU</th><th class="num">Memory</th><th class="num">Memory used</th><th class="num">Limit</th><th class="num">Processes</th></tr></thead><tbody>${rows}</tbody></table></div>${page.bar}`,
      `<span class="chip-row">${[["cpu", "By CPU"], ["memory", "By memory"]]
        .map(
          ([key, label]) =>
            `<button class="filter-chip ${(state.workloadSort || "cpu") === key ? "active" : ""}" data-workload-sort="${key}">${label}</button>`,
        )
        .join("")}</span><span class="muted">${filtered.length} of ${workloads.length}${unmeasured ? ` · ${unmeasured} not sampled` : ""}</span>`,
    )
  );
}

function unmanagedResourcesPage() {
  const data = state.liveResources.filter((r) => !r.agentId);
  return (
    pageHead(
      "Unmanaged resources",
      "Manually catalogued resources without an installed agent",
      `<button class="btn btn-primary" data-action="create-resource">+ Add resource</button>`,
    ) + card("Unmanaged resources", liveResourceTable(data))
  );
}

// Infrastructure map: nodes boxed by group, colored by load, expandable to
// their VMs and containers.
function infraMapPage() {
  const head = pageHead(
    "Infrastructure Map",
    "Whole-infrastructure view grouped by cluster, rack, or label — colored by load",
  );
  const resources = new Map(state.liveResources.map((r) => [r.id, r]));
  const nodes = state.liveResources.filter((r) =>
    ["node", "hypervisor"].includes(r.type),
  );
  if (!nodes.length)
    return (
      head +
      `<div class="card"><div class="${state.apiOnline ? "empty" : "sk sk-chart"}">${state.apiOnline ? "No nodes to map yet — install agents to populate the map" : ""}</div></div>`
    );
  const groupsById = new Map(state.liveGroups.map((g) => [g.id, g]));
  const utilByNode = new Map(
    (state.liveUtilization?.nodes || []).map((n) => [n.id, n]),
  );
  const cells = new Map(
    (state.liveOverview?.cells || []).map((c) => [c.id, c]),
  );
  const outgoing = new Map();
  for (const rel of state.liveRelations) {
    if (!outgoing.has(rel.sourceId)) outgoing.set(rel.sourceId, []);
    outgoing.get(rel.sourceId).push(rel.targetId);
  }
  const membershipsByResource = new Map();
  for (const m of state.liveMemberships) {
    if (!membershipsByResource.has(m.resourceId))
      membershipsByResource.set(m.resourceId, []);
    membershipsByResource.get(m.resourceId).push(m.groupId);
  }

  // Grouping dimension: the distinct group types that actually contain nodes.
  const nodeIds = new Set(nodes.map((n) => n.id));
  const dimTypes = [
    ...new Set(
      state.liveMemberships
        .filter((m) => nodeIds.has(m.resourceId))
        .map((m) => groupsById.get(m.groupId)?.type)
        .filter(Boolean),
    ),
  ];
  const preferred = ["cluster", "rack", "label", "service", "zone"];
  const orderedDims = [
    ...preferred.filter((d) => dimTypes.includes(d)),
    ...dimTypes.filter((d) => !preferred.includes(d)),
  ];
  let dim = state.mapGroupBy;
  if (!orderedDims.includes(dim)) dim = orderedDims[0] || "";

  const tileState = (n) => {
    const u = utilByNode.get(n.id);
    if (u) {
      if (u.state === "saturated") return "saturated";
      if (u.state === "idle") return "idle";
      if (u.cpu >= 60 || u.memory >= 60) return "busy";
      return "normal";
    }
    const health = cells.get(n.id)?.health || n.health;
    return health === "critical"
      ? "saturated"
      : health === "warning"
        ? "busy"
        : "normal";
  };
  const childTone = (k) => {
    const health = cells.get(k.id)?.health || k.health;
    return health === "critical"
      ? "critical"
      : health === "warning"
        ? "warn"
        : "ok";
  };
  const nodeTile = (n) => {
    const u = utilByNode.get(n.id) || {};
    const c = cells.get(n.id) || {};
    const cpu = Number(u.cpu ?? c.cpu ?? 0);
    const mem = Number(u.memory ?? c.memory ?? 0);
    const disk = Number(u.disk ?? c.disk ?? 0);
    const net = Number(u.network ?? c.network ?? 0);
    const rx = Number(u.networkRx ?? c.networkRx ?? 0);
    const tx = Number(u.networkTx ?? c.networkTx ?? 0);
    const kids = (outgoing.get(n.id) || [])
      .map((id) => resources.get(id))
      .filter((k) => k && ["vm", "container"].includes(k.type));
    const vmN = kids.filter((k) => k.type === "vm").length;
    const ctrN = kids.filter((k) => k.type === "container").length;
    const open = state.mapExpanded.has(n.id);
    const childChips =
      open && kids.length
        ? `<div class="map-children">${kids
            .map(
              (k) =>
                `<span class="map-chip ${childTone(k)}" data-live-resource="${k.id}" title="${escapeHTML(k.name)}"><i class="dot"></i>${k.type === "vm" ? "VM" : "CT"} ${escapeHTML(k.name)}</span>`,
            )
            .join("")}</div>`
        : "";
    const summary = [vmN ? `${vmN} VM` : "", ctrN ? `${ctrN} CT` : ""]
      .filter(Boolean)
      .join(" · ");
    return `<div class="map-node ${tileState(n)}"><div class="map-node-head"><span class="map-node-name" data-live-resource="${n.id}">${escapeHTML(n.name)}</span>${kids.length ? `<button class="map-expand" data-map-expand="${n.id}" aria-label="Toggle children">${open ? "−" : "+"}</button>` : ""}</div><div class="map-node-meta"><div class="map-meta-row three"><span class="mono">CPU ${cpu.toFixed(0)}%</span><span class="mono">MEM ${mem.toFixed(0)}%</span><span class="mono">DSK ${disk.toFixed(0)}%</span></div><div class="map-meta-row two"><span class="mono">NET↓ ${formatBytes(rx)}/s</span><span class="mono">NET↑ ${formatBytes(tx)}/s</span></div></div>${summary ? `<div class="map-node-kids muted">${summary}</div>` : ""}${childChips}</div>`;
  };

  // Name/group text filter combined with a load-state filter.
  const groupNamesFor = (n) =>
    (membershipsByResource.get(n.id) || [])
      .map((id) => groupsById.get(id)?.name || "")
      .join(" ");
  const query = (state.mapQuery || "").trim().toLowerCase();
  const load = state.mapLoad || "all";
  const visible = nodes.filter((n) => {
    if (load !== "all" && tileState(n) !== load) return false;
    if (!query) return true;
    return `${n.name} ${n.id} ${groupNamesFor(n)}`.toLowerCase().includes(query);
  });
  const loadCount = (key) => nodes.filter((n) => tileState(n) === key).length;

  // Bucket nodes into their group of the selected dimension.
  const buckets = new Map();
  const ungrouped = [];
  for (const n of visible) {
    const gid = (membershipsByResource.get(n.id) || []).find(
      (id) => groupsById.get(id)?.type === dim,
    );
    if (gid) {
      if (!buckets.has(gid)) buckets.set(gid, []);
      buckets.get(gid).push(n);
    } else {
      ungrouped.push(n);
    }
  }
  const groupBox = (name, type, list) => {
    const agg = list.reduce(
      (a, n) => {
        const u = utilByNode.get(n.id) || cells.get(n.id) || {};
        a.cpu += Number(u.cpu || 0);
        a.mem += Number(u.memory || 0);
        a.disk += Number(u.disk || 0);
        a.rx += Number(u.networkRx || 0);
        a.tx += Number(u.networkTx || 0);
        return a;
      },
      { cpu: 0, mem: 0, disk: 0, rx: 0, tx: 0 },
    );
    const n = list.length || 1;
    const stat = `${list.length} nodes · CPU ${(agg.cpu / n).toFixed(0)}% · MEM ${(agg.mem / n).toFixed(0)}% · DSK ${(agg.disk / n).toFixed(0)}% · NET↓ ${formatBytes(agg.rx)}/s · NET↑ ${formatBytes(agg.tx)}/s`;
    return `<div class="map-group"><div class="map-group-head"><div class="map-group-title"><span class="map-group-name">${escapeHTML(name)}</span>${type ? `<span class="map-type">${escapeHTML(type)}</span>` : ""}</div><div class="map-group-stat muted">${stat}</div></div><div class="map-nodes">${list.map(nodeTile).join("")}</div></div>`;
  };
  const boxes = [...buckets.entries()]
    .sort((a, b) => b[1].length - a[1].length)
    .map(([gid, list]) => {
      const g = groupsById.get(gid);
      return groupBox(g?.name || gid, g?.type, list);
    });
  if (ungrouped.length)
    boxes.push(groupBox("Ungrouped", "", ungrouped));

  const loadChip = (key, label) =>
    `<button class="filter-chip ${load === key ? "active" : ""}" data-map-load="${key}">${label}</button>`;
  const selector = `<div class="filterbar"><input id="map-filter" placeholder="Filter nodes and groups…" value="${escapeHTML(state.mapQuery || "")}">${loadChip("all", `All nodes ${nodes.length}`)}${loadChip("saturated", `Saturated ${loadCount("saturated")}`)}${loadChip("busy", `Busy ${loadCount("busy")}`)}${loadChip("idle", `Idle ${loadCount("idle")}`)}${
    orderedDims.length
      ? `<span class="filterbar-spacer"></span>${orderedDims
          .map(
            (d) =>
              `<button class="filter-chip ${d === dim ? "active" : ""}" data-map-group-by="${d}">By ${d}</button>`,
          )
          .join("")}`
      : ""
  }</div>`;
  const totals = {
    vm: state.liveResources.filter((r) => r.type === "vm").length,
    container: state.liveResources.filter((r) => r.type === "container").length,
  };
  const filtered = visible.length !== nodes.length;
  const counts = filtered
    ? `${visible.length} of ${nodes.length} nodes`
    : `${nodes.length} nodes · ${totals.vm} VMs · ${totals.container} containers`;
  const legend = `<div class="map-legend"><span class="mono muted">${counts}</span><span class="map-key"><i class="map-swatch idle"></i>Idle</span><span class="map-key"><i class="map-swatch normal"></i>Normal</span><span class="map-key"><i class="map-swatch busy"></i>Busy</span><span class="map-key"><i class="map-swatch saturated"></i>Saturated</span></div>`;

  return (
    head +
    legend +
    selector +
    (boxes.length
      ? `<div class="map-grid">${boxes.join("")}</div>`
      : `<div class="card"><div class="empty">No node matches this filter</div></div>`)
  );
}

function groupsPage(mode = "all") {
  let data = state.liveGroups.filter(
      (group) => mode === "all" || group.mode === mode,
    ),
    dynamic = mode === "dynamic";
  return (
    pageHead(
      dynamic ? "Dynamic groups" : "Node groups",
      dynamic
        ? "Selector-managed membership based on resource tags"
        : "Manage physical and logical resource groups",
      `<button class="btn" data-action="export">${icon("download")}Export</button>${dynamic ? '<button class="btn" data-action="auto-group">+ Auto-group by label</button>' : ""}<button class="btn btn-primary" data-action="create-group">+ Create group</button>`,
    ) +
    `<div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Name</th><th>Type</th><th>Mode</th><th>Path</th><th>Members</th><th>Actions</th></tr></thead><tbody>${
      data.length
        ? data
            .map((g) => {
              let count = state.liveMemberships.filter(
                (m) => m.groupId === g.id,
              ).length;
              return `<tr><td><div class="resource"><span class="resource-icon">GR</span>${g.name}</div>${
                g.mode === "dynamic"
                  ? `<div class="muted mono">${Object.entries(g.selector || {})
                      .map(([key, value]) => `${key}=${value}`)
                      .join(", ")}</div>`
                  : ""
              }</td><td>${g.type}</td><td><span class="status-pill ${g.mode === "dynamic" ? "warn" : ""}">${g.mode || "static"}</span></td><td class="mono muted">${g.path || "—"}</td><td class="mono">${count}</td><td><button class="btn btn-sm" data-action="manage-members" data-group-id="${g.id}">Members</button> <button class="btn btn-sm" data-action="edit-group" data-group-id="${g.id}">Edit</button> <button class="btn btn-sm btn-danger" data-action="delete-group" data-group-id="${g.id}">Delete</button></td></tr>`;
            })
            .join("")
        : `<tr><td colspan="6"><div class="empty">No groups configured</div></td></tr>`
    }</tbody></table></div></div>`
  );
}

function tagsPage() {
  const index = new Map();
  for (const resource of state.liveResources) {
    for (const [key, value] of Object.entries(resource.tags || {})) {
      const id = `${key}=${value}`;
      if (!index.has(id)) index.set(id, { key, value, resources: [] });
      index.get(id).resources.push(resource);
    }
  }
  const tags = [...index.values()].sort(
    (left, right) =>
      right.resources.length - left.resources.length ||
      `${left.key}=${left.value}`.localeCompare(`${right.key}=${right.value}`),
  );
  return (
    pageHead(
      "Resource tags",
      "Tag index used by dynamic groups, alert selectors, and access scopes",
      `<button class="btn btn-primary" data-action="create-resource">+ Add tagged resource</button>`,
    ) +
    `<div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Key</th><th>Value</th><th>Resources</th><th>Types</th><th>Action</th></tr></thead><tbody>${tags.length ? tags.map((tag) => `<tr><td class="mono">${tag.key}</td><td><span class="scope-badge">${tag.value}</span></td><td>${tag.resources.length}</td><td>${[...new Set(tag.resources.map((resource) => resource.type))].join(", ")}</td><td><button class="btn btn-sm" data-tag-query="${tag.key}=${tag.value}">View resources</button></td></tr>`).join("") : '<tr><td colspan="5"><div class="empty">No resource tags</div></td></tr>'}</tbody></table></div></div>`
  );
}

// What the fleet is running against what it was told to run. Released only
// says the build was offered; an agent installs one where the host turned
// self-update on, so the two numbers can sit apart for hours with nothing
// else saying so.
function rolloutBanner() {
  const rollout = state.liveRollout;
  if (!rollout || !rollout.target) return "";
  const stalled = rollout.stalled || [];
  const tone = stalled.length ? "warn" : rollout.onTarget === rollout.total ? "ok" : "";
  const headline = stalled.length
    ? `${stalled.length} of ${rollout.total} agents have not taken ${escapeHTML(rollout.target)}`
    : rollout.onTarget === rollout.total
      ? `Every agent is on ${escapeHTML(rollout.target)}`
      : `${rollout.onTarget} of ${rollout.total} agents are on ${escapeHTML(rollout.target)}`;
  const names = stalled.length
    ? `<div class="hero-sub mono">${stalled.map(escapeHTML).join(", ")}</div>`
    : "";
  return `<div class="card rollout-banner ${tone}"><div class="rollout-head"><strong>${headline}</strong><span class="mono muted">${escapeHTML(rollout.reason || "")}</span></div>${names}</div>`;
}

function fleetPage() {
  let data = state.liveAgents;
  const invByAgent = new Map(
    state.liveInventories.map((r) => [r.agentId, r.data || {}]),
  );
  const count = (type) =>
    state.liveResources.filter((r) => r.type === type).length;
  return (
    pageHead(
      "Agents",
      "Connection health, versions, capabilities, and reported hardware",
      `<span class="${state.apiOnline ? "ok" : "critical"}"><i class="dot"></i>API ${state.apiOnline ? "connected" : "offline"}</span><button class="btn btn-primary" data-action="install-agent">Install agent</button>`,
    ) +
    rolloutBanner() +
    `<div class="grid kpis">${kpi("REGISTERED", data.length || "—", "Connected to Server API")}${kpi("ONLINE", data.filter((a) => a.status === "online").length || "—", "Heartbeat within threshold", "ok")}${kpi("VMs", String(count("vm")), "Discovered guests")}${kpi("CONTAINERS", String(count("container")), "Discovered runtimes")}${kpi("PROCESSES", String(count("process")), "Observed node processes")}</div><div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Agent</th><th>Status</th><th>Version</th><th>Operating system</th><th class="num">Cores</th><th class="num">Memory</th><th>Capabilities</th><th>Last seen</th><th class="col-actions">Action</th></tr></thead><tbody>${
      data.length
        ? data
            .map((a) => {
              const inv = invByAgent.get(a.id) || {};
              return `<tr data-live-agent="${a.id}"><td><div class="resource"><span class="resource-icon">AG</span>${a.hostname}</div></td><td class="${a.status === "online" ? "ok" : "unknown"}"><i class="dot"></i>${a.status}</td><td class="mono">${a.version}</td><td>${a.labels?.os || inv.os || "—"}</td><td class="mono num">${inv.cpuCount ? `${inv.cpuCount} cores` : "—"}</td><td class="mono num">${inv.memoryBytes ? formatBytes(inv.memoryBytes) : "—"}</td><td>${a.capabilities.join(", ")}</td><td class="mono muted">${new Date(a.lastSeenAt).toLocaleTimeString()}</td><td>${a.status === "offline" ? `<button class="btn btn-sm btn-danger" data-action="delete-agent" data-agent-id="${a.id}">Remove</button>` : "—"}</td></tr>`;
            })
            .join("")
        : `<tr><td colspan="9"><div class="empty">${state.apiOnline ? "No agents registered" : "Server API is unavailable"}</div></td></tr>`
    }</tbody></table></div></div>`
  );
}

function agentInventoryPage() {
  let report = state.liveInventories.find(
      (x) => x.agentId === state.selectedAgentId,
    ),
    agent = state.liveAgents.find((x) => x.id === state.selectedAgentId);
  if (!report)
    return (
      pageHead(
        "Agent inventory",
        "Inventory report unavailable",
        ``,
      ) + `<div class="empty">Waiting for the first inventory report</div>`
    );
  let d = report.data || {};
  // Live usage of this agent's node, for the per-card sub-lines.
  const node = state.liveResources.find((r) => r.agentId === agent?.id);
  const usage =
    (state.liveUtilization?.nodes || []).find((n) => n.id === node?.id) ||
    (state.liveOverview?.cells || []).find((c) => c.id === node?.id);
  const used = (value) =>
    Number.isFinite(Number(value)) ? `${Number(value).toFixed(0)}% in use` : "";
  const platform = [d.os, d.arch].filter(Boolean).join(" · ");
  const agentTab = state.agentTab === "runtime" ? "runtime" : "hardware";
  const agentTabs = `<div class="tabbar">${[
    ["hardware", "Hardware"],
    ["runtime", "Services"],
  ]
    .map(
      ([key, label]) =>
        `<button class="tab ${key === agentTab ? "active" : ""}" data-agent-tab="${key}">${label} <span class="count-badge">${specCount(d, key)}</span></button>`,
    )
    .join("")}</div>`;
  return `<div class="breadcrumb"><button class="link" data-page="fleet">Agents</button> / <span>${agent?.hostname || report.agentId}</span></div>${pageHead(agent?.hostname || "Agent inventory", `${platform ? `${escapeHTML(platform)} · ` : ""}Observed ${new Date(report.observedAt).toLocaleString(consoleLocale())}`, `${node ? `<button class="btn" data-live-resource="${escapeHTML(node.id)}">Open node</button>` : ""}<button class="btn btn-primary" data-action="create-operation" data-target-id="${escapeHTML(node?.id || "")}">Refresh inventory</button>`)}<div class="grid kpis">${kpi("CPU CORES", String(d.cpuCount || 0), used(usage?.cpu) || d.arch || "—")}${kpi("MEMORY", formatBytes(d.memoryBytes), used(usage?.memory) || "Installed capacity")}${kpi("DISKS", String((d.disks || []).length), used(usage?.disk) || "Block devices")}${kpi("NETWORK", String((d.interfaces || []).length), "Interfaces")}${kpi("PROCESSES", String((d.processes || []).length), "Running on this host")}</div>${agentTabs}${machineSpecs(d, agentTab)}`;
}

// Inventory sections: hardware is what the machine is, runtime what it runs.
// Each renders as a table so columns line up instead of one joined sentence.
const SPEC_SECTIONS = {
  hardware: [
    {
      title: "Disks",
      columns: ["Device", "Size", "Model"],
      numeric: [1],
      pick: (d) => d.disks || [],
      row: (x) => [x.name, formatBytes(x.sizeBytes), x.model || "—"],
    },
    {
      title: "Network interfaces",
      columns: ["Interface", "Addresses", "MTU"],
      numeric: [2],
      pick: (d) => d.interfaces || [],
      row: (x) => [x.name, (x.addresses || []).join(", ") || "no address", x.mtu],
    },
  ],
  runtime: [
    {
      title: "Services",
      columns: ["Service", "Active", "Sub"],
      pick: (d) => d.services || [],
      row: (x) => [x.name, x.active, x.sub],
    },
  ],
};

const SPEC_ROW_CAP = 50;

// The services tab is a table of its own, so it gets the filtering and paging
// the shared spec card cannot: 160 units truncated to 50 hid the failed ones.
const SERVICE_FILTERS = [
  ["all", "All", () => true],
  ["failed", "Failed", (x) => x.sub === "failed" || x.active === "failed"],
  ["running", "Running", (x) => x.sub === "running"],
  ["inactive", "Inactive", (x) => x.active !== "active"],
];

function serviceRank(item) {
  if (item.sub === "failed" || item.active === "failed") return 0;
  if (item.sub === "running") return 1;
  if (item.active === "active") return 2;
  return 3;
}

function servicesSection(d) {
  const all = [...(d.services || [])].sort(
    (a, b) => serviceRank(a) - serviceRank(b) || String(a.name).localeCompare(String(b.name)),
  );
  const key = SERVICE_FILTERS.some(([k]) => k === state.serviceFilter)
    ? state.serviceFilter
    : "all";
  const query = (state.serviceQuery || "").trim().toLowerCase();
  const match = SERVICE_FILTERS.find(([k]) => k === key)[2];
  const shown = all.filter(
    (x) => match(x) && (!query || String(x.name).toLowerCase().includes(query)),
  );
  const count = (k) => all.filter(SERVICE_FILTERS.find(([n]) => n === k)[2]).length;
  const chip = ([k, label]) =>
    `<button class="filter-chip ${key === k ? "active" : ""}" data-service-filter="${k}">${label} ${count(k)}</button>`;
  const page = pagedList(shown, "services");
  const tone = (x) =>
    x.sub === "failed" || x.active === "failed"
      ? "critical"
      : x.sub === "running"
        ? "ok"
        : "muted";
  const rows = shown.length
    ? page.slice
        .map(
          (x) =>
            `<tr><td class="mono">${escapeHTML(x.name || "—")}</td><td class="${tone(x)}"><i class="dot"></i>${escapeHTML(x.active || "—")}</td><td class="mono">${escapeHTML(x.sub || "—")}</td></tr>`,
        )
        .join("")
    : '<tr><td colspan="3"><div class="empty">No service matches the current filters</div></td></tr>';
  return (
    `<div class="filterbar"><input id="service-filter" placeholder="Filter services…" value="${escapeHTML(state.serviceQuery || "")}">${SERVICE_FILTERS.map(chip).join("")}<span class="result-count">${shown.length} of ${all.length}</span></div>` +
    card(
      "Services",
      `<div class="table-wrap"><table class="table compact"><thead><tr><th>Service</th><th>Active</th><th>Sub</th></tr></thead><tbody data-i18n-skip>${rows}</tbody></table></div>${page.bar}`,
    )
  );
}

function specCard(d, section) {
  const items = section.pick(d);
  const shown = items.slice(0, SPEC_ROW_CAP);
  const align = (i) => (section.numeric?.includes(i) ? ' class="num mono"' : "");
  return card(
    section.title,
    items.length
      ? `<div class="table-wrap scroll-list"><table class="table compact"><thead><tr>${section.columns.map((c, i) => `<th${align(i)}>${c}</th>`).join("")}</tr></thead><tbody data-i18n-skip>${shown
          .map(
            (item) =>
              `<tr>${section.row(item).map((value, i) => `<td${align(i)}>${escapeHTML(String(value ?? "—"))}</td>`).join("")}</tr>`,
          )
          .join("")}</tbody></table></div>${items.length > shown.length ? `<div class="list-foot muted">Showing ${shown.length} of ${items.length}</div>` : ""}`
      : '<div class="empty">Not detected or unavailable</div>',
    items.length ? `<span class="count-badge">${items.length}</span>` : "",
  );
}

function machineSpecs(d, section) {
  const sections = section
    ? SPEC_SECTIONS[section]
    : [...SPEC_SECTIONS.hardware, ...SPEC_SECTIONS.runtime];
  if (section === "runtime") return servicesSection(d);
  return `<div class="inventory-grid">${sections.map((spec) => specCard(d, spec)).join("")}</div>`;
}

function specCount(d, section) {
  return SPEC_SECTIONS[section].reduce((total, spec) => total + spec.pick(d).length, 0);
}

function rolesPage() {
  let data = state.liveRoles;
  return (
    pageHead(
      "Roles",
      "Manage permissions by operational responsibility",
      `<button class="btn" data-action="test-access">Test access</button><button class="btn btn-primary" data-action="create-role">+ Create role</button>`,
    ) +
    `<div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Role</th><th>Type</th><th>Permissions</th><th>Usage</th><th>Actions</th></tr></thead><tbody>${data.map((r) => `<tr><td><div class="resource"><span class="resource-icon">RB</span>${r.name}</div></td><td>${r.system ? "System" : "Custom"}</td><td>${r.permissions.map((p) => `<span class="tag">${p.resource}:${p.action}</span>`).join(" ")}</td><td class="mono">${state.liveBindings.filter((b) => b.roleId === r.id).length} bindings</td><td><button class="btn btn-sm" data-action="edit-role" data-role-id="${r.id}">${r.system ? "View" : "Edit"}</button>${r.system ? "" : ` <button class="btn btn-sm btn-danger" data-action="delete-role" data-role-id="${r.id}">Delete</button>`}</td></tr>`).join("")}</tbody></table></div></div>`
  );
}

function scopesPage() {
  let data = state.liveScopes;
  return (
    pageHead(
      "Access scopes",
      "Manage hierarchical resource access boundaries",
      `<button class="btn btn-primary" data-action="create-scope">+ Create scope</button>`,
    ) +
    `<div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Scope</th><th>Hierarchy paths</th><th>Tag selector</th><th>Usage</th><th>Actions</th></tr></thead><tbody>${data
      .map(
        (s) =>
          `<tr><td><div class="resource"><span class="resource-icon">SC</span>${s.name}</div></td><td>${(s.paths || []).map((p) => `<span class="scope-badge">${p}</span>`).join(" ") || "—"}</td><td>${
            Object.entries(s.tags || {})
              .map(([k, v]) => `${k}=${v}`)
              .join(", ") || "—"
          }</td><td class="mono">${state.liveBindings.filter((b) => b.scopeId === s.id).length} bindings</td><td><button class="btn btn-sm" data-action="edit-scope" data-scope-id="${s.id}">${s.system ? "View" : "Edit"}</button>${s.system ? "" : ` <button class="btn btn-sm btn-danger" data-action="delete-scope" data-scope-id="${s.id}">Delete</button>`}</td></tr>`,
      )
      .join("")}</tbody></table></div></div>`
  );
}

function bindingsPage() {
  let roles = new Map(state.liveRoles.map((x) => [x.id, x.name])),
    scopes = new Map(state.liveScopes.map((x) => [x.id, x.name])),
    users = new Set(state.liveUsers.map((x) => x.username));
  return (
    pageHead(
      "Role bindings",
      "Assign roles to users and teams within hierarchy scopes",
      `<button class="btn btn-primary" data-action="create-binding">+ Create binding</button>`,
    ) +
    `<div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Subject</th><th>Role</th><th>Scope</th><th>Expires</th><th>Created</th><th>Actions</th></tr></thead><tbody>${state.liveBindings.length ? state.liveBindings.map((b) => `<tr><td><div class="resource"><span class="resource-icon">ID</span>${users.has(b.subjectId) ? `<button class="link" data-open-user="${escapeHTML(b.subjectId)}">${escapeHTML(b.subjectId)}</button>` : escapeHTML(b.subjectId)}</div></td><td>${roles.get(b.roleId) || b.roleId}</td><td><span class="scope-badge">${scopes.get(b.scopeId) || b.scopeId}</span></td><td class="mono muted">${b.expiresAt ? new Date(b.expiresAt).toLocaleString() : "Never"}</td><td class="mono muted">${new Date(b.createdAt).toLocaleDateString()}</td><td><button class="btn btn-sm" data-action="edit-binding" data-binding-id="${b.id}">Edit</button> <button class="btn btn-sm btn-danger" data-action="delete-binding" data-binding-id="${b.id}">Delete</button></td></tr>`).join("") : `<tr><td colspan="6"><div class="empty">No role bindings</div></td></tr>`}</tbody></table></div></div>`
  );
}

function operationsPage() {
  const all = state.liveOperations;
  const query = (state.opQuery || "").trim().toLowerCase();
  const status = state.opStatus || "all";
  const data = all.filter((o) => {
    if (status === "all" ? false : o.status !== status) return false;
    if (!query) return true;
    return `${o.type} ${o.targetIds.join(" ")} ${o.requestedBy} ${o.reason || ""}`
      .toLowerCase()
      .includes(query);
  });
  const count = (key) => all.filter((o) => o.status === key).length;
  const tile = (key, label, value, sub, tone) =>
    `<button class="kpi-filter ${status === key ? "active" : ""}" data-op-status="${key}">${kpi(label, value, sub, tone)}</button>`;
  const { slice, bar } = pagedList(data, "operations");
  return (
    pageHead(
      "Task history",
      "Approved operations executed through managed agents",
      `<button class="btn" data-action="refresh-data">Refresh</button><button class="btn btn-primary" data-action="create-operation">+ Run diagnostic</button>`,
    ) +
    `<div class="grid kpis">${tile("all", "ALL TASKS", String(all.length), "Every request", count("failed") ? "" : "calm")}${tile("running", "RUNNING", String(count("running")), "Executing now", "info")}${tile("awaiting_approval", "AWAITING APPROVAL", String(count("awaiting_approval")), "Blocked on review", "warn")}${tile("failed", "FAILED", String(count("failed")), "Needs attention", count("failed") ? "critical" : "calm")}</div><div class="filterbar"><input id="op-filter" placeholder="Filter by task, target, requester, or reason…" value="${escapeHTML(state.opQuery || "")}">${status === "all" && !query ? "" : `<span class="mono muted">${data.length} of ${all.length}</span>`}</div><div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Task</th><th>Status</th><th>Target</th><th>Requested by</th><th>Updated</th><th>Actions</th></tr></thead><tbody>${data.length ? slice.map((o) => `<tr data-action="view-operation" data-operation-id="${escapeHTML(o.id)}"><td><div class="resource"><span class="resource-icon">OP</span><div>${escapeHTML(o.type)}<div class="muted line-clip">${escapeHTML(o.reason || o.id)}</div></div></div></td><td class="${statusTone(o.status)}"><i class="dot"></i>${statusText(o.status)}</td><td>${o.targetIds.map((id) => `<button class="link mono" data-live-resource="${escapeHTML(id)}">${escapeHTML(id)}</button>`).join(", ")}</td><td>${escapeHTML(o.requestedBy)}</td><td class="mono muted">${formatWhen(o.updatedAt)}</td><td>${o.status === "awaiting_approval" ? `<button class="btn btn-sm btn-primary" data-action="approve-operation" data-operation-id="${escapeHTML(o.id)}">Approve</button>` : `<button class="btn btn-sm" data-action="view-operation" data-operation-id="${escapeHTML(o.id)}">View</button>`}</td></tr>`).join("") : `<tr><td colspan="6"><div class="empty">${all.length ? "No task matches this filter" : "No operations requested"}</div></td></tr>`}</tbody></table></div>${bar}</div>`
  );
}

function incidentsPage() {
  const all = state.liveIncidents;
  const filter = state.incidentFilter || "all";
  const imatch = {
    active: (x) => x.status !== "resolved",
    investigating: (x) => x.status === "investigating",
    mitigating: (x) => x.status === "mitigating",
    resolved: (x) => x.status === "resolved",
  };
  const data = filter === "all" ? all : all.filter(imatch[filter] || (() => true));
  const icount = (key) => all.filter(imatch[key]).length;
  const itile = (key, label, value, sub, tone) =>
    `<button class="kpi-filter ${filter === key ? "active" : ""}" data-incident-filter="${key}">${kpi(label, value, sub, tone)}</button>`;
  return (
    pageHead(
      "Incidents",
      "Coordinate response and document infrastructure incidents",
      `<button class="btn btn-primary" data-action="create-incident">+ Declare incident</button>`,
    ) +
    `<div class="grid kpis">${itile("active", "ACTIVE", String(icount("active")), "Declared incidents", icount("active") ? "critical" : "calm")}${itile("investigating", "INVESTIGATING", String(icount("investigating")), "Under investigation", icount("investigating") ? "warn" : "calm")}${itile("mitigating", "MITIGATING", String(icount("mitigating")), "Actions in progress", "info")}${itile("resolved", "RESOLVED", String(icount("resolved")), "Closed incidents", "ok")}</div><div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Incident</th><th>Severity</th><th>Status</th><th>Commander</th><th>Resources</th><th>Updated</th></tr></thead><tbody>${data.length ? data.map((i) => `<tr data-incident="${i.id}"><td><div class="resource"><span class="resource-icon">IN</span><div>${i.title}<div class="mono muted">${i.id}</div></div></div></td><td class="${i.severity === "critical" ? "critical" : "warn"}">${i.severity}</td><td><span class="status-pill ${i.status === "resolved" ? "ok" : "warn"}">${escapeHTML(i.status)}</span></td><td>${i.commander || "Unassigned"}</td><td>${(i.resourceIds || []).length}</td><td class="mono muted">${formatWhen(i.updatedAt)}</td></tr>`).join("") : `<tr><td colspan="6"><div class="empty">${all.length ? "No incident matches this filter" : "No active incidents"}</div></td></tr>`}</tbody></table></div></div>`
  );
}

function incidentDetailPage() {
  let incident = state.liveIncidents.find(
    (x) => x.id === state.selectedIncidentId,
  );
  if (!incident) {
    state.selectedIncidentId = null;
    return incidentsPage();
  }
  const resourcesById = new Map(state.liveResources.map((r) => [r.id, r]));
  const cells = new Map(
    (state.liveOverview?.cells || []).map((c) => [c.id, c]),
  );
  const affected = incident.resourceIds || [];
  // Alerts linked to this incident at declaration.
  const linkedAlerts = (incident.alertIds || [])
    .map((id) => state.liveAlerts.find((a) => a.id === id))
    .filter(Boolean);
  const pct = (m, key) => (m[key] != null ? Number(m[key]).toFixed(1) + "%" : "—");
  const affectedRows = affected.length
    ? affected
        .map((id) => {
          const r = resourcesById.get(id),
            m = cells.get(id) || {},
            health = m.health || r?.health || "unknown";
          return `<tr data-live-resource="${id}"><td><div class="resource"><span class="resource-icon">${(r?.type || "rl").slice(0, 2).toUpperCase()}</span><div>${escapeHTML(r?.name || id)}<div class="muted mono">${escapeHTML(r?.type || id)}</div></div></div></td><td class="${healthTone(health)}"><i class="dot"></i>${health}</td><td class="mono num">${pct(m, "cpu")}</td><td class="mono num">${pct(m, "memory")}</td><td class="mono num">${pct(m, "disk")}</td></tr>`;
        })
        .join("")
    : '<tr><td colspan="5"><div class="empty">No resource linked to this incident</div></td></tr>';
  return `<div class="breadcrumb"><button class="link" data-page="incidents">Incidents</button> / <span>${escapeHTML(incident.id)}</span></div>${pageHead(incident.title, incident.description || "Infrastructure incident", `<button class="btn" data-action="add-incident-note">+ Note</button><button class="btn" data-action="edit-incident">Edit</button><button class="btn btn-primary" data-action="change-incident-status">Change status</button><button class="btn btn-danger" data-action="delete-incident">Delete</button>`)}${card(
    "Affected resources",
    `<div class="table-wrap"><table class="table"><thead><tr><th>Resource</th><th>Health</th><th class="num">CPU</th><th class="num">Memory</th><th class="num">Disk</th></tr></thead><tbody>${affectedRows}</tbody></table></div>`,
    `<span class="muted">Click a row to inspect</span>`,
  )}<div style="height:12px"></div><div class="detail-layout"><div>${card("Incident timeline", `<div class="card-body">${incidentTimelineBody(incident)}</div>`)}</div><div>${card("Incident details", `<div class="spec-grid"><div class="spec"><label>Status</label><span class="warn">${incident.status}</span></div><div class="spec"><label>Severity</label><span class="critical">${incident.severity}</span></div><div class="spec"><label>Commander</label><span>${incident.commander || "Unassigned"}</span></div><div class="spec"><label>Resources</label><span>${affected.length}</span></div></div>`)}<div style="height:12px"></div>${card("Linked alerts", `<div class="card-body">${
    linkedAlerts.length
      ? linkedAlerts
          .map(
            (alert) =>
              `<div class="relation-node"><span class="resource-icon">AL</span><div>${escapeHTML(alert.name)}<div class="muted">${escapeHTML(alert.summary || "")} · ${formatWhen(alert.updatedAt)}</div></div><span class="status-pill ${alert.severity === "critical" ? "critical" : "warn"}" style="margin-left:auto">${escapeHTML(alert.status)}</span></div>`,
          )
          .join("")
      : '<div class="empty">No alert was linked to this incident</div>'
  }</div>`, linkedAlerts.length ? `<span class="count-badge">${linkedAlerts.length}</span>` : "")}</div></div>`;
}

function alertRulesPage() {
  let data = state.liveAlertRules,
    silences = state.liveAlertSilences,
    now = Date.now();
  return (
    pageHead(
      "Alert rules",
      "Manage metric thresholds, duration, severity, and hierarchy scope",
      `<button class="btn" data-action="silence">+ Silence window</button><button class="btn btn-primary" data-action="create-alert-rule">+ New rule</button>`,
    ) +
    `<div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Rule</th><th>Condition</th><th>Duration</th><th>Severity</th><th>Scope</th><th>Status</th><th>Actions</th></tr></thead><tbody>${data.length ? data.map((r) => `<tr><td><div class="resource"><span class="resource-icon">AR</span>${r.name}</div></td><td><span>${metricLabel(r.metric)}</span> <span class="mono">${escapeHTML(r.operator)} ${r.threshold}${["cpu", "memory", "disk"].includes(r.metric) ? "%" : "/s"}</span></td><td>${r.duration}</td><td class="${r.severity === "critical" ? "critical" : "warn"}">${r.severity}</td><td><span class="scope-badge">${r.scopePath || "*"}</span>${Object.entries(r.selector || {}).map(([k, v]) => `<span class="scope-badge">${escapeHTML(`${k}=${v}`)}</span>`).join("")}</td><td class="${r.enabled ? "ok" : "unknown"}"><i class="dot"></i>${r.enabled ? "Enabled" : "Disabled"}</td><td><button class="btn btn-sm" data-action="edit-alert-rule" data-rule-id="${r.id}">Edit</button> <button class="btn btn-sm btn-danger" data-action="delete-alert-rule" data-rule-id="${r.id}">Delete</button></td></tr>`).join("") : `<tr><td colspan="7"><div class="empty">No alert rules configured</div></td></tr>`}</tbody></table></div></div><div style="height:12px"></div>${card("Silence windows", `<div class="table-wrap"><table class="table"><thead><tr><th>Name</th><th>Scope</th><th>Selector</th><th>Window</th><th>Status</th><th>Actions</th></tr></thead><tbody>${silences.length ? silences.map((s) => `<tr><td>${escapeHTML(s.name)}</td><td class="mono">${escapeHTML(s.scopePath || "*")}</td><td class="mono">${escapeHTML(Object.entries(s.selector || {}).map(([k, v]) => `${k}=${v}`).join(", ") || "*")}</td><td>${new Date(s.startsAt).toLocaleString()}<br><span class="muted">to ${new Date(s.endsAt).toLocaleString()}</span></td><td><span class="status-pill ${now >= Date.parse(s.startsAt) && now < Date.parse(s.endsAt) ? "ok" : "unknown"}">${now < Date.parse(s.startsAt) ? "Scheduled" : now < Date.parse(s.endsAt) ? "Active" : "Expired"}</span></td><td><button class="btn btn-sm" data-action="edit-alert-silence" data-silence-id="${s.id}">Edit</button> <button class="btn btn-sm btn-danger" data-action="delete-alert-silence" data-silence-id="${s.id}">Delete</button></td></tr>`).join("") : '<tr><td colspan="6"><div class="empty">No silence windows</div></td></tr>'}</tbody></table></div>`)}`
  );
}

function notificationRoutingPage() {
  let channels = new Map(state.liveNotificationChannels.map((item) => [item.id, item.name]));
  const del = pagedList(state.liveNotificationDeliveries, "deliveries");
  return pageHead("Alert delivery", "Inhibition policies, webhook routes, and delivery status", `<button class="btn" data-action="create-inhibition">+ Inhibition</button><button class="btn" data-action="create-channel">+ Channel</button><button class="btn btn-primary" data-action="create-route">+ Route</button>`) +
    `<div class="card"><div class="card-head"><div class="card-title">Inhibition policies</div></div><div class="table-wrap"><table class="table"><thead><tr><th>Name</th><th>Source</th><th>Target</th><th>Scope</th><th>Equal labels</th><th>Actions</th></tr></thead><tbody>${state.liveAlertInhibitions.length ? state.liveAlertInhibitions.map((item) => `<tr><td>${escapeHTML(item.name)}</td><td class="critical">${item.sourceSeverity}</td><td class="warn">${item.targetSeverity}</td><td class="mono">${escapeHTML(item.scopePath || "*")}</td><td class="mono">${escapeHTML((item.equalLabels || []).join(", ") || "same resource")}</td><td><button class="btn btn-sm" data-action="edit-inhibition" data-id="${item.id}">Edit</button> <button class="btn btn-sm btn-danger" data-action="delete-inhibition" data-id="${item.id}">Delete</button></td></tr>`).join("") : '<tr><td colspan="6"><div class="empty">No inhibition policies</div></td></tr>'}</tbody></table></div></div><div style="height:12px"></div>` +
    `<div class="detail-layout"><div class="card"><div class="card-head"><div class="card-title">Webhook channels</div></div><div class="card-body">${state.liveNotificationChannels.length ? state.liveNotificationChannels.map((item) => `<div class="relation-node"><span class="resource-icon">WH</span><div><b>${escapeHTML(item.name)}</b><div class="mono muted">${escapeHTML(item.url)}</div></div><button class="btn btn-sm" style="margin-left:auto" data-action="edit-channel" data-id="${item.id}">Edit</button><button class="btn btn-sm btn-danger" data-action="delete-channel" data-id="${item.id}">Delete</button></div>`).join("") : '<div class="empty">No channels</div>'}</div></div><div class="card"><div class="card-head"><div class="card-title">Routes</div></div><div class="card-body">${state.liveNotificationRoutes.length ? state.liveNotificationRoutes.map((item) => `<div class="relation-node"><span class="resource-icon">RT</span><div><b>${escapeHTML(item.name)}</b><div class="muted">${escapeHTML((item.severities || []).join(", ") || "all severities")} · ${(item.channelIds || []).map((id) => escapeHTML(channels.get(id) || id)).join(", ")}</div></div><button class="btn btn-sm" style="margin-left:auto" data-action="edit-route" data-id="${item.id}">Edit</button><button class="btn btn-sm btn-danger" data-action="delete-route" data-id="${item.id}">Delete</button></div>`).join("") : '<div class="empty">No routes</div>'}</div></div></div><div style="height:12px"></div>` +
    `<div class="card"><div class="card-head"><div class="card-title">Recent deliveries</div></div><div class="table-wrap"><table class="table"><thead><tr><th>Time</th><th>Alert</th><th>Event</th><th>Channel</th><th>Status</th><th>Attempts</th></tr></thead><tbody>${state.liveNotificationDeliveries.length ? del.slice.map((item) => `<tr><td class="mono muted">${new Date(item.createdAt).toLocaleTimeString()}</td><td class="mono">${item.alertId}</td><td>${item.event}</td><td>${escapeHTML(channels.get(item.channelId) || item.channelId)}</td><td class="${item.status === "succeeded" ? "ok" : item.status === "failed" ? "critical" : "warn"}">${item.status}</td><td>${item.attempts}</td></tr>`).join("") : '<tr><td colspan="6"><div class="empty">No deliveries</div></td></tr>'}</tbody></table></div>${del.bar}</div>`;
}

function runbooksPage() {
  const data = state.liveRunbooks;
  const runbookName = new Map(data.map((r) => [r.id, r.name]));
  const query = (state.execQuery || "").trim().toLowerCase();
  const executions = state.liveExecutions.filter((e) =>
    query
      ? `${runbookName.get(e.runbookId) || ""} ${e.targetIds.join(" ")} ${e.requestedBy} ${e.status}`
          .toLowerCase()
          .includes(query)
      : true,
  );
  const execFilter = state.execFilter || "all";
  const eCount = (key) => state.liveExecutions.filter((e) => e.status === key).length;
  const eTile = (key, label, value, sub, tone) =>
    `<button class="kpi-filter ${execFilter === key ? "active" : ""}" data-exec-filter="${key}">${kpi(label, value, sub, tone)}</button>`;
  const execKpis = `<div class="grid kpis">${eTile("all", "RUNBOOKS", String(data.length), "Defined procedures")}${eTile("succeeded", "SUCCEEDED", String(eCount("succeeded")), "Completed runs", "ok")}${eTile("awaiting_approval", "AWAITING APPROVAL", String(eCount("awaiting_approval")), "Blocked on review", eCount("awaiting_approval") ? "warn" : "calm")}${eTile("failed", "FAILED", String(eCount("failed")), "Needs attention", eCount("failed") ? "critical" : "calm")}</div>`;
  const exec = pagedList(
    execFilter === "all"
      ? executions
      : executions.filter((e) => e.status === execFilter),
    "executions",
  );
  return (
    pageHead(
      "Automations",
      "Reviewable runbooks for infrastructure diagnosis and recovery",
      `<button class="btn btn-primary" data-action="create-runbook">+ Create runbook</button>`,
    ) +
    execKpis +
    `<div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Runbook</th><th>Risk</th><th>Steps</th><th>Description</th><th>Actions</th></tr></thead><tbody>${data.length ? data.map((r) => `<tr><td><div class="resource"><span class="resource-icon">RB</span>${r.name}</div></td><td><span class="status-pill ${r.risk === "high" ? "critical" : r.risk === "low" ? "ok" : "warn"}">${escapeHTML(riskText(r.risk))}</span></td><td class="mono">${r.steps.length}</td><td>${r.description || "—"}</td><td><button class="btn btn-sm" data-action="execute-runbook" data-runbook-id="${r.id}">Run</button> <button class="btn btn-sm" data-action="edit-runbook" data-runbook-id="${r.id}">Edit</button> <button class="btn btn-sm btn-danger" data-action="delete-runbook" data-runbook-id="${r.id}">Delete</button></td></tr>`).join("") : `<tr><td colspan="5"><div class="empty">No runbooks configured</div></td></tr>`}</tbody></table></div></div><div style="height:12px"></div><div class="card"><div class="card-head"><div class="card-title">Recent executions</div><input id="exec-filter" class="head-filter" placeholder="Filter executions…" value="${escapeHTML(state.execQuery || "")}"></div><div class="table-wrap"><table class="table"><thead><tr><th>Runbook</th><th>Status</th><th>Target</th><th>Steps</th><th>Requested by</th><th>Ran at</th><th>Action</th></tr></thead><tbody>${executions.length ? exec.slice.map((e) => `<tr data-action="view-execution" data-execution-id="${e.id}"><td><div class="resource"><span class="resource-icon">EX</span><div>${runbookName.has(e.runbookId) ? escapeHTML(runbookName.get(e.runbookId)) : e.runbookName ? `${escapeHTML(e.runbookName)} <span class="muted">(deleted)</span>` : '<span class="muted">Deleted runbook</span>'}<div class="muted mono">${e.id}</div></div></div></td><td class="${statusTone(e.status)}"><i class="dot"></i>${statusText(e.status)}</td><td class="mono">${e.targetIds.join(", ")}</td><td>${e.operationIds.length}</td><td>${e.requestedBy}</td><td class="mono muted">${formatWhen(e.updatedAt || e.createdAt)}</td><td>${e.status === "awaiting_approval" ? `<button class="btn btn-sm" data-action="approve-runbook" data-execution-id="${e.id}">Approve</button>` : `<button class="btn btn-sm" data-action="view-execution" data-execution-id="${e.id}">View</button>`}</td></tr>`).join("") : `<tr><td colspan="7"><div class="empty">${state.liveExecutions.length ? "No execution matches this filter" : "No executions"}</div></td></tr>`}</tbody></table></div>${exec.bar}</div>`
  );
}

// Which session the console is looking at.
//
// Several sessions can be open at once, and the list reorders as they come and
// go. A live connection therefore outranks the first entry: without that, a
// refresh that reshuffled the list moved the socket to a different shell and
// took whatever was in flight with it — keystrokes landing in a pty the
// operator was not looking at.
function activeTerminalSession(sessions) {
  const pinned = state.activeTerminalTab;
  if (pinned) {
    const chosen = sessions.find((s) => s.id === pinned);
    if (chosen) return chosen;
    // The operator has asked for a particular session and it is not active
    // yet. Opening an unrelated one in the meantime would put their first
    // keystrokes into a shell they did not choose, and then move them out of
    // it the moment the approval landed.
    if (state.liveTerminals.some((s) => s.id === pinned)) return undefined;
  }
  return sessions.find((s) => s.id === terminalSessionId) || sessions[0];
}

function managedTerminalPage() {
  const allSessions = state.liveTerminals;
  const sessionFilter = state.terminalFilter || "all";
  const sessionQuery = (state.terminalQuery || "").trim().toLowerCase();
  const sCount = (key) => allSessions.filter((x) => x.status === key).length;
  const sTile = (key, label, value, sub, tone) =>
    `<button class="kpi-filter ${sessionFilter === key ? "active" : ""}" data-terminal-filter="${key}">${kpi(label, value, sub, tone)}</button>`;
  const sessionKpis = `<div class="grid kpis">${sTile("all", "SESSIONS", String(allSessions.length), "Requested in total")}${sTile("awaiting_approval", "AWAITING APPROVAL", String(sCount("awaiting_approval")), "Blocked on review", sCount("awaiting_approval") ? "warn" : "calm")}${sTile("active", "ACTIVE", String(sCount("active")), "Open right now", sCount("active") ? "ok" : "calm")}${sTile("closed", "CLOSED", String(sCount("closed")), "Ended, recording kept")}</div>`;
  let data = allSessions.filter(
    (x) =>
      (sessionFilter === "all" || x.status === sessionFilter) &&
      (!sessionQuery ||
        `${x.targetId} ${x.requestedBy} ${x.approvedBy || ""} ${x.reason || ""}`
          .toLowerCase()
          .includes(sessionQuery)),
  ),
    activeSessions = allSessions.filter((x) => x.status === "active"),
    active = activeTerminalSession(activeSessions);
  // Order: awaiting approval, then active, then closed.
  const rank = (s) =>
    s.status === "awaiting_approval" ? 0 : s.status === "active" ? 1 : 2;
  const ordered = [...data].sort((a, b) => rank(a) - rank(b));
  const sessions = pagedList(ordered, "terminals");
  // The shell itself is drawn by the docked panel, which is on every page.
  // Drawing it here as well would put two elements with the same id on the
  // page, and the paint path finds the pane by that id: one copy would take
  // the output and the other the keystrokes. So this page points at the dock.
  const consoleBlock = active
    ? `<div class="card"><div class="card-body dock-pointer"><div><b>${escapeHTML(active.targetId)}</b> is open in the terminal panel<div class="muted mono">Session ${escapeHTML(active.id)} · Approved by ${escapeHTML(active.approvedBy || "—")}</div></div><button class="btn btn-primary" data-action="show-terminal-dock">Show terminal</button></div></div><div style="height:12px"></div>`
    : `<div class="card"><div class="card-body" style="text-align:center;color:var(--dim);padding:16px">No active session — request one and get it approved to open a shell.</div></div><div style="height:12px"></div>`;
  const terminalTabs =
    activeSessions.length > 1
      ? `<div class="tabbar">${activeSessions
          .map(
            (s) =>
              `<button class="tab ${active?.id === s.id ? "active" : ""}" data-term-tab="${s.id}">${escapeHTML(s.targetId)} <span class="muted mono">#${s.id.slice(-4)}</span></button>`,
          )
          .join("")}</div>`
      : "";
  const rows = data.length
    ? sessions.slice
        .map(
          (s) =>
            `<tr><td><div class="resource"><span class="resource-icon">TS</span><div><button class="link" data-live-resource="${escapeHTML(s.targetId)}">${escapeHTML(s.targetId)}</button><div class="muted mono">${s.id}</div></div></div></td><td>${escapeHTML(s.requestedBy)}</td><td><span class="status-pill ${statusTone(s.status)}">${statusText(s.status)}</span></td><td>${s.approvedBy ? escapeHTML(s.approvedBy) : "—"}</td><td class="mono muted">${formatWhen(s.createdAt)}</td><td class="mono muted">${s.closedAt ? formatWhen(s.closedAt) : "—"}</td><td>${s.status === "awaiting_approval" ? (s.requestedBy === state.subject && !can("terminal", "approve-self") ? `<span class="muted" title="Separation of duties: a different user must approve your own request">Needs another approver</span>` : `<button class="btn btn-sm btn-primary" data-action="approve-terminal" data-session-id="${s.id}">Approve</button>`) : `<button class="btn btn-sm" data-action="view-terminal-recording" data-session-id="${s.id}">Recording</button> <button class="btn btn-sm btn-danger" data-action="delete-terminal-recording" data-session-id="${s.id}">Delete</button>`}</td></tr>`,
        )
        .join("")
    : '<tr><td colspan="7"><div class="empty">No sessions requested</div></td></tr>';
  return (
    pageHead(
      "Remote shell",
      "Approval-controlled, audited terminal sessions to managed nodes",
      `<button class="btn btn-primary" data-action="create-terminal-session">+ Request session</button>`,
    ) +
    sessionKpis +
    terminalTabs +
    consoleBlock +
    card(
      "Session requests",
      `<div class="filterbar"><input id="terminal-filter" placeholder="Filter by target, requester, or reason…" value="${escapeHTML(state.terminalQuery || "")}">${data.length !== allSessions.length ? `<span class="mono muted">${data.length} of ${allSessions.length}</span>` : ""}</div><div class="table-wrap"><table class="table"><thead><tr><th>Target</th><th>Requested by</th><th>Status</th><th>Approved by</th><th>Requested</th><th>Ended</th><th>Actions</th></tr></thead><tbody>${rows}</tbody></table></div>${sessions.bar}`,
    )
  );
}

// The shell docks, and follows the operator from page to page.
//
// A terminal is opened to settle something the rest of the console is showing:
// which container the kernel killed, whether the graph moved after a restart,
// what the alert says now. Every one of those lookups used to cost a
// navigation away from the shell -- and navigating away ended it, because the
// connection was driven by `page === "terminal"`. Half a diagnosis would be on
// screen and the pty holding the other half was gone, with a fresh approval
// needed to get it back. So the panel is drawn on every page, the way the
// sidebar is, and the session outlives the page it was opened from.
//
// It is drawn in exactly one place. `#terminal-screen` is a single id and the
// paint path finds the pane by it, so a second screen anywhere else would take
// the output while the keystrokes went to the other.
//
// Collapsed is the resting state: a bar in the corner, tall enough to say
// which host is open and whether the stream is up, short enough that nothing
// underneath is covered.
function terminalDockPanel() {
  if (!state.auth?.authenticated) return "";
  const actives = (state.liveTerminals || []).filter(
    (s) => s.status === "active",
  );
  const active = activeTerminalSession(actives);
  if (!active) return "";
  const view = terminalDockView(state.terminalDock);
  const streamConnected =
    terminalSessionId === active.id &&
    terminalSocket?.readyState === WebSocket.OPEN;
  // A pager or an editor has asked for the alternate screen, which means it
  // owns the terminal and is waiting on single keys. The command box sends
  // whole lines and cannot answer that -- a `q` typed there arrives as "q\n"
  // and the operator is stuck with no way out -- so it steps aside and says
  // where the keyboard went.
  const fullScreenProgram =
    terminalSessionId === active.id && !!terminalScreen.alternate;
  const tabs =
    actives.length > 1
      ? `<div class="dock-tabs">${actives
          .map(
            (s) =>
              `<button class="tab ${active.id === s.id ? "active" : ""}" data-term-tab="${escapeHTML(s.id)}">${escapeHTML(s.targetId)} <span class="muted mono">#${escapeHTML(s.id.slice(-4))}</span></button>`,
          )
          .join("")}</div>`
      : "";
  const body = !view.showsPane
    ? ""
    : `<div class="term-body">${tabs}<pre id="terminal-screen" class="terminal-screen term-output" data-i18n-skip ${terminalWritable ? 'tabindex="0"' : ""}>${terminalSessionId === active.id ? renderTerminal(terminalScreen, escapeHTML) : ""}</pre>${can("terminal", "create") ? `<div class="terminal-input${fullScreenProgram ? " held" : ""}"><span class="prompt">›</span><input id="managed-term-input" data-session-id="${escapeHTML(active.id)}" autocomplete="off" ${fullScreenProgram ? "disabled" : ""} placeholder="${fullScreenProgram ? "A full-screen program has the terminal — click the screen to use it" : "Click the screen to type, or enter a command here"}"></div>` : '<div class="term-dim">This identity has read-only terminal access.</div>'}<div class="term-dim dock-foot">Session ${escapeHTML(active.id)} · Approved by ${escapeHTML(active.approvedBy || "—")}</div></div>`;
  return `<div class="term-dock ${view.mode}"><div class="terminal"><div class="terminal-head"><span class="term-dots"><i></i><i></i><i></i></span><button class="dock-target" data-action="terminal-dock-toggle" title="${view.foldLabel}">${escapeHTML(active.targetId)}</button><span id="terminal-stream-status" class="${streamConnected ? "ok" : "warn"}">● Active · ${streamConnected ? "Connected" : "Connecting"}</span><span class="dock-tools">${view.showsPane ? `<button class="icon-btn" data-action="terminal-dock-size" title="${view.sizeLabel}" aria-label="${view.sizeLabel}"><span data-i18n-skip>${view.sizeGlyph}</span></button>` : ""}<button class="icon-btn" data-action="terminal-dock-toggle" title="${view.foldLabel}" aria-label="${view.foldLabel}"><span data-i18n-skip>${view.foldGlyph}</span></button><button class="icon-btn dock-close" data-action="close-terminal" data-session-id="${escapeHTML(active.id)}" title="Close session" aria-label="Close session"><span data-i18n-skip>×</span></button></span></div>${body}</div></div>`;
}

// Polls one operation until the agent finishes it or the deadline passes.
async function waitForOperation(id, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    try {
      const list = await api("/api/v1/operations");
      const operation = (list.items || []).find((o) => o.id === id);
      if (operation && ["succeeded", "failed"].includes(operation.status))
        return operation;
    } catch {
      return null;
    }
  }
  return null;
}

// Two views. The live stream is what the agent ships continuously — warning and
// worse, plus authentication activity — so evidence exists even for a node that
// went quiet. On-demand capture reads a wider window from the node itself, as an
// agent operation that is stored and audited like any other.
// The volume a host logs is low enough to stream, so the categories are views
// of what has already arrived rather than a request sent to the node. Only the
// journal read still goes to the host: it reaches debug lines the stream drops
// and history from before the agent started, which memory cannot hold.
// Two views, because there are two things to do with a log.
//
// The live view is what the node pushes as it happens, and it carries only
// access and kernel activity -- the senders worth interrupting someone for.
// The read asks the node directly for anything else, which on a working host
// is almost everything: 41,105 of 41,178 journal entries in twenty minutes
// were one container's access log.
//
// They used to be five tabs over one unfiltered stream, differing only by a
// filter on the sender. That made four of them show the same list whenever a
// host had no kernel or login activity to separate out, which is most of the
// time.
const LOG_SOURCES = [
  ["live", "Live"],
  ["read", "Read from node"],
];

// Mirrors the agent's authIdentifiers, recorded in
// docs/contracts/agent-server.json and asserted against the agent there.
const AUTH_UNITS = new Set([
  "sshd", "sudo", "su", "login", "systemd-logind", "polkitd",
  "gdm-password", "sshd-session", "audit", "auditd", "useradd", "usermod", "passwd",
  "groupadd", "groupmod", "groupdel", "userdel", "chfn", "chsh", "newgrp",
]);

// Nothing in a category is a fact about the window, not a failure, so each says
// what was quiet rather than repeating one generic line.
const LOG_EMPTY = {
  live: "No access or kernel activity in this window.",
};

// The live view shows what arrived, which the agent has already narrowed to
// access and the kernel. Nothing is filtered again here.
const LOG_SCOPES = {
  live: () => true,
};

// What a read may ask a node for, mirroring logCaptureSources and
// logCapturePriorities in docs/contracts/agent-server.json.
//
// "Everything" is the honest default and almost never what is wanted: it
// answers with an application's traffic and buries the host in it. The host
// and container halves are separable because container output carries no
// syslog facility, and conmon is the monitor all of it passes through.
const LOG_READ_SOURCES = [
  ["host", "This host"],
  ["container", "Containers"],
  ["auth", "Logins and sudo"],
  ["kernel", "Kernel"],
  ["journal", "Everything"],
];

// The live view carries named senders rather than a severity range, so a
// severity band is something only a read can select on.
const LOG_READ_BANDS = [
  ["", "Any severity"],
  ["error", "Errors"],
  ["warning", "Warnings and worse"],
  ["routine", "Below warning"],
  ["notice", "Notice"],
  ["info", "Info"],
  ["debug", "Debug"],
];

// Syslog priorities, as the agent reports them.
// Display names for the syslog priorities. The journal's own abbreviations read
// as jargon and have no Korean form; these are what both log tables show.
const PRIORITY_LABELS = [
  "Emergency", "Alert", "Critical", "Error",
  "Warning", "Notice", "Info", "Debug",
];

const LOG_PRIORITIES = [
  ["emerg", 0],
  ["alert", 1],
  ["crit", 2],
  ["err", 3],
  ["warning", 4],
  ["notice", 5],
  ["info", 6],
  ["debug", 7],
];

// Streamed lines carry a priority, so severity is read rather than guessed.
const LIVE_LEVELS = [
  ["all", "All lines", () => true],
  ["error", "Errors", (line) => line.priority <= 3],
  ["warn", "Warnings", (line) => line.priority === 4],
  ["auth", "Access", (line) => /^(sshd|sudo|su|login|systemd-logind|polkitd)/.test(line.unit || "")],
];

// A captured line carries no priority field, so severity is read from its text.
//
// A logger states its own level as a bare uppercase word, and that beats any
// reading of the message: `WARN ... error="connection refused"` is a warning
// the logger already graded, and the words inside its own message must not
// promote it. `error=` is a structured field name, not a level, so a level word
// followed by `=` is not one.
const EXPLICIT_LEVEL =
  /\b(EMERG|ALERT|CRIT|FATAL|PANIC|ERROR|ERR|WARN|WARNING|NOTICE|INFO|DEBUG)\b(?!=)/;
const LEVEL_SEVERITY = {
  EMERG: "error", ALERT: "error", CRIT: "error", FATAL: "error", PANIC: "error",
  ERROR: "error", ERR: "error", WARN: "warn", WARNING: "warn",
  NOTICE: "info", INFO: "info", DEBUG: "info",
};

// Lines from tools that log no level at all -- sshd, sudo, the kernel -- are
// graded on what they say instead. Tests run in order and stop at the first
// match, so a line counts once.
const CAPTURE_SEVERITY = [
  ["error", /\b(fatal|panic|segfault|oom-kill|failed|failure|denied|refused|timed out|unreachable|cannot|unable to)\b/i],
  ["warn", /\b(warn|warning|deprecated?|retrying|throttl)\b/i],
];

function captureSeverity(raw) {
  const level = EXPLICIT_LEVEL.exec(raw);
  if (level) return LEVEL_SEVERITY[level[1].toUpperCase()];
  return CAPTURE_SEVERITY.find(([, pattern]) => pattern.test(raw))?.[0] || "info";
}

// journald's default syslog format:
//   2026-09-10T10:28:22+00:00 xeon1 kloudview-agent[4075458]: message
// The RFC 3164 form some files still use puts a bare "Sep 10 10:28:22" first.
// Anything that matches neither is kept whole as the message, so no line is
// dropped for being unparseable.
const SYSLOG_LINE =
  /^(\S+T\S+|\w{3}\s+\d{1,2}\s[\d:]{8})\s+(\S+)\s+([^:[]+?)(?:\[(\d+)\])?:\s(.*)$/;

function parseCaptureLine(raw) {
  const match = SYSLOG_LINE.exec(raw);
  const severity = captureSeverity(raw);
  const access = /\b(sshd|sudo|su|login|systemd-logind|polkitd|useradd|passwd)\b/i.test(
    match ? match[3] : raw,
  );
  if (!match) return { at: "", host: "", unit: "", pid: "", message: raw, severity, access, raw };
  return {
    at: match[1],
    host: match[2],
    unit: match[3].trim(),
    pid: match[4] || "",
    message: match[5],
    severity,
    access,
    raw,
  };
}

const LOG_LEVELS = [
  ["all", "All lines", () => true],
  ["error", "Errors", (line) => line.severity === "error"],
  ["warn", "Warnings", (line) => line.severity === "warn"],
  ["auth", "Access", (line) => line.access],
];

// The live tab: severity counts across every priority, and the lines the agent
// judged worth keeping.
// A quiet live view is the normal state of a healthy host, not a broken page.
//
// It carries access and kernel activity only, so on most hosts it is empty for
// hours at a time while the journal fills with an application's traffic. The
// counters already measure that at no transfer cost, so saying how much is
// waiting turns "nothing here" from a worry into a measurement -- and points
// at the read that can fetch it.
function waitingNote(totals, containerTotal) {
  const host = Object.values(totals || {}).reduce(
    (sum, n) => sum + Number(n || 0),
    0,
  );
  if (!host && !containerTotal) return "";
  // Two numbers, because there are two reads. One combined figure would be a
  // count nobody can ask the node to reproduce.
  const parts = [];
  if (host) parts.push(`<b class="mono">${host}</b> from this host`);
  if (containerTotal) parts.push(`<b class="mono">${containerTotal}</b> from its containers`);
  return `<div class="term-dim" style="margin-top:8px">Logins and kernel activity stream here. In the same window the node logged ${parts.join(" and ")} — read either from the node.</div>`;
}

function liveLogsSection(nodes, target, level, query, scope) {
  const inScope = LOG_SCOPES[scope] || LOG_SCOPES.live;
  const counters = state.liveLogCounters.filter(
    (window) => !target || window.nodeId === target,
  );
  const totals = {};
  let containerTotal = 0;
  let dropped = 0;
  for (const window of counters) {
    for (const [name, count] of Object.entries(window.counts || {}))
      totals[name] = (totals[name] || 0) + count;
    // Counted apart from the host's because it is read apart. One combined
    // number is dominated by whichever application talks most and answers to
    // no read anyone can make.
    for (const count of Object.values(window.containers || {}))
      containerTotal += Number(count || 0);
    dropped += window.dropped || 0;
  }
  // The window the server actually holds. Streamed logs live in memory only,
  // so a restart starts it over; asking for 24 hours does not make the data
  // older than the process, and the selector alone implied it did.
  const windowStart = counters.reduce(
    (earliest, window) =>
      !earliest || window.from < earliest ? window.from : earliest,
    "",
  );
  const lines = state.liveLogLines.filter(
    (line) => (!target || line.nodeId === target) && inScope(line),
  );
  const matcher = LIVE_LEVELS.find(([key]) => key === level)?.[2] || (() => true);
  const shown = lines.filter(
    (line) =>
      matcher(line) &&
      (!query ||
        (line.message + " " + line.unit).toLowerCase().includes(query)),
  );
  const count = (key) =>
    lines.filter(LIVE_LEVELS.find(([k]) => k === key)?.[2] || (() => true)).length;
  const tile = (key, label, sub, tone) =>
    `<button class="kpi-filter ${level === key ? "active" : ""}" data-log-level="${key}">${kpi(label, String(count(key)), sub, tone)}</button>`;

  // Counts cover every severity, including the ones never shipped as lines, so
  // "is this normal" has a denominator.
  const volume =
    scope !== "live"
      ? ""
      : LOG_PRIORITIES.filter(([name, priority]) => totals[name] && PRIORITY_LABELS[priority])
          .map(
            ([name, priority]) =>
              `<span class="tag-chip"><b class="mono">${totals[name]}</b> ${escapeHTML(t(PRIORITY_LABELS[priority]))}</span>`,
          )
          .join("") +
        (containerTotal
          ? `<span class="tag-chip"><b class="mono">${containerTotal}</b> from containers</span>`
          : "");

  // Paged to the viewport like every other list.
  const logPage = pagedList(shown, "logs");
  const body = shown.length
    ? `<div class="table-wrap"><table class="table compact"><thead><tr><th>Time</th><th>Node</th><th>Unit</th><th>Severity</th><th>Message</th></tr></thead><tbody data-i18n-skip>${logPage.slice
        .map(
          (line) =>
            `<tr><td class="mono muted">${formatWhen(line.at)}</td><td class="mono">${escapeHTML(resourceName(line.nodeId))}</td><td class="mono">${escapeHTML(line.unit || "—")}</td><td class="${line.priority <= 3 ? "critical" : "warn"}"><i class="dot"></i>${escapeHTML(t(PRIORITY_LABELS[line.priority] || String(line.priority)))}</td>${/* Not .prose: wrapping nearly doubled row height, and scanning density is
      what a log view is for. The message is the last column, so it keeps one
      line and the wrap scrolls to the rest of it. */ ""}<td title="${escapeHTML(line.message)}">${escapeHTML(line.message)}${line.repeat ? ` <span class="tag-chip">×${line.repeat + 1}</span>` : ""}</td></tr>`,
        )
        .join("")}</tbody></table></div>${logPage.bar}`
    : `<div class="empty">${LOG_EMPTY[scope] || LOG_EMPTY.live}${waitingNote(totals, containerTotal)}</div>`;

  const controls = `<div class="filterbar"><select id="log-target"><option value="">All nodes</option>${nodes
    .map(
      (n) =>
        `<option value="${escapeHTML(n.id)}" ${n.id === target ? "selected" : ""} data-i18n-skip>${escapeHTML(n.name)}</option>`,
    )
    .join("")}</select><select id="log-window"><option value="60" selected>Last hour</option><option value="360">Last 6 hours</option><option value="1440">Last 24 hours</option></select><input id="log-filter" placeholder="Filter lines…" value="${escapeHTML(state.logQuery || "")}">${/* The pager already carries the matched total; this only adds a fact when a
     filter has narrowed the window. */ ""}${shown.length < lines.length ? `<span class="mono muted">${shown.length} of ${lines.length} match</span>` : ""}</div>`;

  // Counters cover every severity; only warnings and worse, plus login
  // activity, are shipped as lines. Stated, so the chips below are not read as
  // lines being withheld.
  const debug = totals.debug || 0;
  const note =
    debug > 0
      ? `<div class="page-sub" style="margin:-4px 0 12px">Every severity but debug streams in as it happens. The ${debug} debug entries in this window are counted only — reach them with a journal read.${windowStart ? ` Held since ${new Date(windowStart).toLocaleString()}.` : ""}</div>`
      : "";
  return (
    `<div class="grid kpis">${tile("all", "LINES", "Streamed as they happen")}${tile("error", "ERRORS", "Error and worse", count("error") ? "critical" : "calm")}${tile("warn", "WARNINGS", "Warnings", count("warn") ? "warn" : "calm")}${tile("auth", "ACCESS", "Sessions and sudo")}</div>` +
    note +
    card(
      LOG_SOURCES.find(([key]) => key === scope)?.[1] || "Live",
      `${controls}<div class="term-body">${body}</div>`,
      `${volume}${dropped ? `<span class="tag-chip warn">${dropped} dropped</span>` : ""}`,
    )
  );
}

function logsPage() {
  const head = pageHead(
    "Logs",
    "Logins and kernel activity stream in as they happen; everything else waits on the node for a read",
  );
  const nodes = state.liveResources.filter(
    (r) => r.agentId && ["node", "hypervisor"].includes(r.type),
  );
  if (!nodes.length)
    return (
      head +
      `<div class="card"><div class="empty">No agent-managed node to read from</div></div>`
    );
  const source = LOG_SOURCES.some(([key]) => key === state.logSource)
    ? state.logSource
    : "live";
  const level = state.logLevel || "all";
  const query = (state.logQuery || "").trim().toLowerCase();
  const tabs = `<div class="tabbar">${LOG_SOURCES.map(
    ([key, label]) =>
      `<button class="tab ${key === source ? "active" : ""}" data-log-source="${key}">${label}</button>`,
  ).join("")}</div>`;
  // The live view spans the fleet, so an unset target means every node.
  if (LOG_SCOPES[source]) {
    const target = nodes.some((n) => n.id === state.logTarget)
      ? state.logTarget
      : "";
    return head + tabs + liveLogsSection(nodes, target, level, query, source);
  }
  const target = nodes.some((n) => n.id === state.logTarget)
    ? state.logTarget
    : nodes[0].id;

  const capture = state.logCapture;
  const lines = (capture?.text || "")
    .split("\n")
    .filter(Boolean)
    .map(parseCaptureLine);
  const matcher = LOG_LEVELS.find(([key]) => key === level)?.[2] || (() => true);
  const shown = lines.filter(
    (line) => matcher(line) && (!query || line.raw.toLowerCase().includes(query)),
  );
  const count = (key) => {
    const test = LOG_LEVELS.find(([k]) => k === key)?.[2] || (() => true);
    return lines.filter(test).length;
  };

  const tile = (key, label, sub, tone) =>
    `<button class="kpi-filter ${level === key ? "active" : ""}" data-log-level="${key}">${kpi(label, String(count(key)), sub, tone)}</button>`;

  const controls = `<div class="filterbar"><select id="log-target">${nodes
    .map(
      (n) =>
        `<option value="${escapeHTML(n.id)}" ${n.id === target ? "selected" : ""} data-i18n-skip>${escapeHTML(n.name)}</option>`,
    )
    .join("")}</select><select id="log-window"><option value="30">Last 30 minutes</option><option value="120" selected>Last 2 hours</option><option value="1440">Last 24 hours</option></select><select id="log-read-source">${LOG_READ_SOURCES.map(([key, label]) => `<option value="${key}" ${key === (state.logReadSource || "host") ? "selected" : ""}>${label}</option>`).join("")}</select><select id="log-band">${LOG_READ_BANDS.map(([key, label]) => `<option value="${key}" ${key === (state.logBand || "") ? "selected" : ""}>${label}</option>`).join("")}</select><button class="btn btn-primary" data-action="capture-logs">Read logs</button><input id="log-filter" placeholder="Filter lines…" value="${escapeHTML(state.logQuery || "")}">${shown.length < lines.length ? `<span class="mono muted">${shown.length} of ${lines.length} match</span>` : ""}</div>`;

  const capturePage = pagedList(shown, "logs");
  const body = capture?.pending
    ? '<div class="empty">Waiting for the agent to answer…</div>'
    : capture?.error
      ? `<div class="warning-box">${escapeHTML(capture.error)}</div>`
      : !lines.length
        ? '<div class="empty">Choose a node and a window, then read the logs</div>'
        : !shown.length
          ? '<div class="empty">No line matches the current filter</div>'
          : `<div class="table-wrap"><table class="table compact"><thead><tr><th class="pick-col"></th><th>Time</th><th>Host</th><th>Unit</th><th>Severity</th><th>Message</th></tr></thead><tbody data-i18n-skip>${capturePage.slice
              .map(
                (line) =>
                  `<tr class="${state.logPicked.includes(line.raw) ? "picked" : ""}"><td class="pick-col"><input type="checkbox" data-log-pick="${escapeHTML(line.raw)}" ${state.logPicked.includes(line.raw) ? "checked" : ""}></td><td class="mono muted">${escapeHTML(formatCaptureTime(line.at))}</td><td class="mono">${escapeHTML(line.host || "—")}</td><td class="mono">${escapeHTML(line.unit || "—")}${line.pid ? `<span class="muted">[${escapeHTML(line.pid)}]</span>` : ""}</td><td class="${line.severity === "error" ? "critical" : line.severity === "warn" ? "warn" : "muted"}"><i class="dot"></i>${escapeHTML(t(SEVERITY_LABELS[line.severity]))}</td><td class="log-message" title="${escapeHTML(line.message)}">${escapeHTML(line.message)}</td></tr>`,
              )
              .join("")}</tbody></table></div>${capturePage.bar}`;

  // Picking lines out of a read is the point of reading it. A read's own
  // output is let go after half an hour; the lines that explain an outage
  // belong to the incident, so they are copied into it rather than linked.
  const openIncidents = state.liveIncidents.filter((x) => x.status !== "resolved");
  const attachBar = state.logPicked.length
    ? `<div class="filterbar attach-bar"><span><b class="mono">${state.logPicked.length}</b> lines selected</span>${
        openIncidents.length
          ? `<select id="log-attach-incident">${openIncidents
              .map(
                (incident) =>
                  `<option value="${escapeHTML(incident.id)}" data-i18n-skip>${escapeHTML(incident.title)}</option>`,
              )
              .join("")}</select><button class="btn btn-primary" data-action="attach-log-lines">Attach to incident</button>`
          : `<span class="muted">Declare an incident to attach them to</span>`
      }<button class="btn" data-action="clear-log-picks">Clear</button></div>`
    : "";

  // Tabs choose the view and the tiles summarise it, so the tiles sit below
  // them here as they do on the live tab.
  return (
    head +
    tabs +
    `<div class="grid kpis">${tile("all", "LINES", "Read from the node")}${tile("error", "ERRORS", "Failures and denials", count("error") ? "critical" : "calm")}${tile("warn", "WARNINGS", "Warnings", count("warn") ? "warn" : "calm")}${tile("auth", "ACCESS", "Sessions and sudo")}</div>` +
    card(
      capture
        ? `${LOG_READ_SOURCES.find(([key]) => key === state.logReadSource)?.[1] || "Read"} · ${resourceName(target)}`
        : "Logs",
      `${controls}${attachBar}<div class="term-body">${body}</div>`,
      capture?.capturedAt ? `<span class="muted">${formatWhen(capture.capturedAt)}</span>` : "",
    )
  );
}

function auditPage() {
  let data = state.liveAudit,
    pageStart = data.length ? state.auditOffset + 1 : 0,
    pageEnd = state.auditOffset + data.length;
  return (
    pageHead(
      "Audit log",
      "Record of every change across the API, agents, approvals, and terminals",
      `<button class="btn" data-action="export">${icon("download")}Export page</button><button class="btn" data-action="refresh-data">Refresh</button>`,
    ) +
    `<div class="card"><div class="table-wrap"><table class="table"><thead><tr><th>Time</th><th>Actor</th><th>Action</th><th>Target</th><th>Result</th><th>Source</th></tr></thead><tbody>${data.map((e) => `<tr><td class="mono muted">${formatWhen(e.timestamp)}</td><td>${escapeHTML(e.actor)}</td><td class="mono">${escapeHTML(e.action)}</td><td class="mono">${escapeHTML(e.target)}</td><td class="${e.result === "succeeded" ? "ok" : "critical"}"><i class="dot"></i>${escapeHTML(e.result)}</td><td class="mono muted">${escapeHTML(e.sourceIp || "—")}</td></tr>`).join("")}</tbody></table></div><div class="pagination"><button class="btn btn-sm" data-audit-page="previous" ${state.auditOffset === 0 ? "disabled" : ""}>Previous</button><span class="mono muted">${pageStart}–${pageEnd} of ${state.auditTotal}</span><button class="btn btn-sm" data-audit-page="next" ${state.auditOffset + state.auditPageSize >= state.auditTotal ? "disabled" : ""}>Next</button></div></div>`
  );
}

function usersPage() {
  const query = (state.userQuery || "").trim().toLowerCase();
  const all = state.liveUsers || [];
  const status = state.userStatus || "all";
  const users = all.filter(
    (u) =>
      (status === "all" || u.status === status) &&
      (!query ||
        `${u.username} ${u.displayName || ""}`.toLowerCase().includes(query)),
  );
  const count = (key) => all.filter((u) => u.status === key).length;
  const tile = (key, label, value, sub, tone) =>
    `<button class="kpi-filter ${status === key ? "active" : ""}" data-user-status="${key}">${kpi(label, value, sub, tone)}</button>`;
  const kpis = `<div class="grid kpis">${tile("all", "ACCOUNTS", String(all.length), "Local accounts")}${tile("active", "ACTIVE", String(count("active")), "Can sign in", "ok")}${tile("disabled", "DISABLED", String(count("disabled")), "Sign-in blocked", count("disabled") ? "warn" : "calm")}${kpi("TEAMS", String((state.liveTeams || []).length), "Groupings for bindings")}</div>`;
  return (
    pageHead(
      "Users",
      "Manage local accounts and access status",
      `<button class="btn btn-primary" data-action="create-user">+ Add user</button>`,
    ) +
    kpis +
    card(
      "Accounts",
      `<div class="filterbar"><input id="user-filter" placeholder="Filter users…" value="${escapeHTML(state.userQuery || "")}">${users.length !== all.length ? `<span class="mono muted">${users.length} of ${all.length}</span>` : ""}</div><div class="table-wrap"><table class="table"><thead><tr><th>Username</th><th>Display name</th><th>Status</th><th>Created</th><th>Actions</th></tr></thead><tbody>${
        users.length
          ? users
              .map(
                (u) =>
                  `<tr><td class="mono">${escapeHTML(u.username)}</td><td>${escapeHTML(u.displayName || "")}</td><td class="${u.status === "active" ? "ok" : "muted"}"><i class="dot"></i>${u.status}</td><td class="mono muted">${new Date(u.createdAt).toLocaleDateString()}</td><td><button class="btn btn-sm" data-action="edit-user" data-user-id="${u.id}">Edit</button> <button class="btn btn-sm btn-danger" data-action="delete-user" data-user-id="${u.id}">Delete</button></td></tr>`,
              )
              .join("")
          : '<tr><td colspan="5"><div class="empty">No users</div></td></tr>'
      }</tbody></table></div>`,
    )
  );
}

function teamFields(team = {}) {
  let members = new Set(team.memberIds || []);
  return `<div class="form-row"><label>NAME</label><input id="team-name" value="${escapeHTML(team.name || "")}" placeholder="Platform Team"></div><div class="form-row"><label>DESCRIPTION</label><input id="team-desc" value="${escapeHTML(team.description || "")}" placeholder="Owns platform services"></div><div class="form-row"><label>MEMBERS</label><select id="team-members" multiple size="6">${(state.liveUsers || []).map((u) => `<option value="${u.id}" ${members.has(u.id) ? "selected" : ""}>${escapeHTML(u.displayName || u.username)}</option>`).join("")}</select></div>`;
}

function teamPayload() {
  return {
    name: $("#team-name").value,
    description: $("#team-desc").value,
    memberIds: [...$("#team-members").selectedOptions].map((o) => o.value),
  };
}

function teamsPage() {
  let teams = state.liveTeams || [];
  let userName = new Map(
    (state.liveUsers || []).map((u) => [u.id, u.displayName || u.username]),
  );
  return (
    pageHead(
      "Teams",
      "Group users for ownership and organization",
      `<button class="btn btn-primary" data-action="create-team">+ Add team</button>`,
    ) +
    card(
      "Teams",
      `<div class="table-wrap"><table class="table"><thead><tr><th>Name</th><th>Description</th><th>Members</th><th>Actions</th></tr></thead><tbody>${
        teams.length
          ? teams
              .map(
                (t) =>
                  `<tr><td>${escapeHTML(t.name)}</td><td class="muted">${escapeHTML(t.description || "")}</td><td>${(t.memberIds || []).length ? (t.memberIds || []).map((id) => escapeHTML(userName.get(id) || id)).join(", ") : '<span class="muted">—</span>'}</td><td><button class="btn btn-sm" data-action="edit-team" data-team-id="${t.id}">Edit</button> <button class="btn btn-sm btn-danger" data-action="delete-team" data-team-id="${t.id}">Delete</button></td></tr>`,
              )
              .join("")
          : '<tr><td colspan="4"><div class="empty">No teams</div></td></tr>'
      }</tbody></table></div>`,
    )
  );
}

// Fallback for an unrecognized route; every real page routes to its own view.
function generic() {
  return (
    pageHead("Page not found", "This view is unavailable") +
    card(
      "Page not found",
      `<div class="card-body"><div class="empty">This view is unavailable. Use the sidebar to navigate.</div></div>`,
    )
  );
}

// What a URL has to carry for a view to survive a reload or reach a colleague.
//
// A filter is part of what someone is looking at: "the critical alerts" and
// "all eight alerts" are different screens. These were held only in memory, so
// a refresh silently widened the view under the operator and a pasted link
// showed the recipient something else.
const ROUTE_FIELDS = [
  "page",
  "selectedResourceId",
  "selectedResource",
  "selectedAgentId",
  "selectedIncidentId",
  "detailTab",
  "query",
  "resourceType",
  "resourceHealth",
  "resourceLifecycle",
  "metricRange",
  "resourceOffset",
  "alertFilter",
  "incidentFilter",
  "logSource",
  "utilTab",
];

// The ones that travel in the query string, and the state key each maps to.
// Defaults are left out of the URL so an unfiltered view stays a clean link.
const ROUTE_QUERY = [
  ["alerts", "alertFilter", "all"],
  ["incidents", "incidentFilter", "all"],
  ["logs", "logSource", "live"],
  ["util", "utilTab", "nodes"],
];
function routeSnapshot() {
  const snap = {};
  for (const key of ROUTE_FIELDS) snap[key] = state[key];
  return snap;
}
function applyRoute(snap) {
  for (const key of ROUTE_FIELDS) state[key] = snap[key];
}
// Route as a URL path; the server falls back to index.html for these.
function routeURL() {
  const tab =
    state.detailTab && state.detailTab !== "overview"
      ? `?tab=${encodeURIComponent(state.detailTab)}`
      : "";
  if (state.selectedResourceId)
    return `/resources/${encodeURIComponent(state.selectedResourceId)}${tab}`;
  if (state.selectedAgentId)
    return `/agents/${encodeURIComponent(state.selectedAgentId)}`;
  if (state.selectedIncidentId)
    return `/incidents/${encodeURIComponent(state.selectedIncidentId)}`;
  // The list and a detail share one section, so /resources is the list URL and
  // /resources/{id} its detail; the internal page key differs.
  const path =
    state.page === "infrastructure"
      ? "/resources"
      : state.page === "overview"
        ? "/"
        : `/${state.page}`;
  const query = new URLSearchParams();
  for (const [param, key, fallback] of ROUTE_QUERY) {
    const value = state[key];
    if (value && value !== fallback) query.set(param, value);
  }
  if (state.page === "infrastructure" && state.query) query.set("q", state.query);
  const search = query.toString();
  return search ? `${path}?${search}` : path;
}

// URL sync for in-page moves; navTo() is what pushes a history entry.
function syncURL() {
  const url = routeURL();
  if (location.pathname + location.search === url) return;
  try {
    history.replaceState(history.state, "", url);
  } catch {}
}

// State from the current URL, for first load and unrecognized history entries.
async function applyLocation() {
  const [, section, id] = location.pathname.split("/");
  const params = new URLSearchParams(location.search);
  const tab = params.get("tab");
  // A filter in the link is part of the view the sender was looking at.
  for (const [param, key, fallback] of ROUTE_QUERY) {
    state[key] = params.get(param) || fallback;
  }
  if (params.has("q")) state.query = params.get("q");
  state.selectedResourceId = null;
  state.selectedResource = null;
  state.selectedAgentId = null;
  state.selectedIncidentId = null;
  if (!section) {
    state.page = "overview";
    return;
  }
  if (section === "resources" && !id) {
    state.page = "infrastructure";
    return;
  }
  if (section === "resources" && id) {
    try {
      const resource = await api(`/api/v1/resources/${encodeURIComponent(id)}`);
      state.selectedResourceId = id;
      state.selectedResource = resource;
      state.detailTab = tab || "overview";
      await loadResourceMetrics(id);
    } catch {
      state.page = "infrastructure";
    }
    return;
  }
  if (section === "agents" && id) {
    state.selectedAgentId = id;
    return;
  }
  if (section === "incidents" && id) {
    // Fetched rather than looked up in the list. A shared link arrives before
    // any list has loaded, and a page that could only find an incident among
    // the ones already fetched dropped the reader on the dashboard.
    try {
      const incident = await api(`/api/v1/incidents/${encodeURIComponent(id)}`);
      if (!state.liveIncidents.some((x) => x.id === incident.id)) {
        state.liveIncidents = [incident, ...state.liveIncidents];
      }
      state.selectedIncidentId = id;
      await loadIncidentTimeline(id);
    } catch {
      state.page = "incidents";
    }
    return;
  }
  state.page = section;
}

// The timeline merges what people wrote into the incident with what was
// actually done to its resources while it was open. The second half is derived
// by the server at read time, so it also covers the attempts made before anyone
// declared the incident.
async function loadIncidentTimeline(id) {
  try {
    state.incidentEvents =
      (await api(`/api/v1/incidents/${encodeURIComponent(id)}/timeline`))
        .items || [];
  } catch {
    state.incidentEvents = [];
  }
}

// Forward navigation: snapshot the route, apply changes, push history.
function navTo(mutate) {
  state.navStack.push(routeSnapshot());
  mutate();
  try {
    history.pushState({ depth: state.navStack.length }, "", routeURL());
  } catch {}
  render();
}
// Previous route; used by browser Back and the in-app Back button.
function navBack() {
  if (!state.navStack.length) return;
  applyRoute(state.navStack.pop());
  render();
  syncURL();
}
window.addEventListener("popstate", async (event) => {
  // navTo entries carry a depth; anything else is read off the location.
  if (event.state?.depth && state.navStack.length) {
    navBack();
    return;
  }
  await applyLocation();
  render();
});

// Which page the console is showing. Split out of render() so that a page
// that throws can be caught: the expression used to sit in render() itself,
// where a failure meant setHTML was never reached and the operator was left
// with an empty document -- no sidebar, no message, nothing to click.
function pageContent() {
  return state.selectedIncidentId
    ? incidentDetailPage()
    : state.selectedAgentId
      ? agentInventoryPage()
      : state.selectedResourceId
        ? liveResourceDetailPage()
        : state.page === "overview"
          ? overview()
            : state.page === "infra-map"
              ? infraMapPage()
            : state.page === "infrastructure"
              ? infrastructure()
                : state.page === "utilization"
                  ? utilizationPage()
                  : state.page === "unmanaged"
                  ? unmanagedResourcesPage()
                  : state.page === "groups"
                    ? groupsPage()
                  : state.page === "dynamic-groups"
                    ? groupsPage("dynamic")
                    : state.page === "tags"
                      ? tagsPage()
                      : state.page === "alerts"
                        ? alertsPage()
                        : state.page === "alert-rules"
                          ? alertRulesPage()
                          : state.page === "incidents"
                            ? incidentsPage()
                            : state.page === "terminal"
                              ? managedTerminalPage()
                              : state.page === "notification-routing"
                                ? notificationRoutingPage()
                              : state.page === "fleet"
                                ? fleetPage()
                                  : state.page === "jobs"
                                    ? operationsPage()
                                    : state.page === "runbooks"
                                      ? runbooksPage()
                                      : state.page === "roles"
                                        ? rolesPage()
                                        : state.page === "scopes"
                                          ? scopesPage()
                                          : state.page === "bindings"
                                            ? bindingsPage()
                                            : state.page === "logs"
                                              ? logsPage()
                                              : state.page === "audit"
                                              ? auditPage()
                                              : state.page === "users"
                                                ? usersPage()
                                                : state.page === "teams"
                                                  ? teamsPage()
                                                  : generic(state.page);
}

function render() {
  let content;
  try {
    content = pageContent();
  } catch (error) {
    content = renderFailure(error);
  }
  content = sectionTabBar() + content;
  // Scroll survives in-place re-render, resets on navigation.
  let contentEl = $(".content");
  let scrollTop = contentEl && renderState.page === state.page ? contentEl.scrollTop : 0;
  try {
    setHTML($("#app"), renderShell(content, state, nav) + terminalDockPanel());
  } catch (error) {
    // The shell or the docked terminal failed rather than the page. Draw the
    // plain shell so the sidebar is still there to navigate away with.
    setHTML($("#app"), renderShell(renderFailure(error), state, nav));
  }
  try {
    bind();
  } catch (error) {
    console.error("KloudView: binding the page failed", error);
  }
  let restored = $(".content");
  if (restored) restored.scrollTop = scrollTop;
  renderState.page = state.page;
  syncURL();
  applyMeasuredPageSize();
  // Not `page === "terminal"` any more: the panel is on every page, so the
  // session has to be connected on every page too.
  if (state.auth?.authenticated) connectTerminalStream();
}
// What the operator sees when a page cannot be drawn.
//
// A console that goes blank says nothing about whether the server is down,
// the session expired, or one view has a bug -- and with the shell gone there
// is no way to move to a page that still works. So the failure is reported
// where the page would have been, and everything around it stays usable.
function renderFailure(error) {
  console.error("KloudView: rendering this page failed", error);
  return failureCard(error);
}

const renderState = { page: null };

// In-page tabs for multi-page sections.
function sectionTabBar() {
  const section = nav.find(
    (item) => item.tabs && item.tabs.some((t) => t[0] === state.page),
  );
  if (!section || section.tabs.length < 2) return "";
  return `<div class="tabbar">${section.tabs
    .map(
      ([page, tabLabel]) =>
        `<button class="tab ${state.page === page ? "active" : ""}" data-page="${page}">${tabLabel}</button>`,
    )
    .join("")}</div>`;
}

// opts.wide: wider dialog for tables and terminal output.
// opts.viewer: read-only dialog, no Cancel button.
// The top bar advertises "⌘ K" beside the search field, so the shortcut has to
// exist. Bound once: the wiring around it re-runs on every render.
let searchShortcutBound = false;
function bindSearchShortcut() {
  if (searchShortcutBound) return;
  searchShortcutBound = true;
  document.addEventListener("keydown", (event) => {
    if (event.key !== "k" || !(event.metaKey || event.ctrlKey)) return;
    // A dialog owns the keyboard while it is open.
    if ($("#modal-root .modal")) return;
    const field = $("#global-search");
    if (!field) return;
    event.preventDefault();
    field.focus();
    field.select();
  });
}

// The element that raised the open dialog, so focus can return to it.
let modalOpener = null;

// Dismisses the dialog and undoes what opening it changed: the key handler and
// the caller's place in the tab order.
function closeModal() {
  const root = $("#modal-root");
  if (!root.innerHTML) return;
  root.innerHTML = "";
  document.removeEventListener("keydown", modalKeydown, true);
  const opener = modalOpener;
  modalOpener = null;
  if (opener && document.contains(opener)) opener.focus();
}

// Escape dismisses. Tab cycles inside the dialog rather than walking the page
// behind it, which is otherwise still reachable and still operable.
function modalKeydown(event) {
  const dialog = $("#modal-root .modal");
  if (!dialog) return;
  if (event.key === "Escape") {
    event.preventDefault();
    closeModal();
    return;
  }
  if (event.key !== "Tab") return;
  const stops = [...dialog.querySelectorAll(FOCUSABLE)].filter(
    (el) => !el.disabled && el.offsetParent !== null,
  );
  if (!stops.length) return;
  const first = stops[0],
    last = stops[stops.length - 1];
  if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  } else if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  }
}

const FOCUSABLE =
  'input:not([type="hidden"]), select, textarea, button, a[href], [tabindex]:not([tabindex="-1"])';

function modal(
  title,
  body,
  confirm = "Confirm",
  danger = false,
  onConfirm = null,
  opts = {},
) {
  const help = FORM_HELP[opts.help];
  setHTML(
    $("#modal-root"),
    `<div class="modal-backdrop"><div class="modal${opts.wide ? " modal-wide" : ""}" role="dialog" aria-modal="true" aria-labelledby="modal-title"><div class="modal-head"><h3 id="modal-title">${title}</h3>${help ? `<button class="help-toggle" data-help-toggle title="What is this?" aria-label="What is this?">?</button>` : ""}<button class="icon-btn" data-close style="margin-left:auto">×</button></div><div class="modal-body">${help ? `<div class="help-panel" id="form-help" hidden><b>${escapeHTML(help.title)}</b><ul>${help.points.map((point) => `<li>${escapeHTML(point)}</li>`).join("")}</ul></div>` : ""}${body}</div><div class="modal-foot">${opts.viewer ? "" : '<button class="btn" data-close>Cancel</button>'}<button class="btn ${danger ? "btn-danger" : "btn-primary"}" data-confirm>${confirm}</button></div></div></div>`,
  );
  modalOpener = document.activeElement;
  document.addEventListener("keydown", modalKeydown, true);
  document.querySelectorAll("[data-close]").forEach((b) => (b.onclick = closeModal));
  // Focus starts on the first field so the dialog is usable from the keyboard
  // without tabbing through the page it opened over. A viewer has nothing to
  // fill in, so its close button takes the focus instead.
  const firstField = opts.viewer
    ? null
    : [...($("#modal-root .modal-body")?.querySelectorAll(FOCUSABLE) || [])].find(
        // A disabled or hidden field silently refuses focus, leaving it on the
        // page behind the dialog.
        (el) => !el.disabled && el.offsetParent !== null,
      );
  (firstField || $("#modal-root [data-close]"))?.focus();
  // Threshold unit follows the metric: a percentage for cpu/memory/disk, a
  // byte-rate selector for the network metrics.
  const metricSelect = $("#rule-metric");
  if (metricSelect) {
    const applyUnit = () => {
      const spec = METRIC_UNITS[metricSelect.value] || METRIC_UNITS.cpu;
      const rate = spec.unit === "rate";
      $("#rule-threshold-unit").style.display = rate ? "" : "none";
      $("#rule-threshold-unit-text").style.display = rate ? "none" : "";
      // A counter is a count, so it carries neither a percent sign nor a
      // ceiling of 100.
      $("#rule-threshold-unit-text").textContent = spec.unit === "count" ? "" : "%";
      $("#rule-threshold").max = spec.max ?? "";
      setHTML($("#rule-metric-hint"), spec.hint);
    };
    metricSelect.onchange = applyUnit;
    applyUnit();
  }
  // Option filters: rebuild the list from the full set, always keeping the
  // options already chosen so a filtered-out selection is never dropped.
  document.querySelectorAll("[data-option-filter]").forEach((input) => {
    const select = $(`#${input.dataset.optionFilter}`);
    if (!select) return;
    const all = [...select.options].map((option) => ({
      value: option.value,
      text: option.textContent,
    }));
    input.oninput = () => {
      const query = input.value.trim().toLowerCase();
      const chosen = new Set(
        [...select.selectedOptions].map((option) => option.value),
      );
      select.innerHTML = all
        .filter(
          (option) =>
            chosen.has(option.value) ||
            !query ||
            option.text.toLowerCase().includes(query),
        )
        .map(
          (option) =>
            `<option value="${escapeHTML(option.value)}" ${chosen.has(option.value) ? "selected" : ""}>${escapeHTML(option.text)}</option>`,
        )
        .join("");
    };
  });
  // Member picker: filter by text and type without rebuilding the list, so a
  // resource stays selected while the filter moves around it.
  const memberSearch = $("#member-search");
  if (memberSearch) {
    const options = [...document.querySelectorAll(".member-option")];
    const count = $("#member-count");
    let type = "";
    const apply = () => {
      const query = memberSearch.value.trim().toLowerCase();
      let shown = 0;
      for (const option of options) {
        const visible =
          (!type || option.dataset.memberType === type) &&
          (!query || option.dataset.memberText.includes(query));
        option.hidden = !visible;
        if (visible) shown += 1;
      }
      const selected = options.filter((o) => o.querySelector("input").checked).length;
      count.textContent = translateLive(
        `${shown} shown${selected ? ` · ${selected} selected` : ""}`,
      );
    };
    memberSearch.oninput = apply;
    document.querySelectorAll("[data-member-filter]").forEach((chip) => {
      chip.onclick = () => {
        type = chip.dataset.memberFilter;
        document
          .querySelectorAll("[data-member-filter]")
          .forEach((other) => other.classList.toggle("active", other === chip));
        apply();
      };
    });
    // A selection made under one filter has to stay visible in the count.
    document
      .querySelectorAll("[data-member-resource]")
      .forEach((box) => box.addEventListener("change", apply));
    apply();
  }

  // "Custom…" on a plain select swaps it for a text box carrying the same id,
  // so payload builders keep reading one field.
  document.querySelectorAll("[data-custom-select]").forEach((select) => {
    select.onchange = () => {
      if (select.value !== "__custom__") return;
      const input = document.createElement("input");
      input.id = select.id;
      select.removeAttribute("id");
      select.replaceWith(input);
      input.focus();
    };
  });
  // Tag and path pickers: chips are the value, held in a hidden input as a
  // comma-separated list so the payload builders stay unchanged.
  document.querySelectorAll("[data-tag-picker]").forEach((picker) => {
    const field = $(`#${picker.dataset.tagPicker}`);
    const chips = picker.querySelector("[data-tag-chips]");
    const keySelect = picker.querySelector("[data-tag-key]");
    const valueSelect = picker.querySelector("[data-tag-value]");
    const sync = () => {
      const values = [...chips.querySelectorAll("[data-tag-remove]")].map(
        (b) => b.dataset.tagRemove,
      );
      field.value = values.join(", ");
      chips
        .querySelectorAll("[data-tag-remove]")
        .forEach((b) => (b.onclick = () => (b.parentElement.remove(), sync())));
    };
    // "Custom…" turns a select into a text box for a value not observed yet.
    const custom = (select, placeholder) => {
      if (select.value !== "__custom__") return;
      const input = document.createElement("input");
      input.placeholder = placeholder;
      input.dataset[select.dataset.tagKey !== undefined ? "tagKey" : "tagValue"] =
        "";
      input.className = "tag-custom";
      select.replaceWith(input);
      input.focus();
      return input;
    };
    if (keySelect)
      keySelect.onchange = () => {
        if (custom(keySelect, "tag key")) return;
        const values = knownTags().get(keySelect.value) || new Set();
        setHTML(
          valueSelect,
          [...values]
            .sort()
            .map(
              (value) =>
                `<option value="${escapeHTML(value)}">${escapeHTML(value)}</option>`,
            )
            .join("") + '<option value="__custom__">Custom…</option>',
        );
      };
    valueSelect.onchange = () => custom(valueSelect, "value");
    picker.querySelector("[data-tag-add]").onclick = () => {
      const key = picker.querySelector("[data-tag-key]");
      const value = picker.querySelector("[data-tag-value]");
      if (!value.value || value.value === "__custom__") return;
      if (key && (!key.value || key.value === "__custom__")) return;
      const entry = key ? `${key.value}=${value.value}` : value.value;
      if (field.value.split(", ").filter(Boolean).includes(entry)) return;
      chips.insertAdjacentHTML("beforeend", tagChip(entry));
      sync();
    };
    sync();
  });
  const helpToggle = $("[data-help-toggle]");
  if (helpToggle)
    helpToggle.onclick = () => {
      const panel = $("#form-help");
      panel.hidden = !panel.hidden;
      helpToggle.classList.toggle("active", !panel.hidden);
    };
  // Buttons inside a dialog reach the same action dispatcher as the page.
  document.querySelectorAll("#modal-root [data-action]").forEach(
    (b) =>
      (b.onclick = (event) => {
        event.preventDefault();
        action(b.dataset.action, b);
      }),
  );
  document.querySelectorAll("[data-copy]").forEach(
    (b) =>
      (b.onclick = () => {
        const code = b.closest(".copy-field")?.querySelector("code");
        const text = b.dataset.copy || code?.textContent || "";
        navigator.clipboard?.writeText(text);
        const original = b.textContent;
        b.textContent = "Copied";
        setTimeout(() => (b.textContent = original), 1200);
      }),
  );
  document.querySelectorAll("[data-remove-membership]").forEach(
    (b) =>
      (b.onclick = async () => {
        try {
          await api("/api/v1/memberships/" + b.dataset.removeMembership, {
            method: "DELETE",
          });
          await hydrate();
          closeModal();
          toast("Membership removed", "Resource removed from static group.");
        } catch (error) {
          toast("Request failed", error.message);
        }
      }),
  );
  let stepList = $("[data-runbook-steps]");
  if (stepList) {
    let renumber = () =>
      stepList.querySelectorAll("[data-runbook-step]").forEach((row, index) => {
        row.querySelector("[data-step-number]").textContent = `STEP ${index + 1}`;
        row.querySelector("[data-step-name]").placeholder = `Step ${index + 1}`;
        row.querySelector("[data-remove-step]").disabled =
          stepList.querySelectorAll("[data-runbook-step]").length === 1;
      });
    stepList.onclick = (event) => {
      let remove = event.target.closest("[data-remove-step]");
      if (!remove || remove.disabled) return;
      remove.closest("[data-runbook-step]").remove();
      renumber();
    };
    $("[data-add-runbook-step]").onclick = () => {
      stepList.insertAdjacentHTML("beforeend", runbookStepFields({}, stepList.children.length));
      renumber();
    };
    renumber();
  }
  let permissionList = $("[data-role-permissions]");
  if (permissionList && $("[data-add-role-permission]")) {
    let updatePermissionButtons = () =>
      permissionList
        .querySelectorAll("[data-role-permission]")
        .forEach(
          (row) =>
            (row.querySelector("[data-remove-permission]").disabled =
              permissionList.querySelectorAll("[data-role-permission]").length === 1),
        );
    permissionList.onclick = (event) => {
      let remove = event.target.closest("[data-remove-permission]");
      if (!remove || remove.disabled) return;
      remove.closest("[data-role-permission]").remove();
      updatePermissionButtons();
    };
    $("[data-add-role-permission]").onclick = () => {
      permissionList.insertAdjacentHTML("beforeend", rolePermissionFields());
      updatePermissionButtons();
    };
    updatePermissionButtons();
  }
  $("[data-confirm]").onclick = async () => {
    if (!onConfirm) {
      // No request was made; no toast.
      closeModal();
      return;
    }
    try {
      const keepOpen = await onConfirm();
      if (keepOpen === true) return;
      closeModal();
      toast(title, "Request completed successfully.");
    } catch (error) {
      toast("Request failed", error.message);
    }
  };
}
// Failures stay up longer than confirmations and are marked.
function toast(title, detail) {
  const failed = /failed|denied|unavailable|error/i.test(title);
  setHTML(
    $("#toast-root"),
    `<div class="toast ${failed ? "failed" : ""}"><strong>${title}</strong><div>${detail}</div></div>`,
  );
  setTimeout(() => ($("#toast-root").innerHTML = ""), failed ? 6000 : 3200);
}
function resourceFields(resource = {}) {
  let types = ["node", "hypervisor", "vm", "container", "process"],
    health = ["healthy", "warning", "critical", "unknown", "maintenance"];
  return `<div class="form-row"><label>NAME</label><input id="resource-name" value="${resource.name || ""}" placeholder="e.g. legacy-node-01"></div><div class="form-row"><label>TYPE</label><select id="resource-type">${types.map((value) => `<option value="${value}" ${resource.type === value ? "selected" : ""}>${value}</option>`).join("")}</select></div><div class="form-row"><label>HEALTH</label><select id="resource-health">${health.map((value) => `<option value="${value}" ${resource.health === value ? "selected" : ""}>${value}</option>`).join("")}</select></div><div class="form-row"><label>TAGS</label><input id="resource-tags" value="${Object.entries(
    resource.tags || {},
  )
    .map(([key, value]) => `${key}=${value}`)
    .join(
      ", ",
    )}" placeholder="environment=production, owner=platform"></div><div class="form-row"><label>ATTRIBUTES</label><input id="resource-attributes" value="${Object.entries(
    resource.attributes || {},
  )
    .map(([key, value]) => `${key}=${value}`)
    .join(
      ", ",
    )}" placeholder="ip=10.0.0.10, os=linux"></div>${resource.agentId ? '<div class="warning-box">Agent ownership and identity cannot be changed manually.</div>' : ""}`;
}
function resourcePayload() {
  return {
    name: $("#resource-name").value,
    type: $("#resource-type").value,
    health: $("#resource-health").value,
    tags: keyValues($("#resource-tags").value),
    attributes: keyValues($("#resource-attributes").value),
  };
}
function bindingFields(binding = {}) {
  let roles = state.liveRoles
      .map(
        (r) =>
          `<option value="${r.id}" ${r.id === binding.roleId ? "selected" : ""}>${r.name}</option>`,
      )
      .join(""),
    scopes = state.liveScopes
      .map(
        (s) =>
          `<option value="${s.id}" ${s.id === binding.scopeId ? "selected" : ""}>${s.name}</option>`,
      )
      .join("");
  return `${selectField("binding-subject", "SUBJECT", knownSubjects(true), binding.subjectId || "", { custom: true, skipI18n: true, hint: "A local account or a team." })}<div class="form-row"><label>ROLE</label><select id="binding-role">${roles}</select></div><div class="form-row"><label>SCOPE</label><select id="binding-scope">${scopes}</select></div><div class="form-row"><label>EXPIRES AT</label><input id="binding-expires" type="datetime-local" value="${binding.expiresAt ? binding.expiresAt.slice(0, 16) : ""}"></div>`;
}
function bindingPayload() {
  let expires = $("#binding-expires").value;
  return {
    subjectId: $("#binding-subject").value,
    roleId: $("#binding-role").value,
    scopeId: $("#binding-scope").value,
    expiresAt: expires ? new Date(expires).toISOString() : null,
  };
}
// Search box for a long option list. The select keeps its id, so payload
// builders are unchanged.
function optionFilter(id, placeholder = "Filter…") {
  return `<input class="option-filter" data-option-filter="${id}" placeholder="${placeholder}" autocomplete="off">`;
}

// What each metric measures and the unit its threshold is given in.
const METRIC_UNITS = {
  cpu: { unit: "%", max: 100, hint: "Share of all cores, averaged over the sample." },
  memory: { unit: "%", max: 100, hint: "Share of installed memory in use." },
  disk: { unit: "%", max: 100, hint: "Share of the root filesystem in use." },
  network_rx_rate: { unit: "rate", hint: "Inbound throughput on the node's interfaces." },
  network_tx_rate: { unit: "rate", hint: "Outbound throughput on the node's interfaces." },
  // Counters, not shares. They only rise, so a threshold is a count since the
  // container started rather than a level it sits at, and the hint says so:
  // "> 0" means it has happened at all.
  oom_kills: { unit: "count", hint: "Times the kernel killed something in this container. Over 0 means it has happened at all." },
  throttled_usec: { unit: "count", hint: "Microseconds the kernel held this container off the CPU for exceeding its quota." },
  throttled_count: { unit: "count", hint: "Periods in which this container was held off the CPU." },
};
const RATE_UNITS = [
  ["B/s", 1],
  ["KB/s", 1024],
  ["MB/s", 1024 ** 2],
  ["GB/s", 1024 ** 3],
];

// Splits a bytes/second threshold into the largest unit that keeps it readable.
function rateParts(bytesPerSecond) {
  const value = Number(bytesPerSecond) || 0;
  for (let i = RATE_UNITS.length - 1; i > 0; i--) {
    if (value >= RATE_UNITS[i][1]) return { value: value / RATE_UNITS[i][1], scale: RATE_UNITS[i][1] };
  }
  return { value, scale: 1 };
}

// The group a resource is filed under. Label groups (OS, arch) rank last so a
// node reads as its cluster or rack.
const GROUP_RANK = { cluster: 0, rack: 1, zone: 2, service: 3 };
function structuralGroup(resourceID) {
  return state.liveMemberships
    .filter((m) => m.resourceId === resourceID)
    .map((m) => state.liveGroups.find((g) => g.id === m.groupId))
    .filter(Boolean)
    .sort((a, b) => (GROUP_RANK[a.type] ?? 9) - (GROUP_RANK[b.type] ?? 9))[0];
}

// Health values map onto the four status colours; "healthy" has no class of
// its own.
const healthTone = (health) =>
  ({ healthy: "ok", warning: "warn", critical: "critical" })[health] ||
  "unknown";

// Short tags for incident timeline entries.
// The timeline answers two questions at once, and they deserve different
// weight. "What have we done about this" is the live one, so entries from
// after the incident was declared are shown. "What happened just before it"
// is evidence for afterwards, so those entries are folded behind a count —
// they are what caused the incident often enough to be worth keeping, and
// noisy enough not to lead with.
function incidentTimelineBody(incident) {
  if (!state.incidentEvents.length)
    return '<div class="empty">No timeline entry yet</div>';
  const declaredAt = Date.parse(incident.createdAt),
    // A check reads the machine and changes nothing. Repeated inventory
    // refreshes were burying the two lines that changed something, so they sit
    // behind a count with the entries from before the incident was declared.
    isCheck = (e) => e.metadata?.effect === "read",
    before = state.incidentEvents.filter((e) => Date.parse(e.createdAt) < declaredAt),
    since = state.incidentEvents.filter((e) => Date.parse(e.createdAt) >= declaredAt),
    checks = since.filter(isCheck),
    changes = since.filter((e) => !isCheck(e)),
    fold = (open, action, count, label) =>
      count
        ? `<button class="btn btn-sm" data-action="${action}" style="margin:0 6px 8px 0">${open ? "Hide" : "Show"} ${count} ${label}</button>`
        : "";
  return (
    `<div>${fold(state.incidentHistoryOpen, "toggle-incident-history", before.length, "before it was declared")}${fold(state.incidentChecksOpen, "toggle-incident-checks", checks.length, "checks")}</div>` +
    (state.incidentHistoryOpen ? before.map(incidentTimelineRow).join("") : "") +
    (state.incidentChecksOpen
      ? since.map(incidentTimelineRow).join("")
      : changes.length
        ? changes.map(incidentTimelineRow).join("")
        : '<div class="empty">Nothing has changed this host since it was declared</div>')
  );
}

// A timeline row. A derived row was not typed by anyone — it is an action the
// server found against this incident's resources — so it is marked as such and
// carries the record it came from, rather than opening the note editor.
// A derived row points at the record it came from, so it opens that record
// rather than the note editor: a shell session opens its recording, an
// operation opens its result. Reading what was typed during the incident is
// the reason to look at the timeline at all.
function incidentTimelineLink(event) {
  const meta = event.metadata || {};
  if (meta.sessionId)
    return {
      action: `data-action="view-terminal-recording" data-session-id="${escapeHTML(meta.sessionId)}"`,
      title: "View the session recording",
    };
  // Only when the operation is still held: the store prunes old ones, and a
  // row that opens an empty dialog is worse than one that does nothing.
  if (meta.operationId && state.liveOperations.some((o) => o.id === meta.operationId))
    return {
      action: `data-action="view-operation" data-operation-id="${escapeHTML(meta.operationId)}"`,
      title: "View the task",
    };
  return null;
}

function incidentTimelineRow(event) {
  const derived = event.source === "derived",
    tag = EVENT_TAGS[event.type] || event.type.slice(0, 2).toUpperCase(),
    where = event.metadata?.resourceId,
    link = derived ? incidentTimelineLink(event) : null,
    open = derived
      ? link
        ? ` ${link.action} title="${link.title}"`
        : ""
      : ` data-incident-event="${escapeHTML(event.id)}" title="View entry"`;
  // Evidence is shown, not linked. Someone attached these lines because they
  // are the reason for a conclusion, and a reader should not have to ask for
  // them -- especially long after the read that found them was let go.
  const excerpt = event.metadata?.excerpt
    ? `<pre class="timeline-excerpt" data-i18n-skip>${escapeHTML(event.metadata.excerpt)}</pre>`
    : "";
  const steps = timelineSteps(event);
  return `<div class="relation-node${derived && !link ? " timeline-derived" : " clickable"}${derived ? " timeline-derived-row" : ""}"${open}><span class="resource-icon">${tag}</span><div><b>${escapeHTML(event.message)}</b><div class="muted"><span>${escapeHTML(event.actor || "—")}</span> · ${formatWhen(event.createdAt)}${where ? ` · <span class="mono">${escapeHTML(where)}</span>` : ""}${derived ? ' · <span class="timeline-auto">recorded elsewhere</span>' : ""}</div>${steps}${excerpt}</div></div>`;
}

// The steps inside one entry.
//
// A shell session used to be three rows — requested, approved, closed — so a
// response that opened eight of them produced twenty-four lines at the same
// second, none of which could be told from the next. They are one session,
// and the lifecycle belongs underneath it rather than beside every other
// session's.
function timelineSteps(event) {
  const meta = event.metadata || {};
  const steps = [];
  if (meta.requestedAt)
    steps.push(`Requested by ${escapeHTML(meta.requestedBy || "—")} · ${formatWhen(meta.requestedAt)}`);
  if (meta.approvedAt)
    steps.push(`Approved by ${escapeHTML(meta.approvedBy || "—")} · ${formatWhen(meta.approvedAt)}`);
  if (meta.closedAt) steps.push(`Closed · ${formatWhen(meta.closedAt)}`);
  if (meta.heldFor) steps.push(`Held for ${escapeHTML(meta.heldFor)}`);
  if (meta.commands) steps.push(`${escapeHTML(meta.commands)} commands sent`);
  if (!steps.length) return "";
  return `<ul class="timeline-steps">${steps.map((step) => `<li>${step}</li>`).join("")}</ul>`;
}

const EVENT_TAGS = {
  declared: "IN",
  evidence: "EV",
  note: "NO",
  status: "ST",
  resource: "RS",
  operation: "OP",
  terminal: "SH",
  approval: "AP",
  alert: "AL",
};

// The one-line installer. Over plain HTTP the script itself is fetched in the
// clear, so the reviewable form is offered instead of piping it into a shell.
// The agent runs unprivileged, so each collection needs its own group. The
// installer grants only what is enabled here.
const INSTALL_OPTIONS = [
  ["logs", "Logs", "--no-logs", "Reads the system journal and /var/log. Needs the systemd-journal and adm groups."],
  ["containers", "Containers", "--no-containers", "Discovers containers and reads their cgroup usage. Needs the container runtime group."],
  ["terminal", "Remote shell", "--no-terminal", "Offers approval-gated shell sessions on this host."],
];

function installCommands(origin, token, options = {}) {
  const flags = INSTALL_OPTIONS.filter(([key, , flag]) =>
    flag.startsWith("--no-") ? options[key] === false : options[key] === true,
  )
    .map(([, , flag]) => flag)
    .join(" ");
  const args = `${token}${flags ? " " + flags : ""}`;
  return {
    pipe: `curl -fsSL ${origin}/api/v1/agent-install.sh | sudo sh -s -- ${args}`,
  };
}

// The install block, rebuilt whenever a collection option changes.
function installBlock(origin, token, options) {
  const commands = installCommands(origin, token, options);
  const toggles = INSTALL_OPTIONS.map(
    ([key, label, , hint]) =>
      `<label class="check-row" title="${escapeHTML(hint)}"><input type="checkbox" data-install-option="${key}" ${options[key] ? "checked" : ""}> ${label}</label>`,
  ).join("");
  return `<div class="form-row" style="margin-top:12px"><label>COLLECTION</label><div class="check-grid">${toggles}</div><div class="field-hint">The installer grants only the group each enabled collection needs.</div></div><div class="form-row"><label>ONE-LINE INSTALL</label><div class="copy-field wrap"><code>${escapeHTML(commands.pipe)}</code><button class="btn btn-sm" data-copy="${escapeHTML(commands.pipe)}">Copy</button></div><div class="field-hint">Verifies the checksum, installs the service, and enrols. Run before the token expires.</div></div>`;
}

// Help shown behind the [?] on create and edit dialogs.
const FORM_HELP = {
  relation: {
    title: "Attach resource",
    points: [
      "Links a manually registered resource to this host so it appears under its sub-resources and inherits scope through the link.",
      "Only resources below this one in the hierarchy are offered: node > vm > container > process.",
      "Agent-managed resources are attached by their own inventory and are not listed here.",
      "hosts and runs are ownership; contains is grouping; depends_on records a dependency without ownership, so it does not propagate scope.",
    ],
  },
  resource: {
    title: "Unmanaged resource",
    points: [
      "For hosts and services no agent reports on. An agent-managed resource is created automatically and cannot be deleted here.",
      "Type places it in the hierarchy: node > vm > container > process.",
      "Health is what the console shows until a metric or alert says otherwise.",
      "Tags drive dynamic group membership and alert rule selectors. Use key=value pairs separated by commas.",
    ],
  },
  group: {
    title: "Node group",
    points: [
      "Groups aggregate health and capacity, and a group's path is the scope a role binding can be limited to.",
      "Type is what the group means: rack, cluster, service, or zone. The infrastructure map groups by it.",
      "Path is the hierarchy position, such as production/dc-1/rack-08. Scopes match on it by prefix.",
      "Static membership is chosen by hand; dynamic membership follows the tag selector and updates itself.",
    ],
  },
  alertRule: {
    title: "Alert rule",
    points: [
      "An alert is raised only after the condition holds continuously for the duration.",
      "CPU, memory, and disk are percentages of capacity. The network metrics are a byte rate; pick the unit beside the threshold.",
      "Scope limits the rule to a hierarchy path; the tag selector narrows it further to resources carrying every tag.",
      "Editing, disabling, or deleting a rule resolves the alerts it raised and restarts its duration.",
    ],
  },
  silence: {
    title: "Silence window",
    points: [
      "Matching alerts are suppressed for the window; the underlying condition is still evaluated.",
      "Existing matching alerts move to silenced when the window opens.",
      "Scope and tag selector decide what matches. Leave the selector empty to cover the whole scope.",
    ],
  },
  incident: {
    title: "Incident",
    points: [
      "Declare one when something needs coordinated response; the timeline records every change with its author.",
      "An incident needs at least one resource or alert. Resources of linked alerts are added automatically.",
      "The commander is the person accountable for the response, not necessarily the one who declared it.",
    ],
  },
  user: {
    title: "Account",
    points: [
      "An account signs in; what it may do comes from role bindings, not from the account itself.",
      "Assigning a role here creates the binding for you. Fine-grained access is managed under Role bindings.",
      "Disabling blocks sign-in and ends existing sessions but keeps the account and its audit trail.",
    ],
  },
  scope: {
    title: "Scope",
    points: [
      "A scope is the set of hierarchy paths a role binding applies to.",
      "Paths match by prefix: production/dc-1 covers everything beneath it.",
      "A tag selector restricts the scope further to resources carrying every tag.",
    ],
  },
  role: {
    title: "Role",
    points: [
      "A role is a list of resource and action pairs; * means every resource or every action.",
      "A role grants nothing on its own until a binding attaches it to a subject and a scope.",
      "System roles cannot be edited, so a working administrator role always remains.",
    ],
  },
  binding: {
    title: "Role binding",
    points: [
      "A binding is what actually grants access: it attaches a role to a subject within a scope.",
      "The subject is a local account or a team.",
      "An expiry makes the grant temporary; the binding stops applying once it passes.",
    ],
  },
  runbook: {
    title: "Runbook",
    points: [
      "A runbook is an ordered list of agent operations run against one target node.",
      "Risk decides whether an execution needs a second person's approval before it runs.",
      "Only operations on the agent's allowlist execute, whatever the runbook asks for.",
    ],
  },
  operation: {
    title: "Agent task",
    points: [
      "Tasks run through the agent on the target node, never over SSH from the server.",
      "Service operations are limited to the agent's allowlist, and a restart needs approval.",
      "The reason is recorded in the audit trail; write what a reviewer would need to know.",
    ],
  },
  terminal: {
    title: "Terminal session",
    points: [
      "A session opens only after another person approves it, and every keystroke and byte of output is recorded.",
      "Recordings are masked for common secret patterns, capped, and expire on their own.",
      "The session runs as the agent's unprivileged account unless the host configures otherwise.",
    ],
  },
  channel: {
    title: "Notification channel",
    points: [
      "A channel is where notifications go; the current type is a generic HTTP webhook.",
      "Routes decide which alerts reach which channel; a channel referenced by a route cannot be deleted.",
    ],
  },
  route: {
    title: "Notification route",
    points: [
      "A route matches alerts by severity, event, scope, and tags, then sends them to its channels.",
      "Evaluation stops at the first matching route unless it is set to continue.",
    ],
  },
  inhibition: {
    title: "Inhibition policy",
    points: [
      "An inhibition suppresses notification for target alerts while a matching source alert is active.",
      "The alert itself keeps firing; only its delivery is held back.",
      "Equal labels decide what counts as related. With none, only alerts on the same resource are compared.",
    ],
  },
};

// What the affected resources were doing when an event was recorded. The window
// is read from retained history, so an event older than the live series still
// resolves.
async function contextAtTime(resourceIDs, when) {
  const at = Date.parse(when);
  const window = `?from=${new Date(at - 30 * 60 * 1000).toISOString()}&to=${new Date(at + 30 * 60 * 1000).toISOString()}`;
  const rows = await Promise.all(
    resourceIDs.slice(0, 6).map(async (id) => {
      try {
        const result = await api(
          `/api/v1/resources/${encodeURIComponent(id)}/metrics${window}`,
        );
        const samples = result.items || [];
        if (!samples.length) return { id, samples: [] };
        // Nearest sample either side of the event.
        const nearest = samples.reduce((best, sample) =>
          Math.abs(Date.parse(sample.timestamp) - at) <
          Math.abs(Date.parse(best.timestamp) - at)
            ? sample
            : best,
        );
        return { id, nearest, drift: Date.parse(nearest.timestamp) - at, samples };
      } catch {
        return { id, samples: [] };
      }
    }),
  );
  return rows;
}

const CONTEXT_TOLERANCE = 10 * 60 * 1000;

function contextTable(rows) {
  const usable = rows.filter(
    (row) => row.nearest && Math.abs(row.drift) <= CONTEXT_TOLERANCE,
  );
  if (!usable.length)
    return '<div class="empty">No metric sample covers this moment</div>';
  const pct = (value) => `${Number(value || 0).toFixed(1)}%`;
  return `<div class="table-wrap"><table class="table compact"><thead><tr><th>Resource</th><th class="num">CPU</th><th class="num">Memory</th><th class="num">Disk</th><th class="num">Offset</th></tr></thead><tbody data-i18n-skip>${usable
    .map(
      (row) =>
        `<tr><td>${escapeHTML(resourceName(row.id))}</td><td class="num mono">${pct(row.nearest.cpu)}</td><td class="num mono">${pct(row.nearest.memory)}</td><td class="num mono">${pct(row.nearest.disk)}</td><td class="num mono muted">${(row.drift / 1000).toFixed(0)}s</td></tr>`,
    )
    .join("")}</tbody></table></div>`;
}

function resourceName(id) {
  return state.liveResources.find((r) => r.id === id)?.name || id;
}

// Display names for resource types. Capitalised so the language patterns that
// translate a "<Type> <count>" chip apply to them.
const SEVERITY_LABELS = { error: "Error", warn: "Warning", info: "Info" };

const TYPE_LABELS = {
  node: "Node",
  hypervisor: "Hypervisor",
  vm: "VM",
  container: "Container",
  process: "Process",
};

// The node a workload runs on, from the structural relations.
function hostResource(resourceID) {
  const relation = state.liveRelations.find(
    (r) => r.targetId === resourceID && ["hosts", "runs"].includes(r.type),
  );
  return relation
    ? state.liveResources.find((r) => r.id === relation.sourceId)
    : undefined;
}

function hostOf(resourceID) {
  return hostResource(resourceID)?.name || "";
}

// The address a resource answers on. A workload has none of its own, so it is
// reached at its host's -- which is the address an operator would dial.
function addressOf(resource) {
  if (!resource) return "—";
  const own = resource.attributes?.address;
  if (own) return own;
  return hostResource(resource.id)?.attributes?.address || "—";
}

// The line under a resource name that tells two same-named rows apart. A fleet
// runs one `bash` per session and one `(sd-pam)` per login, so the name alone
// never identifies a row: a node is known by the address it answers on, and a
// workload by the host and the pid or container id it holds there.
function resourceIdentity(resource) {
  if (!resource) return "";
  const attributes = resource.attributes || {};
  if (["node", "hypervisor"].includes(resource.type))
    return attributes.address || attributes.hostname || resource.id;
  const parts = [];
  const host = hostOf(resource.id);
  if (host) parts.push(host);
  if (resource.type === "process" && attributes.pid)
    parts.push("pid " + attributes.pid);
  else if (attributes.address) parts.push(attributes.address);
  else if (attributes.id) parts.push(attributes.id);
  return parts.join(" · ") || resource.id;
}

// Load states, named as the map legend names them.
const LOAD_LABELS = {
  idle: "Idle",
  normal: "Normal",
  busy: "Busy",
  saturated: "Saturated",
};

// Display names for the metric identifiers the API stores.
// What a rule can be written against. The first five are shares of capacity;
// the rest are counters the kernel keeps, which used to reach the console only
// as log text and so could not be alerted on at all.
const METRIC_LABELS = {
  cpu: "CPU",
  memory: "Memory",
  disk: "Disk",
  network_rx_rate: "Network (RX rate)",
  network_tx_rate: "Network (TX rate)",
  oom_kills: "OOM kills",
  throttled_usec: "CPU throttled (µs)",
  throttled_count: "CPU throttled (periods)",
};
const metricLabel = (metric) => METRIC_LABELS[metric] || metric;

// A counter only says something as a change: "has been killed three times
// since boot" is not an alert, "was killed again" is. The console says which
// kind of number a rule is comparing so a threshold is not read as a rate.
const COUNTER_METRICS = new Set(["oom_kills", "throttled_usec", "throttled_count"]);

// Rule summaries are stored with the metric identifier; substitute the display
// name so existing alerts read the same as new ones.
const summaryLabel = (summary) =>
  `${summary || ""}`.replace(
    new RegExp(`^(${Object.keys(METRIC_LABELS).join("|")})\\b`),
    (metric) => metricLabel(metric),
  );

// Identities that can be assigned: local accounts and teams.
function knownSubjects(includeTeams = false) {
  const subjects = state.liveUsers.map((user) => user.username);
  if (includeTeams) subjects.push(...state.liveTeams.map((team) => team.id));
  return [...new Set(subjects.filter(Boolean))].sort();
}

// Select over known values. `custom` appends an option that turns the control
// into a text box for a value the console cannot enumerate.
function selectField(id, label, options, value = "", opts = {}) {
  const all = [...new Set([value, ...options].filter(Boolean))];
  const selected = (option) =>
    opts.multiple
      ? (Array.isArray(value) ? value : []).includes(option)
      : option === value;
  const list = opts.multiple ? [...new Set(options.filter(Boolean))] : all;
  return `<div class="form-row"><label>${label}</label><select id="${id}" ${opts.multiple ? `multiple size="${Math.min(5, Math.max(3, list.length))}"` : ""} ${opts.custom ? "data-custom-select" : ""} ${opts.skipI18n ? "data-i18n-skip" : ""}>${list.map((option) => `<option value="${escapeHTML(option)}" ${selected(option) ? "selected" : ""}>${escapeHTML(option)}</option>`).join("")}${opts.custom ? '<option value="__custom__">Custom…</option>' : ""}</select>${opts.hint ? `<div class="field-hint">${opts.hint}</div>` : ""}</div>`;
}

// Hierarchy paths in use, from groups and from assigned scopes.
function knownScopePaths() {
  const paths = new Set();
  for (const group of state.liveGroups) if (group.path) paths.add(group.path);
  for (const scope of state.liveScopes)
    for (const path of scope.paths || []) if (path) paths.add(path);
  return [...paths].sort();
}

// Scope select over the paths in use. A stored path that no longer matches a
// group is kept as an option so editing cannot silently drop it.
function scopeField(id, value = "", label = "SCOPE") {
  const options = [...new Set([value, ...knownScopePaths()].filter(Boolean))];
  return `<div class="form-row"><label>${label}</label><select id="${id}" data-i18n-skip>${options.map((path) => `<option value="${escapeHTML(path)}" ${path === value ? "selected" : ""}>${escapeHTML(path)}</option>`).join("")}</select></div>`;
}

// Tag keys and values observed across resources.
function knownTags() {
  const tags = new Map();
  for (const resource of state.liveResources)
    for (const [key, value] of Object.entries(resource.tags || {})) {
      if (!tags.has(key)) tags.set(key, new Set());
      tags.get(key).add(String(value));
    }
  return tags;
}

// Tag selector built from observed key/value pairs. The chosen pairs are held in
// a hidden input as "k=v, k=v" so the existing payload builders are unchanged.
function tagSelectorField(id, selector = {}, label = "TAG SELECTOR", hint = "") {
  const tags = knownTags();
  const keys = [...tags.keys()].sort();
  const pairs = Object.entries(selector).map(([k, v]) => `${k}=${v}`);
  const first = keys[0] || "";
  return `<div class="form-row"><label>${label}</label><div class="tag-picker" data-tag-picker="${id}"><div class="tag-chips" data-tag-chips data-i18n-skip>${pairs.map((pair) => tagChip(pair)).join("")}</div><div class="tag-controls"><select data-tag-key data-i18n-skip>${keys.map((key) => `<option value="${escapeHTML(key)}">${escapeHTML(key)}</option>`).join("")}<option value="__custom__">Custom…</option></select><select data-tag-value data-i18n-skip>${[...(tags.get(first) || [])].sort().map((value) => `<option value="${escapeHTML(value)}">${escapeHTML(value)}</option>`).join("")}<option value="__custom__">Custom…</option></select><button type="button" class="btn btn-sm" data-tag-add>Add</button></div><input type="hidden" id="${id}" value="${escapeHTML(pairs.join(", "))}"></div>${hint ? `<div class="field-hint">${hint}</div>` : ""}</div>`;
}

// Path list built from the same known paths, for fields that accept several.
function pathPickerField(id, paths = [], label = "HIERARCHY PATHS", disabled = "") {
  const known = knownScopePaths();
  return `<div class="form-row"><label>${label}</label><div class="tag-picker" data-tag-picker="${id}"><div class="tag-chips" data-tag-chips data-i18n-skip>${paths.map((path) => tagChip(path)).join("")}</div><div class="tag-controls"><select data-tag-value data-i18n-skip ${disabled}>${known.map((path) => `<option value="${escapeHTML(path)}">${escapeHTML(path)}</option>`).join("")}<option value="__custom__">Custom…</option></select><button type="button" class="btn btn-sm" data-tag-add ${disabled}>Add</button></div><input type="hidden" id="${id}" value="${escapeHTML(paths.join(", "))}"></div></div>`;
}

function tagChip(pair) {
  return `<span class="tag-chip"><span class="mono">${escapeHTML(pair)}</span><button type="button" data-tag-remove="${escapeHTML(pair)}" aria-label="Remove">×</button></span>`;
}

function alertRuleFields(rule = {}) {
  // A network threshold is stored in bytes/second; show it in the largest unit
  // that keeps the number readable.
  const isRate = (rule.metric || "cpu").startsWith("network_");
  const parts = isRate ? rateParts(rule.threshold) : null;
  const thresholdValue = isRate ? parts.value : (rule.threshold ?? 90);
  const thresholdScale = isRate ? parts.scale : 1;
  return `<div class="form-row"><label>NAME</label><input id="rule-name" value="${rule.name || ""}" placeholder="CPU saturation"></div><div class="form-row"><label>METRIC</label><select id="rule-metric">${Object.keys(METRIC_LABELS).map((x) => `<option value="${x}" ${x === (rule.metric || "cpu") ? "selected" : ""}>${metricLabel(x)}</option>`).join("")}</select></div><div class="form-row"><label>CONDITION</label><div class="condition-row"><select id="rule-operator">${[">", ">=", "<", "<="].map((x) => `<option value="${x}" ${x === (rule.operator || ">") ? "selected" : ""}>${x}</option>`).join("")}</select><input id="rule-threshold" type="number" min="0" step="any" value="${escapeHTML(String(thresholdValue))}"><select id="rule-threshold-unit" data-i18n-skip>${RATE_UNITS.map(([label, scale]) => `<option value="${scale}" ${scale === thresholdScale ? "selected" : ""}>${label}</option>`).join("")}</select><span id="rule-threshold-unit-text" class="unit-suffix">%</span></div><div class="field-hint" id="rule-metric-hint">${METRIC_UNITS[rule.metric || "cpu"].hint}</div></div><div class="form-row"><label>DURATION</label><input id="rule-duration" value="${rule.duration || "5m"}"></div><div class="form-row"><label>SEVERITY</label><select id="rule-severity"><option value="warning" ${rule.severity === "warning" ? "selected" : ""}>Warning</option><option value="critical" ${rule.severity !== "warning" ? "selected" : ""}>Critical</option></select></div>${scopeField("rule-scope", rule.scopePath || "production")}${tagSelectorField("rule-selector", rule.selector, "TAG SELECTOR", "Only resources carrying every tag are evaluated. Leave empty to match the whole scope.")}<div class="form-row"><label>ENABLED</label><select id="rule-enabled"><option value="true" ${rule.enabled !== false ? "selected" : ""}>Enabled</option><option value="false" ${rule.enabled === false ? "selected" : ""}>Disabled</option></select></div>`;
}
function alertRulePayload(rule = {}) {
  return {
    ...rule,
    name: $("#rule-name").value,
    metric: $("#rule-metric").value,
    operator: $("#rule-operator").value,
    threshold:
      Number($("#rule-threshold").value) *
      ($("#rule-metric").value.startsWith("network_")
        ? Number($("#rule-threshold-unit").value) || 1
        : 1),
    duration: $("#rule-duration").value,
    severity: $("#rule-severity").value,
    scopePath: $("#rule-scope").value,
    selector: keyValues($("#rule-selector").value),
    enabled: $("#rule-enabled").value === "true",
  };
}
function alertSilenceFields(silence = {}) {
  let startsAt = silence.startsAt || new Date(),
    endsAt = silence.endsAt || new Date(Date.now() + 60 * 60 * 1000),
    selector = Object.entries(silence.selector || {})
      .map(([key, value]) => `${key}=${value}`)
      .join(", ");
  return `<div class="form-row"><label>NAME</label><input id="silence-name" value="${escapeHTML(silence.name || "")}" placeholder="Planned rack maintenance"></div>${scopeField("silence-scope", silence.scopePath || "production")}${tagSelectorField("silence-selector", silence.selector)}<div class="form-row"><label>START</label><input id="silence-start" type="datetime-local" value="${localDateTime(startsAt)}"></div><div class="form-row"><label>END</label><input id="silence-end" type="datetime-local" value="${localDateTime(endsAt)}"></div><div class="warning-box">Matching active alerts become silenced. New matching alerts are suppressed during this window.</div>`;
}
function alertSilencePayload(silence = {}) {
  return {
    ...silence,
    name: $("#silence-name").value,
    scopePath: $("#silence-scope").value,
    selector: keyValues($("#silence-selector").value),
    startsAt: new Date($("#silence-start").value).toISOString(),
    endsAt: new Date($("#silence-end").value).toISOString(),
  };
}
function inhibitionFields(item = {}) {
  return `<div class="form-row"><label>NAME</label><input id="inhibition-name" value="${escapeHTML(item.name || "")}" placeholder="Critical suppresses warning"></div><div class="form-row"><label>SOURCE SEVERITY</label><select id="inhibition-source"><option value="critical">Critical</option><option value="warning" ${item.sourceSeverity === "warning" ? "selected" : ""}>Warning</option></select></div><div class="form-row"><label>TARGET SEVERITY</label><select id="inhibition-target"><option value="warning">Warning</option><option value="critical" ${item.targetSeverity === "critical" ? "selected" : ""}>Critical</option></select></div>${scopeField("inhibition-scope", item.scopePath || "production")}${selectField("inhibition-labels", "EQUAL LABELS", [...knownTags().keys()].sort(), item.equalLabels || [], { multiple: true, skipI18n: true, hint: "Select none to compare alerts on the same resource only." })}<div class="form-row"><label>ENABLED</label><select id="inhibition-enabled"><option value="true">Enabled</option><option value="false" ${item.enabled === false ? "selected" : ""}>Disabled</option></select></div>`;
}
function inhibitionPayload(item = {}) { return {...item,name:$("#inhibition-name").value,sourceSeverity:$("#inhibition-source").value,targetSeverity:$("#inhibition-target").value,scopePath:$("#inhibition-scope").value,equalLabels:[...$("#inhibition-labels").selectedOptions].map((option)=>option.value),enabled:$("#inhibition-enabled").value === "true"}; }
function channelFields(item = {}) { return `<div class="form-row"><label>NAME</label><input id="channel-name" value="${escapeHTML(item.name || "")}" placeholder="Platform webhook"></div><div class="form-row"><label>WEBHOOK URL</label><input id="channel-url" value="${escapeHTML(item.url || "")}" placeholder="https://hooks.example.net/alerts"></div><div class="form-row"><label>ENABLED</label><select id="channel-enabled"><option value="true">Enabled</option><option value="false" ${item.enabled === false ? "selected" : ""}>Disabled</option></select></div>`; }
function channelPayload() { return {name:$("#channel-name").value,type:"webhook",url:$("#channel-url").value,enabled:$("#channel-enabled").value === "true"}; }
function routeFields(item = {}) { let options=state.liveNotificationChannels.map((channel)=>`<option value="${channel.id}" ${(item.channelIds || []).includes(channel.id)?"selected":""}>${escapeHTML(channel.name)}</option>`).join(""); return `<div class="form-row"><label>NAME</label><input id="route-name" value="${escapeHTML(item.name || "")}" placeholder="Critical production"></div><div class="form-row"><label>CHANNELS</label><select id="route-channels" multiple>${options}</select></div>${selectField("route-severities", "SEVERITIES", ["critical", "warning"], item.severities || ["critical"], { multiple: true })}${selectField("route-events", "EVENTS", ["firing", "resolved"], item.events || ["firing", "resolved"], { multiple: true })}${scopeField("route-scope", item.scopePath || "production")}<div class="form-row"><label>CONTINUE</label><select id="route-continue"><option value="false">Stop after route</option><option value="true" ${item.continue ? "selected" : ""}>Continue matching</option></select></div><div class="form-row"><label>ENABLED</label><select id="route-enabled"><option value="true">Enabled</option><option value="false" ${item.enabled === false ? "selected" : ""}>Disabled</option></select></div>`; }
function routePayload(item = {}) { return {...item,name:$("#route-name").value,channelIds:[...$("#route-channels").selectedOptions].map((option)=>option.value),severities:[...$("#route-severities").selectedOptions].map((option)=>option.value),events:[...$("#route-events").selectedOptions].map((option)=>option.value),scopePath:$("#route-scope").value,continue:$("#route-continue").value === "true",enabled:$("#route-enabled").value === "true"}; }
function runbookStepFields(step = {}, index = 0) {
  return `<div class="card-body" data-runbook-step><div style="display:flex;align-items:center;justify-content:space-between"><label data-step-number>STEP ${index + 1}</label><button type="button" class="btn btn-sm btn-danger" data-remove-step>Remove</button></div><div class="form-row"><label>NAME</label><input data-step-name value="${escapeHTML(step.name || "")}" placeholder="Step ${index + 1}"></div><div class="form-row"><label>OPERATION</label><select data-step-operation>${["inventory.refresh", "service.status", "service.restart"].map((x) => `<option value="${x}" ${x === (step.operation || "inventory.refresh") ? "selected" : ""}>${x}</option>`).join("")}</select></div><div class="form-row"><label>SERVICE</label><input data-step-service value="${escapeHTML(step.parameters?.service || "")}" placeholder="containerd.service · service operations only"></div></div>`;
}
function runbookFields(runbook = {}) {
  let steps = runbook.steps?.length ? runbook.steps : [{}];
  return `<div class="form-row"><label>NAME</label><input id="runbook-name" value="${escapeHTML(runbook.name || "")}" placeholder="Node inventory refresh"></div><div class="form-row"><label>RISK</label><select id="runbook-risk">${["low", "medium", "high"].map((x) => `<option value="${x}" ${x === (runbook.risk || "low") ? "selected" : ""}>${x}</option>`).join("")}</select></div><div class="form-row"><label>DESCRIPTION</label><textarea id="runbook-description">${escapeHTML(runbook.description || "")}</textarea></div><div class="card" data-runbook-steps>${steps.map(runbookStepFields).join("")}</div><button type="button" class="btn" data-add-runbook-step>+ Add step</button>`;
}
function runbookPayload(runbook = {}) {
  return {
    ...runbook,
    name: $("#runbook-name").value,
    risk: $("#runbook-risk").value,
    description: $("#runbook-description").value,
    steps: [...document.querySelectorAll("[data-runbook-step]")].map(
      (row, index) => {
        let operation = row.querySelector("[data-step-operation]").value;
        return {
          name: row.querySelector("[data-step-name]").value || `Step ${index + 1}`,
          operation,
          parameters: operation.startsWith("service.")
            ? { service: row.querySelector("[data-step-service]").value }
            : {},
        };
      },
    ),
  };
}
// Every resource the API guards, and every action a role may grant.
const PERMISSION_RESOURCES = [
  "*",
  "resources",
  "relations",
  "groups",
  "agents",
  "metrics",
  "alerts",
  "alert-rules",
  "incidents",
  "operations",
  "runbooks",
  "terminal",
  "users",
  "teams",
  "roles",
  "scopes",
  "bindings",
  "audit",
];
const PERMISSION_ACTIONS = [
  "read",
  "create",
  "update",
  "delete",
  "execute",
  "approve",
  "approve-self",
  "close",
  "*",
];

function rolePermissionFields(permission = {}, disabled = false) {
  const resources = PERMISSION_RESOURCES,
    actions = PERMISSION_ACTIONS;
  return `<div class="relation-node" data-role-permission><select data-permission-resource ${disabled ? "disabled" : ""}>${resources.map((value) => `<option value="${value}" ${value === permission.resource ? "selected" : ""}>${value}</option>`).join("")}</select><select data-permission-action ${disabled ? "disabled" : ""}>${actions.map((value) => `<option value="${value}" ${value === (permission.action || "read") ? "selected" : ""}>${value}</option>`).join("")}</select>${disabled ? "" : '<button type="button" class="btn btn-sm btn-danger" data-remove-permission>Remove</button>'}</div>`;
}
function roleFields(role = {}) {
  let permissions = role.permissions?.length
    ? role.permissions
    : [{ resource: "resources", action: "read" }];
  return `<div class="form-row"><label>ROLE NAME</label><input id="role-name" value="${escapeHTML(role.name || "")}" ${role.system ? "disabled" : ""}></div><div class="form-row"><label>DESCRIPTION</label><textarea id="role-description" ${role.system ? "disabled" : ""}>${escapeHTML(role.description || "")}</textarea></div><div class="form-row"><label>PERMISSIONS</label><div data-role-permissions>${permissions.map((permission) => rolePermissionFields(permission, role.system)).join("")}</div></div>${role.system ? '<div class="warning-box">System roles are immutable.</div>' : '<button type="button" class="btn" data-add-role-permission>+ Add permission</button>'}`;
}
function rolePayload(role = {}) {
  return {
    ...role,
    name: $("#role-name").value,
    description: $("#role-description").value,
    permissions: [...document.querySelectorAll("[data-role-permission]")].map(
      (row) => ({
        resource: row.querySelector("[data-permission-resource]").value,
        action: row.querySelector("[data-permission-action]").value,
      }),
    ),
  };
}

async function action(a, el) {
  if (a === "toggle-lang") {
    setLang(getLang() === "ko" ? "en" : "ko");
    render();
    return;
  }
  if (a === "toggle-theme") {
    setTheme(getTheme() === "light" ? "dark" : "light");
    render();
    return;
  }
  if (a === "logout") {
    try {
      await api("/api/v1/auth/logout", { method: "POST" });
    } catch {
      /* ignore */
    }
    // Full reload re-evaluates the cleared session.
    window.location.reload();
    return;
  }
  if (a === "show-login") {
    renderLogin();
    return;
  }
  if (a === "user-menu") {
    state.userMenuOpen = !state.userMenuOpen;
    render();
    return;
  }
  if (a === "close-user-menu") {
    state.userMenuOpen = false;
    render();
    return;
  }
  if (a === "toggle-incident-history") {
    state.incidentHistoryOpen = !state.incidentHistoryOpen;
    render();
    return;
  }
  if (a === "clear-log-picks") {
    state.logPicked = [];
    render();
    return;
  }
  if (a === "attach-log-lines") {
    const incidentId = $("#log-attach-incident")?.value;
    if (!incidentId) return;
    const incident = state.liveIncidents.find((x) => x.id === incidentId);
    // The lines go in the order they were logged, not the order they were
    // clicked: an excerpt read back later has to make sense on its own.
    const ordered = (state.logCapture?.text || "")
      .split("\n")
      .filter((raw) => state.logPicked.includes(raw));
    const excerpt = ordered.join("\n");
    modal(
      "Attach to incident",
      `<p>${ordered.length} lines will be copied into <b>${escapeHTML(incident?.title || incidentId)}</b>. A read's output is let go after thirty minutes; what is attached here stays with the incident.</p><div class="form-row"><label>WHY THIS MATTERS</label><input id="evidence-note" placeholder="What these lines show" autofocus></div><pre class="terminal-screen rec-screen" style="max-height:30vh" data-i18n-skip>${escapeHTML(excerpt)}</pre>`,
      "Attach",
      false,
      async () => {
        const note = ($("#evidence-note")?.value || "").trim();
        await api(`/api/v1/incidents/${encodeURIComponent(incidentId)}/events`, {
          method: "POST",
          body: JSON.stringify({
            type: "evidence",
            message: note || `${ordered.length} log lines attached`,
            metadata: {
              excerpt,
              source: state.logReadSource || "host",
              resourceId: state.logTarget || "",
            },
          }),
        });
        state.logPicked = [];
        if (state.selectedIncidentId === incidentId)
          await loadIncidentTimeline(incidentId);
        toast("Attached", `${ordered.length} lines are now part of the incident`);
        render();
      },
      { wide: true },
    );
    return;
  }
  if (a === "toggle-incident-checks") {
    state.incidentChecksOpen = !state.incidentChecksOpen;
    render();
    return;
  }
  if (a === "toggle-sidebar") {
    const mobile = window.matchMedia("(max-width: 760px)").matches;
    if (mobile) {
      state.mobileSidebarOpen = !state.mobileSidebarOpen;
    } else {
      state.sidebarCollapsed = !state.sidebarCollapsed;
      localStorage.setItem(
        "kv-sidebar",
        state.sidebarCollapsed ? "collapsed" : "open",
      );
    }
    render();
  } else if (a === "noc-mode") {
    state.sidebarCollapsed = true;
    render();
    document.documentElement.requestFullscreen?.();
  } else if (a === "settings") {
    state.userMenuOpen = false;
    render();
    modal(
      "Console settings",
      `<div class="spec-grid"><div class="spec"><label>API endpoint</label><span class="mono">${escapeHTML(location.origin)}</span></div><div class="spec"><label>Identity</label><span>${escapeHTML(state.subject)}</span></div><div class="spec"><label>Scope</label><span>${escapeHTML(state.scopePath)}</span></div><div class="spec"><label>API state</label><span class="${state.apiOnline ? "ok" : "critical"}">${state.apiOnline ? "Connected" : "Offline"}</span></div></div>`,
      "Close",
    );
  } else if (a === "edit-profile") {
    const current = state.auth?.user;
    if (!current) return;
    state.userMenuOpen = false;
    render();
    modal(
      "Edit profile",
      `<div class="form-row"><label>USERNAME</label><input value="${escapeHTML(current.username || "")}" disabled></div><div class="form-row"><label>DISPLAY NAME</label><input id="profile-display" value="${escapeHTML(current.displayName || "")}" autocomplete="name"></div><div class="form-divider">Change password (optional)</div><div class="form-row"><label>CURRENT PASSWORD</label><input id="profile-current" type="password" autocomplete="current-password" placeholder="required to set a new password"></div><div class="form-row"><label>NEW PASSWORD</label><input id="profile-new" type="password" autocomplete="new-password" placeholder="at least 6 characters"></div><div class="form-row"><label>CONFIRM NEW PASSWORD</label><input id="profile-confirm" type="password" autocomplete="new-password"></div>`,
      "Save changes",
      false,
      async () => {
        const displayName = $("#profile-display").value.trim();
        if (!displayName) throw new Error("Display name cannot be empty");
        const currentPassword = $("#profile-current").value;
        const newPassword = $("#profile-new").value;
        const confirmPassword = $("#profile-confirm").value;
        const body = { displayName };
        if (newPassword || confirmPassword || currentPassword) {
          if (!currentPassword)
            throw new Error("Enter your current password to change it");
          if (newPassword.length < 6)
            throw new Error("New password must be at least 6 characters");
          if (newPassword !== confirmPassword)
            throw new Error("New password and confirmation do not match");
          body.currentPassword = currentPassword;
          body.newPassword = newPassword;
        }
        await api("/api/v1/auth/me", {
          method: "PUT",
          body: JSON.stringify(body),
        });
        try {
          state.auth = await api("/api/v1/auth/me");
        } catch {
          /* keep existing session view */
        }
        await hydrate();
      },
    );
  } else if (a === "install-agent") {
    modal(
      "Install Agent",
      `<div class="install-flow"><p class="muted">Issue a token, then run the command it produces on the target host.</p>
<div class="form-row"><label>1 · ENROLLMENT TOKEN</label><div class="token-issue"><select id="token-ttl"><option value="60">Valid 1 minute</option><option value="300" selected>Valid 5 minutes</option><option value="900">Valid 15 minutes</option><option value="3600">Valid 1 hour</option></select><input id="token-hostname" placeholder="Bind to hostname (optional)"><input id="token-cidr" placeholder="Bind to CIDR (optional)"><button class="btn btn-primary" data-action="issue-token">Issue token</button></div></div>
<div id="token-result"></div></div>`,
      "Done",
      false,
      null,
      { viewer: true },
    );
  } else if (a === "capture-logs") {
    const target = $("#log-target").value;
    const minutes = Number($("#log-window").value) || 120;
    const since = new Date(Date.now() - minutes * 60000).toISOString();
    // The tab is "read"; what to read is chosen in the bar beside it.
    const source = $("#log-read-source")?.value || state.logReadSource || "host";
    const band = $("#log-band")?.value ?? state.logBand ?? "";
    state.logReadSource = source;
    state.logBand = band;
    state.logCapture = { pending: true, label: `${source} · ${target}` };
    render();
    try {
      const operation = await api("/api/v1/operations", {
        method: "POST",
        body: JSON.stringify({
          type: "logs.capture",
          targetIds: [target],
          reason: `Read ${source} logs for the last ${minutes} minutes`,
          parameters: { source, since, priority: band, lines: "2000" },
        }),
      });
      // The agent claims work on its polling interval, so wait for the result.
      const finished = await waitForOperation(operation.id, 40000);
      // The lines themselves are not in the operation. An operation's result
      // is a field in the state document, bounded at four kilobytes because
      // that document is rewritten whole every few seconds -- a read of one
      // host's last two hours is eighty-six. The operation says how much there
      // is; the text is fetched from where it is actually held.
      let text = "";
      if (finished && !finished.error) {
        text = await api(`/api/v1/operations/${operation.id}/report`)
          .then((r) => r.text || "")
          .catch(() => "");
      }
      state.logCapture = {
        label: `${source} · ${target}`,
        text,
        error:
          finished?.error ||
          (finished
            ? text
              ? ""
              : "The node answered, but the output is no longer held. Read again."
            : "The agent did not answer in time"),
        capturedAt: new Date().toISOString(),
      };
    } catch (error) {
      state.logCapture = { label: source, error: error.message };
    }
    render();
  } else if (a === "issue-token") {
    const box = $("#token-result");
    try {
      const issued = await api("/api/v1/enrollment-tokens", {
        method: "POST",
        body: JSON.stringify({
          ttlSeconds: Number($("#token-ttl").value),
          hostname: $("#token-hostname").value.trim(),
          allowedCidr: $("#token-cidr").value.trim(),
          maxUses: 0,
        }),
      });
      const expires = new Date(issued.token.expiresAt).toLocaleTimeString(
        consoleLocale(),
      );
      const options = { logs: true, containers: true, terminal: true };
      const header = `<div class="field-hint">Shown once. A bound token is refused from any other host and expires whether or not it is used.</div><div class="copy-field"><code>${escapeHTML(issued.value)}</code><button class="btn btn-sm" data-copy="${escapeHTML(issued.value)}">Copy</button></div><div class="field-hint">Expires at ${escapeHTML(expires)}${issued.token.hostname ? ` · host ${escapeHTML(issued.token.hostname)}` : ""}${issued.token.allowedCidr ? ` · from ${escapeHTML(issued.token.allowedCidr)}` : ""}</div>`;
      const bindInstall = () => {
        document.querySelectorAll("[data-copy]").forEach(
          (b) =>
            (b.onclick = () => {
              navigator.clipboard?.writeText(
                b.dataset.copy || b.closest(".copy-field")?.querySelector("code")?.textContent || "",
              );
              const original = b.textContent;
              b.textContent = "Copied";
              setTimeout(() => (b.textContent = original), 1200);
            }),
        );
        document.querySelectorAll("[data-install-option]").forEach(
          (input) =>
            (input.onchange = () => {
              options[input.dataset.installOption] = input.checked;
              draw();
            }),
        );
      };
      const draw = () => {
        setHTML(
          box,
          header +
            installBlock(location.origin, issued.value, options) +
            `<p class="muted">Reconnects after a reboot without a new token.</p>`,
        );
        bindInstall();
      };
      draw();
    } catch (error) {
      if (box) setHTML(box, `<div class="warning-box">${escapeHTML(error.message)}</div>`);
    }
  }
  else if (a === "attach-resource") {
    const hostId = el.dataset.hostId;
    const host = state.liveResources.find((item) => item.id === hostId);
    // Read the current list rather than the cached one: a resource registered
    // moments ago is exactly what someone is here to attach.
    let known = state.liveResources;
    try {
      known = (await api("/api/v1/resources?limit=1000")).items || known;
    } catch {}
    const attached = new Set(
      state.liveRelations
        .filter((item) => item.sourceId === hostId)
        .map((item) => item.targetId),
    );
    const typeRank = { node: 0, hypervisor: 0, vm: 1, container: 2, process: 3 };
    // Only manually registered resources below this one in the hierarchy: an
    // agent-managed resource is already attached by its own inventory.
    const candidates = known.filter(
      (item) =>
        !item.agentId &&
        item.id !== hostId &&
        !attached.has(item.id) &&
        (typeRank[item.type] ?? 9) > (typeRank[host?.type] ?? 0),
    );
    if (!candidates.length) {
      toast(
        "Nothing to attach",
        "Every unmanaged resource below this one is already attached",
      );
      return;
    }
    const names = new Map(candidates.map((item) => [item.name + " · " + item.id, item.id]));
    modal(
      "Attach resource",
      selectField("attach-target", "RESOURCE", [...names.keys()], "", {
        skipI18n: true,
      }) +
        selectField("attach-type", "RELATION", ["hosts", "runs", "contains", "depends_on"], "hosts"),
      "Attach",
      false,
      async () => {
        await api("/api/v1/relations", {
          method: "POST",
          body: JSON.stringify({
            sourceId: hostId,
            targetId: names.get($("#attach-target").value),
            type: $("#attach-type").value,
          }),
        });
        await hydrate();
      },
      { help: "relation" },
    );
  } else if (a === "detach-resource") {
    const relationId = el.dataset.relationId;
    modal(
      "Detach resource",
      `<p>Detach <b>${escapeHTML(el.dataset.peerName || "")}</b>? The resource itself is kept; only the link to this host is removed.</p>`,
      "Detach",
      true,
      async () => {
        await api("/api/v1/relations/" + encodeURIComponent(relationId), {
          method: "DELETE",
        });
        await hydrate();
      },
    );
  } else if (a === "create-resource")
    modal(
      "Add unmanaged resource",
      resourceFields({ health: "unknown", type: "node" }),
      "Create resource",
      false,
      async () => {
        await api("/api/v1/resources", {
          method: "POST",
          body: JSON.stringify(resourcePayload()),
        });
        await hydrate();
      },
      { help: "resource" },
    );
  else if (a === "edit-resource") {
    let resource = state.liveResources.find(
      (item) => item.id === el.dataset.resourceId,
    );
    modal(
      `Edit resource · ${resource.name}`,
      resourceFields(resource),
      "Save",
      false,
      async () => {
        await api("/api/v1/resources/" + resource.id, {
          method: "PUT",
          body: JSON.stringify(resourcePayload()),
        });
        await hydrate();
      },
      { help: "resource" },
    );
  } else if (a === "delete-resource")
    modal(
      "Delete unmanaged resource",
      "<p>Relations, group memberships, and stored metrics for this resource will also be removed.</p>",
      "Delete",
      true,
      async () => {
        await api("/api/v1/resources/" + el.dataset.resourceId, {
          method: "DELETE",
        });
        await hydrate();
      },
    );
  else if (a === "create-user")
    modal(
      "Add user",
      `<div class="form-row"><label>USERNAME</label><input id="user-username" placeholder="jsmith" autocomplete="off"></div><div class="form-row"><label>DISPLAY NAME</label><input id="user-display" placeholder="Jane Smith"></div><div class="form-row"><label>PASSWORD</label><input id="user-password" type="password" placeholder="at least 6 characters" autocomplete="new-password"></div><div class="form-row"><label>CONFIRM PASSWORD</label><input id="user-password-confirm" type="password" autocomplete="new-password"></div><div class="form-row"><label>STATUS</label><select id="user-status"><option value="active">Active</option><option value="disabled">Disabled</option></select></div><div class="form-row"><label>ROLE</label><select id="user-role"><option value="">No role (assign later)</option>${state.liveRoles.map((role) => `<option value="${role.id}">${escapeHTML(role.name)}</option>`).join("")}</select></div><div class="form-row"><label>SCOPE</label><select id="user-scope">${state.liveScopes.map((scope) => `<option value="${scope.id}" ${scope.id === "scope-global" ? "selected" : ""}>${escapeHTML(scope.name)}</option>`).join("")}</select></div>`,
      "Create user",
      false,
      async () => {
        if ($("#user-password").value !== $("#user-password-confirm").value)
          throw new Error("Password and confirmation do not match");
        let username = $("#user-username").value;
        await api("/api/v1/users", {
          method: "POST",
          body: JSON.stringify({
            username,
            displayName: $("#user-display").value,
            password: $("#user-password").value,
            status: $("#user-status").value,
          }),
        });
        let roleId = $("#user-role").value;
        if (roleId) {
          await api("/api/v1/role-bindings", {
            method: "POST",
            body: JSON.stringify({
              subjectId: username,
              roleId,
              scopeId: $("#user-scope").value,
            }),
          });
        }
        await hydrate();
      },
      { help: "user" },
    );
  else if (a === "edit-user") {
    let user = state.liveUsers.find((u) => u.id === el.dataset.userId) || {};
    modal(
      "Edit user",
      `<div class="form-row"><label>USERNAME</label><input value="${escapeHTML(user.username || "")}" disabled></div><div class="form-row"><label>DISPLAY NAME</label><input id="user-display" value="${escapeHTML(user.displayName || "")}"></div>${user.username === state.subject ? '<div class="form-row"><label>CURRENT PASSWORD</label><input id="user-current-password" type="password" autocomplete="current-password"><div class="field-hint">Required to change your own password.</div></div>' : ""}<div class="form-row"><label>NEW PASSWORD</label><input id="user-password" type="password" placeholder="leave blank to keep current" autocomplete="new-password"></div><div class="form-row"><label>CONFIRM NEW PASSWORD</label><input id="user-password-confirm" type="password" autocomplete="new-password"></div><div class="form-row"><label>STATUS</label><select id="user-status"><option value="active" ${user.status === "active" ? "selected" : ""}>Active</option><option value="disabled" ${user.status === "disabled" ? "selected" : ""}>Disabled</option></select></div>`,
      "Save changes",
      false,
      async () => {
        const newPassword = $("#user-password").value;
        if (newPassword && newPassword !== $("#user-password-confirm").value)
          throw new Error("New password and confirmation do not match");
        let body = {
          displayName: $("#user-display").value,
          status: $("#user-status").value,
        };
        if (newPassword) {
          body.password = newPassword;
          const current = $("#user-current-password");
          if (current) {
            if (!current.value)
              throw new Error("Enter your current password to change it");
            body.currentPassword = current.value;
          }
        }
        await api(`/api/v1/users/${user.id}`, {
          method: "PUT",
          body: JSON.stringify(body),
        });
        await hydrate();
      },
      { help: "user" },
    );
  } else if (a === "delete-user")
    modal(
      "Delete user",
      "<p>This permanently removes the account and revokes its active sessions.</p>",
      "Delete",
      true,
      async () => {
        await api(`/api/v1/users/${el.dataset.userId}`, { method: "DELETE" });
        await hydrate();
      },
    );
  else if (a === "create-team")
    modal("Add team", teamFields(), "Create team", false, async () => {
      await api("/api/v1/teams", {
        method: "POST",
        body: JSON.stringify(teamPayload()),
      });
      await hydrate();
    });
  else if (a === "edit-team") {
    let team = state.liveTeams.find((t) => t.id === el.dataset.teamId) || {};
    modal("Edit team", teamFields(team), "Save changes", false, async () => {
      await api(`/api/v1/teams/${team.id}`, {
        method: "PUT",
        body: JSON.stringify(teamPayload()),
      });
      await hydrate();
    });
  } else if (a === "delete-team")
    modal(
      "Delete team",
      "<p>This removes the team. User accounts are not affected.</p>",
      "Delete",
      true,
      async () => {
        await api(`/api/v1/teams/${el.dataset.teamId}`, { method: "DELETE" });
        await hydrate();
      },
    );
  else if (a === "create-role")
    modal(
      "Create role",
      roleFields(),
      "Create role",
      false,
      async () => {
        await api("/api/v1/roles", {
          method: "POST",
          body: JSON.stringify(rolePayload()),
        });
        await hydrate();
      },
      { help: "role" },
    );
  else if (a === "edit-role") {
    let role = state.liveRoles.find((x) => x.id === el.dataset.roleId);
    modal(
      role.system ? "System role" : "Edit role",
      roleFields(role),
      role.system ? "Close" : "Save",
      false,
      role.system
        ? null
        : async () => {
            await api("/api/v1/roles/" + role.id, {
              method: "PUT",
              body: JSON.stringify(rolePayload(role)),
            });
            await hydrate();
          },
    );
  } else if (a === "delete-role")
    modal(
      "Delete custom role",
      `<p>This role will be removed after active bindings are reviewed.</p>`,
      "Delete",
      true,
      async () => {
        await api("/api/v1/roles/" + el.dataset.roleId, { method: "DELETE" });
        await hydrate();
      },
    );
  else if (a === "create-scope")
    modal(
      "Create access scope",
      `<div class="form-row"><label>SCOPE NAME</label><input id="scope-name" placeholder="e.g. DC-1 Production"></div>${pathPickerField("scope-path", [], "HIERARCHY PATHS")}${tagSelectorField("scope-tag", {})}`,
      "Create scope",
      false,
      async () => {
        // Comma-separated paths and tag pairs, as in edit-scope.
        await api("/api/v1/scopes", {
          method: "POST",
          body: JSON.stringify({
            name: $("#scope-name").value,
            paths: $("#scope-path")
              .value.split(",")
              .map((path) => path.trim())
              .filter(Boolean),
            tags: keyValues($("#scope-tag").value),
          }),
        });
        await hydrate();
      },
      { help: "scope" },
    );
  else if (a === "edit-scope") {
    let scope = state.liveScopes.find((x) => x.id === el.dataset.scopeId),
      tags = Object.entries(scope.tags || {})
        .map(([k, v]) => `${k}=${v}`)
        .join(", "),
      disabled = scope.system ? "disabled" : "";
    modal(
      scope.system ? "System scope" : "Edit access scope",
      `<div class="form-row"><label>SCOPE NAME</label><input id="scope-name" value="${scope.name}" ${disabled}></div>${pathPickerField("scope-path", scope.paths || [], "HIERARCHY PATHS", disabled)}${tagSelectorField("scope-tag", scope.tags || {})}${scope.system ? '<div class="warning-box">System scopes are immutable.</div>' : ""}`,
      scope.system ? "Close" : "Save",
      false,
      scope.system
        ? null
        : async () => {
            await api("/api/v1/scopes/" + scope.id, {
              method: "PUT",
              body: JSON.stringify({
                ...scope,
                name: $("#scope-name").value,
                paths: $("#scope-path")
                  .value.split(",")
                  .map((x) => x.trim())
                  .filter(Boolean),
                tags: keyValues($("#scope-tag").value),
              }),
            });
            await hydrate();
          },
    );
  } else if (a === "delete-scope")
    modal(
      "Delete access scope",
      `<p>Scopes referenced by role bindings cannot be deleted.</p>`,
      "Delete",
      true,
      async () => {
        await api("/api/v1/scopes/" + el.dataset.scopeId, { method: "DELETE" });
        await hydrate();
      },
    );
  else if (a === "create-binding")
    modal(
      "Create role binding",
      bindingFields(),
      "Create binding",
      false,
      async () => {
        await api("/api/v1/role-bindings", {
          method: "POST",
          body: JSON.stringify(bindingPayload()),
        });
        await hydrate();
      },
      { help: "binding" },
    );
  else if (a === "edit-binding") {
    let binding = state.liveBindings.find((x) => x.id === el.dataset.bindingId);
    modal(
      "Edit role binding",
      bindingFields(binding),
      "Save",
      false,
      async () => {
        await api("/api/v1/role-bindings/" + binding.id, {
          method: "PUT",
          body: JSON.stringify(bindingPayload()),
        });
        await hydrate();
      },
    );
  } else if (a === "delete-binding")
    modal(
      "Delete role binding",
      `<p>Assigned access will be revoked immediately.</p>`,
      "Delete",
      true,
      async () => {
        await api("/api/v1/role-bindings/" + el.dataset.bindingId, {
          method: "DELETE",
        });
        await hydrate();
      },
    );
  else if (a === "test-access") {
    const subjects = [
      ...new Set(
        [
          state.subject,
          ...state.liveUsers.map((u) => u.username),
          ...state.liveBindings.map((b) => b.subjectId),
        ].filter(Boolean),
      ),
    ];
    modal(
      "Access simulator",
      `${selectField("sim-subject", "SUBJECT", subjects, state.subject, { custom: true, skipI18n: true })}${selectField("sim-resource", "RESOURCE", PERMISSION_RESOURCES, "operations", { skipI18n: true })}${selectField("sim-action", "ACTION", PERMISSION_ACTIONS, "create", { skipI18n: true })}${scopeField("sim-scope", state.scopePath, "SCOPE PATH")}<div id="sim-result" class="warning-box muted">Run the check to see the decision.</div>`,
      "Evaluate",
      false,
      async () => {
        const box = $("#sim-result");
        try {
          const decision = await api("/api/v1/access/evaluate", {
            method: "POST",
            body: JSON.stringify({
              subjectId: $("#sim-subject").value.trim(),
              resource: $("#sim-resource").value.trim(),
              action: $("#sim-action").value.trim(),
              resourcePath: $("#sim-scope").value.trim(),
            }),
          });
          if (!box) return true;
          box.className = "warning-box";
          box.innerHTML = `<span class="${decision.allowed ? "ok" : "critical"}">● ${decision.allowed ? "Allowed" : "Denied"}</span> ${escapeHTML(decision.reason || (decision.allowed ? "matched a role binding" : "no binding grants this"))}`;
        } catch (error) {
          if (box) {
            box.className = "warning-box";
            box.innerHTML = `<span class="critical">● Evaluation failed</span> ${escapeHTML(error.message)}`;
          }
        }
        // Stays open for another evaluation.
        return true;
      },
    );
  }
  else if (a === "refresh-data") hydrate();
  else if (a === "delete-agent")
    modal(
      "Remove offline agent",
      `<p>The Agent record, discovered resources, inventory, relations, and metrics will be removed.</p>`,
      "Remove",
      true,
      async () => {
        await api("/api/v1/agents/" + el.dataset.agentId, { method: "DELETE" });
        await hydrate();
      },
    );
  else if (a === "nav-back") history.back();
  else if (a === "ack-alert" || a === "resolve-alert") {
    let alert = state.liveAlerts.find((x) => x.id === el.dataset.alertId),
      status = a === "ack-alert" ? "acknowledged" : "resolved";
    modal(
      `${status[0].toUpperCase() + status.slice(1)} alert`,
      `${selectField("alert-assignee", "ASSIGNEE", knownSubjects(), alert.assignee || state.subject, { custom: true, skipI18n: true })}<div class="form-row"><label>SUMMARY</label><textarea id="alert-summary">${alert.summary || ""}</textarea></div>`,
      "Update alert",
      false,
      async () => {
        await api("/api/v1/alerts/" + alert.id, {
          method: "PUT",
          body: JSON.stringify({
            ...alert,
            status,
            assignee: $("#alert-assignee").value,
            summary: $("#alert-summary").value,
          }),
        });
        await hydrate();
      },
    );
  } else if (a === "delete-alert")
    modal(
      "Delete alert",
      `<p>The alert record will be removed.</p>`,
      "Delete",
      true,
      async () => {
        await api("/api/v1/alerts/" + el.dataset.alertId, { method: "DELETE" });
        await hydrate();
      },
    );
  else if (a === "add-incident-note")
    modal(
      "Add incident note",
      `<div class="form-row"><label>NOTE</label><textarea id="incident-note" placeholder="Investigation finding or mitigation update"></textarea></div>`,
      "Add note",
      false,
      async () => {
        await api("/api/v1/incidents/" + state.selectedIncidentId + "/events", {
          method: "POST",
          body: JSON.stringify({
            type: "note",
            message: $("#incident-note").value,
          }),
        });
        await loadIncidentTimeline(state.selectedIncidentId);
        render();
      },
    );
  else if (a === "change-incident-status") {
    let incident = state.liveIncidents.find(
      (x) => x.id === state.selectedIncidentId,
    );
    modal(
      "Change incident status",
      `<div class="form-row"><label>STATUS</label><select id="incident-status">${["declared", "investigating", "mitigating", "monitoring", "resolved"].map((x) => `<option value="${x}" ${x === incident.status ? "selected" : ""}>${x}</option>`).join("")}</select></div>`,
      "Update status",
      false,
      async () => {
        await api("/api/v1/incidents/" + incident.id, {
          method: "PUT",
          body: JSON.stringify({
            ...incident,
            status: $("#incident-status").value,
          }),
        });
        await hydrate();
        await loadIncidentTimeline(incident.id);
        render();
      },
    );
  } else if (a === "delete-incident")
    modal(
      "Delete incident",
      `<p>The incident and its timeline will be removed.</p>`,
      "Delete",
      true,
      async () => {
        await api("/api/v1/incidents/" + state.selectedIncidentId, {
          method: "DELETE",
        });
        state.selectedIncidentId = null;
        await hydrate();
      },
    );
  else if (a === "create-operation") {
    // Node the action was invoked from.
    const preset = el.dataset.targetId || "";
    const services = [
      ...new Set(
        state.liveInventories.flatMap((report) =>
          (report.data?.services || []).map((service) => service.name),
        ),
      ),
    ].sort();
    let options = state.liveAgents
      .map(
        (x) =>
          `<option value="${x.nodeId}" ${x.nodeId === preset ? "selected" : ""}>${x.hostname} · ${x.nodeId}</option>`,
      )
      .join("");
    modal(
      "Run agent diagnostic",
      `<div class="form-row"><label>TARGET NODE</label><select id="operation-target">${options || "<option>No connected agents</option>"}</select></div><div class="form-row"><label>OPERATION</label><select id="operation-type"><option value="inventory.refresh">Refresh inventory</option><option value="service.status">Check service status</option><option value="service.restart">Restart service</option></select></div>${selectField("operation-service", "SERVICE", services, "", { custom: true, skipI18n: true, hint: "Only used by the service operations." })}<div class="form-row"><label>REASON</label><textarea id="operation-reason" placeholder="Operational reason and related incident"></textarea></div><div class="warning-box">Service restart requires independent approval. Agent allowlist enforcement remains authoritative.</div>`,
      "Run diagnostic",
      false,
      async () => {
        await api("/api/v1/operations", {
          method: "POST",
          body: JSON.stringify({
            type: $("#operation-type").value,
            targetIds: [$("#operation-target").value],
            parameters: $("#operation-type").value.startsWith("service.")
              ? { service: $("#operation-service").value }
              : {},
            reason: $("#operation-reason").value || "Manual diagnostic",
            requestedBy: state.subject,
          }),
        });
        state.page = "jobs";
        await hydrate();
      },
      { help: "operation" },
    );
  } else if (a === "edit-incident") {
    const incident = state.liveIncidents.find(
      (x) => x.id === state.selectedIncidentId,
    );
    if (!incident) return;
    const selected = new Set(incident.resourceIds || []);
    const resourceOptions = state.liveResources
      .map(
        (resource) =>
          `<option value="${escapeHTML(resource.id)}" ${selected.has(resource.id) ? "selected" : ""}>${escapeHTML(resource.name)} · ${escapeHTML(resource.type)}</option>`,
      )
      .join("");
    modal(
      "Edit incident",
      `<div class="form-row"><label>TITLE</label><input id="incident-title" value="${escapeHTML(incident.title || "")}"></div><div class="form-row"><label>SEVERITY</label><select id="incident-severity"><option value="critical" ${incident.severity === "critical" ? "selected" : ""}>Critical</option><option value="warning" ${incident.severity === "warning" ? "selected" : ""}>Warning</option></select></div>${selectField("incident-commander", "COMMANDER", knownSubjects(), incident.commander || "", { custom: true, skipI18n: true })}<div class="form-row"><label>AFFECTED RESOURCES</label>${optionFilter("incident-resources", "Filter resources…")}<select id="incident-resources" multiple size="6" data-i18n-skip>${resourceOptions}</select><div class="field-hint">Hold Ctrl or Cmd to select more than one.</div></div><div class="form-row"><label>DESCRIPTION</label><textarea id="incident-description">${escapeHTML(incident.description || "")}</textarea></div>`,
      "Save incident",
      false,
      async () => {
        await api(`/api/v1/incidents/${incident.id}`, {
          method: "PUT",
          body: JSON.stringify({
            ...incident,
            title: $("#incident-title").value,
            severity: $("#incident-severity").value,
            commander: $("#incident-commander").value,
            description: $("#incident-description").value,
            resourceIds: [...$("#incident-resources").selectedOptions].map(
              (option) => option.value,
            ),
          }),
        });
        await hydrate();
      },
      { help: "incident" },
    );
  } else if (a === "clear-alert-picks") {
    state.alertsPicked = [];
    render();
    return;
  } else if (a === "create-incident" || a === "declare-from-alerts") {
    // Declaring from the bar acts on everything ticked, one alert included.
    // The screen already knows what the incident is about, so the form is
    // filled in from that rather than from the responder's memory at three in
    // the morning.
    const fromAlerts = a === "declare-from-alerts" ? pickedAlerts() : [];
    const seededResources = new Set(fromAlerts.map((x) => x.resourceId).filter(Boolean));
    const seededAlerts = new Set(fromAlerts.map((x) => x.id));
    const seededSeverity = fromAlerts.some((x) => x.severity === "critical")
      ? "critical"
      : fromAlerts.length
        ? "warning"
        : "critical";
    const listed = new Set(state.liveResources.map((resource) => resource.id));
    let resourceOptions = state.liveResources
        .map(
          (resource) =>
            `<option value="${escapeHTML(resource.id)}" ${seededResources.has(resource.id) ? "selected" : ""}>${escapeHTML(resource.name)} · ${escapeHTML(resource.type)}</option>`,
        )
        .join("") +
        // An alert can name a resource that has since gone — a container the
        // kernel killed is exactly the case, and it is the one the incident
        // is about. Dropping it from the list would take it out of the
        // incident's resources, and the timeline pulls activity by resource,
        // so the response would lose sight of the thing that failed.
        [...seededResources]
          .filter((id) => !listed.has(id))
          .map(
            (id) =>
              `<option value="${escapeHTML(id)}" selected>${escapeHTML(id)} · no longer running</option>`,
          )
          .join(""),
      alertOptions = state.liveAlerts
        .filter((alert) => alert.status !== "resolved" || seededAlerts.has(alert.id))
        .map(
          (alert) =>
            `<option value="${escapeHTML(alert.id)}" ${seededAlerts.has(alert.id) ? "selected" : ""}>${escapeHTML(alert.name)} · ${escapeHTML(alert.resourceId)}</option>`,
        )
        .join("");
    modal(
      "Declare incident",
      `<div class="form-row"><label>TITLE</label><input id="incident-title" value="${escapeHTML(incidentTitleFor(fromAlerts))}" placeholder="Customer-facing impact"></div><div class="form-row"><label>SEVERITY</label><select id="incident-severity"><option value="critical" ${seededSeverity === "critical" ? "selected" : ""}>Critical</option><option value="warning" ${seededSeverity === "warning" ? "selected" : ""}>Warning</option></select></div><div class="form-row"><label>AFFECTED RESOURCES</label>${optionFilter("incident-resource", "Filter resources…")}<select id="incident-resource" multiple size="5" data-i18n-skip>${resourceOptions}</select><div class="field-hint">Hold Ctrl or Cmd to select more than one.</div></div><div class="form-row"><label>RELATED ALERTS</label>${optionFilter("incident-alert", "Filter alerts…")}<select id="incident-alert" multiple size="4" data-i18n-skip>${alertOptions || '<option value="" disabled>No open alerts</option>'}</select></div>${selectField("incident-commander", "COMMANDER", knownSubjects(), state.subject, { custom: true, skipI18n: true })}<div class="form-row"><label>DESCRIPTION</label><textarea id="incident-description"></textarea></div>`,
      "Declare incident",
      false,
      async () => {
        const declared = await api("/api/v1/incidents", {
          method: "POST",
          body: JSON.stringify({
            title: $("#incident-title").value,
            severity: $("#incident-severity").value,
            status: "declared",
            commander: $("#incident-commander").value,
            description: $("#incident-description").value,
            resourceIds: [...$("#incident-resource").selectedOptions].map(
              (option) => option.value,
            ),
            alertIds: [...$("#incident-alert").selectedOptions]
              .map((option) => option.value)
              .filter(Boolean),
          }),
        });
        state.alertsPicked = [];
        // Land on the incident rather than on the list. The next thing anyone
        // does is work it, and the list does not say which one is new.
        state.page = "incidents";
        if (declared?.id) {
          state.selectedIncidentId = declared.id;
          state.liveIncidents = [declared, ...state.liveIncidents];
        }
        await hydrate();
        if (declared?.id) await loadIncidentTimeline(declared.id);
        render();
      },
      { help: "incident" },
    );
  } else if (a === "silence")
    modal(
      "Create silence window",
      alertSilenceFields(),
      "Create silence",
      false,
      async () => {
        await api("/api/v1/alert-silences", {
          method: "POST",
          body: JSON.stringify(alertSilencePayload()),
        });
        state.page = "alert-rules";
        await hydrate();
      },
      { help: "silence" },
    );
  else if (a === "edit-alert-silence") {
    let silence = state.liveAlertSilences.find(
      (item) => item.id === el.dataset.silenceId,
    );
    modal(
      "Edit silence window",
      alertSilenceFields(silence),
      "Save",
      false,
      async () => {
        await api("/api/v1/alert-silences/" + silence.id, {
          method: "PUT",
          body: JSON.stringify(alertSilencePayload(silence)),
        });
        await hydrate();
      },
      { help: "silence" },
    );
  } else if (a === "delete-alert-silence")
    modal(
      "Delete silence window",
      "<p>Matching alert rules will resume evaluation immediately.</p>",
      "Delete",
      true,
      async () => {
        await api("/api/v1/alert-silences/" + el.dataset.silenceId, {
          method: "DELETE",
        });
        await hydrate();
      },
    );
  else if (a === "create-alert-rule" || a === "new-rule")
    modal(
      "Create alert rule",
      alertRuleFields(),
      "Create rule",
      false,
      async () => {
        await api("/api/v1/alert-rules", {
          method: "POST",
          body: JSON.stringify(alertRulePayload()),
        });
        state.page = "alert-rules";
        await hydrate();
      },
      { help: "alertRule" },
    );
  else if (a === "edit-alert-rule") {
    let rule = state.liveAlertRules.find((x) => x.id === el.dataset.ruleId);
    modal("Edit alert rule", alertRuleFields(rule), "Save", false, async () => {
      await api("/api/v1/alert-rules/" + rule.id, {
        method: "PUT",
        body: JSON.stringify(alertRulePayload(rule)),
      });
      await hydrate();
    },
      { help: "alertRule" },
    );
  } else if (a === "delete-alert-rule")
    modal(
      "Delete alert rule",
      `<p>The selected rule will stop evaluating new metric samples.</p>`,
      "Delete",
      true,
      async () => {
        await api("/api/v1/alert-rules/" + el.dataset.ruleId, {
          method: "DELETE",
        });
        await hydrate();
      },
    );
  else if (a === "create-inhibition" || a === "edit-inhibition") {
    let item = state.liveAlertInhibitions.find((value) => value.id === el.dataset.id) || {};
    modal(item.id ? "Edit inhibition" : "Create inhibition", inhibitionFields(item), item.id ? "Save" : "Create", false, async () => {
      await api(item.id ? `/api/v1/alert-inhibitions/${item.id}` : "/api/v1/alert-inhibitions", {method:item.id ? "PUT" : "POST",body:JSON.stringify(inhibitionPayload(item))}); await hydrate();
    },
      { help: "inhibition" },
    );
  } else if (a === "delete-inhibition") modal("Delete inhibition", "<p>Suppressed alerts may become eligible for notification immediately.</p>", "Delete", true, async()=>{await api(`/api/v1/alert-inhibitions/${el.dataset.id}`,{method:"DELETE"});await hydrate();});
  else if (a === "create-channel" || a === "edit-channel") {
    let item = state.liveNotificationChannels.find((value) => value.id === el.dataset.id) || {};
    modal(item.id ? "Edit webhook channel" : "Create webhook channel", channelFields(item), item.id ? "Save" : "Create", false, async()=>{await api(item.id?`/api/v1/notification-channels/${item.id}`:"/api/v1/notification-channels",{method:item.id?"PUT":"POST",body:JSON.stringify(channelPayload(item))});await hydrate();},
      { help: "channel" },
    );
  } else if (a === "delete-channel") modal("Delete channel","<p>Channels referenced by a route cannot be deleted.</p>","Delete",true,async()=>{await api(`/api/v1/notification-channels/${el.dataset.id}`,{method:"DELETE"});await hydrate();});
  else if (a === "create-route" || a === "edit-route") {
    let item = state.liveNotificationRoutes.find((value) => value.id === el.dataset.id) || {};
    modal(item.id ? "Edit notification route" : "Create notification route", routeFields(item), item.id ? "Save" : "Create", false, async()=>{await api(item.id?`/api/v1/notification-routes/${item.id}`:"/api/v1/notification-routes",{method:item.id?"PUT":"POST",body:JSON.stringify(routePayload(item))});await hydrate();},
      { help: "route" },
    );
  } else if (a === "delete-route") modal("Delete route","<p>New alerts will no longer be sent through this route.</p>","Delete",true,async()=>{await api(`/api/v1/notification-routes/${el.dataset.id}`,{method:"DELETE"});await hydrate();});
  else if (a === "create-runbook")
    modal(
      "Create runbook",
      runbookFields(),
      "Create runbook",
      false,
      async () => {
        await api("/api/v1/runbooks", {
          method: "POST",
          body: JSON.stringify(runbookPayload()),
        });
        state.page = "runbooks";
        await hydrate();
      },
      { help: "runbook" },
    );
  else if (a === "edit-runbook") {
    let runbook = state.liveRunbooks.find((x) => x.id === el.dataset.runbookId);
    modal("Edit runbook", runbookFields(runbook), "Save", false, async () => {
      await api("/api/v1/runbooks/" + runbook.id, {
        method: "PUT",
        body: JSON.stringify(runbookPayload(runbook)),
      });
      await hydrate();
    });
  } else if (a === "delete-runbook")
    modal(
      "Delete runbook",
      `<p>The runbook definition will be removed. Execution history remains.</p>`,
      "Delete",
      true,
      async () => {
        await api("/api/v1/runbooks/" + el.dataset.runbookId, {
          method: "DELETE",
        });
        await hydrate();
      },
    );
  else if (a === "execute-runbook") {
    let options = state.liveAgents
      .map((x) => `<option value="${x.nodeId}">${x.hostname}</option>`)
      .join("");
    modal(
      "Execute runbook",
      `<div class="form-row"><label>TARGET</label><select id="runbook-target">${options}</select></div><div class="form-row"><label>REASON</label><textarea id="runbook-reason" placeholder="Incident, ticket, or operational reason"></textarea></div><div class="warning-box">Steps execute sequentially. High-risk runbooks require independent approval.</div>`,
      "Start execution",
      false,
      async () => {
        await api("/api/v1/runbooks/" + el.dataset.runbookId + "/execute", {
          method: "POST",
          body: JSON.stringify({
            targetIds: [$("#runbook-target").value],
            reason: $("#runbook-reason").value || "Manual runbook execution",
          }),
        });
        await hydrate();
      },
    );
  } else if (a === "approve-runbook")
    modal(
      "Approve runbook execution",
      `<p>The execution plan and targets were reviewed. Approval will release the first blocked step.</p>`,
      "Approve",
      false,
      async () => {
        await api(
          "/api/v1/runbook-executions/" + el.dataset.executionId + "/approve",
          {
            method: "POST",
          },
        );
        await hydrate();
      },
    );
  else if (a === "view-execution") {
    let execution = state.liveExecutions.find(
      (x) => x.id === el.dataset.executionId,
    );
    if (!execution) return;
    const runbook = state.liveRunbooks.find((r) => r.id === execution.runbookId);
    // The runbook may be gone; the execution recorded its name at run time.
    const runbookLabel =
      runbook?.name ||
      (execution.runbookName
        ? `${execution.runbookName} (deleted)`
        : "Deleted runbook");
    const ops = (execution.operationIds || [])
      .map((id) => state.liveOperations.find((o) => o.id === id))
      .filter(Boolean);
    // One block per step: outcome on the header, payload behind a summary.
    const steps = ops.length
      ? ops
          .map((o) => {
            const payload = prettyJSON(o.result || o.error || "");
            return `<div class="step"><div class="step-head"><span class="mono">${escapeHTML(o.type)}</span><span class="${statusTone(o.status)}"><i class="dot"></i>${statusText(o.status)}</span><span class="mono muted">${formatWhen(o.updatedAt)}</span>${payload ? `<button class="btn btn-sm" data-copy="${escapeHTML(payload)}" style="margin-left:auto">Copy</button>` : ""}</div>${
              payload
                ? `<details class="step-result"><summary>${escapeHTML(summarizeResult(payload))}</summary><pre class="wrap-any scroll-list">${escapeHTML(payload)}</pre></details>`
                : '<div class="step-result muted">No output recorded</div>'
            }</div>`;
          })
          .join("")
      : '<div class="empty">No step operations recorded</div>';
    modal(
      `Execution · ${escapeHTML(runbookLabel)}`,
      `<div class="spec-grid"><div class="spec"><label>Runbook</label><span>${escapeHTML(runbookLabel)}</span></div><div class="spec"><label>Status</label><span class="${statusTone(execution.status)}">${statusText(execution.status)}</span></div><div class="spec"><label>Target</label><span class="mono">${escapeHTML(execution.targetIds.join(", "))}</span></div><div class="spec"><label>Requested by</label><span>${escapeHTML(execution.requestedBy)}</span></div></div><div class="form-row"><label>STEPS EXECUTED</label></div>${steps}`,
      "Close",
      false,
      null,
      { wide: true, viewer: true },
    );
  } else if (a === "create-terminal-session") {
    // A dialog that offers no target because a background refresh has not
    // landed yet is indistinguishable from having no agents at all.
    let agents = state.liveAgents;
    if (!agents.length) {
      try {
        agents = (await api("/api/v1/agents")).items || [];
        state.liveAgents = agents;
      } catch {}
    }
    if (!agents.length) {
      toast("No target available", "No agent is connected to open a session on");
      return;
    }
    let options = agents
      .map((x) => `<option value="${x.nodeId}">${x.hostname}</option>`)
      .join("");
    modal(
      "Request terminal session",
      `<div class="form-row"><label>TARGET</label><select id="terminal-target">${options}</select></div><div class="form-row"><label>ACCESS REASON</label><textarea id="terminal-reason" placeholder="Incident, ticket, or operational reason"></textarea></div><div class="warning-box">Independent approval required. Session input and output will be audited.</div>`,
      "Request session",
      false,
      async () => {
        const created = await api("/api/v1/terminal-sessions", {
          method: "POST",
          body: JSON.stringify({
            targetId: $("#terminal-target").value,
            reason: $("#terminal-reason").value,
          }),
        });
        // The session just asked for is the one the operator wants to be on,
        // whatever else is already open.
        if (created?.id) state.activeTerminalTab = created.id;
        state.terminalDock = "open";
        await hydrate();
      },
      { help: "terminal" },
    );
  } else if (a === "approve-terminal")
    modal(
      "Approve terminal session",
      `<p>Approval will activate this audited terminal session. The requester and approver are recorded separately.</p>`,
      "Approve",
      false,
      async () => {
        state.activeTerminalTab = el.dataset.sessionId;
        state.terminalDock = "open";
        await api(
          "/api/v1/terminal-sessions/" + el.dataset.sessionId + "/approve",
          {
            method: "POST",
          },
        );
        await hydrate();
      },
    );
  else if (a === "close-terminal")
    modal(
      "Close terminal session",
      `<p>The active session will be closed and retained in the audit trail.</p>`,
      "Close session",
      true,
      async () => {
        await api(
          "/api/v1/terminal-sessions/" + el.dataset.sessionId + "/close",
          { method: "POST" },
        );
        await hydrate();
      },
    );
  else if (a === "view-terminal-recording") {
    const sessionId = el.dataset.sessionId;
    const session = state.liveTerminals.find((x) => x.id === sessionId);
    let recording = await api(
      `/api/v1/terminal-sessions/${sessionId}/recording`,
    );
    let url = URL.createObjectURL(
      new Blob([JSON.stringify(recording, null, 2)], {
        type: "application/json",
      }),
    );
    const events = recording.events || [];
    const locale = getLang() === "ko" ? "ko-KR" : "en-US";
    const clock = (ts) => new Date(ts).toLocaleTimeString(locale);
    // Replay: output goes through the same emulator the live session uses, so
    // a redraw reads as what the operator saw rather than as the escape codes
    // that produced it. Control events are dividers and echoed input is
    // dropped. The recording carries no pty width, so a run is replayed at a
    // width wide enough for most output.
    let replaySize = { cols: 100, rows: 24 };
    let blocks = [];
    let run = null;
    const flushRun = () => {
      if (!run) return;
      blocks.push(renderTerminal(run, escapeHTML));
      run = null;
    };
    for (let i = 0; i < events.length; i++) {
      const event = events[i];
      const data = `${event.data || ""}`;
      if (event.direction === "control") {
        flushRun();
        const size = /^screen (\d+)x(\d+)$/.exec(stripAnsi(data).trim());
        if (size) {
          // Not a divider but the geometry the rest was drawn at.
          replaySize = { cols: Number(size[1]), rows: Number(size[2]) };
          continue;
        }
        blocks.push(
          `<div class="term-line"><span class="rec-mark">── ${escapeHTML(stripAnsi(data).trim())} · ${clock(event.timestamp)} ──</span></div>`,
        );
        continue;
      }
      if (event.direction === "output") {
        if (!run) {
          run = createTerminal(replaySize.cols, replaySize.rows);
          run.cursorVisible = false;
        }
        run.write(data);
        continue;
      }
      // Input the shell echoed back is already in the output; showing it twice
      // would read as the operator typing everything twice.
      const next = events[i + 1];
      if (
        next?.direction === "output" &&
        stripAnsi(next.data || "").trim() === stripAnsi(data).trim()
      )
        continue;
      flushRun();
      const label = event.direction === "blocked" ? "rec-mark" : "rec-input";
      blocks.push(
        `<div class="term-line"><span class="${label}">${escapeHTML(stripAnsi(data))}</span></div>`,
      );
    }
    flushRun();
    const screen = blocks.join("");
    modal(
      "Masked terminal recording",
      `<div class="terminal"><div class="terminal-head"><span class="term-dots"><i></i><i></i><i></i></span>${escapeHTML(session?.targetId || sessionId)} — replay<span style="margin-left:auto" class="term-dim">${events.length} events · ${recording.bytes} bytes${recording.truncated ? " · truncated" : ""}</span></div><div class="term-body"><pre class="terminal-screen rec-screen" data-i18n-skip>${screen || '<span class="rec-mark">Nothing was recorded for this session</span>'}</pre></div></div><div class="term-dim" style="margin:10px 0">Expires ${new Date(recording.expiresAt).toLocaleString()}</div><a class="btn" href="${url}" download="terminal-${recording.sessionId}.json">Download JSON</a>`,
      "Close",
      false,
      async () => URL.revokeObjectURL(url),
      { wide: true, viewer: true },
    );
  } else if (a === "delete-terminal-recording")
    modal(
      "Delete terminal recording",
      `<p>This permanently deletes the masked input and output recording.</p>`,
      "Delete recording",
      true,
      async () => {
        await api(`/api/v1/terminal-sessions/${el.dataset.sessionId}/recording`, { method: "DELETE" });
        await hydrate();
      },
    );
  else if (a === "auto-group") {
    let keys = [
      ...new Set(
        state.liveResources.flatMap((r) => Object.keys(r.tags || {})),
      ),
    ]
      .filter((k) => k !== "source")
      .sort();
    if (!keys.length) {
      toast("No labels", "No resource labels are available to group by.");
      return;
    }
    modal(
      "Auto-group by label",
      `<p>Create a dynamic group for each distinct value of a label. Matching resources — including nodes added later — are grouped automatically.</p><div class="form-row"><label>LABEL</label><select id="autogroup-key">${keys.map((k) => `<option value="${escapeHTML(k)}">${escapeHTML(k)}</option>`).join("")}</select></div>`,
      "Create groups",
      false,
      async () => {
        let key = $("#autogroup-key").value;
        let values = [
          ...new Set(
            state.liveResources.map((r) => (r.tags || {})[key]).filter(Boolean),
          ),
        ];
        let existing = new Set(state.liveGroups.map((g) => g.path));
        let created = 0;
        for (const value of values) {
          let path = `labels/${key}/${value}`;
          if (existing.has(path)) continue;
          await api("/api/v1/groups", {
            method: "POST",
            body: JSON.stringify({
              name: value,
              type: "label",
              path,
              mode: "dynamic",
              selector: { [key]: value },
            }),
          });
          created++;
        }
        await hydrate();
        toast(
          "Auto-grouping complete",
          `${created} dynamic group${created === 1 ? "" : "s"} created for label "${key}".`,
        );
      },
    );
  } else if (a === "create-group") {
    let parents = state.liveGroups
      .map((g) => `<option value="${g.id}">${g.name}</option>`)
      .join("");
    modal(
      "Create node group",
      `<div class="form-row"><label>GROUP NAME</label><input id="group-name" placeholder="e.g. Rack-08"></div><div class="form-row"><label>GROUP TYPE</label><select id="group-type"><option value="rack">Physical rack</option><option value="service">Service</option><option value="cluster">Cluster</option><option value="environment">Environment</option></select></div><div class="form-row"><label>PARENT</label><select id="group-parent"><option value="">No parent</option>${parents}</select></div><div class="form-row"><label>PATH</label><input id="group-path" placeholder="production/dc-1/rack-08"></div><div class="form-row"><label>MEMBERSHIP MODE</label><select id="group-mode"><option value="static">Static</option><option value="dynamic">Dynamic</option></select></div>${tagSelectorField("group-selector", {}, "DYNAMIC SELECTOR", "Resources carrying every tag join this group automatically.")}`,
      "Create group",
      false,
      async () => {
        let tags = keyValues($("#group-selector").value);
        await api("/api/v1/groups", {
          method: "POST",
          body: JSON.stringify({
            name: $("#group-name").value,
            type: $("#group-type").value,
            parentId: $("#group-parent").value,
            path: $("#group-path").value,
            mode: $("#group-mode").value,
            selector: tags,
          }),
        });
        state.page = "groups";
        await hydrate();
      },
      { help: "group" },
    );
  } else if (a === "edit-group") {
    let group = state.liveGroups.find((g) => g.id === el.dataset.groupId),
      parents = state.liveGroups
        .filter((g) => g.id !== group.id)
        .map(
          (g) =>
            `<option value="${g.id}" ${g.id === group.parentId ? "selected" : ""}>${g.name}</option>`,
        )
        .join(""),
      selector = Object.entries(group.selector || {})
        .map(([k, v]) => `${k}=${v}`)
        .join(",");
    modal(
      "Edit node group",
      `<div class="form-row"><label>GROUP NAME</label><input id="group-name" value="${group.name}"></div>${selectField("group-type", "GROUP TYPE", ["rack", "service", "cluster", "zone", "label"], group.type, { custom: true, skipI18n: true })}<div class="form-row"><label>PARENT</label><select id="group-parent"><option value="">No parent</option>${parents}</select></div><div class="form-row"><label>PATH</label><input id="group-path" value="${group.path || ""}"></div><div class="form-row"><label>MEMBERSHIP MODE</label><select id="group-mode"><option value="static" ${group.mode !== "dynamic" ? "selected" : ""}>Static</option><option value="dynamic" ${group.mode === "dynamic" ? "selected" : ""}>Dynamic</option></select></div>${tagSelectorField("group-selector", group.selector || {}, "DYNAMIC SELECTOR")}`,
      "Save changes",
      false,
      async () => {
        let tags = keyValues($("#group-selector").value);
        await api("/api/v1/groups/" + group.id, {
          method: "PUT",
          body: JSON.stringify({
            ...group,
            name: $("#group-name").value,
            type: $("#group-type").value,
            parentId: $("#group-parent").value,
            path: $("#group-path").value,
            mode: $("#group-mode").value,
            selector: tags,
          }),
        });
        await hydrate();
      },
      { help: "group" },
    );
  } else if (a === "manage-members") {
    let group = state.liveGroups.find((g) => g.id === el.dataset.groupId),
      existing = state.liveMemberships.filter((m) => m.groupId === group.id),
      existingIDs = new Set(existing.map((m) => m.resourceId));
    let current = existing
      .map((m) => {
        let resource = state.liveResources.find((r) => r.id === m.resourceId);
        return `<div class="relation-node"><span class="resource-icon">${escapeHTML(String(resource?.type || "rs").slice(0, 2).toUpperCase())}</span><div><b>${escapeHTML(resource?.name || m.resourceId)}</b><div class="muted mono" data-i18n-skip>${escapeHTML(resource ? `${t(TYPE_LABELS[resource.type] || resource.type)} · ${resourceIdentity(resource)}` : m.resourceId)}</div></div>${m.id.startsWith("dynamic-") ? '<span class="scope-badge">dynamic</span>' : `<button class="btn btn-sm btn-danger" data-remove-membership="${m.id}">Remove</button>`}</div>`;
      })
      .join("");
    let pool = state.liveResources.filter((r) => !existingIDs.has(r.id));
    // Chips only for the types actually present, in the hierarchy's own order.
    let types = ["node", "hypervisor", "vm", "container", "process"].filter((t) =>
      pool.some((r) => r.type === t),
    );
    let memberFilter = `<div class="filterbar member-filter"><input id="member-search" placeholder="Filter by name, type, or id…" autocomplete="off"><button type="button" class="filter-chip active" data-member-filter="">All ${pool.length}</button>${types
      .map(
        (t) =>
          `<button type="button" class="filter-chip" data-member-filter="${t}">${TYPE_LABELS[t] || t} ${pool.filter((r) => r.type === t).length}</button>`,
      )
      .join("")}<span class="result-count" id="member-count"></span></div>`;
    // The id disambiguates the repeats a fleet is full of -- several processes
    // share a name, and only the id says which host and pid.
    let candidates = pool
      .map(
        (r) =>
          `<label class="relation-node member-option" data-member-type="${escapeHTML(r.type)}" data-member-text="${escapeHTML(`${r.name} ${r.type} ${r.id} ${resourceIdentity(r)}`.toLowerCase())}"><input type="checkbox" data-member-resource value="${escapeHTML(r.id)}"><div><b>${escapeHTML(r.name)}</b><div class="muted mono" data-i18n-skip>${escapeHTML(t(TYPE_LABELS[r.type] || r.type))} · ${escapeHTML(resourceIdentity(r))}</div></div></label>`,
      )
      .join("");
    modal(
      `Manage members · ${group.name}`,
      `${
        group.mode === "dynamic"
          ? `<div class="warning-box">Dynamic membership: ${Object.entries(
              group.selector || {},
            )
              .map(([k, v]) => `${k}=${v}`)
              .join(", ")}</div>`
          : ""
      }<div class="form-row"><label>CURRENT MEMBERS</label>${current || '<div class="muted">No members</div>'}</div>${group.mode !== "dynamic" ? `<div class="form-row"><label>ADD RESOURCES</label>${candidates ? `${memberFilter}<div class="member-list scroll-list">${candidates}</div>` : '<div class="muted">All resources assigned</div>'}</div>` : ""}`,
      "Save",
      false,
      async () => {
        for (let input of document.querySelectorAll(
          "[data-member-resource]:checked",
        ))
          await api("/api/v1/memberships", {
            method: "POST",
            body: JSON.stringify({
              groupId: group.id,
              resourceId: input.value,
            }),
          });
        await hydrate();
      },
    );
  } else if (a === "delete-group")
    modal(
      "Delete node group",
      `<p>The group will be removed. Member resources remain available.</p>`,
      "Delete",
      true,
      async () => {
        await api("/api/v1/groups/" + el.dataset.groupId, { method: "DELETE" });
        await hydrate();
      },
    );
  else if (a === "terminal-dock-toggle") {
    state.terminalDock =
      state.terminalDock === "collapsed" ? "open" : "collapsed";
    render();
  } else if (a === "terminal-dock-size") {
    state.terminalDock = state.terminalDock === "max" ? "open" : "max";
    render();
  } else if (a === "show-terminal-dock") {
    if (state.terminalDock === "collapsed") state.terminalDock = "open";
    render();
  } else if (a === "connect-terminal")
    modal(
      "Request secure terminal session",
      `<div class="form-row"><label>TARGET NODE</label><input value="${state.selectedResourceId || ""}" disabled></div><div class="form-row"><label>ACCESS REASON</label><textarea id="resource-terminal-reason" placeholder="Incident, ticket, or operational reason"></textarea></div><div class="warning-box">Independent approval is required before commands can run.</div>`,
      "Request",
      false,
      async () => {
        const created = await api("/api/v1/terminal-sessions", {
          method: "POST",
          body: JSON.stringify({
            targetId: state.selectedResourceId,
            reason: $("#resource-terminal-reason").value,
          }),
        });
        // The resource page stays put. A shell is asked for to answer a
        // question about what is on screen, and this used to clear the
        // selection and replace the URL with /terminal -- so the answer
        // arrived with the question gone, and Back went to the dashboard.
        if (created?.id) state.activeTerminalTab = created.id;
        state.terminalDock = "open";
        await hydrate();
      },
    );
  else if (a === "export") {
    let auditExport = state.page === "audit",
      columns = auditExport
        ? ["timestamp", "actor", "action", "target", "result", "sourceIp"]
        : ["id", "name", "type", "health", "agentId"],
      rows = auditExport ? state.liveAudit : state.liveResources,
      quote = (value) => `"${String(value ?? "").replaceAll('"', '""')}"`,
      csv = [columns.join(",")]
        .concat(
          rows.map((row) =>
            columns.map((column) => quote(row[column])).join(","),
          ),
        )
        .join("\n"),
      link = document.createElement("a");
    link.href = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
    link.download = `kloudview-${auditExport ? "audit" : "resources"}-${new Date().toISOString().slice(0, 10)}.csv`;
    link.click();
    URL.revokeObjectURL(link.href);
    toast("Export complete", `${rows.length} ${auditExport ? "audit events" : "resources"} exported.`);
  } else if (a === "view-operation") {
    let operation = state.liveOperations.find(
      (item) => item.id === el.dataset.operationId,
    );
    modal(
      `Operation · ${escapeHTML(operation.id)}`,
      `<div class="spec-grid"><div class="spec"><label>Type</label><span>${escapeHTML(operation.type)}</span></div><div class="spec"><label>Status</label><span>${escapeHTML(operation.status)}</span></div><div class="spec"><label>Target</label><span class="mono">${escapeHTML(operation.targetIds.join(", "))}</span></div><div class="spec"><label>Requested by</label><span>${escapeHTML(operation.requestedBy)}</span></div><div class="spec"><label>Attempts</label><span>${operation.attempts || 0}</span></div><div class="spec"><label>Updated</label><span>${new Date(operation.updatedAt).toLocaleString()}</span></div></div><div class="form-row"><label>REASON</label><div>${escapeHTML(operation.reason)}</div></div>${operation.result ? `<div class="form-row"><label>RESULT</label><pre class="term-output">${escapeHTML(operation.result)}</pre></div>` : ""}${operation.error ? `<div class="warning-box">${escapeHTML(operation.error)}</div>` : ""}`,
      "Close",
    );
  } else if (a === "approve-operation") {
    let operation = state.liveOperations.find(
      (item) => item.id === el.dataset.operationId,
    );
    modal(
      "Approve high-risk operation",
      `<div class="warning-box">Approval authorizes ${escapeHTML(operation.type)} on ${escapeHTML(operation.targetIds.join(", "))}. The requester cannot approve their own operation.</div><div class="form-row"><label>REQUESTED BY</label><div>${escapeHTML(operation.requestedBy)}</div></div><div class="form-row"><label>REASON</label><div>${escapeHTML(operation.reason)}</div></div>`,
      "Approve operation",
      false,
      async () => {
        await api(`/api/v1/operations/${encodeURIComponent(operation.id)}/approve`, {
          method: "POST",
        });
        await hydrate();
      },
    );
  }
  else if (a === "notifications") {
    // A bell is expected to list what is pending, not flash a count that is
    // gone in three seconds and cannot be clicked through to anything.
    const active = state.liveAlerts.filter(
      (alert) => alert.status !== "resolved" && alert.status !== "silenced",
    );
    const rows = active
      .slice(0, 12)
      .map(
        (alert) =>
          `<div class="relation-node" data-live-resource="${escapeHTML(alert.resourceId || "")}"><span class="resource-icon">AL</span><div>${escapeHTML(alert.name)}<div class="muted">${escapeHTML(alert.resourceId || "")} · ${formatWhen(alert.updatedAt)}</div></div><span class="status-pill ${alert.severity === "critical" ? "critical" : "warn"}" style="margin-left:auto">${escapeHTML(alert.severity)}</span></div>`,
      )
      .join("");
    modal(
      "Notifications",
      `<div class="card-body scroll-list">${rows || `<div class="empty">No active alert in ${escapeHTML(state.scopePath)}</div>`}</div>${active.length > 12 ? `<div class="list-foot muted">Showing 12 of ${active.length}</div>` : ""}`,
      "View all alerts",
      false,
      () => {
        navTo(() => {
          state.page = "alerts";
          state.alertFilter = "all";
        });
      },
      // Nothing here is being edited, so there is nothing to cancel.
      { viewer: true },
    );
  }
  // Unrecognized action: no-op.
}

function bind() {
  fitScrollLists();
  // A table whose last column holds row controls pins that column, so the
  // controls stay reachable once the table outgrows the viewport. Detected from
  // the markup rather than the header text, which is translated.
  document.querySelectorAll(".content .table").forEach((table) => {
    // A column can be an actions column while every row's control is currently
    // conditioned away, so the header can also declare it.
    const lastCell = table.querySelector("tbody tr td:last-child");
    const declared = !!table.querySelector("thead th:last-child.col-actions");
    table.classList.toggle(
      "sticky-actions",
      declared || !!lastCell?.querySelector("button"),
    );
  });
  // Tooltips without the native title delay: a heatmap cell, and a mark on a
  // trend. Both say a thing the operator is hovering to find out, and a second
  // of nothing reads as the hover having missed.
  let heatTip = $("#heat-tip");
  const showTip = (text, event) => {
    if (!heatTip || !text) return;
    heatTip.textContent = text;
    heatTip.style.display = "block";
    heatTip.style.left =
      Math.min(event.clientX + 12, window.innerWidth - 220) + "px";
    heatTip.style.top = event.clientY + 14 + "px";
  };
  const hideTip = () => {
    if (heatTip) heatTip.style.display = "none";
  };
  document.querySelectorAll(".heat-cells").forEach((grid) => {
    grid.onmousemove = (e) => {
      let cell = e.target.closest(".heat-cell");
      if (cell && cell.dataset.tip) showTip(cell.dataset.tip, e);
      else hideTip();
    };
    grid.onmouseleave = hideTip;
  });
  // Bound per mark rather than per plot: the layer is click-through so the
  // chart underneath stays readable, and an element the pointer cannot hit
  // is never told the pointer left it.
  document.querySelectorAll(".trend-mark[data-tip]").forEach((mark) => {
    mark.onmousemove = (e) => showTip(mark.dataset.tip, e);
    mark.onmouseleave = hideTip;
  });
  document.querySelectorAll("[data-page]").forEach(
    (b) =>
      (b.onclick = () => {
        navTo(() => {
          state.page = b.dataset.page;
          state.selectedResourceId = null;
          state.selectedResource = null;
          state.selectedAgentId = null;
          state.selectedIncidentId = null;
          state.mobileSidebarOpen = false;
        });
        if (state.page === "logs" && LOG_SCOPES[state.logSource])
          loadLogStream().then(render);
      }),
  );
  document.querySelectorAll("[data-metric]").forEach(
    (b) =>
      (b.onclick = () => {
        state.metric = b.dataset.metric;
        render();
      }),
  );
  document.querySelectorAll("[data-heatmap-type]").forEach(
    (b) =>
      (b.onclick = () => {
        state.heatmapType = b.dataset.heatmapType;
        // Asked for by name: keep showing it even when it empties.
        state.heatmapTypePinned = true;
        loadOverview(state.groupBy);
      }),
  );
  document.querySelectorAll("[data-group-type]").forEach(
    (button) =>
      (button.onclick = () => {
        state.groupBy = button.dataset.groupType;
        state.overviewGroup = "all";
        loadOverview(state.groupBy);
      }),
  );
  document.querySelectorAll("[data-overview-group]").forEach(
    (button) =>
      (button.onclick = () => {
        // Narrows what is already loaded; the payload does not change.
        state.overviewGroup = button.dataset.overviewGroup;
        render();
      }),
  );
  document.querySelectorAll("[data-overview-health]").forEach(
    (button) =>
      (button.onclick = () => {
        state.overviewHealth = button.dataset.overviewHealth;
        state.anomaliesOnly = false;
        render();
      }),
  );
  document.querySelectorAll("[data-workload-sort]").forEach(
    (button) =>
      (button.onclick = () => {
        state.workloadSort = button.dataset.workloadSort;
        state.pager.utilWorkloads = 0;
        render();
      }),
  );
  document.querySelectorAll("[data-util-filter]").forEach(
    (button) =>
      (button.onclick = () => {
        state.utilFilter = button.dataset.utilFilter;
        render();
      }),
  );
  document.querySelectorAll("[data-overview-anomalies]").forEach(
    (button) =>
      (button.onclick = () => {
        state.anomaliesOnly = !state.anomaliesOnly;
        render();
      }),
  );
  document.querySelectorAll("[data-resource-filter]").forEach(
    (button) =>
      (button.onclick = () => {
        let filter = button.dataset.resourceFilter;
        if (filter === "all") {
          state.resourceType = "";
          state.resourceHealth = "";
          state.resourceLifecycle = "";
        } else if (filter === "terminated") {
          // Stopped children are hidden until asked for; health does not apply.
          state.resourceLifecycle =
            state.resourceLifecycle === "terminated" ? "" : "terminated";
          if (state.resourceLifecycle) state.resourceHealth = "";
        } else if (filter === "critical") {
          // Health toggle, independent of the type filter.
          state.resourceHealth =
            state.resourceHealth === "critical" ? "" : "critical";
        } else {
          state.resourceType = filter;
        }
        state.resourceOffset = 0;
        loadResources(state.query);
      }),
  );
  document.querySelectorAll("[data-metric-range]").forEach(
    (button) =>
      (button.onclick = async () => {
        state.metricRange = button.dataset.metricRange;
        await loadResourceMetrics(state.selectedResourceId);
        render();
      }),
  );
  document.querySelectorAll("[data-resource-page]").forEach(
    (button) =>
      (button.onclick = () => {
        state.resourceOffset = Math.max(
          0,
          state.resourceOffset +
            (button.dataset.resourcePage === "next"
              ? state.resourcePageSize
              : -state.resourcePageSize),
        );
        loadResources(state.query);
      }),
  );
  document.querySelectorAll("[data-list-page]").forEach(
    (button) =>
      (button.onclick = () => {
        let [key, dir] = button.dataset.listPage.split(":"),
          size = state.listPageSize || 20;
        state.pager[key] = Math.max(
          0,
          (state.pager[key] || 0) + (dir === "next" ? size : -size),
        );
        render();
      }),
  );
  document.querySelectorAll("[data-audit-page]").forEach(
    (button) =>
      (button.onclick = () => {
        state.auditOffset = Math.max(
          0,
          state.auditOffset +
            (button.dataset.auditPage === "next"
              ? state.auditPageSize
              : -state.auditPageSize),
        );
        loadAudit();
      }),
  );
  document.querySelectorAll("[data-tag-query]").forEach(
    (button) =>
      (button.onclick = () => {
        state.page = "infrastructure";
        state.query = button.dataset.tagQuery;
        loadResources(state.query);
      }),
  );
  document.querySelectorAll("[data-action]").forEach((b) => {
    let permission = actionPermissions[b.dataset.action];
    if (permission && !can(...permission)) {
      b.disabled = true;
      b.title = `${state.subject}: permission denied`;
      b.onclick = (e) => {
        e.stopPropagation();
        toast(
          "Permission denied",
          `${permission[0]}:${permission[1]} is not assigned to this identity.`,
        );
      };
    } else
      b.onclick = (e) => {
        e.stopPropagation();
        action(b.dataset.action, b);
      };
  });
  document.querySelectorAll("[data-map-group-by]").forEach(
    (b) =>
      (b.onclick = () => {
        state.mapGroupBy = b.dataset.mapGroupBy;
        render();
      }),
  );
  document.querySelectorAll("[data-agent-tab]").forEach(
    (b) =>
      (b.onclick = () => {
        state.agentTab = b.dataset.agentTab;
        render();
      }),
  );
  document.querySelectorAll("[data-util-tab]").forEach(
    (b) =>
      (b.onclick = () => {
        state.utilTab = b.dataset.utilTab;
        state.pager.utilWorkloads = 0;
        render();
      }),
  );
  document.querySelectorAll("[data-detail-tab]").forEach(
    (b) =>
      (b.onclick = () => {
        state.detailTab = b.dataset.detailTab;
        render();
      }),
  );
  for (const [attr, key, pager] of [
    ["data-terminal-filter", "terminalFilter", "terminals"],
    ["data-exec-filter", "execFilter", "executions"],
  ]) {
    document.querySelectorAll(`[${attr}]`).forEach(
      (b) =>
        (b.onclick = () => {
          const value = b.getAttribute(attr);
          state[key] = state[key] === value ? "all" : value;
          state.pager[pager] = 0;
          render();
        }),
    );
  }
  document.querySelectorAll("[data-service-filter]").forEach(
    (b) =>
      (b.onclick = () => {
        state.serviceFilter = b.dataset.serviceFilter;
        state.pager.services = 0;
        render();
      }),
  );
  document.querySelectorAll("[data-workload-type]").forEach(
    (b) =>
      (b.onclick = () => {
        state.workloadType = b.dataset.workloadType;
        state.pager.utilWorkloads = 0;
        render();
      }),
  );
  document.querySelectorAll("[data-log-source]").forEach(
    (b) =>
      (b.onclick = async () => {
        state.logSource = b.dataset.logSource;
        state.logCapture = null;
        state.pager.logs = 0;
        if (LOG_SCOPES[state.logSource]) await loadLogStream();
        render();
      }),
  );
  // Picking a line keeps it by its own text rather than by its position, so a
  // choice survives paging, filtering, and the ten-second refresh.
  // Picking an alert keeps it by id, so a choice survives the filter chips,
  // paging, and the refresh that rebuilds the table underneath it.
  document.querySelectorAll("[data-alert-pick]").forEach(
    (box) =>
      (box.onchange = () => {
        const id = box.dataset.alertPick;
        state.alertsPicked = box.checked
          ? [...state.alertsPicked, id]
          : state.alertsPicked.filter((x) => x !== id);
        render();
      }),
  );
  document.querySelectorAll("[data-log-pick]").forEach(
    (box) =>
      (box.onchange = () => {
        const raw = box.dataset.logPick;
        state.logPicked = box.checked
          ? [...state.logPicked, raw]
          : state.logPicked.filter((x) => x !== raw);
        render();
      }),
  );
  document.querySelectorAll("[data-log-level]").forEach(
    (b) =>
      (b.onclick = () => {
        state.logLevel =
          state.logLevel === b.dataset.logLevel ? "all" : b.dataset.logLevel;
        state.pager.logs = 0;
        render();
      }),
  );
  const logTarget = $("#log-target");
  if (logTarget)
    logTarget.onchange = async () => {
      state.logTarget = logTarget.value;
      state.logCapture = null;
      if (LOG_SCOPES[state.logSource]) await loadLogStream();
      render();
    };
  const logWindow = $("#log-window");
  if (logWindow && LOG_SCOPES[state.logSource])
    logWindow.onchange = async () => {
      state.logWindowMinutes = Number(logWindow.value) || 60;
      await loadLogStream();
      render();
    };
  document.querySelectorAll("[data-user-status]").forEach(
    (b) =>
      (b.onclick = () => {
        state.userStatus =
          state.userStatus === b.dataset.userStatus ? "all" : b.dataset.userStatus;
        render();
      }),
  );
  for (const [attr, key, pager] of [
    ["data-alert-filter", "alertFilter", "alerts"],
    ["data-incident-filter", "incidentFilter", "incidents"],
  ]) {
    document.querySelectorAll(`[${attr}]`).forEach(
      (b) =>
        (b.onclick = () => {
          const value = b.getAttribute(attr);
          state[key] = state[key] === value ? "all" : value;
          state.pager[pager] = 0;
          render();
        }),
    );
  }
  document.querySelectorAll("[data-op-status]").forEach(
    (b) =>
      (b.onclick = () => {
        state.opStatus = b.dataset.opStatus;
        state.pager.operations = 0;
        render();
      }),
  );
  for (const [selector, key] of [
    ["#op-filter", "opQuery"],
    ["#exec-filter", "execQuery"],
    ["#user-filter", "userQuery"],
    ["#terminal-filter", "terminalQuery"],
    ["#service-filter", "serviceQuery"],
    ["#workload-filter", "workloadQuery"],
    ["#log-filter", "logQuery"],
  ]) {
    const input = $(selector);
    if (!input) continue;
    input.oninput = (event) => {
      const pos = event.target.selectionStart;
      state[key] = event.target.value;
      // A narrowed list starts at its first page; the old offset described a
      // list that no longer exists.
      if (key === "logQuery") state.pager.logs = 0;
      render();
      const again = $(selector);
      if (again) {
        again.focus();
        again.setSelectionRange(pos, pos);
      }
    };
  }
  document.querySelectorAll("[data-map-load]").forEach(
    (b) =>
      (b.onclick = () => {
        state.mapLoad = b.dataset.mapLoad;
        render();
      }),
  );
  let mf = $("#map-filter");
  if (mf)
    mf.oninput = (event) => {
      const pos = event.target.selectionStart;
      state.mapQuery = event.target.value;
      render();
      const again = $("#map-filter");
      if (again) {
        again.focus();
        again.setSelectionRange(pos, pos);
      }
    };
  document.querySelectorAll("[data-map-expand]").forEach(
    (b) =>
      (b.onclick = (e) => {
        e.stopPropagation();
        const id = b.dataset.mapExpand;
        state.mapExpanded.has(id)
          ? state.mapExpanded.delete(id)
          : state.mapExpanded.add(id);
        render();
      }),
  );
  document.querySelectorAll("[data-open-user]").forEach(
    (b) =>
      (b.onclick = (event) => {
        event.stopPropagation();
        navTo(() => {
          state.page = "users";
          state.userQuery = b.dataset.openUser;
        });
      }),
  );
  document.querySelectorAll("[data-live-agent-id]").forEach(
    (b) =>
      (b.onclick = (event) => {
        event.stopPropagation();
        const agent = state.liveAgents.find(
          (x) => x.id === b.dataset.liveAgentId,
        );
        if (!agent) return toast("Agent unavailable", "This agent is no longer registered.");
        navTo(() => {
          state.selectedAgentId = agent.id;
          state.selectedResourceId = null;
          state.selectedResource = null;
        });
      }),
  );
  document.querySelectorAll("[data-live-agent]").forEach(
    (b) =>
      (b.onclick = () =>
        navTo(() => {
          state.selectedAgentId = b.dataset.liveAgent;
        })),
  );
  document.querySelectorAll("[data-live-resource]").forEach(
    (b) =>
      (b.onclick = async (event) => {
        // These sit inside clickable rows; the row action must not also fire.
        event.stopPropagation();
        try {
          const id = b.dataset.liveResource;
          const resource = await api(
            "/api/v1/resources/" + encodeURIComponent(id),
          );
          state.resourceMetrics = [];
          navTo(() => {
            // Whatever detail is open has to give way, or the view does not
            // change: an incident outranks a resource in the render order, so
            // clicking an affected resource updated the address bar and left
            // the incident on screen, which reads as a dead link.
            state.selectedIncidentId = null;
            state.selectedAgentId = null;
            state.selectedResourceId = id;
            state.selectedResource = resource;
            state.detailTab = "overview";
            state.subFilter = "";
            state.subQuery = "";
          });
          await loadResourceMetrics(id);
          render();
        } catch (error) {
          toast("Resource lookup failed", error.message);
        }
      }),
  );
  document.querySelectorAll("[data-incident-event]").forEach(
    (b) =>
      (b.onclick = () => {
        const event = state.incidentEvents.find(
          (x) => x.id === b.dataset.incidentEvent,
        );
        if (!event) return;
        const metadata = Object.entries(event.metadata || {});
        const incident = state.liveIncidents.find(
          (x) => x.id === state.selectedIncidentId,
        );
        modal(
          "Timeline entry",
          `<div class="spec-grid"><div class="spec"><label>Type</label><span class="mono">${escapeHTML(event.type)}</span></div><div class="spec"><label>Actor</label><span>${escapeHTML(event.actor)}</span></div><div class="spec"><label>Recorded</label><span class="mono">${new Date(event.createdAt).toLocaleString(consoleLocale())}</span></div><div class="spec"><label>Entry</label><span class="mono">${escapeHTML(event.id)}</span></div></div><div class="form-row"><label>MESSAGE</label><div>${escapeHTML(event.message)}</div></div>${
            metadata.length
              ? `<div class="form-row"><label>DETAILS</label><div class="spec-grid">${metadata.map(([key, value]) => `<div class="spec"><label>${escapeHTML(key)}</label><span class="mono">${escapeHTML(value)}</span></div>`).join("")}</div></div>`
              : ""
          }<div class="form-row"><label>STATE AT THIS MOMENT</label><div id="event-context" class="muted">Loading…</div></div>`,
          "Close",
          false,
          null,
          { viewer: true },
        );
        // Fetched after the dialog paints so it opens immediately.
        contextAtTime(incident?.resourceIds || [], event.createdAt).then(
          (rows) => {
            const box = $("#event-context");
            if (box) setHTML(box, contextTable(rows));
          },
        );
      }),
  );
  document.querySelectorAll("[data-incident]").forEach(
    (b) =>
      (b.onclick = async () => {
        const id = b.dataset.incident;
        await loadIncidentTimeline(id);
        navTo(() => {
          state.selectedIncidentId = id;
        });
      }),
  );
  bindSearchShortcut();
  let search = $("#global-search");
  if (search) {
    search.onkeydown = (e) => {
      if (e.key === "Escape") {
        e.target.blur();
        return;
      }
      if (e.key === "Enter") {
        const value = e.target.value;
        navTo(() => {
          state.query = value;
          state.resourceOffset = 0;
          state.page = "infrastructure";
        });
        loadResources(state.query);
      }
    };
  }
  let metricWindow = $("#metric-window");
  if (metricWindow) {
    metricWindow.onchange = async () => {
      state.metricMinutes = Number(metricWindow.value);
      try {
        state.liveMetricSeries = (
          await api(
            `/api/v1/metrics/timeseries?minutes=${state.metricMinutes}&bucketSeconds=${state.metricMinutes >= 360 ? 300 : 60}`,
          )
        ).items || [];
        render();
      } catch (error) {
        toast("Metric refresh failed", error.message);
      }
    };
  }
  let rf = $("#resource-filter");
  if (rf) {
    rf.oninput = (e) => {
      state.query = e.target.value;
      state.resourceOffset = 0;
      clearTimeout(filterTimer);
      filterTimer = setTimeout(() => loadResources(state.query), 300);
    };
  }
  let uf = $("#util-filter");
  if (uf) {
    // Client-side filter; caret restored so typing is uninterrupted.
    uf.oninput = (e) => {
      const pos = e.target.selectionStart;
      state.utilQuery = e.target.value;
      render();
      const again = $("#util-filter");
      if (again) {
        again.focus();
        again.setSelectionRange(pos, pos);
      }
    };
  }
  document.querySelectorAll("[data-sub-filter]").forEach(
    (b) =>
      (b.onclick = () => {
        state.subFilter = b.dataset.subFilter === "all" ? "" : b.dataset.subFilter;
        render();
      }),
  );
  let sf = $("#sub-filter");
  if (sf) {
    sf.oninput = (e) => {
      const pos = e.target.selectionStart;
      state.subQuery = e.target.value;
      render();
      const again = $("#sub-filter");
      if (again) {
        again.focus();
        again.setSelectionRange(pos, pos);
      }
    };
  }
  document.querySelectorAll("[data-term-tab]").forEach(
    (b) =>
      (b.onclick = () => {
        state.activeTerminalTab = b.dataset.termTab;
        if (state.terminalDock === "collapsed") state.terminalDock = "open";
        render();
      }),
  );
  let termScreen = $("#terminal-screen");
  // Report the pane's size once it exists.
  //
  // The socket opens before the pane is drawn, so asking at that moment found
  // nothing and the far side kept the size the server guessed when it opened
  // the session: a shell that believed it had 32 rows drawing into a pane with
  // 15, which put vi's status line below the bottom of every session. Asking
  // on each paint is safe because the send is skipped when the size has not
  // changed.
  if (termScreen) sendTerminalSize(terminalSocket);
  if (termScreen && terminalWritable) {
    termScreen.onkeydown = (event) => {
      let bytes = keyBytes(event);
      if (bytes === null) return; // let the browser keep its own shortcuts
      event.preventDefault();
      sendTerminalKeys(bytes);
    };
    // A paste is just a fast typist as far as the pty is concerned.
    termScreen.onpaste = (event) => {
      event.preventDefault();
      let text = event.clipboardData?.getData("text");
      if (text) sendTerminalKeys(text.replace(/\r?\n/g, "\r"));
    };
    // A re-render must not drop the operator out of the editor they are in:
    // the page repaints every ten seconds, and the screen is a plain element
    // that loses focus when it is replaced.
    // The pane is replaced on every repaint, so focus has to be put back by
    // hand. It is not an `autofocus` attribute on either element: that fires
    // whenever the element is inserted, so a repaint mid-command pulled focus
    // to the command box and split what was being typed between the two --
    // "vi /tmp/notes.txt" reached the shell as "vi /tmp/notes" with ".txt"
    // left sitting in the box, and vi opened an empty buffer.
    if (terminalScreenHadFocus) termScreen.focus({ preventScroll: true });
    termScreen.onfocus = () => {
      terminalScreenHadFocus = true;
    };
    termScreen.onblur = () => {
      // A repaint replaces this element, and the browser blurs it on the way
      // out. That is not the operator leaving the shell -- if it counted as
      // one, focus would never be restored and the next keystroke would land
      // on the page instead of the pty.
      if (termScreen.isConnected) terminalScreenHadFocus = false;
    };
  }

  let managedInput = $("#managed-term-input");
  if (managedInput) {
    const send = (data) => {
      if (terminalSocket?.readyState === WebSocket.OPEN) {
        terminalSocket.send(
          JSON.stringify({ type: "input", data: bytesToBase64(data) }),
        );
        return true;
      }
      toast("Terminal unavailable", "The PTY stream is not connected.");
      return false;
    };
    managedInput.oninput = (event) => {
      terminalDraft = event.target.value;
    };
    managedInput.onkeydown = (event) => {
      if (event.key === "c" && event.ctrlKey) {
        // Ctrl+C interrupts the foreground command.
        event.preventDefault();
        send("\x03");
        event.target.value = terminalDraft = "";
        return;
      }
      if (event.key !== "Enter" || !event.target.value.trim()) return;
      let command = event.target.value + "\n";
      event.target.value = "";
      terminalDraft = "";
      send(command);
    };
    managedInput.value = terminalDraft;
    // Focus the command line unless another field has focus.
    let focused = document.activeElement;
    if (!focused || focused === document.body || focused === managedInput) {
      managedInput.focus();
      managedInput.setSelectionRange(
        terminalDraft.length,
        terminalDraft.length,
      );
    }
    // Click on the screen focuses the command line, unless text is selected.
    let shell = managedInput.closest(".terminal");
    if (shell)
      shell.onclick = (event) => {
        if (
          event.target.closest("button") ||
          window.getSelection()?.toString()
        )
          return;
        if (terminalScreenHadFocus) return;
        managedInput.focus();
      };
  }
}

// sendTerminalKeys hands raw bytes to the pty. Unlike a command they are not
// screened, which is why the mode that permits them is a separate grant.
function sendTerminalKeys(data) {
  if (terminalSocket?.readyState !== WebSocket.OPEN) {
    toast("Terminal unavailable", "The PTY stream is not connected.");
    return;
  }
  terminalSocket.send(
    JSON.stringify({ type: "keys", data: bytesToBase64(data) }),
  );
}

// The emulator's own answers to questions the program asked. They are not
// operator input, so they cross in either mode; the server checks the shape.
terminalScreen.reply = (data) => {
  if (terminalSocket?.readyState !== WebSocket.OPEN) return;
  terminalSocket.send(JSON.stringify({ type: "reply", data: bytesToBase64(data) }));
};



function stripAnsi(value) {
  // CSI and single-character escapes; a replay is plain text.
  return `${value}`
    .replace(/\u001b\[[0-9;?]*[ -/]*[@-~]/g, "")
    .replace(/\u001b[@-Z\\-_]/g, "")
    .replace(/\r\n/g, "\n");
}

// One-line description of a step payload, for the collapsed summary.
function summarizeResult(payload) {
  try {
    const parsed = JSON.parse(payload);
    if (parsed && typeof parsed === "object") {
      const facts = ["hostname", "os", "state", "status", "message", "name"]
        .filter((key) => parsed[key] != null)
        .map((key) => `${parsed[key]}`);
      const counts = ["disks", "interfaces", "services", "containers", "processes"]
        .filter((key) => Array.isArray(parsed[key]))
        .map((key) => `${key} ${parsed[key].length}`);
      const summary = [...facts, ...counts].join(" · ");
      if (summary) return summary;
    }
  } catch {}
  const line = payload.split("\n")[0];
  return line.length > 120 ? `${line.slice(0, 120)}…` : line;
}

// Indented JSON for a step result; other payloads pass through.
// Metric history for the selected resource; not part of hydrate().
async function loadResourceMetrics(resourceID) {
  if (!resourceID) {
    state.resourceMetrics = [];
    return;
  }
  const range = METRIC_RANGES.find((item) => item.key === state.metricRange);
  let query = "";
  if (range?.hours) {
    const to = new Date();
    const from = new Date(to.getTime() - range.hours * 3600 * 1000);
    query = `?from=${from.toISOString()}&to=${to.toISOString()}`;
  }
  try {
    const result = await api(
      `/api/v1/resources/${encodeURIComponent(resourceID)}/metrics${query}`,
    );
    // Stale response for a previous selection.
    if (state.selectedResourceId !== resourceID) return;
    state.resourceMetrics = downsampleSeries(result.items || [], 240);
  } catch {
    state.resourceMetrics = [];
  }
}

// One sparkline per metric over the resource's own history.
// Compresses a window to at most `points` buckets, keeping each bucket's peak
// rather than a sample from it, so a spike inside a wide window stays visible.
function downsampleSeries(samples, points) {
  if (samples.length <= points) return samples;
  const first = Date.parse(samples[0].timestamp);
  const span = Date.parse(samples.at(-1).timestamp) - first;
  if (!(span > 0)) return samples.slice(-points);
  const buckets = new Map();
  for (const sample of samples) {
    const index = Math.min(
      points - 1,
      Math.floor(((Date.parse(sample.timestamp) - first) / span) * points),
    );
    const peak = buckets.get(index);
    if (!peak) {
      buckets.set(index, { ...sample });
      continue;
    }
    // Percentages take the bucket maximum; network counters are cumulative, so
    // the latest reading is both the maximum and the right base for a rate.
    peak.cpu = Math.max(Number(peak.cpu || 0), Number(sample.cpu || 0));
    peak.memory = Math.max(Number(peak.memory || 0), Number(sample.memory || 0));
    peak.disk = Math.max(Number(peak.disk || 0), Number(sample.disk || 0));
    peak.networkRx = sample.networkRx;
    peak.networkTx = sample.networkTx;
    peak.timestamp = sample.timestamp;
  }
  return [...buckets.keys()].sort((a, b) => a - b).map((key) => buckets.get(key));
}

// Windows the trend can be read over. Live is the in-memory series; the rest
// are served from retained history.
const METRIC_RANGES = [
  { key: "", label: "Live" },
  { key: "24h", label: "24h", hours: 24 },
  { key: "7d", label: "7d", hours: 24 * 7 },
  { key: "30d", label: "30d", hours: 24 * 30 },
];

function metricRangeChips() {
  return `<span class="chip-row">${METRIC_RANGES.map(
    (range) =>
      `<button class="filter-chip ${state.metricRange === range.key ? "active" : ""}" data-metric-range="${range.key}">${range.label}</button>`,
  ).join("")}</span>`;
}

// A host's installed memory, which is what a process's memory share is of: a
// process was given no allowance of its own to be a share of. It rides along
// with the reading, because the host resource does not carry its own total.
function hostMemoryOf(resource) {
  return Number(resource.attributes?.hostMemoryBytes || 0);
}

// What to say when there is no line to draw.
//
// For most resources an empty trend means the samples have not arrived yet.
// For a process it usually means something else: only the heaviest few dozen
// on a host are sampled, because a host runs thousands and a sample for each
// of them every tick would cost more than the answer is worth. Saying "not
// enough samples yet" there would be a promise that never comes true, so the
// process is told plainly that it is not in the set — and pointed at the
// thing that does have a trend.
function emptyTrendBody(resource) {
  if (resource?.type !== "process") {
    return `<div class="empty">${state.metricRange ? "No samples retained for this window" : "Not enough samples yet"}</div>`;
  }
  if (resource.attributes?.metricsSampledAt) {
    return `<div class="empty">Sampled, but not long enough yet for a line</div>`;
  }
  const owner = trendOwnerOf(resource);
  const unit = resource.attributes?.unit;
  return `<div class="empty"><div>This process is not among the ones sampled for a trend</div><div class="term-dim" style="margin-top:6px">Only the heaviest by CPU and by memory are measured each tick.</div>${unit ? `<div class="mono" style="margin-top:10px" data-i18n-skip>${escapeHTML(unit)}</div>` : ""}${owner ? `<div style="margin-top:10px"><div class="mono" data-i18n-skip>${escapeHTML(owner.name)}</div><button class="btn" style="margin-top:8px" data-live-resource="${escapeHTML(owner.id)}">${owner.kind === "container" ? "See the container's trend" : "See the host's trend"}</button></div>` : ""}</div>`;
}

// Where to send someone looking for a trend a process does not have: the
// container it runs inside if there is one, otherwise the host itself. Both
// are measured every tick.
function trendOwnerOf(resource) {
  const containerId = resource.attributes?.containerId;
  if (containerId) {
    const short = containerId.slice(0, 12);
    const container = state.liveResources.find(
      (x) =>
        x.type === "container" &&
        String(x.attributes?.id || "").startsWith(short),
    );
    if (container) {
      return { id: container.id, name: container.name, kind: "container" };
    }
  }
  const host = state.liveResources.find(
    (x) => x.id === hostOfResource(resource),
  );
  return host ? { id: host.id, name: host.name, kind: "host" } : null;
}

// Operations that only look. They leave no trace in a metric, so putting one
// on a chart would offer an explanation that cannot be true.
// docs/contracts/agent-server.json holds the full list of four.
const READ_ONLY_OPERATIONS = new Set([
  "logs.capture",
  "inventory.refresh",
  "service.status",
]);

// What was done to this resource, and what it was told, as things that can be
// laid over its charts.
//
// A shell session and a job name the node they ran on, not the container they
// were run for. The container's own chart is where its OOM is read, and the
// fix for it was typed into a shell on the host -- so the host's actions
// belong on the container's chart too. The agent is what ties the two: a
// container carries its agent's id, and the agent carries the node's.
function resourceMarks(resource) {
  if (!resource) return [];
  const agent = (state.liveAgents || []).find((a) => a.id === resource.agentId);
  const hosts = new Set([resource.id, agent?.nodeId].filter(Boolean));
  const marks = [];
  for (const session of state.liveTerminals || []) {
    if (!hosts.has(session.targetId)) continue;
    // A request that was never approved opened no shell and ran nothing, so
    // it cannot be what moved the line.
    if (!session.startedAt) continue;
    marks.push({
      at: session.startedAt,
      kind: "shell",
      label: `Shell opened by ${session.requestedBy} on ${session.targetId}`,
    });
  }
  for (const operation of state.liveOperations || []) {
    if (!(operation.targetIds || []).some((id) => hosts.has(id))) continue;
    // A mark claims the line above it might be explained by this, and reading
    // logs or re-reading an inventory cannot explain anything. An operation
    // type the console has not heard of counts as changing something: a new
    // one should appear on the chart and be argued with, not vanish from it.
    if (READ_ONLY_OPERATIONS.has(operation.type)) continue;
    // Stamped where the effect lands, not where it was asked for: a job that
    // queued at 06:40 and ran at 06:44 explains a dip at 06:44.
    marks.push({
      at: operation.finishedAt || operation.createdAt,
      kind: operation.status === "failed" ? "job-failed" : "job",
      label: `${operation.type} ${operation.status} · ${operation.requestedBy}`,
    });
  }
  for (const alert of state.liveAlerts || []) {
    if (alert.resourceId !== resource.id) continue;
    marks.push({
      at: alert.startedAt,
      kind: "alert",
      label: `${alert.name} fired`,
    });
    if (alert.resolvedAt)
      marks.push({
        at: alert.resolvedAt,
        kind: "alert-clear",
        label: `${alert.name} resolved`,
      });
  }
  return marks;
}

// The marks as an overlay, in the chart's own coordinates. HTML rather than
// more SVG: the plot is drawn with preserveAspectRatio="none", which stretches
// a one-pixel vertical rule into a band whose width depends on how wide the
// card happens to be.
function trendMarkLayer(marks) {
  if (!marks.length) return "";
  return `<div class="trend-marks">${marks
    .map(
      (mark) =>
        `<i class="trend-mark ${mark.kind}" style="left:${(mark.x / 8).toFixed(2)}%" data-tip="${escapeHTML(`${formatWhen(new Date(mark.at).toISOString())} · ${mark.label}`)}"></i>`,
    )
    .join("")}</div>`;
}

function resourceTrend(samples, resource) {
  if (!samples || samples.length < 2)
    return card(
      "Trend",
      `<div class="card-body">${emptyTrendBody(resource)}</div>`,
      metricRangeChips(),
    );
  // Network counters are cumulative; the rate is the delta over elapsed time.
  const points = samples.map((sample, i) => {
    const previous = samples[i - 1];
    const seconds = previous
      ? (Date.parse(sample.timestamp) - Date.parse(previous.timestamp)) / 1000
      : 0;
    const delta = previous
      ? Math.max(
          0,
          Number(sample.networkRx || 0) +
            Number(sample.networkTx || 0) -
            (Number(previous.networkRx || 0) + Number(previous.networkTx || 0)),
        )
      : 0;
    return {
      cpu: Number(sample.cpu || 0),
      memory: Number(sample.memory || 0),
      disk: Number(sample.disk || 0),
      network: seconds > 0 ? delta / seconds : 0,
      timestamp: sample.timestamp,
    };
  });
  const netPeak = Math.max(1, ...points.map((p) => p.network));
  const marks = chartMarks(
    resourceMarks(resource),
    points[0].timestamp,
    points.at(-1).timestamp,
  );
  const markLayer = trendMarkLayer(marks);
  const spark = (key, label, color, isPct) => {
    // A percentage is drawn against 0-100 rather than against its own range.
    // Scaling to fit would make eight percent of a CPU look like a crisis and
    // would stop the four charts being comparable to each other; the axis is
    // what makes a low line readable instead.
    const top = isPct ? 100 : netPeak;
    const path = points
      .map((p, i) => {
        const x = (i / (points.length - 1)) * 800;
        const value = Math.max(0, Math.min(top, p[key]));
        const y = 118 - (value / top) * 112;
        return `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(" ");
    const latest = points.at(-1);
    const first = points[0];
    const change = isPct ? latest[key] - first[key] : 0;
    const arrow = change > 0.5 ? "▲" : change < -0.5 ? "▼" : "→";
    const current = isPct
      ? `${latest[key].toFixed(1)}%`
      : `${formatBytes(latest[key])}/s`;
    const axis = chartAxis(top, isPct ? percentTick : rateTick);
    return `<div class="mini-trend"><div class="mini-trend-head"><span><i style="background:${color}"></i>${label}</span><b class="mono">${current}</b>${isPct ? `<span class="mono muted">${arrow} ${change >= 0 ? "+" : ""}${change.toFixed(1)}</span>` : ""}</div><div class="spark-area">${axis}<div class="chart-grid"></div><svg viewBox="0 0 800 120" preserveAspectRatio="none"><path d="${path}" fill="none" stroke="${color}" stroke-width="2"/></svg>${markLayer}</div></div>`;
  };
  const span = formatWhen(points[0].timestamp);
  // Only what was actually measured gets a line. Nothing reads a process's
  // disk or network, so drawing them would be four charts where two of them
  // are a flat zero that means "never asked".
  const series =
    resource?.type === "process"
      ? `${spark("cpu", "CPU", "#629cf6", true)}${spark("memory", "Memory", "#9d85f5", true)}`
      : `${spark("cpu", "CPU", "#629cf6", true)}${spark("memory", "Memory", "#9d85f5", true)}${spark("disk", "Disk", "#f2b84b", true)}${spark("network", "Network", "#45d49b", false)}`;
  return card(
    "Trend",
    `<div class="card-body"><div class="mini-trend-grid">${series}</div></div>`,
    `${marks.length ? `<span class="mark-legend"><i class="trend-mark alert"></i>fired<i class="trend-mark alert-clear"></i>resolved<i class="trend-mark shell"></i>shell<i class="trend-mark job"></i>job</span>` : ""}<span class="muted">${points.length} samples since ${span}</span>${metricRangeChips()}`,
  );
}

// Locale for browser-rendered dates.
function consoleLocale() {
  return getLang() === "ko" ? "ko-KR" : "en-US";
}

// Log timestamp: 24-hour clock, prefixed with the date when not today.
function formatWhen(value) {
  if (!value) return "—";
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return "—";
  const today = new Date().toDateString() === at.toDateString();
  return at.toLocaleString(consoleLocale(), {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
    ...(today ? { second: "2-digit" } : { month: "numeric", day: "numeric" }),
  });
}

// Shared status vocabulary for operations, executions, and terminal sessions.
const statusTone = (status) =>
  ({
    succeeded: "ok",
    active: "ok",
    failed: "critical",
    rejected: "critical",
    running: "info",
    awaiting_approval: "warn",
    approved: "warn",
    closed: "unknown",
    expired: "unknown",
  })[status] || "warn";

const riskText = (risk) =>
  ({ low: "Low risk", medium: "Medium risk", high: "High risk" })[risk] || risk;

const statusText = (status) =>
  ({
    succeeded: "Succeeded",
    failed: "Failed",
    running: "Running",
    awaiting_approval: "Awaiting approval",
    approved: "Approved",
    rejected: "Rejected",
    active: "Active",
    closed: "Closed",
    expired: "Expired",
  })[status] || status;

function prettyJSON(value) {
  let text = `${value}`;
  try {
    const parsed = JSON.parse(text);
    if (parsed && typeof parsed === "object")
      return JSON.stringify(parsed, null, 2);
  } catch {}
  return text;
}

function bytesToBase64(value) {
  let bytes = new TextEncoder().encode(value),
    binary = "";
  for (let byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

// One character cell, measured rather than guessed: the console font differs
// between platforms, and a pty told the wrong size draws its full-screen
// programs off the edge.
function terminalCell(screen) {
  let probe = document.createElement("span");
  probe.textContent = "0".repeat(40);
  probe.style.cssText =
    "position:absolute;visibility:hidden;white-space:pre;font:inherit";
  screen.appendChild(probe);
  let rect = probe.getBoundingClientRect();
  probe.remove();
  let width = rect.width / 40 || 7,
    height = rect.height || 18;
  return { width, height };
}

function terminalSize(screen) {
  let cell = terminalCell(screen);
  return {
    cols: Math.max(20, Math.floor(screen.clientWidth / cell.width)),
    rows: Math.max(6, Math.floor(screen.clientHeight / cell.height)),
  };
}

function paintTerminal() {
  terminalPaintPending = false;
  // Taking or giving up the alternate screen changes what the pane offers --
  // the command box steps aside for a program that wants single keys -- and
  // that is chrome outside the screen element, so it needs a real render.
  // Painting alone would leave the box live until the next periodic refresh,
  // which is ten seconds of a trap.
  const fullScreen = !!terminalScreen.alternate;
  if (fullScreen !== terminalFullScreen) {
    terminalFullScreen = fullScreen;
    // The program is waiting on keys, so put the keyboard where they go --
    // but only into a pane that is on screen. The panel folds, and stealing
    // focus into a folded one would take the keyboard away from the page the
    // operator is actually reading.
    if (fullScreen && terminalDockView(state.terminalDock).showsPane)
      terminalScreenHadFocus = true;
    render();
    return;
  }
  let screen = $("#terminal-screen");
  if (!screen) return;
  let atBottom =
    screen.scrollHeight - screen.scrollTop - screen.clientHeight < 24;
  setHTML(screen, renderTerminal(terminalScreen, escapeHTML));
  // Follow the output unless the operator has scrolled back to read something.
  if (atBottom) screen.scrollTop = screen.scrollHeight;
}

function appendTerminalOutput(value) {
  terminalScreen.write(value);
  // A busy program sends many small chunks; paint once per frame, not once
  // per chunk.
  if (terminalPaintPending) return;
  terminalPaintPending = true;
  requestAnimationFrame(paintTerminal);
}

// Tell the pty how big the screen is, and resize our own grid to match. The
// far side records every size it is told, so an unchanged size is not sent.
//
// What "unchanged" means is per connection, not per browser: a new session
// gets a new pty that knows nothing of what the last one was told. Remembering
// across sockets meant the second session of a page load was never sent its
// size at all, and its shell kept the size the server guessed when it opened
// it -- 32 rows drawn into a pane with 15, cutting the bottom off vi.
let terminalSentSize = "";
function sendTerminalSize(socket, force = false) {
  let screen = $("#terminal-screen");
  if (!screen || !socket || socket.readyState !== WebSocket.OPEN) return;
  let { cols, rows } = terminalSize(screen);
  if (terminalScreen.resize(cols, rows)) paintTerminal();
  let size = `${cols}x${rows}`;
  if (!force && size === terminalSentSize) return;
  terminalSentSize = size;
  socket.send(JSON.stringify({ type: "resize", cols, rows }));
}

async function connectTerminalStream() {
  let actives = state.liveTerminals.filter((s) => s.status === "active"),
    active = activeTerminalSession(actives);
  if (!active) {
    if (terminalSocket) terminalSocket.close();
    terminalSocket = null;
    terminalSessionId = null;
    terminalScreen.reset();
    terminalWritable = false;
    terminalFullScreen = false;
    return;
  }
  // A connect in flight owns the session; a second ticket would duplicate the
  // PTY stream.
  if (terminalSessionId === active.id && (terminalConnecting || terminalSocket))
    return;
  if (terminalSocket) terminalSocket.close();
  if (terminalSessionId !== active.id) {
    terminalScreen.reset();
    terminalFullScreen = false;
  }
  // A fresh pty has been told nothing yet.
  terminalSentSize = "";
  terminalSessionId = active.id;
  state.activeTerminalTab = active.id;
  terminalConnecting = true;
  try {
    let result = await api(
      `/api/v1/terminal-sessions/${active.id}/stream-ticket`,
      { method: "POST", body: "{}" },
    );
    if (terminalSessionId !== active.id) return;
    terminalWritable = !!result.writable;
    let protocol = location.protocol === "https:" ? "wss:" : "ws:";
    // Handlers bind to their own socket, not to the current terminalSocket.
    let socket = new WebSocket(
      `${protocol}//${location.host}/api/v1/terminal-sessions/${active.id}/stream?ticket=${encodeURIComponent(result.ticket)}`,
    );
    terminalSocket = socket;
    socket.onopen = () => {
      if (terminalSocket !== socket) return;
      let status = $("#terminal-stream-status");
      if (status) {
        status.className = "ok";
        status.textContent = t("● Active · Connected");
      }
      sendTerminalSize(socket, true);
    };
    socket.onmessage = (event) => {
      let message = JSON.parse(event.data);
      if (message.type === "output" && message.data) {
        let binary = atob(message.data),
          bytes = Uint8Array.from(binary, (character) =>
            character.charCodeAt(0),
          );
        appendTerminalOutput(terminalDecoder.decode(bytes, { stream: true }));
      } else if (message.type === "error" || message.type === "status") {
        appendTerminalOutput(`\r\n[${message.message}]\r\n`);
      }
    };
    socket.onclose = () => {
      if (terminalSocket !== socket) return;
      // Cleared so the keepalive reconnects.
      terminalSocket = null;
      let status = $("#terminal-stream-status");
      if (status) {
        status.className = "warn";
        status.textContent = t("● Active · Reconnecting");
      }
    };
  } catch (error) {
    toast("Terminal connection failed", error.message);
  } finally {
    terminalConnecting = false;
  }
}
// Server-side page size for the resource table, fitted to the viewport.
// Starting estimates, used before a table exists to measure. measureTablePage()
// corrects them from the rendered table.
function fitResourcePageSize() {
  let rows = Math.floor((window.innerHeight - 336) / 55);
  state.resourcePageSize = Math.max(10, Math.min(200, rows));
}

function fitListPageSize() {
  let rows = Math.floor((window.innerHeight - 380) / 55);
  state.listPageSize = Math.max(6, Math.min(100, rows));
}

// Applies the measured page size once per change, then re-renders (or re-fetches
// for the server-paged resource table).
let pageSizeFitting = false;
function applyMeasuredPageSize() {
  if (pageSizeFitting) return;
  const fitted = measureTablePage();
  if (!fitted) return;
  const detail = state.selectedResourceId || state.selectedAgentId || state.selectedIncidentId;
  const resourcePaged = state.page === "infrastructure" && !detail;
  const auditPaged = state.page === "audit" && !detail;
  const current = resourcePaged
    ? state.resourcePageSize
    : auditPaged
      ? state.auditPageSize
      : state.listPageSize;
  if (Math.abs(fitted - current) < 1) return;
  pageSizeFitting = true;
  if (resourcePaged) {
    state.resourcePageSize = fitted;
    loadResources(state.query).finally(() => (pageSizeFitting = false));
    return;
  }
  if (auditPaged) {
    state.auditPageSize = fitted;
    state.auditOffset = 0;
    loadAudit().finally(() => (pageSizeFitting = false));
    return;
  }
  state.listPageSize = fitted;
  render();
  pageSizeFitting = false;
}

// Attribute keys arrive as the agent named them. The spec grid uppercases its
// labels, which ran the words together: agentVersion read as "AGENTVERSION".
function labelFromKey(key) {
  return String(key)
    .replace(/([a-z\d])([A-Z])/g, "$1 $2")
    .replace(/[_-]+/g, " ")
    .trim();
}

// Bounds each in-card scrolling list to the space left below it, so a long list
// scrolls inside its card instead of pushing the page past the viewport. The
// list's offset depends on what the page put above it, so it is measured.
function fitScrollLists() {
  const content = $(".content");
  if (!content) return;
  const floor =
    content.getBoundingClientRect().bottom -
    parseFloat(getComputedStyle(content).paddingBottom || 0);
  for (const list of document.querySelectorAll(".content .scroll-list")) {
    const box = list.getBoundingClientRect();
    // Whatever the enclosing card draws below the list -- padding, border --
    // measured rather than assumed, since it differs per card.
    const card = list.closest(".card");
    const chrome = card ? card.getBoundingClientRect().bottom - box.bottom : 0;
    list.style.maxHeight =
      Math.max(160, Math.floor(floor - box.top - chrome)) + "px";
  }
}

// Rows that fit between the rendered table body and the bottom of the content
// area. Row height varies with the locale and with two-line cells, so it is
// measured rather than assumed.
function measureTablePage() {
  const content = $(".content");
  if (!content) return null;
  // A page can stack several tables -- Automations lists runbooks above their
  // executions. The paged one is whichever carries the pagination bar, and
  // failing that the last, since the bottom-most table sets the page height.
  // Measuring the first budgeted rows from far too high up the page.
  const bar = content.querySelector(".pagination");
  const bodies = content.querySelectorAll("tbody");
  const body =
    bar?.closest(".card")?.querySelector("tbody") || bodies[bodies.length - 1];
  const row = body?.querySelector("tr");
  if (!row) return null;
  const rowHeight = row.getBoundingClientRect().height;
  if (rowHeight < 12) return null;
  const available =
    content.getBoundingClientRect().bottom -
    body.getBoundingClientRect().top -
    (bar?.getBoundingClientRect().height || 0) -
    16;
  return Math.max(5, Math.floor(available / rowHeight));
}

// Viewport-sized page of `items`, with a bar only when the list overflows.
// A captured timestamp is journald's ISO form, or the bare "Sep 10 10:28:22" a
// plain log file writes. Only the first can be localised; the second is already
// human-readable and is shown as it was written.
function formatCaptureTime(value) {
  if (!value) return "—";
  return value.includes("T") ? formatWhen(value) : value;
}

function pagedList(items, key) {
  let size = state.listPageSize || 20,
    total = items.length,
    maxOffset = Math.max(0, Math.floor((total - 1) / size) * size),
    offset = Math.min(Math.max(0, state.pager[key] || 0), maxOffset),
    slice = items.slice(offset, offset + size),
    bar =
      total > size
        ? `<div class="pagination"><button class="btn btn-sm" data-list-page="${key}:prev" ${offset === 0 ? "disabled" : ""}>Previous</button><span class="mono muted">${offset + 1}–${Math.min(offset + size, total)} of ${total}</span><button class="btn btn-sm" data-list-page="${key}:next" ${offset + size >= total ? "disabled" : ""}>Next</button></div>`
        : "";
  return { slice, bar };
}

async function loadResources(query = "", silent = false) {
  try {
    let parameters = new URLSearchParams({ q: query });
    if (state.resourceType) parameters.set("type", state.resourceType);
    if (state.resourceHealth) parameters.set("health", state.resourceHealth);
    if (state.resourceLifecycle)
      parameters.set("lifecycle", state.resourceLifecycle);
    parameters.set("limit", state.resourcePageSize);
    parameters.set("offset", state.resourceOffset);
    let result = await api("/api/v1/resources?" + parameters.toString());
    state.liveFilteredResources = result.items || [];
    state.liveResourceTotal = result.total ?? state.liveFilteredResources.length;
    if (!silent) render();
  } catch (error) {
    if (!silent) toast("Resource search failed", error.message);
  }
}
// Streamed lines and their severity counts for the selected window.
async function loadLogStream() {
  const parameters = new URLSearchParams({
    minutes: String(state.logWindowMinutes || 60),
    limit: "1000",
  });
  if (state.logTarget) parameters.set("nodeId", state.logTarget);
  try {
    const [lines, counters] = await Promise.all([
      api("/api/v1/logs/lines?" + parameters.toString()),
      api("/api/v1/logs/counters?" + parameters.toString()),
    ]);
    state.liveLogLines = lines.items || [];
    state.liveLogCounters = counters.items || [];
  } catch {
    state.liveLogLines = [];
    state.liveLogCounters = [];
  }
}

// The heatmap shows one kind at a time. Asking for it keeps the cell cap from
// being filled by whatever is most numerous, which is processes.
function overviewQuery(groupType = state.groupBy) {
  const parameters = new URLSearchParams({
    groupType: groupType || "rack",
    cellLimit: "1000",
  });
  // The dashboard's filters are applied where the heatmap renders, and asking
  // the server for a narrowed payload as well only took cells away from
  // everything else that reads it.
  //
  // The type filter was moved here for that reason; group, health and
  // "anomalies first" had stayed behind and did the same damage. Leave the
  // dashboard set to "critical" and open Utilization, and the header reads
  // "TOTAL CPU 71.7%" above a table where all 306 workloads report nothing —
  // because the cells the table reads were filtered out on a page the
  // operator has already left, with nothing on this one saying so.
  //
  // On a large fleet the right answer is a per-resource metric lookup rather
  // than one payload serving every purpose; the cell limit already bounds
  // what comes back.
  return parameters.toString();
}

async function loadOverview(groupType = "rack") {
  try {
    state.liveOverview = await api("/api/v1/overview?" + overviewQuery(groupType));
    render();
  } catch (error) {
    toast("Overview refresh failed", error.message);
  }
}
async function loadAudit() {
  try {
    let parameters = new URLSearchParams({
      limit: state.auditPageSize,
      offset: state.auditOffset,
    });
    let result = await api("/api/v1/audit-events?" + parameters.toString());
    state.liveAudit = result.items || [];
    state.auditOffset = result.offset ?? state.auditOffset;
    state.auditTotal = result.total ?? state.liveAudit.length;
    render();
  } catch (error) {
    toast("Audit refresh failed", error.message);
  }
}
// A refresh that started earlier must not land after one that started later.
// Every page action ends in a hydrate, and the periodic refresh runs one every
// few seconds, so two are regularly in flight at once. Whichever finishes last
// used to win — which meant a slow refresh could reinstate a snapshot taken
// before the operator's own action, and, in the terminal, move the live socket
// to whatever session that older snapshot listed first.
let hydrateGeneration = 0;

async function hydrate() {
  const generation = ++hydrateGeneration;
  const superseded = () => generation !== hydrateGeneration;
  try {
    let [
      agents,
      inventories,
      resources,
      serverGroups,
      memberships,
      relations,
      metrics,
      metricSeries,
      overviewData,
      serverAlerts,
      incidents,
      roles,
      scopes,
      bindings,
      operations,
      rules,
	      silences,
	      inhibitions,
	      notificationChannels,
	      notificationRoutes,
	      notificationDeliveries,
	      runbooks,
      executions,
      terminals,
      audit,
      rollout,
    ] = await Promise.all([
      api("/api/v1/agents"),
      api("/api/v1/inventories"),
      apiCollection("/api/v1/resources"),
      api("/api/v1/groups"),
      api("/api/v1/memberships"),
      api("/api/v1/relations"),
      api("/api/v1/metrics/summary"),
      api(
        `/api/v1/metrics/timeseries?minutes=${state.metricMinutes}&bucketSeconds=${state.metricMinutes >= 360 ? 300 : 60}`,
      ),
      api("/api/v1/overview?" + overviewQuery()),
      apiCollection("/api/v1/alerts"),
      api("/api/v1/incidents"),
      api("/api/v1/roles"),
      api("/api/v1/scopes"),
      api("/api/v1/role-bindings"),
      api("/api/v1/operations"),
      api("/api/v1/alert-rules"),
	      api("/api/v1/alert-silences"),
	      api("/api/v1/alert-inhibitions"),
	      api("/api/v1/notification-channels"),
	      api("/api/v1/notification-routes"),
	      api("/api/v1/notification-deliveries"),
	      api("/api/v1/runbooks"),
      api("/api/v1/runbook-executions"),
      api("/api/v1/terminal-sessions"),
      api(`/api/v1/audit-events?limit=${state.auditPageSize}&offset=${state.auditOffset}`),
      api("/api/v1/agents/rollout"),
    ]);
    if (superseded()) return;
    state.liveAgents = agents.items || [];
    state.liveRollout = rollout || null;
    state.liveInventories = inventories.items || [];
    state.liveResources = resources.items || [];
    // Re-apply the active filter and page so auto-refresh preserves them.
    await loadResources(state.query, true);
    state.liveGroups = serverGroups.items || [];
    state.liveMemberships = memberships.items || [];
    state.liveRelations = relations.items || [];
    state.liveMetrics = metrics;
    state.liveMetricSeries = metricSeries.items || [];
    state.liveOverview = overviewData;
    state.liveAlerts = serverAlerts.items || [];
    state.liveIncidents = incidents.items || [];
    state.liveRoles = roles.items || [];
    state.liveScopes = scopes.items || [];
    state.liveBindings = bindings.items || [];
    // Users and teams need their read permission; others see none.
    state.liveUsers = await api("/api/v1/users")
      .then((r) => r.items || [])
      .catch(() => []);
    state.liveTeams = await api("/api/v1/teams")
      .then((r) => r.items || [])
      .catch(() => []);
    if (superseded()) return;
    state.liveOperations = operations.items || [];
    state.liveAlertRules = rules.items || [];
	    state.liveAlertSilences = silences.items || [];
	    state.liveAlertInhibitions = inhibitions.items || [];
	    state.liveNotificationChannels = notificationChannels.items || [];
	    state.liveNotificationRoutes = notificationRoutes.items || [];
	    state.liveNotificationDeliveries = notificationDeliveries.items || [];
	    state.liveRunbooks = runbooks.items || [];
    state.liveExecutions = executions.items || [];
    state.liveTerminals = terminals.items || [];
    connectTerminalStream();
    state.liveAudit = audit.items || [];
    state.auditOffset = audit.offset ?? state.auditOffset;
    state.auditTotal = audit.total ?? state.liveAudit.length;
    state.liveUtilization = await api("/api/v1/utilization").catch(() => null);
    if (superseded()) return;
    let checks = permissionChecks();
    let decisions = await Promise.all(
      checks.map((key) => {
        let [resource, action] = key.split(":");
        return api("/api/v1/access/evaluate", {
          method: "POST",
          body: JSON.stringify({
            subjectId: state.subject,
            resource,
            action,
            resourcePath: state.scopePath,
          }),
        });
      }),
    );
    state.permissions = Object.fromEntries(
      checks.map((key, i) => [key, decisions[i].allowed]),
    );
    state.apiOnline = true;
    state.lastSyncAt = Date.now();
  } catch {
    state.apiOnline = false;
  }
  if (state.selectedResourceId)
    await loadResourceMetrics(state.selectedResourceId);
  if (state.page === "logs" && LOG_SCOPES[state.logSource]) await loadLogStream();
  render();
}
document.documentElement.lang = getLang();
applyTheme(getTheme());
boot();
setInterval(() => {
  if (state.auth?.authenticated && state.apiOnline) connectTerminalStream();
}, 3000);

// The log stream refreshes faster than the rest of the console, and only while
// it is on screen: a login should appear seconds after it happens, and the
// general refresh below is too slow to read as live.
setInterval(async () => {
  if (!state.auth?.authenticated || !state.apiOnline) return;
  if (state.page !== "logs" || !LOG_SCOPES[state.logSource]) return;
  if (document.hidden || document.querySelector(".modal")) return;
  // Re-rendering under a focused control would take the caret with it.
  const active = document.activeElement;
  if (active && ["INPUT", "TEXTAREA", "SELECT"].includes(active.tagName)) return;
  await loadLogStream();
  render();
}, 3000);

// Auto-refresh in place. Skipped while a modal is open, the tab is hidden, or
// a field has focus.
setInterval(() => {
  if (!state.auth?.authenticated) return;
  if (document.hidden || document.querySelector(".modal")) return;
  let active = document.activeElement;
  if (active && ["INPUT", "TEXTAREA", "SELECT"].includes(active.tagName)) return;
  hydrate();
}, 10000);

async function boot() {
  try {
    history.replaceState({ depth: 0 }, "");
  } catch {}
  try {
    state.auth = await api("/api/v1/auth/me");
  } catch {
    state.auth = { authenticated: false };
  }
  if (!state.auth.authenticated) {
    renderLogin();
    return;
  }
  if (state.auth.subject) state.subject = state.auth.subject;
  fitResourcePageSize();
  fitListPageSize();
  // Route from the address bar before the first paint.
  await applyLocation();
  render();
  hydrate();
}

let resizeTimer;
window.addEventListener("resize", () => {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(() => {
    let previous = state.resourcePageSize,
      previousList = state.listPageSize;
    fitResourcePageSize();
    fitListPageSize();
    // A narrower window is a narrower pty: the far side re-wraps its output.
    sendTerminalSize(terminalSocket);
    if (
      previous !== state.resourcePageSize &&
      state.page === "infrastructure" &&
      state.apiOnline
    ) {
      state.resourceOffset = 0;
      loadResources(state.query);
    } else if (previousList !== state.listPageSize) {
      render();
    }
  }, 300);
});

function renderLogin(message = "") {
  setHTML(
    $("#app"),
    `<div class="login-screen"><form class="login-card" id="login-form">
      <div class="login-brand"><span class="brand-mark">KV</span> KloudView</div>
      <div class="login-sub">Sign in to the operations console</div>
      <div class="form-row"><label>USERNAME</label><input id="login-username" autocomplete="username" autofocus></div>
      <div class="form-row"><label>PASSWORD</label><input id="login-password" type="password" autocomplete="current-password"></div>
      <div class="login-error">${escapeHTML(message)}</div>
      <button class="btn btn-primary" type="submit" style="width:100%">Sign in</button>
    </form></div>`,
  );
  $("#login-form").onsubmit = async (event) => {
    event.preventDefault();
    try {
      await api("/api/v1/auth/login", {
        method: "POST",
        body: JSON.stringify({
          username: $("#login-username").value,
          password: $("#login-password").value,
        }),
      });
      boot();
    } catch {
      renderLogin("Invalid username or password");
    }
  };
}
