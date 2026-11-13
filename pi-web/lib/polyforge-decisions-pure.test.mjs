// carries: lib/polyforge-decisions-pure.ts, lib/i18n/messages/*
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const pure = await jiti.import("./polyforge-decisions-pure.ts");

// the daemon's decision feed (`GET /decisions`, printed verbatim
// by `pf --json watch decisions`). Wire shape: contract of the retirement —
// empty strings omitted, booleans always present, newest first.
const wi = (id, fields) => ({ id, at: `2026-11-09T10:${String(id).padStart(2, "0")}:00Z`, kind: "work_item", profile: "default",
  wi: `wi_W${id}`, slug: `demo#${id}`, attention: false, resolved: false, dismissed: false, ...fields });

test("the sanitizer keeps the wire fields, drops malformed entries and bounds every string", () => {
  const feed = pure.sanitizePolyforgeDecisionFeed({
    attention: 1,
    decisions: [
      wi(14, { class: "failed", source: "failed_gate", phrase: "the workflow failed", attention: true, extra: "SECRET-EXTRA" }),
      { id: 13, at: "2026-11-09T10:00:00Z", kind: "task", profile: "default", task_id: "task_0123456789abcdef", class: "completed", source: "task",
        phrase: "the background task completed", text: "  summary\n  kept verbatim  ", attention: false, resolved: false, dismissed: false },
      wi(12, { class: "needs_input", source: "needs_input", question_id: "q-colour", phrase: "x".repeat(5000), attention: true, resolved: true }),
      wi(11, { class: "carrier_push_failed", source: "carrier_push_failed", repo: "demo-repo", attention: true, dismissed: true }),
      // Dropped: a non-integer / non-positive id, an unknown kind, a bad class token, missing booleans, a non-object.
      wi(1.5, { class: "failed" }),
      wi(0, { class: "failed" }),
      wi("9", { class: "failed" }),
      { ...wi(8, { class: "failed" }), kind: "cell" },
      wi(7, { class: "Failed; rm -rf" }),
      { ...wi(6, { class: "failed" }), attention: "yes" },
      "banana",
      null,
    ],
  });
  assert.ok(feed);
  assert.deepEqual(feed.decisions.map((d) => d.id), [14, 13, 12, 11], "malformed entries are dropped, wire order kept (newest first)");
  assert.equal(feed.attention, 1);
  const [failed, task, needs, push] = feed.decisions;
  assert.deepEqual(failed, { id: 14, at: "2026-11-09T10:14:00Z", kind: "work_item", profile: "default", wi: "wi_W14", slug: "demo#14",
    class: "failed", source: "failed_gate", phrase: "the workflow failed", attention: true, resolved: false, dismissed: false, replied: false });
  assert.ok(!JSON.stringify(feed).includes("SECRET-EXTRA"), "no unknown field leaves");
  assert.equal(task.task_id, "task_0123456789abcdef");
  assert.equal(task.text, "  summary\n  kept verbatim  ", "the task summary is verbatim (whitespace kept)");
  assert.equal(needs.question_id, "q-colour");
  assert.ok(needs.phrase.length <= pure.DECISION_PHRASE_MAX, "a long phrase is bounded");
  assert.equal(push.repo, "demo-repo");
  assert.equal(pure.sanitizePolyforgeDecisionFeed({ decisions: [{ ...wi(5, { class: "failed" }), text: "y".repeat(100_000), kind: "task", task_id: "task_00000000000000aa" }] })
    .decisions[0].text.length, pure.DECISION_TEXT_MAX, "a long summary is bounded");
});

