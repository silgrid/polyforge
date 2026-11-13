import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-sessions-"));
const agentDir = path.join(scratch, "agent");
const project = path.join(scratch, "project");
fs.mkdirSync(project, { recursive: true });
process.env.PI_CODING_AGENT_DIR = agentDir;
// The inherited claude config dir the transcripts live under.
const configDir = path.join(scratch, "claude-config");
process.env.CLAUDE_CONFIG_DIR = configDir;

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const list = await jiti.import("./route.ts");
const detail = await jiti.import("./[id]/route.ts");
const stats = await jiti.import("./stats/route.ts");
const index = await jiti.import("../../../lib/claude-index.ts");

test.after(() => fs.rmSync(scratch, { recursive: true, force: true }));

const RECORDED = "5b203fcd-56c9-4d0c-b700-749b82c41afb";
const MINE = "11111111-1111-4111-8111-111111111111";
const OTHER = "22222222-2222-4222-8222-222222222222";
const OUTSIDER = "33333333-3333-4333-8333-333333333333";
const transcriptText = fs.readFileSync(new URL("../../../lib/__fixtures__/claude-2.1.312/transcript.jsonl", import.meta.url), "utf8");
const projects = path.join(configDir, "projects", "-tmp-project");
const transcriptOf = (id) => path.join(projects, `${id}.jsonl`);
fs.mkdirSync(projects, { recursive: true });
for (const id of [MINE, OTHER, OUTSIDER]) fs.writeFileSync(transcriptOf(id), transcriptText.replaceAll(RECORDED, id));

const usage = { input: 6, output: 283, cacheRead: 840, cacheWrite: 1102, cost: 0.00028825 };
for (const id of [MINE, OTHER]) {
  await index.upsertClaudeSessionRecord({
    id,
    cwd: project,
    model: "claude-sonnet-5-5",
    created: "2026-11-01T00:00:00.000Z",
    modified: "2026-11-01T00:00:00.000Z",
    firstMessage: `first of ${id}`,
    usage,
  });
}

const params = (id) => ({ params: Promise.resolve({ id }) });
const getList = async (query) => (await list.GET(new Request(`http://localhost/api/sessions${query}`))).json();
const patch = (id, body) => detail.PATCH(new Request("http://localhost", { method: "PATCH", body: JSON.stringify(body) }), params(id));

test("the list carries the index's claude rows with their harness marker, never a transcript outside the index", async () => {
  for (const query of ["?summary=1", ""]) {
    const { sessions } = await getList(query);
    const ids = sessions.map((session) => session.id);
    assert.ok(ids.includes(MINE) && ids.includes(OTHER), query);
    assert.ok(!ids.includes(OUTSIDER), "a claude transcript pi-web did not create is not listed");
    const row = sessions.find((session) => session.id === MINE);
    assert.equal(row.harness, "claude");
    assert.deepEqual(row.claude, { model: "claude-sonnet-5-5", effort: "default" });
    assert.equal(row.cwd, project);
    assert.equal(row.firstMessage, `first of ${MINE}`);
    assert.equal(row.messageCount, 4);
    assert.equal(row.path, transcriptOf(MINE));
    assert.ok(row.projectKey);
  }
});

test("the detail renders history from claude's transcript and writes no parallel log", async () => {
  const response = await detail.GET(new Request(`http://localhost/api/sessions/${MINE}?tree=summary`), params(MINE));
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.info.harness, "claude");
  assert.deepEqual(body.context.messages.map((message) => message.role), ["user", "assistant", "toolResult", "assistant"]);
  assert.deepEqual(body.context.messages[1].content[0], {
    type: "toolCall",
    toolCallId: "toolu_019r4f1eeY8DPjUCUz7UJ7pU",
    toolName: "Read",
    input: { file_path: "/tmp/claude-rec/work/notes.txt" },
  });
  assert.equal(body.context.entryIds.length, 4);
  // the recording's one prompt opens the conversation (parentUuid null), so
  // claude has nothing to keep before it: no in-session edit starts there.
  assert.deepEqual(body.claudeEditEntryIds, []);
  assert.equal(body.context.hasMore, false);
  assert.deepEqual(body.context.model, { provider: "claude", modelId: "claude-sonnet-5-5" });
  assert.equal(body.snapshotRevision, null);
  assert.deepEqual(body.toolNames, [], "claude's tools are the user's claude config's, not pi-web's");
  assert.equal(fs.existsSync(path.join(agentDir, "sessions")), false, "nothing mirrored into the pi sessions store");
});

