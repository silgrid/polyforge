// carries: lib/pi-subagent-panel.ts, components/PiSubagentPanel.tsx, hooks/usePiSubagentPanel.ts
// polyforge#255 u2: the pi subagent panel's pure projection — the status and
// elapsed rules the owner settled (Q2, Q8), the transcript merge the 2 s
// snapshot poll uses (Q5), and the launch-position lookup Jump uses (Q6).
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const {
  piSubagentStatusLabelKey,
  piSubagentIsActive,
  piSubagentCanSteer,
  piSubagentInputReason,
  piSubagentElapsedMs,
  mergePiSubagentEntries,
  findPiSubagentLaunchEntryId,
  piSubagentRunFromJson,
} = await jiti.import("./pi-subagent-panel.ts");

const NOW = Date.UTC(2026, 10, 23, 12, 0, 0);
const run = (fields = {}) => ({
  sessionId: "child-1",
  parentSessionId: "parent-1",
  parentToolCallId: "toolu-agent-1",
  profile: "explore",
  description: "Find the parser",
  status: "running",
  createdAt: new Date(NOW - 12_000).toISOString(),
  ...fields,
});
const entry = (id, text = id) => ({ id, message: { role: "assistant", content: [{ type: "text", text }] } });

test("polyforge#255 u2: only a running subagent takes a steer; every other state names why not", () => {
  assert.equal(piSubagentCanSteer("running"), true, "a running run takes a message");
  assert.equal(piSubagentInputReason("running", "ready"), null, "a running run has no disabled reason");
  for (const status of ["queued", "starting", "completed", "failed", "aborted", "interrupted", "unknown"]) {
    assert.equal(piSubagentCanSteer(status), false, `${status} cannot be steered`);
    assert.ok(piSubagentInputReason(status, "ready"), `${status} names its reason`);
  }
  assert.equal(piSubagentInputReason("running", "loading"), "piSubagent.input.loading", "an unread run disables sending");
  assert.equal(piSubagentInputReason("running", "missing"), "piSubagent.input.missing", "a missing run disables sending");
  assert.equal(piSubagentInputReason("running", "error"), "piSubagent.input.error", "an unreadable run disables sending");
});

test("polyforge#255 u2: elapsed time is the first-created span, fixed once settled and unknown without an end", () => {
  assert.equal(piSubagentElapsedMs(run({ status: "running" }), NOW), 12_000, "a running run ticks from its creation");
  assert.equal(piSubagentElapsedMs(run({ status: "queued" }), NOW), 12_000, "a queued run counts its wait");
  const completedAt = new Date(NOW - 3_000).toISOString();
  assert.equal(piSubagentElapsedMs(run({ status: "completed", completedAt }), NOW + 60_000), 9_000, "a settled run keeps completedAt − createdAt");
  assert.equal(piSubagentElapsedMs(run({ status: "interrupted" }), NOW + 60_000), undefined, "an interrupted run without completedAt never grows");
  assert.equal(piSubagentElapsedMs(run({ status: "completed", completedAt: new Date(NOW - 20_000).toISOString() }), NOW), 0, "a clock that went backwards clamps to 0");
  assert.equal(piSubagentElapsedMs(run({ createdAt: "not-a-date" }), NOW), undefined, "an unreadable creation time is unknown");
});

test("polyforge#255 u2: the snapshot merge appends only unseen entries and prepends a page once", () => {
  const merged = mergePiSubagentEntries([entry("a"), entry("b")], [entry("b"), entry("c")], "append");
  assert.deepEqual(merged.map((e) => e.id), ["a", "b", "c"], "an overlapping poll appends only the new entry");
  const paged = mergePiSubagentEntries([entry("c"), entry("d")], [entry("a"), entry("b"), entry("c")], "prepend");
  assert.deepEqual(paged.map((e) => e.id), ["a", "b", "c", "d"], "an earlier page keeps the loaded history once");
  const replaced = mergePiSubagentEntries([entry("old")], [entry("new")], "replace");
  assert.deepEqual(replaced.map((e) => e.id), ["new"], "the first load replaces whatever came before");
});

test("polyforge#255 u2: Jump finds the entry whose tool call launched the child", () => {
  const messages = [
    { role: "user", content: [{ type: "text", text: "go" }] },
    { role: "assistant", content: [{ type: "toolCall", toolCallId: "toolu-other", toolName: "Read", input: {} }] },
    { role: "assistant", content: [{ type: "toolCall", toolCallId: "toolu-agent-1", toolName: "Agent", input: {} }] },
  ];
  const entryIds = ["e-1", "e-2", "e-3"];
  assert.equal(findPiSubagentLaunchEntryId("toolu-agent-1", messages, entryIds), "e-3", "the Agent block's entry is the launch position");
  assert.equal(findPiSubagentLaunchEntryId("toolu-missing", messages, entryIds), null, "no matching block means no jump");
});

test("polyforge#255 u2: an unknown status keeps its label and the run reader accepts only a real run", () => {
  assert.equal(piSubagentStatusLabelKey("completed"), "piSubagent.status.completed");
  assert.equal(piSubagentStatusLabelKey("interrupted"), "piSubagent.status.interrupted");
  assert.equal(piSubagentStatusLabelKey("weird"), "piSubagent.status.unknown", "an unlisted status reads as unknown");
  assert.equal(piSubagentIsActive("running"), true);
  assert.equal(piSubagentIsActive("queued"), true);
  assert.equal(piSubagentIsActive("completed"), false);
  const parsed = piSubagentRunFromJson({ run: run({ status: "weird" }) });
  assert.equal(parsed.sessionId, "child-1");
  assert.equal(parsed.status, "unknown", "an unlisted wire status reads as unknown, never as running");
  assert.equal(piSubagentRunFromJson({}), null, "a body without a run is not a run");
  assert.equal(piSubagentRunFromJson({ run: { sessionId: 7 } }), null, "a run without a session id is rejected");
});