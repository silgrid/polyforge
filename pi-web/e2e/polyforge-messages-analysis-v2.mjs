// carries: app/api/polyforge/messages/analyze/route.ts, app/api/polyforge/messages/file-ticket/*, app/api/polyforge/polyforge-messages-routes.test.mjs
// carries: components/PolyforgeAnalysisCard*, components/PolyforgeFileTicketDialog*
// — pi-web message-analysis v2 acceptance e2e (authored BEFORE `code`).
//
// Drives the spec's user-visible behaviour through the real panel and the real
// routes against a stateful stub `pf`: the stub answers the task list/show, the
// structured analyze answer, the registration-ticket create and the existing
// Messages reads, and records every argv in a ledger. Each scenario prints
// `PASS S<n>` only when its THEN clause holds; the pre-implementation tree
// prints no PASS line.
//
// Scenario table (artifacts/e2e-scenarios.md mirrors it):
//   S1  received-only inline Analyze icon+text button, direct and group; one click one analysis
//   S2  per-message in-flight state: that message disabled, another message still starts, chat usable
//   S3  structured collapsible card (badge/model/duration/summary/task link/actions), old gray line gone
//   S4  closed type vocabulary localized in en/zh-CN/zh-TW; unknown actions dropped
//   S5  typed refusals (slot-full and terminal failure) in the message's own slot, no fabricated card
//   S6  【建票】 prefilled editable dialog; cancel writes nothing; confirm files one bundle-free registration ticket
//   S7  file-ticket POST route: 403/415/400 before spawn, one create, pf 4xx, 200 {error}, never 5xx, no body log
//   S8  【回复】 sets the target and prefills reply_draft, never sends, leaves the composer unchanged without a draft
//   S9  card lives in page memory: survives conversation switch, gone after reload and re-analyzable, no storage
//   S10 analysis floor 2.20.18: below it no spawn + upgrade text, above it one request and the card renders
//   S11 contacts strip: desktop ~28px and the ≤640px sheet's 40px strip, expand + total unread dot, #222 separator off, stored width restored, shared key
//   S12 shipped docs/File Map and en/zh-CN/zh-TW strings name the new surface, no retired key
//
//   node e2e/polyforge-messages-analysis-v2.mjs
import assert from "node:assert/strict";
import { appendFileSync, chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { polyforgeDock as dock, polyforgeToggle as pfButton } from "./polyforge-dock.mjs";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, rawMessage, root, suiteDist } from "./harness.mjs";

const distDir = suiteDist("polyforge-messages-analysis-v2");
assertNoDevServer(distDir);
rmSync(join(root, distDir), { force: true, recursive: true });

const work = mkdtempSync(join(tmpdir(), "pi-web-pf-analysis-v2-e2e-"));
if (process.env.KEEP_E2E_WORK) console.log(`work=${work}`);
const agentDir = join(work, "agent");
const project = join(work, "project");
const cliDir = join(work, ".config", "polyforge");
for (const dir of [join(agentDir, "sessions", "e2e"), project, cliDir]) mkdirSync(dir, { recursive: true });
const SESSION = "e2e-pf-analysis-v2-session";
const sessionFile = join(agentDir, "sessions", "e2e", `${SESSION}.jsonl`);
const ts = "2026-11-02T00:00:00.000Z";
writeFileSync(sessionFile, [
  { type: "session", version: 3, id: SESSION, timestamp: ts, cwd: project },
  { type: "message", id: "m1", parentId: null, timestamp: ts, message: { role: "user", content: "E2E pf analysis v2 host session" } },
].map((entry) => JSON.stringify(entry)).join("\n") + "\n");

const SELF = "u_e2e_252_self";
const SELF_NAME = "Self Operator 252";
const PEER = "u_e2e_252_peer";
const PEER_NAME = "Alice Chen";
const GROUP_PEER = "u_e2e_252_group_a";
const GROUP_NAME = "Team Alpha";
const OTHER_PEER = "u_e2e_252_other_peer";
const OTHER_NAME = "Olivia Other";
const TASK_ID = "task_252a1b2c3d4e5f60";
const MODEL = "pi:e2e/task-model";
const DURATION = "1m2.3s";

const OWN_TEXT = "OWN-MESSAGE-252";
const MESSAGE_A = "MSG-A-252 the design is ready";
const MESSAGE_B = "MSG-B-252 the scope should change";
const MESSAGE_C = "MSG-C-252 the parser is broken";
const MESSAGE_G = "MSG-GROUP-252 the group asks for a fix";
const MESSAGE_O = "MSG-OTHER-252 the other conversation asks a question";
const MESSAGE_NOTE = "MSG-NOTE-252 only a note is wanted";
const MESSAGE_DIALOG = "MSG-DIALOG-252 the dialog source message";

const SUMMARY_A = "SUMMARY-A-252 the peer asks for a design review.";
const SUMMARY_B = "SUMMARY-B-252 the peer argues for a different scope.";
const SUMMARY_C = "SUMMARY-C-252 the peer reports a bug in the parser.";
const SUMMARY_G = "SUMMARY-G-252 the group reports a parser bug.";
const SUMMARY_O = "SUMMARY-O-252 the peer asks a question.";
const SUMMARY_NOTE = "SUMMARY-NOTE-252 the peer only wants a note.";
const SUMMARY_DIALOG = "SUMMARY-DIALOG-252 the peer asks for a design review. More context follows.";
const DRAFT = "DRAFT-SENTINEL-252 please review the attached design";
const RETIRED_CONCLUSION = "RETIRED-CONCLUSION-SENTINEL-252";
const ROUTE_BODY = "ROUTE-BODY-SENTINEL-252";

const msg = (seq, sender, text, extra = {}) => ({ id: `msg_${seq}_${sender}`, seq, sender, text, about_wi: "", reply_to: "", ...extra });
const conversation = (id, peer, peerName, messages, extra = {}) => ({
  id, peer, peerName, messages, lastSeq: messages.length ? messages[messages.length - 1].seq : 0, ...extra,
});
const DIRECT = conversation("conv_direct", PEER, PEER_NAME, [
  msg(1, "peer", MESSAGE_A),
  msg(2, "self", OWN_TEXT),
  msg(3, "peer", MESSAGE_B),
  msg(4, "peer", MESSAGE_C),
  msg(5, "peer", MESSAGE_NOTE),
  msg(6, "peer", MESSAGE_DIALOG),
]);
const GROUP = conversation("conv_group", GROUP_PEER, GROUP_NAME, [msg(1, "peer", MESSAGE_G, { id: "msg_1_group" })],
  { kind: "group", members: [SELF, GROUP_PEER, "u_e2e_252_group_b"] });
const OTHER = conversation("conv_other", OTHER_PEER, OTHER_NAME, [msg(1, "peer", MESSAGE_O, { id: "msg_1_other" })]);

