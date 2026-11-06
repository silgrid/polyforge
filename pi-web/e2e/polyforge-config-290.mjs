// carries: lib/polyforge-detect*
// — Settings › Polyforge configuration panel on the #286 wire.
// Authored pre-code against artifacts/spec.md. The panel runs against the real
// config routes with a stub `pf` on PATH; its `config workspace` reply
// still holds the OLD summarize/memory_policy while `config list` has the new wire.
//
//   node e2e/polyforge-config-290.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { chromium } from "playwright";
import { COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, joinedLines, writeFileAtomic } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-290-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const SESSION = "e2e-pf-290";
const SESSION_UNMAPPED = "e2e-pf-290-unmapped";
const CWD = join(work, "ws-alpha");
const WS_BETA = join(work, "ws-beta");
const UNMAPPED = join(work, "not-a-workspace");
for (const dir of [agentDir, join(home, ".config", "polyforge"), CWD, WS_BETA, UNMAPPED]) mkdirSync(dir, { recursive: true });
const writeSession = (id, cwd, text) => {
  const dir = join(agentDir, "sessions", "e2e");
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, `${id}.jsonl`), [
    { type: "session", version: 3, id, timestamp: "2026-11-03T00:00:00.000Z", cwd },
    { type: "message", id: "m1", parentId: null, timestamp: "2026-11-03T00:00:00.000Z", message: { role: "user", content: text } },
  ].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
};
writeSession(SESSION, CWD, "E2E polyforge 290 host session");
writeSession(SESSION_UNMAPPED, UNMAPPED, "E2E polyforge 290 unmapped session");

