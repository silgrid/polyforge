// carries: components/PolyforgeMessages*, hooks/usePolyforgeMessages*, lib/polyforge-messages*, app/api/polyforge/users/*, app/api/polyforge/projects/*
// carries: app/api/polyforge/polyforge-members-routes*, app/api/polyforge/polyforge-users-routes*
// — Polyforge Messages contacts grouped by project + project
// membership management (authored BEFORE `code`, against artifacts/spec.md).
//
// Drives the spec's new behaviour through the real panel and the real routes
// against a stateful stub `pf`: the stub answers the cli.toml workspace
// mapping, one `projects get` read per mapped project, the human users list,
// the membership writes and the existing Messages reads, and records every
// argv in a ledger. Each scenario prints `PASS S<n>` only when its THEN clause
// holds; the pre-implementation tree prints no PASS line.
//
// Scenario table (e2e-scenarios.md mirrors it):
//   S1  groups = mapped projects, name order, shared member in both, unmapped none
//   S2  direct rows / unread / 群聊 placement unchanged, unread never duplicated
//   S3  collapse survives reload; missing/corrupt/refused/stale storage degrades
//   S4  header count matches the rows and follows a removal
//   S5  empty mapped group keeps a placeholder and its add affordance
//   S6  one failed project read notes only that group; the answer is 200
//   S7  grouped contact opens its conversation, or the empty pane (no creation)
//   S8  admin probe gates management; a refused probe shows the read-only notice
//   S9  picker lists human non-members only; add is writer and re-reads at once
//   S10 removing a member keeps the conversation, history and later sends
//   S11 confirmations name project + display name; cancelling spawns nothing
//   S12 write route: 403 gate, 415 non-JSON, 400 unmapped/invalid, no spawn
//   S13 read faces: users id/name only; groups have no machine members/roles
//   S14 grouping reads argv + a hanging project read stays within the bound
//   S15 refused write 4xx / unreachable 200; the panel shows it, no false change
//   S16 blank display name -> unknown-user placeholder, never a raw id
//   S17 grouping/folding/management at <=640px, list column unchanged
//   S18 new grouping/admin copy resolves in en, zh-CN, zh-TW
//   S19 docs/agents/polyforge-panel.md + AGENTS.md File Map name the new surface
//
//   node e2e/polyforge-messages-groups-223.mjs
import assert from "node:assert/strict";
import { appendFileSync, chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { polyforgeDock as dock, polyforgeToggle as pfButton } from "./polyforge-dock.mjs";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, rawMessage, root } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-groups-223-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const cliDir = join(work, ".config", "polyforge");
for (const dir of [join(agentDir, "sessions", "e2e"), project, cliDir]) mkdirSync(dir, { recursive: true });
const SESSION = "e2e-pf-groups-session";
const sessionFile = join(agentDir, "sessions", "e2e", `${SESSION}.jsonl`);
const ts = "2026-11-01T00:00:00.000Z";
writeFileSync(sessionFile, [
  { type: "session", version: 3, id: SESSION, timestamp: ts, cwd: project },
  { type: "message", id: "m1", parentId: null, timestamp: ts, message: { role: "user", content: "E2E pf groups host session" } },
].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
const SELF = "u_e2e_self_223";
const SELF_NAME = "Self Operator 223";
const COLLAPSE_KEY = "pi-web:polyforge-messages-collapsed";
writeFileSync(join(cliDir, "cli.toml"), `server = "http://groups.invalid:1"\napi_key = "E2E-KEY-223"\nuser_id = "${SELF}"\n`);

// Sentinels that must never reach the browser read faces.
const EMAIL_SENTINEL = "EMAIL-SENTINEL-223@groups.invalid";
const ROLE_SENTINEL = "ROLE-SENTINEL-223";
const TOKEN_SENTINEL = "API-TOKEN-SENTINEL-223";
const USER_TYPE_SENTINEL = "user_type";

const AVA = { id: "u_223_ava", display_name: "Ava Stone", user_type: "human" };
const BEN = { id: "u_223_ben", display_name: "Ben Ortiz", user_type: "human" };
const ZOE = { id: "u_223_zoe", display_name: "Zoe Adams", user_type: "human" };
const GINA = { id: "u_223_gina", display_name: "Gina Gamma", user_type: "human" };
const NINA = { id: "u_223_nina", display_name: "Nina New", user_type: "human" };
const BLANK = { id: "u_223_blank", display_name: "", user_type: "human" };
const BLANK2 = { id: "u_223_blank2", display_name: "", user_type: "human" };
const MACHINE = { id: "u_223_machine", display_name: "Mia Machine", user_type: "machine" };
const USERS = () => [AVA, BEN, ZOE, GINA, NINA, BLANK, BLANK2, MACHINE].map((user) => ({
  ...user,
  email: EMAIL_SENTINEL,
  role: ROLE_SENTINEL,
  api_tokens: [TOKEN_SENTINEL],
}));

const msg = (seq, sender, text) => ({ id: `msg_${seq}_${sender}`, seq, sender, text, about_wi: "", reply_to: "" });
const conversation = (id, peer, peerName, messages) => ({ id, peer, peerName, messages, lastSeq: messages.length ? messages[messages.length - 1].seq : 0 });
const AVA_CONV = conversation("conv_ava", AVA.id, AVA.display_name, [msg(1, "peer", "AVA-HELLO")]);
const BEN_CONV = conversation("conv_ben", BEN.id, BEN.display_name, [msg(1, "peer", "BEN-HELLO")]);

const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const baseState = () => ({
  version: "v2.20.12", self: SELF, selfName: SELF_NAME,
  // cli.toml's workspace mapping, deliberately not name-sorted: the list must sort it.
  mapping: { "/srv/e2e": "beta, alpha" },
  // Project membership as aihub sees it. gamma exists but is not mapped; the
  // machine is a member of a mapped project but must never enter a group.
  projects: { alpha: [AVA.id, BEN.id, MACHINE.id], beta: [AVA.id, ZOE.id], gamma: [GINA.id] },
  users: USERS(),
  conversations: [], acked: {}, sends: [],
  contactsFail: false, contactsHang: false,
  projectFail: "", projectHang: "",
  usersFail: false,
  membersError: "", membersUnavailable: false,
});
const save = (state) => {
  const tmp = stateFile + ".tmp";
  writeFileSync(tmp, JSON.stringify(state));
  // rename is atomic for a concurrent stub read, so a poll never sees a torn file.
  renameSync(tmp, stateFile);
};
const seed = (patch = {}) => {
  const next = { ...baseState(), ...patch };
  save(next);
  appendFileSync(argvLog, JSON.stringify(["__seed__"]) + "\n");
  return next;
};
seed();

writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename));
const args = process.argv.slice(2);
// The ledger records a completed handler's argv (never the process start), so
// a killed hanging read leaves no membership trace behind.
const record = () => fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
const readState = () => JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const saveState = (state) => { const p = path.join(dir, "state.json"); fs.writeFileSync(p + ".tmp", JSON.stringify(state)); fs.renameSync(p + ".tmp", p); };
const out = (v) => { process.stdout.write(JSON.stringify(v) + "\\n"); process.exit(0); };
const die = (code, text) => { process.stderr.write(text + "\\n"); process.exit(code); };
const s = readState();
if (args.includes("--version")) { record(); process.stdout.write(s.version + "\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
const at = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] : undefined; };
const mapped = () => Object.keys(s.mapping || {});
const projectNameList = () => mapped().reduce((acc, workspace) => acc.concat(String(s.mapping[workspace] || "").split(",").map((name) => name.trim())), []).filter(Boolean);
if (rest[0] === "ops" && rest[1] === "status") {
  record();
  out({ machine_id: "m-e2e-223", workspaces: ["/srv/e2e"], workflows_dir: "/srv/e2e/wf", env_names: [], jobs: [], queue: { pending: 0, running: 0, max_concurrent: 4 } });
}
if (rest[0] === "config" && rest[1] === "list") {
  record();
  const items = [
    { key: "server", value: "http://groups.invalid:1", source: "file" },
    { key: "api_key", set: true, source: "file" },
    { key: "user_id", value: s.self, source: "file" },
  ];
  for (const workspace of mapped()) {
    items.push({ key: '[workspace."' + workspace + '"]', value: s.mapping[workspace], section: true, source: "file" });
  }
  out({ items });
}
if (rest[0] === "projects" && rest[1] === "get") {
  const name = rest[2];
  record();
  if (s.projectHang === name) { setTimeout(() => {}, 600000); return; }
  if (s.projectFail === name) die(4, JSON.stringify({ code: "CORE-503", detail: name + " members are unavailable" }));
  const members = (s.projects[name] || []).map((id) => {
    const user = (s.users || []).find((row) => row.id === id) || {};
    return { user_id: id, role: "${ROLE_SENTINEL}", email: "${EMAIL_SENTINEL}", user_type: user.user_type || "human", api_tokens: ["${TOKEN_SENTINEL}"] };
  });
  out({ project: name, members });
}
if (rest[0] === "users" && rest[1] === "list") {
  record();
  if (s.usersFail) die(4, JSON.stringify({ code: "AUTH-403", detail: "admin rights are required" }));
  out({ items: s.users });
}
if (rest[0] === "projects" && rest[1] === "members") {
  const action = rest[2]; const name = rest[3]; const userId = at("--user");
  record();
  if (s.membersUnavailable) die(1, "pf: daemon unreachable (connection refused)");
  if (s.membersError) die(4, JSON.stringify({ code: s.membersError, detail: "membership refused by aihub policy" }));
  const fresh = readState();
  if (!fresh.projects[name]) die(4, JSON.stringify({ code: "CORE-404", detail: "project is unknown" }));
  if (action === "add") {
    if (!fresh.projects[name].includes(userId)) fresh.projects[name].push(userId);
  } else if (action === "remove") {
    fresh.projects[name] = fresh.projects[name].filter((id) => id !== userId);
  } else {
    die(2, "pf: projects members: unknown action " + action);
  }
  saveState(fresh);
  out({ ok: true, project: name, action });
}
if (rest[0] === "msg") {
  if (rest[1] === "contacts") {
    record();
    if (s.contactsHang) { setTimeout(() => {}, 600000); return; }
    if (s.contactsFail) die(4, JSON.stringify({ code: "CORE-503", detail: "contacts are unavailable" }));
    const ids = new Set();
    for (const name of projectNameList()) for (const id of (s.projects[name] || [])) ids.add(id);
    const contacts = (s.users || [])
      .filter((user) => ids.has(user.id) && user.user_type !== "machine" && user.id !== s.self)
      .map((user) => ({ id: user.id, display_name: user.display_name || "" }));
    out({ contacts });
  }
  if (rest[1] === "list" && at("--conversation")) {
    record();
    const row = (s.conversations || []).find((c) => c.id === at("--conversation"));
    if (!row) die(4, JSON.stringify({ code: "CORE-404", detail: "conversation is unknown" }));
    out({ messages: row.messages.map((m) => ({ id: m.id, seq: m.seq, conversation_id: row.id, sender_id: m.sender === "self" ? s.self : row.peer, text: m.text, about_wi: m.about_wi, reply_to: m.reply_to, created_at: "2026-11-01T00:00:00Z" })), last_seq: row.lastSeq });
  }
  if (rest[1] === "list") {
    record();
    const rows = (s.conversations || []).map((c) => ({
      id: c.id, kind: "direct", members: [s.self, c.peer].sort(), created_at: "2026-11-01T00:00:00Z",
      last_seq: c.lastSeq, unread: c.messages.filter((m) => m.sender !== "self" && m.seq > (s.acked[c.id] || 0)).length,
      ...(c.peerName === null ? {} : { member_names: { [s.self]: s.selfName, [c.peer]: c.peerName } }),
    }));
    out({ conversations: rows });
  }
  if (rest[1] === "ack") {
    record();
    const fresh = readState();
    const c = (fresh.conversations || []).find((x) => x.id === rest[2]);
    if (!c) die(4, JSON.stringify({ code: "CORE-404", detail: "conversation is unknown" }));
    fresh.acked[c.id] = Math.max(fresh.acked[c.id] || 0, at("--seq") !== undefined ? Number(at("--seq")) : c.lastSeq);
    saveState(fresh);
    out({ ok: true });
  }
  if (rest[1] === "send") {
    record();
    const fresh = readState();
    const to = at("--to"); const text = at("--text");
    if (!to || !text) die(2, "pf: --to and --text are required");
    let c = fresh.conversations.find((x) => x.peer === to);
    if (!c) {
      const user = (fresh.users || []).find((x) => x.id === to);
      c = { id: "conv_new_" + Date.now(), peer: to, peerName: user ? user.display_name : "", messages: [], lastSeq: 0 };
      fresh.conversations.push(c);
    }
    const row = { id: "msg_sent_" + (c.lastSeq + 1), seq: c.lastSeq + 1, sender: "self", text, about_wi: at("--about-wi") || "", reply_to: at("--reply-to") || "" };
    c.messages.push(row); c.lastSeq = row.seq;
    fresh.sends.push({ to, text });
    saveState(fresh);
    out({ id: row.id, seq: row.seq, conversation_id: c.id, sender_id: s.self, text: row.text, about_wi: row.about_wi, reply_to: row.reply_to });
  }
  die(2, "pf: msg: unknown action " + rest[1]);
}
die(2, "pf: unknown command " + rest.join(" "));
`);
chmodSync(join(stubDir, "pf"), 0o755);
const calls = () => (existsSync(argvLog) ? readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean) : [])
  .reduce((acc, line) => {
    try {
      const argv = JSON.parse(line);
      if (Array.isArray(argv) && argv[0] === "__seed__") return [];
      if (Array.isArray(argv)) acc.push(argv.filter((part) => part !== "--json"));
    } catch { /* torn append */ }
    return acc;
  }, []);
const membershipCalls = () => calls().filter((a) => a[0] === "projects" && a[1] === "members");
const projectGetCalls = () => calls().filter((a) => a[0] === "projects" && a[1] === "get");
const readState = () => JSON.parse(readFileSync(stateFile, "utf8"));
const appearsIn = (haystack, needle) => haystack.some((value) => (value || "").includes(needle));

let base;
// A page whose storage refuses the collapse key, as a full quota would.
const refuseKey = (key) => {
  const original = Storage.prototype.setItem;
  Storage.prototype.setItem = function setItem(name, value) {
    if (name === key) throw new DOMException("quota exceeded", "QuotaExceededError");
    return original.call(this, name, value);
  };
};
const h = createHarness({
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN" },
  errors: [],
  session: SESSION,
  ready: "E2E pf groups host session",
  server(name, version) {
    const state = readState(); if (version) { state.version = version; save(state); }
    return { name, logFile: join(work, `server-${name}.log`), env: { PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: work,
      PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work } };
  },
  started: (started) => { base = started.base; },
});
const { openPage, startServer, withPage } = h;
const get = (path) => fetch(`${base}${path}`, { signal: AbortSignal.timeout(60_000) }).then(async (r) => ({ status: r.status, text: await r.text() }));
const postRaw = (path, body, headers) => fetch(`${base}${path}`, {
  method: "POST", headers, body, signal: AbortSignal.timeout(60_000),
}).then(async (r) => ({ status: r.status, text: await r.text() }));
const postJson = (path, body) => postRaw(path, JSON.stringify(body ?? {}), { "content-type": "application/json" });
const tab = (page, name) => dock(page).getByRole("tab", { name }).or(dock(page).getByRole("button", { name })).first();
const unreadDots = (root) => root.locator('[aria-label*="unread" i], [aria-label*="未读"], [title*="unread" i], [title*="未读"], [data-testid="unread-dot"], [data-unread-dot]');
async function openDock(page) { await pfButton(page).click(); await dock(page).waitFor(); return dock(page); }
async function messagesTab(page) { const t = tab(page, /^(messages|消息|訊息)/i); await t.waitFor({ timeout: 15_000 }); await t.click(); return t; }

const sectionDirect = (panel) => panel.getByText("单聊", { exact: true }).first();
const sectionGroups = (panel) => panel.getByText("群聊", { exact: true }).first();
const unknownText = (panel) => panel.getByText("未知用户", { exact: true });
const historyRegion = (panel) => panel.getByLabel(/会话记录|Conversation history/).first();
const messageTextField = (panel) => panel.getByLabel("消息", { exact: true }).first();
const sendButton = (panel) => panel.getByRole("button", { name: /^发送$|^Send$/i }).first();
const listColumn = (panel) => panel.locator("#pf-messages-list").first();
const ADD_WORDS = /add|添加|新增|邀请/i;
const REMOVE_WORDS = /remove|移除|移出|删除/i;
const groupHeader = (panel, project) => panel.locator("button[aria-expanded]").filter({ hasText: project }).first();
const addControl = (panel, project) => panel.getByRole("button", { name: new RegExp(`(?:${ADD_WORDS.source})[^]*${project}|${project}[^]*(?:${ADD_WORDS.source})`, "i") }).first();
const removeControl = (panel, project, name) => panel.getByRole("button", { name: new RegExp(`(?:${REMOVE_WORDS.source})[^]*${name}|${name}[^]*(?:${REMOVE_WORDS.source})`, "i") }).first();

// The users read (admin probe) and the groups' project reads start when the
// dock shows Messages (owner answer users-read-timing-vs-route-pins-223: B —
// the work-tab-163 / task-report-194 pins forbid them elsewhere), so UI that
// depends on them is awaited for a bounded time instead of counted at once.
const SETTLE_MS = 5_000;
async function eventually(locator) {
  await locator.first().waitFor({ timeout: SETTLE_MS }).catch(() => {});
  return locator.count();
}
async function requireVisible(locator, what) {
  assert.ok(await eventually(locator) > 0, `${what} is not rendered`);
  return locator.first();
}
// A bounded condition wait: polls `fn` every 150 ms until it
// holds or the deadline passes, then returns; the scenario's own assertion
// that follows reads the same predicate and states the failure.
async function settle(fn, ms = NAV_MS) {
  for (const deadline = Date.now() + ms; Date.now() < deadline; await delay(150)) {
    try { if (await fn()) return; } catch { /* not rendered yet */ }
  }
}
const leafBoxes = (panel, text) => panel.evaluate((dom, wanted) => [...dom.querySelectorAll("*")]
  .filter((node) => (node.textContent || "").trim() === wanted && ![...node.children].some((child) => (child.textContent || "").trim() === wanted))
  .map((node) => { const box = node.getBoundingClientRect(); return { x: box.x, y: box.y, width: box.width, height: box.height }; }), text);
const avatarElements = (root, initial) => root.evaluate((node, wanted) => {
  const hits = [];
  for (const el of node.querySelectorAll("*")) {
    if ((el.textContent || "").trim() !== wanted) continue;
    if ([...el.children].some((child) => (child.textContent || "").trim() === wanted)) continue;
    const box = el.getBoundingClientRect();
    if (box.width < 12 || box.width > 42 || Math.abs(box.width - box.height) > 10) continue;
    const bg = getComputedStyle(el).backgroundColor;
    if (!bg || bg === "transparent" || /rgba\(\s*0\s*,\s*0\s*,\s*0\s*,\s*0\s*\)/.test(bg)) continue;
    hits.push({ bg, x: box.x, y: box.y });
  }
  return hits;
}, initial);
const disclosure = (panel) => panel.evaluate((root) => {
  const labels = [];
  for (const node of root.querySelectorAll("*")) {
    for (const attribute of ["title", "aria-label"]) {
      const value = node.getAttribute(attribute);
      if (value) labels.push(value);
    }
  }
  return { text: root.innerText || root.textContent || "", labels };
});
async function headerCount(panel, project) {
  const header = await requireVisible(groupHeader(panel, project), `the ${project} group header`);
  const text = await header.evaluate((node) => `${node.getAttribute("aria-label") || ""} ${node.innerText || node.textContent || ""}`);
  const match = /(?:^|[^\d])(\d+)(?:[^\d]|$)/.exec(text);
  return match ? Number(match[1]) : null;
}
async function openAdd(panel, project) {
  const control = await requireVisible(addControl(panel, project), `the add control for ${project}`);
  await control.click();
  return control;
}
async function nextDialog(page, accept) {
  let resolveSeen;
  const seen = new Promise((resolve) => { resolveSeen = resolve; });
  const handler = async (dialog) => {
    const message = dialog.message();
    if (accept) await dialog.accept(); else await dialog.dismiss();
    resolveSeen(message);
  };
  page.once("dialog", handler);
  const missed = delay(8000).then(() => ({ missed: true }));
  const result = await Promise.race([seen, missed]);
  if (result && result.missed) throw new Error("no confirmation dialog appeared");
  return result;
}
const failures = [];
const check = createCheck({ failures, record: ([label], text) => `${label}: ${text}`, log: rawMessage });
let suiteError = null;
try {
  await h.launchBrowser(() => chromium.launch());
  await startServer("main");

  // S1: groups are exactly the mapped projects (name-sorted), a member of two
  // projects appears in both, a machine is never a row, and gamma (a real
  // project that is not mapped) creates no group.
  seed();
  await check("S1", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await requireVisible(sectionDirect(panel), "the 单聊 section");
    const alpha = await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
    const beta = await requireVisible(groupHeader(panel, "beta"), "the beta group header");
    const alphaBox = await alpha.boundingBox(); const betaBox = await beta.boundingBox();
    assert.ok(alphaBox && betaBox && alphaBox.y < betaBox.y, "alpha must render before beta (name ascending)");
    assert.equal(await groupHeader(panel, "gamma").count(), 0, "an unmapped project must create no group");
    const ava = await leafBoxes(panel, AVA.display_name);
    assert.equal(ava.length, 2, "a person in two mapped projects must appear once in each group");
    assert.ok(ava.some((box) => box.y > alphaBox.y && box.y < betaBox.y), "Ava must sit inside alpha");
    assert.ok(ava.some((box) => box.y > betaBox.y), "Ava must sit inside beta too");
    assert.equal(await panel.getByText(BEN.display_name, { exact: true }).count(), 1, "Ben belongs to alpha only");
    assert.equal(await panel.getByText(ZOE.display_name, { exact: true }).count(), 1, "Zoe belongs to beta only");
    assert.equal(await panel.getByText(MACHINE.display_name, { exact: true }).count(), 0, "a machine member must never render in a group");
  }));

  // S2: the direct area keeps its rows and unread badge, the 群聊 section keeps
  // its place at the bottom, and the same person's group row carries no unread.
  seed({ conversations: [AVA_CONV], acked: { conv_ava: 0 } });
  await check("S2", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    const direct = await requireVisible(sectionDirect(panel), "the 单聊 section");
    const header = await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
    const groups = await requireVisible(sectionGroups(panel), "the 群聊 section");
    const directBox = await direct.boundingBox(); const headerBox = await header.boundingBox(); const groupsBox = await groups.boundingBox();
    assert.ok(directBox && headerBox && groupsBox && directBox.y < headerBox.y && headerBox.y < groupsBox.y,
      "the order must stay: direct conversation rows, project groups, 群聊 last");
    const ava = await leafBoxes(panel, AVA.display_name);
    assert.ok(ava.length >= 2, "the peer with a conversation must also appear in its project group");
    assert.ok(ava.some((box) => box.y < headerBox.y), "the direct conversation row must stay above the groups");
    assert.equal(await unreadDots(listColumn(panel)).count(), 1, "the unread badge must appear only on the direct row, never duplicated in a group");
    assert.equal(await unreadDots(panel.getByText(AVA.display_name, { exact: true }).last()).count(), 0,
      "the group row for the same person must carry no unread badge");
  }));

  // S3: collapse persists across a reload; absent/corrupt storage and stale
  // entries read as expanded; a refused write keeps the in-session state.
  seed({ projects: { alpha: [BEN.id], beta: [ZOE.id] }, conversations: [] });
  await check("S3", async () => {
    const page = await openPage();
    try {
      await openDock(page); await messagesTab(page);
      const panel = dock(page);
      const alpha = await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
      assert.equal(await alpha.getAttribute("aria-expanded"), "true", "a group must start expanded");
      await alpha.click();
      assert.equal(await groupHeader(panel, "alpha").getAttribute("aria-expanded"), "false", "clicking the header must collapse the group");
      assert.equal(await panel.getByText(BEN.display_name, { exact: true }).count(), 0, "a collapsed group must hide its contacts");
      assert.ok(await panel.getByText(ZOE.display_name, { exact: true }).count() > 0, "the other group must stay visible");
      await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
      await page.getByText("E2E pf groups host session").first().waitFor({ state: "attached", timeout: NAV_MS });
      await openDock(page); await messagesTab(page);
      assert.equal(await groupHeader(dock(page), "alpha").getAttribute("aria-expanded"), "false", "the collapsed state must survive a reload");
      assert.equal(await groupHeader(dock(page), "beta").getAttribute("aria-expanded"), "true", "an untouched group stays expanded");
    } finally { await page.context().close(); }

    for (const [label, collapse] of [["absent", undefined], ["corrupt", "{not-json"], ["stale", JSON.stringify(["ghost-project"])]]) {
      const page2 = await openPage({ storage: { [COLLAPSE_KEY]: collapse } });
      try {
        await openDock(page2); await messagesTab(page2);
        const panel = dock(page2);
        for (const name of ["alpha", "beta"]) {
          assert.equal(await groupHeader(panel, name).getAttribute("aria-expanded"), "true",
            `${label} collapse storage must leave ${name} expanded`);
        }
        assert.ok(await panel.getByText(BEN.display_name, { exact: true }).count() > 0, `${label} storage must still render the contacts`);
      } finally { await page2.context().close(); }
    }

    const page3 = await openPage({ init: [[refuseKey, COLLAPSE_KEY]] });
    try {
      await openDock(page3); await messagesTab(page3);
      const panel = dock(page3);
      await (await requireVisible(groupHeader(panel, "alpha"), "the alpha group header with refused storage")).click();
      assert.equal(await groupHeader(panel, "alpha").getAttribute("aria-expanded"), "false",
        "a refused collapse write must still collapse for the page session");
      assert.ok(await panel.getByText(ZOE.display_name, { exact: true }).count() > 0, "the rest of the list must stay usable");
    } finally { await page3.context().close(); }
  });

  // S4: the header count is the number of rows shown and follows a removal.
  // Both counts are read after one bounded condition wait on the same
  // predicate (NAV_MS), not a fixed number of short polls.
  seed({ projects: { alpha: [AVA.id, BEN.id], beta: [ZOE.id] }, conversations: [] });
  await check("S4", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
    await settle(async () => (await headerCount(panel, "alpha")) === 2);
    assert.equal(await headerCount(panel, "alpha"), 2, "the alpha header must count its two contacts");
    const dialog = nextDialog(page, true);
    await (await requireVisible(removeControl(panel, "alpha", BEN.display_name), "Ben's remove control")).click();
    await dialog;
    await settle(async () => (await headerCount(panel, "alpha")) === 1);
    assert.equal(await headerCount(panel, "alpha"), 1, "the count must follow the removal");
    assert.equal(await panel.getByText(BEN.display_name, { exact: true }).count(), 0, "the removed contact must leave the group");
    assert.deepEqual(membershipCalls().slice(-1)[0], ["projects", "members", "remove", "alpha", "--user", BEN.id],
      "the removal must spawn the CLI's face");
  }));

  // S5: an empty mapped project keeps its group with a placeholder and still
  // offers add to an admin.
  seed({ projects: { alpha: [], beta: [ZOE.id] }, conversations: [] });
  await check("S5", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await requireVisible(groupHeader(panel, "alpha"), "the empty alpha group header");
    assert.equal(await headerCount(panel, "alpha"), 0, "an empty group must count zero");
    assert.ok(await panel.locator('[data-empty-group="alpha"]').count() > 0, "an empty mapped group must show an explicit placeholder");
    assert.ok(await eventually(addControl(panel, "alpha")) > 0, "an empty group must still offer add to an admin");
  }));

  // S6: a failed project read shows a note only in that group, other groups
  // render, and the Messages answer is 200.
  seed({ projectFail: "beta", conversations: [] });
  await check("S6", async () => {
    const answer = await get("/api/polyforge/messages");
    assert.equal(answer.status, 200, `the Messages answer must stay 200, got ${answer.status}`);
    const body = JSON.parse(answer.text);
    assert.ok(Array.isArray(body.groups), "the Messages answer must carry groups");
    const alpha = body.groups.find((group) => group.project === "alpha");
    const beta = body.groups.find((group) => group.project === "beta");
    assert.ok(alpha && alpha.contacts.some((contact) => contact.id === AVA.id), "alpha must answer its contacts");
    assert.ok(beta && typeof beta.error === "string" && beta.error.length > 0, "beta must carry its own failure note");
    await withPage(async (page) => {
      await openDock(page); await messagesTab(page);
      const panel = dock(page);
      assert.ok(await eventually(panel.getByText(/beta members are unavailable/)) > 0, "beta's failure note must render in the panel");
      assert.ok(await panel.getByText(AVA.display_name, { exact: true }).count() > 0, "alpha's contacts must render normally");
    });
  });

  // S7: a grouped contact opens its existing conversation; one without a
  // conversation enters the empty pane, creating no conversation and acking
  // nothing.
  seed({ conversations: [AVA_CONV], acked: { conv_ava: 0 } });
  await check("S7", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
    const ava = panel.getByText(AVA.display_name, { exact: true });
    assert.ok(await ava.count() >= 2, "Ava must be shown in the direct area and in alpha");
    await ava.last().click();
    await requireVisible(historyRegion(panel), "the existing conversation history");
    await panel.getByText("AVA-HELLO", { exact: false }).first().waitFor({ timeout: 10_000 }).catch(() => {});
    assert.ok(await panel.getByText("AVA-HELLO", { exact: false }).count() > 0, "clicking the group row must open Ava's existing conversation");
    // Ava's displayed unread page is auto-acked by its own
    // spawn, which lands late under load; count acks only once it has, so the
    // contact click below is measured alone (bounded condition wait).
    const avaAcked = () => calls().some((a) => a[0] === "msg" && a[1] === "ack" && a[2] === AVA_CONV.id);
    for (const deadline = Date.now() + 20_000; !avaAcked() && Date.now() < deadline;) await delay(150);
    assert.ok(avaAcked(), "opening Ava's unread conversation must auto-ack it before the contact click is measured");
    const before = readState().conversations.length;
    const ackCalls = () => calls().filter((a) => a[0] === "msg" && a[1] === "ack");
    // Opening Ava's conversation legitimately auto-acks it. Under the parallel
    // bar that ack can finish after this point, so wait for it to settle
    // (bounded) and then judge only acks that do not belong to the opened
    // conversation: Ben's click must add none.
    for (let i = 0; i < 100 && !ackCalls().some((call) => call[2] === AVA_CONV.id); i++) await delay(100);
    await (await requireVisible(panel.getByText(BEN.display_name, { exact: true }), "Ben's group row")).click();
    for (let i = 0; i < 40 && await leafBoxes(panel, BEN.display_name).then((boxes) => boxes.length) < 2; i++) await delay(150);
    assert.ok((await leafBoxes(panel, BEN.display_name)).length >= 2, "clicking a contact without a conversation must open the empty pane with its header");
    assert.equal(readState().conversations.length, before, "a click must create no conversation");
    assert.deepEqual(ackCalls().filter((call) => call[2] !== AVA_CONV.id), [], "a contact click must ack nothing");
  }));

  // S8: management is enabled only when the users read succeeds; a refused
  // probe leaves the groups read-only with an explicit needs-admin notice.
  seed();
  await check("S8", async () => {
    await withPage(async (page) => {
      await openDock(page); await messagesTab(page);
      const panel = dock(page);
      await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
      assert.ok(await eventually(addControl(panel, "alpha")) > 0, "a successful users read must enable add");
      assert.ok(await eventually(removeControl(panel, "alpha", BEN.display_name)) > 0, "a successful users read must enable remove");
    });
    seed({ usersFail: true });
    await withPage(async (page) => {
      await openDock(page); await messagesTab(page);
      const panel = dock(page);
      await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
      assert.ok(await eventually(panel.getByText(/管理员|admin|管理員/i)) > 0, "a refused probe must show an explicit needs-admin notice");
      assert.equal(await addControl(panel, "alpha").count(), 0, "a refused probe must not offer add");
      assert.equal(await removeControl(panel, "alpha", BEN.display_name).count(), 0, "a refused probe must not offer remove");
    });
  });

  // S9: the picker offers only human non-members, add is always writer, and
  // the post-write re-read renders the new member before the next poll.
  // Read identity, not a wall clock: an in-page fetch wrapper
  // tags the list read the panel issues in the write's own continuation (it
  // runs in the microtasks after the write's answer is parsed, before any
  // timer can fire, so a poll can never be tagged), and from just before the
  // confirming click until the member is shown every untagged list read (a
  // poll) is held at the page's network layer, released in `finally`.
  // So the member can only appear from the post-write re-read; a panel without
  // that re-read fails at once instead of passing on the next poll.
  seed();
  await check("S9", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
    const avaBefore = await panel.getByText(AVA.display_name, { exact: true }).count();
    const benBefore = await panel.getByText(BEN.display_name, { exact: true }).count();
    await openAdd(panel, "alpha");
    await requireVisible(panel.getByText(NINA.display_name, { exact: true }), "the non-member Nina in the picker");
    assert.equal(await panel.getByText(AVA.display_name, { exact: true }).count(), avaBefore, "an existing member must be hidden from the picker, not repeated");
    assert.equal(await panel.getByText(BEN.display_name, { exact: true }).count(), benBefore, "an existing member must be hidden from the picker");
    assert.equal(await panel.getByText(MACHINE.display_name, { exact: true }).count(), 0, "the picker must never list a machine user");
    await page.evaluate((memberId) => {
      const probe = { armed: false, writeLanded: false, reread: "none", rereadHasMember: false };
      window.__pf223 = probe;
      const original = window.fetch;
      window.fetch = async (input, init) => {
        const path = new URL(String(input instanceof Request ? input.url : input), location.href).pathname;
        const reread = path === "/api/polyforge/messages" && probe.armed;
        if (reread) {
          probe.armed = false; probe.reread = "issued";
          init = { ...init, headers: { ...Object.fromEntries(new Headers(init?.headers ?? {})), "x-e2e-223-reread": "1" } };
        }
        const response = await original(input, init);
        if (path !== "/api/polyforge/projects/members" && !reread) return response;
        const parse = response.json.bind(response);
        response.json = async () => {
          const body = await parse();
          if (reread) {
            probe.reread = "landed";
            probe.rereadHasMember = (body?.groups ?? []).some((group) => group.project === "alpha" && (group.contacts ?? []).some((contact) => contact.id === memberId));
          } else {
            probe.writeLanded = true; probe.armed = true;
            setTimeout(() => { probe.armed = false; }, 0);
          }
          return body;
        };
        return response;
      };
    }, NINA.id);
    let releasePolls; let heldPolls = 0;
    const pollsReleased = new Promise((resolve) => { releasePolls = resolve; });
    await page.route((url) => url.pathname === "/api/polyforge/messages", async (route) => {
      if (!route.request().headers()["x-e2e-223-reread"]) { heldPolls += 1; await pollsReleased; }
      await route.continue().catch(() => { /* the page closed */ });
    });
    const probe = () => page.evaluate(() => window.__pf223);
    const dialog = nextDialog(page, true);
    let elapsed;
    try {
      await (await requireVisible(panel.getByText(NINA.display_name, { exact: true }), "Nina's picker row")).click();
      const message = await dialog;
      assert.ok(message.includes("alpha") && message.includes(NINA.display_name), "the confirmation must name the project and the user's display name");
      const started = Date.now();
      // Waits for the re-read to land and the member to show; ends early once
      // the write has landed with no re-read (held polls could never show it).
      await settle(async () => {
        const seen = await probe();
        if (seen.writeLanded && !seen.armed && seen.reread === "none") return true;
        return seen.reread === "landed" && await panel.getByText(NINA.display_name, { exact: true }).count() > 0;
      });
      elapsed = Date.now() - started;
      const seen = await probe();
      assert.ok(seen.writeLanded && seen.reread !== "none", `the add must be followed by a list re-read in the write's own continuation (probe ${JSON.stringify(seen)}, ${heldPolls} poll(s) held, ${elapsed} ms)`);
      assert.ok(await panel.getByText(NINA.display_name, { exact: true }).count() > 0, "the added member must appear");
      assert.ok(seen.reread === "landed" && seen.rereadHasMember, `the added member must appear from the post-write re-read, not the next poll (probe ${JSON.stringify(seen)}, ${heldPolls} poll(s) held, ${elapsed} ms)`);
    } finally {
      releasePolls();
    }
    assert.ok(readState().projects.alpha.includes(NINA.id), "the stub project must have gained Nina");
    assert.deepEqual(membershipCalls().slice(-1)[0], ["projects", "members", "add", "alpha", "--user", NINA.id, "--role", "writer"],
      "add must always spawn the writer argv");
    await requireVisible(groupHeader(panel, "alpha"), "the alpha header after the add");
    assert.equal(await headerCount(panel, "alpha"), 3, "the count must follow the add");
  }));

  // S10: removing a member keeps the direct conversation, its messages and
  // later sends.
  seed({ conversations: [BEN_CONV], acked: { conv_ben: 1 } });
  await check("S10", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
    const dialog = nextDialog(page, true);
    await (await requireVisible(removeControl(panel, "alpha", BEN.display_name), "Ben's remove control")).click();
    await dialog;
    for (let i = 0; i < 40 && await panel.getByText(BEN.display_name, { exact: true }).count() > 1; i++) await delay(150);
    assert.equal(await panel.getByText(BEN.display_name, { exact: true }).count(), 1, "the removed contact must leave the group but keep the direct row");
    assert.ok(!readState().projects.alpha.includes(BEN.id), "the membership must be gone");
    await (await requireVisible(panel.getByText(BEN.display_name, { exact: true }), "Ben's direct conversation row")).click();
    await requireVisible(historyRegion(panel), "Ben's conversation history after removal");
    await panel.getByText("BEN-HELLO", { exact: false }).first().waitFor({ timeout: 10_000 }).catch(() => {});
    assert.ok(await panel.getByText("BEN-HELLO", { exact: false }).count() > 0, "the conversation and its messages must survive removal");
    await messageTextField(panel).fill("AFTER-REMOVAL");
    await sendButton(panel).click();
    for (let i = 0; i < 20 && !readState().sends.length; i++) await delay(250);
    assert.equal(readState().sends.length, 1, "a later send to the removed member must still work");
    assert.equal(readState().sends[0].to, BEN.id, "the send must still address the existing conversation peer");
  }));

  // S11: both confirmations name the project and the display name; cancelling
  // changes nothing and spawns no membership verb.
  seed();
  await check("S11", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
    await openAdd(panel, "alpha");
    const addDialog = nextDialog(page, false);
    await (await requireVisible(panel.getByText(NINA.display_name, { exact: true }), "Nina's picker row")).click();
    const addMessage = await addDialog;
    assert.ok(addMessage.includes("alpha") && addMessage.includes(NINA.display_name), "the add confirmation must name alpha and Nina New");
    assert.equal(membershipCalls().length, 0, "cancelling the add must spawn no membership verb");
    assert.equal(await panel.getByText(NINA.display_name, { exact: true }).count(), 0, "cancelling the add must not show Nina");

    const removeDialog = nextDialog(page, false);
    await (await requireVisible(removeControl(panel, "alpha", BEN.display_name), "Ben's remove control")).click();
    const removeMessage = await removeDialog;
    assert.ok(removeMessage.includes("alpha") && removeMessage.includes(BEN.display_name), "the remove confirmation must name alpha and Ben Ortiz");
    assert.equal(membershipCalls().length, 0, "cancelling the removal must spawn no membership verb");
    assert.ok(await panel.getByText(BEN.display_name, { exact: true }).count() > 0, "cancelling must keep the member listed");
  }));

  // S12: the write route's gate/JSON/validation refusals all answer before any
  // pf membership spawn.
  seed();
  await check("S12", async () => {
    appendFileSync(argvLog, JSON.stringify(["__seed__"]) + "\n");
    const forbidden = await postRaw("/api/polyforge/projects/members", JSON.stringify({ action: "add", project: "alpha", user_id: NINA.id }),
      { "content-type": "application/json", origin: "http://evil.invalid", "sec-fetch-site": "cross-site" });
    assert.equal(forbidden.status, 403, `the authenticated-surface gate must answer 403, got ${forbidden.status}`);
    const wrongType = await postRaw("/api/polyforge/projects/members", JSON.stringify({ action: "add", project: "alpha", user_id: NINA.id }),
      { "content-type": "text/plain" });
    assert.equal(wrongType.status, 415, `a non-JSON body must answer 415, got ${wrongType.status}`);
    const unmapped = await postJson("/api/polyforge/projects/members", { action: "add", project: "gamma", user_id: NINA.id });
    assert.equal(unmapped.status, 400, `an unmapped project must answer 400, got ${unmapped.status}`);
    const invalid = await postJson("/api/polyforge/projects/members", { action: "add", project: "alpha", user_id: "not a user id" });
    assert.equal(invalid.status, 400, `an invalid user id must answer 400, got ${invalid.status}`);
    assert.equal(membershipCalls().length, 0, "none of the refusals may spawn a pf membership verb");
  });

  // S13: the users read exposes id + display name only, and the grouping answer
  // carries project names and human contact id/display name only.
  seed();
  await check("S13", async () => {
    const users = await get("/api/polyforge/users");
    assert.equal(users.status, 200, `the users read must answer 200, got ${users.status}`);
    for (const sentinel of [EMAIL_SENTINEL, ROLE_SENTINEL, TOKEN_SENTINEL]) {
      assert.ok(!users.text.includes(sentinel), `the users read must not carry ${sentinel}`);
    }
    assert.ok(!users.text.includes(USER_TYPE_SENTINEL), "the users read must not carry user_type");
    const usersBody = JSON.parse(users.text);
    assert.ok(Array.isArray(usersBody.users) && usersBody.users.length > 0, "the users read must list users");
    assert.ok(!usersBody.users.some((user) => user.id === MACHINE.id), "the users read must omit machine users");
    for (const user of usersBody.users) {
      assert.deepEqual(Object.keys(user).sort(), ["display_name", "id"], "a user row must carry exactly id and display_name");
    }

    const listed = await get("/api/polyforge/messages");
    assert.equal(listed.status, 200, `the Messages read must answer 200, got ${listed.status}`);
    const body = JSON.parse(listed.text);
    assert.ok(Array.isArray(body.groups) && body.groups.length > 0, "the Messages read must carry groups");
    for (const sentinel of [EMAIL_SENTINEL, ROLE_SENTINEL, TOKEN_SENTINEL, USER_TYPE_SENTINEL, MACHINE.id, MACHINE.display_name]) {
      assert.ok(!listed.text.includes(sentinel), `the grouping answer must not carry ${sentinel}`);
    }
    for (const group of body.groups) {
      assert.equal(typeof group.project, "string", "a group must be named by its project");
      assert.ok(Array.isArray(group.contacts), "a group must carry its contacts");
      for (const contact of group.contacts) {
        assert.deepEqual(Object.keys(contact).sort(), ["display_name", "id"], "a group contact must carry exactly id and display_name");
      }
    }
    assert.deepEqual(body.groups.map((group) => group.project), ["alpha", "beta"], "groups must be exactly the mapped projects");
  });

  // S14: grouping reads one `projects get` per mapped project, and a hanging
  // project read leaves the list answer within the contact-read bound with a
  // note only in that group.
  seed();
  await check("S14", async () => {
    appendFileSync(argvLog, JSON.stringify(["__seed__"]) + "\n");
    const answer = await get("/api/polyforge/messages");
    assert.equal(answer.status, 200, `the grouping read must answer 200, got ${answer.status}`);
    const gets = projectGetCalls().map((argv) => argv.join(" "));
    assert.ok(gets.includes("projects get alpha"), `the grouping read must spawn pf --json projects get alpha, got ${JSON.stringify(gets)}`);
    assert.ok(gets.includes("projects get beta"), `the grouping read must spawn pf --json projects get beta, got ${JSON.stringify(gets)}`);

    seed({ projectHang: "beta" });
    const started = Date.now();
    const hanging = await get("/api/polyforge/messages");
    const elapsed = Date.now() - started;
    assert.equal(hanging.status, 200, `a hanging project read must still answer 200, got ${hanging.status}`);
    assert.ok(elapsed < 7000, `the list answer took ${elapsed} ms: it must stay within the 2 s contact-read bound, not the hanging project`);
    const body = JSON.parse(hanging.text);
    const alpha = body.groups.find((group) => group.project === "alpha");
    const beta = body.groups.find((group) => group.project === "beta");
    assert.ok(alpha && alpha.contacts.length > 0, "the successful group must render normally");
    assert.ok(beta && typeof beta.error === "string" && beta.error.length > 0, "the hanging group must carry its failure note");
  });

  // S15: a typed refusal answers 4xx with pf's text, an unreachable pf answers
  // 200 with an error field, and the panel shows it without a false change.
  seed({ membersError: "CORE-422", conversations: [] });
  await check("S15", async () => {
    const refused = await postJson("/api/polyforge/projects/members", { action: "remove", project: "alpha", user_id: BEN.id });
    assert.ok(refused.status >= 400 && refused.status < 500, `a typed refusal must answer 4xx, got ${refused.status}`);
    assert.match(refused.text, /membership refused by aihub policy/, "the refusal must carry pf's own text");
    seed({ membersUnavailable: true });
    const unreachable = await postJson("/api/polyforge/projects/members", { action: "add", project: "alpha", user_id: NINA.id });
    assert.equal(unreachable.status, 200, `an unreachable pf must answer 200, got ${unreachable.status}`);
    assert.match(unreachable.text, /"error"\s*:\s*"[^"]+/, "the unreachable answer must carry an error field");

    seed({ membersError: "CORE-422" });
    await withPage(async (page) => {
      await openDock(page); await messagesTab(page);
      const panel = dock(page);
      await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
      const before = await headerCount(panel, "alpha");
      const dialog = nextDialog(page, true);
      await (await requireVisible(removeControl(panel, "alpha", BEN.display_name), "Ben's remove control")).click();
      await dialog;
      for (let i = 0; i < 40 && await panel.getByText(/membership refused by aihub policy/).count() === 0; i++) await delay(150);
      assert.ok(await panel.getByText(/membership refused by aihub policy/).count() > 0, "the panel must show pf's refusal explicitly");
      assert.equal(await headerCount(panel, "alpha"), before, "a refused write must leave the displayed membership unchanged");
      assert.ok(await panel.getByText(BEN.display_name, { exact: true }).count() > 0, "the member must stay listed after a refusal");
    });
  });

  // S16: a blank display name renders the unknown-user placeholder in groups
  // and the picker, and no raw id appears in text, title or aria-label.
  seed({ projects: { alpha: [BLANK.id], beta: [ZOE.id] }, users: USERS(), conversations: [] });
  await check("S16", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await requireVisible(groupHeader(panel, "alpha"), "the alpha group header");
    assert.ok(await unknownText(panel).count() > 0, "a blank display name must render the unknown-user placeholder");
    assert.ok((await avatarElements(panel, "?")).length > 0, "a blank display name must render the ? avatar");
    const beforePicker = await unknownText(panel).count();
    await openAdd(panel, "alpha");
    for (let i = 0; i < 40 && await unknownText(panel).count() <= beforePicker; i++) await delay(150);
    assert.ok(await unknownText(panel).count() > beforePicker, "a blank-name picker candidate must render the unknown-user placeholder too");
    const shown = await disclosure(panel);
    for (const id of [BLANK.id, BLANK2.id, MACHINE.id, SELF]) {
      assert.ok(!shown.text.includes(id), `the panel text must not render the raw user id ${id}`);
      assert.ok(!appearsIn(shown.labels, id), `no title or aria-label may carry the raw user id ${id}`);
    }
  }));

  // S17: grouping, folding and management work at <=640px, and the mobile list
  // column keeps its no-handle behaviour.
  seed();
  await check("S17", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await requireVisible(groupHeader(panel, "alpha"), "the alpha group header on mobile");
    await requireVisible(groupHeader(panel, "beta"), "the beta group header on mobile");
    assert.ok(await listColumn(panel).count() > 0, "the list column must render at <=640px");
    assert.equal(await panel.locator('[data-resize-handle="polyforge-messages-list"]').count(), 0, "the mobile list column keeps no resize handle");
    const alpha = await requireVisible(groupHeader(panel, "alpha"), "the alpha header on mobile");
    await alpha.click();
    assert.equal(await groupHeader(panel, "alpha").getAttribute("aria-expanded"), "false", "a group must fold on mobile");
    assert.ok(await eventually(addControl(panel, "alpha")) > 0, "an admin must still get add on mobile");
    assert.ok(await panel.getByText(ZOE.display_name, { exact: true }).count() > 0, "the other group stays usable on mobile");
  }, { viewport: { width: 600, height: 900 } }));

  // S18: the new grouping and member-management copy resolves under each of
  // the three locales (a missing key would render a raw dotted key).
  seed({ usersFail: true });
  await check("S18", async () => {
    const cases = [
      ["en", /admin|administrator/i],
      ["zh-CN", /管理员/],
      ["zh-TW", /管理員/],
    ];
    for (const [locale, notice] of cases) {
      await withPage(async (page) => {
        await openDock(page); await messagesTab(page);
        const panel = dock(page);
        await requireVisible(groupHeader(panel, "alpha"), `the alpha group header under ${locale}`);
        assert.ok(await eventually(panel.getByText(notice)) > 0, `the needs-admin notice must resolve under ${locale}`);
        const shown = await disclosure(panel);
        assert.ok(!/polyforge\.messages\.[a-zA-Z]+/.test(shown.text), `no raw i18n key may render under ${locale}`);
      }, { locale, storage: { "pi-locale": locale } });
    }
  });

  // S19: the panel docs and the AGENTS.md File Map name the new routes, files
  // and the new collapse key.
  await check("S19", async () => {
    const panelDoc = readFileSync(join(root, "docs/agents/polyforge-panel.md"), "utf8");
    const agents = readFileSync(join(root, "AGENTS.md"), "utf8");
    assert.ok(panelDoc.includes("/api/polyforge/users"), "the panel docs must describe the users read face");
    assert.ok(panelDoc.includes("/api/polyforge/projects/members"), "the panel docs must describe the membership write route");
    assert.ok(panelDoc.includes(COLLAPSE_KEY), "the panel docs must name the collapse storage key");
    assert.ok(/groups/i.test(panelDoc), "the panel docs must describe the project grouping");
    assert.ok(agents.includes("polyforge/users/route.ts"), "the File Map must list the users route");
    assert.ok(agents.includes("polyforge/projects/members/route.ts"), "the File Map must list the membership route");
    assert.ok(agents.includes("polyforge-messages-collapse.ts"), "the File Map must list the collapse module");
    assert.ok(agents.includes(COLLAPSE_KEY), "the File Map must name the collapse storage key");
  });
} catch (error) {
  suiteError = error;
  console.error(error);
} finally {
  await h.close().catch(() => {});
}

if (suiteError) failures.push(`suite: ${suiteError?.message ?? suiteError}`);
if (check.count === 0) failures.push("suite: no scenario ran");
console.log("\n=== groups scenarios ===");
for (const line of failures) console.log(`FAIL ${line}`);
rmSync(work, { force: true, recursive: true });
process.exit(failures.length ? 1 : 0);