// carries: components/ClaudeBackgroundTaskPanel*, components/ClaudeSubagentRunningList*, lib/claude-background-task-view*, e2e/claude-stub/claude-bg-tasks.mjs
// AC4/AC5 (and AC3's page side) — a claude session's background tasks in
// the browser. The stub `e2e/claude-stub/claude-bg-tasks.mjs` stands in for claude on
// the server's PATH: `bg: <command>` starts a background `Bash` and `monitor: <desc>` a
// `Monitor`, each with a real `sleep` child in claude's process group; `hang` is a turn
// an interrupt ends, `stuck` one it does not. Its log names every stdin line, task (with
// its child's pid) and signal, so the suite checks what reached claude and what lives.
//
// One session runs the four scenarios in order, each building on the last:
//   R443a  both tasks appear in the running list's grid (kind, command, running, a
//          ticking elapsed time), never as subagent items; the session is not marked
//          subagentsActive; a reload keeps them; the shell task's panel shows its output.
//   R443b  the panel's Stop stops that one task (claude's `stop_task`): it leaves the
//          list, the panel shows it stopped, the monitor and its child run on.
//   R443c  Stop mid-turn sends the interrupt that spares background work: the turn
//          ends, the monitor is still listed, its child alive, nothing signalled.
//   R443d  a turn claude does not end at the deadline asks before ending background
//          work: the notice names the monitor (no signal yet); Keep waiting dismisses
//          it; Stop again, then Force stop signals claude and the list empties.
// Every wait polls the condition it asserts, with a bounded deadline. The suite ends
// with `PASS claude-background-tasks-443` only when every scenario passed.
import assert from "node:assert/strict";
import { chmodSync, copyFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, claudeEnv, claudeScratch, createApi, createCheck, createHarness, createWaitUntil, errorText, pidAlive, readJsonLines, root, seedStorage, setupFailed } from "./harness.mjs";

const SUITE = "claude-background-tasks-443";
assertNoDevServer();
const kit = claudeScratch("pi-web-claude-443-", { stub: false });
const { artifacts, scratch, project, stubDir, stubLog } = kit;
copyFileSync(join(root, "e2e/claude-stub/claude-bg-tasks.mjs"), join(stubDir, "claude"));
chmodSync(join(stubDir, "claude"), 0o755);

const DEV = "npm run dev";
const MONITOR = "watch logs";
// Beyond claude-session.ts's fixed 5 s interrupt deadline, with room for a loaded host.
const FORCE_STOP_WAIT_MS = 30_000;

const TITLES = {
  R443a: "Background Bash and Monitor tasks show in the running list's grid with kind, command, status and a ticking time, not as subagents, survive a reload, and the panel shows the output",
  R443b: "The panel's Stop stops only that task through claude's stop_task; it leaves the list, the panel shows it stopped, the other task runs on",
  R443c: "Stop mid-turn spares the background task: the interrupt carries spare_background_tasks, the turn ends, the task is still listed and alive, nothing is signalled",
  R443d: "A turn still running at the deadline asks before ending background work: the notice names it, Keep waiting dismisses it, Force stop signals claude and empties the list",
};

const h = createHarness();
let page;
let context;
let base;
let sessionId;
const failures = [];
const scenario = createCheck({ failures, message: errorText, record: ([id], text) => `${id}: ${text}`, fail: ([id], text) => console.log(`FAIL ${id} (${TITLES[id]}): ${text}`) });
const waitFor = createWaitUntil({ every: 200, withLast: true });
const { apiFetch, postJson } = createApi(() => base);

const records = () => readJsonLines(stubLog);
const taskRecord = (command) => records().find((entry) => entry.kind === "task" && entry.command === command);
const stdinLines = () => records().filter((entry) => entry.kind === "stdin").flatMap((entry) => {
  try { return [JSON.parse(entry.line)]; } catch { return []; }
});
const controlRequests = (subtype) => stdinLines().filter((line) => line.type === "control_request" && line.request?.subtype === subtype).map((line) => line.request);
const signals = () => records().filter((entry) => entry.kind === "signal");

