import "#test-env";
import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-messages.ts");
const pure = await jiti.import("./polyforge-messages-pure.ts");

test("the Messages floor is 2.20.12 and below it the capability rep says upgrade", async () => {
  // the contact list needs `pf msg contacts` (2.20.12).
  assert.equal(lib.POLYFORGE_MESSAGES_MINIMUM_PF_VERSION, "2.20.12");
  const detect = await jiti.import("./polyforge-detect.ts");
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.6", lib.POLYFORGE_MESSAGES_MINIMUM_PF_VERSION), true);
  // Numeric compare: 2.20.9 < 2.20.12, no string trap.
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.9", lib.POLYFORGE_MESSAGES_MINIMUM_PF_VERSION), true);
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.11", lib.POLYFORGE_MESSAGES_MINIMUM_PF_VERSION), true);
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.12", lib.POLYFORGE_MESSAGES_MINIMUM_PF_VERSION), false);
});

test("the floor has exactly one capability gate and no alternate below-floor read path", () => {
  const source = readFileSync(new URL("./polyforge-messages.ts", import.meta.url), "utf8");
  const uses = source.split("POLYFORGE_MESSAGES_MINIMUM_PF_VERSION").length - 1;
  assert.equal(uses, 2, "definition + the single Messages gate (compare and reason live in the shared floor helper)");
  // the users read shares the one gate, so the call-site
  // count grows; each entry path is named instead of counted.
  assert.equal(source.split("export async function readPolyforgeMessagesCapability").length - 1, 1, "one gate definition");
  assert.equal(source.split("isPolyforgeVersionBelowMinimum(").length - 1, 1, "one floor compare");
  // the analysis floor is a second gate over the same probe and compare.
  assert.equal(source.split("export async function readPolyforgeAnalysisCapability").length - 1, 1, "one analysis gate definition");
  // the prompt floor is a third gate over the same probe and compare.
  assert.equal(source.split("export async function readPolyforgePromptCapability").length - 1, 1, "one prompt gate definition");
  assert.equal(source.split("capabilityAtFloor(").length - 1, 4, "the helper + the Messages, analysis and prompt gates");
  for (const entry of ["readPolyforgeMessages", "readPolyforgeUsers"]) {
    const start = source.indexOf(`export async function ${entry}(`);
    assert.ok(start >= 0, `${entry} exists`);
    const body = source.slice(start, source.indexOf("\n}\n", start));
    assert.match(body, /const capability = await readPolyforgeMessagesCapability\(\);\s*if \(!capability\.capable\) \{\s*return \{ \.\.\.capability,/,
      `${entry} gates on the floor before any spawn`);
  }
  // No degraded/read-only or id-based read path exists below the floor.
  assert.doesNotMatch(source, /degraded[_ ]?mode|read-only[_ ]?mode|fallback/);
});

test("sanitizers filter membership and derive peer/mine without exposing ids or credentials", () => {
  const conversation = lib.sanitizeConversation({
    id: "conv_1", kind: "direct", members: ["u_self", "u_peer"], peer: "u_peer",
    member_names: { u_self: "Self Name", u_peer: "Peer Name", u_third: "Third Name" },
    last_seq: 9, unread: 2, created_at: "2026-10-01T00:00:00Z", preview: "hi",
    server: "http://10.0.0.1", api_key: "SECRET",
  }, "u_self");
  assert.deepEqual(Object.keys(conversation).sort(), ["created_at", "id", "kind", "last_seq", "peer", "peer_name", "unread"]);
  assert.equal(conversation.peer, "u_peer", "the peer is derived from members");
  assert.equal(conversation.peer_name, "Peer Name", "only the peer's own member_names key is read");
  const wire = JSON.stringify(conversation);
  assert.ok(!wire.includes("u_self"), "the caller's own id never leaves");
  assert.ok(!wire.includes("Self Name"), "the caller's own name never leaves");
  assert.ok(!wire.includes("Third Name"), "a non-member's name never leaves");
  // A missing member_names (old-aihub transient) and a non-string/absent
  // value both read as "" — the unknown-user placeholder, never an id.
  const noNames = lib.sanitizeConversation({
    id: "conv_1", kind: "direct", members: ["u_self", "u_peer"], last_seq: 9, unread: 0,
  }, "u_self");
  assert.equal(noNames.peer_name, "");
  const emptyName = lib.sanitizeConversation({
    id: "conv_1", kind: "direct", members: ["u_self", "u_peer"],
    member_names: { u_peer: "" }, last_seq: 9, unread: 0,
  }, "u_self");
  assert.equal(emptyName.peer_name, "");
  const nonString = lib.sanitizeConversation({
    id: "conv_1", kind: "direct", members: ["u_self", "u_peer"],
    member_names: { u_peer: 42 }, last_seq: 9, unread: 0,
  }, "u_self");
  assert.equal(nonString.peer_name, "");
  assert.equal(lib.sanitizeConversation({
    id: "conv_2", kind: "direct", members: ["u_other", "u_peer"], last_seq: 1, unread: 0,
  }, "u_self"), null, "a conversation the caller is not a member of is filtered");
  assert.equal(lib.sanitizeConversation({ id: "not-a-conv" }), null, "an invalid id drops the row");
  const message = lib.sanitizeMessage({
    id: "msg_1", seq: 3, conversation_id: "conv_1", sender_id: "u_peer", sender: "peer",
    text: "body", about_wi: "task-39", reply_to: "msg_0", created_at: "t", mine: true,
    server: "http://10.0.0.1",
  }, "u_self");
  assert.deepEqual(Object.keys(message).sort(), ["about_wi", "conversation_id", "created_at", "id", "mine", "reply_to", "seq", "text"]);
  assert.equal(message.conversation_id, "conv_1");
  assert.equal(message.mine, false, "mine comes from sender_id, not a stub-only sender/mine field");
  assert.ok(!JSON.stringify(message).includes("u_peer"));
  assert.equal(lib.sanitizeMessage({ id: "msg_2", seq: 4, text: "own", sender_id: "u_self" }, "u_self").mine, true);
  assert.equal(lib.sanitizeMessage({ id: "msg_3", seq: 5, text: "own", sender: "self" }).mine, false, "the stub marker is not trusted");
});

test("older pages prepend with no gap or duplicate and newest refreshes merge by id", () => {
  const msg = (seq) => ({ id: `msg_${seq}`, seq, text: `${seq}`, created_at: "", mine: false });
  const page = [71, 72, 73].map(msg);
  const older = [69, 70, 71].map(msg);
  const merged = pure.mergeOlderPage(page, older);
  assert.deepEqual(merged.map((m) => m.seq), [69, 70, 71, 72, 73], "ascending, duplicate 71 collapsed");
  assert.equal(new Set(merged.map((m) => m.seq)).size, merged.length, "no duplicate");
  const newest = [73, 74].map(msg);
  const refreshed = pure.mergeNewestPage(merged, newest);
  assert.deepEqual(refreshed.map((m) => m.seq), [69, 70, 71, 72, 73, 74]);
});

test("ackDecision: visible newest-displayed seq only; hidden/blocked suppress auto; explicit bypasses", () => {
  const base = {
    panelOpen: true, activeTab: "messages", conversationId: "conv_1",
    blocked: false, newestDisplayedSeq: 12, lastAckedSeq: 11,
  };
  assert.equal(pure.ackDecision(base), 12);
  assert.equal(pure.ackDecision({ ...base, panelOpen: false }), null, "closed panel never auto-acks");
  assert.equal(pure.ackDecision({ ...base, activeTab: "jobs" }), null, "another tab never auto-acks");
  assert.equal(pure.ackDecision({ ...base, blocked: true }), null, "a blocked job suppresses auto-ack");
  assert.equal(pure.ackDecision({ ...base, newestDisplayedSeq: 11 }), null, "already-acked seq is not repeated");
  assert.equal(pure.ackDecision({ ...base, newestDisplayedSeq: 13, lastAckedSeq: 12 }), 13, "the unseen 13 is not used while 12 is displayed");
  assert.equal(pure.ackDecision({ ...base, blocked: true, explicit: true }), 12, "an explicit gesture still acks");
});

test("splitAnchor exposes only the work-item part and leaves the rest as text", () => {
  assert.deepEqual(pure.splitAnchor("acme-39/artifacts/spec.md"), { ref: "acme-39", rest: "/artifacts/spec.md" });
  assert.deepEqual(pure.splitAnchor("acme-39"), { ref: "acme-39", rest: "" });
  assert.deepEqual(pure.splitAnchor("pi#108"), { ref: "pi#108", rest: "" });
  assert.deepEqual(pure.splitAnchor("no anchor here"), { ref: "", rest: "no anchor here" });
});

test("the inline token grammar is word-bounded and only the # form (memo D)", () => {
  const refs = (text) => pure.findWorkItemTokens(text).map((token) => token.ref);
  assert.deepEqual(refs("@acme#272"), ["acme#272"], "a bare token at the start hits");
  assert.deepEqual(refs("@acme#272，"), ["acme#272"], "a CJK comma after the digits still hits");
  assert.deepEqual(refs("@acme#272."), ["acme#272"], "a full stop after the digits still hits");
  assert.deepEqual(refs("@acme#272abc"), [], "a word character after the digits misses");
  assert.deepEqual(refs("a@acme#1"), [], "a word character before the @ misses");
  assert.deepEqual(refs("@wi_abc"), [], "no wi_ form");
  assert.deepEqual(refs("@task-272"), [], "no project-N form");
  assert.deepEqual(refs("x @acme#272/y"), ["acme#272"], "the token ends at the path separator");
  assert.deepEqual(refs("@acme#272\n@other-proj_2#9"), ["acme#272", "other-proj_2#9"], "line starts and project punctuation hit");
  assert.deepEqual(refs("@Acme#272"), ["Acme#272"], "the parser keeps the project's case for the binding check");
  const spaced = pure.findWorkItemTokens("> @acme#272\ntext @other#3");
  assert.equal(spaced[0].quoteLine, true, "a line whose first character is > is a quote line");
  assert.equal(spaced[1].quoteLine, false, "a later plain line is not");
});

test("inline link segments keep the @ text and the rest exactly plain", () => {
  assert.deepEqual(pure.splitWorkItemLinks("before @acme#272 after"), [
    { text: "before " }, { text: "@acme#272", ref: "acme#272" }, { text: " after" },
  ]);
  assert.deepEqual(pure.splitWorkItemLinks("plain text"), [{ text: "plain text" }]);
  assert.deepEqual(pure.splitWorkItemLinks("@acme#272/x"), [
    { text: "@acme#272", ref: "acme#272" }, { text: "/x" },
  ], "only the token is a link; /x stays plain text (no /path form)");
});

test("binding takes the first known-project token outside quote lines (memo E)", () => {
  const projects = ["acme", "ieops"];
  assert.equal(pure.firstWorkItemBinding("see @acme#272 now", projects), "acme#272");
  assert.equal(pure.firstWorkItemBinding("> @acme#272\nplain", projects), null, "a quote-line token never binds");
  assert.equal(pure.firstWorkItemBinding("> @other#1\nplain @acme#272", projects), "acme#272", "the quote line is skipped, the next plain token binds");
  assert.equal(pure.firstWorkItemBinding("@unknown#1 @acme#272", projects), "acme#272", "an unknown project is not the binding");
  assert.equal(pure.firstWorkItemBinding("@Acme#272", projects), null, "the project match is case-sensitive");
  assert.equal(pure.firstWorkItemBinding("@acme#272 @ieops#9", projects), "acme#272", "the first valid token wins");
  assert.equal(pure.firstWorkItemBinding("@acme#272abc", projects), null, "an invalid token never binds");
  assert.equal(pure.firstWorkItemBinding("no token here", projects), null);
  assert.equal(pure.firstWorkItemBinding("", projects), null);
});

test("the own-message quote block and append rule are exact (memo A)", () => {
  assert.equal(pure.quoteBlock("a\n\nb"), "> a\n>\n> b\n\n", "empty lines become > and one blank line follows");
  assert.equal(pure.quoteBlock("only"), "> only\n\n");
  assert.equal(pure.appendQuoteBlock("", "only"), "> only\n\n", "an empty draft becomes the block");
  assert.equal(pure.appendQuoteBlock("draft\n\n", "only"), "draft\n\n> only\n\n", "trailing newlines are trimmed, then a blank line");
  assert.equal(pure.appendQuoteBlock("draft", "only"), "draft\n\n> only\n\n");
});

test("the three locales carry the placeholder and quote action and no anchor key", async () => {
  const registry = await jiti.import("./i18n/registry.ts");
  const expected = {
    en: {
      placeholder: "Message — write @project#number (e.g. @acme#272) to link a work item",
      quoteMessage: "Quote",
    },
    "zh-CN": {
      placeholder: "输入消息；写 @项目#号（如 @acme#272）可关联工单",
      quoteMessage: "引用",
    },
    "zh-TW": {
      placeholder: "輸入訊息；寫 @專案#號（例如 @acme#272）可關聯工單",
      quoteMessage: "引用",
    },
  };
  for (const [locale, strings] of Object.entries(expected)) {
    const messages = registry.getLocalePlugin(locale).messages;
    assert.equal(messages["polyforge.messages.textPlaceholder"], strings.placeholder, `${locale} placeholder`);
    assert.equal(messages["polyforge.messages.quoteMessage"], strings.quoteMessage, `${locale} quote action`);
    assert.equal("polyforge.messages.anchor" in messages, false, `${locale} has no anchor key`);
  }
});

test("layout identity helpers: name, initial, colour, sections", () => {
  // conversationName: the trimmed peer display name; "" for absent, empty
  // and blank — the caller renders the placeholder, never an id.
  const named = { id: "conv_1", kind: "direct", peer: "u_peer", peer_name: "Alice Chen", last_seq: 1, unread: 0, created_at: "" };
  assert.equal(pure.conversationName(named), "Alice Chen");
  assert.equal(pure.conversationName({ ...named, peer_name: "  " }), "");
  assert.equal(pure.conversationName({ ...named, peer_name: undefined }), "");
  assert.equal(pure.conversationName({ id: "conv_1", kind: "direct", last_seq: 1, unread: 0, created_at: "" }), "");
  // avatarInitial: the first code point upper-cased, "?" for no name.
  assert.equal(pure.avatarInitial("alice chen"), "A");
  assert.equal(pure.avatarInitial("béa"), "B");
  assert.equal(pure.avatarInitial("😀emoji"), "😀");
  assert.equal(pure.avatarInitial(""), "?");
  assert.equal(pure.avatarInitial("   "), "?");
  // avatarColor: deterministic, differs across seeds, a CSS colour string
  // that never renders the seed.
  const red = pure.avatarColor("u_a");
  assert.equal(pure.avatarColor("u_a"), red);
  assert.notEqual(pure.avatarColor("u_b"), red);
  assert.match(red, /^hsl\(\d+, 45%, 45%\)$/);
  assert.ok(!red.includes("u_a"));
  // groupConversations: direct vs any other kind, order preserved.
  const group = { id: "conv_g", kind: "group", last_seq: 0, unread: 0, created_at: "" };
  const second = { id: "conv_2", kind: "direct", last_seq: 0, unread: 0, created_at: "" };
  const sections = pure.groupConversations([named, group, second]);
  assert.deepEqual(sections.direct.map((c) => c.id), ["conv_1", "conv_2"]);
  assert.deepEqual(sections.groups.map((c) => c.id), ["conv_g"]);
});

test("send validation happens before any spawn; errors map to 4xx and never 5xx", () => {
  assert.equal(lib.validateSendInput({ to: "u_peer", text: "hi" }).ok, true);
  assert.equal(lib.validateSendInput({ to: "-bad", text: "hi" }).ok, false);
  assert.equal(lib.validateSendInput({ to: "u_peer", text: "  " }).ok, false);
  assert.equal(lib.validateSendInput({ to: "u_peer", text: "hi", reply_to: "not-a-message" }).ok, false);
  assert.equal(lib.validateSendInput({ to: "u_peer", text: "hi", about_wi: "bad\nanchor" }).ok, false);
  assert.equal(lib.validateSendInput({ to: "u_peer", text: "hi", about_wi: "x".repeat(201) }).ok, false);
  assert.equal(lib.messageErrorStatus("CORE-422"), 422);
  assert.equal(lib.messageErrorStatus("CORE-404"), 404);
  assert.equal(lib.messageErrorStatus("DAEMON-409"), 409);
  assert.equal(lib.messageErrorStatus("DAEMON-500"), 200, "no 5xx: the analyzer failure is a 200 error field");
  assert.equal(lib.messageErrorStatus(""), 200, "an untyped/unreachable pf is a 200 error field, not an invented code");
});

test("contacts sanitize to exactly id + trimmed display_name; blank names stay blank", () => {
  const contacts = lib.sanitizeContactsAnswer({
    contacts: [
      { id: "u_ava", display_name: "  Ava Stone ", type: "human", role: "writer", email: "a@example.com" },
      { id: "u_blank", display_name: "" },
      { id: "u_nonstring", display_name: 42 },
      { id: "-bad", display_name: "Bad" },
      { display_name: "No Id" },
      "not-an-object",
    ],
    total: 9,
  });
  assert.deepEqual(contacts, [
    { id: "u_ava", display_name: "Ava Stone" },
    { id: "u_blank", display_name: "" },
    { id: "u_nonstring", display_name: "" },
  ]);
  assert.deepEqual(lib.sanitizeContactsAnswer({ contacts: "nope" }), []);
  assert.deepEqual(lib.sanitizeContactsAnswer(null), []);
});

test("directRows: conversation rows first in order, then silent contacts by name, one row per peer", () => {
  const conv = (id, peer, peer_name, kind = "direct") => ({ id, kind, peer, peer_name, last_seq: 1, unread: 1, created_at: "" });
  const dot = conv("conv_dot", "u_dot", "Dot");
  const ava = conv("conv_ava", "u_ava", "Ava");
  const group = conv("conv_g", undefined, undefined, "group");
  const rows = pure.directRows([dot, group, ava], [
    { id: "u_zoe", display_name: "Zoe" },
    { id: "u_ava", display_name: "Ava" },
    { id: "u_blank", display_name: "" },
    { id: "u_ben", display_name: "Ben" },
    { id: "u_zoe", display_name: "Zoe again" },
  ]);
  assert.deepEqual(rows.map((row) => row.kind === "conversation" ? row.conversation.id : row.contact.id),
    ["conv_dot", "conv_ava", "u_ben", "u_zoe", "u_blank"],
    "direct conversations keep their order; silent contacts follow by name, blank last; no group row; no duplicate peer");
  assert.equal(rows[0].conversation.unread, 1, "unread rides the conversation row unchanged");
  assert.deepEqual(pure.directRows([], []), []);
  assert.deepEqual(pure.directRows([], [{ id: "u_a", display_name: "A" }]).map((row) => row.kind), ["contact"],
    "with no conversation at all every contact still renders");
});

test("conversationForContact: only that contact's direct conversation, else null", () => {
  const conv = (id, peer, kind = "direct") => ({ id, kind, peer, peer_name: "", last_seq: 1, unread: 1, created_at: "" });
  const dot = conv("conv_dot", "u_dot");
  const ben = conv("conv_ben", "u_ben");
  const group = conv("conv_g", "u_ben", "group");
  assert.equal(pure.conversationForContact([dot, group, ben], "u_ben"), ben, "the contact's direct conversation, never a group naming the same peer");
  assert.equal(pure.conversationForContact([dot, group], "u_ben"), null, "a group alone is not the contact's conversation");
  assert.equal(pure.conversationForContact([dot], "u_ben"), null, "another peer's conversation does not match");
  assert.equal(pure.conversationForContact([], "u_ben"), null, "no conversation at all");
  assert.equal(pure.conversationForContact([conv("conv_x", undefined)], "u_ben"), null, "a peer-less row never matches");
});

// A stub `pf` on PATH: the version, the list and the contact read are driven
// by a mode file so one process can exercise the read's isolation.
function contactsStub(t) {
  const work = mkdtempSync(join(tmpdir(), "pf-messages-contacts-"));
  const mode = join(work, "mode.json");
  const argvLog = join(work, "argv.log");
  writeFileSync(argvLog, "");
  writeFileSync(join(work, "pf"), `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(argvLog)}, JSON.stringify(args) + "\\n");
const m = JSON.parse(fs.readFileSync(${JSON.stringify(mode)}, "utf8"));
const rest = args.filter((a) => a !== "--json");
const out = (v) => { process.stdout.write(JSON.stringify(v)); process.exit(0); };
if (args.includes("--version")) { process.stdout.write(m.version + "\\n"); process.exit(0); }
if (rest[0] === "config") out({ items: [
  { key: "user_id", value: "u_self" },
  { key: '[workspace."/srv/one"]', value: m.mapping ?? "beta, alpha", section: true },
  { key: '[workspace."/srv/two"]', value: "alpha", section: true },
] });
if (rest[0] === "msg" && rest[1] === "contacts") {
  if (m.contactsFail) { process.stderr.write("CORE-503: contacts are unavailable\\n"); process.exit(1); }
  out({ contacts: [{ id: "u_ava", display_name: "Ava", type: "human" }] });
}
if (rest[0] === "users" && rest[1] === "list") {
  if (m.usersFail) { process.stderr.write(JSON.stringify({ code: "AUTH-403", detail: "admin rights are required" })); process.exit(4); }
  out({ items: [
    { id: "u_ava", display_name: " Ava ", user_type: "human", email: "ava@example.com", role: "ROLE-SENTINEL", api_tokens: ["TOKEN-1"] },
    { id: "u_self", display_name: "Self", user_type: "human" },
    { id: "u_blank", display_name: "", user_type: "human", email: "blank@example.com" },
    { id: "u_robot", display_name: "Robot", user_type: "machine" },
    { id: "u_untyped", display_name: "Untyped" },
    { id: "-bad", display_name: "Bad", user_type: "human" },
  ] });
}
if (rest[0] === "projects" && rest[1] === "members") {
  if (m.membersUnavailable) { process.stderr.write("pf: daemon unreachable (connection refused)\\n"); process.exit(1); }
  if (m.membersError) { process.stderr.write(JSON.stringify({ code: m.membersError, detail: "membership refused by policy" })); process.exit(4); }
  out({ ok: true });
}
if (rest[0] === "projects" && rest[1] === "get") {
  if (m.projectHang === rest[2]) { setTimeout(() => {}, 600000); return; }
  if (m.projectFail === rest[2]) { process.stderr.write(JSON.stringify({ code: "CORE-503", detail: rest[2] + " members are unavailable" })); process.exit(4); }
  const members = { alpha: ["u_ava", "u_ben", "u_machine", "u_self", "u_ava"], beta: ["u_ava"] }[rest[2]] ?? [];
  out({ project: rest[2], members: members.map((id) => ({ user_id: id, role: "maintainer", email: id + "@example.com" })) });
}
if (rest[0] === "msg" && rest[1] === "list") {
  if (m.listFail) { process.stderr.write("AUTH-401: bad key\\n"); process.exit(1); }
  out({ conversations: [{ id: "conv_1", kind: "direct", members: ["u_self", "u_peer"], member_names: { u_peer: "Peer" }, last_seq: 2, unread: 1 }] });
}
if (rest[0] === "file" && rest[1] === "create") {
  if (m.createUnavailable) { process.stderr.write("pf: daemon unreachable (connection refused)\\n"); process.exit(1); }
  if (m.createError) { process.stderr.write(JSON.stringify({ code: m.createError, detail: "goal is too vague" })); process.exit(4); }
  out({ id: "wi_new", slug: "alpha#7", goal: "g", content: "SECRET-ECHO", bundle_name: null });
}
process.exit(2);
`);
  chmodSync(join(work, "pf"), 0o755);
  const oldPath = process.env.PATH;
  process.env.PATH = `${work}${delimiter}${oldPath}`;
  t.after(() => { process.env.PATH = oldPath; rmSync(work, { recursive: true, force: true }); });
  return {
    set: (value) => writeFileSync(mode, JSON.stringify({ version: "v2.20.12", ...value })),
    calls: () => readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)),
    reset: () => writeFileSync(argvLog, ""),
  };
}

