// carries: lib/claude-permission*, app/api/claude-permissions/*, components/ClaudePermission*, lib/web-push*
// AC1–AC5, AC4b — a claude session's permission prompts in Pi Web: browser e2e.
//
// The child is the PATH-injected e2e/claude-stub/claude.mjs. A `perm: <command>` line asks
// for claude's Bash tool and an `ask: ` line for AskUserQuestion, in claude 2.1.322's
// permission shapes (synthetic): launched in ask mode (`--permission-prompt-tool stdio`)
// the stub sends `can_use_tool` and waits for Pi Web's `control_response`; launched in deny
// mode (`--permission-prompts none`) it sends `permission_denied` instead. The stub logs
// every launch's argv, every stdin line and each permission request it sent, so a scenario
// reads the exact answer Pi Web wrote back.
//
// Both languages are Chinese: the page's (`pi-locale` zh-CN: the sidebar marker, Settings)
// and the Agent language (`pi-web-settings.json` zh-CN before the server starts: the
// dialogs' and the notice's text, which follow the session's language). No
// `claude-permissions.json` exists at the start, so the defaults are in force (ask, push on).
// The idle timeout is the product default; only the suite ends a child.
//
// Not declared (each already has 3 suites, lib/e2e-carries.test.mjs): lib/claude-session.ts,
// lib/claude-adapter.ts, lib/claude-argv*, e2e/claude-stub/*, components/SessionSidebar*,
// components/SettingsPanel*, app/api/claude/*; their changes are covered by those suites
// and the unit tests.
//
// Each scenario prints one `PASS S442-E2E-nn` marker; the suite ends with
// `PASS claude-permission-442` only when every scenario passed. Every wait polls its
// condition to a bounded deadline.
import assert from "node:assert/strict";
import { rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, claudeEnv, claudeScratch, createApi, createCheck, createHarness, createWaitUntil, errorText, pidAlive, readJsonLines, seedStorage, setupFailed } from "./harness.mjs";

assertNoDevServer();
const kit = claudeScratch("pi-web-claude-permission-442-");
const { artifacts, scratch, project, stubLog, agentDir } = kit;
writeFileSync(join(agentDir, "pi-web-settings.json"), `${JSON.stringify({ version: 1, agentLanguage: "zh-CN" }, null, 2)}\n`);

// The page's words (zh-CN) and the dialogs' (the session's zh-CN Agent language).
const UI = {
  settings: "设置",
  general: "常规",
  send: "发送",
  showSidebar: "显示侧边栏",
  processDetails: "处理详情",
  mode: "授权方式",
  ask: "弹窗询问",
  deny: "静默拒绝",
  session: "本会话",
  follow: "跟随默认",
  push: "有待授权时推送提醒",
  awaiting: "等待你的授权",
};
const DIALOG = { allowOnce: "允许一次", allowSession: "本会话内总是允许", deny: "拒绝", wants: "claude 请求使用 Bash", denied: "claude 拒绝了 Bash" };
const ASK = { question: "你喜欢哪种颜色？", pick: "蓝色 — 冷色", label: "蓝色", options: ["红色 — 暖色", "蓝色 — 冷色", "其他…"] };
const STDIO = ["--permission-prompts", "host", "--permission-prompt-tool", "stdio"];
const NONE = ["--permission-prompts", "none"];

const h = createHarness();
let browser;
let context;
let page;
let base;
let firstLoad = true;
const failures = [];

const scenario = createCheck({ failures, message: errorText, record: (names, text) => `${names.join(" ")}: ${text}` });
const waitFor = createWaitUntil({ every: 200, withLast: true });
const { apiFetch, getJson } = createApi(() => base);
const stubRecords = () => readJsonLines(stubLog);
const launchesOf = (id) => stubRecords().filter((record) => record.kind === "launch" && record.sessionId === id);
const hasRun = (argv, run) => argv.some((_, at) => run.every((word, offset) => argv[at + offset] === word));
/** The stdin `control_response` lines a child received, parsed. */
const controlResponses = () => stubRecords()
  .filter((record) => record.kind === "stdin")
  .flatMap((record) => {
    try {
      const line = JSON.parse(record.line);
      return line.type === "control_response" ? [line.response] : [];
    } catch {
      return [];
    }
  });
