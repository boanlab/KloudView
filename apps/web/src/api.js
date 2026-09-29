import { state } from "./state.js";

const baseURL = "";

export async function api(path, options = {}) {
  const response = await fetch(baseURL + path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      "X-KloudView-Subject": state.subject,
      "X-KloudView-Scope": state.scopePath,
      ...(options.headers || {}),
    },
  });
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    const error = new Error(payload?.error?.message || `API ${response.status}`);
    // A record that is gone and a server that is broken need different words.
    error.status = response.status;
    throw error;
  }
  return response.status === 204 ? null : response.json();
}

export async function apiCollection(path, pageSize = 500) {
  const items = [];
  let cursor = "";
  let total = 0;
  do {
    const separator = path.includes("?") ? "&" : "?";
    const page = await api(
      `${path}${separator}limit=${pageSize}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
    );
    items.push(...(page.items || []));
    total = page.total ?? items.length;
    cursor = page.nextCursor || "";
    if (!cursor) break;
  } while (items.length < total);
  return { items, total };
}
