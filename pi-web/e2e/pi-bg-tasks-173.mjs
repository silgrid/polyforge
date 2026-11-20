// carries: components/PiRunningList*, components/PiBackgroundTaskPanel*, hooks/useBackgroundTasks*, hooks/useSubagentRuns*, lib/bg-runtime.ts, lib/bg-protocol.ts, lib/background-tasks-client.ts, lib/subagent-runs-view.ts, lib/subagent-runtime.ts, lib/rpc-manager.ts
// (the agent routes themselves stay under the three existing app/api/agent/* suites' cap;
//  components/ChatWindow* and components/MessageView* are each declared by three
//  suites already, and locale-table globs stay out of carries as #173 ruled)
// polyforge#173 (AC1/AC3/AC4) + polyforge#189 (AC1/AC2) — a pi session's
// built-in background tasks in the browser. A local OpenAI-compatible fake
// model answers the session's first request with a `bg_run` tool call, so the
// built-in executor starts a real task and every later state is the product's
// own: the SSE events, the bg-tasks HTTP interface, the running list above
// the composer and the task panel it opens.
//
// One server runs the five scenarios in order, each in its own session:
//   R173a  a started task appears in the running list's grid (command, Running,
//          a ticking elapsed time), on desktop and at 375px; the list sits
//          above the composer in the chat column.
//   R173b  a short task ends: the session wakes (the completion notification
//          shows as its own message and the model answers it), and without any
//          page reload the task's button leaves the running list within
//          seconds of its end, the whole running area collapses (the node
//          itself gone, no settled entry) and the settled task's detail opens
//          from the bg_run tool card with its final status and output tail
//          (#189 AC1/AC2: settled tasks never occupy the running area).
//   R173c  the detail panel's Stop stops the task: it leaves the list and the
//          open panel shows it stopped.
//   R173d  a task still running through a server restart reads as interrupted
//          afterwards: never in the running list, the running area gone, its
//          detail opened through the card button, and the settled history
//          reachable through the detail panel's history button (#189).
//   R173e  two tasks in one turn: only the ended one's button leaves the list
//          while the other still runs, the list collapses when the last one
//          ends, and a detail panel open across the task's own end survives
//          showing its final status (#189).
// Every wait polls the condition it asserts, with a bounded deadline. The
// suite ends with `PASS pi-bg-tasks-173` only when every scenario passed and
// the browser reported no errors.
import assert from "node:assert/strict";
import { createServer as createHttpServer } from "node:http";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitUntil, errorText, launchBrowser, root } from "./harness.mjs";

const SUITE = "pi-bg-tasks-173";
assertNoDevServer();
const distDir = process.env.PI_WEB_DIST_DIR || `.next/e2e/${SUITE}`;
const shots = join(root, "test-results/e2e", SUITE);
mkdirSync(shots, { recursive: true });
// A leftover dev lock from a killed run would fail the server start; the dist
// dir itself is kept, so a re-run of this suite (and the verification gate's)
// starts warm.
rmSync(join(root, distDir, "dev/lock"), { force: true });
const scratch = mkdtempSync(join(tmpdir(), `${SUITE}-`));
const home = join(scratch, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(scratch, "project");
mkdirSync(project, { recursive: true });
mkdirSync(agentDir, { recursive: true });

// ---- fake model ------------------------------------------------------------
// `scripted` holds one reply per model request, in order (the session's own
// requests are strictly ordered; a stray completion wake beyond the script
// gets the plain OK answer). A `toolCall` reply makes the session run one
// bg_run; a `toolCalls` reply starts several in the same turn.
const OK = { kind: "text", text: "OK-ANSWER" };
let scripted = [];
const model = createHttpServer(async (request, response) => {
  await new Promise((resolve) => {
    request.on("data", () => { /* drain the prompt body */ });
    request.on("end", resolve);
    request.on("error", resolve);
  });
  const reply = scripted.length > 0 ? scripted.shift() : OK;
  response.writeHead(200, { "Content-Type": "text/event-stream" });
  const chunk = (delta, finish = null, extra = {}) => `data: ${JSON.stringify({
    id: "fake", object: "chat.completion.chunk", created: 0, model: "fake-model",
    choices: [{ index: 0, delta, finish_reason: finish }], ...extra,
  })}\n\n`;
  response.write(chunk({ role: "assistant", content: "" }));
  if (reply.kind === "toolCall" || reply.kind === "toolCalls") {
    const commands = reply.kind === "toolCall" ? [reply.command] : reply.commands;
    commands.forEach((command, index) => {
      response.write(chunk({
        tool_calls: [{
          index,
          id: `call_${index + 1}`,
          type: "function",
          function: { name: "bg_run", arguments: JSON.stringify({ command }) },
        }],
      }));
    });
    response.write(chunk({}, "tool_calls"));
  } else {
    response.write(chunk({ content: reply.text }));
    response.write(chunk({}, "stop", { usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 } }));
  }
  response.write("data: [DONE]\n\n");
  response.end();
});
model.listen(0, "127.0.0.1");
await new Promise((resolve) => model.once("listening", resolve));
writeFileSync(join(agentDir, "models.json"), JSON.stringify({
  providers: { fake: { baseUrl: `http://127.0.0.1:${model.address().port}/v1`, api: "openai-completions", apiKey: "fake-key", models: [{ id: "fake-model", name: "Fake Model" }] } },
}));

