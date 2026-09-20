import assert from "node:assert/strict";
import test from "node:test";

import {
  escapeHTML,
  formatBytes,
  incidentTitleFor,
  keyValues,
  statusClass,
} from "../src/ui.js";

test("escapeHTML encodes markup and attributes", () => {
  assert.equal(
    escapeHTML(`<img src=x onerror="alert('x')">`),
    "&lt;img src=x onerror=&quot;alert(&#39;x&#39;)&quot;&gt;",
  );
});

test("statusClass maps operational states", () => {
  assert.equal(statusClass("Critical"), "critical");
  assert.equal(statusClass("Degraded"), "warn");
  assert.equal(statusClass("Offline"), "unknown");
  assert.equal(statusClass("Healthy"), "ok");
});

test("formatBytes selects readable units", () => {
  assert.equal(formatBytes(0), "0 B");
  assert.equal(formatBytes(1536), "1.5 KB");
  assert.equal(formatBytes(1024 ** 3), "1.0 GB");
});

test("keyValues parses complete pairs", () => {
  assert.deepEqual(keyValues("environment=production, owner=platform, invalid"), {
    environment: "production",
    owner: "platform",
  });
});

// A node running out of memory fires memory, then swap, then a service that
// could not allocate: three alerts, one outage. The title has to survive
// being read a week later by someone who was not there.
test("an incident title says what fired and how wide it reached", () => {
  const memory = { id: "a1", name: "Memory saturation", resourceId: "node-01", severity: "critical" };
  const swap = { id: "a2", name: "Swap in use", resourceId: "node-01", severity: "warning" };
  const other = { id: "a3", name: "Memory saturation", resourceId: "node-02", severity: "critical" };

  assert.equal(incidentTitleFor([memory]), "Memory saturation on node-01");
  // Several on one machine: the machine is the fact that matters.
  assert.equal(incidentTitleFor([memory, swap]), "2 alerts on node-01");
  // Several machines: the spread is the fact that decides how it is run.
  assert.equal(incidentTitleFor([memory, swap, other]), "3 alerts across 2 resources");
  // Nothing selected leaves the field empty rather than inventing a title.
  assert.equal(incidentTitleFor([]), "");
  assert.equal(incidentTitleFor(undefined), "");
  // An alert with no resource is still nameable.
  assert.equal(
    incidentTitleFor([{ id: "a4", name: "Agent offline" }]),
    "Agent offline on an unnamed resource",
  );
});