test("the messages read carries contacts; a contact failure leaves conversations, capable and error untouched", async (t) => {
  const stub = contactsStub(t);
  const status = await jiti.import("./polyforge-status.ts");

  stub.set({});
  await status.probePolyforgeVersion(true);
  const ok = await lib.readPolyforgeMessages();
  assert.equal(ok.capable, true);
  assert.equal(ok.error, undefined);
  assert.deepEqual(ok.conversations.map((c) => c.id), ["conv_1"]);
  assert.deepEqual(ok.contacts, [{ id: "u_ava", display_name: "Ava" }], "contacts are sanitized to id + display_name");

  stub.set({ contactsFail: true });
  const failed = await lib.readPolyforgeMessages();
  assert.deepEqual(failed.contacts, [], "a failed contact read is an empty set");
  assert.equal(failed.capable, true, "a contact failure is not a capability downgrade");
  assert.equal(failed.error, undefined, "a contact failure never becomes the messages error");
  assert.equal(failed.reason, undefined);
  assert.deepEqual(failed.conversations, ok.conversations, "the conversation rows survive a contact failure");

  stub.set({ listFail: true });
  const listFailed = await lib.readPolyforgeMessages();
  assert.match(listFailed.error ?? "", /bad key/, "a list failure keeps its own error");
  assert.deepEqual(listFailed.contacts, [{ id: "u_ava", display_name: "Ava" }], "the contact read is independent of the list read");

  stub.set({ version: "v2.20.11" });
  await status.probePolyforgeVersion(true);
  stub.reset();
  const below = await lib.readPolyforgeMessages();
  assert.equal(below.capable, false);
  assert.match(below.reason ?? "", /2\.20\.12/);
  assert.deepEqual(below.contacts, []);
  assert.deepEqual(below.groups, [], "below the floor the answer carries no groups");
  assert.equal(stub.calls().filter((argv) => argv.includes("msg")).length, 0, "below the floor no message or contact verb spawns");
  assert.equal(stub.calls().filter((argv) => argv.includes("projects")).length, 0, "below the floor no grouping verb spawns");
});

