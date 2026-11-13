import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const view = await jiti.import("./claude-subagent-view.ts");

const entry = (id, text) => ({ id, message: { role: "assistant", content: [{ type: "text", text }] } });
const run = (over = {}) => ({ toolCallId: "toolu_a", background: true, status: "running", entries: [], ...over });

test("merging keeps one copy per entry id and appends new ids once, in order", () => {
  const history = run({ entries: [entry("e1", "one"), entry("e2", "two")] });
  const merged = view.mergeClaudeSubagentRun(history, run({ entries: [entry("e2", "two"), entry("e3", "three")] }));
  assert.deepEqual(merged.entries.map((item) => item.id), ["e1", "e2", "e3"]);
  const again = view.mergeClaudeSubagentRun(merged, run({ entries: [entry("e1", "one"), entry("e2", "two"), entry("e3", "three")] }));
  assert.deepEqual(again.entries.map((item) => item.id), ["e1", "e2", "e3"], "a reload of the same entries adds nothing");
});

test("header fields take the incoming value, and a settled status never returns to running", () => {
  const settled = view.mergeClaudeSubagentRun(run({ progress: "Reading" }), run({ status: "completed", usage: { totalTokens: 5, toolUses: 1, durationMs: 9 } }));
  assert.equal(settled.status, "completed");
  assert.equal(settled.progress, "Reading", "an absent field keeps the held one");
  const stale = view.mergeClaudeSubagentRun(settled, run({ status: "running" }));
  assert.equal(stale.status, "completed", "a later running copy (an older read) does not reopen the run");
  assert.deepEqual(stale.usage, { totalTokens: 5, toolUses: 1, durationMs: 9 });
});

test("merging a run list builds a map by tool call id and returns the same map for nothing", () => {
  const empty = {};
  assert.equal(view.mergeClaudeSubagentRuns(empty, []), empty);
  const runs = view.mergeClaudeSubagentRuns(empty, [run({ entries: [entry("e1", "x")] }), run({ toolCallId: "toolu_b" })]);
  assert.deepEqual(Object.keys(runs), ["toolu_a", "toolu_b"]);
});

test("a row opens by default while running, failed or interrupted and stays closed once completed; a manual toggle wins", () => {
  assert.equal(view.isClaudeSubagentExpandedByDefault("running"), true);
  assert.equal(view.isClaudeSubagentExpandedByDefault("failed"), true);
  assert.equal(view.isClaudeSubagentExpandedByDefault("completed"), false);
  assert.equal(view.isClaudeSubagentExpandedByDefault("stopped"), true, "an interrupted run stays open like an error");
  assert.equal(view.isClaudeSubagentExpanded({ toolCallId: "toolu_manual", status: "running" }), true);
  view.setClaudeSubagentManualExpansion("toolu_manual", false);
  assert.equal(view.isClaudeSubagentExpanded({ toolCallId: "toolu_manual", status: "running" }), false, "collapsed by hand stays collapsed");
  view.setClaudeSubagentManualExpansion("toolu_manual", true);
  assert.equal(view.isClaudeSubagentExpanded({ toolCallId: "toolu_manual", status: "completed" }), true, "expanded by hand stays open after finishing");
});

test("header statistics show only known parts, tokens with grouping", () => {
  assert.deepEqual(view.claudeSubagentStatParts(undefined), {});
  assert.deepEqual(view.claudeSubagentStatParts({ totalTokens: 10379, toolUses: 1, durationMs: 1906 }), { toolUses: 1, durationMs: 1906, totalTokens: 10379 });
  assert.deepEqual(view.claudeSubagentStatParts({ totalTokens: 0, toolUses: 0, durationMs: 0 }), {});
  assert.equal(view.formatClaudeSubagentTokens(10379, "en"), "10,379");
  assert.equal(view.CLAUDE_SUBAGENT_PAGE_SIZE, 25);
  assert.equal(view.isClaudeSubagentToolName("Agent"), true);
  assert.equal(view.isClaudeSubagentToolName("Task"), true);
  assert.equal(view.isClaudeSubagentToolName("Read"), false);
});

