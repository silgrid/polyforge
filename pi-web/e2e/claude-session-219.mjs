// carries: lib/claude-*, app/api/claude/*, e2e/claude-stub/*, instrumentation-node.ts, lib/types.ts
// carries: components/SessionSidebar*, hooks/useAgentSession*, app/api/agent/*, app/api/sessions/*
// — pi-web claude code session type: end-to-end acceptance pass.
//
// Authored against artifacts/spec.md's `## Acceptance criteria` and the
// Requirement scenario blocks, BEFORE the implementation exists. Every step
// drives the spec'd behaviour through interfaces that already exist on the
// pre-code tree — `POST /api/agent/new` (the existing create route, extended by
// the spec with a harness choice), `GET /api/sessions`, the sidebar session
// rows, the composer and the chat view — and matches new surfaces only by
// user-visible text/roles. The only new surface assumed is the one the spec
// requires ("New authenticated routes behind the existing password and the
// existing cwd registration guard") plus the create request's harness fields;
// see artifacts/e2e-scenarios.md for the recorded spec under-specifications.
// Pre-implementation this suite therefore fails at assertion level (no claude
// child is spawned, no claude row exists), never at build level: no symbol that
// does not exist today is imported.
//
// The child is a PATH-injected stub (e2e/claude-stub/claude.mjs, a separate
// non-suite file) that speaks the stream-json dialect recorded from the real
// `claude 2.1.312` and writes a real-shaped jsonl transcript, so the resume
// proof is a real process exit (idle expiry, never `PI_WEB_IDLE_TIMEOUT_MS=0`)
// followed by a second launch whose argv carries `--resume <id from the first
// launch>`.
//
// Each scenario is individually guarded: a failing one logs FAIL and sets the
// exit code without stopping the remaining scenarios, so one baseline run lays
// the pre-code bar for every criterion.
import assert from "node:assert/strict";
import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { createCheck, createHarness, createWaitUntil, errorText, pidAlive, readJsonLines, seedStorage, setupFailed } from "./harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const distDir = process.env.PI_WEB_DIST_DIR || ".next";
assert.ok(!existsSync(join(root, distDir, "dev/lock")), "Use a checkout without an active dev server");