const fileSha = (path) => createHash("sha256").update(readFileSync(path)).digest("hex");
const cliPath = join(home, ".config", "polyforge", "cli.toml");
const cliText = `server = "http://127.0.0.1:1"\napi_key = "e2e-key"\ndaemon_socket = "/run/pf.sock"\nuser_id = "u_e2e"\n\n[daemon]\nmax_concurrent_jobs = 6\n\n[workspace."${CWD}"]\nprojects = ["alpha"]\n\n[workspace."${WS_BETA}"]\nprojects = ["beta"]\n\n[summarize]\nmodels = ["pi:list-model"]\nlanguage = "Chinese"\ninterval_sec = 120\n`;
writeFileSync(cliPath, cliText);
const CLI_SHA = createHash("sha256").update(cliText).digest("hex");
const yamlText = (name) => `summarize:\n  models: ["pi:workspace-model"]\n  language: "Japanese"\n  interval_sec: 60\nmemory_policy:\n  default:\n    record: ["Yaml ${name} policy note"]\n`;
const yamlA = join(CWD, ".polyforge.yaml");
const yamlB = join(WS_BETA, ".polyforge.yaml");
writeFileSync(yamlA, yamlText("alpha"));
writeFileSync(yamlB, yamlText("beta"));
// ---- stub pf: config list (the new wire), a drifted config workspace, ops ----
const stubDir = join(work, "stub");
mkdirSync(stubDir);
const listFull = {
  file: { path: cliPath, sha256: CLI_SHA, exists: true },
  items: [
    { key: "server", value: "http://127.0.0.1:1", source: "file", class: "restart-only" },
    { key: "api_key", set: true, source: "file", class: "restart-only" },
    { key: "daemon_socket", value: "/run/pf.sock", source: "file", class: "restart-only" },
    { key: "harness_root", value: "/opt/pf", source: "file", class: "restart-only" },
    { key: "operator_agent_dir", value: "/home/op/.pi", source: "file", class: "restart-only" },
    { key: "user_id", value: "u_e2e", source: "file", class: "cli-only" },
    { key: "claude_bin", value: "/usr/local/bin/claude", source: "file", class: "live" },
    { key: "step_timeout", value: "30m0s", source: "file", class: "live", schema: { kind: "duration" } },
    { key: "stall_window", value: "10m0s", source: "file", class: "live", schema: { kind: "duration" } },
    { key: "max_concurrent_jobs", value: 6, source: "file", class: "live", schema: { kind: "int", min: 1, max: 32 } },
    { key: `[workspace."${CWD}"]`, value: "alpha", source: "file", class: "restart-only", section: true },
    { key: `[workspace."${WS_BETA}"]`, value: "beta", source: "file", class: "restart-only", section: true },
    { key: "summarize.models", value: ["pi:list-model"], source: "file", class: "live", schema: { kind: "model-list" } },
    { key: "summarize.language", value: "Chinese", source: "file", class: "live", schema: { kind: "string" } },
    { key: "summarize.interval_sec", value: 120, source: "file", class: "live", schema: { kind: "int", min: 10, max: 3600 } },
  ],
  memory_policy: {
    [CWD]: {
      default: { record: ["List default note"], skip: ["List default secret"], declared: true },
      alpha: { record: ["List alpha note"], skip: [], declared: true },
      beta: { record: ["List beta note"], skip: ["List beta secret"], declared: true },
    },
    [WS_BETA]: {
      default: { record: ["List beta workspace note"], skip: [], declared: true },
    },
  },
};
const withoutMoved = { file: listFull.file, items: listFull.items.filter((item) => !item.key.startsWith("summarize.")) };
const emptyPolicy = { ...listFull, memory_policy: {} };
for (const [name, doc] of [["full", listFull], ["no-moved", withoutMoved], ["empty-policy", emptyPolicy]]) {
  writeFileSync(join(stubDir, `list-${name}.json`), JSON.stringify(doc));
}
const workspaceDoc = {
  mapped: true,
  workspace: CWD,
  yaml: { path: yamlA, sha256: fileSha(yamlA), exists: true },
  summarize: {
    models: { value: ["pi:workspace-model"], source: "file", class: "live", schema: { kind: "model-list" } },
    language: { value: "Japanese", source: "file", class: "live", schema: { kind: "string" } },
    interval_sec: { value: 60, source: "file", class: "live", schema: { kind: "int", min: 10, max: 3600 } },
  },
  steps: {},
  steps_complete: true,
  memory_policy: {
    source: "file", class: "next-claim", append_to_default: true,
    default: { record: ["Yaml alpha policy note"], skip: [], declared: true },
    projects: { alpha: { record: ["Yaml alpha note"], skip: [], declared: true } },
  },
};
writeFileSync(join(stubDir, "workspace.json"), JSON.stringify(workspaceDoc));
const state = { version: "2.21.0", list: "full", workspace: "mapped" };
const writeState = (patch = {}) => { Object.assign(state, patch); writeFileAtomic(join(stubDir, "state.json"), JSON.stringify(state)); };
const baseState = { version: "2.21.0", list: "full", workspace: "mapped" };
const resetState = (patch = {}) => writeState({ ...baseState, ...patch });
writeState();
const argvLog = join(stubDir, "argv.log");
writeFileSync(argvLog, "");
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename));
const read = (name) => JSON.parse(fs.readFileSync(path.join(dir, name), "utf8"));
const state = read("state.json");
const args = process.argv.slice(2);
let stdin = ""; try { stdin = fs.readFileSync(0, "utf8"); } catch {}
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify({ args, stdin }) + "\\n");
const rest = args.filter((a) => a !== "--json");
const flag = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] ?? "" : ""; };
const out = (doc) => { process.stdout.write(JSON.stringify(doc) + "\\n"); process.exit(0); };
if (args.includes("--version")) { process.stdout.write(state.version + "\\n"); process.exit(0); }
if (rest[0] === "config" && rest[1] === "list") out(read("list-" + state.list + ".json"));
if (rest[0] === "config" && rest[1] === "workspace") {
  if (state.workspace === "fail") { process.stderr.write("pf: daemon not reachable\\n"); process.exit(1); }
  const cwd = flag("--cwd");
  if (cwd !== ${JSON.stringify(CWD)} && cwd !== ${JSON.stringify(WS_BETA)}) out({ mapped: false, workspace: null, yaml: null, steps: null });
  const doc = read("workspace.json"); doc.workspace = cwd; doc.yaml.path = cwd + "/.polyforge.yaml"; out(doc);
}
if (rest[0] === "ops" && rest[1] === "status") out({ queue: { pending: 0, running: 0, max_concurrent: 6, max_configured: 6 }, supervision: { step_timeout: "30m0s", stall_window: "10m0s" }, reload: { pending: [], overridden: [] } });
if (rest[0] === "config" && (rest[1] === "set" || rest[1] === "unset")) out({ key: rest[2] ?? "", class: "live", outcome: "applied" });
process.stderr.write("pf: unknown command " + rest.join(" ") + "\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
const calls = () => (existsSync(argvLog) ? readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)) : []);
const resetCalls = () => writeFileSync(argvLog, "");
const failures = [];
const check = createCheck({ failures, message: joinedLines(4), record: ([label], text) => `${label}: ${text}` });