const answer = (messageId, patch = {}) => ({
  message_id: messageId,
  task_id: TASK_ID,
  project: "alpha",
  type: "requirement",
  summary: SUMMARY_A,
  suggested_actions: ["file_ticket"],
  model: MODEL,
  duration: DURATION,
  // A retired field is included on purpose: no surface may read or render it.
  conclusion: RETIRED_CONCLUSION,
  ...patch,
});
const ANSWERS = () => ({
  [DIRECT.messages[0].id]: answer(DIRECT.messages[0].id),
  [DIRECT.messages[2].id]: answer(DIRECT.messages[2].id, {
    type: "discussion", summary: SUMMARY_B, suggested_actions: ["reply"], reply_draft: DRAFT,
  }),
  [DIRECT.messages[3].id]: answer(DIRECT.messages[3].id, {
    type: "bug", summary: SUMMARY_C, suggested_actions: ["note_only"],
  }),
  [DIRECT.messages[4].id]: answer(DIRECT.messages[4].id, {
    type: "other", summary: SUMMARY_NOTE, suggested_actions: ["file_ticket"],
    reply_draft: "DROPPED-DRAFT-MUST-NOT-PREFILL",
  }),
  [DIRECT.messages[5].id]: answer(DIRECT.messages[5].id, {
    type: "requirement", summary: SUMMARY_DIALOG, suggested_actions: ["file_ticket"],
  }),
  [GROUP.messages[0].id]: answer(GROUP.messages[0].id, {
    type: "bug", summary: SUMMARY_G, suggested_actions: [],
  }),
  [OTHER.messages[0].id]: answer(OTHER.messages[0].id, {
    type: "clarification", summary: SUMMARY_O, suggested_actions: ["reply"], reply_draft: DRAFT,
  }),
});

const TASK_ROW = {
  id: TASK_ID, project: "alpha", state: "completed",
  prompt_summary: `Analyze message ${DIRECT.messages[0].id}`,
  candidate: MODEL, created_at: ts, started_at: ts, ended_at: ts,
  wall_consumed_ns: 62_300_000_000, tokens_consumed: 1_234, reporter: SELF_NAME, report_tail: "",
};
const TASK_DETAIL = {
  ...TASK_ROW,
  spec: { project: "alpha", prompt: `Analyze message ${DIRECT.messages[0].id}`, candidates: [MODEL], max_duration_ns: 300_000_000_000, max_tokens: 100_000 },
  report: `{"type":"requirement","summary":"${SUMMARY_A}"}`, report_len: 80, truncated: false,
};

