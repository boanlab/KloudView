// Drives the console the way an operator meets it: sign in, open every page,
// then take one alert from the threshold that fires it to the incident declared
// from it. Anything the browser reports along the way - a console error, an
// exception, a request that failed or answered 4xx - fails the run, because a
// page that renders while its own scripts are erroring is not working.
import { createRequire } from "module";
const require = createRequire(process.env.KLOUDVIEW_WEB_PACKAGE || "/web/package.json");
const { chromium } = require("playwright");

const BASE = process.env.KLOUDVIEW_E2E_BASE || "http://127.0.0.1:8099";
const PASSWORD = process.env.KLOUDVIEW_E2E_PASSWORD || "e2e-admin";
const SHOTS = process.env.KLOUDVIEW_E2E_SHOTS || "";

// Every entry the sidebar offers, and the tabs the multi-page sections hide
// behind them. A page nobody opens in a test is a page nobody knows renders.
const PAGES = [
  "overview", "alerts", "incidents",
  "infra-map", "infrastructure", "utilization", "logs", "fleet",
  "terminal", "runbooks", "jobs",
  "users", "teams", "roles", "scopes", "bindings",
  "groups", "dynamic-groups", "tags",
  "unmanaged", "alert-rules", "notification-routing", "audit",
];

const failures = [];
const problems = [];
let current = "startup";
const check = (ok, what) => {
  console.log(`${ok ? "ok  " : "FAIL"}  ${what}`);
  if (!ok) failures.push(what);
};
const shot = async (page, name) => {
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/${name}.png` }).catch(() => {});
};
// A dialog left open covers the sidebar, and the next navigation waits on a
// click that can never land.
const dismissOverlays = async (page) => {
  for (let i = 0; i < 3; i++) {
    const open = page.locator("dialog[open], [role=dialog], .modal").first();
    if (!(await open.count())) return;
    await page.keyboard.press("Escape");
    await page.waitForTimeout(400);
  }
};

const browser = await chromium.launch();
const page = await (await browser.newContext({ viewport: { width: 1600, height: 1000 } })).newPage();
page.on("console", (m) => { if (m.type() === "error") problems.push(`${current}: console ${m.text().slice(0, 200)}`); });
page.on("pageerror", (e) => problems.push(`${current}: pageerror ${String(e).slice(0, 200)}`));
page.on("requestfailed", (r) => problems.push(`${current}: requestfailed ${r.method()} ${r.url()}`));
page.on("response", (r) => { if (r.status() >= 400) problems.push(`${current}: http ${r.status()} ${r.request().method()} ${r.url()}`); });

try {
  current = "login";
  await page.goto(BASE, { waitUntil: "networkidle", timeout: 30000 });
  await page.waitForSelector("#login-form", { timeout: 15000 });
  await page.fill("#login-username", "admin");
  await page.fill("#login-password", PASSWORD);
  await page.click("#login-form button");
  await page.waitForSelector("#login-form", { state: "detached", timeout: 20000 });
  check(true, "sign in");
  await shot(page, "dashboard");

  for (const name of PAGES) {
    current = name;
    const link = page.locator(`[data-page="${name}"]`).first();
    if (!(await link.count())) { check(false, `${name}: no nav entry`); continue; }
    await link.click();
    await page.waitForTimeout(1200);
    // A shell with nothing in it is not a rendered page.
    const body = (await page.locator("body").innerText()).trim();
    check(body.length > 200, `${name} renders`);
    await shot(page, name);
  }

  // What the fleet is running, on the page someone opens to ask that. The run
  // names no target, so the banner is absent rather than wrong.
  current = "fleet rollout";
  await page.click('[data-page="fleet"]');
  await page.waitForTimeout(1500);
  const fleetText = await page.locator("body").innerText();
  check(/REGISTERED/i.test(fleetText), "agents page lists the fleet");
  check(!/have not taken/i.test(fleetText), "no stalled rollout is claimed without a target");
  // Two hosts, two agents: one node answers questions a fleet does not.
  check(/e2e-node-a/.test(fleetText) && /e2e-node-b/.test(fleetText), "both agents are listed, each under its own name");

  current = "resources across nodes";
  await page.click('[data-page="infrastructure"]');
  await page.waitForTimeout(1500);
  const resourceText = await page.locator("body").innerText();
  check(/e2e-node-a/.test(resourceText) && /e2e-node-b/.test(resourceText),
    "both nodes appear as resources rather than merging into one");

  // The chain the product exists for: a threshold, the alert it raises, and the
  // incident an operator declares from it.
  current = "alert rule";
  await page.click("text=Alerting");
  await page.waitForTimeout(1200);
  await page.click('button:has-text("New rule")');
  await page.waitForTimeout(1000);
  await page.fill("#rule-name", "e2e memory floor");
  await page.selectOption("#rule-metric", "memory");
  await page.selectOption("#rule-operator", ">");
  await page.fill("#rule-threshold", "1");
  await page.fill("#rule-duration", "0s");
  await page.selectOption("#rule-severity", "critical");
  await page.selectOption("#rule-scope", "*");
  await page.selectOption("#rule-enabled", "true");
  await page.locator("dialog button, .modal button").filter({ hasText: /Create|Save|Add|Confirm/ }).last().click();
  await page.waitForTimeout(1500);
  check((await page.locator("body").innerText()).includes("e2e memory floor"), "alert rule created");

  current = "alert fires";
  let fired = false;
  for (let i = 0; i < 12 && !fired; i++) {
    await page.click('[data-page="alerts"]');
    await page.waitForTimeout(2500);
    fired = /e2e memory floor/.test(await page.locator("body").innerText());
  }
  check(fired, "alert fires on a real reading");
  await shot(page, "alerts-firing");

  if (fired) {
    current = "declare incident";
    await page.locator("table input[type=checkbox]").first().check();
    await page.waitForTimeout(600);
    const declare = page.locator("button").filter({ hasText: /Declare/i }).first();
    check((await declare.count()) > 0, "declare control appears once an alert is ticked");
    await declare.click();
    await page.waitForTimeout(1000);
    await page.locator("dialog button, .modal button").filter({ hasText: /Declare|Create|Confirm/i }).last().click();
    await page.waitForTimeout(2500);

    current = "incident";
    await page.click('[data-page="incidents"]');
    await page.waitForTimeout(2000);
    check(/e2e memory floor/i.test(await page.locator("body").innerText()), "incident carries the alert it came from");
    await page.locator("table tbody tr").first().click();
    await page.waitForTimeout(2000);
    const detail = await page.locator("body").innerText();
    check(/timeline/i.test(detail), "incident has a timeline");
    check(/e2e memory floor/i.test(detail), "incident links the alert");
    await shot(page, "incident-detail");
  }

  // A silence is the answer to an alert an operator already knows about, so it
  // has to be creatable from the page that raised it.
  current = "silence window";
  await page.click("text=Alerting");
  await page.waitForTimeout(1200);
  await page.click('button:has-text("Silence window")');
  await page.waitForTimeout(1000);
  await page.fill("#silence-name", "e2e quiet hours");
  const now = new Date();
  const stamp = (offsetMinutes) =>
    new Date(now.getTime() + offsetMinutes * 60000).toISOString().slice(0, 16);
  await page.fill("#silence-start", stamp(-5));
  await page.fill("#silence-end", stamp(60));
  await page.locator("dialog button, .modal button").filter({ hasText: /Create|Save|Add|Confirm/ }).last().click();
  await page.waitForTimeout(1500);
  check((await page.locator("body").innerText()).includes("e2e quiet hours"), "silence window is created and listed");

  // An automation that cannot be run is a document. This one is run, and the
  // run has to show up where an operator looks for what happened.
  current = "runbook";
  await page.click('[data-page="runbooks"]');
  await page.waitForTimeout(1200);
  const newRunbook = page.locator("button").filter({ hasText: /New runbook|New automation|Create/i }).first();
  if (!(await newRunbook.count())) {
    check(false, "no control to create an automation");
  } else {
    await newRunbook.click();
    await page.waitForTimeout(1000);
    await page.fill("#runbook-name", "e2e inventory refresh");
    await page.selectOption("#runbook-risk", "low");
    // A step without a name is refused, and the default step arrives blank.
    await page.locator("[data-step-name]").first().fill("refresh inventory");
    await page.locator("[data-step-operation]").first().selectOption("inventory.refresh");
    await page.locator("dialog button, .modal button").filter({ hasText: /Create|Save|Add|Confirm/ }).last().click();
    await page.waitForTimeout(1500);
    const listed = (await page.locator("body").innerText()).includes("e2e inventory refresh");
    check(listed, "automation is created and listed");
    if (listed) {
      await page.locator('[data-action="execute-runbook"]').first().click();
      await page.waitForTimeout(1200);
      for (const select of await page.locator("dialog select, .modal select").all()) {
        if ((await select.locator("option").count()) > 1) await select.selectOption({ index: 1 });
      }
      const confirm = page.locator("dialog button, .modal button").filter({ hasText: /Run|Execute|Confirm/i }).last();
      if (await confirm.count()) await confirm.click();
      await page.waitForTimeout(2500);
      check(/e2e inventory refresh/i.test(await page.locator("body").innerText()),
        "the run is recorded against the automation");
      // Running one opens a panel over the page; the sidebar is behind it.
      await dismissOverlays(page);
      await page.click('[data-page="jobs"]');
      await page.waitForTimeout(2000);
      check((await page.locator("body").innerText()).length > 200, "task history renders the run");
      await shot(page, "task-history");
    }
  }

  // An audited shell is only audited if the recording is there afterwards.
  current = "terminal recording";
  await dismissOverlays(page);
  await page.click('[data-page="terminal"]');
  await page.waitForTimeout(1200);
  await page.click("text=Request session");
  await page.waitForTimeout(1000);
  for (const select of await page.locator("dialog select, .modal select, select").all()) {
    if ((await select.locator("option").count()) > 1) await select.selectOption({ index: 1 });
  }
  const why = page.locator("dialog input[type=text], dialog textarea, .modal input[type=text], .modal textarea").first();
  if (await why.count()) await why.fill("e2e recording");
  await page.locator("dialog button, .modal button").filter({ hasText: /Request|Submit/ }).last().click();
  await page.waitForTimeout(1500);
  await page.locator('table button:has-text("Approve")').first().click();
  await page.waitForTimeout(800);
  await page.locator("dialog, [role=dialog], .modal").filter({ hasText: "Approve Terminal Session" }).first()
    .locator('button:has-text("Approve")').click();
  await page.waitForTimeout(3000);
  const surface = page.locator('.terminal, .xterm, [class*="console"], [class*="screen"], pre').first();
  await surface.click().catch(() => {});
  await page.keyboard.type("echo E2E_RECORDED");
  await page.keyboard.press("Enter");
  await page.waitForTimeout(3000);
  check((await page.locator("body").innerText()).includes("E2E_RECORDED"), "the shell runs a command and answers");
  const recording = page.locator('button:has-text("Recording")').first();
  if (await recording.count()) {
    await recording.click();
    await page.waitForTimeout(2000);
    check(/E2E_RECORDED/.test(await page.locator("body").innerText()), "the recording keeps what was typed");
    await shot(page, "recording");
    await page.keyboard.press("Escape");
  } else {
    check(false, "no recording to open for a session that ran a command");
  }

  // Everything above was somebody doing something, and an audit log that does
  // not have it is not an audit log.
  current = "audit log";
  await dismissOverlays(page);
  await page.click('[data-page="audit"]');
  await page.waitForTimeout(2000);
  const audit = await page.locator("body").innerText();
  check(/admin/i.test(audit), "the audit log names who acted");
  check(/terminal|alert|runbook|silence/i.test(audit), "the audit log carries what was done");
  await shot(page, "audit");

} catch (err) {
  failures.push(`${current}: threw ${String(err).split("\n")[0]}`);
  console.log(`FAIL  ${current}: threw ${String(err).split("\n")[0]}`);
}

await browser.close();

if (problems.length) {
  console.log(`\n${problems.length} browser problem(s):`);
  for (const p of problems.slice(0, 40)) console.log("  " + p);
}
if (failures.length || problems.length) {
  console.log(`\nFAILED: ${failures.length} check(s), ${problems.length} browser problem(s)`);
  process.exit(1);
}
console.log("\nAll checks passed with a clean console.");