const TITLES = {
  R173a: "A started background task appears in the running list above the composer, on desktop and at 375px",
  R173b: "A finished task wakes the session, its button leaves the collapsed running list within seconds without a reload, and its detail opens from the bg_run card",
  R173c: "The detail panel's Stop stops the task: it leaves the list, the panel shows it stopped",
  R173d: "A task still running through a server restart reads as interrupted, never in the running list, its detail and the history reachable",
  R173e: "Only an ended task's button leaves the list while another runs; the list collapses at the last end and an open detail survives it",
};

const h = createHarness();
const failures = [];
const scenario = createCheck({ failures, message: errorText, record: ([id], text) => `${id}: ${text}`, fail: ([id], text) => console.log(`FAIL ${id} (${TITLES[id]}): ${text}`) });
const waitFor = createWaitUntil({ every: 200, withLast: true });

const serverEnv = () => ({
  HOME: home,
  PI_CODING_AGENT_DIR: agentDir,
  PI_WEB_ALLOWED_ROOT_PREFIXES: scratch,
  PI_WEB_DIST_DIR: distDir,
  PI_WEB_IDLE_TIMEOUT_MS: "3600000",
  PF_BIN: join(scratch, "no-such-pf"),
});

const STORAGE = { "pi-web:split-view-enabled": "false", "pi-web:session-age-filter-days": "0" };
const composer = (page) => page.locator(".chat-input-textarea").first();
const list = (page) => page.locator("[data-pi-running-list]");
const taskItem = (page, taskId) => list(page).locator(`[data-pi-task-item="${taskId}"]`);
// The settled entry the running list used to carry (#189 removes it); kept as
// a locator so the scenarios can assert it never renders.
const historyEntry = (page) => list(page).locator("[data-pi-bg-history]");
// The bg_run tool card's open-task button (#189) and the detail panel's
// history button (the settled history's entry).
const cardButton = (page, taskId) => page.locator(`[data-pi-open-task="${taskId}"]`);
const historyOpenButton = (page) => page.locator("[data-pi-bg-history-open]");
const taskPanel = (page, taskId) => page.locator(`[data-pi-task-panel="${taskId}"]`);
const historyPanel = (page) => page.locator("[data-pi-bg-history-panel]");

async function api(pathname, init = {}) {
  return fetch(`${h.base}${pathname}`, {
    ...init,
    headers: { ...(init.body ? { "Content-Type": "application/json" } : {}), ...(init.headers ?? {}) },
    signal: AbortSignal.timeout(init.timeout ?? 120_000),
  });
}
async function getJson(pathname) {
  const response = await api(pathname);
  assert.equal(response.status, 200, `GET ${pathname} -> ${response.status}`);
  return response.json();
}
async function postJson(pathname, body) {
  const response = await api(pathname, { method: "POST", body: JSON.stringify(body) });
  return { status: response.status, body: await response.json().catch(() => ({})) };
}

