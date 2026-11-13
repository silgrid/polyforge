import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { appendFileSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, truncateSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-cards.ts");
const pure = await jiti.import("./polyforge-cards-pure.ts");

const T0 = Date.parse("2026-10-30T01:00:00.000Z");
const at = (sec) => new Date(T0 + sec * 1000).toISOString();
const job = (wi, slug = wi, status = "running") => ({ wi, slug, status, stepAgeSec: 1, idleAgeSec: 2, candidate: "pi:m", spawnN: 1 });
const SECRETS = ["SECRET-TOOL-RESULT", "SECRET-THINKING", "SECRET-PROMPT", "SECRET-TOKEN", "SECRET-SYSTEM"];
const jsonl = (records) => records.map((r) => JSON.stringify(r)).join("\n") + "\n";
const piText = (id, text) => ({ type: "message", id, parentId: null, message: { role: "assistant", content: [{ type: "text", text }] } });

function fixture(t) {
  const work = mkdtempSync(join(tmpdir(), "pf-cards-"));
  t.after(() => rmSync(work, { recursive: true, force: true }));
  const root = join(work, "data");
  mkdirSync(join(root, "events"), { recursive: true });
  const records = [];
  const writeIndex = () => writeFileSync(join(root, "runtime-state.json"), JSON.stringify({ version: 1, jobs: records }));
  function capsule(wi, { steps = ["grill", "code"], goal = "goal", execution, journal, bundle = "feature-full" } = {}) {
    const dir = join(work, "capsules", wi);
    mkdirSync(join(dir, "runtime-state"), { recursive: true });
    mkdirSync(join(dir, "workflows", bundle), { recursive: true });
    mkdirSync(join(dir, "session"), { recursive: true });
    writeFileSync(join(dir, "capsule.json"), JSON.stringify({ bundle_name: bundle }));
    writeFileSync(join(dir, "workflows", bundle, "workflow.yaml"), `name: x\nsteps:\n${steps.map((id) => `  - { id: ${id}, recall: true }`).join("\n")}\n`);
    if (goal !== null) writeFileSync(join(dir, "runtime-state", "work-item.json"), JSON.stringify({ goal }));
    if (execution) writeFileSync(join(dir, "runtime-state", "execution.json"), JSON.stringify(execution));
    if (journal) writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl(journal));
    records.push({ work_item_id: wi, capsule_dir: dir, transcript: join(dir, "session", "job.jsonl") });
    writeIndex();
    return dir;
  }
  const events = (wi, list) => writeFileSync(join(root, "events", `wi-${wi}.jsonl`), jsonl(list.map(([sec, kind, step]) => ({ at: at(sec), kind, work_item: wi, body: step ? { step } : {} }))));
  return { work, root, capsule, events };
}

test("step durations: summed runs with ×N, live current step, unstarted and unknown, declared order", async (t) => {
  const f = fixture(t);
  const wi = "wi_steps01";
  f.capsule(wi, {
    steps: ["grill", "spec", "plan", "ghost", "code", "review"],
    execution: { current_step: "code", steps: {
      grill: { status: "done" }, spec: { status: "done" }, plan: { status: "done" }, ghost: { status: "done" },
      code: { status: "in_progress", step_started_at: at(100) }, review: { status: "pending" } } },
  });
  f.events(wi, [[0, "job_started"], [50, "job_started"], [1, "step_started", "grill"], [11, "step_done", "grill"],
    [12, "step_started", "spec"], [20, "step_started", "spec"], [30, "step_done", "spec"],
    [31, "step_started", "plan"], [41, "step_done", "plan"], [42, "step_started", "plan"], [47, "step_done", "plan"]]);
  const [card] = await lib.readPolyforgeCards([job(wi)], new Map(), T0 + 160_000, f.root);
  assert.equal(card.startedAt, at(0), "earliest job_started");
  assert.equal(card.goal, "goal");
  assert.deepEqual(card.steps, [
    { id: "grill", state: "timed", durationSec: 10, runs: 1 },
    { id: "spec", state: "timed", durationSec: 18, runs: 2 },
    { id: "plan", state: "timed", durationSec: 15, runs: 2 },
    { id: "ghost", state: "unknown", runs: 0 },
    { id: "code", state: "timed", durationSec: 60, runs: 1 },
    { id: "review", state: "unstarted", runs: 0 },
  ]);
  const later = (await lib.readPolyforgeCards([job(wi)], new Map(), T0 + 170_000, f.root))[0];
  assert.equal(later.steps[4].durationSec, 70, "the running step grows with now");
  assert.equal(card.currentStep, "code", "the card carries execution.json's current_step");
});

test("currentStep is present only when execution.json names one", async (t) => {
  const f = fixture(t);
  f.capsule("wi_nocur01", { execution: { steps: { grill: { status: "done" } } } });
  f.capsule("wi_noexec1");
  const cards = await lib.readPolyforgeCards([job("wi_nocur01"), job("wi_noexec1")], new Map(), T0, f.root);
  for (const card of cards) assert.equal("currentStep" in card, false, `${card.job.wi}: no current step, no field`);
});

test("an open run closes at now only for the current in-progress step", () => {
  const events = [{ at: at(0), kind: "step_started", step: "a" }, { at: at(5), kind: "step_started", step: "b" }];
  const execution = { currentStep: "b", steps: new Map([["a", { status: "failed" }], ["b", { status: "in_progress" }]]) };
  const steps = lib.computeCardSteps(["a", "b", "c"], events, execution, T0 + 25_000);
  assert.deepEqual(steps, [
    { id: "a", state: "unknown", runs: 1 },
    { id: "b", state: "timed", durationSec: 20, runs: 1 },
    { id: "c", state: "unstarted", runs: 0 },
  ]);
});

test("a skipped step with no run renders as skipped, not unknown", () => {
  const execution = { currentStep: "c", steps: new Map([["a", { status: "skipped" }], ["b", { status: "done" }], ["c", { status: "pending" }]]) };
  assert.deepEqual(lib.computeCardSteps(["a", "b", "c"], [], execution, T0), [
    { id: "a", state: "skipped", runs: 0 },
    { id: "b", state: "unknown", runs: 0 },
    { id: "c", state: "unstarted", runs: 0 },
  ]);
});

test("the declared order is the steps list of the capsule's workflow.yaml; a torn one or a workflow.md declares none", async (t) => {
  const f = fixture(t);
  const dir = f.capsule("wi_lane01", { steps: ["build", "review", "merge"] });
  assert.deepEqual(await lib.readDeclaredSteps(dir), ["build", "review", "merge"]);
  const lane = join(dir, "workflows", "feature-full");
  writeFileSync(join(lane, "workflow.yaml"), "# yaml-language-server: $schema=../_schema/workflow-1.schema.json\ncontract: 1\nsteps:\n  - id: b\n  - id: a\n  - id: b\n");
  assert.deepEqual(await lib.readDeclaredSteps(dir), ["b", "a"], "a leading comment is no defect; a repeated id counts once");
  writeFileSync(join(lane, "workflow.yaml"), "steps: [\n");
  assert.equal(await lib.readDeclaredSteps(dir), undefined, "a torn workflow.yaml declares no order");
  rmSync(join(lane, "workflow.yaml"));
  writeFileSync(join(lane, "workflow.md"), "---\nsteps:\n  - id: a\n---\n");
  assert.equal(await lib.readDeclaredSteps(dir), undefined, "a contract 4 workflow.md is not read");
});

test("without a readable workflow the order falls back to observed starts, then the record's keys", () => {
  const events = [{ at: at(0), kind: "step_started", step: "y" }, { at: at(1), kind: "step_started", step: "x" }];
  const execution = { steps: new Map([["x", { status: "done" }], ["z", { status: "pending" }], ["y", { status: "done" }]]) };
  assert.deepEqual(lib.cardStepOrder(undefined, events, execution), ["y", "x", "z"]);
  assert.deepEqual(lib.cardStepOrder(["a"], events, execution), ["a"]);
});

