import { state } from "./state.js";

const baseURL = "";

export async function api(path, options = {}) {
  let response;
  try {
    response = await fetch(baseURL + path, {
      ...options,
      headers: {
        "Content-Type": "application/json",
        "X-KloudView-Subject": state.subject,
        "X-KloudView-Scope": state.scopePath,
        ...(options.headers || {}),
      },
    });
  } catch (error) {
    // Nothing answered: the only condition that counts as an outage.
    state.apiOnline = false;
    throw error;
  }
  // Any answer, including a refusal, proves the server is up.
  state.apiOnline = true;
  state.lastSyncAt = Date.now();
  // A session that ended elsewhere, to be told apart from an outage.
  if (response.status === 401 && !path.startsWith("/api/v1/auth/login")) {
    window.dispatchEvent(new CustomEvent("kloudview:session-ended"));
  }
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    const error = new Error(payload?.error?.message || `API ${response.status}`);
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
