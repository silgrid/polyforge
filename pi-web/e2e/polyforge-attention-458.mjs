// carries: lib/polyforge-decisions*, components/PolyforgeDecisionEntry*
// — what needs a human is visible at a glance.
//
// The Polyforge button in the top bar is itself amber while anything on this
// machine needs a human (the jobs waiting for an answer plus the decision
// feed's other open entries, the count of `attentionItems`), at every width;
// no separate reminder renders beside it. Its name carries the count and
// `data-pf-attention` the number. With the dock closed a click opens it
// through the attention walk: the next item's job detail in Work › Jobs
// (selected — nothing else opens;), and the name says so; with
// the dock open a click
// closes it and the name carries the count alone. With nothing pending the
// button is the plain toggle.
//
// The suite starts its own `next dev` and drives the page through the
// Polyforge HTTP faces mocked in the browser with the routes' documented wire
// shapes (`/api/polyforge/status`, `/cards`, `/watch`, `/progress`,
// `/decisions`, `/tasks`, `/messages`), the decisions-415
// convention: no pf CLI or daemon runs. Every scenario prints `PASS S458-<n>`;
// the run ends `PASS all attention-458 scenarios` only when every scenario
// passed and the page logged no errors.
//
//   S458-1 (AC1, wide) plain without attention (no data-pf-attention, a
//          transparent background, no reminder); amber with a waiting job and
//          a dropped item's open `blocked` entry (data-pf-attention="2", the
//          count in its name, still no reminder); one click opens the dock on
//          the waiting job's detail (its question), the button stays
//          amber while the dock is open and its name keeps the count but no
//          longer says a click opens the next one (a click closes the dock);
//          closed with the button, its name says so again, and opened again
//          it walks to the dropped item's detail (its park cause above the
//          input box).
//   S458-2 (AC1, phone) the resident toggle is amber with data-pf-attention,
//          no reminder exists, and one click opens the sheet on the waiting
//          job's detail (its question).
//   S458-3 (AC2/AC3, blocked then woken) a dropped item's open `blocked` entry
//          keeps one amber card (tint, amber frame, 已阻塞 marker) first in
//          the row; a click opens its detail with the park cause above the
//          input box; the owner's
//          message wakes it (workflow contract 4; no 继续 button,)
//          and the live card comes back in its place, once and normally
//          coloured.
//   S458-4 (AC2, other classes; dismiss) failed / submit_rejected /
//          submit_dropped / candidates_exhausted give one amber card each with
//          its class label (newest first, before the live cards); a resolved
//          and an informational entry give none; the card's 忽略 removes that
//          card.
//   S458-5 (AC3, needs_input) the waiting job's card is amber with the solid
//          等待输入 marker and appears once despite its open feed entry; once
//          answered (running, entry resolved) it is no longer tinted.
//   S458-6 (AC3, themes) in the light and the dark theme, selected and not,
//          the blocked card's tint differs visibly from the panel and the
//          slug's contrast over the composited tint is at least 4.5.
//   S499-2 ( AC3, dismiss on the card) each feed-only card shows
//          its newest open entry's reason (`data-pf-attention-reason`) and a
//          忽略 button (`data-pf-attention-dismiss`; the live card has none);
//          a refused dismiss keeps the card and shows 忽略失败：<pf's text> on
//          it without selecting it; an accepted one posts every open entry of
//          the item, newest first, in one `{ ids }`, the card is gone and the
//          button's count drops by that many.
//   S458-7 (AC3 of, replied) a running job's only feed entry is
//          a needs_input question the owner already replied to (the daemon's
//          `replied:true`, `attention:false`, unresolved): it counts nowhere —
//          the button stays plain (no data-pf-attention, transparent) and the
//          job's live card shows no attention reason and no 忽略 (
//          deleted the item panel this scenario opened on main; the replied
//          state itself — grey, 「已回复，等 step 处理」, no dismiss — stays the
//          entry component's, pinned by its component test); a new open
//          question of the same job then turns the button amber with
//          data-pf-attention="1" (the replied one is not counted) and the
//          walk opens the job's detail, its card carrying the new question's
//          reason and one 忽略.
//
//   node e2e/polyforge-attention-458.mjs
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { polyforgeDock as dock, waitForPolyforgeToggle } from "./polyforge-dock.mjs";
import { ACTION_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, mockContextUsage, mockPolyforge, NO_MESSAGES, NO_PROGRESS, NO_TASKS, reply as json, suiteDist, writeHostSession } from "./harness.mjs";