test("pi transcript: first load is the newest 20 whitelisted entries; the cursor then returns only the increment", async (t) => {
  const f = fixture(t);
  const wi = "wi_pitrans1";
  const dir = f.capsule(wi, { execution: { current_step: "code", steps: { code: { status: "in_progress" } } } });
  const transcript = join(dir, "session", "s.jsonl");
  writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ step_id: "code", transcript, harness: "pi" }]));
  writeFileSync(transcript, jsonl([
    { type: "session", id: "s" },
    { type: "message", id: "u", message: { role: "user", content: "SECRET-PROMPT" } },
    ...Array.from({ length: 30 }, (_, i) => piText(`p${i}`, `PROGRESS-${String(i).padStart(2, "0")}`)),
    { type: "message", id: "tc", message: { role: "assistant", content: [{ type: "thinking", thinking: "SECRET-THINKING" }, { type: "toolCall", id: "c", name: "bash", arguments: { command: "curl --token=SECRET-TOKEN x" } }] } },
    { type: "message", id: "tr", message: { role: "toolResult", toolName: "bash", content: [{ type: "text", text: "SECRET-TOOL-RESULT" }] } },
  ]));
  const [first] = await lib.readPolyforgeCards([job(wi)], new Map(), T0, f.root);
  assert.equal(first.progress.available, true);
  assert.equal(first.progress.reset, true);
  assert.equal(first.progress.entries.length, 20);
  assert.equal(first.progress.entries[0].text, "PROGRESS-10");
  // narration only — the tool call yields no entry at all.
  assert.deepEqual(first.progress.entries.at(-1), { id: first.progress.entries.at(-1).id, text: "PROGRESS-29" });
  for (const entry of first.progress.entries) assert.deepEqual(Object.keys(entry).sort(), ["id", "text"], "an entry is {id, text}");
  const body = JSON.stringify(first);
  for (const secret of [...SECRETS, "bash", "curl", "token="]) assert.ok(!body.includes(secret), `leaked ${secret}`);
  assert.ok(pure.isPolyforgeCardCursor(first.progress.cursor));

  const same = (await lib.readPolyforgeCards([job(wi)], new Map([[`default/${wi}`, first.progress.cursor]]), T0, f.root))[0];
  assert.deepEqual(same.progress.entries, [], "nothing new, nothing re-sent");
  assert.equal(same.progress.cursor, first.progress.cursor);
  assert.notEqual(same.progress.reset, true);

  appendFileSync(transcript, jsonl([piText("n1", "NEW-ONE"), piText("n2", "NEW-TWO")]));
  const next = (await lib.readPolyforgeCards([job(wi)], new Map([[`default/${wi}`, first.progress.cursor]]), T0, f.root))[0];
  assert.deepEqual(next.progress.entries.map((e) => e.text), ["NEW-ONE", "NEW-TWO"], "only the increment");

  appendFileSync(transcript, jsonl(Array.from({ length: 45 }, (_, i) => piText(`m${i}`, `MORE-${i}`))));
  const page = (await lib.readPolyforgeCards([job(wi)], new Map([[`default/${wi}`, next.progress.cursor]]), T0, f.root))[0];
  assert.equal(page.progress.entries.length, 20, "a poll returns at most 20 entries");
  assert.equal(page.progress.entries[0].text, "MORE-0");
  const page2 = (await lib.readPolyforgeCards([job(wi)], new Map([[`default/${wi}`, page.progress.cursor]]), T0, f.root))[0];
  assert.equal(page2.progress.entries[0].text, "MORE-20", "the cursor continues exactly after the page");

  writeFileSync(transcript, jsonl([piText("r", "REPLACED")]));
  const replaced = (await lib.readPolyforgeCards([job(wi)], new Map([[`default/${wi}`, page2.progress.cursor]]), T0, f.root))[0];
  assert.equal(replaced.progress.reset, true, "a truncated transcript re-sends the newest page");
  assert.deepEqual(replaced.progress.entries.map((e) => e.text), ["REPLACED"]);
});

test("claude transcript: assistant text only; tool_use, api-request-blob, tool results, thinking and prompts never", async (t) => {
  const f = fixture(t);
  const wi = "wi_claude01";
  const dir = f.capsule(wi);
  const transcript = join(dir, "session", "c.jsonl");
  writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ step_id: "code", transcript, harness: "claude" }]));
  writeFileSync(transcript, jsonl([
    { type: "queue-operation", operation: "enqueue", content: "SECRET-PROMPT" },
    { type: "user", message: { role: "user", content: [{ type: "text", text: "SECRET-PROMPT" }] } },
    { type: "api-request-blob", message: { role: "assistant", content: [{ type: "text", text: "SECRET-SYSTEM" }] } },
    { type: "assistant", message: { role: "assistant", content: [{ type: "thinking", thinking: "SECRET-THINKING" }, { type: "text", text: "VISIBLE\n  text" }, { type: "tool_use", id: "t", name: "Bash", input: { command: "echo FOO_API_KEY=SECRET-TOKEN" } }] } },
    { type: "user", message: { role: "user", content: [{ type: "tool_result", tool_use_id: "t", content: "SECRET-TOOL-RESULT" }] } },
  ]));
  const [card] = await lib.readPolyforgeCards([job(wi)], new Map(), T0, f.root);
  assert.deepEqual(card.progress.entries.map(({ text }) => text), ["VISIBLE text"], "the tool_use block yields no entry");
  const body = JSON.stringify(card);
  for (const secret of [...SECRETS, "Bash", "echo FOO_API_KEY"]) assert.ok(!body.includes(secret), `leaked ${secret}`);
});

test("narration keeps its credential mask", async (t) => {
  const f = fixture(t);
  const wi = "wi_mask01";
  const dir = f.capsule(wi);
  const transcript = join(dir, "session", "s.jsonl");
  writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ step_id: "code", transcript, harness: "pi" }]));
  writeFileSync(transcript, jsonl([piText("n", "calling with token=SECRET-TOKEN now")]));
  const [card] = await lib.readPolyforgeCards([job(wi)], new Map(), T0, f.root);
  assert.deepEqual(card.progress.entries.map(({ text }) => text), ["calling with token=*** now"]);
});

test("entries are capped at 400 code points after masking", async (t) => {
  const f = fixture(t);
  const wi = "wi_cap01";
  const dir = f.capsule(wi);
  const transcript = join(dir, "session", "s.jsonl");
  writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ transcript, harness: "pi" }]));
  writeFileSync(transcript, jsonl([piText("a", "界".repeat(900))]));
  const [card] = await lib.readPolyforgeCards([job(wi)], new Map(), T0, f.root);
  assert.equal(Array.from(card.progress.entries[0].text).length, 400);
});

test("a large transcript is never replayed whole: the first load reads only the tail window", async (t) => {
  const f = fixture(t);
  const wi = "wi_large01";
  const dir = f.capsule(wi);
  const transcript = join(dir, "session", "s.jsonl");
  writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ transcript, harness: "pi" }]));
  writeFileSync(transcript, jsonl(Array.from({ length: 5000 }, (_, i) => piText(`l${i}`, `LINE-${i} ${"x".repeat(80)}`))));
  const [card] = await lib.readPolyforgeCards([job(wi)], new Map(), T0, f.root);
  assert.equal(card.progress.entries.length, 20);
  assert.ok(card.progress.entries.at(-1).text.startsWith("LINE-4999 "));
  const offset = pure.parseCardCursor(card.progress.cursor).offset;
  assert.ok(offset > lib.TRANSCRIPT_WINDOW_BYTES, "the cursor points at the end, not the start");
});

test("the per-response budget caps entries across cards and keeps a starved card's cursor", async (t) => {
  const f = fixture(t);
  const jobs = [];
  const cursors = new Map();
  for (let i = 0; i < 12; i++) {
    const wi = `wi_budget${String(i).padStart(2, "0")}`;
    const dir = f.capsule(wi);
    const transcript = join(dir, "session", "s.jsonl");
    writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ transcript, harness: "pi" }]));
    writeFileSync(transcript, jsonl(Array.from({ length: 30 }, (_, j) => piText(`e${j}`, `E${i}-${j}`))));
    f.events(wi, [[i, "job_started"]]);
    jobs.push(job(wi));
    if (i === 11) cursors.set(`default/${wi}`, pure.formatCardCursor("0123456789abcdef", 0));
  }
  const cards = await lib.readPolyforgeCards(jobs, cursors, T0, f.root);
  const total = cards.reduce((sum, c) => sum + c.progress.entries.length, 0);
  assert.equal(total, 200);
  assert.ok(Buffer.byteLength(JSON.stringify(cards)) < 200 * 1024);
  assert.deepEqual(cards[10].progress, { available: true, entries: [] }, "a starved first load leaves no cursor");
  assert.deepEqual(cards[11].progress, { available: true, entries: [], cursor: cursors.get("default/wi_budget11") }, "a starved card keeps its cursor");
});

test("missing, unknown-harness, unrecognised and outside-root transcripts degrade to unavailable", async (t) => {
  const f = fixture(t);
  const make = (wi, journal, content) => {
    const dir = f.capsule(wi);
    const transcript = journal.transcript ?? join(dir, "session", "s.jsonl");
    writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ ...journal, transcript }]));
    if (content !== undefined) writeFileSync(transcript, content);
    return dir;
  };
  make("wi_missing1", { harness: "pi" });
  make("wi_codex01", { harness: "codex" }, jsonl([piText("a", "x")]));
  make("wi_unrec01", { harness: "claude" }, jsonl([piText("a", "x")]));
  make("wi_empty01", { harness: "pi" }, "");
  const outside = join(f.work, "outside.jsonl");
  writeFileSync(outside, jsonl([piText("a", "OUTSIDE")]));
  const records = JSON.parse(readFileSync(join(f.root, "runtime-state.json"), "utf8"));
  records.jobs = records.jobs.map((r) => (r.work_item_id === "wi_missing1" ? { ...r, transcript: outside } : r));
  writeFileSync(join(f.root, "runtime-state.json"), JSON.stringify(records));
  make("wi_outside1", { harness: "pi", transcript: outside });
  const wis = ["wi_missing1", "wi_codex01", "wi_unrec01", "wi_empty01", "wi_outside1"];
  const cards = await lib.readPolyforgeCards(wis.map((wi) => job(wi)), new Map(), T0, f.root);
  for (const card of cards) {
    assert.deepEqual(card.progress, { available: false, entries: [] }, card.job.wi);
    assert.ok(!JSON.stringify(card).includes("OUTSIDE"));
  }
});

