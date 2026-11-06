// carries: lib/__fixtures__/claude-2.1.322/edit/*, e2e/claude-stub/claude.mjs
// AC4 — a claude session's Edit card shows claude's diff, and claude's API
// retry shows pi's retry banner: browser e2e.
//
// The child is the PATH-injected e2e/claude-stub/claude.mjs in its `edit` mode: a user
// line replays the real claude 2.1.322 `edit/` recording (lib/__fixtures__/claude-2.1.322/
// README.md: one Edit, one replace_all Edit and a Write update, each result carrying
// claude's `structuredPatch` receipt), with the recording's session id and cwd replaced by
// the child's, and appends the replayed tool results to claude's transcript as claude
// writes them. A `retry: ` line is the stub's synthesised answer, which first sends
// claude's `api_retry` frame for a rate-limited request (attempt 1 of 10, 429) and holds
// the retry's 1.5 s. The session is created empty (`ensure_session`) so both turns are
// sent from the page and seen live; a reload reads the Edit from claude's transcript.
//
// components/ChatInput.tsx (the banner's renderer) is not declared: a ninth suite for the
// composer would push the light selection past 8 (lib/e2e-carries.test.mjs).
//
// Each scenario prints one `PASS S444-E2E-nn` marker; the suite ends with
// `PASS claude-live-444` only when every scenario passed. Every wait polls its condition
// to a bounded deadline.
import assert from "node:assert/strict";
import { readFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, claudeEnv, claudeScratch, createApi, createCheck, createHarness, createWaitUntil, errorText, readJsonLines, root, seedStorage, setupFailed } from "./harness.mjs";

assertNoDevServer();
const kit = claudeScratch("pi-web-claude-live-444-");
const { artifacts, scratch, project, stubLog } = kit;

const editFixture = join(root, "lib/__fixtures__/claude-2.1.322/edit");
/** The recording's own user text: what the page sends, so the replay and the prompt agree. */
const recordedPrompt = readFileSync(join(editFixture, "stdin.jsonl"), "utf8").split("\n").filter(Boolean)
  .map((line) => JSON.parse(line)).find((line) => line.type === "user").message.content;
/** The first Edit's recorded change (notes.txt line 8) and its file, under this child's cwd. */
const EDIT = { removed: "the middle line", added: "the MIDDLE line", file: join(project, "notes.txt") };
const RETRY_PROMPT = "retry: hi";
const RETRY_REPLY = `E2E final reply to ${RETRY_PROMPT}`;
const RETRY_BANNER = "Retrying (1/10)…";
const RETRY_REASON = "rate_limit (429)";

const h = createHarness();
let browser;
let context;
let page;
let base;
let firstLoad = true;
const failures = [];

const scenario = createCheck({ failures, message: errorText, record: (names, text) => `${names.join(" ")}: ${text}` });
const waitFor = createWaitUntil({ every: 200, withLast: true });
const { apiFetch } = createApi(() => base);
const stubRecords = () => readJsonLines(stubLog);

async function isRunning(id) {
  const response = await apiFetch(`/api/agent/${encodeURIComponent(id)}`);
  if (response.status !== 200) return false;
  const body = await response.json();
  return body.running === true && body.state?.isStreaming === true;
}
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
  await page.getByRole("button", { name: "Send", exact: true }).first().click({ timeout: ACTION_MS });
}

/** The first `Edit` card's header button (the card's name is exactly `Edit`). */
const editHeader = () => page.locator("button[aria-expanded]").filter({ has: page.locator("span", { hasText: /^Edit$/ }) }).first();
/** The header's card: the bordered shell around the header row and the expanded body. */
const editCard = () => editHeader().locator("xpath=../..");

/**
 * What the expanded card shows: its text, and the diff's removed (red) and added (green)
 * line texts — the cells drawn in the diff's tones, not the input rows' plain text.
 */
const cardView = () => editCard().evaluate((card) => {
  const tone = (element) => getComputedStyle(element).backgroundColor.replace(/\s+/g, "");
  const cells = (rgb) => [...card.querySelectorAll("div")]
    .filter((element) => tone(element).startsWith(`rgba(${rgb},`))
    .map((element) => element.innerText.replace(/\s+/g, " ").trim());
  return { text: card.innerText, removed: cells("248,113,113"), added: cells("34,197,94") };
});

