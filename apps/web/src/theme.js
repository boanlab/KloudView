// Light/dark theme control for the KloudView web console.
//
// The dark theme is the default (defined by the base :root variables in
// styles.css). Selecting light stamps data-theme="light" on the root element,
// which activates the light-palette overrides. The choice is persisted so it
// survives reloads.

const STORAGE_KEY = "kv-theme";

export function getTheme() {
  try {
    if (typeof localStorage !== "undefined") {
      const value = localStorage.getItem(STORAGE_KEY);
      if (value === "light" || value === "dark") return value;
    }
  } catch {
    // Storage unavailable; fall back to the default dark theme.
  }
  return "dark";
}

// Reflect a theme onto the document without persisting it.
export function applyTheme(theme) {
  if (typeof document !== "undefined") {
    document.documentElement.dataset.theme = theme === "light" ? "light" : "dark";
  }
}

export function setTheme(theme) {
  const value = theme === "light" ? "light" : "dark";
  try {
    if (typeof localStorage !== "undefined") localStorage.setItem(STORAGE_KEY, value);
  } catch {
    // Ignore storage failures; the choice simply will not persist.
  }
  applyTheme(value);
  return value;
}
