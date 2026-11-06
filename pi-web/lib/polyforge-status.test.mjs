// carries: lib/polyforge-status.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);

async function loadSubject() {
  return jiti.import("./polyforge-status.ts");
}

const STEP_ORDER = ["zulu-grill", "mike-spec", "alpha-plan", "kilo-author", "bravo-code"];
const machineSteps = {
  "alpha-plan": { status: "done", started_at: "2026-10-18T08:10:00Z" },
  "bravo-code": { status: "pending" },
  "kilo-author": { status: "in_progress", started_at: "2026-10-18T08:15:00Z" },
  "mike-spec": { status: "done", started_at: "2026-10-18T08:05:00Z" },
  "zulu-grill": { status: "done", started_at: "2026-10-18T08:00:00Z" },
};

test("refs validate as closed positionals before any spawn", async () => {
  const { isPolyforgeWiRef } = await loadSubject();
  assert.equal(isPolyforgeWiRef("pi#108"), true);
  assert.equal(isPolyforgeWiRef("my-project#7"), true, "dashes in slugs are accepted");
  assert.equal(isPolyforgeWiRef("wi_y3y9SMz4IXKg"), true);
  assert.equal(isPolyforgeWiRef("wi_E2Eaaaa0001"), true);
  // Injection shapes must be refused: the ref is one closed argv positional.
  assert.equal(isPolyforgeWiRef("pi#108 --chat x"), false);
  assert.equal(isPolyforgeWiRef("--help"), false);
  assert.equal(isPolyforgeWiRef("wi_x;rm -rf /"), false);
  assert.equal(isPolyforgeWiRef("not a ref"), false);
  assert.equal(isPolyforgeWiRef("pi#"), false);
  assert.equal(isPolyforgeWiRef("#108"), false);
  assert.equal(isPolyforgeWiRef("pi#1#2"), false);
  assert.equal(isPolyforgeWiRef(undefined), false);
  assert.equal(isPolyforgeWiRef(null), false);
  assert.equal(isPolyforgeWiRef(108), false);
});

test("steps order in execution order: started first, pending last, ties by name", async () => {
  const { orderPolyforgeSteps } = await loadSubject();
  assert.deepEqual(orderPolyforgeSteps(machineSteps).map((step) => step.name), STEP_ORDER);
  // Started steps carry their machine statuses AND startedAt (pi#117);
  // unstarted steps carry pending and no startedAt.
  assert.deepEqual(orderPolyforgeSteps(machineSteps), [
    { name: "zulu-grill", status: "done", startedAt: "2026-10-18T08:00:00Z" },
    { name: "mike-spec", status: "done", startedAt: "2026-10-18T08:05:00Z" },
    { name: "alpha-plan", status: "done", startedAt: "2026-10-18T08:10:00Z" },
    { name: "kilo-author", status: "in_progress", startedAt: "2026-10-18T08:15:00Z" },
    { name: "bravo-code", status: "pending" },
  ]);
});

test("unstarted steps follow started ones even when tied", async () => {
  const { orderPolyforgeSteps } = await loadSubject();
  const tied = {
    "aa-late": { status: "in_progress", started_at: "2026-10-18T09:00:00Z" },
    "zz-early": { status: "in_progress", started_at: "2026-10-18T08:00:00Z" },
    "mm-unstarted": { status: "pending" },
  };
  assert.deepEqual(orderPolyforgeSteps(tied).map((step) => step.name), ["zz-early", "aa-late", "mm-unstarted"]);
  assert.deepEqual(orderPolyforgeSteps(undefined), []);
  assert.deepEqual(orderPolyforgeSteps({}), []);
});

test("event tail clamps to the newest 40, order preserved", async () => {
  const { tailPolyforgeEvents } = await loadSubject();
  const events = Array.from({ length: 60 }, (_, i) => ({
    at: `2026-10-18T10:${String(i).padStart(4, "0")}:00Z`,
    kind: "published",
    seq: i + 1,
  }));
  const tail = tailPolyforgeEvents(events);
  assert.equal(tail.length, 40, "60 machine events clamp to the newest 40");
  assert.equal(tail[39].at, events[59].at, "newest event last");
  assert.equal(tail[0].at, events[20].at, "the 40 newest, starting at index 20");
  assert.deepEqual(tailPolyforgeEvents(events, 5).length, 5);
  assert.deepEqual(tailPolyforgeEvents(events.slice(0, 3)).length, 3, "under the cap the whole event list passes");
});

