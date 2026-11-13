// carries: components/PolyforgeConfig*, components/polyforge-config-helpers*, components/PolyforgeInstallCard*, lib/polyforge-install-pure*, app/api/polyforge/detect/*
// carries: components/SettingsPanel*, components/SettingsUi*, components/settings-ui-helpers*, lib/settings-navigation*, lib/package-settings*, app/api/plugins/*
// pi#122 (wi_Gvm8wRZcr88O) — Settings Polyforge section: end-to-end acceptance pass.
//
// Authored pre-code against artifacts/spec.md (AC1–AC12, AC14; AC13 is this file
// itself running both machine states under the stub-`pf`-on-PATH convention of
// e2e/polyforge*.mjs). Standalone, like e2e/polyforge.mjs: it starts its own dev
// server twice — phase A with no `pf` reachable on PATH, phase B with a stub
// `pf` first on PATH — and drives the new surfaces only by the HTTP route the
// plan pins (`GET /api/polyforge/detect`, `?force=1` bypassing the 60 s presence
// cache), its response contract (flat booleans/labels: pfPresent/pfVersion/
// pfVersionBelowMinimum/line/piSkillInstalled/piSkillEnabled/claudeSkillsSymlink/
// claudeSessionStartHook/daemon:{reachable,error?}|null), and user-visible
// roles/text (the "Polyforge" settings tab). Machine facts are fixtured through
// a throwaway HOME (the polyforge `release/current` clone at install.sh's
// conventional path, the global settings entry pointing at it, `~/.claude`
// symlink + SessionStart hook, a sentinel-bearing cli.toml). Each scenario is
// self-guarding: it logs PASS/FAIL, and the run exits 1 on any FAIL.
//
//   node e2e/polyforge-settings.mjs
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { createConnection } from "node:net";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, reportTitled, root, writeFileAtomic, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-settings-e2e-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-settings-session";
mkdirSync(project);
const ts = "2026-10-22T00:00:00.000Z";
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E polyforge settings host session", timestamp: ts });

// ---- machine facts (throwaway HOME) ----
// The polyforge `release/current` clone at install.sh's conventional path: a git
// repo whose package.json name is "polyforge" (the harness package), with a
// skills/polyforge skill dir. The global settings entry's source points here, so
// the line/pi-skill facts resolve through either location rule.
const clone = join(home, ".local", "share", "polyforge", "release", "current");
mkdirSync(join(clone, "skills", "polyforge"), { recursive: true });
writeFileSync(join(clone, "package.json"), `${JSON.stringify({ name: "polyforge", version: "0.0.0", pi: { skills: ["./skills/polyforge"] } }, null, 2)}\n`);
writeFileSync(join(clone, "skills", "polyforge", "SKILL.md"), "# polyforge e2e skill\n");
const gitSync = (...args) => {
  const result = spawnSync("git", ["-C", clone, ...args], { encoding: "utf8" });
  if (result.status !== 0) throw new Error(`git ${args.join(" ")}: ${result.stderr}`);
};
gitSync("init");
const setOrigin = (url) => {
  spawnSync("git", ["-C", clone, "remote", "remove", "origin"]);
  if (url) gitSync("remote", "add", "origin", url);
};
// Claude Code side: a resolvable ~/.claude/skills/polyforge symlink and a
// settings.json carrying a SessionStart hook referencing polyforge.
const claudeDir = join(home, ".claude");
const claudeSymlink = join(claudeDir, "skills", "polyforge");
const claudeSettings = join(claudeDir, "settings.json");
mkdirSync(join(claudeDir, "skills"), { recursive: true });
mkdirSync(join(work, "claude-skill-target"));
writeFileSync(join(work, "claude-skill-target", "SKILL.md"), "# claude skill target\n");
const setClaude = ({ symlink = true, hook = true } = {}) => {
  rmSync(claudeSymlink, { force: true });
  if (symlink === "broken") symlinkSync(join(work, "gone-target"), claudeSymlink);
  else if (symlink) symlinkSync(join(work, "claude-skill-target", "SKILL.md"), claudeSymlink);
  if (hook) writeFileSync(claudeSettings, `${JSON.stringify({
    hooks: { SessionStart: [{ hooks: [{ type: "command", command: "polyforge hook session-start --token E2E-SECRET-HOOK-122" }] }] },
  }, null, 2)}\n`); else rmSync(claudeSettings, { force: true });
};
setClaude();
// A sentinel-bearing cli.toml the detection never reads (and never echoes).
const cliToml = join(home, ".config", "polyforge", "cli.toml");
mkdirSync(join(home, ".config", "polyforge"), { recursive: true });
const CLI_SECRETS = ["E2E-SECRET-API-KEY-122", "https://e2e-secret-server.example", "e2e-secret-user-122"];
writeFileSync(cliToml, `api_key = "${CLI_SECRETS[0]}"\nserver = "${CLI_SECRETS[1]}"\nuser_id = "${CLI_SECRETS[2]}"\n`);
// Global (never project) settings: the polyforge package entry. Project scope:
// the fixture project's own entry — never consulted.
const globalSettings = join(agentDir, "settings.json");
const setGlobalPackages = (packages) => {
  if (packages === null) rmSync(globalSettings, { force: true });
  else writeFileSync(globalSettings, `${JSON.stringify({ packages }, null, 2)}\n`);
};
mkdirSync(join(project, ".pi"));
writeFileSync(join(project, ".pi", "settings.json"), `${JSON.stringify({ packages: [{ source: clone, skills: ["./skills/polyforge"] }] }, null, 2)}\n`);