test("the messages read groups contacts by the mapped projects with one bounded projects get each", async (t) => {
  const stub = contactsStub(t);
  const status = await jiti.import("./polyforge-status.ts");
  stub.set({});
  await status.probePolyforgeVersion(true);
  stub.reset();
  const ok = await lib.readPolyforgeMessages();
  assert.deepEqual(ok.groups, [
    { project: "alpha", contacts: [{ id: "u_ava", display_name: "Ava" }] },
    { project: "beta", contacts: [{ id: "u_ava", display_name: "Ava" }] },
  ], "mapped projects deduplicated and name-ordered; a non-contact (u_ben), machines, the caller and repeats never enter");
  const wire = JSON.stringify(ok.groups);
  for (const leak of ["maintainer", "example.com", "u_machine", "u_self", "u_ben", "role", "email"]) {
    assert.ok(!wire.includes(leak), `the groups never carry ${leak}`);
  }
  const gets = stub.calls().filter((argv) => argv.includes("projects"));
  assert.deepEqual(gets.map((argv) => argv.join(" ")).sort(), ["--json projects get alpha", "--json projects get beta"],
    "one pf --json projects get per mapped project");

  stub.reset();
  const withoutGroups = await lib.readPolyforgeMessages({ withGroups: false });
  assert.equal("groups" in JSON.parse(JSON.stringify(withoutGroups)), false, "a read without groups omits the field on the wire");
  assert.deepEqual(withoutGroups.conversations, ok.conversations, "the conversations and contacts are unchanged");
  assert.deepEqual(withoutGroups.contacts, ok.contacts);
  assert.equal(stub.calls().filter((argv) => argv.includes("projects")).length, 0, "a read without groups spawns no projects get");

  stub.set({ projectFail: "beta" });
  const failed = await lib.readPolyforgeMessages();
  assert.deepEqual(failed.groups[0].contacts.map((c) => c.id), ["u_ava"], "the other group renders normally");
  assert.equal(failed.groups[1].project, "beta");
  assert.deepEqual(failed.groups[1].contacts, []);
  assert.match(failed.groups[1].error ?? "", /beta members are unavailable/, "the failed project carries pf's own text");
  assert.equal(failed.error, undefined, "a project failure never becomes the messages error");

  stub.set({ projectHang: "beta" });
  const started = Date.now();
  const hanging = await lib.readPolyforgeMessages();
  assert.ok(Date.now() - started < 6000, "a hanging project read is cut at the 2 s contact-read bound");
  assert.equal(hanging.groups[0].contacts.length, 1);
  assert.ok((hanging.groups[1].error ?? "").length > 0, "the hanging project carries a failure note");
  assert.deepEqual(hanging.conversations.map((c) => c.id), ["conv_1"]);

  stub.set({ contactsFail: true });
  const noContacts = await lib.readPolyforgeMessages();
  assert.deepEqual(noContacts.groups, [], "no group is offered over an unknown contact set");
  assert.deepEqual(noContacts.conversations.map((c) => c.id), ["conv_1"]);

  stub.set({ mapping: "" });
  stub.reset();
  const unmapped = await lib.readPolyforgeMessages();
  assert.deepEqual(unmapped.groups.map((g) => g.project), ["alpha"], "only mapped projects become groups");
});

