import { translateFragment } from "./i18n.js";

export const escapeHTML = (value) =>
  String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");

export function setHTML(target, html) {
  const template = document.createElement("template");
  template.innerHTML = html;
  template.content
    .querySelectorAll("script, iframe, object, embed, base, meta")
    .forEach((node) => node.remove());
  template.content.querySelectorAll("*").forEach((node) => {
    for (const attribute of [...node.attributes]) {
      const name = attribute.name.toLowerCase();
      const value = attribute.value.trim().toLowerCase();
      if (
        name.startsWith("on") ||
        name === "srcdoc" ||
        (["href", "src", "action", "formaction"].includes(name) &&
          value.startsWith("javascript:"))
      ) {
        node.removeAttribute(attribute.name);
      }
    }
  });
  translateFragment(template.content);
  target.replaceChildren(template.content.cloneNode(true));
}

// What the docked terminal offers in a given panel mode.
//
// The panel is the only place the pty is ever drawn, so `showsPane` is also
// the answer to "is this session on screen at all": the paint path and the
// focus restore both hang off it, and a program that grabs the alternate
// screen must not pull the keyboard into a panel that is folded away.
//
// A stored mode that is not one of the three folds to the bar. That is the
// one state that cannot cover the page underneath, so it is the safe landing
// for a value from an older console or a hand-edited store.
export function terminalDockView(mode) {
  const resolved = mode === "open" || mode === "max" ? mode : "collapsed";
  const folded = resolved === "collapsed";
  return {
    mode: resolved,
    showsPane: !folded,
    foldLabel: folded ? "Show terminal" : "Collapse terminal",
    foldGlyph: folded ? "▴" : "▾",
    sizeLabel: resolved === "max" ? "Restore panel" : "Expand panel",
    sizeGlyph: resolved === "max" ? "⤡" : "⤢",
  };
}

// What the operator sees where a page should have been.
//
// A console that goes blank says nothing about whether the server is down,
// the session expired, or one view has a bug -- and with the shell gone there
// is no way to reach a page that still works. So the failure is reported in
// the content area and everything around it stays usable.
//
// The error text is escaped like any other interpolation: it carries whatever
// the failing data held, and that data comes off the wire.
export function failureCard(error) {
  const detail = String(error?.stack || error?.message || error);
  return `<div class="card"><div class="card-head"><div class="card-title">This page could not be drawn</div></div><div class="card-body"><p>The rest of the console still works — use the sidebar to move to another page. If this page keeps failing, reload with a fresh copy of the console (Ctrl+Shift+R).</p><pre class="wrap-any mono muted" data-i18n-skip>${escapeHTML(detail)}</pre></div></div>`;
}

// Which tier the heatmap opens on. An untouched heatmap opens on a tier that
// has something in it — a fleet of hypervisors has no node-typed resource at
// all — and a tier the operator picked stays picked even when it empties.
//
// `tiers` is [{ type, count }] in display order, counted under the filters the
// dashboard already has set.
export function heatmapTier(tiers, chosen, pinned) {
  if (pinned) return chosen;
  if (tiers.some((tier) => tier.type === chosen && tier.count > 0))
    return chosen;
  const populated = tiers.find((tier) => tier.count > 0);
  return populated ? populated.type : chosen;
}

// The noun the alert selection bar uses for a count.
//
// Plural because one is the ordinary case: an operator declaring a single
// alert ticks one box, and "1 alerts selected" under their cursor reads as a
// bug in the thing they are about to trust with an outage.
//
// It is a phrase on its own rather than an assembled sentence so that the
// dictionary can translate it: i18n maps the exact text of a node, and a node
// holding a number no one has seen before matches nothing.
export function alertPickNoun(count) {
  return count === 1 ? "alert selected" : "alerts selected";
}