// ---- stub pf (state.json + argv.log next to it; absolute node shebang) ----
const STALE_KEY = "e2e-stale-key-SECRETVALUE-122";
const STDERR_PREFIX = "pf: daemon not reachable (connection refused)";
const STDERR_TAIL = "E2E-STDERR-TAIL-SENTINEL-122";
const STUB_STDERR = `${STDERR_PREFIX} ${" ".repeat(600)}${STDERR_TAIL}`;
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const setStub = (patch) => writeFileAtomic(stateFile, JSON.stringify({
  mode: "ok", version: "v2.19.1",
  ops: { machine_id: "m-e2e", env_names: ["E2E_ENV_NAME_SENTINEL_122"], workspaces: ["/srv/e2e-secret-workspace-122"], queue: { pending: 0, running: 1, max_concurrent: 4 } },
  ...patch,
}));
const argvCalls = () => (existsSync(argvLog) ? readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)) : []);
setStub({});
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename)); const args = process.argv.slice(2);
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
if (args.includes("--version")) { process.stdout.write(String(s.version) + "\\n"); process.exit(0); }
if (s.mode === "down") { process.stderr.write(${JSON.stringify(STUB_STDERR)} + "\\n"); process.exit(1); }
const rest = args.filter((a) => a !== "--json");
if (rest[0] === "ops" && rest[1] === "status") { process.stdout.write(JSON.stringify(s.ops) + "\\n"); process.exit(0); }
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
// Phase A PATH: every PATH dir holding a pf is replaced by a symlink farm without it.
const absentPath = (process.env.PATH || "").split(delimiter).map((dir, index) => {
  if (!existsSync(join(dir, "pf"))) return dir;
  const farm = join(work, `nopf-${index}`);
  mkdirSync(farm);
  for (const name of readdirSync(dir)) if (name !== "pf") symlinkSync(join(dir, name), join(farm, name));
  return farm;
}).join(delimiter);
const resetFixtures = () => {
  setStub({});
  setOrigin("git@github.com:example-org/public-forge.git");
  setClaude();
  setGlobalPackages([{ source: clone, skills: ["./skills/polyforge"] }]);
};
resetFixtures();
const SECRETS = [STALE_KEY, ...CLI_SECRETS, STDERR_TAIL, "E2E-SECRET-HOOK-122", "E2E_ENV_NAME_SENTINEL_122", "/srv/e2e-secret-workspace-122", home];

