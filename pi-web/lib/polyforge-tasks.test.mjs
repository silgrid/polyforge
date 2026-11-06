import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-tasks.ts");
const pure = await jiti.import("./polyforge-tasks-pure.ts");
const memoryPure = await jiti.import("./polyforge-memory-pure.ts");

// The real `pf --json task list` row shape (the daemon's task record, spawn
// facts nested under spec, plus the 2.20.12 prompt_summary and the
// report_tail/reporter).
const REAL_ROW = {
  id: "task_0123456789abcdef", spec: { project: "workbench", candidates: ["pi:m/one"], max_duration_ns: 1200000000000, max_tokens: 300000 },
  state: "running", created_at: "2026-10-27T01:00:00Z", started_at: "2026-10-27T01:00:01Z", ended_at: "0001-01-01T00:00:00Z",
  candidate: "pi:m/one", wall_consumed_ns: 42000000000, tokens_consumed: 12345, prompt_summary: "fix the build",
  report_tail: "…the last report line", reporter: "alice",
  error: "ERROR_SENTINEL", origin_chat: "chat_SENTINEL", delivered: true, report: "REPORT_SENTINEL",
};

test("the Tasks floor is 2.20.12 and the boundary is inclusive", async () => {
  assert.equal(lib.POLYFORGE_TASKS_MINIMUM_PF_VERSION, "2.20.12");
  const detect = await jiti.import("./polyforge-detect.ts");
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.11", lib.POLYFORGE_TASKS_MINIMUM_PF_VERSION), true);
  assert.equal(detect.isPolyforgeVersionBelowMinimum("v2.20.12", lib.POLYFORGE_TASKS_MINIMUM_PF_VERSION), false);
});

test("the list passport keeps exactly the nine keys (id/project/state/prompt_summary, the timing facts, report_tail/reporter) and reads the project under spec", () => {
  const rows = lib.sanitizeTaskListAnswer({ tasks: [REAL_ROW, { id: "not-a-task" }, null], now: "x" });
  assert.equal(rows.length, 1, "an invalid id drops the row");
  // started_at and wall_consumed_ns travel; pf's zero ended_at reads as absent.
  // / owner-intended 2026-10-30 contract extension, not a relaxation:
  // the pin moves from seven to nine keys (report_tail, reporter); every other raw field stays dropped.
  assert.deepEqual(rows[0], {
    id: "task_0123456789abcdef", project: "workbench", state: "running", prompt_summary: "fix the build",
    started_at: "2026-10-27T01:00:01Z", wall_consumed_ns: 42000000000,
    report_tail: "…the last report line", reporter: "alice",
  });
  const serialized = JSON.stringify(rows);
  for (const dropped of ["ERROR_SENTINEL", "chat_SENTINEL", "REPORT_SENTINEL", "candidates", "tokens_consumed", "created_at", "report_len", "origin_chat", "delivered", "ended_at"]) {
    assert.ok(!serialized.includes(dropped), `the list must not carry ${dropped}`);
  }
  assert.deepEqual(lib.sanitizeTaskListAnswer({}), []);
  assert.deepEqual(lib.sanitizeTaskListAnswer(null), []);
});

test("the list row forwards all three timing facts when pf has them and none it lacks", () => {
  const done = lib.sanitizeTaskRow({ ...REAL_ROW, state: "completed", ended_at: "2026-10-27T01:05:00Z", report: "REPORT_SENTINEL" });
  assert.equal(done.started_at, "2026-10-27T01:00:01Z");
  assert.equal(done.ended_at, "2026-10-27T01:05:00Z");
  assert.equal(done.wall_consumed_ns, 42000000000);
  assert.ok(!JSON.stringify(done).includes("REPORT_SENTINEL"), "the report never rides the list row");
  const bare = lib.sanitizeTaskRow({ id: "task_0123456789abcdef", state: "queued", started_at: "0001-01-01T00:00:00Z", wall_consumed_ns: "12" });
  assert.deepEqual(Object.keys(bare).sort(), ["id", "project", "prompt_summary", "state"], "zero time and a non-numeric wall time are absent");
});

