// carries: lib/manual-fetch*
// the session page's manual "fetch latest" affordance.
//
// Authored pre-code from artifacts/spec.md (its acceptance criteria and
// scenario blocks), never from the implementation. The suite mounts the real
// app (`next dev`, Chromium), seeds throwaway sessions under a temporary
// PI_CODING_AGENT_DIR and uses a local fake OpenAI-compatible model for runs.
// A TCP proxy lets one scenario freeze only the selected session's event
// stream, so the page's stream stays ESTABLISHED while no bytes arrive.
//
// Each passing scenario prints its own `PASS S<n>` line; the baseline run on
// the pre-implementation tree prints `FAIL S<n>` instead, so no marker matches.
//
//   node e2e/manual-fetch.mjs
//   npm run test:e2e:all -- manual-fetch.mjs
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

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const artifacts = join(root, "test-results/e2e");
mkdirSync(artifacts, { recursive: true });
const distDir = process.env.PI_WEB_DIST_DIR || ".next/e2e/manual-fetch";
rmSync(join(root, distDir, "dev/lock"), { force: true });

const nextEnvPath = join(root, "next-env.d.ts");
const nextEnvBefore = existsSync(nextEnvPath) ? readFileSync(nextEnvPath, "utf8") : null;

const failures = [];
function scenario(id, title) {
  const errors = [];
  return {
    id,
    title,
    check(condition, message) {
      if (!condition) errors.push(message);
    },
    fail(message) {
      errors.push(message);
    },
    end() {
      if (errors.length) {
        for (const error of errors) console.error(`FAIL ${id}: ${error}`);
        failures.push(id);
        return false;
      }
      console.log(`PASS ${id}`);
      return true;
    },
  };
}

// ---- fake model -------------------------------------------------------------
let plan = null;
const model = createHttpServer(async (request, response) => {
  await new Promise((resolve) => {
    request.on("data", () => { /* drain */ });
    request.on("end", resolve);
    request.on("error", resolve);
  });
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
  await delay(current.delayMs ?? 50);
  response.write(chunk({ role: "assistant", content: current.text }));
  response.write(chunk({}, "stop", { usage: { prompt_tokens: 3, completion_tokens: 2, total_tokens: 5 } }));
  response.write("data: [DONE]\n\n");
  response.end();
});
model.listen(0, "127.0.0.1");
await new Promise((resolve) => model.once("listening", resolve));
const modelPort = model.address().port;

// ---- fixtures ---------------------------------------------------------------
const agentDir = mkdtempSync(join(tmpdir(), "pi-web-manual-fetch-"));
const project = join(agentDir, "project");
const sessionDir = join(agentDir, "sessions", "manual-fetch");
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

const stamp = "2026-10-30T00:00:00.000Z";
const fileFor = (id) => join(sessionDir, `${stamp.replaceAll(":", "-")}_${id}.jsonl`);
// Assistant entries carry pi's full message shape (content blocks, usage,
// provider/model/api), as e2e/session-stall-recovery.mjs seeds them: a bare
// string content makes pi fail every later model call in the session.
const assistantMessage = (text) => ({
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
  timestamp: Date.parse(stamp),
});
const entry = (id, parentId, role, content) => ({
  type: "message",
  id,
  parentId,
  timestamp: stamp,
  message: role === "assistant" ? assistantMessage(content) : { role, content },
});
function writeSession(id, entries) {
  writeFileSync(fileFor(id), [
    { type: "session", version: 3, id, timestamp: stamp, cwd: project },
    ...entries,
  ].map((value) => JSON.stringify(value)).join("\n") + "\n");
  return fileFor(id);
}

const MAIN = "manual-main";
const HALF = "manual-half";
const HISTORY = "manual-history";
const SAME = "manual-same";
const SCROLL = "manual-scroll";
const RUN = "manual-run";

writeSession(MAIN, [entry("u1", null, "user", "initial main prompt"), entry("a1", "u1", "assistant", "initial main answer")]);
writeSession(HALF, [entry("u1", null, "user", "initial half prompt"), entry("a1", "u1", "assistant", "initial half answer")]);
writeSession(SAME, [
  entry("u1", null, "user", "SAME-TEXT-175"),
  entry("a1", "u1", "assistant", "first answer"),
]);
writeSession(RUN, [entry("u1", null, "user", "initial run prompt"), entry("a1", "u1", "assistant", "initial run answer")]);
const historyEntries = Array.from({ length: 160 }, (_, index) => entry(
  `e${index}`,
  index ? `e${index - 1}` : null,
  index % 2 ? "assistant" : "user",
  `HISTORY message ${String(index).padStart(3, "0")}`,
));
writeSession(HISTORY, historyEntries);
const scrollEntries = Array.from({ length: 160 }, (_, index) => entry(
  `s${index}`,
  index ? `s${index - 1}` : null,
  index % 2 ? "assistant" : "user",
  `SCROLL message ${String(index).padStart(3, "0")}`,
));
writeSession(SCROLL, scrollEntries);

// ---- next dev + the freeze-capable proxy ------------------------------------
let started;
let proxy;
let browser;
const conns = new Set();
let frozen = false;
const buffered = [];