test("the sanitizer degrades field by field and refuses a shape that is no feed", () => {
  const feed = pure.sanitizePolyforgeDecisionFeed({ decisions: [
    wi(3, { class: "blocked", slug: 42, profile: "Not A Profile", question_id: "bad id with spaces", wi: "not-a-wi" }),
    { id: 2, at: "t", kind: "task", class: "failed", attention: true, resolved: false, dismissed: false },
  ] });
  assert.deepEqual(feed.decisions[0], { id: 3, at: "2026-11-09T10:03:00Z", kind: "work_item", class: "blocked", attention: false, resolved: false, dismissed: false, replied: false },
    "malformed optional fields are absent, never an error");
  assert.equal(feed.decisions[1].task_id, undefined, "a task entry without its id keeps the rest");
  // the daemon's task ids are `task_` + 16 lowercase hex (task.NewID); any
  // other shape is dropped like every malformed optional field
  for (const bad of ["t_abc", "task_0123", "task_0123456789ABCDEF", "TASK_0123456789abcdef"]) {
    const one = pure.sanitizePolyforgeDecisionFeed({ decisions: [{ id: 9, at: "t", kind: "task", task_id: bad, class: "failed", attention: true, resolved: false, dismissed: false }] });
    assert.equal(one.decisions[0].task_id, undefined, `${bad} is no task id`);
  }
  assert.equal(feed.attention, 1, "without a usable wire count, attention counts the open entries");
  for (const bad of [null, "x", [], {}, { decisions: "x" }]) assert.equal(pure.sanitizePolyforgeDecisionFeed(bad), null, `${JSON.stringify(bad)} is no feed`);
  assert.deepEqual(pure.sanitizePolyforgeDecisionFeed({ decisions: [] }), { decisions: [], attention: 0 });
});

test("OPEN = attention && !resolved && !dismissed", () => {
  assert.equal(pure.isOpenDecision(wi(1, { class: "failed", attention: true })), true);
  assert.equal(pure.isOpenDecision(wi(1, { class: "failed", attention: true, resolved: true })), false);
  assert.equal(pure.isOpenDecision(wi(1, { class: "failed", attention: true, dismissed: true })), false);
  assert.equal(pure.isOpenDecision(wi(1, { class: "completed" })), false);
  assert.equal(pure.decisionState(wi(1, { class: "failed", attention: true })), "open");
  assert.equal(pure.decisionState(wi(1, { class: "completed" })), "open", "an informational entry is open too (not resolved, not dismissed)");
  assert.equal(pure.decisionState(wi(1, { class: "failed", attention: true, resolved: true, dismissed: true })), "resolved");
  assert.equal(pure.decisionState(wi(1, { class: "failed", dismissed: true })), "dismissed");
});

test("the target label: slug, else wi; a non-default Cell's profile prefix; task <id>", () => {
  assert.equal(pure.decisionTargetLabel(wi(1, { class: "failed" })), "demo#1");
  assert.equal(pure.decisionTargetLabel({ ...wi(1, { class: "failed" }), slug: undefined }), "wi_W1");
  assert.equal(pure.decisionTargetLabel(wi(1, { class: "failed", profile: "lab" })), "lab:demo#1");
  assert.equal(pure.decisionTargetLabel({ id: 2, kind: "task", task_id: "task_0000000000000009", class: "completed" }), "task task_0000000000000009");
  assert.equal(pure.decisionTargetLabel({ id: 3, kind: "work_item", class: "daemon_restarted" }), "", "no target named");
});

test("the source shows only when it differs from the class, and never the task kind's own source", () => {
  assert.equal(pure.decisionSourceShown(wi(1, { class: "failed", source: "failed_gate" })), "failed_gate");
  assert.equal(pure.decisionSourceShown(wi(1, { class: "failed", source: "failed" })), undefined);
  assert.equal(pure.decisionSourceShown(wi(1, { class: "completed", source: "wrapped" })), "wrapped");
  assert.equal(pure.decisionSourceShown({ id: 2, kind: "task", task_id: "t", class: "completed", source: "task" }), undefined);
});

test("every class of the contract has a label key; an unknown class falls back to the wire phrase", () => {
  for (const cls of ["needs_input", "blocked", "failed", "completed", "canceled", "paused", "terminal", "step_dead", "candidates_exhausted",
    "carrier_push_failed", "submit_rejected", "submit_dropped", "daemon_restarted", "superseded", "capped", "interrupted"]) {
    assert.ok(pure.DECISION_CLASSES.includes(cls), `${cls} is a known class`);
  }
  assert.equal(pure.isKnownDecisionClass("failed"), true);
  assert.equal(pure.isKnownDecisionClass("brand_new"), false);
});

const job = (n, status, profile) => ({ wi: `wi_W${n}`, slug: `demo#${n}`, status, ...(profile ? { profile } : {}) });

