import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

// Unit tests for the claude session list projection (polyforge#140 u2): the
// list row needs only the transcript's adapter-projected message count, so the
// transcript is parsed once per (path, size, mtime) on the worker pool, a cold
// summary defers its count (owner ruling 1-A: detailsPending, firstMessage from
// the index), and a normal listing's rows stay field-for-field what reading the
// whole history produced.

process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-list-"));
const agentDir = path.join(scratch, "agent");
const configDir = path.join(scratch, "claude-config");
process.env.PI_CODING_AGENT_DIR = agentDir;
process.env.CLAUDE_CONFIG_DIR = configDir;

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { getClaudeSessionInfos, resetClaudeSessionListCachesForTests } = await jiti.import("./claude-session.ts");
const { runSessionScanTask } = await jiti.import("./session-parse-worker-pool.ts");
const { readClaudeHistory } = await jiti.import("./claude-session.ts");
const { claudeTranscriptToMessages, parseClaudeTranscript } = await jiti.import("./claude-adapter.ts");
const index = await jiti.import("./claude-index.ts");

test.after(() => {
  fs.rmSync(scratch, { recursive: true, force: true });
});

const RECORDED = "5b203fcd-56c9-4d0c-b700-749b82c41afb";
const SESSION = "44444444-4444-4444-8444-444444444444";
const projects = path.join(configDir, "projects", "-tmp-project");
const transcriptOf = (id) => path.join(projects, `${id}.jsonl`);
const transcriptText = fs.readFileSync(new URL("./__fixtures__/claude-2.1.312/transcript.jsonl", import.meta.url), "utf8");
const usage = { input: 6, output: 283, cacheRead: 840, cacheWrite: 1102, cost: 0.00028825 };

// The transcript's own adapter projection, derived independently of the list
// path: this is the count the list row must carry (the acceptance's oracle).
function projectedCount(text) {
  return claudeTranscriptToMessages(parseClaudeTranscript(text), {}).messages.length;
}

async function registerSession(id, { firstMessage = `first of ${id}` } = {}) {
  fs.mkdirSync(projects, { recursive: true });
  fs.writeFileSync(transcriptOf(id), transcriptText.replaceAll(RECORDED, id));
  await index.upsertClaudeSessionRecord({
    id,
    cwd: path.join(scratch, "project"),
    model: "claude-sonnet-5-5",
    created: "2026-11-01T00:00:00.000Z",
    modified: "2026-11-01T00:00:00.000Z",
    firstMessage,
    usage,
  });
}

const readTasks = () => (globalThis.__piWebScanTaskLog ??= []);
const claudeParseTasks = () => readTasks().filter((entry) => entry.kind === "claude-count");

test("a normal list parses each transcript once on the pool and carries the adapter's count", async () => {
  resetClaudeSessionListCachesForTests();
  globalThis.__piWebScanTaskLog = [];
  await registerSession(SESSION);
  const expectedCount = projectedCount(transcriptText.replaceAll(RECORDED, SESSION));

  const rows = await getClaudeSessionInfos();
  const row = rows.find((session) => session.id === SESSION);
  assert.equal(row.messageCount, expectedCount, "the adapter's own projection of the transcript");
  assert.equal(row.detailsPending, undefined);
  assert.equal(row.firstMessage, `first of ${SESSION}`);
  assert.equal(row.path, transcriptOf(SESSION));
  assert.equal(row.harness, "claude");
  assert.equal(claudeParseTasks().length, 1, "the transcript was parsed once, as a pool task");
  assert.equal(claudeParseTasks()[0].path, transcriptOf(SESSION));

  // Unchanged transcript: the count comes from the fingerprint cache, not a re-read.
  globalThis.__piWebScanTaskLog = [];
  const again = await getClaudeSessionInfos();
  assert.equal(again.find((session) => session.id === SESSION).messageCount, expectedCount);
  assert.equal(claudeParseTasks().length, 0, "an unchanged transcript is not re-parsed");
});