test("event tail exposes only the per-kind whitelisted body fields", async () => {
  const { tailPolyforgeEvents } = await loadSubject();
  const tail = tailPolyforgeEvents([
    { at: "2026-10-18T10:00:00Z", kind: "step_started", body: { step: "grill", model: "example-gateway/model-a", spawn_n: 1, effort: "high" } },
    { at: "2026-10-18T10:00:01Z", kind: "candidate_switched", body: { step: "grill", from: "pi:old", to: "claude:new", cause: "E2E-SECRET-EVENT-BODY" } },
    { at: "2026-10-18T10:00:02Z", kind: "step_failed", body: { step: "grill", cause: "agent-exit-1", detail: "E2E-SECRET-EVENT-BODY", from_model: "E2E-SECRET-EVENT-BODY", to_model: "E2E-SECRET-EVENT-BODY" } },
    { at: "2026-10-18T10:00:03Z", kind: "step_dead", body: { step: "grill", cause: "timeout", evidence: "E2E-SECRET-EVENT-BODY" } },
    { at: "2026-10-18T10:00:04Z", kind: "rewind", body: { gate: "spec-review", target: "code", archived: ["E2E-SECRET-EVENT-BODY"], spawn_n: 3 } },
    { at: "2026-10-18T10:00:05Z", kind: "terminal", body: { status: "completed", reason: "E2E-SECRET-EVENT-BODY", spawn_n: 3 } },
    { at: "2026-10-18T10:00:06Z", kind: "job_started", body: { spawn_n: 3, detail: "E2E-SECRET-EVENT-BODY" } },
    { at: "2026-10-18T10:00:07Z", kind: "needs_input", body: { question: "  E2E-PENDING-QUESTION-TEXT which colour?\n- red\n- blue\n", question_id: "q1", step: "grill", spawn_n: 3, resumed: "E2E-SECRET-EVENT-BODY" } },
    { at: "2026-10-18T10:00:08Z", kind: "push_failed", body: { error: "E2E-SECRET-EVENT-BODY" } },
    { at: "2026-10-18T10:00:09Z", kind: "some_future_kind", body: { step: "grill", detail: "E2E-SECRET-EVENT-BODY" } },
    // workflow contract v2 (change 14): the owner conversation's kinds
    { at: "2026-10-18T10:00:10Z", kind: "awaiting_owner", body: { step: "build", spawn_n: 4, ask_at: "2026-10-18T10:00:10Z", question_id: "2026-10-18T10:00:10Z", question: " E2E-ASK-TEXT ok?\n", resumed: "E2E-SECRET-EVENT-BODY" } },
    { at: "2026-10-18T10:00:11Z", kind: "owner_message", body: { step: "build", spawn_n: 4, kind: "reply", ask_at: "2026-10-18T10:00:10Z", by: "usr_1", text: "E2E-SECRET-OWNER-WORDS" } },
    { at: "2026-10-18T10:00:12Z", kind: "delivered", body: { step: "build", spawn_n: 4, msg_at: "2026-10-18T10:00:11Z", kind: "reply", disposition: "started", confirmed_by: "E2E-SECRET-EVENT-BODY" } },
    { at: "2026-10-18T10:00:13Z", kind: "intent", body: { step: "review", spawn_n: 4, kind: "rewind", to: "build", reply_at: "2026-10-18T10:00:11Z", reason: "E2E-SECRET-EVENT-BODY", by: "usr_1" } },
  ]);
  const byKind = Object.fromEntries(tail.map((event) => [event.kind, event]));
  assert.equal(byKind.step_started.step, "grill");
  assert.equal(byKind.step_started.model, "example-gateway/model-a", "harness prefix kept, model verbatim");
  assert.equal(byKind.candidate_switched.step, "grill", "step passes through for candidate_switched");
  assert.equal(byKind.candidate_switched.from, "pi:old");
  assert.equal(byKind.candidate_switched.to, "claude:new");
  assert.equal(byKind.candidate_switched.cause, undefined, "cause is not whitelisted for candidate_switched");
  assert.equal(byKind.step_failed.cause, "agent-exit-1");
  assert.equal(byKind.step_dead.cause, "timeout");
  assert.equal(byKind.rewind.gate, "spec-review");
  assert.equal(byKind.rewind.target, "code");
  assert.equal(byKind.terminal.status, "completed");
  assert.equal(byKind.job_started.step, undefined, "body.spawn_n no longer renders as step");
  // (owner decision 2026-11-09): needs_input carries its
  // question verbatim (not trimmed) and its id — and nothing else of the body.
  assert.deepEqual(byKind.needs_input, {
    at: "2026-10-18T10:00:07Z", kind: "needs_input", step: "grill",
    question: "  E2E-PENDING-QUESTION-TEXT which colour?\n- red\n- blue\n", question_id: "q1",
  });
  assert.equal(byKind.push_failed.step, undefined, "push_failed body has no step field");
  assert.equal(byKind.some_future_kind.step, "grill", "unknown kinds stay step-only");
  assert.deepEqual(byKind.awaiting_owner, { at: "2026-10-18T10:00:10Z", kind: "awaiting_owner", step: "build", question: " E2E-ASK-TEXT ok?\n", question_id: "2026-10-18T10:00:10Z" }, "an ask carries its question verbatim and its stamp as the id");
  assert.deepEqual(byKind.owner_message, { at: "2026-10-18T10:00:11Z", kind: "owner_message", step: "build", by: "usr_1" }, "the owner's words ride the progress stream, never the watch tail");
  assert.deepEqual(byKind.delivered, { at: "2026-10-18T10:00:12Z", kind: "delivered", step: "build", disposition: "started" });
  assert.deepEqual(byKind.intent, { at: "2026-10-18T10:00:13Z", kind: "intent", step: "review", intent: "rewind", to: "build" }, "the intent's kind rides as `intent`: the event kind stays");
  const text = JSON.stringify(tail.filter((event) => event.kind !== "needs_input" && event.kind !== "awaiting_owner"));
  assert.ok(!text.includes("E2E-SECRET-OWNER-WORDS"), "an owner message's text never reaches the tail");
  assert.ok(!JSON.stringify(byKind.needs_input).includes("E2E-SECRET-EVENT-BODY"), "needs_input passes no other body field");
  for (const secret of ["E2E-SECRET-EVENT-BODY", "E2E-PENDING-QUESTION-TEXT", "m1", "q1", "spawn 3", "high"]) {
    assert.ok(!text.includes(secret), `non-whitelisted body value reached the shape: ${secret}`);
  }
});

test("event tail stays clean when optional whitelisted fields are absent", async () => {
  const { tailPolyforgeEvents } = await loadSubject();
  const tail = tailPolyforgeEvents([
    { at: "2026-10-18T10:00:00Z", kind: "step_started", body: { step: "grill" } },
    { at: "2026-10-18T10:00:01Z", kind: "terminal", body: { reason: "E2E-SECRET-EVENT-BODY" } },
  ]);
  assert.deepEqual(tail[0], { at: "2026-10-18T10:00:00Z", kind: "step_started", step: "grill" });
  assert.deepEqual(tail[1], { at: "2026-10-18T10:00:01Z", kind: "terminal" });
});

test("ops snapshot keeps queue/jobs fields and drops machine passthrough", async () => {
  const { sanitizeOpsStatus } = await loadSubject();
  const machine = {
    machine_id: "m-e2e",
    workspaces: ["/srv/e2e-secret-workspace"],
    workflows_dir: "/srv/e2e-secret-workspace/wf",
    env_names: ["POLYFORGE_V1_API_KEY", "E2E_ENV_NAME_SENTINEL"],
    shutting_down: false,
    jobs: [{
      work_item_id: "wi_E2Eaaaa0001",
      slug: "pi#108",
      status: "running",
      spawn_n: 3,
      session_fingerprint: "fp-secret-session",
      step_age_sec: 7325,
      last_work_ago_sec: 4337,
      candidate: "claude:claude-opus-5-5-extended-context-e2e",
      origin: { kind: "chat", owner: "u_alice" },
    }],
    queue: { pending: 0, running: 2, max_concurrent: 4 },
  };
  const snapshot = sanitizeOpsStatus(machine);
  assert.ok(snapshot);
  assert.deepEqual(snapshot.queue, { running: 2, max: 4 });
  assert.deepEqual(snapshot.jobs, [{
    wi: "wi_E2Eaaaa0001",
    slug: "pi#108",
    status: "running",
    stepAgeSec: 7325,
    idleAgeSec: 4337,
    candidate: "claude:claude-opus-5-5-extended-context-e2e",
    spawnN: 3,
  }]);
  const text = JSON.stringify(snapshot);
  for (const secret of ["E2E_ENV_NAME_SENTINEL", "/srv/e2e-secret-workspace", "fp-secret-session", "u_alice", "m-e2e"]) {
    assert.ok(!text.includes(secret), `machine passthrough leaked: ${secret}`);
  }
});

test("ops snapshot forwards exactly tasks.running/completed", async () => {
  const { sanitizeOpsStatus } = await loadSubject();
  const base = { queue: { running: 3, max_concurrent: 6 }, jobs: [] };
  const snapshot = sanitizeOpsStatus({ ...base, tasks: { running: 2, completed: 5, capped: 1, failed: 4, interrupted: 7, limit: 9 } });
  assert.deepEqual(snapshot.tasks, { running: 2, completed: 5 }, "only the two counts the badge and the re-read key need");
  assert.deepEqual(snapshot.queue, { running: 3, max: 6 });
  assert.deepEqual(snapshot.jobs, []);
  for (const malformed of [undefined, null, "3", [], { running: "2", completed: Number.NaN }]) {
    const degraded = sanitizeOpsStatus({ ...base, tasks: malformed });
    assert.ok(degraded, "a malformed tasks object never nulls the snapshot");
    assert.deepEqual(degraded.tasks, { running: 0, completed: 0 }, `tasks ${JSON.stringify(malformed)} reads as zeros`);
    assert.deepEqual(degraded.queue, { running: 3, max: 6 }, "queue is untouched");
  }
});