/** Expands the Edit card (once it holds its result) and polls for the diff and the input rows. */
async function assertEditCard(where) {
  await waitFor(async () => {
    if ((await editHeader().count()) > 0) return true;
    // A finished turn folds its steps ("Process details · 5 messages · 5 tool calls"): open the Edit turn's.
    const folded = page.locator('button[aria-expanded="false"][aria-label^="Process details"][aria-label*="5 tool calls"]').first();
    if ((await folded.count()) > 0) await folded.click({ timeout: ACTION_MS });
    return false;
  }, NAV_MS, `${where}: no Edit card`);
  if ((await editHeader().getAttribute("aria-expanded")) !== "true") await editHeader().click({ timeout: ACTION_MS });
  const view = await waitFor(async () => {
    const seen = await cardView();
    const ok = seen.removed.some((text) => text.includes(EDIT.removed))
      && seen.added.some((text) => text.includes(EDIT.added))
      && seen.text.includes("file_path")
      && seen.text.includes(EDIT.file);
    return ok ? seen : false;
  }, NAV_MS, `${where}: the expanded Edit card never showed claude's diff (- ${EDIT.removed} / + ${EDIT.added}) and its file_path row`);
  assert.ok(!view.removed.some((text) => text.includes(EDIT.added)), `${where}: the added line is drawn as removed`);
}

h.trapInterrupt();

try {
  ({ base } = await h.startServer({
    name: "claude-live-444",
    logFile: join(artifacts, "claude-live-444-server.log"),
    env: claudeEnv(kit, {
      PI_E2E_CLAUDE_STUB_MODE: "edit",
      PI_E2E_CLAUDE_EDIT_FIXTURE: editFixture,
      // The product default: only the suite ends a child.
      PI_WEB_IDLE_TIMEOUT_MS: "600000",
    }),
  }));

  const { chromium } = await import("playwright");
  browser = await h.launchBrowser(() => chromium.launch());
  context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US", baseURL: base });
  await seedStorage(context);
  page = await context.newPage();
  page.setDefaultTimeout(ACTION_MS);

  const id = await createEmptyClaudeSession();

  // S444-E2E-01 — AC1/AC4: claude's recorded Edit shows claude's diff under its input rows,
  // live and again from claude's transcript after a reload.
  await scenario("S444-E2E-01", "a claude Edit card shows claude's diff", async () => {
    await openSession(id);
    await send(recordedPrompt);
    await assertEditCard("live");
    await waitFor(async () => !(await isRunning(id)), 60_000, "the replayed turn never ended");
    await openSession(id);
    await assertEditCard("history");
  });

  // S444-E2E-02 — AC2/AC4: claude's API retry is pi's retry banner while claude waits; it
  // goes once the retried request streams.
  await scenario("S444-E2E-02", "claude's API retry shows pi's retry banner until the reply streams, then goes", async () => {
    await openSession(id);
    await waitFor(async () => !(await isRunning(id)), 60_000, "the session is still running before the retry turn");
    await send(RETRY_PROMPT);
    const banner = () => page.locator(".chat-input-shell div").filter({ hasText: RETRY_BANNER }).filter({ hasText: RETRY_REASON });
    await waitFor(async () => (await banner().count()) > 0, 30_000, `the composer never showed "${RETRY_BANNER} — ${RETRY_REASON}"`);
    await waitFor(async () => (await page.locator(".markdown-body").allInnerTexts()).some((text) => text.includes(RETRY_REPLY)), 60_000, "the retried reply never streamed");
    await waitFor(async () => (await banner().count()) === 0 && (await page.getByText(RETRY_BANNER).count()) === 0, 30_000, "the retry banner stayed after the reply streamed");
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  if (page && failures.length) await page.screenshot({ path: join(artifacts, "claude-live-444-failure.png") }).catch(() => {});
  await h.close();
  for (const entry of stubRecords().filter((record) => record.kind === "launch")) {
    try { process.kill(entry.pid, "SIGKILL"); } catch { /* already gone */ }
  }
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL claude-live-444: ${failures.length} scenario(s) failed`);
  process.exitCode = 1;
} else {
  console.log("PASS claude-live-444");
}