test("a cold summary defers the count; the normal listing hydrates it", async () => {
  resetClaudeSessionListCachesForTests();
  globalThis.__piWebScanTaskLog = [];
  await registerSession(SESSION);

  const summary = await getClaudeSessionInfos({ summary: true });
  const row = summary.find((session) => session.id === SESSION);
  assert.equal(row.messageCount, 0, "no transcript parsed for the first paint");
  assert.equal(row.detailsPending, true);
  assert.equal(row.firstMessage, `first of ${SESSION}`, "the index's own first message still paints");
  assert.equal(row.path, transcriptOf(SESSION));
  assert.equal(claudeParseTasks().length, 0, "the summary request reads no transcript");

  const detailed = await getClaudeSessionInfos();
  const detailedRow = detailed.find((session) => session.id === SESSION);
  assert.equal(detailedRow.messageCount, projectedCount(transcriptText.replaceAll(RECORDED, SESSION)));
  assert.equal(detailedRow.detailsPending, undefined);
});

test("the projected count equals the full history's, sidecars included", async () => {
  resetClaudeSessionListCachesForTests();
  const SUB = "55555555-5555-4555-8555-555555555555";
  await registerSession(SUB, { firstMessage: "sub" });
  const recording = new URL("./__fixtures__/claude-2.1.312/subagent/", import.meta.url);
  fs.mkdirSync(path.join(projects, SUB, "subagents"), { recursive: true });
  for (const name of ["agent-a9fb2ff3aa8e8ff40.jsonl", "agent-a9fb2ff3aa8e8ff40.meta.json"]) {
    fs.copyFileSync(new URL(`subagents/${name}`, recording), path.join(projects, SUB, "subagents", name));
  }

  const history = readClaudeHistory(SUB);
  const rows = await getClaudeSessionInfos();
  assert.equal(rows.find((session) => session.id === SUB).messageCount, history.messages.length,
    "skipping the sidecars for the list costs no messages: they feed subagents, not the count");
});

test("the projected count is computed in the worker and only the count crosses the thread", async (t) => {
  // The count is claudeTranscriptToMessages()'s own: the worker source ports the
  // adapter's branch and emission rules, and this cross-check runs both over
  // every claude transcript fixture the repo holds (compaction, model switches,
  // subagents and their sidecars, absorbed and queued prompts, task notes,
  // edits, relays) plus generated corpus transcripts with huge single lines.
  const { execFileSync } = await import("node:child_process");
  const corpus = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-count-check-"));
  t.after(() => fs.rmSync(corpus, { recursive: true, force: true }));
  execFileSync(process.execPath, [
    path.join(process.cwd(), "scripts", "session-list-fixture.mjs"),
    "--root", corpus, "--pi-files", "1", "--pi-bytes", "10000",
    "--claude-files", "3", "--claude-bytes", "120000",
    "--large-file-min-bytes", "30000", "--manifest", path.join(corpus, "manifest.json"),
  ], { encoding: "utf8" });
  const fixtureRoot = new URL("./__fixtures__/", import.meta.url);
  const transcripts = fs.readdirSync(fixtureRoot, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile() && (entry.name === "transcript.jsonl" || /^agent-.*[.]jsonl$/.test(entry.name)))
    .map((entry) => path.join(entry.parentPath ?? fixtureRoot.pathname, entry.name));
  const corpusFiles = JSON.parse(fs.readFileSync(path.join(corpus, "manifest.json"), "utf8")).claude
    .map((entry) => path.join(corpus, entry.path));
  // Synthetic edge shapes the fixtures lack (review r2 N1): an assistant
  // message whose content is only an image block (the adapter's
  // toAssistantBlocks has no image branch, so it emits nothing) and a
  // tool_use whose id is the empty string (the adapter requires only a
  // string id). The worker port must count exactly what the adapter emits.
  // Each edge shape in its own transcript: one file per divergence, so one
  // cannot cancel the other out (the first red run taught us they do).
  const edgeDir = path.join(configDir, "projects", "-edge");
  fs.mkdirSync(edgeDir, { recursive: true });
  const edgeTranscripts = [];
  for (const [suffix, content] of [
    ["image", [{ type: "image", source: { type: "base64", data: "aGk=", media_type: "image/png" } }]],
    ["empty-tool-id", [{ type: "tool_use", id: "", name: "Read", input: {} }]],
  ]) {
    const id = `synthetic-port-edge-${suffix}`;
    const file = path.join(edgeDir, `${id}.jsonl`);
    const entry = (row) => `${JSON.stringify({ isSidechain: false, timestamp: "2026-11-01T00:00:00.000Z", sessionId: id, cwd: "/tmp", version: "2.1.312", ...row })}\n`;
    fs.writeFileSync(file,
      entry({ parentUuid: null, type: "user", message: { role: "user", content: "one question" }, uuid: "e1" })
      + entry({ parentUuid: "e1", type: "assistant", message: { role: "assistant", content }, uuid: "e2" })
      + `${JSON.stringify({ type: "last-prompt", leafUuid: "e2", sessionId: id })}\n`);
    edgeTranscripts.push(file);
  }
  assert.ok(transcripts.length >= 15, `the repo's claude transcript fixtures (${transcripts.length})`);
  // Collect every divergence, not just the first: an early assert would hide
  // the rest of the corpus behind whichever file happens to differ first
  // (review r3 N5).
  const diverged = [];
  for (const file of [...transcripts, ...corpusFiles, ...edgeTranscripts]) {
    const text = fs.readFileSync(file, "utf8");
    const expected = claudeTranscriptToMessages(parseClaudeTranscript(text), {}).messages.length;
    const result = await runSessionScanTask({ kind: "claude-count", path: file }).catch((error) => error);
    if (typeof result !== "number" || result !== expected) diverged.push({ file, expected, result });
  }
  assert.deepEqual(diverged, [], "the worker's count is the adapter's own projection for every fixture");
});

