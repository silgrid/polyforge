// carries: components/Terminal*, components/FileExplorer*, components/terminal-*, hooks/useTerminalTheme*, lib/terminal-*, app/api/terminal/*, bin/prepare-terminal.js
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { chromium } from "playwright";
import { assertNoDevServer, createHarness } from "./harness.mjs";

assertNoDevServer();
const artifacts = mkdtempSync(join(tmpdir(), "pi-web-terminal-e2e-"));
console.log(`Artifacts: ${artifacts}`);
const agentDir = join(artifacts, "agent");
const workspace = join(artifacts, "workspace-a");
const otherWorkspace = join(artifacts, "workspace-b");
mkdirSync(join(agentDir, "sessions", "test"), { recursive: true });
mkdirSync(workspace);
mkdirSync(otherWorkspace);
writeFileSync(join(workspace, "note.txt"), "File viewer fixture\n");
const timestamp = "2026-09-05T00:00:00.000Z";
for (const [id, cwd, name] of [
  ["terminal-a1", workspace, "Terminal session one"],
  ["terminal-a2", workspace, "Terminal session two"],
  ["terminal-b1", otherWorkspace, "Other workspace session"],
]) {
  writeFileSync(join(agentDir, "sessions", "test", `${id}.jsonl`), [
    { type: "session", version: 3, id, timestamp, cwd },
    { type: "session_info", id: "name", parentId: null, timestamp, name },
    { type: "message", id: "message", parentId: "name", timestamp, message: { role: "user", content: `${name} message` } },
  ].map((entry) => JSON.stringify(entry)).join("\n") + "\n");
}