/** Creates a pi session whose first model request starts the given bg tasks. */
async function newSessionWithTasks(commands, followUps = []) {
  scripted = [{ kind: "toolCalls", commands }, ...followUps];
  const created = await postJson("/api/agent/new", { cwd: project, type: "prompt", message: `start background tasks: ${commands.join(" and ")}`, provider: "fake", modelId: "fake-model" });
  assert.equal(created.status, 200, `POST /api/agent/new -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const id = created.body.sessionId ?? created.body.data?.sessionId;
  assert.ok(typeof id === "string" && id.length > 0, "the create route returned no session id");
  const tasks = await waitFor(async () => {
    const body = await getJson(`/api/agent/${encodeURIComponent(id)}/bg-tasks`);
    const running = (body.tasks ?? []).filter((task) => task.status === "running");
    return running.length === commands.length ? running : null;
  }, 45_000, `the bg tasks never appeared in ${id}'s own list`);
  return { sessionId: id, tasks };
}

/** The one-task shape the first scenarios use. */
async function newSessionWithTask(command, followUps = []) {
  const { sessionId, tasks } = await newSessionWithTasks([command], followUps);
  return { sessionId, taskId: tasks[0].id };
}

async function openSession(sessionId, viewport) {
  const page = await h.openPage({
    session: sessionId,
    ...(viewport ? { viewport } : {}),
    storage: STORAGE,
    ready: async (page, _o, timeout) => {
      await composer(page).waitFor({ state: "visible", timeout });
    },
  });
  opened.push(page);
  return page;
}

const shot = (name) => async (page) => { await page.screenshot({ path: join(shots, `${name}.png`), fullPage: false }); };

/** Every page this suite opened; closed after each scenario, so a stopped server's dead EventSources log no browser errors. */
const opened = [];
const closeOpened = async () => {
  while (opened.length > 0) {
    const page = opened.pop();
    try { await page.context().close(); } catch { /* already closed */ }
  }
};

const interrupt = () => { process.exitCode = 1; void h.close().catch(() => {}); };
process.once("SIGINT", interrupt);
process.once("SIGTERM", interrupt);

try {
  ({ base: h.base } = await h.startServer({ name: SUITE, logFile: join(scratch, "server.log"), env: serverEnv() }));
  h.browser = await launchBrowser(() => chromium.launch());

  // R173a — the running list shows the task, desktop and narrow.
  await scenario("R173a", async () => {
    const { sessionId, taskId } = await newSessionWithTask("sleep 20");
    const page = await openSession(sessionId);
    const item = taskItem(page, taskId);
    await waitFor(async () => (await item.count()) === 1, 25_000, "the running task never appeared in the running list");
    const text = await item.innerText();
    assert.match(text, /sleep 20/, `the task button names its command: ${JSON.stringify(text)}`);
    assert.match(text, /Running/, `the task button shows it running: ${JSON.stringify(text)}`);
    // The list sits in the composer's column, above the input box.
    const itemBox = await item.boundingBox();
    const composerBox = await composer(page).boundingBox();
    assert.ok(itemBox && composerBox, "the task item and the composer are both laid out");
    assert.ok(itemBox.y + itemBox.height <= composerBox.y + 2, `the running list sits above the composer (${JSON.stringify(itemBox)} vs ${JSON.stringify(composerBox)})`);
    const first = await item.innerText();
    await waitFor(async () => (await item.innerText()) !== first, 15_000, "the elapsed time never ticked");
    await shot("R173a-desktop")(page);
    // A narrow viewport keeps the same grid and the same facts.
    const narrow = await openSession(sessionId, { width: 375, height: 667 });
    const narrowItem = taskItem(narrow, taskId);
    await waitFor(async () => (await narrowItem.count()) === 1, 25_000, "the running task never appeared in the narrow viewport");
    assert.match(await narrowItem.innerText(), /Running/);
    const narrowBox = await narrowItem.boundingBox();
    assert.ok(narrowBox, "the narrow layout lays the item out");
    assert.ok(narrowBox.width <= 375, `the item stays inside the narrow viewport: ${JSON.stringify(narrowBox)}`);
    await shot("R173a-narrow")(narrow);
    // The scenario's task ends here: stop it through the interface (idempotent).
    const stopped = await postJson(`/api/agent/${encodeURIComponent(sessionId)}/bg-tasks/${encodeURIComponent(taskId)}/kill`, {});
    assert.equal(stopped.status, 200, `kill -> ${stopped.status} ${JSON.stringify(stopped.body).slice(0, 200)}`);
    await closeOpened();
  });

  // R173b — the end wakes the session, the button leaves the collapsed list
  // without a reload within seconds, and the card button opens the detail.
  await scenario("R173b", async () => {
    // Three scripted replies: the turn's own answer after the bg_run result
    // (TURN-ONE), then the wake's answer (WAKE-ANSWER). A same-turn second
    // request consumes TURN-ONE, so WAKE-ANSWER can only appear when the
    // completion notification actually triggered a turn (AC4's 唤醒).
    const { sessionId, taskId } = await newSessionWithTask("echo BG-E2E-DONE; sleep 5", [{ kind: "text", text: "TURN-ONE" }, { kind: "text", text: "WAKE-ANSWER" }]);
    const page = await openSession(sessionId);
    // A page marker: still set at the end proves no reload happened across the
    // task's end (AC2's 不刷新页面).
    await page.evaluate(() => { window.__pf189NoReload = "kept"; });
    // The browser sees the task running first: the item and its card's button.
    await waitFor(async () => (await taskItem(page, taskId).count()) === 1, 25_000, "the running task never appeared in the running list");
    const card = cardButton(page, taskId);
    await waitFor(async () => (await card.count()) >= 1, 25_000, "the bg_run tool card's open-task button never showed");
    // The completion notification shows as its own message and the model answers it.
    await waitFor(async () => (await page.locator("body").innerText()).includes("Background task"), 25_000, "the completion notification never showed in the message list");
    await waitFor(async () => (await page.locator("body").innerText()).includes("TURN-ONE"), 25_000, "the turn's own answer never showed");
    await waitFor(async () => (await page.locator("body").innerText()).includes("WAKE-ANSWER"), 25_000, "the session was never woken (no answer to the notification)");
    // The wake's answer renders after the notification that triggered it
    // (DOM order): a broken triggerTurn would leave WAKE-ANSWER unqueued.
    const settledText = await page.locator("body").innerText();
    assert.ok(
      settledText.indexOf("WAKE-ANSWER") > settledText.indexOf("Background task"),
      `the wake's answer does not follow the completion notification: notification at ${settledText.indexOf("Background task")}, answer at ${settledText.indexOf("WAKE-ANSWER")}`,
    );
    // The task settles on the server; from that moment its button must leave
    // the running list within seconds — no reload, no settled entry (#189 AC2).
    await waitFor(async () => {
      const body = await getJson(`/api/agent/${encodeURIComponent(sessionId)}/bg-tasks`);
      const record = (body.tasks ?? []).find((task) => task.id === taskId);
      return record && record.status !== "running" ? record.status : null;
    }, 45_000, "the task never settled");
    await waitFor(async () => (await taskItem(page, taskId).count()) === 0, 5_000, "the settled task's button did not leave the running list within 5 s of its end (no reload)");
    // The whole running area collapses: the node itself is gone.
    await waitFor(async () => (await list(page).count()) === 0, 5_000, "the running area did not collapse after the last task ended");
    assert.equal(await historyEntry(page).count(), 0, "a settled entry still renders in the running area");
    assert.equal(await page.evaluate(() => window.__pf189NoReload), "kept", "the page reloaded across the task's end");
    // The settled task's detail opens from the bg_run tool card (#189 AC1).
    await card.first().click();
    const detail = taskPanel(page, taskId);
    await waitFor(async () => (await detail.count()) === 1, 25_000, "the task's detail never opened from the card button");
    await waitFor(async () => (await detail.innerText()).includes("BG-E2E-DONE"), 20_000, "the detail never showed the task's output tail");
    assert.match(await detail.innerText(), /Completed/, `the detail shows the final status: ${JSON.stringify(await detail.innerText())}`);
    await shot("R173b-card")(page);
    await closeOpened();
  });

  // R173c — the panel's Stop stops the running task.
  await scenario("R173c", async () => {
    const { sessionId, taskId } = await newSessionWithTask("sleep 30");
    const page = await openSession(sessionId);
    const item = taskItem(page, taskId);
    await waitFor(async () => (await item.count()) === 1, 25_000, "the running task never appeared in the running list");
    await item.click();
    const detail = taskPanel(page, taskId);
    await waitFor(async () => (await detail.count()) === 1, 25_000, "the task panel never opened");
    const stop = detail.getByRole("button", { name: "Stop task", exact: true });
    await stop.click();
    await waitFor(async () => (await taskItem(page, taskId).count()) === 0, 25_000, "the stopped task never left the running list");
    await waitFor(async () => (await detail.innerText()).includes("Stopped"), 20_000, "the open panel never showed the task stopped");
    await closeOpened();
  });

  // R173d — a task still running through a restart reads as interrupted.
  await scenario("R173d", async () => {
    const { sessionId, taskId } = await newSessionWithTask("sleep 30");
    const page = await openSession(sessionId);
    await waitFor(async () => (await taskItem(page, taskId).count()) === 1, 25_000, "the running task never appeared in the running list");
    // The server dies with the task running; a fresh server opens the same
    // registry and recovers the record as interrupted (#172's semantics).
    await closeOpened();
    await h.stop();
    ({ base: h.base } = await h.startServer({ name: `${SUITE}-restart`, logFile: join(scratch, "server-restart.log"), env: serverEnv() }));
    const reloaded = await openSession(sessionId);
    const record = await waitFor(async () => {
      const body = await getJson(`/api/agent/${encodeURIComponent(sessionId)}/bg-tasks`);
      const found = (body.tasks ?? []).find((task) => task.id === taskId);
      return found && found.status === "interrupted" ? found : null;
    }, 45_000, "the running task never read as interrupted after the restart");
    // The running area is gone entirely: no item, no node, no settled entry.
    await waitFor(async () => (await taskItem(reloaded, taskId).count()) === 0, 25_000, "the interrupted task still shows in the running list after the restart");
    await waitFor(async () => (await list(reloaded).count()) === 0, 25_000, "the running area still renders after the restart");
    assert.equal(await historyEntry(reloaded).count(), 0, "a settled entry renders in the running area after the restart");
    // The card button opens the interrupted task's detail.
    const card = cardButton(reloaded, taskId);
    await waitFor(async () => (await card.count()) === 1, 25_000, "the bg_run card's open-task button never showed after the restart");
    await card.click();
    const detail = taskPanel(reloaded, taskId);
    await waitFor(async () => (await detail.count()) === 1, 25_000, "the interrupted task's detail never opened");
    assert.match(await detail.innerText(), /Interrupted/, `the detail shows interrupted: ${JSON.stringify(await detail.innerText())}`);
    await shot("R173d-interrupted")(reloaded);
    // The settled history stays reachable through the detail's history button
    // (#189: the history's entry is the detail panel's, never the running area's).
    await historyOpenButton(reloaded).click();
    const panel = historyPanel(reloaded);
    await waitFor(async () => (await panel.count()) === 1, 25_000, "the history panel never opened from the detail's history button");
    const row = panel.locator(`[data-pi-history-task="${taskId}"]`);
    await waitFor(async () => (await row.count()) === 1, 25_000, "the interrupted task never appeared in the history panel");
    assert.match(await row.innerText(), /Interrupted/, `the history row shows interrupted: ${JSON.stringify(await row.innerText())}`);
    await closeOpened();
  });

  // R173e — only an ended task's button leaves the list while another runs;
  // the list collapses at the last end and an open detail survives it.
  await scenario("R173e", async () => {
    const SHORT = "echo BG-E2E-MIX; sleep 8";
    const LONG = "sleep 16";
    const { sessionId, tasks } = await newSessionWithTasks([SHORT, LONG], [{ kind: "text", text: "TURN-ONE" }]);
    const byCommand = (command) => tasks.find((task) => task.command === command);
    const shortId = byCommand(SHORT).id;
    const longId = byCommand(LONG).id;
    const page = await openSession(sessionId);
    await waitFor(async () => (await taskItem(page, shortId).count()) === 1 && (await taskItem(page, longId).count()) === 1, 25_000, "the two running tasks never both appeared in the running list");
    // Open the long task's detail from its card while it still runs.
    await cardButton(page, longId).first().click();
    const detail = taskPanel(page, longId);
    await waitFor(async () => (await detail.count()) === 1, 25_000, "the long task's detail never opened from the card button");
    // The short task ends: only its button leaves, the long one and the list stay.
    await waitFor(async () => (await taskItem(page, shortId).count()) === 0, 25_000, "the short task's button never left the running list");
    assert.equal(await taskItem(page, longId).count(), 1, "the long task's button left the list while it still runs");
    assert.equal(await list(page).count(), 1, "the running area collapsed while a task still runs");
    assert.equal(await historyEntry(page).count(), 0, "a settled entry rendered beside the running item");
    // The long task ends: the list collapses, the open detail survives with its final status.
    await waitFor(async () => (await taskItem(page, longId).count()) === 0, 30_000, "the long task's button never left the running list");
    await waitFor(async () => (await list(page).count()) === 0, 5_000, "the running area did not collapse after the last task ended");
    await waitFor(async () => (await detail.innerText()).includes("Completed"), 20_000, "the open detail never showed the task completed");
    await shot("R173e-mixed")(page);
    await closeOpened();
  });

  assert.equal(h.errors.length, 0, `browser errors: ${JSON.stringify(h.errors.slice(0, 5))}`);
} catch (error) {
  process.exitCode = 1;
  console.log(errorText(error));
} finally {
  await closeOpened().catch(() => {});
  await h.close().catch(() => {});
  rmSync(join(root, distDir, "dev/lock"), { force: true });
  if (failures.length > 0) {
    console.log(`${failures.length} scenario(s) failed`);
  } else {
    // The suite's own marker: only a run with no failures and no browser
    // errors reaches it.
    console.log(`PASS ${SUITE}`);
  }
  // The fake model's listening socket holds the process open; the suite ends here.
  process.off("SIGINT", interrupt);
  process.off("SIGTERM", interrupt);
  model.closeAllConnections?.();
  await new Promise((resolve) => model.close(resolve));
  process.exit(failures.length > 0 || process.exitCode ? 1 : 0);
}