// ---- scenario bookkeeping ----
const T = {
  S1: "S1: Settings lists a Polyforge section with no project selected, and a pf-less machine renders 未安装 with the copyable install command",
  S2: "S2: The detect endpoint answers 200 decodable JSON in every absent/degraded state; the only non-2xx is the gate's 403 JSON",
  S3: "S3: One detection implementation — the detect surface reuses the dock's presence probe",
  S4: "S4: Version reported with a non-blocking 版本过低 hint below v2.18.1",
  S5: "S5: Line 公开线/未知 by the clone's git origin (内部线 covered by the pure digest unit test)",
  S6: "S6: pi skill state read from the global settings entry only (project scope ignored)",
  S7: "S7: Claude Code side shows two separate read-only items, each with its own absent state",
  S8: "S8: Daemon 可达/不可达 with pf stderr capped at 512 bytes, 不适用 without pf, via pf --json ops status",
  S9: "S9: The detect response discloses no credentials, home paths or env values",
  S10: "S10: The pi package switch writes only through /api/plugins and auto re-detects",
  S11: "S11: Detection runs once per open, never polls, and 重新检测 bypasses the 60 s presence cache",
  S12: "S12: Docs change with the code (polyforge-panel.md and settings-ui.md describe the section)",
};
const failures = new Map(Object.values(T).map((title) => [title, []]));
const ran = new Set();
const check = createCheck({ failures: ([title, label], text) => failures.get(title).push(`${label}: ${text}`), before: ([title]) => ran.add(title), pass: null, fail: null });
const checkPage = (title, label, opts, fn) => check(title, label, () => h.withPage(opts, fn));

let base; const serverLogs = [];
// The first page load and first Settings open after each dev-server start pay
// the cold compile; later ones are warm. Other suites keep that warm-up load
// out of their scenarios the same way (polyforge-panels-123, memory-panel).
let coldSettings = true;
async function get(path) {
  const response = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(30_000) });
  const text = await response.text();
  let json = null; try { json = JSON.parse(text); } catch {}
  return { status: response.status, text, json };
}
const detect = (force = true) => get(`/api/polyforge/detect${force ? "?force=1" : ""}`);
// A raw HTTP request with a chosen Host header (fetch refuses to set it): the
// DNS-rebound shape the in-route isApiRequestAllowed gate must refuse.
function rawRequest(path, host) {
  return new Promise((resolve, reject) => {
    const socket = createConnection({ port: Number(new URL(base).port), host: "127.0.0.1" }, () => {
      socket.write(`GET ${path} HTTP/1.1\r\nHost: ${host}\r\nAccept: */*\r\nConnection: close\r\n\r\n`);
    });
    const chunks = [];
    socket.on("data", (chunk) => chunks.push(chunk));
    socket.on("error", reject);
    socket.on("close", () => {
      const raw = Buffer.concat(chunks).toString("utf8");
      const status = Number(/^HTTP\/[\d.]+ (\d{3})/.exec(raw)?.[1] ?? 0);
      let body = raw.slice(raw.indexOf("\r\n\r\n") + 4);
      if (/transfer-encoding:\s*chunked/i.test(raw)) body = body.split("\r\n").filter((_, index) => index % 2 === 1).join("");
      let json = null; try { json = JSON.parse(body); } catch {}
      resolve({ status, text: body, json });
    });
  });
}
const apiResponses = [];
const detectHits = [];
const h = createHarness({
  permissions: ["clipboard-read", "clipboard-write"],
  // The first page load after a fresh dev-server start compiles the route cold,
  // so it gets COLD_MS (coldFirst; the warm-up ceiling other suites use); later
  // loads use NAV_MS. The first Settings open once exceeded the 30 s action
  // default under a loaded 32-core host ( verification), so that
  // click and its dialog wait share the same load-bound ceiling. Action ceiling
  // only; assertions and their polling unchanged (same reasoning as
  // e2e/run.mjs's settle poll).
  timeout: 30_000,
  coldFirst: true,
  errors: ["pageerror", "console", "5xx"],
  setup(page) {
    page.on("response", (response) => {
      if (response.url().startsWith(base) && new URL(response.url()).pathname.startsWith("/api/polyforge/")) {
        apiResponses.push(`${response.status()} ${new URL(response.url()).pathname}`);
      }
    });
    page.on("request", (request) => {
      if (new URL(request.url()).pathname === "/api/polyforge/detect") {
        detectHits.push({ at: Date.now(), url: request.url(), method: request.method(), body: request.postData() });
      }
    });
  },
  path: ({ session = true }) => (session ? `/?session=${SESSION}` : "/"),
  ready: (page, { session = true }, timeout) => (session
    ? page.getByText("E2E polyforge settings host session")
    : page.getByRole("button", { name: "Settings", exact: true })).first().waitFor({ state: "attached", timeout }),
  server(name, path) {
    const logFile = join(work, `server-${name}.log`); serverLogs.push(logFile);
    return { name, logFile, env: { PATH: path, HOME: home, PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: STALE_KEY } };
  },
  started: (started) => { base = started.base; coldSettings = true; },
});
const { errors, startServer } = h;
const openSettings = async (page) => {
  const show = page.getByRole("button", { name: "Show sidebar", exact: true });
  if (await show.isVisible().catch(() => false)) await show.click();
  // The first open after a cold page load compiles/renders the Settings dialog,
  // so the click and the wait for the dialog use COLD_MS; later opens use the
  // warm NAV_MS first-text ceiling (e2e/timeouts.mjs), never the 30 s default.
  // The button is server-rendered, so it is `attached` before React hydrates it:
  // a click in that gap is dropped and nothing retries, which burned the whole
  // COLD_MS under load. Wait for hydration (the node carries
  // React's own props) before the one click.
  const timeout = coldSettings ? COLD_MS : NAV_MS;
  coldSettings = false;
  await page.waitForFunction(() => {
    const button = [...document.querySelectorAll("button")]
      .find((candidate) => candidate.getAttribute("aria-label") === "Settings" || candidate.textContent.trim() === "Settings");
    return !!button && Object.keys(button).some((key) => key.startsWith("__reactFiber$") || key.startsWith("__reactProps$"));
  }, null, { timeout });
  await page.getByRole("button", { name: "Settings", exact: true }).first().click({ timeout });
  const dialog = page.getByRole("dialog", { name: "Settings", exact: true });
  await dialog.waitFor({ timeout });
  return dialog;
};
const pfTab = (dialog) => dialog.getByRole("button", { name: "Polyforge", exact: true });
const clipboard = (page) => page.evaluate(() => navigator.clipboard?.readText().catch(() => "") ?? "");
// The control that copies `needle`: click the section's buttons (main only —
// the tabs and close button live in the header) until one puts it on the
// clipboard. Wording-independent.
// the page is master-detail — the list's entries switch the
// detail, so the copy control is sought among the shown detail's buttons (the
// page opens on the Host pane, the install command's home).
async function copiesNeedle(page, dialog, needle) {
  for (const button of await dialog.locator("main .config-detail").getByRole("button").all()) {
    await button.click({ timeout: 2000 }).catch(() => {});
    if (String(await clipboard(page)).includes(needle)) return true;
  }
  return false;
}

