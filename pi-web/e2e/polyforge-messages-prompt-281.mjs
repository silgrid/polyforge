// carries: app/api/polyforge/messages/prompt/route.ts
// carries: lib/draft-store.ts
// — message action 「@」 → execution prompt in the target composer.
//
// Authored BEFORE `code`, from artifacts/spec.md's Requirement scenarios and
// `## Acceptance criteria`. The suite drives only surface that exists on the
// pre-implementation tree: the Polyforge dock, the Messages tab, the real pi and
// claude composers, and a stateful stub `pf` on PATH. The new prompt route, the
// 「@」 action and its fill are asserted only through the user-visible facts the
// spec names (the glyph, the composer value, the feedback lines, no send), so a
// pre-code run is red at assertion level, never at build level. The daemon's
// internal task contract is pinned by code's Go tests instead of a browser e2e.
//
// Scenario table (artifacts/e2e-scenarios.md mirrors it):
//   S1  「@」 beside Analyze on every received and own message, none in the analysis card, localized
//   S2  pi composer: old draft + blank line + prompt, 「已填入 <会话名>」, no auto-send
//   S3  empty pi composer: exactly the prompt, no leading blank line
//   S4  claude composer: prompt appended, attached image and toolbar untouched, nothing sent
//   S5  split view: the focused pane's composer fills, the other pane keeps its draft
//   S6  unmounted bound composer: prompt lands in the session draft and survives reopening
//   S7  target pane closed before the result: under the message, never retargeted
//   S8  no composer at click time (a fresh server on an agent dir with no session and no mapped
//       project, so `/` shows no chat): under the message plus 「无目标会话，已显示在消息下」 (runs last)
//   S9  typed failure under the message, no fill, action reusable, later retry fills
//   S10 one run per message; different messages run concurrently and each result fills
//   S11 below the 2.20.20 floor: no spawn, upgrade line, action visible; at the floor one spawn fills
//   S12 shipped panel note, AGENTS.md File Map and zh-CN locale name the new surface
//   S13 <=640px: the sheet fills the current composer and stays open; fallback under the message, sheet open
//   S14 split view: a focused new-session pane alone receives the prompt, 已填入 names it
//
//   node e2e/polyforge-messages-prompt-281.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { polyforgeDock as dock, polyforgeToggle as pfButton } from "./polyforge-dock.mjs";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, rawMessage, root, suiteDist } from "./harness.mjs";

const distDir = suiteDist("polyforge-messages-prompt-281");
assertNoDevServer(distDir);
rmSync(join(root, distDir), { force: true, recursive: true });

const work = mkdtempSync(join(tmpdir(), "pi-web-pf-prompt-281-e2e-"));
if (process.env.KEEP_E2E_WORK) console.log(`work=${work}`);
const agentDir = join(work, "agent");
const project = join(work, "project");
const cliDir = join(work, ".config", "polyforge");
const claudeConfigDir = join(work, "claude-config");
const stubDir = join(work, "bin");
for (const dir of [join(agentDir, "sessions", "e2e"), project, cliDir, claudeConfigDir, stubDir]) mkdirSync(dir, { recursive: true });

