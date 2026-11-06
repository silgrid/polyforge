// carries: lib/claude-adapter.ts, lib/claude-subagent-view.ts, lib/__fixtures__/claude-2.1.322/*
// a message relayed through the main session's SendMessage resumes a
// finished subagent. claude's task events then name the SendMessage call while the
// subagent's frames name the original Agent call; the run reopens (live and in history)
// and no second run appears. Every run carries its start time. Replays the real 2.1.322
// `relay/` recording (lib/__fixtures__/claude-2.1.322/README.md).
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

const relay = path.join(here, "__fixtures__", "claude-2.1.322", "relay");
const jsonl = (file) => fs.readFileSync(path.join(relay, file), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
const stream = jsonl("stream.jsonl");
const transcript = jsonl("transcript.jsonl");
const AGENT_ID = "aabe137615e4325e1";
const sidecars = {
  [AGENT_ID]: {
    entries: jsonl(`subagents/agent-${AGENT_ID}.jsonl`),
    meta: JSON.parse(fs.readFileSync(path.join(relay, "subagents", `agent-${AGENT_ID}.meta.json`), "utf8")),
  },
};

const blocks = (event) => (Array.isArray(event.message?.content) ? event.message.content : []);
/** The main frames' tool calls named `name`, with the frame index and its timestamp. */
const mainCalls = (name) => stream.flatMap((event, index) => (event.type === "assistant" && !event.parent_tool_use_id
  ? blocks(event).filter((block) => block.type === "tool_use" && block.name === name).map((block) => ({ id: block.id, input: block.input, index, at: Date.parse(event.timestamp) }))
  : []));
const [agentCall] = mainCalls("Agent");
const sends = mainCalls("SendMessage").filter((call) => call.input.to === AGENT_ID);
const resumeStart = stream.findIndex((event) => event.subtype === "task_started" && event.tool_use_id === sends.at(-1).id);
const resumeEnd = stream.findIndex((event) => event.subtype === "task_notification" && event.tool_use_id === sends.at(-1).id);

/** Replays the stream through one translator; every `claude_subagent` event, tagged with its frame. */
function replay(frames = stream) {
  const translator = new adapter.ClaudeStreamTranslator(() => 1);
  const runs = [];
  frames.forEach((frame, index) => {
    for (const event of translator.push(frame).events) if (event.type === adapter.CLAUDE_SUBAGENT_EVENT) runs.push({ index, run: event.run });
  });
  return { translator, runs };
}

test("live: the SendMessage resume reopens the original Agent run and starts no run of its own", () => {
  assert.ok(agentCall && sends.length === 2 && resumeStart > 0 && resumeEnd > resumeStart, "the relay recording has its Agent call, two SendMessage calls and the resume's task events");
  const { runs } = replay();
  assert.deepEqual([...new Set(runs.map(({ run }) => run.toolCallId))], [agentCall.id], "only the Agent call is a run; the SendMessage call is none");
  const atStart = runs.filter(({ index }) => index === resumeStart);
  assert.equal(atStart.length, 1, "the resume's task_started announces the run");
  assert.equal(atStart[0].run.toolCallId, agentCall.id);
  assert.equal(atStart[0].run.status, "running", "the resume reopens the settled run");
  const atEnd = runs.filter(({ index }) => index === resumeEnd);
  assert.equal(atEnd.length, 1, "the resume's notification lands on the run");
  assert.equal(atEnd[0].run.status, "completed");
  // claude's own `task_updated {status: completed}` for the resumed run comes just before its notification.
  const resumeDone = stream.findIndex((event, index) => index > resumeStart && event.subtype === "task_updated" && event.patch?.status === "completed");
  assert.ok(resumeDone > resumeStart && resumeDone < resumeEnd);
  const meanwhile = runs.filter(({ index }) => index > resumeStart && index < resumeDone);
  assert.ok(meanwhile.some(({ run }) => run.entries.length > 0), "the resumed run's entries stream into it");
  for (const { run } of meanwhile) assert.equal(run.status, "running", "the run reads running until claude reports the resume completed");
  assert.equal(runs.at(-1).run.prompt, agentCall.input.prompt, "the resume's prompt (the relayed text) does not replace the run's own");
});

test("live: a run starts at its Agent call's time and a resume at its SendMessage call's time", () => {
  const { runs } = replay();
  const first = runs[0].run;
  assert.equal(first.startedAt, agentCall.at, "the run starts at the assistant frame holding its Agent call");
  const resumed = runs.find(({ index }) => index === resumeStart).run;
  assert.equal(resumed.startedAt, sends.at(-1).at, "the resume starts at the assistant frame holding its SendMessage call");
  assert.ok(resumed.startedAt > first.startedAt);
  assert.equal(runs.at(-1).run.startedAt, sends.at(-1).at, "the settled run keeps the resume's start");
});

test("live: no subagent is unsettled after the relay recording's last result", () => {
  const during = replay(stream.slice(0, resumeStart + 1)).translator;
  assert.equal(during.hasUnsettledSubagents(), true, "the resumed background agent is unsettled while it runs");
  const { translator } = replay();
  assert.equal(stream.at(-1).type, "result");
  assert.equal(translator.hasUnsettledSubagents(), false, "the resume's notification and claude's follow-up turn settle it");
});

/** Synthetic frames: a main Agent call and its run's task_started. */
const agentFrames = (id, taskId, runId = "run-1") => [
  { type: "assistant", timestamp: "2026-11-10T10:00:00.000Z", message: { role: "assistant", content: [{ type: "tool_use", id, name: "Agent", input: { description: "probe", prompt: "look" } }] } },
  { type: "system", subtype: "task_started", task_id: taskId, run_id: runId, tool_use_id: id, description: "probe" },
];
const statusOf = (translator, frame) => translator.push(frame).events.filter((event) => event.type === adapter.CLAUDE_SUBAGENT_EVENT).at(-1)?.run.status;

test("live: claude's interrupt settles a run as stopped or failed, and only a resume reopens it", () => {
  const killed = new adapter.ClaudeStreamTranslator(() => 1);
  for (const frame of agentFrames("toolu_k", "task-k")) killed.push(frame);
  assert.equal(statusOf(killed, { type: "system", subtype: "task_notification", task_id: "task-k", tool_use_id: "toolu_k", status: "killed" }), "stopped");
  assert.equal(statusOf(killed, { type: "system", subtype: "task_updated", task_id: "task-k", patch: { status: "running" } }), "stopped", "a later running update leaves it stopped");

  const failed = new adapter.ClaudeStreamTranslator(() => 1);
  for (const frame of agentFrames("toolu_f", "task-f")) failed.push(frame);
  assert.equal(statusOf(failed, { type: "user", message: { role: "user", content: [{ type: "tool_result", tool_use_id: "toolu_f", is_error: true, content: "interrupted" }] } }), "failed");
  assert.equal(statusOf(failed, { type: "system", subtype: "task_updated", task_id: "task-f", patch: { status: "running" } }), "failed", "a later running update leaves it failed");

  failed.push({ type: "assistant", timestamp: "2026-11-10T10:05:00.000Z", message: { role: "assistant", content: [{ type: "tool_use", id: "toolu_send", name: "SendMessage", input: { to: "task-f", message: "again" } }] } });
  const resumed = failed.push({ type: "system", subtype: "task_started", task_id: "task-f", run_id: "run-2", tool_use_id: "toolu_send", prompt: "again" }).events.filter((event) => event.type === adapter.CLAUDE_SUBAGENT_EVENT);
  assert.deepEqual(resumed.map((event) => [event.run.toolCallId, event.run.status]), [["toolu_f", "running"]], "a resume reopens it");
});

test("live: claude's own resume under the Agent call id reopens the run", () => {
  // A message queued after the subagent's last tool round: claude resumes the agent itself,
  // a task_started under the original Agent call with a new run id (README, relay/).
  const translator = new adapter.ClaudeStreamTranslator(() => Date.parse("2026-11-10T10:09:00.000Z"));
  for (const frame of agentFrames("toolu_q", "task-q")) translator.push(frame);
  assert.equal(statusOf(translator, { type: "system", subtype: "task_notification", task_id: "task-q", tool_use_id: "toolu_q", status: "completed" }), "completed");
  const reopened = translator.push({ type: "system", subtype: "task_started", task_id: "task-q", run_id: "run-2", tool_use_id: "toolu_q", prompt: "queued" }).events.filter((event) => event.type === adapter.CLAUDE_SUBAGENT_EVENT);
  assert.deepEqual(reopened.map((event) => [event.run.toolCallId, event.run.status]), [["toolu_q", "running"]]);
  assert.equal(reopened[0].run.prompt, "look", "the queued message does not replace the run's prompt");
  assert.ok(reopened[0].run.startedAt > Date.parse("2026-11-10T10:00:00.000Z"), "the resume starts later than the run");
  assert.equal(statusOf(translator, { type: "system", subtype: "task_notification", task_id: "task-q", tool_use_id: "toolu_q", status: "completed" }), "completed");
});

const coordinatorText = "Now read f1.txt once more";

test("history: the resumed run is rebuilt once, settled, with its resumed entries", () => {
  const { subagents } = adapter.claudeTranscriptToMessages(transcript, sidecars);
  assert.deepEqual(subagents.map((run) => run.toolCallId), [agentCall.id], "one run per Agent call");
  const [run] = subagents;
  assert.equal(run.status, "completed");
  const ids = run.entries.map((entry) => entry.id);
  const sidecarLast = sidecars[AGENT_ID].entries.at(-1).uuid;
  assert.ok(ids.includes(sidecarLast), "the resumed round's reply, appended to the same sidecar, is in the run");
  const coordinator = run.entries.filter((entry) => entry.message.role === "user" && JSON.stringify(entry.message.content).includes(coordinatorText));
  assert.equal(coordinator.length, 1, "the relayed message the resumed agent got is in its timeline, once");
  assert.equal(run.prompt, agentCall.input.prompt);
  assert.equal(run.startedAt, replay().runs.at(-1).run.startedAt, "the same start as the live run: the resume's SendMessage call");
});

test("history: a SendMessage that resumed a run's agent reads that run as running from the SendMessage's time until its notification", () => {
  const callEntry = transcript.findIndex((entry) => entry.type === "assistant" && blocks(entry).some((block) => block.id === sends.at(-1).id));
  const resultEntry = transcript.findIndex((entry) => entry.type === "user" && blocks(entry).some((block) => block.tool_use_id === sends.at(-1).id));
  const noteEntry = transcript.findIndex((entry) => entry.type === "user" && typeof entry.message.content === "string" && entry.message.content.includes(`<tool-use-id>${sends.at(-1).id}</tool-use-id>`));
  assert.ok(callEntry > 0 && resultEntry > callEntry && noteEntry > resultEntry, "the transcript holds the resume's call, its answer and its notification");
  const at = Date.parse(transcript[callEntry].timestamp);
  // Before the SendMessage's result (an awaited resume) and after it (claude 2.1.322
  // answers "Resuming agent" at once and notifies later): running either way.
  for (const cut of [resultEntry, noteEntry]) {
    const { subagents } = adapter.claudeTranscriptToMessages(transcript.slice(0, cut), sidecars);
    assert.deepEqual(subagents.map((run) => [run.toolCallId, run.status, run.startedAt]), [[agentCall.id, "running", at]], `cut at entry ${cut}`);
  }
  const queued = adapter.claudeTranscriptToMessages(transcript.slice(0, transcript.findIndex((entry) => typeof entry.message?.content === "string" && entry.message.content.includes("<task-notification>"))), sidecars).subagents;
  assert.equal(queued[0].startedAt, agentCall.at, "a SendMessage claude queued for the running agent is no resume");
});