test("report_tail and reporter travel only as the strings pf sent", () => {
  const empty = lib.sanitizeTaskRow({ ...REAL_ROW, state: "completed", report_tail: "", reporter: "machine" });
  assert.equal(empty.report_tail, "", "an empty tail is a supplied value, forwarded as \"\"");
  assert.equal(empty.reporter, "machine");
  const spaced = lib.sanitizeTaskRow({ ...REAL_ROW, report_tail: "  **raw** line  " });
  assert.equal(spaced.report_tail, "  **raw** line  ", "the tail is forwarded as sent: no trim, no re-cut");
  const long = "…" + "x".repeat(199);
  assert.equal(lib.sanitizeTaskRow({ ...REAL_ROW, report_tail: long }).report_tail, long, "pf's bounded tail is never re-cut");
  const numeric = lib.sanitizeTaskRow({ ...REAL_ROW, report_tail: 7, reporter: null });
  assert.ok(!("report_tail" in numeric) && !("reporter" in numeric), "a non-string field is absent, never coerced");
  const old = { ...REAL_ROW };
  delete old.report_tail;
  delete old.reporter;
  assert.deepEqual(Object.keys(lib.sanitizeTaskRow(old)).sort(), ["id", "project", "prompt_summary", "started_at", "state", "wall_consumed_ns"],
    "a pf that predates the fields still lists, with no fabricated keys");
});

test("the detail passport keeps status, candidates, budget, times, prompt and report, and drops the rest", () => {
  const detail = lib.sanitizeTaskDetail({ ...REAL_ROW, spec: { ...REAL_ROW.spec, prompt: "the full prompt" }, report: "**done**", truncated: true });
  assert.deepEqual(Object.keys(detail).sort(), ["candidates", "created_at", "id", "max_duration_ns", "max_tokens", "project", "prompt",
    "report", "report_len", "started_at", "state", "tokens_consumed", "truncated", "wall_consumed_ns"],
  "a zero-time ended_at is absent; every other field is in the passport");
  assert.deepEqual(detail.candidates, ["pi:m/one"]);
  assert.equal(detail.max_tokens, 300000);
  assert.equal(detail.tokens_consumed, 12345);
  assert.equal(detail.prompt, "the full prompt");
  assert.equal(detail.report, "**done**");
  assert.equal(detail.report_len, 8);
  assert.equal(detail.truncated, true);
  const serialized = JSON.stringify(detail);
  for (const dropped of ["ERROR_SENTINEL", "chat_SENTINEL", "delivered"]) assert.ok(!serialized.includes(dropped), `the detail must not carry ${dropped}`);
  assert.equal(lib.sanitizeTaskDetail({ id: "bad" }), null);
});

test("the report is capped at 262144 UTF-8 bytes on a code-point boundary with report_len the uncut length", () => {
  const report = "a".repeat(262143) + "é" + "b".repeat(10);
  const detail = lib.sanitizeTaskDetail({ ...REAL_ROW, report });
  assert.ok(Buffer.byteLength(detail.report, "utf8") <= 262144, "never over the cap");
  assert.ok(!detail.report.includes("é"), "the straddling char is dropped whole");
  assert.equal(detail.report_len, Buffer.byteLength(report, "utf8"), "report_len is the uncut byte length");
  assert.equal(memoryPure.isMemoryBodyTruncated(detail.report, detail.report_len), true, "the client's one truncation rule fires");
  const whole = lib.sanitizeTaskDetail({ ...REAL_ROW, report: "a".repeat(262144) });
  assert.equal(memoryPure.isMemoryBodyTruncated(whole.report, whole.report_len), false, "a report at the cap reads whole");
});

test("spawn facts are read only under spec and a stray report_len is not an input", () => {
  const flat = lib.sanitizeTaskDetail({ id: "task_0123456789abcdef", state: "failed", project: "flat", prompt: "flat prompt",
    candidates: ["pi:flat"], max_tokens: 9, report: "abc", report_len: 300000 });
  assert.equal(flat.project, "");
  assert.equal(flat.prompt, "");
  assert.deepEqual(flat.candidates, []);
  assert.equal(flat.max_tokens, 0);
  assert.equal(flat.report_len, 3, "report_len is the measured length of the report pf sent");
});

