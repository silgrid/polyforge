// carries: components/PolyforgeConfig*, components/polyforge-config-helpers*, components/PolyforgeInstallCard*, hooks/usePolyforgeDisclosure*, lib/polyforge-disclosure*, hooks/usePolyforgePanelPreferences*, lib/polyforge-panel-preferences*, lib/polyforge-config*, lib/polyforge-workflows*, lib/polyforge-install-pure*
// carries: components/SettingsPanel*, components/SettingsUi*, components/settings-ui-helpers*, lib/settings-navigation*, lib/package-settings*, app/api/plugins*
// carries: app/settings.css
// — Settings › Polyforge configuration page LAYOUT acceptance.
//
// Authored as the reproduction for the owner's second photo review: #88 made the
// rows a five-column subgrid inside each card, which leaves every card with its
// own column template (so value columns do not line up), keeps an always-empty
// daemon column in cli.toml, parks each Save button at the row's far right and
// renders pf's reload classes as bare English. This suite measures the rendered
// page at the owner's desktop width (1100px) and at the phone breakpoint, with
// the config read mocked so the fixture is the page's data, not pf's.
// the summarize keys and the workspace mapping are one 工作区 /
// Workspace card, and the workflow card (its read mocked too) renders after
// the configuration cards; the assertions below target those cards.
// the cli.toml keys are one cli.toml card with two file
// zones (top-level, [daemon]); "flat" and "daemon" below are those zones.
// the fixture now carries one long non-divergent step chain
// plus its declared chain, and this suite measures the wrap at 1100/390 and
// the machine-scope hint line at 1280/640/390.
// summarize is cli.toml's [summarize] table — the third zone
// of the cli.toml card ("summarize" below); the 工作区 card keeps the mapping
// rows only, so the value-column and Save-gap probes read the summarize zone.
// owner-intended change: the page may break a Latin token
// only after - _ / . : , ; | or beside CJK, with no horizontal overflow; the
// fixture carries a key wider than its column, a long read-only value, long
// model ids and paths, history entries and the install command, and the page
// is scanned at 1280/768/375 x en/zh-CN with every disclosure region open.
//
//   node e2e/polyforge-config-layout.mjs
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, joinedLines, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-layout-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-layout-session";
mkdirSync(project, { recursive: true });
const ts = "2026-10-29T00:00:00.000Z";
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E polyforge layout host session", timestamp: ts });

const WS = `[workspace."${project}"]`;
// a realistic long model id (this host's shape), long enough
// to exceed its chip/column, with every separator the rule allows.
const LONG_MODEL = "pi:example-gateway/deepseek-v4.1-flash-1017";
const LONG_KEY = "experimental_scheduler_quarantine_threshold_override_ratio";
const LONG_VALUE = "capsule-quarantine-threshold-override-2026-11-03T12:00:00Z";
const MODELS = ["pi:zeta", LONG_MODEL, "pi:alpha", "pi:beta"];
// long enough to exceed the card at 1100px and to force the
// declared lead segment onto its own wrapped block before the effective chain.
const WRAP_DECLARED = Array.from({ length: 16 }, (_, i) => `pi:declared-wrap-${i}`);
const WRAP_EFFECTIVE = Array.from({ length: 16 }, (_, i) => `pi:effective-wrap-${i}`);
const HINTS = {
  en: "These settings belong to THIS machine (cli.toml and the mapped workspace .polyforge.yaml), not to the aihub server.",
  "zh-CN": "这些设置属于本机（cli.toml 与已映射工作区的 .polyforge.yaml），不属于 aihub 服务端。",
};
// the workflow card's submit-precedence note is measured by its
// full rendered text, like the machine-scope hint above.
const SUBMIT_NOTES = {
  en: "`pf submit --models` outranks every level above; this panel never edits it.",
  "zh-CN": "`pf submit --models` 优先级最高，高于此处所有层级；本面板不编辑它。",
  "zh-TW": "`pf submit --models` 優先度最高，高於此處所有層級；本面板不編輯它。",
};
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
    // longer than the 190px key column, and a read-only value
    // longer than its value column at every width this suite scans.
    { key: LONG_KEY, value: LONG_VALUE, source: "file", class: "restart-only" },
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
};

// three history entries so the read-out exercises the aligned
// key | old -> new | rollback row with long keys and values too.
const HISTORY = [
  { id: "h1", at: ts, scope: "cli", cwd: project, key: "step_timeout", old: "30m0s", new: "45m0s", secret: false, rollbackable: true },
  { id: "h2", at: ts, scope: "cli", cwd: project, key: LONG_KEY, old: LONG_VALUE, new: "capsule-quarantine-threshold-override-2026-11-04T00:00:00Z", secret: false, rollbackable: true },
  { id: "h3", at: ts, scope: "workspace", cwd: project, key: "summarize.models", old: ["pi:zeta"], new: [LONG_MODEL], secret: false, rollbackable: true },
];

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
        name: "autopilot",
        defaults: { model_candidates: ["pi:auto-default"], effective: chain(["pi:auto-default"], "bundle autopilot defaults.model_candidates") },
        steps: [
          { id: "grill", model_candidates: ["pi:auto-grill"],
            effective: chain(["pi:shared-grill"], "bundle autopilot step grill model_candidates") },
          { id: "code", model_candidates: ["pi:auto-code"], effective: chain(["pi:auto-code"], "bundle autopilot step code model_candidates") },
          { id: "wrap", model_candidates: WRAP_DECLARED, effective: chain(WRAP_EFFECTIVE, "bundle autopilot step wrap model_candidates") },
        ],
      }, {
        name: "feature-full",
        defaults: { model_candidates: ["pi:ff-default"], effective: chain(["pi:ff-default"], "bundle feature-full defaults.model_candidates") },
        steps: [
          { id: "grill", model_candidates: ["pi:ff-grill"],
            effective: chain(["pi:shared-grill"], "bundle feature-full step grill model_candidates") },
          { id: "code", model_candidates: [LONG_MODEL], effective: chain([LONG_MODEL], "bundle feature-full step code model_candidates") },
          { id: "wrap", model_candidates: WRAP_DECLARED, effective: chain(WRAP_EFFECTIVE, "bundle feature-full step wrap model_candidates") },
        ],
      }],
      bundle_errors: [],
    },
    workspace: { workspace: project, yaml: { sha256: "e2e-yaml", exists: true }, steps: { "e2e-project": { wrap: { declared: true, models: WRAP_DECLARED } } } },
  };
};