test("the jump target: any work item opens its job detail in Work › Jobs (live job's Cell, else the entry's), a task Work › Tasks", () => {
  const jobs = [job(1, "running"), job(2, "needs_input", "lab")];
  assert.deepEqual(pure.decisionJumpTarget(wi(1, { class: "failed" }), jobs), { kind: "job", ref: "wi_W1" });
  assert.deepEqual(pure.decisionJumpTarget(wi(2, { class: "needs_input", profile: "lab" }), jobs), { kind: "job", ref: "wi_W2", profile: "lab" });
  assert.deepEqual(pure.decisionJumpTarget(wi(2, { class: "needs_input" }), jobs), { kind: "job", ref: "wi_W2", profile: "default" },
    "another Cell's job of the same literal is not this one: the entry's own Cell is addressed");
  assert.deepEqual(pure.decisionJumpTarget(wi(9, { class: "completed" }), jobs), { kind: "job", ref: "wi_W9", profile: "default" },
    "a work item the daemon dropped still opens its job detail (the watch snapshot), never a tab of its own");
  assert.deepEqual(pure.decisionJumpTarget({ id: 4, kind: "task", task_id: "task_0000000000000001", class: "failed" }, jobs), { kind: "task", id: "task_0000000000000001" });
  assert.equal(pure.decisionJumpTarget({ id: 5, kind: "work_item", class: "daemon_restarted" }, jobs), null);
  assert.equal(pure.decisionJumpTarget({ id: 6, kind: "task", class: "failed" }, jobs), null);
});

test("the walk target: a waiting job or a work item entry opens its job detail; a task entry Work › Tasks", () => {
  const jobs = [job(1, "needs_input"), job(2, "running", "lab")];
  const items = pure.attentionItems([jobs[0]], [wi(30, { class: "failed", attention: true, wi: "wi_W2", profile: "lab" }),
    { id: 29, at: "t", kind: "task", task_id: "task_0000000000000009", class: "failed", attention: true, resolved: false, dismissed: false },
    { id: 28, at: "t", kind: "work_item", class: "daemon_restarted", attention: true, resolved: false, dismissed: false }]);
  assert.deepEqual(items.map((item) => pure.attentionTarget(item, jobs)), [
    { kind: "job", ref: "wi_W1" },
    { kind: "job", ref: "wi_W2", profile: "lab" },
    { kind: "task", id: "task_0000000000000009" },
    null,
  ]);
});

test("an item's entries: its own work item (by id, else slug) in its own Cell, newest first", () => {
  const decisions = [
    wi(5, { class: "failed", attention: true, wi: "wi_A", slug: "demo#1" }),
    wi(4, { class: "completed", wi: "wi_A", slug: "demo#1", profile: "lab" }),
    { ...wi(3, { class: "blocked", attention: true, slug: "demo#1" }), wi: undefined },
    wi(2, { class: "completed", wi: "wi_B" }),
    { id: 1, at: "t", kind: "task", task_id: "task_0000000000000001", class: "completed", attention: false, resolved: false, dismissed: false },
  ];
  assert.deepEqual(pure.itemDecisions(decisions, { wi: "wi_A", slug: "demo#1" }).map((d) => d.id), [5, 3]);
  assert.deepEqual(pure.itemDecisions(decisions, { wi: "wi_A", slug: "demo#1", profile: "lab" }).map((d) => d.id), [4]);
  assert.deepEqual(pure.itemDecisions(decisions, { wi: "wi_Z" }), []);
});

test("a task's entries: matched by task id; it needs attention while one is open", () => {
  const decisions = [
    { id: 3, at: "t", kind: "task", task_id: "task_0000000000000001", class: "failed", attention: true, resolved: false, dismissed: false },
    { id: 2, at: "t", kind: "task", task_id: "task_0000000000000002", class: "completed", text: "done", attention: false, resolved: false, dismissed: false },
    wi(1, { class: "failed", attention: true }),
  ];
  assert.deepEqual(pure.taskDecisions(decisions, "task_0000000000000001").map((d) => d.id), [3]);
  assert.deepEqual(pure.taskDecisions(decisions, "task_0000000000000002").map((d) => d.id), [2]);
  assert.equal(pure.taskNeedsAttention(pure.taskDecisions(decisions, "task_0000000000000001")), true);
  assert.equal(pure.taskNeedsAttention(pure.taskDecisions(decisions, "task_0000000000000002")), false);
});

