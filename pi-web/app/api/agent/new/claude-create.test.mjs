import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { createJiti } from "jiti";
import { readStubLog } from "../../../../lib/__fixtures__/claude-stub-log.mjs";

// Under os.tmpdir(), registrable through the operator prefix (as route.test.mjs does).
process.env.PI_WEB_ALLOWED_ROOT_PREFIXES = os.tmpdir();
process.env.PI_WEB_IDLE_TIMEOUT_MS = "0";
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-create-"));
const agentDir = path.join(scratch, "agent");
const binDir = path.join(scratch, "bin");
const project = path.join(scratch, "project");
const stubLog = path.join(scratch, "stub.jsonl");
fs.mkdirSync(binDir);
fs.mkdirSync(project);
fs.copyFileSync(new URL("../../../../lib/__fixtures__/claude-stub/claude.mjs", import.meta.url), path.join(binDir, "claude"));
fs.chmodSync(path.join(binDir, "claude"), 0o755);
fs.symlinkSync(decodeURIComponent(new URL("../../../../lib/__fixtures__/claude-2.1.312", import.meta.url).pathname), path.join(scratch, "claude-2.1.312"));
process.env.PI_CODING_AGENT_DIR = agentDir;
// The inherited claude config dir the stub writes its transcripts under: never the operator's.
process.env.CLAUDE_CONFIG_DIR = path.join(scratch, "claude-config");
const originalPath = process.env.PATH;
process.env.PATH = `${binDir}${path.delimiter}${originalPath}`;
process.env.PI_TEST_CLAUDE_STUB_LOG = stubLog;
process.env.PI_TEST_CLAUDE_STUB_MODE = "replay";

const startRpcCalls = [];
const jiti = createJiti(import.meta.url, {
  alias: { "@": process.cwd() },
  interopDefault: true,
  moduleCache: false,
  virtualModules: {
    "@/lib/rpc-manager": {
      startRpcSession: async (key, _id, cwd) => {
        startRpcCalls.push({ key, cwd });
        return { session: { send: async () => ({ model: { id: "m", provider: "p" }, thinkingLevel: "off" }) }, realSessionId: "pi-session" };
      },
    },
  },
});
const { POST } = await jiti.import("./route.ts");
const index = await jiti.import("../../../../lib/claude-index.ts");
const claude = await jiti.import("../../../../lib/claude-session.ts");

test.after(async () => {
  await claude.shutdownAllClaudeSessions();
  process.env.PATH = originalPath;
  fs.rmSync(scratch, { recursive: true, force: true });
});

const post = (body) => POST(new Request("http://localhost/api/agent/new", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
}));
const stubRecords = () => readStubLog(stubLog);
const launches = () => stubRecords().filter((entry) => entry.kind === "launch");

async function waitFor(predicate, timeoutMs, message) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = predicate();
    if (value) return value;
    await delay(25);
  }
  assert.fail(message);
}

test("an unregistrable cwd is refused with the pi route's reason before any side effect", async () => {
  const response = await post({ cwd: path.parse(path.resolve(os.tmpdir())).root, type: "prompt", message: "hi", harness: "claude", model: "claude-sonnet-5-5" });
  assert.equal(response.status, 403);
  const body = await response.json();
  assert.equal(body.reason, "outsideRegistrationPrefix");
  assert.equal(body.code, "prompt_rejected");
  assert.equal(fs.existsSync(agentDir), false, "no config dir, no index");
  assert.equal(launches().length, 0, "no claude process");
  assert.equal(startRpcCalls.length, 0);
});

test("the model must be a valid claude model name (no fixed list); other commands are refused for claude", async () => {
  for (const body of [
    { harness: "claude" },
    { harness: "claude", model: "" },
    { harness: "claude", model: "-bad" },
    { harness: "claude", model: "has space" },
    { harness: "claude", model: "x".repeat(129) },
    { harness: "claude", model: 5 },
    { harness: "claude", modelAlias: "sonnet" },
    { harness: "codex", model: "claude-sonnet-5-5" },
    { harness: "claude", model: "claude-sonnet-5-5", type: "bash", command: "ls" },
  ]) {
    const response = await post({ cwd: project, type: "prompt", message: "hi", ...body });
    assert.equal(response.status, 400, JSON.stringify(body));
  }
  assert.equal(fs.existsSync(agentDir), false);
  assert.equal(launches().length, 0);
});

