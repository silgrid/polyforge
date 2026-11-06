// a claude transcript is a tree, and an in-session edit (a truncating
// resume, `--resume <id> --resume-session-at <entry>`) leaves branches in it. The chat
// shows the active branch alone (claudeActiveBranch()), an edit starts only where claude
// can resume before the message (claudeEditPoints() / claudeEditResumePoint()), and a
// "New session" copy takes the active branch. Every transcript here is synthetic, shaped
// like claude 2.1.312's: each entry chained to the one written before it, a parallel tool
// call's results under the split reply entry that made each call, a resumed child's
// SessionStart hook attachment before its prompt, `last-prompt` checkpoints written at
// exit, a compaction boundary (parentUuid null, logicalParentUuid, a preserved segment)
// with its hidden summary, an older claude's sidechain entry in the main file.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const adapter = await jiti.import("./claude-adapter.ts");
const { buildClaudeArgv } = await jiti.import("./claude-argv.ts");

const SID = "37000000-0000-4000-8000-000000000370";
const at = (n) => `2026-11-07T11:${String(Math.floor(n / 60)).padStart(2, "0")}:${String(n % 60).padStart(2, "0")}.000Z`;
const base = (uuid, parentUuid, n, extra = {}) => ({ parentUuid, isSidechain: false, userType: "external", cwd: "/w", sessionId: SID, version: "2.1.312", uuid, timestamp: at(n), ...extra });
const prompt = (uuid, parentUuid, n, content) => ({ type: "user", message: { role: "user", content }, promptSource: "sdk", ...base(uuid, parentUuid, n) });
const reply = (uuid, parentUuid, n, id, block) => ({ type: "assistant", message: { id, type: "message", role: "assistant", model: "claude-haiku-5-5", content: [block], usage: { input_tokens: n, output_tokens: 1 } }, ...base(uuid, parentUuid, n) });
const text = (uuid, parentUuid, n, id, value) => reply(uuid, parentUuid, n, id, { type: "text", text: value });
const call = (uuid, parentUuid, n, id, toolUseId, name = "Read") => reply(uuid, parentUuid, n, id, { type: "tool_use", id: toolUseId, name, input: { file_path: toolUseId } });
const result = (uuid, parentUuid, n, toolUseId) => ({ type: "user", message: { role: "user", content: [{ type: "tool_result", tool_use_id: toolUseId, content: `out ${toolUseId}` }] }, sourceToolAssistantUUID: parentUuid, ...base(uuid, parentUuid, n) });
const turnEnd = (uuid, parentUuid, n) => ({ type: "system", subtype: "turn_duration", durationMs: 1000, isMeta: false, ...base(uuid, parentUuid, n) });
const hook = (uuid, parentUuid, n) => ({ type: "attachment", attachment: { type: "hook_success", hookName: "SessionStart", hookEvent: "SessionStart" }, ...base(uuid, parentUuid, n) });
const checkpoint = (leafUuid, extra = {}) => ({ type: "last-prompt", lastPrompt: "x", leafUuid, sessionId: SID, ...extra });
const queued = (operation) => ({ type: "queue-operation", operation, timestamp: at(0), sessionId: SID });

/**
 * Three turns, then 「从此处编辑」 on the second: the old child's exit checkpoint, the
 * resumed child's SessionStart hook under the kept entry `t1`, the edited prompt and its turn.
 * The newest checkpoint is the stale one: claude writes the next only at exit.
 */
function editedTranscript() {
  return [
    queued("enqueue"), queued("dequeue"),
    prompt("u1", null, 1, "one"),
    text("a1", "u1", 2, "m1", "answer one"),
    turnEnd("t1", "a1", 3),
    queued("enqueue"), queued("dequeue"),
    prompt("u2", "t1", 4, "two"),
    // A parallel tool call: two split entries of one reply, each call's result under its own entry.
    call("a2", "u2", 5, "m2", "tx"),
    call("a3", "a2", 6, "m2", "ty"),
    result("r2x", "a2", 7, "tx"),
    result("r2y", "a3", 8, "ty"),
    text("a4", "r2y", 9, "m3", "answer two"),
    turnEnd("t2", "a4", 10),
    prompt("u3", "t2", 11, "three"),
    { type: "user", message: { role: "user", content: "<task-notification>\n<tool-use-id>toolu_old</tool-use-id>\n<status>completed</status>\n</task-notification>" }, turnOrigin: "task_notification", promptSource: "sdk", ...base("n3", "u3", 12) },
    call("a5", "n3", 13, "m4", "toolu_agent3", "Agent"),
    result("r5", "a5", 14, "toolu_agent3"),
    text("a6", "r5", 15, "m5", "answer three"),
    turnEnd("t3", "a6", 16),
    checkpoint("t3"),
    // The edit: `--resume <id> --resume-session-at t1`, then the edited prompt.
    hook("h1", "t1", 17),
    prompt("u2e", "h1", 18, "two, edited"),
    text("a7", "u2e", 19, "m6", "answer two edited"),
    turnEnd("t4", "a7", 20),
  ];
}

