// carries: components/ChatWindow*, components/ChatInput*, components/MessageView*, hooks/useAgentSession*, lib/agent-*, lib/stream-client*, lib/session-*, app/api/agent/*, app/api/sessions/*
// carries: lib/page-stream-registry*
// repro: the newest session content stays hidden when the page's
// event stream is ESTABLISHED but silent (a weak mobile network), both while
// the page has its own run under way and when an idle page first sees another
// client's write for the selected session.
//
// The real app path is mounted: `next dev`, Chromium at 390x844 with
// isMobile, the sidebar's /api/agent/running poll, ChatWindow and
// useAgentSession. A TCP proxy freezes only the selected session's
// /api/agent/<id>/events connections: the sockets stay ESTABLISHED and no
// bytes arrive, heartbeats included. Every other request goes through. A fake
// OpenAI-compatible model makes runs append real entries through the wrapper,
// the same way another device on the same pi-web does.
//
// Reproduced failures (bound 10s, the WI's acceptance example):
// - the page's own run: turn-1 text and the tool result never show while the
//   run lasts, and the final reply shows well after 10s (observed 30.5s after
//   send for a 17.5s run);
// - an idle page whose selected session has no watcher-ring entry when it is
//   selected: another client's quick run is not shown within 10s (not shown at
//   all in the 30s observation window).
//
//   node e2e/session-stall-recovery.mjs
//   npm run test:e2e:all -- session-stall-recovery.mjs
//
// e2e/all: isolated — BOUND_MS is a wall-clock responsiveness assertion, so
// the parallel browser pool must not share the host with it: under that pool a
// loaded box observed 20167ms against the 10000ms bound, while the suite alone
// passes (full-bar run).
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer as createHttpServer } from "node:http";
import { createServer as createNetServer, connect } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { startNextServer } from "./next-server.mjs";
import { launchBrowser, softCheck } from "./harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const BOUND_MS = 10_000;
const OBSERVE_MS = 30_000;
const distDir = process.env.PI_WEB_DIST_DIR || ".next/e2e/session-stall-recovery";
const artifacts = join(root, "test-results/e2e");
mkdirSync(artifacts, { recursive: true });
rmSync(join(root, distDir, "dev/lock"), { force: true });

const nextEnvPath = join(root, "next-env.d.ts");
const nextEnvBefore = existsSync(nextEnvPath) ? readFileSync(nextEnvPath, "utf8") : null;

const failures = [];
const check = softCheck(failures);

// ---- fake model -------------------------------------------------------------
// `plan.single` is one reply. `plan.toolFirst` makes call #1 return text plus a
// bash tool call; call #2 (after the tool result) returns `plan.final`.
let plan = null;
let modelCalls = 0;
let finalWrittenAt = null;
const model = createHttpServer(async (request, response) => {
  await new Promise((resolve) => {
    request.on("data", () => { /* drain the prompt body */ });
    request.on("end", resolve);
    request.on("error", resolve);
  });
  modelCalls += 1;
  const current = plan;
  if (!current) {
    response.writeHead(500);
    response.end();
    return;
  }
  response.writeHead(200, { "Content-Type": "text/event-stream" });
  const chunk = (delta, finish = null, extra = {}) => `data: ${JSON.stringify({
    id: "fake", object: "chat.completion.chunk", created: 0, model: "fake-model",
    choices: [{ index: 0, delta, finish_reason: finish }], ...extra,
  })}\n\n`;
  if (current.toolFirst && modelCalls === 1) {
    response.write(chunk({ role: "assistant", content: "FIRST-TURN-TEXT" }));
    response.write(chunk({
      tool_calls: [{
        index: 0,
        id: "call_1",
        type: "function",
        function: { name: "bash", arguments: JSON.stringify({ command: "echo TOOL-OUT-MARK" }) },
      }],
    }));
    response.write(chunk({}, "tool_calls"));
    response.write("data: [DONE]\n\n");
    response.end();
    return;
  }
  const reply = current.toolFirst ? current.final : current.single;
  response.write(chunk({ role: "assistant", content: "" }));
  if (reply.gate) await reply.gate;
  await delay(reply.delayMs);
  finalWrittenAt = Date.now();
  response.write(chunk({ content: reply.text }));
  response.write(chunk({}, "stop", { usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 } }));
  response.write("data: [DONE]\n\n");
  response.end();
});
model.listen(0, "127.0.0.1");
await new Promise((resolve) => model.once("listening", resolve));
const modelPort = model.address().port;