const permissionRequests = (id) => stubRecords().filter((record) => record.kind === "permission_request" && record.sessionId === id);

async function isRunning(id) {
  const response = await apiFetch(`/api/agent/${encodeURIComponent(id)}`);
  if (response.status !== 200) return false;
  const body = await response.json();
  return body.running === true && body.state?.isStreaming === true;
}
const waitIdle = (id, where) => waitFor(async () => !(await isRunning(id)), 60_000, `${where}: the session is still running`);
async function createEmptyClaudeSession() {
  const response = await apiFetch("/api/agent/new", { method: "POST", body: JSON.stringify({ cwd: project, type: "ensure_session", harness: "claude", model: "claude-haiku-5-5" }) });
  const body = await response.json().catch(() => ({}));
  assert.equal(response.status, 200, `POST /api/agent/new -> ${response.status} ${JSON.stringify(body).slice(0, 300)}`);
  const id = body.sessionId ?? body.id;
  assert.ok(typeof id === "string" && id, "no claude session id");
  return id;
}

const textarea = () => page.locator(".chat-input-textarea").first();
async function openSession(id) {
  await page.goto(`${base}/?session=${encodeURIComponent(id)}`, { waitUntil: "domcontentloaded", timeout: firstLoad ? COLD_MS : NAV_MS });
  firstLoad = false;
  await textarea().waitFor({ state: "visible", timeout: NAV_MS });
}
async function send(text) {
  await textarea().fill(text);
  await page.getByRole("button", { name: UI.send, exact: true }).first().click({ timeout: ACTION_MS });
}
const replyShown = (text) => async () => (await page.locator(".markdown-body").allInnerTexts()).some((shown) => shown.includes(text));

/** The open permission / question dialog whose title holds `text`. */
const dialogWith = (text) => page.locator('[role="dialog"]:not([aria-modal="true"])').filter({ hasText: text });
async function dialogOptions(dialog) {
  return dialog.locator('[role="button"][aria-label]').evaluateAll((nodes) => nodes.map((node) => node.getAttribute("aria-label")));
}
async function choose(dialog, option) {
  await dialog.locator(`[role="button"][aria-label="${option}"]`).first().click({ timeout: ACTION_MS });
  await waitFor(async () => (await dialog.count()) === 0, 30_000, `the dialog stayed open after "${option}"`);
}

const awaitingMarker = () => page.locator(`[aria-label="${UI.awaiting}"]`);
async function showSidebar() {
  const button = page.getByRole("button", { name: UI.showSidebar, exact: true });
  if (await button.isVisible().catch(() => false)) await button.click({ timeout: ACTION_MS });
}

const settingsDialog = () => page.locator(`[role="dialog"][aria-label="${UI.settings}"]`).first();
const modeGroup = () => settingsDialog().getByRole("radiogroup", { name: UI.mode, exact: true });
const sessionGroup = () => settingsDialog().getByRole("radiogroup", { name: UI.session, exact: true });
const radio = (group, label) => group.getByRole("radio", { name: label });
const pushSwitch = () => settingsDialog().getByRole("switch", { name: UI.push, exact: true });
const checked = async (locator) => (await locator.getAttribute("aria-checked")) === "true";

/** Opens Settings › General and waits for the claude permission block. */
async function openSettings() {
  const button = page.getByRole("button", { name: UI.settings, exact: true }).first();
  await button.waitFor({ state: "visible", timeout: NAV_MS });
  // A click before hydration is swallowed: retry on a bounded wait instead of sleeping.
  await waitFor(async () => {
    if (await settingsDialog().isVisible().catch(() => false)) return true;
    await button.click({ timeout: ACTION_MS }).catch(() => {});
    return settingsDialog().isVisible().catch(() => false);
  }, NAV_MS, "Settings never opened");
  await settingsDialog().locator(".settings-section-tabs").getByRole("button", { name: UI.general }).first().click({ timeout: ACTION_MS }).catch(() => {});
  await waitFor(async () => (await modeGroup().count()) === 1 && modeGroup().isVisible(), 30_000, `Settings › General shows no "${UI.mode}" choice`);
}
async function closeSettings() {
  await settingsDialog().locator(".settings-dialog-close").click({ timeout: ACTION_MS });
  await waitFor(async () => (await settingsDialog().count()) === 0, 30_000, "Settings stayed open");
}
const permissionSettings = (id) => getJson(`/api/claude-permissions${id ? `?session=${encodeURIComponent(id)}` : ""}`);

