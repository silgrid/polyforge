// the job detail's execution progress shows each subagent a
// step ran — claude's sidecars (live under claude-config, or the capsule
// copy beside the conversation's transcript), pi's recorded children
// (session/<stem>/subagents/, written by the driver from the step's stdout)
// — as an entry on its parent tool line: description, agent, state, times
// and its own conversation (narration + tool lines whose detail reads like
// any other's). Replays: the recorded claude 2.1.312 subagent run
// (`__fixtures__/claude-2.1.312/subagent/`) and the pi recorder's output
// for a recorded subagent call (`__fixtures__/subagents-424/`).
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, truncateSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const lib = await jiti.import("./polyforge-cards.ts");
const pure = await jiti.import("./polyforge-cards-pure.ts");

const CLAUDE = new URL("./__fixtures__/claude-2.1.312/subagent/", import.meta.url);
const PI = new URL("./__fixtures__/subagents-424/pi/session/", import.meta.url);
const SESSION = "44444444-4444-4444-8444-444444444444";
const CLAUDE_AGENT = "agent-a9fb2ff3aa8e8ff40";
const PI_CALL = "call_bcb397f329d148e18d52f97f";
const jsonl = (records) => records.map((r) => JSON.stringify(r)).join("\n") + "\n";

function capsule(t, wi) {
  const work = mkdtempSync(join(tmpdir(), "pf-sub424-"));
  t.after(() => rmSync(work, { recursive: true, force: true }));
  const root = join(work, "data");
  const dir = join(work, "capsules", wi);
  mkdirSync(join(root, "events"), { recursive: true });
  mkdirSync(join(dir, "runtime-state"), { recursive: true });
  mkdirSync(join(dir, "session"), { recursive: true });
  writeFileSync(join(dir, "runtime-state", "work-item.json"), JSON.stringify({ id: wi, goal: "g" }));
  const journal = (lines) => writeFileSync(join(dir, "runtime-state", "spawn-journal.jsonl"), jsonl(lines.map((line) => ({ work_item: wi, ...line }))));
  return { work, root, dir, journal };
}

const claudeNativeDir = (root, dir) => join(root, "claude-config", "projects", lib.encodeClaudeProjectDir(dir));
const allTools = (lines) => lines.filter((line) => line.kind === "tool");

/** The recorded claude run as a finished step: its transcript as the
 *  capsule copy and its sidecars beside it (the daemon's sync). */
function claudeCapsuleCopy(t, wi) {
  const c = capsule(t, wi);
  const transcript = join(c.dir, "session", "session-tok.jsonl");
  cpSync(new URL("transcript.jsonl", CLAUDE), transcript);
  cpSync(new URL("subagents/", CLAUDE), join(c.dir, "session", "session-tok", "subagents"), { recursive: true });
  c.journal([{ step_id: "build", spawn_n: 1, started_at: "2026-11-02T02:01:02.000Z", harness: "claude", harness_session: SESSION, transcript }]);
  return { ...c, transcript };
}

test(" AC3/AC5: a claude step's Agent call carries its subagent — description, agent, done, duration and its own tool calls", async (t) => {
  const c = claudeCapsuleCopy(t, "wi_sub424a");
  const snapshot = await lib.readProgressSnapshot(c.root, c.dir, "wi_sub424a");
  assert.equal(snapshot.available, true);
  const agent = allTools(snapshot.lines).find((line) => line.tool.name === "Agent");
  assert.ok(agent, "the Agent call is a tool line");
  assert.equal(agent.subagents.length, 1);
  const [run] = agent.subagents;
  assert.equal(run.description, "Read note.txt first line");
  assert.equal(run.agent, "general-purpose");
  assert.equal(run.status, "done", "the sidecar meta says finished");
  assert.equal(run.startAt, Date.parse("2026-11-02T02:01:03.805Z"), "its first record");
  assert.equal(run.durationMs, Date.parse("2026-11-02T02:01:05.676Z") - Date.parse("2026-11-02T02:01:03.805Z"));
  const [read] = allTools(run.lines);
  assert.equal(read.tool.name, "Read");
  assert.equal(read.tool.status, "ok", "the subagent's own call is paired with its result");
  assert.ok(run.lines.some((line) => line.kind === "narration"), "the subagent's reply is narration");
  assert.ok(!/[\\/]/.test(run.id) && !run.lines.some((line) => line.id.includes("/")), "no id names a path");
  for (const line of snapshot.lines) {
    if (line !== agent) assert.equal(line.subagents, undefined, "only the parent call carries it");
  }
  // the subagent's tool call opens like any other (the on-demand detail read)
  const detail = await lib.readProgressToolDetail(c.root, c.dir, read.id);
  assert.equal(detail.available, true);
  assert.equal(detail.tool.name, "Read");
  assert.equal(detail.hasResult, true);
  assert.ok(detail.tool.output.includes("SUBAGENT_PROBE_TOKEN_253"), "the recorded Read result");
});

