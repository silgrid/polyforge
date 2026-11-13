// carries: lib/claude-adapter.ts, lib/__fixtures__/claude-task-notes-481/*
// the history walk settles a subagent run from every task notification claude
// wrote for it — one that names only its `<task-id>` (no `<tool-use-id>`), and one claude took
// into a running turn (a `queued_command` attachment, no user entry) — while a "waiting on its
// own background work" notification settles nothing. Before, such runs read `running` forever
// after a reload. Replays the synthetic transcripts in lib/__fixtures__/claude-task-notes-481/
// (its README says what each holds).
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createJiti } from "jiti";

const here = path.dirname(fileURLToPath(import.meta.url));
const jiti = createJiti(import.meta.url, { alias: { "@": path.dirname(here) }, interopDefault: true });
const adapter = await jiti.import("./claude-adapter.ts");

const fixtures = path.join(here, "__fixtures__", "claude-task-notes-481");
const transcriptOf = (scenario) => fs.readFileSync(path.join(fixtures, scenario, "transcript.jsonl"), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
const runsOf = (entries) => adapter.claudeTranscriptToMessages(entries).subagents.map((run) => ({ toolCallId: run.toolCallId, status: run.status, result: run.result, startedAt: run.startedAt }));

const text = (entry) => (typeof entry.message?.content === "string" ? entry.message.content : typeof entry.attachment?.prompt === "string" ? entry.attachment.prompt : "");
/** The index of the entry holding the notification whose result is `result`. */
const noteAt = (entries, result) => entries.findIndex((entry) => entry.type !== "queue-operation" && text(entry).includes(`<result>${result}</result>`));
/** The index of the assistant entry calling `id`. */
const callAt = (entries, id) => entries.findIndex((entry) => entry.type === "assistant" && entry.message.content.some((block) => block.id === id));

test("history: a revived run whose notification names only its task-id is completed", () => {
  const entries = transcriptOf("revived");
  const last = noteAt(entries, "first line again: alpha");
  assert.ok(last > 0 && !text(entries[last]).includes("<tool-use-id>"), "the revival's notification names no call");
  const send = Date.parse(entries[callAt(entries, "toolu_481_send_revived")].timestamp);
  assert.deepEqual(runsOf(entries.slice(0, last)), [{ toolCallId: "toolu_481_agent_revived", status: "running", result: "first line: alpha", startedAt: send }], "revived, not notified yet: running from the SendMessage call");
  assert.deepEqual(runsOf(entries), [{ toolCallId: "toolu_481_agent_revived", status: "completed", result: "first line again: alpha", startedAt: send }]);
});

test("history: a first run's notification with only a task-id settles it", () => {
  const entries = transcriptOf("first-run");
  const note = noteAt(entries, "first line: beta");
  assert.ok(note > 0 && !text(entries[note]).includes("<tool-use-id>"), "the notification names no call");
  assert.deepEqual(runsOf(entries.slice(0, note)).map((run) => run.status), ["running"]);
  assert.deepEqual(runsOf(entries).map((run) => [run.toolCallId, run.status, run.result]), [["toolu_481_agent_first", "completed", "first line: beta"]]);
});

test("history: a 'waiting on its own background work' notification settles nothing; the later task-id one does", () => {
  const entries = transcriptOf("waiting");
  const interim = noteAt(entries, "started a background read; waiting for it");
  const final = noteAt(entries, "first line: gamma");
  assert.ok(interim > 0 && final > interim && text(entries[interim]).includes("is waiting on its own background work"), "the interim notification comes first");
  assert.deepEqual(runsOf(entries.slice(0, final)).map((run) => run.status), ["running"], "after the interim notification only: still running");
  assert.deepEqual(runsOf(entries).map((run) => [run.status, run.result]), [["completed", "first line: gamma"]], "the final notification settles it");
});

test("history: a notification claude absorbed into a running turn settles its run, the first and the revived one", () => {
  const entries = transcriptOf("absorbed");
  const first = noteAt(entries, "first line: delta");
  const revived = noteAt(entries, "first line again: delta");
  assert.ok(entries[first].type === "attachment" && entries[revived].type === "attachment", "both notifications are queued_command attachments");
  assert.deepEqual(runsOf(entries.slice(0, first + 1)).map((run) => run.status), ["completed"], "the first run's absorbed notification settles it");
  assert.deepEqual(runsOf(entries.slice(0, revived)).map((run) => run.status), ["running"], "the SendMessage revival reopens it");
  assert.deepEqual(runsOf(entries).map((run) => [run.status, run.result]), [["completed", "first line again: delta"]], "the revival's absorbed notification settles it");
  const { messages } = adapter.claudeTranscriptToMessages(entries);
  assert.ok(!messages.some((message) => JSON.stringify(message).includes("<task-notification>")), "an absorbed notification shows nothing in the chat");
});

test("history: a revival left waiting on its own background work is running until a later revival finishes", () => {
  const entries = transcriptOf("revived-waiting");
  const interim = noteAt(entries, "started a background read; waiting for it");
  const second = callAt(entries, "toolu_481_send_twice_2");
  assert.ok(interim > 0 && second > interim);
  assert.deepEqual(runsOf(entries.slice(0, second)).map((run) => run.status), ["running"], "the first revival only notified 'waiting': running");
  const runs = runsOf(entries);
  assert.deepEqual(runs.map((run) => [run.status, run.result]), [["completed", "first line again: epsilon"]], "the second revival's notification settles the run, the first revival with it");
  assert.equal(runs[0].startedAt, Date.parse(entries[second].timestamp), "the run starts at the latest revival");
});
