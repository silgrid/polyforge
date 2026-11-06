// carries: lib/claude-adapter.ts, lib/rpc-manager.ts, lib/pi-types.ts
// carries: docs/agents/sessions.md
// — the header's context meter after a manual compaction: e2e acceptance pass.
//
// A compaction started from the page's Compact control changes the context the
// header's meter (the desktop "Session info" button and its title) shows, and no
// reply follows it: the meter must show the new size once the compaction ends,
// with no reply, page reload or event-stream reconnect. Before the fix it kept
// the old number until one of those.
//
// - pi: a session file whose last reply reports 40,000 tokens, on a fake
//   OpenAI-compatible model with a 100,000-token window (models.json). The fake
//   model answers pi's summarization request; nothing else may reach it. After
//   the compaction pi reports the size as unknown until the next reply: `? / 100k`.
// - claude: the PATH-injected e2e stub (e2e/claude-stub/claude.mjs) in its
//   `compact` fixture mode replays the real claude 2.1.312 `compact/` recording
//   (lib/__fixtures__/claude-2.1.312/compact/): two replies (16,688 and 16,739
//   tokens), then claude's own /compact, which reports `post_tokens` 2,978.
// - claude's window: the same recording with its `modelUsage[…].contextWindow`
//   rewritten from 1,000,000 to 200,000 (an account without the long context;
//   the stub replays a scratch copy) — the meter divides by claude's window, not
//   pi's catalog entry for the model (1,000,000).
// No real model is called.
//
// Every scenario is individually guarded and prints one `PASS S368-nn` marker.
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer as createHttpServer } from "node:http";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, claudeEnv, claudeScratch, createApi, createCheck, createHarness, createWaitUntil, errorText, readJsonLines, root, same, seedStorage, setupFailed } from "./harness.mjs";

assertNoDevServer();
const kit = claudeScratch("pi-web-compact-368-");
const { artifacts, scratch, agentDir, project, stubLog } = kit;
// The stub reads the recording it replays when a child starts: S368-03 rewrites this copy first.
const compactFixture = join(scratch, "compact-fixture");
const sessionDir = join(agentDir, "sessions", "e2e-368");
for (const dir of [compactFixture, sessionDir]) mkdirSync(dir, { recursive: true });
const recordedStream = readFileSync(join(root, "lib/__fixtures__/claude-2.1.312/compact/stream.jsonl"), "utf8");
writeFileSync(join(compactFixture, "stream.jsonl"), recordedStream);

// The compact/ recording's own counts (lib/__fixtures__/claude-2.1.312/README.md).
const FIRST_REPLY = 16688;
const SECOND_REPLY = 16739;
const POST_COMPACTION = 2978;
const CATALOG_WINDOW = 1_000_000;
const REPORTED_WINDOW = 200_000;
const PI_WINDOW = 100_000;
const PI_LAST_REPLY = 40_000;
const usageFor = (tokens, contextWindow) => ({ tokens, percent: (tokens / contextWindow) * 100, contextWindow });
// How long the meter gets to follow a compaction's end: one state read, never a page load.
const AFTER_COMPACTION_MS = 60_000;

// A local OpenAI-compatible model for the pi session: it records every request's
// system prompt and answers at once with a summary-shaped text.
const SUMMARIZER = "You are a context summarization assistant.";
const modelRequests = [];
const model = createHttpServer(async (request, response) => {
  let body = "";
  for await (const chunk of request) body += chunk;
  let parsed = {};
  try { parsed = JSON.parse(body); } catch { /* recorded as {} */ }
  modelRequests.push(parsed);
  response.writeHead(200, { "Content-Type": "text/event-stream" });
  const chunk = (delta, finish = null, extra = {}) => `data: ${JSON.stringify({
    id: "fake", object: "chat.completion.chunk", created: 0, model: "fake-model",
    choices: [{ index: 0, delta, finish_reason: finish }], ...extra,
  })}\n\n`;
  response.write(chunk({ role: "assistant", content: "## Goal\nE2E 368 summary." }));
  response.write(chunk({}, "stop", { usage: { prompt_tokens: 30, completion_tokens: 6, total_tokens: 36 } }));
  response.write("data: [DONE]\n\n");
  response.end();
});
model.listen(0, "127.0.0.1");
await new Promise((resolve) => model.once("listening", resolve));
const systemOf = (request) => (request.messages ?? []).filter((message) => message.role === "system" || message.role === "developer")
  .map((message) => typeof message.content === "string" ? message.content : (message.content ?? []).map((block) => block.text ?? "").join(""))
  .join("\n\n");
