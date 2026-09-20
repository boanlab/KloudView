import assert from "node:assert/strict";
import test from "node:test";

import {
  escapeHTML,
  formatBytes,
  incidentTitleFor,
  keyValues,
  statusClass,
  terminalDockView,
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

test("the docked terminal folds to a bar, and an unknown mode folds with it", () => {
  const collapsed = terminalDockView("collapsed");
  assert.equal(collapsed.showsPane, false);
  assert.equal(collapsed.foldLabel, "Show terminal");
  // The panel sits over the page, so anything the console does not recognise
  // has to land on the one state that covers nothing.
  for (const stored of [undefined, null, "", "maximised", "open "]) {
    assert.equal(terminalDockView(stored).mode, "collapsed");
    assert.equal(terminalDockView(stored).showsPane, false);
  }

  const open = terminalDockView("open");
  assert.equal(open.showsPane, true);
  assert.equal(open.foldLabel, "Collapse terminal");
  assert.equal(open.sizeLabel, "Expand panel");

  const max = terminalDockView("max");
  assert.equal(max.showsPane, true);
  assert.equal(max.sizeLabel, "Restore panel");
  // Expanded and restored must not offer the same control, or the button
  // stops saying which way it goes.
  assert.notEqual(max.sizeGlyph, open.sizeGlyph);
});

test("the terminal pane is drawn in exactly one place", async () => {
  // `#terminal-screen` is how the paint path, the size report and the key
  // handler all find the pty's pane. A second element with that id -- the
  // Remote shell page keeping its own copy while the dock draws another --
  // would send the output to one and the keystrokes to the other, with
  // nothing on screen to say which half went where.
  const { readFile } = await import("node:fs/promises");
  const source = await readFile(new URL("../src/app.js", import.meta.url), "utf8");
  const panes = source.match(/id="terminal-screen"/g) || [];
  assert.equal(panes.length, 1);
});
