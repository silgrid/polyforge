// carries: lib/claude-session.ts, lib/claude-shared.ts, lib/types.ts
// carries: lib/__fixtures__/claude-2.1.319/*, lib/__fixtures__/claude-stub/*
// AC4 — steer a claude turn: e2e acceptance.
//
// The repository's unit stub (`lib/__fixtures__/claude-stub/claude.mjs`) replays the real
// claude 2.1.319 recordings `steer-absorbed/` and `steer-queued/` (fixture ②,
// lib/__fixtures__/claude-2.1.319/README.md) stdin line by stdin line, through a scratch
// shim on the server's PATH. Each recording's first segment leaves the first turn running
// (a foreground tool call; a streamed reply), so the page sends its second message while
// the turn runs. The messages are the recording's own texts, so the transcript the stub
// writes and what the page sent agree.
//
// Each scenario prints one `PASS S395-E2E-nn` marker; the suite ends with
// `PASS claude-steer-395` only when every scenario passed.
import assert from "node:assert/strict";
import { chmodSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, claudeScratch, createCheck, createHarness, createWaitUntil, errorText, readJsonLines, root, seedStorage, setupFailed } from "./harness.mjs";

assertNoDevServer();
const { artifacts, scratch, home, agentDir, project, stubDir, stubLog, configDir } = claudeScratch("pi-web-claude-395-", { stub: false });

const unitStub = join(root, "lib/__fixtures__/claude-stub/claude.mjs");
const fixtures = join(root, "lib/__fixtures__/claude-2.1.319");
/** The user texts of a recording, in stdin order. */
const recordedTexts = (name) => readFileSync(join(fixtures, name, "stdin.jsonl"), "utf8").split("\n").filter(Boolean)
  .map((line) => JSON.parse(line)).filter((line) => line.type === "user").map((line) => line.message.content);
function installStub(mode) {
  // Paced, so the page sees the message wait before claude takes it in.
  const env = `PI_TEST_CLAUDE_STUB_MODE=${mode} PI_TEST_CLAUDE_STUB_PACE_MS=120 PI_TEST_CLAUDE_STUB_LOG=${JSON.stringify(stubLog)}`;
  writeFileSync(join(stubDir, "claude"), `#!/bin/sh\n${env} exec ${JSON.stringify(process.execPath)} ${JSON.stringify(unitStub)} "$@"\n`);
  chmodSync(join(stubDir, "claude"), 0o755);
}

const STEER_HINT_EN = "Deliver after the current response and its tool calls finish (Stop interrupts them)";
const LABELS = {
  en: { absorbed: "Taken into the running turn", queued: "Queued" },
  "zh-CN": { absorbed: "已并入当前回合", queued: "已排队" },
};

const h = createHarness();
let browser;
let context;
let page;
let base;
let firstLoad = true;
const failures = [];

const scenario = createCheck({ failures, message: errorText, record: (names, text) => `${names.join(" ")}: ${text}` });

const waitFor = createWaitUntil({ every: 200 });
const stubRecords = () => readJsonLines(stubLog);
const userLines = () => stubRecords().filter((entry) => entry.kind === "stdin").map((entry) => JSON.parse(entry.line)).filter((line) => line.type === "user");
async function apiFetch(pathname, init = {}) {
  return fetch(`${base}${pathname}`, {
    ...init,
    headers: { ...(init.body ? { "Content-Type": "application/json" } : {}), ...(init.headers ?? {}) },
    signal: AbortSignal.timeout(120_000),
  });
}
async function isRunning(id) {
  const response = await apiFetch(`/api/agent/${encodeURIComponent(id)}`);
  if (response.status !== 200) return false;
  const body = await response.json();
  return body.running === true && body.state?.isStreaming === true;
}
async function createClaudeSession(message) {
  const response = await apiFetch("/api/agent/new", { method: "POST", body: JSON.stringify({ cwd: project, type: "prompt", message, harness: "claude", model: "claude-haiku-5-5" }) });
  const body = await response.json().catch(() => ({}));
  assert.equal(response.status, 200, `POST /api/agent/new -> ${response.status} ${JSON.stringify(body).slice(0, 300)}`);
  const id = body.sessionId ?? body.id;
  assert.ok(typeof id === "string" && id, "no claude session id");
  return id;
}
const textarea = () => page.locator(".chat-input-textarea").first();
const stopButton = () => page.getByRole("button", { name: "Stop agent", exact: true }).first();
async function openSession(id) {
  await page.goto(`${base}/?session=${encodeURIComponent(id)}`, { waitUntil: "domcontentloaded", timeout: firstLoad ? COLD_MS : NAV_MS });
  firstLoad = false;
  await textarea().waitFor({ state: "visible", timeout: NAV_MS });
}
/** The user bubble that holds `text` (the message row, label and time included). */
const userRow = (text) => page.locator("div").filter({ has: page.locator(".markdown-user-message", { hasText: text }) }).filter({ has: page.locator("[data-claude-delivery]") }).last();