test("ops snapshot forwards a job's verify_queue as verifyQueue; malformed or absent stays absent", async () => {
  const { sanitizeOpsStatus } = await loadSubject();
  const row = (wi, verifyQueue) => ({ work_item_id: wi, slug: `x#${wi}`, status: "running", ...(verifyQueue === undefined ? {} : { verify_queue: verifyQueue }) });
  const reason = "heavy-run slots full (2/2 running)";
  const snapshot = sanitizeOpsStatus({
    // queue.running keeps its meaning: it leaves the queued verifications out.
    queue: { running: 1, max_concurrent: 16 },
    jobs: [
      row("wi_Q1", { position: 1, reason, since: "2026-11-08T03:30:00Z", extra: "dropped" }),
      row("wi_Q2", { position: 2, reason: `  ${reason}  `, since: "2026-11-08T03:31:00Z" }),
      row("wi_R1"),
      row("wi_M1", { position: 0, reason, since: "2026-11-08T03:30:00Z" }),
      row("wi_M2", { position: 1.5, reason, since: "2026-11-08T03:30:00Z" }),
      row("wi_M3", { position: 3, reason: "", since: "2026-11-08T03:30:00Z" }),
      row("wi_M4", { position: "3", reason, since: "2026-11-08T03:30:00Z" }),
      row("wi_M5", null),
      row("wi_M6", "queued #2"),
      row("wi_K1", { position: 4, reason: "auth E2E key sk-EXAMPLE-reason", since: 7 }),
    ],
  });
  assert.ok(snapshot);
  assert.deepEqual(snapshot.queue, { running: 1, max: 16 }, "queue.running is untouched");
  const byWi = new Map(snapshot.jobs.map((job) => [job.wi, job]));
  assert.deepEqual(byWi.get("wi_Q1").verifyQueue, { position: 1, reason, since: "2026-11-08T03:30:00Z" }, "position, reason and since pass; nothing else");
  assert.deepEqual(byWi.get("wi_Q2").verifyQueue, { position: 2, reason, since: "2026-11-08T03:31:00Z" }, "the reason is trimmed");
  for (const wi of ["wi_R1", "wi_M1", "wi_M2", "wi_M3", "wi_M4", "wi_M5", "wi_M6"]) {
    assert.ok(byWi.has(wi), `${wi} stays a job row`);
    assert.equal("verifyQueue" in byWi.get(wi), false, `${wi}: an absent or malformed verify_queue leaves no verifyQueue key`);
  }
  const masked = byWi.get("wi_K1").verifyQueue;
  assert.equal(masked.position, 4);
  assert.ok(!masked.reason.includes("sk-EXAMPLE-reason"), "the reason passes the credential mask");
  assert.equal("since" in masked, false, "a non-string since is dropped, the queue mark stays");
});

test("ops snapshot is null on unusable machine shapes", async () => {
  const { sanitizeOpsStatus } = await loadSubject();
  assert.equal(sanitizeOpsStatus(null), null);
  assert.equal(sanitizeOpsStatus(undefined), null);
  assert.equal(sanitizeOpsStatus("pf: daemon not reachable"), null);
  assert.equal(sanitizeOpsStatus({ queue: { running: 1, max_concurrent: 2 } }), null);
  assert.equal(sanitizeOpsStatus({ queue: { running: 1, max_concurrent: 2 }, jobs: "nope" }), null);
  assert.equal(sanitizeOpsStatus({ jobs: [] }), null);
});

test("watch snapshot keeps header fields, ordered steps and tail events; unknown refs read null", async () => {
  const { sanitizeWatch } = await loadSubject();
  const machine = {
    work_item: "wi_E2Eaaaa0001",
    slug: "pi#108",
    known: true,
    // Non-chat origin (no kind): the whole origin degrades to absent (pi#117).
    job: { status: "running", origin: { owner: "u_alice" } },
    execution: {
      workflow_status: "running",
      current_step: "kilo-author",
      updated_at: "2026-10-18T11:00:00Z",
      steps: machineSteps,
    },
    // pi#117: progress.command leaves only masked (the fixture command carries
    // a credential pattern so the sentinel value can never survive).
    progress: { command: "worker run --token=E2E-SECRET-WORKER-COMMAND-0123456789 --flag", tool_calls: 9 },
    events: Array.from({ length: 60 }, (_, i) => ({
      at: `2026-10-18T10:${String(i).padStart(4, "0")}:00Z`,
      kind: i % 2 ? "published" : "step_started",
      work_item: "wi_E2Eaaaa0001",
      body: { step: "kilo-author", spawn_n: 3, detail: `E2E-SECRET-EVENT-BODY-${i + 1}` },
      seq: i + 1,
    })),
    last_seq: 60,
  };
  const snapshot = sanitizeWatch(machine);
  assert.ok(snapshot);
  assert.equal(snapshot.wi, "wi_E2Eaaaa0001");
  assert.equal(snapshot.slug, "pi#108");
  assert.equal(snapshot.workflowStatus, "running");
  assert.equal(snapshot.currentStep, "kilo-author");
  assert.deepEqual(snapshot.steps.map((step) => step.name), STEP_ORDER, "steps in execution order");
  assert.equal(snapshot.events.length, 40, "event tail clamped to 40");
  assert.equal(snapshot.origin, undefined, "a non-chat origin never leaves");
  const text = JSON.stringify(snapshot);
  for (const secret of ["E2E-SECRET-EVENT-BODY", "E2E-SECRET-WORKER-COMMAND", "u_alice"]) {
    assert.ok(!text.includes(secret), `machine passthrough leaked: ${secret}`);
  }
});

test("redaction masks every credential pattern and truncates to ~120 chars", async () => {
  const { redactPolyforgeCommand } = await loadSubject();
  const command = [
    "run",
    "sk-E2ESECRETKEY0123456789",
    "ghp_E2ESECRETKEY0123456789abc",
    "npm_E2ESECRET0123456789",
    "FOO_API_KEY=E2E-SECRET-VALUE",
    "token=E2E-SECRET-TOKEN",
    "password=E2E-SECRET-PW1",
    "passwd=E2E-SECRET-PW2",
    "--password",
    "E2E-SECRET-PW3",
    "plain",
  ].join(" ");
  const redacted = redactPolyforgeCommand(command);
  for (const secret of ["E2ESECRETKEY0123456789", "E2E-SECRET-VALUE", "E2E-SECRET-TOKEN", "E2E-SECRET-PW"]) {
    assert.ok(!redacted.includes(secret), `secret value survived redaction: ${secret}`);
  }
  assert.equal(
    redacted,
    "run sk-*** ghp_*** npm_*** FOO_API_KEY=*** token=*** password=*** passwd=*** --password *** plain",
  );
  // The flag (=) form masks through the assignment rule; a clean command
  // passes verbatim (the program+args shape is the point, not obfuscation).
  assert.equal(redactPolyforgeCommand("--token=abc --password=def"), "--token=*** --password=***");
  assert.equal(redactPolyforgeCommand("npm install --no-save"), "npm install --no-save");
  // Truncation to ~120 chars applies after masking.
  assert.equal(redactPolyforgeCommand("x".repeat(200)).length, 120);
  assert.equal(redactPolyforgeCommand(`sk-${"y".repeat(300)}`), "sk-***");
});

test("the extracted credential masker masks without truncating, and the command passport is mask + 120", async () => {
  const { maskPolyforgeCredentials, redactPolyforgeCommand } = await loadSubject();
  const long = `printf --token=E2E-SECRET ${"z".repeat(300)} sk-E2ESECRETKEY0123`;
  const masked = maskPolyforgeCredentials(long);
  assert.ok(!masked.includes("E2E-SECRET") && !masked.includes("E2ESECRETKEY"), "every credential shape is masked");
  assert.ok(masked.length > 300, "the masker itself never truncates");
  assert.ok(masked.endsWith("sk-***"));
  assert.equal(redactPolyforgeCommand(long), masked.slice(0, 120), "the command passport is the masker plus the 120 cap");
});

