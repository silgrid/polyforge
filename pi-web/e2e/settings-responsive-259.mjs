// carries: app/settings.css, components/SettingsPanel*
// polyforge#259 (T4) AC3 — 设置对话框的宽度自适应与 tab 栏在 900 / 1280 / 1600
// 三个视口下都不溢出错出视口。
//
// 期望值来自工单「要的结果」的两条（不是实现输出）：对话框宽度
// `min(1280px, calc(100vw - 32px))`；侧栏 `clamp(200px, 20%, 260px)`；tab 栏
// 在 <1000px 时**保留文字**（owner 计划审第 3 条选 B）并给出可见的滚动提示，
// tab 项不被挤出对话框右缘。900 宽的期望宽度因此是 900-32=868，1280 宽是
// 1280-32=1248，1600 宽是 1280。侧栏 900 宽时 20% = 173.6 → 夹到 200，1600 宽
// 时 20% of 1280 = 256（在 200…260 之间）。
//
// 每个场景的等待都是有界轮询（页面布局稳定后才读数），没有固定 sleep。截图落在
// test-results/e2e/（被 .gitignore 覆盖），交付用的三张由 build 拷进胶囊
// artifacts/build/screenshots/。
//
//   node e2e/all.mjs settings-responsive-259.mjs
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import { COLD_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, joinedLines, writeHostSession } from "./harness.mjs";

assertNoDevServer();
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const distDir = process.env.PI_WEB_DIST_DIR || ".next/e2e/settings-responsive-259";
const artifacts = join(root, "test-results/e2e");
mkdirSync(artifacts, { recursive: true });

