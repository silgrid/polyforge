// carries: components/PiSubagentPanel*, components/SubagentPanelFrame*, components/PiRunningList*, components/AppShell*, components/AgentSessionPanel*, components/AppShell.split-pane-subagent.test.mjs, hooks/usePiSubagentPanel*, lib/pi-subagent-panel.ts
// polyforge#255 round 2 (review #1/#2): a pi subagent's in-chat progress
// panel in a real browser. A local OpenAI-compatible fake model answers the
// session's first request with an `Agent` tool call, so the built-in runtime
// starts a real child session; every later fact is the product's own.
//
// P1  the running list's row, the Agent tool card's button and the top-bar
//     subagent row each open the panel in the chat (classic viewport; the
//     split-pane request filter is executed by AppShell.split-pane-subagent).
// P2  the panel shows the child transcript, refreshes as entries arrive, sends
//     a running steer through POST /api/subagents/<id> {action:"steer"}, and
//     disables the box with its reason once the run settles.
// P3  Esc, Close and Jump to launch work; Jump closes the panel and keeps the
//     launch card.
// P4  no right-panel agent tab is created; the Claude panel and file tabs are
//     untouched (their own suites stay green).
//
// Every wait polls the condition it asserts with a bounded deadline.
import assert from "node:assert/strict";
import { createServer as createHttpServer } from "node:http";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { createCheck, createHarness, createWaitUntil, errorText, launchBrowser, root } from "./harness.mjs";