test("mappedProjectsFromConfig reads every workspace section, trimmed, deduped, ordered, argv-safe", () => {
  assert.deepEqual(lib.mappedProjectsFromConfig([
    { key: "user_id", value: "u_self" },
    { key: '[workspace."/a"]', value: " zeta, alpha ,, alpha", section: true },
    { key: '[workspace."/b"]', value: "beta,-flag,has space", section: true },
    { key: "[daemon]", value: "gamma" },
    { key: '[workspace."/c"]', value: 42 },
    null,
  ]), ["alpha", "beta", "zeta"]);
  assert.deepEqual(lib.mappedProjectsFromConfig([]), []);
});

test("buildProjectGroups and contactsOutsideGroups: pure grouping rules", () => {
  const contacts = [
    { id: "u_zoe", display_name: "Zoe" },
    { id: "u_blank", display_name: "" },
    { id: "u_ava", display_name: "Ava" },
  ];
  const groups = pure.buildProjectGroups(["beta", " alpha", "alpha", "empty", "down"], {
    alpha: { ids: ["u_blank", "u_zoe", "u_ava", "u_machine", "u_zoe"] },
    beta: { ids: ["u_ava"] },
    empty: { ids: [] },
    down: { error: "down is unavailable" },
  }, contacts);
  assert.deepEqual(groups, [
    { project: "alpha", contacts: [{ id: "u_ava", display_name: "Ava" }, { id: "u_zoe", display_name: "Zoe" }, { id: "u_blank", display_name: "" }] },
    { project: "beta", contacts: [{ id: "u_ava", display_name: "Ava" }] },
    { project: "down", contacts: [], error: "down is unavailable" },
    { project: "empty", contacts: [] },
  ], "name order, blank names last, unknown ids dropped, one row per id, a failure only on its own group");
  assert.deepEqual(pure.normalizeProjectNames([" b", "a", "b", ""]), ["a", "b"]);
  assert.deepEqual(pure.contactsOutsideGroups(contacts, groups).map((c) => c.id), [],
    "a contact shown in a group is not repeated as a silent direct row");
  assert.deepEqual(pure.contactsOutsideGroups(contacts, []).map((c) => c.id), ["u_zoe", "u_blank", "u_ava"],
    "with no groups every contact stays in the direct area");
});