const artifacts = join(root, "test-results/e2e");
mkdirSync(artifacts, { recursive: true });
const scratch = mkdtempSync(join(tmpdir(), "pi-web-claude-e2e-"));
const agentDir = join(scratch, "agent");
// An isolated HOME: with no CLAUDE_CONFIG_DIR inherited, claude's transcripts
// live under this ~/.claude, never the operator's.
const homeDir = join(scratch, "home");
const project = join(scratch, "project");
const stubDir = join(scratch, "bin");
const stubLog = join(scratch, "claude-stub.jsonl");
// The retired dedicated config dir: it must stay absent.
const claudeDir = join(agentDir, "claude");
mkdirSync(project, { recursive: true });
// A sibling pi session, so S2 can prove a pi row still carries no harness marker.
mkdirSync(join(agentDir, "sessions", "e2e"), { recursive: true });
writeFileSync(join(agentDir, "sessions", "e2e", "e2e-claude-219-pi.jsonl"), [
  { type: "session", version: 3, id: "e2e-claude-219-pi", timestamp: "2026-11-02T00:00:00.000Z", cwd: project },
  { type: "message", id: "m1", parentId: null, timestamp: "2026-11-02T00:00:00.000Z", message: { role: "user", content: "E2E pi sibling session" } },
].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
mkdirSync(stubDir, { recursive: true });
mkdirSync(agentDir, { recursive: true });
mkdirSync(homeDir, { recursive: true });
writeFileSync(join(project, "e2e-probe.txt"), "E2E_TOOL_CONTENT\n");
// The stub is copied (0755) onto the server's PATH as `claude`; the committed
// fixture stays untouched and the scratch tree owns the executable.
copyFileSync(join(root, "e2e/claude-stub/claude.mjs"), join(stubDir, "claude"));
chmodSync(join(stubDir, "claude"), 0o755);

const FIRST = "E2E first message";
const PI_FIRST = "E2E pi sibling session";
const SECOND = "E2E second message";
const THIRD = "E2E third message";
const TITLES = {
  S1: "Create a claude session by harness and model, with no preset",
  S2: "Sidebar shows a claude session with its harness marker",
  S3: "Message round trip renders through the adapter",
  S4: "Reopening a claude session shows its history",
  S5: "Idle expiry then reopen resumes the conversation",
  S6: "No dedicated config dir, no injected CLAUDE_CONFIG_DIR, and no PI_WEB_PASSWORD/CLAUDECODE in the child",
};

const h = createHarness();
let browser;
let context;
let page;
let base;
let sessionId;
let firstLaunch;

const failures = [];
const scenario = createCheck({ failures, message: errorText, record: ([id], text) => `${id}: ${text}`,
  pass: ([id]) => console.log(`PASS ${id} ${TITLES[id]}`), fail: ([id], text) => console.log(`FAIL ${id} ${TITLES[id]}: ${text}`) });

const stubRecords = () => readJsonLines(stubLog);
const stubLaunches = () => stubRecords().filter((entry) => entry.kind === "launch");

const waitFor = createWaitUntil();

function argvValue(argv, flag) {
  const equals = argv.find((arg) => arg.startsWith(`${flag}=`));
  if (equals) return equals.slice(flag.length + 1);
  const index = argv.indexOf(flag);
  return index >= 0 ? argv[index + 1] : undefined;
}

async function getJson(pathname) {
  const response = await fetch(`${base}${pathname}`, { signal: AbortSignal.timeout(60_000) });
  assert.equal(response.status, 200, `GET ${pathname} -> ${response.status}`);
  return response.json();
}

async function newPage() {
  context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US", baseURL: base });
  // The classic single-chat layout (tab mode's default-on split is a separate
  // surface) and the sidebar's default 15-day age filter, which would hide nothing here
  // but is disabled for determinism (the same seed e2e/run.mjs uses).
  await seedStorage(context);
  page = await context.newPage();
  page.setDefaultTimeout(30_000);
  await context.tracing.start({ screenshots: true, snapshots: true });
}

async function openSession() {
  await page.goto(`${base}/?session=${encodeURIComponent(sessionId)}`, { waitUntil: "domcontentloaded", timeout: 480_000 });
  await page.locator(".chat-input-textarea").waitFor({ state: "visible", timeout: 180_000 });
}

/**
 * Asserts a reopened turn the way the existing chat view shows it, an owner-approved
 * rebaseline from review round 1 finding #4: the final reply is visible, an
 * answered turn folds its process details, so the toggle is clicked open before the
 * intermediate text and the tool row are asserted.
 */
async function assertReopenedTurn(message) {
  await page.getByText(`E2E final reply to ${message}`, { exact: true }).first().waitFor({ timeout: 60_000 });
  const folded = page.locator('button[aria-expanded="false"][aria-label^="Process details"]');
  await waitFor(() => folded.count(), 30_000, "the answered turn shows no process-details toggle", 250);
  for (let i = 0; i < 10 && (await folded.count()); i++) await folded.first().click({ timeout: 5_000 });
  await page.getByText(`E2E streamed reply to ${message}`, { exact: true }).first().waitFor({ timeout: 30_000 });
  await page.getByRole("button", { name: /read/i }).first().waitFor({ state: "visible", timeout: 30_000 });
}

const sendButton = () => page.getByRole("button", { name: "Send", exact: true });
const stopButton = () => page.getByRole("button", { name: "Stop agent", exact: true });

async function sendMessage(text) {
  const textarea = page.locator(".chat-input-textarea");
  await textarea.fill(text);
  await sendButton().click();
}

// Owner intentional change the claude row's marker is exactly `claude`
// (never a model name), and it is the last element of the title line, flush with that
// line's right edge. These helpers replace the former generic "row has a marker" pin.
const MODEL_FULL_NAME = /claude-(?:opus|sonnet|haiku|fable)-\d+-\d+/i;
const rowTitleLine = (first) => page.locator(`[title="${first}"]`).first();

/** The claude title line, its one harness marker, and both right edges. */
async function claudeRowHarnessGeometry() {
  const line = rowTitleLine(FIRST);
  await line.waitFor({ state: "attached", timeout: 60_000 });
  return line.evaluate((titleLine) => {
    const markers = [...titleLine.querySelectorAll('[data-harness="claude"]')];
    const marker = markers[0] ?? null;
    const textsAndLabels = [titleLine.textContent ?? ""];
    for (const node of [titleLine, ...titleLine.querySelectorAll("*")]) {
      for (const name of ["title", "aria-label", "alt"]) {
        const value = node.getAttribute(name);
        if (value) textsAndLabels.push(value);
      }
    }
    return {
      markerCount: markers.length,
      markerText: marker?.textContent?.trim() ?? "",
      markerRight: marker?.getBoundingClientRect().right ?? null,
      lineRight: titleLine.getBoundingClientRect().right,
      textsAndLabels,
    };
  });
}

/** The pi title line's marker count and text. */
async function piRowTitleLine() {
  const line = rowTitleLine(PI_FIRST);
  await line.waitFor({ state: "attached", timeout: 60_000 });
  return line.evaluate((titleLine) => ({
    markerCount: titleLine.querySelectorAll("[data-harness]").length,
    text: titleLine.textContent ?? "",
  }));
}

h.trapInterrupt();

try {
  const serverEnv = {
    ...process.env,
    PI_CODING_AGENT_DIR: agentDir,
    // An isolated HOME and no inherited CLAUDE_CONFIG_DIR: the child's
    // transcript lives under <scratch home>/.claude.
    HOME: homeDir,
    PI_WEB_PASSWORD: "",
    // Sentinels the child must never inherit (names only are asserted).
    CLAUDECODE: "1",
    ANTHROPIC_API_KEY: "e2e-dummy-not-a-credential",
    ANTHROPIC_BASE_URL: "http://127.0.0.1:9",
    CLAUDE_TEST_INHERITED: "e2e-dummy-not-a-credential",
    // The scratch tree lives under os.tmpdir(), which the registrable-root
    // guard does not trust by default (pi#55's operator prefix).
    PI_WEB_ALLOWED_ROOT_PREFIXES: scratch,
    // Force a real idle exit during the run; `0` is never used for the
    // resume proof (the spec's lifecycle semantics).
    PI_WEB_IDLE_TIMEOUT_MS: "12000",
    // Only the stub, on PATH. `claude` is resolved from PATH alone.
    PATH: `${stubDir}:${process.env.PATH ?? ""}`,
    PI_E2E_CLAUDE_STUB_LOG: stubLog,
    NEXT_TELEMETRY_DISABLED: "1",
  };
  // The suite never inherits a config dir from whichever shell started it.
  delete serverEnv.CLAUDE_CONFIG_DIR;
  ({ base } = await h.startServer({
    name: "claude-session-219",
    logFile: join(artifacts, "claude-session-219-server.log"),
    from: serverEnv,
  }));

  const { chromium } = await import("playwright");
  browser = await h.launchBrowser(() => chromium.launch());

  // S1 — rewritten: a claude session is created by harness and
  // full model name, with no preset, and its child carries the reduced argv.
  await scenario("S1", async () => {
    const response = await fetch(`${base}/api/agent/new`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        cwd: project,
        type: "prompt",
        message: FIRST,
        harness: "claude",
        model: "claude-sonnet-5-5",
      }),
      signal: AbortSignal.timeout(120_000),
    });
    const body = await response.json().catch(() => ({}));
    assert.ok(response.ok, `POST /api/agent/new with harness "claude" and a full model name failed: ${response.status} ${JSON.stringify(body).slice(0, 400)}`);
    sessionId = body.sessionId ?? body.id;
    assert.ok(typeof sessionId === "string" && sessionId.length > 0, "the create route returned no session id for the claude session");

    firstLaunch = await waitFor(
      () => stubLaunches()[0] ?? false,
      30_000,
      "no claude child process was launched: the PATH stub recorded no launch (harness claude not wired to a child process)",
    );
    const joined = firstLaunch.argv.join(" ");
    assert.equal(argvValue(firstLaunch.argv, "--model"), "claude-sonnet-5-5", `the claude child's argv carries no "--model claude-sonnet-5-5": ${joined}`);
    // The default permission mode is ask: claude's prompts come to Pi Web over stdio.
    assert.equal(argvValue(firstLaunch.argv, "--permission-prompts"), "host", `the child does not carry --permission-prompts host: ${joined}`);
    assert.equal(argvValue(firstLaunch.argv, "--permission-prompt-tool"), "stdio", `the child does not carry --permission-prompt-tool stdio: ${joined}`);
    assert.equal(argvValue(firstLaunch.argv, "--session-id"), sessionId, `the first launch does not carry --session-id <the session id>: ${joined}`);
    assert.equal(argvValue(firstLaunch.argv, "--resume"), undefined, `a first launch must not carry --resume: ${joined}`);
    for (const flag of ["--bare", "--strict-mcp-config", "--disable-slash-commands", "--permission-mode", "--tools", "--allowedTools", "--cwd"]) {
      assert.ok(!firstLaunch.argv.some((arg) => arg === flag || arg.startsWith(`${flag}=`)), `the claude child's argv still carries ${flag}: ${joined}`);
    }
    assert.equal(firstLaunch.argv.filter((arg) => arg === "--model").length, 1, `the claude child's argv must carry exactly one --model: ${joined}`);
    // The session id the child reports is the one the index/list must carry.
    if (firstLaunch.sessionId) assert.ok(typeof firstLaunch.sessionId === "string", "the child reported no session id");

    const list = await getJson("/api/sessions?summary=1");
    assert.ok(Array.isArray(list.sessions), "GET /api/sessions returned no session list");
    const listed = list.sessions.some((session) => session.id === sessionId
      || (session.cwd === project && String(session.firstMessage ?? "").includes(FIRST)));
    assert.ok(listed, `the claude session ${sessionId} is not listed by GET /api/sessions (ids: ${list.sessions.map((session) => session.id).join(", ")})`);
  });

  await newPage();

  // S2 — sidebar harness marker (same flow as the resume proof, per the
  // ticket's acceptance). Owner intentional change the marker is
  // exactly `claude` and flush with the title line's right edge, while a pi row
  // still has no marker.
  await scenario("S2", async () => {
    assert.ok(sessionId, "prerequisite: S1 did not create a claude session");
    await openSession();
    const showSidebar = page.getByRole("button", { name: "Show sidebar", exact: true });
    if (await showSidebar.isVisible().catch(() => false)) await showSidebar.click();
    const geometry = await waitFor(async () => {
      const row = await claudeRowHarnessGeometry();
      const flush = row.markerCount === 1 && row.markerRight !== null && Math.abs(row.markerRight - row.lineRight) <= 1;
      const harnessOnly = row.markerText === "claude" && row.textsAndLabels.every((value) => !MODEL_FULL_NAME.test(value));
      return flush && harnessOnly ? row : false;
    }, 60_000, "the claude row's harness chip is not exactly `claude`, is not flush with its title line's right edge (\u00b11px), or a model name remains in the row", 250);
    assert.equal(geometry.markerCount, 1, "the claude session's sidebar row must show exactly one harness marker");
    assert.equal(geometry.markerText, "claude", "the claude row's harness marker text must be exactly `claude`");
    assert.ok(geometry.textsAndLabels.every((value) => !MODEL_FULL_NAME.test(value)), "a claude model full name remains in the row's text or tooltip");
    assert.ok(Math.abs(geometry.markerRight - geometry.lineRight) <= 1, `the harness chip is not flush with the title line's right edge: chip ${geometry.markerRight}, line ${geometry.lineRight}`);
    const pi = await waitFor(async () => {
      const row = await piRowTitleLine();
      return row.markerCount === 0 && !/(^|[^a-z])claude([^a-z]|$)/i.test(row.text) ? row : false;
    }, 30_000, "the pi session's sidebar row carries a harness marker", 250);
    assert.equal(pi.markerCount, 0, "the pi session's sidebar row must carry no harness marker");
    await page.screenshot({ path: join(artifacts, "claude-219-sidebar.png") });
  });

  // S3 — message round trip through the adapter: user message, streamed
  // assistant text, tool call/result rows, send unavailable while the turn runs.
  await scenario("S3", async () => {
    assert.ok(sessionId, "prerequisite: S1 did not create a claude session");
    if (!page) await newPage();
    const streamed = `E2E streamed reply to ${SECOND}`;
    await sendMessage(SECOND);
    await stopButton().waitFor({ state: "visible", timeout: 30_000 });
    const sendUsable = await sendButton().isEnabled().catch(() => false);
    assert.ok(!sendUsable, "Send stays usable while a claude turn runs (no queue, no steer)");
    await page.waitForFunction((prefix) => document.body.innerText.includes(prefix), streamed.slice(0, 20), { timeout: 20_000, polling: 50 });
    assert.equal(await stopButton().isVisible(), true, "the reply's partial text arrived only after the turn ended (no token-level streaming)");
    await page.getByText(streamed, { exact: true }).first().waitFor({ timeout: 30_000 });
    // An answered turn folds its process details (as for pi): after the final
    // reply, open them, then the turn's Read row, and assert its tool result
    // (owner-approved rebaseline, answer verify1-s3-rebaseline A).
    await page.getByText(`E2E final reply to ${SECOND}`, { exact: true }).first().waitFor({ timeout: 30_000 });
    await stopButton().waitFor({ state: "hidden", timeout: 30_000 });
    const folded = page.locator('button[aria-expanded="false"][aria-label^="Process details"]');
    await waitFor(() => folded.count(), 30_000, "the answered turn shows no process-details toggle", 250);
    for (let i = 0; i < 10 && (await folded.count()); i++) await folded.first().click({ timeout: 5_000 });
    const toolOutput = `E2E tool output: ${SECOND}`;
    const toolRow = page.getByRole("button", { name: /read/i }).last();
    await toolRow.waitFor({ state: "visible", timeout: 30_000 });
    if (!(await page.getByText(toolOutput, { exact: true }).count())) await toolRow.click({ timeout: 5_000 });
    await waitFor(
      () => page.getByText(toolOutput, { exact: true }).count(),
      30_000,
      "the chat rendered no tool result row for the turn's Read call",
      500,
    );
    await page.getByText(SECOND, { exact: true }).first().waitFor({ timeout: 10_000 });
    await page.screenshot({ path: join(artifacts, "claude-219-round-trip.png") });
  });

  // S4 — reopening renders history from claude's own jsonl transcript, and no
  // parallel pi session file is stored for the claude session.
  await scenario("S4", async () => {
    assert.ok(sessionId, "prerequisite: S1 did not create a claude session");
    await page.reload({ waitUntil: "domcontentloaded", timeout: 180_000 });
    await page.locator(".chat-input-textarea").waitFor({ state: "visible", timeout: 180_000 });
    await page.getByText(FIRST, { exact: true }).first().waitFor({ timeout: 60_000 });
    await assertReopenedTurn(SECOND);
    const transcript = join(homeDir, ".claude", "projects");
    assert.ok(existsSync(transcript), "the claude transcript directory was never created under the inherited config dir (no CLAUDE_CONFIG_DIR -> ~/.claude)");
    const copied = existsSync(join(agentDir, "sessions"))
      ? readdirSync(join(agentDir, "sessions"), { recursive: true }).filter((name) => String(name).includes(sessionId))
      : [];
    assert.deepEqual(copied, [], "pi-web stored a parallel pi session copy of the claude session's events");
  });

  // S5 — forced real process exit (idle expiry) then reopen: a new child with
  // `--resume <id from the first launch>`, history rendered, conversation
  // continued. The oracle is the stub's recorded argv plus the browser render.
  await scenario("S5", async () => {
    assert.ok(sessionId, "prerequisite: S1 did not create a claude session");
    assert.ok(stubLaunches().length > 0, "no claude child process was ever launched, so the forced-exit-then-resume flow does not exist");
    assert.ok(firstLaunch, "prerequisite: the first claude launch was not recorded by S1");
    if (page) await page.screenshot({ path: join(artifacts, "claude-219-before-exit.png") });
    await context?.close().catch(() => {});
    context = undefined;
    page = undefined;
    const launched = stubLaunches();
    await waitFor(
      () => launched.every((launch) => !pidAlive(launch.pid)),
      45_000,
      "the claude child processes did not exit after the page stopped renewing the lease (idle expiry)",
    );
    const launchesBefore = stubLaunches().length;
    await newPage();
    await openSession();
    await page.getByText(FIRST, { exact: true }).first().waitFor({ timeout: 60_000 });
    const resumed = await waitFor(
      () => stubLaunches().slice(launchesBefore).find((launch) => launch.resumed || argvValue(launch.argv, "--resume")) ?? false,
      45_000,
      "reopening the session started no child with --resume (a fresh conversation was launched)",
    );
    assert.equal(argvValue(resumed.argv, "--resume"), firstLaunch.sessionId, `the resumed child must carry --resume <id from the first launch>: ${resumed.argv.join(" ")}`);
    await assertReopenedTurn(SECOND);
    await sendMessage(THIRD);
    await page.getByText(`E2E final reply to ${THIRD}`, { exact: true }).first().waitFor({ timeout: 60_000 });
    await page.screenshot({ path: join(artifacts, "claude-219-resumed.png") });
  });

  // S6 — rewritten: no dedicated config dir is created, no
  // CLAUDE_CONFIG_DIR is injected, and PI_WEB_PASSWORD/CLAUDECODE stay out
  // while the inherited ANTHROPIC_*/CLAUDE_* environment reaches the child.
  await scenario("S6", async () => {
    assert.ok(firstLaunch, "prerequisite: no claude child process was launched, so its environment cannot be checked");
    assert.ok(!existsSync(claudeDir), `the first claude session created the retired dedicated config dir ${claudeDir}`);
    assert.ok(!existsSync(join(homeDir, ".pi", "agent", "claude")), "the first claude session created ~/.pi/agent/claude");
    assert.equal(argvValue(firstLaunch.argv, "--model"), "claude-sonnet-5-5", "prerequisite: the recorded first launch is not the claude session's child");
    assert.ok(!firstLaunch.envKeys.includes("CLAUDE_CONFIG_DIR"), "the child environment has an injected CLAUDE_CONFIG_DIR although pi-web inherited none");
    assert.equal(firstLaunch.configDir, join(homeDir, ".claude"), `the child's resolved config dir is not its own ~/.claude: ${String(firstLaunch.configDir)}`);
    assert.ok(!firstLaunch.envKeys.includes("PI_WEB_PASSWORD"), "the child environment inherited PI_WEB_PASSWORD");
    assert.ok(!firstLaunch.envKeys.includes("CLAUDECODE"), "the child environment inherited CLAUDECODE");
    assert.ok(firstLaunch.envKeys.includes("ANTHROPIC_API_KEY"), "the child environment lost the inherited ANTHROPIC_API_KEY");
    assert.ok(firstLaunch.envKeys.includes("ANTHROPIC_BASE_URL"), "the child environment lost the inherited ANTHROPIC_BASE_URL");
    assert.ok(firstLaunch.envKeys.includes("CLAUDE_TEST_INHERITED"), "the child environment lost the inherited CLAUDE_* variable");
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  if (page) await page.screenshot({ path: join(artifacts, "failure.png") }).catch(() => {});
  if (context) await context.tracing.stop({ path: join(artifacts, "claude-219-trace.zip") }).catch(() => {});
  await h.close();
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL claude-session-219: ${failures.length} scenario(s) failed`);
  process.exitCode = 1;
}