// ---- fixtures ---------------------------------------------------------------
const agentDir = mkdtempSync(join(tmpdir(), "pi-web-stall-"));
const project = join(agentDir, "project");
const sessionDir = join(agentDir, "sessions", "stall");
mkdirSync(project);
mkdirSync(sessionDir, { recursive: true });
writeFileSync(join(agentDir, "models.json"), JSON.stringify({
  providers: {
    fake: {
      baseUrl: `http://127.0.0.1:${modelPort}/v1`,
      api: "openai-completions",
      apiKey: "fake-key",
      models: [{ id: "fake-model", name: "Fake Model" }],
    },
  },
}));
writeFileSync(join(agentDir, "settings.json"), JSON.stringify({
  defaultProvider: "fake",
  defaultModel: "fake-model",
}));

const timestamp = "2026-10-30T00:00:00.000Z";
function writeSession(id, text, thinkingLevel) {
  const entries = [
    { type: "session", version: 3, id, timestamp, cwd: project },
    { type: "message", id: "u1", parentId: null, timestamp, message: { role: "user", content: `hello ${id}` } },
    {
      type: "message",
      id: "a1",
      parentId: "u1",
      timestamp,
      message: {
        role: "assistant",
        content: [{ type: "text", text }],
        provider: "fake",
        model: "fake-model",
        api: "openai-completions",
        usage: {
          input: 1,
          output: 1,
          cacheRead: 0,
          cacheWrite: 0,
          totalTokens: 2,
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
        },
        stopReason: "stop",
        timestamp: Date.parse(timestamp),
      },
    },
  ];
  if (thinkingLevel) {
    entries.push({ type: "thinking_level_change", id: "t1", parentId: "a1", timestamp, thinkingLevel });
  }
  writeFileSync(
    join(sessionDir, `${timestamp.replaceAll(":", "-")}_${id}.jsonl`),
    `${entries.map((entry) => JSON.stringify(entry)).join("\n")}\n`,
  );
}

const OWN_RUN = "stall-own-run";
const IDLE_FIRST = "stall-idle-first";
writeSession(OWN_RUN, "initial answer of own-run");
// The session already holds thinking_level_change=off, so opening it appends
// nothing and the watcher ring has no entry for it when it is selected.
writeSession(IDLE_FIRST, "initial answer of idle-first", "off");

// ---- next dev + a proxy that can freeze the event-stream sockets ------------
let started;
let proxy;
let browser;
let context;
let page;
const conns = new Set();
let frozen = false;
const ringEvents = [];
const pageRequests = [];

