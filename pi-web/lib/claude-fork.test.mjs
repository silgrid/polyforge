// a claude session forks at one of its user messages. claudeTranscriptFork()
// cuts claude's jsonl transcript before that message (the file as it stood when the message
// was sent) and re-owns it under a new session id; forkClaudeSession() writes the copy, the
// subagent sidecars it refers to and the new index record. Every transcript here is
// synthetic, shaped like claude 2.1.312's (queue bookkeeping and hook attachments before a
// prompt, one entry per content block, tool results parented to their call, a compaction
// boundary with its hidden summary, a background Agent call and its sidecars).
import "#test-env";
import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-fork-"));
const agentDir = path.join(scratch, "agent");
const configDir = path.join(scratch, "claude-config");
const workDir = path.join(scratch, "work");
fs.mkdirSync(agentDir);
fs.mkdirSync(workDir);
process.env.PI_CODING_AGENT_DIR = agentDir;
// Never the operator's ~/.claude: the transcripts below live in this scratch config dir.
process.env.CLAUDE_CONFIG_DIR = configDir;
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const adapter = await jiti.import("./claude-adapter.ts");
const claude = await jiti.import("./claude-session.ts");
const index = await jiti.import("./claude-index.ts");

test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

const SOURCE = "37000000-0000-4000-8000-000000000001";
const NEW = "37000000-0000-4000-8000-0000000000ff";
const at = (n) => `2026-11-07T10:00:${String(n).padStart(2, "0")}.000Z`;
const base = (sessionId, uuid, parentUuid, n, extra = {}) => ({ parentUuid, isSidechain: false, userType: "external", cwd: workDir, sessionId, version: "2.1.312", uuid, timestamp: at(n), ...extra });
const prompt = (sessionId, uuid, parentUuid, n, content) => ({ type: "user", message: { role: "user", content }, promptSource: "sdk", turnOrigin: "sdk", ...base(sessionId, uuid, parentUuid, n) });
const reply = (sessionId, uuid, parentUuid, n, id, block, extra = {}) => ({ type: "assistant", message: { id, type: "message", role: "assistant", model: "claude-haiku-5-5", content: [block], usage: { input_tokens: 3, output_tokens: 2 } }, ...base(sessionId, uuid, parentUuid, n, extra) });
const result = (sessionId, uuid, parentUuid, n, toolUseId, content, extra = {}) => ({ type: "user", message: { role: "user", content: [{ type: "tool_result", tool_use_id: toolUseId, content }] }, sourceToolAssistantUUID: parentUuid, ...base(sessionId, uuid, parentUuid, n, extra) });
const queued = (sessionId, operation, content) => ({ type: "queue-operation", operation, timestamp: at(0), sessionId, ...(content ? { content } : {}) });

const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAfbLI3wAAAABJRU5ErkJggg==";