const SUITE = "pi-subagent-panel";
const distDir = process.env.PI_WEB_DIST_DIR || `.next/e2e/${SUITE}`;
const scratch = mkdtempSync(join(tmpdir(), `${SUITE}-`));
const home = join(scratch, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(scratch, "project");
mkdirSync(project, { recursive: true });
mkdirSync(agentDir, { recursive: true });
mkdirSync(join(agentDir, "agents"), { recursive: true });
writeFileSync(join(agentDir, "agents/settings.json"), JSON.stringify({ builtInEnabled: true }));

// ---- fake model ------------------------------------------------------------
let scripted = [];
const OK = { kind: "text", text: "OK-ANSWER" };
const model = createHttpServer(async (request, response) => {
  await new Promise((resolve) => {
    request.on("data", () => {});
    request.on("end", resolve);
    request.on("error", resolve);
  });
  const reply = scripted.length > 0 ? scripted.shift() : OK;
  if (reply.delay) await new Promise((resolve) => setTimeout(resolve, reply.delay));
  response.writeHead(200, { "Content-Type": "text/event-stream" });
  const chunk = (delta, finish = null, extra = {}) => `data: ${JSON.stringify({
    id: "fake", object: "chat.completion.chunk", created: 0, model: "fake-model",
    choices: [{ index: 0, delta, finish_reason: finish }], ...extra,
  })}\n\n`;
  response.write(chunk({ role: "assistant", content: "" }));
  if (reply.kind === "agent") {
    response.write(chunk({
      tool_calls: [{
        index: 0,
        id: "call_agent_1",
        type: "function",
        function: {
          name: "Agent",
          arguments: JSON.stringify({ subagent_type: "general-purpose", prompt: "Read the note and report its first line.", description: "Read the note" }),
        },
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

const h = createHarness();
const failures = [];
const scenario = createCheck({ failures, message: errorText, record: ([id], text) => `${id}: ${text}`, fail: ([id], text) => console.log(`FAIL ${id}: ${text}`) });
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
const panel = (page) => page.locator("[data-pi-subagent-panel]").first();
const panelText = async (page) => (await panel(page).count()) ? panel(page).innerText() : "";
const cardButton = (page) => page.getByRole("button", { name: "Open sub-agent panel" }).first();
const runningItem = (page, id) => page.locator(`[data-pi-subagent-item="${id}"]`);

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

/** Starts one session whose first turn dispatches a subagent; returns its id and the child's. */
async function newSessionWithSubagent(delayMs) {
  scripted = [
    { kind: "agent" },
    { kind: "text", text: "CHILD-ANSWER", delay: delayMs },
    { kind: "text", text: "PARENT-DONE" },
  ];
  const created = await postJson("/api/agent/new", { cwd: project, type: "prompt", message: "delegate to a subagent", provider: "fake", modelId: "fake-model" });
  assert.equal(created.status, 200, `POST /api/agent/new -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const sessionId = created.body.sessionId ?? created.body.data?.sessionId;
  assert.ok(typeof sessionId === "string" && sessionId.length > 0, "the create route returned no session id");
  const run = await waitFor(async () => {
    const body = await getJson(`/api/agent/${encodeURIComponent(sessionId)}/subagent-runs`);
    return (body.runs ?? [])[0] ?? null;
  }, 45_000, "the subagent run never appeared in the parent's list");
  return { sessionId, childId: run.sessionId };
}

async function openSession(sessionId) {
  const page = await h.openPage({
    session: sessionId,
    storage: STORAGE,
    ready: async (page, _o, timeout) => { await composer(page).waitFor({ state: "visible", timeout }); },
  });
  opened.push(page);
  return page;
}

async function revealToolCards(page) {
  for (const fold of await page.getByRole("button", { name: /^Process details/ }).all()) {
    if ((await fold.getAttribute("aria-expanded")) !== "true") await fold.click();
  }
}

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

  // P1 + P2 — the three entries, the live transcript and a real steer.
  await scenario("P1P2", async () => {
    const { sessionId, childId } = await newSessionWithSubagent(20_000);
    const page = await openSession(sessionId);
    // The running list's row appears while the child runs.
    await waitFor(async () => (await runningItem(page, childId).count()) === 1, 30_000, "the running subagent never appeared in the list");
    await runningItem(page, childId).click();
    await waitFor(async () => (await panel(page).count()) === 1, 20_000, "the running list did not open the panel");
    await waitFor(async () => /running/.test(await panel(page).getAttribute("data-pi-subagent-status") ?? ""), 20_000, "the panel never showed the run running");
    // The box is enabled while running; the steer is a real POST to the run's route.
    const textarea = panel(page).locator("textarea");
    await textarea.waitFor({ state: "visible", timeout: 10_000 });
    assert.equal(await textarea.isDisabled(), false, "a running run accepts a message");
    let steer = null;
    await page.route(/\/api\/subagents\/[^/?]+$/, async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      steer = JSON.parse(route.request().postData() ?? "{}");
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ok: true }) });
    });
    await textarea.fill("focus tests");
    await textarea.press("Enter");
    await waitFor(async () => steer !== null, 15_000, "the steer never reached the subagents route");
    assert.deepEqual(steer, { action: "steer", message: "focus tests" }, "the message goes through the run's own steer action");
    await page.unroute(/\/api\/subagents\/[^/?]+$/);
    // The transcript refreshes in place; the settled run disables the box with its reason.
    await waitFor(async () => (await panelText(page)).includes("CHILD-ANSWER"), 45_000, "the child transcript never reached the panel");
    await waitFor(async () => /Completed/.test(await panelText(page)), 45_000, "the panel never showed the run completed");
    assert.equal(await textarea.isDisabled(), true, "a settled run disables the box");
    await waitFor(async () => (await panelText(page)).includes("This run has ended"), 20_000, "the settled reason is not shown");
    // Esc closes it.
    await page.keyboard.press("Escape");
    await waitFor(async () => (await panel(page).count()) === 0, 10_000, "Esc did not close the panel");
    await closeOpened();
  });

  // P3 — the Agent card opens it; Jump closes it and keeps the launch card.
  await scenario("P3", async () => {
    const { sessionId, childId } = await newSessionWithSubagent(50);
    const page = await openSession(sessionId);
    await waitFor(async () => (await page.locator("body").innerText()).includes("PARENT-DONE"), 45_000, "the parent turn never finished");
    await waitFor(async () => (await page.locator(`[data-pi-subagent-panel="${childId}"]`).count()) === 0, 5_000, "the panel opened by itself");
    await revealToolCards(page);
    await waitFor(async () => (await cardButton(page).count()) === 1, 20_000, "the Agent card's panel button never appeared");
    await cardButton(page).click();
    await waitFor(async () => (await panel(page).count()) === 1, 20_000, "the Agent card did not open the panel");
    await waitFor(async () => (await panelText(page)).includes("CHILD-ANSWER"), 30_000, "the card's panel never showed the child result");
    const jump = panel(page).getByRole("button", { name: "Jump to launch" });
    await waitFor(async () => !(await jump.isDisabled()), 20_000, "Jump never became available for the launch card");
    await jump.click();
    await waitFor(async () => (await panel(page).count()) === 0, 10_000, "Jump did not close the panel");
    assert.equal(await page.locator(`[data-pi-agent-tool-call]`).count() >= 1, true, "the launch card is still in the chat");
    await closeOpened();
  });

  // P1 top bar — the subagent row opens the panel; no right-panel agent tab.
  await scenario("P1-topbar", async () => {
    const { sessionId, childId } = await newSessionWithSubagent(50);
    const page = await openSession(sessionId);
    await waitFor(async () => (await page.locator("body").innerText()).includes("PARENT-DONE"), 45_000, "the parent turn never finished");
    await page.getByRole("button", { name: /^Agents/ }).click();
    const row = page.getByRole("option").filter({ hasText: "Read the note" }).first();
    await row.waitFor({ state: "visible", timeout: 20_000 });
    await row.click();
    await waitFor(async () => (await page.locator(`[data-pi-subagent-panel="${childId}"]`).count()) === 1, 20_000, "the top-bar row did not open the panel");
    // The right panel gained no agent tab (only file/terminal tabs can exist).
    assert.equal(await page.locator('[role="tab"][aria-label*="Sub-agent"]').count(), 0, "a right-panel agent tab was created");
    await closeOpened();
  });
} finally {
  await closeOpened();
  await h.close().catch(() => {});
  model.close();
  rmSync(scratch, { recursive: true, force: true });
  for (const [title, list] of failures) {
    console.log(list.length ? `FAIL ${title}: ${list[0]}` : `PASS ${title}`);
    if (list.length) process.exitCode = 1;
  }
  if (!process.exitCode) console.log(`PASS ${SUITE}`);
}
