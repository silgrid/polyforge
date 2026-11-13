import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// The job card's conversation vocabulary (workflow contract v2, design §5.2,
// §5.4; change 14; workflow contract 4, design #483 §7): which
// jobs wait, the question the selected one waits on, when the one input box
// is open (running AND awaiting: one path; parked: the message wakes it) and
// when it closes (no agent to reach, or one that cannot start — then the
// copyable commands), and the owner's echoes.
const jiti = createJiti(import.meta.url);
const pure = await jiti.import("./polyforge-conversation-pure.ts");

test("the module stays client-safe: no node: import", async () => {
  const source = await readFile(new URL("./polyforge-conversation-pure.ts", import.meta.url), "utf8");
  assert.doesNotMatch(source, /from\s+["']node:/);
});

const job = (wi, status) => ({ wi, slug: wi, status, stepAgeSec: 1, idleAgeSec: 1, candidate: "pi:m", spawnN: 1 });

test("needsInputJobs: the needs_input jobs in list order (a v1 question and an open pf step ask read alike on the status face)", () => {
  const status = { queue: { running: 1, max: 4 }, jobs: [job("wi_a", "running"), job("wi_b", "needs_input"), job("wi_c", "paused"), job("wi_d", "needs_input")] };
  assert.deepEqual(pure.needsInputJobs(status).map((j) => j.wi), ["wi_b", "wi_d"]);
  assert.deepEqual(pure.needsInputJobs(null), []);
});

test("currentPendingQuestion: the watch snapshot's pending question", () => {
  const base = { wi: "wi_a", slug: "a#1", workflowStatus: "running", steps: [], declared_steps: null, events: [] };
  assert.equal(pure.currentPendingQuestion(null), null);
  assert.equal(pure.currentPendingQuestion(base), null, "nothing asked");
  const q = { id: "2026-11-10T10:00:00.000000Z", step: "build", question: "  Which colour?\n- amber\n", askedAt: "2026-11-10T10:00:00.000000Z" };
  assert.deepEqual(pure.currentPendingQuestion({ ...base, pendingQuestion: q }), q, "an ask: id = its stamp, the question verbatim");
});

// workflow contract 4 (design #483 §7): a parked work item's
// box stays open — the message wakes the step; the card has no buttons
// — and only a park whose agent cannot start closes it, with
// the copyable owner commands.
const BLOCKED = { input: "open", awaiting: false, park: "blocked" };
const PAUSED = { input: "open", awaiting: false, park: "paused" };
const closed = (reason, commands = []) => ({ input: "closed", reason, commands });

test("conversationState: open while the step runs or waits (one path), and while it is parked (the message wakes it)", () => {
  const state = (jobStatus, extra = {}) => pure.conversationState({ jobStatus, daemonPresent: true, ...extra });
  assert.deepEqual(state("running"), { input: "open", awaiting: false }, "a live interjection goes the same way");
  assert.deepEqual(state("needs_input"), { input: "open", awaiting: true });
  assert.deepEqual(state("paused"), PAUSED, "a pause: the message resumes the step");
  assert.deepEqual(state("queued"), closed("queued"), "not dispatched: no agent and no park");
  for (const finished of ["completed", "failed", "cancelled"]) assert.deepEqual(state(finished), closed("finished"), finished);
  assert.deepEqual(state("sealing"), closed("sealing"), "a transient runtime state offers nothing (review r2: named, not unknown)");
  assert.deepEqual(state("some_future_state"), closed("unknown"), "an unknown runtime state offers nothing");
  // a park outranks the job row: every kind a message wakes keeps the box open
  for (const kind of ["incomplete", "driver", "write_failed", "some_future_kind"]) {
    assert.deepEqual(state("running", { blocked: { kind, reason: "r" } }), BLOCKED, kind);
  }
  assert.deepEqual(state("running", { blocked: { kind: "contract", reason: "r" } }), closed("contract", ["cancel"]), "the contract park: its agent cannot start — cancel and re-file");
  // no live job: the watch snapshot's workflow status decides
  assert.deepEqual(state(undefined, { workflowStatus: "blocked" }), BLOCKED);
  assert.deepEqual(state(undefined, { workflowStatus: "paused" }), PAUSED);
  assert.deepEqual(state(undefined, { workflowStatus: "completed" }), closed("finished"));
  assert.deepEqual(state(undefined, { workflowStatus: "wrapped" }), closed("finished"));
  assert.deepEqual(state(undefined, { workflowStatus: "failed_gate" }), closed("finished"));
  assert.deepEqual(state(undefined, {}), closed("unknown"));
  // the daemon offline: nothing can be sent
  assert.deepEqual(pure.conversationState({ jobStatus: "running", daemonPresent: false }), closed("offline"));
  assert.deepEqual(pure.conversationState({ jobStatus: undefined, workflowStatus: "blocked", daemonPresent: false }), closed("offline"), "offline outranks a park");
});

test("the box closes only on queued, ended, offline, transient and cannot-start states — and only cannot-start shows commands", () => {
  const states = [
    ...["running", "needs_input", "paused", "queued", "claiming", "recovering", "sealing", "completed", "some_future_state"].map((jobStatus) => ({ jobStatus })),
    { jobStatus: undefined, workflowStatus: "blocked" },
    { jobStatus: undefined, workflowStatus: "paused" },
    { jobStatus: undefined, blocked: { kind: "driver", reason: "r" } },
    { jobStatus: undefined, blocked: { kind: "contract", reason: "r" } },
    { jobStatus: undefined, entries: [{ id: 1, at: "t", kind: "work_item", wi: "wi_A", class: "blocked", attention: true, resolved: false, dismissed: false }] },
  ];
  const cannotStart = new Set(["contract", "candidates", "runtime"]);
  const seen = new Set();
  for (const input of states) {
    const state = pure.conversationState({ daemonPresent: true, ...input });
    if (state.input === "open") {
      assert.equal("menu" in state, false, `${JSON.stringify(input)}: no buttons ride an open box`);
      seen.add(state.park ?? "agent");
      continue;
    }
    seen.add(state.reason);
    assert.ok(["queued", "finished", "claiming", "recovering", "sealing", "unknown", ...cannotStart].includes(state.reason), `${JSON.stringify(input)} closes for ${state.reason}`);
    assert.equal(state.commands.length > 0, cannotStart.has(state.reason), `${JSON.stringify(input)}: commands only when no agent can start`);
    assert.equal("menu" in state, false, "a closed box carries the commands, no buttons");
  }
  assert.ok(seen.has("blocked") && seen.has("paused") && seen.has("contract") && seen.has("runtime"), "the probe covers both parks and two cannot-start states");
});

test("review r1 #5 (as review r2 corrected it): a runtime-made block cannot start (resume · cancel); a driver park wakes; a feed entry never decides a parked item", () => {
  const entry = (cls, extra = {}) => ({ id: 1, at: "t", kind: "work_item", wi: "wi_A", class: cls, attention: true, resolved: false, dismissed: false, ...extra });
  const state = (extra) => pure.conversationState({ daemonPresent: true, jobStatus: undefined, ...extra });
  const driverPark = { kind: "driver", step: "build", reason: "step build ended without `pf step handoff` 3 times in a row" };
  // an open candidates_exhausted entry proves nothing (it names no step): the park the message wakes
  assert.deepEqual(state({ blocked: driverPark, entries: [entry("candidates_exhausted")] }), BLOCKED);
  assert.deepEqual(state({ workflowStatus: "blocked", entries: [entry("candidates_exhausted")] }), BLOCKED);
  // a supervision block (the driver's park) wakes, 打回到… still beside it until C3
  assert.deepEqual(state({ blocked: driverPark, entries: [entry("step_dead", { attention: false })] }), BLOCKED);
  // I3: the capsule clone failed at the claim — no job, no execution, the runtime's blocked entry
  assert.deepEqual(state({ entries: [entry("blocked")] }), closed("runtime", ["resume", "cancel"]));
  assert.deepEqual(state({ entries: [entry("blocked", { resolved: true })] }), closed("unknown"), "a resolved block is no park");
  assert.deepEqual(state({ entries: [entry("paused")] }), PAUSED, "a dropped paused item: the message resumes it");
  // a live job's entries never close its box
  assert.deepEqual(pure.conversationState({ daemonPresent: true, jobStatus: "running", entries: [entry("blocked"), entry("candidates_exhausted")] }), { input: "open", awaiting: false });
});

test("the routing helper and the option path are gone (workflow contract v2 change 19): every message is words", () => {
  assert.equal(pure.routeOwnerMessage, undefined);
});

test("parseOwnerMessageAck: the route's ack, bounded to the known fields; malformed is null", () => {
  assert.deepEqual(pure.parseOwnerMessageAck({ kind: "reply", at: "2026-11-10T10:00:01Z", askAt: "2026-11-10T10:00:00Z", step: "build", spawnN: 2, delivered: true, disposition: "started", extra: "SECRET" }),
    { kind: "reply", at: "2026-11-10T10:00:01Z", askAt: "2026-11-10T10:00:00Z", step: "build", spawnN: 2, delivered: true, disposition: "started" });
  assert.deepEqual(pure.parseOwnerMessageAck({ kind: "message", at: "t", delivered: false, disposition: "resume", note: "the process did not take it" }),
    { kind: "message", at: "t", delivered: false, disposition: "resume", note: "the process did not take it" });
  assert.deepEqual(pure.parseOwnerMessageAck({ kind: "message", at: "t", step: "build", spawnN: 3, delivered: false, disposition: "wake" }),
    { kind: "message", at: "t", step: "build", spawnN: 3, delivered: false, disposition: "wake" }, "a woken park's ack (workflow contract 4)");
  for (const bad of [null, "reply", {}, { kind: "delivered" }, { kind: 3 }, { kind: "answer", questionId: "q1" }]) assert.equal(pure.parseOwnerMessageAck(bad), null, JSON.stringify(bad));
});

test("pendingOwnerEchoes: an echo leaves once the stream's owner line of the same record stamp arrives; an echo whose ack named no stamp stays", () => {
  const ack = { kind: "reply", delivered: true };
  const echoes = [{ at: "2026-11-10T10:00:01.000Z", text: "amber", ack }, { at: "2026-11-10T10:00:05.000Z", text: "and bold", ack }, { at: "2026-11-10T10:00:09.000Z", text: "v1", ack: { kind: "message", delivered: false } }];
  const lines = [{ kind: "narration" }, { kind: "owner", at: Date.parse("2026-11-10T10:00:01.009Z"), msgAt: "2026-11-10T10:00:01.000Z" }, { kind: "event", at: Date.parse("2026-11-10T10:00:05.000Z") }];
  assert.deepEqual(pure.pendingOwnerEchoes(echoes, lines).map((echo) => echo.text), ["and bold", "v1"], "only an owner line naming the echo's record stamp replaces it");
  assert.deepEqual(pure.pendingOwnerEchoes(echoes, []).map((echo) => echo.text), ["amber", "and bold", "v1"]);
});

test("OWNER_MESSAGE_TEXT_MAX stays inside the daemon's 64 KiB byte bound at four bytes per character", () => {
  assert.equal(pure.OWNER_MESSAGE_TEXT_MAX, 16_000);
  assert.ok(pure.OWNER_MESSAGE_TEXT_MAX * 4 < 64 * 1024);
});

test("review r1 #1: an echo is retired by the record's own stamp (msgAt), not the event envelope's time — they differ", () => {
  const ack = { kind: "reply", at: "2026-11-10T10:00:01.123456Z", delivered: true };
  const echoes = [{ at: "2026-11-10T10:00:01.123456Z", text: "amber", ack }];
  // the event log stamps its envelope after the record's fsync: a later millisecond
  const line = { kind: "owner", at: Date.parse("2026-11-10T10:00:01.131Z"), msgAt: "2026-11-10T10:00:01.123456Z" };
  assert.deepEqual(pure.pendingOwnerEchoes(echoes, [line]), [], "the record's own line replaces the echo");
  assert.equal(pure.pendingOwnerEchoes(echoes, [{ kind: "owner", at: Date.parse("2026-11-10T10:00:01.123Z") }]).length, 1, "an owner line without the record stamp retires nothing");
});

test("review r2 B1: an earlier step's open candidates_exhausted entry never closes a later park's box; the watch tail proves a park that cannot start", () => {
  const entry = (cls) => ({ id: 1, at: "2026-11-10T09:00:00Z", kind: "work_item", wi: "wi_A", class: cls, attention: true, resolved: false, dismissed: false });
  // the reviewer's probe: the chain ran out once in an earlier step, the item later parks on another kind
  for (const kind of ["write_failed", "incomplete", "some_future_kind"]) {
    assert.deepEqual(pure.conversationState({ daemonPresent: true, jobStatus: undefined, blocked: { kind, step: "collect", reason: "r" }, entries: [entry("candidates_exhausted")] }),
      BLOCKED, `${kind}: the message wakes it`);
  }
  // a driver park whose step exhausted its candidates before it parked — proven by the watch tail (same step, at or after the step's start)
  const steps = [{ name: "plan", status: "done", startedAt: "2026-11-10T08:00:00Z" }, { name: "build", status: "blocked", startedAt: "2026-11-10T10:00:00Z" }];
  const park = { kind: "driver", step: "build", reason: "no usable model: step build: a model-class death" };
  const exhausted = (step, at) => ({ at, kind: "candidates_exhausted", step });
  assert.deepEqual(pure.conversationState({ daemonPresent: true, jobStatus: undefined, blocked: park, steps, events: [exhausted("build", "2026-11-10T10:05:00Z")] }),
    closed("candidates", ["resume", "cancel"]), "the current park's step ran out of models: its agent cannot start");
  assert.deepEqual(pure.conversationState({ daemonPresent: true, jobStatus: undefined, blocked: park, steps, events: [exhausted("plan", "2026-11-10T08:30:00Z")] }),
    BLOCKED, "an earlier step's exhaustion is no proof");
  assert.deepEqual(pure.conversationState({ daemonPresent: true, jobStatus: undefined, blocked: park, steps, events: [exhausted("build", "2026-11-10T09:30:00Z")] }),
    BLOCKED, "an exhaustion before the step's current start (an earlier round) is no proof");
  assert.deepEqual(pure.conversationState({ daemonPresent: true, jobStatus: undefined, blocked: { ...park, kind: "write_failed" }, steps, events: [exhausted("build", "2026-11-10T10:05:00Z")] }),
    BLOCKED, "only the driver's park can be one");
});

test("review r2 B2: a run that completed but whose final push failed (an open runtime blocked entry) cannot start: resume · cancel", () => {
  const blocked = { id: 7, at: "t", kind: "work_item", wi: "wi_A", class: "blocked", attention: true, resolved: false, dismissed: false };
  assert.deepEqual(pure.conversationState({ daemonPresent: true, jobStatus: undefined, workflowStatus: "completed", entries: [blocked] }),
    closed("runtime", ["resume", "cancel"]), "the reviewer's probe");
  assert.deepEqual(pure.conversationState({ daemonPresent: true, jobStatus: undefined, workflowStatus: "completed", entries: [{ ...blocked, resolved: true }] }),
    closed("finished"), "a settled block is history");
  assert.deepEqual(pure.conversationState({ daemonPresent: true, jobStatus: undefined, workflowStatus: "completed" }), closed("finished"));
});

test("review r2: the runtime's transient job states read as themselves, no commands", () => {
  for (const status of ["recovering", "claiming", "sealing"]) {
    assert.deepEqual(pure.conversationState({ daemonPresent: true, jobStatus: status }), closed(status), status);
  }
});

test("the answer route's cannot-start reason is the daemon's typed refusal name", () => {
  assert.equal(pure.WAKE_CANNOT_START, "WAKE_CANNOT_START");
  assert.equal(pure.WAKE_CANNOT_START_CONTRACT, "WAKE_CANNOT_START_CONTRACT");
});

test("a cannot-start refusal offers resume and cancel, a contract one cancel alone", () => {
  assert.deepEqual(pure.cannotStartCommands("WAKE_CANNOT_START"), ["resume", "cancel"]);
  assert.deepEqual(pure.cannotStartCommands("WAKE_CANNOT_START_CONTRACT"), ["cancel"], "a resume of another contract's snapshot parks it again");
  for (const other of [undefined, "", "WAKE_PARK_CHANGED", "SOMETHING_ELSE"]) assert.equal(pure.cannotStartCommands(other), null, `${other}: an ordinary refusal`);
});