test("a history read wins for the entry ids it carries and keeps the later live ones", () => {
  const live = view.mergeClaudeSubagentRuns({}, [run({ entries: [entry("e1", "streamed"), entry("e2", "two"), entry("e3", "after the read")] })]);
  const merged = view.mergeClaudeSubagentHistory(live, [run({ status: "running", entries: [entry("e1", "stored"), entry("e2", "two")] })]);
  assert.deepEqual(merged.toolu_a.entries.map((item) => [item.id, item.message.content[0].text]), [["e1", "stored"], ["e2", "two"], ["e3", "after the read"]]);
  assert.equal(view.mergeClaudeSubagentHistory(live, undefined), live, "a detail without the field changes nothing");
});

test("the body keeps tool results out of the steps and shows the final reply once, as the Result", () => {
  const call = { id: "c1", message: { role: "assistant", content: [{ type: "toolCall", toolCallId: "t", toolName: "Read", input: {} }] } };
  const result = { id: "r1", message: { role: "toolResult", toolCallId: "t", content: [] } };
  const running = view.claudeSubagentBody(run({ entries: [call, result, entry("e1", "TOKEN")] }));
  assert.deepEqual(running.steps.map((item) => item.id), ["c1", "e1"]);
  assert.equal(running.settled, false);
  const done = view.claudeSubagentBody(run({ status: "completed", result: "TOKEN", entries: [call, result, entry("e1", "TOKEN")] }));
  assert.deepEqual(done.steps.map((item) => item.id), ["c1"]);
  assert.equal(done.result, "TOKEN");
  const other = view.claudeSubagentBody(run({ status: "completed", result: "summary", entries: [entry("e1", "TOKEN")] }));
  assert.deepEqual(other.steps.map((item) => item.id), ["e1"], "a different last reply stays a step");
  assert.equal(view.claudeSubagentBody(run({ status: "failed" })).result, null, "no result: the row says so");
});

test("only a step made solely of running background Agent calls stays out of the fold", () => {
  const call = (toolCallId, toolName = "Agent") => ({ type: "toolCall", toolCallId, toolName });
  const runs = {
    bg: run({ toolCallId: "bg" }),
    fg: run({ toolCallId: "fg", background: false }),
    done: run({ toolCallId: "done", status: "completed" }),
  };
  assert.equal(view.isRunningBackgroundAgentMessage([call("bg")], runs), true);
  assert.equal(view.isRunningBackgroundAgentMessage([call("bg", "Task")], runs), true);
  assert.equal(view.isRunningBackgroundAgentMessage([call("done")], runs), false, "settled: back into the fold");
  assert.equal(view.isRunningBackgroundAgentMessage([call("fg")], runs), false, "a foreground call is an ordinary step");
  assert.equal(view.isRunningBackgroundAgentMessage([call("bg"), { type: "text", text: "x" }], runs), false, "a step with anything else folds");
  assert.equal(view.isRunningBackgroundAgentMessage([call("bg", "Read")], runs), false);
  assert.equal(view.isRunningBackgroundAgentMessage([call("missing")], runs), false, "no run (a pi session): unchanged");
  assert.equal(view.isRunningBackgroundAgentMessage([], runs), false);
});

// the running list above the composer, derived from the run map alone.
const ids = (runs) => runs.map((item) => item.toolCallId);
const agentCall = (id) => ({ role: "assistant", content: [{ type: "toolCall", id, name: "Agent", arguments: { description: id } }] });