const work = mkdtempSync(join(tmpdir(), "pi-web-settings-259-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const SESSION = "e2e-settings-259-session";
mkdirSync(project, { recursive: true });
writeHostSession(agentDir, { id: SESSION, cwd: project, text: "E2E settings responsive host session", timestamp: "2026-11-24T00:00:00.000Z" });

const WIDTHS = [900, 1280, 1600];
const expectedDialogWidth = (viewport) => Math.min(1280, viewport - 32);

const failures = [];
const check = createCheck({ failures, message: joinedLines(8) });

const h = createHarness();
try {
  await h.startServer({
    name: "settings-responsive-259",
    logFile: join(work, "server.log"),
    env: { HOME: work, PI_CODING_AGENT_DIR: agentDir },
    distDir,
  });
  await h.launchBrowser(() => chromium.launch());

  const open = async ({ width = 1280, height = 900 } = {}) => {
    const context = await h.browser.newContext({ viewport: { width, height }, locale: "zh-CN" });
    await context.addInitScript(() => {
      try { window.localStorage.setItem("pi-locale", "zh-CN"); } catch { /* storage unavailable */ }
    });
    const page = await context.newPage();
    page.setDefaultTimeout(20_000);
    // The General section reads the harness list; a stable fixture keeps the
    // measured layout the page's own, never a half-loaded one.
    await page.route("**/api/harness", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ harnesses: [] }) }));
    await page.route("**/api/polyforge/detect*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({
      pfPresent: false, pfVersion: null, pfVersionBelowMinimum: true, line: null,
      piSkillInstalled: false, piSkillEnabled: false, claudeSkillsSymlink: false, claudeSessionStartHook: false, daemon: null,
    }) }));
    await page.goto(`${h.base}/?session=${SESSION}`, { waitUntil: "domcontentloaded", timeout: COLD_MS });
    await page.getByText("E2E settings responsive host session").first().waitFor({ state: "attached", timeout: NAV_MS });
    const show = page.locator('button[aria-label="Show sidebar"], button[aria-label="显示侧边栏"]').first();
    if (await show.isVisible().catch(() => false)) await show.click();
    await page.locator('button[aria-label="Settings"], button[aria-label="设置"], button[aria-label="設定"]').first().click();
    const dialog = page.locator('[role="dialog"][aria-label="Settings"], [role="dialog"][aria-label="设置"], [role="dialog"][aria-label="設定"]').first();
    await dialog.locator(".settings-dialog-surface").waitFor({ state: "visible", timeout: NAV_MS });
    await dialog.locator('.settings-section-host[data-settings-section="general"]:not([hidden])').first().waitFor({ state: "visible", timeout: NAV_MS });
    return { context, page, dialog };
  };

  // One read of every geometry the assertions use; the caller polls it to a
  // deadline so a value that settles late is never read once, too early.
  const measure = (page) => page.evaluate(() => {
    const dialog = document.querySelector('[role="dialog"]');
    const surface = dialog.querySelector(".settings-dialog-surface");
    const nav = dialog.querySelector(".settings-section-tabs");
    const close = dialog.querySelector(".settings-dialog-close");
    const hint = dialog.querySelector("[data-settings-tabs-more]");
    const sidebar = dialog.querySelector(".config-sidebar");
    const rect = (node) => {
      if (!node) return null;
      const box = node.getBoundingClientRect();
      return { left: box.left, right: box.right, width: box.width, top: box.top, height: box.height };
    };
    return {
      innerWidth: window.innerWidth,
      viewportWidth: document.documentElement.clientWidth,
      documentScrollWidth: document.documentElement.scrollWidth,
      surface: rect(surface),
      surfaceScrollWidth: surface.scrollWidth,
      surfaceClientWidth: surface.clientWidth,
      nav: rect(nav),
      navScrollWidth: nav.scrollWidth,
      navClientWidth: nav.clientWidth,
      navScrollLeft: nav.scrollLeft,
      close: rect(close),
      hint: hint ? { ...rect(hint), shown: getComputedStyle(hint).display !== "none" && hint.offsetWidth > 0 } : null,
      sidebar: rect(sidebar),
      tabs: [...nav.querySelectorAll(".settings-section-tab")].map((tab) => {
        const label = tab.querySelector("span");
        return {
          text: (label ?? tab).textContent.trim(),
          labelWidth: label ? label.getBoundingClientRect().width : 0,
          ...rect(tab),
        };
      }),
    };
  });

  const settled = async (page, ready, what) => {
    const deadline = Date.now() + 8_000;
    let last = null;
    while (Date.now() < deadline) {
      last = await measure(page);
      if (ready(last)) return last;
      await page.waitForTimeout(100);
    }
    return last;
  };

  const at = async (page, width, reason) => {
    await page.setViewportSize({ width, height: 900 });
    return settled(page, (m) => m.innerWidth === width && Math.abs(m.surface.width - expectedDialogWidth(width)) <= 1, reason);
  };

  const desktop = await open({ width: 1280, height: 900 });

  for (const width of WIDTHS) {
    await check(`S259-W${width}-1 — 对话框宽度是 min(1280px, 100vw - 32px)（${width} 视口）`, async () => {
      const m = await at(desktop.page, width, "dialog width");
      assert.ok(m, `the settings dialog did not settle at ${width}px`);
      assert.ok(Math.abs(m.surface.width - expectedDialogWidth(width)) <= 1,
        `dialog width ${m.surface.width} is not min(1280, ${width}-32) = ${expectedDialogWidth(width)}`);
      assert.ok(m.surface.left >= 0 && m.surface.right <= width,
        `the dialog is outside the viewport (left ${m.surface.left}, right ${m.surface.right}, viewport ${width})`);
      assert.ok(m.documentScrollWidth <= m.innerWidth + 1,
        `the page overflows horizontally: ${m.documentScrollWidth} > ${m.innerWidth}`);
      assert.ok(m.surfaceScrollWidth <= m.surfaceClientWidth + 1,
        `the dialog's own content overflows: ${m.surfaceScrollWidth} > ${m.surfaceClientWidth}`);
    });

    await check(`S259-W${width}-2 — tab 栏停在对话框内、不压到关闭按钮（${width} 视口）`, async () => {
      const m = await at(desktop.page, width, "tab bar inside the dialog");
      assert.ok(m.nav && m.close, `the tab bar or the close button is missing at ${width}px`);
      assert.ok(m.nav.right <= m.close.left + 1,
        `the tab bar is pushed out of the dialog: nav right ${m.nav.right} > close left ${m.close.left}`);
      assert.ok(m.nav.left >= m.surface.left, `the tab bar starts outside the dialog (${m.nav.left} < ${m.surface.left})`);
      assert.ok(m.tabs.length > 1, "the tab bar renders the sections");
      const first = m.tabs[0];
      assert.ok(first.left >= m.nav.left - 1 && first.right <= m.nav.right + 1,
        `the first tab is outside the tab bar (${first.left}..${first.right} vs ${m.nav.left}..${m.nav.right})`);
      // A bar that does not scroll shows every tab; a scrolling one carries the
      // rest off the edge, and S259-900-SCROLL proves they are reachable.
      if (m.navScrollWidth <= m.navClientWidth + 1) {
        const last = m.tabs.at(-1);
        assert.ok(last.right <= m.nav.right + 1, `the last tab is outside the tab bar (${last.right} > ${m.nav.right})`);
      }
    });

    await check(`S259-W${width}-3 — tab 文字在窄宽度下仍然可见（${width} 视口）`, async () => {
      const m = await at(desktop.page, width, "tab labels");
      for (const tab of m.tabs) {
        assert.ok(tab.text.length > 0, "a tab lost its name");
        assert.ok(tab.labelWidth > 0, `the tab ${JSON.stringify(tab.text)} renders no text at ${width}px (label width ${tab.labelWidth})`);
      }
    });
  }

  await check("S259-900-SCROLL — 900 宽 tab 栏可横向滚动，且有可见的滚动提示", async () => {
    const m = await at(desktop.page, 900, "900 scroll bar");
    assert.ok(m.navScrollWidth > m.navClientWidth + 1,
      `the tab bar does not scroll at 900px (${m.navScrollWidth} <= ${m.navClientWidth})`);
    assert.ok(m.hint?.shown, "the tab bar shows no scroll hint at 900px");
    // Scrolling to the end reaches the last tab: the hint is not a dead end.
    await desktop.page.evaluate(() => {
      const nav = document.querySelector(".settings-section-tabs");
      nav.scrollLeft = nav.scrollWidth;
    });
    const after = await settled(desktop.page, (next) => {
      const last = next.tabs.at(-1);
      return next.navScrollLeft > 0 && last.right <= next.nav.right + 1;
    }, "last tab in view");
    const last = after.tabs.at(-1);
    assert.ok(after.navScrollLeft > 0, "the tab bar did not scroll");
    assert.ok(last.right <= after.nav.right + 1,
      `the last tab cannot be reached by scrolling (right ${last.right} > nav right ${after.nav.right})`);
    assert.ok(last.right <= after.close.left + 1,
      `the scrolled-in last tab runs under the close button (${last.right} > ${after.close.left})`);
  });

  await check("S259-WIDE — 1280 / 1600 宽不再被 1080px 卡住，没有多余的滚动提示", async () => {
    const wide = await at(desktop.page, 1600, "1600 wide");
    assert.ok(wide.surface.width >= 1280,
      `the dialog at 1600px is still capped below 1280 (${wide.surface.width})`);
    assert.ok(!wide.hint?.shown, "the scroll hint still shows at 1600px");
    assert.ok(wide.navScrollWidth <= wide.navClientWidth + 1,
      `the tab bar still scrolls at 1600px (${wide.navScrollWidth} > ${wide.navClientWidth})`);
    const mid = await at(desktop.page, 1280, "1280 wide");
    assert.ok(!mid.hint?.shown, "the scroll hint still shows at 1280px");
    assert.ok(mid.navScrollWidth <= mid.navClientWidth + 1,
      `the tab bar still scrolls at 1280px (${mid.navScrollWidth} > ${mid.navClientWidth})`);
  });

  await check("S259-SIDEBAR — 侧栏宽度是 clamp(200px, 20%, 260px)", async () => {
    const wide = await at(desktop.page, 1600, "sidebar at 1600");
    assert.ok(wide.sidebar, "the General section renders no sidebar");
    assert.ok(Math.abs(wide.sidebar.width - 256) <= 1,
      `the sidebar is not 20% of the 1280px dialog (${wide.sidebar.width} vs 256)`);
    const narrow = await at(desktop.page, 900, "sidebar at 900");
    assert.ok(Math.abs(narrow.sidebar.width - 200) <= 1,
      `the sidebar is not clamped to 200px at 900px (${narrow.sidebar.width})`);
  });

  await check("S259-SHOTS — 900 / 1280 / 1600 三宽截图", async () => {
    for (const width of WIDTHS) {
      await at(desktop.page, width, "screenshot");
      await desktop.page.screenshot({ path: join(artifacts, `settings-259-${width}-zh.png`), fullPage: false });
    }
  });

  await desktop.context.close();
} catch (error) {
  failures.push(`suite error: ${error?.stack ?? String(error)}`);
  process.exitCode = 1;
} finally {
  await h.close();
}

console.log(`\n${check.count - failures.length}/${check.count} settings responsive checks passed`);
if (failures.length) {
  console.error(`\n${failures.length} settings responsive check(s) failed:`);
  for (const failure of failures) console.error(`- ${failure}`);
  process.exitCode = 1;
} else {
  console.log("PASS settings-responsive-259");
}