test("the reminder: needs_input jobs first, then the other open entries; a needs_input entry of a waiting job counts once", () => {
  const waiting = [job(1, "needs_input"), job(2, "needs_input", "lab")];
  const decisions = [
    wi(30, { class: "failed", source: "failed_gate", attention: true }),
    wi(29, { class: "needs_input", attention: true, wi: "wi_W1", slug: "demo#1" }),
    wi(28, { class: "needs_input", attention: true, wi: "wi_W2", slug: "demo#2" }),
    wi(27, { class: "completed" }),
    wi(26, { class: "blocked", attention: true, resolved: true }),
    { id: 25, at: "t", kind: "task", task_id: "task_0000000000000005", class: "failed", attention: true, resolved: false, dismissed: false },
  ];
  const items = pure.attentionItems(waiting, decisions);
  assert.deepEqual(items.map((item) => item.key), ["job:default:wi_W1", "job:lab:wi_W2", "decision:30", "decision:28", "decision:25"],
    "#29 (wi_W1, default Cell) is the waiting job already counted; #28 names the default Cell, not lab's job");
  assert.equal(pure.attentionItems([], []).length, 0);
});

test("the click walk: the first item, then the one after the last opened, wrapping; the selection stands in for a job", () => {
  const items = pure.attentionItems([job(1, "needs_input"), job(2, "needs_input")], [wi(30, { class: "failed", attention: true })]);
  assert.equal(pure.nextAttentionItem(items, { lastKey: null, selectedWi: null }).key, "job:default:wi_W1");
  assert.equal(pure.nextAttentionItem(items, { lastKey: "job:default:wi_W1", selectedWi: "wi_W1" }).key, "job:default:wi_W2");
  assert.equal(pure.nextAttentionItem(items, { lastKey: "job:default:wi_W2", selectedWi: "wi_W2" }).key, "decision:30");
  assert.equal(pure.nextAttentionItem(items, { lastKey: "decision:30", selectedWi: null }).key, "job:default:wi_W1", "wraps");
  assert.equal(pure.nextAttentionItem(items, { lastKey: "decision:99", selectedWi: "demo#1" }).key, "job:default:wi_W2",
    "a last item that is gone falls back to the selected waiting job (by slug too), as #413 did");
  assert.equal(pure.nextAttentionItem([], { lastKey: null, selectedWi: null }), null);
});

test("the dismiss request: positive integer ids (deduplicated, bounded) or exactly {all:true}", () => {
  assert.deepEqual(pure.parseDismissRequest({ ids: [12, 13, 12] }), { ids: [12, 13] });
  assert.deepEqual(pure.parseDismissRequest({ all: true }), { all: true });
  for (const bad of [null, "x", {}, { ids: [] }, { ids: [0] }, { ids: [-1] }, { ids: [1.5] }, { ids: ["12"] }, { ids: [Number.MAX_SAFE_INTEGER + 1] },
    { ids: "12" }, { all: "true" }, { all: false }, { all: true, ids: [1] }, { ids: Array.from({ length: pure.DISMISS_IDS_MAX + 1 }, (_, i) => i + 1) }]) {
    assert.equal(pure.parseDismissRequest(bad), null, `${JSON.stringify(bad)?.slice(0, 60)} is refused`);
  }
});

test("every blocked kind the panel knows has its label — write_failed (workflow contract 4's failed hub writes) among them", async () => {
  const { enLocale } = await jiti.import("./i18n/messages/en.ts");
  assert.ok(pure.KNOWN_BLOCKED_KINDS.includes("write_failed"), "a `done` hand-off whose hub writes failed parks write_failed");
  assert.ok(pure.KNOWN_BLOCKED_KINDS.includes("incomplete"), "a step's own `blocked` hand-off keeps the incomplete kind");
  for (const kind of pure.KNOWN_BLOCKED_KINDS) {
    assert.ok(enLocale.messages[`polyforge.blocked.kind.${kind}`]?.trim(), `${kind} is labelled (the registry test holds every locale to the English keys)`);
  }
});

test("the blocked kinds: every kind is labelled, a contract block is terminal (pf cancel), resume / cancel commands carry the Cell", () => {
  // a contract block is released by no resume (its lane
  // snapshot targets another workflow contract): the composer shows the
  // cancel command only
  assert.deepEqual([...pure.TERMINAL_BLOCKED_KINDS], ["contract"]);
  assert.deepEqual([...pure.KNOWN_BLOCKED_KINDS].sort(), ["contract", "driver", "incomplete", "write_failed"], "the kinds the panel labels (held equal to the daemon by test/w415_blocked_kinds_test.go)");
  assert.equal(pure.cancelCommand("wi_W9"), "pf cancel wi_W9");
  assert.equal(pure.cancelCommand("wi_W9", "eu"), "pf --profile eu cancel wi_W9");
  assert.equal(pure.resumeCommand("wi_X", "default"), "pf resume wi_X");
  assert.equal(pure.resumeCommand("wi_W1"), "pf resume wi_W1");
  assert.equal(pure.resumeCommand("wi_W1", "lab"), "pf --profile lab resume wi_W1");
  // workflow contract v2 (change 14): the 继续 button is the job card's
  // no-agent menu (conversationState); entries carry no button
  assert.equal("entryResumable" in pure, false, "no entry carries a button any more");
});