const h = createHarness();
const { base } = await h.startServer({
  name: "terminal",
  logFile: join(artifacts, "server.log"),
  env: { PI_CODING_AGENT_DIR: agentDir, HISTFILE: process.platform === "win32" ? "NUL" : "/dev/null", BASH_SILENCE_DEPRECATION_WARNING: "1", SHELL: process.platform === "win32" ? process.env.SHELL : "/bin/bash" },
});
let browser;
try {
  browser = await h.launchBrowser(() => chromium.launch());
  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    const context = await browser.newContext({ viewport, locale: "en-US" });
    // pi#82: the sidebar's age filter defaults to 15 days and the fixture
    // sessions are dated 2026-09-05; run with the filter off, exactly as
    // e2e/run.mjs already does for its own fixtures.
    await context.addInitScript(() => {
      try {
        window.localStorage.setItem("pi-web:session-age-filter-days", "0");
        // This suite drives the classic single-chat layout (session switch
        // keeps one workspace terminal and the right panel in place), as
        // e2e/run.mjs already does for its classic flows; split view is the
        // default since d344694 and turns a session click into a new pane.
        window.localStorage.setItem("pi-web:split-view-enabled", "false");
      } catch {}
    });
    const page = await context.newPage();
    page.setDefaultTimeout(30_000);
    const errors = [];
    const created = new Set();
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("request", (request) => {
      if (new URL(request.url()).pathname === "/api/terminal" && request.method() === "POST") created.add(request.postDataJSON().id);
    });
    const ready = () => page.locator(".terminal-panel:visible .is-ready").waitFor();
    const text = () => page.locator(".terminal-panel:visible .xterm-rows").innerText();
    // xterm replaces its character spans asynchronously after a font change/refit.
    const expectFontWeight = (weight, bold = false) => page.waitForFunction(({ weight, bold }) => {
      const panel = [...document.querySelectorAll(".terminal-panel")].find((element) => element.getBoundingClientRect().width > 0);
      const character = panel?.querySelector(`.xterm-rows span${bold ? ".xterm-bold" : ":not(.xterm-bold)"}`);
      return character && getComputedStyle(character).fontWeight === String(weight);
    }, { weight, bold });
    const run = async (command) => {
      await page.locator(".terminal-panel:visible .xterm-helper-textarea").focus();
      await page.keyboard.type(command);
      await page.keyboard.press("Enter");
    };
    const waitOutput = (pattern) => page.waitForFunction((source) => {
      const panel = [...document.querySelectorAll(".terminal-panel")].find((element) => element.getBoundingClientRect().width > 0);
      return new RegExp(source).test(panel?.querySelector(".xterm-rows")?.textContent ?? "");
    }, pattern);
    const showSidebar = async () => {
      const button = page.getByRole("button", { name: "Show sidebar", exact: true });
      if (await button.count()) await button.click();
    };
    const showPanel = () => page.getByRole("button", { name: "Show file panel", exact: true }).click();
    // After a reload the terminal-tab restore effect seeds both the tabs and
    // the panel-open state. Wait for that effect (the terminal mounts) before
    // deciding, then open the panel only if it stayed closed.
    const ensurePanel = async () => {
      const toggle = page.getByRole("button", { name: /^(Show|Hide) file panel$/i }).first();
      await toggle.waitFor();
      await page.locator(".terminal-panel").first().waitFor({ state: "attached" });
      if ((await toggle.getAttribute("aria-expanded")) !== "true") await toggle.click();
      await page.locator('[aria-controls="file-panel"][aria-expanded="true"]').first().waitFor();
    };
    // Mobile/tabs layout exposes Sessions | Files tabs; the session rows are
    // in the Sessions pane, so bring it forward before clicking one. Desktop
    // stacked layout has no such tabs and this is a no-op.
    const showSessionsTab = async () => {
      const tab = page.getByRole("tab", { name: /^Sessions$/i });
      if (await tab.count() && (await tab.getAttribute("aria-selected")) !== "true") await tab.click();
    };
    const showFilesTab = async () => {
      const tab = page.getByRole("tab", { name: /^Files$/i });
      if (await tab.count() && (await tab.getAttribute("aria-selected")) !== "true") await tab.click();
    };
    // "Open workspace terminal" sits in the explorer toolbar, i.e. the Files
    // pane in the tabs layout; open the sidebar and that pane first.
    const openWorkspaceTerminal = async () => {
      await showSidebar();
      await showFilesTab();
      await page.getByRole("button", { name: "Open workspace terminal", exact: true }).click();
    };
    // pi#14: explorer sections start collapsed (and fetch nothing until
    // expanded), so the fixture file is not in the DOM by default.
    const expandExplorerSection = async (root) => {
      const section = page.locator(`[data-explorer-section="${root}"]`);
      if (await section.count() && (await section.getAttribute("aria-expanded")) === "false") await section.click();
    };
    const hidePanel = async () => {
      const button = page.locator("#file-panel").getByRole("button", { name: "Hide file panel", exact: true, includeHidden: true });
      if (await button.getAttribute("aria-expanded") === "true") await button.click();
    };
    try {
      await page.goto(`${base}/?session=terminal-a1`);
      await page.getByText("Terminal session one message", { exact: true }).waitFor();
      await openWorkspaceTerminal();
      await ready();
      await run("export PR695_TOKEN=alive; printf '\\nTOKEN:%s:%s\\n' \"$PR695_TOKEN\" \"$$\"");
      await waitOutput("TOKEN:alive:[0-9]+");
      const pid = (await text()).match(/TOKEN:alive:(\d+)/)[1];
      const [id] = created;
      assert.equal(created.size, 1);

      // A font change refits an existing xterm without restarting its shell or SSE.
      const fontConnections = [];
      const trackFontConnections = (request) => {
        if (/\/api\/terminal(?:$|\/[^/]+\/events)/.test(new URL(request.url()).pathname)) fontConnections.push(request.url());
      };
      page.on("request", trackFontConnections);
      await hidePanel();
      await showSidebar();
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      // A phone's plain Settings button opens on the list of sections (PR #8 replay).
      if (viewport.width <= 640) await page.locator(".settings-phone-home").getByRole("button", { name: "General", exact: true }).click();
      await page.getByRole("textbox", { name: "Monospace font", exact: true }).fill("monospace");
      await page.getByRole("slider", { name: "Monospace font weight", exact: true }).fill("600");
      await page.keyboard.press("Escape");
      if (viewport.width <= 640) {
        await page.locator(".sidebar-overlay-backdrop").click({ position: { x: viewport.width - 5, y: 100 } });
      }
      await showPanel();
      await ready();
      assert.match(await page.locator(".terminal-panel:visible .xterm-rows").evaluate((el) => getComputedStyle(el).fontFamily), /^monospace,/);
      await expectFontWeight(600);
      await run("printf '\\nFONT:%s:%s\\n' \"$PR695_TOKEN\" \"$$\"");
      await waitOutput(`FONT:alive:${pid}`);
      await run("printf '\\033[1mBOLD_FONT\\033[0m\\n'");
      await expectFontWeight(700, true);
      page.off("request", trackFontConnections);
      assert.deepEqual(fontConnections, [], "Font changes must not recreate terminals or reconnect streams");

      await hidePanel();
      await showSidebar();
      await expandExplorerSection(workspace);
      // the row's hover-revealed "Insert path into chat" control
      // never covers the file name, so a click on the name opens the file
      // whenever the hover render lands, and the control stays reachable.
      {
        const name = page.getByText("note.txt", { exact: true });
        await name.hover();
        const insert = page.locator('button[title="Insert path into chat"]');
        await insert.waitFor();
        const nameBox = await name.boundingBox();
        const insertBox = await insert.boundingBox();
        assert.ok(nameBox.x + nameBox.width <= insertBox.x || insertBox.x + insertBox.width <= nameBox.x,
          `the file name ${JSON.stringify(nameBox)} must not extend under its row's Insert path control ${JSON.stringify(insertBox)}`);
        const centre = { x: nameBox.x + nameBox.width / 2, y: nameBox.y + nameBox.height / 2 };
        assert.equal(await page.evaluate(({ x, y }) => document.elementFromPoint(x, y)?.textContent, centre), "note.txt",
          "the hovered row's name centre must hit the name itself");
        await insert.click();
        const composer = page.locator(".chat-input-textarea");
        assert.equal(await composer.inputValue(), "@note.txt ", "the Insert path control must insert the file's @mention");
        await composer.fill("");
        // A phone closes the drawer after the insertion; bring the explorer back.
        await showSidebar();
        if (viewport.width <= 640) await showFilesTab();
      }
      await page.getByText("note.txt", { exact: true }).click();
      await page.getByText("File viewer fixture", { exact: true }).waitFor();
      assert.equal(await page.locator(".terminal-panel").count(), 1);
      assert.equal(await page.locator(".terminal-panel").isVisible(), false);
      await page.getByRole("tab", { name: "Terminal: workspace-a", exact: true }).click();
      await ready();
      await hidePanel();
      await showSidebar();
      await showSessionsTab();
      await page.getByText("Terminal session two", { exact: true }).click();
      await showPanel();
      await run("printf '\\nSESSION:%s:%s\\n' \"$PR695_TOKEN\" \"$$\"");
      await waitOutput(`SESSION:alive:${pid}`);

      // The terminal's own theme swaps xterm's palette in place and survives a reload.
      const terminalBackground = () => page.locator(".terminal-panel:visible .xterm-viewport").evaluate((el) => getComputedStyle(el).backgroundColor);
      const appTheme = await page.evaluate(() => document.documentElement.dataset.theme);
      await page.getByRole("button", { name: "Switch terminal to light theme", exact: true }).click();
      await page.locator('.terminal-panel:visible[data-terminal-theme="light"]').waitFor();
      assert.equal(await terminalBackground(), "rgb(251, 251, 252)");
      assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), appTheme);
      await run("printf '\\nTHEME:%s:%s\\n' \"$PR695_TOKEN\" \"$$\"");
      await waitOutput(`THEME:alive:${pid}`);

      await page.reload();
      await ensurePanel();
      // The restored slide may land on the empty panel state; the terminal tab
      // itself is restored, so select it (the assertion below is still that
      // the same pty is reconnected, not recreated).
      await page.getByRole("tab", { name: "Terminal: workspace-a", exact: true }).click();
      await ready();
      await expectFontWeight(600);
      await page.locator('.terminal-panel:visible[data-terminal-theme="light"]').waitFor();
      assert.equal(await terminalBackground(), "rgb(251, 251, 252)");
      await page.getByRole("button", { name: "Switch terminal to dark theme", exact: true }).click();
      assert.equal(await terminalBackground(), "rgb(17, 19, 24)");
      assert.equal(await page.evaluate(() => localStorage.getItem("pi-web:terminal-theme")), "dark");
      await run("printf '\\nREFRESH:%s:%s\\n' \"$PR695_TOKEN\" \"$$\"");
      await waitOutput(`REFRESH:alive:${pid}`);
      assert.equal(created.size, 1, "refresh must reconnect, not create");

      await context.setOffline(true);
      await page.locator(".terminal-panel:visible .is-connecting").waitFor();
      await context.setOffline(false);
      await ready();
      await run("printf '\\nRECONNECT:%s:%s\\n' \"$PR695_TOKEN\" \"$$\"");
      await waitOutput(`RECONNECT:alive:${pid}`);
      assert.equal(((await text()).match(new RegExp(`REFRESH:alive:${pid}`, "g")) ?? []).length, 1, "reconnect must not replay delivered output");

      // pi#84: soft keyboards that fire no keydown reach the page only as bare
      // `input` insertions (xterm's screenReaderMode drops that path), and
      // their Enter arrives as insertLineBreak. Dispatching them directly on
      // the xterm textarea must run the command exactly once — no loss, no
      // duplication — including at mobile viewport sizes.
      await page.locator(".terminal-panel:visible .xterm-helper-textarea").focus();
      await page.evaluate(() => {
        const panel = [...document.querySelectorAll(".terminal-panel")].find((element) => element.getBoundingClientRect().width > 0);
        const textarea = panel.querySelector(".xterm-helper-textarea");
        textarea.dispatchEvent(new InputEvent("input", { inputType: "insertText", data: "printf '\\nBARE:%s\\n' ok", bubbles: true, composed: true }));
        textarea.dispatchEvent(new InputEvent("input", { inputType: "insertLineBreak", bubbles: true, composed: true }));
      });
      await waitOutput("BARE:ok");
      assert.equal(((await text()).match(/BARE:ok/g) ?? []).length, 1, "bare input insertions must be delivered exactly once");

      // pi#84 review: a real-xterm IME composition regression. xterm's
      // CompositionHelper delivers the commit itself from the textarea's
      // final value on a deferred timeout, and that final value can differ
      // from every composition candidate. The bridge must swallow the commit
      // echo whatever it says (the commit reaches the shell exactly once),
      // and the suppression must not outlive the commit sequence (an
      // identical bare insertion still flows afterwards). Input POSTs are
      // counted directly so the exactly-once claim does not depend on how
      // the pty echo wraps at this viewport's column count.
      const imeMarker = "I84M";
      const imeSends = [];
      const onImeSend = (request) => {
        if (request.method() === "POST" && new URL(request.url()).pathname === `/api/terminal/${id}`) {
          const body = request.postDataJSON();
          if (body?.type === "input" && String(body.data).includes(imeMarker)) imeSends.push(String(body.data));
        }
      };
      page.on("request", onImeSend);
      const imeSentCount = () => imeSends.join("").split(imeMarker).length - 1;
      const waitForIme = async (count) => {
        for (let i = 0; i < 100 && imeSentCount() < count; i++) await delay(100);
        return imeSentCount();
      };
      try {
        await page.locator(".terminal-panel:visible .xterm-helper-textarea").focus();
        await page.evaluate((marker) => {
          const panel = [...document.querySelectorAll(".terminal-panel")].find((element) => element.getBoundingClientRect().width > 0);
          const textarea = panel.querySelector(".xterm-helper-textarea");
          const before = textarea.value;
          const fire = (event) => textarea.dispatchEvent(event);
          fire(new CompositionEvent("compositionstart"));
          textarea.value = `${before}ime`; // mid-composition candidate
          fire(new CompositionEvent("compositionupdate", { data: "ime" }));
          textarea.value = `${before}echo ${marker}`; // committed final value, unlike the candidate
          fire(new CompositionEvent("compositionend", { data: "ime" }));
          // The commit echo some browsers fire after compositionend, carrying
          // the final value that matches no composition candidate.
          fire(new InputEvent("input", { inputType: "insertText", data: `echo ${marker}`, bubbles: true, composed: true }));
        }, imeMarker);
        // The deferred delivery posts the commit once; a swallowed echo adds
        // nothing, a forwarded one would double the marker (batched into the
        // same write or a neighboring one).
        assert.equal(await waitForIme(1), 1, "the IME commit must reach the shell exactly once (echo swallowed, deferred delivery sent once)");
        // The suppression ended with xterm's deferred delivery: an identical
        // bare insertion is new input and must flow.
        await page.locator(".terminal-panel:visible .xterm-helper-textarea").focus();
        await page.evaluate((marker) => {
          const panel = [...document.querySelectorAll(".terminal-panel")].find((element) => element.getBoundingClientRect().width > 0);
          const textarea = panel.querySelector(".xterm-helper-textarea");
          textarea.dispatchEvent(new InputEvent("input", { inputType: "insertText", data: `echo ${marker}`, bubbles: true, composed: true }));
        }, imeMarker);
        assert.equal(await waitForIme(2), 2, "an identical bare insertion after the finished composition must not be swallowed");
        await page.keyboard.press("Control+c"); // drop the mangled command line
      } finally {
        page.off("request", onImeSend);
      }

      await page.screenshot({ path: join(artifacts, `${viewport.width}.png`), fullPage: true });
      const dimensions = await page.locator(".terminal-panel:visible").evaluate((element) => {
        const panel = element.getBoundingClientRect();
        const screen = element.querySelector(".xterm-screen").getBoundingClientRect();
        return { panelWidth: panel.width, screenWidth: screen.width, screenHeight: screen.height, fits: screen.right <= panel.right + 1 && screen.bottom <= panel.bottom + 1 };
      });
      assert.ok(dimensions.screenWidth > 100 && dimensions.screenHeight > 100 && dimensions.fits, JSON.stringify(dimensions));
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);

      await page.getByRole("button", { name: "Restart terminal", exact: true }).click();
      await page.waitForFunction((oldId) => {
        const saved = JSON.parse(sessionStorage.getItem("pi-web:terminal-tabs"));
        return saved.tabs.length === 1 && saved.tabs[0].id !== oldId;
      }, id);
      await ready();
      assert.equal((await fetch(`${base}/api/terminal/${id}`)).status, 404);
      await run("exit 7");
      await page.getByText("Process exited with code 7", { exact: true }).waitFor();
      const currentId = await page.evaluate(() => JSON.parse(sessionStorage.getItem("pi-web:terminal-tabs")).tabs[0].id);
      await page.getByRole("button", { name: "Terminate terminal workspace-a", exact: true }).click();
      await page.locator(".terminal-panel").waitFor({ state: "detached" });
      assert.equal((await fetch(`${base}/api/terminal/${currentId}`)).status, 404);

      let releaseCreation;
      const creationResponse = new Promise((resolve) => { releaseCreation = resolve; });
      await page.route("**/api/terminal", async (route) => {
        const response = await route.fetch();
        await creationResponse;
        await route.fulfill({ response });
      });
      await hidePanel();
      await openWorkspaceTerminal();
      await page.getByRole("button", { name: "Terminate terminal workspace-a", exact: true }).click();
      releaseCreation();
      await page.locator(".terminal-panel").waitFor({ state: "detached" });
      await page.unroute("**/api/terminal");
      for (const terminalId of created) assert.equal((await fetch(`${base}/api/terminal/${terminalId}`)).status, 404);

      await openWorkspaceTerminal();
      await ready();
      await run("export PR695_WORKSPACE=retained");
      await hidePanel();
      await showSidebar();
      // The redesigned sidebar has no workspace-switch button, and the
      // session list is filtered to the selected project. Enter the other
      // workspace through its session deep link (the same workspace the
      // removed switcher targeted); the sidebar must then show that
      // workspace's session, which is what the old assertion proved too.
      await page.goto(`${base}/?session=terminal-b1`, { waitUntil: "domcontentloaded" });
      // Wait for the app to hydrate before operating the sidebar toggle.
      await page.getByText("Other workspace session message", { exact: true }).waitFor();
      await showSidebar();
      await showSessionsTab();
      await page.getByText("Other workspace session", { exact: true }).waitFor();
      await openWorkspaceTerminal();
      await ready();
      const workspaceTabs = await page.evaluate(() => JSON.parse(sessionStorage.getItem("pi-web:terminal-tabs")).tabs);
      assert.deepEqual(workspaceTabs.map((tab) => tab.cwd).sort(), [workspace, otherWorkspace].sort());
      const otherId = workspaceTabs.find((tab) => tab.cwd === otherWorkspace).id;
      assert.equal((await (await fetch(`${base}/api/terminal/${otherId}`)).json()).cwd, otherWorkspace);
      await page.getByRole("tab", { name: "Terminal: workspace-a", exact: true }).click();
      await run("printf '\\nWORKSPACE:%s\\n' \"$PR695_WORKSPACE\"");
      await waitOutput("WORKSPACE:retained");
      await page.screenshot({ path: join(artifacts, `${viewport.width}-workspaces.png`), fullPage: true });
      for (const name of ["workspace-a", "workspace-b"]) {
        await page.getByRole("button", { name: `Terminate terminal ${name}`, exact: true }).click();
      }
      // Both workspace terminals are mounted as separate panels, and they
      // close asynchronously: wait until every panel is gone instead of a
      // strict single-element locator.
      await page.waitForFunction(() => document.querySelectorAll(".terminal-panel").length === 0);
      for (const terminalId of created) assert.equal((await fetch(`${base}/api/terminal/${terminalId}`)).status, 404);
      assert.deepEqual(errors, []);
      console.log(`PASS ${viewport.width}: real shell, files, sessions, refresh, reconnect, restart, exit, close during creation, workspace isolation`);
    } catch (error) {
      await page.screenshot({ path: join(artifacts, `failure-${viewport.width}.png`), fullPage: true });
      console.error(await page.locator("body").innerText());
      throw error;
    } finally {
      await context.close();
    }
  }
} finally {
  await h.close();
}
