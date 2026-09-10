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