test("single flight (PR #860 minor 3): one read at a time; a call during a read joins it and one more read follows", async () => {
  let reads = 0;
  const gates = [];
  const flight = pure.singleFlight(() => { reads += 1; return new Promise((resolve) => gates.push(resolve)); });
  const first = flight();
  const second = flight();
  const third = flight();
  assert.equal(reads, 1, "the calls during the read start none of their own");
  assert.equal(second, first, "they join the one in flight");
  assert.equal(third, first);
  gates.shift()();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(reads, 2, "exactly one follow-up read for everything asked meanwhile (read-your-write)");
  gates.shift()();
  await first;
  assert.equal(reads, 2);
  const later = flight();
  assert.equal(reads, 3, "settled: the next call reads again");
  gates.shift()();
  await later;
  const failing = pure.singleFlight(() => Promise.reject(new Error("x")));
  await assert.rejects(failing(), /x/);
  await assert.rejects(failing(), /x/, "a failed read frees the slot");
});

test("dropped-item attention: one card per work item the daemon no longer holds, from its newest open entry, until resolved or dismissed", () => {
  // the live jobs of the status read — J1 running, J2 waiting
  // for an answer, and wi_Wcell live in the lab Cell only
  const jobs = [
    { wi: "wi_J1", slug: "demo#J1", status: "running" },
    { wi: "wi_J2", slug: "demo#J2", status: "needs_input" },
    { wi: "wi_Wcell", slug: "demo#cell", status: "running", profile: "lab" },
  ];
  const open = { attention: true };
  const feed = [
    wi(60, { class: "blocked", source: "blocked", ...open, wi: "wi_Wblk", slug: "demo#blk" }),
    wi(59, { class: "failed", source: "failed_gate", ...open, wi: "wi_Wfail", slug: "demo#fail" }),
    wi(58, { class: "submit_rejected", ...open, wi: "wi_Wrej", slug: "demo#rej" }),
    wi(57, { class: "submit_dropped", ...open, wi: "wi_Wdrop", slug: "demo#drop" }),
    wi(56, { class: "candidates_exhausted", ...open, wi: "wi_Wcand", slug: "demo#cand" }),
    wi(55, { class: "failed", ...open, wi: "wi_Wblk", slug: "demo#blk" }),
    wi(54, { class: "needs_input", ...open, wi: "wi_J2", slug: "demo#J2" }),
    wi(53, { class: "failed", ...open, wi: "wi_J1", slug: "demo#J1" }),
    wi(52, { class: "blocked", ...open, resolved: true, wi: "wi_Wres", slug: "demo#res" }),
    wi(51, { class: "failed", ...open, dismissed: true, wi: "wi_Wdis", slug: "demo#dis" }),
    wi(50, { class: "completed", attention: false, wi: "wi_Winfo", slug: "demo#info" }),
    { id: 49, at: "t", kind: "task", task_id: "task_0000000000000049", class: "failed", attention: true, resolved: false, dismissed: false },
    wi(48, { class: "failed", ...open, wi: "wi_Wcell", slug: "demo#cell" }),
    wi(47, { class: "failed", ...open, wi: "wi_Wcell", slug: "demo#cell", profile: "lab" }),
    { ...wi(46, { class: "blocked", ...open, slug: "demo#nowi" }), wi: undefined },
  ];
  const ids = (decisions) => pure.droppedItemAttention(decisions, jobs).map((d) => d.id);
  const got = pure.droppedItemAttention(feed, jobs);
  assert.equal(got.length, 6, "five dropped items plus the default Cell's wi_Wcell, one entry each");
  assert.deepEqual(got.map((d) => d.id), [60, 59, 58, 57, 56, 48],
    "newest first in feed order; W keeps only its newest open entry (#60 blocked, not #55 failed); J1/J2 (live), "
    + "resolved, dismissed, informational, task and wi-less entries give none; lab's live wi_Wcell hides only lab's entry");
  assert.equal(got[0], feed[0], "the entry itself, as the feed holds it");
  assert.deepEqual(got.map((d) => d.class), ["blocked", "failed", "submit_rejected", "submit_dropped", "candidates_exhausted", "failed"]);

  const isW = (d) => d.kind === "work_item" && d.wi === "wi_Wblk";
  const resolvedNewest = feed.map((d) => (d.id === 60 ? { ...d, resolved: true } : d));
  assert.deepEqual(ids(resolvedNewest), [59, 58, 57, 56, 55, 48], "W's newest open entry is then the older failure, still one card");
  const resolvedW = feed.map((d) => (isW(d) ? { ...d, resolved: true } : d));
  assert.deepEqual(ids(resolvedW), [59, 58, 57, 56, 48], "every W entry resolved (job_started): W has no card");
  const dismissedW = feed.map((d) => (isW(d) ? { ...d, dismissed: true } : d));
  assert.deepEqual(ids(dismissedW), [59, 58, 57, 56, 48], "every W entry dismissed: W has no card");
  assert.deepEqual(pure.droppedItemAttention(feed, [...jobs, { wi: "wi_Wblk", slug: "demo#blk", status: "running" }]).map((d) => d.id), [59, 58, 57, 56, 48],
    "W live again (继续): its live card stands, no feed-only card beside it");
  assert.deepEqual(pure.droppedItemAttention([], jobs), []);
});

