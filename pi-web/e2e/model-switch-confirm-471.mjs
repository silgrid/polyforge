// carries: components/ModelSelector*
// carries: docs/agents/models.md
// carries: components/SelectorRow*
// A phone confirms before switching an open session's model: e2e acceptance pass.
//
// On a phone (390 x 844, touch) the composer's model selector of an open session,
// pi or claude, asks 「从 <当前模型> 切换到 <新模型>？」 before it sends `set_model`:
// cancelling sends nothing and keeps the model, choosing the current model asks
// nothing and sends nothing, confirming sends exactly one switch and the selector
// (the place the session shows its model) and the server's state show the new
// model. A row's "save as default" star switches the open session too, so on a
// phone a star on another model asks the same confirmation, and a cancel saves
// nothing and sends nothing. A desktop selector still switches at once, and a
// fresh phone composer (no session yet) picks its starting model without a dialog.
//
// - pi: a local OpenAI-compatible fake model server with two models in
//   models.json (`fake-model`, `fake-model-2`), `fake-model` the default.
// - claude: the PATH-injected e2e stub (e2e/claude-stub/claude.mjs).
// No real model is called and no /api/agent/* request is routed or stubbed: the
// page's own requests are recorded, and each dialog is recorded with the number
// of set_model requests sent at that moment. "Nothing was sent" is proven by the
// final exact request list, never by waiting.
//
// Every scenario is individually guarded and prints one `PASS S471-nn` marker.
import assert from "node:assert/strict";
import { rmSync, writeFileSync } from "node:fs";
import { createServer as createHttpServer } from "node:http";
import { join } from "node:path";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, claudeEnv, claudeScratch, createApi, createCheck, createHarness, createWaitUntil, errorText, root, setupFailed } from "./harness.mjs";

assertNoDevServer();
const kit = claudeScratch("pi-web-model-confirm-471-");
const { artifacts, scratch, agentDir, project } = kit;

const PI_FROM = { id: "fake-model", name: "Fake Model" };
const PI_TO = { id: "fake-model-2", name: "Fake Model 2" };
// The session starts on a full name (listed beside claude's aliases,) and
// switches to one of the aliases claude's initialize answer lists.
const CLAUDE_FROM = "claude-opus-5-5";
const CLAUDE_TO = "fable";
// How long a switch gets to reach the selector and the server's state.
const SWITCH_MS = 60_000;
// How long a dialog gets to show after the click that raises it.
const DIALOG_MS = 10_000;

// A local OpenAI-compatible model for the pi sessions: it answers every request at once.
const model = createHttpServer(async (request, response) => {
  request.resume();
  await new Promise((resolve) => request.once("end", resolve));
  response.writeHead(200, { "Content-Type": "text/event-stream" });
  const chunk = (delta, finish = null, extra = {}) => `data: ${JSON.stringify({
    id: "fake", object: "chat.completion.chunk", created: 0, model: "fake-model",
    choices: [{ index: 0, delta, finish_reason: finish }], ...extra,
  })}\n\n`;
  response.write(chunk({ role: "assistant", content: "E2E 471 answer." }));
  response.write(chunk({}, "stop", { usage: { prompt_tokens: 10, completion_tokens: 3, total_tokens: 13 } }));
  response.write("data: [DONE]\n\n");
  response.end();
});
model.listen(0, "127.0.0.1");
await new Promise((resolve) => model.once("listening", resolve));
writeFileSync(join(agentDir, "models.json"), JSON.stringify({
  providers: {
    fake: {
      baseUrl: `http://127.0.0.1:${model.address().port}/v1`,
      api: "openai-completions",
      apiKey: "fake-key",
      models: [
        { id: PI_FROM.id, name: PI_FROM.name, contextWindow: 100_000, maxTokens: 8192 },
        { id: PI_TO.id, name: PI_TO.name, contextWindow: 100_000, maxTokens: 8192 },
      ],
    },
  },
}));
writeFileSync(join(agentDir, "settings.json"), JSON.stringify({ defaultProvider: "fake", defaultModel: PI_FROM.id }));

const h = createHarness();
let browser;
let mobileContext;
let desktopContext;
let page;
let base;
let firstLoad = true;
const failures = [];

const scenario = createCheck({ failures, message: errorText, record: (names, text) => `${names.join(" ")}: ${text}` });