test("claude missing from PATH is refused without an index entry or a config dir", async () => {
  process.env.PATH = path.join(scratch, "empty");
  try {
    const response = await post({ cwd: project, type: "prompt", message: "hi", harness: "claude", model: "claude-sonnet-5-5" });
    assert.equal(response.status, 400);
    assert.match((await response.json()).error, /claude was not found on PATH/);
  } finally {
    process.env.PATH = `${binDir}${path.delimiter}${originalPath}`;
  }
  assert.equal(fs.existsSync(agentDir), false);
});

test("a claude create writes the index entry (full name, no preset, no config dir), spawns --session-id with the reduced argv and sends the prompt", async () => {
  const response = await post({ cwd: project, type: "prompt", message: "first words", harness: "claude", model: "claude-haiku-5-5" });
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.success, true);
  assert.deepEqual(body.model, { provider: "claude", modelId: "claude-haiku-5-5" });
  const record = index.getClaudeSessionRecord(body.sessionId);
  assert.equal(record.cwd, fs.realpathSync(project));
  assert.equal(record.model, "claude-haiku-5-5");
  assert.equal(record.firstMessage, "first words");
  const stored = JSON.parse(fs.readFileSync(index.claudeIndexPath(), "utf8")).sessions.find((entry) => entry.id === body.sessionId);
  assert.ok(!("preset" in stored) && !("modelAlias" in stored), "no preset or alias is stored");
  assert.equal(fs.existsSync(path.join(agentDir, "claude")), false, "no dedicated config dir is created");
  const launch = await waitFor(() => launches()[0], 5_000, "no claude launch");
  assert.deepEqual(launch.argv, [
    "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--forward-subagent-text",
    "--model", "claude-haiku-5-5", "--permission-prompts", "host", "--permission-prompt-tool", "stdio", "--session-id", body.sessionId,
  ]);
  const stdin = await waitFor(
    () => stubRecords().find((entry) => entry.kind === "stdin"),
    5_000,
    "the prompt never reached stdin",
  );
  assert.equal(JSON.parse(stdin.line).message.content, "first words");
  assert.equal(startRpcCalls.length, 0, "no pi session for a claude create");
});

test("a supplied claudePreset is dropped unread: ensure_session creates the session with the reduced argv and sends nothing", async () => {
  const before = launches().length;
  const response = await post({ cwd: project, type: "ensure_session", harness: "claude", model: "claude-opus-5-5", claudePreset: "writable" });
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.data, null);
  const stored = JSON.parse(fs.readFileSync(index.claudeIndexPath(), "utf8")).sessions.find((entry) => entry.id === body.sessionId);
  assert.equal(stored.model, "claude-opus-5-5");
  assert.ok(!("preset" in stored));
  const launch = await waitFor(() => launches()[before], 5_000, "no claude launch");
  assert.equal(launch.argv[launch.argv.indexOf("--model") + 1], "claude-opus-5-5");
  for (const flag of ["--bare", "--permission-mode", "--tools", "--allowedTools", "acceptEdits"]) assert.ok(!launch.argv.includes(flag), flag);
});

test("without a harness (or with pi) the pi path runs unchanged and spawns no claude", async () => {
  const before = launches().length;
  for (const extra of [{}, { harness: "pi" }]) {
    startRpcCalls.length = 0;
    const response = await post({ cwd: project, type: "ensure_session", ...extra });
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), { success: true, sessionId: "pi-session", data: null, model: { provider: "p", modelId: "m" }, thinkingLevel: "off" });
    assert.equal(startRpcCalls.length, 1);
  }
  await delay(100);
  assert.equal(launches().length, before);
});

