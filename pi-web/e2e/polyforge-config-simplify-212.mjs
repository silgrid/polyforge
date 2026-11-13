// carries: hooks/usePolyforgeDisclosure*, lib/polyforge-disclosure*
// — Settings › Polyforge simplification acceptance (authored pre-code).
//
// The owner asked for progressive disclosure on the Settings › Polyforge page:
// a one-line detection summary, only the seven common writable keys visible by
// default, counted read-only folds, a whole-card workspace fold, default-collapsed
// read-out blocks and per-row feedback. Every scenario below asserts the spec's
// outcome through the page the browser renders, with `/api/polyforge/**` and
// `/api/plugins` stubbed exactly as e2e/polyforge-config-layout.mjs does.
// replaced (never stacked) the card-structure pins: one 工作区 /
// Workspace card (its summarize rows common, its mapping and memory_policy rows
// folded, the legend and column header above the fold), the workflow card after
// the configuration cards, and no declared-candidates card. Every count is read
// from its own element (a fold toggle, a card title), never a card's whole text.
// replaced the cli.toml pins the same way: one cli.toml card with
// a top-level and a [daemon] zone, eight common writable keys visible (five
// cli.toml keys + the three summarize keys), each zone's advanced keys folded
// behind its own counted toggle.
// moved summarize and memory_policy into cli.toml: the three
// summarize rows are the cli.toml card's [summarize] zone (still common), the
// read-only memory_policy rows render inside the cli.toml card grouped per
// workspace path (visible, never folded), and the 工作区 card folds its
// mapping rows only.
//
//   node e2e/polyforge-config-simplify-212.mjs
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, joinedLines, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-simplify-212-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-simplify-212";
const SESSION_TEXT = "E2E polyforge simplify host session";
mkdirSync(project, { recursive: true });
const ts = "2026-10-31T00:00:00.000Z";
writeHostSession(agentDir, { id: SESSION, cwd: project, text: SESSION_TEXT, timestamp: ts });