test("an appended, deleted and recreated transcript is followed without force", async () => {
  resetClaudeSessionListCachesForTests();
  globalThis.__piWebScanTaskLog = [];
  await registerSession(SESSION);
  const before = (await getClaudeSessionInfos()).find((session) => session.id === SESSION);

  // An external append changes the fingerprint; the next ordinary list re-projects.
  const appended = transcriptText.replaceAll(RECORDED, SESSION)
    + JSON.stringify({
      parentUuid: "77d4bcd6-0c8e-4f31-aaa5-345a6aaef1f3", isSidechain: false, type: "user",
      message: { role: "user", content: "one more question" },
      uuid: "8834abcd-1111-4222-8333-444444444444", timestamp: "2026-11-01T00:00:05.000Z",
      sessionId: SESSION,
    }) + "\n";
  fs.writeFileSync(transcriptOf(SESSION), appended);
  const grown = (await getClaudeSessionInfos()).find((session) => session.id === SESSION);
  assert.equal(grown.messageCount, projectedCount(appended),
    "the appended transcript is re-projected, and the count is the adapter's own");
  assert.ok(grown.messageCount > before.messageCount, `the append added a message (${grown.messageCount} > ${before.messageCount})`);

  // A deleted transcript keeps the index row with an empty path and a zero count (3-A).
  fs.rmSync(transcriptOf(SESSION));
  const gone = (await getClaudeSessionInfos()).find((session) => session.id === SESSION);
  assert.equal(gone.path, "");
  assert.equal(gone.messageCount, 0);

  // Recreated elsewhere in the projects dir: the row follows it again.
  const other = path.join(configDir, "projects", "-other-project");
  fs.mkdirSync(other, { recursive: true });
  fs.writeFileSync(path.join(other, `${SESSION}.jsonl`), transcriptText.replaceAll(RECORDED, SESSION));
  const back = (await getClaudeSessionInfos()).find((session) => session.id === SESSION);
  assert.equal(back.path, path.join(other, `${SESSION}.jsonl`));
  assert.equal(back.messageCount, before.messageCount);
});