const shown = (entries) => adapter.claudeTranscriptToMessages(entries);
const userTexts = (history) => history.messages.filter((message) => message.role === "user").map((message) => message.content);

test("an edited session shows its active branch alone: the replaced turns, their runs and notes are hidden", () => {
  const entries = editedTranscript();
  const branch = adapter.claudeActiveBranch(entries);
  assert.equal(branch.leaf, "a7");
  assert.deepEqual(branch.path, ["a7", "u2e", "h1", "t1", "a1", "u1"]);
  assert.deepEqual([...branch.hidden].sort(), ["a2", "a3", "a4", "a5", "a6", "n3", "r2x", "r2y", "r5", "t2", "t3", "u2", "u3"]);
  const history = shown(entries);
  assert.deepEqual(userTexts(history), ["one", "two, edited"]);
  assert.deepEqual(history.entryIds, ["u1", "a1", "u2e", "a7"]);
  // The replaced turns' Agent call started a run on the old branch: it is not listed.
  assert.deepEqual(history.subagents, []);
});

test("the stale checkpoint of the old branch does not win over the newer turns; an explicit one written last does (claude's rewind)", () => {
  const entries = editedTranscript();
  assert.equal(adapter.claudeActiveBranch(entries).leaf, "a7", "the old child's exit checkpoint (t3) is stale");
  // The new child's own exit checkpoint changes nothing.
  assert.equal(adapter.claudeActiveBranch([...entries, checkpoint("t4")]).leaf, "a7");
  // claude's rewind writes an explicit checkpoint: while nothing follows it, it is the leaf.
  const rewound = [...entries, checkpoint("a4", { explicit: true, rewound: true })];
  assert.equal(adapter.claudeActiveBranch(rewound).leaf, "a4");
  assert.deepEqual(userTexts(shown(rewound)), ["one", "two"]);
  // A turn written after it ends the explicit checkpoint's hold: the last row is the leaf again.
  assert.equal(adapter.claudeActiveBranch([...rewound, prompt("u5", "a4", 30, "after"), text("a8", "u5", 31, "m9", "x")]).leaf, "a8");
});

test("a parallel tool call's results stay shown: only a turn that starts beside the path is another branch", () => {
  const entries = editedTranscript().slice(0, 21); // up to the old child's exit checkpoint, before the edit
  const branch = adapter.claudeActiveBranch(entries);
  assert.equal(branch.leaf, "a6");
  assert.ok(!branch.path.includes("r2x"), "the first call's result hangs beside the path");
  assert.equal(branch.hidden.size, 0);
  const history = shown(entries);
  assert.deepEqual(history.entryIds.filter((id) => id.startsWith("r2")), ["r2x", "r2y"]);
  assert.deepEqual(userTexts(history), ["one", "two", "three"]);
});

test("a transcript without a fork shows every entry it showed before, in file order", () => {
  const linear = editedTranscript().slice(0, 21);
  const branch = adapter.claudeActiveBranch(linear);
  assert.deepEqual(adapter.claudeActiveEntries(linear, branch), linear);
  // Unchained transcripts (no parentUuid at all) lose nothing either.
  const unchained = linear.map((entry) => (entry.uuid ? { ...entry, parentUuid: null } : entry));
  assert.deepEqual(adapter.claudeActiveEntries(unchained), unchained);
  assert.deepEqual(adapter.claudeActiveBranch([]), { leaf: null, path: [], resumable: new Set(), hidden: new Set() });
});

