// carries: lib/claude-fork*, lib/claude-branch*, lib/draft-store*
// carries: lib/__fixtures__/claude-2.1.312/*
// carries: components/SessionSidebar*
// — a claude session's 「从此处编辑」 and 「新会话」 on its messages: e2e acceptance pass.
//
// A claude user message carries both actions pi's do (assistant and tool rows carry
// neither), its entry id the transcript entry's uuid. "New session" opens an independent
// claude session whose conversation is the source's before that message (pi's fork point),
// listed in the sidebar like any claude session, the source's transcript byte-identical;
// its first open resumes the copy, and a prompt there continues it. "Edit from here" is
// pi's in-place edit: the composer takes the message, Cancel changes nothing, and Send
// resumes the same session truncated before the message (`--resume <id>
// --resume-session-at <its parent>`) with the edited prompt; the replaced turns stay in
// claude's transcript and the chat shows the new branch alone, after a reload and a
// restart too. The edit is offered only where claude can resume before the message: not
// on a first message with nothing before it, not before the latest compaction; a refused
// edit changes nothing and gives the text back. Neither action is offered while a claude
// turn runs.
//
// The child is the PATH-injected e2e stub (e2e/claude-stub/claude.mjs) in its default mode:
// each prompt gets a streamed reply, one Read tool call and its result, and a final reply,
// all appended to a real-shaped transcript tree (a resumed child chains to the newest
// checkpoint's leaf, a truncating resume to its `--resume-session-at` entry, and the
// checkpoint is written at exit only, as claude 2.1.312 does); a `--resume` launch records
// the uuids its transcript holds (`resumedUuids`). No real model is called.
//
// Every scenario is individually guarded and prints one `PASS S370-nn` marker.
import assert from "node:assert/strict";
import { readFileSync, rmSync } from "node:fs";
import { createHash } from "node:crypto";
import { join } from "node:path";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, claudeScratch, createApi, createCheck, createHarness, createWaitUntil, errorText, findTranscript, readJsonLines, seedStorage, setupFailed } from "./harness.mjs";

assertNoDevServer();
const kit = claudeScratch("pi-web-claude-370-");
const { artifacts, scratch, home, agentDir, project, stubDir, stubLog, configDir } = kit;

const FIRST = "E2E 370 first";
const SECOND = "E2E 370 second";
const THIRD = "E2E 370 third";
const FOURTH = "E2E 370 fourth";
const NEW_SESSION_TITLE = "New session — creates an independent copy from here";
// pi's own title: a claude edit branches within the session too.
const EDIT_TITLE = "Edit from here — branches within this session";
const EDITED = "E2E 370 second edited";
const AFTER_COMPACT = "E2E 370 after compact";
const AFTER_RESTART = "E2E 370 after restart";
const REFUSED = "E2E 370 refused edit";

const h = createHarness();
let browser;
let context;
let page;
let base;
let firstLoad = true;
const failures = [];

const scenario = createCheck({ failures, message: errorText, record: (names, text) => `${names.join(" ")}: ${text}` });

const stubRecords = () => readJsonLines(stubLog);
const launchesFor = (id) => stubRecords().filter((entry) => entry.kind === "launch" && entry.sessionId === id);
const promptsOf = (pid) => stubRecords()
  .filter((entry) => entry.kind === "stdin" && entry.pid === pid)
  .flatMap((entry) => {
    try {
      return [JSON.parse(entry.line)];
    } catch {
      return [];
    }
  })
  .filter((event) => event?.type === "user")
  .map((event) => event.message?.content);

const waitFor = createWaitUntil();

const { getJson, postJson } = createApi(() => base);

/** Waits until the session's child is alive and idle (its turn ended). */
async function waitIdle(id, what) {
  await waitFor(async () => {
    const reply = await getJson(`/api/agent/${encodeURIComponent(id)}`);
    return reply.running === true && reply.state?.isStreaming === false;
  }, 60_000, `${what}: the claude turn never ended`, 250);
}

