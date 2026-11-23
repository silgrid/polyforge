// carries: components/HarnessModelSettings*, lib/harness/model-view*, lib/polyforge-version*
// carries: components/HarnessGeneralSettings*, components/GeneralSettings*, components/HarnessEnvironmentSettings*, components/HarnessEffectiveSettings*, components/UnifiedSetting*, components/HarnessApplyTo*
// carries: components/HarnessPermissionSettings*, components/ClaudePermissionSettings*, lib/harness-permissions-view*, lib/harness-settings-view*, lib/harness/diagnostics*, app/api/harness/*
// carries: components/AccountSettings*, components/PiAccountLogin*, components/ModelsConfig*, app/api/settings/account/*, lib/provider-listing*, lib/harness/account*, lib/polyforge-doctor*
// carries: components/ClaudeCustomModels*
// carries: lib/step-tiers*, lib/step-tier-defined-models*, lib/models-load*, lib/claude-models*
// (polyforge#151: the tier column's projection chain, and the write gate's defined-model set behind it)
// #85 设置页、账号页、统一模型页和权限页：九个导航分区，旧模型页已移除。
// 模型页场景保留 provider 编辑、账号跳转与三列清单的行为校验。
//
// pi's 不做审批 / 无沙箱 notes, claude's own sections, the Pi Web approval
// channel and the rules editor) and the stored old navigation values
// (models → 新「模型」页; the deleted claude key → 常规, review r1#7). The harness adapter APIs are mocked
// at the browser (context.route): this suite pins the UI contract and never
// runs a real native diagnostic command or write; the adapter, the write
// route and the migration are pinned by lib/harness/*.test.mjs,
// app/api/harness/route.test.mjs, lib/claude-migration.test.mjs and
// e2e/claude-permission-442.mjs (the real routes).
//
// Not declared, though exercised: components/SettingsPanel.tsx and
// lib/settings-navigation.ts (already declared by 3 suites) —
// lib/e2e-carries.test.mjs.
//
// Each scenario prints one `PASS S85-nn` marker; the suite ends with
// `PASS harness-settings-85` only when every scenario passed. Every wait
// polls its condition to a bounded deadline; no fixed sleep.
import assert from "node:assert/strict";
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { chromium } from "playwright";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createHarness, createWaitUntil, seedStorage } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-harness-settings-85-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
mkdirSync(agentDir, { recursive: true });

// A stub `claude` for the one-shot login terminal (S85-12): `auth login`
// prints its ready line and waits — a real claude prints the authorization
// URL and waits for the pasted code. Anything else delegates to the
// recorded unit stub (the initialize probe). Only S85-12's terminal runs
// it; every other claude face is mocked at the browser.
const stubBin = join(work, "stub-bin");
mkdirSync(stubBin, { recursive: true });
const stubClaude = join(stubBin, "claude");
writeFileSync(stubClaude, `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "login" ]; then
  printf '%s\r\n' "85 STUB LOGIN READY - open the URL and paste the code"
  sleep 60
  exit 0
fi
exec node ${join(dirname(new URL(import.meta.url).pathname), "..", "lib", "__fixtures__", "claude-stub", "claude.mjs")} "$@"
`);
chmodSync(stubClaude, 0o755);

const h = createHarness({
  timeout: 30_000,
  coldFirst: true,
  errors: ["pageerror", "console", "5xx"],
  server(name) {
    return { name, logFile: join(work, `server-${name}.log`), env: { HOME: home, PI_CODING_AGENT_DIR: agentDir, PATH: `${stubBin}:${process.env.PATH}` } };
  },
});

