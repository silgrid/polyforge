// carries: components/ChatWindow*, components/ChatInput*, components/MessageView*, hooks/useAgentSession*, lib/agent-*, lib/stream-client*, lib/session-*, app/api/agent/*, app/api/sessions/*
// carries: app/page.tsx, app/layout.tsx, app/globals.css, components/AppShell*, components/SessionSidebar*, components/SplitPaneLayout*, components/PaneHeader*, components/TabBar*, components/MarkdownBody*, components/FileViewer*, components/FileExplorer*, components/ModelSelector*, components/ModelContextWindow*, components/FontSettings*
// carries: lib/pane-state*, lib/pane-tab-state*, lib/tab-order*, lib/model-*, lib/font-preferences*, lib/markdown*, hooks/useChatAppearance*, hooks/useFontPreferences*, app/api/files/*, app/api/models/*
// Adapted from @Nuctori's script-style CI/E2E fixtures in PR #617 (issue #599).
import assert from "node:assert/strict";
import { appendFileSync, existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { launchBrowser } from "./harness.mjs";
import { startNextServer } from "./next-server.mjs";
import { checkFilePanel, checkLargeHtmlStorage, filePanelFixture, largeStorageFixture } from "./file-panel.mjs";
import { checkExtensionDialogSizing, checkExtensionDialogs, extensionSource } from "./extension-dialog.mjs";
import { checkChatAppearance } from "./chat-appearance.mjs";
import { checkCodeBackground } from "./code-background.mjs";
import { checkSplitPane } from "./split-pane-pass.mjs";
import { checkModelDiscovery } from "./model-discovery.mjs";
import { checkModelContext } from "./model-context.mjs";
import { checkModelSelector } from "./model-selector.mjs";
import { checkArchive } from "./archive.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const mode = process.env.E2E_SERVER_MODE || "dev";
assert.ok(mode === "dev" || mode === "start", "E2E_SERVER_MODE must be dev or start");
assert.ok(mode !== "dev" || !existsSync(join(root, process.env.PI_WEB_DIST_DIR || ".next", "dev/lock")), "Use a checkout without an active dev server");
const artifacts = join(root, "test-results/e2e");
mkdirSync(artifacts, { recursive: true });
const agentDir = mkdtempSync(join(tmpdir(), "pi-web-e2e-"));
const project = join(agentDir, "project");
const sessionDir = join(agentDir, "sessions", "e2e");
mkdirSync(project);
const previewFile = join(project, "preview.html");
writeFileSync(previewFile, filePanelFixture);
const largeStorageFile = join(project, "large-storage.html");
writeFileSync(largeStorageFile, largeStorageFixture);
mkdirSync(sessionDir, { recursive: true });
const timestamp = "2026-08-23T00:00:00.000Z";
const LONG = "e2e-long-session";
const BRANCH = "e2e-branch-session";
const RICH = "e2e-rich-session";
const COMPACTED = "e2e-compacted-session";
const APPEND = "e2e-external-append-session";
const CODE_BACKGROUND = "e2e-code-background";
// (#232 ledger): a session that scrolls (so the minimap rail
// renders) and holds every turn on its first page (`turnsBefore` 0).
const ZERO = "e2e-minimap-zero-session";
const zeroText = (i) => `E2E zero message ${String(i).padStart(2, "0")}`;
// wi_tN1cVXKf1VIr (sidebar A1: Archive) e2e fixtures: two non-running
// sessions in two distinct projects so the archive pass can verify
// project grouping and external-delete pruning. Authored pre-code against
// artifacts/spec.md; see e2e/archive.mjs.
const ARCHIVE_ALPHA = "e2e-archive-alpha";
const ARCHIVE_BETA = "e2e-archive-beta";
const text = (i) => `E2E message ${String(i).padStart(4, "0")}`;
const ids = (start, end) => Array.from({ length: end - start }, (_, i) => `e${start + i}`);

function message(id, parentId, role, content) {
  return { type: "message", id, parentId, timestamp, message: { role, content } };
}

function writeSession(id, entries, cwd = project) {
  const header = { type: "session", version: 3, id, timestamp, cwd };
  writeFileSync(join(sessionDir, `2026-08-23T00-00-00-000Z_${id}.jsonl`),
    [header, ...entries].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
}


// (upstream #791 client half): authored pre-code against
// artifacts/spec.md. Each scenario is isolated, logs one marker line when it
// passes, and keeps running after a failure so the baseline has one piece of
// evidence per scenario. The 5,000-entry fixture's active branch has 2,500
// user turns; its newest page (e4950..e4999) holds the last 25 of them, so
// 2,475 turns are unloaded and the turn asking text(4998) is turn 2,500.
async function checkMinimapTurns({ browser, base }) {
  const context = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    locale: "en-US",
    baseURL: base,
  });
  await context.addInitScript(() => {
    try {
      window.localStorage.setItem("pi-web:session-age-filter-days", "0");
      window.localStorage.setItem("pi-web:split-view-enabled", "false");
    } catch {}
  });

  const openSession = async (page, id, readyText) => {
    await page.goto(`${base}/?session=${id}`, { waitUntil: "domcontentloaded" });
    await page.getByText(readyText, { exact: true }).waitFor();
    await page.locator("[data-minimap-node-index='0']").waitFor();
  };

  const openPreviewAt = async (page, nodeIndex) => {
    const node = page.locator(`[data-minimap-node-index='${nodeIndex}']`);
    await node.waitFor();
    const rect = await node.boundingBox();
    assert.ok(rect, `minimap node ${nodeIndex} has a box`);
    await page.mouse.click(rect.x + rect.width / 2, rect.y + rect.height / 2);
    const preview = page.locator("[data-minimap-preview-box]");
    await preview.waitFor({ state: "attached", timeout: 10_000 });
    return preview;
  };

  const readPreview = async (preview) => {
    const turn = preview.locator("[data-minimap-preview-index]").filter({ hasText: text(4998) });
    await turn.waitFor({ state: "attached", timeout: 10_000 });
    const earlier = preview.locator("[data-minimap-preview-earlier]");
    const earlierRows = await earlier.count();
    return {
      number: await turn.locator("span[aria-hidden='true']").first().textContent(),
      earlierRows,
      earlier: earlierRows === 1 ? await earlier.textContent() : null,
    };
  };

  const readTurnNumber = async (preview, messageText) => {
    const turn = preview.locator("[data-minimap-preview-index]").filter({ hasText: messageText });
    await turn.waitFor({ state: "attached", timeout: 10_000 });
    return turn.locator("span[aria-hidden='true']").first().textContent();
  };

  // Numbering and the earlier count move when a page commits and the hook
  // settles; poll the real preview state instead of measuring once (the same
  // load-sensitive rule the suite already applies to prepend anchoring).
  const readPreviewUntil = async (preview, expected, timeout = 5_000) => {
    const deadline = Date.now() + timeout;
    let observed = await readPreview(preview);
    while (Date.now() < deadline) {
      observed = await readPreview(preview);
      if (observed.number === expected.number
        && observed.earlierRows === expected.earlierRows
        && observed.earlier === expected.earlier) break;
      await delay(100);
    }
    return observed;
  };

  const scrollTopOfChat = (page) => page.evaluate(() => {
    const sentinel = Array.from(document.querySelectorAll("div")).find((element) =>
      element.textContent === "Scroll up to load earlier messages" && element.children.length === 0);
    let node = sentinel;
    let container = null;
    while (node) {
      const style = getComputedStyle(node);
      if ((style.overflowY === "auto" || style.overflowY === "scroll") && node.scrollHeight > node.clientHeight) {
        container = node;
        break;
      }
      node = node.parentElement;
    }
    return container ? container.scrollTop : null;
  });

  const run = async (id, title, body) => {
    const page = await context.newPage();
    page.setDefaultTimeout(30_000);
    try {
      await body(page);
      console.log(`PASS ${id}`);
    } catch (error) {
      console.error(`FAIL ${id} (${title}): ${error?.message ?? error}`);
      process.exitCode = 1;
    } finally {
      await page.close().catch(() => {});
    }
  };

  try {
    await run(
      "S1",
      "First page numbers turn 2500, reports 2475 earlier turns, and opens the preview by click",
      async (page) => {
        await openSession(page, LONG, text(4999));
        const node = page.locator("[data-minimap-node-index='0']");
        const rect = await node.boundingBox();
        assert.ok(rect);
        const nodeCenter = { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };

        await page.mouse.move(nodeCenter.x, nodeCenter.y);
        assert.equal(await page.locator("[data-minimap-preview-box]").count(), 0,
          "hovering the rail must not open the preview");

        // Clicking node 0 jumps the chat to the first loaded turn, which can put
        // the top sentinel in view and load a page before the first-page values
        // are read (grill-qa round 2). Hold that one request until the read is
        // done, so the assertion observes the first page, not the paged one.
        let release;
        const heldPage = new Promise((resolve) => { release = resolve; });
        let held = false;
        const contextRoute = `**/api/sessions/${LONG}/context?*`;
        await page.route(contextRoute, async (route) => {
          if (!held && new URL(route.request().url()).searchParams.has("before")) {
            held = true;
            await heldPage;
          }
          await route.continue().catch(() => {});
        });
        try {
          await page.mouse.click(nodeCenter.x, nodeCenter.y);
          const preview = page.locator("[data-minimap-preview-box]");
          await preview.waitFor({ state: "attached", timeout: 10_000 });
          const expectedFirstPage = {
            number: "2500",
            earlierRows: 1,
            earlier: "Earlier turns not loaded: 2475. Click to load",
          };
          assert.deepEqual(await readPreviewUntil(preview, expectedFirstPage), expectedFirstPage,
            "the first page must number from the branch's first turn and report the unloaded count");
        } finally {
          release();
          await page.unroute(contextRoute).catch(() => {});
        }
      },
    );

    await run(
      "S2",
      "Loading older pages keeps turn 2500 and lowers the earlier-turns count",
      async (page) => {
        await openSession(page, LONG, text(4999));
        const sentinel = page.getByText("Scroll up to load earlier messages", { exact: true });
        await sentinel.waitFor({ state: "attached" });
        const olderResponses = [];
        page.on("response", (response) => {
          const url = new URL(response.url());
          if (url.pathname === `/api/sessions/${LONG}/context` && url.searchParams.has("before")) {
            olderResponses.push(response);
          }
        });
        for (let turn = 0; turn < 2; turn++) {
          const responsePromise = page.waitForResponse((response) =>
            new URL(response.url()).pathname === `/api/sessions/${LONG}/context`);
          await sentinel.evaluate((element) => element.scrollIntoView({ block: "start", behavior: "instant" }));
          const response = await responsePromise;
          const older = (await response.json()).context;
          const firstMessage = older.messages.find((message) => message.role === "user")?.content;
          assert.equal(typeof firstMessage, "string", "Older page must contain user messages");
          await page.getByText(firstMessage, { exact: true }).waitFor({ state: "attached" });
          await page.getByText(text(4999), { exact: true })
            .evaluate((element) => element.scrollIntoView({ block: "end", behavior: "instant" }));
        }
        assert.equal(olderResponses.length, 2, "two older pages must have loaded");
        const oldest = Number(new URL(olderResponses.at(-1).url()).searchParams.get("before")?.slice(1)) - 50;
        const nodes = page.locator("[data-minimap-node-index]");
        const nodeCount = await nodes.count();
        assert.ok(nodeCount > 0);
        const preview = await openPreviewAt(page, nodeCount - 1);
        const expectedPaged = {
          number: "2500",
          earlierRows: 1,
          earlier: `Earlier turns not loaded: ${oldest / 2}. Click to load`,
        };
        assert.deepEqual(await readPreviewUntil(preview, expectedPaged), expectedPaged,
          "loading older pages must not renumber minimap turns");
      },
    );

    await run(
      "S3",
      "Clicking the earlier-turns row scrolls to the top, loads one page, keeps numbers, and closes the preview",
      async (page) => {
        await openSession(page, LONG, text(4999));
        const sentinel = page.getByText("Scroll up to load earlier messages", { exact: true });
        await sentinel.waitFor({ state: "attached" });
        const olderResponses = [];
        page.on("response", (response) => {
          const url = new URL(response.url());
          if (url.pathname === `/api/sessions/${LONG}/context` && url.searchParams.has("before")) {
            olderResponses.push(response);
          }
        });
        const nodes = page.locator("[data-minimap-node-index]");
        const nodeCount = await nodes.count();
        const preview = await openPreviewAt(page, nodeCount - 1);
        const before = await readPreview(preview);
        assert.equal(before.number, "2500", "the first page starts at turn 2500");
        assert.equal(before.earlier, "Earlier turns not loaded: 2475. Click to load");
        const firstLoadedBefore = await readTurnNumber(preview, text(4950));

        const earlierRow = preview.locator("[data-minimap-preview-earlier]");
        await earlierRow.waitFor({ state: "attached", timeout: 5_000 });
        const olderPage = page.waitForResponse((response) => {
          const url = new URL(response.url());
          return url.pathname === `/api/sessions/${LONG}/context` && url.searchParams.has("before");
        });
        // Owner answer e2e-s3-s4-authored-defects (S3=A): the click moves the
        // chat toward the top through the sentinel; the existing prepend
        // anchoring then keeps the previous first row in view, so scrollTop is
        // not asserted to stay 0 after the page lands.
        const startTop = await scrollTopOfChat(page);
        await page.evaluate(() => {
          const sentinel = Array.from(document.querySelectorAll("div")).find((element) =>
            element.textContent === "Scroll up to load earlier messages" && element.children.length === 0);
          let node = sentinel;
          while (node) {
            const style = getComputedStyle(node);
            if ((style.overflowY === "auto" || style.overflowY === "scroll") && node.scrollHeight > node.clientHeight) break;
            node = node.parentElement;
          }
          window.__w232MinScrollTop = node ? node.scrollTop : null;
          node?.addEventListener("scroll", () => {
            window.__w232MinScrollTop = Math.min(window.__w232MinScrollTop, node.scrollTop);
          });
        });
        await earlierRow.click();
        await preview.waitFor({ state: "detached", timeout: 10_000 });
        const response = await olderPage;
        const older = (await response.json()).context;
        assert.equal(older.turnsBefore, 2450, "one older page leaves 2,450 unloaded turns");
        const firstMessage = older.messages.find((message) => message.role === "user")?.content;
        assert.equal(typeof firstMessage, "string");
        await page.getByText(firstMessage, { exact: true }).waitFor({ state: "attached" });

        const minTop = await page.evaluate(() => window.__w232MinScrollTop);
        assert.ok(typeof startTop === "number" && startTop > 0, "the chat starts below the top");
        assert.ok(typeof minTop === "number" && minTop < startTop,
          "clicking the earlier row must move the chat toward the top (through the sentinel)");

        const loaded = olderResponses.length;
        await delay(500);
        assert.equal(olderResponses.length, loaded, "click-to-top must load exactly one older page");
        assert.equal(loaded, 1, "click-to-top must load one older page");

        const reopened = await openPreviewAt(page, nodeCount - 1);
        const expectedAfterTop = {
          number: "2500",
          earlierRows: 1,
          earlier: "Earlier turns not loaded: 2450. Click to load",
        };
        assert.deepEqual(await readPreviewUntil(reopened, expectedAfterTop), expectedAfterTop,
          "loaded turn numbers must stay while the unloaded count drops");
        assert.equal(await readTurnNumber(reopened, text(4950)), firstLoadedBefore,
          "the already-loaded first turn must keep its number");
      },
    );

    await run(
      "S4",
      "The rail stub and earlier-turns row appear only while earlier turns are unloaded",
      async (page) => {
        await openSession(page, LONG, text(4999));
        const stub = page.locator("[data-minimap-earlier]");
        assert.equal(await stub.count(), 1, "a dotted stub must sit above the first rail node");
        const nodes = page.locator("[data-minimap-node-index]");
        assert.equal(await nodes.count(), 25, "no placeholder node may be drawn for an unloaded turn");
        const stubRect = await stub.boundingBox();
        const firstNodeRect = await nodes.first().boundingBox();
        assert.ok(stubRect && firstNodeRect);
        assert.ok(stubRect.y + stubRect.height <= firstNodeRect.y + firstNodeRect.height / 2 + 1,
          "the stub must sit above the first node");

        // (#232 ledger): the zero gate runs where the rail
        // renders — the ZERO session scrolls and its first page holds all 20
        // turns (`turnsBefore` 0) — so its absence assertions are not vacuous.
        await openSession(page, ZERO, zeroText(39));
        const zeroNodes = page.locator("[data-minimap-node-index]");
        assert.equal(await zeroNodes.count(), 20, "the zero session's rail renders every turn");
        assert.equal(await page.locator("[data-minimap-earlier]").count(), 0,
          "zero unloaded turns must show no stub");
        const zeroPreview = await openPreviewAt(page, 0);
        assert.equal(Number(await readTurnNumber(zeroPreview, zeroText(0))), 1, "with no earlier turn the first turn is turn 1");
        assert.equal(await zeroPreview.locator("[data-minimap-preview-earlier]").count(), 0,
          "zero unloaded turns must show no earlier-turns row");
      },
    );
  } finally {
    await context.close().catch(() => {});
  }
}

