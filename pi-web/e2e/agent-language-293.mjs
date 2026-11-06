// carries: lib/agent-language*, lib/session-agent-language*, lib/agent-language-prompt*
// carries: lib/claude-index.ts, lib/claude-argv.ts, lib/rpc-manager.ts, lib/api-types.ts
// carries: app/api/agent-language/*
// carries: components/AgentLanguagePfStatus*
// carries: lib/subagent-prompt.ts, lib/subagent-skills.ts, lib/subagents.ts, lib/subagent-runtime.ts, lib/i18n/*, lib/__fixtures__/claude-2.1.312/README.md, lib/__fixtures__/claude-2.1.312/language/*
// carries: lib/__fixtures__/claude-stub/*
// carries: docs/agents/claude-sessions.md, docs/agents/tools.md, docs/agents/settings-ui.md
// — Agent language setting: e2e acceptance pass, authored against
// artifacts/spec.md's `## Acceptance criteria` and its Requirement `#### Scenario`
// blocks BEFORE the implementation exists. Every scenario drives surface that
// already exists pre-code: `GET/PUT /api/agent-language` (a not-yet-existing
// route answers 404, an assertion-level red), Settings › General's existing
// dialog, `POST /api/agent/new` and `POST /api/agent/<id>`, the PATH-injected
// claude stub's recorded argv/stdin, a local fake OpenAI-compatible provider
// that records each request's system prompt, `GET /api/sessions`, the claude
// index file, and the three area docs / locale files / real recording. No
// symbol that does not exist today is imported. Each passing scenario prints one
// `PASS S293-nn` marker; the baseline run prints `FAIL` for every kept one.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer as createHttpServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { createCheck, createHarness, createWaitUntil, errorText, setupFailed } from "./harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const distDir = process.env.PI_WEB_DIST_DIR || ".next/e2e/agent-language-293";
const artifacts = join(root, "test-results/e2e");
mkdirSync(artifacts, { recursive: true });
rmSync(join(root, distDir, "dev/lock"), { force: true });

// The contract the spec fixes, authored from the spec alone (never imported from lib/).
const INSTRUCTION = "Use Simplified Chinese (简体中文) for replies, narration and reports. Keep code, code comments, commit messages, files you write, technical terms and identifiers as they are, or as the project's conventions require. Thinking in Chinese is best effort only.";
const STREAM = ["-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--forward-subagent-text"];
// The default permission mode is ask: claude's prompts come to Pi Web over stdio.
const PERMISSION_PROMPTS = ["--permission-prompts", "host", "--permission-prompt-tool", "stdio"];
const PRE_FEATURE = (id, resume) => [...STREAM, "--model", "claude-haiku-5-5", ...PERMISSION_PROMPTS, ...(resume ? ["--resume", id] : ["--session-id", id])];

const CHAT_PROMPT = "CHAT-ONLY-EXACT-293";

