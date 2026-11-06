// carries: lib/pi-types.ts, lib/session-stats-format.ts, lib/__fixtures__/claude-2.1.312/compact/*
// — a claude session's context meter in the header: e2e acceptance pass.
//
// The header's context meter (the "Session info" button's context part and its
// title) shows a claude session's context as it shows a pi session's: claude's own
// count of the latest reply's input, cache write, cache read and output over the
// model's window, `<percent>% / <window>`; a count claude has not reported is shown
// as `? / <window>`, never as a number.
//
// The child is the PATH-injected e2e stub (e2e/claude-stub/claude.mjs) in its
// `compact` fixture mode, which replays the real claude 2.1.312 `compact/`
// recording (lib/__fixtures__/claude-2.1.312/compact/): its first reply reports
// 2 + 16682 + 0 + 4 = 16688 tokens, its second 2 + 3283 + 13449 + 5 = 16739, the
// figure its compaction reports as `pre_tokens`, and the compaction leaves
// `post_tokens` 2978. The resume scenario appends one synthetic reply to the
// transcript while no child runs, so the count the resumed child shows can only
// come from the transcript. No real model is called.
//
// Every scenario is individually guarded and prints one `PASS S363-nn` marker.
import assert from "node:assert/strict";
import { appendFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, claudeEnv, claudeScratch, createApi, createCheck, createHarness, createWaitUntil, errorText, findTranscript, pidAlive, readJsonLines, root, same, seedStorage, setupFailed } from "./harness.mjs";

assertNoDevServer();
const kit = claudeScratch("pi-web-claude-363-");
const { artifacts, scratch, project, stubLog, configDir } = kit;

const CONTEXT_WINDOW = 1_000_000;
// The compact/ recording's own counts (lib/__fixtures__/claude-2.1.312/README.md).
const FIRST_REPLY = 16688;
const SECOND_REPLY = 16739;
const POST_COMPACTION = 2978;
// The synthetic reply the resume scenario appends: 1200 + 3500 + 118250 + 750.
const TRANSCRIPT_ONLY = 123_700;
const usageFor = (tokens) => ({ tokens, percent: (tokens / CONTEXT_WINDOW) * 100, contextWindow: CONTEXT_WINDOW });
const UNKNOWN = { tokens: null, percent: null, contextWindow: CONTEXT_WINDOW };

const h = createHarness();
let browser;
let desktopContext;
let mobileContext;
let page;
let base;
let firstLoad = true;
const failures = [];

const scenario = createCheck({ failures, message: errorText, record: (names, text) => `${names.join(" ")}: ${text}` });

const stubRecords = () => readJsonLines(stubLog);
const launchesFor = (id) => stubRecords().filter((entry) => entry.kind === "launch" && entry.sessionId === id);
const userLinesOf = (pid) => stubRecords()
  .filter((entry) => entry.kind === "stdin" && entry.pid === pid)
  .flatMap((entry) => {
    try {
      return [JSON.parse(entry.line)];
    } catch {
      return [];
    }
  })
  .filter((event) => event?.type === "user");

const waitFor = createWaitUntil();

const { getJson, postJson } = createApi(() => base);