test("progress passes only the whitelisted fields; malformed fields degrade to absent", async () => {
  const { sanitizeWatch } = await loadSubject();
  const base = {
    work_item: "wi_E2Eaaaa0001",
    slug: "pi#108",
    known: true,
    execution: { workflow_status: "running", steps: machineSteps },
    events: [],
  };
  const withProgress = (progress) => sanitizeWatch({ ...base, progress });
  const full = withProgress({
    command: "cmd --token=E2E-SECRET-PROGRESS-TOKEN",
    command_age_sec: 12,
    tool_calls: 3,
    recent_files: ["artifacts/plan.md", "repos/pi-web/lib/x.ts"],
    extra_field: "E2E-SECRET-PROGRESS",
  });
  assert.deepEqual(full?.progress, {
    command: "cmd --token=***",
    command_age_sec: 12,
    tool_calls: 3,
    recent_files: ["artifacts/plan.md", "repos/pi-web/lib/x.ts"],
  }, "only the whitelisted progress fields leave, command masked");
  assert.ok(!JSON.stringify(full).includes("E2E-SECRET-PROGRESS"), "non-whitelisted progress field leaked");
  // Every field is absent when missing or malformed on the wire.
  assert.equal(withProgress(undefined)?.progress, undefined, "absent progress → absent");
  assert.equal(withProgress("banana")?.progress, undefined, "non-object progress → absent");
  assert.equal(withProgress({})?.progress, undefined, "no valid field → the whole field degrades to absent");
  assert.deepEqual(withProgress({ command: 42 })?.progress, undefined, "non-string command → absent");
  assert.deepEqual(
    withProgress({ command: "ok", command_age_sec: "12", tool_calls: "3", recent_files: "nope" })?.progress,
    { command: "ok" },
    "malformed numerics/files degrade field-by-field, never an error",
  );
  assert.deepEqual(
    withProgress({ recent_files: ["a.md", 42] })?.progress,
    undefined,
    "recent_files that is not an array of strings degrades to absent",
  );
  assert.deepEqual(
    withProgress({ tool_calls: 7, recent_files: ["a.md", null] })?.progress,
    { tool_calls: 7 },
    "a non-string file entry drops recent_files only",
  );
});

test("origin exposes chat kind and owner only; other kinds and fields (a wire chat_id included,) never leave", async () => {
  const { sanitizeWatch } = await loadSubject();
  const base = {
    work_item: "wi_E2Eaaaa0001",
    slug: "pi#108",
    known: true,
    execution: { workflow_status: "running", steps: machineSteps },
    events: [],
  };
  const withOrigin = (origin) => sanitizeWatch({ ...base, job: { status: "running", origin } });
  assert.deepEqual(
    withOrigin({ kind: "chat", chat_id: "chat-e2e-0001", owner: "u_alice", extra: "E2E-SECRET-ORIGIN" })?.origin,
    { kind: "chat", owner: "u_alice" },
    "chat origins expose kind/owner only — a wire chat_id is dropped",
  );
  assert.ok(!JSON.stringify(withOrigin({ kind: "chat", chat_id: "chat-e2e-0001", owner: "o" })).includes("chat-e2e-0001"), "no chat id leaves");
  assert.ok(!JSON.stringify(withOrigin({ kind: "chat", chat_id: "c", owner: "o", extra: "E2E-SECRET-ORIGIN" })).includes("E2E-SECRET-ORIGIN"));
  // Every other origin field (and any other kind) degrades the whole origin to absent.
  for (const origin of [
    { kind: "manual", chat_id: "chat-e2e-0001", owner: "u_alice" },
    { chat_id: "chat-e2e-0001", owner: "u_alice" },
    "banana",
    null,
  ]) {
    assert.equal(withOrigin(origin)?.origin, undefined, `origin ${JSON.stringify(origin)} must degrade to absent`);
    assert.ok(!JSON.stringify(withOrigin(origin)).includes("u_alice"), `origin ${JSON.stringify(origin)} leaked a field`);
  }
  // kind chat without an owner still exposes kind (field-level absence).
  assert.deepEqual(withOrigin({ kind: "chat" })?.origin, { kind: "chat" });
  // kind chat with a malformed owner degrades field-by-field, never an error.
  assert.deepEqual(withOrigin({ kind: "chat", owner: 42 })?.origin, { kind: "chat" });
  // A missing job or origin is absent too, and the snapshot survives.
  assert.equal(sanitizeWatch({ ...base })?.origin, undefined, "no job → no origin");
  assert.equal(sanitizeWatch({ ...base, job: { status: "running" } })?.origin, undefined, "no origin → no origin");
});

test("summary fields pass verbatim through the whitelist; malformed fields degrade to absent (pi#119)", async () => {
  const { sanitizeWatch } = await loadSubject();
  const base = {
    work_item: "wi_E2Eaaaa0001",
    slug: "pi#108",
    known: true,
    execution: { workflow_status: "running", steps: machineSteps },
    events: [],
  };
  const withSummary = (fields) => sanitizeWatch({ ...base, ...fields });
  // Full pass: the line verbatim (trimmed), its age, the raw error verbatim
  // (no masking), the cap marker true — and nothing else.
  const full = withSummary({
    summary: "  The plan step finished; tests pass.  ",
    summary_age_sec: 61,
    summary_error: "summarize call failed: provider 429",
    summary_capped: true,
    summary_extra: "E2E-SECRET-SUMMARY",
  });
  assert.ok(full);
  assert.equal(full.summary, "The plan step finished; tests pass.", "summary passes verbatim, trimmed");
  assert.equal(full.summary_age_sec, 61, "age kept together with summary");
  assert.equal(full.summary_error, "summarize call failed: provider 429", "summary_error passes verbatim, no masking");
  assert.equal(full.summary_capped, true, "capped kept only as true");
  assert.ok(!JSON.stringify(full).includes("E2E-SECRET-SUMMARY"), "a non-whitelisted summary-adjacent field leaked");
  // summary_error caps at 512 chars server-side; summary has NO server-side
  // cap (the lane's own bound is 200) — a longer line passes verbatim.
  assert.equal(withSummary({ summary_error: "x".repeat(600) }).summary_error.length, 512, "summary_error must cap at 512");
  assert.equal(withSummary({ summary: "s".repeat(250) }).summary.length, 250, "summary passes verbatim with no server-side cap");
  // summary_age_sec is kept only together with summary.
  assert.equal(withSummary({ summary_age_sec: 61 }).summary_age_sec, undefined, "age without summary is dropped");
  assert.equal(withSummary({ summary: "ok", summary_age_sec: "61" }).summary_age_sec, undefined, "malformed age degrades to absent, never an error");
  // Each field is absent when missing or malformed.
  assert.equal(withSummary({}).summary, undefined, "no summary on the wire → absent");
  assert.equal(withSummary({ summary: "   " }).summary, undefined, "blank summary → absent");
  assert.equal(withSummary({ summary: 42 }).summary, undefined, "non-string summary → absent");
  assert.equal(withSummary({ summary_error: "" }).summary_error, undefined, "blank error → absent");
  assert.equal(withSummary({ summary_error: 42 }).summary_error, undefined, "non-string error → absent");
  // summary_capped survives only when the wire value is exactly true.
  for (const value of [false, "true", 1, null]) {
    assert.equal(withSummary({ summary_capped: value }).summary_capped, undefined, `summary_capped ${JSON.stringify(value)} must degrade to absent`);
  }
  // An error may ride together with an older summary (both kept — the
  // error line may show next to an older summary).
  const both = withSummary({ summary: "older line", summary_age_sec: 5, summary_error: "boom" });
  assert.equal(both.summary, "older line");
  assert.equal(both.summary_age_sec, 5);
  assert.equal(both.summary_error, "boom");
  // The four fields disturb nothing else in the snapshot.
  assert.deepEqual(both.steps.map((step) => step.name), STEP_ORDER, "steps unaffected by the summary fields");
});