const textarea = () => page.locator(".chat-input-textarea").first();
const sendButton = () => page.getByRole("button", { name: "Send", exact: true }).first();
const stopButton = () => page.getByRole("button", { name: "Stop agent", exact: true }).first();
const items = () => page.locator("[data-claude-subagent-items]");
const taskItems = () => items().locator("[data-claude-task-item]");
const taskItem = (taskId) => items().locator(`[data-claude-task-item="${taskId}"]`);
const subagentItem = (toolCallId) => page.locator(`[data-claude-subagent-item="${toolCallId}"]`);
const taskPanel = (taskId) => page.locator(`[data-claude-task-panel="${taskId}"]`);
const forceStopNotice = () => page.locator("[data-claude-force-stop]");

async function isRunning() {
  const response = await apiFetch(`/api/agent/${encodeURIComponent(sessionId)}`);
  if (response.status !== 200) return false;
  const body = await response.json();
  return body.running === true && body.state?.isStreaming === true;
}
async function openSession(cold = false) {
  await page.goto(`${base}/?session=${encodeURIComponent(sessionId)}`, { waitUntil: "domcontentloaded", timeout: cold ? COLD_MS : NAV_MS });
  await textarea().waitFor({ state: "visible", timeout: cold ? COLD_MS : NAV_MS });
}
async function send(text) {
  await textarea().fill(text);
  await sendButton().click({ timeout: ACTION_MS });
}
/** Sends a message whose turn ends on its own, and waits for that end. */
async function sendTurn(text) {
  await send(text);
  await waitFor(async () => stdinLines().some((line) => line.type === "user" && JSON.stringify(line.message?.content ?? "").includes(text)), 30_000, `"${text}" never reached claude's stdin`);
  await waitFor(async () => !(await isRunning()), 60_000, `the turn of "${text}" never ended`);
}
/** The item's elapsed time: its last span, in formatTurnDuration's shape. */
async function elapsedOf(locator) {
  const text = ((await locator.locator("span").last().textContent()) ?? "").trim();
  return /^\d+(s|m \d+s|h \d+m \d+s)$/.test(text) ? text : undefined;
}

h.trapInterrupt();

