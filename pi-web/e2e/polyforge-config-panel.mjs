// carries: lib/polyforge-config*, lib/polyforge-detect*, lib/polyforge-pf-bin*, app/api/polyforge/config/*, app/api/polyforge/detect/*
// carries: components/SettingsPanel*, components/SettingsUi*, components/settings-ui-helpers*, lib/settings-navigation*, lib/package-settings*, app/api/plugins/*, app/api/polyforge/polyforge-config-routes*
// — the Settings › Polyforge CONFIGURATION panel, end-to-end.
//
// Authored pre-code against artifacts/spec.md (the capsule's
// artifacts/e2e-scenarios.md mirrors its scenario blocks): every scenario here
// drives the new surface only through the app's real HTTP face (the routes the
// panel calls) and the machine facts around it (the cli.toml, the workspace
// .polyforge.yaml, the agent dir, the server log, and the argv/stdin the server
// hands to `pf`). `pf` itself is a stub first on PATH, so the suite can prove
// the panel writes nothing itself and never echoes a secret. No browser is
// needed and no 5xx is tolerated.
//
// Baseline posture: the config routes do not exist yet, so every scenario is
// an assertion-level red (404/405, a missing class/schema/daemon fact, a write
// that does not reach pf) — never a build or fixture error.
//
// summarize and memory_policy are cli.toml facts — the stub's
// `config list` carries summarize.* and the per-path memory_policy, its
// `config workspace` neither; the workspace yamls carry step chains only.
//
//   node e2e/polyforge-config-panel.mjs
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { chmodSync, createWriteStream, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { createConnection } from "node:net";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { createCheck, rawMessage, writeFileAtomic } from "./harness.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
assert.ok(!existsSync(join(root, ".next/dev/lock")), "Run in a checkout without an active dev server");
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-config-e2e-"));
const home = join(work, "home");
const agentDir = join(home, ".pi", "agent");
const wsA = join(work, "ws-a");
const wsB = join(work, "ws-b");
const unmapped = join(work, "not-a-workspace");
for (const dir of [agentDir, wsA, wsB, unmapped, join(home, ".config", "polyforge")]) mkdirSync(dir, { recursive: true });
const cliToml = join(home, ".config", "polyforge", "cli.toml");
const yamlA = join(wsA, ".polyforge.yaml");
const yamlB = join(wsB, ".polyforge.yaml");
const API_SENTINEL = "E2E-W22-API-KEY-SENTINEL";
const sha = (path) => createHash("sha256").update(readFileSync(path)).digest("hex");
const writeCli = (max) => writeFileSync(cliToml, `server = "http://127.0.0.1:1"\napi_key = "e2e-current-key"\nuser_id = "usr_root"\n\n[daemon]\nmax_concurrent_jobs = ${max}\n\n[workspace."${wsA}"]\nprojects = ["e2e"]\n\n[workspace."${wsB}"]\nprojects = ["e2e"]\n`);
writeCli(6);
writeFileSync(yamlA, `steps:\n  e2e:\n    fix: { models: ["pi:zeta", "pi:alpha"] }\n`);
writeFileSync(yamlB, `steps:\n  e2e:\n    fix: { models: ["pi:alpha"] }\n`);
const BASELINE_CLI = sha(cliToml);
const BASELINE_A = sha(yamlA);
const BASELINE_B = sha(yamlB);

// ---- stub pf (state.json + list.json + argv.log in its own dir) ----
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const listFile = join(stubDir, "list.json");
const argvLog = join(stubDir, "argv.log");
let stubState = { mode: "ok", version: "v2.20.21", listShape: "new", daemon: "up", afterReload: false, fileMax: 6, daemonMax: 6, maxConfigured: 6, pending: [] };
const listDocument = () => ({
  file: { path: cliToml, sha256: sha(cliToml), exists: true },
  items: [
    { key: "server", value: "http://127.0.0.1:1", source: "file", class: "restart-only" },
    { key: "api_key", set: true, source: "file", class: "restart-only" },
    { key: "daemon_socket", value: "/run/pf.sock", source: "file", class: "restart-only" },
    { key: "harness_root", value: "/opt/pf", source: "file", class: "restart-only" },
    { key: "operator_agent_dir", value: "/home/op/.pi", source: "file", class: "restart-only" },
    { key: "user_id", value: "usr_root", source: "file", class: "cli-only" },
    { key: "claude_bin", value: "/usr/local/bin/claude", source: "file", class: "live" },
    { key: "step_timeout", value: "30m0s", source: "file", class: "live", schema: { kind: "duration" } },
    { key: "stall_window", value: "10m0s", source: "file", class: "live", schema: { kind: "duration" } },
    { key: "max_concurrent_jobs", value: stubState.fileMax, source: "file", class: "live", schema: { kind: "int", min: 1 } },
    // summarize.* is a cli.toml table read here only.
    { key: "summarize.models", value: ["pi:zeta", "pi:alpha"], source: "file", class: "live", schema: { kind: "model-list" } },
    { key: "summarize.language", value: "English", source: "file", class: "live", schema: { kind: "string" } },
    { key: "summarize.interval_sec", value: 30, source: "file", class: "live", schema: { kind: "int", min: 10, max: 3600 } },
    { key: "future_knob", value: "x", source: "file", class: "brand-new-class" },
  ],
  memory_policy: {
    [wsA]: { default: { record: ["policy-a"], skip: [], declared: true }, e2e: { record: [], skip: [], declared: false } },
    [wsB]: { default: { record: ["policy-b"], skip: [], declared: true }, e2e: { record: [], skip: [], declared: false } },
  },
});
const setStub = (patch = {}) => {
  stubState = { ...stubState, ...patch };
  writeFileAtomic(stateFile, JSON.stringify(stubState));
  writeFileAtomic(listFile, JSON.stringify(listDocument()));
};
setStub();
writeFileSync(argvLog, "");
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path"); const crypto = require("crypto");
// A whole-file replace: a sibling spawn reads this file at its start (see the test side).
const saveFile = (p, data) => { fs.writeFileSync(p + "." + process.pid + ".tmp", data); fs.renameSync(p + "." + process.pid + ".tmp", p); };
const dir = path.dirname(fs.realpathSync(__filename));
const state = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const args = process.argv.slice(2);
let stdin = ""; try { stdin = fs.readFileSync(0, "utf8"); } catch {}
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify({ args, stdin }) + "\\n");
if (stdin) fs.writeFileSync(path.join(dir, "stdin.txt"), stdin);
const rest = args.filter((a) => a !== "--json");
const sha = (p) => crypto.createHash("sha256").update(fs.readFileSync(p)).digest("hex");
const WS = ${JSON.stringify({ wsA, wsB, yamlA, yamlB })};
if (args.includes("--version")) { process.stdout.write(state.version + "\\n"); process.exit(0); }
if (rest[0] === "config" && rest[1] === "list") {
  if (state.listShape === "old") { process.stdout.write(JSON.stringify({ items: [{ key: "server", value: "http://127.0.0.1:1", source: "file" }] }) + "\\n"); process.exit(0); }
  process.stdout.write(fs.readFileSync(path.join(dir, "list.json"), "utf8") + "\\n"); process.exit(0);
}
if (rest[0] === "config" && rest[1] === "workspace") {
  const at = rest.indexOf("--cwd"); const cwd = at >= 0 ? rest[at + 1] : "";
  if (cwd !== WS.wsA && cwd !== WS.wsB) { process.stdout.write(JSON.stringify({ mapped: false, workspace: null, yaml: null, steps: null }) + "\\n"); process.exit(0); }
  const yaml = cwd === WS.wsA ? WS.yamlA : WS.yamlB;
  process.stdout.write(JSON.stringify({
    mapped: true, workspace: cwd, yaml: { path: yaml, sha256: sha(yaml), exists: true },
    steps: { fix: { declared: true, models: ["pi:zeta", "pi:alpha"] }, code: { declared: false, models: null } },
    steps_complete: true,
  }) + "\\n"); process.exit(0);
}
if (rest[0] === "ops" && rest[1] === "status") {
  if (state.daemon === "down") { process.stderr.write("pf: daemon not reachable: dial unix /run/pf.sock: connect: connection refused\\n"); process.exit(1); }
  process.stdout.write(JSON.stringify({
    queue: { pending: 0, running: 0, max_concurrent: state.afterReload ? 7 : state.daemonMax, max_configured: state.afterReload ? 7 : state.maxConfigured },
    supervision: { step_timeout: "30m0s", stall_window: "10m0s" },
    reload: { pending: state.afterReload ? [] : state.pending, overridden: [] },
  }) + "\\n"); process.exit(0);
}
if (rest[0] === "config" && rest[1] === "reload") {
  state.afterReload = true; state.fileMax = 7; saveFile(path.join(dir, "state.json"), JSON.stringify(state));
  process.stdout.write(JSON.stringify({ reloaded: ["max_concurrent_jobs"], pending: [] }) + "\\n"); process.exit(0);
}
if (rest[0] === "config" && (rest[1] === "set" || rest[1] === "unset")) {
  if (state.mode === "invalid") { process.stderr.write("pf: config set max_concurrent_jobs: max_concurrent_jobs must be >= 1 (got 0)\\n"); process.exit(1); }
  if (state.mode === "stale") {
    process.stderr.write(JSON.stringify({ error: { code: "CORE-409", message: "config file changed since it was read", extra: { diff: [{ key: "max_concurrent_jobs", expected: "5", current: "6" }, { key: "api_key", changed: true }] } } }) + "\\n");
    process.exit(4);
  }
  // A write advances the stub's own state (state.json + list.json), so a
  // correct implementation that records the real old value from a fresh read
  // sees the value the previous write left behind (owner-approved fixture fix).
  if (rest[1] === "set" && rest[2] === "max_concurrent_jobs") {
    state.fileMax = Number(rest[3]);
    saveFile(path.join(dir, "state.json"), JSON.stringify(state));
    const doc = JSON.parse(fs.readFileSync(path.join(dir, "list.json"), "utf8"));
    for (const item of doc.items) { if (item.key === "max_concurrent_jobs") item.value = state.fileMax; }
    saveFile(path.join(dir, "list.json"), JSON.stringify(doc));
  }
  process.stdout.write(JSON.stringify({ key: rest[2] ?? "max_concurrent_jobs", class: "live", outcome: "applied" }) + "\\n"); process.exit(0);
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
const argvCalls = () => (existsSync(argvLog) ? readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)) : []);
const resetCalls = () => writeFileSync(argvLog, "");
const callsWith = (needle) => argvCalls().filter((call) => call.args.includes(needle));