const scratch = mkdtempSync(join(tmpdir(), "pi-web-agent-language-293-"));
const home = join(scratch, "home");
const agentDir = join(home, ".pi", "agent");
const project = join(scratch, "project");
const stubDir = join(scratch, "bin");
const stubLog = join(scratch, "claude-stub.jsonl");
const configDir = join(scratch, "claude-config");
for (const dir of [home, agentDir, project, stubDir, configDir]) mkdirSync(dir, { recursive: true });
copyFileSync(join(root, "e2e/claude-stub/claude.mjs"), join(stubDir, "claude"));
chmodSync(join(stubDir, "claude"), 0o755);
writeFileSync(join(project, "AGENTS.md"), CHAT_PROMPT);
writeFileSync(join(agentDir, "settings.json"), JSON.stringify({ defaultProvider: "fake", defaultModel: "fake-model" }));
const sessionDir = join(agentDir, "sessions", "e2e-293");
mkdirSync(sessionDir, { recursive: true });
const oldStamp = "2026-01-01T00:00:00.000Z";
const oldAssistant = (text) => ({
  role: "assistant",
  content: [{ type: "text", text }],
  provider: "fake",
  model: "fake-model",
  api: "openai-completions",
  usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
  stopReason: "stop",
  timestamp: Date.parse(oldStamp),
});
function writeOldPiSession(id, text) {
  writeFileSync(join(sessionDir, `${oldStamp.replaceAll(":", "-")}_${id}.jsonl`), [
    { type: "session", version: 3, id, timestamp: oldStamp, cwd: project },
    { type: "message", id: `${id}-u`, parentId: null, timestamp: oldStamp, message: { role: "user", content: text } },
    { type: "message", id: `${id}-a`, parentId: `${id}-u`, timestamp: oldStamp, message: oldAssistant(`old answer ${id}`) },
  ].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
}
const indexPath = () => join(agentDir, "claude-sessions.json");
function appendClaudeIndex(record) {
  let index = { version: 1, sessions: [] };
  try { index = JSON.parse(readFileSync(indexPath(), "utf8")); } catch { /* fresh index */ }
  index.sessions = [...(index.sessions ?? []), record];
  writeFileSync(indexPath(), `${JSON.stringify(index, null, 2)}\n`);
}

// A local OpenAI-compatible model: it records every request body (the system
// prompt included) and answers at once, so a run settles without a network.
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
  response.write(chunk({ role: "assistant", content: "ok" }));
  response.write(chunk({}, "stop", { usage: { prompt_tokens: 3, completion_tokens: 1, total_tokens: 4 } }));
  response.write("data: [DONE]\n\n");
  response.end();
});
model.listen(0, "127.0.0.1");
await new Promise((resolve) => model.once("listening", resolve));
writeFileSync(join(agentDir, "models.json"), JSON.stringify({
  providers: { fake: { baseUrl: `http://127.0.0.1:${model.address().port}/v1`, api: "openai-completions", apiKey: "fake-key", models: [{ id: "fake-model", name: "Fake Model" }] } },
}));

const configPath = join(agentDir, "pi-web-settings.json");
const stored = () => existsSync(configPath) ? readFileSync(configPath, "utf8") : "";
const setStored = (language) => writeFileSync(configPath, `${JSON.stringify({ version: 1, agentLanguage: language }, null, 2)}\n`);
const removeStored = () => rmSync(configPath, { force: true });
const clearReplies = () => { modelRequests.length = 0; };
const systemOf = (request) => (request.messages ?? []).filter((message) => message.role === "system")
  .map((message) => typeof message.content === "string" ? message.content : (message.content ?? []).map((block) => block.text ?? "").join(""))
  .join("\n\n");
const userOf = (request) => (request.messages ?? []).filter((message) => message.role === "user")
  .map((message) => typeof message.content === "string" ? message.content : (message.content ?? []).map((block) => block.text ?? "").join(""))
  .join("\n");

const h = createHarness();
let browser;
let base;
const contexts = [];
const failures = [];

const scenario = createCheck({ failures, message: errorText, record: (names, text) => `${names.join(" ")}: ${text}` });

const waitFor = createWaitUntil({ every: 200, withLast: true });

// The server uses an isolated HOME/PI_CODING_AGENT_DIR, its own dist dir and a
// non-existent PF_BIN, so this suite never touches the operator's sessions,
// Claude config or pf configuration. The shared
// helper starts `next dev`, or `next start` under E2E_SERVER_MODE=start
// ( build-once mode).