test("a replied entry keeps replied/replied_at, shows as replied and counts nowhere", async () => {
  // The daemon marks the entry of an answered question (the owner's reply
  // naming its ask): replied, replied_at = the reply's own stamp,
  // attention:false, still unresolved until the step hands off.
  const ASK = "2026-11-12T08:10:00.123456789Z";
  const REPLIED_AT = "2026-11-12T08:15:30.5Z";
  const feed = pure.sanitizePolyforgeDecisionFeed({ attention: 0, decisions: [
    wi(21, { class: "needs_input", source: "needs_input", question_id: ASK, replied: true, replied_at: REPLIED_AT }),
    wi(20, { class: "needs_input", source: "needs_input", question_id: "2026-11-12T08:00:00Z", attention: true }),
    wi(19, { class: "needs_input", replied: "yes", replied_at: 42 }),
    wi(18, { class: "needs_input", replied: true, replied_at: "z".repeat(500) }),
  ] });
  const [replied, open, bogus, long] = feed.decisions;
  assert.equal(replied.replied, true, "the sanitizer keeps replied");
  assert.equal(replied.replied_at, REPLIED_AT, "and replied_at");
  assert.equal(replied.question_id, ASK, "the ask's RFC3339Nano stamp is a question id");
  assert.equal(open.replied, false, "an unanswered entry is not replied");
  assert.equal(open.replied_at, undefined);
  assert.equal(bogus.replied, false, "only a boolean true is replied");
  assert.equal(bogus.replied_at, undefined, "a non-string replied_at is dropped");
  assert.ok(long.replied_at.length <= 64, "replied_at is bounded like at");

  assert.equal(pure.decisionState(replied), "replied", "a replied entry shows as replied");
  assert.equal(pure.decisionState({ ...replied, resolved: true }), "resolved", "the daemon's resolution wins over replied");
  assert.equal(pure.decisionState({ ...replied, dismissed: true }), "dismissed", "the owner's dismiss wins over replied");
  assert.equal(pure.decisionState(open), "open");
  assert.equal(pure.isOpenDecision(replied), false, "a replied entry is not open");
  assert.deepEqual(pure.attentionItems([], [replied]), [], "the Polyforge button counts no replied entry");
  assert.deepEqual(pure.attentionItems([], [replied, open]).map((item) => item.key), ["decision:20"], "only the open one");
  assert.deepEqual(pure.droppedItemAttention([replied], []), [], "a replied entry makes no attention card");

  const locales = [
    (await jiti.import("./i18n/messages/en.ts")).enLocale,
    (await jiti.import("./i18n/messages/zh-CN.ts")).zhCNLocale,
    (await jiti.import("./i18n/messages/zh-TW.ts")).zhTWLocale,
  ];
  for (const locale of locales) {
    assert.ok(locale.messages["polyforge.decisions.state.replied"]?.trim(), `${locale.id} labels the replied state`);
  }
  assert.equal(locales[1].messages["polyforge.decisions.state.replied"], "已回复，等 step 处理");
});