const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const baseState = () => ({
  version: "v2.20.18", self: SELF, selfName: SELF_NAME,
  mapping: { "/srv/e2e": "alpha, beta" },
  projects: { alpha: [], beta: [] },
  users: [],
  conversations: [DIRECT, GROUP, OTHER],
  acked: { conv_direct: 6, conv_group: 1, conv_other: 1 },
  sends: [], fileCreates: [],
  answers: ANSWERS(),
  analyzeMode: "ok", analyzeFailCode: "DAEMON-500", analyzeFailDetail: "analysis task ended capped", analyzeDelayMs: 0,
  fileCreateMode: "ok", fileCreateError: "CORE-400", fileCreateErrorDetail: "goal is required",
  taskRows: [TASK_ROW], taskDetail: TASK_DETAIL,
});
const save = (state) => {
  const tmp = stateFile + ".tmp";
  writeFileSync(tmp, JSON.stringify(state));
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
const record = () => fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
const readState = () => JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const saveState = (state) => { const p = path.join(dir, "state.json"); fs.writeFileSync(p + ".tmp", JSON.stringify(state)); fs.renameSync(p + ".tmp", p); };
const out = (v) => { process.stdout.write(JSON.stringify(v) + "\\n"); process.exit(0); };
const die = (code, text) => { process.stderr.write(text + "\\n"); process.exit(code); };
const fail = (code, detail) => die(4, JSON.stringify({ code, detail }));
const s = readState();
if (args.includes("--version")) { record(); process.stdout.write(s.version + "\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
const at = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] : undefined; };
const conv = (id) => (s.conversations || []).find((c) => c.id === id);
const membersOf = (c) => Array.isArray(c.members) && c.members.length ? c.members : [s.self, c.peer];
const listRow = (c) => {
  const members = membersOf(c);
  const names = { [s.self]: s.selfName };
  for (const member of members) {
    if (member === s.self) continue;
    names[member] = member === c.peer ? c.peerName : "";
  }
  return {
    id: c.id, kind: c.kind || "direct", members, created_at: "2026-11-02T00:00:00Z",
    last_seq: c.lastSeq, unread: c.messages.filter((m) => m.sender !== "self" && m.seq > (s.acked[c.id] || 0)).length,
    ...(c.peerName === null ? {} : { member_names: names }),
  };
};
if (rest[0] === "ops" && rest[1] === "status") {
  record();
  out({ machine_id: "m-e2e-252", workspaces: ["/srv/e2e"], workflows_dir: "/srv/e2e/wf", env_names: [],
    jobs: [], queue: { pending: 0, running: 0, max_concurrent: 4 }, tasks: { running: 0, completed: 1, capped: 0, failed: 0, interrupted: 0 } });
}
if (rest[0] === "config" && rest[1] === "list") {
  record();
  const items = [
    { key: "server", value: "http://analysis.invalid:1", source: "file" },
    { key: "api_key", set: true, source: "file" },
    { key: "user_id", value: s.self, source: "file" },
  ];
  for (const workspace of Object.keys(s.mapping || {})) {
    items.push({ key: '[workspace."' + workspace + '"]', value: s.mapping[workspace], section: true, source: "file" });
  }
  out({ items });
}
if (rest[0] === "config" && rest[1] === "workspace") {
  record();
  const cwd = at("--cwd");
  out(cwd && s.mapping && Object.prototype.hasOwnProperty.call(s.mapping, cwd)
    ? { mapped: true, workspace: cwd } : { mapped: false, workspace: null });
}
if (rest[0] === "projects" && rest[1] === "get") {
  record();
  out({ project: rest[2], members: (s.projects[rest[2]] || []).map((id) => ({ user_id: id, user_type: "human" })) });
}
if (rest[0] === "users" && rest[1] === "list") { record(); out({ items: s.users || [] }); }
if (rest[0] === "task" && rest[1] === "list") { record(); out({ tasks: s.taskRows || [], now: new Date().toISOString() }); }
if (rest[0] === "task" && rest[1] === "show") { record(); out(s.taskDetail); }
if (rest[0] === "file" && rest[1] === "create") {
  record();
  if (s.fileCreateMode === "unavailable") die(1, "pf: daemon not reachable (connection refused)");
  if (s.fileCreateMode === "refuse") fail(s.fileCreateError, s.fileCreateErrorDetail);
  const fresh = readState();
  fresh.fileCreates = fresh.fileCreates || [];
  fresh.fileCreates.push({ project: at("--project"), goal: at("--goal"), content: at("--content"), bundle: args.includes("--bundle") });
  saveState(fresh);
  out({ slug: "task-252-e2e" });
}
if (rest[0] === "msg") {
  if (rest[1] === "contacts") { record(); out({ contacts: [] }); }
  if (rest[1] === "analyze") {
    record();
    if (s.analyzeMode === "fail") fail(s.analyzeFailCode || "DAEMON-500", s.analyzeFailDetail || "analysis failed");
    const id = rest[2];
    const body = (s.answers || {})[id] || { message_id: id, type: "other", summary: "no seeded answer", suggested_actions: [] };
    if (s.analyzeDelayMs > 0) { setTimeout(() => out(body), s.analyzeDelayMs); return; }
    out(body);
  }
  if (rest[1] === "list" && at("--conversation")) {
    record();
    const c = conv(at("--conversation"));
    if (!c) fail("CORE-404", "conversation is unknown");
    const before = Number(at("--before") || 0); const after = Number(at("--after") || 0); const limit = Number(at("--limit") || 50);
    let rows = c.messages.filter((m) => (!before || m.seq < before) && (!after || m.seq > after));
    if (before) rows = rows.slice(-limit); else rows = rows.slice(0, limit);
    out({ messages: rows.map((m) => ({
      id: m.id, seq: m.seq, conversation_id: c.id,
      sender_id: m.sender === "self" ? s.self : (m.sender_id || c.peer),
      text: m.text, about_wi: m.about_wi, reply_to: m.reply_to,
      created_at: "2026-11-02T00:00:00Z",
    })), last_seq: c.lastSeq });
  }
  if (rest[1] === "list") { record(); out({ conversations: (s.conversations || []).map(listRow) }); }
  if (rest[1] === "ack") {
    record();
    const fresh = readState();
    const c = (fresh.conversations || []).find((x) => x.id === rest[2]);
    if (!c) fail("CORE-404", "conversation is unknown");
    fresh.acked[c.id] = Math.max(fresh.acked[c.id] || 0, at("--seq") !== undefined ? Number(at("--seq")) : c.lastSeq);
    saveState(fresh);
    out({ ok: true });
  }
  if (rest[1] === "send") {
    record();
    const fresh = readState();
    const to = at("--to"); const text = at("--text");
    if (!to || !text) fail("CORE-400", "--to and --text are required");
    let c = fresh.conversations.find((x) => x.peer === to);
    if (!c) { c = { id: "conv_new_" + Date.now(), peer: to, peerName: "", messages: [], lastSeq: 0 }; fresh.conversations.push(c); }
    const row = { id: "msg_sent_" + (c.lastSeq + 1), seq: c.lastSeq + 1, sender: "self", text, about_wi: at("--about-wi") || "", reply_to: at("--reply-to") || "" };
    c.messages.push(row); c.lastSeq = row.seq;
    fresh.sends.push({ to, text });
    saveState(fresh);
    out({ id: row.id, seq: row.seq, conversation_id: c.id, sender_id: s.self, text: row.text, about_wi: row.about_wi, reply_to: row.reply_to });
  }
  die(2, "pf: msg: unknown action " + rest[1]);
}
die(2, "pf: unknown command");
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
const readState = () => JSON.parse(readFileSync(stateFile, "utf8"));
const msgCalls = () => calls().filter((a) => a[0] === "msg");
const analyzeCalls = () => msgCalls().filter((a) => a[1] === "analyze");
const sendCalls = () => msgCalls().filter((a) => a[1] === "send");
const createCalls = () => calls().filter((a) => a[0] === "file" && a[1] === "create");
const taskShowCalls = () => calls().filter((a) => a[0] === "task" && a[1] === "show");
const fileCreates = () => readState().fileCreates || [];
const argAfter = (argv, name) => { const i = argv.indexOf(name); return i >= 0 ? argv[i + 1] : undefined; };

// ---- scenario bookkeeping ----
const failures = [];
const check = createCheck({ failures, record: ([label], text) => `${label}: ${text}`, log: rawMessage });
const waitFor = createWaitFor({ ms: 20_000, every: 100, describe: (what, last) => `timed out waiting for ${what} (last: ${last})` });

let base;
const serverLogs = [];

const h = createHarness({
  viewport: { width: 1280, height: 900 },
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN" },
  timeout: 15_000,
  errors: [],
  session: SESSION,
  ready: "E2E pf analysis v2 host session",
  server(name, version) {
    const state = readState();
    if (version) { state.version = version; save(state); }
    const logFile = join(work, `server-${name}.log`);
    serverLogs.push(logFile);
    return { name, logFile, env: { PI_WEB_DIST_DIR: distDir, PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: work,
      PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work } };
  },
  started: (started) => { base = started.base; },
});
const { openPage, startServer, withPage } = h;
const postRaw = (path, body, headers) => fetch(`${base}${path}`, {
  method: "POST", headers, body, signal: AbortSignal.timeout(60_000),
}).then(async (r) => ({ status: r.status, text: await r.text() }));
const postJson = (path, body) => postRaw(path, JSON.stringify(body ?? {}), { "content-type": "application/json" });

const tab = (page, name) => dock(page).getByRole("tab", { name }).or(dock(page).getByRole("button", { name })).first();
const unreadDots = (root) => root.locator('[aria-label*="unread" i], [aria-label*="未读"], [title*="unread" i], [title*="未读"], [data-testid="unread-dot"], [data-unread-dot]');
async function openDock(page) { await pfButton(page).click(); await dock(page).waitFor(); return dock(page); }
async function messagesTab(page) { const t = tab(page, /^(messages|消息|訊息)/i); await t.waitFor({ timeout: 20_000 }); await t.click(); return t; }

const sectionDirect = (panel) => panel.getByText("单聊", { exact: true }).first();
const historyRegion = (panel) => panel.getByLabel(/会话记录|會話記錄|Conversation history/).first();
const messageTextField = (panel) => panel.getByLabel("消息", { exact: true }).first();
const analyzeButtons = (root) => root.getByRole("button", { name: /分析|Analyze/i });
const listSeparator = (panel) => panel.getByRole("separator", { name: /调整会话列表宽度|Resize conversation list/ }).first();
const collapseControl = (panel) => panel.getByRole("button", { name: /收起.*列|收起|折叠|collapse/i }).first();
const expandControl = (panel) => panel.getByRole("button", { name: /展开.*列|展开|expand/i }).first();

async function requireVisible(locator, what) {
  const count = await locator.count();
  assert.ok(count > 0, `${what} is not rendered`);
  return locator.first();
}

async function openConversation(page, conv) {
  const panel = dock(page);
  const needle = conv.messages.length ? conv.messages[conv.messages.length - 1].text : "";
  const labels = [conv.peerName, conv.peer].filter((label) => typeof label === "string" && label !== "");
  const historyShows = async () => needle !== "" && (await panel.getByText(needle, { exact: false }).count()) > 0;
  const deadline = Date.now() + 45_000;
  while (Date.now() < deadline) {
    if (await historyShows()) return;
    for (const label of labels) {
      const matches = panel.getByText(label, { exact: false });
      const count = await matches.count().catch(() => 0);
      for (let i = 0; i < Math.min(count, 6); i++) {
        await matches.nth(i).click({ timeout: 4000 }).catch(() => {});
        const settleUntil = Math.min(deadline, Date.now() + 15_000);
        while (Date.now() < settleUntil) {
          if (await historyShows()) return;
          await delay(100);
        }
      }
    }
  }
  throw new Error(`conversation ${conv.id} is not in the Messages list`);
}

const leafBoxes = (root, text) => root.evaluate((dom, wanted) => [...dom.querySelectorAll("*")]
  .filter((node) => (node.textContent || "").trim() === wanted && ![...node.children].some((child) => (child.textContent || "").trim() === wanted))
  .map((node) => { const box = node.getBoundingClientRect(); return { x: box.x, y: box.y, width: box.width, height: box.height }; }), text);
const leafWith = async (root, text) => {
  const boxes = await leafBoxes(root, text);
  assert.ok(boxes.length > 0, `the leaf ${text} is not rendered`);
  return boxes[0];
};
const bubbleFor = (history, page, text) => history.locator("div")
  .filter({ hasText: text })
  .filter({ has: page.getByRole("button", { name: /分析|Analyze/i }) })
  .last();
const cardFor = (history, page, summary) => history.locator("div")
  .filter({ hasText: summary })
  .filter({ has: page.getByRole("button", { name: /建票|File ticket/i }) })
  .last();
const cardShellFor = async (history, page, summary) => {
  const shell = history.locator("div")
    .filter({ hasText: summary })
    .filter({ has: page.getByRole("button", { name: /建票|File ticket/i }) })
    .filter({ has: page.locator("button[aria-expanded], a") })
    .last();
  return (await shell.count()) ? shell : cardFor(history, page, summary);
};
const styleSignature = (locator) => locator.evaluate((el) => {
  const css = getComputedStyle(el);
  return [css.backgroundColor, css.color, css.borderTopColor, css.borderTopWidth, css.fontWeight].join("|");
});
const listColumnWidth = (panel) => panel.evaluate((root) => {
  const list = root.querySelector("#pf-messages-list");
  if (list?.parentElement) return list.parentElement.getBoundingClientRect().width;
  const candidates = [...root.querySelectorAll("div")].filter((el) => {
    const box = el.getBoundingClientRect();
    return box.width > 16 && box.width <= 40 && box.height > 200;
  });
  return candidates.sort((a, b) => b.getBoundingClientRect().height - a.getBoundingClientRect().height)[0]?.getBoundingClientRect().width ?? 0;
});
const listColumnBox = (panel) => panel.evaluate((root) => {
  const strip = root.querySelector("[data-pf-messages-strip]");
  const column = strip?.parentElement ?? root.querySelector("#pf-messages-list")?.parentElement;
  if (!column) return null;
  const box = column.getBoundingClientRect();
  return { x: box.x, width: box.width, height: box.height, right: box.right };
});
const chatHistoryWidth = (panel) => panel.evaluate((root) => {
  const region = [...root.querySelectorAll("[aria-label]")].find((el) => /会话记录|Conversation history/.test(el.getAttribute("aria-label") || ""));
  return region ? region.getBoundingClientRect().width : 0;
});

try {
  await h.launchBrowser(() => chromium.launch());
  await startServer("main");

  // S1: received-only inline icon+text Analyze entry (direct and group).
  seed({ acked: { conv_direct: 6, conv_group: 1, conv_other: 1 } });
  await check("S1", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await openConversation(page, DIRECT);
    const history = historyRegion(panel);
    const buttons = analyzeButtons(history);
    const received = DIRECT.messages.filter((m) => m.sender !== "self").length;
    assert.equal(await buttons.count(), received, "every received message must carry exactly one Analyze button");
    const own = history.locator("div").filter({ hasText: OWN_TEXT }).last();
    assert.equal(await analyzeButtons(own).count(), 0, "the user's own message must carry no Analyze button");
    for (let i = 0; i < received; i += 1) {
      const button = buttons.nth(i);
      assert.ok(await button.isVisible(), "the Analyze entry must be visible");
      assert.ok((await button.locator("svg, img").count()) > 0, "the Analyze entry must carry an icon");
      assert.ok((await button.textContent() || "").trim().length > 0, "the Analyze entry must carry text");
    }
    const normalColor = await panel.getByText(PEER_NAME, { exact: true }).last().evaluate((el) => getComputedStyle(el).color);
    const buttonColor = await buttons.first().evaluate((el) => getComputedStyle(el).color);
    assert.equal(buttonColor, normalColor, "the Analyze entry must use the normal foreground colour, not the muted one");
    await buttons.first().click();
    await waitFor(() => analyzeCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "one analyze spawn for the clicked message", 10_000);
    assert.equal(analyzeCalls().length, 1, "one click must start exactly one analysis");

    await openConversation(page, GROUP);
    await waitFor(async () => (await analyzeButtons(historyRegion(panel)).count()) > 0, "the group conversation's Analyze entry");
    assert.equal(await analyzeButtons(historyRegion(panel)).count(), GROUP.messages.filter((m) => m.sender !== "self").length,
      "the group conversation must carry one Analyze button per received message");
  }));

  // S2: per-message in-flight state; another message still starts; chat usable.
  seed({ analyzeDelayMs: 4_000, acked: { conv_direct: 6, conv_group: 1, conv_other: 1 } });
  await check("S2", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await openConversation(page, DIRECT);
    const history = historyRegion(panel);
    const bubbleA = bubbleFor(history, page, MESSAGE_A);
    const bubbleC = bubbleFor(history, page, MESSAGE_C);
    const buttonA = analyzeButtons(bubbleA).first();
    const buttonC = analyzeButtons(bubbleC).first();
    await buttonA.click();
    await waitFor(() => analyzeCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "the first analysis spawn", 10_000);
    assert.equal(await buttonA.isDisabled(), true, "the in-flight message's control must be disabled");
    assert.equal(await buttonC.isEnabled(), true, "another message's control must stay enabled");
    await buttonA.click({ force: true }).catch(() => {});
    assert.equal(analyzeCalls().filter((a) => a[2] === DIRECT.messages[0].id).length, 1, "a second click on the in-flight message must spawn no second task");
    await buttonC.click();
    await waitFor(() => analyzeCalls().filter((a) => a[2] === DIRECT.messages[3].id).length === 1, "the second message's analysis spawn", 10_000);
    assert.equal(analyzeCalls().length, 2, "a second message must start while the first is in flight");
    await messageTextField(panel).fill("TYPED-WHILE-ANALYZING");
    assert.equal(await messageTextField(panel).inputValue(), "TYPED-WHILE-ANALYZING", "the chat must stay usable while an analysis waits");
  }));

  // S3: structured collapsible card, task link, action row; no conclusion.
  seed({ acked: { conv_direct: 6, conv_group: 1, conv_other: 1 } });
  await check("S3", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await openConversation(page, DIRECT);
    const history = historyRegion(panel);
    await analyzeButtons(bubbleFor(history, page, MESSAGE_A)).first().click();
    await waitFor(async () => (await panel.getByText(SUMMARY_A, { exact: false }).count()) > 0, "the structured card's summary");
    assert.ok(await panel.getByText("需求", { exact: true }).count() > 0, "the requirement badge must render localized");
    assert.ok(await panel.getByText(MODEL, { exact: false }).count() > 0, "the card must show the model");
    assert.ok((await panel.textContent() || "").includes(DURATION), "the card must show the duration");
    assert.equal(await panel.getByText(RETIRED_CONCLUSION, { exact: false }).count(), 0, "no surface may read or render the retired conclusion");
    const messageBox = await leafWith(history, MESSAGE_A);
    const summaryBox = await leafWith(history, SUMMARY_A);
    assert.ok(summaryBox.y > messageBox.y, "the card must sit under its message");
    const card = cardFor(history, page, SUMMARY_A);
    await requireVisible(card, "the analysis card");
    assert.ok(await card.getByRole("button", { name: /建票|File ticket/i }).count() > 0, "【建票】 must always be present");
    assert.ok(await card.getByRole("button", { name: /回复|Reply/i }).count() > 0, "【回复】 must always be present");
    const fileTicket = card.getByRole("button", { name: /建票|File ticket/i }).first();
    const reply = card.getByRole("button", { name: /回复|Reply/i }).first();
    assert.notEqual(await styleSignature(fileTicket), await styleSignature(reply), "the suggested action must be the primary style");
    // Collapse keeps the header and hides the body.
    const cardShell = await cardShellFor(history, page, SUMMARY_A);
    const collapse = cardShell.getByRole("button", { name: /收起|折叠|collapse/i })
      .or(cardShell.locator("button[aria-expanded='true']")).or(cardShell.locator("summary")).first();
    await requireVisible(collapse, "the card's collapse control");
    await collapse.click();
    await waitFor(async () => {
      const matches = panel.getByText(SUMMARY_A, { exact: false });
      return (await matches.count()) === 0 || !(await matches.first().isVisible().catch(() => false));
    }, "the collapsed card to hide its summary");
    assert.ok(await panel.getByText("需求", { exact: true }).count() > 0, "the collapsed card must keep its badge");
    assert.ok(await panel.getByText(MODEL, { exact: false }).count() > 0, "the collapsed card must keep its model");
    // Expand again, then the action matrix: reply-suggested and note_only.
    const expand = cardShell.getByRole("button", { name: /展开|expand/i })
      .or(cardShell.locator("button[aria-expanded='false']")).or(cardShell.locator("summary")).first();
    if (await expand.count()) await expand.click();
    await waitFor(async () => {
      const matches = panel.getByText(SUMMARY_A, { exact: false });
      return (await matches.count()) > 0 && (await matches.first().isVisible().catch(() => false));
    }, "the card to reopen");
    await analyzeButtons(bubbleFor(history, page, MESSAGE_B)).first().click();
    await waitFor(async () => (await panel.getByText(SUMMARY_B, { exact: false }).count()) > 0, "the reply-suggested card");
    const cardB = cardFor(history, page, SUMMARY_B);
    assert.notEqual(await styleSignature(cardB.getByRole("button", { name: /回复|Reply/i }).first()),
      await styleSignature(cardB.getByRole("button", { name: /建票|File ticket/i }).first()),
      "a suggested reply must be the primary style");
    await analyzeButtons(bubbleFor(history, page, MESSAGE_C)).first().click();
    await waitFor(async () => (await panel.getByText(SUMMARY_C, { exact: false }).count()) > 0, "the note-only card");
    const cardC = cardFor(history, page, SUMMARY_C);
    assert.equal(await styleSignature(cardC.getByRole("button", { name: /回复|Reply/i }).first()),
      await styleSignature(cardC.getByRole("button", { name: /建票|File ticket/i }).first()),
      "note_only must leave both actions secondary");
    // The task link opens that task's detail in Work › Tasks.
    const taskLink = cardShell.getByRole("link", { name: /任务|Task/i })
      .or(cardShell.getByRole("button", { name: /任务|Task/i })).first();
    await requireVisible(taskLink, "the card's task link");
    await taskLink.click();
    await waitFor(async () => (await panel.locator(`[data-pf-task="${TASK_ID}"]`).count()) > 0, "the Tasks tab row for the linked task", 20_000);
    await waitFor(() => taskShowCalls().some((a) => a[2] === TASK_ID), "the linked task's detail read");
  }));

  // S4: the closed type vocabulary renders localized in all three locales;
  // unknown action members are dropped rather than rendered.
  await check("S4", async () => {
    const cases = [
      ["en", "Discussion"],
      ["zh-CN", "讨论"],
      ["zh-TW", "討論"],
    ];
    for (const [locale, badge] of cases) {
      seed({ acked: { conv_direct: 6, conv_group: 1, conv_other: 1 }, answers: {
        ...ANSWERS(),
        [DIRECT.messages[2].id]: answer(DIRECT.messages[2].id, {
          type: "discussion", summary: SUMMARY_B, suggested_actions: ["reply", "bogus_action"], reply_draft: DRAFT,
        }),
      } });
      await withPage(async (page) => {
        await openDock(page); await messagesTab(page);
        const panel = dock(page);
        await openConversation(page, DIRECT);
        const history = historyRegion(panel);
        await analyzeButtons(bubbleFor(history, page, MESSAGE_B)).first().click();
        await waitFor(async () => (await panel.getByText(SUMMARY_B, { exact: false }).count()) > 0, `the ${locale} analysis card`);
        assert.ok(await panel.getByText(badge, { exact: true }).count() > 0, `the discussion badge must be ${badge} under ${locale}`);
        assert.equal(await panel.getByText("bogus_action", { exact: false }).count(), 0, "an unknown action member must be dropped");
        assert.ok(await panel.getByRole("button", { name: /回复|回覆|Reply/i }).count() > 0, "the kept reply action must render");
        assert.ok(!/polyforge\.messages\.analysisType/.test(await panel.textContent() || ""), "no raw i18n key may render");
      }, { locale, storage: { "pi-locale": locale } });
    }
  });

  // S5: typed refusals stay in the message's own slot and fabricate no card.
  seed({ acked: { conv_direct: 6, conv_group: 1, conv_other: 1 } });
  await check("S5", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await openConversation(page, DIRECT);
    const history = historyRegion(panel);
    const bubble = bubbleFor(history, page, MESSAGE_A);
    const state = readState();
    state.analyzeMode = "fail"; state.analyzeFailCode = "DAEMON-409";
    state.analyzeFailDetail = "2 tasks already running; nothing is queued";
    save(state);
    await analyzeButtons(bubble).first().click();
    await waitFor(async () => (await bubble.getByText(/2 tasks already running|nothing is queued/i).count()) > 0,
      "the slot-full refusal in the message's own slot", 10_000);
    assert.equal(await panel.getByText(SUMMARY_A, { exact: false }).count(), 0, "a refusal must not fabricate a card");
    assert.equal(await analyzeButtons(bubble).first().isEnabled(), true, "a slot-full refusal must leave the control available to retry");
    const terminal = readState();
    terminal.analyzeFailCode = "DAEMON-500"; terminal.analyzeFailDetail = "analysis task ended capped";
    save(terminal);
    await analyzeButtons(bubble).first().click();
    await waitFor(async () => (await bubble.getByText(/ended capped/i).count()) > 0,
      "the terminal-state refusal in the message's own slot", 10_000);
    assert.equal(await panel.getByText(SUMMARY_A, { exact: false }).count(), 0, "a terminal failure must not fabricate a type=other result");
    assert.equal(await analyzeButtons(bubble).first().isEnabled(), true, "a terminal refusal must leave the control available");
  }));

  // S6: 【建票】 dialog prefill, cancel, confirm, refusal.
  seed({ acked: { conv_direct: 6, conv_group: 1, conv_other: 1 } });
  await check("S6", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await openConversation(page, DIRECT);
    const history = historyRegion(panel);
    await analyzeButtons(bubbleFor(history, page, MESSAGE_DIALOG)).first().click();
    await waitFor(async () => (await panel.getByText(SUMMARY_DIALOG, { exact: false }).count()) > 0, "the dialog-source card");
    const card = cardFor(history, page, SUMMARY_DIALOG);
    await card.getByRole("button", { name: /建票|File ticket/i }).first().click();
    const dialog = page.getByRole("dialog").filter({ has: page.locator("select") }).last();
    await requireVisible(dialog, "the file-ticket confirmation dialog");
    const projectSelect = dialog.locator("select").first();
    const selected = await projectSelect.evaluate((el) => el.options[el.selectedIndex]?.textContent?.trim() ?? "");
    assert.equal(selected, "alpha", "the project select must default to the analysis's project");
    const fieldValues = await dialog.locator("input, textarea").evaluateAll((els) => els.map((el) => el.value));
    assert.ok(fieldValues.some((value) => value.includes("design review")), "goal must be prefilled from the analysis's suggested next line");
    assert.ok(fieldValues.some((value) => value.includes(SUMMARY_DIALOG) || value.includes(DIRECT.messages[5].id)),
      "content must be prefilled with the source message reference and the summary");
    const goalIndex = fieldValues.findIndex((value) => value.includes("design review"));
    const goalField = dialog.locator("input, textarea").nth(goalIndex);
    await goalField.fill("EDITED-GOAL-252");
    assert.equal(await goalField.inputValue(), "EDITED-GOAL-252", "every dialog field must be editable");
    await dialog.getByRole("button", { name: /取消|Cancel/i }).first().click();
    await waitFor(async () => (await dialog.count()) === 0 || !(await dialog.isVisible().catch(() => false)), "the cancelled dialog to close");
    assert.equal(createCalls().length, 0, "cancelling must spawn no pf file create");
    await card.getByRole("button", { name: /建票|File ticket/i }).first().click();
    const dialog2 = page.getByRole("dialog").filter({ has: page.locator("select") }).last();
    await requireVisible(dialog2, "the reopened file-ticket dialog");
    const values2 = await dialog2.locator("input, textarea").evaluateAll((els) => els.map((el) => el.value));
    const goalIndex2 = values2.findIndex((value) => value.includes("design review"));
    if (goalIndex2 >= 0) await dialog2.locator("input, textarea").nth(goalIndex2).fill("EDITED-GOAL-252");
    await dialog2.getByRole("button", { name: /确认|创建|建票|Create|Confirm/i }).first().click();
    await waitFor(() => createCalls().length === 1, "one pf file create on confirm", 15_000);
    const argv = createCalls()[0];
    assert.equal(argv.includes("--bundle"), false, "the confirm must create a registration ticket, never pass --bundle");
    assert.equal(argAfter(argv, "--project"), "alpha", "the create must carry the selected mapped project");
    assert.equal(argAfter(argv, "--goal"), "EDITED-GOAL-252", "the create must carry the edited goal");
    assert.ok((argAfter(argv, "--content") || "").includes(SUMMARY_DIALOG), "the create must carry the prefilled content");
    assert.equal(fileCreates().length, 1, "exactly one ticket must be created");
    await waitFor(async () => (await panel.getByText("task-252-e2e", { exact: false }).count()) > 0, "the dialog must report the created ticket slug");
    const refuse = readState(); refuse.fileCreateMode = "refuse"; save(refuse);
    await card.getByRole("button", { name: /建票|File ticket/i }).first().click();
    const dialog3 = page.getByRole("dialog").filter({ has: page.locator("select") }).last();
    await requireVisible(dialog3, "the refused file-ticket dialog");
    await dialog3.getByRole("button", { name: /确认|创建|建票|Create|Confirm/i }).first().click();
    await waitFor(async () => (await dialog3.getByText(/goal is required/i).count()) > 0, "pf's typed refusal in the dialog");
    assert.equal(await dialog3.isVisible(), true, "a refused create must keep the dialog open");
    assert.equal(createCalls().length, 2, "a refusal must not retry automatically");
    assert.equal(fileCreates().length, 1, "a refused create must create no ticket");
  }));

  // S7: the file-ticket POST route's gate, validation and failure contract.
  seed({ acked: { conv_direct: 6, conv_group: 1, conv_other: 1 } });
  await check("S7", async () => {
    const route = "/api/polyforge/messages/file-ticket";
    const valid = { goal: "E2E-ROUTE-GOAL", project: "alpha", content: ROUTE_BODY };
    const forbidden = await postRaw(route, JSON.stringify(valid),
      { "content-type": "application/json", origin: "http://evil.invalid", "sec-fetch-site": "cross-site" });
    assert.equal(forbidden.status, 403, `a cross-site request must answer 403, got ${forbidden.status}`);
    const wrongType = await postRaw(route, JSON.stringify(valid), { "content-type": "text/plain" });
    assert.equal(wrongType.status, 415, `a non-JSON body must answer 415, got ${wrongType.status}`);
    const blankGoal = await postJson(route, { goal: "   ", project: "alpha", content: "x" });
    assert.equal(blankGoal.status, 400, `a blank goal must answer 400, got ${blankGoal.status}`);
    const unmapped = await postJson(route, { goal: "g", project: "gamma", content: "x" });
    assert.equal(unmapped.status, 400, `an unmapped project must answer 400, got ${unmapped.status}`);
    const overBound = await postJson(route, { goal: "g".repeat(501), project: "alpha", content: "x" });
    assert.equal(overBound.status, 400, `a goal over the 500-character bound must answer 400, got ${overBound.status}`);
    assert.equal(createCalls().length, 0, "no validation refusal may spawn a pf file create");
    const created = await postJson(route, valid);
    assert.equal(created.status, 200, `a valid confirm must answer 200, got ${created.status}: ${created.text.slice(0, 120)}`);
    assert.match(created.text, /task-252-e2e/, "the valid create must answer the created slug");
    assert.equal(createCalls().length, 1, "a valid confirm must run exactly one create");
    assert.equal(createCalls()[0].includes("--bundle"), false, "the route must never pass --bundle");
    assert.equal(fileCreates().length, 1, "the valid route call must create one ticket");
    const refuse = readState(); refuse.fileCreateMode = "refuse"; save(refuse);
    const refused = await postJson(route, valid);
    assert.ok(refused.status >= 400 && refused.status < 500, `pf's typed refusal must keep a 4xx, got ${refused.status}`);
    assert.match(refused.text, /goal is required/i, "the route must surface pf's typed text");
    const unavailable = readState(); unavailable.fileCreateMode = "unavailable"; save(unavailable);
    const degraded = await postJson(route, valid);
    assert.equal(degraded.status, 200, `an unavailable daemon must answer 200 {error}, got ${degraded.status}`);
    assert.match(degraded.text, /error/i, "an unavailable daemon must answer an error field");
    for (const response of [forbidden, wrongType, blankGoal, unmapped, overBound, created, refused, degraded]) {
      assert.ok(response.status < 500, `no file-ticket answer may be 5xx, got ${response.status}`);
    }
    for (const logFile of serverLogs) {
      assert.ok(!readFileSync(logFile, "utf8").includes(ROUTE_BODY), "the route must log no request body");
    }
  });

  // S8: 【回复】 sets the target and prefills the draft; never sends.
  seed({ acked: { conv_direct: 6, conv_group: 1, conv_other: 1 } });
  await check("S8", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await openConversation(page, DIRECT);
    const history = historyRegion(panel);
    await analyzeButtons(bubbleFor(history, page, MESSAGE_B)).first().click();
    await waitFor(async () => (await panel.getByText(SUMMARY_B, { exact: false }).count()) > 0, "the reply-draft card");
    await messageTextField(panel).fill("EXISTING-COMPOSER-TEXT");
    const cardB = cardFor(history, page, SUMMARY_B);
    await cardB.getByRole("button", { name: /回复|Reply/i }).first().click();
    assert.equal(await messageTextField(panel).inputValue(), DRAFT, "【回复】 must fill the composer with reply_draft");
    assert.ok((await history.textContent() || "").includes(MESSAGE_B), "the reply target must quote the source message");
    assert.equal(sendCalls().length, 0, "【回复】 must never send");
    assert.ok(await panel.getByText(SUMMARY_B, { exact: false }).count() > 0, "【回复】 must keep the card");
    await analyzeButtons(bubbleFor(history, page, MESSAGE_NOTE)).first().click();
    await waitFor(async () => (await panel.getByText(SUMMARY_NOTE, { exact: false }).count()) > 0, "the no-draft card");
    await messageTextField(panel).fill("KEEP-ME");
    const cardNote = cardFor(history, page, SUMMARY_NOTE);
    await cardNote.getByRole("button", { name: /回复|Reply/i }).first().click();
    assert.equal(await messageTextField(panel).inputValue(), "KEEP-ME", "a card without reply_draft must not overwrite the composer");
    assert.ok((await history.textContent() || "").includes(MESSAGE_NOTE), "the reply target must still be set without a draft");
    assert.equal(sendCalls().length, 0, "no reply path may send a message");
  }));

  // S9: the card lives only in page memory.
  seed({ acked: { conv_direct: 6, conv_group: 1, conv_other: 1 } });
  await check("S9", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await openConversation(page, DIRECT);
    await analyzeButtons(bubbleFor(historyRegion(panel), page, MESSAGE_A)).first().click();
    await waitFor(async () => (await panel.getByText(SUMMARY_A, { exact: false }).count()) > 0, "the card before navigation");
    await openConversation(page, OTHER);
    await waitFor(async () => (await panel.getByText(SUMMARY_O, { exact: false }).count()) === 0, "the other conversation without the card");
    await openConversation(page, DIRECT);
    assert.ok(await panel.getByText(SUMMARY_A, { exact: false }).count() > 0, "the card must survive an in-page conversation switch");
    for (const store of ["localStorage", "sessionStorage"]) {
      const dump = await page.evaluate((name) => JSON.stringify(globalThis[name]), store);
      assert.ok(!dump.includes(SUMMARY_A), `the analysis result must not be written to ${store}`);
    }
    const cookies = JSON.stringify(await page.context().cookies());
    assert.ok(!cookies.includes(SUMMARY_A), "the analysis result must not be written to a cookie");
    await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
    await page.getByText("E2E pf analysis v2 host session").first().waitFor({ state: "attached", timeout: NAV_MS });
    await openDock(page); await messagesTab(page);
    await openConversation(page, DIRECT);
    assert.equal(await dock(page).getByText(SUMMARY_A, { exact: false }).count(), 0, "the card must be gone after a reload");
    const before = analyzeCalls().filter((a) => a[2] === DIRECT.messages[0].id).length;
    await analyzeButtons(bubbleFor(historyRegion(dock(page)), page, MESSAGE_A)).first().click();
    await waitFor(() => analyzeCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === before + 1,
      "a fresh analysis after the reload", 15_000);
    await waitFor(async () => (await dock(page).getByText(SUMMARY_A, { exact: false }).count()) > 0, "the re-derived card");
  }));

  // S10: the analysis floor is 2.20.18, separate from the Messages floor.
  seed({ acked: { conv_direct: 6, conv_group: 1, conv_other: 1 } });
  await check("S10", async () => {
    await h.stop();
    writeFileSync(argvLog, "");
    await startServer("analysis-floor", "v2.20.15");
    const page = await openPage();
    try {
      await openDock(page); await messagesTab(page);
      const panel = dock(page);
      await requireVisible(sectionDirect(panel), "the Messages list below the analysis floor");
      await openConversation(page, DIRECT);
      await analyzeButtons(bubbleFor(historyRegion(panel), page, MESSAGE_A)).first().click().catch(() => {});
      const outcome = await waitFor(async () => {
        if (analyzeCalls().length > 0) return "spawned";
        if (await panel.getByText(/upgrade|升级/i).count() > 0) return "upgrade";
        return "";
      }, "either an analyze spawn or the upgrade text below the analysis floor", 5_000);
      assert.equal(outcome, "upgrade", "below pf 2.20.18 the entry must spawn nothing and show the upgrade text");
      assert.equal(analyzeCalls().length, 0, "below the analysis floor no analyze spawn may happen");
    } finally { await page.context().close(); }
    await h.stop();
    writeFileSync(argvLog, "");
    await startServer("analysis-supported", "v2.20.18");
    const page2 = await openPage();
    try {
      await openDock(page2); await messagesTab(page2);
      const panel2 = dock(page2);
      await openConversation(page2, DIRECT);
      await analyzeButtons(bubbleFor(historyRegion(panel2), page2, MESSAGE_A)).first().click();
      await waitFor(() => analyzeCalls().length === 1, "one analyze spawn at the analysis floor");
      await waitFor(async () => (await panel2.getByText(SUMMARY_A, { exact: false }).count()) > 0, "the structured card at the analysis floor");
    } finally { await page2.context().close(); }
  });

  // S11: the contacts strip at every width, its own key, #222/#223 untouched.
  // the ≤640px sheet folds to its own 40px strip (the desktop
  // half below is unchanged).
  seed({ acked: { conv_direct: 0, conv_group: 1, conv_other: 1 } });
  await check("S11", async () => {
    const page = await openPage({ storage: { "pi-web:polyforge-messages-list-width": "200" } });
    try {
      await openDock(page); await messagesTab(page);
      const panel = dock(page);
      await openConversation(page, DIRECT);
      const expandedWidth = await listColumnWidth(panel);
      assert.ok(expandedWidth >= 195 && expandedWidth <= 205, `the stored #222 width must be restored (got ${expandedWidth})`);
      const expandedChat = await chatHistoryWidth(panel);
      assert.ok(await listSeparator(panel).count() > 0, "the #222 resize separator must exist while expanded");
      const collapse = await requireVisible(collapseControl(panel), "the collapse control");
      await collapse.click();
      await waitFor(async () => { const width = await listColumnWidth(panel); return width > 0 && width <= 40; }, "the collapsed strip width", 10_000);
      const stripWidth = await listColumnWidth(panel);
      assert.ok(stripWidth >= 20 && stripWidth <= 40, `the collapsed strip must be about 28px (got ${stripWidth})`);
      assert.ok(await expandControl(panel).count() > 0, "the collapsed strip must carry an expand control");
      assert.ok(await unreadDots(panel).count() > 0, "the collapsed strip must carry the total-unread dot");
      assert.ok(await chatHistoryWidth(panel) > expandedChat + 100, "the chat pane must take the remaining width");
      assert.equal(await listSeparator(panel).count(), 0, "the #222 separator must be inactive on a collapsed strip");
      const stored = await page.evaluate(() => ({
        collapsed: localStorage.getItem("pi-web:polyforge-messages-list-collapsed"),
        width: localStorage.getItem("pi-web:polyforge-messages-list-width"),
        groups: localStorage.getItem("pi-web:polyforge-messages-collapsed"),
      }));
      assert.ok(stored.collapsed !== null && stored.collapsed !== "false" && stored.collapsed !== "0", "the collapse state must use its own stored key");
      assert.equal(stored.width, "200", "collapsing must not write #222's width key");
      assert.equal(stored.groups, null, "collapsing the whole column must not touch #223's project-group key");
      await expandControl(panel).click();
      await waitFor(async () => { const width = await listColumnWidth(panel); return width >= 195 && width <= 205; }, "expanding to the stored #222 width", 10_000);
      assert.ok(await listSeparator(panel).count() > 0, "the #222 separator must return on expand");
    } finally { await page.context().close(); }

    // the sheet folds through the same key — at 560px and
    // 375px: collapse control ≥40×40 → 40px strip flush with the panel's
    // right edge → expand control ≥40×40 + dot + key "1" → expand restores
    // the fixed clamp.
    for (const width of [560, 375]) {
      const mobile = await openPage({ viewport: { width, height: 900 }, storage: { "pi-web:polyforge-messages-list-width": "300" } });
      try {
        await openDock(mobile); await messagesTab(mobile);
        const panel = dock(mobile);
        await openConversation(mobile, DIRECT);
        const expandedList = await listColumnWidth(panel);
        assert.ok(expandedList >= 120 && expandedList <= 220, `the expanded sheet must keep its fixed clamp at ${width}px (got ${expandedList})`);
        const expandedChat = await chatHistoryWidth(panel);
        const collapse = await requireVisible(collapseControl(panel), `the collapse control at ${width}px`);
        const collapseBox = await collapse.boundingBox();
        assert.ok(collapseBox && collapseBox.width >= 40 && collapseBox.height >= 40,
          `the mobile collapse control must be at least 40×40 at ${width}px (got ${collapseBox?.width}×${collapseBox?.height})`);
        await collapse.click();
        await waitFor(async () => { const w = await listColumnWidth(panel); return w >= 36 && w <= 44; }, `the mobile strip width at ${width}px`, 10_000);
        const stripBox = await listColumnBox(panel);
        const panelRight = await panel.evaluate((el) => el.getBoundingClientRect().right);
        assert.ok(stripBox && Math.abs(stripBox.right - panelRight) <= 1,
          `the mobile strip must be flush with the panel's right edge at ${width}px (strip ${stripBox?.right}, panel ${panelRight})`);
        const expand = await requireVisible(expandControl(panel), `the expand control at ${width}px`);
        const expandBox = await expand.boundingBox();
        assert.ok(expandBox && expandBox.width >= 40 && expandBox.height >= 40,
          `the mobile expand control must be at least 40×40 at ${width}px (got ${expandBox?.width}×${expandBox?.height})`);
        assert.ok(await unreadDots(panel).count() > 0, `the mobile strip must carry the total-unread dot at ${width}px`);
        const stored = await mobile.evaluate(() => ({
          collapsed: localStorage.getItem("pi-web:polyforge-messages-list-collapsed"),
          width: localStorage.getItem("pi-web:polyforge-messages-list-width"),
          groups: localStorage.getItem("pi-web:polyforge-messages-collapsed"),
        }));
        assert.equal(stored.collapsed, "1", `folding the sheet must write the shared key at ${width}px`);
        assert.equal(stored.width, "300", `folding the sheet must not write #222's width key at ${width}px`);
        assert.equal(stored.groups, null, `folding the sheet must not touch #223's project-group key at ${width}px`);
        assert.ok(await chatHistoryWidth(panel) >= expandedChat + expandedList - 44, `the chat pane must take the freed width at ${width}px`);
        await expand.click();
        await waitFor(async () => { const w = await listColumnWidth(panel); return w >= 120 && w <= 220; }, `the restored mobile clamp at ${width}px`, 10_000);
        assert.ok(await panel.getByRole("button", { name: /←|back|返回/i }).count() > 0, `the mobile sheet must keep its ← control at ${width}px`);
      } finally { await mobile.context().close(); }
    }
  });

  // S12: shipped documentation and the three locales name the new surface;
  // no retired `conclusion` key remains.
  await check("S12", async () => {
    const panelDoc = readFileSync(join(root, "docs/agents/polyforge-panel.md"), "utf8");
    const agents = readFileSync(join(root, "AGENTS.md"), "utf8");
    assert.ok(panelDoc.includes("/api/polyforge/messages/file-ticket"), "the panel docs must describe the file-ticket write route");
    assert.ok(panelDoc.includes("2.20.18"), "the panel docs must name the analysis floor");
    assert.ok(panelDoc.includes("pi-web:polyforge-messages-list-collapsed"), "the panel docs must name the collapse key");
    assert.ok(/analysis|card|分析/i.test(panelDoc), "the panel docs must describe the analysis card");
    for (const needle of [
      "app/api/polyforge/messages/file-ticket/route.ts",
      "components/PolyforgeAnalysisCard.tsx",
      "components/PolyforgeFileTicketDialog.tsx",
      "lib/polyforge-messages-list-collapse.ts",
    ]) assert.ok(agents.includes(needle), `the AGENTS.md File Map must list ${needle}`);
    const locales = {
      en: ["Requirement", "Discussion", "Other"],
      "zh-CN": ["需求", "讨论", "其他"],
      "zh-TW": ["需求", "討論", "其他"],
    };
    for (const [locale, words] of Object.entries(locales)) {
      const source = readFileSync(join(root, "lib/i18n/messages", `${locale}.ts`), "utf8");
      for (const key of [
        "polyforge.messages.analysisType.discussion",
        "polyforge.messages.analysisUpgrade",
        "polyforge.messages.analysisCollapse",
        "polyforge.messages.analysisExpand",
        "polyforge.messages.actionFileTicket",
        "polyforge.messages.ticketTitle",
        "polyforge.messages.collapseList",
        "polyforge.messages.expandList",
        "polyforge.messages.unreadTotal",
      ]) assert.ok(source.includes(`"${key}"`), `${locale} must carry ${key}`);
      for (const word of words) assert.ok(source.includes(`"${word}"`), `${locale} must carry the badge word ${word}`);
      assert.ok(!source.includes("polyforge.messages.conclusion"), `${locale} must carry no retired conclusion key`);
    }
  });
} catch (error) {
  console.error(error);
  failures.push(`suite: ${error?.message ?? error}`);
} finally {
  await h.close().catch(() => {});
}

if (check.count === 0) failures.push("suite: no scenario ran");
console.log("\n=== message-analysis v2 scenarios ===");
for (const line of failures) console.log(`FAIL ${line}`);
if (!process.env.KEEP_E2E_WORK) rmSync(work, { force: true, recursive: true });
process.exit(failures.length ? 1 : 0);