test("the harness's live session file comes first; the job record's transcript is never a progress source", async (t) => {
  const f = fixture(t);
  // pi: the newest step-agent session written since the step started wins over the capsule copy.
  const piWi = "wi_livepi01";
  const piDir = f.capsule(piWi);
  const copy = join(piDir, "session", "copy.jsonl");
  writeFileSync(copy, jsonl([{ type: "session", id: "s" }, piText("c", "CAPSULE-COPY")]));
  writeFileSync(join(piDir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ step_id: "code", started_at: new Date(Date.now() - 1000).toISOString(), transcript: copy, harness: "pi" }]));
  const sessions = join(f.root, "step-agent-sessions", lib.encodePiSessionDir(piDir));
  mkdirSync(sessions, { recursive: true });
  writeFileSync(join(sessions, "old.jsonl"), jsonl([piText("o", "OLDER-STEP")]));
  const old = new Date(Date.now() - 3_600_000);
  utimesSync(join(sessions, "old.jsonl"), old, old);
  writeFileSync(join(sessions, "new.jsonl"), jsonl([piText("n", "LIVE-PI")]));
  // claude: the harness_session file under claude-config wins.
  const clWi = "wi_liveclaude";
  const clDir = f.capsule(clWi);
  writeFileSync(join(clDir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ step_id: "code", harness: "claude", harness_session: "11bff798-b38a-4b0b-9601-814d9e471ff4", transcript: join(clDir, "session", "not-yet.jsonl") }]));
  const projects = join(f.root, "claude-config", "projects", lib.encodeClaudeProjectDir(clDir));
  mkdirSync(projects, { recursive: true });
  writeFileSync(join(projects, "11bff798-b38a-4b0b-9601-814d9e471ff4.jsonl"), jsonl([{ type: "assistant", message: { role: "assistant", content: [{ type: "text", text: "LIVE-CLAUDE" }] } }]));
  // A step whose live file is not there yet: only an older pi session exists → the capsule copy.
  const oldWi = "wi_stalepi01";
  const oldDir = f.capsule(oldWi);
  const oldCopy = join(oldDir, "session", "copy.jsonl");
  writeFileSync(oldCopy, jsonl([{ type: "session", id: "s" }, piText("c", "STEP-COPY")]));
  writeFileSync(join(oldDir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ step_id: "code", started_at: new Date().toISOString(), transcript: oldCopy, harness: "pi" }]));
  const oldSessions = join(f.root, "step-agent-sessions", lib.encodePiSessionDir(oldDir));
  mkdirSync(oldSessions, { recursive: true });
  writeFileSync(join(oldSessions, "prev.jsonl"), jsonl([piText("p", "PREVIOUS-STEP")]));
  utimesSync(join(oldSessions, "prev.jsonl"), old, old);
  // The job record's transcript (the job's first step) is ignored when the journal's path is gone.
  const recWi = "wi_recordonly";
  const recDir = f.capsule(recWi);
  writeFileSync(join(recDir, "session", "job.jsonl"), jsonl([piText("j", "FIRST-STEP")]));
  writeFileSync(join(recDir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ step_id: "code", transcript: join(recDir, "session", "gone.jsonl"), harness: "pi" }]));

  const cards = await lib.readPolyforgeCards([job(piWi), job(clWi), job(oldWi), job(recWi)], new Map(), T0, f.root);
  const texts = Object.fromEntries(cards.map((c) => [c.job.wi, c.progress.entries.map((e) => e.text)]));
  assert.deepEqual(texts[piWi], ["LIVE-PI"]);
  assert.deepEqual(texts[clWi], ["LIVE-CLAUDE"]);
  assert.deepEqual(texts[oldWi], ["STEP-COPY"], "a session file older than the step is not the current step's");
  assert.deepEqual(cards.find((c) => c.job.wi === recWi).progress, { available: false, entries: [] });
  assert.equal(lib.encodePiSessionDir("/root/a/b-c"), "--root-a-b-c--");
  assert.equal(lib.encodeClaudeProjectDir("/root/a/b_c.d"), "-root-a-b-c-d");
});

test("a previous step's pi session written after the next step started is not its progress", async (t) => {
  const f = fixture(t);
  const stepStart = Date.now() - 20_000;
  const before = new Date(stepStart - 600_000);
  const stamp = (date) => date.toISOString().replace(/[:.]/g, "-");
  const setup = (wi, name, records) => {
    const dir = f.capsule(wi);
    const copy = join(dir, "session", "copy.jsonl");
    writeFileSync(copy, jsonl([{ type: "session", id: "s" }, piText("c", "STEP-COPY")]));
    writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl([{ step_id: "review", started_at: new Date(stepStart).toISOString(), transcript: copy, harness: "pi" }]));
    const sessions = join(f.root, "step-agent-sessions", lib.encodePiSessionDir(dir));
    mkdirSync(sessions, { recursive: true });
    // Its last write is now: inside the step, and inside the old 60 s mtime slot.
    writeFileSync(join(sessions, name), jsonl(records));
    return dir;
  };
  // The session record dates it: started ten minutes before the step.
  setup("wi_prevhdr01", `${stamp(new Date(stepStart + 1000))}_a.jsonl`,
    [{ type: "session", version: 3, id: "a", timestamp: before.toISOString(), cwd: "/x" }, piText("p", "PREVIOUS-STEP")]);
  // No session record: the file name's ISO timestamp dates it.
  setup("wi_prevname1", `${stamp(before)}_b.jsonl`, [piText("p", "PREVIOUS-STEP")]);
  // The current step's own session (started after the step) is served.
  setup("wi_ownpi0001", `${stamp(before)}_c.jsonl`,
    [{ type: "session", version: 3, id: "c", timestamp: new Date(stepStart + 2000).toISOString(), cwd: "/x" }, piText("n", "OWN-STEP")]);
  // A small clock skew before the journal's stamp is still the step's own.
  setup("wi_skewpi001", "s.jsonl",
    [{ type: "session", version: 3, id: "d", timestamp: new Date(stepStart - 2000).toISOString(), cwd: "/x" }, piText("n", "SKEWED-OWN")]);
  const wis = ["wi_prevhdr01", "wi_prevname1", "wi_ownpi0001", "wi_skewpi001"];
  const cards = await lib.readPolyforgeCards(wis.map((wi) => job(wi)), new Map(), T0, f.root);
  const texts = Object.fromEntries(cards.map((c) => [c.job.wi, c.progress.entries.map((e) => e.text)]));
  assert.deepEqual(texts.wi_prevhdr01, ["STEP-COPY"], "a session whose own record predates the step is the previous step's");
  assert.deepEqual(texts.wi_prevname1, ["STEP-COPY"], "a session whose file name predates the step is the previous step's");
  assert.deepEqual(texts.wi_ownpi0001, ["OWN-STEP"], "the session record wins over the file name");
  assert.deepEqual(texts.wi_skewpi001, ["SKEWED-OWN"]);
  for (const card of cards) assert.ok(!JSON.stringify(card).includes("PREVIOUS-STEP"), card.job.wi);
});

test("unknown, malformed and record-less jobs keep their job facts and never throw", async (t) => {
  const f = fixture(t);
  f.capsule("wi_known01", { goal: null, steps: [] });
  f.events("wi_known01", [[5, "job_started"]]);
  const jobs = [job("../../etc/passwd", "evil#1"), job("", "noid#2"), job("wi_notindex", "x#3"), job("wi_known01", "k#4")];
  const cards = await lib.readPolyforgeCards(jobs, new Map([["default/wi_known01", "c1.0123456789abcdef.99999999"]]), T0, f.root);
  assert.deepEqual(cards.map((c) => c.job.slug), ["k#4", "evil#1", "noid#2", "x#3"], "known start first, then by key");
  const known = cards[0];
  assert.equal(known.goal, undefined, "a missing work item renders the slug alone");
  assert.deepEqual(known.steps, []);
  for (const card of cards.slice(1)) {
    assert.deepEqual(card.steps, []);
    assert.deepEqual(card.progress, { available: false, entries: [] });
  }
  assert.deepEqual((await lib.readPolyforgeCards([job("wi_x")], new Map(), T0, join(f.work, "absent-root")))[0].progress, { available: false, entries: [] });
});