const transcriptOf = (id) => findTranscript(configDir, id);
const entriesOf = (file) => readFileSync(file, "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
const sha256 = (file) => createHash("sha256").update(readFileSync(file)).digest("hex");
/** The transcript's user entry whose content is `text`. */
function promptEntry(file, text) {
  const entry = entriesOf(file).find((item) => item.type === "user" && item.message?.content === text);
  assert.ok(entry, `the transcript holds no user entry ${JSON.stringify(text)}`);
  return entry;
}
const promptUuid = (file, text) => promptEntry(file, text).uuid;
const exitedPids = () => new Set(stubRecords().filter((entry) => entry.kind === "exit").map((entry) => entry.pid));

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
const stopButton = () => page.getByRole("button", { name: "Stop agent", exact: true });
const currentSessionId = () => new URL(page.url()).searchParams.get("session");
const row = (entryId) => page.locator(`[data-entry-id="${entryId}"]`).first();
const actionCount = (title) => page.locator(`button[title="${title}"]`).count();
/** Hovers a user bubble's row and clicks one of its actions. */
async function clickAction(entryId, title) {
  const target = row(entryId);
  await target.waitFor({ state: "visible", timeout: NAV_MS });
  await target.hover();
  await target.locator(`button[title="${title}"]`).click({ timeout: ACTION_MS });
}
/** Waits until the page shows another session than `from`, and returns its id. */
async function waitForNewSession(from) {
  return waitFor(() => {
    const id = currentSessionId();
    return id && id !== from ? id : false;
  }, NAV_MS, () => `the page stayed on ${from}: ${page.url()}`);
}
async function waitForText(text) {
  await page.getByText(text, { exact: true }).first().waitFor({ state: "visible", timeout: NAV_MS });
}
/** The texts of the user bubbles the chat shows, top to bottom. */
async function userBubbles() {
  return page.locator(".markdown-user-message").allInnerTexts().then((texts) => texts.map((text) => text.trim()));
}
/** Whether a user bubble's row offers the action (hovered first: the actions show on hover). */
async function offers(entryId, title) {
  const target = row(entryId);
  await target.waitFor({ state: "attached", timeout: NAV_MS });
  return (await target.locator(`button[title="${title}"]`).count()) === 1;
}

h.trapInterrupt();

try {
  const env = {
    ...process.env,
    HOME: home,
    PI_CODING_AGENT_DIR: agentDir,
    PI_WEB_PASSWORD: "",
    CLAUDECODE: "1",
    CLAUDE_CONFIG_DIR: configDir,
    PI_WEB_ALLOWED_ROOT_PREFIXES: scratch,
    // The product default: only an explicit kill ends a child mid-scenario.
    PI_WEB_IDLE_TIMEOUT_MS: "600000",
    PATH: `${stubDir}:${process.env.PATH ?? ""}`,
    PI_E2E_CLAUDE_STUB_LOG: stubLog,
    NEXT_TELEMETRY_DISABLED: "1",
  };
  delete env.PI_E2E_CLAUDE_STUB_MODE;
  ({ base } = await h.startServer({
    name: "claude-fork-370",
    logFile: join(artifacts, "claude-fork-370-server.log"),
    from: env,
  }));

  const { chromium } = await import("playwright");
  browser = await h.launchBrowser(() => chromium.launch());
  context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US", baseURL: base });
  await seedStorage(context);
  page = await context.newPage();
  page.setDefaultTimeout(ACTION_MS);
  await context.tracing.start({ screenshots: true, snapshots: true });

  // The source: three turns, made through the API.
  const created = await postJson("/api/agent/new", { cwd: project, harness: "claude", model: "claude-haiku-5-5", type: "prompt", message: FIRST });
  assert.equal(created.status, 200, `POST /api/agent/new -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const source = created.body.sessionId;
  await waitIdle(source, "the first turn");
  for (const text of [SECOND, THIRD]) {
    const sent = await postJson(`/api/agent/${encodeURIComponent(source)}`, { type: "prompt", message: text });
    assert.equal(sent.status, 200, `prompt ${text} -> ${sent.status} ${JSON.stringify(sent.body).slice(0, 300)}`);
    await waitIdle(source, text);
  }
  const sourceFile = transcriptOf(source);
  assert.ok(sourceFile, "the stub wrote no transcript for the source");
  const ids = { first: promptUuid(sourceFile, FIRST), second: promptUuid(sourceFile, SECOND), third: promptUuid(sourceFile, THIRD) };
  let forkId;
  let forkUuids;

  await scenario("S370-01", "An idle claude session's user messages carry both actions under their transcript uuid (the edit in-session, pi's); assistant and tool rows carry none", async () => {
    await openSession(source);
    await waitForText(THIRD);
    for (const uuid of [ids.first, ids.second, ids.third]) {
      assert.ok(await offers(uuid, NEW_SESSION_TITLE), `the user message ${uuid} has no New session action`);
    }
    for (const uuid of [ids.second, ids.third]) {
      assert.ok(await offers(uuid, EDIT_TITLE), `the user message ${uuid} has no in-session Edit from here action`);
    }
    // The first message opens the conversation: claude keeps nothing before it, so no in-place edit there.
    assert.equal(await offers(ids.first, EDIT_TITLE), false, "the first message offers an edit claude cannot resume for");
    // Exactly one of each per offered user message: no assistant, tool call or tool result row has one.
    assert.equal(await actionCount(NEW_SESSION_TITLE), 3);
    assert.equal(await actionCount(EDIT_TITLE), 2);
    assert.equal(await page.locator('button[title*="opens a new session"]').count(), 0, "an edit still promises another session");
    await waitForText(`E2E final reply to ${FIRST}`);
    const assistantRows = page.locator("[data-entry-id]").filter({ hasText: `E2E final reply to ${FIRST}` });
    assert.equal(await assistantRows.locator(`button[title="${NEW_SESSION_TITLE}"], button[title="${EDIT_TITLE}"]`).count(), 0);
    await page.screenshot({ path: join(artifacts, "claude-370-actions.png") });
  });

  await scenario("S370-02", "While a claude turn runs neither action is offered; both return when it ends", async () => {
    await openSession(source);
    await waitForText(THIRD);
    assert.equal(await actionCount(NEW_SESSION_TITLE), 3);
    await sendPrompt(FOURTH);
    await stopButton().waitFor({ state: "visible", timeout: NAV_MS });
    await waitFor(async () => (await actionCount(NEW_SESSION_TITLE)) === 0 && (await actionCount(EDIT_TITLE)) === 0 && await stopButton().isVisible(), 10_000, "the actions stayed offered while the turn ran", 50);
    await waitIdle(source, FOURTH);
    await waitFor(async () => (await actionCount(NEW_SESSION_TITLE)) === 4 && (await actionCount(EDIT_TITLE)) === 3, NAV_MS, async () => `after the turn the actions are ${await actionCount(NEW_SESSION_TITLE)} / ${await actionCount(EDIT_TITLE)}, expected 4 / 3`);
    // The new message's id is its transcript entry's too.
    await row(promptUuid(sourceFile, FOURTH)).waitFor({ state: "attached", timeout: NAV_MS });
  });

  await scenario("S370-03", "New session forks before the message: an independent, listed claude session; the source's transcript is byte-identical", async () => {
    await openSession(source);
    await waitForText(THIRD);
    const before = sha256(sourceFile);
    const sourceEntries = entriesOf(sourceFile);
    const launchesBefore = stubRecords().filter((entry) => entry.kind === "launch").length;
    await clickAction(ids.second, NEW_SESSION_TITLE);
    forkId = await waitForNewSession(source);
    await waitForText(`E2E final reply to ${FIRST}`);
    assert.deepEqual(await userBubbles(), [FIRST], "the fork shows other messages than the ones before the forked one");
    assert.equal(await textarea().inputValue(), "", "New session prefilled the composer");
    assert.equal(sha256(sourceFile), before, "the source's transcript changed");

    const copy = transcriptOf(forkId);
    assert.ok(copy, "the fork has no transcript");
    const forked = entriesOf(copy);
    const cut = sourceEntries.findIndex((entry) => entry.uuid === ids.second);
    forkUuids = sourceEntries.slice(0, cut).map((entry) => entry.uuid);
    assert.deepEqual(forked.map((entry) => entry.uuid), forkUuids, "the copy is not the source's entries before the message");
    assert.ok(forked.every((entry) => entry.sessionId === forkId), "an entry of the copy keeps another session id");

    // Listed like any claude session: the index row and the sidebar's claude row.
    const list = await getJson("/api/sessions?summary=1");
    const listed = list.sessions.find((session) => session.id === forkId);
    assert.ok(listed, `the fork ${forkId} is not listed`);
    assert.equal(listed.harness, "claude");
    assert.equal(listed.cwd, project);
    assert.equal(listed.claude?.model, "claude-haiku-5-5");
    const sidebarLink = page.locator(`a[href="?session=${encodeURIComponent(forkId)}"]`).first();
    await sidebarLink.waitFor({ state: "attached", timeout: NAV_MS });
    // The row's title line: its link, then the harness marker (components/SessionSidebar.tsx).
    assert.equal(await sidebarLink.locator("xpath=..").locator('[data-harness="claude"]').count(), 1, "the fork's sidebar row carries no claude marker");
    assert.ok(await page.locator(`a[href="?session=${encodeURIComponent(source)}"]`).count() > 0, "the source left the sidebar");

    // Its first open resumed the copy: the child loads exactly the truncated conversation.
    const launch = await waitFor(() => launchesFor(forkId)[0] ?? false, 60_000, "opening the fork started no child");
    assert.equal(launch.resumed, true, `the fork's child was not resumed: ${launch.argv.join(" ")}`);
    assert.deepEqual(launch.resumedUuids, forkUuids, "the resumed child does not see the truncated conversation");
    assert.ok(stubRecords().filter((entry) => entry.kind === "launch").length > launchesBefore);
    await page.screenshot({ path: join(artifacts, "claude-370-new-session.png") });
  });

  await scenario("S370-04", "A prompt in the fork continues the truncated conversation; the source stays untouched", async () => {
    assert.ok(forkId, "prerequisite: S370-03 made no fork");
    const before = sha256(sourceFile);
    await sendPrompt("E2E 370 in the fork");
    await waitForText("E2E final reply to E2E 370 in the fork");
    await waitIdle(forkId, "the fork's turn");
    const [launch] = launchesFor(forkId);
    assert.deepEqual(promptsOf(launch.pid), ["E2E 370 in the fork"], "the fork's child got other prompts");
    const forked = entriesOf(transcriptOf(forkId));
    assert.deepEqual(forked.slice(0, forkUuids.length).map((entry) => entry.uuid), forkUuids, "the fork's history was rewritten");
    assert.ok(forked.slice(forkUuids.length).some((entry) => entry.type === "user" && entry.message?.content === "E2E 370 in the fork"));
    assert.equal(sha256(sourceFile), before, "a prompt in the fork changed the source's transcript");
    assert.ok(!readFileSync(sourceFile, "utf8").includes("E2E 370 in the fork"));
    assert.deepEqual(await userBubbles(), [FIRST, "E2E 370 in the fork"]);
  });

  await scenario("S370-05", "Edit from here stages the edit in place: the composer takes the message; Cancel changes nothing", async () => {
    await openSession(source);
    await waitForText(FOURTH);
    const before = sha256(sourceFile);
    const launchesBefore = stubRecords().filter((entry) => entry.kind === "launch").length;
    const stdinBefore = stubRecords().filter((entry) => entry.kind === "stdin").length;
    await textarea().fill("");
    await clickAction(ids.second, EDIT_TITLE);
    await waitFor(async () => (await textarea().inputValue()) === SECOND, NAV_MS, async () => `the composer holds ${JSON.stringify(await textarea().inputValue())}, expected the edited message`);
    const cancel = row(ids.second).locator('button[title="Cancel"]');
    await cancel.waitFor({ state: "visible", timeout: NAV_MS });
    assert.equal(currentSessionId(), source, "staging an edit left the session");
    assert.deepEqual(await userBubbles(), [FIRST, SECOND, THIRD, FOURTH], "staging an edit changed the history");
    await page.screenshot({ path: join(artifacts, "claude-370-edit-staged.png") });
    await cancel.click({ timeout: ACTION_MS });
    await waitFor(async () => (await row(ids.second).locator('button[title="Cancel"]').count()) === 0, NAV_MS, "Cancel left the edit pending");
    assert.ok(await offers(ids.second, EDIT_TITLE), "after Cancel the message offers no edit");
    assert.deepEqual(await userBubbles(), [FIRST, SECOND, THIRD, FOURTH]);
    assert.equal(sha256(sourceFile), before, "staging and cancelling an edit changed the transcript");
    assert.equal(stubRecords().filter((entry) => entry.kind === "launch").length, launchesBefore, "staging an edit started a child");
    assert.equal(stubRecords().filter((entry) => entry.kind === "stdin").length, stdinBefore, "staging an edit wrote to the child");
    assert.equal(currentSessionId(), source);
    await textarea().fill("");
  });

  await scenario("S370-06", "Edit from here, sent, resumes the same session truncated before the message: one new child, the old branch kept on disk and hidden", async () => {
    await openSession(source);
    await waitForText(FOURTH);
    const sessionsBefore = (await getJson("/api/sessions?summary=1")).sessions.filter((session) => session.harness === "claude").length;
    const [previous] = launchesFor(source).slice(-1);
    assert.ok(previous, "the source has no child");
    const resumeAt = promptEntry(sourceFile, SECOND).parentUuid;
    assert.ok(resumeAt, "the edited message has no parent entry");
    await textarea().fill("");
    await clickAction(ids.second, EDIT_TITLE);
    await waitFor(async () => (await textarea().inputValue()) === SECOND, NAV_MS, "the composer never took the edited message");
    await sendPrompt(EDITED);
    await waitForText(`E2E final reply to ${EDITED}`);
    await waitIdle(source, "the edited turn");
    assert.equal(currentSessionId(), source, "the edit moved to another session");

    // One new child of the same session: the truncating resume at the message's parent, the edited prompt alone on its stdin.
    const launches = launchesFor(source);
    const edit = launches.at(-1);
    assert.notEqual(edit.pid, previous.pid, "no new child was started");
    const at = edit.argv.indexOf("--resume-session-at");
    assert.ok(at > 0, `the edit's child carries no --resume-session-at: ${edit.argv.join(" ")}`);
    assert.deepEqual(edit.argv.slice(at - 2), ["--resume", source, "--resume-session-at", resumeAt]);
    assert.ok(!edit.argv.includes("--fork-session") && !edit.argv.includes("--session-id"));
    assert.ok(exitedPids().has(previous.pid), "the idle child was not ended first");
    assert.deepEqual(promptsOf(edit.pid), [EDITED]);

    // On disk: the replaced turns are still there; the edited prompt hangs under the kept entry.
    const entries = entriesOf(sourceFile);
    for (const text of [SECOND, THIRD, FOURTH]) assert.ok(entries.some((entry) => entry.type === "user" && entry.message?.content === text), `the old branch lost ${text}`);
    assert.equal(promptEntry(sourceFile, EDITED).parentUuid, resumeAt);
    // The newest checkpoint is the previous child's exit one, naming the old branch's leaf (claude writes
    // the next at exit): the chat follows the newer turns all the same.
    const stale = entries.filter((entry) => entry.type === "last-prompt").at(-1);
    assert.ok(stale, "the previous child wrote no exit checkpoint");
    assert.equal(entries.find((entry) => entry.uuid === stale.leafUuid)?.message?.content?.[0]?.text, `E2E final reply to ${FOURTH}`);

    // The chat: the new branch alone, live and after a reload; no session was added.
    assert.deepEqual(await userBubbles(), [FIRST, EDITED]);
    await openSession(source);
    await waitForText(`E2E final reply to ${EDITED}`);
    assert.deepEqual(await userBubbles(), [FIRST, EDITED], "a reload shows the replaced turns");
    assert.equal(await page.getByText(`E2E final reply to ${THIRD}`, { exact: true }).count(), 0);
    const sessionsAfter = (await getJson("/api/sessions?summary=1")).sessions.filter((session) => session.harness === "claude").length;
    assert.equal(sessionsAfter, sessionsBefore, "the edit added a session");
    ids.edited = promptUuid(sourceFile, EDITED);
    assert.ok(await offers(ids.edited, EDIT_TITLE), "the edited message cannot be edited again");
    assert.equal(await actionCount(NEW_SESSION_TITLE), 2);
    await page.screenshot({ path: join(artifacts, "claude-370-edit-sent.png") });
  });

  await scenario("S370-07", "After a compaction the edit is offered only on messages after it; New session stays on every message", async () => {
    assert.ok(ids.edited, "prerequisite: S370-06 made no edit");
    const compacted = await postJson(`/api/agent/${encodeURIComponent(source)}`, { type: "compact" });
    assert.equal(compacted.status, 200, `compact -> ${compacted.status} ${JSON.stringify(compacted.body).slice(0, 300)}`);
    await waitIdle(source, "the compaction");
    const sent = await postJson(`/api/agent/${encodeURIComponent(source)}`, { type: "prompt", message: AFTER_COMPACT });
    assert.equal(sent.status, 200);
    await waitIdle(source, AFTER_COMPACT);
    ids.afterCompact = promptUuid(sourceFile, AFTER_COMPACT);
    await openSession(source);
    await waitForText(`E2E final reply to ${AFTER_COMPACT}`);
    assert.deepEqual(await userBubbles(), [FIRST, EDITED, AFTER_COMPACT]);
    assert.ok(await offers(ids.afterCompact, EDIT_TITLE), "the message after the compaction offers no edit");
    assert.equal(await offers(ids.edited, EDIT_TITLE), false, "a message before the compaction offers an edit claude cannot resume for");
    assert.equal(await offers(ids.first, EDIT_TITLE), false);
    for (const uuid of [ids.first, ids.edited, ids.afterCompact]) assert.ok(await offers(uuid, NEW_SESSION_TITLE), `${uuid} lost New session`);
    // The server refuses the same edit by itself, and changes nothing.
    const before = sha256(sourceFile);
    const refused = await postJson(`/api/agent/${encodeURIComponent(source)}`, { type: "prompt", message: "x", entryId: ids.edited });
    assert.equal(refused.status, 500);
    assert.match(refused.body.error, /before the conversation's last compaction/);
    assert.equal(refused.body.code, "prompt_rejected");
    assert.equal(sha256(sourceFile), before);
  });

  await scenario("S370-08", "A restarted child continues the edited branch", async () => {
    assert.ok(ids.afterCompact, "prerequisite: S370-07 made no turn after the compaction");
    const [before] = launchesFor(source).slice(-1);
    // Switching to the default effort ends the idle child; the next prompt starts a plain --resume.
    const ended = await postJson(`/api/agent/${encodeURIComponent(source)}`, { type: "set_effort", effort: "default" });
    assert.equal(ended.status, 200);
    await waitFor(() => exitedPids().has(before.pid), 10_000, "the idle child did not end");
    const sent = await postJson(`/api/agent/${encodeURIComponent(source)}`, { type: "prompt", message: AFTER_RESTART });
    assert.equal(sent.status, 200);
    await waitIdle(source, AFTER_RESTART);
    const restart = launchesFor(source).at(-1);
    assert.notEqual(restart.pid, before.pid);
    assert.deepEqual(restart.argv.slice(-2), ["--resume", source], `the restart is no plain resume: ${restart.argv.join(" ")}`);
    const lastOfBranch = entriesOf(sourceFile).findLast((entry) => entry.type === "assistant" && entry.message?.content?.[0]?.text === `E2E final reply to ${AFTER_COMPACT}`);
    assert.equal(promptEntry(sourceFile, AFTER_RESTART).parentUuid, lastOfBranch.uuid, "the restarted child continued another branch");
    await openSession(source);
    await waitForText(`E2E final reply to ${AFTER_RESTART}`);
    assert.deepEqual(await userBubbles(), [FIRST, EDITED, AFTER_COMPACT, AFTER_RESTART]);
    await page.screenshot({ path: join(artifacts, "claude-370-after-restart.png") });
  });

  await scenario("S370-09", "A refused edit changes nothing: the error shows, the history stays, the text comes back and the edit stays pending", async () => {
    assert.ok(ids.afterCompact, "prerequisite: S370-07 made no turn after the compaction");
    await openSession(source);
    await waitForText(`E2E final reply to ${AFTER_RESTART}`);
    const afterRestart = promptUuid(sourceFile, AFTER_RESTART);
    await textarea().fill("");
    await clickAction(afterRestart, EDIT_TITLE);
    await waitFor(async () => (await textarea().inputValue()) === AFTER_RESTART, NAV_MS, "the composer never took the edited message");
    // Meanwhile the session compacts (here through the API): the staged message now lies before the latest compaction.
    const compacted = await postJson(`/api/agent/${encodeURIComponent(source)}`, { type: "compact" });
    assert.equal(compacted.status, 200);
    await waitIdle(source, "the second compaction");
    await waitFor(async () => !(await offers(afterRestart, EDIT_TITLE)), NAV_MS, "the message still offers an edit after the compaction");
    const before = sha256(sourceFile);
    const launchesBefore = launchesFor(source).length;
    await textarea().fill(REFUSED);
    await page.getByRole("button", { name: "Send", exact: true }).first().click({ timeout: ACTION_MS });
    await page.getByText("claude cannot resume before the conversation's last compaction", { exact: false }).first().waitFor({ state: "visible", timeout: NAV_MS });
    await waitFor(async () => (await textarea().inputValue()) === REFUSED, NAV_MS, async () => `the composer holds ${JSON.stringify(await textarea().inputValue())}, expected the refused text back`);
    await waitFor(async () => JSON.stringify(await userBubbles()) === JSON.stringify([FIRST, EDITED, AFTER_COMPACT, AFTER_RESTART]), NAV_MS, async () => `the history shows ${JSON.stringify(await userBubbles())}`);
    await row(afterRestart).locator('button[title="Cancel"]').waitFor({ state: "visible", timeout: NAV_MS });
    assert.equal(sha256(sourceFile), before, "a refused edit wrote to the transcript");
    assert.equal(launchesFor(source).length, launchesBefore, "a refused edit started a child");
    assert.equal(currentSessionId(), source);
    await row(afterRestart).locator('button[title="Cancel"]').click({ timeout: ACTION_MS });
    await waitFor(async () => (await row(afterRestart).locator('button[title="Cancel"]').count()) === 0, NAV_MS, "Cancel left the edit pending");
    await page.screenshot({ path: join(artifacts, "claude-370-edit-refused.png") });
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  if (page) await page.screenshot({ path: join(artifacts, "claude-370-final.png") }).catch(() => {});
  if (context) await context.tracing.stop({ path: join(artifacts, "claude-370-trace.zip") }).catch(() => {});
  await h.close();
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL claude-fork-370: ${failures.length} scenario(s) failed`);
  for (const failure of failures) console.log(`  ${failure}`);
  process.exitCode = 1;
}
