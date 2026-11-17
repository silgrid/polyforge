// carries: components/PolyforgeMemory*, components/PolyforgeSearchBar*, lib/polyforge-memory*, app/api/polyforge/memory/*, app/api/polyforge/polyforge-memory-routes*
// — pi-web Polyforge dock Memory tab: stub-pf end-to-end pass.
//
// Authored pre-code by e2e-author against artifacts/spec.md (route names and
// query parameters from artifacts/plan.md D2); the capsule's
// artifacts/e2e-scenarios.md mirrors these scenario blocks (MP1..MP12). The
// suite starts its own `next dev` on a stub `pf` first on PATH whose
// `recall` / `memory show` answers copy the real `pf --json` field shapes
// (owner_user_id, project_id, content_sha, content_excerpt included) and which
// records every argv in a ledger.
//
// Baseline posture: the Memory tab and its routes do not exist yet, so every
// scenario is an assertion-level red, never a build or fixture error.
//
// moved this suite to the new required behaviour (never a
// relaxation): the tab strip reads Work | Memory | Messages (#178), the
// Memory tab browses on open (one no-keyword `pf recall --limit 20 --sort
// updated`) instead of showing a keyword guidance, filter changes re-issue the
// current mode's read, and the pf floor is 2.20.12. polyforge#120: every
// row and the detail show the author's display name and the writer (a
// missing one renders its explicit unknown placeholder), the owner and user
// ids never render, and a writer-kind select appears only on a pf at or
// above the filter's own 0.2.3 floor — there a kind rides the browse/search
// argv, while a legal kind on an older pf answers the route's 400 upgrade
// hint with no recall spawn.
//
//   node e2e/polyforge-memory-panel.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { request } from "node:http";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { polyforgeDock as dock, polyforgeToggle as pfButton } from "./polyforge-dock.mjs";
import { COLD_MS } from "./timeouts.mjs";
import { assertNoDevServer, writeFileAtomic, createCheck, createHarness, rawMessage, suiteDist } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-memory-e2e-"));
const agentDir = join(work, "agent");
const ws1 = join(work, "ws1");
const unmapped = join(work, "not-a-workspace");
for (const dir of [join(agentDir, "sessions", "e2e"), ws1, unmapped]) mkdirSync(dir, { recursive: true });
const ts = "2026-10-27T00:00:00.000Z";
const SESSIONS = { mapped: ["e2e-pf-memory-session", ws1, "E2E pf memory host session"], unmapped: ["e2e-pf-memory-unmapped", unmapped, "E2E pf memory unmapped session"] };
for (const [id, cwd, text] of Object.values(SESSIONS)) writeFileSync(join(agentDir, "sessions", "e2e", `${id}.jsonl`), [
  { type: "session", version: 3, id, timestamp: ts, cwd },
  { type: "message", id: "m1", parentId: null, timestamp: ts, message: { role: "user", content: text } },
].map((entry) => JSON.stringify(entry)).join("\n") + "\n");

// The #56 sentinel set plus a body sentinel and the fields the passport drops.
const KEY_SENTINEL = "E2E-PF-API-KEY-SENTINEL-99";
const USER_SENTINEL = "u_e2e_sentinel_owner_99";
const SERVER_SENTINEL = "http://10.9.9.99:8420";
const ENV_SENTINEL = "E2E_ENV_NAME_SENTINEL_99";
const BODY_SENTINEL = "E2E-MEMORY-BODY-SENTINEL-99";
const PROJECT_ID = "prj_e2e_sentinel_99";
const CONTENT_SHA = "e2econtentsha99e2econtentsha99";
const HINT = "E2E-INTERNAL-HINT-99";
const NESTED_SENTINEL = "E2E-NESTED-WRITER-SENTINEL-99";
const DROPPED = ["owner_user_id", "project_id", "content_sha", "internal_hint", USER_SENTINEL, PROJECT_ID, CONTENT_SHA, HINT, NESTED_SENTINEL];
const SENTINELS = [KEY_SENTINEL, USER_SENTINEL, SERVER_SENTINEL, ENV_SENTINEL, NESTED_SENTINEL];

// ---- fixtures: real `pf --json recall` / `memory show` shapes ----
const bytes = (s) => Buffer.byteLength(s, "utf8");
const mem = (id, type, title, updated, extra = {}) => ({ id, project_id: PROJECT_ID, owner_user_id: USER_SENTINEL,
  work_item_id: null, type, title, visibility: "project", content_sha: CONTENT_SHA, tags: [], related: [], status: "active",
  created_at: "2026-10-20T00:00:00Z", updated_at: updated, internal_hint: HINT, content: `${title} body`, ...extra });
const DOC_BODY = `${BODY_SENTINEL} decision record\n\n<script>window.__pwned=1</script>\n\n<img src=x onerror="window.__pwned=2">\n\n**E2E-BOLD** stays emphasis.\n`;
let LONG_BODY = "# Long experience\n\n" + Array.from({ length: 120 }, (_, i) => `- line ${i + 1} of the long experience body, padded for height`).join("\n") + "\n\n";
LONG_BODY += "x".repeat(7751 - bytes(LONG_BODY) - bytes("\nE2E-LAST-LINE")) + "\nE2E-LAST-LINE";
// polyforge#120: DOC is a step write whose writer_details carry a hostile
// unknown key and a nested object under a known one (both dropped by the
// passport), EXP a session write, CAP a pre-writer row with no display name
// (the explicit unknown placeholders).
const DOC = mem("mem_e2eDoc000001", "doc.decision", "E2E-DOC memory panel decision", "2026-10-26T10:00:00Z", { visibility: "private",
  tags: ["audit", "review"], related: ["mem_e2eExp000002"], work_item_id: "wi_e2eaaaa0001", content: DOC_BODY,
  owner_display_name: "E2E Ada", writer_kind: "step",
  writer_details: { work_item: "wi_e2eaaaa0001", step: "build", attempt: 1, model: "pi:e2e", evil: NESTED_SENTINEL, machine: { deep: NESTED_SENTINEL } } });
