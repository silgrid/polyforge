// carries: components/PiRunningList*, components/PiBackgroundTaskPanel*, components/ChatWindow.tsx, hooks/useBackgroundTasks*, hooks/useSubagentRuns*, app/api/agent/[id]/bg-tasks/*, app/api/agent/[id]/subagent-runs/*, lib/bg-runtime.ts, lib/bg-protocol.ts, lib/subagent-runtime.ts, lib/rpc-manager.ts, lib/i18n/messages/*
// polyforge#173 (AC1/AC3/AC4) — a pi session's built-in background tasks in the
// browser. A local OpenAI-compatible fake model answers the session's first
// request with a `bg_run` tool call, so the built-in executor starts a real
// task and every later state is the product's own: the SSE events, the
// bg-tasks HTTP interface, the running list above the composer and the task
// panel it opens.
//
// One server runs the four scenarios in order, each in its own session:
//   R173a  a started task appears in the running list's grid (command, Running,
//          a ticking elapsed time), on desktop and at 375px; the list sits
//          above the composer in the chat column.
//   R173b  a short task ends: the session wakes (the completion notification
//          shows as its own message and the model answers it), the list
//          collapses to no task items, the 「已结束」 entry opens the task's
//          detail with its final status and output tail.
//   R173c  the detail panel's Stop stops the task: it leaves the list and the
//          open panel shows it stopped.
//   R173d  a task still running through a server restart reads as interrupted
//          afterwards: never in the running list, visible through the 「已结束」
//          entry's detail.
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
const scratch = mkdtempSync(join(tmpdir(), `${SUITE}-`));
const home = join(scratch, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(scratch, "project");
mkdirSync(project, { recursive: true });
mkdirSync(agentDir, { recursive: true });

// ---- fake model ------------------------------------------------------------
// `scripted` holds one reply per model request, in order (the session's own
// requests are strictly ordered; a stray completion wake beyond the script
// gets the plain OK answer). A `toolCall` reply makes the session run bg_run.
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
  if (reply.kind === "toolCall") {
    response.write(chunk({
      tool_calls: [{
        index: 0,
        id: "call_1",
        type: "function",
        function: { name: "bg_run", arguments: JSON.stringify({ command: reply.command }) },
      }],
    }));
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
  R173b: "A finished task wakes the session, collapses the list and opens from the 「已结束」 entry with its status and output",
  R173c: "The detail panel's Stop stops the task: it leaves the list, the panel shows it stopped",
  R173d: "A task still running through a server restart reads as interrupted, never in the running list",
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
const historyEntry = (page) => list(page).locator("[data-pi-bg-history]");
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

/** Creates a pi session whose first model request starts one bg task. */
async function newSessionWithTask(command, followUps = []) {
  scripted = [{ kind: "toolCall", command }, ...followUps];
  const created = await postJson("/api/agent/new", { cwd: project, type: "prompt", message: `start a background task: ${command}`, provider: "fake", modelId: "fake-model" });
  assert.equal(created.status, 200, `POST /api/agent/new -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const id = created.body.sessionId ?? created.body.data?.sessionId;
  assert.ok(typeof id === "string" && id.length > 0, "the create route returned no session id");
  const listed = await waitFor(async () => {
    const body = await getJson(`/api/agent/${encodeURIComponent(id)}/bg-tasks`);
    const running = (body.tasks ?? []).filter((task) => task.status === "running");
    return running.length === 1 ? running[0] : null;
  }, 45_000, `the bg task never appeared in ${id}'s own list`);
  return { sessionId: id, taskId: listed.id };
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
  ({ base: h.base } = await h.startServer({ name: SUITE, logFile: join(scratch, "server.log"), distDir, env: serverEnv() }));
  h.browser = await launchBrowser(() => chromium.launch());

  // R173a — the running list shows the task, desktop and narrow.
  await scenario("R173a", async () => {
    const { sessionId, taskId } = await newSessionWithTask("sleep 20");
    const page = await openSession(sessionId);
    const item = taskItem(page, taskId);
    await item.waitFor({ state: "visible", timeout: NAV_MS });
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
    await narrowItem.waitFor({ state: "visible", timeout: NAV_MS });
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

  // R173b — the end wakes the session, collapses the list, and the settled
  // task opens from the 「已结束」 entry with its status and output.
  await scenario("R173b", async () => {
    const { sessionId, taskId } = await newSessionWithTask("echo BG-E2E-DONE; sleep 1", [{ kind: "text", text: "WAKE-ANSWER" }]);
    const page = await openSession(sessionId);
    // The completion notification shows as its own message and the model answers it.
    await waitFor(async () => (await page.locator("body").innerText()).includes("Background task"), 45_000, "the completion notification never showed in the message list");
    await waitFor(async () => (await page.locator("body").innerText()).includes("WAKE-ANSWER"), 45_000, "the session was never woken (no answer to the notification)");
    // The list collapsed: no task item, and the 「已结束」 entry took over.
    await waitFor(async () => (await taskItem(page, taskId).count()) === 0, 45_000, "the settled task never left the running list");
    const entry = historyEntry(page);
    await entry.waitFor({ state: "visible", timeout: NAV_MS });
    await entry.click();
    const panel = historyPanel(page);
    await panel.waitFor({ state: "visible", timeout: NAV_MS });
    const row = panel.locator(`[data-pi-history-task="${taskId}"]`);
    await row.waitFor({ state: "visible", timeout: NAV_MS });
    assert.match(await row.innerText(), /Completed/, `the history row shows the final status: ${JSON.stringify(await row.innerText())}`);
    await shot("R173b-history")(page);
    await row.click();
    const detail = taskPanel(page, taskId);
    await detail.waitFor({ state: "visible", timeout: NAV_MS });
    await waitFor(async () => (await detail.innerText()).includes("BG-E2E-DONE"), 20_000, "the detail never showed the task's output tail");
    await closeOpened();
  });

  // R173c — the panel's Stop stops the running task.
  await scenario("R173c", async () => {
    const { sessionId, taskId } = await newSessionWithTask("sleep 30");
    const page = await openSession(sessionId);
    const item = taskItem(page, taskId);
    await item.waitFor({ state: "visible", timeout: NAV_MS });
    await item.click();
    const detail = taskPanel(page, taskId);
    await detail.waitFor({ state: "visible", timeout: NAV_MS });
    const stop = detail.getByRole("button", { name: "Stop task", exact: true });
    await stop.click();
    await waitFor(async () => (await taskItem(page, taskId).count()) === 0, 45_000, "the stopped task never left the running list");
    await waitFor(async () => (await detail.innerText()).includes("Stopped"), 20_000, "the open panel never showed the task stopped");
    await closeOpened();
  });

  // R173d — a task still running through a restart reads as interrupted.
  await scenario("R173d", async () => {
    const { sessionId, taskId } = await newSessionWithTask("sleep 30");
    const page = await openSession(sessionId);
    await taskItem(page, taskId).waitFor({ state: "visible", timeout: NAV_MS });
    // The server dies with the task running; a fresh server opens the same
    // registry and recovers the record as interrupted (#172's semantics).
    await closeOpened();
    await h.stop();
    ({ base: h.base } = await h.startServer({ name: `${SUITE}-restart`, logFile: join(scratch, "server-restart.log"), distDir, env: serverEnv() }));
    const reloaded = await openSession(sessionId);
    await waitFor(async () => (await taskItem(reloaded, taskId).count()) === 0, 45_000, "the interrupted task still shows in the running list after the restart");
    const entry = historyEntry(reloaded);
    await entry.waitFor({ state: "visible", timeout: NAV_MS });
    await entry.click();
    const panel = historyPanel(reloaded);
    await panel.waitFor({ state: "visible", timeout: NAV_MS });
    const row = panel.locator(`[data-pi-history-task="${taskId}"]`);
    await row.waitFor({ state: "visible", timeout: NAV_MS });
    assert.match(await row.innerText(), /Interrupted/, `the history row shows interrupted: ${JSON.stringify(await row.innerText())}`);
    await row.click();
    const detail = taskPanel(reloaded, taskId);
    await detail.waitFor({ state: "visible", timeout: NAV_MS });
    await shot("R173d-interrupted")(reloaded);
    await closeOpened();
  });

  assert.equal(h.errors.length, 0, `browser errors: ${JSON.stringify(h.errors.slice(0, 5))}`);
  console.log(`PASS ${SUITE}`);
} catch (error) {
  process.exitCode = 1;
  console.log(errorText(error));
} finally {
  await closeOpened().catch(() => {});
  await h.close().catch(() => {});
  rmSync(join(root, distDir, "dev/lock"), { force: true });
  if (failures.length > 0) {
    process.exitCode = 1;
    console.log(`${failures.length} scenario(s) failed`);
  }
}
