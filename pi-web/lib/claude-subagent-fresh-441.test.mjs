// carries: lib/claude-adapter.ts, lib/claude-session.ts, lib/__fixtures__/claude-2.1.322/*
// a claude child is restarted routinely (idle reap, server restart, edit), and
// a new child's translator has seen none of the session's runs. A message relayed to a
// finished subagent through such a child must still reopen the original Agent run — not
// start a run keyed by the SendMessage call — and carry the resume's start, so the client
// merge reopens it. In history, a SendMessage queued for a running agent is no resume and
// keeps the run's start. Replays the real 2.1.322 `relay/` recording.
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
const view = await jiti.import("./claude-subagent-view.ts");

const relay = path.join(here, "__fixtures__", "claude-2.1.322", "relay");
const jsonl = (file) => fs.readFileSync(path.join(relay, file), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
const stream = jsonl("stream.jsonl");
const stdin = jsonl("stdin.jsonl");
const transcript = jsonl("transcript.jsonl");
const AGENT_ID = "aabe137615e4325e1";
const sidecarEntries = jsonl(`subagents/agent-${AGENT_ID}.jsonl`);
const sidecarMeta = JSON.parse(fs.readFileSync(path.join(relay, "subagents", `agent-${AGENT_ID}.meta.json`), "utf8"));
/** The sidecar as it stood at `at` (entries written by then), with `meta`. */
const sidecarsAt = (at, meta = sidecarMeta) => ({
  [AGENT_ID]: { entries: sidecarEntries.filter((entry) => Date.parse(entry.timestamp) <= at), meta },
});

const blocks = (event) => (Array.isArray(event.message?.content) ? event.message.content : []);
const mainCalls = (name) => stream.flatMap((event) => (event.type === "assistant" && !event.parent_tool_use_id
  ? blocks(event).filter((block) => block.type === "tool_use" && block.name === name).map((block) => ({ id: block.id, input: block.input, at: Date.parse(event.timestamp) }))
  : []));
const [agentCall] = mainCalls("Agent");
const sends = mainCalls("SendMessage").filter((call) => call.input.to === AGENT_ID);

test("a fresh translator that never saw the run reopens it when a SendMessage resumes its agent, and starts no phantom run", () => {
  // The third stdin line relays to the finished agent; a respawned child sees the stream from its lifecycle on.
  const relayLine = stdin.filter((line) => line.type === "user").at(-1);
  const from = stream.findIndex((event) => event.type === "command_lifecycle" && event.command_uuid === relayLine.uuid);
  const resumeStart = stream.findIndex((event) => event.subtype === "task_started" && event.tool_use_id === sends.at(-1).id);
  const resumeEnd = stream.findIndex((event) => event.subtype === "task_notification" && event.tool_use_id === sends.at(-1).id);
  assert.ok(agentCall && sends.length === 2 && from > 0 && resumeStart > from && resumeEnd > resumeStart, "the relay recording holds the relay line's lifecycle, then the resume's task events");

  // The session's runs as the transcript held them when the child was spawned (readClaudeHistory().subagents).
  const cut = transcript.findIndex((entry) => entry.uuid === relayLine.uuid);
  assert.ok(cut > 0, "the transcript holds the relay line");
  const known = adapter.claudeTranscriptToMessages(transcript.slice(0, cut), sidecarsAt(Date.parse(transcript[cut].timestamp))).subagents;
  assert.deepEqual(known.map((run) => [run.toolCallId, run.agentId, run.status]), [[agentCall.id, AGENT_ID, "completed"]], "the run had finished before the relay");

  const translator = new adapter.ClaudeStreamTranslator(() => 1);
  translator.seedRuns(known);
  const runs = [];
  stream.slice(from).forEach((frame, offset) => {
    for (const event of translator.push(frame).events) if (event.type === adapter.CLAUDE_SUBAGENT_EVENT) runs.push({ index: from + offset, run: event.run });
  });

  assert.deepEqual([...new Set(runs.map(({ run }) => run.toolCallId))], [agentCall.id], "no run is keyed by the SendMessage call");
  const atStart = runs.filter(({ index }) => index === resumeStart);
  assert.equal(atStart.length, 1, "the resume's task_started announces the original run");
  assert.equal(atStart[0].run.status, "running");
  assert.equal(atStart[0].run.startedAt, sends.at(-1).at, "the reopened run starts at the SendMessage call");
  const resumeDone = stream.findIndex((event, index) => index > resumeStart && event.subtype === "task_updated" && event.patch?.status === "completed");
  for (const { index, run } of runs) if (index > resumeStart && index < resumeDone) assert.equal(run.status, "running", `frame ${index}: running during the resume`);
  const atEnd = runs.filter(({ index }) => index === resumeEnd);
  assert.equal(atEnd.length, 1, "the resume's notification lands on the original run");
  assert.equal(atEnd[0].run.status, "completed");

  // The page holds the history's settled run; the live events reopen it there and settle it again.
  let held = view.mergeClaudeSubagentRuns({}, known);
  held = view.mergeClaudeSubagentRuns(held, [atStart[0].run]);
  assert.equal(held[agentCall.id].status, "running", "the client merge reopens the settled run");
  assert.deepEqual(view.runningClaudeSubagents(held).map((run) => run.toolCallId), [agentCall.id], "the running list holds the original run, nothing else");
  held = view.mergeClaudeSubagentRuns(held, runs.filter(({ index }) => index > resumeStart).map(({ run }) => run));
  assert.equal(held[agentCall.id].status, "completed");
  assert.deepEqual(Object.keys(held), [agentCall.id]);
  assert.equal(translator.hasUnsettledSubagents(), false, "the resume's notification and claude's follow-up turn settle it");
});

test("a SendMessage to a still-running agent read in history keeps that run's start time", () => {
  // The first SendMessage, before claude answered it: the agent was still reading (its meta not finished yet).
  const callEntry = transcript.findIndex((entry) => entry.type === "assistant" && blocks(entry).some((block) => block.id === sends[0].id));
  const resultEntry = transcript.findIndex((entry) => entry.type === "user" && blocks(entry).some((block) => block.tool_use_id === sends[0].id));
  assert.ok(callEntry > 0 && resultEntry === callEntry + 1, "the transcript holds the first SendMessage and its answer right after it");
  const at = Date.parse(transcript[callEntry].timestamp);
  const { subagents } = adapter.claudeTranscriptToMessages(transcript.slice(0, resultEntry), sidecarsAt(at, { ...sidecarMeta, finished: false }));
  assert.deepEqual(subagents.map((run) => [run.toolCallId, run.status, run.startedAt]), [[agentCall.id, "running", agentCall.at]], "still running, from its Agent call");
});