const EXP = mem("mem_e2eExp000002", "experience.wrap", "E2E-EXP memory panel experience", "2026-10-25T10:00:00Z", { status: "superseded", content: LONG_BODY,
  owner_display_name: "E2E Grace", writer_kind: "session", writer_details: { machine: "m-e2e", cell: "public" } });
const CAP = mem("mem_e2eCap000003", "fact.decision", "E2E-CAP capped memory", "2026-10-24T10:00:00Z", { content: "E2E-CAP head only" });
// The writer strings the rows must render (the fixed key order, the valued
// known keys only — the evil/machine intrusions above never show).
const DOC_WRITER = "step (work_item=wi_e2eaaaa0001, step=build, attempt=1, model=pi:e2e)";
const EXP_WRITER = "session (machine=m-e2e, cell=public)";
const withLen = (m, len) => ({ ...m, content_len: len ?? bytes(m.content) });
const MEMORIES = Object.fromEntries([withLen(DOC), withLen(EXP), withLen(CAP, 9000)].map((m) => [m.id, m]));
const hit = (m) => { const { content, ...rest } = MEMORIES[m.id] ?? withLen(m); return { ...rest, content_excerpt: content.slice(0, 80) }; };
const numbered = (prefix, n, updated) => Array.from({ length: n }, (_, i) => mem(`mem_e2e${prefix}${String(i + 1).padStart(5, "0")}`, "doc.note",
  `${prefix}-HIT-${String(i + 1).padStart(3, "0")}`, updated(i)));
const RECALL = {
  "": [DOC, EXP], nomatch: [], capped: [CAP],
  // relevance order C, A, B; updated_at order A < B < C
  order: [mem("mem_e2eOrdC00000", "doc.note", "ORDER-C", "2026-10-03T00:00:00Z"), mem("mem_e2eOrdA00000", "doc.note", "ORDER-A", "2026-10-01T00:00:00Z"),
    mem("mem_e2eOrdB00000", "doc.note", "ORDER-B", "2026-10-02T00:00:00Z")],
  many21: numbered("PAGE", 21, (i) => `2026-09-01T00:00:${String(i).padStart(2, "0")}Z`),
  many200: numbered("FULL", 200, (i) => new Date(Date.UTC(2026, 0, 1) + i * 60_000).toISOString()),
};

// ---- stub pf ----
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const seed = (patch = {}) => { writeFileAtomic(stateFile, JSON.stringify({ version: "v2.20.12", mode: "ok", ws1, recall: Object.fromEntries(
  Object.entries(RECALL).map(([k, v]) => [k, v.map(hit)])), memories: MEMORIES, ...patch })); writeFileSync(argvLog, ""); };
seed();
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename));
const args = process.argv.slice(2);
fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const out = (v) => { process.stdout.write(JSON.stringify(v) + "\\n"); process.exit(0); };
if (args.includes("--version")) { process.stdout.write(s.version + "\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
const at = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] : undefined; };
if (rest[0] === "ops" && rest[1] === "status") out({ machine_id: "m-e2e", workspaces: [s.ws1], env_names: ["${ENV_SENTINEL}"],
  jobs: [{ work_item_id: "wi_e2eaaaa0001", slug: "task-99", status: "running", spawn_n: 1, step_age_sec: 30, last_work_ago_sec: 3, candidate: "pi:e2e" }],
  queue: { pending: 0, running: 1, max_concurrent: 4 } });
if (rest[0] === "config" && rest[1] === "workspace") out(at("--cwd") === s.ws1 ? { mapped: true, workspace: s.ws1 } : { mapped: false, workspace: null });
if (rest[0] === "config" && rest[1] === "list") out({ items: [{ key: "server", value: "${SERVER_SENTINEL}", source: "file" },
  { key: "api_key", set: true, source: "file" }, { key: "user_id", value: "${USER_SENTINEL}", source: "file" },
  { key: '[workspace."' + s.ws1 + '"]', section: true, value: "workbench, polyforge" }] });
if (rest[0] === "watch") out({ work_item: rest[1], slug: "task-99", known: true, job: { status: "running" },
  execution: { workflow_status: "running", current_step: "WATCH-DETAIL-99", steps: {} }, events: [], last_seq: 0 });
