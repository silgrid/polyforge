import assert from "node:assert/strict";
import { rmSync } from "node:fs";
import { NAV_MS } from "./timeouts.mjs";

// wi_tN1cVXKf1VIr / wi_uyIF45t806CP (pi-web sidebar A1: Archive) —
// end-to-end acceptance pass.
//
// Authored against artifacts/spec.md's Acceptance criteria and Scenario
// blocks, BEFORE the implementation exists. Every locator below drives the
// spec'd behavior through interfaces that already exist on the pre-code tree
// (the sidebar session rows and their title attributes, the row right-click
// hover, the sessions pane bottom, the global sidebar session search) and
// matches the new surfaces only by user-visible text/roles.
// Pre-implementation this pass therefore fails at assertion level (missing
// 归档/Archive menu item, missing 已归档 N entry), never at build level — no
// symbol that does not exist today is imported or referenced. The fixture
// premise below was validated against the pre-implementation tree with an
// out-of-tree probe (row visibility, project switching, search reach, and
// hover-action rendering all behave as the steps assume).
//
// Recovery provenance: this file was recovered verbatim by
// artifacts/recover-e2e-standalone.sh (ops JSON from the pi#102 round), then
// the two owner-confirmed corrections were layered on top by e2e-author
// AFTER the recovery script (they are not in the ops JSON — the pi#102
// blocked-round lessons):
//
//   1. baseURL wiring: e2e/run.mjs's browser.newContext now carries
//      `baseURL: base` so the relative `page.goto("/")` below resolves (a
//      relative goto without a context baseURL throws "invalid URL").
//
//   2. Fixture precondition (grill Batch 1 Q2, owner ruling (b)): the
//      two-project alpha/beta fixture is kept as authored, and one project
//      (alpha) is PRE-SELECTED at test start. The main-list disappearance
//      assertions therefore target only the selected project's session
//      (alpha while alpha is selected, beta while beta is selected); the
//      other project's archived session is additionally asserted through
//      the search path and the archive view (which shows both projects'
//      groups), so per-project grouping is asserted through the real UI.
//
//      Pre-selection and mid-pass project switching ride the existing
//      global session search: searching for a session and clicking its
//      result opens that session, which moves the sidebar's effective
//      project to the session's project (handleSelectSessionFromList →
//      setSelectedCwd) — the fork's real UI for cross-project reach. A
//      /?session=<id> page.goto does NOT work mid-pass: the persisted
//      split-view pane tabs (localStorage `pi-web:open-pane-tabs`) restore
//      the previous tab strip on load and re-focus it, so the URL deep link
//      only adopts its session on a tab's first load. The search-click path
//      is deterministic across loads and is what the steps below use.
//
// The exact en/zh-TW label strings, the accessible roles of the new menu
// item / entry / back button, and the localStorage serialization of the
// archive set are under-specified in the spec (it names only the zh-CN forms
// 归档 / 已归档 and cites the pi-web:unread-session-ids precedent). Each
// locator below accepts the small candidate set the spec implies; see
// artifacts/e2e-scenarios.md (spec under-specifications) before diverging.
//
// Steps are individually guarded: one failing scenario logs FAIL and sets
// the exit code without aborting the rest of the suite, so e2e-baseline can
// lay the pre-code bar for every criterion in one run.

const ARCHIVE_STORAGE_KEY = "pi-web:archived-session-ids";
// WAIT_MS bounds a reaction to an in-page action. A state that only the
// session list of a fresh page load produces (the main list's rows, the
// archive count, the external-delete prune) is a first-data wait after a
// navigation: it waits for the reload, the hydration and the server's
// session scan, so it takes the shared page-load ceiling, NAV_MS
// (e2e/timeouts.mjs). At 5 s the prune step failed once on a loaded gate
// (host loadavg 19.7) and passed on its re-runs.
const WAIT_MS = 5000;

// ── helpers ─────────────────────────────────────────────────────────────