test("the running list holds every running run, foreground and background, oldest start first", () => {
  const runs = view.mergeClaudeSubagentRuns({}, [
    run({ toolCallId: "toolu_fg", background: false, startedAt: 300, entries: [{ id: "e1", message: agentCall("toolu_nested") }] }),
    run({ toolCallId: "toolu_bg", background: true, startedAt: 100 }),
    run({ toolCallId: "toolu_nested", background: false, startedAt: 200 }),
    run({ toolCallId: "toolu_done", status: "completed", startedAt: 50 }),
    run({ toolCallId: "toolu_failed", status: "failed", startedAt: 60 }),
    run({ toolCallId: "toolu_unknown", background: false }),
  ]);
  assert.deepEqual(ids(view.runningClaudeSubagents(runs)), ["toolu_bg", "toolu_nested", "toolu_fg", "toolu_unknown"], "running at any depth, by start; an unknown start last");
  assert.deepEqual(view.runningClaudeSubagents({}), []);
});

test("a run leaves the running list once claude settles it as completed, failed or stopped", () => {
  for (const status of ["completed", "failed", "stopped"]) {
    const started = view.mergeClaudeSubagentRuns({}, [run({ startedAt: 100, entries: [entry("e1", "working")] })]);
    assert.deepEqual(ids(view.runningClaudeSubagents(started)), ["toolu_a"], "started: listed");
    const settled = view.mergeClaudeSubagentRuns(started, [run({ status, startedAt: 100 })]);
    assert.deepEqual(view.runningClaudeSubagents(settled), [], `${status}: removed from the list`);
    assert.equal(settled.toolu_a.status, status, "the run itself stays in the map");
    assert.deepEqual(settled.toolu_a.entries, started.toolu_a.entries, "with the same entries");
  }
});

test("a later start reopens a settled run, and a read with the same or no start never does", () => {
  const completed = view.mergeClaudeSubagentRun(run({ startedAt: 100 }), run({ status: "completed", startedAt: 100 }));
  const resumed = view.mergeClaudeSubagentRun(completed, run({ status: "running", startedAt: 200 }));
  assert.equal(resumed.status, "running", "a resume (a later start) reopens it");
  assert.equal(resumed.startedAt, 200);
  assert.equal(view.mergeClaudeSubagentRun(completed, run({ status: "running", startedAt: 100 })).status, "completed", "the same start does not reopen");
  assert.equal(view.mergeClaudeSubagentRun(completed, run({ status: "running" })).status, "completed", "no start does not reopen");
  // A history read taken mid-resume arrives after the live completion of that resume.
  const liveDone = view.mergeClaudeSubagentRun(resumed, run({ status: "completed", startedAt: 200 }));
  const staleRead = view.mergeClaudeSubagentRun(liveDone, run({ status: "running", startedAt: 200 }));
  assert.equal(staleRead.status, "completed", "a stale read of the resume does not reopen it");
  assert.equal(view.mergeClaudeSubagentRun(liveDone, run({ status: "completed", startedAt: 100 })).startedAt, 200, "an older read keeps the later start");
});

test("folding the same history twice, or history after live runs, lists each running run once", () => {
  const history = [run({ toolCallId: "toolu_a", startedAt: 100, entries: [entry("e1", "one")] }), run({ toolCallId: "toolu_b", status: "completed", startedAt: 50 })];
  const once = view.mergeClaudeSubagentHistory({}, history);
  const twice = view.mergeClaudeSubagentHistory(once, history);
  assert.deepEqual(ids(view.runningClaudeSubagents(twice)), ["toolu_a"]);
  assert.deepEqual(twice.toolu_a.entries.map((item) => item.id), ["e1"]);
  const live = view.mergeClaudeSubagentRuns({}, [run({ toolCallId: "toolu_a", startedAt: 100, entries: [entry("e1", "one"), entry("e2", "live")] }), run({ toolCallId: "toolu_c", startedAt: 150 })]);
  const reloaded = view.mergeClaudeSubagentHistory(live, history);
  assert.deepEqual(ids(view.runningClaudeSubagents(reloaded)), ["toolu_a", "toolu_c"]);
});