if (rest[0] === "msg") out({ conversations: [] });
if (rest[0] === "recall" || (rest[0] === "memory" && rest[1] === "show")) {
  if (s.mode === "down") { process.stderr.write("pf: daemon not reachable (connection refused)\\n"); process.exit(1); }
  if (s.mode === "fail") { process.stderr.write(JSON.stringify({ code: "CORE-500", detail: "stub recall failure" }) + "\\n"); process.exit(4); }
}
if (rest[0] === "recall") {
  const words = []; for (let i = 1; i < rest.length; i++) { if (["--project", "--limit", "--tags", "--type", "--sort", "--after", "--writer-kind"].includes(rest[i])) i++; else if (rest[i] !== "--any") words.push(rest[i]); }
  let hits = s.recall[words.join(" ")] || s.recall[""];
  const kind = at("--writer-kind");
  if (kind) hits = hits.filter((h) => h.writer_kind === kind);
  out({ items: hits, total: hits.length });
}
if (rest[0] === "memory" && rest[1] === "show") {
  const m = s.memories[rest[2]];
  if (!m) { process.stderr.write(JSON.stringify({ code: "MEM-404", detail: "memory not found" }) + "\\n"); process.exit(4); }
  out(m);
}
process.stderr.write("pf: unknown command\\n"); process.exit(2);
`);
chmodSync(join(stubDir, "pf"), 0o755);
const calls = () => readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((l) => JSON.parse(l).filter((a) => a !== "--json"));
const recalls = () => calls().filter((a) => a[0] === "recall");
const shows = () => calls().filter((a) => a[0] === "memory" && a[1] === "show");
const memoryVerbs = () => recalls().length + shows().length;
const argAfter = (argv, name) => { const i = argv.indexOf(name); return i >= 0 ? argv[i + 1] : undefined; };
// Every PATH dir holding a pf is replaced by a symlink farm without it (the polyforge.mjs phase-A rule).
const absentPath = (process.env.PATH || "").split(delimiter).map((dir, n) => {
  if (!existsSync(join(dir, "pf"))) return dir;
  const farm = join(work, `nopf-${n}`); mkdirSync(farm);
  for (const name of readdirSync(dir)) if (name !== "pf") symlinkSync(join(dir, name), join(farm, name));
  return farm;
}).join(delimiter);

// ---- scenario bookkeeping ----
const T = {
  MP1: "MP1: the dock tab strip reads Work | Memory | Messages on desktop and in the mobile sheet, with translated zh-CN labels",
  MP2: "MP2: opening the Memory tab browses the project with exactly one no-keyword recall, idling reads nothing more, and an empty submit stays in browse mode",
  MP3: "MP3: a project dropdown mirrors the cwd's workspace projects, a default submit spawns exactly one recall --project workbench --limit 200 <keyword>, and the three filters are opt-in",
  MP4: "MP4: a flag-shaped keyword, an off-list project, an unknown type or a malformed memory id answers 400 with no memory spawn",
  MP5: "MP5: results render newest-first by updated_at, page at 20 client-side, show the narrowing notice at 200 and the empty state at zero hits",
  MP6: "MP6: a result row shows [type] title, updated time, tags, the author and the writer — and never an owner or user id",
  MP7: "MP7: selecting a row spawns one memory show, renders inert sanitized markdown in place, and back restores the list without re-searching",
  MP8: "MP8: the detail shows metadata with private and non-active badges, collapses a 7751-byte body behind a toggle, copies the id and announces a capped body",
  MP9: "MP9: a work_item_id opens the Jobs watch read, a related id opens in the Memory tab, and a null work_item_id renders no link",
  MP10: "MP10: both memory routes pass only the allowed field set, degrade to 200 JSON on failure, and answer 403 to an in-route gate refusal with no spawn",
  MP11: "MP11: below pf 2.20.12 the tab shows the upgrade-required state and spawns no recall; without pf the route degrades to 200 with no console error; the writer filter keeps its own gate — v0.2.2 hides the control and 400s a manual kind, v0.2.3 shows it and the kind rides the argv",
  MP12: "MP12: an unmapped cwd shows the unmapped state and runs no search, and no response or server log carries a sentinel or a memory body",
};
const failures = new Map(Object.values(T).map((title) => [title, []]));
const check = createCheck({ failures: ([title, label], text) => failures.get(title).push(`${label}: ${text}`), log: rawMessage,
  pass: ([, label]) => console.log(`PASS ${label}`), fail: ([, label], text) => console.log(`FAIL ${label}: ${text}`) });

const serverLogs = []; const responses = [];
let base;
const h = createHarness({
  permissions: ["clipboard-read", "clipboard-write"], permissionOrigin: true,
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "en" },
  errors: ["pageerror", "console", "5xx"],
  session: SESSIONS.mapped,
  path: ({ session }) => `/?session=${session[0]}`,
  ready: (page, { session }, timeout) => page.getByText(session[2]).first().waitFor({ state: "attached", timeout }),
  setup(page) {
    page.on("response", async (r) => {
      if (r.url().startsWith(base) && r.url().includes("/api/polyforge/memory")) responses.push({ status: r.status(), text: await r.text().catch(() => "") });
    });
  },
  // One fresh dist dir per server, nested so e2e/all.mjs clears it with the suite dir: a restart reusing a killed server's dist dir reload-looped the page.
  server(name, path = `${stubDir}${delimiter}${process.env.PATH}`) {
    const logFile = join(work, `server-${name}.log`); serverLogs.push(logFile);
    return { name, logFile, distDir: join(suiteDist("polyforge-memory-panel"), name), env: { PATH: path, HOME: work, PI_CODING_AGENT_DIR: agentDir,
      PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: KEY_SENTINEL, [ENV_SENTINEL]: KEY_SENTINEL } };
  },
  async started(started) {
    base = started.base;
    // Warm-up: the first page load compiles and may reload under a loaded host; keep that out of the scenarios.
    if (h.browser) await h.withPage({ gotoTimeout: COLD_MS, readyTimeout: COLD_MS }, async () => {});
  },
});
const { errors, startServer, withPage } = h;
const get = (path) => fetch(`${base}${path}`, { signal: AbortSignal.timeout(60_000) }).then(async (r) => { const text = await r.text(); responses.push({ status: r.status, text }); return { status: r.status, text }; });
const rawGet = (path, host) => new Promise((resolve, reject) => {
  // node:http decodes Transfer-Encoding: chunked (the dev server chunks the
  // middleware's 403 JSON), so the body arrives as plain bytes — unlike a raw
  // socket, which hands back the chunk framing verbatim.
  const req = request({ host: "127.0.0.1", port: Number(new URL(base).port), path, headers: { Host: host }, setHost: false }, (res) => {
    const chunks = []; res.on("data", (c) => chunks.push(c));
    res.on("end", () => resolve({ status: res.statusCode ?? 0, text: Buffer.concat(chunks).toString("utf8") }));
  });
  req.on("error", reject);
  req.end();
});
const searchPath = (q, extra = {}) => `/api/polyforge/memory?${new URLSearchParams({ cwd: ws1, q, project: "workbench", type: "*", ...extra })}`;
const json = (text) => { try { return JSON.parse(text); } catch { return undefined; } };
const hasKey = (v, keys) => v && typeof v === "object" && Object.entries(v).some(([k, c]) => keys.includes(k) || hasKey(c, keys));
const tabs = (page) => dock(page).getByRole("tab");
const topTabs = (page) => dock(page).locator('[data-pf-tabstrip="top"] [role="tab"]');
const keyword = (page) => dock(page).getByRole("searchbox").or(dock(page).getByPlaceholder(/keyword|search/i)).or(dock(page).getByLabel(/keyword/i)).first();
async function memoryTab(page, { browse = true } = {}) {
  await pfButton(page).click(); await dock(page).waitFor();
  const tab = tabs(page).filter({ hasText: /memory/i }).first();
  await tab.waitFor({ timeout: 15_000 }); await tab.click();
  // A mapped, capable tab browses on open; let that read land first.
  if (browse) await settled(isBrowse, "opening the tab must issue its browse read");
  return dock(page);
}
// The browse read the tab issues on open (or a filter's re-read) has landed:
// the newest recall matches pred. Searching before it lands would count it.
async function settled(pred, label) {
  for (let i = 0; i < 60 && !(recalls().length > 0 && pred(recalls().at(-1))); i++) await delay(250);
  assert.ok(recalls().length > 0 && pred(recalls().at(-1)), `${label} (ledger ${JSON.stringify(recalls())})`);
}
// A browse read: --limit 20 --sort updated and no positional keyword (every
// bare token is a flag's value).
const isBrowse = (argv) => argAfter(argv, "--sort") === "updated" && argAfter(argv, "--limit") === "20"
  && argv.slice(1).every((a, i, all) => a.startsWith("--") || all[i - 1]?.startsWith("--"));
async function search(page, q, n = recalls().length + 1) {
  await keyword(page).fill(q); await keyword(page).press("Enter");
  for (let i = 0; i < 40 && recalls().length < n; i++) await delay(250);
  assert.equal(recalls().length, n, `the submit must spawn exactly one recall (ledger ${JSON.stringify(recalls())})`);
}
const topOf = (panel, needle) => panel.getByText(needle, { exact: false }).first().boundingBox().then((b) => b?.y ?? -1);
const clipped = (panel, needle) => panel.evaluate((root, needle) => {
  const el = [...root.querySelectorAll("*")].find((n) => (n.textContent || "").includes(needle) && ![...n.children].some((c) => (c.textContent || "").includes(needle)));
  if (!el) return null; const r = el.getBoundingClientRect();
  for (let n = el.parentElement; n && n !== root.parentElement; n = n.parentElement) {
    if (/(hidden|clip|auto|scroll)/.test(getComputedStyle(n).overflowY) && n.getBoundingClientRect().bottom < r.top) return true; }
  return false;
}, needle);

try {
  await h.launchBrowser(() => chromium.launch());
  await startServer("main");

  await check(T.MP1, "MP1 three tabs desktop, mobile, zh-CN", async () => {
    for (const viewport of [{ width: 1280, height: 800 }, { width: 600, height: 900 }]) await withPage({ viewport }, async (page) => {
      await pfButton(page).click(); await dock(page).waitFor();
      // (owner-approved intentional change ①); (owner
      // 2026-10-30, intentional change): Work | Memory | Messages.
      await topTabs(page).first().waitFor();
      assert.deepEqual((await topTabs(page).allInnerTexts()).map((t) => t.trim().split(/\s/)[0].toLowerCase()), ["work", "memory", "messages"], `tab strip at ${viewport.width}px`);
      await topTabs(page).nth(1).click();
      assert.ok(await dock(page).isVisible(), "selecting Memory must keep the dock open");
      await keyword(page).waitFor({ timeout: 10_000 });
    });
    await withPage({ storage: { "pi-locale": "zh-CN" } }, async (page) => {
      await pfButton(page).click(); await dock(page).waitFor();
      await topTabs(page).first().waitFor();
      assert.equal(await topTabs(page).count(), 3, "zh-CN tab strip must carry three tabs");
      await topTabs(page).nth(1).click();
      assert.match(await topTabs(page).nth(1).innerText(), /[一-鿿]/, "the Memory tab label must be translated");
      assert.doesNotMatch(await dock(page).innerText(), /polyforge\.memory\./, "no untranslated polyforge.memory.* key may be visible");
    });
  });

  await check(T.MP2, "MP2 browse on open", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    await panel.getByText(DOC.title).waitFor({ timeout: 10_000 });
    assert.equal(recalls().length, 1, `opening the tab must spawn exactly one browse recall (ledger ${JSON.stringify(recalls())})`);
    const browse = recalls()[0];
    assert.deepEqual([argAfter(browse, "--project"), argAfter(browse, "--limit"), argAfter(browse, "--sort")], ["workbench", "20", "updated"], `browse argv ${JSON.stringify(browse)}`);
    assert.ok(!browse.includes("--any") && !browse.includes("--after"), `the first page carries no keyword flag or cursor: ${JSON.stringify(browse)}`);
    assert.doesNotMatch(await panel.innerText(), /enter a keyword/i, "no keyword guidance state remains");
    await delay(4000);
    assert.equal(memoryVerbs(), 1, "idling on the tab must spawn nothing more (no timer)");
    await keyword(page).fill(""); await keyword(page).press("Enter");
    await settled((a) => isBrowse(a) && recalls().length === 2, "an empty submit re-reads the browse page");
    assert.equal(shows().length, 0, "no memory show without a selection");
  }));

  await check(T.MP3, "MP3 project dropdown and recall argv", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    const project = panel.getByRole("combobox", { name: /project/i }).first();
    await project.waitFor({ timeout: 10_000 });
    assert.deepEqual((await project.locator("option").allInnerTexts()).map((t) => t.trim()), ["workbench", "polyforge"], "the dropdown must list ws1's projects in order");
    assert.equal(await project.inputValue(), "workbench", "the first project must be selected");
    await search(page, "memory panel");
    const plain = recalls().at(-1);
    assert.deepEqual([argAfter(plain, "--project"), argAfter(plain, "--limit"), plain.at(-1)], ["workbench", "200", "memory panel"], `default argv ${JSON.stringify(plain)}`);
    assert.ok(!plain.includes("--any") && !plain.includes("--tags") && !plain.includes("--type"), `the default search carries no optional flag: ${JSON.stringify(plain)}`);
    await panel.getByRole("checkbox", { name: /any/i }).check();
    // A filter change re-issues the current (keyword) read; `any` applies at
    // the next submit.
    await panel.getByLabel(/tags/i).or(panel.getByPlaceholder(/tag/i)).first().fill("audit,review");
    await settled((a) => argAfter(a, "--tags") === "audit,review", "a tag change re-issues the keyword read");
    await panel.getByRole("combobox", { name: /type/i }).selectOption("doc.*");
    await settled((a) => argAfter(a, "--type") === "doc.*", "a type change re-issues the keyword read");
    await search(page, "memory panel");
    const full = recalls().at(-1);
    assert.ok(full.includes("--any") && argAfter(full, "--tags") === "audit,review" && argAfter(full, "--type") === "doc.*", `filtered argv ${JSON.stringify(full)}`);
    await panel.getByRole("checkbox", { name: /any/i }).uncheck();
    await panel.getByLabel(/tags/i).or(panel.getByPlaceholder(/tag/i)).first().fill("");
    await settled((a) => !a.includes("--tags"), "clearing the tags re-issues the keyword read");
    await panel.getByRole("combobox", { name: /type/i }).selectOption("*");
    await settled((a) => !a.includes("--type"), "the all-types choice re-issues the keyword read");
    await search(page, "memory panel");
    assert.ok(!recalls().at(-1).some((a) => ["--any", "--tags", "--type", "--writer-kind"].includes(a)), "unticking returns to the flag-less form");
    assert.equal(await panel.getByRole("combobox", { name: /writer kind|writer filter/i }).count(), 0,
      "on a pf below the filter's own floor the kind control stays hidden (Q2 — the browse floor it is not)");
  }));

  await check(T.MP4, "MP4 refused before spawn", async () => {
    seed();
    for (const [label, path] of [["flag keyword", searchPath("--limit 1")], ["off-list project", searchPath("memory", { project: "not-offered" })],
      ["unknown type", searchPath("memory", { type: "bogus.*" })], ["traversal id", `/api/polyforge/memory/${encodeURIComponent("../../etc/passwd")}`],
      ["blank id", `/api/polyforge/memory/${encodeURIComponent("mem_   ")}`],
      ["illegal writer kind", searchPath("memory", { writer_kind: "human" })],
      ["legal writer kind on a filter-incapable pf", searchPath("memory", { writer_kind: "session" })]]) {
      const r = await get(path);
      assert.equal(r.status, 400, `${label} must answer 400, got ${r.status}`);
      assert.ok(json(r.text), `${label} must answer JSON`);
      assert.equal(memoryVerbs(), 0, `${label} must spawn no recall / memory show`);
      if (label.startsWith("legal writer kind")) {
        assert.match(r.text, /0\.2\.3/, "the refusal must name the filter's own floor (Q4)");
      }
    }
    // A browse face kind meets the same two refusals.
    const browseKind = await get(`/api/polyforge/memory?${new URLSearchParams({ cwd: ws1, project: "workbench", type: "*", writer_kind: "step" })}`);
    assert.equal(browseKind.status, 400, "a browse writer kind on a filter-incapable pf must answer 400");
    assert.match(browseKind.text, /0\.2\.3/, "the browse refusal names the floor too");
    assert.equal(memoryVerbs(), 0, "the refused browse spawns nothing");
  });

  await check(T.MP5, "MP5 order, paging, notice, empty", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    await search(page, "order"); await panel.getByText("ORDER-A").waitFor();
    const [c, b, a] = [await topOf(panel, "ORDER-C"), await topOf(panel, "ORDER-B"), await topOf(panel, "ORDER-A")];
    assert.ok(c < b && b < a, `rows must read C, B, A (tops ${c}, ${b}, ${a})`);
    await search(page, "many21"); await panel.getByText("PAGE-HIT-021").waitFor();
    assert.equal(await panel.getByText(/PAGE-HIT-\d{3}/).count(), 20, "the first page must show 20 rows");
    assert.match(await panel.innerText(), /\b1\s*(of|\/)\s*2\b/, "the page indicator must read 1 of 2");
    const spawned = recalls().length;
    await panel.getByRole("button", { name: /next|›|»|下一/i }).first().click();
    await panel.getByText("PAGE-HIT-001").waitFor();
    assert.equal(recalls().length, spawned, "paging must not spawn another pf read");
    await search(page, "many200"); await panel.getByText(/FULL-HIT-200/).waitFor();
    assert.match(await panel.innerText(), /narrow/i, "200 hits must show the narrowing notice");
    assert.match(await panel.innerText(), /\b1\s*(of|\/)\s*10\b/, "200 hits page as 10 pages, never more");
    assert.equal(await panel.getByRole("button", { name: /load more|more results/i }).count(), 0, "no control may page past 200");
    await search(page, "nomatch");
    await panel.getByText(/no memories/i).first().waitFor({ timeout: 10_000 });
  }));

  await check(T.MP6, "MP6 row content", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    await search(page, "memory panel"); await panel.getByText(DOC.title).waitFor();
    const text = await panel.innerText();
    for (const want of ["doc.decision", DOC.title, "audit", "review", "2026",
      "E2E Ada", DOC_WRITER, "E2E Grace", EXP_WRITER]) assert.ok(text.includes(want), `the row must show ${JSON.stringify(want)}`);
    const html = await panel.innerHTML();
    assert.ok(!html.includes(USER_SENTINEL) && !html.includes("owner_user_id"), "no owner or user id may render, hover titles included");
    assert.ok(!html.includes(NESTED_SENTINEL), "an unknown writer-details key never reaches the DOM");
  }));

  await check(T.MP7, "MP7 show, inert markdown, back", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    await search(page, "memory panel"); await panel.getByText(DOC.title).click();
    await panel.getByText("E2E-BOLD").first().waitFor({ timeout: 15_000 });
    assert.deepEqual(shows().map((a) => a[2]), [DOC.id], "the selection must spawn exactly one memory show for that id");
    assert.equal(await page.evaluate(() => window.__pwned), undefined, "raw HTML in a body must never execute");
    assert.equal(await panel.locator("script").count(), 0, "no script element may reach the panel");
    assert.equal(await panel.locator("img[onerror], [onerror], [onload]").count(), 0, "no event handler attribute may reach the DOM");
    assert.ok(await panel.locator("strong", { hasText: "E2E-BOLD" }).count() > 0, "markdown emphasis must still render");
    const spawned = recalls().length;
    await panel.getByRole("button", { name: /back|←/i }).first().click();
    await panel.getByText(EXP.title).waitFor();
    assert.equal(await keyword(page).inputValue(), "memory panel", "back must keep the query");
    assert.equal(recalls().length, spawned, "back must not re-run the search");
  }));

  await check(T.MP8, "MP8 metadata, collapse, copy, cap", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    await search(page, "memory panel"); await panel.getByText(DOC.title).click(); await panel.getByText("E2E-BOLD").first().waitFor();
    const text = await panel.innerText();
    for (const want of ["doc.decision", "private", "active", "audit", "review", EXP.id, "wi_e2eaaaa0001",
      "E2E Ada", DOC_WRITER]) assert.ok(text.includes(want), `the detail must show ${want}`);
    for (const day of [/2026-10-20|10\/20\/2026|Oct\w* 20/, /2026-10-26|10\/26\/2026|Oct\w* 26/]) assert.match(text, day, "the detail must show created and updated");
    assert.ok(!/inactive|non-active/i.test(text), "an active status carries no non-active badge");
    assert.equal(await panel.getByRole("button", { name: /expand|show more|展开/i }).count(), 0, "a short body has no expand control");
    assert.doesNotMatch(text, /truncat/i, "an uncapped body shows no truncation notice");
    await panel.getByRole("button", { name: /copy/i }).first().click();
    await panel.getByText(/copied/i).first().waitFor({ timeout: 5000 });
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), DOC.id, "the clipboard must hold the exact id");
    await panel.getByRole("button", { name: /back|←/i }).first().click(); await panel.getByText(EXP.title).click();
    await panel.getByText(/superseded/i).first().waitFor({ timeout: 15_000 });
    const expand = panel.getByRole("button", { name: /expand|show more|展开/i }).first();
    assert.ok(await expand.isVisible(), "a 7751-byte body must start collapsed behind an expand control");
    assert.equal(await clipped(panel, "E2E-LAST-LINE"), true, "the collapsed view must clip the last line");
    await expand.click();
    assert.equal(await clipped(panel, "E2E-LAST-LINE"), false, "expanding must reveal the real last line");
    await panel.getByRole("button", { name: /collapse|show less|收起/i }).first().click();
    assert.equal(await clipped(panel, "E2E-LAST-LINE"), true, "collapsing again must restore the clipped view");
    await panel.getByRole("button", { name: /back|←/i }).first().click();
    await search(page, "capped"); await panel.getByText(CAP.title).click(); await panel.getByText("E2E-CAP head only").waitFor();
    const capText = await panel.innerText();
    assert.match(capText, /truncat/i, "a body shorter than content_len must carry a truncation notice");
    assert.match(capText, /unknown author/i, "a row with no display name shows the explicit unknown-author placeholder (Q3)");
    assert.match(capText, /unknown writer/i, "a pre-writer row shows the explicit unknown-writer placeholder (Q3)");
  }));

  await check(T.MP9, "MP9 reference links", () => withPage({}, async (page) => {
    seed(); const panel = await memoryTab(page);
    await search(page, "memory panel"); await panel.getByText(DOC.title).click(); await panel.getByText("E2E-BOLD").first().waitFor();
    const link = (name) => panel.getByRole("link", { name }).or(panel.getByRole("button", { name })).first();
    await link(new RegExp(EXP.id)).click();
    await panel.getByText(/superseded/i).first().waitFor({ timeout: 15_000 });
    assert.deepEqual(shows().map((a) => a[2]), [DOC.id, EXP.id], "a related id must run one memory show for it");
    assert.equal(await tabs(page).filter({ hasText: /memory/i }).first().getAttribute("aria-selected"), "true", "the dock must stay on Memory");
    assert.equal(await link(/wi_/).count(), 0, "a null work_item_id must render no link");
    await panel.getByRole("button", { name: /back|←/i }).first().click(); await panel.getByText(DOC.title).click(); await panel.getByText("E2E-BOLD").first().waitFor();
    const before = calls().length;
    await link(/wi_e2eaaaa0001/).click();
    // owner-intended change: the current step moved from the old detail header's visible text into
    // the title row's hover; the same guarantee (this work item's watch read opened its detail) is read from
    // the title row carrying the slug with the watched current step on hover.
    await panel.locator('[data-pf-detail-title][title*="WATCH-DETAIL-99"]').getByText("task-99").first().waitFor({ timeout: 15_000 });
    assert.equal(await tabs(page).filter({ hasText: /jobs/i }).first().getAttribute("aria-selected"), "true", "the dock must switch to Jobs");
    assert.ok(calls().slice(before).some((a) => a[0] === "watch" && a.includes("wi_e2eaaaa0001")), "the existing watch read must run for wi_e2eaaaa0001");
  }));

  await check(T.MP10, "MP10 passport, degradation, gate", async () => {
    seed();
    const found = await get(searchPath("memory panel")); const shown = await get(`/api/polyforge/memory/${DOC.id}`);
    assert.equal(found.status, 200, `search answered ${found.status}`); assert.equal(shown.status, 200, `show answered ${shown.status}`);
    assert.ok(found.text.includes(DOC.id) && shown.text.includes("E2E-BOLD"), "both routes must carry the memory");
    assert.ok(found.text.includes("E2E Ada") && found.text.includes("writer_kind"), "the search answer carries the attribution the passport now passes");
    assert.ok(shown.text.includes("E2E Ada") && shown.text.includes("writer_kind"), "the show answer carries the attribution the passport now passes");
    for (const r of [found, shown]) for (const word of DROPPED) assert.ok(!r.text.includes(word), `a response carries the dropped ${word}`);
    assert.ok(!hasKey(json(found.text), ["content", "content_excerpt"]) && !found.text.includes(BODY_SENTINEL), "the search carries no content field");
    for (const mode of ["down", "fail"]) {
      seed({ mode });
      for (const path of [searchPath("memory panel"), `/api/polyforge/memory/${DOC.id}`]) {
        const r = await get(path); const body = json(r.text);
        assert.equal(r.status, 200, `${mode} ${path} must answer 200, got ${r.status}`);
        assert.ok(body && /\w/.test(String(body.error ?? body.reason ?? "")), `${mode} ${path} must carry a degradation reason`);
      }
    }
    seed();
    for (const path of [searchPath("memory panel"), `/api/polyforge/memory/${DOC.id}`]) {
      const r = await rawGet(path, "e2e-evil.example.com");
      assert.equal(r.status, 403, `a refused request must answer 403, got ${r.status}`); assert.ok(json(r.text), "the 403 must be JSON");
    }
    assert.equal(memoryVerbs(), 0, "a refused request must spawn nothing");
    assert.ok(!errors.some((l) => /^5\d\d /.test(l)), `no route may answer 5xx: ${errors.join(" | ")}`);
  });

  await check(T.MP12, "MP12 unmapped cwd", () => withPage({ session: SESSIONS.unmapped }, async (page) => {
    seed(); const panel = await memoryTab(page, { browse: false });
    await panel.getByText(/unmapped|not mapped|no workspace|maps to no/i).first().waitFor({ timeout: 10_000 });
    if (await keyword(page).isEditable().catch(() => false)) { await keyword(page).fill("memory"); await keyword(page).press("Enter"); }
    await delay(1500);
    assert.equal(recalls().length, 0, "an unmapped cwd must spawn no recall");
  }));

  await check(T.MP11, "MP11 below floor", async () => {
    await h.stop(); seed({ version: "v2.20.11" }); await startServer("floor");
    const scope = await get(`/api/polyforge/memory?${new URLSearchParams({ cwd: ws1 })}`);
    assert.equal(scope.status, 200, `the below-floor route answered ${scope.status}`);
    assert.match(scope.text, /2\.20\.12|upgrade/i, "below the floor the reason must name the upgrade");
    await get(searchPath("memory panel"));
    await withPage({}, async (page) => {
      const panel = await memoryTab(page, { browse: false });
      await panel.getByText(/upgrade/i).first().waitFor({ timeout: 10_000 });
      if (await keyword(page).isEditable().catch(() => false)) { await keyword(page).fill("memory"); await keyword(page).press("Enter"); await delay(1500); }
    });
    assert.equal(recalls().length, 0, "below the floor no recall may be spawned");
  });

  await check(T.MP11, "MP11 pf absent", async () => {
    await h.stop(); await startServer("absent", absentPath); errors.length = 0;
    const r = await get(`/api/polyforge/memory?${new URLSearchParams({ cwd: ws1 })}`);
    assert.equal(r.status, 200, `without pf the route must answer 200, got ${r.status}`);
    assert.ok(json(r.text) && /pfPresent"\s*:\s*false|not installed|absent/i.test(r.text), "without pf the body must carry the not-installed reason");
    await withPage({}, async () => delay(1500));
    assert.deepEqual(errors.filter((l) => !/favicon/i.test(l)), [], "without pf no browser console error may be raised");
  });

  await check(T.MP11, "MP11 the writer filter keeps its own gate: v0.2.2 hides the control and 400s a manual kind; v0.2.3 shows it and the kind rides the argv", async () => {
    await h.stop(); seed({ version: "v0.2.2" }); await startServer("v022");
    const refused = await get(searchPath("memory", { writer_kind: "step" }));
    assert.equal(refused.status, 400, `a legal kind on v0.2.2 must answer 400, got ${refused.status}`);
    assert.match(refused.text, /0\.2\.3/, "the refusal names the filter's own floor, never the browse floor");
    await withPage({}, async (page) => {
      const panel = await memoryTab(page);
      await panel.getByText(DOC.title).waitFor({ timeout: 10_000 });
      assert.equal(await panel.getByRole("combobox", { name: /writer kind|writer filter/i }).count(), 0,
        "v0.2.2 keeps the tab (its browse floor is met) without the kind control");
      const text = await panel.innerText();
      assert.ok(text.includes("E2E Ada") && text.includes(DOC_WRITER), "the attribution display is never gated by the filter");
    });
    await h.stop(); seed({ version: "v0.2.3" }); await startServer("v023");
    await withPage({}, async (page) => {
      const panel = await memoryTab(page);
      const kindSelect = panel.getByRole("combobox", { name: /writer kind|writer filter/i }).first();
      await kindSelect.waitFor({ timeout: 10_000 });
      assert.ok(!recalls().at(-1).includes("--writer-kind"), "the all-writers default browse sends no flag");
      await kindSelect.selectOption("session");
      await settled((a) => argAfter(a, "--writer-kind") === "session", "a session kind re-issues the browse read carrying the flag");
      await panel.getByText(EXP.title).waitFor({ timeout: 10_000 });
      const sessionText = await panel.innerText();
      assert.ok(sessionText.includes(EXP_WRITER) && !sessionText.includes(DOC.title), "the filtered browse shows only the session rows (the stub filters like the aihub)");
      await kindSelect.selectOption("step");
      await settled((a) => argAfter(a, "--writer-kind") === "step" && !a.includes("--after"), "a kind switch re-reads page 1 — the cursor resets (Q5)");
      await panel.getByText(DOC.title).waitFor({ timeout: 10_000 });
      await search(page, "memory panel", recalls().length + 1);
      assert.equal(argAfter(recalls().at(-1), "--writer-kind"), "step", "the keyword search carries the same kind (Q5)");
      await panel.getByText(DOC.title).first().waitFor({ timeout: 10_000 });
      assert.ok(!(await panel.innerText()).includes(EXP.title), "the step-filtered search leaves the session row out");
    });
  });

  await check(T.MP12, "MP12 sentinels and bodies stay server-side", async () => {
    assert.ok(responses.some((r) => r.status === 200 && json(r.text) && r.text.includes(DOC.id)), "the memory routes must have answered 200 JSON during the run");
    for (const { text } of responses) for (const s of SENTINELS) assert.ok(!text.includes(s), `a memory response carries ${s}`);
    for (const file of serverLogs) { const log = readFileSync(file, "utf8");
      for (const s of [...SENTINELS, BODY_SENTINEL]) assert.ok(!log.includes(s), `the server log ${file} carries ${s}`); }
  });
} catch (error) {
  console.error(error);
  for (const [title, list] of failures) if (!list.length) failures.set(title, [`suite: ${error?.message ?? error}`]);
} finally {
  await h.close().catch(() => {});
}

let failed = 0;
console.log("\n=== scenarios ===");
for (const [title, list] of failures) {
  if (list.length) { failed++; console.log(`FAIL ${title}`); for (const line of list) console.log(`     ${line}`); }
  else console.log(`PASS ${title}`);
}
rmSync(work, { force: true, recursive: true });
process.exit(failed ? 1 : 0);