async function readArchiveSet(page) {
  return page.evaluate((key) => {
    const raw = window.localStorage.getItem(key);
    if (raw == null) return [];
    try {
      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed)) return parsed.map(String);
    } catch {}
    // Spec under-specification: the set's serialization. The fork precedent
    // (pi-web:unread-session-ids) stores JSON.stringify([...ids]); a raw
    // scalar is tolerated so the assertion stays at the spec's level
    // ("the id is added to the archive set under this key").
    return [raw];
  }, ARCHIVE_STORAGE_KEY);
}

async function clearArchive(page) {
  await page.evaluate((key) => {
    try { window.localStorage.removeItem(key); } catch {}
  }, ARCHIVE_STORAGE_KEY);
}

/** Races candidate locators; returns the first visible one or null. */
async function waitForAny(page, candidates, timeout = WAIT_MS) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    for (const candidate of candidates) {
      const locator = candidate(page);
      if ((await locator.count().catch(() => 0)) > 0) {
        const first = locator.first();
        if (await first.isVisible().catch(() => false)) return first;
      }
    }
    await page.waitForTimeout(200);
  }
  return null;
}

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** Polls an async predicate until it returns the expected value. */
async function assertEventually(predicate, expected, message, timeout = WAIT_MS) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    try { last = await predicate(); } catch { last = undefined; }
    if (last === expected) return;
    await delay(200);
  }
  assert.equal(last, expected, message);
}

async function ensureSidebar(page) {
  const showSidebar = page.getByRole("button", { name: "Show sidebar", exact: true });
  if (await showSidebar.isVisible().catch(() => false)) await showSidebar.click();
}

const sessionRow = (page, title) => page.locator(`[title="${title}"]`).first();

const searchToggle = (page) =>
  page.getByRole("button", { name: /^Search conversations$|^搜索对话$/ }).first();
const searchBox = (page) =>
  page.getByLabel(/^Search all conversations\.\.\.$|^搜索所有对话\.\.\.$/).first();

async function searchPanelOpen(page) {
  return searchBox(page).isVisible().catch(() => false);
}

/**
 * Switches the sidebar's effective project through the global session
 * search (ruling-b pre-selection and mid-pass project switching): open the
 * search panel, query, click the target session's result — that opens the
 * session, which moves the effective project to the session's project —
 * then close the panel so the (now project-scoped) main list renders again.
 */
async function switchProjectViaSearch(page, query, resultText) {
  if (!(await searchPanelOpen(page))) await searchToggle(page).click();
  await searchBox(page).waitFor({ state: "visible", timeout: WAIT_MS });
  await searchBox(page).fill(query);
  const result = page.locator("button", { hasText: resultText }).first();
  await result.waitFor({ state: "visible", timeout: WAIT_MS * 3 });
  await result.click();
  await delay(800); // let the pane switch and cwd sync settle
  if (await searchPanelOpen(page)) await searchToggle(page).click();
  await delay(300);
}

// Candidate locators for the spec'd new surfaces. The spec names only the
// zh-CN strings; English labels are implied by the i18n requirement.
const menuArchiveCandidates = [
  (page) => page.getByRole("menuitem", { name: /^归档$|^Archive$/i }),
  (page) => page.getByRole("button", { name: /^归档$|^Archive$/i }),
  (page) => page.getByText(/^归档$|^Archive$/, { exact: true }),
];
const archiveEntryCandidates = [
  (page) => page.getByText(/^已归档\s*\d+$|^Archived\s*\d+$|^Archived\s*\(\d+\)$/i),
  (page) => page.getByRole("button", { name: /^已归档\s*\d+$|^Archived\s*\d+$|^Archived\s*\(\d+\)$/i }),
];
const backButtonCandidates = [
  (page) => page.getByRole("button", { name: /^Back$|^返回$|^←$/i }),
  (page) => page.getByText(/^Back$|^返回$/),
];
const UNARCHIVE_NAME = /^Unarchive$|^取消归档$|^恢复归档$/i;
const unarchiveCandidates = [
  (page) => page.getByRole("menuitem", { name: UNARCHIVE_NAME }),
  (page) => page.getByRole("button", { name: UNARCHIVE_NAME }),
  (page) => page.getByText(UNARCHIVE_NAME),
];
const archivedBadgeCandidates = [
  (page) => page.getByText(/^已归档$|^Archived$/, { exact: true }),
];