test(" AC5: a running claude subagent is read live from claude-config, and a resumed spawn of the same session shows it once", async (t) => {
  const c = capsule(t, "wi_sub424b");
  const native = claudeNativeDir(c.root, c.dir);
  mkdirSync(join(native, SESSION, "subagents"), { recursive: true });
  cpSync(new URL("transcript.jsonl", CLAUDE), join(native, `${SESSION}.jsonl`));
  const sidecar = readFileSync(new URL(`subagents/${CLAUDE_AGENT}.jsonl`, CLAUDE), "utf8").split("\n").filter(Boolean);
  writeFileSync(join(native, SESSION, "subagents", `${CLAUDE_AGENT}.jsonl`), sidecar.slice(0, 2).join("\n") + "\n");
  const meta = JSON.parse(readFileSync(new URL(`subagents/${CLAUDE_AGENT}.meta.json`, CLAUDE), "utf8"));
  delete meta.finished;
  writeFileSync(join(native, SESSION, "subagents", `${CLAUDE_AGENT}.meta.json`), JSON.stringify(meta));
  const first = join(c.dir, "session", "session-tok.jsonl");
  cpSync(new URL("transcript.jsonl", CLAUDE), first);
  c.journal([
    { step_id: "build", spawn_n: 1, started_at: "2026-11-02T02:01:02.000Z", harness: "claude", harness_session: SESSION, transcript: first },
    { step_id: "build", spawn_n: 2, resume: true, started_at: "2026-11-02T02:01:06.000Z", harness: "claude", harness_session: SESSION, transcript: join(c.dir, "session", "session-tok#1.jsonl") },
  ]);
  const running = await lib.readProgressSnapshot(c.root, c.dir, "wi_sub424b");
  const runs = running.lines.flatMap((line) => line.subagents ?? []);
  assert.equal(runs.length, 1, "one subagent, though the resumed spawn's transcript repeats the call");
  assert.equal(runs[0].status, "running", "no finished mark yet");
  assert.equal(runs[0].durationMs, undefined, "a running subagent's age is the UI's");
  assert.deepEqual(allTools(runs[0].lines).map((line) => [line.tool.name, line.tool.status]), [["Read", "running"]], "its call so far, no result yet");

  // the next poll sees it grow and finish
  writeFileSync(join(native, SESSION, "subagents", `${CLAUDE_AGENT}.jsonl`), sidecar.join("\n") + "\n");
  writeFileSync(join(native, SESSION, "subagents", `${CLAUDE_AGENT}.meta.json`), JSON.stringify({ ...meta, finished: true }));
  const done = (await lib.readProgressSnapshot(c.root, c.dir, "wi_sub424b")).lines.flatMap((line) => line.subagents ?? []);
  assert.equal(done.length, 1);
  assert.equal(done[0].status, "done");
  assert.deepEqual(allTools(done[0].lines).map((line) => [line.tool.name, line.tool.status]), [["Read", "ok"]]);
});

/** A pi step whose main transcript holds the recorded subagent call and
 *  whose capsule holds the recorder's output for it. */