const WS = `[workspace."${project}"]`;
const MODELS = ["pi:zeta", "pi:alpha", "pi:beta"];
// (review s17#2): the Host-only keys have one surface, the Host
// region of the Host and Cells card — the cli.toml card no longer renders them.
const HOST_ONLY = ["max_concurrent_jobs", "daemon_socket", "harness_root", "operator_agent_dir"];
const COMMON = ["step_timeout", "stall_window", "claude_bin", "api_key", "summarize.models", "summarize.language", "summarize.interval_sec"];
const CONFIG = {
  pfPresent: true,
  pfVersion: "v2.20.11",
  capable: true,
  file: { sha256: "e2e-cli", exists: true },
  mapped: true,
  daemonReachable: true,
  reload: { pending: [], overridden: [] },
  workspaceFile: { sha256: "e2e-yaml", exists: true },
  items: [
    { key: "server", value: "http://127.0.0.1:1", source: "file", class: "restart-only" },
    { key: "api_key", set: true, source: "file", class: "restart-only" },
    { key: "daemon_socket", value: "/run/pf.sock", source: "file", class: "restart-only" },
    { key: "user_id", value: "usr_root", source: "file", class: "cli-only" },
    { key: "claude_bin", value: "/usr/local/bin/claude", source: "file", class: "live" },
    { key: "future_knob", value: "x", source: "file", class: "brand-new-class" },
    { key: "harness_root", value: "/opt/pf", source: "file", class: "restart-only" },
    { key: "operator_agent_dir", value: "/home/op/.pi", source: "file", class: "restart-only" },
    { key: "step_timeout", value: "30m0s", source: "file", class: "live", schema: { kind: "duration" }, daemon: { value: "30m0s" } },
    { key: "stall_window", value: "10m0s", source: "file", class: "live", schema: { kind: "duration" }, daemon: { value: "10m0s" } },
    { key: "max_concurrent_jobs", value: 6, source: "file", class: "live", schema: { kind: "int", min: 1 }, daemon: { value: 6 } },
    { key: WS, value: "e2e-project", source: "file", class: "restart-only", section: true },
    { key: "summarize.models", value: MODELS, source: "file", class: "live", schema: { kind: "model-list" } },
    { key: "summarize.language", value: "English", source: "file", class: "live", schema: { kind: "string" } },
    { key: "summarize.interval_sec", value: 30, source: "file", class: "live", schema: { kind: "int", min: 10, max: 3600 } },
  ],
  steps: [{ project: "e2e", step: "fix", declared: true, models: MODELS }, { project: "e2e", step: "code", declared: false, models: null }],
  steps_complete: true,
  memoryPolicy: {
    [project]: {
      default: { record: ["base-record"], skip: ["base-skip"], declared: true },
      "e2e-project": { record: ["project-record"], skip: [], declared: true },
    },
  },
};
// The memory_policy rows the cli.toml card shows for the workspace path (the default entry, then each project).
const MEMORY_KEYS = ["memory_policy.default", "memory_policy.e2e-project"];
// the workflow card's read, fixed so the page is the fixture's
// (the card renders after the configuration cards whatever pf is installed).
const workflowsFixture = (project) => {
  const chain = (models, from) => ({ "e2e-project": { models, from } });
  return {
    pfPresent: true,
    pfVersion: "v2.20.16",
    capable: true,
    workflows: {
      workflows_dir: "/e2e/workflows", daemon_reachable: true, reason: "", workspace: project, projects: ["e2e-project"],
      write_capable: true, write_reason: "",
      bundles: [{
        name: "feature-full",
        defaults: { model_candidates: ["pi:ff-default"], effective: chain(["pi:ff-default"], "bundle feature-full defaults.model_candidates") },
        steps: [
          { id: "grill", model_candidates: ["pi:ff-grill"],
            effective: chain(["pi:ff-grill"], "bundle feature-full step grill model_candidates") },
          { id: "code", model_candidates: ["pi:ff-code"], effective: chain(["pi:ff-code"], "bundle feature-full step code model_candidates") },
        ],
      }],
      bundle_errors: [],
    },
    workspace: { workspace: project, yaml: { sha256: "e2e-yaml", exists: true }, steps: { "e2e-project": {} } },
  };
};