/** The main transcript, as entries; `id` names the uuids the tests address. */
function sourceEntries(sid = SOURCE) {
  return [
    queued(sid, "enqueue", "first: start a helper"),
    queued(sid, "dequeue"),
    { type: "attachment", attachment: { type: "hook_success", hookName: "SessionStart" }, ...base(sid, "a0000000-0000-4000-8000-000000000001", null, 1) },
    prompt(sid, "u1", "a0000000-0000-4000-8000-000000000001", 2, "first: start a helper"),
    { type: "attachment", attachment: { type: "total_tokens_reminder" }, ...base(sid, "a0000000-0000-4000-8000-000000000002", "u1", 3) },
    reply(sid, "a1", "a0000000-0000-4000-8000-000000000002", 4, "msg_1", { type: "thinking", thinking: "plan", signature: "sig" }),
    reply(sid, "a2", "a1", 5, "msg_1", { type: "tool_use", id: "toolu_agent", name: "Agent", input: { subagent_type: "general-purpose", description: "helper", prompt: "look around", run_in_background: true } }),
    result(sid, "r1", "a2", 6, "toolu_agent", "Async agent launched", { toolUseResult: { isAsync: true, status: "async_launched", agentId: "agentA", outputFile: `/tmp/claude-0/${sid}/tasks/agentA.output` } }),
    reply(sid, "a3", "r1", 7, "msg_2", { type: "text", text: "helper started" }),
    { type: "last-prompt", lastPrompt: "first: start a helper", leafUuid: "a3", sessionId: sid },
    // An older claude's subagent entry in the main file: another conversation, never shown.
    { type: "user", message: { role: "user", content: "sidechain prompt" }, ...base(sid, "s1", null, 8, { isSidechain: true, agentId: "legacy" }) },
    queued(sid, "enqueue", "second: read a file"),
    queued(sid, "dequeue"),
    prompt(sid, "u2", "a3", 9, "second: read a file"),
    reply(sid, "a4", "u2", 10, "msg_3", { type: "tool_use", id: "toolu_read", name: "Read", input: { file_path: `${workDir}/notes.txt` } }),
    result(sid, "r2", "a4", 11, "toolu_read", `quoted "sessionId":"${sid}" stays`),
    reply(sid, "a5", "r2", 12, "msg_4", { type: "text", text: "read it" }),
    { type: "system", subtype: "compact_boundary", content: "Conversation compacted", isMeta: false, level: "info", logicalParentUuid: "a5", compactMetadata: { trigger: "manual", preTokens: 1234, postTokens: 99 }, ...base(sid, "cb", null, 13) },
    { type: "user", message: { role: "user", content: "This session is being continued from a previous conversation. Summary." }, isCompactSummary: true, isVisibleInTranscriptOnly: true, ...base(sid, "sum", "cb", 14) },
    { type: "user", message: { role: "user", content: "<local-command-stdout>Compacted </local-command-stdout>" }, ...base(sid, "cmd", "sum", 15) },
    "{\"type\":\"assistant\",\"truncated", // a line claude never finished: kept as written
    queued(sid, "enqueue", "third: an image"),
    queued(sid, "dequeue"),
    prompt(sid, "u3", "cmd", 16, [{ type: "text", text: "third: an image" }, { type: "image", source: { type: "base64", media_type: "image/png", data: PNG } }]),
    { type: "user", message: { role: "user", content: [{ type: "text", text: "[Image: source: /tmp/claude-0/x/images/1.png]" }] }, isMeta: true, turnCompanion: true, ...base(sid, "meta3", "u3", 17) },
    reply(sid, "a6", "meta3", 18, "msg_5", { type: "text", text: "a red square" }),
    { type: "user", message: { role: "user", content: "<task-notification>\n<task-id>agentA</task-id>\n<tool-use-id>toolu_agent</tool-use-id>\n<status>completed</status>\n<result>found it</result>\n</task-notification>" }, turnOrigin: "task_notification", promptSource: "sdk", ...base(sid, "n1", "a6", 19) },
    reply(sid, "a7", "n1", 20, "msg_6", { type: "text", text: "the helper finished" }),
    queued(sid, "enqueue", "fourth: two reads"),
    queued(sid, "dequeue"),
    prompt(sid, "u4", "a7", 21, "fourth: two reads"),
    reply(sid, "a8", "u4", 22, "msg_7", { type: "tool_use", id: "toolu_x", name: "Read", input: { file_path: "x" } }),
    reply(sid, "a9", "a8", 23, "msg_7", { type: "tool_use", id: "toolu_y", name: "Read", input: { file_path: "y" } }),
    result(sid, "r4x", "a8", 24, "toolu_x", "x"),
    // A message written while toolu_y is still unanswered: inside the tool exchange.
    prompt(sid, "u5", "r4x", 25, "fifth: while a call runs"),
    result(sid, "r4y", "a9", 26, "toolu_y", "y"),
    reply(sid, "a10", "r4y", 27, "msg_8", { type: "text", text: "both read" }),
  ];
}

const jsonl = (entries) => `${entries.map((entry) => (typeof entry === "string" ? entry : JSON.stringify(entry))).join("\n")}\n`;
const SOURCE_TEXT = jsonl(sourceEntries());
const linesOf = (text) => text.split("\n").filter(Boolean);
/** The source's lines before the line holding `uuid`. */
const sourceLinesBefore = (uuid) => {
  const lines = linesOf(SOURCE_TEXT);
  return lines.slice(0, lines.findIndex((line) => line.includes(`"uuid":"${uuid}"`)));
};
const history = (text) => adapter.claudeTranscriptToMessages(adapter.parseClaudeTranscript(text));