test("the users read: humans only, id + display name only, refusal is admin:false, below the floor nothing spawns", async (t) => {
  const stub = contactsStub(t);
  const status = await jiti.import("./polyforge-status.ts");
  stub.set({});
  await status.probePolyforgeVersion(true);
  stub.reset();
  const ok = await lib.readPolyforgeUsers();
  assert.equal(ok.admin, true, "a successful users list is the admin answer");
  assert.equal(ok.capable, true);
  assert.deepEqual(ok.users, [{ id: "u_ava", display_name: "Ava" }, { id: "u_blank", display_name: "" }],
    "humans only, and never the caller (re-adding it would rewrite its own role)");
  const wire = JSON.stringify(ok);
  for (const leak of ["example.com", "ROLE-SENTINEL", "TOKEN-1", "user_type", "api_tokens", "u_robot", "Robot", "u_untyped", "u_self"]) {
    assert.ok(!wire.includes(leak), `the users read never carries ${leak}`);
  }
  assert.deepEqual(stub.calls().filter((argv) => argv.includes("users")), [["--json", "users", "list"]], "one pf --json users list");

  stub.set({ usersFail: true });
  const refused = await lib.readPolyforgeUsers();
  assert.equal(refused.admin, false, "a refusal is the read-only answer");
  assert.deepEqual(refused.users, []);
  assert.match(refused.error ?? "", /admin rights are required/, "pf's own text");
  assert.equal(refused.capable, true, "a refusal is not a capability downgrade");

  stub.set({ version: "v2.20.11" });
  await status.probePolyforgeVersion(true);
  stub.reset();
  const below = await lib.readPolyforgeUsers();
  assert.equal(below.capable, false);
  assert.equal(below.admin, false);
  assert.deepEqual(below.users, []);
  assert.equal(stub.calls().filter((argv) => argv.includes("users")).length, 0, "below the floor no users verb spawns");
});

