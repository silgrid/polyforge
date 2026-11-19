// carries: components/ChatWindow*, components/ChatInput*, components/MessageView*, hooks/useAgentSession*, lib/agent-*, lib/stream-client*, lib/session-*, app/api/agent/*
// carries: lib/page-stream-registry*
// No app/api/sessions/* declaration: this suite never calls a /api/sessions
// route itself — its own requests go to /api/agent/* and the /api/sessions
// reads it observes (and asserts on) are the page's own polls. Per
// docs/contributing.md a shared path is declared only by the 1–3 suites that
// exercise it directly, and the sessions routes keep their light-tier
// selection through e2e/claude-session-219.mjs, e2e/run.mjs and
// e2e/sessions-compression.mjs; this suite still runs in the full bar.
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
// Asserted (polyforge#137, plan A′ — event order plus staged timestamps; the
// 10s wall-clock bound is unchanged and stays the acceptance criterion):
// - the page's own run: turn-1 text and the tool result must be on screen
//   BEFORE the final reply is released, and the forced /api/sessions reads
//   that carried them start after the write-ring rise (the sidebar poll's
//   first recentSessionWrites sighting of the selected session) and before
//   the first 15s reconcile tick — the write-ring recovery path delivered
//   them, not the tick fallback. BOUND_MS still bounds every mark.
// - an idle page whose selected session has no watcher-ring entry at
//   selection: another client's quick run shows within BOUND_MS through a
//   forced read after the ring rise.
// Diagnostics (AC3): the RESULT lines carry the ring rise (with its
// generation), the first tick, and every /api/sessions read with its source
// (plain/force) and start time, all relative to the send; a failure line
// says which side it is on: the recovery path never triggered (no forced
// read between the ring rise and the first tick) or triggered but out of
// bound.
// The no-relaxation proof (AC2): E2E_STALL_DISABLE_RING_RECOVERY=1 makes the
// proxy cut the page's forced session reads — the observable outcome of a
// dead write-ring recovery path (refreshFromDisk returning early, the
// sidebar ignoring the selected session's rise) — while the 15s tick's
// plain read still lands the content late, the #128 signature (all three
// marks at once ≈ tick + 1s). The suite must then exit non-zero with the
// recovery-path-not-triggered lines.
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
//   E2E_STALL_DISABLE_RING_RECOVERY=1 node e2e/session-stall-recovery.mjs   (must exit non-zero)
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
// AC2's no-relaxation mutation, materialized as a switch so the proof reruns
// without touching the tree: with it on, the proxy cuts the page's forced
// session reads (the write-ring recovery path's own reads), leaving the 15s
// reconcile tick's plain read to land the content late — the #128 signature.
// The test commit of polyforge#137's unit shipped it ON by default (the
// committed mutation, red); this tree turns the default off (green) and
// leaves the env for the proof's rerun.
const disableRingRecovery = process.env.E2E_STALL_DISABLE_RING_RECOVERY === "1";
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
// The mutation's cut is armed only inside the frozen windows the scenarios
// define (right before their prompts), so a scenario's initial mount reads —
// which legitimately carry force=1 — still pass.
let cutForceReads = false;
const ringEvents = [];
const pageRequests = [];
const pageReads = new Map();

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
      if (disableRingRecovery && cutForceReads && /GET \/api\/sessions\/[^ ]*force=1/.test(head)) {
        // The mutation: the write-ring recovery path's read never completes.
        end();
        return;
      }
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
    if (!url.pathname.startsWith("/api/")) return;
    const entry = { at: Date.now(), path: url.pathname + url.search, done: false };
    pageRequests.push(entry);
    if (url.pathname.startsWith("/api/sessions/")) pageReads.set(request, entry);
  });
  page.on("response", (response) => {
    const url = new URL(response.url());
    if (!url.pathname.startsWith("/api/sessions/")) return;
    const entry = pageReads.get(response.request());
    if (entry) {
      entry.done = true;
      entry.doneAt = Date.now();
    }
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
    cutForceReads = false;
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
  // Every /api/sessions read of one session since a moment, with its source
  // (the recovery path reads carry force=1) and its start time relative to it.
  const readEvents = (sid, since) => pageRequests
    .filter((request) => request.at >= since && request.path.startsWith(`/api/sessions/${sid}?`))
    .map((request) => ({
      s: request.path.includes("force=1") ? "force" : "plain",
      at: request.at - since,
      done: request.done === true,
    }));
  // The first 15s reconcile tick's status read (or any other status read)
  // after a moment, relative to it — the fallback that must not be the one
  // carrying the recovery.
  const firstTickAt = (sid, since) => {
    const tick = pageRequests.find((request) => request.at >= since
      && request.path === `/api/agent/${sid}?systemPrompt=0`);
    return tick ? tick.at - since : null;
  };
  // The write-ring rise the sidebar polls report: the first poll response at
  // or after a moment that carries a watcher-ring entry for the session.
  const ringRiseEntry = (sid, since) => ringEvents.find((event) => event.at >= since
    && event.writes.some((write) => write.sessionId === sid));
  const ringRiseAt = (sid, since) => {
    const seen = ringRiseEntry(sid, since);
    return seen ? seen.at - since : null;
  };
  const ringRiseGeneration = (sid, since) => {
    const seen = ringRiseEntry(sid, since);
    return seen ? (seen.writes.find((write) => write.sessionId === sid)?.generation ?? null) : null;
  };

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
    // Hold the final reply until turn-1 and the tool result have been shown
    // (A′: they must appear before the final is released) or the acceptance
    // bound has elapsed — releasing then anyway lets the run end and the staged
    // diagnostics complete; the bound failure is already recorded.
    let releaseFinal;
    const finalGate = new Promise((resolve) => { releaseFinal = resolve; });
    plan = { toolFirst: true, final: { text: "REPLY-RUN-FINAL", delayMs: 0, gate: finalGate } };
    const sentAt = Date.now();
    cutForceReads = true;
    await page.locator("textarea").first().fill("phone asks while the network stalls");
    await page.getByRole("button", { name: "Send" }).first().click();
    await waitUntil(async () => (await firstSeen("FIRST-TURN-TEXT")) !== null
      && (await firstSeen("TOOL-OUT-MARK")) !== null, BOUND_MS);
    const releaseAt = Date.now();
    releaseFinal();

    // The recorder times first appearance from its own sampling, so turn-1 and
    // tool content shown during the bounded wait above is not mistaken for late.
    await waitUntil(async () => (await firstSeen("REPLY-RUN-FINAL")) !== null, 60_000);
    await waitIdle(sid);
    // The first 15s reconcile tick is a diagnostic anchor, not a user-visible
    // outcome: the frozen stream never settles the run for the page, so the
    // interval still fires. Its timestamp bounds the window the recovery
    // reads must precede; when none comes within 25s the RESULT line says so.
    await waitUntil(() => firstTickAt(sid, sentAt) !== null, 25_000);
    const tickAt = firstTickAt(sid, sentAt);
    const firstTurnAt = await firstSeen("FIRST-TURN-TEXT");
    const toolAt = await firstSeen("TOOL-OUT-MARK");
    const finalAt = await firstSeen("REPLY-RUN-FINAL");
    const riseAt = ringRiseAt(sid, sentAt);
    const riseGeneration = ringRiseGeneration(sid, sentAt);
    const readsList = readEvents(sid, sentAt);
    const firstForceRead = readsList.find((entry) => entry.s === "force" && entry.done);
    const ms = (value) => value === null ? "never" : `${value - sentAt}ms`;
    const finalLate = finalAt === null || finalWrittenAt === null ? "never" : `${finalAt - finalWrittenAt}ms`;
    // Which side of the AC3 taxonomy a failure is on: no forced read inside
    // the ring-rise→first-tick window is an untriggered recovery path; one
    // there with late content is a triggered-but-out-of-bound one.
    const recoveryState = firstForceRead === undefined || (tickAt !== null && firstForceRead.at >= tickAt)
      ? `recovery path not triggered — no forced read completed between the ring rise `
        + `(${riseAt === null ? "never" : `${riseAt}ms`}) and the first tick (${tickAt === null ? "none" : `${tickAt}ms`})`
      : `recovery path triggered but out of bound — first completed forced read at ${firstForceRead.at}ms`;
    console.log(
      `INFO own-run (stream frozen): final reply released ${releaseAt - sentAt}ms after send; `
      + `first reconcile tick ${tickAt === null ? "not observed within 25000ms" : `observed at ${tickAt}ms`}`,
    );
    console.log(
      `RESULT own-run (stream frozen): turn-1 text ${ms(firstTurnAt)}, tool output ${ms(toolAt)}, `
      + `final reply ${ms(finalAt)} (${finalLate} after it was written); `
      + `ring rise ${riseAt === null ? "never" : `${riseAt}ms (generation ${riseGeneration})`}; `
      + `first tick ${tickAt === null ? "none within 25000ms" : `${tickAt}ms`}; `
      + `reads=${JSON.stringify(readsList)}`,
    );
    check(firstTurnAt !== null && firstTurnAt - sentAt <= BOUND_MS,
      `${sid}: turn-1 content was not shown within ${BOUND_MS}ms of send (observed ${ms(firstTurnAt)}; ${recoveryState})`);
    check(toolAt !== null && toolAt - sentAt <= BOUND_MS,
      `${sid}: the tool result was not shown within ${BOUND_MS}ms of send (observed ${ms(toolAt)}; ${recoveryState})`);
    check(finalAt !== null && finalWrittenAt !== null && finalAt - finalWrittenAt <= BOUND_MS,
      `${sid}: the final reply was not shown within ${BOUND_MS}ms of being written (observed ${finalLate}; ${recoveryState})`);
    // A′ order: the recovery must show turn-1 and the tool result before the
    // final reply is released — while the run is still busy.
    check(firstTurnAt !== null && toolAt !== null && firstTurnAt < releaseAt && toolAt < releaseAt,
      `${sid}: turn-1/tool content did not appear before the final reply was released `
      + `(turn-1 ${ms(firstTurnAt)}, tool ${ms(toolAt)}, released ${releaseAt - sentAt}ms; ${recoveryState})`);
    // A′ order: the read that carried them runs after the ring rise and
    // before the first 15s tick — the write-ring recovery path, not the tick
    // fallback.
    check(riseAt !== null && firstForceRead !== undefined && firstForceRead.at >= riseAt
        && (tickAt === null || firstForceRead.at < tickAt),
      `${sid}: the recovery read did not run between the write-ring rise and the first tick `
      + `(ring rise ${riseAt === null ? "never" : `${riseAt}ms`}, `
      + `first completed forced read ${firstForceRead === undefined ? "never" : `${firstForceRead.at}ms`}, `
      + `first tick ${tickAt === null ? "none" : `${tickAt}ms`}; recovery path not triggered)`);
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
    cutForceReads = true;
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
    const readsList = readEvents(sid, promptAt);
    const firstForceRead = readsList.find((entry) => entry.s === "force" && entry.done);
    const shown = shownAt === null ? `not within ${OBSERVE_MS}ms` : `${shownAt - promptAt}ms after the prompt`;
    const ring = ringAt === null ? "never" : `${ringAt - promptAt}ms after the prompt`;
    console.log(
      `RESULT idle-first (stream frozen): reply ${shown}; sidebar ring entry ${ring}; `
      + `reads=${JSON.stringify(readsList)}`,
    );
    check(ringAt !== null, `${sid}: the sidebar never reported the first write for the selected session`);
    check(shownAt !== null && shownAt - promptAt <= BOUND_MS,
      `${sid}: the first external write was not shown within ${BOUND_MS}ms of the prompt (observed ${shown}; `
      + `${firstForceRead === undefined
        ? "recovery path not triggered — no forced read completed after the ring rise"
        : `recovery path triggered but out of bound — first completed forced read at ${firstForceRead.at}ms`})`);
    // A′ order carried to the idle-first sighting: the reply is shown by a
    // forced read that starts after the ring rise — the write-ring recovery
    // path, not an accident of timing.
    check(shownAt !== null && ringAt !== null && firstForceRead !== undefined
        && firstForceRead.at >= ringAt - promptAt && firstForceRead.at <= shownAt - promptAt,
      `${sid}: the reply was not carried by a forced read after the ring rise `
      + `(ring entry ${ring}, first completed forced read ${firstForceRead === undefined ? "never" : `${firstForceRead.at}ms`}, shown ${shown})`);
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