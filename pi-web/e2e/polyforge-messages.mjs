// carries: components/PolyforgeMessages*, components/PolyforgeAnalysisCard*, components/PolyforgeFileTicketDialog*, hooks/usePolyforgeMessages*, lib/polyforge-messages*, app/api/polyforge/messages/*
// — pi-web Messages panel redo (Slack-like), dual-user stub-pf e2e.
//
// Originally authored against artifacts/spec.md; this revision
// is authored BEFORE `code` against the artifacts/spec.md (the
// Slack-like layout, display names, zero raw id, the 2.20.10 floor). The suite
// starts its own `next dev` with a stub `pf` first on PATH. The stub is a real
// state machine for TWO human users: it keeps each conversation's messages and
// per-user unread, records every send/ack/analyze argv in a ledger, models the
// real aihub conversation row (members + member_names, never a `peer` field),
// and keeps the acting identity (`self`) server-side, so the suite drives one
// conversation through the UI alone.
//
// Scenario table: PM1..PM8 are the #56 scenarios, kept and adapted to the new
// markup with no relaxed assertion (their layout-specific selectors follow the
// new panel); PM9..PM18 are this work item's new-behaviour scenarios. The
// capsule document artifacts/e2e-scenarios.md mirrors them.
//
// Baseline posture: the new layout/names/floor do not exist yet, so every
// authored scenario is an assertion-level red (no 单聊/群聊 column, no display
// name, an id where a name must be, the old 2.20.7 floor) — never a build or
// fixture error.
//
// (the contact list): the stub also answers `pf msg contacts`
// from a configured contact set and its base version is the new 2.20.12
// floor. The compose entry, recipient picker and typed-id field are gone
// (ruling 5 removes the feature), so PM5/PM9/PM10/PM11/PM13/PM17 drive their
// first contact and identity surfaces through silent contact rows instead;
// PM8 moves to the 2.20.12 floor. Every other assertion is unchanged; the
// new contact-list behaviour itself is e2e/polyforge-messages-contacts.mjs.
//
//   node e2e/polyforge-messages.mjs
import assert from "node:assert/strict";
import { appendFileSync, chmodSync, existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";

import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { polyforgeDock as dock, polyforgeToggle as pfButton } from "./polyforge-dock.mjs";
import { NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, rawMessage } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-messages-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const cliDir = join(work, ".config", "polyforge");
for (const dir of [join(agentDir, "sessions", "e2e"), project, cliDir]) mkdirSync(dir, { recursive: true });
const SESSION = "e2e-pf-messages-session";
const sessionFile = join(agentDir, "sessions", "e2e", `${SESSION}.jsonl`);
const ts = "2026-10-18T00:00:00.000Z";
const writeSession = (body) => writeFileSync(sessionFile, [
  { type: "session", version: 3, id: SESSION, timestamp: ts, cwd: project },
  { type: "message", id: "m1", parentId: null, timestamp: ts, message: { role: "user", content: body } },
].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
writeSession("E2E pf messages host session");

// Sentinels: the operator identity and its display name, the credential, the
// server address and an environment name must never leave the server; a
// message body must never reach the pi-web server log. SELF_NAME is the new
// disclosure surface of this work item: aihub returns it in member_names, so
// the pi-web messages routes must drop it with the self id (PM7 unchanged).
const KEY_SENTINEL = "E2E-PF-API-KEY-SENTINEL-56";
const USER_SENTINEL = "u_e2e_sentinel_self";
const SELF_NAME = "Self Operator Sentinel";
const PEER = "u_e2e_peer";
const PEER_NAME = "Alice Chen";
const SERVER_SENTINEL = "http://10.9.9.56:8420";
const ENV_SENTINEL = "E2E_ENV_NAME_SENTINEL_56";
const BODY_SENTINEL = "E2E-BODY-SENTINEL-56";
const SECRET_SENTINEL = "AKIASENTINELE2E00056";
// rewrites PM6 to the task-backed structured analysis card.
const ANALYSIS_SUMMARY = "ANALYSIS-SUMMARY-SENTINEL-252 the peer asks for a review";
const ANALYSIS_CONCLUSION = "RETIRED-CONCLUSION-SENTINEL-252";
const ANALYSIS_MODEL = "stub-task-model";
const ANALYSIS_DURATION = "1m2.3s";
const ANALYSIS_DRAFT = "ANALYSIS-DRAFT-SENTINEL-252";
const LONG_PEER = "u_e2e_long_peer";
const LONG_NAME = "Bartholomew Featherstonehaugh Longname";
writeFileSync(join(cliDir, "cli.toml"), `server = "${SERVER_SENTINEL}"\napi_key = "${KEY_SENTINEL}"\nuser_id = "${USER_SENTINEL}"\n`);

// ---- stateful dual-user pf stub (state.json + argv.log in its own dir) ----
const stubDir = join(work, "stub-bin");
mkdirSync(stubDir);
const stateFile = join(stubDir, "state.json");
const argvLog = join(stubDir, "argv.log");
const msg = (seq, sender, text, extra = {}) => ({ id: `msg_${seq}_${sender}`, seq, sender, text, about_wi: "", reply_to: "", ...extra });
// A conversation mirrors the real aihub row inputs: the peer, its display
// name, and the messages. `peerName: null` models the transient old-aihub
// answer (no member_names at all); "" models an empty users.display_name.
const conversation = (id, peer, peerName, messages) => ({ id, peer, peerName, messages, lastSeq: messages.length ? messages[messages.length - 1].seq : 0 });
const PAGED = conversation("conv_paged", "u_e2e_paged_peer", "Pat Paged", Array.from({ length: 120 }, (_, i) =>
  // Numbered globally (not 1001..1120 per conversation) so paging cannot
  // pass by assuming a conversation's first seq is 1 (code review).
  msg(1001 + i, i % 2 ? "peer" : "self", `MSG-${String(i + 1).padStart(3, "0")}`)));
const ANCHOR = conversation("conv_anchor", PEER, PEER_NAME, [
  msg(1, "peer", "ANCHOR-ONE", { about_wi: "task-39" }),
  msg(2, "peer", "ANCHOR-DEEP", { about_wi: "task-39/artifacts/spec.md" }),
  msg(3, "peer", BODY_SENTINEL + " UNANCHORED-ONE"),
  msg(4, "self", "OWN-MESSAGE"),
]);
// PM15's stream: one own message, then a four-message peer run (the reply at
// the end quotes the run's first message), plus an anchor quote in the run.
const BUBBLES = conversation("conv_bubbles", "u_e2e_bubbles_peer", "Bea Run", [
  msg(1, "self", "OWN-BUBBLE"),
  msg(2, "peer", "PEER-RUN-ONE"),
  msg(3, "peer", "PEER-RUN-TWO"),
  msg(4, "peer", "PEER-ANCHORED", { about_wi: "task-39" }),
  msg(5, "peer", "PEER-REPLY", { reply_to: "msg_2_peer" }),
]);
const DOT = conversation("conv_dot", "u_e2e_dot_peer", "Dot Rowan", [msg(1, "peer", "DOT-UNREAD-ONE")]);
const LONG = conversation("conv_long", LONG_PEER, LONG_NAME, [msg(1, "peer", "LONG-ONE")]);
// PM19: inline @project#number links, the duplicated anchor
// line, the historical anchor line and an own bubble.
const WORK_ITEM = conversation("conv_work_item", "u_e2e_work_peer", "Wanda Work", [
  msg(1, "peer", "LINK-DUP @acme#272 tail", { about_wi: "acme#272" }),
  msg(2, "peer", "LINK-LEGACY", { about_wi: "task-39" }),
  msg(3, "self", "OWN-LINK @acme#272"),
  msg(4, "peer", "PEER-REPLY-ME"),
]);
// PM11: an empty display name (legitimate data) and the old-aihub answer that
// carries no member_names at all — both render 未知用户 with a ? avatar.
const NAMELESS = conversation("conv_nameless", "u_e2e_nameless_peer", "", [
  msg(1, "peer", "NAMELESS-ONE"),
  msg(2, "peer", "NAMELESS-TWO"),
]);
const ABSENT = conversation("conv_absent", "u_e2e_absent_peer", null, [msg(1, "peer", "ABSENT-ONE")]);
const baseState = () => ({
  version: "v2.20.18", mode: "ok", self: USER_SENTINEL, selfName: SELF_NAME, running: 2, blocked: false, sendError: "",
  conversations: [DOT, PAGED, ANCHOR, BUBBLES, LONG],
  // The configured contact set `pf msg contacts` answers;
  // empty unless a check seeds one.
  contacts: [],
  // The cli.toml [workspace."…"] project mapping `pf projects get` is read
  // for (only a check that binds @project#number seeds one).
  mappedProjects: [],
  // acked seeds name each conversation's newest sequence (w93 owner ruling
  // w93-pm1-stub-acked-fixture): conv_paged's messages carry seqs 1001–1120,
  // so its cursor is 1120 — the earlier 120 left every message permanently
  // unread (120 of them) and contradicted PM1's zero-unread arm.
  acked: { conv_paged: 1120, conv_anchor: 4, conv_bubbles: 5, conv_dot: 0, conv_long: 1 },
  sends: [], analyses: [],
});
// The state lock (w93 owner ruling: pin the seed/late-write race): the stub's
// mutating handlers and every test-side state write hold an exclusive
// O_EXCL lock on the same file, so a dying page's in-flight ack or send can
// never read-modify-write across a fresh seed() — it either sees the new
// state (and applies or 404s correctly) or waits for the write to finish.
const stateLockPath = join(stubDir, "state-lock");
const withStateLock = (fn) => {
  const deadline = Date.now() + 5000;
  let fd = null;
  while (!fd) {
    try { fd = openSync(stateLockPath, "wx"); }
    catch { if (Date.now() > deadline) throw new Error("the stub state lock is held"); Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 5); }
  }
  try { return fn(); } finally { rmSync(stateLockPath, { force: true }); }
};
// Every state.json write is a whole-file replace (temp file + rename): the
// lock serializes writers only, and the readers — the stub's startup read on
// every spawn, readState() here — take no lock. A plain writeFileSync
// truncates first, so a reader landing before the bytes did parsed an empty
// file ("Unexpected end of JSON input"): in the test that failed a check
// outright, in the stub it failed the spawn (a scroll-up page answered
// nothing, so paging stopped).
const write = (state) => withStateLock(() => {
  const tmp = `${stateFile}.${process.pid}.tmp`;
  writeFileSync(tmp, JSON.stringify(state));
  renameSync(tmp, stateFile);
});
const readState = () => JSON.parse(readFileSync(stateFile, "utf8"));
const seed = (patch = {}) => {
  const next = { ...baseState(), ...patch };
  write(next);
  // The ledger is check-scoped by a marker, not a clear (w93 owner ruling):
  // a dying page's in-flight ack can append its argv after a plain clear and
  // pollute the next check's counts. calls() reads only what follows the
  // last marker, so a late argv written before this marker is invisible.
  appendFileSync(argvLog, JSON.stringify(["__seed__"]) + "\n");
  return next;
};
seed();
const watchDetail = { work_item: "wi_e2e_39", slug: "task-39", known: true, job: { status: "running" },
  execution: { workflow_status: "running", current_step: "WATCH-DETAIL-39", updated_at: ts,
    steps: { "WATCH-DETAIL-39": { status: "in_progress", started_at: ts } } }, events: [], last_seq: 0 };
writeFileSync(join(stubDir, "state-watch.json"), JSON.stringify(watchDetail));
writeFileSync(join(stubDir, "pf"), `#!${process.execPath}
const fs = require("fs"); const path = require("path");
const dir = path.dirname(fs.realpathSync(__filename));
const args = process.argv.slice(2);
// The ledger records argv per acting handler, not at spawn start (w93 owner
// ruling): a late ack from a dying page that the fresh seed's world does not
// know must leave NO trace at all — no state write, no ledger line.
const record = () => fs.appendFileSync(path.join(dir, "argv.log"), JSON.stringify(args) + "\\n");
const s = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
const out = (v) => { process.stdout.write(JSON.stringify(v) + "\\n"); process.exit(0); };
const die = (code, text) => { process.stderr.write(text + "\\n"); process.exit(code); };
// The state lock (see the test-side withStateLock): mutating handlers hold
// it across their re-read→save, so a concurrent seed() write can never land
// between them (w93 owner ruling — the late-ack/seed race). The handler body
// never exits inside the lock: it returns the answer, and out()/fail() run
// after the lock is released (they call process.exit, which would skip the
// cleanup).
const withStateLock = (fn) => {
  const lockPath = path.join(dir, "state-lock");
  const deadline = Date.now() + 5000;
  let lockFd = null;
  while (!lockFd) {
    try { lockFd = fs.openSync(lockPath, "wx"); }
    catch { if (Date.now() > deadline) return { fail: ["CORE-409", "the stub state lock is held"] }; Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 5); }
  }
  try { return fn(); } finally { try { fs.closeSync(lockFd); } catch (e) {} try { fs.unlinkSync(lockPath); } catch (e) {} }
};
const fail = (code, detail) => { process.stderr.write(JSON.stringify({ code, detail }) + "\\n"); process.exit(4); };
// A whole-file replace, like the test side's write(): the unlocked startup
// read above must never see a truncated file.
const saveState = (state) => { const p = path.join(dir, "state.json"); fs.writeFileSync(p + "." + process.pid + ".tmp", JSON.stringify(state)); fs.renameSync(p + "." + process.pid + ".tmp", p); };
if (args.includes("--version")) { record(); process.stdout.write(s.version + "\\n"); process.exit(0); }
const rest = args.filter((a) => a !== "--json");
const at = (name) => { const i = rest.indexOf(name); return i >= 0 ? rest[i + 1] : undefined; };
const conv = (id) => s.conversations.find((c) => c.id === id);
// The real aihub conversation row: members plus their display names, no
// invented peer field (the pi-web server derives the peer from members).
const listRow = (c) => {
  const row = {
    id: c.id, kind: "direct", members: [s.self, c.peer].sort(), created_at: "2026-10-18T00:00:00Z",
    last_seq: c.lastSeq, unread: c.messages.filter((m) => m.sender !== s.self && m.seq > (s.acked[c.id] || 0)).length,
  };
  if (c.peerName !== null) row.member_names = { [s.self]: s.selfName, [c.peer]: c.peerName };
  return row;
};
if (rest[0] === "ops" && rest[1] === "status") {
  record();
  if (s.mode === "down") die(1, "pf: daemon not reachable (connection refused)");
  const jobs = [{ work_item_id: "wi_e2e_39", slug: "task-39", status: "running", spawn_n: 1, step_age_sec: 30, last_work_ago_sec: 3, candidate: "pi:e2e" }];
  if (s.blocked) jobs.push({ work_item_id: "wi_e2e_blocked", slug: "blocked-job", status: "needs_input", spawn_n: 1, step_age_sec: 900, last_work_ago_sec: 880, candidate: "pi:e2e" });
  out({ machine_id: "m-e2e", workspaces: ["/srv/e2e-workspace"], workflows_dir: "/srv/e2e-workspace/wf",
    env_names: ["${ENV_SENTINEL}"], jobs, queue: { pending: 0, running: s.running, max_concurrent: 4 } });
}
if (rest[0] === "config" && rest[1] === "list") { record(); const items = [
  { key: "server", value: "${SERVER_SENTINEL}", source: "file" }, { key: "api_key", set: true, source: "file" },
  { key: "user_id", value: "${USER_SENTINEL}", source: "file" }];
  // the panel's known projects are its groups, so a binding
  // check seeds the same cli.toml workspace mapping the groups read uses.
  if (s.mappedProjects && s.mappedProjects.length) items.push({ key: '[workspace."/srv/e2e-workspace"]', value: s.mappedProjects.join(","), source: "file" });
  out({ items }); }
if (rest[0] === "watch") { record(); out(JSON.parse(fs.readFileSync(path.join(dir, "state-watch.json"), "utf8"))); }
if (rest[0] === "projects" && rest[1] === "get") { record(); out({ project: rest[2], members: [] }); }
if (rest[0] === "msg") {
  if (rest[1] === "analyze") {
    record();
    if (s.mode === "analyzeFail") fail("DAEMON-500", "analyzer is not assembled on this machine");
    out({ message_id: rest[2], task_id: "task_252a1b2c3d4e5f67", project: "workbench", type: "requirement",
      summary: "${ANALYSIS_SUMMARY}", suggested_actions: ["file_ticket", "reply"],
      model: "${ANALYSIS_MODEL}", duration: "${ANALYSIS_DURATION}",
      reply_draft: "${ANALYSIS_DRAFT}", conclusion: "${ANALYSIS_CONCLUSION}" });
  }
  if (rest[1] === "ack") {
    // Barrier (w93 owner ruling): a dying page's in-flight ack can land
    // after the next check's seed() rewrote state.json; mutating the startup
    // snapshot would revert the fresh seed. Mutating a re-read taken at
    // handling time shrinks the stale window to the re-read→save span, and a
    // late ack for a conversation the new seed dropped 404s instead of
    // resurrecting old state.
    const acked = withStateLock(() => {
      const fresh = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
      const c = (fresh.conversations || []).find((x) => x.id === rest[2]);
      // A conversation the fresh seed does not know is a late ack from a
      // dying page: no state write and no ledger line (owner ruling).
      if (!c) return { fail: ["CORE-404", "conversation " + rest[2] + " is unknown"] };
      const asked = at("--seq") !== undefined ? Number(at("--seq")) : c.lastSeq;
      if (asked <= 0 || asked > c.lastSeq) { record(); return { fail: ["CORE-400", "seq " + asked + " is outside 1.." + c.lastSeq] }; }
      fresh.acked[c.id] = Math.max(fresh.acked[c.id] || 0, asked);
      saveState(fresh);
      // The ledger line lands after the state it reports: a check that polls
      // acks() and then reads the state never sees the call before its write.
      record();
      return { ok: true };
    });
    if (acked.fail) fail(acked.fail[0], acked.fail[1]);
    out(acked);
  }
  if (rest[1] === "send") {
    // Same barrier as ack: mutate a re-read state, never the startup
    // snapshot, so a late send cannot revert a fresh seed either.
    const sent = withStateLock(() => {
      const fresh = JSON.parse(fs.readFileSync(path.join(dir, "state.json"), "utf8"));
      if (fresh.sendError) { record(); return { fail: [fresh.sendError, "message refused: secret-shaped content (aws_key) is never stored or sent"] }; }
      const to = at("--to"); const text = at("--text");
      if (!to || !text) { record(); return { fail: ["CORE-400", "--to and --text are required"] }; }
      let c = fresh.conversations.find((x) => x.peer === to);
      // A first contact's row carries the configured contact's display name;
      // a blank (or unconfigured) one is an empty name (未知用户), never the
      // id as a name.
      const contact = (fresh.contacts || []).find((x) => x.id === to);
      if (!c) { c = { id: "conv_new_" + Date.now(), peer: to, peerName: contact ? contact.display_name : "", messages: [], lastSeq: 0 }; fresh.conversations.push(c); }
      const row = { id: "msg_sent_" + (c.lastSeq + 1), seq: c.lastSeq + 1, sender: "self", text,
        about_wi: at("--about-wi") || "", reply_to: at("--reply-to") || "" };
      c.messages.push(row); c.lastSeq = row.seq; fresh.sends.push({ to, text, aboutWi: row.about_wi, replyTo: row.reply_to });
      saveState(fresh);
      record(); // after the state it reports, as in ack
      return { id: row.id, seq: row.seq, conversation_id: c.id, sender_id: s.self, text: row.text,
        about_wi: row.about_wi, reply_to: row.reply_to };
    });
    if (sent.fail) fail(sent.fail[0], sent.fail[1]);
    out(sent);
  }
  if (rest[1] === "list" && at("--conversation")) {
    record();
    const c = conv(at("--conversation"));
    if (!c) fail("CORE-404", "conversation is unknown");
    const before = Number(at("--before") || 0); const after = Number(at("--after") || 0); const limit = Number(at("--limit") || 50);
    const cap = s.historyCap === undefined ? Infinity : s.historyCap;
    let rows = c.messages.filter((m) => m.seq <= cap && (!before || m.seq < before) && (!after || m.seq > after));
    if (before) rows = rows.slice(-limit); else rows = rows.slice(0, limit);
    out({ messages: rows.map((m) => ({
      id: m.id, seq: m.seq, conversation_id: c.id,
      sender_id: m.sender === "self" ? s.self : c.peer,
      text: m.text, about_wi: m.about_wi, reply_to: m.reply_to,
      created_at: "2026-10-18T00:00:00Z",
    })), last_seq: c.lastSeq });
  }
  if (rest[1] === "list") { record(); out({ conversations: s.conversations.map(listRow) }); }
  if (rest[1] === "contacts") { record(); out({ contacts: s.contacts || [] }); }
  fail("CORE-400", "msg: unknown action " + rest[1]);
}
die(2, "pf: unknown command");
`);
chmodSync(join(stubDir, "pf"), 0o755);
const calls = () => (existsSync(argvLog) ? readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean) : [])
  // Only the current check's argv: everything after the last __seed__ marker.
  // A marker resets the accumulator, so late argv lines from the previous
  // check's dying page cannot leak into this check's spawn counts.
  .reduce((acc, line) => {
    try {
      const argv = JSON.parse(line);
      if (Array.isArray(argv) && argv[0] === "__seed__") return [];
      if (Array.isArray(argv)) acc.push(argv.filter((part) => part !== "--json"));
    } catch { /* a torn append from a concurrent spawn never counts */ }
    return acc;
  }, []);