const HEALTHY_DETECT = {
  pfPresent: true, pfVersion: "v2.20.11", pfVersionBelowMinimum: false, line: "internal",
  piSkillInstalled: true, piSkillEnabled: true, claudeSkillsSymlink: true, claudeSessionStartHook: true,
  daemon: { reachable: true },
};
const ABSENT_DETECT = {
  pfPresent: false, pfVersion: null, pfVersionBelowMinimum: false, line: "unknown",
  piSkillInstalled: false, piSkillEnabled: false, claudeSkillsSymlink: false, claudeSessionStartHook: false,
  daemon: null,
};
const HISTORY_ENTRY = { id: "h1", key: "step_timeout", old: "20m0s", new: "30m0s", rollbackable: true };
const DETAIL_LABELS = ["Version", "Line", "pi skill package (global)", "Claude Code skill symlink", "Claude Code SessionStart hook", "Daemon"];
const failures = [];
const check = createCheck({ failures, message: joinedLines(8), record: ([label], text) => `${label}: ${text}` });
const h = createHarness({
  storage: { "pi-locale": "en", "pi-web:session-age-filter-days": "0" },
  shape: ({ width = 1100, height = 980, ...o }) => ({ detect: HEALTHY_DETECT, pane: "cell:default", ...o, viewport: { width, height } }),
  timeout: 20_000,
  errors: [],
  session: SESSION,
  ready: SESSION_TEXT,
  setup,
  after: async (page, o) => ({ context: page.context(), page, dialog: await openSettings(page, o.pane), state: o.state }),
});
const { openPage } = h;
// the page is master-detail — the Host pane holds the machine
// status and the Host-level cards (package switch, Jobs card stream, history,
// install), the default Cell's pane the configuration cards and the workflow
// card. A list entry selects its pane; the page opens on the Host.
async function selectPane(dialog, pane) {
  const entry = pane === "host" ? "[data-pf-host-entry]" : `[data-pf-cell-entry="${pane.slice("cell:".length)}"]`;
  await dialog.locator(entry).first().click({ timeout: NAV_MS });
  await dialog.locator(`[data-pf-pane="${pane}"]`).first().waitFor({ state: "visible", timeout: NAV_MS });
}
async function openSettings(page, pane = "cell:default") {
  const show = page.locator('button[aria-label="Show sidebar"], button[aria-label="显示侧边栏"]').first();
  if (await show.isVisible().catch(() => false)) await show.click();
  await page.locator('button[aria-label="Settings"], button[aria-label="设置"]').first().click({ timeout: NAV_MS });
  const dialog = page.locator('[role="dialog"][aria-label="Settings"], [role="dialog"][aria-label="设置"]').first();
  await dialog.waitFor({ timeout: NAV_MS });
  await dialog.getByRole("button", { name: "Polyforge", exact: true }).click({ timeout: NAV_MS });
  await dialog.locator('[data-pf-pane="host"]').first().waitFor({ state: "visible", timeout: NAV_MS });
  await selectPane(dialog, pane);
  if (pane === "cell:default") await dialog.locator(".polyforge-config-row").first().waitFor({ state: "visible", timeout: NAV_MS });
  return dialog;
}
async function setup(page, o) {
  const state = { history: [{ ...HISTORY_ENTRY }], posts: [], requests: [], historyReads: 0 };
  o.state = state;
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (!url.pathname.startsWith("/api/polyforge/")) return;
    state.requests.push({ method: request.method(), url: url.pathname + url.search, body: request.postData() ?? "" });
  });
  await page.route("**/api/polyforge/**", (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const json = (body) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    if (path === "/api/polyforge/config/history") {
      if (request.method() === "POST") return json({ message: "E2E-ROLLBACK-OK-212" });
      state.historyReads += 1;
      return json({ entries: state.history });
    }
    if (path === "/api/polyforge/config") {
      if (request.method() === "POST") {
        const body = JSON.parse(request.postData() || "{}");
        state.posts.push(body);
        state.history = [{ id: `save-${state.posts.length}`, key: body.key, old: 6, new: body.value, rollbackable: true }, ...state.history];
        return json({ message: "E2E-SAVE-OK-212" });
      }
      return json(CONFIG);
    }
    if (path === "/api/polyforge/workflows") return json(workflowsFixture(project));
    if (path === "/api/polyforge/detect") return json(o.detect);
    if (path === "/api/polyforge/detect?force=1") return json(o.detect);
    return json({ error: `unexpected e2e path ${path}` });
  });
  await page.route("**/api/plugins?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({
    packages: [{ scope: "global", packageName: "polyforge", source: "/opt/polyforge" }],
  }) }));
}

