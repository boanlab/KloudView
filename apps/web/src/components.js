import { getLang } from "./i18n.js";
import { getTheme } from "./theme.js";
import { escapeHTML } from "./ui.js";

// Feather-style inline SVG icon set (currentColor stroke, no external deps).
const ICON_PATHS = {
  overview:
    '<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/>',
  alerting:
    '<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/>',
  incidents:
    '<path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>',
  infra:
    '<rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/>',
  groups:
    '<path d="M12 2 3 7l9 5 9-5-9-5z"/><path d="M3 17l9 5 9-5"/><path d="M3 12l9 5 9-5"/>',
  inventory:
    '<path d="M8 4H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2h-2"/><rect x="8" y="2" width="8" height="4" rx="1"/><line x1="8" y1="11" x2="16" y2="11"/><line x1="8" y1="15" x2="14" y2="15"/>',
  fleet:
    '<rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><line x1="9" y1="1" x2="9" y2="4"/><line x1="15" y1="1" x2="15" y2="4"/><line x1="9" y1="20" x2="9" y2="23"/><line x1="15" y1="20" x2="15" y2="23"/><line x1="20" y1="9" x2="23" y2="9"/><line x1="20" y1="14" x2="23" y2="14"/><line x1="1" y1="9" x2="4" y2="9"/><line x1="1" y1="14" x2="4" y2="14"/>',
  terminal:
    '<polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/>',
  runbooks:
    '<circle cx="12" cy="12" r="10"/><polygon points="10 8 16 12 10 16 10 8"/>',
  jobs: '<polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>',
  access: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>',
  audit:
    '<path d="M3 3v5h5"/><path d="M3.05 13A9 9 0 1 0 6 5.3L3 8"/><path d="M12 7v5l4 2"/>',
  settings:
    '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>',
  notifications:
    '<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/>',
  download:
    '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>',
  activity: '<polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>',
};

export function icon(name) {
  const path = ICON_PATHS[name];
  if (!path) return "";
  return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${path}</svg>`;
}

export function renderShell(content, state, navigation) {
  const label = (name) => `<span class="nav-text">${name}</span>`;
  const navigationHTML = navigation
    .map((item) => {
      if (item.label) return `<div class="nav-label">${item.label}</div>`;
      const active =
        state.page === item.page ||
        (item.tabs && item.tabs.some((t) => t[0] === state.page));
      return `<button class="nav-item ${active ? "active" : ""}" data-page="${item.page}"><span class="nav-icon">${icon(item.icon)}</span>${label(item.name)}${item.badge ? `<span class="nav-badge">${item.badge}</span>` : ""}</button>`;
    })
    .join("");
  const sidebarLabel = state.sidebarCollapsed ? "Open" : "Close";
  const pending = (state.liveAlerts || []).filter(
    (alert) => alert.status !== "resolved" && alert.status !== "silenced",
  );
  const unread = pending.length;
  const unreadCritical = pending.some((alert) => alert.severity === "critical");
  return `<div class="app ${state.sidebarCollapsed ? "sidebar-collapsed" : ""} ${state.mobileSidebarOpen ? "mobile-sidebar-open" : ""}"><aside class="sidebar"><div class="brand"><span class="brand-mark">KV</span> KloudView</div>
  ${navigationHTML}
  <div class="sidebar-foot">${
    state.userMenuOpen
      ? `<div class="menu-backdrop" data-action="close-user-menu"></div><div class="user-menu"><button class="user-menu-item" data-action="edit-profile">Edit profile</button><button class="user-menu-item" data-action="settings">Console settings</button>${state.auth?.source === "session" ? `<button class="user-menu-item danger" data-action="logout">Logout</button>` : `<button class="user-menu-item" data-action="show-login">Login</button>`}</div>`
      : ""
  }<button class="user-trigger ${state.userMenuOpen ? "open" : ""}" data-action="user-menu" aria-haspopup="true" aria-expanded="${state.userMenuOpen ? "true" : "false"}"><div class="avatar">${escapeHTML((state.auth?.user?.displayName || state.auth?.subject || "KV").slice(0, 2).toUpperCase())}</div><div class="user-id"><span class="user-name">${escapeHTML(state.auth?.user?.displayName || state.auth?.subject || "Not signed in")}</span><small>${state.auth?.source === "session" ? "Signed in" : "Header identity"}</small></div><span class="user-caret">⌄</span></button></div></aside>
  <main class="main"><header class="topbar"><button class="icon-btn sidebar-toggle" data-action="toggle-sidebar" title="${sidebarLabel} sidebar" aria-label="${sidebarLabel} sidebar">☰</button>${state.navStack && state.navStack.length ? `<button class="btn btn-sm topbar-back" data-action="nav-back" title="Back" aria-label="Back"><span data-i18n-skip>←</span> Back</button>` : ""}<div class="search"><input id="global-search" placeholder="Search nodes, VMs, IPs, containers…" value="${escapeHTML(state.searchDraft)}"><span class="key">⌘ K</span></div><div class="top-actions"><span class="live ${state.apiOnline ? "" : "stale"}" title="${state.lastSyncAt ? `Last update ${new Date(state.lastSyncAt).toLocaleTimeString()}` : "Waiting for the first update"}">${state.apiOnline ? "LIVE" : "OFFLINE"}</span><button class="icon-btn" data-action="toggle-theme" title="Theme" aria-label="Theme">${getTheme() === "light" ? "☾" : "☀"}</button><button class="icon-btn lang-toggle" data-action="toggle-lang" title="Language" aria-label="Language">${getLang() === "ko" ? "한" : "EN"}</button>${/* A bell with nothing on it gives no reason to press it; the count is the
      whole point of the control. */ ""}<button class="icon-btn notif-btn" data-action="notifications" aria-label="Notifications">${icon("notifications")}${unread ? `<span class="notif-dot ${unreadCritical ? "critical" : ""}">${unread > 9 ? "9+" : unread}</span>` : ""}</button></div></header><section class="content">${content}</section></main></div>`;
}

// verbatim marks a title that is somebody's data - a hostname, an incident
// someone named - rather than one of the console's own headings. Title Case is
// right for "Node Groups" and wrong for "db-01", which is an identifier.
export function pageHead(title, subtitle, actions = "", verbatim = false) {
  return `<div class="page-head"><div><h1 class="page-title${verbatim ? " verbatim" : ""}">${title}</h1><div class="page-sub">${subtitle}</div></div><div class="head-actions">${actions}</div></div>`;
}

// `meter` turns the tile into a capacity gauge: a fill proportional to the
// value with a tick at the saturation threshold, so "is this full?" is
// answered by shape before the number is read.
export function kpi(label, value, footer, className = "", glow = "", meter = null) {
  const gauge =
    meter == null
      ? ""
      : `<div class="kpi-meter"><i style="width:${Math.max(0, Math.min(100, Number(meter) || 0))}%"></i><b style="left:85%"></b></div>`;
  return `<div class="kpi" style="--glow:${glow}"><div class="kpi-label">${label}</div><div class="kpi-value ${className}">${value}</div>${gauge}<div class="kpi-foot">${footer}</div></div>`;
}

export function card(title, body, tools = "", className = "") {
  return `<div class="card ${className}"><div class="card-head"><div class="card-title">${title}</div>${tools ? `<div class="card-tools">${tools}</div>` : ""}</div>${body}</div>`;
}