const msgCalls = () => calls().filter((a) => a[0] === "msg");
const acks = () => msgCalls().filter((a) => a[1] === "ack");
const sends = () => msgCalls().filter((a) => a[1] === "send");
const argAfter = (argv, name) => { const i = argv.indexOf(name); return i >= 0 ? argv[i + 1] : undefined; };
const appearsIn = (haystack, needle) => haystack.some((value) => (value || "").includes(needle));

// ---- scenario bookkeeping ----
const T = {
  PM1: "PM1: the Messages tab shows an unread dot beside the unchanged running-count badge, and no dot at zero unread",
  PM2: "PM2: opening a conversation shows the newest page at the bottom and scroll-up prepends the page immediately above with no gap or duplicate, stopping at the oldest message",
  PM3: "PM3: a visible conversation acks exactly the newest displayed sequence once and clears unread, and a newer unseen sequence is never the acked one",
  PM4: "PM4: a hidden conversation and a locally blocked job suppress automatic ack, and an explicit conversation click acks the newest displayed sequence",
  PM5: "PM5: a send spawns exactly one pf msg send (with its anchor), a reply passes --reply-to and quotes the loaded original, a first contact is sent from its silent contact row, and a refused send answers a 4xx with the documented text",
  PM6: "PM6: a work-item anchor opens the existing Jobs detail through the watch read, the Analyze entry appears only on received messages and renders a task-backed structured card whose result is transient, and incoming messages never enter chat or the composer",
  PM7: "PM7: no messages route response or server log carries the operator key, the self id or self display name, the server address or a message body, and a CORE-422-refused send leaves no trace",
  PM8: "PM8: below the pf 2.20.12 Messages floor the tab shows an upgrade-needed state and spawns no message verb, and at the floor the panel works with names",
  PM9: "PM9: the two-user panel's text, titles and aria-labels carry neither the peer id nor the self id",
  PM10: "PM10: the list row, chat header, bubble attribution and silent contact row identify the peer by display name and initial avatar",
  PM11: "PM11: an unnamed peer renders 未知用户 with a ? avatar on every surface and never an id",
  PM12: "PM12: opening Messages selects nothing and acks nothing, and a click opens the conversation and acknowledges it",
  PM13: "PM13: the conversation list is a right-hand column of 120–220px with avatars, names, unread badges, 单聊/群聊 sections, 尚无群聊, contact rows and no compose entry",
  PM14: "PM14: the open conversation header shows the peer's avatar and display name",
  PM15: "PM15: own bubbles sit right without attribution while peer bubbles sit left with avatar and name on the first of a run, keeping timestamps, anchor quotes and reply quotes",
  // owner-intended change（重立自 #209）: mobile keeps the desktop two columns (was a single view).
  PM16: "PM16: desktop keeps two columns at the default and the minimum dock width and mobile keeps the same two columns with a back control",
  PM17: "PM17: the list offers the existing peers and silent contacts by display name, a peer row sends to its id and a first contact is its silent contact row",
  PM18: "PM18: the conversation route keeps peer as the send address and adds peer_name, and a send still addresses the peer id",
  // the anchor field is gone and @project#number is the binding.
  PM19: "PM19: @project#number in the text binds about_wi (known project, quote lines excluded, first wins), every bubble token links to the watch read, own bubbles quote, and no anchor field remains",
};
const failures = new Map(Object.values(T).map((title) => [title, []]));
const check = createCheck({ failures: ([title, label], text) => failures.get(title).push(`${label}: ${text}`), log: rawMessage,
  pass: ([, label]) => console.log(`PASS ${label}`), fail: ([, label], text) => console.log(`FAIL ${label}: ${text}`) });