writeFileSync(join(agentDir, "models.json"), JSON.stringify({
  providers: {
    fake: {
      baseUrl: `http://127.0.0.1:${model.address().port}/v1`,
      api: "openai-completions",
      apiKey: "fake-key",
      models: [{ id: "fake-model", name: "Fake Model", contextWindow: PI_WINDOW, maxTokens: 8192 }],
    },
  },
}));
// keepRecentTokens 1: the two-turn session below has an earlier turn to summarize.
writeFileSync(join(agentDir, "settings.json"), JSON.stringify({ defaultProvider: "fake", defaultModel: "fake-model", compaction: { keepRecentTokens: 1 } }));

const PI_SESSION = "e2e-368-pi";
const stamp = "2026-11-01T00:00:00.000Z";
const piUsage = (tokens) => ({ input: tokens, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: tokens, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } });
const piReply = (text, tokens) => ({ role: "assistant", content: [{ type: "text", text }], provider: "fake", model: "fake-model", api: "openai-completions", usage: piUsage(tokens), stopReason: "stop", timestamp: Date.parse(stamp) });
writeFileSync(join(sessionDir, `${stamp.replaceAll(":", "-")}_${PI_SESSION}.jsonl`), [
  { type: "session", version: 3, id: PI_SESSION, timestamp: stamp, cwd: project },
  { type: "message", id: "u1", parentId: null, timestamp: stamp, message: { role: "user", content: "E2E 368 first question" } },
  { type: "message", id: "a1", parentId: "u1", timestamp: stamp, message: piReply("E2E 368 first answer", 12_000) },
  { type: "message", id: "u2", parentId: "a1", timestamp: stamp, message: { role: "user", content: "E2E 368 second question" } },
  { type: "message", id: "a2", parentId: "u2", timestamp: stamp, message: piReply("E2E 368 second answer", PI_LAST_REPLY) },
].map((entry) => JSON.stringify(entry)).join("\n") + "\n");

const h = createHarness();
let browser;
let desktopContext;
let page;
let base;
let firstLoad = true;
const failures = [];

const scenario = createCheck({ failures, message: errorText, record: (names, text) => `${names.join(" ")}: ${text}` });

const stubRecords = () => readJsonLines(stubLog);
const launchesFor = (id) => stubRecords().filter((entry) => entry.kind === "launch" && entry.sessionId === id);
const userTextsFor = (id) => {
  const pids = new Set(launchesFor(id).map((entry) => entry.pid));
  return stubRecords()
    .filter((entry) => entry.kind === "stdin" && pids.has(entry.pid))
    .flatMap((entry) => {
      try {
        const event = JSON.parse(entry.line);
        if (event?.type !== "user") return [];
        const content = event.message?.content;
        return [typeof content === "string" ? content : (content ?? []).map((block) => block.text ?? "").join("")];
      } catch {
        return [];
      }
    });
};

const waitFor = createWaitUntil();

const { getJson, postJson } = createApi(() => base);