// claude images are validated before a record or a child exists.
const PNG = { type: "image", data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAfbLI3wAAAABJRU5ErkJggg==", mimeType: "image/png" };
const stdinLines = () => stubRecords().filter((entry) => entry.kind === "stdin");
const indexSize = () => (fs.existsSync(index.claudeIndexPath()) ? index.readClaudeIndex().length : 0);

test("invalid claude images are refused with 400 and a reason: no record, no child, nothing on stdin", async () => {
  const oversized = "AAAA".repeat(Math.ceil((10 * 1024 * 1024 + 1) / 3));
  const cases = [
    [[{ ...PNG, data: "not base64!" }], /valid base64/],
    [Array.from({ length: 11 }, () => PNG), /at most 10/],
    [[{ ...PNG, data: oversized }], /10MB/],
    [[{ ...PNG, mimeType: "image/svg+xml" }], /PNG, JPEG, GIF and WebP/],
  ];
  const before = { launches: launches().length, stdin: stdinLines().length, records: indexSize() };
  for (const [images, reason] of cases) {
    const response = await post({ cwd: project, type: "prompt", message: "look", images, harness: "claude", model: "claude-haiku-5-5" });
    assert.equal(response.status, 400);
    const body = await response.json();
    assert.match(body.error, reason);
    assert.equal(body.code, "prompt_rejected");
    assert.equal(body.accepted, false);
  }
  await delay(100);
  assert.deepEqual({ launches: launches().length, stdin: stdinLines().length, records: indexSize() }, before);
});

test("an image-only claude create starts the turn, writes the image once and lists the session as [Image]", async () => {
  const before = stdinLines().length;
  const response = await post({ cwd: project, type: "prompt", message: "", images: [PNG], harness: "claude", model: "claude-haiku-5-5" });
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(index.getClaudeSessionRecord(body.sessionId).firstMessage, "[Image]");
  const line = await waitFor(() => stdinLines()[before], 5_000, "the prompt never reached stdin");
  assert.deepEqual(JSON.parse(line.line).message.content, [{ type: "image", source: { type: "base64", media_type: "image/png", data: PNG.data } }]);
  await delay(100);
  assert.equal(stdinLines().length, before + 1, "written once");
});

test("a claude create stores the composer's effort and passes it to the first child; untouched is the default; an invalid one is 400", async () => {
  let before = launches().length;
  const seeded = await post({ cwd: project, type: "ensure_session", harness: "claude", model: "claude-opus-5-5", effort: "medium" });
  assert.equal(seeded.status, 200);
  const seededId = (await seeded.json()).sessionId;
  assert.equal(index.getClaudeSessionRecord(seededId).effort, "medium");
  const launch = await waitFor(() => launches()[before], 5_000, "no claude launch");
  assert.equal(launch.argv[launch.argv.indexOf("--effort") + 1], "medium");

  before = launches().length;
  const untouched = await post({ cwd: project, type: "ensure_session", harness: "claude", model: "claude-opus-5-5" });
  const untouchedId = (await untouched.json()).sessionId;
  assert.equal(JSON.parse(fs.readFileSync(index.claudeIndexPath(), "utf8")).sessions.find((entry) => entry.id === untouchedId).effort, "default");
  assert.ok(!(await waitFor(() => launches()[before], 5_000, "no claude launch")).argv.includes("--effort"));

  const count = index.readClaudeIndex().length;
  before = launches().length;
  for (const effort of ["off", "minimal", "auto", 3]) {
    const refused = await post({ cwd: project, type: "prompt", message: "x", harness: "claude", model: "claude-opus-5-5", effort });
    assert.equal(refused.status, 400, String(effort));
  }
  assert.equal(index.readClaudeIndex().length, count, "no record for an invalid effort");
  await delay(100);
  assert.equal(launches().length, before, "no child for an invalid effort");
});

const INSTRUCTION = "Use Simplified Chinese (简体中文) for replies, narration and reports. Keep code, code comments, commit messages, files you write, technical terms and identifiers as they are, or as the project's conventions require. Thinking in Chinese is best effort only.";

test("a claude create stores the server's Agent language; a 中文 record launches with the instruction and a later setting change does not rewrite it", async () => {
  const settings = path.join(agentDir, "pi-web-settings.json");
  fs.mkdirSync(agentDir, { recursive: true });
  fs.writeFileSync(settings, JSON.stringify({ version: 1, agentLanguage: "zh-CN" }));
  let before = launches().length;
  const zh = await post({ cwd: project, type: "ensure_session", harness: "claude", model: "claude-haiku-5-5" });
  assert.equal(zh.status, 200);
  const zhId = (await zh.json()).sessionId;
  assert.equal(index.getClaudeSessionRecord(zhId).agentLanguage, "zh-CN");
  const launch = await waitFor(() => launches()[before], 5_000, "no claude launch");
  assert.deepEqual(launch.argv, [
    "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--forward-subagent-text",
    "--model", "claude-haiku-5-5", "--append-system-prompt", INSTRUCTION, "--permission-prompts", "host", "--permission-prompt-tool", "stdio", "--session-id", zhId,
  ]);

  fs.writeFileSync(settings, JSON.stringify({ version: 1, agentLanguage: "en" }));
  assert.equal(index.getClaudeSessionRecord(zhId).agentLanguage, "zh-CN", "the setting change rewrote nothing");
  before = launches().length;
  const en = await post({ cwd: project, type: "ensure_session", harness: "claude", model: "claude-haiku-5-5" });
  const enId = (await en.json()).sessionId;
  assert.equal(index.getClaudeSessionRecord(enId).agentLanguage, "en");
  assert.ok(!(await waitFor(() => launches()[before], 5_000, "no claude launch")).argv.includes("--append-system-prompt"));
  fs.rmSync(settings, { force: true });
});

test("a claude create captures the stored permission mode and hook switch and launches with them; a later settings change rewrites neither the record nor a resume's argv", async () => {
  const settings = path.join(agentDir, "pi-web-claude-settings.json");
  fs.mkdirSync(agentDir, { recursive: true });
  fs.writeFileSync(settings, JSON.stringify({ version: 1, launchPermissionMode: "acceptEdits", customModels: [], includeHookEvents: true }));
  const STREAM = ["-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--forward-subagent-text"];
  let before = launches().length;
  const created = await post({ cwd: project, type: "prompt", message: "permission words", harness: "claude", model: "claude-haiku-5-5" });
  assert.equal(created.status, 200);
  const id = (await created.json()).sessionId;
  const record = index.getClaudeSessionRecord(id);
  assert.equal(record.launchPermissionMode, "acceptEdits");
  assert.equal(record.includeHookEvents, true);
  const launch = await waitFor(() => launches()[before], 5_000, "no claude launch");
  assert.deepEqual(launch.argv, [
    ...STREAM, "--model", "claude-haiku-5-5", "--permission-mode", "acceptEdits", "--include-hook-events", "--permission-prompts", "host", "--permission-prompt-tool", "stdio", "--session-id", id,
  ]);
  // The turn's transcript makes the next child a resume.
  const transcript = path.join(process.env.CLAUDE_CONFIG_DIR, "projects", fs.realpathSync(project).replace(/[^A-Za-z0-9]/g, "-"), `${id}.jsonl`);
  await waitFor(() => fs.existsSync(transcript), 5_000, "no transcript");
  await claude.getClaudeSession(id)?.shutdown();

  fs.writeFileSync(settings, JSON.stringify({ version: 1, launchPermissionMode: "default", customModels: [], includeHookEvents: false }));
  const kept = index.getClaudeSessionRecord(id);
  assert.equal(kept.launchPermissionMode, "acceptEdits", "the settings change rewrote nothing");
  assert.equal(kept.includeHookEvents, true, "the settings change rewrote nothing");
  before = launches().length;
  const resumed = claude.startClaudeSession(kept);
  const again = await waitFor(() => launches()[before], 5_000, "no resume launch");
  assert.deepEqual(again.argv, [
    ...STREAM, "--model", "claude-haiku-5-5", "--permission-mode", "acceptEdits", "--include-hook-events", "--permission-prompts", "host", "--permission-prompt-tool", "stdio", "--resume", id,
  ]);
  await resumed.shutdown();

  before = launches().length;
  const plain = await post({ cwd: project, type: "ensure_session", harness: "claude", model: "claude-haiku-5-5" });
  const plainId = (await plain.json()).sessionId;
  assert.equal(index.getClaudeSessionRecord(plainId).launchPermissionMode, "default");
  assert.equal(index.getClaudeSessionRecord(plainId).includeHookEvents, false);
  assert.deepEqual((await waitFor(() => launches()[before], 5_000, "no claude launch")).argv, [
    ...STREAM, "--model", "claude-haiku-5-5", "--permission-prompts", "host", "--permission-prompt-tool", "stdio", "--session-id", plainId,
  ]);
  fs.rmSync(settings, { force: true });
});

test("claude's alias or any valid full name is stored and passed to --model verbatim", async () => {
  for (const model of ["sonnet", "opus[1m]", "claude-custom-1"]) {
    const before = launches().length;
    const response = await post({ cwd: project, type: "ensure_session", harness: "claude", model });
    assert.equal(response.status, 200, model);
    const body = await response.json();
    assert.deepEqual(body.model, { provider: "claude", modelId: model });
    assert.equal(index.getClaudeSessionRecord(body.sessionId).model, model);
    const launch = await waitFor(() => launches().slice(before).find((entry) => entry.argv.includes(body.sessionId)), 5_000, `no claude launch for ${model}`);
    assert.equal(launch.argv[launch.argv.indexOf("--model") + 1], model);
  }
});