/**
 * Finds the unarchive row action for one specific archived session row.
 * Scoped first (the action inside the hovered row's container — the fork's
 * hover-action-row precedent), then falls back to the global candidates:
 * with hover-revealed actions only the hovered row renders its buttons, so
 * hovering the target row scopes the global match too.
 */
async function findUnarchiveAction(page, rowTitle) {
  const row = sessionRow(page, rowTitle);
  await row.hover().catch(() => {});
  const scope = row.locator("xpath=../..");
  const scoped = [
    () => scope.getByRole("menuitem", { name: UNARCHIVE_NAME }),
    () => scope.getByRole("button", { name: UNARCHIVE_NAME }),
    () => scope.getByText(UNARCHIVE_NAME),
  ];
  for (const make of scoped) {
    const locator = make();
    if ((await locator.count().catch(() => 0)) > 0) return locator.first();
  }
  return waitForAny(page, unarchiveCandidates);
}

/** Opens the archive view idempotently (clicks the entry unless already open). */
async function openArchiveView(page) {
  if (await waitForAny(page, backButtonCandidates, 500)) return;
  const entry = await waitForAny(page, archiveEntryCandidates);
  assert.ok(entry, "the 已归档/Archived entry must be clickable");
  await entry.click();
  const back = await waitForAny(page, backButtonCandidates);
  assert.ok(back, "the archive view must show a clearly visible back button");
}

/** Closes the archive view idempotently (clicks back if the view is open). */
async function closeArchiveView(page) {
  const back = await waitForAny(page, backButtonCandidates, 500);
  if (!back) return;
  await back.click();
  await page.waitForTimeout(300);
}

async function archiveEntryCount(page) {
  let entry;
  try {
    entry = await waitForAny(page, archiveEntryCandidates, 1000);
  } catch {
    return null;
  }
  if (!entry) return null;
  const text = (await entry.textContent().catch(() => null)) ?? "";
  const match = text.match(/\d+/);
  return match ? Number(match[0]) : null;
}

// ── the pass ─────────────────────────────────────────────────────────────