test("a fork before a user message is the source's lines before it, minus that message's queue bookkeeping, re-owned", () => {
  const fork = adapter.claudeTranscriptFork(SOURCE_TEXT, "u2", NEW);
  assert.ok(fork.text);
  const before = sourceLinesBefore("u2");
  // The two queue-operation lines of u2's own submission are not kept; u1's are.
  assert.match(before.at(-1), /"queue-operation"/);
  assert.match(before.at(-2), /"queue-operation"/);
  const expected = before.slice(0, -2);
  const copy = linesOf(fork.text);
  assert.equal(copy.length, expected.length);
  for (const [i, line] of copy.entries()) {
    const source = JSON.parse(expected[i]);
    const forked = JSON.parse(line);
    assert.deepEqual(forked, { ...source, sessionId: NEW }, `line ${i}`);
    assert.deepEqual(Object.keys(forked), Object.keys(source), `line ${i} keeps its key order`);
  }
  assert.ok(!fork.text.includes("second: read a file"), "the forked-at message (and its queued text) is not in the copy");
  assert.ok(!fork.text.includes(`"sessionId":"${SOURCE}"`), "an entry kept the source's session id");
  // Ids: the copy's entries keep the source's uuids, so its history's entry ids are the source's before u2.
  const source = history(SOURCE_TEXT);
  const forkedHistory = history(fork.text);
  assert.deepEqual(forkedHistory.entryIds, source.entryIds.slice(0, source.entryIds.indexOf("u2")));
  assert.deepEqual(forkedHistory.messages.map((message) => message.role), ["user", "assistant", "assistant", "toolResult", "assistant"]);
  // The older claude's sidechain entry before the message is kept as written, and still not shown.
  assert.ok(copy.some((line) => JSON.parse(line).uuid === "s1"));
  assert.ok(!forkedHistory.entryIds.includes("s1"));
  // claude's own bookkeeping before the message stays: the turn's last-prompt and the hook attachment.
  assert.ok(copy.some((line) => JSON.parse(line).type === "last-prompt"));
  assert.deepEqual(fork.entries.map((entry) => entry.uuid ?? entry.type), copy.map((line) => JSON.parse(line).uuid ?? JSON.parse(line).type));
});

test("only an entry's own sessionId is rewritten: a value it quotes, and a line that does not parse, stay as written", () => {
  const fork = adapter.claudeTranscriptFork(SOURCE_TEXT, "u3", NEW);
  const copy = linesOf(fork.text);
  const quoted = JSON.parse(copy.find((line) => JSON.parse(line).uuid === "r2"));
  assert.equal(quoted.sessionId, NEW);
  assert.equal(quoted.message.content[0].content, `quoted "sessionId":"${SOURCE}" stays`);
  const receipt = JSON.parse(copy.find((line) => JSON.parse(line).uuid === "r1"));
  assert.equal(receipt.toolUseResult.outputFile, `/tmp/claude-0/${SOURCE}/tasks/agentA.output`);
  assert.ok(copy.includes("{\"type\":\"assistant\",\"truncated"), "the unfinished line was not kept as written");
  // A rewritten line differs from the source's in its session id alone.
  const sourceLine = linesOf(SOURCE_TEXT).find((line) => line.includes('"last-prompt"'));
  assert.ok(copy.includes(sourceLine.replace(SOURCE, NEW)));
});

test("the message's queue lines go even when claude wrote the previous turn's stop-hook summary after them", () => {
  // As claude 2.1.312 writes a turn whose Stop hook reported after the next prompt was queued.
  const text = jsonl([
    prompt(SOURCE, "p1", null, 1, "one"),
    reply(SOURCE, "r1", "p1", 2, "msg_1", { type: "text", text: "answer one" }),
    { type: "last-prompt", lastPrompt: "one", leafUuid: "r1", sessionId: SOURCE },
    queued(SOURCE, "enqueue", "two"),
    queued(SOURCE, "dequeue"),
    { type: "system", subtype: "stop_hook_summary", hookCount: 1, hookInfos: [], hookErrors: [], level: "suggestion", ...base(SOURCE, "h1", "r1", 3) },
    prompt(SOURCE, "p2", "h1", 4, "two"),
  ]);
  const fork = adapter.claudeTranscriptFork(text, "p2", NEW);
  assert.deepEqual(linesOf(fork.text).map((line) => JSON.parse(line).uuid ?? JSON.parse(line).type), ["p1", "r1", "last-prompt", "h1"]);
  assert.ok(!fork.text.includes('"two"'), "the forked-at message's queued text is in the copy");
});

