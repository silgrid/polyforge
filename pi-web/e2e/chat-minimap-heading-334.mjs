// carries: components/ChatMinimap*, components/MessageView*, components/MarkdownBody*, components/SafeMarkdownBody*
// repro: a click on a minimap heading row whose target heading is
// not in the answer element at click time is dropped with no retry and no
// feedback (ChatMinimap.scrollToHeading's silent `return`).
//
// The real app path is mounted: `next dev`, Chromium at 1280x900, and a session
// fixture whose single turn answers with a markdown body holding two headings.
// S1 is the positive control — a heading-row click with the heading present
// lands it at 30% of the viewport (the assertion e2e/run.mjs already makes).
// S2 reproduces the drop: the heading is taken out of the answer element over
// the click while the preview still lists it, and put back RETRY_RESTORE_MS
// after the click was dispatched. That is the state a stale preview or a late
// layout pass leaves behind, made deterministic here instead of load-dependent.
// The click must not be dropped: the preview closing proves the click reached
// the handler, and the jump must still land the (re-appeared) heading within
// JUMP_DEADLINE_MS. Old code returns silently at the missing heading, never
// scrolls and never tells the user.
//
//   node e2e/chat-minimap-heading-334.mjs
//   npm run test:e2e:all -- chat-minimap-heading-334.mjs
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { startNextServer } from "./next-server.mjs";
import { ACTION_MS, COLD_MS, NAV_MS } from "./timeouts.mjs";
import { launchBrowser, softCheck } from "./harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const distDir = process.env.PI_WEB_DIST_DIR || ".next/e2e/chat-minimap-heading-334";
const artifacts = join(root, "test-results/e2e");
mkdirSync(artifacts, { recursive: true });
rmSync(join(root, distDir, "dev/lock"), { force: true });

// The jump may retry a bounded time before it lands; the assertion stays a
// bounded wait on the real geometry.
const JUMP_DEADLINE_MS = 10_000;
// How long the heading stays out of the answer element past the click: a
// retry loop with a sub-second cadence finds it, a silent return never does.
const RETRY_RESTORE_MS = 200;

const nextEnvPath = join(root, "next-env.d.ts");
const nextEnvBefore = existsSync(nextEnvPath) ? readFileSync(nextEnvPath, "utf8") : null;

const failures = [];
const check = softCheck(failures);

// ---- fixture ----------------------------------------------------------------
const agentDir = mkdtempSync(join(tmpdir(), "pi-web-minimap-heading-"));
const project = join(agentDir, "project");
const sessionDir = join(agentDir, "sessions", "e2e");
mkdirSync(project);
mkdirSync(sessionDir, { recursive: true });

const timestamp = "2026-08-23T00:00:00.000Z";
const SESSION = "e2e-minimap-heading-334";
function message(id, parentId, role, content) {
  return { type: "message", id, parentId, timestamp, message: { role, content } };
}
const paragraphs = (count, lead) => Array.from(
  { length: count },
  (_, index) => `${lead} paragraph ${index + 1}. ${"Filler sentence for scroll height. ".repeat(3)}`,
).join("\n\n");
// One heading, with enough body above and below that scrolling it to 30% of
// the viewport is inside the scroll range. One heading keeps the preview row
// and the answer element's heading list at the same single index: with a second
// heading below, removing the first would still leave an element at index 0 and
// the click would jump to the wrong heading instead of being dropped.
const answerText = [
  paragraphs(30, "Above"),
  "## E2E minimap heading one",
  "",
  paragraphs(60, "Below"),
].join("\n\n");
const entries = [
  message("prompt", null, "user", "E2E minimap heading prompt"),
  message("answer", "prompt", "assistant", [{ type: "text", text: answerText }]),
];
writeFileSync(
  join(sessionDir, `2026-08-23T00-00-00-000Z_${SESSION}.jsonl`),
  [{ type: "session", version: 3, id: SESSION, timestamp, cwd: project }, ...entries]
    .map((entry) => JSON.stringify(entry)).join("\n") + "\n",
);