const waitFor = createWaitUntil();

const { apiFetch, getJson, postJson } = createApi(() => base);

/** The live session's model id once it is idle; undefined while it runs or when nothing is alive. */
async function idleModelId(id) {
  const reply = await getJson(`/api/agent/${encodeURIComponent(id)}`);
  return reply.running === true && reply.state?.isStreaming === false ? reply.state?.model?.id : undefined;
}
async function waitIdleModel(id, expected, what) {
  let seen;
  await waitFor(async () => {
    seen = await idleModelId(id);
    return seen === expected;
  }, SWITCH_MS, () => `${what}: GET /api/agent/<id> reports model ${JSON.stringify(seen)}, expected ${JSON.stringify(expected)}`);
}

async function createPiSession(message) {
  const created = await postJson("/api/agent/new", { cwd: project, type: "prompt", message, provider: "fake", modelId: PI_FROM.id });
  assert.equal(created.status, 200, `POST /api/agent/new (pi) -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const id = created.body.sessionId;
  assert.ok(typeof id === "string" && id.length > 0, "the create route returned no pi session id");
  await waitIdleModel(id, PI_FROM.id, "the new pi session");
  return id;
}
async function createClaudeSession(message) {
  const created = await postJson("/api/agent/new", { cwd: project, type: "prompt", message, harness: "claude", model: CLAUDE_FROM });
  assert.equal(created.status, 200, `POST /api/agent/new (claude) -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const id = created.body.sessionId;
  assert.ok(typeof id === "string" && id.length > 0, "the create route returned no claude session id");
  await waitIdleModel(id, CLAUDE_FROM, "the new claude session");
  return id;
}

const bottomBar = () => page.locator(".chat-input-controls").first();
const textarea = () => page.locator(".chat-input-textarea").first();
const modelTrigger = () => bottomBar().locator(".model-selector > button").first();
const triggerText = async () => (await modelTrigger().innerText()).trim();

async function load(url) {
  await page.goto(url, { waitUntil: "domcontentloaded", timeout: firstLoad ? COLD_MS : NAV_MS });
  firstLoad = false;
  await textarea().waitFor({ state: "visible", timeout: NAV_MS });
}
const openSession = (id) => load(`${base}/?session=${encodeURIComponent(id)}`);

/** Waits until the selector reads `name` and can be used (an open session's selector is disabled while it is busy). */
async function waitForTrigger(name, what, timeoutMs = SWITCH_MS) {
  let seen = "";
  await waitFor(async () => {
    seen = await triggerText();
    return seen === name && await modelTrigger().isEnabled();
  }, timeoutMs, () => `${what}: the model selector reads ${JSON.stringify(seen)}, expected ${JSON.stringify(name)} and enabled`);
}

/** Opens the selector and clicks the row whose name is `name`. */
async function chooseModel(name) {
  await modelTrigger().click({ timeout: ACTION_MS });
  const listbox = page.locator('[role="listbox"]').last();
  await listbox.waitFor({ state: "visible", timeout: ACTION_MS });
  const rows = listbox.locator('[role="option"]');
  let names = [];
  await waitFor(async () => {
    names = (await rows.allInnerTexts()).map((text) => text.trim().split("\n")[0].trim());
    return names.includes(name);
  }, ACTION_MS, () => `no "${name}" row in the model selector (rows: ${names.join(", ")})`);
  await rows.nth(names.indexOf(name)).click({ timeout: ACTION_MS });
  await listbox.waitFor({ state: "detached", timeout: ACTION_MS });
}

/** Opens the selector and taps the "save as default" star of the row whose name is `name`. */
async function starModel(name) {
  await modelTrigger().click({ timeout: ACTION_MS });
  const listbox = page.locator('[role="listbox"]').last();
  await listbox.waitFor({ state: "visible", timeout: ACTION_MS });
  const rows = listbox.locator('[role="option"]');
  let names = [];
  await waitFor(async () => {
    names = (await rows.allInnerTexts()).map((text) => text.trim().split("\n")[0].trim());
    return names.includes(name);
  }, ACTION_MS, () => `no "${name}" row in the model selector (rows: ${names.join(", ")})`);
  // SelectorRow: the row's option button and, beside it, the star button (always shown on a touch layout).
  const star = rows.nth(names.indexOf(name)).locator('xpath=../button[not(@role="option")]');
  await star.waitFor({ state: "visible", timeout: ACTION_MS });
  await star.click({ timeout: ACTION_MS });
  await listbox.waitFor({ state: "detached", timeout: ACTION_MS });
}

/**
 * Records, from here on, the page's set_model requests (`POST /api/agent/<id>` whose JSON
 * `type` is `set_model`), its default-model saves (`PUT /api/models/default`) and every dialog
 * with the number of set_model requests sent when it showed. Each dialog is answered with
 * `answer` as it stands then.
 */
function watchPage() {
  const switches = [];
  const defaults = [];
  const dialogs = [];
  const watch = { switches, defaults, dialogs, answer: "dismiss" };
  const onRequest = (request) => {
    const { pathname } = new URL(request.url());
    if (request.method() === "PUT" && pathname === "/api/models/default") {
      let body = {};
      try { body = JSON.parse(request.postData() ?? "{}"); } catch { /* not a save */ }
      defaults.push(body.modelId);
      return;
    }
    if (request.method() !== "POST") return;
    const match = /^\/api\/agent\/([^/]+)$/.exec(pathname);
    if (!match || match[1] === "new") return;
    let body = {};
    try { body = JSON.parse(request.postData() ?? "{}"); } catch { /* not a command */ }
    if (body.type === "set_model") switches.push({ id: decodeURIComponent(match[1]), body });
  };
  const onDialog = (dialog) => {
    dialogs.push({ type: dialog.type(), message: dialog.message(), switchesAtDialog: switches.length });
    void (watch.answer === "accept" ? dialog.accept() : dialog.dismiss()).catch(() => {});
  };
  page.on("request", onRequest);
  page.on("dialog", onDialog);
  watch.stop = () => {
    page.off("request", onRequest);
    page.off("dialog", onDialog);
  };
  return watch;
}
const switchModel = (entry) => entry.body.modelId ?? entry.body.model;

async function waitForDialogs(watch, count, what) {
  await waitFor(() => watch.dialogs.length >= count, DIALOG_MS,
    () => `${what}: no confirmation dialog (dialogs: ${JSON.stringify(watch.dialogs)}, set_model requests: ${JSON.stringify(watch.switches.map(switchModel))})`);
}

/**
 * The phone steps for one open session: the current model asks nothing; another model asks
 * 「从 <from> 切换到 <to>」 with nothing sent, a cancel keeps the model, a confirm switches it;
 * exactly one set_model, carrying the new model, was sent in all.
 */
async function phoneConfirmSteps(id, from, to) {
  await openSession(id);
  await waitForTrigger(from.name, "the opened session");
  const watch = watchPage();
  try {
    // 1. The current model: no dialog.
    await chooseModel(from.name);
    assert.deepEqual(watch.dialogs, [], "choosing the current model raised a dialog");

    // 2. Another model, cancelled: the dialog names both models, nothing was sent.
    watch.answer = "dismiss";
    await chooseModel(to.name);
    await waitForDialogs(watch, 1, `choosing ${to.name}`);
    const [asked] = watch.dialogs;
    assert.equal(asked.type, "confirm", `the dialog is a ${asked.type}, not a confirmation`);
    assert.ok(asked.message.includes(`从 ${from.name} 切换到 ${to.name}`), `the dialog does not read 从 ${from.name} 切换到 ${to.name}: ${JSON.stringify(asked.message)}`);
    assert.equal(asked.switchesAtDialog, 0, "a set_model request was sent before the dialog was answered");
    await waitForTrigger(from.name, "after cancelling");

    // 3. The same model again, confirmed: the dialog shows first, still nothing sent.
    watch.answer = "accept";
    await chooseModel(to.name);
    await waitForDialogs(watch, 2, `choosing ${to.name} again`);
    assert.ok(watch.dialogs[1].message.includes(`从 ${from.name} 切换到 ${to.name}`), `the second dialog does not read 从 ${from.name} 切换到 ${to.name}: ${JSON.stringify(watch.dialogs[1].message)}`);
    assert.equal(watch.dialogs[1].switchesAtDialog, 0, "a set_model request was sent before the confirmation (the cancel or the current model sent one)");

    // 4. The selector and the server show the new model.
    await waitForTrigger(to.name, "after confirming");
    await waitIdleModel(id, to.id, "after confirming");

    // 5. Exactly one switch in all, carrying the new model: the cancel and the current model sent none.
    assert.deepEqual(watch.switches.map((entry) => [entry.id, switchModel(entry)]), [[id, to.id]], "the set_model requests sent are not exactly the one confirmed switch");
    assert.equal(watch.dialogs.length, 2, `more dialogs than the two switches: ${JSON.stringify(watch.dialogs)}`);
  } finally {
    watch.stop();
  }
}

/**
 * The phone star steps for one open pi session on `from`: a star on `to` asks
 * 「从 <from> 切换到 <to>」 with nothing sent; a cancel keeps the model and saves nothing;
 * a confirm saves the default and switches the session: exactly one set_model, carrying `to`.
 */
async function phoneStarSteps(id, from, to) {
  await openSession(id);
  await waitForTrigger(from.name, "the opened session");
  const watch = watchPage();
  try {
    // 1. The star on another model, cancelled: the dialog names both models, nothing was sent or saved.
    watch.answer = "dismiss";
    await starModel(to.name);
    await waitForDialogs(watch, 1, `the star on ${to.name}`);
    const [asked] = watch.dialogs;
    assert.equal(asked.type, "confirm", `the star's dialog is a ${asked.type}, not a confirmation`);
    assert.ok(asked.message.includes(`从 ${from.name} 切换到 ${to.name}`), `the star's dialog does not read 从 ${from.name} 切换到 ${to.name}: ${JSON.stringify(asked.message)}`);
    assert.equal(asked.switchesAtDialog, 0, "the star sent a set_model request before the dialog was answered");
    await waitForTrigger(from.name, "after cancelling the star");

    // 2. The star again, confirmed: the dialog shows first, still nothing sent.
    watch.answer = "accept";
    await starModel(to.name);
    await waitForDialogs(watch, 2, `the star on ${to.name} again`);
    assert.ok(watch.dialogs[1].message.includes(`从 ${from.name} 切换到 ${to.name}`), `the second star dialog does not read 从 ${from.name} 切换到 ${to.name}: ${JSON.stringify(watch.dialogs[1].message)}`);
    assert.equal(watch.dialogs[1].switchesAtDialog, 0, "a set_model request was sent before the star's confirmation (the cancelled star sent one)");

    // 3. The selector and the server show the starred model.
    await waitForTrigger(to.name, "after confirming the star");
    await waitIdleModel(id, to.id, "after confirming the star");

    // 4. Exactly one switch and one default save in all, both for the starred model: the cancel sent none.
    assert.deepEqual(watch.switches.map((entry) => [entry.id, switchModel(entry)]), [[id, to.id]], "the set_model requests sent are not exactly the one confirmed star");
    assert.deepEqual(watch.defaults, [to.id], "the default saves are not exactly the one confirmed star");
    assert.equal(watch.dialogs.length, 2, `more dialogs than the two stars: ${JSON.stringify(watch.dialogs)}`);
  } finally {
    watch.stop();
  }
}

h.trapInterrupt();

try {
  ({ base } = await h.startServer({ name: "model-switch-confirm-471", logFile: join(artifacts, "model-switch-confirm-471-server.log"), env: claudeEnv(kit, {
      PI_WEB_DISABLE_MCP: "1",
      // The product default: no child or wrapper is reaped mid-scenario.
      PI_WEB_IDLE_TIMEOUT_MS: "600000",
      // The stub answers Pi Web's probe with the recorded 2.1.322 initialize answer.
      PI_E2E_CLAUDE_INITIALIZE_FIXTURE: join(root, "lib/__fixtures__/claude-2.1.322/initialize"),
  }) }));

  const { chromium } = await import("playwright");
  browser = await h.launchBrowser(() => chromium.launch());
  mobileContext = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true, locale: "zh-CN", baseURL: base });
  desktopContext = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US", baseURL: base });
  for (const [context, locale] of [[mobileContext, "zh-CN"], [desktopContext, "en"]]) {
    await context.addInitScript((value) => {
      try {
        window.localStorage.setItem("pi-web:split-view-enabled", "false");
        window.localStorage.setItem("pi-web:session-age-filter-days", "0");
        window.localStorage.setItem("pi-locale", value);
      } catch {}
    }, locale);
  }
  const mobilePage = await mobileContext.newPage();
  mobilePage.setDefaultTimeout(ACTION_MS);
  const desktopPage = await desktopContext.newPage();
  desktopPage.setDefaultTimeout(ACTION_MS);
  await mobileContext.tracing.start({ screenshots: true, snapshots: true });

  await scenario("S471-01", "a phone confirms an open claude session's model switch", async () => {
    page = mobilePage;
    const id = await createClaudeSession("E2E 471 claude phone");
    await phoneConfirmSteps(id, { id: CLAUDE_FROM, name: CLAUDE_FROM }, { id: CLAUDE_TO, name: CLAUDE_TO });
    await page.screenshot({ path: join(artifacts, "model-confirm-471-claude-390.png") });
  });

  await scenario("S471-02", "a phone confirms an open pi session's model switch", async () => {
    page = mobilePage;
    const id = await createPiSession("E2E 471 pi phone");
    let restored;
    try {
      // The star first (the session then holds PI_TO), then the rows back to PI_FROM.
      await phoneStarSteps(id, PI_FROM, PI_TO);
      await phoneConfirmSteps(id, PI_TO, PI_FROM);
    } finally {
      // The fixture's default for the later scenarios, whatever the star saved.
      restored = await apiFetch("/api/models/default", { method: "PUT", body: JSON.stringify({ cwd: project, provider: "fake", modelId: PI_FROM.id }) }).catch((error) => ({ status: String(error) }));
    }
    assert.equal(restored.status, 200, `PUT /api/models/default (restoring the fixture default) -> ${restored.status}`);
    await page.screenshot({ path: join(artifacts, "model-confirm-471-pi-390.png") });
  });

  await scenario("S471-03", "a desktop selector switches an open session's model at once, with no dialog", async () => {
    page = desktopPage;
    for (const [harness, create, from, to] of [
      ["pi", createPiSession, PI_FROM, PI_TO],
      ["claude", createClaudeSession, { id: CLAUDE_FROM, name: CLAUDE_FROM }, { id: CLAUDE_TO, name: CLAUDE_TO }],
    ]) {
      const id = await create(`E2E 471 ${harness} desktop`);
      await openSession(id);
      await waitForTrigger(from.name, `the opened ${harness} session`);
      const watch = watchPage();
      try {
        await chooseModel(to.name);
        await waitForTrigger(to.name, `${harness}: after choosing ${to.name}`);
        await waitIdleModel(id, to.id, `${harness}: after choosing ${to.name}`);
        assert.deepEqual(watch.dialogs, [], `${harness}: a desktop switch raised a dialog`);
        assert.deepEqual(watch.switches.map((entry) => [entry.id, switchModel(entry)]), [[id, to.id]], `${harness}: the desktop switch did not send exactly one set_model`);
      } finally {
        watch.stop();
      }
    }
  });

  await scenario("S471-04", "a fresh phone composer picks its starting model without a dialog", async () => {
    page = mobilePage;
    await load(`${base}/?cwd=${encodeURIComponent(project)}`);
    await waitForTrigger(PI_FROM.name, "the fresh composer");
    const watch = watchPage();
    try {
      await chooseModel(PI_TO.name);
      await waitForTrigger(PI_TO.name, "the fresh composer after choosing");
      assert.deepEqual(watch.dialogs, [], "the fresh composer's model pick raised a dialog");
      assert.deepEqual(watch.switches, [], "the fresh composer's model pick sent a set_model with no session");
    } finally {
      watch.stop();
    }
    await page.screenshot({ path: join(artifacts, "model-confirm-471-fresh-390.png") });
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  if (page) await page.screenshot({ path: join(artifacts, "model-confirm-471-final.png") }).catch(() => {});
  if (mobileContext) await mobileContext.tracing.stop({ path: join(artifacts, "model-confirm-471-trace.zip") }).catch(() => {});
  await h.close();
  model.closeAllConnections();
  await new Promise((resolve) => model.close(() => resolve()));
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL model-switch-confirm-471: ${failures.length} scenario(s) failed`);
  for (const failure of failures) console.log(`  ${failure}`);
  process.exitCode = 1;
}
