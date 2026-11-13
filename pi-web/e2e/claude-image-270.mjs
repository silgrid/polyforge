// carries: lib/image-attachments*, lib/__fixtures__/claude-2.1.312/image/*
// — claude image attach: end-to-end acceptance pass.
//
// Authored against artifacts/spec.md's `## Acceptance criteria` and the
// Requirement `#### Scenario` blocks BEFORE the implementation exists. Every
// step drives surface that already exists on the pre-code tree: `POST
// /api/agent/new` (extended by the spec with claude prompt images), GET
// /api/sessions, the fresh composer, an open claude session, the stub child's
// stdin log and claude's transcript. New surfaces are matched only by the
// user-visible facts the spec names (the attach button, the visible format
// refusal, the live/replayed image bubble, no unsupported-image warning), so
// the pre-code run fails at assertion level, never at build level.
//
// The child is the PATH-injected e2e/claude-stub/claude.mjs copied onto PATH.
// Its image mode (e2e-author's sibling step 4) logs each stdin line and
// persists the received image block before it streams `result`; the suite only
// reads those facts, never the stub's source.
//
// Every scenario is individually guarded and prints one `PASS S270-nn` marker.
import assert from "node:assert/strict";
import { readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, claudeScratch, createCheck, createHarness, createWaitUntil, errorText, findTranscript as findTranscriptIn, readJsonLines, root, setupFailed } from "./harness.mjs";

assertNoDevServer();
const { artifacts, scratch, home, agentDir, project, stubDir, stubLog, configDir } = claudeScratch("pi-web-claude-image-270-");
const imageFixture = join(root, "lib/__fixtures__/claude-2.1.312/image");
writeFileSync(join(project, "e2e-probe.txt"), "E2E_TOOL_CONTENT\n");

// the claude models are the aliases claude's initialize answer lists (the stub
// answers with the claude 2.1.322 recording), no longer four pinned full names.
const CLAUDE_MODELS = ["default", "opus", "fable", "sonnet", "haiku"];
const TINY_PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAFgwJ/lK3r9wAAAABJRU5ErkJggg==";
const TINY_PNG_URL = `data:image/png;base64,${TINY_PNG}`;
const ATTACH_LABEL = "Attach files";
const WARNING_TITLE = "Images may not be sent";
const FORMAT_REFUSAL = /Only PNG, JPEG, GIF and WebP images can be attached to a claude session\./;

const h = createHarness();
let browser;
let context;
let page;
let base;
let firstLoad = true;
const failures = [];

const scenario = createCheck({ failures, message: errorText, record: (names, text) => `${names.join(" ")}: ${text}` });

const delayMs = (ms) => delay(ms);

const stubRecords = () => readJsonLines(stubLog);
const stubLaunches = () => stubRecords().filter((entry) => entry.kind === "launch");
function stubStdinEvents() {
  return stubRecords()
    .filter((entry) => entry.kind === "stdin" && typeof entry.line === "string")
    .flatMap((entry) => {
      try {
        return [JSON.parse(entry.line)];
      } catch {
        return [];
      }
    });
}

const waitFor = createWaitUntil();

async function getJson(pathname) {
  const response = await fetch(`${base}${pathname}`, { signal: AbortSignal.timeout(60_000) });
  assert.equal(response.status, 200, `GET ${pathname} -> ${response.status}`);
  return response.json();
}