// ---- polyforge#151 (85b2, review r2): the tier scenarios' own server ----
// The model list's tier column writes and reads back through the REAL
// /api/polyforge/config route against a stub `pf` that keeps the Host's
// [step_tiers] in a fixture state file (the w385 pattern): the read-back is
// the storage the writes really changed, the CAS compares the state's real
// hash, and a one-shot refusal marker injects the conflict. The server's env
// is its own: a fixture models.json (the write gate's defined set is then
// exactly tierdemo/opus — the route test's isolation), the recorded claude
// stub (its aliases are the gate's claude set) and a PATH without the
// machine's real pf or claude.
const tierHome = join(work, "tier-home");
const tierAgent = join(tierHome, ".pi", "agent");
mkdirSync(tierAgent, { recursive: true });
writeFileSync(join(tierAgent, "models.json"), JSON.stringify({
  providers: { anthropic: { baseUrl: "https://example.invalid/v1", apiKey: "sk-test", api: "openai-completions", models: [{ id: "opus", name: "Opus" }] } },
}, null, 2));
const tierBin = join(work, "tier-bin");
mkdirSync(tierBin, { recursive: true });
copyFileSync(new URL("../lib/__fixtures__/claude-stub/claude.mjs", import.meta.url), join(tierBin, "claude"));
chmodSync(join(tierBin, "claude"), 0o755);
// the recorded fixtures sit BESIDE the stub's directory (the stub resolves
// `../claude-2.1.322` — the route test's layout)
symlinkSync(decodeURIComponent(new URL("../lib/__fixtures__/claude-2.1.322", import.meta.url).pathname), join(work, "claude-2.1.322"));
const tierStateFile = join(tierBin, "tier-state.json");
const tierArgvFile = join(tierBin, "tier-argv.log");
const tierRefuseFile = join(tierBin, "refuse-next");
writeFileSync(join(tierBin, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path"); const crypto = require("crypto");
const dir = path.dirname(fs.realpathSync(__filename));
const stateFile = ${JSON.stringify(tierStateFile)};
const argvFile = ${JSON.stringify(tierArgvFile)};
const refuseFile = ${JSON.stringify(tierRefuseFile)};
const args = process.argv.slice(2);
fs.appendFileSync(argvFile, JSON.stringify(args) + "\\n");
const out = (doc) => { process.stdout.write(JSON.stringify(doc) + "\\n"); process.exit(0); };
const refuse = (code, detail) => { process.stderr.write(JSON.stringify({ error: { code, detail } }) + "\\n"); process.exit(1); };
if (args.includes("--version")) out(args.includes("--json") ? { version: "v0.2.4", capabilities: ["cells-v1", "config-host-cell-v1", "cell-lifecycle-v1"] } : "v0.2.4");
const load = () => JSON.parse(fs.readFileSync(stateFile, "utf8"));
const save = (doc) => { fs.writeFileSync(stateFile + "." + process.pid, JSON.stringify(doc)); fs.renameSync(stateFile + "." + process.pid, stateFile); };
const hash = () => crypto.createHash("sha256").update(fs.readFileSync(stateFile)).digest("hex");
const rest = args.filter((a) => a !== "--json" && a !== "--host");
if (rest[0] === "ops" && rest[1] === "status") out({ queue: { pending: 0, running: 0, max_concurrent: 6, max_configured: 6 }, reload: { pending: [], overridden: [] } });
if (rest[0] === "config" && rest[1] === "list") {
  const doc = load();
  out({ file: { path: path.join(dir, "cli.toml"), sha256: hash(), exists: true },
    items: Object.entries(doc.tiers).map(([tier, list]) => ({ key: "step_tiers." + tier, value: list, source: "file", class: "live", schema: { kind: "model-list" } })) });
}
if (rest[0] === "config" && (rest[1] === "set" || rest[1] === "unset")) {
  if (fs.existsSync(refuseFile)) { fs.unlinkSync(refuseFile); refuse("config-cas-409", "the file changed since it was read"); }
  const key = rest[2];
  const expAt = args.indexOf("--expect-state");
  if (expAt >= 0) {
    const exp = JSON.parse(args[expAt + 1]);
    if (exp.file_sha256 !== hash()) refuse("config-cas-409", "the file changed since it was read");
  }
  const doc = load();
  const tier = key.replace(/^step_tiers\\./, "");
  if (rest[1] === "set") doc.tiers[tier] = JSON.parse(rest[3]);
  else delete doc.tiers[tier];
  save(doc);
  out({ ok: true, key, class: "live", outcome: "applied" });
}
process.stderr.write("pf: unknown command " + rest.join(" ") + "\\n"); process.exit(2);
`);
chmodSync(join(tierBin, "pf"), 0o755);
const seedTiers = (tiers) => {
  writeFileSync(tierStateFile + ".seed", JSON.stringify({ tiers }));
  renameSync(tierStateFile + ".seed", tierStateFile);
};
const tierState = () => JSON.parse(readFileSync(tierStateFile, "utf8"));
const tierArgv = () => (existsSync(tierArgvFile) ? readFileSync(tierArgvFile, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)) : []);

// ---- the mocked adapter contract (the shapes 85a1's routes answer) ----
const HARNESS_LIST = {
  harnesses: [
    { id: "pi", version: "1.5.0", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "pi" } },
    { id: "claude", version: "2.1.322", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "claude" } },
  ],
};
const ENV_PI = { harness: "pi", concept: "env", data: { target: "pi", setKeys: ["ANTHROPIC_API_KEY", "GEMINI_API_KEY"] } };
const ENV_CLAUDE = { harness: "claude", concept: "env", data: { target: "claude", setKeys: ["ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_EXTRA_BODY"] } };
const EFFECTIVE_PI = {
  harness: "pi", concept: "effective-config",
  data: { fields: {
    model: { value: "route-model-85", sourceScope: "user", sourcePath: join(agentDir, "settings.json"), inferred: true },
    "reasoning-effort": { value: "low", sourceScope: "project", sourcePath: join(work, "project", ".pi", "settings.json"), inferred: true },
  } },
};
const EFFECTIVE_CLAUDE = {
  harness: "claude", concept: "effective-config",
  data: { fields: {
    model: { value: "claude-sonnet-5-5", sourceScope: "project", sourcePath: join(work, "project", ".claude", "settings.json"), inferred: true },
    "pi-web:launch-permission-mode": { value: "acceptEdits", sourceScope: "pi-web", sourcePath: join(agentDir, "pi-web-claude-settings.json"), inferred: true },
  } },
};
const DIAG_CLAUDE = {
  harness: "claude",
  diagnostics: { available: true, command: ["claude", "doctor"], lines: ["claude doctor: installation healthy"], truncated: false, timedOut: false },
};
const DIAG_PI = {
  harness: "pi",
  diagnostics: { available: false, reason: "pi has no fixed safe diagnostic command yet" },
};
const CLAUDE_REPORT = { available: true, models: [{ value: "default", displayName: "Default", source: "alias" }], version: "2.1.322", versionRecorded: true, recordedVersions: ["2.1.322"], capabilities: ["hooks", "ui_surface_v1"], features: [
  { id: "approvals", available: true, basis: "host-prompts" },
  { id: "backgroundTasks", available: true, basis: "recorded" },
  { id: "subagentText", available: true, basis: "recorded" },
] };

// The permission page's reads (85a4): the compare's four fields and the two
// harnesses' permission payloads. The trust field answers as a project would
// (pi trusted, claude not) so the unify flow is pinnable without a project.
const NULL_SOURCES = { defaultMode: null, sandbox: null, rules: null, denyInsteadOfPrompting: null };
const COMPARE = {
  results: [
    { field: "reasoning-effort", status: "consistent", values: { pi: { supported: true, neutral: "low", raw: "low" }, claude: { supported: true, neutral: "low", raw: "low" } } },
    { field: "approval-mode", status: "unsupported", values: { pi: { supported: false, neutral: null, raw: null }, claude: { supported: true, neutral: "ask", raw: "default" } } },
    { field: "sandbox", status: "unsupported", values: { pi: { supported: false, neutral: null, raw: null }, claude: { supported: true, neutral: "on", raw: "on" } } },
    { field: "project-trust", status: "inconsistent", values: { pi: { supported: true, neutral: "trusted", raw: "trusted" }, claude: { supported: true, neutral: "untrusted", raw: "untrusted" } } },
  ],
};
const PERMISSION_PI = {
  harness: "pi", concept: "permission",
  data: { approvalMode: { supported: false, neutral: null, raw: null }, projectTrust: { trusted: true }, sandbox: { supported: false, enabled: null, autoAllowBashIfSandboxed: null, otherKeys: [] }, rules: null, denyInsteadOfPrompting: null, sources: NULL_SOURCES },
};
const PERMISSION_CLAUDE = {
  harness: "claude", concept: "permission",
  data: {
    approvalMode: { supported: true, neutral: "ask", raw: "default" },
    projectTrust: { trusted: false },
    sandbox: { supported: true, enabled: true, autoAllowBashIfSandboxed: false, otherKeys: ["failIfUnavailable"] },
    rules: { allow: ["WebSearch"], ask: [], deny: ["Bash(rm:*)"] },
    denyInsteadOfPrompting: false,
    sources: { defaultMode: { scope: "user", path: join(agentDir, "claude-user-settings.json") }, sandbox: { scope: "user", path: join(agentDir, "claude-user-settings.json") }, rules: null, denyInsteadOfPrompting: null },
  },
};
/** The permission PUTs the browser sent (each scenario clears and reads it). */
const permissionPuts = [];

/** Answers the harness/claude adapter routes the settings skeleton reads; everything else hits the server. */
async function mockAdapterApis(context) {
  // The app's fresh-page flow would otherwise select a default project
  // asynchronously (POST /api/default-cwd → POST /api/cwd/validate): a cwd
  // landing mid-scenario re-fires the panes' reads with ?cwd=, racing every
  // no-project assertion (v1 round 1: S85-09's alert was cleared by exactly
  // that). The suite is a no-project suite; the flow answers no cwd.
  await context.route("**/api/default-cwd", (route) => route.fulfill({ status: 200, json: {} }));
  await context.route(/\/api\/harness(?:\/|\?|$)/, async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() === "PUT") {
      // The permission page's one write path: recorded, answered as written.
      if (path === "/api/harness/pi/permission" || path === "/api/harness/claude/permission") {
        permissionPuts.push({ path, body: route.request().postDataJSON() });
        await route.fulfill({ status: 200, json: { harness: path.split("/")[3], concept: "permission", result: { status: "written", summary: "mocked" } } });
        return;
      }
      await route.fulfill({ status: 400, json: { error: "write-refused", message: "not a permission write path" } });
      return;
    }
    const body = path === "/api/harness" ? HARNESS_LIST
      : path === "/api/harness/compare" ? COMPARE
      : path === "/api/harness/pi/permission" ? PERMISSION_PI
      : path === "/api/harness/claude/permission" ? PERMISSION_CLAUDE
      : path === "/api/harness/pi/env" ? ENV_PI
      : path === "/api/harness/claude/env" ? ENV_CLAUDE
      : path === "/api/harness/pi/effective-config" ? EFFECTIVE_PI
      : path === "/api/harness/claude/effective-config" ? EFFECTIVE_CLAUDE
      : path === "/api/harness/pi/diagnose" ? DIAG_PI
      : path === "/api/harness/claude/diagnose" ? DIAG_CLAUDE
      : null;
    if (body === null) await route.fulfill({ status: 404, json: { error: "unknown-harness-path" } });
    else await route.fulfill({ status: 200, json: body });
  });
  await context.route("**/api/models-config", (route) => route.fulfill({ status: 200, json: { providers: {} } }));
  await context.route("**/api/claude", (route) => route.fulfill({ status: 200, json: CLAUDE_REPORT }));
  // 85a3-3: the models page's Claude section fires its migration POST on
  // every mount; the default answer keeps the other scenarios hermetic (a
  // scenario with its own page.route — S23 — overrides it).
  await context.route("**/api/claude-models-migration", (route) => route.fulfill({ status: 200, json: { status: "done", changed: false, shadowed: false } }));
  await context.route("**/api/claude-permissions", (route) => route.fulfill({ status: 200, json: { mode: "ask", push: true, includeHookEvents: false, session: null } }));
}

const TABS = [
  "General", "Account", "Models", "Permissions", "Skills", "MCP",
  "Sub-agents", "Plugins", "Polyforge",
];

const labelRe = (label) => new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);

const T = {
  S1: "S85-01: Settings shows the unified model page in the settled order",
  S2: "S85-02: General is a left-column layout whose three items switch the detail pane",
  S3: "S85-03: a phone walks General as list → detail → back",
  S4: "S85-04: the account / unified model pages open; the permission page is real",
  S5: "S85-05: the unified Models page shows the model table and Pi editor",
  S6: "S85-06: the environment pane lists only variable names, grouped per harness",
  S7: "S85-07: effective values carry the Inferred mark and their source; native diagnostics are a collapsed block per harness",
  S8: "S85-08: stored old navigation values land on the new pages",
  S9: "S85-09: a failed harness-list read is a visible reason in both panes, never a contradictory empty registry",
  S10: "S85-10: the account tab lists the harnesses, shows the interactive card and the per-Cell rows with every field, and never a credential value",
  S11: "S85-11: pi sign-in lives on Account › pi and the unified Models page has no duplicate login form",
  S12: "S85-12: the account page's one-shot login terminal attaches and streams the declared command's output — the real login path a human would walk (review r1#1)",
  S16: "S85-16: the permission page's left column is 审批模式 / 沙箱 / 项目信任 / 规则, its approval pane unified and claude-sectioned",
  S17: "S85-17: the unified approval write reaches claude's native mode (Q1 A), the extra values its own radio",
  S18: "S85-18: the sandbox pane writes claude's sandbox keys, pi answering 无沙箱",
  S19: "S85-19: the trust pane shows each harness's own decision and 统一成当前值 writes the differing one",
  S20: "S85-20: the rules pane edits claude's allow / ask / deny lists with no unified block",
  S21: "S85-21: General has no claude permission controls; its hook switch stays",
  S22: "S85-22: the table and the editor share one enabledModels controller — a Save never re-enables a model the table just disabled (review r1#1)",
  S23: "S85-23: the models page's Claude section merges the retired custom models under a preview and edits the native picker with full-list writes (85a3-3)",
  S30: "S85-30: the model list's step-tier column is a read-only badge — a shared tier shows its name, no editor renders and opening the page writes nothing (polyforge#256 B7/AC1)",
  S31: "S85-31: differing harnesses show their own read-only 归属 (pi: default / claude: fast-chat), never an apply-to control (polyforge#256 B7)",
  S32: "S85-32: an incapable catalog read leaves the column read-only with a localized 0.2.4-0 reason, the other columns untouched (polyforge#256 B1)",
  S33: "S85-33: the page offers no step-tier write entry at all — no select, no checkbox, no button, no config POST (polyforge#256 B7)",
  S34: "S85-34: an external [step_tiers] write is what the read-only labels show after a reload (polyforge#256 B7/AC1)",
};
const failures = new Map(Object.values(T).map((title) => [title, []]));
const ran = new Set();

async function part(id, label, fn) {
  const title = T[id];
  ran.add(title);
  const startedAt = Date.now();
  try {
    await fn();
    console.log(`PASS ${id} (${((Date.now() - startedAt) / 1000).toFixed(0)}s)`);
  } catch (error) {
    failures.get(title).push(`${label}: ${String(error?.message ?? error).split("\n")[0]}`);
    console.log(`FAIL ${id} ${label}: ${error?.message ?? error}`);
  }
}

/** The tier scenarios' own server, started once: the real config routes
 *  against the stub pf (the fixture state is the Host's [step_tiers]). */
let tierServerStarted = false;
async function startTierServer() {
  if (tierServerStarted) return;
  tierServerStarted = true;
  // the main server's scenarios (S1–S23) are done; stop it first (the
  // polyforge.mjs two-phase pattern) so it cannot outlive the suite
  await h.stop();
  await h.startServer({
    name: "settings-85-tiers",
    logFile: join(work, "server-settings-85-tiers.log"),
    // its own dist dir: the main server keeps running beside it
    distDir: ".next/e2e/harness-settings-85-tiers",
    env: {
      HOME: tierHome,
      PI_CODING_AGENT_DIR: tierAgent,
      PI_OFFLINE: "1",
      // the machine's real pf and claude stay off this PATH (they live in
      // /root/.local/...); node/npm and the system tools stay on it
      PATH: `${tierBin}${delimiter}/usr/local/bin:/usr/bin:/bin`,
    },
  });
}

/** The tier scenarios' page-level mocks: the unified list's rows — one
 *  merged pi+claude row (tierdemo/opus), matching the gate's defined set. */
async function tierPageMocks(page) {
  await page.route(/\/api\/models\?scope=all/, (route) => route.fulfill({ json: {
    models: { "anthropic:opus": "Opus" }, modelList: [{ id: "opus", name: "Opus", provider: "anthropic" }],
    defaultModel: null, defaultThinkingLevel: null, savedDefaultThinkingLevel: null,
    thinkingLevels: {}, thinkingLevelMaps: {}, thinkingLevelPins: {}, isScoped: false } }));
  await page.route("**/api/claude", (route) => route.fulfill({ json: { ...CLAUDE_REPORT, models: [{ value: "opus", source: "alias" }] } }));
}

const waitFor = createWaitUntil({ every: 250 });
const dialog = (page) => page.getByRole("dialog", { name: "Settings", exact: true });
const sectionTabs = (page) => dialog(page).locator("nav.settings-section-tabs button.settings-section-tab");
const sectionHost = (page, id) => dialog(page).locator(`.settings-section-host[data-settings-section="${id}"]:not([hidden])`);
const tab = (page, label) => sectionTabs(page).filter({ hasText: typeof label === "string" ? labelRe(label) : label });

let coldSettings = true;

async function newPage(viewport = { width: 1280, height: 900 }, storage = null) {
  const context = await h.browser.newContext({ viewport, locale: "en-US" });
  await seedStorage(context, storage ?? {});
  await mockAdapterApis(context);
  const page = await context.newPage();
  page.setDefaultTimeout(30_000);
  return page;
}

async function goto(page, path = "/") {
  await page.goto(`${h.base}${path}`, { waitUntil: "domcontentloaded", timeout: coldSettings ? COLD_MS : NAV_MS });
  coldSettings = false;
}

/** The Settings button is server-rendered; a click before hydration is dropped, so wait for React's own props first. */
async function clickSettingsButton(page) {
  const button = page.getByRole("button", { name: "Settings", exact: true }).first();
  await button.waitFor({ state: "visible", timeout: COLD_MS });
  await page.waitForFunction(() => {
    const candidate = [...document.querySelectorAll("button")]
      .find((entry) => entry.getAttribute("aria-label") === "Settings" || entry.textContent.trim() === "Settings");
    return !!candidate && Object.keys(candidate).some((key) => key.startsWith("__reactFiber$") || key.startsWith("__reactProps$"));
  }, null, { timeout: COLD_MS });
  await button.click({ timeout: ACTION_MS });
}

async function openSettings(page) {
  await goto(page);
  await clickSettingsButton(page);
  await dialog(page).waitFor({ state: "visible", timeout: COLD_MS });
  return dialog(page);
}

const generalSidebarItems = (page) => sectionHost(page, "general").locator(".config-sidebar-item");
const generalPane = (page, pane) => sectionHost(page, "general").locator(`[data-general-pane="${pane}"]`);

h.trapInterrupt();

try {
  await h.startServer("settings-85");
  h.browser = await h.launchBrowser(() => chromium.launch());

  // S85-01 — AC1: 9 new tabs + 2 legacy-marked, order and no-project behaviour.
  await part("S1", "tabs", async () => {
    const page = await newPage();
    await openSettings(page);
    const labels = (await sectionTabs(page).allInnerTexts()).map((text) => text.trim());
    assert.deepEqual(labels, TABS, `tabs (got: ${labels.join(", ")})`);
    // Skills opens without a project too (85d AC3: it shows the user scope
    // alone); Agents/Plugins still need one; the rest open without one.
    for (const name of ["Skills", "Sub-agents", "Plugins"]) {
      if (name === "Skills") {
        assert.ok(!(await tab(page, name).isDisabled()), `${name} should open without a project (85d AC3)`);
        continue;
      }
      assert.ok(await tab(page, name).isDisabled(), `${name} should need a project`);
    }
    for (const name of ["General", "Account", "Models", "Permissions", "MCP", "Polyforge"]) {
      assert.ok(!(await tab(page, name).isDisabled()), `${name} should open without a project`);
    }
    assert.equal(labels.length, 9, "the unified model page is duplicated in the rail");
    assert.ok(!labels.some((label) => /legacy/i.test(label)), "a legacy Models tab remains in the rail");
    await page.context().close();
  });

  // S85-02 — AC1: General's left column and its three switchable items.
  await part("S2", "general left column", async () => {
    const page = await newPage();
    const opened = await openSettings(page);
    const items = generalSidebarItems(page);
    await items.first().waitFor({ state: "visible", timeout: ACTION_MS });
    const labels = (await items.allInnerTexts()).map((text) => text.trim());
    assert.deepEqual(labels, ["Interface & conversation", "Environment", "Effective values & sources"], `General's left column (got: ${labels.join(", ")})`);
    assert.equal(await items.first().getAttribute("aria-current"), "page", "the first item is not the default pane");
    // The default detail is the old General content.
    await generalPane(page, "conversation").getByRole("heading", { name: "Appearance", exact: true }).waitFor({ state: "visible", timeout: ACTION_MS });
    // Switching panes swaps the detail.
    await items.filter({ hasText: "Environment" }).click({ timeout: ACTION_MS });
    await generalPane(page, "environment").waitFor({ state: "visible", timeout: ACTION_MS });
    await generalPane(page, "conversation").waitFor({ state: "hidden", timeout: ACTION_MS });
    await items.filter({ hasText: "Effective values & sources" }).click({ timeout: ACTION_MS });
    await generalPane(page, "effective").waitFor({ state: "visible", timeout: ACTION_MS });
    await items.filter({ hasText: "Interface & conversation" }).click({ timeout: ACTION_MS });
    await generalPane(page, "conversation").getByRole("heading", { name: "Appearance", exact: true }).waitFor({ state: "visible", timeout: ACTION_MS });
    assert.equal(await opened.count(), 1);
    await page.context().close();
  });

  // S85-03 — AC1: the phone walks General as list → detail → back (the shared split view).
  await part("S3", "phone levels", async () => {
    const page = await newPage({ width: 320, height: 568 });
    await goto(page);
    // The phone's sidebar starts folded; its toggle and the Settings button
    // both need hydration first (a pre-hydration click is swallowed).
    const sidebar = page.getByRole("button", { name: "Show sidebar", exact: true });
    await page.waitForFunction(() => [...document.querySelectorAll("button")]
      .some((entry) => (entry.getAttribute("aria-label") ?? entry.textContent.trim()) === "Show sidebar"
        && Object.keys(entry).some((key) => key.startsWith("__reactFiber$") || key.startsWith("__reactProps$"))), null, { timeout: COLD_MS });
    if (await sidebar.count()) await sidebar.click({ timeout: ACTION_MS });
    await clickSettingsButton(page);
    const home = page.locator(".settings-phone-home");
    await home.waitFor({ state: "visible", timeout: COLD_MS });
    await home.getByRole("button", { name: "General", exact: true }).click({ timeout: ACTION_MS });
    // The section level shows the three-item list, not the detail.
    const items = generalSidebarItems(page);
    await items.first().waitFor({ state: "visible", timeout: ACTION_MS });
    assert.ok(!(await generalPane(page, "conversation").isVisible()), "the phone's section level shows the detail pane");
    await items.filter({ hasText: "Interface & conversation" }).click({ timeout: ACTION_MS });
    await generalPane(page, "conversation").getByRole("heading", { name: "Appearance", exact: true }).waitFor({ state: "visible", timeout: ACTION_MS });
    const back = page.locator(".settings-phone-back");
    await back.waitFor({ state: "visible", timeout: ACTION_MS });
    await back.click({ timeout: ACTION_MS });
    await items.first().waitFor({ state: "visible", timeout: ACTION_MS });
    await generalPane(page, "conversation").waitFor({ state: "hidden", timeout: ACTION_MS });
    await page.context().close();
  });

  // S85-04 — AC1: the unified Models page is real; the permission page is real.
  await part("S4", "model page", async () => {
    const page = await newPage();
    await openSettings(page);
    await tab(page, /^Models$/).click({ timeout: ACTION_MS });
    const host = sectionHost(page, "models");
    await host.locator('[data-model-page="unified"]').waitFor({ state: "visible", timeout: ACTION_MS });
    const modelText = await host.innerText();
    assert.ok(modelText.includes("Default model"), "the unified model page lacks the default model");
    assert.ok(modelText.includes("Model list"), "the unified model page lacks the model list");
    assert.ok(modelText.includes("Chat availability"), "the unified model page lacks the chat availability column");
    assert.ok(modelText.includes("pi"), "the unified model page lacks the pi editor");
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const permission = sectionHost(page, "permission");
    await permission.locator(".config-sidebar-item").first().waitFor({ state: "visible", timeout: ACTION_MS });
    assert.deepEqual((await permission.locator(".config-sidebar-item").allInnerTexts()).map((text) => text.trim()),
      ["Approval mode", "Sandbox", "Project trust", "Rules"], "the permission page's left column");
    await page.context().close();
  });

  // S85-10 — polyforge#142 (85a2 AC1/AC7): the account tab — the two harness
  // panes, the interactive claude card, the per-Cell rows with the ticket's
  // field list (来源含 shared default, the keychain hint, 步骤能否使用及原因),
  // the credential form, and no credential value anywhere. The account and
  // harness answers are mocked at the browser; the S85-09 pattern (a later
  // route registration wins) carries the richer capability declaration.
  await part("S10", "the account tab", async () => {
    const page = await newPage();
    const ACCOUNT = {
      claude: { available: true, loggedIn: true, authMethod: "oauth_token", configDirectory: "/home/op/.claude", reason: null,
        envCredentials: { ANTHROPIC_AUTH_TOKEN: true, ANTHROPIC_BASE_URL: true, ANTHROPIC_API_KEY: false, CLAUDE_CODE_OAUTH_TOKEN: false } },
      cells: [
        { profile: "default", executionDisabled: null, probeError: null, harnessOrder: ["pi", "claude"], harnesses: {
          pi: { id: "pi", configRoot: "/h/default/step-agent", credential: "set", status: "ready", authType: "api_key", checkReason: null, source: "/home/op/.pi/agent", sourceKind: "operator", credentialsFollow: true, keychainHint: false, credentialKinds: null, credentialsFile: null, usable: true, reason: null },
          claude: { id: "claude", configRoot: "/h/default/claude-config", credential: "set", status: "ready", authType: null, checkReason: null, source: "/home/op/.pi/agent", sourceKind: "operator", credentialsFollow: true, keychainHint: false, credentialKinds: { oauthToken: false, apiKey: false, gateway: true }, credentialsFile: "absent", usable: true, reason: null } } },
        { profile: "beta", executionDisabled: null, probeError: null, harnessOrder: ["pi", "claude"], harnesses: {
          pi: { id: "pi", configRoot: "/h/beta/step-agent", credential: "unset", status: "not_ready", authType: null, checkReason: "credentials_not_configured", source: "/home/op/.pi/agent", sourceKind: "shared-default", credentialsFollow: true, keychainHint: false, credentialKinds: null, credentialsFile: null, usable: false, reason: "credentials_not_configured" },
          claude: { id: "claude", configRoot: "/h/beta/claude-config", credential: "unset", status: "not_ready", authType: null, checkReason: null, source: "/home/op/.pi/agent", sourceKind: "shared-default", credentialsFollow: false, keychainHint: true, credentialKinds: null, credentialsFile: "present", usable: true, reason: null } } },
      ],
      doctorError: null,
    };
    await page.context().route(/\/api\/settings\/account/, (route) => route.fulfill({ json: ACCOUNT }));
    await page.context().route(/\/api\/harness(?:\/|\?|$)/, (route) => route.fulfill({ json: { harnesses: [
      { id: "pi", version: "1.5.0", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "pi", loginCommands: [], credentialsFollowConfigRoot: true } },
      { id: "claude", version: "2.1.322", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "claude", loginCommands: [
        { id: "auth-login", argv: ["claude", "auth", "login"], cell: false },
        { id: "setup-token", argv: ["claude", "setup-token"], cell: false },
      ], credentialsFollowConfigRoot: false } },
    ] } }));
    await openSettings(page);
    await tab(page, /^Account$/).click({ timeout: ACTION_MS });
    const host = sectionHost(page, "account");
    await host.waitFor({ state: "visible", timeout: ACTION_MS });
    // the left column: the two harnesses
    const claudeItem = host.locator(".config-sidebar-item").filter({ hasText: "Claude" });
    await claudeItem.click({ timeout: ACTION_MS });
    // the settle duty (review r1#4): the asserted data items are polled
    // directly — every expected string is part of the polled condition,
    // never a heading read once and a second value asserted on one read
    const CLAUDE_EXPECTED = ["oauth_token", "/home/op/.claude", "Log in with Claude (OAuth)", "Mint a setup token",
      "/h/default/claude-config", "shared default", "/home/op/.pi/agent", "Keychain", "Residual OAuth login",
      "Write a credential", "copied server-side"];
    const claudeText = await waitFor(async () => {
      const text = await host.innerText();
      return CLAUDE_EXPECTED.every((expected) => text.includes(expected)) ? text : false;
    }, ACTION_MS, "the claude pane never showed every card and row field");
    assert.ok(!claudeText.includes("sk-SENTINEL"), "a credential value rendered");
    // the pi pane: its own rows, no claude form
    await host.locator(".config-sidebar-item").filter({ hasText: /^pi$/ }).click({ timeout: ACTION_MS });
    const piText = await waitFor(async () => {
      const text = await host.innerText();
      return text.includes("api_key") && text.includes("/h/beta/step-agent") ? text : false;
    }, ACTION_MS, "the pi pane never showed its rows");
    assert.ok(!piText.includes("Write a credential"), "the pi pane carries the claude form");
    await page.context().close();
  });

  // S85-11 — polyforge#142 (85a2 AC4/AC8): pi's sign-in, API-key entry and
  // Add-provider picker live only on Account › pi; the legacy Models tab
  // shows a moved notice for a managed provider; and the never-offered
  // Anthropic subscription login appears nowhere (the mocked providers answer
  // is the filtered shape the route now returns).
  await part("S11", "pi sign-in only on the account page", async () => {
    const page = await newPage();
    const PROVIDERS = {
      providers: [
        { id: "openai-codex", name: "ChatGPT Plus/Pro", usesCallbackServer: false, loggedIn: false, supportsApiKey: false },
        { id: "google", name: "Google", usesCallbackServer: false, loggedIn: true, supportsApiKey: true },
      ],
      oauthProviders: [
        { id: "openai-codex", name: "ChatGPT Plus/Pro", usesCallbackServer: false, loggedIn: false, supportsApiKey: false },
        { id: "google", name: "Google", usesCallbackServer: false, loggedIn: true, supportsApiKey: true },
      ],
      apiKeyProviders: [
        { id: "google", displayName: "Google", configured: true, modelCount: 8, supportsOAuth: true },
        { id: "anthropic", displayName: "Anthropic", configured: false, modelCount: 12, supportsOAuth: true },
      ],
    };
    await page.context().route(/\/api\/auth\/providers/, (route) => route.fulfill({ json: PROVIDERS }));
    await openSettings(page);
    // The unified Models page owns the model table and Pi editor, while login
    // remains on Account › pi; no duplicate legacy page exists. The managed
    // provider's detail must settle before the no-login assertions read it
    // (review r1#3: reading a single count over a still-loading list pinned
    // nothing).
    await tab(page, /^Models$/).click({ timeout: ACTION_MS });
    const models = sectionHost(page, "models");
    await models.locator('[data-model-page="unified"]').waitFor({ state: "visible", timeout: ACTION_MS });
    await models.locator(".config-sidebar-item").filter({ hasText: "Google" }).first().click({ timeout: ACTION_MS });
    const detailText = await waitFor(async () => {
      const detail = models.locator(".enabled-models-section, .enabled-models-empty, .enabled-models-error").first();
      if (!(await detail.count())) return false;
      const text = await models.innerText();
      if (/Loading/i.test(text)) return false;
      if (text.includes("Login") || text.includes("Re-login")) return false;
      if ((await models.locator('input[type="password"]').count()) !== 0) return false;
      return text;
    }, ACTION_MS, "the managed provider detail still offers a login");
    assert.ok(!detailText.includes("Login") && !detailText.includes("Re-login"), "the Models page exposes a duplicate login form");
    assert.equal(await models.locator('input[type="password"]').count(), 0, "the Models page exposes a duplicate API-key form");
    await models.getByRole("button", { name: "Account", exact: true }).waitFor({ state: "visible", timeout: ACTION_MS });
    // Account › pi: the rows, the Add provider button, and no Anthropic
    // subscription anywhere
    await tab(page, /^Account$/).click({ timeout: ACTION_MS });
    const account = sectionHost(page, "account");
    await account.locator(".config-sidebar-item").filter({ hasText: /^pi$/ }).click({ timeout: ACTION_MS });
    const piText = await waitFor(async () => {
      const text = await account.innerText();
      return text.includes("ChatGPT Plus/Pro") ? text : false;
    }, ACTION_MS, "the account pi pane never listed the offered providers");
    assert.ok(piText.includes("Add provider"), "the account pi pane lost the Add provider entry");
    assert.ok(piText.includes("Anthropic"), "the Anthropic API-key entry is gone from the account pane");
    assert.ok(!piText.includes("Anthropic (Claude Pro/Max)"), "the never-offered Anthropic subscription appears");
    await page.context().close();
  });

  // S85-12 — polyforge#142 (85a2 AC3, review r1#1): the account page's
  // 「打开终端」 runs the declared login command in the one-shot terminal, and
  // the embedded panel ATTACHES to it — the terminal's output reaches the
  // page (the stub claude prints its ready line and waits, exactly where a
  // real claude auth login prints its URL and waits for the code). Closing
  // the terminal tears it down. The red of the round-1 defect under review
  // was exactly here: the panel's start request failed, the stream never
  // opened, and this poll never settled.
  await part("S12", "the one-shot login terminal attaches", async () => {
    const page = await newPage();
    const ACCOUNT = {
      claude: { available: true, loggedIn: false, authMethod: "none", configDirectory: join(home, ".claude"), reason: null,
        envCredentials: { ANTHROPIC_AUTH_TOKEN: false, ANTHROPIC_BASE_URL: false, ANTHROPIC_API_KEY: false, CLAUDE_CODE_OAUTH_TOKEN: false }, consoleProfile: "absent" },
      cells: [],
      doctorError: null,
    };
    await page.context().route(/\/api\/settings\/account/, (route) => route.fulfill({ json: ACCOUNT }));
    await page.context().route(/\/api\/harness(?:\/|\?|$)/, (route) => route.fulfill({ json: { harnesses: [
      { id: "pi", version: "1.5.0", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "pi", loginCommands: [], credentialsFollowConfigRoot: true } },
      { id: "claude", version: "2.1.322", versionKnown: true, knownRange: null, versionReason: null, writable: true, capabilities: { id: "claude", loginCommands: [
        { id: "auth-login", argv: ["claude", "auth", "login"], cell: false },
      ], credentialsFollowConfigRoot: true } },
    ] } }));
    await openSettings(page);
    await tab(page, /^Account$/).click({ timeout: ACTION_MS });
    const account = sectionHost(page, "account");
    await account.locator(".config-sidebar-item").filter({ hasText: "Claude" }).click({ timeout: ACTION_MS });
    const loginButton = account.locator('[data-login-command="auth-login"]');
    await loginButton.waitFor({ state: "visible", timeout: ACTION_MS });
    await loginButton.click({ timeout: ACTION_MS });
    // the panel attaches and the command's output streams: poll the
    // terminal's own text, not a static heading
    const terminal = account.locator("[data-account-login-terminal]");
    await terminal.waitFor({ state: "visible", timeout: ACTION_MS });
    const termText = await waitFor(async () => {
      const text = await terminal.innerText();
      return text.includes("85 STUB LOGIN READY") ? text : false;
    }, ACTION_MS, "the login terminal never showed the command's output");
    assert.ok(!termText.includes("SENTINEL"), "a value leaked into the terminal");
    // closing tears it down: the panel goes and the terminal with it
    await account.getByRole("button", { name: "Close terminal" }).click({ timeout: ACTION_MS });
    await waitFor(async () => !(await terminal.count()), ACTION_MS, "the closed login terminal stayed mounted");
    await page.context().close();
  });

  // S85-05 — AC1: the unified Models page keeps the provider editor and table together.
  await part("S5", "unified model page", async () => {
    const page = await newPage();
    await openSettings(page);
    await tab(page, /^Models$/).click({ timeout: ACTION_MS });
    const models = sectionHost(page, "models");
    await models.locator('[data-model-page="unified"]').waitFor({ state: "visible", timeout: ACTION_MS });
    await models.locator(".config-sidebar-list").first().waitFor({ state: "visible", timeout: COLD_MS });
    assert.ok((await models.innerText()).includes("Model list"), "the unified model table is missing");
    await models.getByRole("button", { name: "Add provider", exact: true }).waitFor({ state: "visible", timeout: ACTION_MS });
    await models.getByRole("button", { name: "Add provider", exact: true }).click({ timeout: ACTION_MS });
    await models.getByPlaceholder("provider-name", { exact: true }).waitFor({ state: "visible", timeout: ACTION_MS });
    await page.context().close();
  });

  // S85-22 — review r1#1: the model table and the embedded models.json editor
  // share one enabledModels controller. The Save's resync declares the
  // providers that are fully enabled *right now*; with two controllers the
  // editor's stale view would list a provider the table just partially
  // disabled, and the server would re-enable what the user switched off.
  await part("S22", "shared enabledModels controller", async () => {
    const page = await newPage();
    const CONFIG = { providers: { gateway: {
      baseUrl: "https://gw.example/v1", apiKey: "fixture-key", api: "openai-completions",
      models: [{ id: "m1", name: "Gateway One" }, { id: "m2", name: "Gateway Two" }],
    } } };
    let m1Enabled = true;
    const enabledView = () => ({
      allEnabled: false, patterns: ["gateway/*"], stalePatterns: [],
      enabledTotal: m1Enabled ? 2 : 1, availableTotal: 2, scope: "global",
      settingsPath: "fixture/settings.json", editable: true,
      providers: [{ id: "gateway", name: "Gateway", kind: "custom", enabledCount: m1Enabled ? 2 : 1, models: [
        { id: "m1", name: "Gateway One", ref: "gateway/m1", enabled: m1Enabled, contextWindow: 2048 },
        { id: "m2", name: "Gateway Two", ref: "gateway/m2", enabled: true, contextWindow: 2048 },
      ] }],
    });
    const puts = [];
    await page.route("**/api/models/enabled*", (route) => {
      const request = route.request();
      if (request.method() === "PUT") {
        const body = request.postDataJSON();
        puts.push(body);
        if (body.op === "models" && body.enabled === false && body.refs.includes("gateway/m1")) m1Enabled = false;
        if (body.op === "resync" && (body.fullyEnabled ?? []).includes("gateway")) m1Enabled = true;
      }
      return route.fulfill({ json: enabledView() });
    });
    await page.route("**/api/models-config", (route) => {
      if (route.request().method() === "PUT") return route.fulfill({ json: { success: true } });
      return route.fulfill({ json: CONFIG });
    });
    await page.route(/\/api\/models\?/, (route) => route.fulfill({ json: {
      models: {}, modelList: [
        { id: "m1", name: "Gateway One", provider: "gateway" },
        { id: "m2", name: "Gateway Two", provider: "gateway" },
      ], defaultModel: { provider: "gateway", modelId: "m1" }, thinkingLevels: {}, thinkingLevelMaps: {}, thinkingLevelPins: {}, isScoped: false,
    } }));
    await page.route("**/api/models/default", (route) => route.fulfill({ json: {
      defaultHarness: "pi", claudeDefaultModel: "default", claudeDefaultEffort: "default",
    } }));
    await page.route("**/api/claude", (route) => route.fulfill({ json: { models: [] } }));
    await page.route("**/api/harness/pi/model", (route) => route.fulfill({ json: { harness: "pi", concept: "model", data: {
      defaultProvider: "gateway", defaultModel: "m1", reasoningEffort: null,
      perModelReasoningEffort: {}, providers: [], customModels: [],
    } } }));
    await page.route("**/api/harness/claude/model", (route) => route.fulfill({ json: { harness: "claude", concept: "model", data: {
      defaultProvider: null, defaultModel: null, reasoningEffort: null,
      perModelReasoningEffort: {}, providers: [], customModels: [], userCustomModels: [],
    } } }));
    await page.route(/\/api\/auth\/providers/, (route) => route.fulfill({ json: {
      providers: [], oauthProviders: [], apiKeyProviders: [],
    } }));
    await page.route("**/api/provider-usage/query", (route) => route.fulfill({ json: { providers: [] } }));
    await openSettings(page);
    await tab(page, /^Models$/).click({ timeout: ACTION_MS });
    const models = sectionHost(page, "models");
    await models.locator('[data-model-page="unified"]').waitFor({ state: "visible", timeout: ACTION_MS });
    const m1Row = models.getByRole("row").filter({ hasText: "gateway/m1" });
    const m1Toggle = m1Row.getByRole("checkbox");
    await m1Toggle.waitFor({ state: "visible", timeout: ACTION_MS });
    await m1Toggle.click();
    await waitFor(async () => puts.some((body) => body.op === "models" && body.refs.includes("gateway/m1"))
      && !(await m1Toggle.isChecked()),
      ACTION_MS, "the table toggle never reached the writer");
    await models.getByRole("button", { name: "Save", exact: true }).click({ timeout: ACTION_MS });
    const resync = await waitFor(() => puts.find((body) => body.op === "resync") ?? false,
      ACTION_MS, "the Save never re-verified the stored patterns");
    assert.ok(!(resync.fullyEnabled ?? []).includes("gateway"),
      `the resync re-enabled the model the table just disabled: ${JSON.stringify(resync)}`);
    await page.context().close();
  });

  // S85-06 — AC2: the environment pane lists only variable names, per harness.
  await part("S6", "environment names only", async () => {
    const page = await newPage();
    await openSettings(page);
    await generalSidebarItems(page).filter({ hasText: "Environment" }).click({ timeout: ACTION_MS });
    const pane = generalPane(page, "environment");
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    const text = await waitFor(async () => {
      const body = await pane.innerText();
      return body.includes("ANTHROPIC_API_KEY") && body.includes("CLAUDE_CODE_EXTRA_BODY") ? body : false;
    }, ACTION_MS, "the environment pane never listed the mocked names");
    for (const name of ["ANTHROPIC_API_KEY", "GEMINI_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_EXTRA_BODY"]) {
      assert.ok(text.includes(name), `the environment pane does not list ${name}`);
    }
    for (const harness of ["pi", "claude"]) {
      await pane.locator(`[data-harness="${harness}"]`).waitFor({ state: "visible", timeout: ACTION_MS });
    }
    assert.match(text, /Only variable names are shown/, "the names-only note is missing");
    await page.context().close();
  });

  // S85-07 — AC2/AC6: Inferred marks and sources; native diagnostics as a collapsed block.
  await part("S7", "effective values and diagnostics", async () => {
    const page = await newPage();
    await openSettings(page);
    await generalSidebarItems(page).filter({ hasText: "Effective values & sources" }).click({ timeout: ACTION_MS });
    const pane = generalPane(page, "effective");
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    const text = await waitFor(async () => {
      const body = await pane.innerText();
      return body.includes("route-model-85") ? body : false;
    }, ACTION_MS, "the effective pane never showed a mocked value");
    assert.match(text, /Inferred/, "the Inferred mark is missing");
    assert.ok(text.includes(join(agentDir, "settings.json")), "the value's source path is missing");
    // Diagnostics: one collapsed block per harness; expanding loads the output once.
    const claudeBlock = pane.locator("details.settings-harness-diagnostics[data-harness=\"claude\"]");
    await claudeBlock.waitFor({ state: "visible", timeout: ACTION_MS });
    assert.equal(await claudeBlock.getAttribute("open"), null, "the diagnostics block starts open");
    await claudeBlock.locator("summary").click({ timeout: ACTION_MS });
    await claudeBlock.getByText("claude doctor: installation healthy").waitFor({ state: "visible", timeout: ACTION_MS });
    const piBlock = pane.locator("details.settings-harness-diagnostics[data-harness=\"pi\"]");
    await piBlock.locator("summary").click({ timeout: ACTION_MS });
    await piBlock.getByText(/no fixed safe diagnostic command/).waitFor({ state: "visible", timeout: ACTION_MS });
    await page.context().close();
  });

  // S85-09 — AC2/Q4A (r2#3): a failed harness-list read is a visible reason in both
  // panes — never silence, and never the contradictory「No harnesses registered.」.
  await part("S9", "a failed harness list says why", async () => {
    const page = await newPage();
    // The later registration wins over the shared adapter mocks: the list itself 500s.
    await page.context().route(/\/api\/harness(?:\/|\?|$)/, (route) => route.fulfill({ status: 500, json: { error: "internal" } }));
    await openSettings(page);
    await generalSidebarItems(page).filter({ hasText: "Effective values & sources" }).click({ timeout: ACTION_MS });
    const effectivePane = generalPane(page, "effective");
    await effectivePane.waitFor({ state: "visible", timeout: ACTION_MS });
    const effectiveText = await waitFor(async () => {
      const body = await effectivePane.innerText();
      return body.includes("The harness list could not be read: HTTP 500") ? body : false;
    }, ACTION_MS, "the effective pane never showed the list read's failure");
    assert.ok(!effectiveText.includes("No harnesses registered."), `the failed read masquerades as an empty registry: ${effectiveText.slice(0, 200)}`);
    await generalSidebarItems(page).filter({ hasText: "Environment" }).click({ timeout: ACTION_MS });
    const envPane = generalPane(page, "environment");
    await envPane.waitFor({ state: "visible", timeout: ACTION_MS });
    await waitFor(async () => (await envPane.innerText()).includes("The harness list could not be read: HTTP 500"), ACTION_MS, "the environment pane never showed the list read's failure");
    await page.context().close();
  });

  // S85-08 — AC4: the stored models value lands on the new page; the deleted
  // Claude page's `claude` key reads as unknown and lands on General (the
  // migration map went with the page, review r1#7).
  await part("S8", "navigation migration", async () => {
    for (const [stored, expectedTab, expectedHost] of [
      ["models", /^Models$/, "models"],
      ["claude", /^General$/, "general"],
    ]) {
      const page = await newPage(undefined, { "pi-web:settings-navigation": JSON.stringify({ section: stored }) });
      await openSettings(page);
      const current = sectionTabs(page).filter({ hasText: expectedTab }).first();
      await current.waitFor({ state: "visible", timeout: COLD_MS });
      assert.equal(await current.getAttribute("aria-current"), "page", `stored ${stored} did not land on ${expectedHost}`);
      await sectionHost(page, expectedHost).waitFor({ state: "visible", timeout: ACTION_MS });
      await page.context().close();
    }
  });

  // S85-16 — 85a4 AC1/AC4: the approval pane's unified block and claude's own sections.
  await part("S16", "approval pane", async () => {
    const page = await newPage();
    permissionPuts.length = 0;
    await openSettings(page);
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const permission = sectionHost(page, "permission");
    await permission.locator(".config-sidebar-item").first().waitFor({ state: "visible", timeout: ACTION_MS });
    const pane = permission.locator('[data-permission-pane="approval"]');
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    // The unified block: the current value, pi's own 不做审批 note, claude's value.
    const unified = pane.locator(".unified-setting").first();
    await unified.waitFor({ state: "visible", timeout: ACTION_MS });
    const text = await waitFor(async () => {
      const body = await unified.innerText();
      return body.includes("pi does no approval prompting") ? body : false;
    }, ACTION_MS, "the unified block never showed pi's own note");
    assert.ok(text.includes("default"), `the unified block does not show the current mode: ${text}`);
    // claude's own sections: the extra values' radio, denyInsteadOfPrompting,
    // and the Pi Web approval channel (the moved claude tool permissions).
    await pane.getByRole("radio", { name: /bypassPermissions/ }).waitFor({ state: "visible", timeout: ACTION_MS });
    await pane.getByRole("switch", { name: "denyInsteadOfPrompting" }).waitFor({ state: "visible", timeout: ACTION_MS });
    await pane.getByRole("radiogroup", { name: "Permission mode", exact: true }).waitFor({ state: "visible", timeout: ACTION_MS });
    await pane.getByRole("switch", { name: "Push a reminder when a permission waits" }).waitFor({ state: "visible", timeout: ACTION_MS });
    // The retired page's Claude Code report block renders again (owner
    // ruling: taken over): version, claude's own capabilities, the features.
    const reportText = await waitFor(async () => {
      const body = await pane.innerText();
      return body.includes("2.1.322") && body.includes("Subagent text forwarding") ? body : false;
    }, ACTION_MS, "the Claude Code report block never showed its mocked answer");
    assert.ok(reportText.includes("ui_surface_v1"), "claude's own capabilities are not listed");
    assert.ok(reportText.includes("Background tasks"), "the feature table is missing");
    assert.equal(permissionPuts.length, 0, "opening the page wrote something");
    await page.context().close();
  });

  // S85-17 — 85a4 AC1/AC2 (Q1 A): the unified three write claude's native mode;
  // the extra values are claude's own radio.
  await part("S17", "unified approval write", async () => {
    const page = await newPage();
    permissionPuts.length = 0;
    await openSettings(page);
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const pane = sectionHost(page, "permission").locator('[data-permission-pane="approval"]');
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    // 不询问即拒绝 writes claude's native dontAsk (Q1 A).
    await pane.getByRole("radio", { name: "Deny without asking" }).click({ timeout: ACTION_MS });
    await waitFor(() => permissionPuts.some((put) => put.path === "/api/harness/claude/permission" && put.body?.approvalMode === "dontAsk"),
      ACTION_MS, `the unified deny write never reached claude: ${JSON.stringify(permissionPuts)}`);
    // An extra value is claude's own, written from its radio.
    await pane.getByRole("radio", { name: /acceptEdits/ }).click({ timeout: ACTION_MS });
    await waitFor(() => permissionPuts.some((put) => put.body?.approvalMode === "acceptEdits"),
      ACTION_MS, `the extra value write never reached claude: ${JSON.stringify(permissionPuts)}`);
    await page.context().close();
  });

  // S85-18 — 85a4 AC1/AC2: the sandbox pane writes claude's sandbox keys; pi answers 无沙箱.
  await part("S18", "sandbox pane", async () => {
    const page = await newPage();
    permissionPuts.length = 0;
    await openSettings(page);
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const permission = sectionHost(page, "permission");
    await permission.locator(".config-sidebar-item").filter({ hasText: "Sandbox" }).click({ timeout: ACTION_MS });
    const pane = permission.locator('[data-permission-pane="sandbox"]');
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    await waitFor(async () => (await pane.locator(".unified-setting").first().innerText()).includes("pi has no sandbox"),
      ACTION_MS, "the sandbox block never showed pi's own note");
    const enabled = pane.getByRole("switch", { name: "Bash sandbox" });
    await enabled.waitFor({ state: "visible", timeout: ACTION_MS });
    assert.equal(await enabled.getAttribute("aria-checked"), "true", "the mocked enabled switch is off");
    await enabled.click({ timeout: ACTION_MS });
    await waitFor(() => permissionPuts.some((put) => put.body?.sandboxEnabled === false),
      ACTION_MS, `the sandbox write never reached claude: ${JSON.stringify(permissionPuts)}`);
    const autoAllow = pane.getByRole("switch", { name: "Auto-allow Bash if sandboxed" });
    await autoAllow.click({ timeout: ACTION_MS });
    await waitFor(() => permissionPuts.some((put) => put.body?.autoAllowBashIfSandboxed === true),
      ACTION_MS, `the auto-allow write never reached claude: ${JSON.stringify(permissionPuts)}`);
    // claude's other sandbox keys show by name only.
    assert.match(await pane.innerText(), /failIfUnavailable/, "the other sandbox keys are missing");
    await page.context().close();
  });

  // S85-19 — 85a4 AC1: the trust pane shows each harness's own decision, and
  // 统一成当前值 writes the differing harness with the current value.
  await part("S19", "trust unify", async () => {
    const page = await newPage();
    permissionPuts.length = 0;
    await openSettings(page);
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const permission = sectionHost(page, "permission");
    await permission.locator(".config-sidebar-item").filter({ hasText: "Project trust" }).click({ timeout: ACTION_MS });
    const pane = permission.locator('[data-permission-pane="trust"]');
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    const unified = pane.locator(".unified-setting").first();
    await waitFor(async () => {
      const body = await unified.innerText();
      return body.includes("trusted") && body.includes("untrusted") ? body : false;
    }, ACTION_MS, "the trust block never showed both harnesses' decisions");
    const unify = pane.getByRole("button", { name: "Unify to the current value" });
    await unify.waitFor({ state: "visible", timeout: ACTION_MS });
    await unify.click({ timeout: ACTION_MS });
    // pi is the current value (trusted); claude differs, so claude is written.
    await waitFor(() => permissionPuts.some((put) => put.path === "/api/harness/claude/permission" && put.body?.projectTrust?.trusted === true),
      ACTION_MS, `the trust unify never reached claude: ${JSON.stringify(permissionPuts)}`);
    // The unify writes its targets in order, so a pi write would have landed
    // before the claude one that was just observed: the negative is settled.
    assert.ok(!permissionPuts.some((put) => put.path === "/api/harness/pi/permission"), "the unify rewrote the harness that already agreed");
    await page.context().close();
  });

  // S85-20 — 85a4 AC2: the rules pane edits claude's allow / ask / deny lists,
  // with no unified block (规则没有统一块).
  await part("S20", "rules editor", async () => {
    const page = await newPage();
    permissionPuts.length = 0;
    await openSettings(page);
    await tab(page, /^Permissions$/).click({ timeout: ACTION_MS });
    const permission = sectionHost(page, "permission");
    await permission.locator(".config-sidebar-item").filter({ hasText: "Rules" }).click({ timeout: ACTION_MS });
    const pane = permission.locator('[data-permission-pane="rules"]');
    await pane.waitFor({ state: "visible", timeout: ACTION_MS });
    // No unified block on the rules pane.
    assert.equal(await pane.locator(".unified-setting").count(), 0, "the rules pane shows a unified block");
    // The textareas render empty until the page's read fills them: poll the
    // values themselves, never one read after visibility (r1#6).
    const allow = pane.locator("#permission-rules-allow");
    await allow.waitFor({ state: "visible", timeout: ACTION_MS });
    await waitFor(async () => (await allow.inputValue()) === "WebSearch", ACTION_MS, "the mocked allow rules never arrived");
    await waitFor(async () => (await pane.locator("#permission-rules-deny").inputValue()) === "Bash(rm:*)", ACTION_MS, "the mocked deny rules never arrived");
    await allow.fill("WebSearch\nWebFetch(domain:example.com)");
    await pane.getByRole("button", { name: "Save", exact: true }).click({ timeout: ACTION_MS });
    await waitFor(() => permissionPuts.some((put) => put.body?.rules?.allow?.length === 2 && put.body.rules.allow.includes("WebFetch(domain:example.com)")
      && put.body.rules.deny?.length === 1),
      ACTION_MS, `the rules write never reached claude: ${JSON.stringify(permissionPuts)}`);
    await page.context().close();
  });

  // S85-21 — 85a4 AC4: General has no claude permission controls; the hook
  // switch (a Pi Web preference now) stays.
  await part("S21", "general cleanup", async () => {
    const page = await newPage();
    await openSettings(page);
    const conversation = generalPane(page, "conversation");
    await conversation.waitFor({ state: "visible", timeout: ACTION_MS });
    // Settle on the hook switch first (it renders only once its read landed),
    // then read the pane once for the negatives (r1#15).
    const text = await waitFor(async () => {
      const body = await conversation.innerText();
      return body.includes("Show hook activity") ? body : false;
    }, ACTION_MS, "the conversation pane never settled");
    assert.ok(!text.includes("claude tool permissions"), `General still shows the claude permission block: ${text.slice(0, 200)}`);
    assert.ok(!text.includes("Ask in a dialog"), `General still shows the approval channel radios: ${text.slice(0, 200)}`);
    await page.context().close();
  });

  // S85-23 — 85a3-3: entering the Claude section fires the protected migration
  // POST; a non-empty native target answers the merge preview (the native rows
  // kept, the missing names appended, nothing written until「合并」); the editor
  // writes every add and remove as the full next list through the adapter's
  // PUT. The backend is browser-mocked: this pins the page's contract.
  await part("S23", "claude custom models section", async () => {
    const page = await newPage();
    const DIGEST = "f".repeat(64);
    const migrationPosts = [];
    const modelPuts = [];
    let names = ["claude-a", "claude-b"];
    const modelPayload = () => ({ harness: "claude", concept: "model", data: {
      defaultProvider: null, defaultModel: null, reasoningEffort: null,
      perModelReasoningEffort: {}, providers: [], customModels: names, userCustomModels: names,
    } });
    await page.route("**/api/claude-models-migration", (route) => {
      migrationPosts.push(route.request().postDataJSON());
      if (route.request().postDataJSON().confirm === DIGEST) {
        names = ["native-keep", "claude-a", "claude-b"];
        return route.fulfill({ json: { status: "done", changed: true, shadowed: false } });
      }
      return route.fulfill({ json: {
        status: "preview",
        native: [{ model: "native-keep", label: "K" }],
        missing: ["claude-a", "claude-b"],
        digest: DIGEST,
        shadowed: false,
      } });
    });
    await page.route("**/api/harness/claude/model", (route) => {
      if (route.request().method() === "PUT") {
        const body = route.request().postDataJSON();
        modelPuts.push(body);
        names = body.customModels;
        return route.fulfill({ json: { harness: "claude", concept: "model", result: { status: "written", summary: "modelPicker.options" } } });
      }
      return route.fulfill({ json: modelPayload() });
    });
    await page.route(/\/api\/models\?/, (route) => route.fulfill({ json: {
      models: {}, modelList: [], defaultModel: null, thinkingLevels: {}, thinkingLevelMaps: {}, thinkingLevelPins: {}, isScoped: false,
    } }));
    await page.route("**/api/models/default", (route) => route.fulfill({ json: {
      defaultHarness: "pi", claudeDefaultModel: "default", claudeDefaultEffort: "default",
    } }));
    await page.route("**/api/models/enabled*", (route) => route.fulfill({ json: {
      allEnabled: true, patterns: [], stalePatterns: [], enabledTotal: 0, availableTotal: 0,
      scope: "global", settingsPath: "fixture/settings.json", editable: true, providers: [],
    } }));
    await page.route("**/api/models-config", (route) => route.fulfill({ json: { providers: {} } }));
    await page.route("**/api/harness/pi/model", (route) => route.fulfill({ json: { harness: "pi", concept: "model", data: {
      defaultProvider: null, defaultModel: null, reasoningEffort: null,
      perModelReasoningEffort: {}, providers: [], customModels: [], userCustomModels: [],
    } } }));
    await openSettings(page);
    await tab(page, /^Models$/).click({ timeout: ACTION_MS });
    const models = sectionHost(page, "models");
    const section = models.locator("[data-claude-custom-models]");
    await section.waitFor({ state: "visible", timeout: ACTION_MS });
    const banner = section.locator("[data-claude-migration-preview]");
    await banner.waitFor({ state: "visible", timeout: ACTION_MS });
    const bannerText = await banner.innerText();
    assert.ok(bannerText.includes("native-keep (K)"), `the preview lost the native row: ${bannerText}`);
    assert.ok(bannerText.includes("claude-a"), `the preview lost the missing name: ${bannerText}`);
    // The preview writes nothing: every mount POST so far carries no confirm
    // (a dev-mode StrictMode double mount may fire the effect twice), and no
    // PUT has happened.
    assert.ok(migrationPosts.length >= 1 && migrationPosts.length <= 2,
      `the mount fired ${migrationPosts.length} POSTs (one, or a dev-mode StrictMode double)`);
    assert.ok(migrationPosts.every((body) => body?.confirm === undefined), "a mount POST carried a confirm");
    assert.equal(modelPuts.length, 0, "the preview wrote the picker");
    await banner.getByRole("button", { name: "Merge", exact: true }).click({ timeout: ACTION_MS });
    await waitFor(async () => {
      const confirmed = migrationPosts.find((body) => body?.confirm === DIGEST);
      return confirmed && await section.locator("[data-claude-migration-preview]").count() === 0 ? confirmed : false;
    }, ACTION_MS, "the merge never confirmed under its digest");
    await waitFor(async () => (await section.innerText()).includes("native-keep"),
      ACTION_MS, "the merged list never showed the native name");
    // The editor removes and adds with full-list writes through the PUT.
    await section.getByRole("button", { name: "Remove claude-a", exact: true }).click({ timeout: ACTION_MS });
    await waitFor(() => modelPuts.some((body) => JSON.stringify(body.customModels) === JSON.stringify(["native-keep", "claude-b"])),
      ACTION_MS, "the remove never wrote the full next list");
    await section.getByPlaceholder("Model name --model accepts").fill("claude-c");
    await section.getByRole("button", { name: "Add", exact: true }).click({ timeout: ACTION_MS });
    await waitFor(() => modelPuts.some((body) => JSON.stringify(body.customModels) === JSON.stringify(["native-keep", "claude-b", "claude-c"])),
      ACTION_MS, "the add never wrote the full next list");
    await page.context().close();
  });
  // S85-30 — polyforge#256 B7: the model list's step-tier column is a
  // read-only badge. A merged pi+claude row that shares one tier shows that
  // tier's name; no editor renders anywhere in the cell, and opening the page
  // writes nothing (the write gate and CLI stay, the UI entry is gone).
  await part("S30", "read-only tier badge", async () => {
    await startTierServer();
    const page = await newPage();
    seedTiers({ default: ["pi:anthropic/opus", "claude:opus"] });
    await tierPageMocks(page);
    await openSettings(page);
    await tab(page, /^Models$/).click({ timeout: ACTION_MS });
    const host = sectionHost(page, "models");
    await host.locator('[data-model-page="unified"]').waitFor({ state: "visible", timeout: ACTION_MS });
    const cell = host.locator("[data-step-tier-cell]").first();
    await cell.waitFor({ state: "visible", timeout: ACTION_MS });
    assert.match(await cell.innerText(), /default/, "a shared tier shows its name");
    assert.equal(await cell.locator("[data-step-tier-select]").count(), 0, "no tier select renders");
    assert.equal(await cell.locator("button").count(), 0, "no button renders in the cell");
    assert.equal(await cell.locator('input[type="checkbox"]').count(), 0, "no apply-to checkbox renders");
    assert.ok(!(await cell.innerText()).includes("Apply to"), "no apply-to control renders");
    assert.ok((await host.innerText()).includes("Chat availability"), "the other columns are untouched");
    // opening the page is a read: no Host config write reached pf
    const writes = tierArgv().filter((args) => (args.includes("set") || args.includes("unset")) && args.some((entry) => String(entry).startsWith("step_tiers.")));
    assert.deepEqual(writes, [], "no step-tier write from the page");
    await page.context().close();
  });

  // S85-31 — polyforge#256 B7: differing harnesses keep their real 归属 as
  // short read-only labels, default first.
  await part("S31", "per-harness badges", async () => {
    const page = await newPage();
    seedTiers({ default: ["pi:anthropic/opus"], "fast-chat": ["claude:opus"] });
    await tierPageMocks(page);
    await openSettings(page);
    await tab(page, /^Models$/).click({ timeout: ACTION_MS });
    const host = sectionHost(page, "models");
    const cell = host.locator("[data-step-tier-cell]").first();
    await cell.waitFor({ state: "visible", timeout: ACTION_MS });
    const text = await cell.innerText();
    assert.match(text, /pi: default/, "pi's own tier is named with its harness");
    assert.match(text, /claude: fast-chat/, "claude's own tier is named with its harness");
    assert.equal(await cell.locator("[data-step-tier-select]").count(), 0, "no editor renders");
    assert.equal(await cell.locator("button").count(), 0, "no control renders");
    await page.context().close();
  });

  // S85-32 — polyforge#256 B1: an incapable catalog read (pf below the
  // family's 0.2.4-0 floor) leaves the column read-only with a localized
  // reason naming the found version, while the other columns stay.
  await part("S32", "localized read-only reason", async () => {
    const page = await newPage();
    await tierPageMocks(page);
    await page.route(/\/api\/polyforge\/config\?host=1/, (route) => route.fulfill({ json: {
      pfPresent: true, pfVersion: "v0.2.3", capable: true,
      stepTiersReason: "the step-tier catalog needs pf 0.2.4-0 or newer (found v0.2.3)",
      items: [], file: { sha256: "e2e-sha", exists: true }, mapped: false } }));
    await openSettings(page);
    await tab(page, /^Models$/).click({ timeout: ACTION_MS });
    const host = sectionHost(page, "models");
    const reason = host.locator("[data-step-tier-reason]");
    await reason.waitFor({ state: "visible", timeout: ACTION_MS });
    const text = await reason.innerText();
    assert.match(text, /needs pf 0\.2\.4-0/, "the reason names the new floor");
    assert.match(text, /v0\.2\.3/, "the reason names the found version");
    assert.ok(!text.startsWith("the step-tier catalog needs"), "the reason is localized, not the server's English sentence");
    assert.equal(await host.locator("[data-step-tier-select]").count(), 0, "an incapable catalog leaves the column read-only");
    assert.ok((await host.innerText()).includes("Chat availability"), "the other columns are untouched");
    await page.context().close();
  });

  // S85-33 — polyforge#256 B7: there is no write entry at all. The cell has
  // no interactive control, and no step-tier write reaches pf from the page.
  await part("S33", "no write entry", async () => {
    const page = await newPage();
    seedTiers({ default: ["pi:anthropic/opus"] });
    await tierPageMocks(page);
    await openSettings(page);
    await tab(page, /^Models$/).click({ timeout: ACTION_MS });
    const host = sectionHost(page, "models");
    const cell = host.locator("[data-step-tier-cell]").first();
    await cell.waitFor({ state: "visible", timeout: ACTION_MS });
    assert.equal(await cell.locator("select, input, button").count(), 0, "the cell is pure text");
    await cell.click({ timeout: ACTION_MS }).catch(() => {});
    const writes = tierArgv().filter((args) => (args.includes("set") || args.includes("unset")) && args.some((entry) => String(entry).startsWith("step_tiers.")));
    assert.deepEqual(writes, [], "clicking the read-only cell writes nothing");
    await page.context().close();
  });

  // S85-34 — polyforge#256 B7/AC1: the labels follow the Host storage. An
  // external writer changes [step_tiers]; a reload shows the new 归属.
  await part("S34", "external write re-read", async () => {
    const page = await newPage();
    seedTiers({ default: ["pi:anthropic/opus", "claude:opus"] });
    await tierPageMocks(page);
    await openSettings(page);
    await tab(page, /^Models$/).click({ timeout: ACTION_MS });
    const host = sectionHost(page, "models");
    let cell = host.locator("[data-step-tier-cell]").first();
    await cell.waitFor({ state: "visible", timeout: ACTION_MS });
    assert.match(await cell.innerText(), /default/, "the row starts sharing default");
    // the external write (another tab, the CLI) moves claude to fast-chat
    seedTiers({ default: ["pi:anthropic/opus"], "fast-chat": ["claude:opus"] });
    await goto(page);
    await clickSettingsButton(page);
    await dialog(page).waitFor({ state: "visible", timeout: COLD_MS });
    await tab(page, /^Models$/).click({ timeout: ACTION_MS });
    cell = sectionHost(page, "models").locator("[data-step-tier-cell]").first();
    await cell.waitFor({ state: "visible", timeout: ACTION_MS });
    const text = await cell.innerText();
    assert.match(text, /pi: default/, "the reload shows pi's new 归属");
    assert.match(text, /claude: fast-chat/, "the reload shows claude's new 归属");
    await page.context().close();
  });

} finally {
  await h.close();
}

const failed = [...failures].filter(([, list]) => list.length > 0);
if (failed.length > 0 || ran.size < Object.keys(T).length) {
  for (const [title, list] of failed) {
    console.log(`FAIL harness-settings-85: ${title}`);
    for (const entry of list) console.log(`  ${entry}`);
  }
  for (const title of Object.values(T)) if (!ran.has(title)) console.log(`FAIL harness-settings-85: never ran: ${title}`);
  process.exitCode = 1;
} else {
  console.log("PASS harness-settings-85");
}