// ---- decoded-body helpers (tolerant: the spec pins facts, not nesting) ----
const walkJSON = (value, visit) => {
  if (Array.isArray(value)) { visit(value); value.forEach((child) => walkJSON(child, visit)); return; }
  if (value && typeof value === "object") { visit(value); Object.values(value).forEach((child) => walkJSON(child, visit)); }
};
const findArray = (body, predicate) => {
  let best = null;
  walkJSON(body, (node) => {
    if (Array.isArray(node) && node.length && node.every(predicate) && (!best || node.length > best.length)) best = node;
  });
  return best;
};
const findItems = (body) => findArray(body, (e) => e && typeof e === "object" && typeof e.key === "string") ?? [];
const findEntries = (body) => findArray(body, (e) => e && typeof e === "object" && typeof e.key === "string" && ("new" in e || "old" in e)) ?? [];
const findScalar = (body, nameRe) => {
  let found;
  walkJSON(body, (node) => {
    if (found !== undefined || Array.isArray(node) || !node || typeof node !== "object") return;
    for (const [key, value] of Object.entries(node)) {
      if (nameRe.test(key) && (typeof value === "string" || typeof value === "number" || typeof value === "boolean")) { found = value; return; }
    }
  });
  return found;
};

// ---- the dev server, on the stub pf's PATH ----
// Deliberately not the shared harness/next-server start: this serial-lane
// suite has no browser and always runs its own `next dev`, in either
// E2E_SERVER_MODE (e2e/README.md, "Build-once mode").
const serverLog = createWriteStream(join(work, "server.log"));
let server; let base;
const startDevServer = async () => {
  const probe = createConnection({ port: 0, host: "127.0.0.1" });
  probe.destroy();
  const port = 31000 + Math.floor(Math.random() * 400);
  base = `http://127.0.0.1:${port}`;
  server = spawn(process.execPath, [join(root, "node_modules/next/dist/bin/next"), "dev", "-H", "127.0.0.1", "-p", String(port)], {
    cwd: root, stdio: ["ignore", "pipe", "pipe"],
    env: { ...process.env, PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: home, PI_CODING_AGENT_DIR: agentDir,
      PI_WEB_PASSWORD: "", NEXT_TELEMETRY_DISABLED: "1" },
  });
  server.stdout.pipe(serverLog); server.stderr.pipe(serverLog);
  for (let i = 0; ; i++) {
    if ((await fetch(`${base}/api/sessions`).catch(() => null))?.ok) break;
    assert.ok(i < 360 && server.exitCode === null, "Server did not become ready");
    await delay(500);
  }
};
const stopServer = async () => {
  if (!server || server.exitCode !== null) return;
  const exited = once(server, "exit"); server.kill("SIGTERM"); await exited;
  const lock = join(root, ".next/dev/lock");
  try { if (JSON.parse(readFileSync(lock, "utf8")).pid === server.pid) rmSync(lock); } catch {}
};
const read = async (path) => {
  const response = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(60_000) });
  const text = await response.text();
  let json = null; try { json = JSON.parse(text); } catch {}
  return { status: response.status, text, json };
};
const post = (path, body) => fetch(`${base}${path}`, {
  method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body ?? {}), signal: AbortSignal.timeout(60_000),
}).then(async (response) => ({ status: response.status, text: await response.text() })).then((r) => ({ ...r, json: (() => { try { return JSON.parse(r.text); } catch { return null; } })() }));
const rawRequest = (method, path, host) => new Promise((resolve, reject) => {
  const socket = createConnection({ port: Number(new URL(base).port), host: "127.0.0.1" }, () => {
    socket.write(`${method} ${path} HTTP/1.1\r\nHost: ${host}\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}`);
  });
  const chunks = [];
  socket.on("data", (chunk) => chunks.push(chunk));
  socket.on("error", reject);
  socket.on("close", () => {
    const raw = Buffer.concat(chunks).toString("utf8");
    resolve({ status: Number(/^HTTP\/[\d.]+ (\d{3})/.exec(raw)?.[1] ?? 0), text: raw.slice(raw.indexOf("\r\n\r\n") + 4) });
  });
});
const resetFixtures = () => {
  writeCli(6);
  writeFileSync(yamlA, `steps:\n  e2e:\n    fix: { models: ["pi:zeta", "pi:alpha"] }\n`);
  writeFileSync(yamlB, `steps:\n  e2e:\n    fix: { models: ["pi:alpha"] }\n`);
  setStub({ mode: "ok", version: "v2.20.21", listShape: "new", daemon: "up", afterReload: false, fileMax: 6, daemonMax: 6, maxConfigured: 6, pending: [] });
  resetCalls();
};
const SET = (patch) => post("/api/polyforge/config", { action: "set", cwd: wsA, expect_state: { file_sha256: BASELINE_CLI }, ...patch });