test("sections list running tasks on top and the rest under all, and the search matches id, project, state and summary case-insensitively", () => {
  const rows = [
    { id: "task_1111111111111111", project: "workbench", state: "running", prompt_summary: "ALPHA job" },
    { id: "task_2222222222222222", project: "polyforge", state: "completed", prompt_summary: "bravo report" },
    { id: "task_3333333333333333", project: "workbench", state: "failed", prompt_summary: "charlie" },
  ];
  const sections = pure.splitTaskSections(rows);
  assert.deepEqual(sections.running.map((row) => row.id), ["task_1111111111111111"]);
  assert.deepEqual(sections.all.map((row) => row.id), ["task_2222222222222222", "task_3333333333333333"],
    "全部 holds the rest: every task is listed exactly once, the running ones on top");
  const ids = (needle) => rows.filter((row) => pure.taskMatches(row, needle)).map((row) => row.id.slice(-1));
  assert.deepEqual(ids("BRAVO"), ["2"], "summary, case-insensitive");
  assert.deepEqual(ids("polyforge"), ["2"], "project");
  assert.deepEqual(ids("task_3333"), ["3"], "id");
  assert.deepEqual(ids("failed"), ["3"], "state");
  assert.deepEqual(ids("  "), ["1", "2", "3"], "an empty search keeps every row");
  assert.equal(pure.isPolyforgeTaskId("task_0123456789abcdef"), true);
  for (const bad of ["task_0123456789ABCDEF", "task_123", "../task_0123456789abcdef", "", 7]) {
    assert.equal(pure.isPolyforgeTaskId(bad), false, `${bad} is not a task id`);
  }
});

test("the modules never log, read env, or spawn a write verb", async () => {
  for (const path of ["./polyforge-tasks.ts", "./polyforge-tasks-pure.ts"]) {
    const source = await readFile(new URL(path, import.meta.url), "utf8");
    assert.doesNotMatch(source, /console\.(log|error|warn)/, `${path} never logs`);
    assert.doesNotMatch(source, /process\.env/, `${path} never reads an environment value`);
    assert.doesNotMatch(source, /"(spawn|cancel)"/, `${path} never names a task write verb in an argv`);
  }
});

test("block timing: running counts from started_at, finished shows wall_consumed_ns and the ended age; absent fields stay undefined", () => {
  const now = Date.parse("2026-10-30T02:10:00Z");
  const running = { id: "task_1111111111111111", project: "p", state: "running", prompt_summary: "s", started_at: "2026-10-30T02:09:55Z", wall_consumed_ns: 0 };
  assert.equal(pure.taskDurationSeconds(running, now), 5, "now - started_at, whole seconds");
  assert.equal(pure.taskDurationSeconds(running, now + 1000), 6, "the clock ticks it");
  assert.equal(pure.taskEndedAgo(running, now), undefined, "a running task has not ended");
  const done = { id: "task_2222222222222222", project: "p", state: "completed", prompt_summary: "s", started_at: "2026-10-30T02:00:00Z", ended_at: "2026-10-30T02:08:00Z", wall_consumed_ns: 42_000_000_000 };
  assert.equal(pure.taskDurationSeconds(done, now), 42, "wall_consumed_ns, not a wall-clock difference");
  assert.equal(pure.taskEndedAgo(done, now), 120);
  const bare = { id: "task_3333333333333333", project: "p", state: "queued", prompt_summary: "s" };
  assert.equal(pure.taskDurationSeconds(bare, now), undefined);
  assert.equal(pure.taskEndedAgo(bare, now), undefined);
  assert.equal(pure.taskDurationSeconds({ ...running, started_at: "garbage" }, now), undefined, "never NaN");
  assert.equal(pure.taskDurationSeconds({ ...running, started_at: "2026-10-30T02:11:00Z" }, now), 0, "a start in the future is never negative");
  assert.equal(pure.taskEndedAgo({ ...done, ended_at: "2026-10-30T03:00:00Z" }, now), 0, "never negative");
  assert.deepEqual([0, 45, 600, 7200, 200000, -3, Number.NaN].map(pure.formatTaskAge), ["0s", "45s", "10m", "2h", "2d", "0s", "0s"]);
});