test("an oversized event file is read as a bounded head and tail", async (t) => {
  const f = fixture(t);
  const wi = "wi_bigevents";
  f.capsule(wi, { steps: ["code"], execution: { current_step: "code", steps: { code: { status: "done" } } } });
  const filler = Array.from({ length: 9000 }, (_, i) => [1 + i / 1000, "published"]);
  f.events(wi, [[0, "job_started"], ...filler, [100, "step_started", "code"], [130, "step_done", "code"]]);
  const [card] = await lib.readPolyforgeCards([job(wi)], new Map(), T0, f.root);
  assert.equal(card.startedAt, at(0));
  assert.deepEqual(card.steps, [{ id: "code", state: "timed", durationSec: 30, runs: 1 }]);
});

// ---- the job detail's whole-job execution progress ----

const FIXTURES = new URL("./__fixtures__/progress-238/", import.meta.url);
const ev = (wi, sec, kind, body = {}) => ({ at: at(sec), kind, work_item: wi, body });
const writeEvents = (f, wi, list) => writeFileSync(join(f.root, "events", `wi-${wi}.jsonl`), jsonl(list));
const stamped = (record, sec) => ({ ...record, timestamp: at(sec) });

/** A capsule whose journal names `spawns` ([step, spawn_n, harness, file, records?, startSec]). */
function progressCapsule(f, wi, spawns) {
  const dir = f.capsule(wi);
  const journal = spawns.map(([step, spawnN, harness, file, records, startSec = 0]) => {
    const transcript = join(dir, "session", file);
    if (records !== undefined) writeFileSync(transcript, typeof records === "string" ? records : jsonl(records));
    return { at: at(startSec), work_item: wi, step_id: step, spawn_n: spawnN, started_at: at(startSec), transcript, harness };
  });
  writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl(journal));
  return dir;
}
const texts = (snapshot) => snapshot.lines.map((line) => line.text);

test("the job index reports a missing, unreadable or non-JSON runtime-state.json as unreadable", async (t) => {
  const f = fixture(t);
  f.capsule("wi_index01");
  const index = await lib.readJobIndex(f.root);
  assert.equal(index.readable, true);
  assert.ok(index.records.get("wi_index01").capsuleDir);
  assert.deepEqual([...(await lib.readJobRecords(f.root)).keys()], ["wi_index01"], "the card read uses the same index");
  writeFileSync(join(f.root, "runtime-state.json"), "{not json");
  assert.deepEqual(await lib.readJobIndex(f.root), { readable: false, records: new Map() });
  writeFileSync(join(f.root, "runtime-state.json"), "[]");
  assert.equal((await lib.readJobIndex(f.root)).readable, false, "a non-object index is unreadable");
  rmSync(join(f.root, "runtime-state.json"));
  assert.equal((await lib.readJobIndex(f.root)).readable, false, "a missing index is unreadable");
  writeFileSync(join(f.root, "runtime-state.json"), JSON.stringify({ version: 1 }));
  assert.deepEqual(await lib.readJobIndex(f.root), { readable: true, records: new Map() }, "a readable index without jobs knows no job");
});

test("every spawn of every step, reruns included, merged with the lifecycle lines in time order", async (t) => {
  const f = fixture(t);
  const wi = "wi_whole01";
  const dir = progressCapsule(f, wi, [
    ["grill", 1, "pi", "grill.jsonl", [{ type: "session", id: "s", timestamp: at(0) }, stamped(piText("g", "GRILL-NARRATION"), 5),
      stamped({ type: "message", id: "gt", message: { role: "assistant", content: [{ type: "toolCall", id: "c", name: "bash", arguments: { command: "ls grill" } }] } }, 10)], 0],
    ["spec", 1, "pi", "spec1.jsonl", [stamped(piText("s1", "SPEC1-NARRATION"), 25)], 20],
    ["spec", 2, "claude", "spec2.jsonl", [stamped({ type: "assistant", message: { role: "assistant", content: [{ type: "text", text: "SPEC2-CLAUDE" }, { type: "tool_use", name: "Bash", input: { command: "echo spec2" } }] } }, 65)], 60],
  ]);
  writeEvents(f, wi, [
    ev(wi, 0, "job_started"),
    ev(wi, 0, "step_started", { step: "grill", spawn_n: 1, model: "pi:m" }),
    ev(wi, 15, "step_done", { step: "grill", spawn_n: 1 }),
    ev(wi, 20, "step_started", { step: "spec", spawn_n: 1, model: "pi:m" }),
    ev(wi, 30, "step_failed", { step: "spec", spawn_n: 1, cause: "gate-failed" }),
    ev(wi, 48, "rewind", { gate: "spec-review", target: "spec" }),
    ev(wi, 60, "step_started", { step: "spec", spawn_n: 2, model: "claude:m" }),
    ev(wi, 70, "step_done", { step: "spec", spawn_n: 2 }),
    ev(wi, 75, "terminal", { status: "completed" }),
  ]);
  const snapshot = await lib.readProgressSnapshot(f.root, dir, wi);
  assert.equal(snapshot.available, true);
  assert.equal(snapshot.omitted, 0);
  const strip = (text) => text.replace(/^\d{2}:\d{2}:\d{2} /, "");
  assert.deepEqual(snapshot.lines.map((line) => [line.kind, line.kind === "event" ? strip(line.text) : line.text]), [
    ["event", "▶ grill #1 · pi:m"],
    ["narration", "GRILL-NARRATION"],
    ["tool", "bash: ls grill"],
    ["event", "■ grill +15s"],
    ["event", "▶ spec #1 · pi:m"],
    ["narration", "SPEC1-NARRATION"],
    ["event", "✕ spec · gate-failed"],
    ["event", "↺ spec-review → spec"],
    ["event", "▶ spec #2 · claude:m"],
    ["narration", "SPEC2-CLAUDE"],
    ["tool", "Bash: echo spec2"],
    ["event", "■ spec +10s"],
    ["event", "⏹ completed"],
  ]);
  assert.deepEqual(snapshot.lines.filter((line) => line.kind === "event").map((line) => [line.event, line.tone]),
    [["step_started", "accent"], ["step_done", "dim"], ["step_started", "accent"], ["step_failed", "red"], ["rewind", undefined],
      ["step_started", "accent"], ["step_done", "dim"], ["terminal", "green"]]);
  assert.equal(new Set(snapshot.lines.map((line) => line.id)).size, snapshot.lines.length, "line ids are unique");
  for (const line of snapshot.lines) assert.ok(!line.id.includes("/"), `an id never names a path: ${line.id}`);
  writeFileSync(join(dir, "runtime-state", "work-item.json"), JSON.stringify({ id: wi, goal: "g" }));
  assert.deepEqual(await lib.readProgressSnapshot(f.root, dir), snapshot, "the wi defaults to the capsule's own work item");
});

test("workflow contract v2 (change 14): the owner conversation's events become ask, owner and status lines of the stream, in time order with the transcript", async (t) => {
  const f = fixture(t);
  const wi = "wi_owner01";
  const dir = progressCapsule(f, wi, [
    ["build", 1, "pi", "build.jsonl", [stamped(piText("b1", "I need a decision."), 5), stamped(piText("b2", "Amber it is."), 25)], 0],
  ]);
  writeEvents(f, wi, [
    ev(wi, 0, "step_started", { step: "build", spawn_n: 1, model: "pi:m" }),
    ev(wi, 10, "awaiting_owner", { step: "build", spawn_n: 1, ask_at: at(10), question_id: at(10), question: "Which colour?\n- amber\n- red token=SECRET-TOKEN" }),
    ev(wi, 20, "owner_message", { at: at(19), step: "build", spawn_n: 1, kind: "reply", ask_at: at(10), by: "usr_1", text: "amber please token=SECRET-TOKEN" }),
    ev(wi, 21, "delivered", { step: "build", spawn_n: 1, msg_at: at(20), kind: "reply", disposition: "started", confirmed_by: "user_message" }),
    ev(wi, 30, "intent", { step: "build", spawn_n: 1, kind: "retry", reply_at: at(20), reason: "the owner asked for a fresh start", by: "usr_1" }),
    ev(wi, 31, "effects_applied", { step: "build", spawn_n: 1, keys: ["x"] }),
    ev(wi, 32, "blocked", { step: "build", kind: "write_failed", reason: "write failed: the hub refused", report: false }),
  ]);
  const snapshot = await lib.readProgressSnapshot(f.root, dir, wi);
  const strip = (text) => text.replace(/^\d{2}:\d{2}:\d{2} /, "");
  assert.deepEqual(snapshot.lines.map((line) => [line.kind, line.kind === "event" ? strip(line.text) : line.text, line.by]), [
    ["event", "▶ build #1 · pi:m", undefined],
    ["narration", "I need a decision.", undefined],
    ["ask", "Which colour?\n- amber\n- red token=***", undefined],
    ["owner", "amber please token=***", "usr_1"],
    ["event", "✓ build · delivered · started", undefined],
    ["narration", "Amber it is.", undefined],
    ["event", "⚑ build · intent retry · the owner asked for a fresh start", undefined],
    ["event", "⛔ build · write_failed · write failed: the hub refused", undefined],
  ], "the ask and the owner's words verbatim and masked; effects_applied dropped");
  assert.deepEqual(snapshot.lines.filter((line) => line.kind !== "narration").map((line) => line.event), ["step_started", "awaiting_owner", "owner_message", "delivered", "intent", "blocked"]);
  assert.equal(snapshot.lines.find((line) => line.kind === "owner").msgAt, at(19), "the owner line names its record's own stamp (review r1)");
  assert.ok(!JSON.stringify(snapshot).includes("SECRET-TOKEN"));
  const dropped = await lib.readProgressSnapshot(f.root, undefined, wi);
  assert.deepEqual(dropped.lines.map((line) => line.kind), ["event", "ask", "owner", "event", "event", "event"], "with no capsule the ask and owner lines still come from the event log");
});