function piCapsule(t, wi, { finished = true } = {}) {
  const c = capsule(t, wi);
  const transcript = join(c.dir, "session", "session-tok.jsonl");
  writeFileSync(transcript, jsonl([
    { type: "session", id: "s", timestamp: "2026-11-02T13:24:40.000Z" },
    { type: "message", id: "m1", timestamp: "2026-11-02T13:24:44.000Z", message: { role: "assistant", content: [{ type: "toolCall", id: PI_CALL, name: "subagent", arguments: { agent: "merge-conflict-reviewer", task: "Review the merge resolution of one conflicted file and report whether either side is lost." } }] } },
    ...(finished ? [{ type: "message", id: "m2", timestamp: "2026-11-02T13:25:51.818Z", message: { role: "toolResult", toolCallId: PI_CALL, toolName: "subagent", isError: false, content: [{ type: "text", text: "done" }] } }] : []),
  ]));
  cpSync(new URL("session-tok/", PI), join(c.dir, "session", "session-tok"), { recursive: true });
  if (!finished) {
    const metaPath = join(c.dir, "session", "session-tok", "subagents", `${PI_CALL}-0.meta.json`);
    const meta = JSON.parse(readFileSync(metaPath, "utf8"));
    writeFileSync(metaPath, JSON.stringify({ ...meta, finished: false, exitCode: undefined, isError: undefined }));
  }
  c.journal([{ step_id: "review", spawn_n: 1, started_at: "2026-11-02T13:24:40.000Z", harness: "pi", transcript }]);
  return { ...c, transcript };
}

test(" AC3/AC5: a pi step's subagent call carries the recorded child conversation — every message, its tool calls paired", async (t) => {
  const c = piCapsule(t, "wi_sub424c");
  const snapshot = await lib.readProgressSnapshot(c.root, c.dir, "wi_sub424c");
  const call = allTools(snapshot.lines).find((line) => line.tool.name === "subagent");
  assert.equal(call.subagents.length, 1);
  const [run] = call.subagents;
  assert.equal(run.agent, "merge-conflict-reviewer");
  assert.ok(run.description.startsWith("Review the merge resolution"));
  assert.equal(run.status, "done");
  assert.equal(run.startAt, Date.parse("2026-11-02T13:24:44.435Z"), "the earliest record time (the user prompt)");
  assert.ok(run.durationMs > 0);
  const tools = allTools(run.lines);
  assert.ok(tools.length >= 2, "the child's tool calls are lines");
  assert.ok(tools.every((line) => line.tool.name === "bash" && line.tool.status === "ok"), "each paired with its recorded result");
  assert.ok(run.lines.some((line) => line.kind === "narration" && line.variant === "thinking"), "the child's thinking is narration");
  const detail = await lib.readProgressToolDetail(c.root, c.dir, tools[0].id);
  assert.equal(detail.available, true);
  assert.equal(detail.tool.input.includes("git status --short"), true);
});

test(" AC5: a pi subagent recorded mid-run is running; a malformed or unattached record is no entry", async (t) => {
  const c = piCapsule(t, "wi_sub424d", { finished: false });
  const sub = join(c.dir, "session", "session-tok", "subagents");
  writeFileSync(join(sub, "orphan-0.jsonl"), "{}\n"); // no meta: cannot be attached
  writeFileSync(join(sub, "other-0.jsonl"), "{}\n");
  writeFileSync(join(sub, "other-0.meta.json"), JSON.stringify({ toolUseId: "call_not_in_any_transcript" }));
  const snapshot = await lib.readProgressSnapshot(c.root, c.dir, "wi_sub424d");
  const call = allTools(snapshot.lines).find((line) => line.tool.name === "subagent");
  assert.equal(call.tool.status, "running");
  assert.equal(call.subagents.length, 1);
  assert.equal(call.subagents[0].status, "running");
  assert.equal(snapshot.lines.flatMap((line) => line.subagents ?? []).length, 1, "nothing else attached");
});

/** A pi step whose subagent call `PI_CALL` has the given parent result
 *  (`null`: still running) and the given recorded children (meta overrides
 *  per index; each child's transcript is the recorder fixture's). */