async function createClaudeSession(message) {
  const created = await postJson("/api/agent/new", { cwd: project, harness: "claude", model: "claude-haiku-5-5", type: "prompt", message });
  assert.equal(created.status, 200, `POST /api/agent/new -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const id = created.body.sessionId;
  assert.ok(typeof id === "string" && id.length > 0, "the create route returned no claude session id");
  return id;
}
async function sendApiPrompt(id, message) {
  const sent = await postJson(`/api/agent/${encodeURIComponent(id)}`, { type: "prompt", message });
  assert.equal(sent.status, 200, `prompt -> ${sent.status} ${JSON.stringify(sent.body).slice(0, 300)}`);
}

/** The live state's contextUsage once the session is idle; undefined while it runs or when nothing is alive. */
async function idleContextUsage(id) {
  const reply = await getJson(`/api/agent/${encodeURIComponent(id)}`);
  return reply.running === true && reply.state?.isStreaming === false && reply.state?.isCompacting !== true ? reply.state.contextUsage : undefined;
}
async function waitForContextUsage(id, expected, what) {
  let seen;
  await waitFor(async () => {
    seen = await idleContextUsage(id);
    return same(seen, expected);
  }, 60_000, () => `${what}: GET /api/agent/<id> contextUsage is ${JSON.stringify(seen)}, expected ${JSON.stringify(expected)}`, 250);
}

const textarea = () => page.locator(".chat-input-textarea").first();
async function openSession(id) {
  await page.goto(`${base}/?session=${encodeURIComponent(id)}`, { waitUntil: "domcontentloaded", timeout: firstLoad ? COLD_MS : NAV_MS });
  firstLoad = false;
  await textarea().waitFor({ state: "visible", timeout: NAV_MS });
}

/** The desktop header's stats button: its visible text and its title. */
const desktopMeter = () => page.locator('button[aria-label="Session info"]:not([data-mobile-toolbar-stats])').first();
async function readMeter() {
  const meter = desktopMeter();
  return { text: (await meter.innerText()).replace(/\s+/g, " ").trim(), title: (await meter.getAttribute("title")) ?? "" };
}
/** Waits until the desktop meter shows `text` and its title the `Context: …` part. */
async function waitForMeter(text, titlePart, what, timeoutMs = NAV_MS) {
  let seen = { text: "", title: "" };
  await waitFor(async () => {
    seen = await readMeter();
    return seen.text.includes(text) && seen.title.includes(titlePart);
  }, timeoutMs, () => `${what}: the header meter reads ${JSON.stringify(seen)}, expected ${JSON.stringify(text)} and a title with ${JSON.stringify(titlePart)}`, 250);
  return seen;
}

const compactControl = () => page.locator('.chat-input-controls button[aria-label="Compact context"]').first();

/**
 * What the page did from here on: its loads (a reload resets the marker), its event
 * stream connections for `id` and the commands it posted to the session.
 */
async function watchPage(id) {
  const marker = `s368-${Date.now()}`;
  await page.evaluate((value) => { window.__s368Marker = value; }, marker);
  const streams = [];
  const commands = [];
  const onRequest = (request) => {
    const { pathname } = new URL(request.url());
    if (pathname === `/api/agent/${encodeURIComponent(id)}/events`) streams.push(pathname);
    if (pathname === `/api/agent/${encodeURIComponent(id)}` && request.method() === "POST") {
      try {
        commands.push(JSON.parse(request.postData() ?? "{}").type);
      } catch {
        commands.push("?");
      }
    }
  };
  page.on("request", onRequest);
  return {
    commands,
    async assertSamePage(what) {
      page.off("request", onRequest);
      assert.equal(await page.evaluate(() => window.__s368Marker), marker, `${what}: the page was reloaded`);
      assert.deepEqual(streams, [], `${what}: the event stream reconnected`);
      assert.deepEqual(commands, ["compact"], `${what}: the page sent more than the compaction`);
    },
  };
}

/** Clicks Compact once the control is enabled, past the run-end click guard (600 ms). */
async function clickCompact() {
  const control = compactControl();
  await control.waitFor({ state: "visible", timeout: NAV_MS });
  await waitFor(() => control.isEnabled(), NAV_MS, "the Compact control stays disabled");
  await delay(1_000);
  await control.click({ timeout: ACTION_MS });
}

h.trapInterrupt();

try {
  ({ base } = await h.startServer({ name: "context-after-compact-368", logFile: join(artifacts, "context-after-compact-368-server.log"), env: claudeEnv(kit, {
      PI_WEB_DISABLE_MCP: "1",
      // The product default: no child or wrapper is reaped mid-scenario.
      PI_WEB_IDLE_TIMEOUT_MS: "600000",
      // Every claude child replays the compact/ recording (a scratch copy): reply, reply, then /compact.
      PI_E2E_CLAUDE_STUB_MODE: "compact",
      PI_E2E_CLAUDE_COMPACT_FIXTURE: compactFixture,
  }) }));

  const { chromium } = await import("playwright");
  browser = await h.launchBrowser(() => chromium.launch());
  desktopContext = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US", baseURL: base });
  await seedStorage(desktopContext);
  page = await desktopContext.newPage();
  page.setDefaultTimeout(ACTION_MS);
  await desktopContext.tracing.start({ screenshots: true, snapshots: true });

  await scenario("S368-01", "pi: after Compact the meter shows pi's unknown size without a reply, reload or reconnect", async () => {
    await openSession(PI_SESSION);
    await page.getByText("E2E 368 second answer", { exact: true }).first().waitFor({ timeout: NAV_MS });
    await waitForMeter("40% / 100k", "Context: 40.0% / 100,000", "before the compaction");
    assert.equal(modelRequests.length, 0, "opening the session reached the model");

    const watch = await watchPage(PI_SESSION);
    await clickCompact();
    await waitFor(() => modelRequests.length > 0, 60_000, "the compaction never asked the fake model for a summary");
    await waitForContextUsage(PI_SESSION, { tokens: null, percent: null, contextWindow: PI_WINDOW }, "after the compaction");
    const meter = await waitForMeter("? / 100k", "Context: ? / 100,000", "after the compaction", AFTER_COMPACTION_MS);
    assert.ok(!/\d%|NaN/.test(meter.text), `the meter still shows a number: ${meter.text}`);
    await watch.assertSamePage("pi");
    for (const request of modelRequests) assert.ok(systemOf(request).includes(SUMMARIZER), `a model request that is not the summary: ${JSON.stringify(request).slice(0, 300)}`);
    await page.screenshot({ path: join(artifacts, "compact-368-pi.png") });
  });

  await scenario("S368-02", "claude: after Compact the meter shows the size claude left without a reply, reload or reconnect", async () => {
    const id = await createClaudeSession("E2E 368 first");
    await waitForContextUsage(id, usageFor(FIRST_REPLY, CATALOG_WINDOW), "after the first reply");
    await openSession(id);
    await page.getByText("E2E 368 first", { exact: true }).first().waitFor({ timeout: NAV_MS });
    await sendApiPrompt(id, "E2E 368 second");
    await waitForContextUsage(id, usageFor(SECOND_REPLY, CATALOG_WINDOW), "after the second reply");
    await waitForMeter("2% / 1.0M", "Context: 1.7% / 1,000,000", "before the compaction");

    const watch = await watchPage(id);
    await clickCompact();
    await waitFor(() => userTextsFor(id).includes("/compact"), 60_000, "no /compact reached the child's stdin");
    await waitForContextUsage(id, usageFor(POST_COMPACTION, CATALOG_WINDOW), "after the compaction");
    await waitForMeter("0% / 1.0M", "Context: 0.3% / 1,000,000", "after the compaction", AFTER_COMPACTION_MS);
    await watch.assertSamePage("claude");
    assert.deepEqual(userTextsFor(id), ["E2E 368 first", "E2E 368 second", "/compact"], "the child got a prompt after the compaction");
    await page.screenshot({ path: join(artifacts, "compact-368-claude.png") });
  });

  await scenario("S368-03", "claude: the meter divides by the window claude reported, before and after a compaction", async () => {
    const rewritten = recordedStream.replaceAll(`"contextWindow":${CATALOG_WINDOW}`, `"contextWindow":${REPORTED_WINDOW}`);
    assert.notEqual(rewritten, recordedStream, "the recording carries no contextWindow to rewrite");
    writeFileSync(join(compactFixture, "stream.jsonl"), rewritten);
    const id = await createClaudeSession("E2E 368 window");
    await waitForContextUsage(id, usageFor(FIRST_REPLY, REPORTED_WINDOW), "after the first reply");
    await openSession(id);
    await page.getByText("E2E 368 window", { exact: true }).first().waitFor({ timeout: NAV_MS });
    await waitForMeter("8% / 200k", "Context: 8.3% / 200,000", "after the first reply");
    await sendApiPrompt(id, "E2E 368 window second");
    await waitForContextUsage(id, usageFor(SECOND_REPLY, REPORTED_WINDOW), "after the second reply");
    await waitForMeter("8% / 200k", "Context: 8.4% / 200,000", "after the second reply");

    const watch = await watchPage(id);
    await clickCompact();
    await waitForContextUsage(id, usageFor(POST_COMPACTION, REPORTED_WINDOW), "after the compaction");
    await waitForMeter("1% / 200k", "Context: 1.5% / 200,000", "after the compaction", AFTER_COMPACTION_MS);
    await watch.assertSamePage("claude window");
    await page.screenshot({ path: join(artifacts, "compact-368-window.png") });
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  if (page) await page.screenshot({ path: join(artifacts, "compact-368-final.png") }).catch(() => {});
  if (desktopContext) await desktopContext.tracing.stop({ path: join(artifacts, "compact-368-trace.zip") }).catch(() => {});
  await h.close();
  model.closeAllConnections();
  await new Promise((resolve) => model.close(() => resolve()));
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL context-after-compact-368: ${failures.length} scenario(s) failed`);
  for (const failure of failures) console.log(`  ${failure}`);
  process.exitCode = 1;
}