// ---- next dev + Chromium ----------------------------------------------------
let started;
let browser;
let page;
try {
  started = await startNextServer({
    name: "chat-minimap-heading-334",
    logFile: join(artifacts, "chat-minimap-heading-334-server.log"),
    env: {
      ...process.env,
      PI_CODING_AGENT_DIR: agentDir,
      PI_WEB_PASSWORD: "",
      NEXT_TELEMETRY_DISABLED: "1",
      PI_WEB_ALLOWED_ROOT_PREFIXES: agentDir,
      PI_WEB_DIST_DIR: distDir,
      PI_WEB_DISABLE_MCP: "1",
    },
  });

  browser = await launchBrowser(() => chromium.launch());
  page = await browser.newPage({ viewport: { width: 1280, height: 900 }, locale: "en-US" });
  page.setDefaultTimeout(ACTION_MS);

  const answerRow = page.locator("[data-entry-id='answer']");
  const pageHeading = answerRow.getByRole("heading", { name: "E2E minimap heading one", exact: true });

  const scrollTop = () => page.evaluate(() => {
    const row = document.querySelector("[data-entry-id='answer']");
    return row?.closest(".overflow-y-auto")?.scrollTop ?? null;
  });
  const headingOffset = () => page.evaluate(() => {
    const heading = document.querySelector("[data-entry-id='answer'] h2");
    const scroll = heading?.closest(".overflow-y-auto");
    return heading && scroll
      ? heading.getBoundingClientRect().top - scroll.getBoundingClientRect().top - scroll.clientHeight * 0.3
      : null;
  });
  const scrollToTop = () => page.evaluate(() => {
    const scroll = document.querySelector("[data-entry-id='answer']")?.closest(".overflow-y-auto");
    if (scroll) scroll.scrollTop = 0;
  });
  async function waitForLanding(deadlineMs) {
    const deadline = Date.now() + deadlineMs;
    let offset = await headingOffset();
    while (!(offset !== null && Math.abs(offset) < 5) && Date.now() < deadline) {
      await delay(100);
      offset = await headingOffset();
    }
    return offset;
  }
  // The rail mousedown opens the preview and jumps the rail node's turn in with
  // a smooth scroll; wait until the real scroll position stops moving.
  async function settleScroll(timeoutMs = 3_000) {
    const deadline = Date.now() + timeoutMs;
    let last = await scrollTop();
    let stable = 0;
    while (Date.now() < deadline && stable < 3) {
      await delay(100);
      const current = await scrollTop();
      stable = current === last ? stable + 1 : 0;
      last = current;
    }
    return last;
  }
  async function openPreview() {
    const node = page.locator("[data-minimap-node-index='0']");
    const rect = await node.boundingBox();
    assert.ok(rect, "minimap node 0 has a box");
    await page.mouse.click(rect.x + rect.width / 2, rect.y + rect.height / 2);
    const preview = page.locator("[data-minimap-preview-box]");
    await preview.waitFor({ state: "attached" });
    await settleScroll();
    const row = preview.locator("[data-preview-heading-index='0']");
    await row.waitFor({ state: "attached" });
    assert.equal((await row.innerText()).trim(), "E2E minimap heading one",
      "the first preview heading row is the answer's first heading");
    return { preview, row };
  }
  const resetView = async () => {
    await scrollToTop();
    await delay(400);
  };

  await page.goto(`${started.base}/?session=${SESSION}`, { waitUntil: "domcontentloaded", timeout: COLD_MS });
  await pageHeading.waitFor({ state: "attached", timeout: NAV_MS });
  await page.locator("[data-minimap-node-index='0']").waitFor({ timeout: NAV_MS });

  // S1 — positive control: with the heading present, the row click lands it.
  await resetView();
  {
    const { preview, row } = await openPreview();
    await row.click();
    await preview.waitFor({ state: "detached" });
    const offset = await waitForLanding(JUMP_DEADLINE_MS);
    check(offset !== null && Math.abs(offset) < 5,
      `S1 control: the heading row click must land the heading at 30% of the chat viewport (offset ${offset}px)`);
    console.log(`RESULT S1: heading offset after the click ${offset}px (accepted within 5px)`);
    await page.screenshot({ path: join(artifacts, "chat-minimap-heading-334-control.png") });
  }

  // S2 — the reported failure: the heading is missing over the click and comes
  // back a moment later; the click must not be dropped.
  await resetView();
  {
    const { preview, row } = await openPreview();
    const before = await scrollTop();
    const removed = await page.evaluate(() => {
      const heading = document.querySelector("[data-entry-id='answer'] h2");
      if (!heading) return false;
      const parent = heading.parentNode;
      const next = heading.nextSibling;
      heading.remove();
      window.__w334RestoreHeading = () => {
        parent.insertBefore(heading, next);
        delete window.__w334RestoreHeading;
      };
      return true;
    });
    check(removed, "S2: the answer heading was in the DOM before the click");
    await row.click();
    await delay(RETRY_RESTORE_MS);
    const restored = await page.evaluate(() => {
      const restore = window.__w334RestoreHeading;
      if (typeof restore !== "function") return false;
      restore();
      return true;
    });
    check(restored, `S2: the heading was put back ${RETRY_RESTORE_MS}ms after the click`);
    // A swallowed click (a re-render replacing the button between mousedown and
    // mouseup) would leave the preview open; it closing proves the click landed
    // in the handler, so a missing jump is the silent drop itself.
    await preview.waitFor({ state: "detached" });
    const offset = await waitForLanding(JUMP_DEADLINE_MS);
    const after = await scrollTop();
    console.log(`RESULT S2: scrollTop before ${before}, after ${after}; heading offset ${offset}px`);
    check(offset !== null && Math.abs(offset) < 5,
      `S2: a heading row click whose heading is missing at click time must still land the jump within ${JUMP_DEADLINE_MS}ms of the heading returning (offset ${offset}px, scrollTop ${before} -> ${after})`);
    await page.screenshot({ path: join(artifacts, "chat-minimap-heading-334-repro.png") });
  }
} catch (error) {
  failures.push(`suite error: ${error?.stack ?? String(error)}`);
  if (page) await page.screenshot({ path: join(artifacts, "chat-minimap-heading-334-failure.png") }).catch(() => {});
} finally {
  await browser?.close().catch(() => {});
  if (started) await started.stop().catch(() => {});
  rmSync(agentDir, { recursive: true, force: true });
  if (nextEnvBefore !== null) {
    try {
      if (readFileSync(nextEnvPath, "utf8") !== nextEnvBefore) writeFileSync(nextEnvPath, nextEnvBefore);
    } catch { /* next-env.d.ts unavailable after the run */ }
  } else if (existsSync(nextEnvPath)) {
    rmSync(nextEnvPath, { force: true });
  }
}

if (failures.length) {
  for (const failure of failures) console.error(`FAIL: ${failure}`);
  assert.fail(`${failures.length} failure(s) reproduced`);
}
console.log("PASS: a minimap heading-row click is not dropped when its heading is momentarily missing");