const distDir = suiteDist("polyforge-attention-458");
assertNoDevServer(distDir);
const work = mkdtempSync(join(tmpdir(), "pi-web-pf-458-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-attention-458-session";
const HOST_TEXT = "E2E pf attention 458 host session";
const EPOCH = "2026-11-10T03:00:00.000Z";
mkdirSync(project);
writeHostSession(agentDir, { id: SESSION, cwd: project, text: HOST_TEXT, timestamp: EPOCH });

// ---- fixture: a running job, a waiting job, a work item the daemon dropped ----
const J1 = { wi: "wi_E2E458J1", slug: "demo#458x1", status: "running", stepAgeSec: 60, idleAgeSec: 5, candidate: "pi:glm-e2e", spawnN: 1 };
const J2 = { wi: "wi_E2E458J2", slug: "demo#458x2", status: "needs_input", stepAgeSec: 60, idleAgeSec: 5, candidate: "pi:glm-e2e", spawnN: 1 };
// The daemon dropped this one (blocked, no job): only the feed names it.
const W = { wi: "wi_E2E458W3", slug: "demo#458x3" };
const QUESTION = "E2E-458 which colour?";
const BLOCKED = { kind: "driver", step: "verification", reason: "E2E-458 the driver parked the workflow",
  requiredAction: "E2E-458 resolve the cause, then resume the work item (`pf resume`) or cancel it" };
const at = (n) => `2026-11-10T10:${String(n).padStart(2, "0")}:00Z`;
const entry = (id, item, cls, fields = {}) => ({ id, at: at(id), kind: "work_item", profile: "default", wi: item.wi, slug: item.slug,
  class: cls, source: cls, phrase: `E2E-458 the ${cls} phrase`, attention: true, resolved: false, dismissed: false, ...fields });
const isOpen = (e) => e.attention && !e.resolved && !e.dismissed;
// The fixture the mocked routes read; a scenario flips it.
let fixture;
const quiet = () => ({ jobs: [J1], decisions: [] });
const pending = () => ({ jobs: [J1, J2], decisions: [entry(2, W, "blocked"), entry(1, J2, "needs_input", { question_id: "q-colour" })] });
const statusBody = () => ({ pfPresent: true, status: { queue: { running: fixture.jobs.filter((j) => j.status === "running").length, max: 8 },
  jobs: fixture.jobs, tasks: { running: 0, completed: 0 } } });
const card = (job) => ({ job, goal: `E2E 458 goal of ${job.slug}`, startedAt: EPOCH,
  steps: [{ id: "build", state: "running", runs: 1 }], currentStep: "build", progress: { available: true, entries: [] } });
const feedBody = () => ({ pfPresent: true, feed: { decisions: fixture.decisions, attention: fixture.decisions.filter(isOpen).length } });
// The work items the daemon dropped, each with the workflow status its watch
// snapshot reads (W is the blocked one, with its cause).
const X4 = { wi: "wi_E2E458X4", slug: "demo#458x4" };
const X5 = { wi: "wi_E2E458X5", slug: "demo#458x5" };
const X6 = { wi: "wi_E2E458X6", slug: "demo#458x6" };
const X7 = { wi: "wi_E2E458X7", slug: "demo#458x7" };
const X8 = { wi: "wi_E2E458X8", slug: "demo#458x8" };
const X9 = { wi: "wi_E2E458X9", slug: "demo#458x9" };
const DROPPED = [W, X4, X5, X6, X7, X8, X9];
const watchBody = (wi) => {
  const job = fixture.jobs.find((candidate) => candidate.wi === wi || candidate.slug === wi);
  const dropped = job ? null : DROPPED.find((item) => item.wi === wi || item.slug === wi);
  if (dropped === W) {
    return { status: { wi: W.wi, slug: W.slug, workflowStatus: "blocked", currentStep: "verification", updatedAt: EPOCH,
      steps: [{ name: "verification", status: "blocked", startedAt: EPOCH }], declared_steps: [{ id: "verification" }],
      events: [{ at: EPOCH, kind: "step_started", step: "verification" }, { at: EPOCH, kind: "terminal", status: "blocked" }], blocked: BLOCKED } };
  }
  if (dropped) {
    return { status: { wi: dropped.wi, slug: dropped.slug, workflowStatus: "failed", currentStep: "build", updatedAt: EPOCH,
      steps: [{ name: "build", status: "failed", startedAt: EPOCH }], declared_steps: [{ id: "build" }],
      events: [{ at: EPOCH, kind: "step_started", step: "build" }, { at: EPOCH, kind: "terminal", status: "failed" }] } };
  }
  const shown = job ?? J1;
  return { status: { wi: shown.wi, slug: shown.slug, workflowStatus: shown.status, currentStep: "build", updatedAt: EPOCH,
    steps: [{ name: "build", status: shown.status === "needs_input" ? "needs_input" : "running", startedAt: EPOCH }], declared_steps: [{ id: "build" }],
    events: [{ at: EPOCH, kind: "step_started", step: "build", model: "glm-e2e" },
      ...(shown.status === "needs_input" ? [{ at: EPOCH, kind: "needs_input", step: "build", question: QUESTION, question_id: "q-colour" }] : [])],
    // workflow contract v2 (change 14): the question the step waits on is the snapshot's pendingQuestion
    ...(shown.status === "needs_input" ? { pendingQuestion: { id: "q-colour", step: "build", question: QUESTION } } : {}) } };
};
// S458-4: four dropped items, one open entry each (newest X7), plus a
// resolved and an informational entry that make no card.
const classes = () => ({ jobs: [J1], decisions: [
  entry(17, X9, "completed", { attention: false }),
  entry(16, X8, "failed", { resolved: true }),
  entry(15, X7, "candidates_exhausted"),
  entry(14, X6, "submit_dropped"),
  entry(13, X5, "submit_rejected"),
  entry(12, X4, "failed", { source: "failed_gate" }),
] });
const blockedOnly = () => ({ jobs: [J1], decisions: [entry(2, W, "blocked")] });
// S499-2: S458-4's classes plus an older open entry of X5 — five open entries,
// two of them X5's (#13 newest, #11).
const dismissable = () => ({ jobs: [J1], decisions: [...classes().decisions, entry(11, X5, "failed")] });
// S499-2: the dismiss route refuses while this is set (the post is still recorded).
let refuseDismiss = false;
// S458-7: J1 runs; its question (ask stamp RFC3339Nano, the question id) was
// answered — the daemon's replied entry; then a new question of J1 opens.
const REPLIED = entry(20, J1, "needs_input", { question_id: "2026-11-10T10:20:00.123456789Z", attention: false,
  replied: true, replied_at: "2026-11-10T10:21:30.5Z" });
const repliedOnly = () => ({ jobs: [J1], decisions: [REPLIED] });
const repliedThenAsked = () => ({ jobs: [J1], decisions: [entry(21, J1, "needs_input", { question_id: "2026-11-10T10:30:00.25Z" }), REPLIED] });
// ---- scenario bookkeeping and helpers ----
const failures = [];
const check = createCheck({ failures });
// One constant answers both the session state read and the agent's GET.
const CONTEXT_USAGE = { percent: 10, contextWindow: 200_000, tokens: 20_000 };
// The page's posts to the two write routes, in order.
const posts = [];
const h = createHarness({
  viewport: { width: 1280, height: 900 },
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN", "pi-web:polyforge-dock-width": "640" },
  timeout: ACTION_MS,
  errors: ["pageerror"],
  session: SESSION,
  ready: HOST_TEXT,
  async setup(page) {
    await mockPolyforge(page, {
      status: () => statusBody(),
      cards: () => ({ pfPresent: true, cards: fixture.jobs.map(card) }),
      watch: (url) => watchBody(url.searchParams.get("wi")),
      progress: NO_PROGRESS,
      decisions: () => feedBody(),
      // the owner's message wakes the parked item (`pf answer`, workflow
      // contract 4): the job is back in status/cards and job_started
      // resolves its open entries.
      answer: (url, route) => {
        if (route.request().method() !== "POST") return {};
        const body = route.request().postDataJSON();
        posts.push({ path: url.pathname, body });
        const item = DROPPED.find((candidate) => candidate.wi === body.wi);
        if (!item) return json(route, { ok: false, error: "unknown work item" }, 409);
        fixture = { jobs: [...fixture.jobs, { ...J1, wi: item.wi, slug: item.slug }],
          decisions: fixture.decisions.map((e) => (e.wi === item.wi ? { ...e, resolved: true } : e)) };
        return { ok: true, ack: { kind: "message", at: "2026-11-10T10:00:40.000Z", step: "build", spawnN: 2, delivered: false, disposition: "wake" } };
      },
      "decisions/dismiss": (url, route) => {
        if (route.request().method() !== "POST") return {};
        const body = route.request().postDataJSON();
        posts.push({ path: url.pathname, body });
        if (refuseDismiss) return json(route, { ok: false, error: "E2E-499 refused" }, 409);
        let dismissed = 0;
        fixture = { ...fixture, decisions: fixture.decisions.map((e) => {
          if (!isOpen(e) || !(body.ids ?? []).includes(e.id)) return e;
          dismissed += 1;
          return { ...e, dismissed: true };
        }) };
        return { ok: true, dismissed };
      },
      tasks: NO_TASKS,
      messages: NO_MESSAGES,
    });
    await mockContextUsage(page, SESSION, CONTEXT_USAGE);
  },
});
const { errors, withPage } = h;
const waitFor = createWaitFor({ ms: 20_000, done: (value) => value === true, describe: (label, last) => `${label} (last: ${JSON.stringify(last)})` });
// The Polyforge toggle itself (aria-pressed: the dock's open state), the one visible at this width.
const button = (page) => page.locator("[data-pf-toggle][aria-pressed]").filter({ visible: true }).first();
// The retired reminder's attribute, spelled in two parts: the docs validator
// greps the tree for the deleted name, and this suite only proves its absence.
const RETIRED_REMINDER = ["data-pf-needs-input", "badge"].join("-");
const reminder = (page) => page.locator(`[${RETIRED_REMINDER}]`);
const background = (page) => button(page).evaluate((el) => getComputedStyle(el).backgroundColor);
const AMBER = "rgb(217, 119, 6)";
// The walk the dock-closed click does, as its name says it.
const WALK_TEXT = "点击打开下一项";
const titleText = async (page) => {
  const title = dock(page).locator("[data-pf-detail-title]");
  return (await title.count()) > 0 ? (await title.first().innerText()).replace(/\s+/g, " ") : null;
};
const waitTitle = (page, slug, label) => waitFor(async () => { const t = await titleText(page); return Boolean(t && t.includes(slug)) || t; }, label);
// The status and feed reads keep their 30 s cadence: a flipped fixture shows within one tick.
const TICK_MS = 45_000;
// ---- the Jobs card row (S458-3..6) ----
// A work item's cards in the row (live or feed-only), and its attention card.
const cardsOf = (page, item) => dock(page).locator(`[data-pf-card-wi="${item.wi}"]`);
const attentionCard = (page, item) => dock(page).locator(`[data-pf-card-wi="${item.wi}"][data-pf-card-attention]`);
const rowOrder = (page) => dock(page).locator("[data-pf-card-wi]").evaluateAll((els) => els.map((el) => el.getAttribute("data-pf-card-wi")));
const bg = (locator) => locator.evaluate((el) => getComputedStyle(el).backgroundColor);
const rgba = (value) => {
  const m = /rgba?\(([^)]+)\)/.exec(value ?? "");
  if (!m) return null;
  const [r, g, b, a = 1] = m[1].split(/[\s,/]+/).filter(Boolean).map(Number);
  return { r, g, b, a };
};
// The amber tint: the amber's channels (r > g > b) at a partial alpha.
const isAmberTint = (value) => { const c = rgba(value); return Boolean(c && c.r === 217 && c.g === 119 && c.b === 6 && c.r > c.g && c.g > c.b && c.a > 0 && c.a < 1); };
const isAmberish = (value) => { const c = rgba(value); return Boolean(c && c.r === 217 && c.g === 119 && c.b === 6 && c.a > 0); };
// A colour that a later render may still change is read once two reads agree.
async function settled(read, ok, label) {
  let previous;
  await waitFor(async () => { const now = await read(); const done = ok(now) && now === previous; previous = now; return done || now; }, label);
  return previous;
}
const openDock = async (page) => {
  await button(page).click();
  await dock(page).waitFor();
};
const wakePosts = () => posts.filter((post) => post.path === "/api/polyforge/answer");
const dismissPosts = () => posts.filter((post) => post.path === "/api/polyforge/decisions/dismiss");

try {
  await h.launchBrowser(() => chromium.launch());
  await h.startServer({ name: "polyforge-attention-458", logFile: join(work, "server.log"), distDir, env: {
    PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "" } });

  await check("S458-1", async () => {
    fixture = quiet();
    await withPage(async (page) => {
      // 1. nothing needs a human: the plain toggle, once the status read has landed (the running count shows).
      await waitForPolyforgeToggle(page);
      await waitFor(async () => (await button(page).innerText()).trim() === "1" || (await button(page).innerText()), "the status read lands (running count 1)");
      assert.equal(await button(page).getAttribute("data-pf-attention"), null, "no data-pf-attention without attention");
      await waitFor(async () => (await background(page)) === "rgba(0, 0, 0, 0)" || await background(page), "the background is transparent");
      assert.equal(await button(page).getAttribute("aria-label"), "Polyforge", "the plain name");
      assert.equal(await reminder(page).count(), 0, "no reminder");
      // 2. a waiting job and a dropped item's open blocked entry: the button itself turns amber.
      fixture = pending();
      await waitFor(async () => (await button(page).getAttribute("data-pf-attention")) === "2" || await button(page).getAttribute("data-pf-attention"),
        "the button carries data-pf-attention=2", TICK_MS);
      await waitFor(async () => (await background(page)) === AMBER || await background(page), "the button is amber");
      const name = await button(page).getAttribute("aria-label");
      assert.match(name, /Polyforge/, "the name still says Polyforge");
      assert.match(name, /本机有 2 项需要你处理/, "the name carries the count");
      assert.match(await button(page).getAttribute("title"), /本机有 2 项需要你处理/, "so does the title");
      assert.equal(await reminder(page).count(), 0, "no separate reminder renders");
      assert.equal(await dock(page).count(), 0, "the dock is still closed");
      // 3. one click: the waiting job's detail (its question); the button stays amber while open.
      await button(page).click();
      await dock(page).waitFor();
      await waitTitle(page, J2.slug, "one click opens the waiting job's detail");
      await dock(page).locator("[data-pf-chat-question]").waitFor(); // workflow contract v2 (change 14): the question sits in the card's conversation
      await waitFor(async () => (await button(page).getAttribute("aria-pressed")) === "true", "aria-pressed says the dock is open");
      await waitFor(async () => (await background(page)) === AMBER || await background(page), "the button stays amber with the dock open");
      // With the dock open a click closes it, so the name keeps the count and claims no walk.
      await waitFor(async () => {
        const [label, title] = [await button(page).getAttribute("aria-label"), await button(page).getAttribute("title")];
        return [label, title].every((text) => Boolean(text) && text.includes("本机有 2 项需要你处理") && !text.includes(WALK_TEXT)) || { label, title };
      }, "with the dock open the name and title carry the count and claim no walk");
      // 4. closed with the button, opened again: the walk goes on to the dropped item.
      await button(page).click();
      await waitFor(async () => (await dock(page).count()) === 0, "the button closes the dock");
      await waitFor(async () => {
        const [label, title] = [await button(page).getAttribute("aria-label"), await button(page).getAttribute("title")];
        return [label, title].every((text) => Boolean(text) && text.includes(WALK_TEXT)) || { label, title };
      }, "with the dock closed again the name says a click opens the next one");
      await button(page).click();
      await dock(page).waitFor();
      await waitTitle(page, W.slug, "the next opening walks to the dropped item's detail");
      await dock(page).locator(`[data-pf-chat] [data-pf-chat-park-cause="${BLOCKED.kind}"]`).waitFor();
    });
  });

  await check("S458-2", async () => {
    fixture = pending();
    await withPage(async (page) => {
      await waitForPolyforgeToggle(page);
      const more = page.locator('[data-mobile-toolbar-more="true"]');
      if ((await more.count()) > 0 && (await more.getAttribute("aria-expanded")) === "true") {
        await more.click();
        await page.locator('[data-mobile-toolbar-actions="true"]').waitFor({ state: "hidden", timeout: 10_000 });
      }
      await waitFor(async () => (await button(page).getAttribute("data-pf-attention")) === "2" || await button(page).getAttribute("data-pf-attention"),
        "the resident toggle carries data-pf-attention=2");
      await waitFor(async () => (await background(page)) === AMBER || await background(page), "the resident toggle is amber");
      assert.equal(await reminder(page).count(), 0, "no reminder exists");
      await button(page).click();
      await dock(page).waitFor();
      await waitTitle(page, J2.slug, "one click opens the sheet on the waiting job's detail");
      await dock(page).locator("[data-pf-chat-question]").waitFor(); // workflow contract v2 (change 14): the question sits in the card's conversation
    }, { viewport: { width: 375, height: 812 }, context: { isMobile: true, hasTouch: true } });
  });

  await check("S458-3", async () => {
    fixture = pending();
    await withPage(async (page) => {
      await waitForPolyforgeToggle(page);
      await waitFor(async () => (await button(page).getAttribute("data-pf-attention")) === "2" || await button(page).getAttribute("data-pf-attention"), "the button carries the attention");
      await openDock(page);
      await waitTitle(page, J2.slug, "the walk opens the waiting job first");
      // 1. the dropped blocked item keeps one amber card, first in the row.
      await waitFor(async () => (await attentionCard(page, W).count()) === 1 || await attentionCard(page, W).count(), "W has its attention card");
      assert.equal(await cardsOf(page, W).count(), 1, "W has exactly one card");
      assert.equal((await rowOrder(page))[0], W.wi, "the feed-only card leads the row");
      const marker = attentionCard(page, W).locator("[data-pf-attention-marker]");
      assert.equal((await marker.innerText()).trim(), "已阻塞", "the marker names the class");
      assert.equal(await marker.getAttribute("title"), "E2E-458 the blocked phrase", "the marker's title is the daemon's phrase");
      assert.equal(await attentionCard(page, W).getAttribute("aria-label"), `${W.slug} · 已阻塞`);
      const tint = await settled(() => bg(attentionCard(page, W)), isAmberTint, "W's card is the amber tint");
      const j1 = await settled(() => bg(cardsOf(page, J1)), (value) => value !== undefined, "J1's card background settles");
      assert.notEqual(tint, j1, "the attention card differs from the running card");
      assert.equal(await cardsOf(page, J1).getAttribute("data-pf-card-attention"), null, "the running card is no attention card");
      assert.equal(await attentionCard(page, W).evaluate((el) => getComputedStyle(el).borderTopColor), AMBER, "the amber frame");
      // 2. a click opens W's detail with the park cause above the input box.
      await attentionCard(page, W).click();
      await waitTitle(page, W.slug, "the card opens W's detail");
      await dock(page).locator(`[data-pf-chat] [data-pf-chat-park-cause="${BLOCKED.kind}"]`).waitFor();
      // 3. the owner's message wakes W (workflow contract 4: the card's box
      // stays open on a park; no 继续 button,): W runs again,
      // its entry resolves; the live card takes its place.
      assert.equal(await dock(page).locator("[data-pf-resume-button], [data-pf-chat-menu]").count(), 0, "no 继续 anywhere: the card is the box");
      const box = dock(page).locator("[data-pf-chat-input]");
      await box.fill("go on");
      await box.press("Enter");
      await waitFor(() => wakePosts().length === 1 || wakePosts().length, "the message posts the wake");
      assert.equal(wakePosts()[0].body.wi, W.wi, "the message names W");
      assert.equal(wakePosts()[0].body.text, "go on", "the words verbatim");
      await waitFor(async () => {
        const n = await cardsOf(page, W).count();
        if (n !== 1) return `cards ${n}`;
        const card = cardsOf(page, W);
        if ((await card.getAttribute("data-pf-card")) === null) return "not the live card yet";
        if ((await card.getAttribute("data-pf-card-attention")) !== null) return "still an attention card";
        const now = await bg(card);
        return !isAmberish(now) || now;
      }, "W's card is the live, normally coloured card", TICK_MS);
      assert.equal(await cardsOf(page, W).count(), 1, "W still has exactly one card");
    });
  });

  await check("S458-4", async () => {
    fixture = classes();
    await withPage(async (page) => {
      await waitForPolyforgeToggle(page);
      await waitFor(async () => (await button(page).getAttribute("data-pf-attention")) === "4" || await button(page).getAttribute("data-pf-attention"), "four open entries need a human");
      await openDock(page);
      const expected = [[X7, "候选模型已用尽"], [X6, "提交被丢弃"], [X5, "提交被拒绝"], [X4, "失败 · failed_gate"]];
      for (const [item, label] of expected) {
        await waitFor(async () => (await attentionCard(page, item).count()) === 1 || await attentionCard(page, item).count(), `${item.slug} has one attention card`);
        assert.equal(await cardsOf(page, item).count(), 1, `${item.slug} has exactly one card`);
        assert.equal((await attentionCard(page, item).locator("[data-pf-attention-marker]").innerText()).trim(), label, `${item.slug}'s marker`);
        await settled(() => bg(attentionCard(page, item)), isAmberTint, `${item.slug}'s card is amber`);
      }
      assert.deepEqual((await rowOrder(page)).slice(0, 5), [X7.wi, X6.wi, X5.wi, X4.wi, J1.wi], "feed-only cards first, newest first, then the live card");
      assert.equal(await cardsOf(page, X8).count(), 0, "a resolved entry makes no card");
      assert.equal(await cardsOf(page, X9).count(), 0, "an informational entry makes no card");
      // X5's 忽略 on its card (no detail opened) removes its card.
      await attentionCard(page, X5).locator("[data-pf-attention-dismiss]").click();
      await waitFor(() => dismissPosts().length === 1 || dismissPosts().length, "the dismiss posts");
      assert.deepEqual(dismissPosts()[0].body, { ids: [13] });
      await waitFor(async () => (await cardsOf(page, X5).count()) === 0 || await cardsOf(page, X5).count(), "the dismissed item's card is gone", TICK_MS);
      for (const item of [X7, X6, X4]) assert.equal(await attentionCard(page, item).count(), 1, `${item.slug} keeps its card`);
    });
  });

  await check("S458-5", async () => {
    fixture = pending();
    await withPage(async (page) => {
      await waitForPolyforgeToggle(page);
      await waitFor(async () => (await button(page).getAttribute("data-pf-attention")) === "2" || await button(page).getAttribute("data-pf-attention"), "the button carries the attention");
      await openDock(page);
      await waitFor(async () => (await attentionCard(page, J2).count()) === 1 || await attentionCard(page, J2).count(), "J2's card is an attention card");
      assert.equal(await cardsOf(page, J2).count(), 1, "J2 has one card although the feed holds its open needs_input entry");
      await settled(() => bg(attentionCard(page, J2)), isAmberTint, "J2's card is the amber tint");
      assert.equal(await attentionCard(page, J2).evaluate((el) => getComputedStyle(el).borderTopColor), AMBER, "the amber frame");
      const marker = attentionCard(page, J2).locator("[data-pf-needs-input]");
      assert.equal((await marker.innerText()).trim(), "等待输入");
      assert.equal(await marker.evaluate((el) => getComputedStyle(el).backgroundColor), AMBER, "the marker is solid amber");
      assert.equal(await marker.evaluate((el) => getComputedStyle(el).color), "rgb(255, 255, 255)", "white text on the marker");
      // answered: J2 runs, its entry resolves; the card is no longer tinted.
      fixture = { jobs: [J1, { ...J2, status: "running" }], decisions: pending().decisions.map((e) => (e.wi === J2.wi ? { ...e, resolved: true } : e)) };
      await waitFor(async () => {
        const card = cardsOf(page, J2);
        if ((await card.count()) !== 1) return `cards ${await card.count()}`;
        if ((await card.getAttribute("data-pf-card-attention")) !== null) return "still an attention card";
        if ((await card.locator("[data-pf-needs-input]").count()) > 0) return "still the needs-input marker";
        const now = await bg(card);
        return !isAmberish(now) || now;
      }, "the answered job's card is no longer tinted", TICK_MS);
    });
  });

  await check("S458-6", async () => {
    for (const theme of ["light", "dark"]) {
      fixture = blockedOnly();
      await withPage(async (page) => {
        await waitForPolyforgeToggle(page);
        assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), theme, `the page is in the ${theme} theme`);
        await waitFor(async () => (await button(page).getAttribute("data-pf-attention")) === "1" || await button(page).getAttribute("data-pf-attention"), "the button carries the attention");
        await openDock(page);
        await waitTitle(page, W.slug, "the walk opens W");
        await attentionCard(page, W).waitFor();
        // Measured selected (the walk's selection) and again deselected.
        for (const state of ["selected", "deselected"]) {
          if (state === "deselected") {
            await attentionCard(page, W).click();
            await waitFor(async () => (await attentionCard(page, W).getAttribute("aria-pressed")) === "false", "a second click deselects");
          }
          await settled(() => bg(attentionCard(page, W)), isAmberTint, `${theme}/${state}: the tint settles`);
          const m = await attentionCard(page, W).evaluate((el) => {
            const parse = (value) => {
              const match = /rgba?\(([^)]+)\)/.exec(value);
              if (!match) return { r: 0, g: 0, b: 0, a: 0 };
              const [r, g, b, a = 1] = match[1].split(/[\s,/]+/).filter(Boolean).map(Number);
              return { r, g, b, a };
            };
            const over = (top, base) => ({ r: top.r * top.a + base.r * (1 - top.a), g: top.g * top.a + base.g * (1 - top.a), b: top.b * top.a + base.b * (1 - top.a), a: 1 });
            const layers = [];
            for (let node = el; node; node = node.parentElement) layers.unshift(parse(getComputedStyle(node).backgroundColor));
            const composite = (list) => list.reduce((base, layer) => (layer.a > 0 ? over(layer, base) : base), { r: 255, g: 255, b: 255, a: 1 });
            const panel = composite(layers.slice(0, -1));
            const card = composite(layers);
            const slug = el.firstElementChild.firstElementChild;
            const text = over(parse(getComputedStyle(slug).color), card);
            const lum = (c) => {
              const ch = (v) => { const s = v / 255; return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4; };
              return 0.2126 * ch(c.r) + 0.7152 * ch(c.g) + 0.0722 * ch(c.b);
            };
            const [hi, lo] = [lum(text), lum(card)].sort((x, y) => y - x);
            return { slug: slug.textContent, delta: Math.abs(card.r - panel.r) + Math.abs(card.g - panel.g) + Math.abs(card.b - panel.b), contrast: (hi + 0.05) / (lo + 0.05) };
          });
          assert.equal(m.slug, W.slug, "the measured text is the slug");
          assert.ok(m.delta >= 30, `${theme}/${state}: the tint differs visibly from the panel (channel delta ${m.delta.toFixed(1)})`);
          assert.ok(m.contrast >= 4.5, `${theme}/${state}: the slug's contrast over the tint is ${m.contrast.toFixed(2)}, at least 4.5`);
        }
      }, { storage: { "pi-theme": theme } });
    }
  });

  await check("S499-2", async () => {
    fixture = dismissable();
    refuseDismiss = false;
    await withPage(async (page) => {
      await waitForPolyforgeToggle(page);
      // 1. five open entries; the dock shows the four feed-only cards.
      await waitFor(async () => (await button(page).getAttribute("data-pf-attention")) === "5" || await button(page).getAttribute("data-pf-attention"), "five open entries need a human");
      await openDock(page);
      for (const item of [X7, X6, X5, X4]) {
        await waitFor(async () => (await attentionCard(page, item).count()) === 1 || await attentionCard(page, item).count(), `${item.slug} has its attention card`);
      }
      // 2. the reason line (the newest open entry's phrase) and one 忽略 per feed-only card.
      const reason = (item) => attentionCard(page, item).locator("[data-pf-attention-reason]");
      const dismiss = (item) => attentionCard(page, item).locator("[data-pf-attention-dismiss]");
      await waitFor(async () => (await reason(X5).count()) === 1 && (await reason(X5).innerText()).trim() === "E2E-458 the submit_rejected phrase"
        || (await reason(X5).count() ? await reason(X5).innerText() : "no reason line"), "X5's card shows its newest open entry's reason");
      await waitFor(async () => (await reason(X4).count()) === 1 && (await reason(X4).innerText()).trim() === "E2E-458 the failed phrase"
        || (await reason(X4).count() ? await reason(X4).innerText() : "no reason line"), "X4's card shows its reason");
      assert.equal((await attentionCard(page, X5).locator("[data-pf-attention-marker]").innerText()).trim(), "提交被拒绝", "X5's marker is unchanged");
      await waitFor(async () => (await dismiss(X5).count()) === 1 || await dismiss(X5).count(), "X5's card has one 忽略");
      assert.equal((await dismiss(X5).innerText()).trim(), "忽略", "the button says 忽略");
      assert.equal(await cardsOf(page, J1).locator("[data-pf-attention-dismiss]").count(), 0, "the live card has no 忽略");
      // 3. refused: the card stays, the refusal shows on it, the count holds, the card is not selected.
      refuseDismiss = true;
      const before = dismissPosts().length;
      await dismiss(X5).click();
      await waitFor(() => dismissPosts().length === before + 1 || dismissPosts().length - before, "the refused dismiss posts once");
      assert.deepEqual(dismissPosts()[before].body, { ids: [13, 11] }, "the post names every open entry of X5, newest first");
      const refusal = attentionCard(page, X5).locator("[data-pf-attention-dismiss-error]");
      await waitFor(async () => (await refusal.count()) === 1 && (await refusal.innerText()).trim() === "忽略失败：E2E-499 refused"
        || (await refusal.count() ? await refusal.innerText() : "no refusal line"), "the refusal shows on X5's card");
      assert.equal(await attentionCard(page, X5).count(), 1, "X5's card stays");
      assert.equal(await button(page).getAttribute("data-pf-attention"), "5", "the button still counts five");
      await waitFor(async () => (await attentionCard(page, X5).getAttribute("aria-pressed")) === "false" || await attentionCard(page, X5).getAttribute("aria-pressed"),
        "the 忽略 click does not select X5");
      // 4. accepted: every open entry of X5 in one post, its card gone, the count down by two.
      refuseDismiss = false;
      await dismiss(X5).click();
      await waitFor(() => dismissPosts().length === before + 2 || dismissPosts().length - before, "the second dismiss posts");
      assert.deepEqual(dismissPosts()[before + 1].body, { ids: [13, 11] }, "the second post is exactly X5's open entries, newest first");
      await waitFor(async () => (await cardsOf(page, X5).count()) === 0 || await cardsOf(page, X5).count(), "X5's card is gone", TICK_MS);
      await waitFor(async () => (await button(page).getAttribute("data-pf-attention")) === "3" || await button(page).getAttribute("data-pf-attention"), "the button counts three", TICK_MS);
      for (const item of [X7, X6, X4]) assert.equal(await attentionCard(page, item).count(), 1, `${item.slug} keeps its card`);
    });
  });

  await check("S458-7", async () => {
    fixture = repliedOnly();
    await withPage(async (page) => {
      await waitForPolyforgeToggle(page);
      await waitFor(async () => (await button(page).innerText()).trim() === "1" || (await button(page).innerText()), "the status read lands (running count 1)");
      // The replied entry counts nowhere: the button stays plain, as with nothing pending.
      await settled(() => background(page), (value) => value === "rgba(0, 0, 0, 0)", "the button stays transparent with only a replied entry");
      assert.equal(await button(page).getAttribute("data-pf-attention"), null, "no data-pf-attention with only a replied entry");
      assert.equal(await button(page).getAttribute("aria-label"), "Polyforge", "the plain name");
      // The dock opens on no walk (nothing needs a human); J1's live card shows
      // no attention surface for the replied entry — it is not counted, so no
      // reason line and no 忽略 (the item panel this scenario
      // opened on main is gone; the replied state stays the entry component's).
      await openDock(page);
      const j1 = cardsOf(page, J1);
      await j1.waitFor();
      assert.equal(await j1.getAttribute("data-pf-card-attention"), null, "J1's live card is no attention card");
      assert.equal(await j1.locator("[data-pf-attention-reason]").count(), 0, "no reason line for a replied entry");
      assert.equal(await j1.locator("[data-pf-attention-dismiss]").count(), 0, "no 忽略 for a replied entry");
      // With the dock closed again, the button settles plain: the replied entry needs no human.
      await button(page).click();
      await waitFor(async () => (await dock(page).count()) === 0, "the button closes the dock");
      // A new open question of J1: the button counts exactly that one.
      fixture = repliedThenAsked();
      await waitFor(async () => (await button(page).getAttribute("data-pf-attention")) === "1" || await button(page).getAttribute("data-pf-attention"),
        "the new open question alone counts (data-pf-attention=1)", TICK_MS);
      await settled(() => background(page), (value) => value === AMBER, "the button is amber");
      assert.equal(await button(page).getAttribute("data-pf-attention"), "1", "the replied entry is still not counted");
      // The walk opens J1's detail: the new question is the live card's reason, with one 忽略;
      // the replied entry still renders nothing.
      await button(page).click();
      await dock(page).waitFor();
      await waitTitle(page, J1.slug, "the walk opens J1's detail");
      await waitFor(async () => (await j1.getAttribute("aria-pressed")) === "true" || await j1.getAttribute("aria-pressed"), "the walk selected J1");
      const reason = j1.locator("[data-pf-attention-reason]");
      await waitFor(async () => (await reason.count()) === 1 && (await reason.innerText()).trim() === "E2E-458 the needs_input phrase"
        || (await reason.count() ? await reason.innerText() : "no reason line"), "the new question is the card's reason");
      assert.equal(await j1.locator("[data-pf-attention-dismiss]").count(), 1, "the new question carries one 忽略");
    });
  });

  if (errors.length) console.log(`\nBROWSER ERRORS: ${errors.join(" | ")}`);
  if (failures.length || errors.length) {
    console.log(`\nFAILED ${failures.join(", ")} — ${failures.length} scenario(s) red`);
    process.exitCode = 1;
  } else {
    console.log("\nPASS all attention-458 scenarios");
  }
} finally {
  await h.close();
  rmSync(work, { force: true, recursive: true });
}