// ---- scenario bookkeeping ----
const T = {
  S1: "S1: the config read carries each key's pf class and schema, the file value beside the daemon's loaded value, the machine-level summarize and the per-path memory policy; an unknown class passes through verbatim",
  S2: "S2: an unmapped cwd reports the workspace sections unwritable while the machine-scope cli.toml read keeps working",
  S3: "S3: a stale expected hash is refused through the panel write route with pf's key-level diff and the file untouched",
  S4: "S4: a value past the form's bounds is passed to pf, refused with pf's own error, and leaves the file's sha256 unchanged",
  S5: "S5: an api_key write reaches pf on stdin only and its value appears in no response, server log or history entry",
  S6: "S6: the history holds the last 50 panel writes and a rollback writes the old value through the same expected-hash path",
  S7: "S7: with the daemon down a save still writes and says it applies at the next start, and the reload action re-reads the daemon value",
  S8: "S8: a same-origin write is accepted while an unauthenticated or cross-site one is refused, and pi-web never writes a config file itself",
  S9: "S9: a pf below the capability floor leaves the write surface unavailable with a visible reason while detection keeps working",
};
const failures = new Map(Object.values(T).map((title) => [title, []]));
const check = createCheck({ failures: ([title, label], text) => failures.get(title).push(`${label}: ${text}`), log: rawMessage,
  pass: ([, label]) => console.log(`PASS ${label}`), fail: ([, label], text) => console.log(`FAIL ${label}: ${text}`) });