async function createClaudeSession(body) {
  const created = await postJson("/api/agent/new", { cwd: project, harness: "claude", model: "claude-haiku-5-5", ...body });
  assert.equal(created.status, 200, `POST /api/agent/new -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const id = created.body.sessionId;
  assert.ok(typeof id === "string" && id.length > 0, "the create route returned no claude session id");
  return id;
}

/** The live state's contextUsage once the child is idle; undefined while it runs or when none is alive. */
async function idleContextUsage(id) {
  const reply = await getJson(`/api/agent/${encodeURIComponent(id)}`);
  return reply.running === true && reply.state?.isStreaming === false ? reply.state.contextUsage : undefined;
}
async function waitForContextUsage(id, expected, what) {
  let seen;
  await waitFor(async () => {
    seen = await idleContextUsage(id);
    return same(seen, expected);
  }, 60_000, () => `${what}: GET /api/agent/<id> contextUsage is ${JSON.stringify(seen)}, expected ${JSON.stringify(expected)}`, 250);
}

const transcriptOf = (id) => findTranscript(configDir, id);

async function killChild(id) {
  const pids = launchesFor(id).map((entry) => entry.pid).filter(pidAlive);
  assert.ok(pids.length > 0, "no live claude child was found to end");
  for (const pid of pids) {
    try {
      process.kill(pid, "SIGKILL");
    } catch {
      // already gone
    }
  }
  await waitFor(async () => (await getJson(`/api/agent/${encodeURIComponent(id)}`)).running === false, 60_000, "the claude child did not exit", 400);
}

const textarea = () => page.locator(".chat-input-textarea").first();
async function openSession(id) {
  await page.goto(`${base}/?session=${encodeURIComponent(id)}`, { waitUntil: "domcontentloaded", timeout: firstLoad ? COLD_MS : NAV_MS });
  firstLoad = false;
  await textarea().waitFor({ state: "visible", timeout: NAV_MS });
}
async function sendPrompt(text) {
  await textarea().fill(text);
  await page.getByRole("button", { name: "Send", exact: true }).first().click({ timeout: ACTION_MS });
}

/** The desktop header's stats button: its visible text and its title. */
const desktopMeter = () => page.locator('button[aria-label="Session info"]:not([data-mobile-toolbar-stats])').first();
/** A phone's stats button, the toolbar's current-session element. */
const phoneMeter = (phone) => phone.locator('button[data-mobile-toolbar-stats="true"]').first();
async function readMeter(locator) {
  return { text: (await locator.innerText()).replace(/\s+/g, " ").trim(), title: (await locator.getAttribute("title")) ?? "" };
}
/** Waits until the desktop meter shows `text` and its title the `Context: …` part. */
async function waitForMeter(text, titlePart, what) {
  let seen = { text: "", title: "" };
  await waitFor(async () => {
    seen = await readMeter(desktopMeter());
    return seen.text.includes(text) && seen.title.includes(titlePart);
  }, NAV_MS, () => `${what}: the header meter reads ${JSON.stringify(seen)}, expected ${JSON.stringify(text)} and a title with ${JSON.stringify(titlePart)}`, 250);
  return seen;
}

h.trapInterrupt();

try {
  ({ base } = await h.startServer({ name: "claude-context-363", logFile: join(artifacts, "claude-context-363-server.log"), env: claudeEnv(kit, {
      // The product default: only an explicit kill ends a child mid-scenario.
      PI_WEB_IDLE_TIMEOUT_MS: "600000",
      // Every child replays the compact/ recording: reply, reply, then /compact.
      PI_E2E_CLAUDE_STUB_MODE: "compact",
      PI_E2E_CLAUDE_COMPACT_FIXTURE: join(root, "lib/__fixtures__/claude-2.1.312/compact"),
  }) }));

  const { chromium } = await import("playwright");
  browser = await h.launchBrowser(() => chromium.launch());
  desktopContext = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US", baseURL: base });
  mobileContext = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, locale: "en-US", baseURL: base });
  for (const context of [desktopContext, mobileContext]) {
    await seedStorage(context);
  }
  page = await desktopContext.newPage();
  page.setDefaultTimeout(ACTION_MS);
  await desktopContext.tracing.start({ screenshots: true, snapshots: true });

  let replied;

  await scenario("S363-01", "A claude reply fills the header's context meter with claude's count over the model's window", async () => {
    replied = await createClaudeSession({ type: "prompt", message: "E2E 363 first" });
    await waitForContextUsage(replied, usageFor(FIRST_REPLY), "after the first reply");
    await openSession(replied);
    await page.getByText("E2E 363 first", { exact: true }).first().waitFor({ timeout: NAV_MS });
    const meter = await waitForMeter("2% / 1.0M", "Context: 1.7% / 1,000,000", "after the first reply");
    assert.ok(!/NaN|\?/.test(meter.text), `the meter shows an unknown or broken value: ${meter.text}`);
    await page.screenshot({ path: join(artifacts, "claude-363-meter.png") });

    const phone = await mobileContext.newPage();
    try {
      await phone.goto(`${base}/?session=${encodeURIComponent(replied)}`, { waitUntil: "domcontentloaded", timeout: NAV_MS });
      const stats = phoneMeter(phone);
      await waitFor(async () => (await stats.innerText()).includes("2%"), NAV_MS, "the phone's stats button shows no context percent", 250);
    } finally {
      await phone.close();
    }
  });

  await scenario("S363-02", "The meter follows the next reply, and its count is the one claude compacts from", async () => {
    assert.ok(replied, "prerequisite: S363-01 created no session");
    await sendPrompt("E2E 363 second");
    await waitForContextUsage(replied, usageFor(SECOND_REPLY), "after the second reply");
    await waitForMeter("2% / 1.0M", "Context: 1.7% / 1,000,000", "after the second reply");
    const compacted = await postJson(`/api/agent/${encodeURIComponent(replied)}`, { type: "compact" });
    assert.equal(compacted.status, 200, `compact -> ${compacted.status} ${JSON.stringify(compacted.body).slice(0, 300)}`);
    assert.equal(compacted.body.data?.tokensBefore, SECOND_REPLY, "claude's own pre-compaction count is not the meter's count");
  });

  await scenario("S363-03", "After a compaction the meter shows the size claude reported it left", async () => {
    assert.ok(replied, "prerequisite: S363-01 created no session");
    await waitForContextUsage(replied, usageFor(POST_COMPACTION), "after the compaction");
    // The page reads the usage again on a reply, a turn's end or a stream (re)connect (as for pi).
    await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
    await textarea().waitFor({ state: "visible", timeout: NAV_MS });
    await waitForMeter("0% / 1.0M", "Context: 0.3% / 1,000,000", "after the compaction");
  });

  await scenario("S363-04", "A resumed child shows the transcript's count before its first reply", async () => {
    assert.ok(replied, "prerequisite: S363-01 created no session");
    // No open page: its stream would start the --resume child before the transcript changes.
    await page.goto("about:blank");
    await killChild(replied);
    assert.deepEqual(await getJson(`/api/sessions/${encodeURIComponent(replied)}/state`), { running: false }, "with no live child the state carries no usage");
    const transcript = transcriptOf(replied);
    assert.ok(transcript, "the stub wrote no transcript for the session");
    appendFileSync(transcript, `${JSON.stringify({
      type: "assistant",
      uuid: "36300000-0000-4000-8000-000000000001",
      parentUuid: null,
      sessionId: replied,
      isSidechain: false,
      timestamp: new Date().toISOString(),
      message: {
        id: "msg_e2e_363_transcript",
        type: "message",
        role: "assistant",
        model: "claude-haiku-5-5",
        content: [{ type: "text", text: "E2E 363 transcript-only reply" }],
        stop_reason: "end_turn",
        usage: { input_tokens: 1200, cache_creation_input_tokens: 3500, cache_read_input_tokens: 118250, output_tokens: 750 },
      },
    })}\n`);
    const launchesBefore = launchesFor(replied).length;
    await openSession(replied);
    const resumed = await waitFor(() => launchesFor(replied).slice(launchesBefore).find((launch) => launch.resumed) ?? false, 60_000, "opening the session started no --resume child");
    await waitForMeter("12% / 1.0M", "Context: 12.4% / 1,000,000", "after the resume");
    assert.deepEqual(await idleContextUsage(replied), usageFor(TRANSCRIPT_ONLY));
    assert.deepEqual(userLinesOf(resumed.pid), [], "the resumed child was sent a prompt: the count is not the transcript's alone");
  });

  await scenario("S363-05", "A child that has reported nothing shows an unknown count, never a number", async () => {
    const fresh = await createClaudeSession({ type: "ensure_session" });
    await waitForContextUsage(fresh, UNKNOWN, "before any reply");
    await openSession(fresh);
    const meter = await waitForMeter("? / 1.0M", "Context: ? / 1,000,000", "before any reply");
    assert.ok(!/\d%|NaN/.test(meter.text), `an unknown count is shown as a number: ${meter.text}`);
    assert.ok(!/NaN|\d%/.test(meter.title.slice(meter.title.indexOf("Context:"))), `an unknown count's title carries a number: ${meter.title}`);
    await page.screenshot({ path: join(artifacts, "claude-363-unknown.png") });

    const phone = await mobileContext.newPage();
    try {
      // The phone's own state read: it carries the unknown usage, and the stats button renders from it.
      const stateRead = phone.waitForResponse(async (response) => {
        const { pathname } = new URL(response.url());
        if (pathname !== `/api/sessions/${encodeURIComponent(fresh)}/state` && pathname !== `/api/agent/${encodeURIComponent(fresh)}`) return false;
        const body = await response.json().catch(() => null);
        return same(body?.state?.contextUsage, UNKNOWN);
      }, { timeout: NAV_MS });
      await phone.goto(`${base}/?session=${encodeURIComponent(fresh)}`, { waitUntil: "domcontentloaded", timeout: NAV_MS });
      await stateRead;
      const stats = phoneMeter(phone);
      await stats.waitFor({ state: "visible", timeout: NAV_MS });
      // Absent for the whole window after the read, not only at one instant.
      const settleUntil = Date.now() + 1_500;
      while (Date.now() < settleUntil) {
        const text = (await stats.innerText()).replace(/\s+/g, " ");
        assert.ok(!/%|NaN|\?/.test(text), `the phone shows a context figure for an unknown count: ${text}`);
        await delay(100);
      }
    } finally {
      await phone.close();
    }
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  if (page) await page.screenshot({ path: join(artifacts, "claude-363-final.png") }).catch(() => {});
  if (desktopContext) await desktopContext.tracing.stop({ path: join(artifacts, "claude-363-trace.zip") }).catch(() => {});
  await h.close();
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL claude-context-363: ${failures.length} scenario(s) failed`);
  for (const failure of failures) console.log(`  ${failure}`);
  process.exitCode = 1;
}