test("declared_steps sanitize to ordered ids only; on_fail and extras never leak", async () => {
  const { sanitizeDeclaredSteps } = await loadSubject();
  const raw = [
    { id: " grill ", on_fail: "spec", path: "/srv/e2e-secret-workspace" },
    { id: "spec", on_fail: null },
    { id: "code" },
  ];
  assert.deepEqual(sanitizeDeclaredSteps(raw), [{ id: "grill" }, { id: "spec" }, { id: "code" }], "order preserved, ids trimmed");
  const text = JSON.stringify(sanitizeDeclaredSteps(raw));
  for (const leak of ["on_fail", "path", "/srv/e2e-secret-workspace"]) {
    assert.ok(!text.includes(leak), `declared step leaked ${leak}`);
  }
  // Absent / null / non-array / empty / entries without ids → null.
  assert.equal(sanitizeDeclaredSteps(undefined), null);
  assert.equal(sanitizeDeclaredSteps(null), null);
  assert.equal(sanitizeDeclaredSteps({ grill: "x" }), null);
  assert.equal(sanitizeDeclaredSteps("nope"), null);
  assert.equal(sanitizeDeclaredSteps([]), null);
  assert.equal(sanitizeDeclaredSteps([{ on_fail: "spec" }, { name: "plan" }]), null, "entries without ids → null");
  assert.equal(sanitizeDeclaredSteps([{ id: "grill" }, { id: "  " }]), null, "blank id → null");
  assert.equal(sanitizeDeclaredSteps([{ id: 42 }]), null, "non-string id → null");
  assert.equal(sanitizeDeclaredSteps([null]), null, "null entry → null");
});

test("malformed declared_steps degrades to null without disturbing steps/events", async () => {
  const { sanitizeWatch } = await loadSubject();
  const base = {
    work_item: "wi_E2Eaaaa0001",
    slug: "pi#108",
    known: true,
    execution: {
      workflow_status: "running",
      steps: machineSteps,
    },
    events: [{ at: "2026-10-18T10:00:00Z", kind: "step_started", body: { step: "grill" } }],
  };
  for (const declared of [undefined, null, { grill: "x" }, [], [{ on_fail: "spec" }]]) {
    const snapshot = sanitizeWatch({ ...base, execution: { ...base.execution, declared_steps: declared } });
    assert.ok(snapshot, `declared_steps ${JSON.stringify(declared)} must not nuke the snapshot`);
    assert.equal(snapshot.declared_steps, null, `declared_steps ${JSON.stringify(declared)} → null`);
    assert.deepEqual(snapshot.steps.map((step) => step.name), STEP_ORDER, "steps unaffected by malformed declared_steps");
    assert.deepEqual(snapshot.events, [{ at: "2026-10-18T10:00:00Z", kind: "step_started", step: "grill" }], "events unaffected");
  }
  const ok = sanitizeWatch({ ...base, execution: { ...base.execution, declared_steps: [{ id: "grill" }, { id: "code", on_fail: "plan" }] } });
  assert.deepEqual(ok?.declared_steps, [{ id: "grill" }, { id: "code" }], "well-formed declared_steps pass with ids only");
});

test("watch snapshot reads null for daemon-unknown refs and unusable shapes", async () => {
  const { sanitizeWatch } = await loadSubject();
  assert.equal(sanitizeWatch({ known: false, work_item: "wi_x", events: [], last_seq: 0 }), null);
  assert.equal(sanitizeWatch(null), null);
  assert.equal(sanitizeWatch("garbage"), null);
  assert.equal(sanitizeWatch({ known: true, execution: null }), null);
  assert.equal(sanitizeWatch({ known: true, execution: { steps: {} } }), null, "no wi/slug");
  assert.equal(sanitizeWatch({
    known: true, wi: "wi_E2Eaaaa0001", slug: "pi#108",
    execution: { steps: {} },
  }), null, "a stub-only `wi` field is not the daemon's work_item shape");
  assert.ok(sanitizeWatch({
    known: true, work_item: "wi_E2Eaaaa0001", slug: "pi#108",
    execution: { steps: {} },
  }), "the daemon's work_item field is accepted");
});

test("watch transport validates the ref before argv and builds the once/demand/40 argv", async () => {
  const source = await (await import("node:fs/promises")).readFile(new URL("./polyforge-status.ts", import.meta.url), "utf8");
  assert.match(source, /isPolyforgeWiRef\(wiRef\)/, "the ref is validated before any spawn");
  assert.match(source, /"--once"/, "the resident watcher must never be started: --once is mandatory");
  // pi#119: every watch read arms the summarize lane — --demand on every
  // call, always, with no client parameter (the open panel's 3s poll is the
  // route's only in-app caller) and no fallback for a pf without the flag.
  assert.match(source, /"--once", "--demand", "--events", String\(EVENT_TAIL\)/, "the watch argv must arm the summarize lane (--demand) on every read");
  assert.match(source, /runPfVersion\(PRESENCE_PROBE_TIMEOUT_MS\)/, "the presence probe outlives its own 3s timeout");
  assert.match(source, /timeout: READ_TIMEOUT_MS/, "reads outlive their own 10s timeout");
});

// ---------------------------------------------------------------------------
// pi#122: the presence cache carries the version; probePolyforgeVersion
// shares the one `pf --version` spawn + cache (60s present, 5s absent,
//), force bypasses the read.
// ---------------------------------------------------------------------------

test("version parse keeps a semver-ish line verbatim and reads anything else as absent", async () => {
  const { parsePolyforgeVersion } = await loadSubject();
  assert.equal(parsePolyforgeVersion("v2.19.1\n"), "v2.19.1", "the reported version passes verbatim");
  assert.equal(parsePolyforgeVersion("2.19.1"), "2.19.1", "the leading v is optional");
  assert.equal(parsePolyforgeVersion("v2.17.0-e2e\n"), "v2.17.0-e2e", "a prerelease suffix passes verbatim");
  assert.equal(parsePolyforgeVersion("v2.19.1+build.7"), "v2.19.1+build.7");
  for (const stdout of ["", "   \n", "not a version", "2.19", "polyforge 2.19.1", "v2.19.1\nsecond line", "42"]) {
    assert.equal(parsePolyforgeVersion(stdout), null, `unparseable stdout ${JSON.stringify(stdout)} → version absent`);
  }
});

test("the presence cache carries the version and force bypasses the cache read", async () => {
  const source = await (await import("node:fs/promises")).readFile(new URL("./polyforge-status.ts", import.meta.url), "utf8");
  // One cache, one spawn: probePolyforgePresent keeps its boolean contract by
  // delegating to probePolyforgeVersion (no second detection implementation).
  assert.match(source, /let presenceCache: \{ present: boolean; version: string \| null; expiresAt: number \}/);
  assert.match(source, /export async function probePolyforgeVersion\(force = false\)/);
  assert.match(source, /if \(!force && presenceCache && now < presenceCache\.expiresAt\)/, "force bypasses the cache read");
  assert.match(source, /return \(await probePolyforgeVersion\(\)\)\.present;/, "probePolyforgePresent delegates to the same spawn");
  assert.match(source, /version = parsePolyforgeVersion\(stdout\)/, "the probe parses the version out of the same stdout");
  assert.ok(
    source.indexOf("export async function probePolyforgePresent") < source.indexOf("export async function probePolyforgeVersion"),
    "probePolyforgePresent delegates; it does not re-implement the spawn",
  );
});

// ---- shared ops status reader ----

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