try {
  await startDevServer();

  await check(T.S1, "S1 read: classes, schema, two truths, workspace", async () => {
    resetFixtures();
    setStub({ fileMax: 7, daemonMax: 6, maxConfigured: 6, pending: ["max_concurrent_jobs"] });
    const r = await read(`/api/polyforge/config?cwd=${encodeURIComponent(wsA)}`);
    assert.equal(r.status, 200, `config read answered ${r.status}: ${r.text.slice(0, 200)}`);
    const items = findItems(r.json);
    const byKey = Object.fromEntries(items.map((it) => [it.key, it]));
    const want = {
      server: "restart-only", daemon_socket: "restart-only", harness_root: "restart-only",
      operator_agent_dir: "restart-only", user_id: "cli-only",
      claude_bin: "live", step_timeout: "live", stall_window: "live", max_concurrent_jobs: "live",
    };
    for (const [key, cls] of Object.entries(want)) {
      assert.ok(byKey[key], `the read carries no ${key}`);
      assert.equal(byKey[key].class, cls, `${key} must carry pf's class ${cls}`);
    }
    assert.equal(byKey.max_concurrent_jobs?.schema?.min, 1, "the schema's min must come from pf (1)");
    assert.equal(byKey.summarize?.interval_sec?.schema?.max ?? byKey["summarize.interval_sec"]?.schema?.max, 3600, "summarize.interval_sec schema max 3600");
    const languageSchema = byKey["summarize.language"]?.schema ?? byKey.summarize?.language?.schema;
    if (languageSchema) assert.ok(!("enum" in languageSchema), "the validator has no language enum; the read must not invent one");
    assert.ok(r.text.includes("brand-new-class"), "a class the panel has never seen must pass through verbatim");
    assert.ok(r.text.includes("max_concurrent_jobs"), "the read must carry max_concurrent_jobs");
    assert.ok(r.text.includes("7") && r.text.includes("6"), "the file value (7) and the daemon's loaded value (6) must both be present");
    assert.match(r.text, /pending|reload/i, "the read must carry the reload facts the panel states a stale reason from");
    assert.ok(r.text.includes("30m0s") && r.text.includes("10m0s"), "step_timeout/stall_window must carry the daemon's loaded values too");
    for (const it of items.filter((entry) => entry.key.includes("summarize"))) {
      assert.ok(!("daemon" in it) && !("daemonValue" in it), "summarize is read live; it must have no daemon column");
    }
    const summarizeItems = items.filter((it) => String(it.key).startsWith("summarize."));
    const intervalItem = summarizeItems.find((it) => it.key === "summarize.interval_sec");
    assert.ok(intervalItem && Number(intervalItem.value) === 30, "the summarize rows must be config list's (30)");
    const other = await read(`/api/polyforge/config?cwd=${encodeURIComponent(wsB)}`);
    const otherInterval = findItems(other.json).find((it) => it.key === "summarize.interval_sec");
    assert.ok(otherInterval && Number(otherInterval.value) === 30, "summarize is one machine-level table: a second mapped workspace shows the same interval (30)");
    assert.equal(r.json?.memoryPolicy?.[wsA]?.default?.record?.[0], "policy-a", "the read carries workspace A's memory_policy block");
    assert.equal(r.json?.memoryPolicy?.[wsB]?.default?.record?.[0], "policy-b", "the read carries workspace B's memory_policy block, kept apart");
    assert.ok(!callsWith("workspace").some((call) => /summarize|memory_policy/.test(JSON.stringify(call.args))), "nothing is read for summarize from config workspace");
    const declared = findArray(r.json, (e) => e && typeof e === "object" && typeof e.declared === "boolean") ?? [];
    const declaredFix = declared.find((e) => e.declared === true);
    const undeclared = declared.find((e) => e.declared === false);
    assert.ok(declaredFix && String(declaredFix.models) === "pi:zeta,pi:alpha", "the declared step must keep pf's candidate order");
    assert.ok(undeclared && (undeclared.models == null || undeclared.models.length === 0), "an undeclared step must carry no composed list");
    assert.ok(!r.text.includes(API_SENTINEL), "the read must not carry an api_key value");
  });

  await check(T.S2, "S2 unmapped cwd", async () => {
    resetFixtures();
    const r = await read(`/api/polyforge/config?cwd=${encodeURIComponent(unmapped)}`);
    assert.equal(r.status, 200, `an unmapped cwd must answer 200, got ${r.status}`);
    assert.equal(findScalar(r.json, /^mapped$/), false, "an unmapped cwd must report mapped=false");
    assert.ok(findItems(r.json).some((it) => it.key === "server"), "the machine-scope cli.toml read must keep working");
    assert.equal(findScalar(r.json, /^mapped$/), false);
    const summarize = findScalar(r.json, /^summarize$/);
    assert.ok(summarize === undefined || summarize === null, "an unmapped cwd must not present writable summarize values");
  });

  await check(T.S3, "S3 stale expected hash", async () => {
    resetFixtures();
    setStub({ mode: "stale" });
    const r = await SET({ key: "max_concurrent_jobs", value: 7 });
    assert.ok(r.status >= 400 && r.status < 500, `a stale write must be refused, got ${r.status}`);
    assert.match(r.text, /max_concurrent_jobs/, "the refusal must carry pf's key-level diff (key name)");
    assert.match(r.text, /diff|current|expected/i, "the refusal must carry the key-level difference");
    assert.equal(sha(cliToml), BASELINE_CLI, "the refused write must leave the file byte-identical");
    const setCalls = callsWith("set");
    assert.ok(setCalls.length >= 1, "the write must go through pf (no pf config set spawn seen)");
    const call = setCalls.at(-1);
    assert.ok(JSON.stringify(call.args).includes("7"), "the value must be handed to pf");
    assert.ok(call.args.some((a) => /^--expect-(hash|state)$/.test(a)), "the write must carry the expected file hash/state to pf");
  });

  await check(T.S4, "S4 invalid value past the form", async () => {
    resetFixtures();
    setStub({ mode: "invalid" });
    const r = await SET({ key: "max_concurrent_jobs", value: 0 });
    assert.ok(r.status < 500, `an invalid value must not answer 5xx (got ${r.status})`);
    assert.ok(/must be >= 1/.test(r.text), `pf's own validation error must be surfaced; got ${r.text.slice(0, 200)}`);
    assert.equal(sha(cliToml), BASELINE_CLI, "the refused write must leave the file byte-identical");
    assert.ok(JSON.stringify(callsWith("set").at(-1)?.args ?? []).includes("0"), "the value 0 must reach pf (the UI does not re-implement the range)");
    const readBack = await read(`/api/polyforge/config?cwd=${encodeURIComponent(wsA)}`);
    assert.equal(findItems(readBack.json).find((it) => it.key === "max_concurrent_jobs")?.schema?.min, 1, "the form bound (min 1) comes from pf's schema, so 0 is unreachable in the form");
  });

  await check(T.S5, "S5 api_key stdin only, never echoed", async () => {
    resetFixtures();
    const r = await SET({ key: "api_key", value: API_SENTINEL });
    assert.ok(r.status < 500, `an api_key write must not answer 5xx (got ${r.status})`);
    assert.ok(!r.text.includes(API_SENTINEL), "the write response echoed the api_key value");
    const keyCalls = callsWith("api_key");
    assert.ok(keyCalls.length >= 1, "no pf config set api_key spawn seen");
    const call = keyCalls.at(-1);
    assert.ok(!call.args.some((a) => a.includes(API_SENTINEL)), "the api_key value must never be an argv token");
    assert.ok(call.stdin.includes(API_SENTINEL), "the api_key value must reach pf on stdin");
    const config = await read(`/api/polyforge/config?cwd=${encodeURIComponent(wsA)}`);
    const history = await read("/api/polyforge/config/history");
    assert.ok(!config.text.includes(API_SENTINEL), "the config read echoed the api_key value");
    assert.ok(!history.text.includes(API_SENTINEL), "the history echoed the api_key value");
    const entry = findEntries(history.json).find((e) => e.key === "api_key");
    assert.ok(entry, "a successful api_key write must be recorded in the history (as set/replaced)");
    assert.notEqual(entry.rollbackable, true, "an api_key history entry must not be rollbackable");
    assert.ok(!JSON.stringify(entry).includes(API_SENTINEL), "the api_key history entry carries the value");
    const rollback = await post("/api/polyforge/config/history", { id: entry.id, key: "api_key", entry, cwd: wsA });
    assert.ok(rollback.status >= 400, "rolling back an api_key entry must be refused");
    assert.ok(!JSON.stringify(callsWith("api_key").at(-1)?.args ?? []).includes(API_SENTINEL), "the rollback path must never carry the value");
    const log = readFileSync(join(work, "server.log"), "utf8");
    assert.ok(!log.includes(API_SENTINEL), "the server log carries the api_key value");
    const historyFiles = [];
    const walk = (dir) => {
      for (const name of readdirSync(dir, { withFileTypes: true })) {
        const child = join(dir, name.name);
        if (name.isDirectory()) walk(child);
        else historyFiles.push(readFileSync(child, "utf8"));
      }
    };
    walk(agentDir);
    assert.ok(!historyFiles.some((text) => text.includes(API_SENTINEL)), "the history file on disk carries the api_key value");
  });

  await check(T.S6, "S6 history bound and rollback", async () => {
    resetFixtures();
    for (let i = 1; i <= 51; i++) {
      const r = await SET({ key: "max_concurrent_jobs", value: String(i) });
      assert.ok(r.status < 400, `write ${i} failed: ${r.status} ${r.text.slice(0, 120)}`);
    }
    const first = await read("/api/polyforge/config/history");
    const entries = findEntries(first.json);
    assert.equal(entries.length, 50, `the history must hold the last 50 writes, got ${entries.length}`);
    const news = new Set(entries.map((e) => String(e.new)));
    assert.ok(news.has("51") && news.has("2"), "the last 50 writes (2..51) must be present");
    assert.ok(!news.has("1"), "the dropped first write must be gone");
    const target = entries.find((e) => e.key === "max_concurrent_jobs" && String(e.old) === "3");
    assert.ok(target, "an entry with the old value 3 must exist");
    resetCalls();
    const rolled = await post("/api/polyforge/config/history", { id: target.id, key: target.key, old: target.old, entry: target, cwd: wsA });
    assert.ok(rolled.status < 400, `the rollback must go through the write path (got ${rolled.status}: ${rolled.text.slice(0, 160)})`);
    const setCall = callsWith("set").at(-1);
    assert.ok(setCall, "the rollback must issue a pf config set");
    assert.ok(setCall.args.includes("3"), `the rollout must write the old value (3); args: ${JSON.stringify(setCall.args)}`);
    assert.ok(setCall.args.some((a) => /^--expect-(hash|state)$/.test(a)), "the rollback must use the same expected-hash write path");
    const second = await read("/api/polyforge/config/history");
    const after = findEntries(second.json);
    assert.equal(after.length, 50, "the history stays bounded at 50 after a rollback");
    assert.equal(String(after[0].new), "3", `the rollback must be recorded as a new entry (newest first); got ${JSON.stringify(after[0])}`);
  });

  await check(T.S7, "S7 daemon down write + reload re-read", async () => {
    resetFixtures();
    setStub({ daemon: "down" });
    const saved = await SET({ key: "max_concurrent_jobs", value: 7 });
    assert.ok(saved.status < 500, `a daemon-down save must not answer 5xx (got ${saved.status})`);
    assert.ok(callsWith("set").length >= 1, "a daemon-down save must still reach pf's file write");
    assert.equal(sha(cliToml), BASELINE_CLI, "pi-web must not write the file itself");
    assert.equal(sha(yamlB), BASELINE_B, "a write scoped to workspace A left workspace B's yaml unchanged");
    assert.match(saved.text, /next\s+(daemon\s+)?start|at the next start|next start/i, "the result must say the value applies at the next daemon start");
    setStub({ daemon: "up", fileMax: 7, daemonMax: 6, maxConfigured: 6 });
    resetCalls();
    const reloaded = await post("/api/polyforge/config/reload", { cwd: wsA });
    assert.ok(reloaded.status < 400, `the reload action failed: ${reloaded.status} ${reloaded.text.slice(0, 160)}`);
    const calls = argvCalls();
    const iReload = calls.findIndex((c) => c.args.includes("reload"));
    const iOps = calls.findIndex((c) => c.args.includes("ops") && c.args.includes("status"));
    assert.ok(iReload >= 0, "the reload action must call pf config reload");
    assert.ok(iOps > iReload, "the panel must re-read the daemon value itself after the reload");
    assert.ok(reloaded.text.includes("7"), "the panel must show the daemon's newly loaded value (7)");
  });

  await check(T.S8, "S8 auth surface, no second writer", async () => {
    resetFixtures();
    const okWrite = await SET({ key: "max_concurrent_jobs", value: "7" });
    assert.ok(okWrite.status < 400, `an authenticated same-origin write must reach the route (got ${okWrite.status}: ${okWrite.text.slice(0, 160)})`);
    assert.ok(callsWith("set").length >= 1, "the accepted write must go through pf");
    // every non-secret cli.toml key is on the allowlist and
    // reaches pf as `config set <key> <value>`, with no reload/shutdown/ensure.
    // (restated): server is a Cell's identity — the route
    // refuses it (400 naming the new-profile route) before any pf spawn
    resetCalls();
    const identity = await SET({ key: "server", value: "http://127.0.0.1:2" });
    assert.equal(identity.status, 400, `server must be refused (got ${identity.status})`);
    assert.match(identity.text, /new profile/, "the refusal names the new-profile route");
    assert.equal(callsWith("set").length, 0, "a server write reached pf");
    const NINE = { daemon_socket: "/run/pf-2.sock", user_id: "usr_2", claude_bin: "/usr/bin/claude",
      step_timeout: "45m0s", stall_window: "12m0s", harness_root: "/opt/pf2", operator_agent_dir: "/home/op/.pi2", max_concurrent_jobs: "8" };
    for (const [key, value] of Object.entries(NINE)) {
      resetCalls();
      const written = await SET({ key, value });
      assert.ok(written.status < 400, `${key} must be writable (got ${written.status}: ${written.text.slice(0, 160)})`);
      const call = callsWith("set").at(-1);
      assert.ok(call && call.args.includes(key) && call.args.includes(value), `${key} did not reach pf as config set ${key} ${value}: ${JSON.stringify(call?.args)}`);
      assert.ok(!argvCalls().some((c) => /reload|shutdown|ensure/.test(c.args.join(" "))), `a ${key} save reloaded, shut down or ensured the daemon`);
    }
    // a summarize key is a flat cli.toml write — no --cwd.
    resetCalls();
    const summarize = await SET({ key: "summarize.interval_sec", value: 40 });
    assert.ok(summarize.status < 400, `summarize.interval_sec must be writable (got ${summarize.status}: ${summarize.text.slice(0, 160)})`);
    const summarizeCall = callsWith("set").at(-1);
    assert.ok(summarizeCall && summarizeCall.args.includes("summarize.interval_sec") && summarizeCall.args.includes("40"), `summarize did not reach pf as config set: ${JSON.stringify(summarizeCall?.args)}`);
    assert.ok(!summarizeCall.args.includes("--cwd") && !summarizeCall.args.includes("--workspace"), "a summarize write must carry no path flag");
    // below the 2.20.21 summarize floor (the panel's own floor
    // still met) the read hides both groups behind the reason, with no
    // config workspace stand-in, and a summarize write is refused 400 before
    // any set spawn.
    setStub({ version: "v2.20.20" });
    resetCalls();
    const belowRead = await read(`/api/polyforge/config?cwd=${encodeURIComponent(wsA)}`);
    assert.equal(belowRead.status, 200, `below the summarize floor the read must answer 200 (got ${belowRead.status})`);
    assert.equal(belowRead.json?.capable, true, "the rest of the panel stays capable below the summarize floor");
    assert.ok(!findItems(belowRead.json).some((it) => String(it.key).startsWith("summarize.")), "no summarize row below the summarize floor");
    assert.equal(belowRead.json?.memoryPolicy, undefined, "no memory_policy block below the summarize floor");
    assert.match(String(belowRead.json?.summarizeReason ?? ""), /2\.20\.21[^)]*found v2\.20\.20/, "the reason names the floor and the found version");
    assert.ok(callsWith("workspace").length <= 1, "no extra config workspace read serves the hidden groups");
    resetCalls();
    const belowWrite = await SET({ key: "summarize.interval_sec", value: 45 });
    assert.equal(belowWrite.status, 400, `a summarize write below the floor must answer 400 (got ${belowWrite.status})`);
    assert.match(belowWrite.text, /2\.20\.21/, "the refusal names the summarize floor");
    assert.equal(callsWith("set").length, 0, "a refused summarize write spawns no pf config set");
    setStub({ version: "v2.20.21" });
    resetCalls();
    // server is writable now; a key pf lists but the panel
    // never edits (future_knob) is still off the allowlist.
    const readOnly = await SET({ key: "future_knob", value: "http://e2e-evil.example.com" });
    assert.ok(readOnly.status >= 400, `a read-only key must be refused by the write allowlist (got ${readOnly.status})`);
    assert.ok(callsWith("set").length === 0, "a read-only key must be refused without spawning pf");
    resetCalls();
    const evilHost = await rawRequest("POST", "/api/polyforge/config", "e2e-evil.example.com");
    assert.equal(evilHost.status, 403, `a disallowed Host must be refused with 403, got ${evilHost.status}`);
    const cross = await fetch(`${base}/api/polyforge/config`, {
      method: "POST", headers: { "content-type": "application/json", origin: "http://e2e-evil.example.com", "sec-fetch-site": "cross-site" },
      body: JSON.stringify({ key: "max_concurrent_jobs", value: "7" }),
    });
    assert.equal(cross.status, 403, `a cross-site write must be refused with 403, got ${cross.status}`);
    assert.equal(argvCalls().length, 0, "a refused request must not spawn pf");
    assert.equal(sha(cliToml), BASELINE_CLI, "pi-web wrote cli.toml itself");
    assert.equal(sha(yamlA), BASELINE_A, "pi-web wrote workspace A's yaml itself");
    assert.equal(sha(yamlB), BASELINE_B, "pi-web wrote workspace B's yaml itself");
  });

  await check(T.S9, "S9 capability floor", async () => {
    resetFixtures();
    setStub({ version: "v2.20.5" });
    resetCalls();
    const below = await read(`/api/polyforge/config?cwd=${encodeURIComponent(wsA)}`);
    assert.equal(below.status, 200, `below the floor the read must still answer 200 (got ${below.status})`);
    assert.equal(findScalar(below.json, /capable$/i), false, "a pf below the required capability must report capable=false");
    assert.ok(typeof findScalar(below.json, /reason|detail|message/i) === "string", "the reason must be visible text, not a tooltip-only field");
    const refused = await SET({ key: "max_concurrent_jobs", value: 7 });
    assert.ok(refused.status >= 400, `the write controls must be unavailable below the floor (got ${refused.status})`);
    assert.ok(callsWith("set").length === 0 && callsWith("workspace").length === 0, "below the floor no write/workspace spawn may be issued");
    setStub({ version: "v2.20.6", listShape: "old" });
    resetCalls();
    const malformed = await read(`/api/polyforge/config?cwd=${encodeURIComponent(wsA)}`);
    assert.equal(malformed.status, 200, "an old list shape must degrade to 200");
    assert.equal(findScalar(malformed.json, /capable$/i), false, "a pf whose list lacks the class/schema shape must report capable=false");
    assert.ok(typeof findScalar(malformed.json, /reason|detail|message/i) === "string", "the malformed-shape reason must be visible text");
    assert.ok(callsWith("workspace").length === 0 && callsWith("set").length === 0, "a malformed capability must issue no new-verb spawn");
    const detect = await read("/api/polyforge/detect?force=1");
    assert.equal(detect.status, 200, `detection must keep working (got ${detect.status})`);
    assert.equal(detect.json?.pfPresent, true, "detection must still report pf present");
  });
} catch (error) {
  console.error(`ABORT ${error?.stack ?? error}`);
  process.exitCode = 1;
} finally {
  await stopServer();
  serverLog.end();
}

console.log("");
let failed = 0;
for (const [title, problems] of failures) {
  if (!problems.length) continue;
  failed++;
  console.log(`FAIL ${title}`);
  for (const problem of problems) console.log(`  - ${problem}`);
}
console.log(failed ? `${failed} scenario(s) failed` : "all scenarios passed");
if (failed) process.exitCode = 1;
