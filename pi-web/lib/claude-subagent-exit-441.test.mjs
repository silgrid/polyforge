// carries: lib/claude-adapter.ts, lib/claude-session.ts
// a claude child that exits (crash, kill, shutdown) sends no task_notification
// or tool result for the runs it still had running. Those runs settle as stopped on the
// exit, so the running list above the composer drops them instead of ticking forever.
import "#test-env";
import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createJiti } from "jiti";

const here = path.dirname(fileURLToPath(import.meta.url));
const jiti = createJiti(import.meta.url, { alias: { "@": path.dirname(here) }, interopDefault: true });
const adapter = await jiti.import("./claude-adapter.ts");
const view = await jiti.import("./claude-subagent-view.ts");

const agentCall = (id, at, parent) => ({
  type: "assistant",
  timestamp: at,
  ...(parent ? { parent_tool_use_id: parent, uuid: `entry-${id}` } : {}),
  message: { role: "assistant", content: [{ type: "tool_use", id, name: "Agent", input: { description: id, prompt: "look" } }] },
});
const started = (id, taskId, extra = {}) => ({ type: "system", subtype: "task_started", task_id: taskId, tool_use_id: id, description: id, ...extra });
const subagentRuns = (events) => events.filter((event) => event.type === adapter.CLAUDE_SUBAGENT_EVENT).map((event) => event.run);

test("a run still running when the claude child exits settles as stopped and leaves the running list", () => {
  const translator = new adapter.ClaudeStreamTranslator(() => Date.parse("2026-11-10T10:10:00.000Z"));
  const frames = [
    { type: "system", subtype: "init", model: "claude-haiku-5-5" },
    agentCall("toolu_fg", "2026-11-10T10:00:00.000Z"),
    started("toolu_fg", "task-fg"),
    agentCall("toolu_nested", "2026-11-10T10:00:05.000Z", "toolu_fg"),
    started("toolu_nested", "task-nested"),
    agentCall("toolu_bg", "2026-11-10T10:01:00.000Z"),
    started("toolu_bg", "task-bg", { is_backgrounded: true }),
    agentCall("toolu_done", "2026-11-10T10:02:00.000Z"),
    started("toolu_done", "task-done"),
    { type: "system", subtype: "task_notification", task_id: "task-done", tool_use_id: "toolu_done", status: "completed", result: "ok" },
  ];
  let held = {};
  for (const frame of frames) held = view.mergeClaudeSubagentRuns(held, subagentRuns(translator.push(frame).events));
  assert.deepEqual(view.runningClaudeSubagents(held).map((run) => run.toolCallId), ["toolu_fg", "toolu_nested", "toolu_bg"], "three runs are running before the exit");
  assert.equal(translator.hasUnsettledSubagents(), true, "the background run holds the session");

  // The child exits: the session flushes the turn and settles what the child left running.
  const exit = subagentRuns([...translator.flush(), ...translator.settleOnExit()]);
  assert.deepEqual(exit.map((run) => [run.toolCallId, run.status]).sort(), [["toolu_bg", "stopped"], ["toolu_fg", "stopped"], ["toolu_nested", "stopped"]], "each run still running is announced stopped, once; the settled one is left alone");
  held = view.mergeClaudeSubagentRuns(held, exit);
  assert.deepEqual(view.runningClaudeSubagents(held), [], "the running list is empty");
  assert.equal(held.toolu_done.status, "completed", "claude's own last word stands");
  assert.equal(held.toolu_fg.description, "toolu_fg", "the run itself stays, only settled");
  assert.equal(translator.hasUnsettledSubagents(), false, "nothing holds the session after the exit");
});
