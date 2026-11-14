// carries: components/MachineLoadSection*, components/DebugPanel*, components/debug-panel-state*, hooks/useMachineLoad*, lib/machine-load*, app/api/machine-load/*
// pi-web machine load (wi_p2nDPX2BoBAp), in the Debug panel
// only (the grid's narrow rightmost column
// on a wide panel, the right column under the sessions between that width and
// 760px, the last block of the phone stack); the Session-info popover carries
// none of it any more (ML01 pins both halves).
//
// End-to-end acceptance pass authored PRE-CODE by e2e-author against
// artifacts/spec.md (Acceptance criteria + `#### Scenario` blocks) and the
// route path `plan` pinned (GET /api/machine-load; the spec leaves the path to
// plan). Every locator drives behaviour through surfaces that already exist on
// the pre-implementation tree — the toolbar's Session-info button
// (`session.title` aria-label), the existing `.session-info-popover`, the
// existing /api gate — and matches the new section only through user-visible
// text/values, never through implementation-only symbols. Pre-implementation
// this pass is assertion-level RED: the machine-load section never renders and
// the route answers 404, so nothing fails to build or import.
// retargeted every scenario from the popover to the Debug panel (toolbar
// `debug.title` button, `.debug-panel`); the assertions are unchanged.
//
// The spec pins the locale-invariant number formats and the zh-CN strings
// (`机器负载`, `宿主机`, `不可用`) but not a DOM anchor, the row labels, or the
// en/zh-TW label strings; the scenarios therefore anchor the section on the
// pinned values (and, in zh-CN, on `机器负载`) and flag the rest as
// under-specification in artifacts/e2e-scenarios.md.
//
// Oracle posture (from the grill/spec):
//  - cadence uses Playwright `page.clock` (installed before navigation, paused
//    after hydration): exact request counts per 2 s tick, 0 while closed/hidden;
//  - no sleeps for UI state: bounded node-side polling, 20 s action ceiling,
//    navigation ceilings NAV_MS (COLD_MS for the cold first load) from
//    e2e/timeouts.mjs under the shared host load;
//  - the real-data check is Linux-only (`/proc/meminfo`, `nproc`, `/proc/loadavg`).
//
//   node e2e/machine-load.mjs
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { cpus, hostname, tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { createCheck, createHarness } from "./harness.mjs";
import { settlePolyforgeToggle } from "./polyforge-dock.mjs";
import { COLD_MS, NAV_MS } from "./timeouts.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
// Own Turbopack cache: a `next dev` sharing `.next` with another run can
// reload-loop while it compiles cold. `all.mjs` already gives every suite this
// distDir; a standalone run gets the same isolation here.
process.env.PI_WEB_DIST_DIR ||= ".next/e2e/machine-load";
// Start this suite from a clean cache. Reusing a persisted Turbopack cache here
// makes `next dev` full-reload the page in a loop while it recompiles (observed:
// ~7 document requests/s, `/` re-requested forever and the readiness locator
// never settles). Warm caches are a speed optimisation; correctness first.
// Only a distDir this suite owns (`.next/e2e/*`, the `all.mjs` convention) is
// removed, never the checkout's shared `.next`.
if (process.env.PI_WEB_DIST_DIR.startsWith(".next/e2e/")) {
  rmSync(join(root, process.env.PI_WEB_DIST_DIR), { recursive: true, force: true });
}
assert.ok(!existsSync(join(root, process.env.PI_WEB_DIST_DIR || ".next", "dev/lock")), "Run in a checkout without an active dev server");

// Pinned by plan.md ("the spec leaves the route path to plan").
const ROUTE = "/api/machine-load";
const SESSION = "e2e-machine-load";
const WAIT = 20_000; // Playwright action ceiling with margin under host load
const VALUE_WAIT = 10_000; // bounded wait on the asserted values

// Spec assumptions 1/2: the pinned, locale-invariant strings for one payload.
const PAYLOAD = { cpuPercent: 12.3, coreCount: 8, loadavg: [0.42, 0.51, 0.63], memoryUsedBytes: 5583457485, memoryTotalBytes: 17179869184 };
const PINNED = { cpu: "12.3%", cores: "8", loadavg: "0.42 / 0.51 / 0.63", memory: "5.2 / 16.0 GiB" };
const VALUES = [PINNED.cpu, PINNED.cores, PINNED.loadavg, PINNED.memory];
const DASH = "\u2014";
const ZH = { heading: "机器负载", host: "宿主机", unavailable: "不可用" };
const METRIC_KEYS = ["coreCount", "cpuPercent", "loadavg", "memoryTotalBytes", "memoryUsedBytes"];
const FORBIDDEN = /hostname|disk|uptime|per[-_ ]?core|process\b|argv|environment|\benv\b|cgroup|credential|secret|sparkline|threshold|磁盘|主机名|运行时间|每核|进程|趋势|阈值|配额/i;

const work = mkdtempSync(join(tmpdir(), "pi-web-machine-load-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
mkdirSync(join(agentDir, "sessions", "e2e"), { recursive: true });
mkdirSync(project);
const ts = "2026-10-30T00:00:00.000Z";
const SESSION_FILE = join(agentDir, "sessions", "e2e", `${SESSION}.jsonl`);
writeFileSync(SESSION_FILE, [
  { type: "session", version: 3, id: SESSION, timestamp: ts, cwd: project },
  { type: "message", id: "m1", parentId: null, timestamp: ts, message: { role: "user", content: "E2E machine load host session" } },
  { type: "message", id: "m2", parentId: "m1", timestamp: ts, message: { role: "assistant", content: [{ type: "text", text: "E2E reply" }] } },
].map((entry) => JSON.stringify(entry)).join("\n") + "\n");

// ── route stub ────────────────────────────────────────────────────────────
// `mode` is per scenario: ok | 500 | nonjson | null-metrics | abort | delayed |
// null-cpu | null-loadavg. `hits` records every request the client issued.
const stub = { mode: "ok", hits: [], pending: [] };

async function fulfill(route) {
  const body = (overrides = {}) => ({ metrics: { ...PAYLOAD, ...overrides } });
  switch (stub.mode) {
    case "500": return route.fulfill({ status: 500, json: { error: "E2E stub failure" } });
    case "nonjson": return route.fulfill({ status: 200, contentType: "text/html", body: "<html>E2E not json</html>" });
    case "null-metrics": return route.fulfill({ json: { metrics: null } });
    case "null-cpu": return route.fulfill({ json: body({ cpuPercent: null }) });
    case "null-loadavg": return route.fulfill({ json: body({ loadavg: null }) });
    case "abort": return route.abort("failed");
    case "delayed": {
      await new Promise((resolve) => stub.pending.push(resolve));
      return route.fulfill({ json: body() }).catch(() => {});
    }
    default: return route.fulfill({ json: body() });
  }
}

function resetStub(mode) {
  stub.mode = mode;
  stub.hits = [];
  stub.pending = [];
}
function releaseDelayed() {
  for (const resolve of stub.pending.splice(0)) resolve();
}

// ── node-side bounded waits (page timers may be frozen by page.clock) ──────
async function pollUntil(predicate, timeout = VALUE_WAIT) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    try { last = await predicate(); } catch { last = undefined; }
    if (last) return last;
    await delay(100);
  }
  return last;
}
async function waitForHits(count) {
  const ok = await pollUntil(() => stub.hits.length === count, VALUE_WAIT);
  assert.ok(ok, `expected exactly ${count} ${ROUTE} request(s), saw ${stub.hits.length}`);
}
const tick = (page, ms) => (ms > 0 ? page.clock.runFor(ms) : Promise.resolve());

/**
 * A page-side ordering barrier instead of a wall-clock grace window:
 * the page fetches a stubbed marker URL and the call returns
 * once it answered. Every request the page issued before the marker (a poll
 * fired by the clock advance just before) has been logged by then, so an
 * exact-count assertion right after the barrier proves the surface stopped
 * instead of merely not having been seen yet.
 */
const BARRIER = "/__e2e_request_barrier";
async function requestBarrier(page) {
  await page.evaluate(async (url) => { await fetch(url, { cache: "no-store" }); }, BARRIER);
}

// ── page helpers ───────────────────────────────────────────────────────────
let base;

const sessionInfoButton = (page) =>
  page.getByRole("button", { name: /^(Session info|会话信息|工作階段資訊)$/ }).first();
const popover = (page) => page.locator(".session-info-popover").first();
// The section's one mount: the Debug panel (toolbar button
// labelled `debug.title` in the three locales; on a phone it may sit in More).
const debugButton = (page) => page.getByRole("button", { name: /^(Debug|调试|除錯)$/ }).first();
const panel = (page) => page.locator(".debug-panel").first();

// `locale` is the page's language (the context stays en-US); the page is not
// navigated here (loadSession does that).
const h = createHarness({
  shape: ({ locale = "en-US", ...o }) => ({ ...o, loc: locale, context: { baseURL: h.base }, storage: { "pi-web:split-view-enabled": "false", "pi-locale": locale } }),
  init: [() => {
    window.__mlUnhandled = [];
    window.addEventListener("unhandledrejection", (event) => window.__mlUnhandled.push(String(event.reason)));
  }],
  timeout: WAIT,
  errors: [],
  ready: null,
  async setup(page, o) {
    resetStub(o.stubMode ?? "ok");
    o.state = { context: page.context(), page, pageErrors: [], unhandled: [], loc: o.loc };
    page.on("pageerror", (error) => o.state.pageErrors.push(error.message));
    page.on("request", (request) => {
      if (/\/api\/machine-load(\?|$)/.test(request.url())) stub.hits.push(request.url());
    });
    if (o.stubRoute ?? true) await page.route(/\/api\/machine-load(\?|$)/, (route) => fulfill(route));
    await page.route((url) => url.pathname === BARRIER, (route) => route.fulfill({ status: 204 }));
  },
  after: (page, o) => o.state,
});
const { openPage } = h;

/**
 * Loads the fixture session with real timers, THEN installs the fake clock.
 * Installing before navigation makes the Next dev client reload-loop on a cold
 * compile, so the page always boots with real time; the machine-load hook's
 * interval is created after the Debug panel opens, i.e. after the install, so the
 * clock still owns every 2 s tick below. A bounded retry restarts the load if a
 * cold compile still churns (the shared `next-server.mjs` helper retries too).
 */
async function loadSession(state, { clock = false, cold = false } = {}) {
  const { page } = state;
  const ceiling = cold ? COLD_MS : NAV_MS;
  let lastError;
  for (let attempt = 1; attempt <= 3; attempt += 1) {
    try {
      await page.goto(`/?session=${SESSION}`, { waitUntil: "domcontentloaded", timeout: ceiling });
      await page.getByText("E2E machine load host session", { exact: true }).first()
        .waitFor({ state: "attached", timeout: ceiling });
      lastError = null;
      break;
    } catch (error) {
      lastError = error;
      await delay(1000);
    }
  }
  assert.ok(!lastError, `the fixture session page did not become ready: ${lastError?.message ?? "unknown"}`);
  if (clock) {
    await page.clock.install({ time: new Date() });
    // Pause slightly AHEAD: pauseAt fast-forwards to the target and a target
    // already behind the fake clock throws "Cannot fast-forward to the past".
    await page.clock.pauseAt(new Date(Date.now() + 2000));
  }
}

async function revealDebugButton(page) {
  if (await debugButton(page).isVisible().catch(() => false)) return;
  // Let the resident Polyforge toggle settle (bounded)
  // before opening ⋯, so its late render cannot land under the open sheet.
  await settlePolyforgeToggle(page);
  const more = page.locator('[data-mobile-toolbar-more="true"]');
  if ((await more.count()) > 0 && (await more.getAttribute("aria-expanded")) !== "true") await more.click();
}

async function openPanel(state) {
  const { page } = state;
  await revealDebugButton(page);
  const button = debugButton(page);
  await button.waitFor({ state: "visible", timeout: WAIT });
  await button.click();
  await panel(page).waitFor({ state: "visible", timeout: WAIT });
  // Ground the pass on the panel's own columns (the section mounts with them,
  // after the page's first reading): never read a half-mounted panel.
  await page.locator(".debug-cols").first().waitFor({ state: "visible", timeout: WAIT });
}

async function closePanel(state) {
  const { page } = state;
  await revealDebugButton(page);
  await debugButton(page).click();
  await panel(page).waitFor({ state: "hidden", timeout: WAIT });
}

/**
 * The machine-load section, found without depending on an implementation-only
 * class or an unpinned label: the deepest element containing every expected
 * value, then the largest ancestor that still holds them below the Debug
 * panel's column grid (the load's own column there) and has not pulled in a
 * neighbouring section (the session file / project-path sentinels).
 */
async function machineScope(state, values = VALUES) {
  const { page } = state;
  return page.evaluate(({ wanted, sentinels }) => {
    const panel = document.querySelector(".debug-panel");
    if (!panel) return null;
    const grid = panel.querySelector(".debug-cols");
    const text = (element) => element.innerText || "";
    const depth = (element) => {
      let node = element;
      let level = 0;
      while (node && node !== panel) { level += 1; node = node.parentElement; }
      return level;
    };
    const holding = [...panel.querySelectorAll("*")].filter((element) => {
      const value = text(element);
      return wanted.every((item) => value.includes(item));
    });
    if (holding.length === 0) return null;
    let scope = holding.reduce((best, element) => (depth(element) > depth(best) ? element : best));
    let parent = scope.parentElement;
    while (parent && parent !== panel && parent !== grid) {
      const value = text(parent);
      if (!wanted.every((item) => value.includes(item))) break;
      if (sentinels.some((sentinel) => value.includes(sentinel))) break;
      scope = parent;
      parent = scope.parentElement;
    }
    return {
      text: text(scope),
      html: scope.outerHTML,
      insidePanel: panel.contains(scope),
      inGrid: Boolean(grid?.contains(scope)),
      overflow: { section: scope.scrollWidth - scope.clientWidth, panel: panel.scrollWidth - panel.clientWidth },
    };
  }, { wanted: values, sentinels: [`${SESSION}.jsonl`, project] });
}

async function waitForScope(state, values = VALUES) {
  const scope = await pollUntil(() => machineScope(state, values), VALUE_WAIT);
  assert.ok(scope, `the machine-load section did not render the expected values ${JSON.stringify(values)}`);
  return scope;
}

/** Counts the MINIMAL elements anywhere in the document that hold every value. */
async function documentHolders(state, values = VALUES) {
  return state.page.evaluate((wanted) => {
    const holds = (element) => {
      const text = element.innerText || "";
      return wanted.every((item) => text.includes(item));
    };
    return [...document.querySelectorAll("*")]
      .filter((element) => holds(element) && ![...element.children].some(holds)).length;
  }, values);
}

// ── scenario harness (self-guarding, like e2e/polyforge.mjs) ──────────────
const failures = new Map();
const ran = new Set();

const check = createCheck({
  before: ([id]) => ran.add(id),
  passed: ([id, title]) => failures.set(id, { title, problems: [] }),
  failures: ([id, title], text) => failures.set(id, { title, problems: [text] }),
  message: (error) => (error && error.message ? error.message : String(error)),
  log: (error) => `${error && error.message ? error.message : error}`,
  pass: null,
  fail: ([id, title], text) => console.error(`FAIL ${id}: ${title}\n  - ${text}`),
});

async function noPageErrors(state, label) {
  state.unhandled = await state.page.evaluate(() => window.__mlUnhandled ?? []);
  assert.deepEqual(state.pageErrors, [], `${label}: browser page errors`);
  assert.deepEqual(state.unhandled, [], `${label}: unhandled promise rejections`);
}

// ════════════════════════════ scenarios ══════════════════════════════════
try {
  ({ base } = await h.startServer({ name: "machine-load", logFile: join(work, "server.log"), env: { PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work } }));
  await h.launchBrowser(() => chromium.launch());

  // Warm the fixture page once: the first page load after a clean checkout
  // compiles cold, and the per-scenario bounded waits must not pay for it.
  {
    const warm = await openPage({ locale: "en-US" });
    try { await loadSession(warm, { cold: true }); } finally { await warm.context.close(); }
  }

  // ML01 — spec req 1, retargeted: the section lives in the
  // Debug panel's column grid of a session WITHOUT subagents, and only there;
  // the Session-info popover renders none and issues no request of its own.
  await check("ML01", "Section lives in the Debug panel only; the Session-info popover has none and requests nothing", async () => {
    const state = await openPage({ locale: "zh-CN" });
    try {
      await loadSession(state);
      // Pre-existing gating: a session without subagents has no Agents dropdown.
      assert.equal(await state.page.getByRole("button", { name: "Agents" }).count(), 0,
        "the fixture session must have no subagent gating (no Agents dropdown)");
      await sessionInfoButton(state.page).click();
      await popover(state.page).waitFor({ state: "visible", timeout: WAIT });
      assert.ok(await pollUntil(async () => (await popover(state.page).innerText()).includes(SESSION), WAIT),
        "the Session-info popover body did not render the session info");
      await requestBarrier(state.page);
      assert.equal(stub.hits.length, 0, "the Session-info popover must issue no machine-load request");
      assert.ok(!(await popover(state.page).innerText()).includes(ZH.heading), `the popover must not carry ${ZH.heading}`);
      await sessionInfoButton(state.page).click();
      await popover(state.page).waitFor({ state: "hidden", timeout: WAIT });

      await openPanel(state);
      const scope = await waitForScope(state);
      assert.ok(scope.insidePanel && scope.inGrid, "the values must render inside the Debug panel's column grid");
      assert.ok((await panel(state.page).innerText()).includes(ZH.heading), `${ZH.heading} heading must be present in the Debug panel`);
      assert.equal(await documentHolders(state), 1, "the machine-load section must be rendered exactly once");
      await closePanel(state);
      const gone = await pollUntil(async () => (await documentHolders(state)) === 0, VALUE_WAIT);
      assert.ok(gone, "the values must disappear with the Debug panel (no section outside it)");
    } finally { await state.context.close(); }
  });

  // ML02 — spec req 2: exactly the four metric groups, from the route payload,
  // in the pinned formats.
  await check("ML02", "Four metric groups render from the stub payload with no extra group", async () => {
    const state = await openPage({ locale: "zh-CN" });
    try {
      await loadSession(state);
      await openPanel(state);
      const scope = await waitForScope(state);
      for (const value of VALUES) assert.ok(scope.text.includes(value), `expected ${value} in the section`);
      assert.equal((scope.text.match(/%/g) || []).length, 1, "only the CPU value may carry a percent sign (no extra metric group)");
      assert.ok(!FORBIDDEN.test(scope.text), `no extra metric group vocabulary may render: ${scope.text}`);
      assert.ok(!scope.text.includes(hostname()), "the host name must not render");
      await noPageErrors(state, "ML02");
    } finally { await state.context.close(); }
  });

  // ML03 — spec req 3: the real route exposes only the whitelisted aggregate
  // payload, and neither the response nor the DOM carries host facts.
  await check("ML03", "Route whitelist only; no host facts in the response or the DOM", async () => {
    const response = await fetch(`${base}${ROUTE}`, { signal: AbortSignal.timeout(60_000) });
    assert.equal(response.status, 200, `${ROUTE} must answer 200 to an allowed request`);
    const raw = await response.text();
    const body = JSON.parse(raw);
    assert.deepEqual(Object.keys(body), ["metrics"], "the body has exactly one top-level key: metrics");
    assert.deepEqual(Object.keys(body.metrics).sort(), METRIC_KEYS, "the metrics shape is the five-field whitelist");
    const { cpuPercent, coreCount, loadavg, memoryUsedBytes, memoryTotalBytes } = body.metrics;
    assert.ok(cpuPercent === null || (typeof cpuPercent === "number" && cpuPercent >= 0 && cpuPercent <= 100),
      `cpuPercent must be null or 0..100 (got ${cpuPercent})`);
    assert.ok(Number.isInteger(coreCount) && coreCount > 0, `coreCount must be a positive integer (got ${coreCount})`);
    assert.ok(loadavg === null || (Array.isArray(loadavg) && loadavg.length === 3 && loadavg.every(Number.isFinite)),
      `loadavg must be null or three finite numbers (got ${JSON.stringify(loadavg)})`);
    assert.ok(Number.isInteger(memoryUsedBytes) && Number.isInteger(memoryTotalBytes) && memoryUsedBytes >= 0
      && memoryUsedBytes <= memoryTotalBytes, "memory bytes must be integers with used <= total");
    assert.ok(!FORBIDDEN.test(raw), `the response must not carry host facts: ${raw}`);
    assert.ok(!/[\\/]/.test(raw), "the response must not carry filesystem paths");
    assert.ok(!raw.includes(hostname()), "the response must not carry the host name");

    const state = await openPage({ locale: "en-US" });
    try {
      await loadSession(state);
      await openPanel(state);
      const scope = await waitForScope(state);
      assert.ok(!FORBIDDEN.test(scope.text), `the section must not render host facts: ${scope.text}`);
      assert.ok(!scope.text.includes(hostname()), "the host name must not render");
      assert.ok(!scope.text.includes(agentDir) && !scope.text.includes(project), "no filesystem path may render");
      await noPageErrors(state, "ML03");
    } finally { await state.context.close(); }
  });

  // ML04 — spec req 3 (gate half): a refused request answers 403 with no
  // metrics; an allowed request needs no new permission.
  await check("ML04", "Gate refusal answers 403 without metrics; an allowed request needs no new permission", async () => {
    const refused = await fetch(`${base}${ROUTE}`, {
      headers: { "sec-fetch-site": "cross-site" },
      signal: AbortSignal.timeout(60_000),
    });
    assert.equal(refused.status, 403, "a request the /api gate refuses must answer 403");
    const refusedBody = await refused.text();
    assert.ok(!METRIC_KEYS.some((key) => refusedBody.includes(key)), `the 403 carries no metrics: ${refusedBody}`);

    const allowed = await fetch(`${base}${ROUTE}`, { signal: AbortSignal.timeout(60_000) });
    assert.equal(allowed.status, 200, "an allowed request answers without any new permission");
    assert.ok("metrics" in (await allowed.json()), "the allowed body carries metrics");

    // The browser's own same-origin path (the proxy gate sees sec-fetch-site):
    const state = await openPage({ locale: "en-US", stubRoute: false });
    try {
      await loadSession(state);
      const status = await state.page.evaluate(async (route) => (await fetch(route)).status, ROUTE);
      assert.equal(status, 200, "a same-origin browser request needs no new permission");
    } finally { await state.context.close(); }
  });

  // ML05 — spec req 4 (client half): fixed 2 s cadence, 0 while closed or
  // hidden, immediate fetch on reopen / visible, with a controllable clock.
  await check("ML05", "Deterministic clock: one request per 2 s tick, zero closed/hidden, immediate resume", async () => {
    const state = await openPage({ locale: "en-US" });
    try {
      await loadSession(state, { clock: true });
      await openPanel(state);
      await waitForScope(state);
      await waitForHits(1); // the immediate fetch on open
      await requestBarrier(state.page);
      assert.equal(stub.hits.length, 1, "no second request without a clock tick");
      await tick(state.page, 2000); await waitForHits(2);
      await tick(state.page, 2000); await waitForHits(3);
      await tick(state.page, 2000); await waitForHits(4);

      await closePanel(state);
      const closed = stub.hits.length;
      await tick(state.page, 6000);
      await requestBarrier(state.page);
      assert.equal(stub.hits.length, closed, "a closed Debug panel must issue 0 requests over 3 ticks");

      await openPanel(state);
      await waitForHits(closed + 1); // reopening fetches immediately
      await tick(state.page, 2000); await waitForHits(closed + 2);

      const hiddenAt = stub.hits.length;
      await state.page.evaluate(() => {
        Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
        Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await tick(state.page, 6000);
      await requestBarrier(state.page);
      assert.equal(stub.hits.length, hiddenAt, "an open Debug panel on a hidden page must issue 0 requests");

      await state.page.evaluate(() => {
        Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
        Object.defineProperty(document, "hidden", { configurable: true, get: () => false });
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await waitForHits(hiddenAt + 1); // becoming visible fetches immediately
      await tick(state.page, 2000); await waitForHits(hiddenAt + 2);
      await noPageErrors(state, "ML05");
    } finally { await state.context.close(); }
  });

  // ML06 — spec req 4 (config half): fixed 2 s constant, no settings entry,
  // no browser-stored interval.
  await check("ML06", "Fixed 2 s interval with no settings entry and no stored interval", async () => {
    const state = await openPage({ locale: "en-US" });
    try {
      await loadSession(state, { clock: true });
      await openPanel(state);
      await waitForScope(state);
      await waitForHits(1);
      await tick(state.page, 1900);
      await requestBarrier(state.page);
      assert.equal(stub.hits.length, 1, "no request may be issued before the fixed 2 s window elapses");
      await tick(state.page, 300);
      await waitForHits(2);

      const stored = await state.page.evaluate(() => {
        const keys = (store) => Object.keys(store);
        return [...keys(localStorage), ...keys(sessionStorage)];
      });
      assert.equal(stored.filter((key) => /machine|interval|poll|refresh/i.test(key) && !/shared-model-runtime-refresh/.test(key)).length, 0,
        `no browser-stored interval may exist (keys: ${stored.join(", ")})`);

      const controls = await state.page.evaluate(() => {
        const root = document.querySelector(".debug-panel");
        const values = ["12.3%", "0.42 / 0.51 / 0.63", "5.2 / 16.0 GiB"];
        const holder = [...(root?.querySelectorAll("*") ?? [])]
          .filter((element) => values.every((value) => (element.innerText || "").includes(value)))
          .pop();
        if (!holder) return null;
        return holder.querySelectorAll("input, select, textarea").length;
      });
      assert.equal(controls, 0, "the section must not offer an interval control");

      await state.page.getByRole("button", { name: "Settings", exact: true }).click();
      const settings = state.page.getByRole("dialog", { name: "Settings", exact: true });
      await settings.waitFor({ state: "visible", timeout: WAIT });
      const settingsText = await settings.innerText();
      assert.ok(!/machine load|machine-load|机器负载|interval|轮询间隔|輪詢間隔/i.test(settingsText),
        "Settings must not offer a machine-load or interval entry");
      await settings.getByRole("button", { name: "Close", exact: true }).click();
      await noPageErrors(state, "ML06");
    } finally { await state.context.close(); }
  });

  // ML07 — spec req 5: no CPU baseline renders — while the other metrics
  // render; a later delta sample is a bounded one-decimal number.
  await check("ML07", "CPU shows — until a baseline exists, then a bounded one-decimal value", async () => {
    const state = await openPage({ locale: "zh-CN", stubMode: "null-cpu" });
    try {
      await loadSession(state, { clock: true });
      await openPanel(state);
      const first = await waitForScope(state, [DASH, PINNED.cores, PINNED.loadavg, PINNED.memory]);
      assert.ok(first.text.includes(DASH), "a null cpuPercent renders the em dash");
      assert.ok(!/\d+(\.\d+)?%/.test(first.text), "no fabricated percentage before a CPU baseline exists");

      stub.mode = "ok";
      await tick(state.page, 2000);
      const second = await waitForScope(state);
      assert.ok(second.text.includes(PINNED.cpu), "the next sample renders the one-decimal CPU percentage");
      assert.ok(!second.text.includes(DASH), "the em dash clears once a CPU value exists");
      const cpu = Number(/(\d{1,3}\.\d)%/.exec(second.text)?.[1]);
      assert.ok(Number.isFinite(cpu) && cpu >= 0 && cpu <= 100, `CPU must stay within 0..100 (got ${cpu})`);
      await noPageErrors(state, "ML07");
    } finally { await state.context.close(); }
  });

  // ML08 is document-only (sampler semantics are unit-tested with a mocked os;
  // see artifacts/e2e-scenarios.md). No mechanical marker exists for it.

  // ML09 — spec req 7: every first-open failure renders the localized 不可用
  // with no page error and no fabricated zero; a failed tick keeps the last
  // good values; closing clears them and reopening fetches immediately.
  await check("ML09", "Failure paths: 不可用 on first open, last good kept, cleared on close, no page error", async () => {
    const state = await openPage({ locale: "zh-CN" });
    try {
      await loadSession(state, { clock: true });
      const unavailableScope = async () => {
        const scope = await pollUntil(() => machineScope(state, [ZH.heading, ZH.unavailable]), VALUE_WAIT);
        assert.ok(scope, `${ZH.heading} must render ${ZH.unavailable} when no good value exists`);
        assert.ok(scope.text.includes(ZH.unavailable), `${ZH.unavailable} must render`);
        assert.ok(!/\b0(\.0)?%/.test(scope.text), "no fabricated 0 or 0.0% may replace the placeholder");
        return scope;
      };
      for (const mode of ["500", "nonjson", "abort", "null-metrics"]) {
        stub.mode = mode;
        await openPanel(state);
        await unavailableScope();
        assert.equal(await panel(state.page).isVisible(), true, "the Debug panel must stay usable after a failed fetch");
        await closePanel(state);
      }

      stub.mode = "ok";
      await openPanel(state);
      const good = await waitForScope(state);
      const before = good.text;
      stub.mode = "500";
      const failedTick = state.page.waitForResponse((response) =>
        /\/api\/machine-load(\?|$)/.test(response.url()) && response.status() === 500, { timeout: WAIT });
      await tick(state.page, 2000);
      await failedTick;
      // The failed response has landed; a page round-trip after it lets the
      // hook settle on it before the kept text is read (no grace window).
      await requestBarrier(state.page);
      const kept = await pollUntil(() => machineScope(state), VALUE_WAIT);
      assert.ok(kept, "the last good values must survive a failed tick");
      assert.equal(kept.text, before, "a failed tick must keep the last good values unchanged (no placeholder flash)");
      assert.ok(!kept.text.includes(ZH.unavailable), "no 不可用 may flash after a good value existed");

      stub.mode = "delayed";
      stub.hits = [];
      await closePanel(state);
      await openPanel(state);
      await pollUntil(() => stub.pending.length === 1, WAIT);
      const pendingScope = await machineScope(state, [ZH.heading, ZH.unavailable]);
      assert.ok(pendingScope, "reopening must show 不可用 (not previous values) before the fetch resolves");
      assert.ok(!VALUES.some((value) => pendingScope.text.includes(value)), "reopening must not show values from the previous open before the fetch resolves");
      releaseDelayed();
      await waitForScope(state);
      await noPageErrors(state, "ML09");
    } finally { await state.context.close(); }
  });

  // ML10 — spec req 8: the section is labelled as the host's view, makes no
  // cgroup claim, and renders — (not 0.00) where loadavg is not meaningful.
  await check("ML10", "Host badge and — for a meaningless loadavg, with no cgroup claim", async () => {
    const state = await openPage({ locale: "zh-CN", stubMode: "null-loadavg" });
    try {
      await loadSession(state);
      await openPanel(state);
      const scope = await waitForScope(state, [PINNED.cpu, PINNED.cores, DASH, PINNED.memory]);
      assert.ok(scope.text.includes(DASH), "an unavailable loadavg renders the em dash");
      assert.ok(!scope.text.includes("0.00"), "an unavailable loadavg must not render a fabricated 0.00");
      assert.ok((await panel(state.page).innerText()).includes(ZH.host), `${ZH.host} must label the values as the host's view`);
      assert.ok(!/cgroup|container|容器|配额|quota/i.test(scope.text), "the section must make no cgroup/container claim");
      await noPageErrors(state, "ML10");
    } finally { await state.context.close(); }
  });

  // ML11 — spec req 9: pinned formats, localized labels in en / zh-CN / zh-TW,
  // and the mobile single-column Debug panel without horizontal overflow.
  await check("ML11", "Pinned formats, three locales, and the mobile Debug panel without overflow", async () => {
    for (const locale of ["en-US", "zh-CN", "zh-TW"]) {
      const state = await openPage({ locale });
      try {
        await loadSession(state);
        await openPanel(state);
        const scope = await waitForScope(state);
        for (const value of VALUES) assert.ok(scope.text.includes(value), `${locale}: expected the pinned format ${value}`);
        const panelText = await panel(state.page).innerText();
        assert.ok(!panelText.includes("session.machine"), `${locale}: no missing-key fallback may render`);
        if (locale === "zh-CN") {
          assert.ok(panelText.includes(ZH.heading) && panelText.includes(ZH.host), "zh-CN labels must resolve");
        }
        if (locale === "en-US") {
          assert.ok(!panelText.includes(ZH.heading) && !panelText.includes(ZH.host), "en labels must not fall back to zh-CN");
          assert.ok(!/[\u4e00-\u9fff]/.test(scope.text), "no untranslated CJK literal may appear in en");
        }
        if (locale === "zh-TW") {
          assert.ok(!panelText.includes(ZH.heading), "zh-TW must not fall back to the zh-CN label");
          assert.ok(/[\u4e00-\u9fff]/.test(panelText), "zh-TW labels must resolve to that locale's message");
        }
        await noPageErrors(state, `ML11/${locale}`);
      } finally { await state.context.close(); }
    }
    const mobile = await openPage({ viewport: { width: 390, height: 844 }, locale: "zh-CN" });
    try {
      await loadSession(mobile);
      await openPanel(mobile);
      const scope = await waitForScope(mobile);
      const documentOverflow = await mobile.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      assert.ok(documentOverflow <= 1, `the page must not scroll horizontally at phone width (${documentOverflow}px)`);
      assert.ok(scope.overflow.panel <= 1, `the Debug panel must not overflow horizontally at phone width (${scope.overflow.panel}px)`);
      assert.ok(scope.overflow.section <= 1, `the section's values must wrap inside the Debug panel (${scope.overflow.section}px)`);
      assert.ok(scope.text.includes(PINNED.memory), "the section renders at phone width");
      await noPageErrors(mobile, "ML11/mobile");
    } finally { await mobile.context.close(); }
  });

  // ML12 is document-only (the route's own failure response cannot be induced
  // through a public interface; see artifacts/e2e-scenarios.md).

  // ML13 — spec req 10: AGENTS.md's File Map lists the new route with method
  // and purpose (docs change ships with the code).
  await check("ML13", "AGENTS.md File Map lists the new route with its method and purpose", async () => {
    const doc = readFileSync(join(root, "AGENTS.md"), "utf8");
    const fileMap = /## File Map[\s\S]*?```[\s\S]*?```/.exec(doc)?.[0] ?? "";
    assert.ok(fileMap.length > 0, "AGENTS.md must have a File Map block");
    assert.ok(/machine-load\/route\.ts\s+GET\s+\S/.test(fileMap),
      "the File Map must list machine-load/route.ts with its GET method and a one-line purpose");
    const row = fileMap.split("\n").find((line) => /machine-load\/route\.ts/.test(line)) ?? "";
    assert.ok(!/TODO|TBD/i.test(row), "the File Map row must carry a real purpose");
  });

  // ML14 — spec req 11: Linux real-data cross-check against /proc/meminfo
  // MemTotal (±1%), nproc (exact) and /proc/loadavg (±0.05), with
  // 0 <= CPU% <= 100 and used <= total.
  await check("ML14", "Linux real-data cross-check against /proc and nproc", async () => {
    assert.equal(process.platform, "linux", "the real-data oracle is Linux-only");
    const readRoute = async () => {
      const response = await fetch(`${base}${ROUTE}`, { signal: AbortSignal.timeout(60_000) });
      assert.equal(response.status, 200, `${ROUTE} must answer 200 on the real host`);
      return (await response.json()).metrics;
    };
    let metrics = await readRoute();
    if (metrics?.cpuPercent === null) {
      // A fresh sample at or after the next 2 s window: bounded poll, no fixed
      // sleep assertion.
      const deadline = Date.now() + 15_000;
      while (metrics.cpuPercent === null && Date.now() < deadline) {
        await delay(300);
        metrics = await readRoute();
      }
    }
    assert.ok(metrics, "the real host must serve a metrics payload");
    assert.ok(typeof metrics.cpuPercent === "number" && metrics.cpuPercent >= 0 && metrics.cpuPercent <= 100,
      `CPU% must be a bounded number after a baseline (got ${metrics.cpuPercent})`);
    // GNU nproc honours OMP_NUM_THREADS / OMP_THREAD_LIMIT, which would shadow
    // the affinity count the route reports and false-red the oracle on a dev
    // shell that exports them: read nproc from a copied env without both.
    const nprocEnv = { ...process.env };
    delete nprocEnv.OMP_NUM_THREADS;
    delete nprocEnv.OMP_THREAD_LIMIT;
    assert.equal(metrics.coreCount, Number(execFileSync("nproc", { encoding: "utf8", env: nprocEnv }).trim()),
      "core count must equal nproc exactly");
    const memTotalKb = Number(/MemTotal:\s+(\d+)\s+kB/.exec(readFileSync("/proc/meminfo", "utf8"))?.[1]);
    assert.ok(Number.isFinite(memTotalKb) && memTotalKb > 0, "MemTotal must be readable");
    const delta = Math.abs(metrics.memoryTotalBytes - memTotalKb * 1024) / (memTotalKb * 1024);
    assert.ok(delta <= 0.01, `memoryTotalBytes must match MemTotal within 1% (off by ${(delta * 100).toFixed(2)}%)`);
    assert.ok(metrics.memoryUsedBytes >= 0 && metrics.memoryUsedBytes <= metrics.memoryTotalBytes, "0 <= used <= total");
    // Read /proc/loadavg on both sides of the route call: on a loaded host the
    // 1-minute average legitimately moves between the two reads, so the spec's
    // ±0.05 tolerance is applied to the interval the two reads bound.
    const readProcLoad = () => readFileSync("/proc/loadavg", "utf8").trim().split(/\s+/).slice(0, 3).map(Number);
    const loadBefore = readProcLoad();
    const sampled = await readRoute();
    const loadAfter = readProcLoad();
    assert.ok(Array.isArray(sampled.loadavg) && sampled.loadavg.length === 3,
      "Linux has a meaningful loadavg: the route must serve it, not null");
    for (let index = 0; index < 3; index += 1) {
      const low = Math.min(loadBefore[index], loadAfter[index]) - 0.05;
      const high = Math.max(loadBefore[index], loadAfter[index]) + 0.05;
      const value = sampled.loadavg[index];
      assert.ok(value >= low && value <= high,
        `loadavg[${index}] ${value} must sit within 0.05 of /proc/loadavg [${loadBefore[index]}, ${loadAfter[index]}]`);
    }
    assert.ok(cpus().length > 0, "the host reports at least one logical CPU");
  });

  // ML15 — (#151 ledger): the first open's request never
  // settles. The hook aborts it at MACHINE_LOAD_REQUEST_TIMEOUT_MS (2000 ms,
  // one poll interval): the section keeps 不可用 with no
  // fabricated value and no page error, and the freed slot lets the next
  // poll tick fetch and render the values. Driven by the suite's own
  // page.clock and the request barrier; no sleeps.
  await check("ML15", "First-open timeout: the bound aborts a never-settling request, 不可用 stays, the slot frees", async () => {
    const state = await openPage({ locale: "zh-CN", stubMode: "delayed" });
    const failed = [];
    state.page.on("requestfailed", (request) => {
      if (/\/api\/machine-load(\?|$)/.test(request.url())) failed.push(request.url());
    });
    try {
      await loadSession(state, { clock: true });
      await openPanel(state);
      await waitForHits(1);
      assert.ok(await pollUntil(() => stub.pending.length === 1, WAIT), "the first request is held by the stub");
      await tick(state.page, 1999);
      await requestBarrier(state.page);
      assert.equal(failed.length, 0, "the request is not aborted before the bound");
      await tick(state.page, 1);
      assert.ok(await pollUntil(() => failed.length === 1, VALUE_WAIT), "the hook aborts the request at the 2000 ms bound");
      // The bound and the poll tick fall due together; the page clock runs the
      // abort's promise continuations before the tick, so the tick finds the
      // slot freed and requests anew (still held by the delayed stub).
      assert.ok(await pollUntil(() => stub.hits.length === 2 && stub.pending.length === 2, VALUE_WAIT),
        `the freed slot lets the coinciding tick request again (hits ${stub.hits.length}, held ${stub.pending.length})`);
      await requestBarrier(state.page);
      const scope = await pollUntil(() => machineScope(state, [ZH.heading, ZH.unavailable]), VALUE_WAIT);
      assert.ok(scope, `${ZH.heading} must still render ${ZH.unavailable} after the timeout`);
      assert.ok(!VALUES.some((value) => scope.text.includes(value)), "no value may be fabricated after the timeout");
      assert.ok(!/\b0(\.0)?%/.test(scope.text), "no fabricated 0 or 0.0% may replace the placeholder");

      // Answer the held routes with the clock still paused (the aborted one's
      // late answer goes nowhere): the fresh request renders the values.
      releaseDelayed();
      await waitForScope(state);
      assert.equal(stub.hits.length, 2, "no further request was needed");
      await noPageErrors(state, "ML15");
    } finally { await state.context.close(); }
  });
} catch (error) {
  console.error(error);
  process.exitCode = 1;
} finally {
  await h.close().catch(() => {});
  for (const [id, { title, problems }] of [...failures.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    if (!ran.has(id)) problems.push("scenario not reached");
    if (problems.length) {
      console.log(`FAIL ${id}: ${title}\n  - ${problems.join("\n  - ")}`);
      process.exitCode = 1;
    } else {
      console.log(`PASS ${id}: ${title}`);
    }
  }
  for (const id of ["ML08", "ML12"]) {
    if (!ran.has(id)) console.log(`NOTE ${id}: document-only scenario (judged by verification; see artifacts/e2e-scenarios.md)`);
  }
  if (process.exitCode) console.log(`Artifacts kept: ${work}`);
  else rmSync(work, { recursive: true, force: true });
}