test("review r1 #4: a restarted driver's re-announced ask (resumed: true, same ask_at) is one ask line, a new ask another", async (t) => {
  const f = fixture(t);
  const wi = "wi_resumed01";
  writeEvents(f, wi, [
    ev(wi, 0, "step_started", { step: "build", spawn_n: 1, model: "pi:m" }),
    ev(wi, 10, "awaiting_owner", { step: "build", spawn_n: 1, ask_at: at(10), question_id: at(10), question: "Which colour?" }),
    ev(wi, 40, "awaiting_owner", { step: "build", spawn_n: 1, ask_at: at(10), question_id: at(10), question: "Which colour?", resumed: true }),
    ev(wi, 50, "owner_message", { at: at(50), step: "build", spawn_n: 1, kind: "reply", ask_at: at(10), by: "usr_1", text: "amber" }),
    ev(wi, 60, "awaiting_owner", { step: "build", spawn_n: 1, ask_at: at(60), question_id: at(60), question: "Bold too?" }),
  ]);
  const snapshot = await lib.readProgressSnapshot(f.root, undefined, wi);
  assert.deepEqual(snapshot.lines.filter((line) => line.kind === "ask").map((line) => line.text), ["Which colour?", "Bold too?"], "the re-announcement adds no second block");
});

test("with no capsule dir (a job the daemon dropped) the snapshot is the event log alone — lifecycle lines, no narration or tool line", async (t) => {
  const f = fixture(t);
  const wi = "wi_dropped01";
  const dir = progressCapsule(f, wi, [
    ["code", 1, "pi", "code.jsonl", [stamped(piText("n", "DROPPED-NARRATION"), 5),
      stamped({ type: "message", id: "t", message: { role: "assistant", content: [{ type: "toolCall", id: "c", name: "bash", arguments: { command: "ls dropped" } }] } }, 6)], 0],
  ]);
  writeEvents(f, wi, [
    ev(wi, 0, "step_started", { step: "code", spawn_n: 1, model: "pi:m" }),
    ev(wi, 10, "step_done", { step: "code", spawn_n: 1 }),
    ev(wi, 11, "terminal", { status: "completed" }),
    ev(wi, 12, "job_stopped", { spawn_n: 1 }),
  ]);
  const full = await lib.readProgressSnapshot(f.root, dir, wi);
  assert.ok(full.lines.some((line) => line.kind === "narration"), "the capsule read carries the narration");
  const dropped = await lib.readProgressSnapshot(f.root, undefined, wi);
  assert.equal(dropped.available, true);
  assert.equal(dropped.omitted, 0);
  assert.deepEqual(dropped.lines, full.lines.filter((line) => line.kind === "event"), "exactly the lifecycle lines of the full read");
  assert.deepEqual(dropped.lines.map((line) => line.event), ["step_started", "step_done", "terminal", "job_stopped"]);
  assert.ok(!JSON.stringify(dropped).includes("DROPPED-NARRATION") && !JSON.stringify(dropped).includes("ls dropped"), "no transcript is read");
  assert.deepEqual(await lib.readProgressSnapshot(f.root, undefined, "wi_dropped02"), { available: false, lines: [], omitted: 0 }, "no event log: unavailable");
  assert.deepEqual(await lib.readProgressSnapshot(f.root, undefined), { available: false, lines: [], omitted: 0 }, "no capsule and no wi: unavailable");
});

test("a transcript several spawns name (a resumed session) is read once; each spawn keeps its start line", async (t) => {
  const f = fixture(t);
  const wi = "wi_resume01";
  const records = [stamped(piText("a", "FIRST"), 5), stamped(piText("b", "AFTER-RESUME"), 25)];
  const dir = progressCapsule(f, wi, [["review", 1, "pi", "shared.jsonl", records, 0], ["review", 2, "pi", "shared.jsonl", undefined, 20]]);
  writeEvents(f, wi, [ev(wi, 0, "step_started", { step: "review", spawn_n: 1 }), ev(wi, 20, "step_started", { step: "review", spawn_n: 2 })]);
  const snapshot = await lib.readProgressSnapshot(f.root, dir, wi);
  assert.deepEqual(texts(snapshot).map((text) => text.replace(/^\d{2}:\d{2}:\d{2} /, "")), ["▶ review #1", "FIRST", "▶ review #2", "AFTER-RESUME"]);
});

test("a resumed claude step's earlier lines appear once; a resumed pi step and an unresumed claude step are unchanged", async (t) => {
  const f = fixture(t);
  const wi = "wi_resume436";
  const dir = f.capsule(wi);
  const SESSION = "43600000-0000-4000-8000-000000000001";
  const OTHER = "43600000-0000-4000-8000-000000000002";
  const claude = (uuid, sec, content) => ({ type: "assistant", uuid, sessionId: SESSION, timestamp: at(sec), message: { role: "assistant", content } });
  const result = (uuid, sec, id) => ({ type: "user", uuid, sessionId: SESSION, timestamp: at(sec), message: { role: "user", content: [{ type: "tool_result", tool_use_id: id, content: "ok" }] } });
  // claude --resume appends to its native session; each dispatch's capsule
  // copy is the whole conversation so far (the first copy a byte prefix)
  const before = jsonl([claude("u1", 5, [{ type: "text", text: "BEFORE-RESUME" }]), claude("u2", 6, [{ type: "tool_use", id: "toolu_1", name: "Bash", input: { command: "echo before" } }]), result("u3", 7, "toolu_1")]);
  const after = before + jsonl([claude("u4", 25, [{ type: "text", text: "AFTER-RESUME" }])]);
  writeFileSync(join(dir, "session", "c1.jsonl"), before);
  writeFileSync(join(dir, "session", "c2.jsonl"), after);
  writeFileSync(join(dir, "session", "c3.jsonl"), jsonl([{ ...claude("v1", 45, [{ type: "text", text: "FRESH-CLAUDE" }]), sessionId: OTHER }]));
  writeFileSync(join(dir, "session", "pi.jsonl"), jsonl([stamped(piText("a", "PI-FIRST"), 55), stamped(piText("b", "PI-AFTER-RESUME"), 65)]));
  const spawn = (step, spawnN, harness, file, sec, extra = {}) => ({ at: at(sec), work_item: wi, step_id: step, spawn_n: spawnN, started_at: at(sec), transcript: join(dir, "session", file), harness, ...extra });
  writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl([
    spawn("code", 1, "claude", "c1.jsonl", 0, { harness_session: SESSION }),
    spawn("code", 2, "claude", "c2.jsonl", 20, { harness_session: SESSION, resume: true }),
    spawn("review", 3, "claude", "c3.jsonl", 40, { harness_session: OTHER }),
    spawn("verify", 4, "pi", "pi.jsonl", 50),
    spawn("verify", 5, "pi", "pi.jsonl", 60, { resume: true }),
  ]));
  const snapshot = await lib.readProgressSnapshot(f.root, dir, wi);
  assert.equal(snapshot.available, true);
  assert.deepEqual(texts(snapshot), ["BEFORE-RESUME", "Bash: echo before", "AFTER-RESUME", "FRESH-CLAUDE", "PI-FIRST", "PI-AFTER-RESUME"], "each line once, in time order");
  const bash = snapshot.lines.find((line) => line.kind === "tool");
  assert.equal(bash.tool.status, "ok", "the call is still paired with its result");
  assert.equal(new Set(snapshot.lines.map((line) => line.id)).size, snapshot.lines.length, "line ids are unique");
  const detail = await lib.readProgressToolDetail(f.root, dir, bash.id);
  assert.equal(detail.available, true, "the tool line still opens its detail");
  assert.equal(detail.hasResult, true);
});