let server;
let stopServer = async () => {};
let browser;
let page;
let context;
const interrupt = () => {
  process.exitCode = 1;
  server?.kill("SIGTERM");
  void browser?.close().catch(() => {});
};
process.once("SIGINT", interrupt);
process.once("SIGTERM", interrupt);

try {
  // Seed before startup so the first catalogue scan sees every fixture.
  mkdirSync(join(agentDir, "extensions"));
  writeFileSync(join(agentDir, "extensions", "e2e-dialog.js"), extensionSource);
  const longEntries = Array.from({ length: 5000 }, (_, i) =>
    message(`e${i}`, i ? `e${i - 1}` : null, i % 2 ? "assistant" : "user", text(i)));
  longEntries.splice(1, 0, message("alternate", "e0", "user", "E2E alternate history branch"));
  writeSession(LONG, longEntries);
  writeSession(ZERO, Array.from({ length: 40 }, (_, i) =>
    message(`z${i}`, i ? `z${i - 1}` : null, i % 2 ? "assistant" : "user",
      i % 2 ? `${zeroText(i)}\n\n${"E2E zero answer paragraph.\n\n".repeat(3)}` : zeroText(i))));
  writeSession(BRANCH, [
    message("root", null, "user", "Branch root"),
    message("old", "root", "assistant", "Inactive branch answer"),
    message("new", "root", "assistant", "Active branch answer"),
  ]);
  const toolResult = message("result", "call", "toolResult", [{ type: "text", text: "E2E tool output" }]);
  Object.assign(toolResult.message, { toolCallId: "t1", toolName: "bash", isError: false });
  const richEntries = [
    message("user", null, "user", "Render **E2E markdown**"),
    message("call", "user", "assistant", [
      { type: "thinking", thinking: "" },
      { type: "thinking", thinking: "E2E intermediate reasoning\nIntermediate thinking details." },
      { type: "text", text: "E2E process paragraph.\n\n".repeat(20) },
      { type: "toolCall", id: "t1", name: "bash", arguments: { command: "echo E2E tool output" } },
    ]),
    toolResult,
    message("answer", "result", "assistant", [
      { type: "thinking", thinking: "" },
      { type: "thinking", thinking: "E2E follow-up reasoning\nFollow-up thinking details." },
      { type: "text", text: "E2E process note" },
      { type: "thinking", thinking: "E2E final reasoning\nFinal thinking details." },
      { type: "text", text:
        "E2E final answer\n```js\nconsole.log('E2E code');\n```\n\n"
        + "| Kind | " + "Long headings should wrap without stretching the table. ".repeat(8) + " |\n| --- | --- |\n"
        + "| Text | " + "Long sentences should wrap while wide tables keep their natural column widths. ".repeat(8) + " |\n"
        + "| 中文 | " + "表格中的长句应该自然换行，多列表格仍然可以横向滚动。".repeat(12) + " |\n"
        + "| URL | https://example.invalid/" + "long-path-segment".repeat(24) + " |\n"
        + "| Code | `" + "long_identifier_".repeat(24) + "` |\n\n"
        + "| " + Array.from({ length: 8 }, (_, i) => `Column ${i + 1}`).join(" | ") + " |\n"
        + "| " + Array(8).fill("---").join(" | ") + " |\n"
        + "| " + Array.from({ length: 8 }, (_, i) => `release-20260901-${String(i + 1).padStart(6, "0")}`).join(" | ") + " |\n\n"
        + "E2E answer paragraph.\n\n".repeat(20)
        + "## E2E reading position\n\n"
        + "E2E answer paragraph.\n\n".repeat(20),
      },
    ]),
  ];
  Object.assign(richEntries.at(-1).message, { provider: "test", model: "E2E Model" });
  writeSession(RICH, richEntries);
  writeSession(CODE_BACKGROUND, [
    message("user", null, "user", "E2E code background"),
    message("answer", "user", "assistant", [{ type: "text", text:
      "```js\nconst x = 1;\n```\n\n```mermaid\ngraph TD\n A --> B\n```",
    }]),
  ]);
  // The default page is 50 *visible* messages (user / assistant / compaction).
  // toolResults ride along free after #810, so 48 tool-call assistants + the
  // final answer + the divider fill that window; the user prompt is the 51st
  // visible entry and must stay outside the first page.
  const compactedEntries = [
    message("user", null, "user", "E2E prompt outside the compacted page"),
    { type: "compaction", id: "compact", parentId: "user", timestamp, summary: "E2E compaction anchor", firstKeptEntryId: "user", tokensBefore: 100 },
  ];
  for (let i = 0; i < 48; i++) {
    compactedEntries.push(message(`call${i}`, compactedEntries.at(-1).id, "assistant", [
      { type: "toolCall", id: `t${i}`, name: "bash", arguments: { command: `echo step${i}` } },
    ]));
    const result = message(`result${i}`, `call${i}`, "toolResult", [{ type: "text", text: `step${i}` }]);
    Object.assign(result.message, { toolCallId: `t${i}`, toolName: "bash", isError: false });
    compactedEntries.push(result);
  }
  compactedEntries.push(message("answer", "result47", "assistant", [{ type: "text", text:
    "E2E compacted answer paragraph.\n\n".repeat(20)
    + "## E2E compacted heading\n\n"
    + "E2E compacted answer paragraph.\n\n".repeat(20),
  }]));
  writeSession(COMPACTED, compactedEntries);
  writeSession(APPEND, [
    message("root", null, "user", "E2E wrapper root"),
    message("reply", "root", "assistant", "E2E wrapper reply"),
  ]);
  const archiveProjectA = join(agentDir, "alpha-project");
  const archiveProjectB = join(agentDir, "beta-project");
  mkdirSync(archiveProjectA);
  mkdirSync(archiveProjectB);
  writeSession(ARCHIVE_ALPHA, [
    message("root", null, "user", "E2E archive alpha"),
    message("reply", "root", "assistant", "E2E archive alpha reply"),
  ], archiveProjectA);
  writeSession(ARCHIVE_BETA, [
    message("root", null, "user", "E2E archive beta"),
    message("reply", "root", "assistant", "E2E archive beta reply"),
  ], archiveProjectB);

  const started = await startNextServer({
    name: "run",
    logFile: join(artifacts, "server.log"),
    env: {
      ...process.env,
      PI_CODING_AGENT_DIR: agentDir,
      PI_WEB_PASSWORD: "",
      NEXT_TELEMETRY_DISABLED: "1",
      // The e2e workspace lives under os.tmpdir(), which the registrable-root
      // guard does NOT trust by default (only homedir + operator prefixes).
      // Register the e2e scratch tree as an operator prefix — the exact
      // operator escape hatch the guard ships for (pi#55).
      PI_WEB_ALLOWED_ROOT_PREFIXES: agentDir,
    },
  });
  server = started.server;
  stopServer = started.stop;
  const base = started.base;

  async function api(path, status = 200) {
    const response = await fetch(`${base}${path}`, { signal: AbortSignal.timeout(30_000) });
    assert.equal(response.status, status, path);
    return response.json();
  }

  async function post(path, body) {
    const response = await fetch(`${base}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(60_000),
    });
    assert.ok(response.ok, `POST ${path} -> ${response.status}`);
    return response.json();
  }

  const { sessions } = await api("/api/sessions");
  assert.deepEqual(sessions.map((session) => session.id).sort(), [LONG, BRANCH, RICH, COMPACTED, APPEND, CODE_BACKGROUND, ARCHIVE_ALPHA, ARCHIVE_BETA, ZERO].sort());

  const detail = await api(`/api/sessions/${LONG}?deferThinking=1&deferMedia=1`);
  assert.deepEqual(detail.context.entryIds, ids(4950, 5000));
  assert.equal(detail.context.messages.length, 50);
  assert.equal(detail.context.hasMore, true);
  assert.ok(JSON.stringify(detail).length < 100_000, "Detail transferred unbounded history");
  const tail = await api(`/api/sessions/${LONG}/context?tail=50`);
  assert.deepEqual(tail.context.entryIds, ids(4950, 5000));
  assert.equal(tail.context.messages.length, 50);
  const selectedBranch = await api(`/api/sessions/${BRANCH}/context?leafId=old`);
  assert.deepEqual(selectedBranch.context.entryIds, ["root", "old"]);
  const rootPage = await api(`/api/sessions/${BRANCH}/context?before=old&tail=1`);
  assert.deepEqual(rootPage.context.entryIds, ["root"]);
  assert.equal(rootPage.context.hasMore, false);
  const beforeRoot = await api(`/api/sessions/${BRANCH}/context?before=root`);
  assert.deepEqual(beforeRoot.context.entryIds, []);
  assert.equal(beforeRoot.context.hasMore, false);
  await api("/api/sessions/e2e-does-not-exist", 404);
  await api("/api/files/..%2F..%2Fetc%2Fpasswd?type=read", 403);
  const compacted = await api(`/api/sessions/${COMPACTED}`);
  assert.equal(compacted.context.entryIds[0], "compact");
  assert.equal(compacted.context.messages.some((entry) => entry.role === "user"), false);
  console.log("PASS: bounded history, branch context, pagination root, and API errors");

  // #632 regression: a live wrapper shadows the session file. Ordinary reads
  // keep that snapshot (two processes writing one JSONL is unsupported). A
  // mount/refresh GET (?force=1) must see the external append and stay stable.
  {
    const file = join(sessionDir, `2026-08-23T00-00-00-000Z_${APPEND}.jsonl`);
    await post(`/api/agent/${APPEND}`, { type: "get_state" });
    const beforeAppend = await api(`/api/sessions/${APPEND}`);
    assert.deepEqual(beforeAppend.context.entryIds, ["root", "reply"], "the wrapper must serve its own snapshot first");
    appendFileSync(file, `${JSON.stringify(message("external", "reply", "assistant", "E2E external append"))}\n`);
    const ordinaryRead = await api(`/api/sessions/${APPEND}`);
    assert.deepEqual(ordinaryRead.context.entryIds, ["root", "reply"], "post-turn reads must not probe disk");
    assert.equal(ordinaryRead.wrapperRebuilt, undefined);
    const afterForce = await api(`/api/sessions/${APPEND}?force=1`);
    assert.deepEqual(afterForce.context.entryIds, ["root", "reply", "external"], "a mount/refresh read must see the external append");
    assert.equal(afterForce.wrapperRebuilt, true);
    const again = await api(`/api/sessions/${APPEND}`);
    assert.deepEqual(again.context.entryIds, ["root", "reply", "external"], "repeated reads must stay stable");
    const appended = await api(`/api/sessions/${APPEND}/context?tail=1`);
    assert.deepEqual(appended.context.entryIds, ["external"], "the appended entry must be readable on its own");
    console.log("PASS: external session-file appends are visible on force/mount reads");
  }

  browser = await launchBrowser(() => chromium.launch());
  await checkMinimapTurns({ browser, base });
  for (const viewport of [{ width: 1280, height: 800 }, { width: 390, height: 844 }]) {
    // pi#102 blocked-round correction (layered by e2e-author after the
    // recovery script): the archive pass drives the UI through a relative
    // page.goto("/"), so the context needs a baseURL — a relative goto
    // without one throws "invalid URL".
    context = await browser.newContext({ viewport, locale: "en-US", baseURL: base });
    // pi#82: the sidebar's age filter defaults to 15 days, and every fixture
    // session carries its real (weeks-old) last-activity timestamp — the
    // filter would legitimately hide them. The age semantics are unit-tested
    // in lib/session-filter.test.mjs; the pass list runs with the filter off.
    await context.addInitScript(() => {
      try { window.localStorage.setItem("pi-web:session-age-filter-days", "0"); } catch {}
    });
    await context.tracing.start({ screenshots: true, snapshots: true });
    page = await context.newPage();
    page.setDefaultTimeout(30_000);
    const errors = [];
    const olderResponses = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("console", (event) => { if (event.type() === "error") errors.push(event.text()); });
    page.on("response", (response) => {
      if (response.url().startsWith(base) && response.status() >= 500) errors.push(`${response.status()} ${response.url()}`);
      const url = new URL(response.url());
      if (url.pathname === `/api/sessions/${LONG}/context` && url.searchParams.has("before")) olderResponses.push(response);
    });
    if (viewport.width > 600) {
      // pi#27: tab mode is the default. Fresh entry (no ?session, nothing
      // restorable) lands on the focused new-session tab — assert it before
      // the classic flows seed their split-view disable below.
      await page.goto(`${base}/`, { waitUntil: "domcontentloaded" });
      await page.locator("[data-split-pane-area]").waitFor();
      const entryTabs = page.getByRole("tab");
      assert.equal(await entryTabs.count(), 1, "fresh entry lands on the new-session tab (default-on split)");
      assert.equal(await entryTabs.first().getAttribute("aria-selected"), "true",
        "the entry new-session tab is focused");
      await page.locator(".chat-input-textarea").waitFor({ state: "visible" });
      // Classic flows below assume the single-chat layout: seed the
      // persisted split-view disable so they keep running classic.
      await page.evaluate(() => {
        try { localStorage.setItem("pi-web:split-view-enabled", "false"); } catch {}
      });
    }
    const stateReady = page.waitForResponse((response) => new URL(response.url()).pathname === `/api/sessions/${LONG}/state`);
    await page.goto(`${base}/?session=${LONG}`, { waitUntil: "domcontentloaded" });
    assert.equal((await stateReady).status(), 200);
    await page.getByText(text(4999), { exact: true }).waitFor();
    const latestUser = await page.getByText(text(4998), { exact: true }).elementHandle();
    assert.ok(latestUser, "Latest user message must be mounted before pagination");
    const sentinel = page.getByText("Scroll up to load earlier messages", { exact: true });
    await sentinel.waitFor({ state: "attached" });
    assert.equal(await page.getByText(text(4949), { exact: true }).count(), 0);

    // Exercise the real IntersectionObserver and prepend path, twice.
    for (let turn = 0; turn < 2; turn++) {
      const responsePromise = page.waitForResponse((response) =>
        new URL(response.url()).pathname === `/api/sessions/${LONG}/context`);
      await sentinel.evaluate((element) => element.scrollIntoView({ block: "start", behavior: "instant" }));
      const response = await responsePromise;
      const older = (await response.json()).context;
      const firstMessage = older.messages.find((message) => message.role === "user")?.content;
      assert.equal(typeof firstMessage, "string", "Older page must contain user messages");
      await page.getByText(firstMessage, { exact: true }).waitFor({ state: "attached" });
      await page.getByText(text(4999), { exact: true }).evaluate((element) => element.scrollIntoView({ block: "end", behavior: "instant" }));
    }

    // pi#16 regression: after the upward loads, a prepend must keep the
    // viewport anchored on the message the user was reading (the anchoring
    // restore must run even when visibleCount already exceeds
    // messages.length) and an anchored load must not cascade further pages
    // (the sentinel observer must not re-arm itself per loaded page).
    const scrollState = () => page.evaluate(() => {
      const sentinel = Array.from(document.querySelectorAll("div")).find((element) =>
        element.textContent === "Scroll up to load earlier messages" && element.children.length === 0);
      let node = sentinel;
      let container = null;
      while (node) {
        const style = getComputedStyle(node);
        if ((style.overflowY === "auto" || style.overflowY === "scroll") && node.scrollHeight > node.clientHeight) {
          container = node;
          break;
        }
        node = node.parentElement;
      }
      if (!container) return { err: "no scroll container" };
      return {
        firstRenderedEntryId: container.querySelector("[data-entry-id]")?.dataset.entryId ?? null,
        sentinelInView: sentinel.getBoundingClientRect().bottom > container.getBoundingClientRect().top,
      };
    });
    const entryViewportOffset = (entryId) => page.evaluate((entryId) => {
      const sentinel = Array.from(document.querySelectorAll("div")).find((element) =>
        element.textContent === "Scroll up to load earlier messages" && element.children.length === 0);
      let node = sentinel;
      let container = null;
      while (node) {
        const style = getComputedStyle(node);
        if ((style.overflowY === "auto" || style.overflowY === "scroll") && node.scrollHeight > node.clientHeight) {
          container = node;
          break;
        }
        node = node.parentElement;
      }
      const element = container?.querySelector(`[data-entry-id="${entryId}"]`);
      if (!container || !element) return null;
      return Math.round(element.getBoundingClientRect().top - container.getBoundingClientRect().top);
    }, entryId);
    for (let turn = 0; turn < 2; turn++) {
      const before = await scrollState();
      assert.ok(before.firstRenderedEntryId, "the loaded window must have a top entry before paging up");
      const responsePromise = page.waitForResponse((response) =>
        new URL(response.url()).pathname === `/api/sessions/${LONG}/context`);
      await sentinel.evaluate((element) => element.scrollIntoView({ block: "start", behavior: "instant" }));
      const response = await responsePromise;
      const older = (await response.json()).context;
      const firstMessage = older.messages.find((message) => message.role === "user")?.content;
      await page.getByText(firstMessage, { exact: true }).waitFor({ state: "attached" });
      // The prepend's scroll compensation runs in a layout effect right after
      // the new page commits. On a slow runner the assertion can observe the
      // DOM between commit and compensation, so poll until the anchor settles
      // instead of measuring once (CI flake, pi#56).
      let anchoredOffset = null;
      // Same load-dependent wait: the prepend's passive-effect compensation
      // can be scheduled well after the DOM commit when the default pool
      // shares the host with other suites. Poll the real condition longer;
      // the accepted offset window is unchanged.
      const settleDeadline = Date.now() + 15_000;
      while (Date.now() < settleDeadline) {
        anchoredOffset = await entryViewportOffset(before.firstRenderedEntryId);
        if (anchoredOffset !== null && anchoredOffset >= 0 && anchoredOffset <= 96) break;
        await delay(250);
      }
      assert.ok(anchoredOffset !== null && anchoredOffset >= 0 && anchoredOffset <= 96,
        `viewport must stay anchored on ${before.firstRenderedEntryId} after the prepend (offset ${anchoredOffset})`);
      const anchored = await scrollState();
      assert.equal(anchored.sentinelInView, false,
        "an anchored prepend must move the sentinel above the viewport");
      const cascadedFrom = olderResponses.length;
      await delay(500);
      assert.equal(olderResponses.length, cascadedFrom,
        "an anchored page load must not cascade further page loads");
    }
    assert.deepEqual(await latestUser.evaluate((element) => ({
      connected: element.isConnected,
      text: element.textContent,
    })), { connected: true, text: text(4998) }, "Prepending history must preserve existing message nodes");
    await latestUser.dispose();
    assert.ok(olderResponses.length >= 2, "Scrolling must fetch consecutive older pages");
    let oldest = Number(new URL(olderResponses[0].url()).searchParams.get("before")?.slice(1));
    assert.ok(Number.isInteger(oldest), "Older page must include a numeric before cursor");
    for (const response of olderResponses) {
      assert.equal(response.status(), 200);
      assert.equal(new URL(response.url()).searchParams.get("before"), `e${oldest}`);
      const older = (await response.json()).context;
      assert.deepEqual(older.entryIds, ids(oldest - 50, oldest));
      assert.equal(older.messages.length, 50);
      oldest -= 50;
      assert.equal(older.oldestEntryId, `e${oldest}`);
      assert.equal(older.hasMore, true);
    }
    const rendered = await page.getByText(/^E2E message \d{4}$/).allTextContents();
    // The sidebar also displays the first message as the session title.
    assert.deepEqual(rendered.filter((value) => value !== text(0)),
      Array.from({ length: 5000 - oldest }, (_, i) => text(oldest + i)), "Missing, reordered, or duplicate chat messages");
    await page.screenshot({ path: join(artifacts, `history-${viewport.width}.png`) });

    await page.goto(`${base}/?session=${BRANCH}`, { waitUntil: "domcontentloaded" });
    await page.getByText("Active branch answer", { exact: true }).waitFor();
    assert.equal(await page.getByText("Inactive branch answer", { exact: true }).count(), 0);
    const thinkingRequests = [];
    page.on("request", (request) => {
      if (new URL(request.url()).pathname.endsWith("/thinking")) thinkingRequests.push(request.url());
    });
    await page.goto(`${base}/?session=${RICH}`, { waitUntil: "domcontentloaded" });
    await page.locator("strong").filter({ hasText: "E2E markdown" }).waitFor();
    await page.locator("pre").filter({ hasText: "console.log('E2E code');" }).waitFor();
    await page.getByText("E2E final answer", { exact: true }).waitFor();
    const processDetails = page.getByRole("button", { name: /^Process details/ });
    const thinking = page.getByRole("button", { name: /^Thinking/ });
    assert.equal(await processDetails.count(), 1);
    assert.equal(await thinking.count(), 0, "All thinking stays inside process details");
    const finalMessage = page.locator("[data-entry-id='answer']");
    const tables = finalMessage.last().locator(".markdown-table-wrap");
    assert.equal(await tables.count(), 2);
    await tables.first().scrollIntoViewIfNeeded();
    await page.evaluate(() => document.fonts.ready);
    const tableLayout = await tables.evaluateAll(([prose, wide]) => {
      const wraps = (cell) => {
        const range = document.createRange();
        range.selectNodeContents(cell);
        return range.getBoundingClientRect().height > parseFloat(getComputedStyle(cell).lineHeight);
      };
      return {
        proseFits: prose.scrollWidth <= prose.clientWidth + 1,
        longCellsWrap: [...prose.querySelectorAll("tr > :last-child")].every(wraps),
        wideScrolls: wide.scrollWidth > wide.clientWidth + 1,
        wideCellsStayOnOneLine: [...wide.querySelectorAll("tbody td")].every((cell) => !wraps(cell)),
      };
    });
    assert.ok(tableLayout.proseFits, "Long table cells must not stretch a two-column table beyond the chat");
    assert.ok(tableLayout.longCellsWrap, "Long headings, sentences, URLs, and inline code must wrap");
    assert.ok(tableLayout.wideScrolls, "Wide tables must retain horizontal scrolling");
    assert.ok(tableLayout.wideCellsStayOnOneLine, "Wide tables must not squeeze ordinary values into narrow columns");
    await page.screenshot({ path: join(artifacts, `markdown-tables-${viewport.width}.png`) });
    console.log(`PASS: ${viewport.width}px wrapping long table cells and scrolling wide tables`);
    assert.equal(await finalMessage.getByRole("button", { name: /^Thinking/ }).count(), 0);
    assert.equal(await finalMessage.getByText("test/E2E Model", { exact: true }).count(), 1);
    assert.equal(thinkingRequests.length, 0);
    await processDetails.click();
    assert.equal(await thinking.count(), 3);
    assert.equal(await thinking.last().innerText(), "E2E final reasoning");
    assert.equal(thinkingRequests.length, 0);
    for (const [index, text] of ["Intermediate thinking details.", "Follow-up thinking details.", "Final thinking details."].entries()) {
      await thinking.nth(index).click();
      await page.getByText(text, { exact: false }).waitFor();
    }
    assert.equal(thinkingRequests.length, 3);
    const processText = await processDetails.locator("..").innerText();
    assert.ok(processText.indexOf("E2E intermediate reasoning") < processText.indexOf("echo E2E tool output"));
    assert.ok(processText.indexOf("echo E2E tool output") < processText.indexOf("E2E follow-up reasoning"));
    assert.ok(processText.indexOf("E2E follow-up reasoning") < processText.indexOf("E2E process note"));
    assert.ok(processText.indexOf("E2E process note") < processText.indexOf("E2E final reasoning"));
    assert.equal(processText.split("E2E final reasoning").length - 1, 1);
    assert.ok(!(await finalMessage.last().innerText()).includes("E2E final reasoning"));
    await page.getByText("echo E2E tool output", { exact: true }).waitFor();
    await page.getByRole("button", { name: /bash.*echo E2E tool output/ }).click();
    await page.getByText("E2E tool output", { exact: true }).waitFor();
    await page.goto(`${base}/?session=${COMPACTED}`, { waitUntil: "domcontentloaded" });
    const heading = page.getByRole("heading", { name: "E2E compacted heading", exact: true });
    await heading.waitFor({ state: "attached" });
    if (viewport.width > 600) {
      const node = page.locator("[data-minimap-node-index='0']");
      await node.waitFor();
      const rect = await node.boundingBox();
      assert.ok(rect);
      // pi#19: the preview is click-to-toggle (no hover path) — a single
      // click opens it (and jumps to the clicked node).
      await page.mouse.click(rect.x + rect.width / 2, rect.y + rect.height / 2);
      const preview = page.locator("[data-minimap-preview-box]");
      await preview.getByRole("button", { name: "E2E compaction anchor", exact: true }).waitFor();
      const headingRow = preview.getByRole("button", { name: "E2E compacted heading", exact: true });
      await headingRow.click();
      // The jump is load-sensitive (full bar: the heading-row
      // click was dispatched, yet the chat never settled at 30%). Under load
      // the click can land on a preview whose target was stale (the jump
      // returns silently) or defer to a history reveal that a later layout
      // pass never resolves. Poll the real geometry with a deadline and, when
      // it stays off for a while, re-issue the user action: re-open the
      // preview from the rail node and click the heading row again. The
      // assertion itself (heading at 30% of the viewport, within 5px) is
      // unchanged. Inner Playwright calls carry explicit timeouts so the
      // loop's deadline stays authoritative.
      const headingOffset = () => page.evaluate(() => {
        const heading = document.querySelector("[data-entry-id='answer'] h2");
        const scroll = heading?.closest(".overflow-y-auto");
        return heading && scroll
          ? heading.getBoundingClientRect().top - scroll.getBoundingClientRect().top - scroll.clientHeight * 0.3
          : null;
      });
      const jumpDeadline = Date.now() + 30_000;
      let jumpOffset = await headingOffset();
      let lastJump = Date.now();
      while (!(jumpOffset !== null && Math.abs(jumpOffset) < 5) && Date.now() < jumpDeadline) {
        if (Date.now() - lastJump > 2_000) {
          lastJump = Date.now();
          try {
            if (await preview.count() === 0) {
              const railRect = await node.boundingBox({ timeout: 5_000 });
              if (railRect) await page.mouse.click(railRect.x + railRect.width / 2, railRect.y + railRect.height / 2);
            }
            await headingRow.click({ timeout: 5_000 });
          } catch {
            // The next pass reads the geometry again and re-drives the jump.
          }
        }
        await delay(100);
        jumpOffset = await headingOffset();
      }
      assert.ok(jumpOffset !== null && Math.abs(jumpOffset) < 5,
        `The minimap heading jump must place the heading at 30% of the chat viewport (offset ${jumpOffset}px)`);
      await page.screenshot({ path: join(artifacts, "compaction-minimap.png") });

      const selectSession = async (title, entryId) => {
        await page.locator(`[title="${title}"]`).click();
        await page.locator(`[data-entry-id="${entryId}"]:not([data-message-role])`).waitFor({ state: "visible" });
      };
      const readingOffset = (target) => target.evaluate((element) => (
        element.getBoundingClientRect().top - element.closest(".overflow-y-auto").getBoundingClientRect().top
      ));
      const positionForReading = async (target) => {
        await target.evaluate((element) => {
          const scroll = element.closest(".overflow-y-auto");
          scroll.scrollTop += element.getBoundingClientRect().top - scroll.getBoundingClientRect().top - 120;
        });
        return readingOffset(target);
      };
      await selectSession(text(0), "e4999");
      const olderPage = page.waitForResponse((response) => response.url().includes(`/api/sessions/${LONG}/context?`));
      await page.getByText("Scroll up to load earlier messages", { exact: true }).evaluate((element) => element.scrollIntoView({ block: "start", behavior: "instant" }));
      await olderPage;
      const olderMessage = page.locator("[data-entry-id='e4920']");
      const olderOffset = await positionForReading(olderMessage);
      await selectSession("Render **E2E markdown**", "user");
      const process = page.getByRole("button", { name: /process details/i });
      await process.click();
      const answerHeading = page.getByRole("heading", { name: "E2E reading position", exact: true });
      const answerOffset = await positionForReading(answerHeading);
      await selectSession(text(0), "e4920");
      assert.ok(Math.abs(await readingOffset(olderMessage) - olderOffset) < 5, "Returning to older history must restore its reading offset");
      await selectSession("Render **E2E markdown**", "user");
      assert.equal(await process.getAttribute("aria-expanded"), "false");
      assert.ok(Math.abs(await readingOffset(answerHeading) - answerOffset) < 5, "Collapsing process details on remount must not displace the answer");

      // Hold pagination until a different branch has loaded, exercising effect cancellation.
      let releaseHistory;
      const historyGate = new Promise((resolve) => { releaseHistory = resolve; });
      const contextRoute = `**/api/sessions/${LONG}/context?*`;
      await page.route(contextRoute, async (route) => {
        if (new URL(route.request().url()).searchParams.has("before")) await historyGate;
        await route.continue().catch(() => {});
      });
      // Context loading is real; avoid starting an agent just to persist the test branch.
      const agentRoute = `**/api/agent/${LONG}`;
      await page.route(agentRoute, (route) => route.fulfill({ json: {} }));
      try {
        const pendingHistory = page.waitForRequest((request) => request.url().includes(`/api/sessions/${LONG}/context?`) && new URL(request.url()).searchParams.has("before"));
        await page.locator(`[title="${text(0)}"]`).click();
        await pendingHistory;
        await page.getByRole("button", { name: "Branches", exact: true }).click();
        await page.getByText("E2E alternate history branch", { exact: true }).click();
        await page.locator("[data-entry-id='alternate']:not([data-message-role])").waitFor({ state: "visible" });
        await page.screenshot({ path: join(artifacts, "scroll-restore-branch.png") });
      } finally {
        releaseHistory();
        await page.unroute(contextRoute);
        await page.unroute(agentRoute);
      }
      console.log("PASS: session reading offsets, collapsed process details, and cancelled branch restoration");
      await page.goto(`${base}/?session=${COMPACTED}`, { waitUntil: "domcontentloaded" });
      await heading.waitFor({ state: "visible" });
    }
    await page.goto(`${base}/?session=${RICH}`, { waitUntil: "domcontentloaded" });
    await page.locator(".markdown-code-block pre").waitFor();
    await checkFilePanel(page, previewFile);
    await checkLargeHtmlStorage(page, largeStorageFile);
    await checkExtensionDialogs(page, artifacts, viewport.width);
    await checkExtensionDialogSizing(page, viewport.width);
    if (viewport.width > 600) {
      await page.goto(`${base}/?session=${RICH}`, { waitUntil: "domcontentloaded" });
      await page.locator(".markdown-code-block pre").waitFor();
      await checkChatAppearance(page);
      // pi#9: the opt-in split view pass rides the same desktop-only section;
      // the classic assertions above it are unchanged.
      await checkSplitPane(page, {
        longTitle: text(0),
        compactedTitle: "E2E prompt outside the compacted page",
        longTailText: text(4999),
      });
      await page.setViewportSize(viewport);
      const showSidebar = page.getByRole("button", { name: "Show sidebar", exact: true });
      if (await showSidebar.isVisible()) await showSidebar.click();
      await checkModelDiscovery(page);
    }
    await checkModelContext(page);
    await page.goto(`${base}/?session=${CODE_BACKGROUND}`, { waitUntil: "domcontentloaded" });
    await page.locator(".markdown-code-block pre").waitFor();
    await checkCodeBackground(page);
    await checkModelSelector(page, { base, project, artifacts });
    // wi_tN1cVXKf1VIr: the archive acceptance pass (authored pre-code; see
    // e2e/archive.mjs). Desktop-only, and self-guarding — a failing archive
    // scenario logs FAIL and sets the exit code without aborting the rest
    // of the suite, so the classic passes above still run to completion.
    if (viewport.width > 600) {
      await checkArchive(page, {
        alphaId: ARCHIVE_ALPHA,
        betaId: ARCHIVE_BETA,
        alphaTitle: "E2E archive alpha",
        betaTitle: "E2E archive beta",
        betaFile: join(sessionDir, `2026-08-23T00-00-00-000Z_${ARCHIVE_BETA}.jsonl`),
        alphaProject: "alpha-project",
        betaProject: "beta-project",
        // Fixture-premise correction (grill Batch 1 Q2, ruling b): one
        // project is pre-selected at test start, and every project switch
        // rides the global session search — so the non-archived main-list
        // probe must be a session the search can reach by a unique query:
        // the rich-markdown session in the main e2e project.
        probeTitle: "Render **E2E markdown**",
      });
    }
    assert.deepEqual(errors, [], `Browser errors at width ${viewport.width}`);
    console.log(`PASS: ${viewport.width}px browser pagination, branch, markdown, code, tool call, and compaction navigation`);
    await context.tracing.stop();
    await context.close();
    context = undefined;
    page = undefined;
  }
} catch (error) {
  console.error(error);
  process.exitCode = 1;
  if (page) await page.screenshot({ path: join(artifacts, "failure.png") }).catch(() => {});
  if (context) await context.tracing.stop({ path: join(artifacts, "trace.zip") }).catch(() => {});
} finally {
  await browser?.close().catch(() => {});
  await stopServer();
  rmSync(agentDir, { recursive: true, force: true });
  process.off("SIGINT", interrupt);
  process.off("SIGTERM", interrupt);
}