test("with no checkpoint ever written, claude's leaf is the last row's newest descendant", () => {
  // The last row in file order (z) has a descendant written earlier in the file but newer.
  const entries = [
    prompt("p1", null, 1, "one"),
    text("q1", "p1", 2, "m1", "a"),
    text("q2", "z", 9, "m2", "newest, under z"),
    { ...prompt("z", "q1", 5, "two") },
  ];
  assert.equal(adapter.claudeActiveBranch(entries).leaf, "q2");
  assert.equal(adapter.claudeActiveBranch([...entries, checkpoint("q1")]).leaf, "z", "a keyed checkpoint ends the newest-descendant rule");
});

test("a compaction boundary continues the path through its logical parent; branches before it stay hidden, the messages before it shown", () => {
  const entries = [
    ...editedTranscript(),
    { type: "system", subtype: "compact_boundary", content: "Conversation compacted", isMeta: false, level: "info", logicalParentUuid: "t4", compactMetadata: { trigger: "manual", preTokens: 900, preservedSegment: { headUuid: "a7", anchorUuid: "sum", tailUuid: "a7" } }, ...base("cb", null, 21) },
    { type: "user", message: { role: "user", content: "Summary." }, isCompactSummary: true, isVisibleInTranscriptOnly: true, ...base("sum", "cb", 22) },
    { type: "user", message: { role: "user", content: "<local-command-stdout>Compacted </local-command-stdout>" }, ...base("cmd", "sum", 23) },
    prompt("u4", "cmd", 24, "four"),
    text("a9", "u4", 25, "m7", "answer four"),
  ];
  const branch = adapter.claudeActiveBranch(entries);
  assert.equal(branch.leaf, "a9");
  assert.deepEqual(branch.path, ["a9", "u4", "cmd", "sum", "cb", "t4", "a7", "u2e", "h1", "t1", "a1", "u1"]);
  assert.deepEqual([...branch.resumable], ["a9", "u4", "cmd", "sum", "cb"]);
  assert.ok(branch.hidden.has("u2") && branch.hidden.has("a6"));
  const history = shown(entries);
  assert.deepEqual(userTexts(history), ["one", "two, edited", "four"]);
  assert.equal(history.messages.find((message) => message.role === "custom")?.customType, "compaction");
});

test("an older claude's sidechain entries in the main file are never part of the tree", () => {
  const sidechain = { type: "user", message: { role: "user", content: "sidechain" }, ...base("s1", "t1", 17, { isSidechain: true, agentId: "legacy" }) };
  const entries = [...editedTranscript(), sidechain, { ...prompt("s2", "s1", 30, "sidechain two"), isSidechain: true }];
  const branch = adapter.claudeActiveBranch(entries);
  assert.equal(branch.leaf, "a7");
  assert.ok(!branch.hidden.has("s1"));
  assert.deepEqual(adapter.claudeActiveEntries(entries, branch).filter((entry) => entry.isSidechain), [sidechain, entries.at(-1)]);
});

test("the context meter reads the active branch's latest reply", () => {
  const entries = editedTranscript();
  // Old branch's last reply (a6) carries 15 input tokens; the edited turn's (a7) 19.
  assert.equal(adapter.claudeTranscriptContextTokens(entries), 19 + 1);
  const rewound = [...entries, checkpoint("a4", { explicit: true })];
  assert.equal(adapter.claudeTranscriptContextTokens(rewound), 9 + 1);
});

test("edit points: each user message on the active path after the latest compaction, resumed at its parent", () => {
  const entries = editedTranscript();
  // u1 has no parent: claude has nothing to keep before it. u2e resumes at h1 (the edit's hook).
  assert.deepEqual([...adapter.claudeEditPoints(entries)], [["u2e", "h1"]]);
  const linear = editedTranscript().slice(0, 21);
  assert.deepEqual([...adapter.claudeEditPoints(linear)], [["u2", "t1"], ["u3", "t2"]]);
  // A SessionStart hook before the first prompt is a chain entry claude keeps: the first message is editable.
  const hooked = [hook("h0", null, 0), { ...prompt("u1", "h0", 1, "one") }, text("a1", "u1", 2, "m1", "answer")];
  assert.deepEqual([...adapter.claudeEditPoints(hooked)], [["u1", "h0"]]);
  assert.equal(adapter.claudeEditResumePoint(linear, "u3"), "t2");
});