/** The tool card whose header names `tool` and previews `preview`: its name's colour. */
async function toolCardTone(tool, preview) {
  return page.evaluate(([name, text]) => {
    for (const button of document.querySelectorAll("button[aria-expanded]")) {
      const spans = [...button.querySelectorAll(":scope > span")];
      if (spans[0]?.textContent?.trim() === name && (spans[1]?.textContent ?? "").includes(text)) {
        return getComputedStyle(spans[0]).color.replace(/\s+/g, "");
      }
    }
    return null;
  }, [tool, preview]);
}
async function unfoldProcessDetails() {
  const folded = page.locator(`button[aria-expanded="false"][aria-label^="${UI.processDetails}"]`);
  for (let i = 0; i < 10 && (await folded.count()); i++) await folded.first().click({ timeout: ACTION_MS }).catch(() => {});
}

h.trapInterrupt();

let sessionA;
try {
  ({ base } = await h.startServer({
    name: "claude-permission-442",
    logFile: join(artifacts, "claude-permission-442-server.log"),
    // No pf on the server's PATH lookups: the Agent language read stays local.
    env: claudeEnv(kit, { PF_BIN: join(scratch, "no-pf") }),
  }));

  const { chromium } = await import("playwright");
  browser = await h.launchBrowser(() => chromium.launch());
  context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "zh-CN", baseURL: base });
  await seedStorage(context, { "pi-web:split-view-enabled": "false", "pi-web:session-age-filter-days": "0", "pi-locale": "zh-CN" });
  page = await context.newPage();
  page.setDefaultTimeout(ACTION_MS);

  sessionA = await createEmptyClaudeSession();

  // S442-E2E-01 — AC1/AC3/AC5: ask is the default; a Bash call opens the dialog with the
  // tool, its command and the three choices; the claude row reads 「等待你的授权」; a reload
  // keeps the dialog; 「允许一次」 reaches claude as allow under its request id and the turn
  // goes on, the marker gone.
  await scenario("S442-E2E-01", "a claude permission prompt is a dialog, survives a reload and allow once reaches claude", async () => {
    await openSession(sessionA);
    await openSettings();
    assert.ok(await checked(radio(modeGroup(), UI.ask)), `the default "${UI.mode}" is not "${UI.ask}"`);
    await waitFor(async () => (await sessionGroup().count()) === 1, 30_000, `a claude session's Settings shows no "${UI.session}" choice`);
    assert.ok(await checked(radio(sessionGroup(), UI.follow)), `a fresh claude session's override is not "${UI.follow}"`);
    await closeSettings();

    await send("perm: ls -la");
    const dialog = dialogWith("ls -la");
    await waitFor(async () => (await dialog.count()) === 1, 60_000, "no permission dialog naming `ls -la` opened");
    const title = await dialog.getAttribute("aria-label");
    assert.ok(title?.includes(DIALOG.wants) && title.includes("ls -la"), `the dialog's title does not name Bash and the command: ${JSON.stringify(title)}`);
    assert.deepEqual(await dialogOptions(dialog), [DIALOG.allowOnce, DIALOG.allowSession, DIALOG.deny], "the dialog's choices");
    const [request] = permissionRequests(sessionA);
    assert.ok(request, "the stub sent no can_use_tool request");

    await showSidebar();
    await waitFor(async () => (await awaitingMarker().count()) > 0, 30_000, `the claude row never showed the "${UI.awaiting}" marker`);

    await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
    await textarea().waitFor({ state: "visible", timeout: NAV_MS });
    await waitFor(async () => (await dialog.count()) === 1, 60_000, "the pending dialog was not shown again after a reload");

    await choose(dialog, DIALOG.allowOnce);
    const answer = await waitFor(() => controlResponses().find((response) => response.request_id === request.requestId), 30_000, `no control_response for ${request.requestId} reached claude`);
    assert.equal(answer.subtype, "success");
    assert.equal(answer.response?.behavior, "allow", `the answer is not allow: ${JSON.stringify(answer)}`);
    assert.equal(answer.response?.decisionClassification, "user_temporary");
    await waitFor(replyShown("E2E allowed: perm: ls -la"), 60_000, "the allowed turn's reply never showed");
    await showSidebar();
    await waitFor(async () => (await awaitingMarker().count()) === 0, 30_000, `the "${UI.awaiting}" marker stayed after the answer`);
  });

  // S442-E2E-02 — AC1/AC5: 「拒绝」 reaches claude as deny; the tool card shows the error.
  await scenario("S442-E2E-02", "deny reaches claude and the tool card shows the error", async () => {
    await openSession(sessionA);
    await waitIdle(sessionA, "before the deny turn");
    const before = permissionRequests(sessionA).length;
    await send("perm: rm x");
    const dialog = dialogWith("rm x");
    await waitFor(async () => (await dialog.count()) === 1, 60_000, "no permission dialog naming `rm x` opened");
    const request = await waitFor(() => permissionRequests(sessionA)[before], 30_000, "the stub logged no second can_use_tool request");
    await choose(dialog, DIALOG.deny);
    const answer = await waitFor(() => controlResponses().find((response) => response.request_id === request.requestId), 30_000, `no control_response for ${request.requestId} reached claude`);
    assert.equal(answer.response?.behavior, "deny", `the answer is not deny: ${JSON.stringify(answer)}`);
    assert.equal(answer.response?.decisionClassification, "user_reject");
    await waitFor(replyShown("E2E denied: perm: rm x"), 60_000, "the denied turn's reply never showed");
    await waitFor(async () => {
      await unfoldProcessDetails();
      return (await toolCardTone("Bash", "rm x")) === "rgb(248,113,113)";
    }, 30_000, "the denied Bash call's card is not shown as an error");
  });

  // S442-E2E-03 — AC2: AskUserQuestion is a select of claude's options (plus 其他…); the
  // pick reaches claude as updatedInput.answers keyed by the question.
  await scenario("S442-E2E-03", "AskUserQuestion is answered from Pi Web", async () => {
    await openSession(sessionA);
    await waitIdle(sessionA, "before the question turn");
    const before = permissionRequests(sessionA).length;
    await send("ask: 颜色");
    const dialog = dialogWith(ASK.question);
    await waitFor(async () => (await dialog.count()) === 1, 60_000, "no question dialog opened");
    assert.deepEqual(await dialogOptions(dialog), ASK.options, "the question dialog's choices");
    const request = await waitFor(() => permissionRequests(sessionA)[before], 30_000, "the stub logged no AskUserQuestion request");
    await choose(dialog, ASK.pick);
    const answer = await waitFor(() => controlResponses().find((response) => response.request_id === request.requestId), 30_000, `no control_response for ${request.requestId} reached claude`);
    assert.equal(answer.response?.behavior, "allow");
    assert.deepEqual(answer.response?.updatedInput?.answers, { [ASK.question]: ASK.label }, "the answers claude received");
    await waitFor(replyShown("E2E allowed: ask: 颜色"), 60_000, "the answered question's reply never showed");
  });

  // S442-E2E-04 — AC4/AC4b: 「静默拒绝」 as the default leaves the live child's argv; a new
  // session launches with --permission-prompts none and claude's denial is a warning notice,
  // no dialog; the session's 「弹窗询问」 override restarts it with --resume and stdio; the push
  // switch's off survives a reload.
  await scenario("S442-E2E-04", "silent deny, the session override and the push switch", async () => {
    await openSession(sessionA);
    await waitIdle(sessionA, "before the settings change");
    const liveA = launchesOf(sessionA).at(-1);
    assert.ok(liveA && hasRun(liveA.argv, STDIO), `session A's live child was not launched in ask mode: ${JSON.stringify(liveA?.argv)}`);
    const launchesA = launchesOf(sessionA).length;

    await openSettings();
    await radio(modeGroup(), UI.deny).click({ timeout: ACTION_MS });
    await waitFor(async () => checked(radio(modeGroup(), UI.deny)), 30_000, `"${UI.deny}" was not shown selected`);
    await waitFor(async () => (await permissionSettings()).mode === "deny", 30_000, "the global mode was not stored as deny");
    await closeSettings();
    assert.equal(launchesOf(sessionA).length, launchesA, "changing the default relaunched the live child");
    assert.ok(pidAlive(liveA.pid), "changing the default ended the live child");

    const sessionB = await createEmptyClaudeSession();
    await openSession(sessionB);
    await send("perm: ls");
    const launchB = await waitFor(() => launchesOf(sessionB).at(-1), 60_000, "session B's child never launched");
    assert.ok(hasRun(launchB.argv, NONE) && !launchB.argv.includes("--permission-prompt-tool"), `session B's argv is not deny mode: ${JSON.stringify(launchB.argv)}`);
    await waitFor(async () => (await page.getByText(DIALOG.denied).count()) > 0, 60_000, `no "${DIALOG.denied}…" notice showed`);
    await waitFor(replyShown("E2E denied: perm: ls"), 60_000, "the denied turn's reply never showed");
    assert.equal(permissionRequests(sessionB).length, 0, "a deny-mode child asked Pi Web");
    assert.equal(await dialogWith("ls").count(), 0, "a deny-mode session opened a permission dialog");
    await waitIdle(sessionB, "before the override");

    const launchesB = launchesOf(sessionB).length;
    await openSettings();
    await waitFor(async () => (await sessionGroup().count()) === 1, 30_000, `session B's Settings shows no "${UI.session}" choice`);
    await radio(sessionGroup(), UI.ask).click({ timeout: ACTION_MS });
    await waitFor(async () => checked(radio(sessionGroup(), UI.ask)), 30_000, `the session's "${UI.ask}" was not shown selected`);
    await waitFor(async () => (await permissionSettings(sessionB)).session?.override === "ask", 30_000, "the session override was not stored");
    await closeSettings();
    await send("perm: pwd");
    const resumed = await waitFor(() => launchesOf(sessionB).slice(launchesB).find((launch) => launch.resumed), 60_000, "session B was not restarted with --resume after the override");
    assert.ok(hasRun(resumed.argv, ["--resume", sessionB]) && hasRun(resumed.argv, STDIO), `the restarted argv is not ask mode: ${JSON.stringify(resumed.argv)}`);
    const dialog = dialogWith("pwd");
    await waitFor(async () => (await dialog.count()) === 1, 60_000, "the overridden session opened no permission dialog");
    await choose(dialog, DIALOG.allowOnce);
    await waitFor(replyShown("E2E allowed: perm: pwd"), 60_000, "the overridden session's allowed reply never showed");

    await openSettings();
    assert.ok(await checked(pushSwitch()), `"${UI.push}" is not on by default`);
    await pushSwitch().click({ timeout: ACTION_MS });
    await waitFor(async () => !(await checked(pushSwitch())), 30_000, `"${UI.push}" did not turn off`);
    await waitFor(async () => (await permissionSettings()).push === false, 30_000, "the push switch was not stored off");
    await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
    await textarea().waitFor({ state: "visible", timeout: NAV_MS });
    await openSettings();
    await waitFor(async () => (await pushSwitch().count()) === 1 && !(await checked(pushSwitch())), 30_000, `"${UI.push}" did not read back off after a reload`);
    await closeSettings();
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  if (page && failures.length) await page.screenshot({ path: join(artifacts, "claude-permission-442-failure.png") }).catch(() => {});
  await h.close();
  for (const entry of stubRecords().filter((record) => record.kind === "launch")) {
    try { process.kill(entry.pid, "SIGKILL"); } catch { /* already gone */ }
  }
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL claude-permission-442: ${failures.length} scenario(s) failed`);
  process.exitCode = 1;
} else {
  console.log("PASS claude-permission-442");
}
