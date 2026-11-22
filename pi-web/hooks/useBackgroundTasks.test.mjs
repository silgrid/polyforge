// carries: hooks/useBackgroundTasks.ts, lib/background-tasks-client.ts, app/api/agent/*/bg-tasks/*
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { stripTypeScriptTypes } from "node:module";
import vm from "node:vm";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { tsconfigPaths: true });
const {
  nextBackgroundTasksListState,
  parseTaskListBody,
  isBackgroundTasksChangeSignal,
  answerErrorText,
} = await jiti.import("@/lib/background-tasks-client");

const source = await readFile(new URL("./useBackgroundTasks.ts", import.meta.url), "utf8");

test("terminal-notification dedupe fires exactly once per task id", () => {
  const context = vm.createContext({ Set, console });
  vm.runInContext(stripTypeScriptTypes(source.slice(source.indexOf("const notifiedTerminalTaskIds"), source.indexOf("export type BackgroundTasksFetchState")).replace(/export /g, "")), context);
  const first = context.markTerminalNotified("task-a");
  const second = context.markTerminalNotified("task-a");
  const other = context.markTerminalNotified("task-b");
  assert.equal(first, true);
  assert.equal(second, false);
  assert.equal(other, true);
});

/** A registry record, hand-made (the wire shape AC2 promises). */
function record(fields = {}) {
  return {
    id: "t-1", sessionId: "session-a", command: "npm run build", cwd: "/tmp", status: "running",
    startedAt: 1, endedAt: null, exitCode: null, signal: null, reason: null, bytesWritten: null,
    logPath: "/tmp/log", notified: false, ...fields,
  };
}

test("the list state machine drops a session's late answer after a switch (切换会话不串项)", () => {
  const loading = { kind: "loading" };
  const ofA = { kind: "ready", tasks: [record({ id: "t-a", sessionId: "session-a" })] };
  const ofB = { kind: "ready", tasks: [record({ id: "t-b", sessionId: "session-b" })] };

  // Session A's answer lands while A is current: the state is A's records.
  assert.equal(nextBackgroundTasksListState(loading, 1, { seq: 1, status: 200, body: { tasks: [record({ id: "t-a" })] } }).kind, "ready");
  // The switch to B bumps the sequence to 2; A's late answer (seq 1) changes
  // nothing — B's state keeps B's rows, never A's.
  assert.equal(nextBackgroundTasksListState(ofB, 2, { seq: 1, status: 200, body: { tasks: [record({ id: "t-a" })] } }), ofB);
  // An unmount (the effect's cleanup bumps the sequence) drops the same way.
  assert.equal(nextBackgroundTasksListState(ofA, 7, { seq: 3, status: 200, body: { tasks: [] } }), ofA);
  // B's own answer (the current sequence) replaces the state as usual.
  const afterB = nextBackgroundTasksListState(ofA, 2, { seq: 2, status: 200, body: { tasks: [record({ id: "t-b", sessionId: "session-b" })] } });
  assert.equal(afterB.kind, "ready");
  assert.deepEqual(afterB.kind === "ready" ? afterB.tasks.map((task) => task.id) : null, ["t-b"]);
});

test("the list state machine answers a refusal as an error and keeps a valid answer", () => {
  const prev = { kind: "ready", tasks: [record()] };
  // A refused answer surfaces as the error state naming the body's own reason.
  const refused = nextBackgroundTasksListState(prev, 4, { seq: 4, status: 404, body: { error: "Background task not found: x" } });
  assert.deepEqual(refused, { kind: "error", error: "Background task not found: x" });
  // A transport failure (status 0, no body) names the failure, never crashes.
  assert.deepEqual(nextBackgroundTasksListState(prev, 5, { seq: 5, status: 0, body: null }), { kind: "error", error: "HTTP 0" });
  // A malformed body (no task array) is an error, not a crash.
  const malformed = nextBackgroundTasksListState(prev, 6, { seq: 6, status: 200, body: { unexpected: true } });
  assert.equal(malformed.kind, "error");
  // A body whose task array holds a non-record is refused whole.
  assert.equal(parseTaskListBody({ tasks: [record(), { id: 5 }] }), null);
  assert.deepEqual(parseTaskListBody({ tasks: [record({ id: "t-2" })] })?.map((task) => task.id), ["t-2"]);
  assert.equal(answerErrorText(500, null), "HTTP 500");
});

test("both SSE kinds are change signals and nothing else is", () => {
  assert.equal(isBackgroundTasksChangeSignal({ type: "background_task_terminal", task: { id: "t" } }), true);
  assert.equal(isBackgroundTasksChangeSignal({ type: "background_tasks_update", tasks: [] }), true);
  // An event of any other shape is no signal: the hook does not re-read.
  assert.equal(isBackgroundTasksChangeSignal({ type: "queue_update", queuedMessages: 0 }), false);
  assert.equal(isBackgroundTasksChangeSignal({ type: "message_end" }), false);
});

test("the hook wires the pure machine (a wiring check, not a behaviour claim)", () => {
  // The behaviour lives in lib/background-tasks-client.ts and its tests; this
  // pins only that the hook runs that machine, so the machine's rules are the
  // hook's.
  assert.match(source, /nextBackgroundTasksListState\(prev, fetchSeq\.current, \{ seq, status, body \}\)/);
  assert.match(source, /if \(isBackgroundTasksChangeSignal\(event\)\) refresh\(\);/);
  assert.match(source, /fetchSeq\.current \+= 1;/, "a session switch invalidates the in-flight answer");
});
