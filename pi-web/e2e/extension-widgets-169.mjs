// carries: components/ExtensionStatusBar*, components/ExtensionWidgets*, lib/extension-widgets*
// The chat pane's bottom extension shelf (pi#111,,).
// Standalone: it starts its own dev server with a stub pi extension in a
// temporary PI_CODING_AGENT_DIR. The extension's `/e2e-widgets` command sets
// two widgets, so the run asserts through the real SSE → useAgentSession →
// ExtensionStatusBar path. Each scenario logs PASS/FAIL and the run exits 1 on
// any FAIL. No model credentials needed: an extension command runs without an
// LLM call.
//
// retired the polyforge skill's `pf watch --statusline` widget,
// and with it the shelf's one special case (the exact `polyforge` key used to
// be dropped): a widget of that key now renders like every other (S1).
//
//   node e2e/extension-widgets-169.mjs
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, reportTitled } from "./harness.mjs";

assertNoDevServer();
const work = mkdtempSync(join(tmpdir(), "pi-web-ext-widgets-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
mkdirSync(join(agentDir, "extensions"), { recursive: true });
mkdirSync(join(agentDir, "sessions", "e2e"), { recursive: true });
mkdirSync(project);
const SESSION = "e2e-ext-widgets-session";
// A session of its own for the delayed-snapshot scenario: its wrapper starts
// with no widgets, so the held `/state` reply really is stale.
const SESSION_DELAYED = "e2e-ext-widgets-delayed-session";
const ts = "2026-10-18T00:00:00.000Z";
for (const id of [SESSION, SESSION_DELAYED]) {
  writeFileSync(join(agentDir, "sessions", "e2e", `${id}.jsonl`), [
    { type: "session", version: 3, id, timestamp: ts, cwd: project },
    { type: "message", id: "m1", parentId: null, timestamp: ts, message: { role: "user", content: "E2E ext widgets host session" } },
  ].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
}

// ---- stub extension: a control widget and one under the formerly filtered key ----
writeFileSync(join(agentDir, "extensions", "e2e-widget.js"), `export default function (pi) {
  pi.registerCommand("e2e-widgets", {
    handler: async (_args, ctx) => {
      ctx.ui.setWidget("usage", ["E2E control widget line"]);
      ctx.ui.setWidget("polyforge", ["E2E-PF-KEY widget line"]);
      ctx.ui.notify("E2E widgets set");
    },
  });
};
`);

// ---- scenario bookkeeping ----
const T = {
  S1: "S1: every widget renders in the shelf — the formerly filtered polyforge key too",
  S4: "S4: the same on a mobile viewport",
  S5: "S5: no 5xx responses or browser console errors",
  S6: "S6: a delayed mount /state snapshot does not erase the mobile shelf's widgets",
};
const failures = new Map(Object.values(T).map((title) => [title, []]));
const ran = new Set();
const check = createCheck({ failures: ([title, label], text) => failures.get(title).push(`${label}: ${text}`), before: ([title]) => ran.add(title), pass: null, fail: null });

const h = createHarness({
  storage: { "pi-web:split-view-enabled": "false" },
  // Navigation headroom only: the host is shared and other capsules' e2e
  // servers routinely push a cold page load past this page's 20s action
  // timeout. Assertions below still use the 20s default.
  timeout: 20_000,
  errors: ["pageerror", "console", "5xx"],
  session: SESSION,
  async setup(page, { session, stateGate }) {
    // Deterministic form of S6's race: the mount's snapshot read
    // is held until the widgets command has landed, so its widget section is
    // stale by the time it applies. The gate releases the real response, read
    // before the command ran.
    await page.route((url) => url.pathname === BARRIER, (route) => route.fulfill({ status: 204 }));
    if (!stateGate) return;
    await page.route(`**/api/sessions/${session}/state`, async (route) => {
      stateGate.held += 1;
      try {
        const response = await route.fetch();
        stateGate.reached.resolve();
        await stateGate.release.promise;
        await route.fulfill({ response });
      } catch {
        await route.abort().catch(() => {});
      } finally {
        stateGate.held -= 1;
      }
    });
  },
  // Locale- and viewport-independent readiness: the host session's first
  // message is mounted (the sidebar row is hidden behind the drawer on mobile).
  ready: "E2E ext widgets host session",
});
const { errors, openPage } = h;
const shelf = (page) => page.locator(".extension-status-shelf");

// Sends the stub extension's command and waits for its completion notice.
async function sendWidgetsCommand(page) {
  await page.mouse.move(0, 0);
  await page.locator("[data-minimap-preview-box]").waitFor({ state: "hidden" });
  const input = page.locator("textarea").last();
  await input.fill("/e2e-widgets");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await page.getByText("E2E widgets set", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Stop agent", exact: true }).waitFor({ state: "hidden" });
  // The widget events ride the same SSE stream ahead of the notice, so they
  // have landed once the control widget shows in the shelf (a bounded wait on
  // the real condition).
  await shelf(page).getByText("usage", { exact: true }).waitFor();
}

/**
 * A page-side ordering barrier instead of a wall-clock grace window (the
 * e2e/machine-load.mjs pattern): the page fetches a stubbed
 * marker URL, then waits two animation frames. Every request the page issued
 * before the marker has reached its route by then, and a response the page
 * received earlier has been applied and rendered.
 */
const BARRIER = "/__e2e_request_barrier";
async function requestBarrier(page) {
  await page.evaluate(async (url) => {
    await fetch(url, { cache: "no-store" });
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  }, BARRIER);
}
const waitFor = createWaitFor({ ms: 20_000, every: 50, rethrow: true, describe: (label) => `timed out: ${label}` });

async function assertShelfShowsBoth(page, title) {
  await shelf(page).waitFor();
  await check(title, "the shelf shows both widget keys", async () => {
    const keys = await shelf(page).locator(".extension-widget-key").allInnerTexts();
    assert.ok(keys.some((key) => /usage/.test(key)), `control widget key missing: ${JSON.stringify(keys)}`);
    assert.ok(keys.some((key) => /polyforge/.test(key)), `polyforge widget key missing: ${JSON.stringify(keys)}`);
  });
  await check(title, "each widget expands to its own lines", async () => {
    for (const [key, line] of [["usage", "E2E control widget line"], ["polyforge", "E2E-PF-KEY widget line"]]) {
      await shelf(page).getByText(key, { exact: true }).click();
      const panel = page.locator(".extension-widget-panel");
      await panel.waitFor();
      await waitFor(async () => (await panel.innerText()).includes(line), `${key} expands to its line`);
    }
  });
}

try {
  await h.launchBrowser(() => chromium.launch());
  // No pf: the shelf needs none, and the operator's pf and daemon are never read.
  await h.startServer({ name: "extension-widgets-169", logFile: join(work, "server.log"),
    env: { PF_BIN: join(work, "no-pf"), PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work } });
  const page = await openPage();
  try {
    await sendWidgetsCommand(page);
    await assertShelfShowsBoth(page, T.S1);
  } finally {
    await page.context().close();
  }

  // 600px: inside the app's mobile breakpoint (<= 640px) but above the narrow
  // (<= 480px) layout that tucks toolbar actions behind a "more" menu.
  const mobile = await openPage({ viewport: { width: 600, height: 900 } });
  try {
    await sendWidgetsCommand(mobile);
    await assertShelfShowsBoth(mobile, T.S4);
  } finally {
    await mobile.context().close();
  }

  // The mount /state reply, read before the command and applied after it, must
  // not erase the widgets the stream delivered meanwhile.
  const gate = { reached: Promise.withResolvers(), release: Promise.withResolvers(), held: 0 };
  const delayed = await openPage({ viewport: { width: 600, height: 900 }, session: SESSION_DELAYED, stateGate: gate });
  try {
    await gate.reached.promise;
    await sendWidgetsCommand(delayed);
    // Land the stale snapshot after the command's own completion state read:
    // the barrier proves every read the page issued so far reached the gate.
    await requestBarrier(delayed);
    gate.release.resolve();
    // Every held reply was answered, then the barrier proves the page applied
    // and rendered them before the shelf is read.
    await waitFor(() => gate.held === 0, "the held /state replies were answered");
    await requestBarrier(delayed);
    await assertShelfShowsBoth(delayed, T.S6);
  } catch (error) {
    // The shelf detaching is this scenario's failure; keep S5 runnable.
    ran.add(T.S6);
    failures.get(T.S6).push(`stale snapshot: ${String(error?.message ?? error).split("\n")[0]}`);
  } finally {
    gate.release.resolve();
    await delayed.context().close();
  }

  await check(T.S5, "error signals", async () => assert.deepEqual(errors.splice(0), []));
  await h.stop();
} catch (error) {
  console.error(error);
  process.exitCode = 1;
} finally {
  await h.close().catch(() => {});
  reportTitled(failures, ran);
  if (process.exitCode) console.log(`Artifacts kept: ${work}`); else rmSync(work, { recursive: true, force: true });
}