test("validateMembershipInput: add/remove only, a mapped project, a user id, no role", () => {
  const mapped = ["alpha", "beta"];
  assert.deepEqual(lib.validateMembershipInput({ action: "add", project: " alpha ", user_id: "u_ava", role: "maintainer" }, mapped),
    { ok: true, value: { action: "add", project: "alpha", userId: "u_ava" } }, "the project is trimmed and a role field is ignored");
  assert.equal(lib.validateMembershipInput({ action: "remove", project: "beta", user_id: "u_ava" }, mapped).ok, true);
  for (const bad of [
    null, "text", {},
    { action: "upsert", project: "alpha", user_id: "u_ava" },
    { action: "add", project: "gamma", user_id: "u_ava" },
    { action: "add", project: "", user_id: "u_ava" },
    { action: "add", project: ["alpha"], user_id: "u_ava" },
    { action: "add", project: "alpha", user_id: "not a user id" },
    { action: "add", project: "alpha", user_id: "-u" },
    { action: "add", project: "alpha" },
  ]) {
    assert.equal(lib.validateMembershipInput(bad, mapped).ok, false, JSON.stringify(bad));
  }
  assert.equal(lib.validateMembershipInput({ action: "add", project: "alpha", user_id: "u_ava" }, []).ok, false, "no mapping, no project");
});

test("changePolyforgeMembership spawns exactly the CLI's argv; refusals keep pf's code and text", async (t) => {
  const stub = contactsStub(t);
  stub.set({});
  stub.reset();
  await lib.changePolyforgeMembership({ action: "add", project: "alpha", userId: "u_nina" });
  await lib.changePolyforgeMembership({ action: "remove", project: "alpha", userId: "u_ben" });
  assert.deepEqual(stub.calls(), [
    ["--json", "projects", "members", "add", "alpha", "--user", "u_nina", "--role", "writer"],
    ["--json", "projects", "members", "remove", "alpha", "--user", "u_ben"],
  ]);
  assert.deepEqual(await lib.readPolyforgeMappedProjects(), ["alpha", "beta"], "the mapping read for validation");

  stub.set({ membersError: "CORE-422" });
  await assert.rejects(lib.changePolyforgeMembership({ action: "remove", project: "alpha", userId: "u_ben" }), (error) => {
    assert.equal(error.code, "CORE-422");
    assert.match(error.message, /membership refused by policy/);
    assert.equal(lib.messageErrorStatus(error.code), 422, "a typed refusal is its 4xx");
    return true;
  });
  stub.set({ membersUnavailable: true });
  await assert.rejects(lib.changePolyforgeMembership({ action: "add", project: "alpha", userId: "u_nina" }), (error) => {
    assert.equal(error.code, "");
    assert.equal(lib.messageErrorStatus(error.code), 200, "an unreachable pf is a 200 error field, never 5xx");
    return true;
  });
});

// ---- the structured analysis ----

test("the analysis floor is 2.20.18, separate from the 2.20.12 Messages floor", async () => {
  assert.equal(lib.POLYFORGE_ANALYZE_MINIMUM_PF_VERSION, "2.20.18");
  assert.equal(pure.POLYFORGE_ANALYZE_MINIMUM_PF_VERSION, "2.20.18", "the panel and the server read one constant");
  assert.equal(lib.POLYFORGE_MESSAGES_MINIMUM_PF_VERSION, "2.20.12", "the Messages floor is unchanged");
  const detect = await jiti.import("./polyforge-detect.ts");
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.17", lib.POLYFORGE_ANALYZE_MINIMUM_PF_VERSION), true);
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.18", lib.POLYFORGE_ANALYZE_MINIMUM_PF_VERSION), false);
  const source = readFileSync(new URL("./polyforge-messages.ts", import.meta.url), "utf8");
  const start = source.indexOf("export async function readPolyforgeAnalysisCapability(");
  const body = source.slice(start, source.indexOf("\n}\n", start));
  assert.match(body, /capabilityAtFloor\(POLYFORGE_ANALYZE_MINIMUM_PF_VERSION, "message analysis"\)/);
  assert.match(source, /const ANALYZE_TIMEOUT_MS = 420000;/, "the analyze bound outlasts the daemon's 6-minute wait");
});

const analysisAnswer = (patch = {}) => ({
  message_id: "msg_a1", task_id: "task_0123456789abcdef", project: "workbench", type: "requirement",
  summary: "  File the ticket. Then reply.  ", suggested_actions: ["file_ticket"], model: "pi:m", duration: "1m2.3s",
  ...patch,
});

test("parsePolyforgeAnalysis keeps each closed type and drops everything else", () => {
  for (const type of ["requirement", "bug", "ruling", "discussion", "clarification", "outreach", "other"]) {
    assert.equal(pure.parsePolyforgeAnalysis(analysisAnswer({ type }))?.type, type);
  }
  const parsed = pure.parsePolyforgeAnalysis(analysisAnswer({ conclusion: "retired", extra: 1 }));
  assert.deepEqual(parsed, {
    message_id: "msg_a1", task_id: "task_0123456789abcdef", project: "workbench", type: "requirement",
    summary: "File the ticket. Then reply.", suggested_actions: ["file_ticket"], model: "pi:m", duration: "1m2.3s",
  }, "the passport keeps the structured fields only — never the retired conclusion");
  assert.deepEqual(pure.parsePolyforgeAnalysis(analysisAnswer({ suggested_actions: ["reply", "bogus", "reply", 3, "note_only"] }))?.suggested_actions,
    ["reply", "note_only"], "unknown and duplicate actions are dropped");
  assert.deepEqual(pure.parsePolyforgeAnalysis(analysisAnswer({ suggested_actions: "reply" }))?.suggested_actions, []);
});