async function postJson(pathname, body, timeout = 60_000) {
  const response = await fetch(`${base}${pathname}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(timeout),
  });
  return { status: response.status, body: await response.json().catch(() => ({})) };
}

async function createClaudeSession(message, model = "claude-haiku-5-5") {
  const created = await postJson("/api/agent/new", {
    cwd: project, type: "prompt", message, harness: "claude", model,
  });
  assert.equal(created.status, 200, `POST /api/agent/new (harness claude) -> ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  const id = created.body.sessionId ?? created.body.id;
  assert.ok(typeof id === "string" && id.length > 0, "the create route returned no claude session id");
  return id;
}

async function pageReady() {
  await page.locator(".chat-input-textarea").waitFor({ state: "visible", timeout: NAV_MS });
}

async function openFreshComposer() {
  await page.goto(`${base}/?cwd=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded", timeout: firstLoad ? COLD_MS : NAV_MS });
  firstLoad = false;
  await pageReady();
}

async function openSession(id) {
  await page.goto(`${base}/?session=${encodeURIComponent(id)}`, { waitUntil: "domcontentloaded", timeout: firstLoad ? COLD_MS : NAV_MS });
  firstLoad = false;
  await pageReady();
}

const findTranscript = (id) => findTranscriptIn(configDir, id);

const bottomBar = () => page.locator(".chat-input-controls");
const composer = () => page.locator(".chat-input-shell");
const modelTrigger = () => page.locator(".chat-input-controls .model-selector > button");
const attachButton = () => composer().locator(`button[title="${ATTACH_LABEL}"]`);

async function closeMenu() {
  await page.keyboard.press("Escape").catch(() => {});
  await page.locator('[role="listbox"]').last().waitFor({ state: "detached", timeout: 5_000 }).catch(() => {});
}

async function modelMenu() {
  const openMenu = async () => {
    await modelTrigger().click({ timeout: ACTION_MS });
    const listbox = page.locator('[role="listbox"]').last();
    try {
      await listbox.waitFor({ state: "visible", timeout: 3_000 });
      return listbox;
    } catch {
      await modelTrigger().click({ timeout: ACTION_MS });
      await listbox.waitFor({ state: "visible", timeout: 10_000 });
      return listbox;
    }
  };
  const listbox = await openMenu();
  const texts = (await listbox.locator('[role="option"]').allInnerTexts()).map((text) => text.trim()).filter(Boolean);
  return { listbox, texts };
}

async function clickOption(listbox, name) {
  const options = listbox.locator('[role="option"]');
  const rows = [];
  for (let i = 0, n = await options.count(); i < n; i += 1) {
    const option = options.nth(i);
    const label = (await option.innerText()).trim();
    rows.push(label);
    if (label.toLowerCase() === name.toLowerCase()) {
      await option.click({ timeout: ACTION_MS });
      return;
    }
  }
  assert.ok(false, `no "${name}" row in the selector (rows: ${rows.join(", ")})`);
}

async function tryHarnessTrigger(trigger) {
  try {
    await trigger.click({ timeout: 5_000 });
    const listbox = page.locator('[role="listbox"]').last();
    await listbox.waitFor({ state: "visible", timeout: 5_000 });
    const texts = (await listbox.locator('[role="option"]').allInnerTexts()).map((text) => text.trim().toLowerCase());
    if (texts.includes("pi") && texts.includes("claude")) return { trigger, listbox };
  } catch {
    // not this control
  }
  await closeMenu();
  return null;
}

async function harnessControl() {
  const bar = bottomBar();
  const candidates = bar.locator('button[aria-haspopup="listbox"]');
  for (let i = 0, n = await candidates.count(); i < n; i += 1) {
    const candidate = candidates.nth(i);
    if (await candidate.evaluate((element) => Boolean(element.closest(".model-selector")))) continue;
    const found = await tryHarnessTrigger(candidate);
    if (found) return found;
  }
  return null;
}

async function chooseHarness(name) {
  const control = await harnessControl();
  assert.ok(control, "the composer's bottom bar offers no harness selector");
  await clickOption(control.listbox, name);
  await page.locator('[role="listbox"]').last().waitFor({ state: "detached", timeout: 5_000 }).catch(() => {});
}

async function chooseClaudeModel(name) {
  const menu = await modelMenu();
  await clickOption(menu.listbox, name);
  await closeMenu();
}

async function chooseClaudeHarness() {
  await chooseHarness("claude");
  // Wait for the harness switch to commit before the composer is touched: a
  // paste dispatched into the outgoing render would attach to a discarded one.
  await page.getByText(/Uses the system claude/i).first().waitFor({ state: "visible", timeout: 10_000 });
  await delayMs(250);
}

const previews = () => composer().locator('img[src^="blob:"]');

function imageFilePayload({ name = "e2e-270.png", type = "image/png", base64 = TINY_PNG, bytes = null } = {}) {
  return { name, type, encoded: (bytes ?? Buffer.from(base64, "base64")).toString("base64") };
}

async function pasteImage(payload = {}) {
  const data = imageFilePayload(payload);
  await page.evaluate(({ name, type, encoded }) => {
    const bytes = Uint8Array.from(atob(encoded), (char) => char.charCodeAt(0));
    const file = new File([bytes], name, { type });
    const transfer = new DataTransfer();
    transfer.items.add(file);
    const textarea = document.querySelector(".chat-input-textarea");
    const event = new Event("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(event, "clipboardData", { value: transfer });
    textarea.dispatchEvent(event);
  }, { name: data.name, type: data.type, encoded: data.encoded });
}

async function dropImage(payload = {}) {
  const data = imageFilePayload(payload);
  await page.evaluate(({ name, type, encoded }) => {
    const bytes = Uint8Array.from(atob(encoded), (char) => char.charCodeAt(0));
    const file = new File([bytes], name, { type });
    const transfer = new DataTransfer();
    transfer.items.add(file);
    const target = document.querySelector(".chat-content");
    for (const kind of ["dragenter", "dragover", "drop"]) {
      const event = new DragEvent(kind, { bubbles: true, cancelable: true, dataTransfer: transfer });
      target.dispatchEvent(event);
    }
  }, { name: data.name, type: data.type, encoded: data.encoded });
}

async function attachThroughPicker(payload = {}) {
  const data = imageFilePayload(payload);
  const [chooser] = await Promise.all([
    page.waitForEvent("filechooser", { timeout: 10_000 }),
    attachButton().click({ timeout: ACTION_MS }),
  ]);
  await chooser.setFiles({
    name: data.name,
    mimeType: data.type,
    buffer: Buffer.from(data.encoded, "base64"),
  });
}

function userContentOf(event) {
  const content = event?.message?.content;
  return Array.isArray(content) ? content : null;
}
const imageBlocks = (event) => (userContentOf(event) ?? []).filter((block) => block?.type === "image");

async function sendMessage(text) {
  const textarea = page.locator(".chat-input-textarea");
  if (text) await textarea.fill(text);
  await page.getByRole("button", { name: "Send", exact: true }).click({ timeout: ACTION_MS });
}

async function openSidebar() {
  const show = page.locator('button[aria-label="Show sidebar"], button[aria-label="显示侧边栏"]').first();
  if (await show.isVisible().catch(() => false)) await show.click();
}

h.trapInterrupt();

try {
  ({ base } = await h.startServer({
    name: "claude-image-270",
    logFile: join(artifacts, "claude-image-270-server.log"),
    env: {
      HOME: home,
      PI_CODING_AGENT_DIR: agentDir,
      CLAUDE_CONFIG_DIR: configDir,
      PI_WEB_ALLOWED_ROOT_PREFIXES: scratch,
      PI_WEB_IDLE_TIMEOUT_MS: "12000",
      PATH: `${stubDir}:${process.env.PATH ?? ""}`,
      PI_E2E_CLAUDE_STUB_LOG: stubLog,
      PI_E2E_CLAUDE_STUB_MODE: "image",
      PI_E2E_CLAUDE_IMAGE_FIXTURE: imageFixture,
      PI_E2E_CLAUDE_INITIALIZE_FIXTURE: join(root, "lib/__fixtures__/claude-2.1.322/initialize"),
    },
  }));

  const { chromium } = await import("playwright");
  browser = await h.launchBrowser(() => chromium.launch());
  context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US", baseURL: base });
  await context.addInitScript(() => {
    try {
      window.localStorage.setItem("pi-web:split-view-enabled", "false");
      window.localStorage.setItem("pi-web:session-age-filter-days", "0");
    } catch { /* storage unavailable */ }
  });
  // A pi model known not to accept image input: pre-change, this is the model
  // the composer warns about even after choosing claude (the S270-07 red).
  await context.route(/\/api\/models(?:\?|$)/, (route) => route.fulfill({
    json: {
      models: { "e2e/model-0": "Model 0" },
      modelList: [{ provider: "e2e", id: "model-0", name: "Model 0", input: ["text"] }],
      defaultModel: { provider: "e2e", modelId: "model-0" },
      defaultThinkingLevel: "medium",
      savedDefaultThinkingLevel: null,
      thinkingLevels: {},
      thinkingLevelMaps: {},
      thinkingLevelPins: {},
    },
  }));
  page = await context.newPage();
  page.setDefaultTimeout(ACTION_MS);
  await context.tracing.start({ screenshots: true, snapshots: true });

  // S270-01 — acceptance: paste, the attach button and drag-drop all attach in
  // a fresh claude composer and in an open claude session; none is hidden.
  await scenario("S270-01", "Claude image entry points attach in a fresh composer and an open session", async () => {
    await openFreshComposer();
    await chooseClaudeHarness();
    await pasteImage();
    await waitFor(async () => (await previews().count()) >= 1, 10_000, "a paste attached no image in a fresh claude composer");
    await dropImage();
    await waitFor(async () => (await previews().count()) >= 2, 10_000, "a drag-drop attached no image in a fresh claude composer");
    await attachButton().waitFor({ state: "visible", timeout: 10_000 });
    await attachThroughPicker();
    await waitFor(async () => (await previews().count()) >= 3, 10_000, "the attach button's file picker attached no image in a fresh claude composer");

    const id = await createClaudeSession("E2E 270 entry points open session");
    await openSession(id);
    await pasteImage();
    await waitFor(async () => (await previews().count()) >= 1, 10_000, "a paste attached no image in an open claude session");
    await dropImage();
    await waitFor(async () => (await previews().count()) >= 2, 10_000, "a drag-drop attached no image in an open claude session");
    await attachButton().waitFor({ state: "visible", timeout: 10_000 });
    await attachThroughPicker();
    await waitFor(async () => (await previews().count()) >= 3, 10_000, "the attach button's file picker attached no image in an open claude session");
  });

  // S270-02 — acceptance: the child receives the composer's exact base64 and
  // media type inside the same user message as the text.
  await scenario("S270-02", "A sent claude image reaches the child unchanged", async () => {
    const id = await createClaudeSession("E2E 270 exact stdin start");
    await openSession(id);
    await pasteImage();
    await waitFor(async () => (await previews().count()) >= 1, 10_000, "the image did not attach to the open claude composer");
    const text = "E2E 270 exact stdin";
    await sendMessage(text);
    const event = await waitFor(
      () => stubStdinEvents().find((entry) => (userContentOf(entry) ?? []).some((block) => block?.type === "text" && block.text === text)) ?? false,
      30_000,
      "the claude child's stdin log holds no user line for the sent prompt (the image never reached the child)",
    );
    const content = userContentOf(event);
    assert.ok(content, `the stdin user line's content is not an array: ${JSON.stringify(event).slice(0, 400)}`);
    const images = imageBlocks(event);
    assert.equal(images.length, 1, `the stdin user line carries ${images.length} image blocks, not the one attached`);
    const image = images[0];
    assert.equal(image.source?.type, "base64", `the stdin image block is not base64: ${JSON.stringify(image).slice(0, 300)}`);
    assert.equal(image.source?.media_type, "image/png", `the stdin image media type is not the composer's image/png: ${image.source?.media_type}`);
    assert.equal(image.source?.data, TINY_PNG, "the stdin image base64 differs from the composer's image bytes");
    assert.ok(content.some((block) => block?.type === "text" && block.text === text), "the image block is not in the same user message as the prompt text");
  });

  // S270-03 — acceptance: a format outside the claude whitelist is not attached
  // and a visible refusal names the accepted formats.
  await scenario("S270-03", "A non-whitelisted claude image is refused visibly", async () => {
    await openFreshComposer();
    await chooseClaudeHarness();
    const bmp = { name: "e2e-270.bmp", type: "image/bmp", bytes: Buffer.from("BMfake-bmp-image", "utf8") };
    await pasteImage(bmp);
    await delayMs(1_000);
    assert.equal(await previews().count(), 0, "a BMP image was attached to a claude composer");
    const refusal = await page.getByText(FORMAT_REFUSAL).count();
    assert.ok(refusal >= 1, "a non-whitelisted image in a claude composer produced no visible format refusal naming PNG/JPEG/GIF/WebP");
  });

  // S270-04 — acceptance: the agent API refuses invalid claude prompt images
  // with 400 + a reason before spawning a child or writing stdin.
  await scenario("S270-04", "The agent API refuses invalid claude images before stdin", async () => {
    const eleven = Array.from({ length: 11 }, () => ({ type: "image", data: TINY_PNG, mimeType: "image/png" }));
    const oversize = Buffer.alloc(10 * 1024 * 1024 + 1, 1);
    const cases = [
      { label: "invalid base64", images: [{ type: "image", data: "@@@not-base64@@@", mimeType: "image/png" }] },
      { label: "eleven images", images: eleven },
      { label: "an image over 10 MiB", images: [{ type: "image", data: oversize.toString("base64"), mimeType: "image/png" }] },
      { label: "a non-whitelisted media type", images: [{ type: "image", data: Buffer.from("<svg></svg>", "utf8").toString("base64"), mimeType: "image/svg+xml" }] },
    ];
    for (const [index, item] of cases.entries()) {
      const message = `E2E 270 invalid ${index}`;
      const beforeLaunch = stubLaunches().length;
      const response = await postJson("/api/agent/new", {
        cwd: project,
        type: "prompt",
        message,
        harness: "claude",
        model: "claude-haiku-5-5",
        images: item.images,
      }, 120_000);
      assert.equal(response.status, 400, `${item.label}: POST /api/agent/new -> ${response.status} ${JSON.stringify(response.body).slice(0, 300)}`);
      assert.ok(typeof response.body.error === "string" && response.body.error.length > 0, `${item.label}: the 400 carries no reason`);
      await delayMs(500);
      assert.equal(stubLaunches().length, beforeLaunch, `${item.label}: the refused prompt launched a claude child`);
      assert.equal(
        stubStdinEvents().filter((event) => (userContentOf(event) ?? []).some((block) => block?.type === "text" && block.text === message)).length,
        0,
        `${item.label}: the refused prompt still wrote a user line to the claude child's stdin`,
      );
    }
  });

  // S270-05 — acceptance: an image-only claude send starts a turn and the index
  // and sidebar title carry the locale-independent literal [Image].
  await scenario("S270-05", "An image-only claude send starts a turn with the [Image] title", async () => {
    await openFreshComposer();
    await chooseClaudeHarness();
    await pasteImage();
    await waitFor(async () => (await previews().count()) >= 1, 10_000, "the image did not attach to the fresh claude composer");
    assert.equal((await page.locator(".chat-input-textarea").inputValue()).trim(), "", "the image-only send fixture left text in the composer");
    await sendMessage("");
    await delayMs(2_000);
    assert.equal(await page.getByText(/A claude prompt needs text/i).count(), 0, "an image-only claude send was refused with \"A claude prompt needs text\"");
    const listed = await waitFor(async () => {
      const list = await getJson("/api/sessions?summary=1");
      return (list.sessions ?? []).find((session) => session.cwd === project
        && session.harness === "claude"
        && session.firstMessage === "[Image]") ?? false;
    }, 30_000, "no claude session with the index firstMessage [Image] appeared after an image-only send");
    await openSession(listed.id);
    await openSidebar();
    await page.locator('[title="[Image]"]').first().waitFor({ state: "attached", timeout: 30_000 });
  });

  // S270-06 — acceptance: the live user bubble shows the image immediately and
  // a reload replays it from claude's transcript; claude's hidden isMeta
  // companion still renders nothing.
  await scenario("S270-06", "A sent claude image shows live and again after reload", async () => {
    const id = await createClaudeSession("E2E 270 replay start");
    await openSession(id);
    await pasteImage();
    await waitFor(async () => (await previews().count()) >= 1, 10_000, "the image did not attach to the open claude composer");
    await sendMessage("E2E 270 replay image");
    const live = page.locator(`.chat-content img[src="${TINY_PNG_URL}"]`);
    await live.first().waitFor({ state: "visible", timeout: 30_000 });
    const transcript = await waitFor(
      () => {
        const file = findTranscript(id);
        return file && readFileSync(file, "utf8").includes(TINY_PNG) ? file : false;
      },
      30_000,
      "claude's transcript never persisted the received image block",
    );
    assert.ok(readFileSync(transcript, "utf8").includes("isMeta"), "the stub's transcript carries no hidden [Image: source: …] companion to hide");
    await page.reload({ waitUntil: "domcontentloaded", timeout: NAV_MS });
    await pageReady();
    await page.locator(`.chat-content img[src="${TINY_PNG_URL}"]`).first().waitFor({ state: "visible", timeout: 30_000 });
    assert.equal(await page.getByText(/\[Image: source:/).count(), 0, "claude's hidden [Image: source: …] companion rendered in the history");
  });

  // S270-07 — acceptance: none of claude's models (its aliases,) shows the
  // unsupported-image warning for an attached whitelisted image.
  await scenario("S270-07", "No unsupported-image warning for any claude model", async () => {
    await openFreshComposer();
    await chooseClaudeHarness();
    await pasteImage();
    await waitFor(async () => (await previews().count()) >= 1, 10_000, "the whitelisted image did not attach");
    for (const model of CLAUDE_MODELS) {
      await chooseClaudeModel(model);
      await delayMs(300);
      assert.equal(await page.getByText(WARNING_TITLE, { exact: true }).count(), 0, `${model}: the "images may not be sent" warning is shown for a claude model`);
    }
  });
} catch (error) {
  setupFailed(failures, error);
} finally {
  if (page) await page.screenshot({ path: join(artifacts, "claude-image-270-failure.png") }).catch(() => {});
  if (context) await context.tracing.stop({ path: join(artifacts, "claude-image-270-trace.zip") }).catch(() => {});
  await h.close();
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL claude-image-270: ${failures.length} scenario(s) failed`);
  process.exitCode = 1;
}