/** Creates the session (its first turn starts and stays running), opens it and steers it with the recording's second message. */
async function steer(mode) {
  installStub(mode);
  const [first, second] = recordedTexts(mode);
  const before = userLines().length;
  const id = await createClaudeSession(first);
  await openSession(id);
  await waitFor(() => isRunning(id), 60_000, "the first turn is not running");
  await stopButton().waitFor({ state: "visible", timeout: NAV_MS });
  await textarea().fill(second);
  const steerButton = page.getByRole("button", { name: STEER_HINT_EN }).first();
  await waitFor(async () => (await steerButton.count()) > 0 && await steerButton.isVisible(), ACTION_MS, "the composer offers no steer while the claude turn runs");
  await steerButton.click({ timeout: ACTION_MS });
  // The message goes to claude's stdin at once, with the uuid claude reports it by.
  const sent = await waitFor(() => userLines().slice(before).find((line) => line.message?.content === second), 30_000, "the steered message never reached claude's stdin");
  assert.match(String(sent.uuid), /^[0-9a-f-]{36}$/, "the steered line carries no uuid");
  return { id, second };
}

h.trapInterrupt();

try {
  installStub("steer-absorbed");
  ({ base } = await h.startServer({
    name: "claude-steer-395",
    logFile: join(artifacts, "claude-steer-395-server.log"),
    env: {
      HOME: home,
      PI_CODING_AGENT_DIR: agentDir,
      CLAUDECODE: "1",
      CLAUDE_CONFIG_DIR: configDir,
      PI_WEB_ALLOWED_ROOT_PREFIXES: scratch,
      // The product default: only the suite ends a child.
      PI_WEB_IDLE_TIMEOUT_MS: "600000",
      PATH: `${stubDir}:${process.env.PATH ?? ""}`,
    },
  }));

  const { chromium } = await import("playwright");
  browser = await h.launchBrowser(() => chromium.launch());
  context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US", baseURL: base });
  await seedStorage(context);
  page = await context.newPage();
  page.setDefaultTimeout(ACTION_MS);
  await context.tracing.start({ screenshots: true, snapshots: true });

  // S395-E2E-01 — steer-absorbed: the message sent mid-turn waits in the queue, claude
  // takes it into the running turn, the bubble says so, and that one turn ends.
  await scenario("S395-E2E-01", "A message sent mid-turn that claude absorbs is labelled and the turn ends", async () => {
    const { id, second } = await steer("steer-absorbed");
    await waitFor(async () => (await page.getByText(/^Queued · 1$/).count()) > 0, 10_000, "the queue never showed the waiting message");
    await waitFor(async () => !(await isRunning(id)), 60_000, "the turn never ended after claude absorbed the message");
    await stopButton().waitFor({ state: "detached", timeout: NAV_MS });
    const label = userRow(second).locator('[data-claude-delivery="absorbed"]');
    await label.waitFor({ state: "visible", timeout: NAV_MS });
    assert.equal((await label.innerText()).trim(), LABELS.en.absorbed);
    assert.equal(await page.getByText(/^Queued · \d+$/).count(), 0, "the delivered message is still in the queue");
    // The label is claude's transcript's, not page memory: a reload keeps it.
    await openSession(id);
    await userRow(second).locator('[data-claude-delivery="absorbed"]').waitFor({ state: "visible", timeout: NAV_MS });
    assert.equal(await textarea().isEditable(), true, "the input stayed locked");
  });

  // S395-E2E-02 — steer-queued: claude queues the message behind the running turn and
  // runs it as its next turn; the run ends at that turn's result.
  await scenario("S395-E2E-02", "A message sent mid-turn that claude queues runs as the next turn, labelled, and the run ends", async () => {
    const { id, second } = await steer("steer-queued");
    await waitFor(async () => (await page.getByText(/^Queued · 1$/).count()) > 0, 10_000, "the queue never showed the waiting message");
    await waitFor(async () => !(await isRunning(id)), 90_000, "the run never ended after claude's queued turn");
    await stopButton().waitFor({ state: "detached", timeout: NAV_MS });
    const label = userRow(second).locator('[data-claude-delivery="queued"]');
    await label.waitFor({ state: "visible", timeout: NAV_MS });
    assert.equal((await label.innerText()).trim(), LABELS.en.queued);
    const replies = await page.locator(".markdown-body").allInnerTexts();
    assert.ok(replies.some((text) => text.includes("QUEUED_REPLY_395")), "claude's queued turn's reply is not shown");
    // Both labels read in 中文 too, from history.
    await page.evaluate(() => localStorage.setItem("pi-locale", "zh-CN"));
    await openSession(id);
    const zh = userRow(second).locator('[data-claude-delivery="queued"]');
    await zh.waitFor({ state: "visible", timeout: NAV_MS });
    assert.equal((await zh.innerText()).trim(), LABELS["zh-CN"].queued);
    await page.evaluate(() => localStorage.setItem("pi-locale", "en"));
    assert.equal(await textarea().isEditable(), true, "the input stayed locked");
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  if (page) await page.screenshot({ path: join(artifacts, "claude-steer-395-failure.png") }).catch(() => {});
  if (context) await context.tracing.stop({ path: join(artifacts, "claude-steer-395-trace.zip") }).catch(() => {});
  await h.close();
  for (const entry of stubRecords().filter((record) => record.kind === "launch")) {
    try { process.kill(entry.pid, "SIGKILL"); } catch { /* already gone */ }
  }
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL claude-steer-395: ${failures.length} scenario(s) failed`);
  process.exitCode = 1;
} else {
  console.log("PASS claude-steer-395");
}