test("a non-structured or unusable answer is refused, never translated", () => {
  for (const raw of [
    null, "text", [], { conclusion: "the old shape" },
    analysisAnswer({ type: "feature" }), analysisAnswer({ type: undefined }),
    analysisAnswer({ summary: "   " }), analysisAnswer({ summary: 3 }),
    analysisAnswer({ task_id: "task_123" }), analysisAnswer({ task_id: "../x" }),
    analysisAnswer({ message_id: "x" }),
  ]) {
    assert.equal(pure.parsePolyforgeAnalysis(raw), null, `refused: ${JSON.stringify(raw)}`);
  }
  assert.equal(pure.isPolyforgeAnalysisType("Bug"), false);
  assert.equal(pure.isPolyforgeSuggestedAction("file_ticket"), true);
});

test("the summary and draft display bounds", () => {
  const long = "字".repeat(2500);
  const cut = pure.parsePolyforgeAnalysis(analysisAnswer({ summary: long }))?.summary ?? "";
  assert.equal(Array.from(cut).length, 2000, "a summary is cut to 2000 code points");
  assert.ok(cut.endsWith("…"));
  const draft = (value, actions = ["reply"]) => pure.parsePolyforgeAnalysis(analysisAnswer({ suggested_actions: actions, reply_draft: value }))?.reply_draft;
  assert.equal(draft("  hi  "), "hi");
  assert.equal(draft("hi", ["file_ticket"]), undefined, "a draft without a reply suggestion is dropped");
  assert.equal(draft("   "), undefined);
  assert.equal(draft(7), undefined);
  assert.equal(draft("é".repeat(2049)), undefined, "a draft over pf's 4096-byte send bound is dropped");
  assert.equal(draft("é".repeat(2048)), "é".repeat(2048));
});

test("the primary action is reply, else file_ticket, else none", () => {
  assert.equal(pure.primarySuggestedAction(["file_ticket", "reply"]), "reply");
  assert.equal(pure.primarySuggestedAction(["file_ticket"]), "file_ticket");
  assert.equal(pure.primarySuggestedAction(["note_only"]), null);
  assert.equal(pure.primarySuggestedAction([]), null);
});

test("the analyze read spawns one structured read and reads no conclusion", () => {
  const source = readFileSync(new URL("./polyforge-messages.ts", import.meta.url), "utf8");
  const start = source.indexOf("export async function analyzePolyforgeMessage(");
  const body = source.slice(start, source.indexOf("\n}\n", start));
  assert.match(body, /runPfJson\(\["--json", "msg", "analyze", messageId\], ANALYZE_TIMEOUT_MS, ANALYZE_MAX_BUFFER\)/);
  assert.match(body, /parsePolyforgeAnalysis\(raw\)/);
  assert.match(body, /"the analyzer answered no structured result", "DAEMON-500"/);
  assert.doesNotMatch(source, /conclusion/, "no conclusion read anywhere");
});

// ---- 【建票】 ----

test("the dialog prefill — first sentence goal, the analysis project, message reference + summary", () => {
  assert.equal(pure.firstSentence("Review the design. Then reply."), "Review the design.");
  assert.equal(pure.firstSentence("v2.20.18 ships it. Next."), "v2.20.18 ships it.", "a dot inside a token is not a sentence end");
  assert.equal(pure.firstSentence("先评审设计。然后回复。"), "先评审设计。");
  assert.equal(pure.firstSentence("line one\nline two"), "line one");
  assert.equal(Array.from(pure.firstSentence("x".repeat(900))).length, 500, "the goal is cut to pf's 500-rune bound");
  const defaults = pure.fileTicketDialogDefaults(
    { summary: "Ask for a design review. More follows.", project: "beta" },
    { id: "msg_9", text: "please\n  review   this" },
    ["alpha", "beta"],
  );
  assert.deepEqual(defaults, {
    goal: "Ask for a design review.",
    project: "beta",
    content: '消息 msg_9: "please review this"\n\nAsk for a design review. More follows.',
  });
  assert.equal(pure.fileTicketDialogDefaults({ summary: "S.", project: "gone" }, { id: "msg_9", text: "t" }, ["alpha"]).project, "alpha",
    "an unmapped analysis project falls back to the first mapped one");
});

test("validateFileTicketInput refuses every malformed body before any spawn", () => {
  const mapped = ["alpha", "beta"];
  assert.deepEqual(lib.validateFileTicketInput({ goal: "  Do it  ", project: " alpha ", content: " body " }, mapped),
    { ok: true, value: { goal: "Do it", project: "alpha", content: "body" } });
  assert.equal(lib.FILE_TICKET_GOAL_MAX_RUNES, 500);
  assert.equal(lib.FILE_TICKET_CONTENT_MAX_BYTES, 65536);
  assert.equal(lib.validateFileTicketInput({ goal: "字".repeat(500), project: "alpha", content: "c" }, mapped).ok, true, "500 runes is inside the bound");
  for (const bad of [
    null, "x", { goal: "   ", project: "alpha", content: "c" }, { goal: "g".repeat(501), project: "alpha", content: "c" },
    { goal: "two\nlines", project: "alpha", content: "c" }, { goal: "g", project: "gamma", content: "c" },
    { goal: "g", project: ["alpha"], content: "c" }, { goal: "g", project: "alpha", content: "  " },
    { goal: "g", project: "alpha", content: "x".repeat(65537) }, { goal: "g", project: "alpha", content: "@/etc/passwd" },
    { goal: "g", project: "alpha", content: "-" }, { goal: "g", project: "alpha" },
  ]) {
    assert.equal(lib.validateFileTicketInput(bad, mapped).ok, false, JSON.stringify(bad)?.slice(0, 80));
  }
});

test("createPolyforgeFileTicket spawns one bundle-free create and answers only the slug", async (t) => {
  const stub = contactsStub(t);
  stub.set({});
  stub.reset();
  assert.deepEqual(await lib.createPolyforgeFileTicket({ goal: "Do it", project: "alpha", content: "body" }), { slug: "alpha#7" });
  assert.deepEqual(stub.calls(), [["--json", "file", "create", "--project", "alpha", "--goal", "Do it", "--content", "body"]]);
  assert.ok(stub.calls().every((argv) => !argv.includes("--bundle")), "a registration ticket: never --bundle");
  stub.set({ createError: "CORE-400" });
  await assert.rejects(lib.createPolyforgeFileTicket({ goal: "g", project: "alpha", content: "c" }), (error) => {
    assert.equal(error.code, "CORE-400");
    assert.match(error.message, /goal is too vague/);
    assert.equal(lib.messageErrorStatus(error.code), 400, "pf's typed refusal keeps its 4xx");
    return true;
  });
  stub.set({ createUnavailable: true });
  await assert.rejects(lib.createPolyforgeFileTicket({ goal: "g", project: "alpha", content: "c" }), (error) => {
    assert.equal(lib.messageErrorStatus(error.code), 200, "an unreachable pf is a 200 error field, never 5xx");
    return true;
  });
});

// ---- the 「@」 action's execution prompt ----