test("a record without a readable timestamp sits at its spawn's start, in file order", async (t) => {
  const f = fixture(t);
  const wi = "wi_nostamp01";
  const dir = progressCapsule(f, wi, [["code", 1, "pi", "code.jsonl", [piText("a", "NO-STAMP-1"), piText("b", "NO-STAMP-2")], 30]]);
  writeEvents(f, wi, [ev(wi, 10, "step_started", { step: "plan", spawn_n: 1 }), ev(wi, 30, "step_started", { step: "code", spawn_n: 1 }), ev(wi, 31, "step_done", { step: "code", spawn_n: 1 })]);
  const lines = texts(await lib.readProgressSnapshot(f.root, dir, wi)).map((text) => text.replace(/^\d{2}:\d{2}:\d{2} /, ""));
  assert.deepEqual(lines, ["▶ plan #1", "▶ code #1", "NO-STAMP-1", "NO-STAMP-2", "■ code +1s"]);
});

test("recorded pi and claude transcripts project to the same narration + one masked tool line per call", async (t) => {
  const f = fixture(t);
  const wi = "wi_recorded01";
  const dir = progressCapsule(f, wi, [
    ["spec", 4, "pi", "pi.jsonl", readFileSync(new URL("pi.jsonl", FIXTURES), "utf8"), 0],
    ["grill-review", 5, "claude", "claude.jsonl", readFileSync(new URL("claude.jsonl", FIXTURES), "utf8"), 0],
  ]);
  const snapshot = await lib.readProgressSnapshot(f.root, dir, wi);
  assert.equal(snapshot.available, true);
  const pi = snapshot.lines.filter((line) => line.text === "I'll start by reading the grill artifact and checking execution state.");
  assert.equal(pi.length, 1, "the recorded pi narration");
  assert.equal(pi[0].kind, "narration");
  const piTools = snapshot.lines.filter((line) => line.kind === "tool" && /^(bash|read): /.test(line.text));
  assert.ok(piTools.some((line) => line.text.startsWith("bash: cd /workspace/capsules/task-219 && ls -la")), "a recorded pi toolCall → `bash: <command>`");
  assert.ok(piTools.some((line) => line.text === "read: /workspace/capsules/task-219/artifacts/grill-qa.md"), "no command: the first string argument");
  const claudeTools = snapshot.lines.filter((line) => line.kind === "tool" && line.text.startsWith("Bash: "));
  assert.ok(claudeTools.some((line) => line.text === "Bash: ls artifacts runtime-state && cat artifacts/spec.md && echo ===== && cat artifacts/grill-qa.md"), "a recorded claude tool_use → `Bash: <command>`");
  assert.equal(claudeTools.filter((line) => line.text.startsWith("Bash: ls artifacts runtime-state")).length, 1, "the api-request-blob duplicate never becomes a line");
  assert.ok(snapshot.lines.some((line) => line.kind === "narration" && line.text === "No response requested."), "the recorded claude narration");
  // the recorded pi thinking (record bb24b5ba, thinking + toolCall,
  // no text) is a thinking narration line ahead of that record's tool lines.
  // owner-intended change: narration keeps its original whitespace (the recorded
  // paragraph break), so the match is on the recorded text itself.
  const recordedThinking = "No pending_rewind_input → not a rewind. Good.\n\nLet me run the other recall angles and read repo AGENTS.md.";
  const thinkingAt = snapshot.lines.findIndex((line) => line.text === recordedThinking);
  assert.ok(thinkingAt >= 0, "the recorded pi thinking projects as a line");
  assert.equal(snapshot.lines[thinkingAt].kind, "narration", "the recorded pi thinking is narration");
  assert.equal(snapshot.lines[thinkingAt].variant, "thinking", "and carries the thinking variant");
  assert.ok(snapshot.lines.slice(thinkingAt + 1, thinkingAt + 3).every((line) => line.kind === "tool"), "it precedes its record's tool lines");
  // the recorded claude thinking blocks are the empty string on disk
  // (Claude redacts them), so they project to nothing — no thinking line at all.
  const claudeRecords = readFileSync(new URL("claude.jsonl", FIXTURES), "utf8").trim().split("\n").map((line) => JSON.parse(line));
  const claudeThinkingRecords = claudeRecords.filter((record) => (Array.isArray(record.message?.content) ? record.message.content : []).some((block) => block?.type === "thinking"));
  assert.ok(claudeThinkingRecords.length > 0, "the claude fixture carries recorded thinking blocks");
  const claudeProjected = claudeThinkingRecords.flatMap((record, index) => lib.progressEntries(record, "claude", `claude-thinking-${index}`));
  assert.equal(claudeProjected.filter((line) => line.variant === "thinking").length, 0, "a recorded claude thinking:\"\" yields no line");
  for (const line of snapshot.lines) {
    assert.ok(line.kind === "narration" || line.kind === "tool", "transcripts never produce event lines");
    assert.ok(!line.text.includes("[redacted"), `a redacted (excluded) record became a line: ${line.text}`);
  }
});

test("narration (text and non-empty thinking) + one masked tool line; no tool result, prompt, system record or raw credential", async (t) => {
  const f = fixture(t);
  const wi = "wi_mask01";
  const dir = progressCapsule(f, wi, [["code", 1, "pi", "code.jsonl", [
    { type: "session", id: "s" },
    { type: "message", id: "u", message: { role: "user", content: "SECRET-PROMPT" } },
    { type: "message", id: "sys", message: { role: "system", content: "SECRET-SYSTEM" } },
    piText("n", "working token=SECRET-TOKEN now"),
    { type: "message", id: "tc", message: { role: "assistant", content: [{ type: "thinking", thinking: "THINKING-NARRATION token=SECRET-TOKEN" }, { type: "toolCall", id: "c", name: "bash", arguments: { timeout: 5, command: "curl --token=SECRET-TOKEN https://x.invalid" } }] } },
    { type: "message", id: "tr", message: { role: "toolResult", toolName: "bash", content: [{ type: "text", text: "SECRET-TOOL-RESULT" }] } },
  ]]]);
  writeEvents(f, wi, [ev(wi, 1, "push_failed", { error: "push token=SECRET-TOKEN" }), ev(wi, 2, "needs_input", { step: "code", question: "SECRET-PROMPT" }),
    ev(wi, 3, "envelope_rejected", { step: "code", detail: "SECRET-SYSTEM" })]);
  const snapshot = await lib.readProgressSnapshot(f.root, dir, wi);
  assert.deepEqual(snapshot.lines.filter((line) => line.kind !== "event").map(({ kind, text, variant }) => [kind, text, variant]), [
    ["narration", "working token=*** now", undefined],
    ["narration", "THINKING-NARRATION token=***", "thinking"],
    ["tool", "bash: curl --token=*** https://x.invalid", undefined],
  ]);
  assert.deepEqual(snapshot.lines.filter((line) => line.kind === "event").map((line) => line.event), ["push_failed", "needs_input"]);
  const body = JSON.stringify(snapshot);
  for (const secret of SECRETS) assert.ok(!body.includes(secret), `leaked ${secret}`);
  for (const word of ["toolResult", dir, f.root]) assert.ok(!body.includes(word), `the snapshot carries ${word}`);
});

test("a pi non-empty thinking block with no text block is a thinking narration line; empty thinking and tool results produce none", async (t) => {
  const f = fixture(t);
  const dir = progressCapsule(f, "wi_think01", [["code", 1, "pi", "code.jsonl", [
    { type: "message", id: "empty", message: { role: "assistant", content: [{ type: "thinking", thinking: "" }] } },
    { type: "message", id: "thought", message: { role: "assistant", content: [{ type: "thinking", thinking: "THINKING-ONLY" }, { type: "toolCall", id: "c", name: "bash", arguments: { command: "ls" } }] } },
  ]]]);
  const snapshot = await lib.readProgressSnapshot(f.root, dir, "wi_think01");
  assert.deepEqual(snapshot.lines.map(({ kind, text, variant }) => [kind, text, variant]), [
    ["narration", "THINKING-ONLY", "thinking"],
    ["tool", "bash: ls", undefined],
  ], "the pre-fix tree projected none of the thinking line");
});

test("narration, thinking and tool lines are masked but keep their full length", async (t) => {
  const f = fixture(t);
  const dir = progressCapsule(f, "wi_cap01", [["code", 1, "pi", "code.jsonl", [
    piText("n", "界".repeat(900)),
    { type: "message", id: "tc", message: { role: "assistant", content: [{ type: "thinking", thinking: "思".repeat(900) }, { type: "toolCall", id: "c", name: "bash", arguments: { command: `echo token=${"S".repeat(50)} ${"界".repeat(400)}` } }] } },
  ]]]);
  const [narration, thinking, tool] = (await lib.readProgressSnapshot(f.root, dir, "wi_cap01")).lines;
  assert.equal(Array.from(narration.text).length, 900, "text narration is not capped");
  assert.equal(Array.from(thinking.text).length, 900, "thinking narration is not capped");
  assert.equal(thinking.variant, "thinking");
  assert.ok(tool.text.length > 400, `${tool.text.length} code points kept`);
  assert.ok(tool.text.startsWith("bash: echo token=*** 界"), tool.text);
});