const card = (dialog, label) => dialog.locator(`section[aria-label="${label}"]`).first();
// A row's key: the key cell's text without its source-file tag.
const rowKey = (row) => row.locator(".polyforge-config-key").evaluate((cell) => {
  const copy = cell.cloneNode(true);
  copy.querySelectorAll(".polyforge-config-file-tag").forEach((tag) => tag.remove());
  return (copy.textContent ?? "").trim();
});
const visibleRowKeys = async (scope) => {
  const rows = scope.locator(".polyforge-config-row:visible");
  const keys = [];
  for (let index = 0; index < await rows.count(); index += 1) keys.push(await rowKey(rows.nth(index)));
  return keys.sort();
};
// A fold's count is read from that fold's own toggle, never a card's whole text.
const foldCount = async (section) => {
  const text = (await section.locator(".polyforge-disclosure-toggle").first().innerText()).trim();
  const match = /\((\d+)\)/.exec(text);
  assert.ok(match, `the fold toggle states no count: ${text}`);
  return Number(match[1]);
};
async function expandAll(dialog) {
  const target = '[aria-expanded="false"]:visible, details:not([open]) > summary:visible';
  for (let round = 0; round < 12; round += 1) {
    const remaining = await dialog.locator(target).count();
    if (!remaining) return;
    for (let index = 0; index < remaining; index += 1) await dialog.locator(target).nth(0).click({ timeout: 4_000 }).catch(() => {});
    if (await dialog.locator(target).count() === 0) return;
  }
}
async function waitUntil(predicate, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await predicate()) return true;
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  return false;
}
async function top(locator, name) {
  const box = await locator.first().boundingBox();
  assert.ok(box, `${name} is not visible`);
  return box.y;
}
try {
  await h.startServer({
    name: "polyforge-config-simplify-212",
    logFile: join(work, "server.log"),
    env: { HOME: work, PI_CODING_AGENT_DIR: agentDir },
  });
  await h.launchBrowser(() => chromium.launch());

  await check("S212-1 default collapsed configuration set at 1100px", async () => {
    const { context, dialog } = await openPage();
    try {
      assert.deepEqual(await visibleRowKeys(dialog), [...COMMON, ...MEMORY_KEYS].sort(),
        "the default page does not show exactly the seven common writable keys (max_concurrent_jobs is Host-only) and the read-only memory_policy rows");
      assert.equal(await card(dialog, "cli.toml · [daemon]").count(), 0, "the separate [daemon] card still renders");
      const cli = card(dialog, "cli.toml");
      // Top-level zone: server, user_id + the unknown future_knob (daemon_socket is Host-only).
      assert.equal(await foldCount(cli.locator(".polyforge-config-zone.is-flat")), 3, "the cli.toml zone's advanced toggle does not state its 3 hidden rows");
      assert.equal(await cli.locator(".polyforge-config-zone.is-daemon .polyforge-disclosure-toggle").count(), 0, "the [daemon] zone folds rows the cli.toml card no longer holds (harness_root, operator_agent_dir are Host-only)");
      assert.deepEqual(await visibleRowKeys(cli.locator(".polyforge-config-zone.is-summarize")), ["summarize.interval_sec", "summarize.language", "summarize.models"],
        "the cli.toml card's [summarize] zone does not show exactly the three summarize keys");
      assert.deepEqual(await visibleRowKeys(cli.locator(".polyforge-config-zone.is-memory-policy")), [...MEMORY_KEYS].sort(),
        "the cli.toml card does not show the path's memory_policy rows");
      const workspace = card(dialog, "Workspace");
      assert.deepEqual(await visibleRowKeys(workspace), [], "the 工作区 card shows a row besides its folded mapping");
      assert.equal(await foldCount(workspace), 1, "the 工作区 fold does not count its one mapping row");
      // The legend and the column header stay outside the fold: visible while it is collapsed.
      assert.equal(await workspace.locator(".polyforge-disclosure-toggle").first().getAttribute("aria-expanded"), "false");
      assert.ok(await workspace.locator(".polyforge-config-legend").first().isVisible(), "the legend is hidden inside the collapsed fold");
      assert.ok(await workspace.locator(".polyforge-config-grid-header").first().isVisible(), "the column header is hidden inside the collapsed fold");
      // Every 工作区 row names its own file.
      for (const row of await workspace.locator(".polyforge-config-row").all()) {
        const mapping = await row.locator(".polyforge-config-workspace-path").count() > 0;
        const tag = (await row.locator(".polyforge-config-file-tag").textContent() ?? "").trim();
        assert.ok(mapping, `${await rowKey(row)} is not a mapping row`);
        assert.equal(tag, "cli.toml", `${await rowKey(row)} carries the wrong file tag ${tag}`);
      }
    } finally { await context.close(); }
  });

  await check("S212-2 expanding every disclosure keeps the whole key inventory reachable", async () => {
    const { context, dialog } = await openPage();
    try {
      assert.ok(await dialog.locator('[aria-expanded="false"]:visible, details:not([open]) > summary:visible').count() > 0,
        "the page renders no default-collapsed disclosure at all");
      await expandAll(dialog);
      const keys = await visibleRowKeys(dialog);
      const cardItems = CONFIG.items.filter((entry) => !HOST_ONLY.includes(entry.key));
      assert.equal(keys.length, cardItems.length + MEMORY_KEYS.length, `expanded rows ${keys.length}, stub items ${cardItems.length} (Host-only excluded) + memory rows ${MEMORY_KEYS.length}`);
      for (const key of HOST_ONLY) assert.equal(keys.filter((entry) => entry === key).length, 0, `${key} is Host-only and must not render in the cli.toml card`);
      for (const key of [...cardItems.filter((entry) => !entry.section).map((entry) => entry.key), ...MEMORY_KEYS]) {
        assert.equal(keys.filter((entry) => entry === key).length, 1, `${key} does not have exactly one row`);
      }
      for (const key of MEMORY_KEYS) {
        const row = dialog.locator(".polyforge-config-row").filter({ has: dialog.page().getByText(key, { exact: true }) }).first();
        assert.equal(await row.locator("input, textarea, button").count(), 0, `${key} is not read-only`);
      }
      for (const key of COMMON) {
        const input = dialog.locator(`input[aria-label="${key}"]`).first();
        assert.ok(await input.isEnabled(), `${key} has no enabled write input after expanding`);
        const row = dialog.locator(`.polyforge-config-row:has(input[aria-label="${key}"])`).first();
        assert.ok(await row.getByRole("button", { name: "Save", exact: true }).count() >= 1, `${key} has no Save control after expanding`);
      }
      assert.ok(await card(dialog, "cli.toml").getByText(/Other \(brand-new-class\)/).count() >= 1,
        "the unknown key lost its fallback class label");
    } finally { await context.close(); }
  });

  await check("S212-3 read-only rows share one muted presentation across cards", async () => {
    const { context, dialog } = await openPage();
    try {
      await expandAll(dialog);
      const rows = dialog.locator(".polyforge-config-row.is-readonly");
      assert.ok(await rows.count() >= 4, "no read-only rows rendered after expanding");
      const colours = await rows.evaluateAll((nodes) => nodes.map((row) => {
        const leaf = row.querySelector(".polyforge-config-value, .polyforge-config-workspace-path, .polyforge-config-chips, .polyforge-config-empty");
        return leaf ? getComputedStyle(leaf).color : null;
      }));
      assert.ok(colours.every(Boolean), "a read-only row has no value element");
      assert.equal(new Set(colours).size, 1, `read-only values use different colours: ${[...new Set(colours)].join(" / ")}`);
      assert.equal(await rows.locator("input, button").count(), 0, "a read-only row renders a write control");
    } finally { await context.close(); }
  });

  await check("S212-4 the page order is the confirmed top-to-bottom one", async () => {
    // owner-intended change: the confirmed order holds within
    // each home of the master-detail page — the Host entry lists above the
    // default Cell, the Host pane runs status → Host-level cards → read-outs,
    // the default Cell's pane runs its configuration cards → workflow — and
    // the footer closes the page below either pane.
    const { context, dialog } = await openPage({ pane: "host" });
    try {
      const homes = [
        ["list", null, [
          ["Host entry", dialog.locator("[data-pf-host-entry]").first()],
          ["default Cell entry", dialog.locator('[data-pf-cell-entry="default"]').first()],
          ["Add Cell entry", dialog.locator("[data-pf-add-entry]").first()],
        ]],
        ["Host pane", "host", [
          ["machine status", dialog.getByText("Machine status", { exact: true }).first()],
          ["package switch", dialog.getByText("Polyforge package (global)", { exact: true }).first()],
          ["Jobs card stream", dialog.getByText("Jobs card stream", { exact: true }).first()],
          ["history", dialog.getByText(/^Recent panel writes/).first()],
          ["install command", dialog.getByText("Install / upgrade command", { exact: true }).first()],
        ]],
        ["default Cell pane", "cell:default", [
          ["cli.toml", dialog.getByText("cli.toml", { exact: true }).first()],
          ["cli.toml [daemon]", dialog.getByText("cli.toml [daemon]", { exact: true }).first()],
          ["workspace", card(dialog, "Workspace").locator(".polyforge-config-card-title").first()],
          ["workflow", card(dialog, "Workflow").locator(".polyforge-config-card-title").first()],
        ]],
      ];
      for (const [home, pane, landmarks] of homes) {
        if (pane) await selectPane(dialog, pane);
        const ys = [];
        for (const [name, locator] of landmarks) ys.push({ name, y: await top(locator, `${home}: ${name}`) });
        for (let index = 1; index < ys.length; index += 1) {
          assert.ok(ys[index].y >= ys[index - 1].y - 1,
            `${home}: ${ys[index].name} renders above ${ys[index - 1].name}: ${ys[index].y.toFixed(1)} < ${ys[index - 1].y.toFixed(1)}`);
        }
      }
      // The footer sits below the split view (the list and the scrolling
      // detail), so it closes the page whichever pane is shown.
      for (const pane of ["host", "cell:default"]) {
        await selectPane(dialog, pane);
        const split = await dialog.locator(".config-split-view").first().boundingBox();
        const footer = await top(dialog.getByRole("button", { name: "Re-detect", exact: true }), `${pane}: footer`);
        assert.ok(split && footer >= split.y + split.height - 1, `${pane}: the footer renders inside the split view (${footer.toFixed(1)} < ${(split?.y ?? 0) + (split?.height ?? 0)})`);
      }
    } finally { await context.close(); }
  });

  await check("S212-5 detection collapses to one summary line and names abnormal state", async () => {
    const healthy = await openPage({ pane: "host" });
    try {
      const status = card(healthy.dialog, "Machine status");
      for (const label of DETAIL_LABELS) {
        assert.equal(await status.getByText(label, { exact: true }).first().isVisible().catch(() => false), false,
          `${label} is visible while detection is collapsed`);
      }
      assert.match(await status.innerText(), /v2\.20\.11/, "the collapsed summary does not name the detected version");
      const toggle = status.locator('[aria-expanded="false"]:visible, summary:visible').first();
      assert.ok(await toggle.count() > 0, "the detection region has no summary toggle");
      await toggle.click({ timeout: 10_000 });
      for (const label of DETAIL_LABELS) {
        assert.ok(await status.getByText(label, { exact: true }).first().isVisible().catch(() => false),
          `${label} is not visible after expanding detection`);
      }
    } finally { await healthy.context.close(); }

    const absent = await openPage({ detect: ABSENT_DETECT, pane: "host" });
    try {
      const status = card(absent.dialog, "Machine status");
      for (const label of DETAIL_LABELS) {
        assert.equal(await status.getByText(label, { exact: true }).first().isVisible().catch(() => false), false,
          `${label} is visible while abnormal detection is collapsed`);
      }
      assert.ok(await absent.dialog.getByText("Polyforge is not installed on this machine.", { exact: true }).first().isVisible(),
        "the absent state is not visible text on the collapsed page");
    } finally { await absent.context.close(); }
  });

  await check("S212-6 read-out blocks are collapsed with counts and the install command is visible when pf is absent", async () => {
    const healthy = await openPage({ pane: "host" });
    try {
      // the declared-candidates read-out is gone (the workflow card carries the steps).
      assert.equal(await card(healthy.dialog, "Declared step candidates").count(), 0, "the declared-candidates card still renders");
      assert.equal(await healthy.dialog.getByText(/^Declared step candidates/).count(), 0, "the declared-candidates title still renders");
      assert.equal(await healthy.dialog.locator(".polyforge-config-history-row:visible").count(), 0,
        "history entries are visible in the default state");
      const history = healthy.dialog.locator('section[aria-label="Recent panel writes"]').first();
      const historyCount = (await history.locator(".polyforge-config-candidate-count").first().innerText()).trim();
      assert.match(historyCount, /^\D*1\D*$/, `the history title's own count does not state its 1 entry: ${historyCount}`);
      assert.equal(await healthy.dialog.getByText(/tools\/install\.sh/).first().isVisible().catch(() => false), false,
        "the install command is visible although detection is healthy");
      await expandAll(healthy.dialog);
      assert.ok(await healthy.dialog.locator(".polyforge-config-history-row:visible").count() > 0, "expanding history reveals no entries");
      assert.ok(await healthy.dialog.getByText(/tools\/install\.sh/).first().isVisible(), "expanding the install block reveals no command");
    } finally { await healthy.context.close(); }

    // the page opens on the Host pane, so pf's absence and its
    // remedy are on screen without selecting anything.
    const absent = await openPage({ detect: ABSENT_DETECT, pane: "host" });
    try {
      assert.ok(await absent.dialog.getByText(/tools\/install\.sh/).first().isVisible(),
        "the install command is not visible without an extra disclosure when pf is absent");
      const copy = absent.dialog.getByRole("button", { name: "Copy install command", exact: true }).first();
      assert.ok(await copy.isVisible(), "the install copy control is not visible when pf is absent");
    } finally { await absent.context.close(); }
  });

  await check("S212-7 each region's open state survives a reload", async () => {
    const { context, page, dialog, state } = await openPage({ pane: "host" });
    try {
      const detectToggle = card(dialog, "Machine status").locator('[aria-expanded="false"]:visible, summary:visible').first();
      assert.ok(await detectToggle.count() > 0, "detection has no open/closed toggle");
      await detectToggle.click({ timeout: 10_000 });
      await selectPane(dialog, "cell:default");
      const workspaceToggle = card(dialog, "Workspace").locator('[aria-expanded="false"]:visible, summary:visible').first();
      // a Cell pane reads its own file when first shown, so its cards land after that read
      await workspaceToggle.waitFor({ state: "visible", timeout: NAV_MS }).catch(() => {});
      assert.ok(await workspaceToggle.count() > 0, "the workspace card has no open/closed toggle");
      await workspaceToggle.click({ timeout: 10_000 });
      assert.ok(await dialog.locator(".polyforge-config-row:visible .polyforge-config-workspace-path").first().isVisible(),
        "the workspace rows did not open");
      const stored = await page.evaluate(() => Object.keys(localStorage).filter((key) => /polyforge/i.test(key)));
      assert.ok(stored.length >= 1, "no browser key holds the open/closed state");
      assert.ok(state.requests.every((request) => request.method === "GET" || !/disclosure|collapsed/.test(request.body)),
        "a request carried the collapse state");
      await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
      await page.getByText(SESSION_TEXT).first().waitFor({ state: "attached", timeout: NAV_MS });
      const reopened = await openSettings(page, "host");
      const status = card(reopened, "Machine status");
      for (const label of DETAIL_LABELS) {
        assert.ok(await status.getByText(label, { exact: true }).first().isVisible().catch(() => false),
          `${label} did not reopen from the stored detection state`);
      }
      await selectPane(reopened, "cell:default");
      await reopened.locator(".polyforge-config-row:visible .polyforge-config-workspace-path").first().waitFor({ state: "visible", timeout: NAV_MS }).catch(() => {});
      assert.ok(await reopened.locator(".polyforge-config-row:visible .polyforge-config-workspace-path").first().isVisible(),
        "the workspace card did not reopen from the stored state");
    } finally { await context.close(); }
  });

  await check("S212-8 per-key save and validation feedback render under their row", async () => {
    const { context, page, dialog, state } = await openPage();
    try {
      // (review s17#2): max_concurrent_jobs is Host-only; the int key with a schema bound here is summarize.interval_sec
      const input = dialog.locator('input[aria-label="summarize.interval_sec"]').first();
      const row = dialog.locator('.polyforge-config-row:has(input[aria-label="summarize.interval_sec"])').first();
      const footer = dialog.locator(".config-footer").first();
      const rowBefore = await row.innerText();
      const footerBefore = await footer.innerText();
      const historyReadsBefore = state.historyReads;
      await input.fill("70");
      await Promise.all([
        page.waitForResponse((response) => response.url().endsWith("/api/polyforge/config") && response.request().method() === "POST", { timeout: 20_000 }),
        row.getByRole("button", { name: "Save", exact: true }).click(),
      ]);
      assert.ok(await waitUntil(async () => (await row.innerText()) !== rowBefore), "the save result is not under the summarize.interval_sec row");
      assert.equal(await footer.innerText(), footerBefore, "the footer duplicates or replaces its page-level status with the row's save result");
      assert.equal(state.posts.length, 1, `the save issued ${state.posts.length} config writes`);
      assert.ok(await waitUntil(() => state.historyReads > historyReadsBefore), "the change history was not re-read after the save");
      // the history is a Host-level read-out (the Host pane).
      await selectPane(dialog, "host");
      await expandAll(dialog);
      const historyRows = await dialog.locator(".polyforge-config-history-row:visible").allInnerTexts();
      assert.ok(historyRows.length >= 2, `the change history did not gain the save entry (${historyRows.length} rows)`);
      assert.ok(historyRows.some((text) => text.includes("summarize.interval_sec") && text.includes("70")),
        "the change history has no entry for the saved summarize.interval_sec value");
      await selectPane(dialog, "cell:default");

      await input.fill("0");
      await row.getByRole("button", { name: "Save", exact: true }).click();
      const alert = row.locator('[role="alert"]').first();
      await alert.waitFor({ state: "visible", timeout: 10_000 });
      assert.ok((await alert.innerText()).trim().length > 0, "the validation error has no text");
      assert.equal(state.posts.length, 1, "an invalid value reached the write path");

      // a connection key writes only after its confirmation,
      // which a cancel dismisses without a write; no save reloads the daemon.
      // (restated): server is an identity key and not editable,
      // so a same-identity connection key carries the confirmation scenario
      // (user_id: daemon_socket is Host-only, edited in the Host region).
      await expandAll(dialog);
      assert.equal(await dialog.locator('input[aria-label="server"]').count(), 0, "server must not be editable in place");
      const server = dialog.locator('input[aria-label="user_id"]').first();
      const serverRow = dialog.locator('.polyforge-config-row:has(input[aria-label="user_id"])').first();
      await server.fill("usr_212");
      await serverRow.getByRole("button", { name: "Save", exact: true }).click();
      const confirm = dialog.locator('[role="alertdialog"]').first();
      await confirm.waitFor({ state: "visible", timeout: 10_000 });
      assert.equal(state.posts.length, 1, "a connection key posted before its confirmation");
      await confirm.getByRole("button", { name: "Cancel", exact: true }).click();
      assert.equal(await confirm.count(), 0, "cancel did not dismiss the confirmation");
      assert.equal(state.posts.length, 1, "a cancelled confirmation wrote");
      await serverRow.getByRole("button", { name: "Save", exact: true }).click();
      await Promise.all([
        page.waitForResponse((response) => response.url().endsWith("/api/polyforge/config") && response.request().method() === "POST", { timeout: 20_000 }),
        dialog.locator('[role="alertdialog"]').first().getByRole("button", { name: "Confirm save", exact: true }).click(),
      ]);
      assert.equal(state.posts.length, 2, "a confirmed connection key did not write exactly once");
      assert.equal(state.posts[1].key, "user_id");
      assert.ok(!state.requests.some((request) => /\/config\/reload/.test(request.url)), "a save called the reload route");
    } finally { await context.close(); }
  });
} catch (error) {
  console.error(`ABORT ${error?.stack ?? error}`);
  process.exitCode = 1;
} finally {
  await h.close();
}

console.log(`\n${check.count - failures.length}/${check.count} simplification checks passed`);
if (failures.length) {
  console.error(`\n${failures.length} simplification check(s) failed:`);
  for (const failure of failures) console.error(`- ${failure}`);
  process.exitCode = 1;
}
