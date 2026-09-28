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

const PAGES = [
  "fleet", "infrastructure", "utilization", "infra-map", "alerts",
  "incidents", "logs", "terminal", "runbooks", "users", "teams",
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