async function api(pathname, init = {}) {
  return fetch(`${base}${pathname}`, {
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
async function putLanguage(language, init = {}) {
  const response = await api("/api/agent-language", { ...init, method: "PUT", body: JSON.stringify({ language }) });
  return { status: response.status, body: await response.json().catch(() => ({})) };
}

async function newPi(message, extra = {}) {
  const created = await postJson("/api/agent/new", { cwd: project, type: "prompt", message, provider: "fake", modelId: "fake-model", ...extra });
  assert.equal(created.status, 200, `POST /api/agent/new (pi) -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const id = created.body.sessionId ?? created.body.data?.sessionId;
  assert.ok(typeof id === "string" && id.length > 0, "the create route returned no pi session id");
  return id;
}
async function newClaude(message) {
  const created = await postJson("/api/agent/new", { cwd: project, type: "prompt", message, harness: "claude", model: "claude-haiku-5-5" });
  assert.equal(created.status, 200, `POST /api/agent/new (claude) -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const id = created.body.sessionId ?? created.body.id;
  assert.ok(typeof id === "string" && id.length > 0, "the create route returned no claude session id");
  return id;
}
async function prompt(id, message) {
  const sent = await postJson(`/api/agent/${encodeURIComponent(id)}`, { type: "prompt", message });
  assert.equal(sent.status, 200, `POST /api/agent/${id} -> ${sent.status} ${JSON.stringify(sent.body).slice(0, 300)}`);
  return sent;
}
async function waitIdle(id) {
  await waitFor(async () => {
    const running = await getJson("/api/agent/running");
    return !(running.runningSessionIds ?? []).includes(id);
  }, 45_000, `session ${id} never became idle`);
}
async function sessionRow(id) {
  const listing = await getJson("/api/sessions");
  return (listing.sessions ?? []).find((session) => session.id === id);
}

function stubRecords() {
  if (!existsSync(stubLog)) return [];
  return readFileSync(stubLog, "utf8").split("\n").filter(Boolean).flatMap((line) => {
    try { return [JSON.parse(line)]; } catch { return []; }
  });
}
const launches = () => stubRecords().filter((entry) => entry.kind === "launch");
async function waitLaunch(id, after = 0, resume = false) {
  return waitFor(() => launches().slice(after).find((entry) => entry.sessionId === id && entry.resumed === resume), 45_000, `no ${resume ? "resume" : "first"} claude launch for ${id}`);
}
const flagValue = (argv, flag) => {
  const at = argv.indexOf(flag);
  return at >= 0 ? argv[at + 1] : undefined;
};

function newContext(locale = "zh-CN") {
  const contextPromise = browser.newContext({
    viewport: { width: 1280, height: 900 },
    locale: locale === "zh-CN" ? "zh-CN" : "en-US",
    baseURL: base,
  }).then(async (context) => {
    await context.addInitScript((value) => {
      try {
        localStorage.setItem("pi-locale", value);
        localStorage.setItem("pi-web:split-view-enabled", "false");
        localStorage.setItem("pi-web:session-age-filter-days", "0");
      } catch {}
    }, locale);
    contexts.push(context);
    return context;
  });
  return contextPromise;
}
async function openSettings(context) {
  const page = await context.newPage();
  page.setDefaultTimeout(ACTION_MS);
  await page.goto(`${base}/?cwd=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded", timeout: COLD_MS });
  const settingsButton = page.getByRole("button", { name: /^(Settings|设置)$/ }).first();
  await settingsButton.waitFor({ state: "visible", timeout: NAV_MS });
  const dialog = page.locator('[role="dialog"][aria-label="Settings"], [role="dialog"][aria-label="设置"]').first();
  // The button is server-rendered before its click handler hydrates; a click in
  // that window is swallowed. Retry on a bounded wait instead of sleeping.
  for (let attempt = 0; attempt < 6; attempt += 1) {
    await settingsButton.click({ timeout: ACTION_MS }).catch(() => {});
    try {
      await dialog.waitFor({ state: "visible", timeout: 20_000 });
      return { page, dialog };
    } catch {
      // not hydrated yet, or the click landed before the handler attached
    }
  }
  await dialog.waitFor({ state: "visible", timeout: NAV_MS });
  return { page, dialog };
}
const agentSection = (dialog) => dialog.locator(".settings-general-section").filter({ hasText: "Agent 使用语言" });
const languageSection = (dialog) => dialog.locator(".settings-general-section").filter({ has: dialog.page().getByRole("heading", { name: /^(语言|Language)$/, exact: true }) });
const selectedValue = async (section) => {
  const selected = section.locator('[role="radio"][aria-checked="true"]');
  assert.equal(await selected.count(), 1, "the Agent language control has no single selected radio");
  return (await selected.innerText()).trim();
};

const interrupt = () => { process.exitCode = 1; };
process.once("SIGINT", interrupt);
process.once("SIGTERM", interrupt);

try {
  ({ base } = await h.startServer({
    name: "agent-language-293",
    logFile: join(scratch, "server.log"),
    env: {
      HOME: home,
      PI_CODING_AGENT_DIR: agentDir,
      PI_WEB_ALLOWED_ROOT_PREFIXES: scratch,
      PI_WEB_DIST_DIR: distDir,
      PI_WEB_IDLE_TIMEOUT_MS: "5000",
      CLAUDE_CONFIG_DIR: configDir,
      PATH: `${stubDir}:${process.env.PATH ?? ""}`,
      // The save also reaches pf: a non-existent pf keeps the
      // suite off the machine's pf (Pi Web's own write only).
      PF_BIN: join(scratch, "no-such-pf"),
      PI_E2E_CLAUDE_STUB_LOG: stubLog,
    },
  }));
  browser = await h.launchBrowser(() => chromium.launch());

  // S293-01 — acceptance: Settings › General offers the Agent language item as its
  // own control beside the UI language section, default English, and neither
  // choice rewrites the other.
  await scenario("S293-01", "Settings General offers an independent Agent language item, default English", async () => {
    removeStored();
    const context = await newContext("zh-CN");
    const { page, dialog } = await openSettings(context);
    const section = agentSection(dialog);
    await section.waitFor({ state: "visible", timeout: 30_000 });
    assert.ok(await languageSection(dialog).isVisible(), "the existing UI language section is not visible beside the new item");
    assert.equal(await section.locator('[role="radio"]').count(), 2, "the Agent language item does not offer exactly two options");
    assert.match(await selectedValue(section), /English/i, "the default Agent language is not English");
    assert.ok(!(await page.evaluate(() => localStorage.getItem("pi-locale")))?.includes("agent"), "the Agent language leaked into pi-locale");
    // Changing the Agent language must not touch the browser UI locale.
    // The save is the click's own PUT: await that response and assert it, so a
    // refused or failed save reports its status and body (
    // verification round 2 #1: a bare GET poll could not tell a slow first save
    // from a refused one). The first save is the route's first PUT, so it is
    // bounded like a cold route (COLD_MS); the stored value is then read back.
    const putResponse = page.waitForResponse((response) => response.url().endsWith("/api/agent-language") && response.request().method() === "PUT", { timeout: COLD_MS });
    await section.locator('[role="radio"]').filter({ hasText: /中文/ }).click({ timeout: ACTION_MS });
    const put = await putResponse;
    const putBody = await put.text();
    assert.equal(put.status(), 200, `the Agent language save was refused: PUT /api/agent-language -> ${put.status()} ${putBody.slice(0, 200)}`);
    assert.equal(JSON.parse(putBody).language, "zh-CN", `the save did not return the selected value: ${putBody.slice(0, 200)}`);
    await waitFor(async () => (await getJson("/api/agent-language")).language === "zh-CN", 10_000, "the selected Agent language was not saved");
    assert.equal(await page.evaluate(() => localStorage.getItem("pi-locale")), "zh-CN", "saving the Agent language changed the UI locale");
    // Changing the UI language must not touch the stored Agent language.
    await languageSection(dialog).locator('[role="radio"]').filter({ hasText: /English/ }).click({ timeout: ACTION_MS });
    await waitFor(() => page.evaluate(() => localStorage.getItem("pi-locale")).then((value) => value === "en"), 10_000, "the UI language did not switch to English");
    assert.equal((await getJson("/api/agent-language")).language, "zh-CN", "changing the UI language rewrote the Agent language");
    await page.close();
  });

  // S293-02 — acceptance: the value lives on the server (a second, independent
  // browser sees it) and only an authenticated pi-web API call may write it.
  await scenario("S293-02", "The saved Agent language is shared across devices and a write outside the authenticated API is refused", async () => {
    const saved = await putLanguage("zh-CN");
    assert.equal(saved.status, 200, `PUT /api/agent-language -> ${saved.status} ${JSON.stringify(saved.body).slice(0, 200)}`);
    assert.equal(saved.body.language, "zh-CN", "the save did not return the stored value");
    assert.equal((await getJson("/api/agent-language")).language, "zh-CN", "GET /api/agent-language does not read the saved value");
    assert.match(stored(), /"agentLanguage"\s*:\s*"zh-CN"/, "the value was not written to pi-web's server-side configuration");
    // Device B: a fresh browser context with its own empty localStorage.
    const deviceB = await newContext("zh-CN");
    const { page, dialog } = await openSettings(deviceB);
    const section = agentSection(dialog);
    await section.waitFor({ state: "visible", timeout: 30_000 });
    assert.match(await selectedValue(section), /中文/, "a second device does not show the server-saved Agent language");
    await page.close();
    // A write outside pi-web's authenticated API surface is refused before any
    // write: a cross-site caller is not a pi-web page (the proxy's API trust gate).
    const untrusted = await fetch(`${base}/api/agent-language`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", Origin: "https://evil.example", "Sec-Fetch-Site": "cross-site" },
      body: JSON.stringify({ language: "en" }),
      signal: AbortSignal.timeout(10_000),
    });
    assert.equal(untrusted.status, 403, `a write outside the authenticated API surface was not refused: ${untrusted.status}`);
    assert.equal((await getJson("/api/agent-language")).language, "zh-CN", "an untrusted request changed the stored value");
  });

  // S293-03 — acceptance: missing, malformed or unknown server config reads as
  // English and blocks no session.
  await scenario("S293-03", "Missing, malformed or unknown stored Agent language reads as English without blocking a session", async () => {
    removeStored();
    assert.equal((await getJson("/api/agent-language")).language, "en", "an absent config does not read as English");
    writeFileSync(configPath, "{ this is not json\n");
    assert.equal((await getJson("/api/agent-language")).language, "en", "a malformed config does not read as English");
    setStored("klingon");
    assert.equal((await getJson("/api/agent-language")).language, "en", "an unknown stored value does not read as English");
    writeFileSync(configPath, "{ this is not json\n");
    clearReplies();
    const id = await newPi("E2E 293 tolerant config");
    assert.ok(await waitFor(() => modelRequests.length >= 1, 30_000, "a malformed config blocked the session before its first model request"), "no model request");
    assert.ok(!systemOf(modelRequests[0]).includes(INSTRUCTION), "a malformed config injected a language instruction");
    assert.ok(await sessionRow(id), "the session created under a malformed config is missing from the list");
  });

  // S293-04 — acceptance: the control shows the saved value, and a failed save
  // shows a visible error without claiming the new value.
  await scenario("S293-04", "The settings control shows the saved value and a failed save shows a visible error", async () => {
    setStored("en");
    const context = await newContext("zh-CN");
    const { page, dialog } = await openSettings(context);
    const section = agentSection(dialog);
    await section.waitFor({ state: "visible", timeout: 30_000 });
    assert.match(await selectedValue(section), /English/i, "the control does not show the saved English value");
    await page.route("**/api/agent-language", async (route) => {
      if (route.request().method() === "PUT") await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "save refused" }) });
      else await route.continue();
    });
    await section.locator('[role="radio"]').filter({ hasText: /中文/ }).click({ timeout: ACTION_MS });
    await section.locator('[role="alert"]').waitFor({ state: "visible", timeout: 10_000 });
    assert.ok((await section.locator('[role="alert"]').innerText()).trim().length > 0, "the failed save shows an empty error line");
    assert.match(await selectedValue(section), /English/i, "a failed save claims the new value was saved");
    assert.equal((await getJson("/api/agent-language")).language, "en", "a failed save changed the stored value");
    await page.close();
  });

  // S293-05 — acceptance: a Chinese claude session's first launch and every
  // resume carry --append-system-prompt with the fixed text, the capture survives
  // a setting change, and an English claude session keeps the pre-feature argv.
  await scenario("S293-05", "A Chinese claude session's launch and resume argv carry the fixed instruction, byte-exact", async () => {
    setStored("zh-CN");
    clearReplies();
    const before = launches().length;
    const zh = await newClaude("E2E 293 claude chinese one");
    const first = await waitLaunch(zh, before, false);
    assert.equal(flagValue(first.argv, "--append-system-prompt"), INSTRUCTION, `the first claude launch does not carry the fixed instruction byte-exact: ${first.argv.join(" ")}`);
    assert.deepEqual(first.argv, [...STREAM, "--model", "claude-haiku-5-5", "--append-system-prompt", INSTRUCTION, ...PERMISSION_PROMPTS, "--session-id", zh], "the Chinese first-launch argv differs from the specified shape");
    for (const boundary of [/replies, narration and reports/, /code, code comments, commit messages/, /technical terms and identifiers/, /Thinking in Chinese is best effort only/]) {
      assert.match(INSTRUCTION, boundary, `the instruction does not state the required boundary: ${boundary}`);
    }
    assert.ok(!String((await sessionRow(zh))?.firstMessage ?? "").includes(INSTRUCTION), "the instruction entered the claude session title");
    // Switch the global setting; the existing session keeps its captured language.
    setStored("en");
    await waitIdle(zh);
    // The live child takes a prompt as it is; wait for its real idle reap so the next prompt resumes (answer code-293-s05-resume-wait).
    await waitFor(() => stubRecords().some((entry) => entry.kind === "exit" && entry.pid === first.pid), 30_000, "the first claude child never exited");
    const beforeResume = launches().length;
    await prompt(zh, "E2E 293 claude chinese two");
    const resumed = await waitLaunch(zh, beforeResume, true);
    assert.equal(flagValue(resumed.argv, "--append-system-prompt"), INSTRUCTION, "the resumed Chinese child lost the instruction");
    // A new session under English has exactly the pre-feature argv.
    const beforeEnglish = launches().length;
    const en = await newClaude("E2E 293 claude english one");
    const english = await waitLaunch(en, beforeEnglish, false);
    assert.deepEqual(english.argv, PRE_FEATURE(en, false), `an English claude launch is not byte-identical to the pre-feature argv: ${english.argv.join(" ")}`);
    assert.ok(!english.argv.some((arg) => /language/i.test(arg)), "an English claude launch carries a language flag");
    // A session created before this feature has no language record and stays English.
    const oldId = "e2e-293-pre-feature-claude";
    appendClaudeIndex({ id: oldId, cwd: project, model: "claude-haiku-5-5", created: "2026-01-01T00:00:00.000Z", modified: "2026-01-01T00:00:00.000Z", firstMessage: "old claude", usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0 } });
    setStored("zh-CN");
    const beforeOld = launches().length;
    await prompt(oldId, "E2E 293 old claude");
    const oldLaunch = await waitLaunch(oldId, beforeOld, false);
    assert.deepEqual(oldLaunch.argv, PRE_FEATURE(oldId, false), `a pre-feature claude session adopted the new Chinese setting: ${oldLaunch.argv.join(" ")}`);
  });

  // S293-06 — acceptance: a Chinese pi session's provider system prompt carries
  // the fixed instruction (not the user message or the title), it survives a
  // setting change, and a later English session sends none.
  await scenario("S293-06", "A Chinese pi session's provider system prompt carries the fixed instruction outside the user message and title", async () => {
    setStored("zh-CN");
    clearReplies();
    const zh = await newPi("E2E 293 pi chinese one");
    assert.ok(await waitFor(() => modelRequests.length >= 1, 30_000, "the Chinese pi session made no model request"), "no request");
    const first = modelRequests[0];
    assert.ok(systemOf(first).includes(INSTRUCTION), "the Chinese pi provider system prompt lacks the fixed instruction");
    assert.ok(!userOf(first).includes(INSTRUCTION), "the instruction was prefixed to the user's message");
    assert.ok(!String((await sessionRow(zh))?.firstMessage ?? "").includes(INSTRUCTION), "the instruction entered the pi session title");
    setStored("en");
    await waitIdle(zh);
    // The live wrapper takes a prompt as it is; wait for its idle release so the next prompt reopens the file (answer code-293-s06-resume-wait).
    await waitFor(async () => !((await getJson("/api/agent/debug")).sessions ?? []).some((s) => s.sessionId === zh && s.state !== "none"), 30_000, `the idle pi wrapper of ${zh} was never released`);
    clearReplies();
    await prompt(zh, "E2E 293 pi chinese two");
    assert.ok(await waitFor(() => modelRequests.length >= 1, 30_000, "the resumed Chinese pi session made no model request"), "no request");
    assert.ok(systemOf(modelRequests[0]).includes(INSTRUCTION), "a setting change rewrote an existing pi session's language");
    clearReplies();
    await newPi("E2E 293 pi english one");
    assert.ok(await waitFor(() => modelRequests.length >= 1, 30_000, "the English pi session made no model request"), "no request");
    assert.ok(!systemOf(modelRequests[0]).includes(INSTRUCTION), "a new English pi session was sent the Chinese instruction");
    // A pi session created before this feature has no language entry and stays English.
    const oldId = "e2e-293-pre-feature-pi";
    writeOldPiSession(oldId, "old pi prompt");
    setStored("zh-CN");
    clearReplies();
    await prompt(oldId, "E2E 293 old pi");
    assert.ok(await waitFor(() => modelRequests.length >= 1, 30_000, "the pre-feature pi session made no model request"), "no request");
    assert.ok(!systemOf(modelRequests[0]).includes(INSTRUCTION), "a pre-feature pi session adopted the new Chinese setting");
  });

  // S293-08 — acceptance: Chat only's exact prompt is followed by the instruction
  // for Chinese, and English is the exact prompt byte-for-byte.
  await scenario("S293-08", "A Chat only session gets the exact prompt plus the instruction in Chinese and the exact prompt in English", async () => {
    setStored("zh-CN");
    clearReplies();
    await newPi("E2E 293 chat chinese", { toolNames: [] });
    assert.ok(await waitFor(() => modelRequests.length >= 1, 30_000, "the Chinese Chat only session made no model request"), "no request");
    const zhSystem = systemOf(modelRequests[0]);
    assert.ok(zhSystem.startsWith(CHAT_PROMPT), `the Chat only prompt no longer starts with the exact text: ${zhSystem.slice(0, 200)}`);
    assert.ok(zhSystem.endsWith(INSTRUCTION), "the Chat only Chinese prompt does not end with the fixed instruction");
    assert.equal(zhSystem.slice(CHAT_PROMPT.length, -INSTRUCTION.length), "\n\n", "the instruction was not appended after the exact text with one blank line");
    setStored("en");
    clearReplies();
    await newPi("E2E 293 chat english", { toolNames: [] });
    assert.ok(await waitFor(() => modelRequests.length >= 1, 30_000, "the English Chat only session made no model request"), "no request");
    assert.equal(systemOf(modelRequests[0]), CHAT_PROMPT, "the English Chat only prompt is not byte-identical to the exact prompt");
  });

  // S293-09 — acceptance: the area docs and all three locale files ship the new
  // Agent language surface with the code.
  await scenario("S293-09", "The area docs and all three locale files ship the new Agent language surface", async () => {
    const claudeDoc = readFileSync(join(root, "docs/agents/claude-sessions.md"), "utf8");
    assert.match(claudeDoc, /--append-system-prompt/, "docs/agents/claude-sessions.md does not list --append-system-prompt");
    const toolsDoc = readFileSync(join(root, "docs/agents/tools.md"), "utf8");
    assert.match(toolsDoc, /Chat only[\s\S]{0,800}(instruction|Agent language)/i, "docs/agents/tools.md does not record the Chat only append behavior");
    const settingsDoc = readFileSync(join(root, "docs/agents/settings-ui.md"), "utf8");
    assert.match(settingsDoc, /Agent (使用语言|使用語言|language)[\s\S]{0,800}(pi-web-settings\.json|\/api\/agent-language)/, "docs/agents/settings-ui.md does not document the new General item");
    for (const locale of ["en", "zh-CN", "zh-TW"]) {
      const messages = readFileSync(join(root, `lib/i18n/messages/${locale}.ts`), "utf8");
      for (const key of ["agentLanguage", "agentLanguageDescription", "agentLanguageEnglish", "agentLanguageChinese", "agentLanguageSaveError", "agentLanguageLoadFailed"]) {
        assert.match(messages, new RegExp(`"settings\\.${key}"\\s*:`), `${locale} lacks settings.${key}`);
      }
    }
    const status = execFileSync("git", ["status", "--porcelain", "--", "AGENTS.md"], { cwd: root, encoding: "utf8" }).trim();
    assert.equal(status, "", `the e2e run rewrote AGENTS.md's generated block: ${status}`);
  });

  // S293-10 — acceptance: one real claude 2.1.312 recording with the Chinese
  // instruction exists, follows the fixture conventions and shows a Chinese reply.
  await scenario("S293-10", "One real claude 2.1.312 recording with the Chinese instruction contains a Simplified Chinese reply", async () => {
    const dir = join(root, "lib/__fixtures__/claude-2.1.312/language");
    for (const file of ["stdin.jsonl", "stream.jsonl", "transcript.jsonl"]) assert.ok(existsSync(join(dir, file)), `the language recording lacks ${file}`);
    const readme = readFileSync(join(root, "lib/__fixtures__/claude-2.1.312/README.md"), "utf8");
    assert.match(readme, /language\//, "the fixture README does not list the language recording");
    assert.match(readme, /--append-system-prompt/, "the fixture README does not state the recorded Chinese argv");
    const stream = readFileSync(join(dir, "stream.jsonl"), "utf8");
    const transcript = readFileSync(join(dir, "transcript.jsonl"), "utf8");
    assert.match(stream, /[\u4e00-\u9fff]/, "the recorded stream has no Simplified Chinese reply");
    assert.match(transcript, /[\u4e00-\u9fff]/, "the recorded transcript has no Simplified Chinese reply");
    assert.ok(!/sk-[A-Za-z0-9]{20,}|ANTHROPIC_API_KEY=|Bearer [A-Za-z0-9._-]{20,}/.test(stream + transcript + readFileSync(join(dir, "stdin.jsonl"), "utf8")), "the language recording leaks a credential");
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  for (const context of contexts) await context.close().catch(() => {});
  await browser?.close().catch(() => {});
  await h.stop().catch(() => {});
  model.closeAllConnections?.();
  await new Promise((resolve) => model.close(resolve));
  for (const entry of launches()) {
    try { process.kill(entry.pid, "SIGKILL"); } catch { /* already gone */ }
  }
  rmSync(scratch, { recursive: true, force: true });
  process.off("SIGINT", interrupt);
  process.off("SIGTERM", interrupt);
}

if (failures.length) console.log(`FAIL agent-language-293: ${failures.length} scenario(s) failed`);
process.exit(failures.length ? 1 : 0);