test("the ops status reader shares one in-flight read between concurrent callers", async () => {
  const { createOpsStatusReader, OPS_STATUS_REUSE_MS } = await loadSubject();
  assert.equal(OPS_STATUS_REUSE_MS, 1000);
  let reads = 0;
  const pending = deferred();
  const reader = createOpsStatusReader(() => { reads += 1; return pending.promise; }, () => 0);
  const first = reader();
  const second = reader();
  assert.equal(reads, 1, "the second caller joins the in-flight read");
  pending.resolve({ jobs: ["a"] });
  assert.deepEqual(await first, { jobs: ["a"] });
  assert.equal(await second, await first, "both callers get the same snapshot");
});

test("a completed ops status read is reused inside the window and re-read after it", async () => {
  const { createOpsStatusReader } = await loadSubject();
  let clock = 10_000;
  let reads = 0;
  const reader = createOpsStatusReader(async () => { reads += 1; return { n: reads }; }, () => clock, 1000);
  assert.deepEqual(await reader(), { n: 1 });
  clock += 999;
  assert.deepEqual(await reader(), { n: 1 }, "a call inside the window reuses the snapshot");
  assert.equal(reads, 1);
  clock += 1;
  assert.deepEqual(await reader(), { n: 2 }, "a call at the window's end reads again");
  assert.equal(reads, 2);
});

test("a failed ops status read is not reused", async () => {
  const { createOpsStatusReader } = await loadSubject();
  let reads = 0;
  const reader = createOpsStatusReader(async () => {
    reads += 1;
    if (reads === 1) throw new Error("daemon down");
    return { ok: true };
  }, () => 0);
  await assert.rejects(reader(), /daemon down/);
  assert.deepEqual(await reader(), { ok: true }, "the next call retries instead of replaying the failure");
  assert.equal(reads, 2);
});

test("a Host-aggregate job row keeps its Cell; a malformed profile is dropped", async () => {
  const { sanitizeOpsStatus } = await loadSubject();
  const snapshot = sanitizeOpsStatus({
    jobs: [
      { work_item_id: "wi_A", slug: "x#1", status: "running", profile: "alpha" },
      { work_item_id: "wi_A", slug: "x#1", status: "running", profile: "default" },
      { work_item_id: "wi_B", slug: "x#2", status: "running", profile: "Bad Name" },
      { work_item_id: "wi_C", slug: "x#3", status: "running" },
    ],
    queue: { running: 3, max_concurrent: 4 },
  });
  assert.deepEqual(snapshot.jobs.map((job) => job.profile), ["alpha", "default", undefined, undefined],
    "equal wi literals on two Cells stay distinguishable by profile");
});

test("workflow contract v2 (change 14): pending_question passes as pendingQuestion — id, step, the question verbatim, asked_at; the asker never leaves; absent stays absent", async () => {
  const { sanitizeWatch } = await loadSubject();
  const base = { work_item: "wi_E2Eaaaa0001", slug: "pi#108", known: true, events: [] };
  const plain = sanitizeWatch({ ...base, execution: { workflow_status: "running", steps: machineSteps } });
  assert.equal(plain.pendingQuestion, undefined, "nothing asked: no field");
  assert.equal("askedBy" in plain, false, "the asker is no longer projected (the discussion assistant that read it is gone)");
  const ask = "2026-11-10T10:00:00.123456Z";
  const asked = sanitizeWatch({ ...base, execution: { workflow_status: "running", steps: machineSteps,
    pending_question: { id: ask, step: "build", question: "  Which colour?\n- amber\n", asked_at: ask, asked_by: { spawn_n: 3, definition: "verify-judge", harness: "claude",
      harness_session: "E2E-SECRET-SESSION", transcript: "/secret/E2E-SECRET-PATH.jsonl" } } } });
  assert.deepEqual(asked.pendingQuestion, { id: ask, step: "build", question: "  Which colour?\n- amber\n", askedAt: ask }, "an ask: id and asked_at are its stamp, the question untrimmed");
  const text = JSON.stringify(asked);
  for (const secret of ["E2E-SECRET-SESSION", "E2E-SECRET-PATH", "verify-judge", "spawn_n"]) assert.ok(!text.includes(secret), `${secret} never leaves`);
  const v1 = sanitizeWatch({ ...base, execution: { workflow_status: "needs_input", steps: machineSteps, pending_question: { id: "q1", question: "Q?" } } });
  assert.deepEqual(v1.pendingQuestion, { id: "q1", question: "Q?" }, "a contract-1 question by its id");
  for (const bad of [{ id: "q1" }, { question: "Q?" }, { id: "bad id; rm", question: "Q?" }, { id: "q1", question: "   " }, "banana"]) {
    const snap = sanitizeWatch({ ...base, execution: { workflow_status: "running", steps: machineSteps, pending_question: bad } });
    assert.equal(snap.pendingQuestion, undefined, `${JSON.stringify(bad)} is no question`);
  }
});

test("a blocked workflow's cause reaches the panel read-only — kind, step, reason, required action; never evidence or the log tail", async () => {
  const { sanitizeWatch, sanitizeBlocked } = await loadSubject();
  const machine = { known: true, work_item: "wi_E2Ebbbb0002", slug: "demo#2",
    execution: { workflow_status: "blocked", current_step: "verification", steps: {}, blocked: {
      kind: "incomplete", step: "verification", reason: "docker is not running token=SECRET-BLOCKED-TOKEN", required_action: "start the docker engine, then pf resume",
      evidence: "E2E-SECRET-EVIDENCE", log_tail: "E2E-SECRET-LOG-TAIL" } } };
  const status = sanitizeWatch(machine);
  assert.deepEqual(status.blocked, { kind: "incomplete", step: "verification", reason: "docker is not running token=***", requiredAction: "start the docker engine, then pf resume" });
  const text = JSON.stringify(status);
  for (const secret of ["SECRET-BLOCKED-TOKEN", "E2E-SECRET-EVIDENCE", "E2E-SECRET-LOG-TAIL"]) assert.ok(!text.includes(secret), secret);
  assert.equal(sanitizeBlocked({ kind: "incomplete" }), undefined, "no reason, no face");
  assert.equal(sanitizeWatch({ ...machine, execution: { ...machine.execution, blocked: undefined } }).blocked, undefined);
});