try {
  started = await startNextServer({
    name: "session-stall-recovery",
    logFile: join(artifacts, "session-stall-recovery-server.log"),
    env: {
      ...process.env,
      PI_CODING_AGENT_DIR: agentDir,
      PI_WEB_PASSWORD: "",
      NEXT_TELEMETRY_DISABLED: "1",
      PI_WEB_ALLOWED_ROOT_PREFIXES: agentDir,
      PI_WEB_DIST_DIR: distDir,
      PI_WEB_DISABLE_MCP: "1",
    },
  });
  const upstreamPort = Number(new URL(started.base).port);
  proxy = createNetServer((client) => {
    const upstream = connect(upstreamPort, "127.0.0.1");
    const connection = { client, upstream, isEvents: false, dead: false, dropped: 0 };
    conns.add(connection);
    client.on("data", (data) => {
      const head = data.toString("latin1");
      if (/GET \/api\/agent\/[^ ]+\/events/.test(head)) connection.isEvents = true;
      upstream.write(data);
    });
    upstream.on("data", (data) => {
      if (frozen && connection.isEvents) {
        connection.dropped += data.length;
        return;
      }
      client.write(data);
    });
    const end = () => {
      connection.dead = true;
      conns.delete(connection);
      client.destroy();
      upstream.destroy();
    };
    client.on("close", end);
    upstream.on("close", end);
    client.on("error", end);
    upstream.on("error", end);
  });
  proxy.listen(0, "127.0.0.1");
  await new Promise((resolve) => proxy.once("listening", resolve));
  const base = `http://127.0.0.1:${proxy.address().port}`;

  browser = await launchBrowser(() => chromium.launch());
  context = await browser.newContext({
    viewport: { width: 390, height: 844 },
    isMobile: true,
    hasTouch: true,
    locale: "en-US",
  });
  await context.addInitScript(() => {
    try { window.localStorage.setItem("pi-web:session-age-filter-days", "0"); } catch { /* storage blocked */ }
  });
  // First-appearance recorder. It samples from page load, so content that is on
  // screen before the own-run block's reconcile-tick wait is still timed from
  // when it actually appeared.
  await context.addInitScript(() => {
    window.__seen = {};
    const marks = ["FIRST-TURN-TEXT", "TOOL-OUT-MARK", "REPLY-RUN-FINAL", "REPLY-IDLE-FIRST"];
    setInterval(() => {
      const text = document.body ? document.body.innerText : "";
      for (const mark of marks) {
        if (window.__seen[mark] === undefined && text.includes(mark)) window.__seen[mark] = Date.now();
      }
    }, 100);
  });
  page = await context.newPage();
  page.setDefaultTimeout(120_000);
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname.startsWith("/api/")) pageRequests.push({ at: Date.now(), path: url.pathname + url.search });
  });
  page.on("response", async (response) => {
    const url = new URL(response.url());
    if (url.pathname !== "/api/agent/running") return;
    try {
      const body = await response.json();
      ringEvents.push({
        at: Date.now(),
        writes: (body.recentSessionWrites ?? []).map((write) => ({
          sessionId: write.sessionId,
          generation: write.generation,
        })),
      });
    } catch { /* the poll response was not readable JSON */ }
  });

  const shows = (text) => page.evaluate((value) => document.body.innerText.includes(value), text);
  const firstSeen = (text) => page.evaluate((value) => window.__seen?.[value] ?? null, text);
  async function waitUntil(predicate, timeoutMs) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      if (await predicate()) return true;
      await delay(100);
    }
    return await predicate();
  }
  async function desktopPrompt(sid, text) {
    const response = await fetch(`${started.base}/api/agent/${sid}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ type: "prompt", message: `desk asks for ${text}` }),
      signal: AbortSignal.timeout(60_000),
    });
    assert.ok(response.ok, `desktop prompt -> ${response.status}`);
  }
  async function waitIdle(sid) {
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      const body = await fetch(`${started.base}/api/agent/running`).then((response) => response.json());
      if (!body.runningSessionIds.includes(sid)) return;
      await delay(200);
    }
    throw new Error(`${sid}: run did not end`);
  }
  async function loadSession(sid) {
    frozen = false;
    const loadedAt = Date.now();
    await page.goto(`${base}/?session=${sid}`, { waitUntil: "domcontentloaded" });
    assert.ok(await waitUntil(() => shows(`initial answer of ${sid.slice("stall-".length)}`), 60_000),
      `${sid}: initial snapshot did not render`);
    // A condition instead of a fixed post-load sleep: the page's own event
    // stream must be up, and the sidebar must have completed a poll after it.
    assert.ok(await waitUntil(
      () => [...conns].some((connection) => connection.isEvents && !connection.dead),
      30_000,
    ), `${sid}: the page never opened an event stream`);
    assert.ok(await waitUntil(
      () => ringEvents.some((event) => event.at >= loadedAt),
      15_000,
    ), `${sid}: the sidebar never polled /api/agent/running after load`);
  }
  const reads = (sid, since) => pageRequests
    .filter((request) => request.at >= since && request.path.startsWith(`/api/sessions/${sid}?`))
    .map((request) => request.path.includes("force=1") ? "force" : "plain");

  // The page's own run: establish a watcher-ring baseline first (so the run's
  // writes are rises above it), then freeze the stream and send from the page.
  {
    const sid = OWN_RUN;
    await loadSession(sid);
    plan = { single: { text: "BASELINE-WARM", delayMs: 100 } };
    modelCalls = 0;
    const baselineAt = Date.now();
    await desktopPrompt(sid, "BASELINE-WARM");
    const baselineShown = await waitUntil(() => shows("BASELINE-WARM"), 30_000);
    const baselineRing = await waitUntil(
      () => ringEvents.some((event) => event.at >= baselineAt
        && event.writes.some((write) => write.sessionId === sid)),
      15_000,
    );
    await waitIdle(sid);
    check(baselineShown, `${sid}: the healthy-stream baseline reply did not render`);
    check(baselineRing, `${sid}: the sidebar never reported the baseline write for the selected session`);

    frozen = true;
    finalWrittenAt = null;
    modelCalls = 0;
    // Hold the final reply until the page's run-time reconcile has ticked once.
    // Then the run ends just after a tick, so a missed end is not shown until
    // the next 15s tick — the late-final failure the SWE recorded.
    let releaseFinal;
    const finalGate = new Promise((resolve) => { releaseFinal = resolve; });
    plan = { toolFirst: true, final: { text: "REPLY-RUN-FINAL", delayMs: 0, gate: finalGate } };
    const sentAt = Date.now();
    await page.locator("textarea").first().fill("phone asks while the network stalls");
    await page.getByRole("button", { name: "Send" }).first().click();
    const tickObserved = await waitUntil(
      () => pageRequests.some((request) => request.at >= sentAt
        && request.path === `/api/agent/${sid}?systemPrompt=0`),
      20_000,
    );
    // Let that tick's status read finish while the run is still busy, then end
    // the run. The next tick is a full interval away, which is the late case.
    // A tick is a mechanism, not a user-visible outcome: when none happens the
    // final reply is released at the 20s deadline instead of failing the suite.
    if (tickObserved) await delay(1_000);
    releaseFinal();

    // The recorder times first appearance from its own sampling, so turn-1 and
    // tool content shown before the tick wait above is not mistaken for late.
    await waitUntil(async () => (await firstSeen("REPLY-RUN-FINAL")) !== null, 60_000);
    await waitIdle(sid);
    const firstTurnAt = await firstSeen("FIRST-TURN-TEXT");
    const toolAt = await firstSeen("TOOL-OUT-MARK");
    const finalAt = await firstSeen("REPLY-RUN-FINAL");
    console.log(
      `INFO own-run (stream frozen): reconcile tick ${tickObserved ? "observed" : "not observed within 20000ms"}`,
    );
    const ms = (value) => value === null ? "never" : `${value - sentAt}ms`;
    const finalLate = finalAt === null || finalWrittenAt === null ? "never" : `${finalAt - finalWrittenAt}ms`;
    console.log(
      `RESULT own-run (stream frozen): turn-1 text ${ms(firstTurnAt)}, tool output ${ms(toolAt)}, `
      + `final reply ${ms(finalAt)} (${finalLate} after it was written); reads=${JSON.stringify(reads(sid, sentAt))}`,
    );
    check(firstTurnAt !== null && firstTurnAt - sentAt <= BOUND_MS,
      `${sid}: turn-1 content was not shown within ${BOUND_MS}ms of send (observed ${ms(firstTurnAt)})`);
    check(toolAt !== null && toolAt - sentAt <= BOUND_MS,
      `${sid}: the tool result was not shown within ${BOUND_MS}ms of send (observed ${ms(toolAt)})`);
    check(finalAt !== null && finalWrittenAt !== null && finalAt - finalWrittenAt <= BOUND_MS,
      `${sid}: the final reply was not shown within ${BOUND_MS}ms of being written (observed ${finalLate})`);
  }

  // Idle first sighting: the selected session has no ring entry at selection;
  // another client's quick run is only the first entry the sidebar ever sees.
  {
    const sid = IDLE_FIRST;
    await loadSession(sid);
    const selectedAt = Date.now();
    assert.ok(await waitUntil(
      () => ringEvents.some((event) => event.at >= selectedAt),
      15_000,
    ), `${sid}: the sidebar never polled after the session was selected`);
    const entryAtSelection = ringEvents.some((event) => event.at >= selectedAt
      && event.writes.some((write) => write.sessionId === sid));
    check(!entryAtSelection, `${sid}: precondition failed — the ring already held an entry at selection`);

    frozen = true;
    plan = { single: { text: "REPLY-IDLE-FIRST", delayMs: 200 } };
    modelCalls = 0;
    const promptAt = Date.now();
    await desktopPrompt(sid, "REPLY-IDLE-FIRST");

    let shownAt = null;
    let ringAt = null;
    while (Date.now() - promptAt < OBSERVE_MS) {
      if (shownAt === null && await shows("REPLY-IDLE-FIRST")) shownAt = Date.now();
      if (ringAt === null) {
        const seen = ringEvents.find((event) => event.at >= promptAt
          && event.writes.some((write) => write.sessionId === sid));
        if (seen) ringAt = seen.at;
      }
      if (shownAt !== null && ringAt !== null) break;
      await delay(250);
    }
    await waitIdle(sid);
    const shown = shownAt === null ? `not within ${OBSERVE_MS}ms` : `${shownAt - promptAt}ms after the prompt`;
    const ring = ringAt === null ? "never" : `${ringAt - promptAt}ms after the prompt`;
    console.log(
      `RESULT idle-first (stream frozen): reply ${shown}; sidebar ring entry ${ring}; `
      + `reads=${JSON.stringify(reads(sid, promptAt))}`,
    );
    check(ringAt !== null, `${sid}: the sidebar never reported the first write for the selected session`);
    check(shownAt !== null && shownAt - promptAt <= BOUND_MS,
      `${sid}: the first external write was not shown within ${BOUND_MS}ms of the prompt (observed ${shown})`);
  }
} catch (error) {
  failures.push(`suite error: ${error?.stack ?? String(error)}`);
  if (page) await page.screenshot({ path: join(artifacts, "session-stall-recovery-failure.png") }).catch(() => {});
} finally {
  await browser?.close().catch(() => {});
  if (started) await started.stop().catch(() => {});
  if (proxy) await new Promise((resolve) => proxy.close(resolve)).catch(() => {});
  model.close();
  rmSync(agentDir, { recursive: true, force: true });
  if (nextEnvBefore !== null) {
    try {
      if (readFileSync(nextEnvPath, "utf8") !== nextEnvBefore) writeFileSync(nextEnvPath, nextEnvBefore);
    } catch { /* next-env.d.ts unavailable after the run */ }
  } else if (existsSync(nextEnvPath)) {
    rmSync(nextEnvPath, { force: true });
  }
}

if (failures.length) {
  for (const failure of failures) console.error(`FAIL: ${failure}`);
  assert.fail(`${failures.length} failure(s) reproduced`);
}
console.log("PASS: newest session content appears within the bound with a half-dead stream");