test("a fork after a compaction keeps the boundary, its hidden summary and the history before it", () => {
  const fork = adapter.claudeTranscriptFork(SOURCE_TEXT, "u3", NEW);
  const copy = linesOf(fork.text).flatMap((line) => {
    try {
      return [JSON.parse(line)];
    } catch {
      return [];
    }
  });
  const boundary = copy.find((entry) => entry.subtype === "compact_boundary");
  assert.deepEqual(boundary, { ...sourceEntries().find((entry) => entry.uuid === "cb"), sessionId: NEW });
  assert.ok(copy.some((entry) => entry.uuid === "sum" && entry.isCompactSummary === true));
  const forkedHistory = history(fork.text);
  const source = history(SOURCE_TEXT);
  assert.deepEqual(forkedHistory.entryIds, source.entryIds.slice(0, source.entryIds.indexOf("u3")));
  const marker = forkedHistory.messages.at(-1);
  assert.equal(marker.role, "custom");
  assert.equal(marker.customType, "compaction");
  assert.deepEqual(marker.details, { tokensBefore: 1234 });
  assert.ok(forkedHistory.entryIds.includes("u1") && forkedHistory.entryIds.includes("u2"), "the messages before the compaction are kept");
  // A fork before the compaction holds none of it.
  assert.ok(!adapter.claudeTranscriptFork(SOURCE_TEXT, "u2", NEW).text.includes("compact_boundary"));
});

test("a fork before the first user message has nothing to copy: the new session starts fresh", () => {
  assert.deepEqual(adapter.claudeTranscriptFork(SOURCE_TEXT, "u1", NEW), { text: null, entries: [] });
});

test("a message after claude's follow-up turn and an image prompt are fork points; the image prompt's hidden companion goes with it", () => {
  const atImage = adapter.claudeTranscriptFork(SOURCE_TEXT, "u3", NEW);
  assert.ok(!atImage.text.includes("meta3") && !atImage.text.includes(PNG));
  const afterFollowUp = adapter.claudeTranscriptFork(SOURCE_TEXT, "u4", NEW);
  const forkedHistory = history(afterFollowUp.text);
  assert.ok(forkedHistory.entryIds.includes("n1"), "claude's follow-up anchor is history before the message");
  assert.equal(forkedHistory.entryIds.at(-1), "a7");
});

test("only a user bubble of the main conversation is a fork point", () => {
  for (const id of ["a1", "a2", "r1", "a0000000-0000-4000-8000-000000000002", "s1", "cb", "sum", "cmd", "meta3", "n1"]) {
    assert.throws(() => adapter.claudeTranscriptFork(SOURCE_TEXT, id, NEW), /forks only at one of its user messages/, id);
  }
  assert.throws(() => adapter.claudeTranscriptFork(SOURCE_TEXT, "nope", NEW), /Invalid entry ID for forking/);
  assert.throws(() => adapter.claudeTranscriptFork("", "u1", NEW), /Invalid entry ID for forking/);
});

test("a message in the middle of a tool exchange is refused: the copy would hold a call with no answer", () => {
  assert.throws(() => adapter.claudeTranscriptFork(SOURCE_TEXT, "u5", NEW), /middle of a tool call/);
  // The message before that exchange is a fork point, and its copy holds no part of it.
  const fork = adapter.claudeTranscriptFork(SOURCE_TEXT, "u4", NEW);
  assert.ok(!fork.text.includes("toolu_x") && !fork.text.includes("toolu_y"));
});

const SIDECAR_A = (sid) => [
  { type: "user", message: { role: "user", content: "look around" }, ...base(sid, "sa1", null, 30, { isSidechain: true, agentId: "agentA" }) },
  reply(sid, "sa2", "sa1", 31, "msg_a", { type: "tool_use", id: "toolu_nested", name: "Agent", input: { subagent_type: "Explore", description: "nested", prompt: "deeper" } }, { isSidechain: true, agentId: "agentA" }),
  result(sid, "sa3", "sa2", 32, "toolu_nested", "nested done", { isSidechain: true, agentId: "agentA" }),
  reply(sid, "sa4", "sa3", 33, "msg_b", { type: "text", text: "found it" }, { isSidechain: true, agentId: "agentA" }),
];
const SIDECAR_B = (sid) => [
  { type: "user", message: { role: "user", content: "deeper" }, ...base(sid, "sb1", null, 34, { isSidechain: true, agentId: "agentB" }) },
  reply(sid, "sb2", "sb1", 35, "msg_c", { type: "text", text: "deep result" }, { isSidechain: true, agentId: "agentB" }),
];
const SIDECAR_C = (sid) => [
  { type: "user", message: { role: "user", content: "unrelated" }, ...base(sid, "sc1", null, 36, { isSidechain: true, agentId: "agentC" }) },
];
const META = {
  agentA: { agentType: "general-purpose", description: "helper", toolUseId: "toolu_agent", requestShape: "background", finished: true },
  agentB: { agentType: "Explore", description: "nested", toolUseId: "toolu_nested", finished: true },
  agentC: { agentType: "Explore", description: "unrelated", toolUseId: "toolu_other", finished: true },
};