test("PR #860 review minor 6: each pf write's docblock sits on its own function", async () => {
  const source = await (await import("node:fs/promises")).readFile(new URL("./polyforge-status.ts", import.meta.url), "utf8");
  assert.match(source, /\/\*\* The owner's message to the work item's current step agent[\s\S]*?\*\/\nexport async function sendPolyforgeOwnerMessage\(/);
});

test("workflow contract v2 change 19: a pending question's options never leave (the v1 option set is gone)", async () => {
  const { sanitizeWatch } = await loadSubject();
  const base = { work_item: "wi_E2Eaaaa0001", slug: "pi#108", known: true, events: [] };
  const snap = sanitizeWatch({ ...base, execution: { workflow_status: "needs_input", steps: machineSteps, pending_question: { id: "q1", step: "build", question: "Q?", options: [
    { id: "revert", label: "E2E-SECRET-OPTION", action: "revert" },
  ] } } });
  assert.deepEqual(snap.pendingQuestion, { id: "q1", step: "build", question: "Q?" });
  assert.equal(snap.questionOptions, undefined);
  assert.ok(!JSON.stringify(snap).includes("E2E-SECRET-OPTION"));
});

test("workflow contract v2 (change 14): sendPolyforgeOwnerMessage runs `pf --json answer <wi> --text=…` with no question id and answers the daemon's ack; no text is refused before any spawn", async (t) => {
  const { sendPolyforgeOwnerMessage, parseOwnerMessageAckWire, PolyforgeError } = await loadSubject();
  const { mkdtempSync, writeFileSync, chmodSync, readFileSync, rmSync } = await import("node:fs");
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");
  const work = mkdtempSync(join(tmpdir(), "pf-owner-message-"));
  const previous = process.env.PF_BIN;
  t.after(() => { if (previous === undefined) delete process.env.PF_BIN; else process.env.PF_BIN = previous; rmSync(work, { force: true, recursive: true }); });
  const log = join(work, "argv.log");
  writeFileSync(log, "");
  writeFileSync(join(work, "pf"), `#!${process.execPath}\nrequire("fs").appendFileSync(${JSON.stringify(log)}, JSON.stringify(process.argv.slice(2)) + "\\n");\nprocess.stdout.write(JSON.stringify({ status: "accepted", at: "2026-11-10T10:00:01Z", kind: "message", step: "build", spawn_n: 2, delivered: false, disposition: "queued", note: "busy token=SECRET" }));\n`);
  chmodSync(join(work, "pf"), 0o755);
  process.env.PF_BIN = join(work, "pf");
  const ack = await sendPolyforgeOwnerMessage("wi_A1", "  -x amber\n\nbold  ", "lab");
  assert.deepEqual(ack, { kind: "message", at: "2026-11-10T10:00:01Z", step: "build", spawnN: 2, delivered: false, disposition: "queued", note: "busy token=***" }, "the ack, its note masked");
  await sendPolyforgeOwnerMessage("wi_A1", "the hold is wrong");
  const argv = readFileSync(log, "utf8").trim().split("\n").map((line) => JSON.parse(line));
  assert.deepEqual(argv, [
    ["--profile", "lab", "--json", "answer", "wi_A1", "--text=  -x amber\n\nbold  "],
    ["--json", "answer", "wi_A1", "--text=the hold is wrong"],
  ], "the words verbatim in `=` form; never --question or --option");
  for (const bad of ["", "   ", undefined, 3]) {
    await assert.rejects(sendPolyforgeOwnerMessage("wi_A1", bad), (error) => error instanceof PolyforgeError, `${JSON.stringify(bad)} is no message`);
  }
  await assert.rejects(sendPolyforgeOwnerMessage("demo#1", "x"), (error) => error instanceof PolyforgeError, "a slug is no wi id");
  assert.equal(readFileSync(log, "utf8").trim().split("\n").length, 2, "no refused call spawned pf");
  // the ack parser: the relay's two kinds; anything else is an undelivered message
  assert.deepEqual(parseOwnerMessageAckWire(JSON.stringify({ kind: "reply", at: "t", ask_at: "a", step: "s", spawn_n: 1, delivered: true, disposition: "started" })),
    { kind: "reply", at: "t", askAt: "a", step: "s", spawnN: 1, delivered: true, disposition: "started" });
  assert.deepEqual(parseOwnerMessageAckWire(JSON.stringify({ status: "accepted", question_id: "q1" })), { kind: "message", delivered: false });
  assert.deepEqual(parseOwnerMessageAckWire("not json"), { kind: "message", delivered: false });
  assert.deepEqual(parseOwnerMessageAckWire("[1]"), { kind: "message", delivered: false });
});

test("pf's typed refusal of an owner message is unwrapped — context lines, then `<code>: <detail>`, masked and bounded; its reason only when a token", async () => {
  const { parseOwnerMessageRefusal } = await loadSubject();
  assert.equal(parseOwnerMessageRefusal(""), null);
  assert.equal(parseOwnerMessageRefusal("pf: DAEMON-409: wi_A is wrapped\n"), null, "a human line is no envelope");
  assert.equal(parseOwnerMessageRefusal("{not json}\n"), null);
  assert.equal(parseOwnerMessageRefusal(JSON.stringify({ detail: "no code" })), null, "an envelope names its code");
  assert.deepEqual(parseOwnerMessageRefusal(JSON.stringify({ code: "DAEMON-409", detail: "x is parked token=SECRET", extra: { reason: "WAKE_CANNOT_START" } }) + "\n"),
    { text: "DAEMON-409: x is parked token=***", reason: "WAKE_CANNOT_START" });
  assert.deepEqual(parseOwnerMessageRefusal(JSON.stringify({ code: "DAEMON-409", detail: "d", extra: { reason: "not a token; rm" } })), { text: "DAEMON-409: d" }, "a malformed reason is dropped");
  assert.deepEqual(parseOwnerMessageRefusal("answer: wi_A's blocked door is open, but the wake did not complete:\n" + JSON.stringify({ code: "DAEMON-409", detail: "the claim did not start" })),
    { text: "answer: wi_A's blocked door is open, but the wake did not complete:\nDAEMON-409: the claim did not start" }, "pf's context line stays in front");
  assert.equal(parseOwnerMessageRefusal(JSON.stringify({ code: "DAEMON-409", detail: "y".repeat(5000) })).text.length, 2000, "bounded");
});

test("a `pf answer` that outlives its bound (a wake's claim) is reported as possibly recorded, never as unavailable", async (t) => {
  const { sendPolyforgeOwnerMessage, OWNER_MESSAGE_TIMEOUT_MS } = await loadSubject();
  assert.equal(OWNER_MESSAGE_TIMEOUT_MS, 120_000, "pf's own bound on a daemon request: a wake claims the work item inside it");
  const { mkdtempSync, writeFileSync, chmodSync, rmSync } = await import("node:fs");
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");
  const work = mkdtempSync(join(tmpdir(), "pf-owner-slow-"));
  const previous = process.env.PF_BIN;
  t.after(() => { if (previous === undefined) delete process.env.PF_BIN; else process.env.PF_BIN = previous; rmSync(work, { force: true, recursive: true }); });
  writeFileSync(join(work, "pf"), `#!${process.execPath}\nsetTimeout(() => {}, 5000);\n`);
  chmodSync(join(work, "pf"), 0o755);
  process.env.PF_BIN = join(work, "pf");
  await assert.rejects(sendPolyforgeOwnerMessage("wi_A1", "go on", undefined, 200), (error) => /did not answer within 0 s: the message may still be recorded/.test(error.message));
});

test("the watch carries no send-back fields — the job card has no 打回到… form", async () => {
  const { sanitizeWatch } = await loadSubject();
  const snap = sanitizeWatch({ work_item: "wi_E2Eaaaa0001", slug: "pi#108", known: true, events: [],
    execution: { workflow_status: "running", current_step: "review", steps: machineSteps,
      rewind_targets: ["plan", "build"], rewind_busy: "busy", rewinds: { agent: 1, max_rewinds: 4, owner: 2 } } });
  assert.ok(snap, "the snapshot passes");
  for (const field of ["rewindTargets", "rewindBusy", "rewinds"]) assert.equal(snap[field], undefined, `${field} never leaves`);
});

// ---- context usage (the running step session's context window, per job) ----

const contextJob = (wi, candidate, context) => ({ work_item_id: wi, slug: `workbench#${wi.slice(-4)}`, status: "running",
  spawn_n: 1, candidate, ...(context === undefined ? {} : { context }) });

test("context usage: the job row keeps only the five numeric/model fields", async () => {
  const { sanitizeOpsStatus } = await loadSubject();
  const AS_OF = "2026-11-13T04:00:00.123Z";
  const snapshot = sanitizeOpsStatus({
    queue: { running: 1, max_concurrent: 4 },
    jobs: [
      contextJob("wi_CTXaaaa0001", "pi:example-gateway/model-a", {
        tokens: 420000, window: 1000000, percent: 42, model: "example-gateway/model-a", as_of: AS_OF,
        transcript: "CTX-TRANSCRIPT-MARKER", api_key: "CTX-KEY-MARKER", session: "/srv/ctx-secret/session.jsonl",
      }),
      contextJob("wi_CTXaaaa0002", "claude:claude-model-e2e", { tokens: -1, window: 0, percent: -3, model: "m".repeat(129), as_of: "not a time" }),
      contextJob("wi_CTXaaaa0003", "pi:example-gateway/model-a", { tokens: 5000, model: "bad model; rm -rf" }),
      contextJob("wi_CTXaaaa0004", "claude:claude-model-e2e", { window: 200000, model: "claude-model-e2e" }),
      contextJob("wi_CTXaaaa0005", "pi:x", { tokens: Number.NaN, window: Number.POSITIVE_INFINITY, percent: Number.NaN }),
      contextJob("wi_CTXaaaa0006", "pi:x", { tokens: "100", window: "200", percent: "50", model: 7 }),
      contextJob("wi_CTXaaaa0007", "pi:x", { as_of: AS_OF }),
      contextJob("wi_CTXaaaa0008", "pi:x", "42%"),
      contextJob("wi_CTXaaaa0009", "pi:x", null),
      contextJob("wi_CTXaaaa0010", "pi:x", [420000]),
      contextJob("wi_CTXaaaa0011", "pi:x"),
      contextJob("wi_CTXaaaa0012", "claude:claude-model-e2e", { tokens: 0, percent: 0, model: "claude-model-e2e", as_of: AS_OF }),
    ],
  });
  assert.ok(snapshot);
  const byWi = new Map(snapshot.jobs.map((job) => [job.wi, job]));
  assert.deepEqual(byWi.get("wi_CTXaaaa0001").context,
    { tokens: 420000, window: 1000000, percent: 42, model: "example-gateway/model-a", asOf: AS_OF },
    "the five fields pass, renamed to the panel's keys; every other wire field is dropped");
  assert.equal(byWi.get("wi_CTXaaaa0002").context, undefined, "negative counts, a zero window, an over-long model and a malformed as_of leave nothing");
  assert.deepEqual(byWi.get("wi_CTXaaaa0003").context, { tokens: 5000 }, "a model outside the id charset is dropped, the count stays");
  assert.deepEqual(byWi.get("wi_CTXaaaa0004").context, { window: 200000, model: "claude-model-e2e" }, "unknown tokens stay absent, never 0");
  for (const wi of ["wi_CTXaaaa0005", "wi_CTXaaaa0006", "wi_CTXaaaa0007", "wi_CTXaaaa0008", "wi_CTXaaaa0009", "wi_CTXaaaa0010", "wi_CTXaaaa0011"]) {
    assert.equal(byWi.get(wi).context, undefined, `${wi}: no valid count, window or model → no context`);
    assert.ok(!("context" in byWi.get(wi)), `${wi}: the key itself is absent`);
  }
  assert.deepEqual(byWi.get("wi_CTXaaaa0012").context, { tokens: 0, percent: 0, model: "claude-model-e2e", asOf: AS_OF }, "a real zero is a count, kept");
  const text = JSON.stringify(snapshot);
  for (const leak of ["CTX-TRANSCRIPT-MARKER", "CTX-KEY-MARKER", "/srv/ctx-secret", "not a time", "bad model"]) {
    assert.ok(!text.includes(leak), `context passthrough leaked: ${leak}`);
  }
});

test("context usage: a missing window comes from the chat header's catalog", async (t) => {
  const { mkdirSync, writeFileSync } = await import("node:fs");
  const { join } = await import("node:path");
  const previous = process.env.PI_CODING_AGENT_DIR;
  const agentDir = join(process.env.HOME, "ctx-agent");
  mkdirSync(agentDir, { recursive: true });
  writeFileSync(join(agentDir, "models.json"), JSON.stringify({
    providers: {
      "example-gateway": {
        baseUrl: "http://127.0.0.1:9/v1",
        api: "openai-completions",
        apiKey: "example-key",
        models: [{ id: "model-a", name: "Model A", contextWindow: 400000, maxTokens: 8192 }],
      },
    },
  }));
  process.env.PI_CODING_AGENT_DIR = agentDir;
  t.after(() => { if (previous === undefined) delete process.env.PI_CODING_AGENT_DIR; else process.env.PI_CODING_AGENT_DIR = previous; });

  const { sanitizeOpsStatus, fillPolyforgeContextWindows } = await loadSubject();
  const { claudeContextWindow } = await jiti.import("./claude-session.ts");
  const CLAUDE_MODEL = "claude-haiku-4-5";
  const catalogWindow = claudeContextWindow(CLAUDE_MODEL);
  assert.ok(typeof catalogWindow === "number" && catalogWindow > 0, "the claude fixture model has a catalog window");

  const status = sanitizeOpsStatus({
    queue: { running: 7, max_concurrent: 8 },
    jobs: [
      contextJob("wi_CTXbbbb0001", "pi:example-gateway/model-a", { tokens: 100000, model: "example-gateway/model-a" }),
      contextJob("wi_CTXbbbb0002", `claude:${CLAUDE_MODEL}`, { tokens: 50000, model: CLAUDE_MODEL }),
      contextJob("wi_CTXbbbb0003", `claude:${CLAUDE_MODEL}`, { tokens: 50000, window: 1000000, percent: 5, model: CLAUDE_MODEL }),
      contextJob("wi_CTXbbbb0004", "pi:example-gateway/model-a", { model: "example-gateway/model-a" }),
      contextJob("wi_CTXbbbb0005", "pi:example-gateway/model-unknown", { tokens: 1000, model: "example-gateway/model-unknown" }),
      contextJob("wi_CTXbbbb0006", "claude:claude-model-e2e", { tokens: 1000, model: "claude-model-e2e" }),
      contextJob("wi_CTXbbbb0007", "pi:example-gateway/model-a", { tokens: 1000 }),
      contextJob("wi_CTXbbbb0008", "pi:example-gateway/model-a"),
    ],
  });
  const filled = await fillPolyforgeContextWindows(status);
  const byWi = new Map(filled.jobs.map((job) => [job.wi, job]));
  assert.deepEqual(byWi.get("wi_CTXbbbb0001").context, { tokens: 100000, window: 400000, percent: 25, model: "example-gateway/model-a" },
    "pi: the pi model registry's window (models.json here), and the percent from it");
  assert.deepEqual(byWi.get("wi_CTXbbbb0002").context, { tokens: 50000, window: catalogWindow, percent: (50000 / catalogWindow) * 100, model: CLAUDE_MODEL },
    "claude: claudeContextWindow, the chat header's fallback");
  assert.deepEqual(byWi.get("wi_CTXbbbb0003").context, { tokens: 50000, window: 1000000, percent: 5, model: CLAUDE_MODEL },
    "a window the harness reported is kept as it is");
  assert.deepEqual(byWi.get("wi_CTXbbbb0004").context, { window: 400000, model: "example-gateway/model-a" },
    "unknown tokens: the window, and no percent");
  assert.deepEqual(byWi.get("wi_CTXbbbb0005").context, { tokens: 1000, model: "example-gateway/model-unknown" }, "no catalog entry: no window, no percent");
  assert.deepEqual(byWi.get("wi_CTXbbbb0006").context, { tokens: 1000, model: "claude-model-e2e" }, "no claude catalog entry: no window");
  assert.deepEqual(byWi.get("wi_CTXbbbb0007").context, { tokens: 1000 }, "no model: nothing to look up");
  assert.equal(byWi.get("wi_CTXbbbb0008").context, undefined, "no context stays none");
  assert.deepEqual(filled.queue, status.queue, "the rest of the snapshot is untouched");
  assert.equal(await fillPolyforgeContextWindows(null), null, "a degraded read stays degraded");
});