test("an edit is refused, with its reason, where claude cannot resume before the message", () => {
  const entries = editedTranscript();
  assert.throws(() => adapter.claudeEditResumePoint(entries, "nope"), /Invalid entry ID for editing/);
  for (const id of ["a1", "t1", "h1", "r2x", "n3"]) assert.throws(() => adapter.claudeEditResumePoint(entries, id), /edits only at one of its user messages/, id);
  for (const id of ["u2", "u3"]) assert.throws(() => adapter.claudeEditResumePoint(entries, id), /not on the conversation's current branch/, id);
  assert.throws(() => adapter.claudeEditResumePoint(entries, "u1"), /Nothing comes before this message/);
  const compacted = [
    ...entries,
    { type: "system", subtype: "compact_boundary", content: "Conversation compacted", logicalParentUuid: "t4", compactMetadata: { trigger: "manual", preTokens: 1 }, ...base("cb", null, 21) },
    { type: "user", message: { role: "user", content: "Summary." }, isCompactSummary: true, isVisibleInTranscriptOnly: true, ...base("sum", "cb", 22) },
    prompt("u4", "sum", 24, "four"),
  ];
  assert.throws(() => adapter.claudeEditResumePoint(compacted, "u2e"), /before the conversation's last compaction/);
  assert.equal(adapter.claudeEditResumePoint(compacted, "u4"), "sum");
  // A message written while a call made before it is unanswered: under the parallel call's first result.
  const midCall = [
    prompt("p1", null, 1, "one"), text("x0", "p1", 2, "m0", "ok"), prompt("p2", "x0", 3, "two"),
    call("c1", "p2", 4, "m1", "tx"), call("c2", "c1", 5, "m1", "ty"), result("rx", "c1", 6, "tx"),
    prompt("p3", "rx", 7, "three"), result("ry", "c2", 8, "ty"), text("x1", "ry", 9, "m2", "both"),
  ];
  assert.deepEqual(adapter.claudeActiveBranch(midCall).hidden, new Set(), "the call's first result and what hangs under it stay shown");
  assert.throws(() => adapter.claudeEditResumePoint(midCall, "p3"), /middle of a tool call/);
  assert.equal(adapter.claudeEditResumePoint(midCall, "p2"), "x0");
  // A call id used again by a later child opens an exchange of its own: the prompts between are no part of one.
  const reused = [...midCall, prompt("p4", "x1", 10, "four"), call("c3", "p4", 11, "m3", "tx"), result("rx2", "c3", 12, "tx"), text("x2", "rx2", 13, "m4", "again")];
  assert.equal(adapter.claudeEditResumePoint(reused, "p4"), "x1");
});

test("the truncating resume's argv: --resume <id> --resume-session-at <entry>, only with --resume", () => {
  const STREAM = ["-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--forward-subagent-text"];
  assert.deepEqual(buildClaudeArgv({ sessionId: SID, resume: true, model: "claude-haiku-5-5", resumeAt: "t1" }), [
    ...STREAM,
    "--model", "claude-haiku-5-5",
    "--permission-prompts", "none",
    "--resume", SID,
    "--resume-session-at", "t1",
  ]);
  assert.deepEqual(
    buildClaudeArgv({ sessionId: SID, resume: false, model: "claude-haiku-5-5", resumeAt: "t1" }),
    buildClaudeArgv({ sessionId: SID, resume: false, model: "claude-haiku-5-5" }),
    "a first launch has nothing to truncate",
  );
  const argv = buildClaudeArgv({ sessionId: SID, resume: true, model: "claude-haiku-5-5", resumeAt: "t1" });
  for (const flag of ["--fork-session", "--resume-drops-turn", "--session-id"]) assert.ok(!argv.includes(flag), flag);
});

test("New session on an edited session copies the active branch before the message, never the replaced turns", () => {
  const text_ = `${editedTranscript().map((entry) => JSON.stringify(entry)).join("\n")}\n`;
  const fork = adapter.claudeTranscriptFork(text_, "u2e", "37000000-0000-4000-8000-0000000003ff");
  const kept = fork.entries.map((entry) => entry.uuid ?? entry.type);
  assert.deepEqual(kept, ["queue-operation", "queue-operation", "u1", "a1", "t1", "h1"]);
  assert.deepEqual(adapter.claudeTranscriptToMessages(fork.entries).entryIds, ["u1", "a1"]);
  // The copy's own leaf is on that branch.
  assert.equal(adapter.claudeActiveBranch(fork.entries).leaf, "a1");
  assert.throws(() => adapter.claudeTranscriptFork(text_, "u3", "x"), /not on the conversation's current branch/);
});