test("filters: the six state options, project options from the list, and project + state + search composed", () => {
  assert.deepEqual([...pure.TASK_STATE_FILTERS], ["all", "running", "completed", "capped", "failed", "interrupted"]);
  const rows = [
    { id: "task_1111111111111111", project: "workbench", state: "running", prompt_summary: "ALPHA job" },
    { id: "task_2222222222222222", project: "polyforge", state: "completed", prompt_summary: "bravo report" },
    { id: "task_3333333333333333", project: "workbench", state: "failed", prompt_summary: "charlie" },
    { id: "task_4444444444444444", project: "", state: "weird", prompt_summary: "delta" },
  ];
  assert.deepEqual(pure.taskProjectOptions(rows), ["polyforge", "workbench"], "distinct, sorted, an empty project is no option");
  assert.deepEqual(pure.taskProjectOptions([]), []);
  const ids = (filter) => pure.filterTaskRows(rows, { search: "", project: "", state: "all", ...filter }).map((row) => row.id.slice(-1));
  assert.deepEqual(ids({}), ["1", "2", "3", "4"], "no filter keeps every row in pf's order");
  assert.deepEqual(ids({ project: "workbench" }), ["1", "3"]);
  assert.deepEqual(ids({ state: "failed" }), ["3"]);
  assert.deepEqual(ids({ project: "workbench", state: "running" }), ["1"]);
  assert.deepEqual(ids({ project: "polyforge", state: "completed", search: "BRAVO" }), ["2"], "project + state + search compose");
  assert.deepEqual(ids({ project: "polyforge", search: "alpha" }), [], "the search still narrows");
  assert.deepEqual(ids({ project: "nope" }), [], "an unknown project matches nothing");
  assert.deepEqual(ids({ state: "capped" }), []);
});

test("reporter options and filter: humans ascending, machine last, whole list, composed with the other filters", () => {
  const rows = [
    { id: "task_1111111111111111", project: "workbench", state: "running", prompt_summary: "ALPHA job", reporter: "bob" },
    { id: "task_2222222222222222", project: "polyforge", state: "completed", prompt_summary: "bravo report", reporter: "machine" },
    { id: "task_3333333333333333", project: "workbench", state: "failed", prompt_summary: "charlie", reporter: "alice" },
    { id: "task_4444444444444444", project: "workbench", state: "completed", prompt_summary: "delta", reporter: "alice" },
    { id: "task_5555555555555555", project: "polyforge", state: "completed", prompt_summary: "echo", reporter: "" },
    { id: "task_6666666666666666", project: "polyforge", state: "completed", prompt_summary: "foxtrot" },
  ];
  assert.equal(pure.TASK_REPORTER_MACHINE, "machine");
  assert.deepEqual(pure.taskReporterOptions(rows), { humans: ["alice", "bob"], machine: true },
    "distinct humans ascending; machine flagged (offered last); empty and absent reporters ignored");
  assert.deepEqual(pure.taskReporterOptions([rows[1]]), { humans: [], machine: true }, "a machine-only list");
  assert.deepEqual(pure.taskReporterOptions(rows.slice(4)), { humans: [], machine: false }, "a field-less list offers only 全部发起人");
  assert.deepEqual(pure.taskReporterOptions([]), { humans: [], machine: false });
  const ids = (filter) => pure.filterTaskRows(rows, { search: "", project: "", state: "all", ...filter }).map((row) => row.id.slice(-1));
  assert.deepEqual(ids({}), ["1", "2", "3", "4", "5", "6"], "no reporter term keeps every row, a field-less one included");
  assert.deepEqual(ids({ reporter: null }), ["1", "2", "3", "4", "5", "6"], "null is 全部发起人");
  assert.deepEqual(ids({ reporter: "alice" }), ["3", "4"]);
  assert.deepEqual(ids({ reporter: "bob" }), ["1"]);
  assert.deepEqual(ids({ reporter: "machine" }), ["2"], "machine matches only machine rows, never a field-less one");
  assert.deepEqual(ids({ reporter: "alice", state: "completed" }), ["4"], "reporter + state compose");
  assert.deepEqual(ids({ reporter: "alice", project: "polyforge" }), [], "reporter + project compose");
  assert.deepEqual(ids({ reporter: "alice", search: "CHARLIE" }), ["3"], "reporter + search compose");
  assert.deepEqual(ids({ reporter: "carol" }), [], "an unknown reporter matches nothing");
});