test("the detail carries the subagent runs rebuilt from claude's sidecars; a session without any carries none", async () => {
  const plain = await (await detail.GET(new Request(`http://localhost/api/sessions/${MINE}`), params(MINE))).json();
  assert.deepEqual(plain.claudeSubagents, []);
  const SUB = "55555555-5555-4555-8555-555555555555";
  const recording = new URL("../../../lib/__fixtures__/claude-2.1.312/subagent/", import.meta.url);
  fs.copyFileSync(new URL("transcript.jsonl", recording), transcriptOf(SUB));
  fs.mkdirSync(path.join(projects, SUB, "subagents"), { recursive: true });
  for (const name of ["agent-a9fb2ff3aa8e8ff40.jsonl", "agent-a9fb2ff3aa8e8ff40.meta.json"]) {
    fs.copyFileSync(new URL(`subagents/${name}`, recording), path.join(projects, SUB, "subagents", name));
  }
  await index.upsertClaudeSessionRecord({ id: SUB, cwd: project, model: "claude-haiku-5-5", created: "2026-11-02T00:00:00.000Z", modified: "2026-11-02T00:00:00.000Z", firstMessage: "sub", usage });
  const body = await (await detail.GET(new Request(`http://localhost/api/sessions/${SUB}`), params(SUB))).json();
  assert.equal(body.claudeSubagents.length, 1);
  assert.equal(body.claudeSubagents[0].toolCallId, "toolu_01F1YZd2Gseivs4hckhQVw5s");
  assert.equal(body.claudeSubagents[0].entries.length, 3);
  assert.ok(!JSON.stringify(body.context.messages).includes("toolu_012N7brUKegpGiA36sdi1ApH"), "the sidecar's Read is not a main message");
  assert.ok(!body.context.messages.some((message) => message.role === "user" && JSON.stringify(message).includes("<task-notification>")));
});

test("usage on every face is the result aggregate, not the streamed per-message sum", async () => {
  const body = await (await detail.GET(new Request(`http://localhost/api/sessions/${MINE}`), params(MINE))).json();
  assert.deepEqual(body.stats.tokens, { input: 6, output: 283, cacheRead: 840, cacheWrite: 1102, total: 2231 });
  assert.equal(body.stats.cost, 0.00028825);
  assert.equal(body.stats.toolCalls, 1);
  const summary = await (await stats.POST(new Request("http://localhost/api/sessions/stats", {
    method: "POST",
    headers: { "Content-Type": "application/json", Host: "localhost", Origin: "http://localhost" },
    body: JSON.stringify({ ids: [MINE] }),
  }))).json();
  assert.equal(summary.stats[MINE].tokens.output, 283);
  assert.notEqual(summary.stats[MINE].tokens.output, 2 + 5, "not the streamed per-message output tokens");
  assert.deepEqual(summary.stats[MINE].model, { provider: "claude", modelId: "claude-sonnet-5-5" });
});

test("a legacy #219 record (alias + preset) is listed with its alias as the model claude takes and no preset", async () => {
  const legacy = "55555555-5555-4555-8555-555555555555";
  const file = index.claudeIndexPath();
  const before = fs.readFileSync(file, "utf8");
  const parsed = JSON.parse(before);
  parsed.sessions.push({ id: legacy, cwd: project, modelAlias: "haiku", preset: "writable", created: "2026-11-01T00:00:00.000Z" });
  fs.writeFileSync(file, JSON.stringify(parsed));
  try {
    const row = (await getList("?summary=1")).sessions.find((session) => session.id === legacy);
    assert.ok(row, "the legacy record is still listed");
    assert.deepEqual(row.claude, { model: "haiku", effort: "default" }, "a pre-#272 record reads as the default effort");
  } finally {
    fs.writeFileSync(file, before);
  }
});

test("rename and archive persist in the index and the row follows", async () => {
  assert.equal((await patch(MINE, { name: "Renamed claude" })).status, 200);
  assert.equal((await patch(MINE, { archived: true })).status, 200);
  assert.equal((await patch(MINE, {})).status, 400);
  assert.equal(index.getClaudeSessionRecord(MINE).name, "Renamed claude");
  assert.equal(index.getClaudeSessionRecord(MINE).archived, true);
  let row = (await getList("?summary=1")).sessions.find((session) => session.id === MINE);
  assert.equal(row.name, "Renamed claude");
  assert.equal(row.archived, true);
  assert.equal((await patch(MINE, { archived: false })).status, 200);
  row = (await getList("?summary=1")).sessions.find((session) => session.id === MINE);
  assert.equal(row.archived, undefined);
});

test("delete removes the index entry and only that session's transcript", async () => {
  assert.equal((await detail.DELETE(new Request("http://localhost", { method: "DELETE" }), params(MINE))).status, 200);
  assert.equal(index.getClaudeSessionRecord(MINE), undefined);
  assert.equal(fs.existsSync(transcriptOf(MINE)), false);
  assert.ok(fs.existsSync(transcriptOf(OTHER)));
  assert.ok(fs.existsSync(transcriptOf(OUTSIDER)));
  assert.ok(index.getClaudeSessionRecord(OTHER));
  assert.equal((await detail.GET(new Request("http://localhost"), params(MINE))).status, 404);
});

test("a pi id still goes through the pi routes", async () => {
  const id = "44444444-4444-4444-8444-444444444444";
  assert.equal((await detail.GET(new Request("http://localhost"), params(id))).status, 404);
  assert.equal((await patch(id, { name: "x" })).status, 404);
});