test("the sidecars a fork refers to: its Agent calls' runs (receipt or meta), and the runs those started", () => {
  const sidecars = {
    agentA: { entries: SIDECAR_A(SOURCE), meta: META.agentA },
    agentB: { entries: SIDECAR_B(SOURCE), meta: META.agentB },
    agentC: { entries: SIDECAR_C(SOURCE), meta: META.agentC },
  };
  assert.deepEqual(adapter.claudeLinkedSidecars(adapter.claudeTranscriptFork(SOURCE_TEXT, "u2", NEW).entries, sidecars), ["agentA", "agentB"]);
  // Without the receipt, the meta's toolUseId links the call.
  const noReceipt = sourceEntries().filter((entry) => entry.uuid !== "r1");
  assert.deepEqual(adapter.claudeLinkedSidecars(noReceipt, sidecars), ["agentA", "agentB"]);
  assert.deepEqual(adapter.claudeLinkedSidecars(adapter.claudeTranscriptFork(SOURCE_TEXT, "u1", NEW).entries, sidecars), []);
  assert.equal(adapter.claudeTranscriptWithSessionId("", NEW), "");
  const rewritten = adapter.claudeTranscriptWithSessionId(jsonl(SIDECAR_A(SOURCE)), NEW);
  assert.deepEqual(adapter.parseClaudeTranscript(rewritten), SIDECAR_A(NEW));
});

// forkClaudeSession(): the files and the record.
const encodedProject = path.join(configDir, "projects", workDir.replace(/[^A-Za-z0-9]/g, "-"));
const sha = (file) => createHash("sha256").update(fs.readFileSync(file)).digest("hex");
const treeOf = (dir) => (fs.existsSync(dir) ? fs.readdirSync(dir, { recursive: true }).map(String).sort() : []);

async function writeSource() {
  const id = randomUUID();
  fs.mkdirSync(path.join(encodedProject, id, "subagents"), { recursive: true });
  fs.writeFileSync(path.join(encodedProject, `${id}.jsonl`), jsonl(sourceEntries(id)));
  for (const [agentId, entries] of [["agentA", SIDECAR_A], ["agentB", SIDECAR_B], ["agentC", SIDECAR_C]]) {
    fs.writeFileSync(path.join(encodedProject, id, "subagents", `agent-${agentId}.jsonl`), jsonl(entries(id)));
    fs.writeFileSync(path.join(encodedProject, id, "subagents", `agent-${agentId}.meta.json`), JSON.stringify(META[agentId]));
  }
  const record = {
    id,
    cwd: workDir,
    name: "the source",
    model: "claude-haiku-5-5",
    effort: "high",
    agentLanguage: "zh-CN",
    launchPermissionMode: "acceptEdits",
    includeHookEvents: true,
    created: at(0),
    modified: at(1),
    firstMessage: "first: start a helper",
    usage: { input: 10, output: 20, cacheRead: 30, cacheWrite: 40, cost: 0.5 },
    archived: true,
  };
  await index.upsertClaudeSessionRecord(record);
  return { id, record, transcript: path.join(encodedProject, `${id}.jsonl`) };
}

function snapshot(id) {
  const files = [path.join(encodedProject, `${id}.jsonl`), ...treeOf(path.join(encodedProject, id)).map((name) => path.join(encodedProject, id, name)).filter((file) => fs.statSync(file).isFile())];
  return Object.fromEntries(files.map((file) => [file, sha(file)]));
}