const h = createHarness();
let base;
try {
  ({ base } = await h.startServer({
    name: "polyforge-config-290",
    logFile: join(work, "server.log"),
    env: {
      PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: home,
      PI_CODING_AGENT_DIR: agentDir },
  }));
  await h.launchBrowser(() => chromium.launch());

  const newPage = async (locale = "en") => {
    // no service worker. In build-once mode the production one
    // registers, and a navigation of this reused page that it does not see
    // answered within its 8 s bound becomes offline.html (no Settings button).
    // The suite tests the config panel, not the worker; `next dev` has none.
    const context = await h.browser.newContext({
      viewport: { width: 1280, height: 1000 }, locale: locale === "en" ? "en-US" : locale, serviceWorkers: "block",
    });
    await context.addInitScript((value) => { try { window.localStorage.setItem("pi-locale", value); } catch { /* storage */ } }, locale);
    const page = await context.newPage();
    page.setDefaultTimeout(30_000);
    await page.route("**/api/plugins?*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ packages: [] }) }));
    return page;
  };
  const openSettings = async (page, session = SESSION, cold = false) => {
    await page.goto(`${base}/?session=${session}`, { waitUntil: "domcontentloaded", timeout: cold ? COLD_MS : NAV_MS });
    const show = page.locator('button[aria-label="Show sidebar"]').first();
    if (await show.isVisible().catch(() => false)) await show.click();
    const settings = page.getByRole("button", { name: /^(Settings|设置|設定)$/ }).first();
    const dialog = page.locator('[role="dialog"]').first();
    // Hydration race: a click before the app is live is lost. Retry the real
    // condition (the dialog opens) a bounded number of times, never on a sleep.
    for (let attempt = 0; attempt < 3; attempt += 1) {
      await settings.click();
      if (await dialog.waitFor({ state: "visible", timeout: 3000 }).then(() => true).catch(() => false)) break;
    }
    await dialog.waitFor({ state: "visible", timeout: NAV_MS });
    await dialog.getByRole("button", { name: /Polyforge/i }).first().click();
    await page.locator(".settings-polyforge").first().waitFor({ state: "attached", timeout: 60_000 });
    // the cli.toml card is the default Cell's own pane of the master-detail page.
    await page.locator('[data-pf-cell-entry="default"]').first().click();
    await page.locator('[data-pf-pane="cell:default"]').first().waitFor({ state: "visible", timeout: 30_000 });
    // (review s17#2): max_concurrent_jobs is Host-only (the Host region), so the cli.toml card is awaited on step_timeout
    await page.locator('input[aria-label="step_timeout"]').first().waitFor({ state: "attached", timeout: 60_000 });
  };
  const panelText = (page) => page.locator(".settings-polyforge").first().textContent().then((text) => (text ?? "").replace(/\s+/g, " "));
  const cardText = (page, label) => page.evaluate((wanted) => {
    const card = [...document.querySelectorAll("section.polyforge-config-card")]
      .find((node) => node.getAttribute("aria-label") === wanted);
    return card ? (card.textContent ?? "").replace(/\s+/g, " ") : "";
  }, label);
  // The body is read once (text), then parsed; a non-JSON body is json null.
  const parsed = (status, text) => {
    let json = null;
    try { json = JSON.parse(text); } catch { json = null; }
    return { status, text, json };
  };
  const read = async (path) => {
    const response = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(60_000) });
    return parsed(response.status, await response.text());
  };
  const post = (path, body) => fetch(`${base}${path}`, {
    method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body ?? {}), signal: AbortSignal.timeout(60_000),
  }).then(async (response) => parsed(response.status, await response.text()));

  const page = await newPage();
  await openSettings(page, SESSION, true);

  await check("S290-1", async () => {
    resetState();
    const r = await read(`/api/polyforge/config?cwd=${encodeURIComponent(CWD)}`);
    assert.equal(r.status, 200, `config read answered ${r.status}`);
    const items = Array.isArray(r.json?.items) ? r.json.items : [];
    const byKey = Object.fromEntries(items.map((item) => [item.key, item]));
    assert.equal(byKey["summarize.language"]?.value, "Chinese", "the read must carry config list's summarize.language");
    assert.equal(byKey["summarize.interval_sec"]?.value, 120, "the read must carry config list's summarize.interval_sec");
    assert.deepEqual(byKey["summarize.models"]?.value, ["pi:list-model"], "the read must carry config list's summarize.models");
    assert.equal(byKey["summarize.language"]?.class, "live", "summarize rows must keep pf's class");
    assert.equal(byKey["summarize.language"]?.source, "file", "summarize rows must keep pf's source");
    const language = page.locator('input[aria-label="summarize.language"]').first();
    assert.equal(await language.inputValue(), "Chinese", "the row must show the list value, not the workspace yaml's Japanese");
    const card = await cardText(page, "cli.toml");
    assert.ok(card.includes("pi:list-model"), "the summarize.models chips must come from config list");
    assert.ok(!card.includes("pi:workspace-model"), "a workspace-yaml summarize model reached the cli.toml card");
    const all = await panelText(page);
    assert.ok(!all.includes("Japanese"), "a workspace-yaml summarize value reached the panel");
    assert.ok(!all.includes("2.20.21"), "at/above the floor no upgrade hint may render");
  });
  await check("S290-2", async () => {
    resetState();
    const r = await read(`/api/polyforge/config?cwd=${encodeURIComponent(CWD)}`);
    const policy = r.json?.memoryPolicy;
    assert.ok(policy && typeof policy === "object" && !Array.isArray(policy), "the read must carry config list's path-keyed memory_policy");
    assert.ok(policy[CWD]?.default && policy[CWD]?.alpha, "a path's entries must be default + projects");
    assert.deepEqual(policy[CWD].alpha.record, ["List alpha note"], "a project's own record list must be shown as declared");
    const card = await cardText(page, "cli.toml");
    assert.ok(card.includes("[memory_policy]"), "the read-only memory_policy blocks must live in the cli.toml card");
    assert.ok(card.includes("List default note") && card.includes("List alpha note") && card.includes("List beta note"), "the list's policy notes must reach the panel");
    assert.ok(!card.includes("Yaml alpha policy note"), "a workspace-yaml policy value reached the cli.toml card");
    assert.ok(card.indexOf("List default note") < card.indexOf("List alpha note"), "default must render first");
    assert.ok(card.indexOf("List alpha note") < card.indexOf("List beta note"), "project entries must follow in stable alphabetical order");
    assert.equal((card.match(/List default note/g) ?? []).length, 1, "a project's row was pre-merged with default's lists");
    assert.equal(await page.locator('input[aria-label^="memory_policy"]').count(), 0, "a memory_policy row must have no input");
    assert.equal(await page.locator('button[aria-label*="memory_policy"]').count(), 0, "a memory_policy row must have no save control");

    writeState({ list: "empty-policy" });
    await openSettings(page, SESSION);
    const emptyCard = await cardText(page, "cli.toml");
    assert.ok(!emptyCard.includes("[memory_policy]"), "an empty memory_policy object must render no blocks");
    assert.ok(!emptyCard.includes("List default note"), "an empty memory_policy object invented a placeholder entry");
    assert.equal(await page.locator('input[aria-label="summarize.language"]').count(), 1, "the summarize zone must still render beside an empty policy");
  });
  await check("S290-3", async () => {
    resetState({ workspace: "fail" });
    resetCalls();
    await openSettings(page, SESSION);
    const language = page.locator('input[aria-label="summarize.language"]').first();
    assert.equal(await language.count(), 1, "the summarize rows must render without the workspace read");
    assert.equal(await language.inputValue(), "Chinese", "the row must show the list value");
    const card = await cardText(page, "cli.toml");
    assert.ok(card.includes("List default note"), "the policy blocks must render without the workspace read");
    assert.ok(card.includes("[memory_policy]"), "the memory_policy blocks are missing when config workspace fails");
    const listCalls = calls().filter((call) => call.args.includes("config") && call.args.includes("list"));
    assert.ok(listCalls.length >= 1, "the panel never read config list");
  });
  await check("S290-4", async () => {
    resetState({ list: "no-moved" });
    await openSettings(page, SESSION);
    assert.equal(await page.locator('input[aria-label^="summarize."]').count(), 0, "a drifted workspace yaml must not source summarize rows");
    const all = await panelText(page);
    assert.ok(!all.includes("Japanese"), "a drifted workspace summarize value reached the panel");
    assert.ok(!all.includes("Yaml alpha policy note"), "a drifted yaml-shaped memory_policy reached the panel");
    assert.ok(!all.includes("[memory_policy]"), "a drifted yaml-shaped memory_policy produced a policy block");
  });
  await check("S290-5", async () => {
    resetState();
    await openSettings(page, SESSION_UNMAPPED);
    const card = await cardText(page, "cli.toml");
    assert.ok(card.includes("cli.toml [summarize]"), "the cli.toml card must carry a literal cli.toml [summarize] zone");
    for (const key of ["summarize.models", "summarize.language", "summarize.interval_sec"]) {
      const input = page.locator(`input[aria-label="${key}"]`).first();
      assert.equal(await input.count(), 1, `${key} must be editable with an unmapped cwd`);
      assert.ok(await input.isEnabled(), `${key} must be enabled with an unmapped cwd`);
    }
    const workspace = await cardText(page, "Workspace");
    assert.ok(workspace.includes("ws-alpha") && workspace.includes("ws-beta"), "the 工作区 card must keep its mapping rows");
    assert.ok(workspace.includes("cli.toml"), "the mapping rows must stay tagged cli.toml");
    assert.ok(!workspace.includes("summarize."), "the 工作区 card still presents summarize");
    assert.ok(!workspace.includes("memory_policy"), "the 工作区 card still presents memory_policy");
  });
  await check("S290-6", async () => {
    resetState();
    await openSettings(page, SESSION_UNMAPPED);
    const input = page.locator('input[aria-label="summarize.interval_sec"]').first();
    assert.equal(await input.count(), 1, "the summarize.interval_sec row is missing");
    const beforeA = fileSha(yamlA);
    const beforeB = fileSha(yamlB);
    // The panel may still have a config read in flight (next dev runs the
    // mount effect twice): wait, bounded, until the argv log gains no new
    // `config workspace` line for 1.5 s before counting the write's spawns.
    {
      const workspaceReads = () => calls().filter((call) => call.args.includes("config") && call.args.includes("workspace")).length;
      const deadline = Date.now() + 20_000;
      let seen = workspaceReads();
      let quietSince = Date.now();
      while (Date.now() - quietSince < 1500) {
        assert.ok(Date.now() < deadline, "the panel's config reads never settled within 20 s");
        await new Promise((resolve) => setTimeout(resolve, 250));
        const now = workspaceReads();
        if (now !== seen) { seen = now; quietSince = Date.now(); }
      }
    }
    resetCalls();
    const row = page.locator(".polyforge-config-row").filter({ has: page.locator('input[aria-label="summarize.interval_sec"]') }).first();
    const [response] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/api/polyforge/config") && r.request().method() === "POST", { timeout: NAV_MS }),
      (async () => { await input.fill("240"); await row.getByRole("button", { name: /save|保存/i }).first().click(); })(),
    ]);
    assert.equal(response.status(), 200, `the summarize write answered ${response.status()}`);
    const body = await response.json();
    assert.equal(body.entry?.key, "summarize.interval_sec", "the write did not record its history entry");
    assert.equal(body.entry?.old, 120, "the history's old value must come from config list");
    assert.equal(body.entry?.new, 240, "the history must record the new value");
    assert.equal(body.entry?.scope, "cli", "a summarize write is a cli.toml write");
    const allCalls = calls();
    const atSet = allCalls.findIndex((call) => call.args.includes("set") && call.args.includes("summarize.interval_sec"));
    assert.ok(atSet >= 0, "the panel did not spawn pf config set summarize.interval_sec");
    assert.ok(!allCalls.slice(0, atSet).some((call) => call.args.includes("config") && call.args.includes("workspace")), "a summarize write spawned a config workspace read before the write");
    const setCall = allCalls[atSet];
    assert.ok(setCall.args.includes("240"), "the new value must reach pf");
    assert.ok(!setCall.args.includes("--cwd"), "a summarize write must not carry --cwd");
    const at = setCall.args.indexOf("--expect-state");
    assert.ok(at >= 0, "the summarize write must carry --expect-state");
    const expectState = JSON.parse(setCall.args[at + 1]);
    assert.equal(expectState.file_sha256, CLI_SHA, "the CAS hash must be the cli.toml sha256");
    assert.equal(expectState.values?.["summarize.interval_sec"], 120, "the CAS snapshot must carry the declared cli.toml values");
    assert.equal(fileSha(yamlA), beforeA, "a summarize write changed workspace A's .polyforge.yaml");
    assert.equal(fileSha(yamlB), beforeB, "a summarize write changed workspace B's .polyforge.yaml");
  });
  await check("S290-7", async () => {
    resetState();
    resetCalls();
    const unset = await post("/api/polyforge/config", { action: "unset", key: "summarize.language", cwd: WS_BETA, expect_state: { file_sha256: CLI_SHA } });
    assert.equal(unset.status, 200, `the summarize unset answered ${unset.status}: ${unset.text.slice(0, 160)}`);
    const unsetCall = calls().filter((call) => call.args.includes("unset") && call.args.includes("summarize.language")).at(-1);
    assert.ok(unsetCall, "the unset did not spawn pf config unset summarize.language");
    assert.ok(!unsetCall.args.includes("--cwd"), "a summarize unset must not carry --cwd");
    const at = unsetCall.args.indexOf("--expect-state");
    assert.equal(JSON.parse(unsetCall.args[at + 1]).file_sha256, CLI_SHA, "the unset must CAS on the cli.toml hash");
    resetCalls();
    const oldEntry = { id: "pre-286", at: "2026-11-01T00:00:00.000Z", scope: "workspace", cwd: CWD, key: "summarize.interval_sec", old: 60, new: 30, secret: false, rollbackable: true };
    const rolled = await post("/api/polyforge/config/history", { id: oldEntry.id, key: oldEntry.key, old: oldEntry.old, entry: oldEntry, cwd: CWD });
    assert.equal(rolled.status, 200, `the pre-#286 rollback answered ${rolled.status}: ${rolled.text.slice(0, 160)}`);
    const setCall = calls().filter((call) => call.args.includes("set") && call.args.includes("summarize.interval_sec")).at(-1);
    assert.ok(setCall, "the rollback did not spawn pf config set summarize.interval_sec");
    assert.ok(setCall.args.includes("60"), "the rollback must write the old machine-level value");
    assert.ok(!setCall.args.includes("--cwd"), "a rollback must use the flat cli.toml path");
    const at2 = setCall.args.indexOf("--expect-state");
    assert.equal(JSON.parse(setCall.args[at2 + 1]).file_sha256, CLI_SHA, "a rollback must CAS on the cli.toml hash");
    const history = await read("/api/polyforge/config/history");
    const newest = (history.json?.entries ?? [])[0];
    assert.equal(newest?.key, "summarize.interval_sec", "the rollback must append a new history entry");
    assert.equal(newest?.old, 120, "a rollback's new entry must record the old cli.toml value");
    assert.equal(newest?.new, 60, "a rollback's new entry must record the restored value");
  });

  const hintCount = async (page) => ((await panelText(page)).match(/2\.20\.21/g) ?? []).length;

  await check("S290-8", async () => {
    resetState({ version: "2.20.20" });
    await openSettings(page, SESSION);
    assert.equal(await page.locator('input[aria-label^="summarize."]').count(), 0, "summarize rows stayed visible below the 2.20.21 floor");
    const all = await panelText(page);
    assert.ok(!all.includes("[summarize]"), "an empty summarize zone rendered below the floor");
    assert.ok(!all.includes("[memory_policy]"), "memory_policy blocks rendered below the floor");
    assert.equal(await hintCount(page), 1, "exactly one upgrade hint naming 2.20.21 must be visible");
    assert.match(all, /2\.20\.20/, "the hint must name the found version");
    assert.equal(await page.locator('input[aria-label="step_timeout"]').first().inputValue(), "30m0s", "the rest of the panel (cli.toml rows) must keep working");
    const workspace = await cardText(page, "Workspace");
    assert.ok(workspace.includes("ws-alpha"), "the rest of the panel (工作区 mapping) must keep working");
  });
  await check("S290-9", async () => {
    resetState({ version: "not-a-version" });
    resetCalls();
    await openSettings(page, SESSION);
    assert.equal(await page.locator('input[aria-label^="summarize."]').count(), 0, "an unparseable version must hide the summarize rows");
    const all = await panelText(page);
    assert.equal(await hintCount(page), 1, "an unparseable version must show exactly one 2.20.21 hint");
    assert.match(all, /unparseable|unknown|无法解析|未知/i, "the hint must say the version is unparseable");
    resetCalls();
    const write = await post("/api/polyforge/config", { action: "set", key: "summarize.interval_sec", value: 240, cwd: CWD, expect_state: { file_sha256: CLI_SHA } });
    assert.equal(write.status, 400, `a summarize write with an unparseable version must answer 400, got ${write.status}`);
    assert.match(write.text, /2\.20\.21/, "the refusal must name the required pf version");
    assert.match(write.text, /unparseable|unknown|无法解析|未知/i, "the refusal must name the unparseable version");
    assert.ok(!calls().some((call) => call.args.includes("set") && call.args.includes("summarize.")), "a refused summarize write must not spawn pf config set");
  });
  await check("S290-10", async () => {
    resetState({ version: "2.20.20" });
    const zh = await newPage("zh-CN");
    await openSettings(zh, SESSION);
    const all = await panelText(zh);
    assert.ok(all.includes("2.20.21"), "the zh-CN panel must carry the summarize floor hint");
    const around = all.slice(Math.max(0, all.indexOf("2.20.21") - 40), all.indexOf("2.20.21") + 80);
    assert.match(around, /[\u3400-\u9fff]/, "the zh-CN floor hint is English-only");
    assert.equal(await zh.locator('input[aria-label^="summarize."]').count(), 0, "the zh-CN panel must hide the summarize rows below the floor");
  });
} finally {
  await h.close();
  if (failures.length > 0) {
    console.log(`FAILED ${failures.length}/${check.count} scenario(s): ${failures.join("; ")}`);
    process.exitCode = 1;
  } else {
    console.log(`PASS ${check.count}/${check.count} scenarios`);
  }
}