try {
  await h.launchBrowser(() => chromium.launch());
  // ================= Phase A: no pf on PATH =================
  await startServer("absent", absentPath);
  setGlobalPackages(null);
  await check(T.S2, "pf absent: detect 200 JSON, dependent fields absent", async () => {
    const response = await detect();
    assert.equal(response.status, 200, `detect answered ${response.status}`);
    assert.ok(response.json, "detect body is not decodable JSON");
    assert.equal(response.json.pfPresent, false);
    assert.equal(response.json.pfVersion, null, "pfVersion must be absent without pf");
    assert.equal(response.json.daemon, null, "daemon must be 不适用 (null) without pf, no probe attempted");
  });
  await check(T.S2, "gate refusal 403 JSON", async () => {
    const response = await rawRequest("/api/polyforge/detect", "e2e-evil.example.com");
    assert.equal(response.status, 403, `a refused request answered ${response.status}`);
    assert.ok(response.json?.error, "the 403 needs a decodable JSON error body");
  });
  await check(T.S9, "pf absent: surface audit", async () => {
    const response = await detect();
    assert.equal(response.status, 200);
    for (const secret of SECRETS) assert.ok(!response.text.includes(secret), `detect leaks ${secret}`);
  });
  // S1 — always-visible section, 未安装 state, copyable install command. No
  // session selected: the settings dialog opens with no project (cwd null).
  await checkPage(T.S1, "pf absent: 未安装 section", { session: false }, async (page) => {
    const dialog = await openSettings(page);
    const tab = pfTab(dialog);
    await tab.waitFor({ timeout: COLD_MS });
    assert.equal(await tab.isDisabled(), false, "the Polyforge section is project-gated (must open with no project)");
    await tab.click();
    await page.waitForRequest((request) => new URL(request.url()).pathname === "/api/polyforge/detect", { timeout: 15_000 }).catch(() => {});
    const text = await dialog.innerText();
    assert.ok(text.includes("tools/install.sh"), "the 未安装 state does not show the install.sh install command");
    assert.ok(text.includes("raw.githubusercontent.com"), "the install command text is missing its source URL");
    assert.ok(await copiesNeedle(page, dialog, "tools/install.sh"), "no control copies the install command");
    await delay(1500);
    assert.deepEqual(errors.splice(0), [], "console errors on the pf-less path");
    assert.ok(apiResponses.every((entry) => /^[23]/.test(entry)), `non-2xx polyforge request on the pf-less path: ${apiResponses.join(", ")}`);
  });
  await check(T.S1, "pf absent: error signals", async () => assert.deepEqual(errors.splice(0), []));
  await h.stop();

  // ================= Phase B: stub pf first on PATH =================
  await startServer("stub", `${stubDir}${delimiter}${process.env.PATH}`);
  resetFixtures();
  await check(T.S2, "present state: 200 JSON", async () => {
    const response = await detect();
    assert.equal(response.status, 200, `detect answered ${response.status}`);
    assert.ok(response.json, "detect body is not decodable JSON");
    assert.equal(response.json.pfPresent, true);
    assert.equal(response.json.pfVersion, "v2.19.1");
    assert.equal(response.json.pfVersionBelowMinimum, false, "v2.19.1 must not carry a version warning");
    assert.equal(response.json.daemon?.reachable, true);
  });
  await check(T.S4, "below-minimum hint is non-blocking", async () => {
    setStub({ version: "v2.17.0-e2e" });
    const response = await detect();
    assert.equal(response.json?.pfVersion, "v2.17.0-e2e");
    assert.equal(response.json?.pfVersionBelowMinimum, true, "below v2.18.1 must carry the 版本过低 hint");
    assert.equal(response.json?.pfPresent, true);
    assert.equal(response.json?.line, "public", "the hint must not block the line detection");
    assert.equal(response.json?.piSkillInstalled, true, "the hint must not block the pi-skill detection");
    assert.equal(response.json?.piSkillEnabled, true);
    assert.equal(response.json?.claudeSkillsSymlink, true, "the hint must not block the Claude Code detection");
    assert.equal(response.json?.claudeSessionStartHook, true);
    assert.equal(response.json?.daemon?.reachable, true, "the hint must not block the daemon detection");
    setStub({});
  });
  await check(T.S5, "line by origin", async () => {
    setOrigin("git@github.com:example-org/public-forge.git");
    assert.equal((await detect()).json?.line, "public", "a neutral public origin must report 公开线");
    setOrigin("https://github.com/silgrid/some-other-repo.git");
    assert.equal((await detect()).json?.line, "public", "any other origin must report 公开线");
    setOrigin(null);
    assert.equal((await detect()).json?.line, "unknown", "no origin must report 未知, never a guess");
    setOrigin("git@github.com:example-org/public-forge.git");
  });
  await check(T.S6, "pi skill from the global entry only", async () => {
    setGlobalPackages([{ source: clone, skills: ["./skills/polyforge"] }]);
    let response = await detect();
    assert.equal(response.json?.piSkillInstalled, true);
    assert.equal(response.json?.piSkillEnabled, true);
    setGlobalPackages([{ source: clone, extensions: [], skills: [], prompts: [], themes: [] }]);
    response = await detect();
    assert.equal(response.json?.piSkillInstalled, true);
    assert.equal(response.json?.piSkillEnabled, false, "empty resource arrays must report 未启用");
    setGlobalPackages(null); // the project's own entry (fixture above) must be ignored
    response = await detect();
    assert.equal(response.json?.piSkillInstalled, false, "a project-scope entry must be ignored");
    setGlobalPackages([{ source: join(work, "no-such-package"), skills: [] }]);
    response = await detect();
    assert.equal(response.json?.piSkillInstalled, false, "a missing target must report 未安装");
    setGlobalPackages([{ source: clone, skills: ["./skills/polyforge"] }]);
  });
  await check(T.S7, "claude items separate and read-only", async () => {
    let response = await detect();
    assert.equal(response.json?.claudeSkillsSymlink, true);
    assert.equal(response.json?.claudeSessionStartHook, true);
    setClaude({ symlink: false, hook: true });
    response = await detect();
    assert.equal(response.json?.claudeSkillsSymlink, false, "the symlink item must report its own absent state");
    assert.equal(response.json?.claudeSessionStartHook, true, "the hook item must not follow the symlink");
    setClaude({ symlink: "broken", hook: false });
    response = await detect();
    assert.equal(response.json?.claudeSkillsSymlink, false, "an unresolvable symlink must report absent");
    assert.equal(response.json?.claudeSessionStartHook, false);
    setClaude();
  });
  await check(T.S8, "daemon states", async () => {
    let response = await detect();
    assert.equal(response.json?.daemon?.reachable, true, "the running daemon must report 可达");
    setStub({ mode: "down" });
    response = await detect();
    assert.equal(response.json?.daemon?.reachable, false);
    const error = String(response.json?.daemon?.error ?? "");
    assert.ok(error.includes(STDERR_PREFIX), "pf's own stderr must surface on 不可达");
    assert.ok(error.length <= 512, `pf stderr exceeds the 512-byte cap (${error.length})`);
    assert.ok(!response.text.includes(STDERR_TAIL), "stderr beyond the cap leaked");
    setStub({});
    assert.ok(argvCalls().some((call) => call.includes("--json") && call.includes("ops") && call.includes("status")),
      "daemon reachability must be probed via `pf --json ops status`");
  });
  await check(T.S9, "present-state surface audit", async () => {
    for (const response of [await detect(), await get("/api/polyforge/detect")]) {
      assert.equal(response.status, 200);
      for (const secret of SECRETS) assert.ok(!response.text.includes(secret), `detect leaks ${secret}`);
    }
    assert.equal((await get("/api/polyforge/detect")).json?.pfPresent, true, "non-force detect must answer too");
  });
  // S10 — the pi package switch: exactly one switch (the Claude rows are
  // read-only), one POST through the existing /api/plugins global write path,
  // an automatic re-detect, and no other write anywhere.
  await checkPage(T.S10, "the pi package switch", {}, async (page) => {
    resetFixtures();
    detectHits.length = 0;
    const cliBefore = readFileSync(cliToml, "utf8");
    const claudeBefore = readFileSync(claudeSettings, "utf8");
    const dialog = await openSettings(page);
    await pfTab(dialog).click();
    await page.waitForRequest((request) => new URL(request.url()).pathname === "/api/polyforge/detect", { timeout: 15_000 }).catch(() => {});
    const switches = dialog.locator("main").getByRole("switch");
    assert.equal(await switches.count(), 1, "the section must offer exactly one switch (the pi package switch; the Claude rows are read-only)");
    // The switch renders as soon as detection reports the package, but it stays
    // disabled until the section's package read (/api/plugins) answers. That
    // first read is a cold route compile, which a loaded host can push past the
    // 30 s action default; the one click then times out ( full-1
    // S10). Wait for the enabled switch under the suite's warm-open ceiling,
    // before the write audit starts, so background session-lease writes during
    // the wait are not counted against the toggle.
    for (const deadline = Date.now() + NAV_MS; Date.now() < deadline && !(await switches.first().isEnabled().catch(() => false));) await delay(100);
    const before = detectHits.length;
    const posts = [];
    const otherWrites = [];
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.origin !== base || request.method() === "GET") return;
      const entry = { path: url.pathname, body: request.postData() ?? "" };
      (url.pathname === "/api/plugins" ? posts : otherWrites).push(entry);
    });
    await switches.first().click();
    await delay(4000);
    assert.equal(posts.length, 1, `the switch issued ${posts.length} POSTs (want exactly one /api/plugins write)`);
    assert.equal(posts[0]?.path, "/api/plugins");
    const body = JSON.parse(posts[0]?.body ?? "{}");
    assert.equal(body.action, "disable");
    assert.equal(body.scope, "global");
    assert.equal(body.source, clone);
    assert.equal(body.cwd, project);
    assert.deepEqual(otherWrites, [], "a write path other than /api/plugins was used");
    assert.ok(detectHits.length > before, "detection did not re-run automatically after the toggle");
    assert.equal(readFileSync(cliToml, "utf8"), cliBefore, "cli.toml was written");
    assert.equal(readFileSync(claudeSettings, "utf8"), claudeBefore, "Claude Code config was written");
  });
  // S11 — cadence: one detect per open, no polling, and the manual re-detect
  // control bypasses the 60 s presence cache (the stub version flips within the
  // cache window and the section must show the new value). Fixture: no global
  // entry, so the section renders no toggle (nothing else to flip).
  await checkPage(T.S11, "cadence", {}, async (page) => {
    resetFixtures();
    setGlobalPackages(null);
    detectHits.length = 0;
    const dialog = await openSettings(page);
    await pfTab(dialog).click();
    await page.waitForRequest((request) => new URL(request.url()).pathname === "/api/polyforge/detect", { timeout: 15_000 }).catch(() => {});
    await delay(1500);
    assert.equal(detectHits.length, 1, `the section issued ${detectHits.length} detect requests on open (want exactly one)`);
    await delay(6000);
    assert.equal(detectHits.length, 1, `the section polled: ${detectHits.length} requests in 7s`);
    await dialog.getByRole("button", { name: "General", exact: true }).click();
    await pfTab(dialog).click();
    await page.waitForRequest((request) => new URL(request.url()).pathname === "/api/polyforge/detect", { timeout: 15_000 }).catch(() => {});
    await delay(1500);
    assert.equal(detectHits.length, 2, "re-opening the section did not re-detect");
    setStub({ version: "v2.20.0-e2e" });
    let reDetected = false;
    for (const button of await dialog.locator("main").getByRole("button").all()) {
      const before = detectHits.length;
      await button.click({ timeout: 2000 }).catch(() => {});
      await delay(700);
      if (detectHits.length > before && (await dialog.innerText()).includes("2.20.0")) { reDetected = true; break; }
    }
    assert.ok(reDetected, "no control re-runs detection past the 60 s presence cache");
    setStub({});
  });
  // S3 — one detection implementation: the detect route delegates to the
  // shared probe, and no second `pf --version` spawn site appears anywhere.
  await check(T.S3, "single implementation", async () => {
    const route = join(root, "app", "api", "polyforge", "detect", "route.ts");
    assert.ok(existsSync(route), "app/api/polyforge/detect/route.ts does not exist");
    assert.match(readFileSync(route, "utf8"), /polyforge-status|probePolyforge/, "the detect route does not reuse the shared presence probe");
    const walk = (dir) => readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
      const path = join(dir, entry.name);
      return entry.isDirectory() ? walk(path) : [path];
    });
    for (const dir of ["app", "lib", "components", "hooks"]) {
      for (const file of walk(join(root, dir))) {
        if (!/\.(ts|tsx)$/.test(file) || file === join(root, "lib", "polyforge-status.ts")) continue;
        assert.ok(!readFileSync(file, "utf8").includes('"--version"'), `${file} spawns pf --version itself (one detection implementation)`);
      }
    }
  });
  // S12 — docs change with the code.
  await check(T.S12, "docs", async () => {
    const panel = readFileSync(join(root, "docs", "agents", "polyforge-panel.md"), "utf8");
    assert.match(panel, /detect/i, "polyforge-panel.md does not describe the detect route");
    assert.match(panel, /settings/i, "polyforge-panel.md does not mention the settings section");
    const settings = readFileSync(join(root, "docs", "agents", "settings-ui.md"), "utf8");
    assert.match(settings, /[Pp]olyforge/, "settings-ui.md does not describe the Polyforge section");
  });
  await check(T.S2, "no 5xx or console errors", async () => assert.deepEqual(errors.splice(0), []));
  await h.stop();
  await check(T.S9, "no env value in server logs", async () => {
    for (const file of serverLogs) assert.ok(!readFileSync(file, "utf8").includes(STALE_KEY), `${file} contains an env value`);
  });
} catch (error) {
  console.error(error);
  process.exitCode = 1;
} finally {
  await h.close().catch(() => {});
  reportTitled(failures, ran);
  if (process.exitCode) console.log(`Artifacts kept: ${work}`); else rmSync(work, { recursive: true, force: true });
}