function piCall(t, wi, { result, children }) {
  const c = piCapsule(t, wi);
  writeFileSync(c.transcript, jsonl([
    { type: "session", id: "s", timestamp: "2026-11-02T13:24:40.000Z" },
    { type: "message", id: "m1", timestamp: "2026-11-02T13:24:44.000Z", message: { role: "assistant", content: [{ type: "toolCall", id: PI_CALL, name: "subagent", arguments: { chain: [] } }] } },
    ...(result ? [{ type: "message", id: "m2", timestamp: "2026-11-02T13:25:51.818Z", message: { role: "toolResult", toolCallId: PI_CALL, toolName: "subagent", isError: result === "error", content: [{ type: "text", text: "r" }] } }] : []),
  ]));
  const sub = join(c.dir, "session", "session-tok", "subagents");
  const body = readFileSync(join(sub, `${PI_CALL}-0.jsonl`));
  const meta = JSON.parse(readFileSync(join(sub, `${PI_CALL}-0.meta.json`), "utf8"));
  children.forEach((over, index) => {
    writeFileSync(join(sub, `${PI_CALL}-${index}.jsonl`), body);
    const merged = { ...meta, index, ...over };
    for (const key of Object.keys(merged)) if (merged[key] === undefined) delete merged[key];
    writeFileSync(join(sub, `${PI_CALL}-${index}.meta.json`), JSON.stringify(merged));
  });
  return c;
}
const runsOf = async (c, wi) => (await lib.readProgressSnapshot(c.root, c.dir, wi)).lines.flatMap((line) => line.subagents ?? []);

test(" review: a chain stopped by its failed last step keeps the earlier, successful steps done", async (t) => {
  const c = piCall(t, "wi_sub424g", { result: "error", children: [{ finished: true, isError: false, exitCode: 0 }, { finished: true, isError: true, exitCode: 0 }] });
  assert.deepEqual((await runsOf(c, "wi_sub424g")).map((run) => run.status), ["done", "error"]);
});

test(" review: a pi child the recorder never saw finish (the 4 MiB stdout bound) takes its ended parent call's ok/error", async (t) => {
  const unfinished = { finished: false, isError: undefined, exitCode: undefined };
  const ok = piCall(t, "wi_sub424h", { result: "ok", children: [unfinished] });
  const [done] = await runsOf(ok, "wi_sub424h");
  assert.equal(done.status, "done");
  assert.equal(done.durationMs, Date.parse("2026-11-02T13:25:51.818Z") - Date.parse("2026-11-02T13:24:44.000Z"), "the parent call's own duration");
  const failed = piCall(t, "wi_sub424i", { result: "error", children: [unfinished] });
  assert.equal((await runsOf(failed, "wi_sub424i"))[0].status, "error");
  const running = piCall(t, "wi_sub424j", { result: null, children: [unfinished] });
  assert.equal((await runsOf(running, "wi_sub424j"))[0].status, "running", "a parent still running keeps it running");
});

test(" review: subagent bytes past the job bound drop that subagent's detail, never the main view", async (t) => {
  const c = piCall(t, "wi_sub424k", { result: "ok", children: [{}, {}] });
  truncateSync(join(c.dir, "session", "session-tok", "subagents", `${PI_CALL}-0.jsonl`), 128 * 1024 * 1024 + 1);
  const snapshot = await lib.readProgressSnapshot(c.root, c.dir, "wi_sub424k");
  assert.equal(snapshot.available, true, "the main view stays");
  const call = allTools(snapshot.lines).find((line) => line.tool.name === "subagent");
  assert.ok(call, "the main transcript's call is still a line");
  const [big, small] = call.subagents;
  assert.equal(big.omitted, true, "the oversized subagent is listed with its detail omitted");
  assert.deepEqual(big.lines, []);
  assert.equal(small.omitted, undefined);
  assert.ok(allTools(small.lines).length >= 2, "a subagent that fits keeps its detail");
});

test("a subagent directory or file reaching outside the capsule and data root is never read", async (t) => {
  const c = piCapsule(t, "wi_sub424e");
  const outside = mkdtempSync(join(tmpdir(), "pf-sub424-outside-"));
  t.after(() => rmSync(outside, { recursive: true, force: true }));
  cpSync(join(c.dir, "session", "session-tok", "subagents"), outside, { recursive: true });
  rmSync(join(c.dir, "session", "session-tok"), { recursive: true });
  mkdirSync(join(c.dir, "session", "session-tok"));
  symlinkSync(outside, join(c.dir, "session", "session-tok", "subagents"));
  const snapshot = await lib.readProgressSnapshot(c.root, c.dir, "wi_sub424e");
  assert.equal(snapshot.lines.flatMap((line) => line.subagents ?? []).length, 0);
});