test("every line is returned; no 2000-line window or omitted count", async (t) => {
  const f = fixture(t);
  const many = progressCapsule(f, "wi_many01", [["code", 1, "pi", "code.jsonl", Array.from({ length: 2005 }, (_, i) => stamped(piText(`m${i}`, `MANY-${i + 1}`), i))]]);
  const snapshot = await lib.readProgressSnapshot(f.root, many, "wi_many01");
  assert.equal(snapshot.lines.length, 2005);
  assert.equal(snapshot.omitted, 0);
  assert.equal(snapshot.lines[0].text, "MANY-1");
  assert.equal(snapshot.lines.at(-1).text, "MANY-2005");
  const wide = progressCapsule(f, "wi_wide01", [["code", 1, "pi", "code.jsonl", Array.from({ length: 1999 }, (_, i) => piText(`w${i}`, `${i}-${"界".repeat(390)}`))]]);
  const all = await lib.readProgressSnapshot(f.root, wide, "wi_wide01");
  assert.equal(all.lines.length, 1999, "every line, whatever its bytes");
  assert.equal(all.omitted, 0);
  assert.equal(pure.nextProgressSnapshot(null, JSON.parse(JSON.stringify(snapshot))).lines.length, 2005, "the wire round-trips every line");
});

test("a partial last line is not read, an over-8 MiB line is skipped, and the job's transcripts are bounded", async (t) => {
  const f = fixture(t);
  const dir = progressCapsule(f, "wi_bound01", [["code", 1, "pi", "code.jsonl",
    `${JSON.stringify(piText("blob", "X".repeat(8 * 1024 * 1024 + 10)))}\n${jsonl([piText("a", "AFTER-BLOB")])}{"type":"message","id":"half","message":{"role":"assistant","content":[{"type":"text","text":"HALF`]]);
  const snapshot = await lib.readProgressSnapshot(f.root, dir, "wi_bound01");
  assert.deepEqual(texts(snapshot), ["AFTER-BLOB"], "the blob line and the partial line produce nothing");
  const huge = progressCapsule(f, "wi_bound02", [["code", 1, "pi", "code.jsonl", jsonl([piText("a", "SMALL")])], ["review", 1, "pi", "big.jsonl", ""]]);
  truncateSync(join(huge, "session", "big.jsonl"), 128 * 1024 * 1024 + 1);
  assert.deepEqual(await lib.readProgressSnapshot(f.root, huge, "wi_bound02"), { available: false, lines: [], omitted: 0 }, "over the job bound: unavailable, never an inexact count");
});

test("degradation — nothing readable is unavailable; a journal or an event log alone is a stream; never throws", async (t) => {
  const f = fixture(t);
  const none = f.capsule("wi_none01");
  assert.deepEqual(await lib.readProgressSnapshot(f.root, none, "wi_none01"), { available: false, lines: [], omitted: 0 }, "no journal, no event log");
  assert.deepEqual(await lib.readProgressSnapshot(f.root, join(f.work, "no-such-capsule"), "wi_none02"), { available: false, lines: [], omitted: 0 });
  writeEvents(f, "wi_none01", [ev("wi_none01", 1, "terminal", { status: "cancelled" })]);
  const eventsOnly = await lib.readProgressSnapshot(f.root, none, "wi_none01");
  assert.equal(eventsOnly.available, true);
  assert.deepEqual(eventsOnly.lines.map((line) => [line.event, line.tone]), [["terminal", undefined]]);
  const outside = join(f.work, "outside.jsonl");
  writeFileSync(outside, jsonl([piText("a", "OUTSIDE")]));
  const out = f.capsule("wi_out01");
  writeFileSync(join(out, "runtime-state", "spawn-journal.jsonl"), jsonl([{ step_id: "code", spawn_n: 1, transcript: outside, harness: "pi" },
    { step_id: "x", spawn_n: 2, transcript: outside, harness: "codex" }, "not an object", { step_id: "y" }]));
  assert.deepEqual(await lib.readProgressSnapshot(f.root, out, "wi_out01"), { available: true, lines: [], omitted: 0 }, "outside-root and unknown-harness transcripts contribute nothing");
});

test("the newest spawn reads the harness's live pi session before its capsule copy", async (t) => {
  const f = fixture(t);
  const wi = "wi_live01";
  const dir = progressCapsule(f, wi, [["grill", 1, "pi", "grill.jsonl", [stamped(piText("g", "GRILL-COPY"), 2)], 0],
    ["code", 1, "pi", "code.jsonl", [{ type: "session", id: "s", timestamp: at(10) }], 10]]);
  const sessions = join(f.root, "step-agent-sessions", lib.encodePiSessionDir(dir));
  mkdirSync(sessions, { recursive: true });
  writeFileSync(join(sessions, "live.jsonl"), jsonl([{ type: "session", id: "live", timestamp: at(10) }, stamped(piText("l", "CODE-LIVE"), 12)]));
  assert.deepEqual(texts(await lib.readProgressSnapshot(f.root, dir, wi)), ["GRILL-COPY", "CODE-LIVE"]);
});