test("forkClaudeSession writes the copy beside the source, its linked sidecars and a fresh record, and leaves the source byte-identical", async () => {
  const { id, record, transcript } = await writeSource();
  const before = snapshot(id);
  const sourceText = fs.readFileSync(transcript, "utf8");
  const startedAt = new Date().toISOString();
  const reply = await claude.forkClaudeSession(id, "u2");
  assert.equal(reply.cancelled, false);
  const forkId = reply.newSessionId;
  assert.match(forkId, /^[0-9a-f-]{36}$/);
  assert.notEqual(forkId, id);

  assert.deepEqual(snapshot(id), before, "the source's transcript or sidecars changed");
  assert.deepEqual(index.getClaudeSessionRecord(id), record, "the source's record changed");

  const copy = path.join(encodedProject, `${forkId}.jsonl`);
  assert.equal(fs.readFileSync(copy, "utf8"), adapter.claudeTranscriptFork(sourceText, "u2", forkId).text);
  assert.equal((fs.statSync(copy).mode & 0o777).toString(8), "600");
  assert.deepEqual(treeOf(path.join(encodedProject, forkId)), [
    "subagents",
    path.join("subagents", "agent-agentA.jsonl"),
    path.join("subagents", "agent-agentA.meta.json"),
    path.join("subagents", "agent-agentB.jsonl"),
    path.join("subagents", "agent-agentB.meta.json"),
  ]);
  assert.deepEqual(adapter.parseClaudeTranscript(fs.readFileSync(path.join(encodedProject, forkId, "subagents", "agent-agentA.jsonl"), "utf8")), SIDECAR_A(forkId));
  assert.equal(fs.readFileSync(path.join(encodedProject, forkId, "subagents", "agent-agentB.meta.json"), "utf8"), JSON.stringify(META.agentB));
  assert.ok(!fs.readdirSync(encodedProject).some((name) => name.endsWith(".tmp")), "a temp file was left behind");

  const forked = index.getClaudeSessionRecord(forkId);
  assert.equal(forked.cwd, record.cwd);
  assert.equal(forked.model, record.model);
  assert.equal(forked.effort, record.effort);
  assert.equal(forked.agentLanguage, record.agentLanguage);
  assert.equal(forked.launchPermissionMode, record.launchPermissionMode);
  assert.equal(forked.includeHookEvents, record.includeHookEvents);
  assert.equal(forked.firstMessage, record.firstMessage);
  assert.deepEqual(forked.usage, index.EMPTY_CLAUDE_USAGE, "the copy has spent nothing yet");
  assert.equal(forked.name, undefined);
  assert.equal(forked.archived, undefined);
  assert.ok(forked.created >= startedAt && forked.modified === forked.created, "the copy is a session created now");

  // A first-class session: listed, its history the source's before the message, its subagent run whole.
  assert.ok(claude.getClaudeSessionInfos().some((row) => row.id === forkId && row.harness === "claude"));
  const sourceHistory = claude.readClaudeHistory(id);
  const forkHistory = claude.readClaudeHistory(forkId);
  assert.equal(forkHistory.file, copy);
  assert.deepEqual(forkHistory.entryIds, sourceHistory.entryIds.slice(0, sourceHistory.entryIds.indexOf("u2")));
  const run = forkHistory.subagents.find((entry) => entry.toolCallId === "toolu_agent");
  assert.equal(run.agentId, "agentA");
  assert.deepEqual(run.entries.map((entry) => entry.id), sourceHistory.subagents.find((entry) => entry.toolCallId === "toolu_agent").entries.map((entry) => entry.id));
  assert.ok(run.entries.length > 0);
});

test("a fork before the first message writes no transcript: the new session's first launch is a fresh one", async () => {
  const { id } = await writeSource();
  const { newSessionId } = await claude.forkClaudeSession(id, "u1");
  assert.equal(fs.existsSync(path.join(encodedProject, `${newSessionId}.jsonl`)), false);
  assert.equal(fs.existsSync(path.join(encodedProject, newSessionId)), false);
  const record = index.getClaudeSessionRecord(newSessionId);
  assert.equal(record.firstMessage, "");
  assert.deepEqual(claude.readClaudeHistory(newSessionId).messages, []);
});

test("a refused fork writes nothing: no transcript, no sidecar, no record", async () => {
  const { id } = await writeSource();
  const files = treeOf(encodedProject);
  const records = index.readClaudeIndex().length;
  for (const [entryId, error] of [["a2", /user messages/], ["u5", /middle of a tool call/], ["missing", /Invalid entry ID/]]) {
    await assert.rejects(claude.forkClaudeSession(id, entryId), error, entryId);
  }
  await assert.rejects(claude.forkClaudeSession(randomUUID(), "u2"), /Session not found/);
  assert.deepEqual(treeOf(encodedProject), files);
  assert.equal(index.readClaudeIndex().length, records);
});
