// carries: lib/claude-adapter.ts
// claude reports several background agents in one `<task-notification>` (one
// `<task-id>` each, one shared status) — after a resumed session, for the agents that "didn't
// finish before the previous session ended". The history walk settles every run it names, as a
// notification turn and as one absorbed into a running turn (a `queued_command` attachment).
// Before, only the first `<task-id>` was read and the others stayed `running` on every reload.
import "#test-env";
import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createJiti } from "jiti";

const here = path.dirname(fileURLToPath(import.meta.url));
const jiti = createJiti(import.meta.url, { alias: { "@": path.dirname(here) }, interopDefault: true });
const adapter = await jiti.import("./claude-adapter.ts");

const runsOf = (entries) => adapter.claudeTranscriptToMessages(entries).subagents.map((run) => [run.toolCallId, run.status]);

const session = "50400000-0000-4000-8000-000000000000";
let seq = 0;
const stamp = () => {
  seq += 1;
  return { uuid: `50400000-0000-4000-8001-${String(seq).padStart(12, "0")}`, timestamp: new Date(Date.UTC(2026, 10, 9, 5, 11, seq)).toISOString(), sessionId: session };
};

/** The notification of the ticket, as claude wrote it on resume (two task-ids, one status). */
const notification = [
  "<task-notification>",
  "<task-id>a686ae11e278f5cd7</task-id>",
  "<task-id>ad8770e0da5e9712d</task-id>",
  "<status>stopped</status>",
  `<summary>2 background agents didn't finish before the previous session ended: "B: implement reliable K8s cleanup" (a686ae11e278f5cd7), "bizplane: re-pin writer to core fix" (ad8770e0da5e9712d).</summary>`,
  "<note>No completion record was found for them in the previous session. ...</note>",
  "</task-notification>",
].join("\n");

const user = (content, extra = {}) => ({ isSidechain: false, type: "user", message: { role: "user", content }, ...extra, ...stamp() });
const call = (id, name, input) => ({ isSidechain: false, type: "assistant", message: { model: "claude-opus-5-5", type: "message", role: "assistant", content: [{ type: "tool_use", id, name, input }], stop_reason: "tool_use" }, ...stamp() });
const launch = (id, description) => call(id, "Agent", { description, subagent_type: "general-purpose", run_in_background: true, prompt: description });
const receipt = (id, agentId, description) =>
  user([{ tool_use_id: id, type: "tool_result", content: [{ type: "text", text: `Async agent launched successfully.\nagentId: ${agentId}\nThe agent is working in the background.` }] }], {
    toolUseResult: { isAsync: true, status: "async_launched", agentId, description, prompt: description },
  });

/** Two background agents launched in one turn, both still running. */
const launched = () => [
  user("Start both agents.", { promptSource: "sdk" }),
  launch("toolu_504_b", "B: implement reliable K8s cleanup"),
  receipt("toolu_504_b", "a686ae11e278f5cd7", "B: implement reliable K8s cleanup"),
  launch("toolu_504_repin", "bizplane: re-pin writer to core fix"),
  receipt("toolu_504_repin", "ad8770e0da5e9712d", "bizplane: re-pin writer to core fix"),
];

test("history: a notification naming two task-ids settles both runs stopped", () => {
  const before = launched();
  assert.deepEqual(runsOf(before), [["toolu_504_b", "running"], ["toolu_504_repin", "running"]], "launched, not notified: both running");
  const entries = [...before, user(notification, { promptSource: "system", origin: { kind: "task-notification", producer: "session-task" } })];
  const runs = runsOf(entries);
  assert.deepEqual(runs, [["toolu_504_b", "stopped"], ["toolu_504_repin", "stopped"]]);
  assert.ok(!runs.some(([, status]) => status === "running"), "no run is left running");
});

test("history: an absorbed notification naming two task-ids settles both runs", () => {
  const entries = [
    ...launched(),
    call("toolu_504_bash", "Bash", { command: "sleep 5" }),
    { type: "queue-operation", operation: "enqueue", content: notification, ...stamp() },
    { isSidechain: false, type: "attachment", attachment: { type: "queued_command", prompt: notification, commandMode: "task-notification", origin: { kind: "task-notification", producer: "session-task" } }, ...stamp() },
    { type: "queue-operation", operation: "remove", content: notification, ...stamp() },
    user([{ tool_use_id: "toolu_504_bash", type: "tool_result", content: [{ type: "text", text: "" }] }], { toolUseResult: { stdout: "", stderr: "", interrupted: false } }),
  ];
  assert.deepEqual(runsOf(entries), [["toolu_504_b", "stopped"], ["toolu_504_repin", "stopped"]]);
  const { messages } = adapter.claudeTranscriptToMessages(entries);
  assert.ok(!messages.some((message) => JSON.stringify(message).includes("<task-notification>")), "an absorbed notification shows nothing in the chat");
});

test("history: a single task-id notification in the same transcript settles only its run", () => {
  const single = notification.replace("<task-id>ad8770e0da5e9712d</task-id>\n", "<tool-use-id>toolu_504_b</tool-use-id>\n");
  const entries = [...launched(), user(single, { promptSource: "system", origin: { kind: "task-notification", producer: "session-task" } })];
  assert.deepEqual(runsOf(entries), [["toolu_504_b", "stopped"], ["toolu_504_repin", "running"]]);
});

test("history: a <task-id> quoted in a notification's <result> settles nothing ( r1#1)", () => {
  const quoted = `the stuck card's notice was <task-notification>\n<task-id>ad8770e0da5e9712d</task-id>\n<status>stopped</status>\n</task-notification>`;
  const single = [
    "<task-notification>",
    "<task-id>a686ae11e278f5cd7</task-id>",
    "<tool-use-id>toolu_504_b</tool-use-id>",
    "<status>completed</status>",
    `<summary>Agent "B: implement reliable K8s cleanup" finished</summary>`,
    `<result>${quoted}</result>`,
    "</task-notification>",
  ].join("\n");
  const entries = [...launched(), user(single, { promptSource: "system", origin: { kind: "task-notification", producer: "session-task" } })];
  const runs = adapter.claudeTranscriptToMessages(entries).subagents.map((run) => [run.toolCallId, run.status, run.result]);
  assert.deepEqual(runs, [["toolu_504_b", "completed", quoted], ["toolu_504_repin", "running", undefined]]);
});