try {
  started = await startNextServer({
    name: "manual-fetch",
    logFile: join(artifacts, "manual-fetch-server.log"),
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
    const connection = { client, upstream, isEvents: false, dead: false, openedAt: Date.now(), ready: false };
    conns.add(connection);
    client.on("data", (data) => {
      if (/GET \/api\/agent\/[^ ]+\/events/.test(data.toString("latin1"))) {
        connection.isEvents = true;
        connection.openedAt = Date.now();
      }
      upstream.write(data);
    });
    upstream.on("data", (data) => {
      if (frozen && connection.isEvents) {
        buffered.push({ connection, data });
        return;
      }
      if (connection.isEvents && data.toString("utf8").includes('"type":"connected"')) connection.ready = true;
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

  browser = await chromium.launch();

  const shows = (page, text) => page.evaluate((value) => document.body.innerText.includes(value), text);
  async function waitUntil(predicate, timeoutMs, intervalMs = 100) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      if (await predicate()) return true;
      await delay(intervalMs);
    }
    return await predicate();
  }
  async function api(path, init) {
    const response = await fetch(`${started.base}${path}`, { signal: AbortSignal.timeout(60_000), ...init });
    assert.ok(response.ok, `${init?.method ?? "GET"} ${path} -> ${response.status}`);
    return response.status === 204 ? null : response.json();
  }
  async function prompt(sid, message) {
    return api(`/api/agent/${sid}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ type: "prompt", message }),
    });
  }
  async function waitIdle(sid) {
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      const body = await api("/api/agent/running");
      if (!body.runningSessionIds.includes(sid)) return;
      await delay(200);
    }
    throw new Error(`${sid}: run did not end`);
  }
  async function runReply(sid, text, promptText = text) {
    plan = { text, delayMs: 50 };
    await prompt(sid, promptText);
    await waitIdle(sid);
  }
  function flushFrozenEvents() {
    frozen = false;
    for (const item of buffered.splice(0)) {
      if (!item.connection.dead) item.connection.client.write(item.data);
    }
  }

  async function newPage({ viewport, mobile = false, locale = "en-US" } = {}) {
    const context = await browser.newContext({
      viewport: viewport ?? { width: 1280, height: 900 },
      isMobile: mobile,
      hasTouch: mobile,
      locale,
    });
    await context.addInitScript(() => {
      try { window.localStorage.setItem("pi-web:session-age-filter-days", "0"); } catch { /* storage blocked */ }
    });
    // The passive external-write net is not what this WI changes: keep its
    // writer signal out of the page so a manual trigger is the only thing that
    // can surface a run the frozen stream never delivered.
    await context.route("**/api/agent/running", async (route) => {
      try {
        const response = await route.fetch();
        const body = await response.json();
        body.recentSessionWrites = [];
        await route.fulfill({
          status: response.status(),
          headers: { ...response.headers(), "content-type": "application/json" },
          body: JSON.stringify(body),
        });
      } catch {
        await route.continue().catch(() => {});
      }
    });
    const page = await context.newPage();
    page.setDefaultTimeout(60_000);
    const requests = [];
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname.startsWith("/api/")) requests.push({ at: Date.now(), path: url.pathname + url.search });
    });
    return { context, page, requests };
  }

  async function openSession(page, sid, marker) {
    await page.goto(`${base}/?session=${sid}`, { waitUntil: "domcontentloaded" });
    assert.ok(await waitUntil(() => shows(page, marker), 60_000), `${sid}: initial content did not render`);
    assert.ok(await waitUntil(
      () => [...conns].some((connection) => connection.isEvents && !connection.dead),
      30_000,
    ), `${sid}: the page never opened an event stream`);
    assert.ok(await waitUntil(
      () => (page.__requests ?? []).some((request) => request.path === "/api/agent/running"),
      15_000,
    ), `${sid}: the page never polled /api/agent/running`);
  }

  // Attach the request log to the page object once, so openSession can use it.
  function instrument(created) {
    created.page.__requests = created.requests;
    return created;
  }

  // the ↻ control is the chat's floating button in its refresh
  // form (shown at the tail of a saved session; scrolled up the same button is
  // ↓ "scroll to latest" and fetches nothing). The toolbar has none: find the
  // shown floating button whose label is the manual fetch's (the ↻ glyph + its
  // refresh label), or null when it is absent, hidden or in its ↓ form.
  async function refreshElement(page) {
    const handle = await page.evaluateHandle(() => {
      const shown = (element) => element.getClientRects().length > 0 && getComputedStyle(element).visibility === "visible";
      const textOf = (element) => `${element.innerText ?? ""} ${element.getAttribute("aria-label") ?? ""} ${element.getAttribute("title") ?? ""}`;
      return Array.from(document.querySelectorAll(".chat-scroll-to-bottom"))
        .filter(shown)
        .find((button) => /↻|⟳|fetch|refresh|刷新|拉取|擷取/i.test(textOf(button))) ?? null;
    });
    const element = handle.asElement();
    if (element) return element;
    await handle.dispose();
    return null;
  }
  async function clickRefresh(page) {
    const element = await refreshElement(page);
    if (!element) return false;
    await element.click();
    await element.dispose();
    return true;
  }
  async function refreshBusy(page) {
    const element = await refreshElement(page);
    if (!element) return false;
    const busy = await element.evaluate((button) => button.getAttribute("aria-busy") === "true"
      || Boolean(button.querySelector('.animate-spin, [class*="spin"]')));
    await element.dispose();
    return busy;
  }
  async function refreshLabel(page) {
    const element = await refreshElement(page);
    if (!element) return null;
    const label = await element.evaluate((button) => `${button.innerText ?? ""} ${button.getAttribute("aria-label") ?? ""}`.trim());
    await element.dispose();
    return label;
  }
  const count = (requests, predicate, since = 0) => requests.filter((request) => request.at >= since && predicate(request)).length;
  const forceReads = (requests, sid, since) => count(requests, (request) => request.path.startsWith(`/api/sessions/${sid}?`) && request.path.includes("force=1"), since);
  const statusReads = (requests, sid, since) => count(requests, (request) => request.path === `/api/agent/${sid}?systemPrompt=0`, since);
  const streamRequests = (requests, sid, since) => count(requests, (request) => request.path.startsWith(`/api/agent/${sid}/events`), since);

  const stopButton = (page) => page.locator('button[title="Stop agent"], button[aria-label="Stop agent"]');
  async function stopVisible(page) {
    return stopButton(page).first().isVisible().catch(() => false);
  }
  async function dragUp(page, { fromBottom = true } = {}) {
    const cdp = await page.context().newCDPSession(page);
    try {
      await page.evaluate(() => {
        const list = document.querySelector("[data-chat-focused]")
          ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
        if (list) list.scrollTop = list.scrollHeight;
      });
      await delay(250);
      const box = await page.evaluate((useBottom) => {
        const list = document.querySelector("[data-chat-focused]")
          ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
        if (!list) return null;
        if (!useBottom) list.scrollTop = Math.max(0, Math.floor((list.scrollHeight - list.clientHeight) / 2));
        const rect = list.getBoundingClientRect();
        return { x: rect.left + rect.width / 2, y: rect.bottom - 20, bottom: rect.bottom };
      }, fromBottom);
      if (!box) return false;
      const startY = box.y;
      const endY = startY - 120;
      await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: box.x, y: startY }] });
      for (let step = 1; step <= 4; step += 1) {
        await cdp.send("Input.dispatchTouchEvent", {
          type: "touchMove",
          touchPoints: [{ x: box.x, y: startY + ((endY - startY) * step) / 4 }],
        });
        await delay(40);
      }
      await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
      return true;
    } finally {
      await cdp.detach().catch(() => {});
    }
  }
  async function dragDownAtTop(page) {
    const box = await page.evaluate(() => {
      const list = document.querySelector("[data-chat-focused]")
        ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
      if (!list) return null;
      list.scrollTop = 0;
      const rect = list.getBoundingClientRect();
      return { x: rect.left + rect.width / 2, y: rect.top + 20 };
    });
    if (!box) return false;
    const cdp = await page.context().newCDPSession(page);
    try {
      await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: box.x, y: box.y }] });
      for (let step = 1; step <= 4; step += 1) {
        await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: box.x, y: box.y + step * 30 }] });
        await delay(40);
      }
      await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
      return true;
    } finally {
      await cdp.detach().catch(() => {});
    }
  }

  // ---- S1/S2/S15/S19: half-dead stream, newest reply on disk --------------
  {
    const s1 = scenario("S1", "Half-dead stream, newest reply already on disk");
    const s2 = scenario("S2", "The fetch reports the version it read");
    const s15 = scenario("S15", "Updated, with the local completion time");
    const s19 = scenario("S19", "Reading at the bottom stays at the bottom");
    let created;
    try {
      created = instrument(await newPage());
      const { page, requests } = created;
      await openSession(page, HALF, "initial half answer");
      const control = await refreshElement(page);
      if (!control) {
        for (const s of [s1, s2, s15, s19]) s.fail("no ↻ refresh form of the chat's floating button");
        await control?.dispose();
      } else {
        await control.dispose();
        frozen = true;
        await delay(200);
        await runReply(HALF, "REPLY-HALF-DEAD-175");
        const triggerAt = Date.now();
        const clicked = await clickRefresh(page);
        if (!clicked) for (const s of [s1, s2, s15, s19]) s.fail("the refresh control could not be clicked");
        else {
          const rendered = await waitUntil(() => shows(page, "REPLY-HALF-DEAD-175"), 2_000, 100);
          s1.check(rendered, "the on-disk reply was not rendered within 2s of the trigger");
          if (rendered) {
            const firstSeen = await waitUntil(() => shows(page, "REPLY-HALF-DEAD-175"), 0);
            s1.check(firstSeen, "the on-disk reply was not rendered");
          }
          s1.check(await waitUntil(() => forceReads(requests, HALF, triggerAt) === 1, 5_000),
            `expected one forced read, observed ${forceReads(requests, HALF, triggerAt)}`);
          const fresh = await api(`/api/sessions/${HALF}?force=1&deferThinking=1&deferMedia=1&tree=summary`);
          const domIds = await page.evaluate(() => Array.from(document.querySelectorAll("[data-entry-id]")).map((node) => node.dataset.entryId));
          const freshIds = fresh.context.entryIds ?? [];
          s2.check(freshIds.every((id) => domIds.includes(id)), `the view is missing read entries: ${freshIds.filter((id) => !domIds.includes(id)).join(", ")}`);
          const positions = freshIds.filter((id) => domIds.includes(id)).map((id) => domIds.indexOf(id));
          s2.check(positions.every((value, index) => index === 0 || value > positions[index - 1]),
            "the read entries were not rendered in session order");
          s15.check(await shows(page, "Updated to "), "the updated notice did not appear");
          const notice = await page.evaluate(() => document.body.innerText.match(/Updated to\s+(\d{2}):(\d{2})/i));
          s15.check(Boolean(notice), "the updated notice carries no hh:mm completion time");
          if (notice) {
            const now = new Date();
            const minutes = Number(notice[1]) * 60 + Number(notice[2]);
            s15.check(Math.abs(minutes - (now.getHours() * 60 + now.getMinutes())) <= 2, `the notice time ${notice[1]}:${notice[2]} is not the local completion time`);
          }
          await waitUntil(async () => {
            const list = await page.evaluate(() => {
              const node = document.querySelector("[data-chat-focused]") ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
              return node ? { atTail: node.scrollTop + node.clientHeight >= node.scrollHeight - 8 } : null;
            });
            return list?.atTail === true;
          }, 3_000);
          const atTail = await page.evaluate(() => {
            const node = document.querySelector("[data-chat-focused]") ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
            return node ? node.scrollTop + node.clientHeight >= node.scrollHeight - 8 : false;
          });
          s19.check(atTail, "the list was not still at its bottom after the fetch");
          s19.check(await shows(page, "REPLY-HALF-DEAD-175"), "the newest content is not visible at the bottom");
        }
      }
    } catch (error) {
      for (const s of [s1, s2, s15, s19]) s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s1.end();
      s2.end();
      s15.end();
      s19.end();
      await created?.context.close().catch(() => {});
      frozen = false;
    }
  }

  // ---- S3/S9/S12/S16: desktop control, healthy stream, bound, current -----
  {
    const s3 = scenario("S3", "Desktop entry point");
    const s9 = scenario("S9", "Healthy stream is left alone");
    const s12 = scenario("S12", "Request bound per trigger");
    const s16 = scenario("S16", "Already current");
    let created;
    try {
      created = instrument(await newPage());
      const { page, requests } = created;
      await openSession(page, MAIN, "initial main answer");
      const control = await refreshElement(page);
      s3.check(Boolean(control), "no ↻ refresh form of the floating button at the chat's tail at desktop width");
      await control?.dispose();
      const triggerAt = Date.now();
      if (!(await clickRefresh(page))) {
        for (const s of [s3, s9, s12, s16]) s.fail("the refresh control could not be clicked");
      } else {
        s3.check(await waitUntil(() => forceReads(requests, MAIN, triggerAt) === 1, 5_000),
          `clicking the control did not start exactly one manual fetch (forced reads: ${forceReads(requests, MAIN, triggerAt)})`);
        await waitUntil(() => shows(page, "Already up to date"), 5_000);
        s9.check(streamRequests(requests, MAIN, triggerAt) === 0,
          `a stream with bytes within 30s was reconnected (${streamRequests(requests, MAIN, triggerAt)} event requests)`);
        s9.check(forceReads(requests, MAIN, triggerAt) === 1, `healthy trigger forced ${forceReads(requests, MAIN, triggerAt)} reads`);
        s12.check(forceReads(requests, MAIN, triggerAt) <= 1, "more than one forced read");
        s12.check(statusReads(requests, MAIN, triggerAt) <= 1, "more than one run-state read");
        s12.check(streamRequests(requests, MAIN, triggerAt) <= 1, "more than one stream reconnect");
        s16.check(await shows(page, "Already up to date"), "the already-current notice did not appear");
      }
    } catch (error) {
      for (const s of [s3, s9, s12, s16]) s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s3.end();
      s9.end();
      s12.end();
      s16.end();
      await created?.context.close().catch(() => {});
    }
  }

  // ---- S4/S5: mobile control and the touch pull-up gesture -----------------
  {
    const s4 = scenario("S4", "Mobile entry point");
    const s5 = scenario("S5", "Pull-up at the bottom starts one fetch");
    let created;
    try {
      created = instrument(await newPage({ viewport: { width: 390, height: 844 }, mobile: true }));
      const { page, requests } = created;
      await openSession(page, HALF, "initial half answer");
      const control = await refreshElement(page);
      s4.check(Boolean(control), "no ↻ refresh form of the floating button is visible/tappable at 390x844");
      await control?.dispose();
      const triggerAt = Date.now();
      if (!(await clickRefresh(page))) s4.fail("the mobile refresh control could not be tapped");
      else s4.check(await waitUntil(() => forceReads(requests, HALF, triggerAt) === 1, 5_000),
        "tapping the mobile control did not start the same one manual fetch");
      // The tap's fetch must have settled first: a pull during it would join it (no
      // new read), and a read logged in the same millisecond as beforePull would count.
      await waitUntil(async () => !(await refreshBusy(page)), 5_000);
      await delay(100);

      const beforePull = Date.now();
      const dragged = await dragUp(page);
      s5.check(dragged, "could not dispatch the touch pull-up");
      s5.check(await waitUntil(() => forceReads(requests, HALF, beforePull) === 1, 5_000),
        `the bottom pull-up did not start exactly one fetch (forced reads: ${forceReads(requests, HALF, beforePull)})`);
    } catch (error) {
      s4.fail(`suite error: ${error?.stack ?? String(error)}`);
      s5.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s4.end();
      s5.end();
      await created?.context.close().catch(() => {});
    }
  }

  // ---- S6/S7: the negative touch regions -----------------------------------
  {
    const s6 = scenario("S6", "A drag in earlier history scrolls, never fetches");
    let created;
    try {
      created = instrument(await newPage({ viewport: { width: 390, height: 844 }, mobile: true }));
      const { page, requests } = created;
      await openSession(page, HISTORY, "HISTORY message 159");
      s6.check(Boolean(await refreshElement(page)), "the manual fetch control is not present, so the negative case proves nothing");
      const beforeHistoryDrag = Date.now();
      const historyDrag = await dragUp(page, { fromBottom: false });
      s6.check(historyDrag, "could not dispatch the earlier-history drag");
      await delay(1_200);
      s6.check(forceReads(requests, HISTORY, beforeHistoryDrag) === 0, "a drag in earlier history started a fetch");
    } catch (error) {
      s6.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s6.end();
      await created?.context.close().catch(() => {});
    }
  }
  {
    const s7 = scenario("S7", "The top area stays with loading earlier messages");
    let created;
    try {
      created = instrument(await newPage({ viewport: { width: 390, height: 844 }, mobile: true }));
      const { page, requests } = created;
      await openSession(page, HISTORY, "HISTORY message 159");
      s7.check(Boolean(await refreshElement(page)), "the manual fetch control is not present, so the negative case proves nothing");
      const beforeTopDrag = Date.now();
      const beforeCount = await page.evaluate(() => document.querySelectorAll("[data-entry-id]").length);
      const topDrag = await dragDownAtTop(page);
      s7.check(topDrag, "could not dispatch the pull-down at the top");
      s7.check(await waitUntil(() => page.evaluate(() => document.querySelectorAll("[data-entry-id]").length > 50), 15_000),
        "the existing load-earlier prefetch did not load an older page at the top");
      await delay(1_200);
      s7.check(forceReads(requests, HISTORY, beforeTopDrag) === 0, "a pull-down at the top started a manual fetch");
      const afterCount = await page.evaluate(() => document.querySelectorAll("[data-entry-id]").length);
      s7.check(afterCount > beforeCount || beforeCount > 50, "the load-earlier prefetch did not behave as before");
    } catch (error) {
      s7.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s7.end();
      await created?.context.close().catch(() => {});
    }
  }

  // ---- S8: a new unsaved session hides both entry points -------------------
  {
    const s = scenario("S8", "New unsaved session hides both entry points");
    let created;
    try {
      const saved = instrument(await newPage({ viewport: { width: 390, height: 844 }, mobile: true }));
      await openSession(saved.page, MAIN, "initial main answer");
      s.check(Boolean(await refreshElement(saved.page)), "the control is absent on a saved session, so the unsaved case proves nothing");
      await saved.context.close().catch(() => {});
      // A fresh browser context: the app's per-tab memory (sessionStorage)
      // would otherwise reopen the tab's last session at `/`.
      created = instrument(await newPage({ viewport: { width: 390, height: 844 }, mobile: true }));
      const { page, requests } = created;
      await page.goto(`${base}/`, { waitUntil: "domcontentloaded" });
      await delay(1_000);
      s.check((await refreshElement(page)) === null, "a new unsaved session still shows the ↻ control");
      const forceBefore = count(requests, (request) => request.path.startsWith("/api/sessions/") && request.path.includes("force=1"));
      await dragUp(page).catch(() => false);
      await delay(1_000);
      const forceAfter = count(requests, (request) => request.path.startsWith("/api/sessions/") && request.path.includes("force=1"));
      s.check(forceAfter === forceBefore, "a pull-up on a new unsaved session started a fetch");
    } catch (error) {
      s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s.end();
      await created?.context.close().catch(() => {});
    }
  }

  // ---- S10: a stream silent for more than one heartbeat reconnects once ----
  {
    const s = scenario("S10", "Silent stream is reconnected once");
    let created;
    try {
      created = instrument(await newPage());
      const { page, requests } = created;
      await openSession(page, MAIN, "initial main answer");
      const control = await refreshElement(page);
      if (!control) {
        s.fail("no ↻ refresh form of the chat's floating button");
        await control?.dispose();
      } else {
        await control.dispose();
        frozen = true;
        await delay(31_000);
        const triggerAt = Date.now();
        s.check(await clickRefresh(page), "the refresh control could not be clicked");
        s.check(await waitUntil(() => streamRequests(requests, MAIN, triggerAt) === 1, 5_000),
          `expected exactly one reconnect, observed ${streamRequests(requests, MAIN, triggerAt)}`);
        s.check(await waitUntil(() => forceReads(requests, MAIN, triggerAt) === 1, 5_000), "the silent-stream trigger did not force one read");
      }
    } catch (error) {
      s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s.end();
      await created?.context.close().catch(() => {});
      frozen = false;
    }
  }

  // ---- S11: server idle but the view still shows running -------------------
  {
    const s = scenario("S11", "Run state reconciled from the server");
    let created;
    try {
      created = instrument(await newPage());
      const { page, requests } = created;
      await openSession(page, RUN, "initial run answer");
      const control = await refreshElement(page);
      if (!control) {
        s.fail("no ↻ refresh form of the chat's floating button");
        await control?.dispose();
      } else {
        await control.dispose();
        // Sending waits for a ready stream: freeze only once its `connected`
        // event reached the page, or the prompt never leaves it.
        assert.ok(await waitUntil(
          () => [...conns].some((connection) => connection.isEvents && !connection.dead && connection.ready),
          30_000,
        ), `${RUN}: the event stream never delivered its connected event`);
        frozen = true;
        plan = { text: "REPLY-RUN-RECONCILED-175", delayMs: 50 };
        await page.locator("textarea").first().fill("run while the stream is frozen");
        await page.getByRole("button", { name: "Send" }).first().click();
        s.check(await waitUntil(() => stopVisible(page), 15_000),
          "the page never showed the run as running");
        await waitIdle(RUN);
        s.check(await stopVisible(page),
          "precondition failed: the page stopped showing the run before the trigger");
        const triggerAt = Date.now();
        s.check(await clickRefresh(page), "the refresh control could not be clicked");
        s.check(await waitUntil(async () => !(await stopVisible(page)), 2_000),
          "the stale running state did not end within the trigger");
        s.check(await shows(page, "REPLY-RUN-RECONCILED-175"), "the finished reply was not rendered");
        s.check(statusReads(requests, RUN, triggerAt) <= 1, "more than one run-state read was issued");
      }
    } catch (error) {
      s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s.end();
      await created?.context.close().catch(() => {});
      frozen = false;
    }
  }

  // ---- S13/S14: in-flight merge and in-progress indication -----------------
  {
    const s13 = scenario("S13", "Second trigger during an in-flight fetch");
    const s14 = scenario("S14", "In-progress indication");
    let created;
    try {
      created = instrument(await newPage());
      const { page, requests } = created;
      await openSession(page, MAIN, "initial main answer");
      const control = await refreshElement(page);
      if (!control) {
        s13.fail("no ↻ refresh form of the chat's floating button");
        s14.fail("no ↻ refresh form of the chat's floating button");
        await control?.dispose();
      } else {
        await control.dispose();
        await page.route(/\/api\/sessions\/[^/]+\?.*force=1/, async (route) => {
          await delay(1_500);
          await route.continue().catch(() => {});
        });
        const triggerAt = Date.now();
        s13.check(await clickRefresh(page), "the first trigger could not be clicked");
        await delay(150);
        s14.check(await refreshBusy(page), "the ↻ control did not show an in-progress state");
        s13.check(await clickRefresh(page), "the second trigger could not be clicked");
        await delay(400);
        s13.check(forceReads(requests, MAIN, triggerAt) === 1,
          `a second trigger issued another forced read (${forceReads(requests, MAIN, triggerAt)} total)`);
        s13.check(statusReads(requests, MAIN, triggerAt) <= 1, "a second trigger issued another run-state read");
        s13.check(await refreshBusy(page), "the in-progress indication ended before the first fetch");
        s13.check(await waitUntil(() => shows(page, "Already up to date"), 6_000), "the in-flight fetch never ended in its notice");
        s14.check(!(await refreshBusy(page)), "the in-progress indication did not clear");
        s13.check(forceReads(requests, MAIN, triggerAt) === 1, "the merged trigger issued another forced read after settling");
      }
    } catch (error) {
      s13.fail(`suite error: ${error?.stack ?? String(error)}`);
      s14.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s13.end();
      s14.end();
      await created?.context.close().catch(() => {});
    }
  }

  // ---- S17: a failed read keeps the conversation ---------------------------
  {
    const s = scenario("S17", "Failure keeps the conversation");
    let created;
    try {
      created = instrument(await newPage());
      const { page, requests } = created;
      await openSession(page, MAIN, "initial main answer");
      const control = await refreshElement(page);
      if (!control) {
        s.fail("no ↻ refresh form of the chat's floating button");
        await control?.dispose();
      } else {
        await control.dispose();
        await page.locator("textarea").first().fill("composer draft survives");
        await page.route(/\/api\/sessions\/[^/]+\?.*force=1/, (route) => route.abort("failed"));
        const triggerAt = Date.now();
        s.check(await clickRefresh(page), "the refresh control could not be clicked");
        s.check(await waitUntil(() => shows(page, "Fetch failed"), 5_000), "the failure notice did not appear");
        s.check(await shows(page, "initial main answer"), "the conversation was not kept after the failure");
        s.check(await page.locator("textarea").first().inputValue() === "composer draft survives", "the composer content was not kept after the failure");
        s.check(await waitUntil(async () => !(await refreshBusy(page)), 2_000), "the progress indication did not end after the failure");
        s.check(forceReads(requests, MAIN, triggerAt) === 1, "the failed trigger did not attempt exactly one forced read");
      }
    } catch (error) {
      s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s.end();
      await created?.context.close().catch(() => {});
    }
  }

  // ---- S18: an unresponsive read gives up at 15 seconds --------------------
  {
    const s = scenario("S18", "Unresponsive read");
    let created;
    try {
      created = instrument(await newPage());
      const { page } = created;
      await openSession(page, MAIN, "initial main answer");
      const control = await refreshElement(page);
      if (!control) {
        s.fail("no ↻ refresh form of the chat's floating button");
        await control?.dispose();
      } else {
        await control.dispose();
        await page.route(/\/api\/sessions\/[^/]+\?.*force=1/, (route) => {
          setTimeout(() => { void route.abort("failed").catch(() => {}); }, 20_000);
        });
        const triggerAt = Date.now();
        s.check(await clickRefresh(page), "the refresh control could not be clicked");
        s.check(await waitUntil(() => shows(page, "Fetch failed"), 15_500, 100),
          "the unresponsive read did not report failure within 15s");
        const elapsed = Date.now() - triggerAt;
        s.check(elapsed <= 15_500, `the failure took ${elapsed}ms, above the 15s bound`);
        s.check(await waitUntil(async () => !(await refreshBusy(page)), 2_000), "the spinner did not stop at the timeout");
        s.check(await shows(page, "initial main answer"), "the conversation was not intact after the timeout");
      }
    } catch (error) {
      s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s.end();
      await created?.context.close().catch(() => {});
    }
  }

  // ---- S19/S20: reading position is preserved -------------------------------
  // scrolled up, the floating button is ↓ (scroll to latest), not
  // ↻: there is no fetch entry point there (owner ruling), so reading earlier
  // content is never disturbed by one, and ↓ only scrolls down without a read.
  {
    const s = scenario("S20", "Reading earlier content keeps that content in place");
    let created;
    try {
      created = instrument(await newPage());
      const { page, requests } = created;
      await openSession(page, SCROLL, "SCROLL message 159");
      const control = await refreshElement(page);
      if (!control) {
        s.fail("no ↻ refresh form of the chat's floating button");
        await control?.dispose();
      } else {
        await control.dispose();
        const before = await page.evaluate(() => {
          const list = document.querySelector("[data-chat-focused]") ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
          if (!list) return null;
          list.scrollTop = Math.max(0, Math.floor((list.scrollHeight - list.clientHeight) / 2));
          const row = list.querySelector("[data-entry-id]");
          return { entryId: row?.dataset.entryId ?? null, top: row?.getBoundingClientRect().top ?? null };
        });
        const floating = page.locator(".chat-scroll-to-bottom").first();
        s.check(await waitUntil(async () => (await floating.getAttribute("aria-label")) === "Scroll to latest", 5_000),
          "scrolled up, the floating button is not the ↓ scroll-to-latest form");
        s.check((await refreshElement(page)) === null, "scrolled up, a ↻ fetch entry point is still offered");
        const after = await page.evaluate((entryId) => {
          const row = entryId ? document.querySelector(`[data-entry-id="${CSS.escape(entryId)}"]`) : null;
          return row ? row.getBoundingClientRect().top : null;
        }, before?.entryId ?? null);
        s.check(before?.top !== null && after !== null && Math.abs(after - before.top) <= 24,
          `the content the reader was on moved (${before?.top} -> ${after})`);
        const scrollAt = Date.now();
        await floating.click();
        s.check(await waitUntil(() => page.evaluate(() => {
          const list = document.querySelector("[data-chat-focused]") ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
          return Boolean(list) && list.scrollTop + list.clientHeight >= list.scrollHeight - 8;
        }), 5_000), "the ↓ form did not scroll to the bottom");
        await delay(400);
        s.check(forceReads(requests, SCROLL, scrollAt) === 0, "the ↓ form started a fetch");
        s.check(await waitUntil(async () => Boolean(await refreshElement(page)), 5_000), "back at the bottom the button did not become ↻");
      }
    } catch (error) {
      s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s.end();
      await created?.context.close().catch(() => {});
    }
  }

  // ---- S21: history paged in beyond the newest-50 window survives ----------
  {
    const s = scenario("S21", "More than the newest-50 window is loaded and stays loaded");
    let created;
    try {
      created = instrument(await newPage());
      const { page } = created;
      await openSession(page, HISTORY, "HISTORY message 159");
      const control = await refreshElement(page);
      if (!control) {
        s.fail("no ↻ refresh form of the chat's floating button");
        await control?.dispose();
      } else {
        await control.dispose();
        const beforeCount = await page.evaluate(() => document.querySelectorAll("[data-entry-id]").length);
        await page.evaluate(() => {
          const list = document.querySelector("[data-chat-focused]") ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
          if (list) list.scrollTop = 0;
        });
        await waitUntil(() => page.evaluate(() => document.querySelectorAll("[data-entry-id]").length > 50), 15_000);
        const loaded = await page.evaluate(() => Array.from(document.querySelectorAll("[data-entry-id]")).map((node) => node.dataset.entryId));
        s.check(loaded.length > 50, `the fixture did not page in more than 50 entries (${beforeCount} -> ${loaded.length})`);
        const oldestLoaded = loaded[0];
        frozen = true;
        await runReply(HISTORY, "REPLY-HISTORY-175");
        // ↻ is the floating button's tail form; back to the bottom first,
        // again on each poll, since entries still settling above can leave the list short of it.
        await waitUntil(async () => {
          await page.evaluate(() => {
            const list = document.querySelector("[data-chat-focused]") ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
            if (list) list.scrollTop = list.scrollHeight;
          });
          return Boolean(await refreshElement(page));
        }, 5_000);
        s.check(await clickRefresh(page), "the refresh control could not be clicked");
        s.check(await waitUntil(() => shows(page, "REPLY-HISTORY-175"), 5_000), "the fetched new reply did not arrive");
        const afterLoaded = await page.evaluate(() => Array.from(document.querySelectorAll("[data-entry-id]")).map((node) => node.dataset.entryId));
        s.check(afterLoaded.length >= loaded.length, `loaded entries collapsed (${loaded.length} -> ${afterLoaded.length})`);
        s.check(oldestLoaded !== undefined && afterLoaded.includes(oldestLoaded), "the oldest already-loaded entry was dropped");
        await page.evaluate(() => {
          const list = document.querySelector("[data-chat-focused]") ?? document.querySelector(".scrollbar-subtle.overflow-y-auto");
          if (list) list.scrollTop = 0;
        });
        await delay(1_500);
        const afterPaging = await page.evaluate(() => document.querySelectorAll("[data-entry-id]").length);
        s.check(afterPaging >= afterLoaded.length, "paging further upward stopped working after the fetch");
      }
    } catch (error) {
      s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s.end();
      await created?.context.close().catch(() => {});
      frozen = false;
    }
  }

  // ---- S22: the stream delivers copies of what the read already rendered ---
  {
    const s = scenario("S22", "The stream delivers what the read already rendered");
    let created;
    try {
      created = instrument(await newPage());
      const { page, requests } = created;
      await openSession(page, HALF, "initial half answer");
      const control = await refreshElement(page);
      if (!control) {
        s.fail("no ↻ refresh form of the chat's floating button");
        await control?.dispose();
      } else {
        await control.dispose();
        frozen = true;
        // A prompt of its own text: the counts below are of the reply's bubble.
        await runReply(HALF, "REPLY-DEDUPE-175", "prompt for the dedupe reply");
        const triggerAt = Date.now();
        s.check(await clickRefresh(page), "the refresh control could not be clicked");
        s.check(await waitUntil(() => shows(page, "REPLY-DEDUPE-175"), 5_000), "the read did not render the fresh reply");
        s.check(forceReads(requests, HALF, triggerAt) === 1, "the scenario did not run through a manual read");
        const once = await page.evaluate(() => (document.body.innerText.match(/REPLY-DEDUPE-175/g) ?? []).length);
        s.check(once === 1, `the read rendered the reply ${once} times`);
        flushFrozenEvents();
        await delay(1_500);
        const afterStream = await page.evaluate(() => (document.body.innerText.match(/REPLY-DEDUPE-175/g) ?? []).length);
        s.check(afterStream === 1, `the stream duplicated an already-read message (${afterStream} bubbles)`);
      }
    } catch (error) {
      s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s.end();
      await created?.context.close().catch(() => {});
      frozen = false;
      buffered.splice(0);
    }
  }

  // ---- S23: two user messages with identical text both survive -------------
  {
    const s = scenario("S23", "Same text sent twice in one run");
    let created;
    try {
      created = instrument(await newPage());
      const { page, requests } = created;
      await openSession(page, SAME, "first answer");
      const control = await refreshElement(page);
      if (!control) {
        s.fail("no ↻ refresh form of the chat's floating button");
        await control?.dispose();
      } else {
        await control.dispose();
        frozen = true;
        await runReply(SAME, "REPLY-SAME-175", "SAME-TEXT-175");
        const triggerAt = Date.now();
        s.check(await clickRefresh(page), "the refresh control could not be clicked");
        s.check(await waitUntil(() => shows(page, "REPLY-SAME-175"), 5_000), "the fetch did not render the new reply");
        const matches = await page.evaluate(() => (document.body.innerText.match(/SAME-TEXT-175/g) ?? []).length);
        s.check(matches >= 2, `only ${matches} of the identical user messages survived the fetch`);
        s.check(forceReads(requests, SAME, triggerAt) === 1,
          "the scenario did not run through a manual read");
      }
    } catch (error) {
      s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s.end();
      await created?.context.close().catch(() => {});
      frozen = false;
    }
  }

  // ---- S24: every new string exists in all three locales -------------------
  {
    const s = scenario("S24", "Every new string exists in all three locales");
    let created;
    try {
      const localeFiles = ["en", "zh-CN", "zh-TW"].map((locale) => ({
        locale,
        text: readFileSync(join(root, `lib/i18n/messages/${locale}.ts`), "utf8"),
      }));
      const required = {
        en: ["Updated to", "Already up to date", "Fetch failed"],
        "zh-CN": ["已更新至", "已是最新", "拉取失败"],
        "zh-TW": ["已更新至", "已是最新", "拉取失敗"],
      };
      for (const file of localeFiles) {
        for (const text of required[file.locale]) {
          s.check(file.text.includes(text), `${file.locale} is missing the new string "${text}"`);
        }
      }
      created = instrument(await newPage({ locale: "en-US" }));
      const { page } = created;
      await page.goto(`${base}/`, { waitUntil: "domcontentloaded" });
      for (const locale of ["en", "zh-CN", "zh-TW"]) {
        await page.evaluate((value) => window.localStorage.setItem("pi-locale", value), locale);
        await openSession(page, MAIN, "initial main answer");
        const label = await refreshLabel(page);
        s.check(Boolean(label), `${locale}: the ↻ control has no label`);
        s.check(Boolean(label) && !/[a-zA-Z]+\.[a-zA-Z]+/.test(label), `${locale}: the control label looks like a raw i18n key (${label})`);
        s.check(await clickRefresh(page), `${locale}: the refresh control could not be clicked`);
        s.check(await waitUntil(async () => {
          const body = await page.evaluate(() => document.body.innerText);
          return locale === "en" ? /Already up to date/i.test(body) : /已是最新/.test(body);
        }, 5_000), `${locale}: the already-current notice was not translated`);
      }
      await page.evaluate(() => window.localStorage.setItem("pi-locale", "zh-CN"));
      await openSession(page, MAIN, "initial main answer");
      frozen = true;
      await runReply(MAIN, "REPLY-LOCALE-175");
      const triggerAt = Date.now();
      s.check(await clickRefresh(page), "zh-CN: the refresh control could not be clicked");
      s.check(await waitUntil(async () => /已更新至\s*\d{2}:\d{2}/.test(await page.evaluate(() => document.body.innerText)), 5_000),
        "zh-CN: the updated notice did not read 已更新至 hh:mm");
      s.check(forceReads(created.requests, MAIN, triggerAt) === 1, "zh-CN: the scenario did not run through a manual read");
    } catch (error) {
      s.fail(`suite error: ${error?.stack ?? String(error)}`);
    } finally {
      s.end();
      await created?.context.close().catch(() => {});
      frozen = false;
    }
  }
} catch (error) {
  failures.push(`suite error: ${error?.stack ?? String(error)}`);
} finally {
  frozen = false;
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
  console.error(`FAIL: ${failures.length} scenario(s) red`);
  assert.fail(`${failures.length} assertion-level failure(s)`);
}
console.log("PASS: manual fetch acceptance scenarios");