// How much a mark explains, most first. A cluster is shown in the colour of
// the most telling thing in it: a restart that sits inside the minute an
// alert cleared should not be read as "an alert cleared, and also something".
const MARK_RANK = ["alert", "alert-clear", "job-failed", "shell", "job"];

// Where an action falls on a chart drawn from `first` to `last`, so a trend
// says when the doing happened as well as what followed.
//
// x comes back in the chart's own 0-800 viewBox, so a mark lines up with the
// point above it. Anything outside the window is dropped rather than pinned
// to an edge, where it would claim a time it did not happen at.
//
// Marks closer together than `gap` merge into one that says how many and what
// they were. `gap` is in viewBox units: the trend plots are about 200px wide
// for those 800 units, and the default 24 is roughly six pixels, the closest
// two marks can be and still read as two.
export function chartMarks(marks, first, last, gap = 24) {
  const from = Date.parse(first);
  const to = Date.parse(last);
  if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from) return [];
  const placed = (marks || [])
    .map((mark) => ({ ...mark, at: Date.parse(mark.at) }))
    .filter(
      (mark) => Number.isFinite(mark.at) && mark.at >= from && mark.at <= to,
    )
    .sort((a, b) => a.at - b.at)
    .map((mark) => ({ ...mark, x: ((mark.at - from) / (to - from)) * 800 }));
  if (gap <= 0) return placed.map((mark) => ({ ...mark, count: 1 }));

  const clusters = [];
  for (const mark of placed) {
    const open = clusters[clusters.length - 1];
    if (open && mark.x - open[0].x < gap) open.push(mark);
    else clusters.push([mark]);
  }
  return clusters.map((members) => {
    // The cluster sits where it began, which is the moment worth reading.
    const [head] = members;
    if (members.length === 1) return { ...head, count: 1 };
    const kind = MARK_RANK.find((rank) =>
      members.some((member) => member.kind === rank),
    );
    // Distinct labels: five shells opened by the same person say one thing,
    // and repeating it five times buries the alert that cleared among them.
    const distinct = [...new Set(members.map((member) => member.label))];
    const named = distinct.slice(0, 3);
    const rest = distinct.length - named.length;
    return {
      at: head.at,
      x: head.x,
      kind: kind || head.kind,
      count: members.length,
      label: `${members.length} events · ${named.join(" · ")}${rest ? ` · +${rest} more` : ""}`,
    };
  });
}

export function statusClass(status) {
  if (status === "Critical") return "critical";
  if (status === "Warning" || status === "Degraded") return "warn";
  if (status === "Unknown" || status === "Offline") return "unknown";
  return "ok";
}

export function formatBytes(value) {
  let size = Number(value || 0);
  const units = ["B", "KB", "MB", "GB", "TB"];
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit++;
  }
  return `${size.toFixed(unit ? 1 : 0)} ${units[unit]}`;
}

export function localDateTime(value) {
  const date = new Date(value);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);
}

export function keyValues(value) {
  return Object.fromEntries(
    value
      .split(",")
      .map((item) => item.trim())
      .filter((item) => item.includes("="))
      .map((item) => item.split("=").map((part) => part.trim()))
      .filter(([key, itemValue]) => key && itemValue),
  );
}

// A title the responder can recognise a week later, built from what fired.
//
// One alert names itself and its resource. Several on one resource name the
// resource; several across resources say how wide it reached, because that is
// the fact that decides how the response gets run — and it is the thing a
// responder typing a title from memory at three in the morning is least
// likely to get right.
export function incidentTitleFor(alerts) {
  if (!alerts || !alerts.length) return "";
  const resources = [...new Set(alerts.map((alert) => alert.resourceId).filter(Boolean))];
  if (alerts.length === 1) {
    return `${alerts[0].name} on ${alerts[0].resourceId || "an unnamed resource"}`;
  }
  if (resources.length === 1) {
    return `${alerts.length} alerts on ${resources[0]}`;
  }
  return `${alerts.length} alerts across ${resources.length} resources`;
}
