import assert from "node:assert/strict";
import test from "node:test";

import {
  escapeHTML,
  formatBytes,
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
