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
