// Shared Polyforge dock opener for the browser e2e suites.
//
// A helper, not a suite: it declares no `// carries:` header, and because
// suites import it, e2e/all.mjs and the verify-scope engine both discover it
// as a helper (CONTRIBUTING.md §6). The toggle and dock locators, the bounded
// wait for the resident toggle and the open path live here once instead of in
// eight per-suite copies (follow-up).
//
// the toggle renders only once the mount-time status probe
// resolves (`pfPresent`), so a caller waits for that bounded condition instead
// of letting a click's actionability window absorb a slow probe under host
// load. made the toggle resident right of the toolbar's More
// toggle at ≤480px (never folded into the More sheet) and inline wider.
// Never open ⋯ to reveal it; ⋯'s overlay covers that slot
// (left: TOP_BAR_ICON_BUTTON_SIZE, z-index 20) and intercepts the click; close
// ⋯ if it is open, then click the toggle.
//
// both locators match by identity (`data-pf-toggle`,
// `#polyforge-dock`, the dialog's exact name "Polyforge"), never by a
// substring of an accessible name: a project path holding "polyforge" shows up
// in other buttons' names (the file explorer's project button sorts first).
import { setTimeout as delay } from "node:timers/promises";
import { NAV_MS } from "./timeouts.mjs";

export const polyforgeDock = (page) => page.locator("#polyforge-dock").or(page.getByRole("dialog", { name: "Polyforge", exact: true })).first();
export const polyforgeToggle = (page) => page.locator("[data-pf-toggle]:visible").first();

// Bounded wait (own deadline, 150 ms polls) for the resident toggle to render.
// The toggle waits on the mount-time status probe (and, on a cold server, that
// route's first compile), so the ceiling is the suite's page-open one, not a
// fixed 20 s guess: under host contention the probe can outlast 20 s while the
// rest of the bar is healthy (full-1: S203-1..S203-5 red with
// `the Polyforge toggle renders (last: 0)` at loadavg 27).
export async function waitForPolyforgeToggle(page, ms = NAV_MS) {
  const deadline = Date.now() + ms; let last;
  while (Date.now() < deadline) {
    try { last = await polyforgeToggle(page).count(); if (last > 0) return; } catch (error) { last = error.message; }
    await delay(150);
  }
  throw new Error(`the Polyforge toggle renders (last: ${last})`);
}

// For a suite that opens ⋯ for something else (the Debug button, the More
// sheet itself) and does not mock the status read: the toggle renders only when
// pf is present, so ask the same status route the mount probe reads (a page
// route mock answers it too) and, when it says present, wait bounded for the
// toggle, so the resident slot has settled before ⋯ opens.
// With no pf there is no toggle and nothing to wait for.
export async function settlePolyforgeToggle(page, ms = 20_000) {
  // The deadline runs in Node, not the page: a suite may have paused the page clock.
  const deadline = new AbortController();
  const read = page.evaluate(() => fetch("/api/polyforge/status")
    .then((response) => response.json()).then((body) => body.pfPresent === true).catch(() => false));
  let present;
  try { present = await Promise.race([read, delay(ms, "timeout", { signal: deadline.signal })]); } finally { deadline.abort(); }
  if (present === "timeout") throw new Error(`the Polyforge status read did not answer within ${ms}ms`);
  if (present) await waitForPolyforgeToggle(page, ms);
}

// Toggle wait → close ⋯ if it is open → click → bounded dock wait; returns the dock.
export async function openPolyforgeDock(page) {
  const more = page.locator('[data-mobile-toolbar-more="true"]');
  await waitForPolyforgeToggle(page);
  if ((await more.count()) > 0 && (await more.getAttribute("aria-expanded")) === "true") {
    await more.click();
    await page.locator('[data-mobile-toolbar-actions="true"]').waitFor({ state: "hidden", timeout: 10_000 });
  }
  await polyforgeToggle(page).click();
  await polyforgeDock(page).waitFor({ timeout: 20_000 });
  return polyforgeDock(page);
}