test("progressEntries is reached only from the whole-job read; the card face's whitelist is unchanged", async () => {
  const source = readFileSync(new URL("./polyforge-cards.ts", import.meta.url), "utf8");
  const uses = [...source.matchAll(/progressEntries\(/g)].length;
  assert.equal(uses, 2, "progressEntries is declared once and called once");
  // owner-intended change: its one caller is the transcript projector that the
  // whole-job read and the on-demand tool detail share (result pairing lives there).
  const start = source.indexOf("function projectTranscript(");
  const end = source.indexOf("\n}\n", start);
  assert.ok(source.slice(start, end).includes("progressEntries("), "projectTranscript is its one caller");
  // owner-intended change: a subagent's conversation is projected by the same
  // projector (its third caller), so its lines pair, mask and time exactly like a spawn's.
  const projected = [...source.matchAll(/projectTranscript\(/g)].length;
  assert.equal(projected, 4, "projectTranscript is declared once and called by the snapshot, the detail read and the subagent projection");
  for (const reader of ["export async function readProgressSnapshot(", "export async function readProgressToolDetail(", "async function projectSubagents("]) {
    const from = source.indexOf(reader);
    assert.ok(from >= 0 && source.slice(from, source.indexOf("\n}\n", from)).includes("projectTranscript("), `${reader} projects through it`);
  }
  const cards = source.slice(source.indexOf("export async function readCardProgress("), source.indexOf("// ---- the whole-job execution progress"));
  assert.doesNotMatch(cards, /progressEntries/, "the card progress never uses the widened projection");
  assert.equal("readProgressTail" in lib, false, "the current-step tail is retired");
});

// ---- whitespace, light tool fields, result pairing, on-demand detail ----

test("narration keeps its original whitespace (masked only); blank narration is no line", async (t) => {
  const f = fixture(t);
  const text = "P1 line.\n\n- item 1\n- item 2\n\n```js\nconst x = 1;\n```\n\n  indented token=SECRET-TOKEN";
  const dir = progressCapsule(f, "wi_ws01", [["code", 1, "pi", "code.jsonl", [
    piText("n", text), piText("blank", "  \n\t "),
    { type: "message", id: "th", message: { role: "assistant", content: [{ type: "thinking", thinking: "T1\n\nT2" }] } },
  ]]]);
  const lines = (await lib.readProgressSnapshot(f.root, dir, "wi_ws01")).lines;
  assert.deepEqual(lines.map(({ kind, text: lineText, variant }) => [kind, lineText, variant]), [
    ["narration", text.replace("token=SECRET-TOKEN", "token=***"), undefined],
    ["narration", "T1\n\nT2", "thinking"],
  ], "whitespace and indentation survive; only the credential is masked; a blank block yields nothing");
});

const piCall = (id, sec, callId, name, args) => stamped({ type: "message", id, message: { role: "assistant", content: [{ type: "toolCall", id: callId, name, arguments: args }] } }, sec);
const piResult = (id, sec, callId, isError, text) => stamped({ type: "message", id, message: { role: "toolResult", ...(callId ? { toolCallId: callId } : {}), toolName: "bash", isError, content: [{ type: "text", text }] } }, sec);

test("tool lines carry only name, masked argument, status and a timestamp duration; no input or output rides the poll", async (t) => {
  const f = fixture(t);
  const wi = "wi_tool01";
  const dir = progressCapsule(f, wi, [
    ["code", 1, "pi", "pi.jsonl", [
      piCall("c1", 3, "k1", "bash", { command: "curl --token=SECRET-TOKEN https://x.invalid", extra: "FULL-INPUT-ONLY" }),
      piResult("r1", 4, "k1", false, "FULL-RESULT-ONLY"),
      piCall("c2", 5, "k2", "read", { path: "NO-RESULT" }),
      piCall("c3", 6, "k3", "grep", { pattern: "ERR" }),
      piResult("r3", 8, "k3", true, "ERROR-RESULT-ONLY"),
      { type: "message", id: "c4", message: { role: "assistant", content: [{ type: "toolCall", id: "k4", name: "ls", arguments: {} }] } },
      { type: "message", id: "r4", message: { role: "toolResult", toolCallId: "k4", isError: false, content: [] } },
      piResult("r5", 9, "unknown-call", true, "ORPHAN"),
    ], 0],
    ["review", 1, "claude", "claude.jsonl", [
      { type: "assistant", timestamp: at(30), message: { role: "assistant", content: [{ type: "tool_use", id: "tu1", name: "Bash", input: { command: "echo claude", extra: "CLAUDE-EXTRA" } }] } },
      { type: "user", timestamp: at(33), message: { role: "user", content: [{ type: "tool_result", tool_use_id: "tu1", is_error: false, content: "CLAUDE-RESULT" }] } },
    ], 30],
  ]);
  const snapshot = await lib.readProgressSnapshot(f.root, dir, wi);
  const tools = snapshot.lines.filter((line) => line.kind === "tool");
  assert.deepEqual(tools.map(({ text, tool }) => [text, tool]), [
    // the unstamped call sits at its spawn's start, ahead of the stamped ones
    ["ls", { name: "ls", status: "ok" }],
    ["bash: curl --token=*** https://x.invalid", { name: "bash", argument: "curl --token=*** https://x.invalid", status: "ok", durationMs: 1000 }],
    ["read: NO-RESULT", { name: "read", argument: "NO-RESULT", status: "running" }],
    ["grep: ERR", { name: "grep", argument: "ERR", status: "error", durationMs: 2000 }],
    ["Bash: echo claude", { name: "Bash", argument: "echo claude", status: "ok", durationMs: 3000 }],
  ], "paired by id on both harnesses; an unpaired call is running; no timestamps, no duration; an unknown result id pairs nothing");
  const body = JSON.stringify(snapshot);
  for (const absent of ["FULL-INPUT-ONLY", "FULL-RESULT-ONLY", "ERROR-RESULT-ONLY", "CLAUDE-EXTRA", "CLAUDE-RESULT", "ORPHAN", "SECRET-TOKEN"]) {
    assert.ok(!body.includes(absent), `the poll carries ${absent}`);
  }
});

test("a result without a call id pairs with the oldest unpaired call of its transcript (the recorded pi format)", async (t) => {
  const f = fixture(t);
  const dir = progressCapsule(f, "wi_order01", [["code", 1, "pi", "pi.jsonl", [
    piCall("c1", 1, undefined, "a", { command: "one" }), piCall("c2", 2, undefined, "b", { command: "two" }),
    piResult("r1", 4, undefined, false, "R1"), piResult("r2", 7, undefined, true, "R2"),
  ]]]);
  const tools = (await lib.readProgressSnapshot(f.root, dir, "wi_order01")).lines.map((line) => line.tool);
  assert.deepEqual(tools, [{ name: "a", argument: "one", status: "ok", durationMs: 3000 }, { name: "b", argument: "two", status: "error", durationMs: 5000 }]);
  const recorded = progressCapsule(f, "wi_order02", [["spec", 4, "pi", "pi.jsonl", readFileSync(new URL("pi.jsonl", FIXTURES), "utf8"), 0]]);
  const recordedTools = (await lib.readProgressSnapshot(f.root, recorded, "wi_order02")).lines.filter((line) => line.kind === "tool");
  assert.ok(recordedTools.length > 0 && recordedTools.every((line) => ["ok", "error", "running"].includes(line.tool.status)), "every recorded call has a status");
  assert.ok(recordedTools.some((line) => line.tool.status !== "running"), "the recorded results pair with their calls");
});

test("the detail read returns one call's full masked input and output, no result as null, and degrades without a path", async (t) => {
  const f = fixture(t);
  const wi = "wi_detail01";
  const dir = progressCapsule(f, wi, [["code", 1, "pi", "pi.jsonl", [
    piCall("c1", 3, "k1", "bash", { command: "curl --token=SECRET-TOKEN https://x.invalid", extra: `FULL-INPUT ${"i".repeat(5000)}` }),
    stamped({ type: "message", id: "r1", message: { role: "toolResult", toolCallId: "k1", isError: false, content: [{ type: "text", text: `FULL-OUT ${"o".repeat(9000)} token=SECRET-TOKEN` }, { type: "image", data: "AAAA" }] } }, 4),
    piCall("c2", 5, "k2", "read", { path: "NO-RESULT" }),
  ]]]);
  const lines = (await lib.readProgressSnapshot(f.root, dir, wi)).lines.filter((line) => line.kind === "tool");
  for (const line of lines) assert.equal(lib.isProgressToolId(line.id), true, `a tool line id has the detail id shape: ${line.id}`);
  const full = await lib.readProgressToolDetail(f.root, dir, lines[0].id);
  assert.equal(full.available, true);
  assert.equal(full.hasResult, true);
  assert.deepEqual({ ...full.tool, input: undefined, output: undefined }, { name: "bash", argument: "curl --token=*** https://x.invalid", status: "ok", durationMs: 1000, isError: false, input: undefined, output: undefined });
  assert.ok(full.tool.input.includes(`FULL-INPUT ${"i".repeat(5000)}`) && full.tool.input.includes("token=***"), "the complete input, masked");
  assert.ok(full.tool.output.startsWith(`FULL-OUT ${"o".repeat(9000)} token=***`), "the complete output, masked");
  assert.ok(full.tool.output.endsWith("\n[image]"), "a non-text block is a neutral placeholder");
  const pending = await lib.readProgressToolDetail(f.root, dir, lines[1].id);
  assert.deepEqual({ available: pending.available, hasResult: pending.hasResult, output: pending.tool.output, status: pending.tool.status }, { available: true, hasResult: false, output: null, status: "running" });
  const answers = [full, pending];
  for (const id of ["0123456789abcdef.0.0", `${lines[0].id.split(".")[0]}.1.0`, "not-an-id"]) {
    const missing = await lib.readProgressToolDetail(f.root, dir, id);
    assert.equal(missing.available, false, id);
    assert.ok(missing.error.length > 0);
    answers.push(missing);
  }
  answers.push(await lib.readProgressToolDetail(f.root, undefined, lines[0].id));
  renameSync(join(dir, "session", "pi.jsonl"), join(dir, "session", "gone.jsonl"));
  const gone = await lib.readProgressToolDetail(f.root, dir, lines[0].id);
  assert.equal(gone.available, false, "a transcript moved away is unavailable");
  answers.push(gone);
  for (const answer of answers) {
    const body = JSON.stringify(answer);
    for (const word of ["SECRET-TOKEN", dir, f.root, "/session/"]) assert.ok(!body.includes(word), `an answer carries ${word}`);
  }
});

test("each job reads its own Cell's state dir (its WorkRef.profile); a pre-Cell root is its own dir", async () => {
  const root = mkdtempSync(join(tmpdir(), "pf-cells-root-"));
  try {
    assert.deepEqual(await lib.polyforgeCellRoots(root), [root], "no cells/ → the root itself");
    assert.equal(await lib.polyforgeCellRoot("alpha", root), root, "a pre-Cell root is its own single dir");
    for (const profile of ["zeta", "default", "alpha"]) mkdirSync(join(root, "cells", profile, "events"), { recursive: true });
    mkdirSync(join(root, "cells", "Not_A_Cell"), { recursive: true });
    assert.deepEqual(await lib.polyforgeCellRoots(root),
      ["default", "alpha", "zeta"].map((profile) => join(root, "cells", profile)), "default first, then by name; non-profile dirs ignored");
    assert.equal(await lib.polyforgeCellRoot("alpha", root), join(root, "cells", "alpha"));
    assert.equal(await lib.polyforgeCellRoot(undefined, root), join(root, "cells", "default"), "no profile = the default Cell");
    assert.equal(await lib.polyforgeCellRoot("../x", root), undefined, "a malformed profile resolves nothing");
    // the same work-item literal on two Cells: each card reads its own Cell
    for (const [profile, started] of [["default", "2026-10-30T01:00:00Z"], ["alpha", "2026-10-30T02:00:00Z"]]) {
      writeFileSync(join(root, "cells", profile, "events", "wi-wi_Same0001.jsonl"), `${JSON.stringify({ at: started, kind: "job_started" })}\n`);
    }
    const job = { wi: "wi_Same0001", slug: "p#1", status: "running", stepAgeSec: 0, idleAgeSec: 0, candidate: "", spawnN: 0 };
    const cards = await lib.readPolyforgeCards([{ ...job, profile: "alpha" }, job], new Map(), Date.now(), root);
    const startOf = (profile) => cards.find((c) => (c.job.profile ?? "default") === profile)?.startedAt;
    assert.equal(startOf("alpha"), "2026-10-30T02:00:00Z", "alpha's card reads alpha's events");
    assert.equal(startOf("default"), "2026-10-30T01:00:00Z", "default's card reads default's events");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