try {
  ({ base } = await h.startServer({
    name: SUITE,
    logFile: join(artifacts, `${SUITE}-server.log`),
    // The product default: only the suite ends a child.
    env: claudeEnv(kit, { PI_WEB_IDLE_TIMEOUT_MS: "600000" }),
  }));

  const { chromium } = await import("playwright");
  await h.launchBrowser(() => chromium.launch());
  context = await h.browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US", baseURL: base });
  await seedStorage(context);
  page = await context.newPage();
  page.setDefaultTimeout(ACTION_MS);
  await context.tracing.start({ screenshots: true, snapshots: true });

  // Created without a prompt, so the page attaches its stream before the first task starts.
  const created = await postJson("/api/agent/new", { cwd: project, type: "ensure_session", harness: "claude", model: "claude-haiku-5-5" });
  assert.equal(created.status, 200, `POST /api/agent/new -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  sessionId = created.body.sessionId;
  assert.ok(typeof sessionId === "string" && sessionId, "the create route returned no session id");
  await openSession(true);

  let dev;
  let monitor;

  await scenario("R443a", async () => {
    await sendTurn(`bg: ${DEV}`);
    await sendTurn(`monitor: ${MONITOR}`);
    dev = await waitFor(() => taskRecord(DEV), 10_000, "the stub never started the Bash task");
    monitor = await waitFor(() => taskRecord(MONITOR), 10_000, "the stub never started the Monitor task");

    // Both in the running list's grid, as task items, oldest first.
    await waitFor(async () => (await taskItems().count()) === 2, 30_000, async () => `the running list's grid does not hold the two tasks as [data-claude-task-item] (task items: ${await taskItems().count()}, subagent items: ${await page.locator("[data-claude-subagent-item]").count()})`);
    assert.deepEqual(await taskItems().evaluateAll((nodes) => nodes.map((node) => node.getAttribute("data-claude-task-item"))), [dev.taskId, monitor.taskId], "the tasks are not listed oldest first");
    const devText = (await taskItem(dev.taskId).textContent()) ?? "";
    const monitorText = (await taskItem(monitor.taskId).textContent()) ?? "";
    assert.match(devText, /Bash/, `the Bash task's item shows no Bash kind: ${devText}`);
    assert.ok(devText.includes(DEV), `the Bash task's item shows no command: ${devText}`);
    assert.match(devText, /Running/, `the Bash task's item shows no running status: ${devText}`);
    assert.match(monitorText, /Monitor/, `the Monitor task's item shows no Monitor kind: ${monitorText}`);
    assert.ok(monitorText.includes(MONITOR), `the Monitor task's item shows no description: ${monitorText}`);
    assert.match(monitorText, /Running/, `the Monitor task's item shows no running status: ${monitorText}`);
    // The elapsed time ticks.
    const first = await waitFor(() => elapsedOf(taskItem(dev.taskId)), 10_000, "the Bash task's item shows no elapsed time");
    await waitFor(async () => {
      const now = await elapsedOf(taskItem(dev.taskId));
      return now !== undefined && now !== first;
    }, 10_000, `the Bash task's elapsed time does not tick (stuck at ${first})`);
    // Not a subagent: no subagent item for either tool call.
    assert.equal(await subagentItem(dev.toolUseId).count(), 0, "the background Bash call is listed as a subagent");
    assert.equal(await subagentItem(monitor.toolUseId).count(), 0, "the Monitor call is listed as a subagent");
    // The session's turn is settled: a running task does not hold it as subagentsActive.
    const running = await (await apiFetch("/api/agent/running")).json();
    assert.ok(!(running.subagentsActiveSessionIds ?? []).includes(sessionId), "the session is still reported subagentsActive while only background tasks run");

    // A reload keeps both (the server replays the running tasks).
    await openSession();
    await waitFor(async () => (await taskItem(dev.taskId).count()) === 1 && (await taskItem(monitor.taskId).count()) === 1, 30_000, "a reload lost the running tasks");

    // The Bash task's panel shows its output, read from claude.
    await taskItem(dev.taskId).click({ timeout: ACTION_MS });
    await taskPanel(dev.taskId).waitFor({ state: "visible", timeout: 10_000 });
    await waitFor(async () => ((await taskPanel(dev.taskId).textContent()) ?? "").includes("stub output line 2"), 15_000, "the task panel does not show the task's output");
    const header = (await taskPanel(dev.taskId).textContent()) ?? "";
    assert.ok(header.includes(DEV), `the task panel does not name the command: ${header.slice(0, 200)}`);
    assert.ok(controlRequests("get_task_output").some((request) => request.task_id === dev.taskId), "the panel's output did not come from claude's get_task_output");
  });

  await scenario("R443b", async () => {
    assert.ok(dev && monitor, "R443a started no tasks");
    await taskPanel(dev.taskId).waitFor({ state: "visible", timeout: 10_000 });
    await taskPanel(dev.taskId).getByRole("button", { name: "Stop task", exact: true }).click({ timeout: ACTION_MS });
    await waitFor(() => controlRequests("stop_task").some((request) => request.task_id === dev.taskId), 15_000, "no stop_task for the Bash task reached claude");
    await waitFor(async () => (await taskItem(dev.taskId).count()) === 0, 15_000, "the stopped task is still listed");
    await waitFor(async () => /Stopped/.test((await taskPanel(dev.taskId).textContent()) ?? ""), 15_000, "the open panel does not show the task stopped");
    await waitFor(() => !pidAlive(dev.taskPid), 10_000, "the stopped task's process still runs");
    assert.equal(await taskItem(monitor.taskId).count(), 1, "stopping the Bash task took the Monitor off the list");
    assert.ok(pidAlive(monitor.taskPid), "stopping the Bash task killed the Monitor's process");
    assert.deepEqual(controlRequests("stop_task").map((request) => request.task_id), [dev.taskId], "stop_task went to another task too");
    await taskPanel(dev.taskId).getByRole("button", { name: "Close", exact: true }).click({ timeout: ACTION_MS });
    await waitFor(async () => (await taskPanel(dev.taskId).count()) === 0, 10_000, "Close left the task panel open");
  });

  await scenario("R443c", async () => {
    assert.ok(monitor, "R443a started no Monitor");
    const interruptsBefore = controlRequests("interrupt").length;
    await send("hang");
    await stopButton().waitFor({ state: "visible", timeout: 30_000 });
    await waitFor(() => isRunning(), 30_000, "the hang turn is not running");
    await stopButton().click({ timeout: ACTION_MS });
    const interrupt = await waitFor(() => controlRequests("interrupt")[interruptsBefore], 15_000, "Stop sent claude no interrupt");
    assert.equal(interrupt.spare_background_tasks, true, `the interrupt does not spare background work: ${JSON.stringify(interrupt)}`);
    await waitFor(async () => !(await isRunning()), 30_000, "the interrupted turn never ended");
    await stopButton().waitFor({ state: "detached", timeout: 30_000 });
    assert.equal(await taskItem(monitor.taskId).count(), 1, "Stop mid-turn took the Monitor off the list");
    assert.ok(pidAlive(monitor.taskPid), "Stop mid-turn killed the Monitor's process");
    assert.deepEqual(signals(), [], "Stop mid-turn signalled claude");
  });

  await scenario("R443d", async () => {
    assert.ok(monitor, "R443a started no Monitor");
    const launch = records().find((entry) => entry.kind === "launch");
    await send("stuck");
    await stopButton().waitFor({ state: "visible", timeout: 30_000 });
    await waitFor(() => isRunning(), 30_000, "the stuck turn is not running");
    await stopButton().click({ timeout: ACTION_MS });
    await waitFor(async () => (await forceStopNotice().count()) === 1 && (await forceStopNotice().isVisible()), FORCE_STOP_WAIT_MS, "no force-stop notice after the interrupt deadline");
    const notice = (await forceStopNotice().textContent()) ?? "";
    assert.ok(notice.includes(MONITOR), `the force-stop notice does not name the Monitor: ${notice}`);
    assert.deepEqual(signals(), [], "claude was signalled before the force stop was confirmed");
    assert.ok(pidAlive(monitor.taskPid), "the Monitor's process died before the force stop was confirmed");
    assert.ok(pidAlive(launch?.pid), "claude died before the force stop was confirmed");
    // Keep waiting: the notice goes, nothing is signalled.
    await forceStopNotice().getByRole("button", { name: "Keep waiting", exact: true }).click({ timeout: ACTION_MS });
    await waitFor(async () => (await forceStopNotice().count()) === 0, 10_000, "Keep waiting left the notice up");
    assert.deepEqual(signals(), [], "Keep waiting signalled claude");
    assert.equal(await taskItem(monitor.taskId).count(), 1, "Keep waiting took the Monitor off the list");
    // Stop again; at the deadline, Force stop.
    await stopButton().click({ timeout: ACTION_MS });
    await waitFor(async () => (await forceStopNotice().count()) === 1 && (await forceStopNotice().isVisible()), FORCE_STOP_WAIT_MS, "no force-stop notice after the second Stop's deadline");
    await forceStopNotice().getByRole("button", { name: "Force stop", exact: true }).click({ timeout: ACTION_MS });
    await waitFor(() => signals().some((entry) => entry.signal === "SIGINT"), 15_000, "Force stop sent claude no SIGINT");
    await waitFor(async () => (await taskItems().count()) === 0, 15_000, "the running list still holds a task after the force stop");
    await waitFor(async () => (await forceStopNotice().count()) === 0, 15_000, "the force-stop notice stayed after the force stop");
    await waitFor(() => !pidAlive(monitor.taskPid), 10_000, "the force stop left the Monitor's process running");
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  if (page && failures.length) await page.screenshot({ path: join(artifacts, `${SUITE}-failure.png`) }).catch(() => {});
  if (context) await context.tracing.stop({ path: join(artifacts, `${SUITE}-trace.zip`) }).catch(() => {});
  await h.close();
  for (const entry of records()) {
    const pid = entry.kind === "launch" ? entry.pid : entry.kind === "task" ? entry.taskPid : undefined;
    if (pid) {
      try { process.kill(pid, "SIGKILL"); } catch { /* already gone */ }
    }
  }
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL ${SUITE}: ${failures.length} scenario(s) failed`);
  process.exitCode = 1;
} else {
  console.log(`PASS ${SUITE}`);
}