const serverLogs = [];
let base;
// delivers every ResizeObserver notification `lateMs` late — a
// renderer that has not produced its next frame yet, as on a loaded host — so
// a layout measurement taken before the panel refits reads the stale width.
const lateResizeObserver = (lateMs) => {
  const Native = window.ResizeObserver;
  if (!Native) return;
  window.ResizeObserver = class extends Native {
    constructor(callback) {
      super((entries, observer) => { setTimeout(() => callback(entries, observer), lateMs); });
    }
  };
};
// The panel must speak the spec's contract strings (单聊/群聊/尚无群聊/
// 选择一个会话/未知用户), so every page runs in zh-CN; wording reused from
// existing keys is matched locale-tolerantly.
const h = createHarness({
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN" },
  errors: ["pageerror", "console", "5xx"],
  session: SESSION,
  ready: "E2E pf messages host session",
  server(name, version) {
    const state = readState(); if (version) { state.version = version; write(state); }
    const logFile = join(work, `server-${name}.log`); serverLogs.push(logFile);
    return { name, logFile, env: { PATH: `${stubDir}${delimiter}${process.env.PATH}`, HOME: work, PI_CODING_AGENT_DIR: agentDir,
      PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: KEY_SENTINEL, [ENV_SENTINEL]: KEY_SENTINEL } };
  },
  started: (started) => { base = started.base; },
});
const { errors, openPage, startServer, withPage } = h;
const get = (path) => fetch(`${base}${path}`, { signal: AbortSignal.timeout(60_000) }).then(async (r) => ({ status: r.status, text: await r.text() }));
const post = (path, body) => fetch(`${base}${path}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body ?? {}), signal: AbortSignal.timeout(60_000) })
  .then(async (r) => ({ status: r.status, text: await r.text() }));
const tab = (page, name) => dock(page).getByRole("tab", { name }).or(dock(page).getByRole("button", { name })).first();
const unreadDots = (root) => root.locator('[aria-label*="unread" i], [aria-label*="未读"], [title*="unread" i], [title*="未读"], [data-testid="unread-dot"], [data-unread-dot]');
async function openDock(page) { await pfButton(page).click(); await dock(page).waitFor(); return dock(page); }
async function messagesTab(page) { const t = tab(page, /^(messages|消息)/i); await t.waitFor({ timeout: 15_000 }); await t.click(); return t; }

// the composer binds about_wi from the panel's mapped-project
// groups, which only the list read made while Messages is shown carries (the
// mount read skips them: `?groups=0`). A token send that lands between the two
// reads carries no --about-wi (full-1 PM5: actual undefined). A token-binding
// scenario waits for its seeded project's group before typing, under the
// page-open ceiling and with the assertion unchanged.
async function waitForProjectGroup(panel, project) {
  const label = new RegExp(`^${project.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}[，,]`);
  await panel.getByRole("button", { name: label }).first().waitFor({ timeout: NAV_MS });
}

// ---- panel locators (spec strings; shared keys locale-tolerant) ----
const selectEmpty = (panel) => panel.getByText(/选择一个会话|Select a conversation/).first();
const sectionDirect = (panel) => panel.getByText("单聊", { exact: true }).first();
const sectionGroups = (panel) => panel.getByText("群聊", { exact: true }).first();
const noGroupsText = (panel) => panel.getByText("尚无群聊", { exact: true }).first();
const unknownText = (panel) => panel.getByText("未知用户", { exact: true });
const composeEntry = (panel) => panel.getByRole("button", { name: /新消息|New message/ }).first();
const historyRegion = (panel) => panel.getByLabel(/会话记录|Conversation history/).first();
const messageTextField = (panel) => panel.getByLabel("消息", { exact: true }).first();
// removes the anchor field: this probe must find nothing.
const anchorLabels = (panel) => panel.getByLabel(/工作项锚点|Work item anchor|工作項錨點/);
const quoteButtons = (panel) => panel.getByRole("button", { name: /^引用$|^Quote$/i });
const recipientField = (panel) => panel.getByLabel("收件人", { exact: true }).first();
const sendButton = (panel) => panel.getByRole("button", { name: /^发送$|^Send$/i }).first();
const analyzeButtons = (panel) => panel.getByRole("button", { name: /^分析$|^Analyze$/i });
const replyButtons = (panel) => panel.getByRole("button", { name: /^回复$|^Reply$/i });
const avatarInitialOf = (name) => (name ? [...name.trim()][0].toUpperCase() : "?");

async function requireVisible(locator, what) {
  const count = await locator.count();
  assert.ok(count > 0, `${what} is not rendered`);
  return locator.first();
}

// Open a conversation through the list. `labels` carries the names the new
// layout must show; the peer id is appended as a fallback so the pre-change
// tree can still be driven to an assertion instead of a wiring error.
async function openConversation(page, conv, extraLabels = []) {
  const panel = dock(page);
  const needle = conv.messages.length ? conv.messages[conv.messages.length - 1].text : "";
  const labels = [conv.peerName, ...extraLabels, conv.peer].filter((label) => typeof label === "string" && label !== "");
  const historyShows = async () => needle !== "" && (await panel.getByText(needle, { exact: false }).count()) > 0;
  const deadline = Date.now() + 45_000;
  let lastFailure = "no label matched";
  while (Date.now() < deadline) {
    if (needle && await historyShows()) return;
    for (const label of labels) {
      const matches = panel.getByText(label, { exact: false });
      const count = await matches.count().catch(() => 0);
      for (let i = 0; i < Math.min(count, 6); i++) {
        lastFailure = `the click on ${label} did not open ${conv.id}`;
        await matches.nth(i).click({ timeout: 4000 }).catch(() => {});
        if (!needle) return;
        // Two unnamed rows share the 未知用户 label, so the next candidate can
        // be another conversation. Give this click's read time to land before
        // replacing it: clicking the next candidate immediately cancels an
        // in-flight read (the hook drops it by selection identity), and a
        // loaded host then alternates rows until the deadline instead of
        // opening `conv`. Bounded by the outer deadline and a per-click settle
        // window; the history text is the condition, not a fixed pause.
        const settleUntil = Math.min(deadline, Date.now() + 15_000);
        while (Date.now() < settleUntil) {
          if (await historyShows()) return;
          await delay(100);
        }
      }
    }
  }
  throw new Error(`conversation ${conv.id} is not in the Messages list: ${lastFailure}`);
}

// The history scroll region: the deepest scrollable element containing the newest text.
const historyInfo = (panel, needle) => panel.evaluate((root, needle) => {
  const candidates = [...root.querySelectorAll("*")].filter((el) => {
    const cs = getComputedStyle(el);
    return /(auto|scroll)/.test(cs.overflowY) && el.scrollHeight > el.clientHeight + 20 && (el.textContent || "").includes(needle);
  });
  if (!candidates.length) return null;
  const el = candidates.sort((a, b) => (b.scrollHeight - b.clientHeight) - (a.scrollHeight - a.clientHeight))[0];
  return { text: el.textContent || "", top: el.scrollTop, height: el.scrollHeight, client: el.clientHeight };
}, needle);
async function scrollHistoryTop(panel, needle) {
  await panel.evaluate((root, needle) => {
    const candidates = [...root.querySelectorAll("*")].filter((el) => {
      const cs = getComputedStyle(el);
      return /(auto|scroll)/.test(cs.overflowY) && el.scrollHeight > el.clientHeight + 20 && (el.textContent || "").includes(needle);
    });
    const el = candidates.sort((a, b) => (b.scrollHeight - b.clientHeight) - (a.scrollHeight - a.clientHeight))[0];
    if (el) { el.scrollTop = 0; el.dispatchEvent(new Event("scroll", { bubbles: true })); }
  }, needle);
}
// Visual de-emphasis: contrast to the nearest opaque background × product of opacity.
const emphasis = (panel, needle) => panel.evaluate((root, needle) => {
  const lum = (color) => { const m = /rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*(?:,\s*([\d.]+))?\s*\)/.exec(String(color)); return !m || (m[4] !== undefined && Number(m[4]) === 0) ? null : (0.2126 * Number(m[1]) + 0.7152 * Number(m[2]) + 0.0722 * Number(m[3])) / 255; };
  const el = [...root.querySelectorAll("*")].find((node) => (node.textContent || "").includes(needle) && ![...node.children].some((child) => (child.textContent || "").includes(needle)));
  if (!el) return null;
  let bg = 0.5; for (let node = el; node; node = node.parentElement) { const l = lum(getComputedStyle(node).backgroundColor); if (l !== null) { bg = l; break; } }
  let opacity = 1; for (let node = el; node; node = node.parentElement) opacity *= Number(getComputedStyle(node).opacity || 1);
  const fg = lum(getComputedStyle(el).color); return fg === null ? null : Math.abs(fg - bg) * opacity;
}, needle);

// The panel's whole visible text plus every title/aria-label (the spec's
// disclosure surface: innerText, title, aria-label).
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

// Bounding boxes of every leaf element whose trimmed text is exactly `text`.
const leafBoxes = (panel, text) => panel.evaluate((dom, text) => [...dom.querySelectorAll("*")]
  .filter((node) => (node.textContent || "").trim() === text && ![...node.children].some((child) => (child.textContent || "").trim() === text))
  .map((node) => { const box = node.getBoundingClientRect(); return { x: box.x, y: box.y, width: box.width, height: box.height, right: box.x + box.width, bottom: box.y + box.height }; }), text);

// Avatar elements: a small, roughly square leaf whose text is `initial` and
// which carries a real background colour.
const avatarElements = (root, initial) => root.evaluate((node, initial) => {
  const hits = [];
  for (const el of node.querySelectorAll("*")) {
    if ((el.textContent || "").trim() !== initial) continue;
    if ([...el.children].some((child) => (child.textContent || "").trim() === initial)) continue;
    const box = el.getBoundingClientRect();
    if (box.width < 12 || box.width > 42 || Math.abs(box.width - box.height) > 10) continue;
    const bg = getComputedStyle(el).backgroundColor;
    if (!bg || bg === "transparent" || /rgba\(\s*0\s*,\s*0\s*,\s*0\s*,\s*0\s*\)/.test(bg)) continue;
    hits.push({ bg, x: box.x, y: box.y, width: box.width, height: box.height, centre: box.y + box.height / 2 });
  }
  return hits;
}, initial);

// An avatar rendered immediately beside a name (same row, left of the text).
const avatarBeside = (root, text, initial) => root.evaluate((node, [text, initial]) => {
  const leaf = [...node.querySelectorAll("*")].find((el) => (el.textContent || "").trim() === text
    && ![...el.children].some((child) => (child.textContent || "").trim() === text));
  if (!leaf) return null;
  const anchor = leaf.getBoundingClientRect();
  const hits = [];
  for (const el of node.querySelectorAll("*")) {
    if ((el.textContent || "").trim() !== initial) continue;
    if ([...el.children].some((child) => (child.textContent || "").trim() === initial)) continue;
    const box = el.getBoundingClientRect();
    if (box.width < 12 || box.width > 42 || Math.abs(box.width - box.height) > 10) continue;
    const bg = getComputedStyle(el).backgroundColor;
    if (!bg || bg === "transparent" || /rgba\(\s*0\s*,\s*0\s*,\s*0\s*,\s*0\s*\)/.test(bg)) continue;
    if (Math.abs((box.y + box.height / 2) - (anchor.y + anchor.height / 2)) > 16) continue;
    if (box.x >= anchor.x) continue;
    hits.push({ bg, x: box.x, y: box.y, width: box.width, height: box.height });
  }
  return hits;
}, [text, initial]);

// The list column: the innermost element holding both section headers and
// the empty-群聊 placeholder (the compose entry it used to hold is gone —
// ruling 5 removes the feature). Its box is measured for the geometry
// assertions.
const listColumn = (panel) => panel.evaluate((root) => {
  const has = (node, needle) => (node.textContent || "").includes(needle);
  const header = [...root.querySelectorAll("*")].find((node) => (node.textContent || "").trim() === "单聊");
  if (!header) return null;
  for (let node = header; node && node !== root; node = node.parentElement) {
    if (has(node, "单聊") && has(node, "群聊") && has(node, "尚无群聊")) {
      const box = node.getBoundingClientRect();
      return { x: box.x, y: box.y, width: box.width, height: box.height, right: box.x + box.width };
    }
  }
  return null;
});

const clamp = (value, low, high) => Math.min(high, Math.max(low, value));
// The desktop list's resize separator; the mobile sheet
// renders none.
const listSeparator = (panel) => panel.getByRole("separator", { name: /调整会话列表宽度|Resize conversation list/ }).first();

const waitForColumnWidth = async (panel, predicate, what) => {
  const deadline = Date.now() + 10_000;
  let width = 0;
  while (Date.now() < deadline) {
    width = (await listColumn(panel))?.width ?? 0;
    if (predicate(width)) return width;
    await delay(50);
  }
  throw new Error(`${what}: the list width stayed ${Math.round(width)}px`);
};

// a layout check measured right after a resize can read the
// frame before the panel refits. Re-run the check until it holds, bounded at
// 10 s like waitForColumnWidth; past the bound the check's own failure is the
// one reported. The check's assertions and numbers are never changed here.
const untilLayoutHolds = async (checkLayout) => {
  const deadline = Date.now() + 10_000;
  for (;;) {
    try { return await checkLayout(); } catch (error) { if (Date.now() >= deadline) throw error; }
    await delay(50);
  }
};

// Drag the list's left divider by deltaX (negative widens, positive
// narrows). The handle straddles the divider and spans the list's height.
const dragListSeparator = async (page, panel, deltaX) => {
  const handle = await requireVisible(listSeparator(panel), "the list resize separator");
  const box = await handle.boundingBox();
  assert.ok(box, "the list resize separator must have a box");
  const x = box.x + box.width / 2;
  const y = box.y + box.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + deltaX, y, { steps: 8 });
  await page.mouse.up();
};

const storedListWidth = (page) => page.evaluate(() => localStorage.getItem("pi-web:polyforge-messages-list-width"));


try {
  await h.launchBrowser(() => chromium.launch());
  await startServer("main");

  seed();
  await check(T.PM1, "PM1 dot beside the running badge", () => withPage(async (page) => {
    await openDock(page);
    const button = pfButton(page);
    assert.ok((await button.textContent() || "").includes("2"), "the topbar button must still show the running count 2");
    const messages = await messagesTab(page);
    assert.ok((await unreadDots(messages).count()) >= 1 || /unread|未读/i.test(await messages.getAttribute("aria-label") || ""), "the Messages tab must show an unread dot");
    assert.ok((await unreadDots(button).count()) >= 1, "the topbar button must show the unread dot beside the running count");
    assert.ok((await button.textContent() || "").includes("2"), "the unread dot must not replace the running-count badge");
    // Zero unread: a fresh page with the dot's conversation acknowledged.
    const state = readState(); state.acked.conv_dot = 1; write(state);
    await page.context().close();
    const page2 = await openPage();
    try {
      await openDock(page2);
      const messages2 = await messagesTab(page2);
      assert.equal(await unreadDots(messages2).count(), 0, "zero unread must show no dot on the tab");
      assert.equal(await unreadDots(pfButton(page2)).count(), 0, "zero unread must show no dot on the topbar button");
    } finally { await page2.context().close(); }
  }));

  seed();
  await check(T.PM2, "PM2 newest page then scroll-up paging", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page); await openConversation(page, PAGED);
    const panel = dock(page);
    await panel.getByText("MSG-120", { exact: false }).first().waitFor({ timeout: 20_000 });
    let info = await historyInfo(panel, "MSG-120");
    assert.ok(info, "the conversation history must be a scrollable region");
    assert.ok(info.top + info.client >= info.height - 24, `opening must be positioned at the bottom (${info.top}/${info.height - info.client})`);
    assert.ok(info.text.includes("MSG-071"), "the newest page must start at MSG-071");
    assert.ok(!info.text.includes("MSG-070"), "MSG-070 belongs to the next page and must not be loaded yet");

    await scrollHistoryTop(panel, "MSG-120");
    await panel.getByText("MSG-070", { exact: false }).first().waitFor({ timeout: 20_000 });
    info = await historyInfo(panel, "MSG-120");
    assert.ok(info.text.includes("MSG-021"), "the page below 71 must start at MSG-021");
    assert.ok(!info.text.includes("MSG-020"), "MSG-020 belongs to the third page");
    assert.equal((info.text.match(/MSG-071/g) || []).length, 1, "the page boundary must not duplicate MSG-071");
    assert.ok(msgCalls().some((a) => a[1] === "list" && argAfter(a, "--before") === "1071"), "scroll-up must request --before 1071");

    await scrollHistoryTop(panel, "MSG-120");
    await panel.getByText("MSG-001", { exact: false }).first().waitFor({ timeout: 20_000 });
    const beforeCount = msgCalls().filter((a) => a[1] === "list" && argAfter(a, "--before")).length;
    await scrollHistoryTop(panel, "MSG-120");
    await delay(1500);
    assert.equal(msgCalls().filter((a) => a[1] === "list" && argAfter(a, "--before")).length, beforeCount, "the oldest message must end paging");
  }));

  seed({ conversations: [conversation("conv_ack", PEER, PEER_NAME, [msg(1, "self", "ACK-ONE"), msg(2, "self", "ACK-TWO"), msg(3, "peer", "ACK-THREE")])],
    acked: { conv_ack: 2 } });
  await check(T.PM3, "PM3 visible auto-ack names the newest displayed seq", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page); await openConversation(page, readState().conversations[0]);
    for (let i = 0; i < 20 && !acks().length; i++) await delay(500);
    const first = acks();
    assert.equal(first.length, 1, `exactly one automatic ack must be sent, got ${JSON.stringify(first)}`);
    assert.equal(argAfter(first[0], "--seq"), "3", "the ack must name the newest displayed sequence 3");
    assert.equal(readState().acked.conv_ack, 3, "the ack must be recorded server-side");
    for (let i = 0; i < 12 && await unreadDots(dock(page)).count(); i++) await delay(500);
    assert.equal(await unreadDots(dock(page)).count(), 0, "unread must clear after the ack");

    // A newer message the user has not seen yet: the history page still ends at 3.
    const state = readState();
    state.conversations[0].messages.push(msg(4, "peer", "ACK-FOUR"));
    state.conversations[0].lastSeq = 4; state.historyCap = 3; write(state);
    await delay(4200);
    assert.ok(!acks().some((a) => argAfter(a, "--seq") === "4"), "a sequence the user has not seen must never be acked");
    state.historyCap = undefined; state.conversations[0].lastSeq = 4; write(state);
    for (let i = 0; i < 20 && !acks().some((a) => argAfter(a, "--seq") === "4"); i++) await delay(500);
    assert.ok(acks().some((a) => argAfter(a, "--seq") === "4"), "once sequence 4 is displayed it must be acked");
  }));

  seed({ conversations: [conversation("conv_hidden", PEER, PEER_NAME, [msg(1, "self", "HIDDEN-ONE"), msg(2, "peer", "HIDDEN-TWO")])], acked: { conv_hidden: 1 } });
  await check(T.PM4, "PM4 hidden and blocked suppress auto-ack", () => withPage(async (page) => {
    await openDock(page);
    // The panel is open but the Messages tab is not active: arrivals never ack.
    let state = readState(); state.conversations[0].messages.push(msg(3, "peer", "HIDDEN-THREE")); state.conversations[0].lastSeq = 3; write(state);
    await delay(4200);
    assert.equal(acks().length, 0, "another tab must not acknowledge an arrival");
    // Panel closed: still nothing.
    await page.getByRole("button", { name: /close polyforge|关闭 polyforge/i }).first().click();
    state = readState(); state.conversations[0].messages.push(msg(4, "peer", "HIDDEN-FOUR")); state.conversations[0].lastSeq = 4; write(state);
    await delay(4200);
    assert.equal(acks().length, 0, "a closed panel must not acknowledge an arrival");
    // Visible, but a local job is blocked: still no automatic ack or auto-switch.
    state = readState(); state.blocked = true; write(state);
    await openDock(page); await messagesTab(page); await openConversation(page, state.conversations[0]);
    await delay(4200);
    assert.equal(acks().length, 0, "a blocked local job must suppress automatic acknowledgment");
    assert.ok(await dock(page).getByText("HIDDEN-FOUR", { exact: false }).count() > 0, "the panel must not switch tabs or drop the conversation");
    // An explicit user action acknowledges the newest displayed sequence.
    await dock(page).getByText("HIDDEN-FOUR", { exact: false }).last().click();
    for (let i = 0; i < 20 && !acks().length; i++) await delay(500);
    assert.equal(acks().length, 1, "an explicit click must send the acknowledgment");
    assert.equal(argAfter(acks()[0], "--seq"), "4", "the explicit ack must name the newest displayed sequence");
    assert.equal(readState().acked.conv_hidden, 4, "the explicit ack must clear unread");
  }));

  await check(T.PM4, "PM4 arrival while visible and blocked keeps unread and scroll", () => {
    const blockedConversation = conversation("conv_blocked", PEER, PEER_NAME, Array.from({ length: 60 }, (_, i) =>
      msg(i + 1, i % 2 ? "peer" : "self", `BLOCKED-${String(i + 1).padStart(3, "0")}`)));
    seed({ conversations: [blockedConversation], acked: { conv_blocked: 60 }, blocked: true });
    return withPage(async (page) => {
      await openDock(page); await messagesTab(page); await openConversation(page, blockedConversation);
      const panel = dock(page);
      await panel.getByText("BLOCKED-060", { exact: false }).first().waitFor({ timeout: 20_000 });
      const before = await historyInfo(panel, "BLOCKED-060");
      assert.ok(before && before.height > before.client + 20, "the blocked conversation needs an overflowing history");

      // A new message arrives while that overflowing conversation is visible and
      // a local job is blocked: no ack and no auto-scroll.
      const state = readState();
      state.conversations[0].messages.push(msg(61, "peer", "BLOCKED-061"));
      state.conversations[0].lastSeq = 61;
      write(state);
      await delay(4200);
      assert.equal(acks().length, 0, "a blocked arrival while visible must not be acknowledged");
      // Seed acked conv_blocked at 60, so the blocked arrival takes unread from
      // 0 to 1: this sub-case must prove the unread growth, not just the no-ack.
      const listed = JSON.parse((await get("/api/polyforge/messages")).text);
      assert.equal(listed.conversations.find((row) => row.id === "conv_blocked")?.unread, 1,
        "the blocked arrival must remain unread (unread count rises from 0 to 1)");
      const after = await historyInfo(panel, "BLOCKED-060");
      assert.ok(after, "the history must stay rendered");
      assert.equal(after.top, before.top, "a blocked job must suppress auto-scroll");
    });
  });

  await check(T.PM5, "PM5 list-view first contact starts a conversation", () => withPage(async (page) => {
    const FIRST_PEER = "u_e2e_first_peer";
    // The configured contact has no display name yet (ruling 5 removes the
    // feature: the first contact is its silent contact row, not a typed id).
    seed({ conversations: [], acked: {}, contacts: [{ id: FIRST_PEER, display_name: "" }] });
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    // The empty conversation list still carries the configured contact: a
    // user with no conversation sends the first message from its row.
    await unknownText(panel).first().waitFor({ timeout: 15_000 });
    await (await requireVisible(unknownText(panel), "the silent contact row for the first contact")).click();
    const text = messageTextField(panel).or(panel.getByLabel(/^message|message text/i)).first();
    await text.fill("FIRST-MESSAGE");
    await sendButton(panel).click();
    for (let i = 0; i < 20 && !sends().length; i++) await delay(500);
    assert.equal(sends().length, 1, `a list-view send must spawn exactly one pf msg send, got ${JSON.stringify(sends())}`);
    assert.equal(argAfter(sends()[0], "--to"), FIRST_PEER);
    assert.equal(argAfter(sends()[0], "--text"), "FIRST-MESSAGE");
    assert.equal(argAfter(sends()[0], "--about-wi"), undefined, "a text without a @project#number token carries no --about-wi");
    assert.equal(await anchorLabels(panel).count(), 0, "the composer carries no work-item anchor field");
    assert.equal(readState().sends.length, 1, "the stub ledger must record exactly one send");
    // The created conversation has no display name yet: the list and the
    // header show 未知用户, never the typed user id.
    await panel.getByText("FIRST-MESSAGE", { exact: false }).first().waitFor({ timeout: 15_000 });
    await requireVisible(unknownText(panel), "the unknown-name placeholder for the created conversation");
    const created = readState().conversations.find((conversation) => conversation.peer === FIRST_PEER);
    assert.ok(created, "the stub must have created the direct conversation");
    const shown = await disclosure(panel);
    assert.ok(!shown.text.includes(FIRST_PEER), "a created conversation must not render the typed user id");
    assert.ok(!appearsIn(shown.labels, FIRST_PEER), "no title or aria-label may carry the typed user id");
  }));

  seed({ conversations: [conversation("conv_anchor", PEER, PEER_NAME, ANCHOR.messages)], acked: { conv_anchor: 4 }, mappedProjects: ["acme"] });
  await check(T.PM5, "PM5 one send, reply id, documented refusal", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page); await openConversation(page, ANCHOR);
    const panel = dock(page);
    // The @token binds only after the panel has its mapped projects.
    await waitForProjectGroup(panel, "acme");
    const text = messageTextField(panel);
    await text.fill("SENT-ONE @acme#272");
    await sendButton(panel).click();
    for (let i = 0; i < 20 && !sends().length; i++) await delay(500);
    assert.equal(sends().length, 1, `a send must spawn exactly one pf msg send, got ${JSON.stringify(sends())}`);
    assert.equal(argAfter(sends()[0], "--to"), PEER, "the open conversation's send must address the route's peer id");
    assert.equal(argAfter(sends()[0], "--text"), "SENT-ONE @acme#272", "the @ token stays in the sent text");
    assert.equal(argAfter(sends()[0], "--about-wi"), "acme#272", "the text token feeds the unchanged about_wi wire");
    assert.equal(readState().sends.length, 1, "the stub ledger must record exactly one send");
    // Wait for the send to settle (the composer clears) before starting the
    // reply; the ledger records the spawn, not the route's completion.
    await panel.getByText("SENT-ONE", { exact: false }).first().waitFor({ timeout: 15_000 });
    for (let i = 0; i < 20 && (await text.inputValue()) !== ""; i++) await delay(250);
    assert.equal(await text.inputValue(), "", "the composer must clear after a successful send");

    // Reply to a loaded original: the original is quoted and its id is passed.
    await replyButtons(panel).first().click();
    const original = panel.getByText("ANCHOR-ONE", { exact: false });
    assert.ok(await original.count() >= 2, "a loaded original must render as a quote while replying");
    await messageTextField(panel).fill("REPLY-ONE");
    await sendButton(panel).click();
    for (let i = 0; i < 20 && sends().length < 2; i++) await delay(500);
    const reply = sends()[1] || [];
    assert.equal(argAfter(reply, "--reply-to"), "msg_1_peer", "a reply must pass --reply-to <original id>");
    await panel.getByText("REPLY-ONE", { exact: false }).last().waitFor({ timeout: 15_000 });
    assert.ok(await panel.getByText("ANCHOR-ONE", { exact: false }).count() >= 2, "the history must render the loaded original as a quote");

    // A refused send: 4xx with pf's documented text, never a 5xx route.
    const state = readState(); state.sendError = "CORE-422"; write(state);
    const refused = await post("/api/polyforge/messages", { to: PEER, text: `refused ${SECRET_SENTINEL}` });
    assert.ok(refused.status >= 400 && refused.status < 500, `a refused send must answer 4xx, got ${refused.status}`);
    assert.match(refused.text, /CORE-422|secret-shaped/i, "the refusal must surface pf's documented text");
  }));

  seed({ conversations: [conversation("conv_anchor", PEER, PEER_NAME, ANCHOR.messages)], acked: { conv_anchor: 4 } });
  await check(T.PM6, "PM6", () => withPage(async (page) => {
    const beforeSession = readFileSync(sessionFile, "utf8");
    await openDock(page); await messagesTab(page); await openConversation(page, ANCHOR);
    const panel = dock(page);
    // Analyzer: one control per received message, none on the user's own message.
    assert.equal(await analyzeButtons(panel).count(), 3, "the analyzer control must appear only on the three received messages");
    assert.equal(await panel.getByText("OWN-MESSAGE", { exact: false }).count() > 0, true);
    await analyzeButtons(panel).first().click();
    await panel.getByText(ANALYSIS_SUMMARY, { exact: false }).first().waitFor({ timeout: 10_000 });
    // The structured card replaces the old one-line gray conclusion.
    assert.ok(await panel.getByText("需求", { exact: true }).count() > 0, "the card must render the localized type badge");
    assert.ok(await panel.getByText(ANALYSIS_MODEL, { exact: false }).count() > 0, "the card must render the model");
    assert.ok((await panel.textContent() || "").includes(ANALYSIS_DURATION), "the card must render the duration");
    assert.equal(await panel.getByText(ANALYSIS_CONCLUSION, { exact: false }).count(), 0, "no surface may read or render the retired conclusion");
    assert.ok(await panel.getByRole("button", { name: /建票|File ticket/i }).count() > 0, "the card must carry the ticket action");
    assert.ok(await panel.getByRole("button", { name: /回复|Reply/i }).count() > 0, "the card must carry the reply action");
    for (const store of ["localStorage", "sessionStorage"]) {
      const dump = await page.evaluate((s) => JSON.stringify(globalThis[s]), store);
      assert.ok(!dump.includes(ANALYSIS_SUMMARY), `the transient analysis must not be written to ${store}`);
      assert.ok(!dump.includes(ANALYSIS_CONCLUSION), `the retired conclusion must not be written to ${store}`);
    }
    const written = readdirSync(agentDir, { recursive: true }).filter((f) => typeof f === "string")
      .map((f) => join(agentDir, f)).filter((p) => { try { const text = readFileSync(p, "utf8"); return text.includes(ANALYSIS_SUMMARY) || text.includes(ANALYSIS_CONCLUSION); } catch { return false; } });
    assert.deepEqual(written, [], "the analysis must not be written to disk");
    // Transient: closing and reopening the dock drops the card.
    await page.getByRole("button", { name: /close polyforge|关闭 polyforge/i }).first().click();
    await openDock(page); await messagesTab(page); await openConversation(page, ANCHOR);
    assert.equal(await panel.getByText(ANALYSIS_SUMMARY, { exact: false }).count(), 0, "the card must not persist across a reopen");

    // Incoming messages never enter the chat session or the composer.
    assert.equal(readFileSync(sessionFile, "utf8"), beforeSession, "an incoming message must not be written into the chat session");
    const composers = await panel.locator("textarea").evaluateAll((els) => els.map((el) => el.value));
    for (const value of composers) assert.ok(!value.includes(BODY_SENTINEL), "an incoming message must not be written into the composer");

    // A work-item anchor switches to Jobs and runs the existing watch read.
    await panel.getByRole("button", { name: /task-39/ }).or(panel.getByRole("link", { name: /task-39/ })).first().click();
    // owner-intended change: the current step moved from the old detail header's visible text into
    // the title row's hover; the same guarantee (this work item's watch read opened its detail) is read from
    // the title row carrying the slug with the watched current step on hover.
    await panel.locator('[data-pf-detail-title][title*="WATCH-DETAIL-39"]').getByText("task-39").first().waitFor({ timeout: 15_000 });
    assert.ok(calls().some((a) => a[0] === "watch" && a[1] === "task-39"), "the anchor must run the existing watch read for task-39");
    // The deep reference exposes only its work-item part.
    await messagesTab(page); await openConversation(page, ANCHOR);
    assert.ok(await panel.getByText("task-39/artifacts/spec.md", { exact: false }).count() > 0, "the deep reference must render");
    assert.equal(await panel.getByRole("link", { name: /artifacts\/spec\.md/ }).count(), 0, "the artifact path must not be clickable");
    assert.equal(await panel.getByRole("button", { name: /artifacts\/spec\.md/ }).count(), 0, "the artifact path must not be clickable");
    // Unanchored messages keep chronological order but are de-emphasized.
    const order = await panel.evaluate((root) => ["ANCHOR-DEEP", "UNANCHORED-ONE", "OWN-MESSAGE"].map((needle) =>
      [...root.querySelectorAll("*")].find((el) => (el.textContent || "").includes(needle) && ![...el.children].some((c) => (c.textContent || "").includes(needle)))?.getBoundingClientRect().top ?? -1));
    assert.ok(order[0] < order[1] && order[1] < order[2], `messages must stay chronological, got tops ${order}`);
    const anchored = await emphasis(panel, "ANCHOR-ONE"); const unanchored = await emphasis(panel, "UNANCHORED-ONE");
    assert.ok(anchored !== null && unanchored !== null && unanchored < anchored, `the unanchored message must be de-emphasized (${unanchored} !< ${anchored})`);
  }));

  seed();
  await check(T.PM7, "PM7 sentinels", () => withPage(async (page) => {
    const listed = await get("/api/polyforge/messages");
    assert.equal(listed.status, 200, `the messages list route answered ${listed.status}: ${listed.text.slice(0, 160)}`);
    // The new name field is present where it belongs (a peer's name) …
    assert.ok(listed.text.includes(PEER_NAME), "the messages route must carry the peer's display name");
    // … and the self id, the self display name and the credentials never are.
    for (const sentinel of [KEY_SENTINEL, USER_SENTINEL, SELF_NAME, SERVER_SENTINEL, ENV_SENTINEL]) {
      assert.ok(!listed.text.includes(sentinel), `the messages route must not carry ${sentinel}`);
    }
    const history = await get("/api/polyforge/messages/conv_anchor?before=999");
    assert.equal(history.status, 200, `the conversation history route answered ${history.status}`);
    for (const sentinel of [KEY_SENTINEL, USER_SENTINEL, SELF_NAME, SERVER_SENTINEL, ENV_SENTINEL]) {
      assert.ok(!history.text.includes(sentinel), `the history route must not carry ${sentinel}`);
    }
    // A refused secret-shaped send leaves the sentinel out of the answer.
    const state = readState(); state.sendError = "CORE-422"; write(state);
    const refused = await post("/api/polyforge/messages", { to: PEER, text: `leak ${SECRET_SENTINEL}` });
    assert.ok(refused.status >= 400 && refused.status < 500, `the refusal must answer 4xx, got ${refused.status}`);
    assert.ok(!refused.text.includes(SECRET_SENTINEL), "the refused body sentinel must not appear in the answer");
    // Message bodies and credentials never reach the pi-web server log.
    await openDock(page); await messagesTab(page); await openConversation(page, ANCHOR);
    await analyzeButtons(dock(page)).first().click().catch(() => {});
    await delay(500);
    for (const logFile of serverLogs) {
      const log = readFileSync(logFile, "utf8");
      for (const sentinel of [KEY_SENTINEL, USER_SENTINEL, SELF_NAME, SERVER_SENTINEL, ENV_SENTINEL, BODY_SENTINEL, SECRET_SENTINEL]) {
        assert.ok(!log.includes(sentinel), `the pi-web server log must not carry ${sentinel}`);
      }
    }
    assert.ok(!errors.some((line) => /^5\d\d /.test(line) || /Internal Server Error/.test(line)), `no route may answer 5xx: ${errors.join(" | ")}`);
  }));

  await check(T.PM8, "PM8 below-floor upgrade state", async () => {
    // A fresh server process re-probes pf, so the floor is seen (the probe is
    // cached per process); no message verb may be spawned below 2.20.12
    // (moves the floor: below-floor fixture 2.20.11).
    await h.stop();
    writeFileSync(argvLog, "");
    await startServer("floor", "v2.20.11");
    const floor = await get("/api/polyforge/messages");
    assert.equal(floor.status, 200, `the below-floor messages route answered ${floor.status}`);
    assert.match(floor.text, /"capable"\s*:\s*false/, "below the floor the route must report capable:false");
    assert.match(floor.text, /2\.20\.12|upgrade|升级/i, "below the floor the reason must name the 2.20.12 upgrade");
    assert.equal(msgCalls().length, 0, "below the floor no message verb may be spawned");
    const page3 = await openPage();
    try {
      await openDock(page3); await messagesTab(page3);
      assert.ok(await dock(page3).getByText(/upgrade|升级/i).count() > 0, "below the floor the tab must show the upgrade-needed state");
      assert.equal(await dock(page3).getByText("单聊", { exact: true }).count(), 0, "below the floor no conversation list may render");
    } finally { await page3.context().close(); }
    // At or above the floor (2.20.12) the panel is usable with names.
    await h.stop();
    writeFileSync(argvLog, "");
    await startServer("supported", "v2.20.12");
    const supported = await get("/api/polyforge/messages");
    assert.match(supported.text, /"capable"\s*:\s*true/, "at 2.20.12 the route must report capable:true");
    const page4 = await openPage();
    try {
      await openDock(page4); await messagesTab(page4);
      await requireVisible(dock(page4).getByText("单聊", { exact: true }), "the 单聊 section at the supported floor");
    } finally { await page4.context().close(); }
  });

  // A silent contact is part of the inspected surface (the compose entry and
  // its picker are gone — ruling 5 removes the feature).
  seed({ conversations: [ANCHOR, BUBBLES], acked: { conv_anchor: 4, conv_bubbles: 5 }, contacts: [{ id: "u_e2e_silent_peer", display_name: "Sid Silent" }] });
  await check(T.PM9, "PM9 zero raw id in the panel", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page); await openConversation(page, ANCHOR);
    const panel = dock(page);
    await requireVisible(panel.getByText("Sid Silent", { exact: true }), "the silent contact row");
    const shown = await disclosure(panel);
    assert.ok(shown.text.includes(PEER_NAME), "the peer display name must be rendered (the panel is on its names)");
    for (const id of [PEER, USER_SENTINEL, "u_e2e_silent_peer"]) {
      assert.ok(!shown.text.includes(id), `the panel text must not carry the raw user id ${id}`);
      assert.ok(!appearsIn(shown.labels, id), `no title or aria-label may carry the raw user id ${id}`);
    }
  }));

  seed({ conversations: [ANCHOR, BUBBLES], acked: { conv_anchor: 4, conv_bubbles: 5 }, contacts: [{ id: "u_e2e_cleo_peer", display_name: "Cleo Silent" }] });
  await check(T.PM10, "PM10 names and avatars on every surface", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page); await openConversation(page, ANCHOR);
    const panel = dock(page);
    const column = await listColumn(panel);
    assert.ok(column, "the conversation list column is not rendered");
    const historyBox = await (await requireVisible(historyRegion(panel), "the conversation history")).boundingBox();
    assert.ok(historyBox, "the history must have a box");

    // Every surface inside the panel: the list row, the chat header, the peer
    // bubble attribution, and a silent contact row.
    const boxes = await leafBoxes(panel, PEER_NAME);
    assert.ok(boxes.length > 0, `the peer display name ${PEER_NAME} is not rendered anywhere`);
    assert.ok(boxes.some((box) => box.x >= column.x - 2), "the list row must show the peer display name");
    assert.ok(boxes.some((box) => box.bottom <= historyBox.y + 2), "the chat header must show the peer display name");
    assert.ok(boxes.some((box) => box.y >= historyBox.y), "the first peer bubble of the run must show the display name");
    assert.ok((await avatarBeside(panel, PEER_NAME, avatarInitialOf(PEER_NAME))).length > 0,
      "an initial avatar must sit beside every rendered display name");

    // The picker is gone (ruling 5 removes the feature): the silent contact
    // row is the remaining list surface, by display name and avatar.
    const silent = await leafBoxes(panel, "Cleo Silent");
    assert.ok(silent.some((box) => box.x >= column.x - 2), "a silent contact row must list the contact by display name");
    assert.ok((await avatarBeside(panel, "Cleo Silent", avatarInitialOf("Cleo Silent"))).length > 0,
      "a silent contact row must show the contact's initial avatar");
  }));

  seed({ conversations: [NAMELESS, ABSENT], acked: {} });
  await check(T.PM11, "PM11 unknown name placeholder and no id fallback", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    // List: one 未知用户 row per unnamed conversation, each with a ? avatar.
    assert.equal(await unknownText(panel).count(), 2, "each unnamed conversation row must render 未知用户");
    assert.ok((await avatarElements(panel, "?")).length >= 2, "an unnamed peer must get a ? avatar in its row");
    const listed = await disclosure(panel);
    for (const id of [NAMELESS.peer, ABSENT.peer]) {
      assert.ok(!listed.text.includes(id), `the list must not substitute the raw id ${id} for the name`);
      assert.ok(!appearsIn(listed.labels, id), `no title or aria-label may carry the raw id ${id}`);
    }

    // Header and bubble attribution for an empty display name.
    await openConversation(page, NAMELESS, ["未知用户"]);
    const historyBox = await (await requireVisible(historyRegion(panel), "the conversation history")).boundingBox();
    assert.ok(historyBox, "the history must have a box");
    const boxes = await leafBoxes(panel, "未知用户");
    assert.ok(boxes.some((box) => box.bottom <= historyBox.y + 2), "the header must show 未知用户 for the unnamed peer");
    assert.ok(boxes.some((box) => box.y >= historyBox.y), "the peer bubble attribution must show 未知用户");
    assert.ok((await avatarBeside(panel, "未知用户", "?")).length > 0, "the header/attribution must show the ? avatar");

    // A blank-name silent contact (the picker is gone — ruling 5 removes the
    // feature) is offered as 未知用户 with a ? avatar, still without ids.
    const BLANK_CONTACT = "u_e2e_blank_contact";
    const placeholders = await unknownText(panel).count();
    const configured = readState(); configured.contacts = [{ id: BLANK_CONTACT, display_name: "" }]; write(configured);
    for (let i = 0; i < 40 && await unknownText(panel).count() <= placeholders; i++) await delay(250);
    assert.ok(await unknownText(panel).count() > placeholders, "the blank-name silent contact must render 未知用户");
    assert.ok((await avatarElements(panel, "?")).length >= 2, "the silent contact must show a ? avatar for an unnamed peer");
    const shown = await disclosure(panel);
    for (const id of [NAMELESS.peer, ABSENT.peer, USER_SENTINEL, BLANK_CONTACT]) {
      assert.ok(!shown.text.includes(id), `no surface may render the raw id ${id}`);
      assert.ok(!appearsIn(shown.labels, id), `no title or aria-label may carry the raw id ${id}`);
    }
  }));

  seed({ conversations: [DOT, ANCHOR], acked: { conv_anchor: 4, conv_dot: 0 } });
  await check(T.PM12, "PM12 tab open selects and acks nothing", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await requireVisible(selectEmpty(panel), "the 选择一个会话 empty state");
    assert.equal(await historyRegion(panel).count(), 0, "opening the tab must not open a conversation");
    assert.equal(await panel.getByText("DOT-UNREAD-ONE", { exact: false }).count(), 0, "no message history may render before a click");
    await delay(4200);
    assert.equal(acks().length, 0, "opening the tab must not acknowledge any conversation");
    // The click is the gesture: it opens the conversation and acknowledges the
    // newest displayed sequence through the existing semantics.
    await panel.getByText("Dot Rowan", { exact: true }).or(panel.getByText(DOT.peer, { exact: false })).first().click();
    await panel.getByText("DOT-UNREAD-ONE", { exact: false }).first().waitFor({ timeout: 15_000 });
    for (let i = 0; i < 20 && !acks().length; i++) await delay(500);
    assert.equal(acks().length, 1, "the click must send exactly one acknowledgment");
    assert.equal(argAfter(acks()[0], "--seq"), "1", "the click must acknowledge the newest displayed sequence");
    assert.equal(readState().acked.conv_dot, 1, "the click must clear the unread state server-side");
  }));

  seed({ contacts: [{ id: "u_e2e_sam_peer", display_name: "Sam Silent" }] });
  await check(T.PM13, "PM13 right-hand list column and rows", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page); await openConversation(page, ANCHOR);
    const panel = dock(page);
    const column = await listColumn(panel);
    assert.ok(column, "the conversation list column (单聊/群聊 + 尚无群聊) is not rendered");
    const panelBox = await panel.boundingBox();
    const historyBox = await (await requireVisible(historyRegion(panel), "the bubble stream")).boundingBox();
    assert.ok(panelBox && historyBox);
    assert.ok(column.x >= historyBox.x + historyBox.width - 8, `the list must sit right of the bubble stream (${column.x} vs ${historyBox.x + historyBox.width})`);
    assert.ok(column.x + column.width >= panelBox.x + panelBox.width - 18, "the list must be flush with the panel's right edge");
    const expected = clamp(panelBox.width * 0.35, 120, 220);
    assert.ok(column.width >= 118 && column.width <= 222, `the list width ${Math.round(column.width)}px must be clamped to 120–220px`);
    assert.ok(Math.abs(column.width - expected) <= 22, `the list width ${Math.round(column.width)}px must be about 35% of ${Math.round(panelBox.width)}px (${Math.round(expected)}px)`);

    // Row contents: initial avatar, display name and unread badge.
    const dotBoxes = await leafBoxes(panel, "Dot Rowan");
    assert.ok(dotBoxes.some((box) => box.x >= column.x - 2), "a row must show the peer display name");
    assert.ok((await avatarBeside(panel, "Dot Rowan", avatarInitialOf("Dot Rowan"))).length > 0, "a row must show the initial-plus-colour avatar");
    assert.ok(await unreadDots(panel.getByRole("button", { name: /Dot Rowan/ }).first()).count() > 0, "an unread row must show its unread badge");

    // Sections in order, 群聊 empty with 尚无群聊; no compose entry (ruling 5
    // removes the feature) — a contact row sits in the 单聊 area instead.
    const directBox = await (await requireVisible(sectionDirect(panel), "the 单聊 section")).boundingBox();
    const groupsBox = await (await requireVisible(sectionGroups(panel), "the 群聊 section")).boundingBox();
    const noGroupsBox = await (await requireVisible(noGroupsText(panel), "the 尚无群聊 placeholder")).boundingBox();
    assert.ok(directBox.y < groupsBox.y && groupsBox.y < noGroupsBox.y, "单聊, then 群聊, then 尚无群聊 must render in that order");
    assert.equal(await composeEntry(panel).count(), 0, "no 新消息 compose entry may remain");
    assert.equal(await recipientField(panel).count(), 0, "no typed recipient field may remain");
    const contactBox = await (await requireVisible(panel.getByText("Sam Silent", { exact: true }), "the silent contact row")).boundingBox();
    assert.ok(directBox.y < contactBox.y && contactBox.y < groupsBox.y, "the contact row must sit in the 单聊 area");
    assert.ok(contactBox.x >= column.x - 2, "the contact row must be inside the list column");

    // Long names are ellipsized inside the column.
    const ellipsis = await panel.evaluate((dom, name) => {
      const leaf = [...dom.querySelectorAll("*")].find((el) => (el.textContent || "").trim() === name
        && ![...el.children].some((child) => (child.textContent || "").trim() === name));
      if (!leaf) return null;
      for (let node = leaf; node && node !== dom; node = node.parentElement) {
        const style = getComputedStyle(node);
        if (style.textOverflow === "ellipsis") return { clipped: node.scrollWidth > node.clientWidth + 1, width: node.clientWidth };
      }
      return { clipped: false, width: leaf.clientWidth };
    }, LONG_NAME);
    assert.ok(ellipsis, `the long display name ${LONG_NAME} must render in its row`);
    assert.ok(ellipsis.clipped, "a long display name must be ellipsized inside the 120–220px column");
  }));

  // owner-intended change: the desktop list is drag-resizable from its
  // left divider, bounded by 120px…min(480px, panel−200px), defaulting to the
  // old 35% clamp, and its width survives a reload.
  seed({ conversations: [ANCHOR], acked: { conv_anchor: 4 } });
  await check(T.PM13, "PM13 desktop list resize: drag bounds, keyboard reset and reload persistence", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page); await openConversation(page, ANCHOR);
    const panel = dock(page);
    const panelBox = await panel.boundingBox();
    assert.ok(panelBox, "the dock must have a box");
    const defaultWidth = await waitForColumnWidth(panel, (width) => width >= 118 && width <= 222, "the untouched default list width");
    const expectedDefault = clamp(panelBox.width * 0.35, 120, 220);
    assert.ok(Math.abs(defaultWidth - expectedDefault) <= 2,
      `an untouched desktop list keeps the 35% default (${Math.round(defaultWidth)} vs ${Math.round(expectedDefault)}px)`);

    const handle = await requireVisible(listSeparator(panel), "the desktop list resize separator");
    assert.equal(await handle.getAttribute("role"), "separator", "the list divider is a separator");
    assert.equal(await handle.getAttribute("aria-valuemin"), "120", "the separator advertises the 120px minimum");
    const max = Math.min(480, Math.round(panelBox.width - 200));
    const ariaMax = Number(await handle.getAttribute("aria-valuemax"));
    assert.ok(Math.abs(ariaMax - max) <= 2, `the separator's max is min(480, panel−200) (${ariaMax} vs ${max})`);
    assert.ok(Math.abs(Number(await handle.getAttribute("aria-valuenow")) - defaultWidth) <= 2, "aria-valuenow is the shown width");

    // Without a stored choice the default still follows the panel: at the
    // dock's 340px minimum it is 35% clamped to 120, and it returns when the
    // dock does. (The dock's own Enter resets the dock to 460.)
    const dockHandle = await requireVisible(page.locator('[data-resize-handle="polyforge-dock"]').first(), "the dock resize handle");
    await dockHandle.focus();
    await dockHandle.press("Home");
    const narrowPanelBox = await panel.boundingBox();
    assert.ok(narrowPanelBox, "the narrowed dock must have a box");
    const narrowDefault = clamp(narrowPanelBox.width * 0.35, 120, 220);
    await waitForColumnWidth(panel, (width) => Math.abs(width - narrowDefault) <= 2,
      `the untouched default at the ${Math.round(narrowPanelBox.width)}px dock (${Math.round(narrowDefault)}px)`);
    const narrowColumn = await listColumn(panel);
    assert.ok(narrowPanelBox.width - narrowColumn.width >= 198,
      `the chat pane must keep at least 200px at the dock minimum (${Math.round(narrowPanelBox.width - narrowColumn.width)}px)`);
    await dockHandle.press("Enter");
    await waitForColumnWidth(panel, (width) => Math.abs(width - expectedDefault) <= 2, "the untouched default after restoring the dock");

    // Dragging left widens the list; the chat pane keeps its 200px floor.
    await dragListSeparator(page, panel, -(defaultWidth < max - 70 ? 70 : Math.max(0, max - defaultWidth - 10)));
    const widened = await waitForColumnWidth(panel, (width) => width > defaultWidth + 40, "a left drag widening the list");
    assert.ok(widened <= max + 2, `the widened list ${Math.round(widened)}px must stay within ${max}px`);
    const widenedBox = await listColumn(panel);
    assert.ok(panelBox.width - widenedBox.width >= 198,
      `the chat pane must keep at least 200px after the drag (${Math.round(panelBox.width - widenedBox.width)}px)`);

    // Past the cap: the display and the saved value both stop at the cap.
    await dragListSeparator(page, panel, -400);
    const capped = await waitForColumnWidth(panel, (width) => Math.abs(width - max) <= 2, "a drag past the maximum");
    const cappedBox = await listColumn(panel);
    assert.ok(panelBox.width - cappedBox.width >= 198,
      `the chat pane must keep at least 200px at the maximum (${Math.round(panelBox.width - cappedBox.width)}px)`);
    assert.equal(await storedListWidth(page), String(Math.round(capped)), "the capped drag stores the width it ended at");

    // Past the minimum: 120px wins.
    await dragListSeparator(page, panel, 400);
    const floored = await waitForColumnWidth(panel, (width) => width >= 118 && width <= 122, "a drag past the minimum");
    assert.ok(floored >= 118 && floored <= 122, `the list must clamp at 120px, got ${Math.round(floored)}`);
    assert.equal(await storedListWidth(page), "120", "the minimum drag stores 120px");

    // Keyboard: End reaches the same cap, Enter resets and stores the default.
    await handle.focus();
    await handle.press("End");
    await waitForColumnWidth(panel, (width) => Math.abs(width - max) <= 2, "End at the maximum");
    await handle.press("Enter");
    const reset = await waitForColumnWidth(panel, (width) => Math.abs(width - expectedDefault) <= 2, "Enter reset to the default");
    assert.equal(await storedListWidth(page), String(Math.round(reset)), "the reset stores the default it returns to");

    // A fresh drag is remembered across a reload (same browser context).
    await dragListSeparator(page, panel, -30);
    const chosen = await waitForColumnWidth(panel, (width) => width > reset + 20 && width < max - 1, "a drag committing a chosen width");
    const chosenStored = Number(await storedListWidth(page));
    assert.equal(chosenStored, Math.round(chosen), "the drag commits the integer px it ended at");
    await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
    await page.getByText("E2E pf messages host session").first().waitFor({ state: "attached", timeout: NAV_MS });
    await openDock(page); await messagesTab(page); await openConversation(page, ANCHOR);
    const reloaded = dock(page);
    await waitForColumnWidth(reloaded, (width) => Math.abs(width - chosenStored) <= 1, "the stored width after a reload");
  }, {}));

  seed({ conversations: [BUBBLES], acked: { conv_bubbles: 5 } });
  await check(T.PM14, "PM14 header shows avatar and name", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page); await openConversation(page, BUBBLES);
    const panel = dock(page);
    const historyBox = await (await requireVisible(historyRegion(panel), "the bubble stream")).boundingBox();
    assert.ok(historyBox);
    const boxes = await leafBoxes(panel, "Bea Run");
    const header = boxes.find((box) => box.bottom <= historyBox.y + 2);
    assert.ok(header, "the open conversation header must show the peer display name");
    assert.ok((await avatarBeside(panel, "Bea Run", avatarInitialOf("Bea Run"))).length > 0, "the header must show the peer's initial avatar");
    const headerSurface = await panel.evaluate((dom, name) => {
      const leaf = [...dom.querySelectorAll("*")].find((el) => (el.textContent || "").trim() === name
        && ![...el.children].some((child) => (child.textContent || "").trim() === name));
      if (!leaf) return null;
      let node = leaf;
      for (let i = 0; i < 3 && node.parentElement && node.parentElement !== dom; i++) node = node.parentElement;
      const labels = [];
      for (const el of node.querySelectorAll("*")) for (const attribute of ["title", "aria-label"]) { const v = el.getAttribute(attribute); if (v) labels.push(v); }
      return { text: node.innerText || node.textContent || "", labels };
    }, "Bea Run");
    assert.ok(headerSurface, "the header must render the peer name");
    assert.ok(!headerSurface.text.includes(BUBBLES.peer), "the header must not carry the peer id");
    assert.ok(!appearsIn(headerSurface.labels, BUBBLES.peer), "no header title or aria-label may carry the peer id");
  }));

  seed({ conversations: [BUBBLES], acked: { conv_bubbles: 5 } });
  await check(T.PM15, "PM15 bubble alignment and attribution", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page); await openConversation(page, BUBBLES);
    const panel = dock(page);
    const history = historyRegion(panel);
    await requireVisible(history, "the bubble stream");
    const historyBox = await history.boundingBox();
    assert.ok(historyBox);
    const centre = historyBox.x + historyBox.width / 2;

    const own = (await leafBoxes(panel, "OWN-BUBBLE"))[0];
    const peer = (await leafBoxes(panel, "PEER-RUN-ONE"))[0];
    assert.ok(own && peer, "both the own and the peer message must render");
    assert.ok(own.x + own.width / 2 > centre, `an own bubble must sit right of the centre (${own.x + own.width / 2} vs ${centre})`);
    assert.ok(peer.x + peer.width / 2 < centre, `a peer bubble must sit left of the centre (${peer.x + peer.width / 2} vs ${centre})`);

    // The four consecutive peer messages name/avatar only on the first.
    assert.equal(await history.getByText("Bea Run", { exact: true }).count(), 1, "the peer name must appear once for a consecutive run");
    assert.equal((await avatarElements(history, avatarInitialOf("Bea Run"))).length, 1, "the peer avatar must appear once for a consecutive run");

    // An own bubble carries no peer name and no avatar.
    const ownRow = await history.evaluate((dom, needle) => {
      const leaf = [...dom.querySelectorAll("*")].find((el) => (el.textContent || "").includes(needle)
        && ![...el.children].some((child) => (child.textContent || "").includes(needle)));
      if (!leaf) return null;
      let node = leaf;
      for (let i = 0; i < 3 && node.parentElement && node.parentElement !== dom; i++) node = node.parentElement;
      let avatars = 0;
      for (const el of node.querySelectorAll("*")) {
        if ((el.textContent || "").trim() !== "B") continue;
        const box = el.getBoundingClientRect();
        const bg = getComputedStyle(el).backgroundColor;
        if (box.width >= 12 && box.width <= 42 && Math.abs(box.width - box.height) <= 10 && bg && bg !== "transparent" && !/rgba\(\s*0\s*,\s*0\s*,\s*0\s*,\s*0\s*\)/.test(bg)) avatars++;
      }
      return { text: node.innerText || node.textContent || "", avatars };
    }, "OWN-BUBBLE");
    assert.ok(ownRow, "the own message must render");
    assert.ok(!ownRow.text.includes("Bea Run"), "an own bubble must carry no peer name");
    assert.equal(ownRow.avatars, 0, "an own bubble must carry no avatar");

    // Timestamps, the work-item anchor quote and the reply quote remain.
    const text = await history.innerText();
    assert.match(text, /\b\d{2}:\d{2}\b/, "each message must still show its timestamp");
    assert.ok(text.includes("task-39"), "the work-item anchor quote must still render");
    assert.ok((await leafBoxes(panel, "PEER-RUN-ONE")).length >= 2, "the reply must quote its original message");
  }));

  // PM16's desktop procedure, run as it is and with a late layout refit.
  // The list column refits to the panel from a
  // ResizeObserver, which the browser runs in its next rendering frame: right
  // after the dock resizes, the column still has the old dock's width until
  // that frame comes, and on a loaded host it comes after a measurement taken
  // at once. Each layout check below therefore runs on the settled layout
  // (untilLayoutHolds); its numbers are the ones it always asserted.
  const pm16Desktop = async (page) => {
    await openDock(page); await messagesTab(page); await openConversation(page, ANCHOR);
    const panel = dock(page);
    const twoColumns = async (where) => {
      const column = await listColumn(panel);
      assert.ok(column, `the list column must render at ${where}`);
      const historyBox = await (await requireVisible(historyRegion(panel), `the bubble stream at ${where}`)).boundingBox();
      assert.ok(historyBox, `the bubble stream must render at ${where}`);
      assert.ok(column.x >= historyBox.x + historyBox.width - 8, `the list and the stream must be two columns at ${where}`);
    };
    await untilLayoutHolds(async () => {
      const dockBox = await panel.boundingBox();
      assert.ok(dockBox, "the dock must have a box");
      assert.ok(Math.abs(dockBox.width - 460) <= 2, `the dock default width must stay 460px, got ${Math.round(dockBox.width)}`);
      await twoColumns("the default dock width");
    });

    // The dock's own separator jumps to the 340px minimum (Home): the two
    // columns stay at the tightest dock width.
    const handle = await requireVisible(page.locator('[data-resize-handle="polyforge-dock"]').first(), "the dock resize handle");
    await handle.focus();
    await handle.press("Home");
    await untilLayoutHolds(async () => {
      const narrow = await panel.boundingBox();
      assert.ok(Math.abs(narrow.width - 340) <= 2, `the dock must clamp at its 340px minimum, got ${Math.round(narrow.width)}`);
      await twoColumns("the minimum dock width");
      const narrowColumn = await listColumn(panel);
      const narrowExpected = clamp(narrow.width * 0.35, 120, 220);
      assert.ok(Math.abs(narrowColumn.width - narrowExpected) <= 22,
        `the column ${Math.round(narrowColumn.width)}px must stay within the clamp (${Math.round(narrowExpected)}px)`);
    });
  };
  seed();
  await check(T.PM16, "PM16 desktop two columns and mobile two columns", () => withPage(pm16Desktop, {}));

  // the same procedure when the panel's refit lands 1.5 s
  // after the dock jumps to its minimum (the stale 35%-of-460px column the
  // flaky runs measured): the assertions must read the settled layout.
  seed();
  await check(T.PM16, "PM16 desktop two columns after a late layout refit", () => withPage(pm16Desktop, { init: [[lateResizeObserver, 1500]] }));

  seed({ conversations: [DOT, ANCHOR], acked: { conv_anchor: 4, conv_dot: 1 } });
  // owner-intended change（重立自 #209）: the mobile sheet keeps the
  // desktop two-pane layout — the list column (clamped width, divider) stays
  // beside the chat pane before, while and after a conversation is open, and
  // the ← back control still closes the conversation (was a single view).
  await check(T.PM16, "PM16 mobile two columns with a back control", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    await requireVisible(sectionDirect(panel), "the list column on mobile");
    assert.equal(await historyRegion(panel).count(), 0, "nothing is auto-opened on mobile");
    const sheet = await panel.boundingBox();
    const empty = await (await requireVisible(selectEmpty(panel), "the select-a-conversation pane on mobile")).boundingBox();
    const column = await listColumn(panel);
    assert.ok(sheet && empty && column, "the sheet, the empty pane and the list column must render");
    assert.ok(column.x >= empty.x + empty.width - 8, "the list column sits right of the empty chat pane on mobile");
    const expected = clamp(sheet.width * 0.35, 120, 220);
    assert.ok(Math.abs(column.width - expected) <= 22, `the mobile column ${Math.round(column.width)}px must resolve from the clamp (${Math.round(expected)}px)`);
    assert.equal(await listSeparator(panel).count(), 0, "the mobile sheet renders no list resize separator");
    const divider = await panel.evaluate((root) => {
      const header = [...root.querySelectorAll("*")].find((node) => (node.textContent || "").trim() === "单聊");
      // the sheet's 单聊 heading sits in the column's 40px
      // header row, which carries the divider above the scroll region.
      const row = header?.closest("[data-pf-messages-column-header]");
      if (row) return parseFloat(getComputedStyle(row).borderLeftWidth) || 0;
      for (let node = header; node && node !== root; node = node.parentElement) {
        const text = node.textContent || "";
        if (text.includes("单聊") && text.includes("群聊") && text.includes("尚无群聊")) return parseFloat(getComputedStyle(node).borderLeftWidth) || 0;
      }
      return 0;
    });
    assert.ok(divider >= 1, `the mobile list column keeps the desktop divider (${divider}px)`);
    await panel.getByText("Dot Rowan", { exact: true }).or(panel.getByText(DOT.peer, { exact: false })).first().click();
    const history = await (await requireVisible(historyRegion(panel), "the chat pane on mobile")).boundingBox();
    await requireVisible(sectionDirect(panel), "the list column beside the open chat on mobile");
    const openColumn = await listColumn(panel);
    assert.ok(history && openColumn && openColumn.x >= history.x + history.width - 8, "the list and the open chat are two columns on mobile");
    await requireVisible(panel.getByRole("button", { name: /←/ }).first(), "the mobile back control").then((back) => back.click());
    await historyRegion(panel).waitFor({ state: "detached", timeout: 10_000 });
    assert.equal(await historyRegion(panel).count(), 0, "the back control closes the conversation");
    await requireVisible(sectionDirect(panel), "the list column after going back");
    await requireVisible(selectEmpty(panel), "the select-a-conversation pane after going back");
  }, { viewport: { width: 600, height: 900 } }));

  // The picker and the typed id are gone (ruling 5 removes the feature): the
  // list itself offers the existing peers (a configured contact that already
  // has a conversation appears once) and the silent contacts.
  seed({ conversations: [ANCHOR, BUBBLES], acked: { conv_anchor: 4, conv_bubbles: 5 }, contacts: [{ id: PEER, display_name: PEER_NAME }] });
  await check(T.PM17, "PM17 peer rows and a silent first contact", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page);
    const panel = dock(page);
    assert.equal(await composeEntry(panel).count(), 0, "no 新消息 compose entry may remain");
    assert.equal(await recipientField(panel).count(), 0, "no typed recipient field may remain");

    // The list offers exactly the existing peers, by display name + avatar.
    for (const name of [PEER_NAME, "Bea Run"]) {
      assert.ok(await panel.getByText(name, { exact: true }).count() >= 1, `the list must offer ${name}`);
      assert.ok((await avatarBeside(panel, name, avatarInitialOf(name))).length > 0, `the list must show ${name}'s initial avatar`);
    }
    assert.equal(await panel.getByText(PEER_NAME, { exact: true }).count(), 1, "a contact with a conversation must appear once");
    assert.equal(await unknownText(panel).count(), 0, "no unnamed peer exists in this seed");
    const shown = await disclosure(panel);
    for (const id of [PEER, "u_e2e_bubbles_peer", USER_SENTINEL]) {
      assert.ok(!shown.text.includes(id), `the panel must not render the raw id ${id}`);
      assert.ok(!appearsIn(shown.labels, id), `no title or aria-label may carry the raw id ${id}`);
    }

    // A peer row sends to that conversation's peer id (no name lookup).
    await openConversation(page, BUBBLES);
    await messageTextField(panel).fill("PICKED-PEER");
    await sendButton(panel).click();
    for (let i = 0; i < 20 && !sends().length; i++) await delay(500);
    assert.equal(sends().length, 1, `a peer row must spawn exactly one send, got ${JSON.stringify(sends())}`);
    assert.equal(argAfter(sends()[0], "--to"), "u_e2e_bubbles_peer", "a peer must be addressed by its user id, never its name");

    // A first contact is its silent contact row; the created row has no name.
    const configured = readState(); configured.contacts = [...configured.contacts, { id: "u_e2e_typed_peer", display_name: "" }]; write(configured);
    const silent = unknownText(panel);
    for (let i = 0; i < 40 && !(await silent.count()); i++) await delay(250);
    await (await requireVisible(silent, "the silent contact row for a first contact")).click();
    await messageTextField(panel).fill("FIRST-CONTACT");
    await sendButton(panel).click();
    for (let i = 0; i < 20 && sends().length < 2; i++) await delay(500);
    assert.equal(sends().length, 2, "a first contact must spawn exactly one more send");
    assert.equal(argAfter(sends()[1], "--to"), "u_e2e_typed_peer", "the contact's user id must be the send address");
    assert.equal(argAfter(sends()[1], "--text"), "FIRST-CONTACT");
    await panel.getByText("FIRST-CONTACT", { exact: false }).first().waitFor({ timeout: 15_000 });
    await requireVisible(unknownText(panel), "the 未知用户 placeholder for the new unnamed peer");
  }));

  // the composer's anchor field is gone; the message text is
  // the binding, bubbles link every valid token, and own messages quote.
  seed({ conversations: [WORK_ITEM], acked: { conv_work_item: 4 }, mappedProjects: ["acme"] });
  await check(T.PM19, "PM19 inline @project#number binding, links and own-message quote", () => withPage(async (page) => {
    await openDock(page); await messagesTab(page); await openConversation(page, WORK_ITEM);
    const panel = dock(page);
    const text = messageTextField(panel);
    const field = () => text;
    // The binding cases below read the mapped projects; wait for the group
    // before the first send so @acme#272 always has its project known.
    await waitForProjectGroup(panel, "acme");

    // The field is gone; the placeholder names the token form.
    assert.equal(await anchorLabels(panel).count(), 0, "the composer must carry no work-item anchor field");
    assert.equal(await text.getAttribute("placeholder"), "输入消息；写 @项目#号（如 @acme#272）可关联工单",
      "the textarea carries the zh-CN @project#number placeholder");

    // Own bubble: 引用 only. Peer bubbles: the unchanged 回复.
    assert.equal(await quoteButtons(panel).count(), 1, "the one own message carries 引用");
    assert.equal(await replyButtons(panel).count(), 3, "the three peer messages keep 回复");
    assert.equal(await quoteButtons(panel).first().getAttribute("aria-label"), null, "the quote action is its visible text, not a hidden label");

    // The duplicated anchor line is hidden; the historical one stays.
    assert.equal(await panel.getByRole("button", { name: "acme#272", exact: true }).count(), 2,
      "the duplicated inline token and the own bubble token are the two links");
    assert.equal(await panel.getByText("acme#272", { exact: true }).count(), 0,
      "a bubble whose about_wi duplicates an inline token shows no separate anchor line");
    assert.ok(await panel.getByText("task-39", { exact: true }).count() > 0,
      "a historical about_wi without an inline token keeps its anchor line");
    assert.equal(await panel.getByRole("button", { name: "task-39", exact: true }).count(), 1,
      "the historical anchor line stays clickable");

    // 【引用】: exact block, append, never overwrite, replyTo untouched.
    await quoteButtons(panel).first().click();
    assert.equal(await field().inputValue(), "> OWN-LINK @acme#272\n\n",
      "an empty draft becomes the plain-text quote block");
    await quoteButtons(panel).first().click();
    assert.equal(await field().inputValue(), "> OWN-LINK @acme#272\n\n> OWN-LINK @acme#272\n\n",
      "a repeated click appends the block again");
    await field().fill("DRAFT-A");
    await quoteButtons(panel).first().click();
    assert.equal(await field().inputValue(), "DRAFT-A\n\n> OWN-LINK @acme#272\n\n",
      "a non-empty draft keeps its text, a blank line, then the block");
    assert.equal(await field().evaluate((el) => document.activeElement === el
      && el.selectionStart === el.value.length && el.selectionEnd === el.value.length), true,
      "the textarea is focused with the caret at the end");
    // A pending peer reply survives the own quote, and the later send carries it.
    await replyButtons(panel).first().click();
    await quoteButtons(panel).first().click();
    await field().fill("AFTER-QUOTE");
    await sendButton(panel).click();
    for (let i = 0; i < 20 && !sends().length; i++) await delay(500);
    assert.equal(sends().length, 1, "the own quote never sends by itself");
    assert.equal(argAfter(sends()[0], "--reply-to"), "msg_1_peer", "a pending peer reply survives an own quote");
    // With no pending reply, an own quote adds no reply target at all.
    for (let i = 0; i < 40 && ((await field().inputValue()) !== "" || await sendButton(panel).isDisabled()); i++) await delay(250);
    await quoteButtons(panel).first().click();
    const ownOnly = await field().inputValue();
    await field().fill(`OWN-ONLY ${ownOnly.trim()}`);
    await sendButton(panel).click();
    for (let i = 0; i < 40 && sends().length < 2; i++) await delay(250);
    assert.equal(sends().length, 2);
    assert.equal(argAfter(sends()[1], "--reply-to"), undefined, "an own quote never makes the later send reply to the own message");
    for (let i = 0; i < 40 && ((await field().inputValue()) !== "" || await sendButton(panel).isDisabled()); i++) await delay(250);

    // One send per binding case; the text rides verbatim and only a known,
    // word-bounded token on a plain line supplies --about-wi.
    const sendText = async (value) => {
      const before = sends().length;
      await field().fill(value);
      await sendButton(panel).click();
      for (let i = 0; i < 40 && sends().length <= before; i++) await delay(250);
      assert.equal(sends().length, before + 1, `one send expected for ${JSON.stringify(value)}`);
      for (let i = 0; i < 40 && ((await field().inputValue()) !== "" || await sendButton(panel).isDisabled()); i++) await delay(250);
      return sends()[before];
    };
    const known = await sendText("BIND-ONE @acme#272 now");
    assert.equal(argAfter(known, "--text"), "BIND-ONE @acme#272 now", "the @ token stays in the sent text");
    assert.equal(argAfter(known, "--about-wi"), "acme#272", "a known project token binds about_wi");
    assert.equal(argAfter(await sendText("BIND-UNKNOWN @nope#12"), "--about-wi"), undefined,
      "an unmapped project is plain text with no --about-wi");
    assert.equal(argAfter(await sendText("BIND-BAD @acme#272abc"), "--about-wi"), undefined,
      "a word character after the number makes no token");
    assert.equal(argAfter(await sendText("BIND-QUOTE\n> @acme#272\nplain"), "--about-wi"), undefined,
      "a token only on a > quote line never binds");
    assert.equal(argAfter(await sendText("BIND-TWO @acme#272 then @acme#999"), "--about-wi"), "acme#272",
      "the first token wins");
    assert.equal(argAfter(await sendText("> @acme#999\nBIND-REAL @acme#272"), "--about-wi"), "acme#272",
      "the quote line is skipped for the first plain-line token");

    // A bubble token click runs the existing watch read for its ref.
    await panel.getByRole("button", { name: "acme#272", exact: true }).first().click();
    const watchDeadline = Date.now() + 15_000;
    while (Date.now() < watchDeadline && !calls().some((a) => a[0] === "watch" && a[1] === "acme#272")) await delay(250);
    assert.ok(calls().some((a) => a[0] === "watch" && a[1] === "acme#272"),
      "the inline link must run the existing watch read for acme#272");
    await panel.locator('[data-pf-detail-title]').first().waitFor({ timeout: 15_000 });
  }));

  seed();
  await check(T.PM18, "PM18 route peer/peer_name and send addressing", () => withPage(async (page) => {
    const listed = await get("/api/polyforge/messages");
    assert.equal(listed.status, 200, `the messages list route answered ${listed.status}: ${listed.text.slice(0, 160)}`);
    const body = JSON.parse(listed.text);
    assert.ok(Array.isArray(body.conversations) && body.conversations.length > 0, "the route must list conversations");
    const anchorRow = body.conversations.find((row) => row.id === "conv_anchor");
    assert.ok(anchorRow, "the route must carry the seeded conversation");
    assert.equal(anchorRow.peer, PEER, "the route must keep peer as the send address");
    assert.equal(anchorRow.peer_name, PEER_NAME, "the route must add peer_name with the display name");
    for (const row of body.conversations) {
      if (typeof row.peer_name !== "string" || row.peer_name === "") continue;
      assert.ok(!row.peer_name.includes(row.peer), "peer_name must never be the raw id");
    }
    assert.ok(!listed.text.includes(USER_SENTINEL), "no messages route response may carry the self user id");
    assert.ok(!listed.text.includes(SELF_NAME), "no messages route response may carry the self display name");

    // A display name is never an address; the peer id is.
    const byName = await post("/api/polyforge/messages", { to: PEER_NAME, text: "NAME-ADDRESS" });
    assert.ok(byName.status >= 400 && byName.status < 500, `a display name must never be accepted as an address, got ${byName.status}`);
    const sent = await post("/api/polyforge/messages", { to: PEER, text: "ROUTE-SEND" });
    assert.equal(sent.status, 200, `a send addressed by peer id must succeed, got ${sent.status}: ${sent.text.slice(0, 160)}`);
    for (let i = 0; i < 20 && !sends().length; i++) await delay(500);
    assert.equal(sends().length, 1, "the route send must spawn exactly one pf msg send");
    assert.equal(argAfter(sends()[0], "--to"), PEER, "the send must address the peer user id");
    assert.equal(argAfter(sends()[0], "--text"), "ROUTE-SEND");
    // The DOM never renders the address either (the list stays ids-free).
    await openDock(page); await messagesTab(page);
    const shown = await disclosure(dock(page));
    assert.ok(!shown.text.includes(PEER), "the panel must not render the peer id it addresses sends to");
  }));
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
