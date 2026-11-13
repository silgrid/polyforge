// carries: components/PolyforgeMessages*, components/PolyforgeAnalysisCard*, components/PolyforgeFileTicketDialog*, hooks/usePolyforgeMessages*, lib/polyforge-messages*, app/api/polyforge/messages/*
// — pi-web Messages contact-list acceptance e2e (authored before code).
//
// Drives the spec's contact-list behaviour through the real panel against a
// stateful stub `pf`: the stub answers conversations and a configured contact
// set, and records every msg verb. Each scenario prints `PASS C<n>` only when
// its THEN clause holds; the pre-implementation tree prints no PASS line.
//
//   node e2e/polyforge-messages-contacts.mjs
import assert from "node:assert/strict";
import { appendFileSync, chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { polyforgeDock as dock, polyforgeToggle as pfButton } from "./polyforge-dock.mjs";
import { assertNoDevServer, createCheck, createHarness, rawMessage } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-contacts-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const cliDir = join(work, ".config", "polyforge");
for (const dir of [join(agentDir, "sessions", "e2e"), project, cliDir]) mkdirSync(dir, { recursive: true });
const SESSION = "e2e-pf-contacts-session";
const sessionFile = join(agentDir, "sessions", "e2e", `${SESSION}.jsonl`);
const ts = "2026-10-30T00:00:00.000Z";
writeFileSync(sessionFile, [
  { type: "session", version: 3, id: SESSION, timestamp: ts, cwd: project },
  { type: "message", id: "m1", parentId: null, timestamp: ts, message: { role: "user", content: "E2E pf contacts host session" } },
].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
const SELF = "u_e2e_self";
const SELF_NAME = "Self Operator";
writeFileSync(join(cliDir, "cli.toml"), `server = "http://contacts.invalid:1"\napi_key = "E2E-KEY"\nuser_id = "${SELF}"\n`);

const AVA = { id: "u_contact_ava", display_name: "Ava Stone" };
const BEN = { id: "u_contact_ben", display_name: "Ben Ortiz" };
const ZOE = { id: "u_contact_zoe", display_name: "Zoe Adams" };
const BLANK = { id: "u_contact_blank", display_name: "" };
const msg = (seq, sender, text) => ({ id: `msg_${seq}_${sender}`, seq, sender, text, about_wi: "", reply_to: "" });
const conversation = (id, peer, peerName, messages) => ({ id, peer, peerName, messages, lastSeq: messages.length ? messages[messages.length - 1].seq : 0 });
const DOT = conversation("conv_dot", "u_dot_peer", "Dot Rowan", [msg(1, "peer", "DOT-UNREAD-ONE")]);
const AVA_CONV = conversation("conv_ava", AVA.id, AVA.display_name, [msg(1, "peer", "AVA-HELLO")]);

const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const baseState = () => ({
  version: "v2.20.12", self: SELF, selfName: SELF_NAME,
  conversations: [DOT], contacts: [AVA, BEN, ZOE], contactsFail: false, contactsDelayMs: 0,
  acked: { conv_dot: 0 }, sends: [],
});
const save = (state) => {
  const tmp = `${stateFile}.${process.pid}.tmp`;
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
writeFileSync(join(stubDir, "state-watch.json"), JSON.stringify({ work_item: "wi_e2e_39", slug: "task-39", known: true, job: { status: "running" }, execution: { workflow_status: "running", current_step: "WATCH-DETAIL-39", updated_at: ts, steps: {} }, events: [], last_seq: 0 }));
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename));
const args = process.argv.slice(2);
const record = () => fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const out = (v) => { process.stdout.write(JSON.stringify(v) + "\\n"); process.exit(0); };
const die = (code, text) => { process.stderr.write(text + "\\n"); process.exit(code); };
const save = (state) => { const p = path.join(dir, "state.json"); const t = p + "." + process.pid + ".tmp"; fs.writeFileSync(t, JSON.stringify(state)); fs.renameSync(t, p); };
if (args.includes("--version")) { record(); process.stdout.write(s.version + "\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
const at = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] : undefined; };
const listRow = (c) => ({
  id: c.id, kind: "direct", members: [s.self, c.peer].sort(), created_at: "2026-10-30T00:00:00Z",
  last_seq: c.lastSeq, unread: c.messages.filter((m) => m.sender !== "self" && m.seq > (s.acked[c.id] || 0)).length,
  ...(c.peerName === null ? {} : { member_names: { [s.self]: s.selfName, [c.peer]: c.peerName } }),
});
if (rest[0] === "ops" && rest[1] === "status") {
  record();
  out({ machine_id: "m-e2e", workspaces: ["/srv/e2e"], workflows_dir: "/srv/e2e/wf", env_names: [], jobs: [{ work_item_id: "wi_e2e_39", slug: "task-39", status: "running", spawn_n: 1, step_age_sec: 1, last_work_ago_sec: 1, candidate: "pi:e2e" }], queue: { pending: 0, running: 1, max_concurrent: 4 } });
}
if (rest[0] === "config" && rest[1] === "list") { record(); out({ items: [{ key: "server", value: "http://contacts.invalid:1", source: "file" }, { key: "api_key", set: true, source: "file" }, { key: "user_id", value: s.self, source: "file" }] }); }
if (rest[0] === "watch") { record(); out(JSON.parse(fs.readFileSync(path.join(dir, "state-watch.json"), "utf8"))); }
if (rest[0] === "msg") {
  if (rest[1] === "contacts") {
    record();
    if (s.contactsFail) die(4, JSON.stringify({ code: "CORE-503", detail: "contacts are unavailable" }));
    // A slow contact read: answer only after the delay.
    if (s.contactsDelayMs > 0) { setTimeout(() => out({ contacts: s.contacts }), s.contactsDelayMs); return; }
    out({ contacts: s.contacts });
  }
  if (rest[1] === "list" && at("--conversation")) {
    record();
    const c = s.conversations.find((x) => x.id === at("--conversation"));
    if (!c) die(4, "conversation is unknown");
    const before = Number(at("--before") || 0); const after = Number(at("--after") || 0); const limit = Number(at("--limit") || 50);
    let rows = c.messages.filter((m) => (!before || m.seq < before) && (!after || m.seq > after));
    rows = before ? rows.slice(-limit) : rows.slice(0, limit);
    out({ messages: rows.map((m) => ({ id: m.id, seq: m.seq, conversation_id: c.id, sender_id: m.sender === "self" ? s.self : c.peer, text: m.text, about_wi: m.about_wi, reply_to: m.reply_to, created_at: "2026-10-30T00:00:00Z" })), last_seq: c.lastSeq });
  }
  if (rest[1] === "list") { record(); out({ conversations: s.conversations.map(listRow) }); }
  if (rest[1] === "ack") {
    const fresh = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
    record();
    const c = fresh.conversations.find((x) => x.id === rest[2]);
    if (!c) die(4, "conversation is unknown");
    const asked = at("--seq") !== undefined ? Number(at("--seq")) : c.lastSeq;
    fresh.acked[c.id] = Math.max(fresh.acked[c.id] || 0, asked);
    // A slow ack: write the state it read only after the delay.
    if (fresh.ackDelayMs > 0) { setTimeout(() => { save(fresh); out({ ok: true }); }, fresh.ackDelayMs); return; }
    save(fresh); out({ ok: true });
  }
  if (rest[1] === "send") {
    record();
    const fresh = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
    const to = at("--to"); const text = at("--text");
    if (!to || !text) die(2, "pf: --to and --text are required");
    let c = fresh.conversations.find((x) => x.peer === to);
    if (!c) {
      const contact = (fresh.contacts || []).find((x) => x.id === to);
      c = { id: "conv_new_" + Date.now(), peer: to, peerName: contact ? contact.display_name : "", messages: [], lastSeq: 0 };
      fresh.conversations.push(c);
    }
    const row = { id: "msg_sent_" + (c.lastSeq + 1), seq: c.lastSeq + 1, sender: "self", text, about_wi: at("--about-wi") || "", reply_to: at("--reply-to") || "" };
    c.messages.push(row); c.lastSeq = row.seq;
    fresh.sends.push({ to, text, aboutWi: row.about_wi });
    save(fresh);
    out({ id: row.id, seq: row.seq, conversation_id: c.id, sender_id: s.self, text: row.text, about_wi: row.about_wi, reply_to: row.reply_to });
  }
  if (rest[1] === "analyze") {
    // the analyzer answers the task-backed structured shape.
    record();
    out({ message_id: rest[2], task_id: "task_252a1b2c3d4e5f67", project: "workbench", type: "other",
      summary: "CONTACTS-ANALYSIS-SENTINEL", suggested_actions: [], model: "stub", duration: "1s" });
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
const msgCalls = () => calls().filter((a) => a[0] === "msg");
const sends = () => msgCalls().filter((a) => a[1] === "send");
const acks = () => msgCalls().filter((a) => a[1] === "ack");
const argAfter = (argv, name) => { const i = argv.indexOf(name); return i >= 0 ? argv[i + 1] : undefined; };
const readState = () => JSON.parse(readFileSync(stateFile, "utf8"));

let base;
const h = createHarness({
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN" },
  errors: [],
  session: SESSION,
  ready: "E2E pf contacts host session",
  server(name, version) {
    const state = readState(); if (version) { state.version = version; save(state); }
    return { name, logFile: join(work, `server-${name}.log`), env: { PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: work,
      PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work } };
  },
  started: (started) => { base = started.base; },
});
const { openPage, startServer, withPage } = h;
const get = (path) => fetch(`${base}${path}`, { signal: AbortSignal.timeout(60_000) }).then(async (r) => ({ status: r.status, text: await r.text() }));
const tab = (page, name) => dock(page).getByRole("tab", { name }).or(dock(page).getByRole("button", { name })).first();
async function openDock(page) { await pfButton(page).click(); await dock(page).waitFor(); return dock(page); }
async function messagesTab(page) { const t = tab(page, /^(messages|消息)/i); await t.waitFor({ timeout: 15_000 }); await t.click(); return t; }
const composeEntry = (panel) => panel.getByRole("button", { name: /新消息|New message/ }).first();
const recipientField = (panel) => panel.getByLabel("收件人", { exact: true }).first();
const messageTextField = (panel) => panel.getByLabel("消息", { exact: true }).first();
const sendButton = (panel) => panel.getByRole("button", { name: /^发送$|^Send$/i }).first();
const unknownText = (panel) => panel.getByText("未知用户", { exact: true });

/** A bounded wait on a positive condition (no fixed grace window). */
async function waitUntil(predicate, what, timeout = 15_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await predicate().catch(() => false)) return;
    await delay(100);
  }
  assert.fail(`${what} (waited ${timeout} ms)`);
}

async function requireVisible(locator, what) {
  assert.ok(await locator.count() > 0, `${what} is not rendered`);
  return locator.first();
}
const leafBoxes = (panel, text) => panel.evaluate((dom, text) => [...dom.querySelectorAll("*")]
  .filter((node) => (node.textContent || "").trim() === text && ![...node.children].some((child) => (child.textContent || "").trim() === text))
  .map((node) => { const box = node.getBoundingClientRect(); return { x: box.x, y: box.y, width: box.width, height: box.height }; }), text);
const avatarElements = (root, initial) => root.evaluate((node, initial) => {
  const hits = [];
  for (const el of node.querySelectorAll("*")) {
    if ((el.textContent || "").trim() !== initial) continue;
    if ([...el.children].some((child) => (child.textContent || "").trim() === initial)) continue;
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

const failures = [];
const check = createCheck({ failures, record: ([label], text) => `${label}: ${text}`, log: rawMessage });

let suiteError = null;
try {
  await h.launchBrowser(() => chromium.launch());
  await startServer("main");

  // C1: no messages at all — every configured contact still renders with its
  // name and initial avatar (the list is not conversation-gated).
  seed({ conversations: [], contacts: [AVA, BEN, ZOE], acked: {} });
  await check("C1", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    for (const name of [AVA.display_name, BEN.display_name, ZOE.display_name]) {
      assert.ok(await panel.getByText(name, { exact: false }).count() > 0, `the configured contact ${name} is not in the list`);
    }
    for (const initial of ["A", "B", "Z"]) {
      assert.ok((await avatarElements(panel, initial)).length > 0, `the contact avatar ${initial} is not rendered`);
    }
  }));

  // C2: direct conversation rows first in their current order, then silent
  // contacts sorted by display name; a contact with a conversation appears
  // once; 群聊 keeps its current placeholder.
  seed({ conversations: [DOT, AVA_CONV], contacts: [AVA, BEN, ZOE], acked: { conv_dot: 0, conv_ava: 1 } });
  await check("C2", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    const dot = await leafBoxes(panel, DOT.peerName);
    const ava = await leafBoxes(panel, AVA.display_name);
    const ben = await leafBoxes(panel, BEN.display_name);
    const zoe = await leafBoxes(panel, ZOE.display_name);
    assert.ok(dot.length > 0 && ava.length > 0 && ben.length > 0 && zoe.length > 0, "conversation rows and silent contacts must all render");
    assert.equal(ava.length, 1, "a contact with an existing conversation must not appear twice");
    assert.ok(dot[0].y < ben[0].y && ava[0].y < ben[0].y, "conversation rows must come before silent contacts");
    assert.ok(ben[0].y < zoe[0].y, "silent contacts must be ordered by display name (Ben before Zoe)");
    assert.ok(await panel.getByText("群聊", { exact: true }).count() > 0, "the 群聊 section must stay rendered");
    assert.ok(await panel.getByText("尚无群聊", { exact: true }).count() > 0, "the empty 群聊 placeholder must stay rendered");
  }));

  // C3: the compose entry, recipient picker and typed-id field are gone.
  seed({ conversations: [DOT], contacts: [BEN] });
  await check("C3", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    assert.equal(await composeEntry(panel).count(), 0, "no ＋新消息 compose entry may remain");
    assert.equal(await recipientField(panel).count(), 0, "no typed recipient field may remain");
    const shown = await disclosure(panel);
    assert.ok(!/新消息|New message|收件人/.test(shown.text), "the panel must carry no compose/picker text");
    // The narrow (phone) layout carries none of them either:
    // the contact row renders first, so the absences are measured on a live list.
    await page.setViewportSize({ width: 390, height: 844 });
    await waitUntil(async () => (await leafBoxes(panel, BEN.display_name)).some((box) => box.width > 0 && box.x + box.width <= 390),
      "the narrow layout must render the contact list inside the 390 px viewport");
    assert.equal(await composeEntry(panel).count(), 0, "no ＋新消息 compose entry may remain in the narrow layout");
    assert.equal(await recipientField(panel).count(), 0, "no typed recipient field may remain in the narrow layout");
    const narrow = await disclosure(panel);
    assert.ok(!/新消息|New message|收件人/.test(narrow.text), "the narrow panel must carry no compose/picker text");
  }));

  // C4: clicking a silent contact opens an empty pane with its header; no
  // conversation is created and nothing is acked.
  seed({ conversations: [DOT], contacts: [BEN], acked: { conv_dot: 0 } });
  await check("C4", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    const row = await requireVisible(panel.getByText(BEN.display_name, { exact: true }), `the silent contact ${BEN.display_name}`);
    await row.click();
    await waitUntil(async () => await panel.getByText(BEN.display_name, { exact: true }).count() >= 2,
      "the opened pane must show the contact in its header");
    assert.ok((await avatarElements(panel, "B")).length > 0, "the opened pane header must show the contact's initial avatar");
    assert.equal(await panel.getByText("DOT-UNREAD-ONE", { exact: false }).count(), 0, "the empty pane must show no conversation's messages");
    assert.equal(acks().length, 0, "selecting a silent contact must ack nothing");
    assert.equal(sends().length, 0, "selecting a silent contact must send nothing");
    assert.equal(readState().conversations.length, 1, "selecting a silent contact must create no conversation");
  }));

  // C5: the first send addresses the contact id, echoes, and opens the
  // conversation from the send's own answer.
  seed({ conversations: [], contacts: [BEN], acked: {} });
  await check("C5", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await (await requireVisible(panel.getByText(BEN.display_name, { exact: true }), `the silent contact ${BEN.display_name}`)).click();
    await messageTextField(panel).fill("HELLO-FROM-CONTACT");
    await sendButton(panel).click();
    for (let i = 0; i < 30 && !sends().length; i++) await delay(500);
    assert.equal(sends().length, 1, `the first send must spawn exactly one pf msg send, got ${JSON.stringify(sends())}`);
    assert.equal(argAfter(sends()[0], "--to"), BEN.id, "the first send must address the contact's id");
    assert.equal(argAfter(sends()[0], "--text"), "HELLO-FROM-CONTACT");
    await panel.getByText("HELLO-FROM-CONTACT", { exact: false }).first().waitFor({ timeout: 15_000 });
    const created = readState().conversations;
    assert.equal(created.length, 1, "the first send must create exactly one conversation");
    assert.equal(created[0].peer, BEN.id, "the created conversation must be addressed to the contact id");
    assert.ok(await panel.getByText(BEN.display_name, { exact: true }).count() > 0, "the opened conversation must keep the contact's display name");
    // the scenario ends once the pane's ack has landed: no stub write outlives it
    await waitUntil(async () => (readState().acked[created[0].id] ?? 0) >= 1, "the opened conversation's acknowledgment landed in pf's state");
  }));

  // C6: a blank display name renders 未知用户 with the ? avatar; no rendered
  // text, title or aria-label carries a warm user id.
  seed({ conversations: [DOT], contacts: [BLANK, BEN], acked: { conv_dot: 0 } });
  await check("C6", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    assert.ok(await unknownText(panel).count() > 0, "a blank-name contact must render 未知用户");
    assert.ok((await avatarElements(panel, "?")).length > 0, "a blank-name contact must render the ? avatar");
    await requireVisible(unknownText(panel), "the blank-name contact row").then((row) => row.click());
    await waitUntil(async () => await unknownText(panel).count() >= 2, "the opened blank-name pane must show 未知用户 in its header");
    assert.ok(await unknownText(panel).count() > 0, "the opened blank-name pane must keep 未知用户");
    const shown = await disclosure(panel);
    for (const id of [BLANK.id, BEN.id, SELF]) {
      assert.ok(!shown.text.includes(id), `the panel text must not render the raw user id ${id}`);
      assert.ok(!shown.labels.some((value) => value.includes(id)), `no title or aria-label may carry the raw user id ${id}`);
    }
  }));

  // C9: an open silent contact's empty pane follows the peer's
  // first message — once the list read carries BEN's direct conversation, the
  // pane shows it without a click, and nothing is sent from the pane.
  seed({ conversations: [DOT], contacts: [BEN], acked: { conv_dot: 0 }, ackDelayMs: 1500 });
  await check("C9", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await (await requireVisible(panel.getByText(BEN.display_name, { exact: true }), `the silent contact ${BEN.display_name}`)).click();
    await waitUntil(async () => await panel.getByText(BEN.display_name, { exact: true }).count() >= 2,
      "the opened pane must show the contact in its header");
    assert.equal(await panel.getByText("BEN-FIRST-HELLO", { exact: false }).count(), 0, "the silent contact's pane starts empty");
    const state = readState();
    state.conversations.push(conversation("conv_ben", BEN.id, BEN.display_name, [msg(1, "peer", "BEN-FIRST-HELLO")]));
    save(state);
    await waitUntil(async () => await panel.getByText("BEN-FIRST-HELLO", { exact: false }).count() > 0,
      "the open pane must show the peer's first message without a click");
    await waitUntil(async () => acks().some((argv) => argv[2] === "conv_ben"), "the open pane acknowledges the peer's first message");
    // the scenario ends once the pane's ack has landed: no stub write outlives it
    await waitUntil(async () => (readState().acked.conv_ben ?? 0) >= 1, "the pane's acknowledgment landed in pf's state");
    assert.ok(await panel.getByText(BEN.display_name, { exact: true }).count() > 0, "the opened conversation keeps the contact's display name");
    assert.equal(await panel.getByText("DOT-UNREAD-ONE", { exact: false }).count(), 0, "the pane shows only the contact's conversation");
    const conversations = readState().conversations;
    assert.equal(conversations.filter((row) => row.peer === BEN.id).length, 1, "exactly one conversation with the contact exists");
    assert.equal(conversations.length, 2, "the pane created no conversation of its own");
    assert.equal(sends().length, 0, "the pane must send nothing");
  }));

  // C8: a slow `pf msg contacts` no longer holds the list
  // answer. The stub delays the contact read far past the route's 2 s contact
  // bound; the list reads stay fast, so the answer lands near the bound with
  // the conversations intact and an empty contact set — its latency must not
  // scale with the contact delay.
  seed({ conversations: [DOT], contacts: [BEN], contactsDelayMs: 12_000 });
  await check("C8", async () => {
    const started = Date.now();
    const answer = await get("/api/polyforge/messages");
    const elapsed = Date.now() - started;
    assert.equal(answer.status, 200, `the messages route answered ${answer.status}`);
    const body = JSON.parse(answer.text);
    assert.deepEqual(body.contacts, [], "a contact read past its bound answers an empty contact set");
    assert.equal(body.capable, true);
    assert.ok(Array.isArray(body.conversations) && body.conversations.some((row) => row.id === DOT.id), "the conversation rows stay intact");
    assert.ok(elapsed < 7_000, `the list answer took ${elapsed} ms: it must be bounded by the 2 s contact bound, not the 12 s contact delay`);
    assert.ok(msgCalls().some((argv) => argv[1] === "contacts"), "the contact read was spawned");
    assert.equal(readState().contactsDelayMs, 12_000, "C8's seed held: no stub write of an earlier scenario landed over it");
  });

  // C7: below the 2.20.12 floor the upgrade state shows and no message or
  // contact verb spawns; at 2.20.12 the contact list works.
  await check("C7", async () => {
    await h.stop();
    appendFileSync(argvLog, JSON.stringify(["__seed__"]) + "\n");
    await startServer("floor", "v2.20.11");
    const floor = await get("/api/polyforge/messages");
    assert.equal(floor.status, 200, `the below-floor messages route answered ${floor.status}`);
    assert.match(floor.text, /"capable"\s*:\s*false/, "below the floor the route must report capable:false");
    assert.match(floor.text, /2\.20\.12|upgrade|升级/i, "below the floor the reason must name the 2.20.12 upgrade");
    assert.equal(msgCalls().length, 0, "below the floor no message or contact verb may be spawned");
    const page = await openPage();
    try {
      await openDock(page); await messagesTab(page);
      assert.ok(await dock(page).getByText(/upgrade|升级/i).count() > 0, "below the floor the tab must show the upgrade-needed state");
      assert.equal(await dock(page).getByText("单聊", { exact: true }).count(), 0, "below the floor no contact list may render");
    } finally { await page.context().close(); }
    await h.stop();
    appendFileSync(argvLog, JSON.stringify(["__seed__"]) + "\n");
    await startServer("supported", "v2.20.12");
    const supported = await get("/api/polyforge/messages");
    assert.match(supported.text, /"capable"\s*:\s*true/, "at 2.20.12 the route must report capable:true");
  });
} catch (error) {
  suiteError = error;
  console.error(error);
} finally {
  await h.close().catch(() => {});
}

if (suiteError) failures.push(`suite: ${suiteError?.message ?? suiteError}`);
if (check.count === 0) failures.push("suite: no scenario ran");
console.log("\n=== contacts scenarios ===");
for (const line of failures) console.log(`FAIL ${line}`);
rmSync(work, { force: true, recursive: true });
process.exit(failures.length ? 1 : 0);