const failures = [];
const check = createCheck({ failures, message: joinedLines(8), record: ([label], text) => `${label}: ${text}` });

const h = createHarness();
let base;
try {
  ({ base } = await h.startServer({
    name: "polyforge-config-layout",
    logFile: join(work, "server.log"),
    env: { HOME: work, PI_CODING_AGENT_DIR: agentDir },
  }));
  await h.launchBrowser(() => chromium.launch());

  // the page is master-detail — the Host pane holds the
  // Host-level cards (the history and the Jobs card preferences among them),
  // the default Cell's pane the cli.toml, 工作区 and workflow cards.
  const selectPane = async (page, pane) => {
    const entry = pane === "host" ? "[data-pf-host-entry]" : `[data-pf-cell-entry="${pane.slice("cell:".length)}"]`;
    await page.locator(entry).first().click();
    await page.locator(`[data-pf-pane="${pane}"]`).first().waitFor({ state: "visible", timeout: 30_000 });
  };
  const open = async ({ width = 1100, height = 980, locale = "en" } = {}) => {
    const context = await h.browser.newContext({ viewport: { width, height }, locale: "en-US" });
    await context.addInitScript((selected) => {
      try {
        window.localStorage.setItem("pi-locale", selected);
        window.localStorage.setItem("pi-web:session-age-filter-days", "0");
        // the break scan runs with every disclosure region open.
        window.localStorage.setItem("pi-web:polyforge-config-disclosure", JSON.stringify({
          detect: true, cliToml: true, daemon: true, workspace: true, history: true, install: true, workflow: true,
        }));
      } catch { /* storage unavailable */ }
    }, locale);
    const page = await context.newPage();
    page.setDefaultTimeout(20_000);
    await page.route("**/api/polyforge/config/history", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ entries: HISTORY }) }));
    await page.route("**/api/polyforge/config?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(CONFIG) }));
    await page.route("**/api/polyforge/workflows?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(workflowsFixture(project)) }));
    await page.route("**/api/polyforge/detect*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({
      pfPresent: true, pfVersion: "v2.20.11", pfVersionBelowMinimum: false, line: "internal",
      piSkillInstalled: true, piSkillEnabled: true, claudeSkillsSymlink: true, claudeSessionStartHook: true,
      daemon: { reachable: true },
    }) }));
    await page.route("**/api/plugins?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({
      packages: [{ scope: "global", packageName: "polyforge", source: "/opt/polyforge" }],
    }) }));
    await page.goto(`${base}/?session=${SESSION}`, { waitUntil: "domcontentloaded", timeout: NAV_MS });
    await page.getByText("E2E polyforge layout host session").first().waitFor({ state: "attached", timeout: NAV_MS });
    const show = page.locator('button[aria-label="Show sidebar"], button[aria-label="显示侧边栏"]').first();
    if (await show.isVisible().catch(() => false)) await show.click();
    await page.locator('button[aria-label="Settings"], button[aria-label="设置"], button[aria-label="設定"]').first().click();
    const dialog = page.locator('[role="dialog"][aria-label="Settings"], [role="dialog"][aria-label="设置"], [role="dialog"][aria-label="設定"]').first();
    await dialog.getByRole("button", { name: "Polyforge", exact: true }).click();
    // the cli.toml card is the default Cell's own pane.
    await selectPane(page, "cell:default");
    const flatLabel = locale === "zh-CN" ? "cli.toml" : "cli.toml";
    await dialog.locator(`[aria-label="${flatLabel}"]`).first().waitFor({ state: "attached", timeout: 30_000 });
    return { context, page, dialog };
  };

  const cli = (dialog) => dialog.locator('section[aria-label="cli.toml"]');
  const cards = (dialog) => ({
    flat: cli(dialog).first().locator(".polyforge-config-zone.is-flat"),
    daemon: cli(dialog).first().locator(".polyforge-config-zone.is-daemon"),
    summarize: cli(dialog).first().locator(".polyforge-config-zone.is-summarize"),
    workspace: dialog.locator('section[aria-label="Workspace"]').first(),
    workflow: dialog.locator('section[aria-label="Workflow"]').first(),
  });
  const workflowRowGeometry = async (dialog, id) => {
    const card = cards(dialog).workflow;
    const measured = await card.locator(".polyforge-workflow-row").evaluateAll((nodes, wanted) => {
      const row = nodes.find((node) => (node.querySelector(".polyforge-workflow-id")?.textContent ?? "").trim() === wanted);
      if (!row) return null;
      const rect = (node) => {
        if (!node) return null;
        const r = node.getBoundingClientRect();
        return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height };
      };
      const card = row.closest(".polyforge-config-card");
      const summary = row.querySelector(".polyforge-workflow-summary");
      const effective = summary?.querySelector(".polyforge-workflow-effective");
      const effectiveLabel = effective?.querySelector(".polyforge-workflow-label");
      const suffix = summary?.querySelector(".polyforge-workflow-bundle-suffix");
      return {
        id: (row.querySelector(".polyforge-workflow-id")?.textContent ?? "").trim(),
        divergent: summary?.classList.contains("is-divergent") ?? false,
        summaryFlexWrap: summary ? getComputedStyle(summary).flexWrap : "",
        row: rect(row),
        summary: summary ? { ...rect(summary), scrollWidth: summary.scrollWidth, clientWidth: summary.clientWidth } : null,
        card: card ? { ...rect(card), scrollWidth: card.scrollWidth, clientWidth: card.clientWidth } : null,
        idBox: rect(row.querySelector(".polyforge-workflow-id")),
        effective: rect(effective),
        effectiveLabel: rect(effectiveLabel),
        suffix: rect(suffix),
        suffixInnerText: suffix?.innerText ?? "",
        bundleNames: [...(suffix?.querySelectorAll(".polyforge-workflow-bundle-name") ?? [])].map((node) => (node.textContent ?? "").trim()),
      };
    }, id);
    return measured;
  };
  const hintGeometry = async (dialog, text) => {
    const locator = dialog.getByText(text, { exact: true }).first();
    await locator.waitFor({ state: "attached", timeout: 30_000 });
    return locator.evaluate((element) => {
      const style = getComputedStyle(element);
      const box = element.getBoundingClientRect();
      const container = element.closest(".polyforge-config-panel") ?? element.parentElement;
      const textNode = [...element.childNodes].find((node) => node.nodeType === Node.TEXT_NODE && (node.textContent ?? "").trim());
      const lines = [];
      const chars = [];
      if (textNode) {
        const range = document.createRange();
        const value = textNode.textContent ?? "";
        for (let i = 0; i < value.length; i += 1) {
          range.setStart(textNode, i);
          range.setEnd(textNode, i + 1);
          const r = range.getBoundingClientRect();
          chars.push({ ch: value[i], rect: r.width || r.height ? r : null });
          if (/\s/.test(value[i]) || (!r.width && !r.height)) continue;
          const top = Math.round(r.top);
          let line = lines.find((entry) => Math.abs(entry.top - top) <= 2);
          if (!line) { line = { top, count: 0 }; lines.push(line); }
          line.count += 1;
        }
      }
      lines.sort((a, b) => a.top - b.top);
      // a phone line change is legal only after - _ / . : , ; |
      // or beside a CJK glyph (the rule), never inside a token.
      const SEPARATORS = new Set(["-", "_", "/", ".", ":", ",", ";", "|"]);
      const CJK = /[\u2E80-\u2FFF\u3000-\u303F\u3040-\u30FF\u3400-\u4DBF\u4E00-\u9FFF\uAC00-\uD7AF\uF900-\uFAFF\uFF00-\uFFEF]/;
      const illegalBreaks = [];
      for (let i = 1; i < chars.length; i += 1) {
        const before = chars[i - 1];
        const after = chars[i];
        if (!before.rect || !after.rect) continue;
        if (/\s/.test(before.ch) || /\s/.test(after.ch)) continue;
        if (Math.abs(before.rect.top - after.rect.top) <= 2) continue;
        if (SEPARATORS.has(before.ch) || CJK.test(before.ch) || CJK.test(after.ch)) continue;
        illegalBreaks.push(`${before.ch}|${after.ch}`);
      }
      return {
        text: element.textContent ?? "",
        className: String(element.className),
        title: element.getAttribute("title"),
        whiteSpace: style.whiteSpace,
        textOverflow: style.textOverflow,
        textWrap: style.textWrap || style.textWrapStyle || "",
        lineHeight: parseFloat(style.lineHeight) || 0,
        height: box.height,
        scrollWidth: element.scrollWidth,
        clientWidth: element.clientWidth,
        scrollHeight: element.scrollHeight,
        clientHeight: element.clientHeight,
        containerScrollWidth: container?.scrollWidth ?? 0,
        containerClientWidth: container?.clientWidth ?? 0,
        lines,
        illegalBreaks,
      };
    });
  };
  const assertLongStepRowWrapped = async (dialog, width) => {
    const row = await workflowRowGeometry(dialog, "wrap");
    assert.ok(row, `the ${width}px workflow card has no long-chain "wrap" row`);
    assert.equal(row.divergent, false, `the ${width}px long chain is not the fixture's non-divergent row`);
    assert.equal(row.summaryFlexWrap, "wrap", `the ${width}px long-chain summary is not a wrapping flex container (${row.summaryFlexWrap})`);
    assert.ok(row.summary.scrollWidth <= row.summary.clientWidth + 1, `the ${width}px long-chain summary overflows (${row.summary.scrollWidth} > ${row.summary.clientWidth})`);
    assert.ok(row.card.scrollWidth <= row.card.clientWidth + 1, `the ${width}px workflow card overflows (${row.card.scrollWidth} > ${row.card.clientWidth})`);
    assert.ok(row.summary.height > 24, `the ${width}px long-chain row did not wrap (summary height ${row.summary.height})`);
    assert.ok(row.effectiveLabel && row.effectiveLabel.top >= row.idBox.top + 8, `the ${width}px long-chain row did not wrap the effective segment below the id/declared segment`);
    assert.ok(row.suffix, `the ${width}px long-chain row lost its bundle suffix`);
    assert.deepEqual(row.bundleNames, ["autopilot", "feature-full"], `the ${width}px long-chain row's bundle suffix is not the fixture's two bundles`);
    assert.ok(row.suffix.right >= row.summary.right - 2, `the ${width}px long-chain bundle suffix is not flush right (${row.suffix.right} vs ${row.summary.right})`);
    assert.ok(Math.abs(row.suffix.bottom - row.summary.bottom) <= 10, `the ${width}px long-chain bundle suffix does not end the last line`);
    assert.match(row.suffixInnerText, /autopilot · feature-full/, `the ${width}px bundle suffix lost its ' · ' separator (${JSON.stringify(row.suffixInnerText)})`);
  };
  const assertHintDesktop = async (dialog, locale) => {
    const text = HINTS[locale];
    const hint = await hintGeometry(dialog, text);
    assert.equal(hint.text, text, `${locale}: the rendered machine-scope hint differs from its full text`);
    assert.ok(!hint.className.split(/\s+/).includes("config-scope-switch-reason"), `${locale}: the machine-scope hint still uses the shared .config-scope-switch-reason class`);
    assert.equal(hint.whiteSpace, "nowrap", `${locale}: the desktop hint is not white-space: nowrap (${hint.whiteSpace})`);
    assert.equal(hint.textOverflow, "ellipsis", `${locale}: the desktop hint is not text-overflow: ellipsis (${hint.textOverflow})`);
    assert.equal(hint.title, text, `${locale}: the desktop hint's title is not the full untruncated text (${JSON.stringify(hint.title)})`);
    assert.equal(hint.lines.length, 1, `${locale}: the desktop hint occupies ${hint.lines.length} lines`);
    assert.ok(hint.height <= hint.lineHeight * 1.6, `${locale}: the desktop hint is not one visual line (height ${hint.height}, line-height ${hint.lineHeight})`);
  };
  const assertHintPhone = async (dialog, width) => {
    const hint = await hintGeometry(dialog, HINTS.en);
    assert.notEqual(hint.textOverflow, "ellipsis", `${width}px: the hint still ellipsises`);
    assert.notEqual(hint.whiteSpace, "nowrap", `${width}px: the hint still uses nowrap`);
    assert.equal(hint.textWrap, "balance", `${width}px: text-wrap: balance does not apply (${hint.textWrap})`);
    assert.ok(hint.scrollWidth <= hint.clientWidth + 1, `${width}px: the hint overflows horizontally (${hint.scrollWidth} > ${hint.clientWidth})`);
    assert.ok(hint.scrollHeight <= hint.clientHeight + 1, `${width}px: the hint is vertically clipped (${hint.scrollHeight} > ${hint.clientHeight})`);
    assert.ok(hint.containerScrollWidth <= hint.containerClientWidth + 1, `${width}px: the hint's container overflows horizontally (${hint.containerScrollWidth} > ${hint.containerClientWidth})`);
    if (hint.lines.length > 1) {
      const last = hint.lines[hint.lines.length - 1];
      assert.ok(last.count >= 2, `${width}px: the hint's last line is an orphan (${last.count} glyph)`);
    }
  };
  // the workflow submit note is one whole line at desktop
  // widths and may wrap only at whole tokens on a phone.
  const assertSubmitNoteDesktop = async (dialog, locale, width) => {
    const text = SUBMIT_NOTES[locale];
    const note = await hintGeometry(dialog, text);
    assert.equal(note.text.trim(), text.trim(), `${locale} ${width}px: the rendered submit note differs from its full text`);
    assert.ok(!note.className.split(/\s+/).includes("config-scope-switch-reason"), `${locale} ${width}px: the submit note still uses the shared .config-scope-switch-reason class`);
    assert.notEqual(note.whiteSpace, "nowrap", `${locale} ${width}px: the submit note is nowrap`);
    assert.notEqual(note.textOverflow, "ellipsis", `${locale} ${width}px: the submit note is ellipsised`);
    assert.equal(note.lines.length, 1, `${locale} ${width}px: the submit note occupies ${note.lines.length} visual lines (lines=${JSON.stringify(note.lines)})`);
    assert.ok(note.height <= note.lineHeight * 1.6, `${locale} ${width}px: the submit note is not one visual line (height ${note.height}, line-height ${note.lineHeight})`);
    assert.ok(note.scrollWidth <= note.clientWidth + 1, `${locale} ${width}px: the submit note clips/overflows (${note.scrollWidth} > ${note.clientWidth})`);
    assert.ok(note.scrollHeight <= note.clientHeight + 1, `${locale} ${width}px: the submit note is vertically clipped (${note.scrollHeight} > ${note.clientHeight})`);
    assert.ok(note.containerScrollWidth <= note.containerClientWidth + 1, `${locale} ${width}px: the submit note's container overflows horizontally (${note.containerScrollWidth} > ${note.containerClientWidth})`);
  };
  const assertSubmitNotePhone = async (dialog, locale, width) => {
    const text = SUBMIT_NOTES[locale];
    const note = await hintGeometry(dialog, text);
    assert.equal(note.text.trim(), text.trim(), `${locale} ${width}px: the rendered submit note differs from its full text`);
    assert.ok(!note.className.split(/\s+/).includes("config-scope-switch-reason"), `${locale} ${width}px: the submit note still uses the shared .config-scope-switch-reason class`);
    assert.ok(note.scrollWidth <= note.clientWidth + 1, `${locale} ${width}px: the submit note overflows horizontally (${note.scrollWidth} > ${note.clientWidth})`);
    assert.ok(note.scrollHeight <= note.clientHeight + 1, `${locale} ${width}px: the submit note is vertically clipped (${note.scrollHeight} > ${note.clientHeight})`);
    assert.ok(note.containerScrollWidth <= note.containerClientWidth + 1, `${locale} ${width}px: the submit note's container overflows horizontally (${note.containerScrollWidth} > ${note.containerClientWidth})`);
    assert.deepEqual(note.illegalBreaks, [], `${locale} ${width}px: the submit note breaks inside a token: ${note.illegalBreaks.join(", ")}`);
    if (note.lines.length > 1) {
      const last = note.lines[note.lines.length - 1];
      assert.ok(last.count >= 2, `${locale} ${width}px: the submit note's last line is an orphan (${last.count} glyph)`);
    }
  };
  // owner-intended change: the falsifiable no-mid-word
  // oracle. Every text node of the page is scanned per character with
  // Range.getClientRects(); a line change between two non-space characters is
  // legal only directly after - _ / . : , ; | or beside a CJK character. A
  // break between two [A-Za-z0-9] (or after <, $( or ") is a violation unless
  // the whole segment between separators is itself wider than its box. At the
  // same time every card/zone must stay within its client width.
  const assertNoMidWordBreaks = async (dialog, width, locale) => {
    const result = await dialog.locator(".config-detail").first().evaluate((root) => {
      const SEPARATORS = new Set(["-", "_", "/", ".", ":", ",", ";", "|"]);
      const CJK = /[\u2E80-\u2FFF\u3000-\u303F\u3040-\u30FF\u3400-\u4DBF\u4E00-\u9FFF\uAC00-\uD7AF\uF900-\uFAFF\uFF00-\uFFEF]/;
      const isSpace = (ch) => /\s/.test(ch);
      const sameLine = (a, b) => Math.abs(a.top - b.top) <= 2;
      const violations = [];
      const boxWidthOf = (node) => {
        let current = node;
        while (current && current !== root) {
          const style = getComputedStyle(current);
          if (style.display !== "inline" && style.display !== "contents" && current.clientWidth > 0) return current.clientWidth;
          current = current.parentElement;
        }
        return root.clientWidth;
      };
      const check = (node) => {
        const text = node.textContent ?? "";
        if (text.trim() === "") return;
        const parent = node.parentElement;
        if (!parent) return;
        const style = getComputedStyle(parent);
        if (style.display === "none" || style.visibility === "hidden" || style.opacity === "0") return;
        const range = document.createRange();
        const chars = [];
        for (let index = 0; index < text.length; index += 1) {
          range.setStart(node, index);
          range.setEnd(node, index + 1);
          const rect = range.getClientRects()[0];
          chars.push(rect ? { ch: text[index], rect, width: rect.width } : null);
        }
        const boxWidth = boxWidthOf(parent);
        for (let index = 1; index < text.length; index += 1) {
          const before = chars[index - 1];
          const after = chars[index];
          if (!before || !after || isSpace(before.ch) || isSpace(after.ch)) continue;
          if (sameLine(before.rect, after.rect)) continue;
          if (SEPARATORS.has(before.ch) || CJK.test(before.ch) || CJK.test(after.ch)) continue;
          const inSegment = (ch) => !isSpace(ch) && !SEPARATORS.has(ch) && !CJK.test(ch);
          let start = index - 1;
          let end = index;
          while (start - 1 >= 0 && chars[start - 1] && inSegment(text[start - 1])) start -= 1;
          while (end < text.length && chars[end] && inSegment(text[end])) end += 1;
          let segmentWidth = 0;
          let complete = true;
          for (let at = start; at < end; at += 1) {
            if (!chars[at]) { complete = false; break; }
            segmentWidth += chars[at].width;
          }
          // Sole exemption: one segment between separators wider than its box.
          if (complete && segmentWidth > boxWidth - 1) continue;
          violations.push({
            before: before.ch,
            after: after.ch,
            segment: text.slice(start, end),
            parent: parent.className,
            chip: parent.closest(".polyforge-config-chip")?.className ?? null,
            boxWidth: Math.round(boxWidth),
            segmentWidth: Math.round(segmentWidth),
          });
          if (violations.length >= 12) return;
        }
      };
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      while (walker.nextNode()) check(walker.currentNode);
      const overflowing = [...root.querySelectorAll(".polyforge-config-card, .polyforge-config-zone")]
        .filter((node) => node.scrollWidth > node.clientWidth + 1)
        .map((node) => `${node.getAttribute("aria-label") ?? node.className}: ${node.scrollWidth} > ${node.clientWidth}`);
      return { violations, overflowing };
    });
    assert.deepEqual(result.violations, [], `${locale} ${width}px mid-word breaks: ${JSON.stringify(result.violations)}`);
    assert.deepEqual(result.overflowing, [], `${locale} ${width}px horizontal overflow: ${result.overflowing.join("; ")}`);
  };
  const assertDensity = async (dialog) => {
    const zones = await dialog.locator(".polyforge-config-zone").evaluateAll((nodes) => nodes.map((node) => {
      const style = getComputedStyle(node);
      return {
        id: node.className,
        background: style.backgroundColor,
        borderLeft: style.borderLeftWidth,
        borderTop: style.borderTopWidth,
      };
    }));
    assert.ok(zones.length >= 2, "the fixture must render both cli.toml zones");
    for (const zone of zones) {
      // P1: de-boxed — no tinted box in a grey card, one divider.
      assert.match(zone.background, /rgba\(0, 0, 0, 0\)|transparent/, `${zone.id} still paints a zone background (${zone.background})`);
      assert.equal(zone.borderLeft, "0px", `${zone.id} still has a boxed left border (${zone.borderLeft})`);
      assert.equal(zone.borderTop, "1px", `${zone.id} has no thin divider (${zone.borderTop})`);
    }
    // the history is a Host-level read-out, in the Host pane.
    await selectPane(dialog.page(), "host");
    const historyToggle = dialog.locator('section[aria-label="Recent panel writes"] button.polyforge-disclosure-toggle').first();
    if ((await historyToggle.getAttribute("aria-expanded")) !== "true") await historyToggle.click();
    const history = await dialog.locator(".polyforge-config-history-row").evaluateAll((nodes) => nodes.map((node) => {
      const rect = (selector) => {
        const element = node.querySelector(selector);
        if (!element) return null;
        const box = element.getBoundingClientRect();
        return { left: box.left, right: box.right, top: box.top };
      };
      return { key: rect(".polyforge-config-history-key"), change: rect(".polyforge-config-history-change"), rollback: rect("button") };
    }));
    assert.ok(history.length >= 3, "the fixture must render its history entries");
    // P2: one aligned key | old -> new | rollback row.
    for (const row of history) {
      assert.ok(row.key && row.change, "a history row lost its key or change cell");
      assert.ok(Math.abs(row.key.top - row.change.top) <= 2, "a history row's key and change are not on one line");
      assert.ok(row.change.left >= row.key.right - 1, "a history row's change is not right of its key");
      if (row.rollback) assert.ok(row.rollback.left >= row.change.left, "a history row's rollback is not in its own column");
    }
    await selectPane(dialog.page(), "cell:default");
  };
  const assertPhoneStackedControls = async (page) => {
    // the Jobs card preferences are Host-level (the Host pane),
    // the workflow chain editor is the default Cell's (its pane).
    await selectPane(page, "host");
    const measured = await page.evaluate(() => {
      const card = document.querySelector('section[aria-label="Jobs card stream"]');
      if (!card) return null;
      const label = card.querySelector(".config-detail-grid-label");
      const value = label?.nextElementSibling;
      if (!label || !value) return null;
      const rect = (element) => {
        const box = element.getBoundingClientRect();
        return { left: box.left, right: box.right, top: box.top, bottom: box.bottom, width: box.width };
      };
      const grid = getComputedStyle(card.querySelector(".config-detail-grid"));
      const cardBox = rect(card);
      return { label: rect(label), value: rect(value), columns: grid.gridTemplateColumns, card: cardBox };
    });
    assert.ok(measured, "the Jobs card stream has no label/value row");
    // P4: label above, the control across the full row below.
    assert.ok(measured.value.top >= measured.label.bottom - 1, `the Jobs preference label is not above its control (${measured.label.bottom} vs ${measured.value.top})`);
    assert.match(measured.columns, /^[\d.]+px$/, `the Jobs preference grid is not one column (${measured.columns})`);
    await selectPane(page, "cell:default");
    // the step-attr row is gone; the chain-add input keeps
    // the full row below its chips.
    const add = await page.evaluate(() => {
      const input = [...document.querySelectorAll(".polyforge-workflow-editor input")].find((node) => (node.getAttribute("aria-label") ?? "").startsWith("Add candidate · "));
      if (!input || input.getBoundingClientRect().width === 0) return null;
      const rect = (element) => { const box = element.getBoundingClientRect(); return { left: box.left, right: box.right, top: box.top, bottom: box.bottom, width: box.width }; };
      return { input: rect(input), parent: rect(input.parentElement) };
    });
    assert.ok(add, "the workflow chain editor did not render its add input");
    assert.ok(Math.abs(add.input.width - add.parent.width) <= 1, `the workflow chain-add control is not the row's full width (${add.input.width} vs ${add.parent.width})`);
    assert.equal(await page.locator('input[aria-label="Questions per round (0 = all at once)"]').count(), 0, "a step-attr input is still rendered");
  };
  const centerY = (box) => box.y + box.height / 2;
  const saveGap = async (card, label) => {
    const input = card.locator(`input[aria-label="${label}"]`).first();
    const box = await input.boundingBox();
    assert.ok(box, `no input for ${label}`);
    const saves = card.getByRole("button", { name: "Save", exact: true });
    const count = await saves.count();
    assert.ok(count > 0, `no Save button in the ${label} row's card`);
    let nearest = null;
    for (let i = 0; i < count; i++) {
      const button = saves.nth(i);
      const b = await button.boundingBox();
      if (!b) continue;
      const distance = Math.abs(centerY(b) - centerY(box));
      if (!nearest || distance < nearest.distance) nearest = { distance, box: b };
    }
    assert.ok(nearest && nearest.distance <= 24, `${label}'s Save button is not on the input's row (nearest row distance ${nearest?.distance?.toFixed?.(1) ?? "?"}px)`);
    return { input: box, button: nearest.box, gap: nearest.box.x - (box.x + box.width) };
  };

  const desktop = await open();

  await check("the value column starts at the same x in every configuration card", async () => {
    const { dialog } = desktop;
    const all = cards(dialog);
    for (const [name, card] of Object.entries(all)) assert.equal(await card.count(), 1, `${name} card/zone is missing`);
    assert.equal(await cli(dialog).count(), 1, "exactly one cli.toml card holds its zones");
    // the summarize/mapping cards are gone, merged into Workspace;
    // the separate [daemon] card is gone, a zone of cli.toml.
    for (const gone of ["Workspace mapping", "Summarize (.polyforge.yaml)", "cli.toml · [daemon]"]) {
      assert.equal(await dialog.locator(`section[aria-label="${gone}"]`).count(), 0, `the retired ${gone} card still renders`);
    }
    const xs = [];
    for (const [name, card] of [["cli.toml", all.flat], ["[daemon]", all.daemon], ["[summarize]", all.summarize]]) {
      const input = card.locator("input").first();
      const box = await input.boundingBox();
      assert.ok(box, `${name} has no editable value in its value column`);
      xs.push({ name, x: box.x });
    }
    const min = Math.min(...xs.map((entry) => entry.x));
    const max = Math.max(...xs.map((entry) => entry.x));
    assert.ok(max - min <= 2, `value columns are not aligned: ${xs.map((entry) => `${entry.name} x=${entry.x.toFixed(1)}`).join(", ")}`);
  });

  await check("the cli.toml top-level zone renders no daemon column, while the [daemon] zone keeps it", async () => {
    const { dialog } = desktop;
    const all = cards(dialog);
    assert.equal(await all.flat.getByText("Daemon value", { exact: true }).count(), 0, "cli.toml still shows a Daemon value column");
    assert.equal(await all.flat.getByText("— (not set)", { exact: true }).count(), 0, "cli.toml still renders daemon-column placeholders");
    assert.ok(await all.daemon.getByText("Daemon value", { exact: true }).count() >= 1, "[daemon] lost its daemon column");
  });

  await check("every writable row's Save button sits in or beside its input cell", async () => {
    const { dialog } = desktop;
    const all = cards(dialog);
    const probes = [
      [all.flat, "api_key"],
      [all.daemon, "step_timeout"], // max_concurrent_jobs is Host-only (the Host region)
      [all.summarize, "summarize.models"],
      [all.summarize, "summarize.interval_sec"],
    ];
    const gaps = [];
    for (const [card, label] of probes) {
      const measured = await saveGap(card, label);
      gaps.push(`${label} gap=${measured.gap.toFixed(1)}px`);
      assert.ok(measured.gap <= 24, `${label}'s Save button is ${measured.gap.toFixed(1)}px past its input (limit 24px)`);
    }
    console.log(`  save gaps: ${gaps.join(", ")}`);
  });

  await check("pf's reload classes never render as bare English text (en)", async () => {
    const { dialog } = desktop;
    assert.equal(await dialog.getByText(/^(restart-only|cli-only|live|next-claim)$/).count(), 0,
      "a bare reload class is still a text node");
  });

  await check("summarize.models renders as individual chips, not one comma string", async () => {
    const card = cards(desktop.dialog).summarize;
    for (const model of MODELS) {
      assert.ok(await card.getByText(model, { exact: true }).count() >= 1, `${model} is not its own chip`);
    }
    const rows = await card.locator("body *").evaluateAll((nodes) => nodes
      .filter((node) => node.children.length === 0)
      .map((node) => (node.textContent ?? "").trim())
      .filter((text) => (text.match(/,/g) ?? []).length >= 2));
    assert.deepEqual(rows, [], `the [summarize] zone still has a comma-joined long value: ${rows.join(" / ")}`);
  });

  // (owner-intentional): the next-dispatch note and the live
  // badge moved to card level, exactly once, and a row is one summary line.
  await check("the workflow card states the next dispatch once with one live badge and no per-row copy", async () => {
    const card = cards(desktop.dialog).workflow;
    await card.locator(".polyforge-workflow-row").first().waitFor({ state: "visible", timeout: 30_000 });
    const ids = await card.locator(".polyforge-workflow-id").allInnerTexts();
    assert.deepEqual(ids.map((id) => id.trim()), ["default", "code", "grill", "wrap", "task"], `workflow rows: ${ids}`);
    const note = "Takes effect at the next step dispatch; a running step does not change.";
    assert.equal(await card.getByText(note, { exact: true }).count(), 1, "the card must state the next-dispatch note exactly once");
    for (const row of await card.locator(".polyforge-workflow-row").all()) {
      assert.equal(await row.getByText(note, { exact: true }).count(), 0, "a row repeats the card-level next-dispatch note");
    }
    const badges = await card.locator(".polyforge-config-badge").allInnerTexts();
    assert.deepEqual(badges.map((text) => text.trim()), ["Live"], `the card must carry exactly one live badge: ${badges}`);
    assert.equal(await card.locator(".polyforge-workflow-row .polyforge-config-badge").count(), 0, "a row still carries the live badge");
    const refresh = await card.getByRole("button", { name: "Refresh", exact: true }).boundingBox();
    assert.ok(refresh && Math.abs(refresh.width - 26) <= 1 && Math.abs(refresh.height - 26) <= 1, `the refresh button is not 26px: ${JSON.stringify(refresh)}`);
  });

  await check("every row is one summary line with its id, chain and a right-aligned bundle suffix", async () => {
    const card = cards(desktop.dialog).workflow;
    await card.locator(".polyforge-workflow-summary").first().waitFor({ state: "visible", timeout: 30_000 });
    assert.equal(await card.getByText("Bundles", { exact: true }).count(), 0, "the retired Bundles label is rendered");
    const rows = await card.locator(".polyforge-workflow-row").evaluateAll((nodes) => nodes.map((row) => {
      const rect = (node) => { const r = node.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height }; };
      const summary = row.querySelector(".polyforge-workflow-summary");
      const suffix = summary?.querySelector(".polyforge-workflow-bundle-suffix");
      const chain = summary?.querySelector(".polyforge-workflow-effective");
      return {
        id: (row.querySelector(".polyforge-workflow-id")?.textContent ?? "").trim(),
        summaries: row.querySelectorAll(".polyforge-workflow-summary").length,
        idInSummary: Boolean(summary?.querySelector(".polyforge-workflow-id")),
        summary: summary ? rect(summary) : null,
        suffix: suffix ? rect(suffix) : null,
        chain: chain ? rect(chain) : null,
        divergent: summary?.classList.contains("is-divergent") ?? false,
        bundleNames: [...(suffix?.querySelectorAll(".polyforge-workflow-bundle-name") ?? [])].map((node) => ({ text: (node.textContent ?? "").trim(), title: node.getAttribute("title") ?? "" })),
        suffixText: (suffix?.textContent ?? "").trim(),
        // textContent keeps the literal ' · ' even when the
        // rendered layout drops the spaces, so the pin reads innerText too.
        suffixInnerText: suffix?.innerText ?? "",
      };
    }));
    assert.deepEqual(rows.map((row) => row.id), ["default", "code", "grill", "wrap", "task"]);
    for (const row of rows) {
      assert.equal(row.summaries, 1, `${row.id} must have exactly one summary container`);
      assert.ok(row.summary && row.idInSummary, `${row.id} does not hold its id in the summary container`);
      assert.ok(row.chain, `${row.id} does not show its effective chain in the summary`);
    }
    const byId = Object.fromEntries(rows.map((row) => [row.id, row]));
    // The default row names every bundle; the bundle-less task row has none.
    assert.deepEqual(byId.default.bundleNames.map((bundle) => bundle.text), ["autopilot", "feature-full"]);
    assert.match(byId.default.suffixText, /autopilot · feature-full/, `the default suffix is not a ' · '-joined list: ${byId.default.suffixText}`);
    assert.match(byId.default.suffixInnerText, /autopilot · feature-full/, `the rendered default suffix is not a ' · '-joined list: ${JSON.stringify(byId.default.suffixInnerText)}`);
    assert.match(byId.default.bundleNames.find((bundle) => bundle.text === "feature-full").title, /pi:ff-default/, "a bundle name's tooltip does not carry its own candidates");
    assert.equal(byId.task.suffix, null, "the task row must not carry a bundle suffix");
    assert.deepEqual(byId.code.bundleNames.map((bundle) => bundle.text), ["autopilot", "feature-full"], "a shared step must name every bundle");
    assert.deepEqual(byId.grill.bundleNames.map((bundle) => bundle.text), ["autopilot", "feature-full"], "a shared step must name every bundle");
    for (const row of rows) {
      if (!row.suffix) continue;
      assert.ok(row.suffix.right >= row.summary.right - 1, `${row.id}'s suffix is not flush right (${row.suffix.right} vs ${row.summary.right})`);
      assert.ok(row.suffix.left >= row.chain.left, `${row.id}'s suffix is not right of the chain`);
      // the long non-divergent row wraps, so its suffix ends the
      // last line instead of staying beside the chain; S270-STEP-1/2 pins that.
      if (!row.divergent && row.id !== "wrap") assert.ok(row.suffix.left >= row.chain.right - 1, `${row.id}'s suffix is not on the chain's line`);
    }
    // At 1100px every short non-divergent row is one visual line; divergent
    // rows and the long-chain wrap row may wrap.
    for (const row of rows) {
      if (["grill", "task"].includes(row.id)) {
        assert.ok(row.summary.height <= 24, `${row.id}'s short non-divergent summary wraps at 1100px (height ${row.summary.height})`);
      }
    }
    assert.equal(byId.code.divergent, true, "the fixture's code step must exercise the divergent chain");
    assert.equal(byId.grill.divergent, false, "the fixture's grill step must exercise a non-divergent chain");
    assert.equal(byId.wrap.divergent, false, "the fixture's wrap step must exercise the long non-divergent chain");
  });

  await check("S270-STEP-1", async () => {
    await assertLongStepRowWrapped(desktop.dialog, 1100);
  });

  const zh = await open({ locale: "zh-CN" });
  await zh.page.setViewportSize({ width: 1280, height: 980 });
  await desktop.page.setViewportSize({ width: 1280, height: 980 });
  await check("S270-HINT-1", async () => {
    await assertHintDesktop(desktop.dialog, "en");
    await assertHintDesktop(zh.dialog, "zh-CN");
  });
  await check("pf's reload classes never render as bare English text (zh-CN)", async () => {
    assert.equal(await zh.dialog.getByText(/^(restart-only|cli-only|live|next-claim)$/).count(), 0,
      "a bare reload class is still a text node under zh-CN");
  });
  await zh.context.close();

  await check("phones stack the cards with no horizontal overflow and no column headers", async () => {
    const { page, dialog } = desktop;
    await page.setViewportSize({ width: 390, height: 844 });
    const all = cards(dialog);
    for (const [name, card] of [["workspace", all.workspace], ["workflow", all.workflow]]) {
      const overflow = await card.evaluate((node) => ({ scrollWidth: node.scrollWidth, clientWidth: node.clientWidth }));
      assert.ok(overflow.scrollWidth <= overflow.clientWidth + 1, `${name} card overflows horizontally: ${overflow.scrollWidth} > ${overflow.clientWidth}`);
    }
    const body = await dialog.evaluate((node) => ({ scrollWidth: node.scrollWidth, clientWidth: node.clientWidth }));
    assert.ok(body.scrollWidth <= body.clientWidth + 1, `settings dialog overflows horizontally: ${body.scrollWidth} > ${body.clientWidth}`);
    const header = all.daemon.getByText("Daemon value", { exact: true }).first();
    assert.equal(await header.isVisible().catch(() => false), false, "column header is still visible on a phone");
  });

  // at <=640px a row keeps the key and its class badge on one
  // line (badge flush right), and the file and daemon values span the row below.
  await check("phones put the key and its class badge on one row, the values across the row below", async () => {
    const { page } = desktop;
    const geometry = await page.evaluate(() => {
      const row = [...document.querySelectorAll(".polyforge-config-row")]
        .find((node) => (node.querySelector(".polyforge-config-key")?.textContent ?? "").trim() === "step_timeout");
      if (!row) return null;
      const rect = (node) => { const r = node.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width }; };
      const style = getComputedStyle(row);
      const box = row.getBoundingClientRect();
      return {
        key: rect(row.querySelector(".polyforge-config-key")),
        badge: rect(row.querySelector(".polyforge-config-class .polyforge-config-badge")),
        file: rect(row.querySelector(".polyforge-config-file")),
        daemon: rect(row.querySelector(".polyforge-config-daemon")),
        contentLeft: box.left + parseFloat(style.paddingLeft),
        contentRight: box.right - parseFloat(style.paddingRight),
      };
    });
    assert.ok(geometry, "the badged [daemon] row step_timeout did not render");
    const { key, badge, file, daemon, contentLeft, contentRight } = geometry;
    const rowBottom = Math.max(key.bottom, badge.bottom);
    assert.ok(Math.abs(badge.top - key.top) <= 2, `the badge is not on the key's row (key top ${key.top}, badge top ${badge.top})`);
    assert.ok(Math.abs(badge.right - contentRight) <= 1, `the badge is not flush right (badge ${badge.right}, row ${contentRight})`);
    assert.ok(key.left < badge.left, "the key is not left of the badge");
    for (const [name, value] of [["file", file], ["daemon", daemon]]) {
      assert.ok(value.top >= rowBottom - 1, `the ${name} value is not below the key/badge row (${value.top} < ${rowBottom})`);
      assert.ok(Math.abs(value.width - (contentRight - contentLeft)) <= 1, `the ${name} value does not span the row (${value.width} vs ${contentRight - contentLeft})`);
    }
  });

  await check("S270-STEP-2", async () => {
    await assertLongStepRowWrapped(desktop.dialog, 390);
  });

  await check("S270-HINT-2", async () => {
    await desktop.page.setViewportSize({ width: 390, height: 844 });
    await assertHintPhone(desktop.dialog, 390);
    await desktop.page.setViewportSize({ width: 640, height: 900 });
    await assertHintPhone(desktop.dialog, 640);
  });

  // density pins (owner-intended change).
  await check("cli.toml zones are de-boxed and history rows are aligned", async () => {
    await desktop.page.setViewportSize({ width: 1280, height: 980 });
    await assertDensity(desktop.dialog);
  });

  await check("the workflow step blocks separate, title line weighs more, editor is subordinate", async () => {
    const row = cards(desktop.dialog).workflow.locator(".polyforge-workflow-row").first();
    const style = await row.evaluate((node) => {
      const rowStyle = getComputedStyle(node);
      const id = getComputedStyle(node.querySelector(".polyforge-workflow-id"));
      const editor = node.querySelector(".polyforge-workflow-editor");
      return {
        border: rowStyle.borderTopWidth,
        radius: parseFloat(rowStyle.borderTopLeftRadius) || 0,
        idWeight: Number(id.fontWeight) || 0,
        editorBackground: editor ? getComputedStyle(editor).backgroundColor : null,
      };
    });
    assert.equal(style.border, "1px", "a workflow step block has no outlined border");
    assert.ok(style.radius >= 4, `a workflow step block has no rounded block shape (${style.radius})`);
    assert.ok(style.idWeight >= 700, `the workflow step title line is not weightier (${style.idWeight})`);
    assert.notEqual(style.editorBackground, "rgba(0, 0, 0, 0)", "the workflow editor area is not visually subordinate");
  });

  await check(" P4: at 390px the Jobs preferences put their label above and the workflow chain-add input spans the row", async () => {
    await desktop.page.setViewportSize({ width: 390, height: 844 });
    await assertPhoneStackedControls(desktop.page);
  });

  for (const locale of ["en", "zh-CN"]) {
    const scanned = await open({ width: 1280, locale });
    for (const width of [1280, 768, 375]) {
      await check(`no mid-word break and no overflow (${locale}, ${width}px)`, async () => {
        await scanned.page.setViewportSize({ width, height: width >= 768 ? 980 : 844 });
        await assertNoMidWordBreaks(scanned.dialog, width, locale);
      });
    }
    await scanned.context.close();
  }

  // one line at >=768px, whole-token wrapping at 390px, in every
  // shipped locale. This fails on the pre-fix tree at 1280/768 (2 lines, the
  // shared class's 320px cap) and passes once the note has its own class.
  for (const locale of ["en", "zh-CN", "zh-TW"]) {
    const noted = await open({ width: 1280, locale });
    for (const width of [1280, 768, 390]) {
      await check(`the workflow submit note keeps its geometry (${locale}, ${width}px)`, async () => {
        await noted.page.setViewportSize({ width, height: width >= 768 ? 980 : 844 });
        if (width >= 768) await assertSubmitNoteDesktop(noted.dialog, locale, width);
        else await assertSubmitNotePhone(noted.dialog, locale, width);
      });
    }
    await noted.context.close();
  }

  await desktop.context.close();
} catch (error) {
  console.error(`ABORT ${error?.stack ?? error}`);
  process.exitCode = 1;
} finally {
  await h.close();
}

console.log(`\n${check.count - failures.length}/${check.count} layout checks passed`);
if (failures.length) {
  console.error(`\n${failures.length} layout check(s) failed:`);
  for (const failure of failures) console.error(`- ${failure}`);
  process.exitCode = 1;
}