export async function checkArchive(page, fixtures) {
  const { alphaId, betaId, alphaTitle, betaTitle, betaFile, alphaProject, betaProject, probeTitle } = fixtures;
  const failures = [];
  const step = async (name, fn) => {
    try {
      await fn();
      console.log(`PASS: archive e2e — ${name}`);
    } catch (error) {
      failures.push(name);
      console.error(`FAIL: archive e2e — ${name}\n${error && error.message ? error.message : error}`);
    }
  };

  // A non-archived fixture session (the rich-markdown probe, main e2e
  // project) whose main-list row proves the sessions pane is showing the
  // ordinary list (used to detect replacement mode).
  const mainListProbeTitle = probeTitle ?? "Render **E2E markdown**";

  // Reset to a clean N=0 state before laying the bar, then pre-select the
  // alpha project (ruling b) through the global search: clicking a search
  // result opens that session, which moves the sidebar's effective project
  // to the session's project.
  await clearArchive(page);
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await ensureSidebar(page);
  await switchProjectViaSearch(page, "archive alpha", alphaTitle);

  // Scenario: the selected project's fixture session renders in the
  // sessions pane (pre-code invariant — grounds every later assertion on
  // interfaces that exist). The beta session lives in the other fixture
  // project, so its row is intentionally NOT in the project-scoped main
  // list; its cross-project reachability is asserted through the global
  // search (S4/S11) and the archive view (S7) per the ruling.
  await step("fixture row renders in the selected project's sessions pane", async () => {
    await sessionRow(page, alphaTitle).waitFor({ state: "visible", timeout: NAV_MS });
  });

  // Scenario: Entry hidden at zero.
  await step("已归档 N entry is completely hidden when N=0", async () => {
    assert.equal(await archiveEntryCount(page), null, "no 已归档/Archived entry may render at N=0");
  });

  const mutations = [];
  const collectMutations = (request) => {
    if (request.method() !== "GET" && request.url().includes("/api/")) {
      mutations.push(`${request.method()} ${request.url()}`);
    }
  };

  // Scenario: Archive a non-running session (alpha, the selected project's
  // session — the main-list disappearance assertion targets only it).
  await step("row-menu 归档/Archive archives a non-running session (alpha)", async () => {
    page.on("request", collectMutations);
    const before = mutations.length;
    await sessionRow(page, alphaTitle).click({ button: "right" });
    const item = await waitForAny(page, menuArchiveCandidates);
    assert.ok(item, "the session row context menu must offer a 归档/Archive item");
    await item.click();
    await assertEventually(() => sessionRow(page, alphaTitle).isVisible().catch(() => false),
      false, "the archived session must disappear from the main sessions list");
    const set = await readArchiveSet(page);
    assert.ok(set.includes(alphaId),
      `pi-web:archived-session-ids must contain ${alphaId} after archiving (got ${JSON.stringify(set)})`);
    assert.equal(mutations.length, before, "archiving must not send any mutating /api/ request");
    page.off("request", collectMutations);
  });

  // Scenario: Archive the second session (beta, the other fixture project).
  // Reach beta through the global search (the ruling's cross-project path):
  // clicking its search result opens it and switches the effective project
  // to beta's, so beta's row is grounded in the beta-scoped main list before
  // the archive action; the disappearance assertion again targets the
  // selected project's session. Beta's archived state is further asserted
  // via the archive view (S7) and search (S11) — the cross-project paths.
  await step("row-menu 归档/Archive archives a second session (beta, other project)", async () => {
    await switchProjectViaSearch(page, "archive beta", betaTitle);
    await sessionRow(page, betaTitle).waitFor({ state: "visible", timeout: WAIT_MS * 2 });
    page.on("request", collectMutations);
    const before = mutations.length;
    await sessionRow(page, betaTitle).click({ button: "right" });
    const item = await waitForAny(page, menuArchiveCandidates);
    assert.ok(item, "the session row context menu must offer a 归档/Archive item");
    await item.click();
    await assertEventually(() => sessionRow(page, betaTitle).isVisible().catch(() => false),
      false, "the archived session must disappear from the main sessions list");
    const set = await readArchiveSet(page);
    assert.ok(set.includes(alphaId) && set.includes(betaId),
      `pi-web:archived-session-ids must contain both ${alphaId} and ${betaId} (got ${JSON.stringify(set)})`);
    assert.equal(mutations.length, before, "archiving must not send any mutating /api/ request");
    page.off("request", collectMutations);
  });

  // Scenario: Entry appears with the global count and is stable across
  // project switches (beta selected → probe's main project selected).
  await step("已归档 N entry appears with the correct global count and is stable across project switches", async () => {
    await assertEventually(() => archiveEntryCount(page), 2,
      "the 已归档/Archived entry must show N=2 after archiving two sessions across two projects");
    await switchProjectViaSearch(page, "markdown", mainListProbeTitle);
    await sessionRow(page, mainListProbeTitle).waitFor({ state: "visible", timeout: WAIT_MS * 2 });
    await assertEventually(() => archiveEntryCount(page), 2,
      "the global entry count must not change when the selected project changes");
  });

  // Scenario: Persistence across reload. The alpha project is re-selected
  // through the search first (alpha is archived — the search must still
  // find it and opening it is view-only), so after the reload the
  // disappearance assertion stays on the selected project's session.
  await step("archive set survives a page reload", async () => {
    await switchProjectViaSearch(page, "archive alpha", alphaTitle);
    await page.reload({ waitUntil: "domcontentloaded" });
    await ensureSidebar(page);
    await assertEventually(() => sessionRow(page, alphaTitle).isVisible().catch(() => false), false,
      "alpha must stay excluded from the main list after reload");
    const set = await readArchiveSet(page);
    assert.ok(set.includes(alphaId) && set.includes(betaId),
      "the archive set must be restored from localStorage after reload");
    await assertEventually(() => archiveEntryCount(page), 2,
      "entry count must survive the reload", NAV_MS);
  });

  // Scenario: Opening the archive view. The probe's project is selected so
  // the main list provably has a non-archived row: replacement mode means
  // that row stops rendering while the archive view is open.
  await step("已归档 N click opens a replacement archive view with back button and project grouping", async () => {
    await switchProjectViaSearch(page, "markdown", mainListProbeTitle);
    await sessionRow(page, mainListProbeTitle).waitFor({ state: "visible", timeout: WAIT_MS * 2 });
    await openArchiveView(page);
    await sessionRow(page, alphaTitle).waitFor({ state: "visible", timeout: WAIT_MS });
    await sessionRow(page, betaTitle).waitFor({ state: "visible", timeout: WAIT_MS });
    // Replacement mode: while the archive view is open the main list (a
    // non-archived session row) must not render — this is a replacement of
    // the pane, not an inline expanding block.
    await assertEventually(() => sessionRow(page, mainListProbeTitle).isVisible().catch(() => false),
      false, "the archive view must replace the main sessions list, not append to it");
    // Grouped by project: the two fixture projects (distinct directory
    // basenames) must each be represented as a group label in the view.
    // Pane-tab labels also carry the project basename ("alpha-project · …"),
    // so tab matches are subtracted to keep the assertion non-vacuous.
    const projectRe = new RegExp(`${alphaProject}|${betaProject}`);
    const totalMatches = await page.getByText(projectRe).count();
    const tabMatches = await page.getByRole("tab").filter({ hasText: projectRe }).count();
    assert.ok(totalMatches - tabMatches >= 2,
      `archive view must group rows by project (expected labels for ${alphaProject} and ${betaProject}; non-tab matches: ${totalMatches - tabMatches})`);
  });

  // Scenario: Back navigation.
  await step("back button restores the main sessions list", async () => {
    const back = await waitForAny(page, backButtonCandidates);
    assert.ok(back, "the archive view must show a back button");
    await back.click();
    await sessionRow(page, mainListProbeTitle).waitFor({ state: "visible", timeout: WAIT_MS * 2 });
  });

  // Scenario: Open from the archive view is view-only.
  await step("opening a session from the archive view is view-only", async () => {
    await openArchiveView(page);
    await sessionRow(page, alphaTitle).waitFor({ state: "visible", timeout: WAIT_MS });
    await sessionRow(page, alphaTitle).click();
    await page.waitForFunction((id) => window.location.search.includes(`session=${id}`), alphaId,
      { timeout: WAIT_MS * 2 });
    const set = await readArchiveSet(page);
    assert.ok(set.includes(alphaId), "merely opening an archived session must not unarchive it");
  });

  // Scenario: Unarchive from the archive view. After the view-only open the
  // effective project is alpha's, so the restored main list must show
  // alpha's row again.
  await step("unarchive row action returns the session to the main list and updates N", async () => {
    page.on("request", collectMutations);
    const before = mutations.length;
    await openArchiveView(page);
    await sessionRow(page, alphaTitle).waitFor({ state: "visible", timeout: WAIT_MS });
    const action = await findUnarchiveAction(page, alphaTitle);
    assert.ok(action, "the archive view must offer an unarchive row action");
    await action.click();
    await assertEventually(() => archiveEntryCount(page), 1,
      "the entry count must drop to 1 after unarchiving alpha");
    const back = await waitForAny(page, backButtonCandidates);
    assert.ok(back, "the archive view must still show a back button");
    await back.click();
    await sessionRow(page, alphaTitle).waitFor({ state: "visible", timeout: WAIT_MS * 2 });
    const set = await readArchiveSet(page);
    assert.ok(!set.includes(alphaId), "alpha must be removed from the archive set");
    assert.ok(set.includes(betaId), "beta must remain archived");
    assert.equal(mutations.length, before, "unarchiving must not send any mutating /api/ request");
    page.off("request", collectMutations);
  });

  // Scenario: Search finds archived session (badge + view-only click).
  // Alpha is back in the (alpha-selected) main list; re-archive it from its
  // row menu — the disappearance assertion again targets the selected
  // project's session — then reach it through the global search.
  await step("sidebar search reaches an archived session with a 已归档 badge and view-only click", async () => {
    await sessionRow(page, alphaTitle).waitFor({ state: "visible", timeout: WAIT_MS * 2 });
    await sessionRow(page, alphaTitle).click({ button: "right" });
    const item = await waitForAny(page, menuArchiveCandidates);
    assert.ok(item, "the session row context menu must offer a 归档/Archive item");
    await item.click();
    await assertEventually(() => sessionRow(page, alphaTitle).isVisible().catch(() => false), false,
      "alpha must leave the main list after re-archiving");
    if (!(await searchPanelOpen(page))) await searchToggle(page).click();
    await searchBox(page).waitFor({ state: "visible", timeout: WAIT_MS });
    await searchBox(page).fill("archive alpha");
    const result = page.locator("button", { hasText: alphaTitle }).first();
    await result.waitFor({ state: "visible", timeout: WAIT_MS * 3 });
    const badge = await waitForAny(page, archivedBadgeCandidates, WAIT_MS);
    assert.ok(badge, "the search result for an archived session must carry a 已归档/Archived badge");
    await result.click();
    await page.waitForFunction((id) => window.location.search.includes(`session=${id}`), alphaId,
      { timeout: WAIT_MS * 2 });
    const set = await readArchiveSet(page);
    assert.ok(set.includes(alphaId), "clicking a search result must not change archive state");
    // Search-panel open state is React state: a reload resets it (toggling
    // could re-open the panel if the implementation auto-closes it on
    // selection) so the external-delete step below starts from the plain
    // sessions pane.
    await page.reload({ waitUntil: "domcontentloaded" });
    await ensureSidebar(page);
  });

  // Scenario: Cleanup on delete (external deletion). The beta session (the
  // other project's archived session) is asserted through the archive view:
  // after the prune it must be gone from the view while alpha remains.
  await step("an externally deleted session id is pruned from the archive set", async () => {
    const set = await readArchiveSet(page);
    assert.ok(set.includes(betaId), "beta must be archived before the external delete");
    rmSync(betaFile, { force: true });
    const refresh = page.getByRole("button", { name: /^Refresh$|^刷新$/ });
    await refresh.first().click();
    await page.reload({ waitUntil: "domcontentloaded" });
    await ensureSidebar(page);
    await assertEventually(async () => (await readArchiveSet(page)).includes(betaId), false,
      "the externally deleted session id must be pruned from the archive set", NAV_MS);
    await assertEventually(() => archiveEntryCount(page), 1,
      "the entry count must reflect the pruned session (no stale counts or ghost rows)", NAV_MS);
    await openArchiveView(page);
    await sessionRow(page, alphaTitle).waitFor({ state: "visible", timeout: WAIT_MS * 2 });
    await assertEventually(() => sessionRow(page, betaTitle).isVisible().catch(() => false), false,
      "no ghost row may render in the archive view for the deleted session");
    await closeArchiveView(page);
  });

  // Leave the tree clean for any later pass.
  try {
    await clearArchive(page);
    await page.goto("/", { waitUntil: "domcontentloaded" });
    await ensureSidebar(page);
  } catch {}

  if (failures.length > 0) {
    process.exitCode = 1;
    console.error(`FAIL: archive e2e — ${failures.length} scenario(s) failed: ${failures.join("; ")}`);
  }
}