test("a subagent's description is credential-masked", async (t) => {
  const c = piCapsule(t, "wi_sub424f");
  const metaPath = join(c.dir, "session", "session-tok", "subagents", `${PI_CALL}-0.meta.json`);
  const meta = JSON.parse(readFileSync(metaPath, "utf8"));
  writeFileSync(metaPath, JSON.stringify({ ...meta, description: "deploy with token=SECRET-TOKEN" }));
  const snapshot = await lib.readProgressSnapshot(c.root, c.dir, "wi_sub424f");
  assert.ok(!JSON.stringify(snapshot).includes("SECRET-TOKEN"));
  assert.equal(snapshot.lines.flatMap((line) => line.subagents ?? [])[0].description, "deploy with token=***");
});

test("the client keeps a snapshot's subagents through the wire sanitizer and refuses a malformed one", () => {
  const sub = { id: "abc", description: "d", agent: "a", status: "running", startAt: 1, lines: [{ id: "n", kind: "narration", text: "x" }, { id: "t", kind: "tool", text: "Read: f", tool: { name: "Read", status: "ok" } }], extra: 1 };
  const body = { available: true, omitted: 0, lines: [{ id: "1", kind: "tool", text: "Agent", tool: { name: "Agent", status: "running" }, subagents: [sub] }] };
  const next = pure.nextProgressSnapshot(null, body);
  assert.deepEqual(next.lines[0].subagents, [{ id: "abc", description: "d", agent: "a", status: "running", startAt: 1, lines: sub.lines }], "wire fields only");
  const held = next;
  for (const bad of [
    { ...sub, status: "weird" },
    { ...sub, lines: [{ id: "e", kind: "event", text: "x" }] },
    { ...sub, durationMs: -1 },
    { ...sub, description: 3 },
  ]) {
    assert.equal(pure.nextProgressSnapshot(held, { ...body, lines: [{ ...body.lines[0], subagents: [bad] }] }), held, "a malformed subagent keeps the last good snapshot");
  }
  assert.equal(pure.nextProgressSnapshot(held, { ...body, lines: [{ id: "n", kind: "narration", text: "x", subagents: [sub] }] }), held, "only a tool line carries subagents");
});

test("a subagent entry's header — running age live, recorded duration once done, stopped when its block ended mid-run", () => {
  const run = { id: "x", description: "  read\n note  ", status: "running", startAt: 1_000, lines: [{ id: "t", kind: "tool", text: "Read", tool: { name: "Read", status: "ok" } }] };
  assert.deepEqual(pure.progressSubagentHeader(run, 6_000, true), { description: "read note", state: "running", durationMs: 5_000, tools: 1 });
  assert.deepEqual(pure.progressSubagentHeader(run, 6_000, false), { description: "read note", state: "stopped", tools: 1 }, "never finishes: no live age");
  assert.deepEqual(pure.progressSubagentHeader({ ...run, status: "done", durationMs: 1_234 }, 9_000, false), { description: "read note", state: "done", durationMs: 1_234, tools: 1 });
  assert.deepEqual(pure.progressSubagentHeader({ ...run, startAt: undefined }, 6_000, true), { description: "read note", state: "running", tools: 1 });
});

test(" review: the wire keeps an omitted subagent's mark and refuses a malformed one", () => {
  const sub = { id: "abc", description: "d", status: "done", lines: [], omitted: true };
  const body = { available: true, omitted: 0, lines: [{ id: "1", kind: "tool", text: "Agent", tool: { name: "Agent", status: "ok" }, subagents: [sub] }] };
  const next = pure.nextProgressSnapshot(null, body);
  assert.deepEqual(next.lines[0].subagents, [sub]);
  assert.equal(pure.nextProgressSnapshot(next, { ...body, lines: [{ ...body.lines[0], subagents: [{ ...sub, omitted: "yes" }] }] }), next);
});