test("elapsed time counts from the run's start and is unknown without one", () => {
  assert.equal(view.claudeSubagentElapsedMs(run({ startedAt: 1_000 }), 4_500), 3_500);
  assert.equal(view.claudeSubagentElapsedMs(run({ startedAt: 1_000 }), 900), 0, "a clock behind the start reads zero, never negative");
  assert.equal(view.claudeSubagentElapsedMs(run(), 4_500), undefined);
});

test("a run's launch position is the main message holding its top-level Agent call", () => {
  const messages = [
    { role: "user", content: "go" },
    { role: "assistant", content: [{ type: "text", text: "starting" }, ...agentCall("toolu_top").content] },
    { role: "toolResult", toolCallId: "toolu_top", content: [] },
  ];
  const entryIds = ["u1", "a1", "r1"];
  const runs = view.mergeClaudeSubagentRuns({}, [
    run({ toolCallId: "toolu_top", entries: [{ id: "s1", message: agentCall("toolu_nested") }] }),
    run({ toolCallId: "toolu_nested", entries: [{ id: "s2", message: agentCall("toolu_deep") }] }),
    run({ toolCallId: "toolu_deep" }),
  ]);
  assert.equal(view.claudeSubagentLaunchEntryId("toolu_top", messages, entryIds, runs), "a1");
  assert.equal(view.claudeSubagentLaunchEntryId("toolu_deep", messages, entryIds, runs), "a1", "a nested run jumps to its outermost ancestor's call");
  assert.equal(view.claudeSubagentLaunchEntryId("toolu_missing", messages, entryIds, runs), undefined);
});

test("the launch position also reads the chat's normalized tool call blocks", () => {
  // The page holds main messages and run entries normalized (lib/normalize.ts: `toolCallId`, `toolName`).
  const normalized = (id) => ({ role: "assistant", content: [{ type: "toolCall", toolCallId: id, toolName: "Agent", input: {} }] });
  const runs = view.mergeClaudeSubagentRuns({}, [run({ toolCallId: "toolu_top", entries: [{ id: "s1", message: normalized("toolu_nested") }] }), run({ toolCallId: "toolu_nested" })]);
  assert.equal(view.claudeSubagentLaunchEntryId("toolu_nested", [{ role: "user", content: "go" }, normalized("toolu_top")], ["u1", "a1"], runs), "a1");
});

test("the relay message names the agent id and description in the fixed wording, then the text verbatim", () => {
  const text = "  first line\nsecond line  ";
  assert.equal(
    view.claudeSubagentRelayText(run({ agentId: "aabe137615e4325e1", description: "chain", agentType: "general-purpose" }), text),
    "请用 SendMessage 把下面这段话原样转给 subagent aabe137615e4325e1（chain），不要做别的：\n  first line\nsecond line  ",
  );
  assert.equal(
    view.claudeSubagentRelayText(run({ agentId: "a1", agentType: "Explore" }), "go"),
    "请用 SendMessage 把下面这段话原样转给 subagent a1（Explore），不要做别的：\ngo",
    "no description: the agent type",
  );
  assert.equal(
    view.claudeSubagentRelayText(run({ agentId: "a1" }), "go"),
    "请用 SendMessage 把下面这段话原样转给 subagent a1（Agent），不要做别的：\ngo",
  );
});

// a live step has no entry id until the post-run reload, so with no pending
// jump an unguarded `jump?.entryId === entryId` was `undefined === undefined` — every answered
// live turn opened its process details (claude-session-219 S3, red in start mode).
test("a subagent jump targets only its own entry; no pending jump targets nothing, an id-less live step included", () => {
  assert.equal(view.isSubagentJumpTarget(null, undefined), false);
  assert.equal(view.isSubagentJumpTarget(undefined, undefined), false);
  assert.equal(view.isSubagentJumpTarget(null, "e1"), false);
  assert.equal(view.isSubagentJumpTarget({ entryId: "e1" }, undefined), false);
  assert.equal(view.isSubagentJumpTarget({ entryId: "e1" }, "e2"), false);
  assert.equal(view.isSubagentJumpTarget({ entryId: "e1" }, "e1"), true);
});