const SESSION_A = "e2e-prompt-281-session-a";
const SESSION_B = "e2e-prompt-281-session-b";
const TITLE_A = "E2E prompt host session A";
const TITLE_B = "E2E prompt host session B";
// Every fixture time is relative to the run (an hour before it), never a
// fixed date: the sidebar lists only sessions active within its age window
// (DEFAULT_SESSION_AGE_FILTER_DAYS, 15 days) and session B is reached through
// its sidebar row, so a fixed date stops the suite 15 days after it was written.
const ts = new Date(Date.now() - 60 * 60 * 1000).toISOString();
const createdAt = ts.replace(/\.\d{3}Z$/, "Z");
const writePiSession = (id, title) => {
  const file = join(agentDir, "sessions", "e2e", `${id}.jsonl`);
  writeFileSync(file, [
    { type: "session", version: 3, id, timestamp: ts, cwd: project },
    { type: "message", id: "m1", parentId: null, timestamp: ts, message: { role: "user", content: title } },
  ].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
  return file;
};
const sessionFileA = writePiSession(SESSION_A, TITLE_A);
writePiSession(SESSION_B, TITLE_B);

const SELF = "u_e2e_281_self";
const SELF_NAME = "Self Operator 281";
const PEER = "u_e2e_281_peer";
const PEER_NAME = "Alice Chen";

const OWN_TEXT = "OWN-MESSAGE-281 the viewer's own message";
const MESSAGE_A = "MSG-A-281 please turn this into an execution prompt";
const MESSAGE_B = "MSG-B-281 the second message asks for a fix";
const MESSAGE_C = "MSG-C-281 the third message reports a bug";

const SUMMARY_A = "SUMMARY-A-281 the peer asks for an execution task.";
const PROMPT_HEAD = "PROMPT-281-FIXED";
const PROMPT = `${PROMPT_HEAD} execution prompt\nSecond line of the prompt.`;
const TASK_ID = "task_281a1b2c3d4e5f60";
const MODEL = "pi:e2e/prompt-model";
const DURATION = "1m2.3s";

const msg = (seq, sender, text, extra = {}) => ({ id: `msg_${seq}_281`, seq, sender, text, about_wi: "", reply_to: "", ...extra });
const DIRECT = {
  id: "conv_direct_281", peer: PEER, peerName: PEER_NAME,
  messages: [
    msg(1, "peer", MESSAGE_A),
    msg(2, "self", OWN_TEXT),
    msg(3, "peer", MESSAGE_B),
    msg(4, "peer", MESSAGE_C),
  ],
  lastSeq: 4,
};

const analyzeAnswer = (messageId) => ({
  message_id: messageId, task_id: TASK_ID, project: "alpha", type: "requirement",
  summary: SUMMARY_A, suggested_actions: ["file_ticket"], model: MODEL, duration: DURATION,
});

const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const baseState = () => ({
  version: "2.20.20", self: SELF, selfName: SELF_NAME,
  mapping: { [project]: "alpha, beta" },
  projects: { alpha: [], beta: [] },
  users: [],
  conversations: [DIRECT],
  acked: { [DIRECT.id]: DIRECT.lastSeq },
  sends: [],
  answers: { [DIRECT.messages[0].id]: analyzeAnswer(DIRECT.messages[0].id) },
  promptMode: "ok", promptDelayMs: 0,
  promptFailCode: "DAEMON-500", promptFailDetail: "prompt task ended capped",
  promptAnswer: { prompt: PROMPT, task_id: TASK_ID, model: MODEL, duration: DURATION },
});
const save = (state) => {
  const tmp = stateFile + ".tmp";
  writeFileSync(tmp, JSON.stringify(state));
  renameSync(tmp, stateFile);
};
const seed = (patch = {}) => {
  const next = { ...baseState(), ...patch };
  save(next);
  writeFileSync(argvLog, "");
  return next;
};
const patchState = (patch) => {
  const next = { ...JSON.parse(readFileSync(stateFile, "utf8")), ...patch };
  save(next);
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
    id: c.id, kind: c.kind || "direct", members, created_at: ${JSON.stringify(createdAt)},
    last_seq: c.lastSeq, unread: c.messages.filter((m) => m.sender !== "self" && m.seq > (s.acked[c.id] || 0)).length,
    ...(c.peerName === null ? {} : { member_names: names }),
  };
};
if (rest[0] === "ops" && rest[1] === "status") {
  record();
  out({ machine_id: "m-e2e-281", workspaces: Object.keys(s.mapping || {}), workflows_dir: Object.keys(s.mapping || {})[0] + "/wf", env_names: [],
    jobs: [], queue: { pending: 0, running: 0, max_concurrent: 4 }, tasks: { running: 0, completed: 1, capped: 0, failed: 0, interrupted: 0 } });
}
if (rest[0] === "config" && rest[1] === "list") {
  record();
  const items = [
    { key: "server", value: "http://prompt.invalid:1", source: "file" },
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
if (rest[0] === "task" && rest[1] === "list") { record(); out({ tasks: [], now: new Date().toISOString() }); }
if (rest[0] === "task" && rest[1] === "show") { record(); fail("DAEMON-404", "task is unknown"); }
if (rest[0] === "msg") {
  if (rest[1] === "contacts") { record(); out({ contacts: [] }); }
  if (rest[1] === "analyze") {
    record();
    const id = rest[2];
    out((s.answers || {})[id] || { message_id: id, type: "other", summary: "no seeded answer", suggested_actions: [] });
  }
  if (rest[1] === "prompt") {
    record();
    if (s.promptMode === "fail") fail(s.promptFailCode || "DAEMON-500", s.promptFailDetail || "prompt task ended capped");
    const body = s.promptAnswer;
    if (s.promptDelayMs > 0) { setTimeout(() => out(body), s.promptDelayMs); return; }
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
      created_at: ${JSON.stringify(createdAt)},
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

const stubLog = join(work, "claude-stub.jsonl");
const claudeStub = join(root, "e2e/claude-stub/claude.mjs");
writeFileSync(join(stubDir, "claude"), `#!/bin/sh\nexec ${JSON.stringify(process.execPath)} ${JSON.stringify(claudeStub)} "$@"\n`);
chmodSync(join(stubDir, "claude"), 0o755);

const calls = () => (existsSync(argvLog) ? readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean) : [])
  .flatMap((line) => {
    try {
      const argv = JSON.parse(line);
      return Array.isArray(argv) ? [argv.filter((part) => part !== "--json")] : [];
    } catch { return []; }
  });
const msgCalls = () => calls().filter((a) => a[0] === "msg");
const promptCalls = () => msgCalls().filter((a) => a[1] === "prompt");
const sendCalls = () => msgCalls().filter((a) => a[1] === "send");
const claudeRecords = () => (existsSync(stubLog) ? readFileSync(stubLog, "utf8").trim().split("\n").filter(Boolean) : [])
  .flatMap((line) => {
    try { return [JSON.parse(line)]; } catch { return []; }
  });
const claudeUserTexts = () => claudeRecords()
  .filter((entry) => entry.kind === "stdin" && typeof entry.line === "string")
  .flatMap((entry) => {
    try {
      const event = JSON.parse(entry.line);
      if (event?.type !== "user") return [];
      const content = event.message?.content;
      if (typeof content === "string") return [content];
      return Array.isArray(content) ? content.filter((block) => block?.type === "text").map((block) => String(block.text ?? "")) : [];
    } catch { return []; }
  });

const ONLY = (process.env.E2E_ONLY || "").split(",").filter(Boolean);
const failures = [];
const check = createCheck({ failures, record: ([label], text) => `${label}: ${text}`, log: rawMessage, skip: ([label]) => ONLY.length && !ONLY.includes(label) });
const waitFor = createWaitFor({ ms: 20_000, every: 100, describe: (what, last) => `timed out waiting for ${what} (last: ${last})` });

let base;
let firstLoad = true;
const serverOptions = (dirs = { home: work, agent: agentDir, claude: claudeConfigDir }) => ({
  name: "polyforge-messages-prompt-281",
  logFile: join(work, "server.log"),
  env: {
    PI_WEB_DIST_DIR: distDir,
    PATH: `${stubDir}${delimiter}${process.env.PATH}`,
    HOME: dirs.home, PI_CODING_AGENT_DIR: dirs.agent, CLAUDE_CONFIG_DIR: dirs.claude,
    PI_WEB_ALLOWED_ROOT_PREFIXES: work, PI_WEB_IDLE_TIMEOUT_MS: "15000",
    PI_E2E_CLAUDE_STUB_LOG: stubLog,
  },
});
const postJson = async (pathname, body) => {
  const response = await fetch(`${base}${pathname}`, {
    method: "POST", headers: { "content-type": "application/json" },
    body: JSON.stringify(body), signal: AbortSignal.timeout(60_000),
  });
  return { status: response.status, body: await response.json().catch(() => ({})) };
};
const createClaudeSession = async () => {
  const created = await postJson("/api/agent/new", {
    cwd: project, type: "ensure_session", harness: "claude", model: "claude-haiku-5-5",
  });
  assert.equal(created.status, 200, `POST /api/agent/new (claude ensure_session) -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const id = created.body.sessionId ?? created.body.id;
  assert.ok(typeof id === "string" && id.length > 0, "the create route returned no claude session id");
  return id;
};

// `locale` is both the context's locale and the page's language (default
// en-US / en); `split` turns split view on; `noChat` waits for the Polyforge
// toggle instead of the chat input.
const h = createHarness({
  viewport: { width: 1280, height: 900 },
  timeout: ACTION_MS,
  errors: [],
  session: SESSION_A,
  shape: (o) => ({ ...o, locale: o.locale ?? "en-US", storage: { ...o.storage, "pi-locale": o.locale ?? "en", "pi-web:split-view-enabled": o.split ? "true" : "false" } }),
  path: ({ session }) => `/?session=${encodeURIComponent(session)}`,
  gotoTimeout: () => { const timeout = firstLoad ? COLD_MS : NAV_MS; firstLoad = false; return timeout; },
  ready: (page, { noChat }) => (noChat
    ? pfButton(page)
    : page.locator(".chat-input-textarea").first()).waitFor({ state: "visible", timeout: NAV_MS }),
});
const { withPage } = h;

const tab = (page, name) => dock(page).getByRole("tab", { name }).or(dock(page).getByRole("button", { name })).first();
async function openDock(page) { await pfButton(page).click(); await dock(page).waitFor(); return dock(page); }
async function ensureMessages(page) {
  if (!(await dock(page).isVisible().catch(() => false))) await openDock(page);
  const messages = tab(page, /^(messages|消息|訊息)/i);
  await messages.waitFor({ timeout: 20_000 });
  if ((await messages.getAttribute("aria-selected")) !== "true") await messages.click();
  return dock(page);
}
async function messagesTab(page) { const t = tab(page, /^(messages|消息|訊息)/i); await t.waitFor({ timeout: 20_000 }); await t.click(); return t; }
const historyRegion = (panel) => panel.getByLabel(/会话记录|會話記錄|Conversation history/).first();
const atButtons = (root) => root.locator("button").filter({ hasText: "@" });
const analyzeButtons = (root) => root.getByRole("button", { name: /分析|Analyze/i });
const bubbleForText = (history, text) => history.locator("div").filter({ hasText: text }).last();
const cardFor = (history, page, summary) => history.locator("div")
  .filter({ hasText: summary })
  .filter({ has: page.getByRole("button", { name: /建票|File ticket/i }) })
  .last();
const composer = (page) => page.locator(".chat-input-textarea").first();

async function requireVisible(locator, what) {
  const count = await locator.count();
  assert.ok(count > 0, `${what} is not rendered`);
  return locator.first();
}
async function openConversation(page, conv = DIRECT) {
  const panel = dock(page);
  const needle = conv.messages[conv.messages.length - 1].text;
  const labels = [conv.peerName, conv.peer].filter((label) => typeof label === "string" && label !== "");
  const historyShows = async () => (await panel.getByText(needle, { exact: false }).count()) > 0;
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
const messageBubble = async (page, text) => {
  const panel = await ensureMessages(page);
  await openConversation(page);
  return bubbleForText(historyRegion(panel), text);
};
const clickAt = async (page, text) => {
  const bubble = await messageBubble(page, text);
  const button = await requireVisible(atButtons(bubble), `the 「@」 action on ${text}`);
  await button.click();
  return { panel: dock(page), bubble, button };
};
const paneComposers = (page) => page.locator("[data-split-pane-area] > div").locator(".chat-input-textarea");
const toolbarSignature = (page) => page.locator(".chat-input-controls").first().locator("button").evaluateAll((nodes) => nodes
  .map((node) => `${(node.getAttribute("aria-label") || "").trim()}|${(node.textContent || "").trim()}`));

const TINY_PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAFgwJ/lK3r9wAAAABJRU5ErkJggg==";
async function pasteImage(page) {
  await page.evaluate((encoded) => {
    const bytes = Uint8Array.from(atob(encoded), (char) => char.charCodeAt(0));
    const file = new File([bytes], "e2e-281.png", { type: "image/png" });
    const transfer = new DataTransfer();
    transfer.items.add(file);
    const textarea = document.querySelector(".chat-input-textarea");
    const event = new Event("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(event, "clipboardData", { value: transfer });
    textarea.dispatchEvent(event);
  }, TINY_PNG);
}
const imagePreviews = (page) => page.locator('.chat-input-shell img[src^="blob:"]');

try {
  ({ base } = await h.startServer(serverOptions()));
  await h.launchBrowser(() => chromium.launch());

  // S1: 「@」 on every message row including own, none in the analysis card,
  // localized in all three shipped locales.
  await check("S1", async () => {
    for (const locale of ["en", "zh-CN", "zh-TW"]) {
      seed();
      await withPage(async (page) => {
        await openDock(page); await messagesTab(page);
        const panel = dock(page);
        const history = historyRegion(panel);
        const bubbleA = await messageBubble(page, MESSAGE_A);
        const bubbleOwn = bubbleForText(history, OWN_TEXT);
        assert.equal(await atButtons(bubbleA).count(), 1, `${locale}: every received message must carry exactly one 「@」 action`);
        assert.equal(await atButtons(bubbleOwn).count(), 1, `${locale}: the viewer's own message must carry the 「@」 action too`);
        assert.equal(await analyzeButtons(bubbleOwn).count(), 0, `${locale}: the viewer's own message must still carry no Analyze`);
        const button = atButtons(bubbleA).first();
        assert.ok(await button.isVisible(), `${locale}: the 「@」 action must be visible`);
        assert.ok(((await button.textContent()) || "").includes("@"), `${locale}: the action must render the @ glyph`);
        assert.ok(((await button.getAttribute("aria-label")) || (await button.textContent()) || "").trim().length > 0,
          `${locale}: the action must carry a non-empty accessible name`);
        if (locale === "en") {
          await analyzeButtons(bubbleA).first().click();
          await waitFor(async () => (await panel.getByText(SUMMARY_A, { exact: false }).count()) > 0, "the analysis card under the message");
          const card = cardFor(history, page, SUMMARY_A);
          await requireVisible(card, "the analysis card");
          assert.equal(await atButtons(card).count(), 0, "the analysis card must not repeat the 「@」 action");
        }
      }, { locale });
    }
  });

  // S2: pi composer with an existing draft gets old text + one blank line + prompt,
  // reports the fill, and sends nothing.
  await check("S2", async () => {
    seed();
    await withPage(async (page) => {
      const draft = "DRAFT-SENTINEL-281 please review the design";
      await composer(page).fill(draft);
      const { panel, bubble } = await clickAt(page, MESSAGE_A);
      await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "one prompt task for the clicked message");
      await waitFor(async () => (await composer(page).inputValue()) === `${draft}\n\n${PROMPT}`, "the composer value old draft + blank line + prompt");
      assert.ok(await panel.getByText(/已填入/).first().isVisible(), "the fill must report 「已填入 <会话名>」 under the message");
      assert.equal(await bubble.getByText(/已填入/).count() > 0, true, "the feedback belongs to the filled message");
      assert.ok(((await bubble.getByText(/已填入/).first().textContent()) || "").includes(TITLE_A),
        "the 已填入 line must name the bound session (TITLE_A)");
      assert.equal(sendCalls().length, 0, "the fill must never run pf msg send");
      assert.equal(readFileSync(sessionFileA, "utf8").includes(PROMPT_HEAD), false, "no user turn carrying the prompt may reach the pi session");
    }, { locale: "zh-CN" });
  });

  // S3: an empty pi composer receives exactly the prompt itself, with no leading blank line.
  await check("S3", async () => {
    seed();
    await withPage(async (page) => {
      assert.equal(await composer(page).inputValue(), "", "the target composer must start empty");
      await clickAt(page, MESSAGE_B);
      await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[2].id).length === 1, "one prompt task for the second message");
      await waitFor(async () => (await composer(page).inputValue()) === PROMPT, "the empty composer to receive exactly the prompt");
      assert.equal((await composer(page).inputValue()).startsWith("\n"), false, "an empty draft must get no leading blank line");
      assert.equal(await sendCalls().length, 0, "nothing may be sent");
    }, { locale: "zh-CN" });
  });

  // S4: claude composer keeps its attached image and toolbar; nothing goes to the harness.
  await check("S4", async () => {
    seed();
    const claudeId = await createClaudeSession();
    await withPage(async (page) => {
      await pasteImage(page);
      await waitFor(async () => (await imagePreviews(page).count()) === 1, "the attached image preview in the claude composer");
      const beforeToolbar = await toolbarSignature(page);
      const beforeImage = await imagePreviews(page).count();
      await clickAt(page, MESSAGE_A);
      await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "one prompt task for the claude target");
      await waitFor(async () => (await composer(page).inputValue()) === PROMPT, "the claude composer to receive the prompt");
      assert.equal(await imagePreviews(page).count(), beforeImage, "the fill must not add, remove or re-encode the attached image");
      assert.deepEqual(await toolbarSignature(page), beforeToolbar, "the model/effort/compact toolbar must be unchanged");
      assert.equal(await sendCalls().length, 0, "the fill must never run pf msg send");
      assert.equal(claudeUserTexts().some((text) => text.includes(PROMPT_HEAD)), false, "no user prompt may reach the claude child");
    }, { session: claudeId, locale: "en" });
  });

  // S5: split view writes the focused pane's composer only.
  await check("S5", async () => {
    seed();
    await withPage(async (page) => {
      const paneA = paneComposers(page).nth(0);
      await paneA.fill("PANE-A-DRAFT");
      await page.locator(`[title="${TITLE_B}"]`).first().click();
      const tabs = page.locator("[data-split-pane-area]").getByRole("tab");
      await waitFor(async () => (await tabs.count()) === 2, "the second session pane");
      const paneB = paneComposers(page).nth(1);
      await paneB.waitFor({ state: "visible" });
      await paneB.fill("PANE-B-DRAFT");
      const { bubble } = await clickAt(page, MESSAGE_A);
      await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "one prompt task in split view");
      await waitFor(async () => (await paneComposers(page).nth(1).inputValue()) === "PANE-B-DRAFT\n\n" + PROMPT, "the focused pane's composer fill");
      assert.equal(await paneComposers(page).nth(0).inputValue(), "PANE-A-DRAFT", "the unfocused pane's composer must be untouched");
      await waitFor(async () => ((await bubble.getByText(/已填入/).first().textContent()) || "").includes(TITLE_B),
        "the 已填入 line to name the focused pane (TITLE_B)");
    }, { split: true, locale: "en" });
  });

  // S6: the bound session's composer unmounts before the answer; the session draft receives it.
  await check("S6", async () => {
    seed({ promptDelayMs: 2500 });
    await withPage(async (page) => {
      const draft = "DRAFT-A-281";
      await composer(page).fill(draft);
      const { bubble } = await clickAt(page, MESSAGE_A);
      await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "the delayed prompt task");
      await waitFor(async () => await atButtons(bubble).first().isDisabled(), "the action's in-flight state");
      await page.locator(`[title="${TITLE_B}"]`).first().click();
      await waitFor(async () => (await composer(page).inputValue()) === "", "the other session's composer");
      await waitFor(async () => await atButtons(bubble).first().isEnabled(), "the prompt result to settle", 30_000);
      await page.locator(`[title="${TITLE_A}"]`).first().click();
      await waitFor(async () => (await composer(page).inputValue()) === `${draft}\n\n${PROMPT}`, "the stored draft to survive reopening the session");
    }, { locale: "en" });
  });

  // S7: a target pane closed before the answer is not retargeted; the prompt lands under its message.
  await check("S7", async () => {
    seed({ promptDelayMs: 2500 });
    await withPage(async (page) => {
      await page.locator(`[title="${TITLE_B}"]`).first().click();
      const tabs = page.locator("[data-split-pane-area]").getByRole("tab");
      await waitFor(async () => (await tabs.count()) === 2, "the second session pane");
      await tabs.nth(0).click();
      const { bubble } = await clickAt(page, MESSAGE_A);
      await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "the delayed prompt task");
      await waitFor(async () => await atButtons(bubble).first().isDisabled(), "the action's in-flight state");
      await tabs.nth(0).getByRole("button", { name: "Close tab" }).click();
      await waitFor(async () => (await tabs.count()) === 1, "the target pane to close");
      await waitFor(async () => (await dock(page).getByText(PROMPT_HEAD, { exact: false }).count()) > 0, "the prompt under the clicked message", 30_000);
      assert.equal((await composer(page).inputValue()).includes(PROMPT_HEAD), false, "a closed target must not be retargeted to the surviving composer");
    }, { split: true, locale: "en" });
  });

  // S9: a typed failure is stated under the message, fills nothing and leaves the action reusable.
  await check("S9", async () => {
    seed({ promptMode: "fail" });
    await withPage(async (page) => {
      const { panel, bubble } = await clickAt(page, MESSAGE_A);
      await waitFor(async () => (await bubble.getByText(/prompt task ended capped/i).count()) > 0, "the typed failure under the message");
      assert.equal((await composer(page).inputValue()).includes(PROMPT_HEAD), false, "a failed task must fill nothing");
      assert.equal(await atButtons(bubble).first().isEnabled(), true, "a typed failure must leave the action usable");
      patchState({ promptMode: "ok" });
      await atButtons(bubble).first().click();
      await waitFor(async () => (await composer(page).inputValue()) === PROMPT, "the retry to fill the prompt");
      assert.equal(promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length, 2, "the retry must spawn exactly one new task");
      assert.ok(await panel.getByText(/已填入/).first().isVisible(), "the successful retry must report the fill");
    }, { locale: "en" });
  });

  // S10: one run per message while different messages run concurrently and each fills.
  await check("S10", async () => {
    seed({ promptDelayMs: 3000 });
    await withPage(async (page) => {
      const { bubble: bubbleA } = await clickAt(page, MESSAGE_A);
      const buttonA = atButtons(bubbleA).first();
      await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "the first message's prompt task");
      await waitFor(async () => await buttonA.isDisabled(), "the first message's in-flight state");
      await buttonA.click({ force: true }).catch(() => {});
      await delay(300);
      assert.equal(promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length, 1, "a second click on the running message must spawn no second task");
      const { bubble: bubbleB } = await clickAt(page, MESSAGE_B);
      await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[2].id).length === 1, "the different message's prompt task while the first runs");
      assert.equal(await atButtons(bubbleB).first().isDisabled(), true, "the different message's own in-flight state must start");
      await waitFor(async () => (await composer(page).inputValue()).split(PROMPT_HEAD).length - 1 === 2, "both results appended to the target", 30_000);
      assert.equal(await atButtons(bubbleA).first().isEnabled(), true, "the first action must be reusable after its result");
      assert.equal(await atButtons(bubbleB).first().isEnabled(), true, "the second action must be reusable after its result");
    }, { locale: "en" });
  });

  // S11: the floor gates before any spawn and leaves the action visible; at the floor one task fills.
  await check("S11", async () => {
    seed({ version: "2.20.19" });
    await withPage(async (page) => {
      const { bubble } = await clickAt(page, MESSAGE_A);
      await waitFor(async () => (await bubble.getByText(/2\.20\.20/).count()) > 0, "the below-floor upgrade line under the message");
      assert.equal(promptCalls().length, 0, "below the floor no prompt task may be spawned");
      assert.equal(await composer(page).inputValue(), "", "below the floor nothing may be filled");
      assert.ok(await atButtons(bubble).first().isVisible(), "the 「@」 action must stay visible below the floor");
      assert.equal(await atButtons(bubble).first().isEnabled(), true, "the action must remain usable after the floor refusal");
    }, { locale: "en" });
    seed({ version: "2.20.20" });
    await withPage(async (page) => {
      await clickAt(page, MESSAGE_A);
      await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "one prompt task at the floor");
      await waitFor(async () => (await composer(page).inputValue()) === PROMPT, "the at-floor task to fill the prompt");
    }, { locale: "en" });
  });

  // S13: at <=640px the Messages sheet fills the current page's composer and stays open.
  const MOBILE = { width: 390, height: 844 };
  await check("S13", async () => {
    seed();
    await withPage(async (page) => {
      const { bubble } = await clickAt(page, MESSAGE_A);
      await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "one prompt task from the mobile sheet");
      await waitFor(async () => (await composer(page).inputValue()) === PROMPT, "the current page's composer to receive the prompt");
      await waitFor(async () => ((await bubble.getByText(/已填入/).first().textContent()) || "").includes(TITLE_A), "the 已填入 line naming the session");
      assert.ok(await dock(page).isVisible(), "the Messages sheet must stay open after the fill");
    }, { locale: "zh-CN", viewport: MOBILE });
  });

  // S14: split view with a focused new-session pane: only that pane's composer receives the prompt.
  await check("S14", async () => {
    seed();
    await withPage(async (page) => {
      const tabs = page.locator("[data-split-pane-area]").getByRole("tab");
      await page.locator(`[title="${TITLE_B}"]`).first().click();
      await waitFor(async () => (await tabs.count()) === 2, "the second session pane");
      await paneComposers(page).nth(1).fill("PANE-B-DRAFT");
      // The sidebar lists the project's new-session "+" once the directory is added
      // (same path as split-pane-pass.mjs: wait for the picker's latest browse first).
      await page.getByRole("button", { name: "Add", exact: true }).click();
      await page.waitForFunction(() => {
        const submit = document.querySelector("#directory-path")?.closest("form")?.querySelector('button[type="submit"]');
        return Boolean(submit && !submit.disabled);
      }, null, { timeout: 30_000 });
      await page.locator("#directory-path").fill(project);
      await page.locator("#directory-path").press("Enter");
      await page.getByRole("button", { name: "Select this folder", exact: true }).click();
      const groupNewSession = page.getByRole("button", { name: `New session in ${project}`, exact: true });
      await groupNewSession.waitFor();
      await groupNewSession.click();
      await waitFor(async () => (await tabs.count()) === 3, "the new-session pane");
      await waitFor(async () => (await tabs.nth(2).getAttribute("aria-selected")) === "true", "the new-session pane to be focused");
      const newLabel = ((await tabs.nth(2).textContent()) || "").trim();
      const { bubble } = await clickAt(page, MESSAGE_A);
      await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "one prompt task for the new-session pane");
      await waitFor(async () => (await paneComposers(page).nth(2).inputValue()) === PROMPT, "the new-session pane's composer fill");
      assert.equal(await paneComposers(page).nth(1).inputValue(), "PANE-B-DRAFT", "the other pane's draft must be untouched");
      assert.equal((await paneComposers(page).nth(0).inputValue()).includes(PROMPT_HEAD), false, "the first pane must be untouched");
      const line = ((await bubble.getByText(/已填入/).first().textContent()) || "").replace(/^.*已填入\s*/, "").trim();
      assert.ok(line.length > 0 && newLabel.includes(line), `the 已填入 line must name the new-session pane (${line} vs ${newLabel})`);
    }, { split: true, locale: "en" });
  });

  // The no-composer paths (S8, S13's fallback) need a server whose `/` shows no chat.
  // `/` on the shared fixture opens the new-session composer of the seeded
  // project, which the spec counts as a target; so they restart the server on
  // an agent dir with no session and no mapped project (fresh HOME and claude
  // dir too), where `/` shows no chat at all. They run last for that reason.
  if (!ONLY.length || ONLY.includes("S8") || ONLY.includes("S13")) {
    const empty = join(work, "empty");
    const dirs = { home: join(empty, "home"), agent: join(empty, "agent"), claude: join(empty, "claude-config") };
    for (const dir of [join(dirs.agent, "sessions"), dirs.home, dirs.claude]) mkdirSync(dir, { recursive: true });
    await h.stop();
    ({ base } = await h.startServer(serverOptions(dirs)));
  }
  const noComposerClick = async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await openConversation(page);
    const bubble = bubbleForText(historyRegion(panel), MESSAGE_A);
    await requireVisible(atButtons(bubble), "the 「@」 action on a page without a composer").then((button) => button.click());
    await waitFor(() => promptCalls().filter((a) => a[2] === DIRECT.messages[0].id).length === 1, "one prompt task with no composer");
    await waitFor(async () => (await panel.getByText(PROMPT_HEAD, { exact: false }).count()) > 0, "the prompt under the message");
    assert.ok(await panel.getByText("无目标会话，已显示在消息下", { exact: true }).first().isVisible(),
      "the no-composer path must read 「无目标会话，已显示在消息下」");
    assert.equal(await sendCalls().length, 0, "nothing may be sent");
    assert.equal(await page.locator(".chat-input-textarea").count(), 0, "the fixture must have no composer to fill");
    return panel;
  };

  // S13 (fallback): at <=640px with no composer the prompt shows under the message and the sheet stays open.
  await check("S13", async () => {
    seed({ mapping: {}, projects: {} });
    await withPage(async (page) => {
      const panel = await noComposerClick(page);
      assert.ok(await panel.isVisible(), "the Messages sheet must stay open on the fallback path");
    }, { locale: "zh-CN", noChat: true, path: "/", viewport: MOBILE });
  });

  // S8: no composer at click time renders under the message with the no-target feedback line.
  // `/` on the shared fixture opens the new-session composer of the seeded
  // project, which the spec counts as a target; so S8 restarts the server on
  // an agent dir with no session and no mapped project (fresh HOME and claude
  // dir too), where `/` shows no chat at all. It runs last for that reason.
  await check("S8", async () => {
    seed({ mapping: {}, projects: {} });
    await withPage(noComposerClick, { locale: "zh-CN", noChat: true, path: "/" });
  });

  // S12: shipped docs, File Map and the zh-CN locale name the new surface.
  await check("S12", async () => {
    const panelDoc = readFileSync(join(root, "docs/agents/polyforge-panel.md"), "utf8");
    const agents = readFileSync(join(root, "AGENTS.md"), "utf8");
    const zhCn = readFileSync(join(root, "lib/i18n/messages/zh-CN.ts"), "utf8");
    assert.ok(panelDoc.includes("/api/polyforge/messages/prompt"), "the panel note must describe the prompt route");
    assert.ok(panelDoc.includes("@"), "the panel note must describe the 「@」 action");
    assert.ok(/bind|fill|draft|composer|fallback/i.test(panelDoc), "the panel note must cover binding, fill and fallbacks");
    assert.ok(panelDoc.includes("已填入"), "the panel note must name the filled feedback line");
    assert.ok(panelDoc.includes("无目标会话，已显示在消息下"), "the panel note must name the no-target line");
    assert.ok(agents.includes("app/api/polyforge/messages/prompt/route.ts"), "the AGENTS.md File Map must list the prompt route");
    assert.ok(zhCn.includes("已填入"), "zh-CN must carry the filled feedback line");
    assert.ok(zhCn.includes("无目标会话，已显示在消息下"), "zh-CN must carry the no-target feedback line");
  });
} catch (error) {
  console.error(error);
  failures.push(`suite: ${error?.message ?? error}`);
} finally {
  await h.close().catch(() => {});
}

if (check.count === 0) failures.push("suite: no scenario ran");
console.log("\n=== message prompt @ scenarios ===");
for (const line of failures) console.log(`FAIL ${line}`);
if (!process.env.KEEP_E2E_WORK) rmSync(work, { force: true, recursive: true });
process.exit(failures.length ? 1 : 0);