// The one fixture shape the daemon's Go test pins too (same four keys).
const promptFixture = { prompt: "Fix the login timeout in the session store.", task_id: "task_0123456789abcdef", model: "pi:t/m", duration: "1m2.3s" };

test("the prompt floor is its own constant, read by the panel and the server alike", async () => {
  assert.match(pure.POLYFORGE_PROMPT_MINIMUM_PF_VERSION, /^\d+\.\d+\.\d+$/);
  assert.equal(lib.POLYFORGE_PROMPT_MINIMUM_PF_VERSION, pure.POLYFORGE_PROMPT_MINIMUM_PF_VERSION, "one constant");
  const detect = await jiti.import("./polyforge-detect.ts");
  assert.equal(detect.isPolyforgeVersionBelowMinimum(lib.POLYFORGE_ANALYZE_MINIMUM_PF_VERSION, lib.POLYFORGE_PROMPT_MINIMUM_PF_VERSION), true,
    "the prompt floor is above the analysis floor");
  assert.equal(detect.isPolyforgeVersionBelowMinimum(`v${lib.POLYFORGE_PROMPT_MINIMUM_PF_VERSION}`, lib.POLYFORGE_PROMPT_MINIMUM_PF_VERSION), false);
  assert.equal(lib.POLYFORGE_ANALYZE_MINIMUM_PF_VERSION, "2.20.18", "the analysis floor is unchanged");
  assert.equal(lib.POLYFORGE_MESSAGES_MINIMUM_PF_VERSION, "2.20.12", "the Messages floor is unchanged");
});

test("parsePolyforgePrompt keeps exactly the four fields and refuses any other shape", () => {
  assert.deepEqual(pure.parsePolyforgePrompt(promptFixture), promptFixture, "the shared fixture passes unchanged");
  assert.deepEqual(Object.keys(pure.parsePolyforgePrompt({ ...promptFixture, extra: 1, summary: "x", message_id: "msg_1" }) ?? {}).sort(),
    ["duration", "model", "prompt", "task_id"], "extra fields are dropped");
  assert.equal(pure.parsePolyforgePrompt({ ...promptFixture, prompt: "\n  line one\n\n  line two  \n" })?.prompt, "line one\n\n  line two",
    "the prompt is trimmed at its ends only");
  for (const raw of [
    null, "text", [], {},
    { ...promptFixture, prompt: undefined }, { ...promptFixture, prompt: "   " }, { ...promptFixture, prompt: 3 },
    { ...promptFixture, task_id: undefined }, { ...promptFixture, task_id: "task_123" }, { ...promptFixture, task_id: "../x" },
    { ...promptFixture, model: undefined }, { ...promptFixture, duration: 62 },
  ]) {
    assert.equal(pure.parsePolyforgePrompt(raw), null, `refused: ${JSON.stringify(raw)}`);
  }
});

// A stub `pf` for the prompt read: the version and the prompt answer are
// driven by a mode file; every argv is logged.
function promptStub(t) {
  const work = mkdtempSync(join(tmpdir(), "pf-messages-prompt-"));
  const mode = join(work, "mode.json");
  const argvLog = join(work, "argv.log");
  writeFileSync(argvLog, "");
  writeFileSync(join(work, "pf"), `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(argvLog)}, JSON.stringify(args) + "\\n");
const m = JSON.parse(fs.readFileSync(${JSON.stringify(mode)}, "utf8"));
const rest = args.filter((a) => a !== "--json");
if (args.includes("--version")) { process.stdout.write(m.version + "\\n"); process.exit(0); }
if (rest[0] === "msg" && rest[1] === "prompt") {
  if (m.fail) { process.stderr.write(JSON.stringify({ code: m.fail, detail: "the prompt task ended capped" })); process.exit(4); }
  process.stdout.write(JSON.stringify(m.answer));
  process.exit(0);
}
process.exit(2);
`);
  chmodSync(join(work, "pf"), 0o755);
  const oldPath = process.env.PATH;
  process.env.PATH = `${work}${delimiter}${oldPath}`;
  t.after(() => { process.env.PATH = oldPath; rmSync(work, { recursive: true, force: true }); });
  return {
    set: (value) => writeFileSync(mode, JSON.stringify({ version: `v${pure.POLYFORGE_PROMPT_MINIMUM_PF_VERSION}`, answer: promptFixture, ...value })),
    calls: () => readFileSync(argvLog, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)),
    reset: () => writeFileSync(argvLog, ""),
  };
}

test("the prompt read is one `pf --json msg prompt <id>` behind its own floor", async (t) => {
  const stub = promptStub(t);
  const status = await jiti.import("./polyforge-status.ts");
  stub.set({});
  await status.probePolyforgeVersion(true);
  assert.equal((await lib.readPolyforgePromptCapability()).capable, true, "at the floor the gate passes");
  stub.reset();
  assert.deepEqual(await lib.promptPolyforgeMessage("msg_a1"), promptFixture);
  assert.deepEqual(stub.calls(), [["--json", "msg", "prompt", "msg_a1"]], "exactly one prompt spawn");

  stub.set({ answer: { ...promptFixture, prompt: "  " } });
  await assert.rejects(lib.promptPolyforgeMessage("msg_a1"), (error) => error.code === "DAEMON-500" && /no prompt/.test(error.message),
    "a blank prompt is the typed failure, never an invented prompt");
  stub.set({ fail: "DAEMON-500" });
  await assert.rejects(lib.promptPolyforgeMessage("msg_a1"), (error) => error.code === "DAEMON-500" && /ended capped/.test(error.message),
    "pf's typed failure keeps its code and text");
  stub.set({ fail: "DAEMON-409" });
  await assert.rejects(lib.promptPolyforgeMessage("msg_a1"), (error) => lib.messageErrorStatus(error.code) === 409, "the lane-full refusal stays a retryable 409");

  stub.reset();
  await assert.rejects(lib.promptPolyforgeMessage("bad id"), (error) => error.code === "CORE-400");
  assert.deepEqual(stub.calls(), [], "a malformed id spawns nothing");

  const [major, minor, patch] = pure.POLYFORGE_PROMPT_MINIMUM_PF_VERSION.split(".").map(Number);
  stub.set({ version: `v${major}.${minor}.${patch - 1}` });
  // No forced probe here: the gate itself re-reads the version (a fresh probe
  // per click), so a pf downgraded a moment ago is refused at once.
  const below = await lib.readPolyforgePromptCapability();
  assert.equal(below.capable, false);
  assert.ok((below.reason ?? "").includes(pure.POLYFORGE_PROMPT_MINIMUM_PF_VERSION), "the refusal names the floor");
  await status.probePolyforgeVersion(true);
});
