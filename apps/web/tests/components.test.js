import assert from "node:assert/strict";
import test from "node:test";

import { card, kpi, pageHead, renderShell } from "../src/components.js";

const shellState = {
  mobileSidebarOpen: false,
  page: "resources",
  query: "node",
  // The box shows what is being typed, so that is the field that has to be
  // escaped on the way back out.
  searchDraft: `"><script>alert(1)</script>`,
  scopePath: "production",
  sidebarCollapsed: false,
  subject: "admin",
};

test("renderShell marks the active page and escapes search input", () => {
  const html = renderShell("<p>content</p>", shellState, [
    { label: "Infrastructure" },
    { page: "resources", icon: "infra", name: "Resources" },
    { page: "utilization", icon: "activity", name: "Utilization" },
  ]);
  assert.match(html, /class="nav-item active" data-page="resources"/);
  assert.match(html, /data-page="utilization"/);
  assert.match(html, /&lt;script&gt;alert\(1\)&lt;\/script&gt;/);
  assert.doesNotMatch(html, /<script>alert\(1\)<\/script>/);
});

test("shared components preserve content slots", () => {
  assert.match(pageHead("Title", "Subtitle", "Action"), /Title.*Subtitle.*Action/);
  assert.match(kpi("CPU", "42%", "Average", "info"), /CPU.*42%.*Average/);
  assert.match(card("Health", "Body", "Tools", "wide"), /Health.*Tools.*Body/);
});
