// carries: components/PolyforgeDecisionEntry*, app/api/polyforge/answer/*
// carries: lib/polyforge-decisions*, app/api/polyforge/polyforge-decisions-routes*, e2e/polyforge-dock.mjs
// — every human decision is handled where its subject lives.
//
// The chat subscription model is retired: the daemon's machine-level decision
// feed (`pf --json watch decisions`, served as `GET /api/polyforge/decisions`)
// is shown per work item (owner ruling 2026-11-09;): a work
// item the daemon no longer holds has a feed-only attention card in the Jobs
// row with the entry's reason and 忽略 (dismissing the item's open entries); a
// live job is its card — the flow and one input box, with the waiting
// question or the park cause (and its copyable `pf resume` / `pf cancel`)
// above it; its Jobs-row card carries the newest counted entry's reason and
// 忽略 (the item's open entries but a waiting job's question); the job card
// has no 交互 button and no panel of entries. A
// background task's summary sits on its row in Work › Tasks. The Polyforge
// button turns amber while anything needs a human (the waiting jobs plus the
// other open entries; retired the separate reminder), its
// name carries the count, and each opening walks it, selecting each target.
//
// The suite starts its own `next dev` and drives the panel through its HTTP
// faces mocked in the browser with the routes' documented wire shapes
// (`/api/polyforge/status`, `/cards`, `/watch`, `/decisions`,
// `/decisions/dismiss`, `/tasks`), the panel-213 convention: no pf CLI or
// daemon runs (the routes' server side is
// app/api/polyforge/polyforge-decisions-routes.test.mjs, with a fake pf) —
// except `/answer`, which reaches the real route and a fake pf that records
// its argv. Every scenario prints `PASS S415-<n>` (and `PASS S499-3`); the run ends
// `PASS all decisions-415 scenarios`.
//
//   S415-1 no 决策 sub-tab and no 交互 button: a live job's entries are not
//          listed in Work › 作业 (its card is the flow and the input box);
//          a dropped work item's feed-only card shows its class label, the
//          entry's reason and 忽略; the waiting job's question sits verbatim
//          above its input box.
//   S415-2 the button counts the waiting job plus the other open entries (a
//          needs_input entry of the waiting job counted once), dock closed.
//   S415-3 the walk, one step per opening of the dock from the button (the
//          button closes it in between): the waiting job (its question in
//          the composer); then the newest open entry's live job (no 继续);
//          then the failed task in Work › Tasks; then a work item the daemon
//          dropped — its feed-only card selected in Work › 作业, its job
//          detail from the watch snapshot, the `blocked` cause rendered once
//          above the input box (#858's read-only face: a `driver` block,
//          labelled, step, reason, required action) with the copyable
//          `pf resume` command — no 继续 button: the card's
//          box stays open, a message wakes it.
//   S415-4 background tasks: both task ends on their rows in Work › Tasks (the
//          failed one marked), each summary in its detail, the failed one
//          dismissible.
//   S415-5 dismiss through a feed-only attention card's 忽略: the posted body
//          (the item's open entries), the card goes, the Polyforge button's
//          count follows.
//   S499-3 a live job's Jobs-row card carries its counted entries: the newest
//          one's reason and 忽略 (J1's six open entries; the waiting J2's only
//          entry is its question, so its card carries none); a refused
//          dismiss keeps them and shows the refusal on the card; an accepted
//          one posts every counted entry, newest first, the reason and 忽略
//          go, the card stays, the button's count drops by that many.
//   S415-6 nothing of the retired chat face is requested.
//   S415-7 no option path (workflow contract v2 change 19): options a stale
//          snapshot still names are never shown under the question, and a
//          typed option-like word runs the real answer route as words: the
//          fake pf receives `answer <wi> --text=<words>`, never --option.
//
//   node e2e/polyforge-decisions-415.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { polyforgeDock as dock, polyforgeToggle } from "./polyforge-dock.mjs";
import { ACTION_MS, NAV_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, mockContextUsage, mockPolyforge, NO_MESSAGES, NO_PROGRESS, reply as json, suiteDist, writeHostSession } from "./harness.mjs";

const distDir = suiteDist("polyforge-decisions-415");
assertNoDevServer(distDir);
// the project path always holds "polyforge", so the toggle
// lookup meets it whatever TMPDIR is.
const work = mkdtempSync(join(tmpdir(), "pi-web-polyforge-415-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-decisions-415-session";
const HOST_TEXT = "E2E pf decisions 415 host session";
const EPOCH = "2026-11-09T03:00:00.000Z";
// The route that reaches the server: a typed message → POST /api/polyforge/answer →
// `pf --json answer <wi> --text=…` (workflow contract v2 change 14: no
// question id; change 19: no option); this fake pf records its argv (every
// other polyforge route is answered in the browser).
const fakePf = join(work, "fake-pf");
const pfArgv = join(work, "pf-argv.jsonl");
mkdirSync(join(work, "data"));
writeFileSync(fakePf, `#!${process.execPath}\nrequire("fs").appendFileSync(${JSON.stringify(pfArgv)}, JSON.stringify(process.argv.slice(2)) + "\\n");\nprocess.stdout.write("ok\\n");\n`);
chmodSync(fakePf, 0o755);
const pfCalls = () => (existsSync(pfArgv) ? readFileSync(pfArgv, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)) : []);
mkdirSync(project);
writeHostSession(agentDir, { id: SESSION, cwd: project, text: HOST_TEXT, timestamp: EPOCH });

// ---- fixture: a running job, a waiting job, a dropped work item, two tasks ----
const J1 = { wi: "wi_E2E415J1", slug: "demo#4151", status: "running", stepAgeSec: 60, idleAgeSec: 5, candidate: "pi:glm-e2e", spawnN: 1 };
const J2 = { wi: "wi_E2E415J2", slug: "demo#4152", status: "needs_input", stepAgeSec: 60, idleAgeSec: 5, candidate: "pi:glm-e2e", spawnN: 1 };
// The daemon dropped this one (no job): its detail is the watch snapshot.
const W3 = { wi: "wi_E2E415W3", slug: "demo#4153" };
const TASK_DONE = "task_0123456789abcdef";
const TASK_FAILED = "task_fedcba9876543210";
const LONG_SUMMARY = Array.from({ length: 12 }, (_, i) => `E2E-415 summary line ${i + 1}`).join("\n");
const QUESTION = "E2E-415 which colour?";
// The watch route's sanitized face of `execution.blocked` (#858's
// sanitizeBlocked: evidence and the log tail never leave the server).
// A `driver` block (PR #860 code review: the `pf resume` command is offered for every kind, the
// driver's park included — its own text says `pf resume`).
const BLOCKED = { kind: "driver", step: "verification", reason: "E2E-415 the driver parked the workflow",
  requiredAction: "E2E-415 resolve the cause, then resume the work item (`pf resume`) or cancel it" };
const at = (n) => `2026-11-09T10:${String(n).padStart(2, "0")}:00Z`;
const entry = (id, item, cls, source, fields = {}) => ({ id, at: at(id), kind: "work_item", profile: "default", wi: item.wi, slug: item.slug,
  class: cls, source, phrase: `E2E-415 the ${source} phrase`, attention: false, resolved: false, dismissed: false, ...fields });
const task = (id, taskId, cls, text, attention) => ({ id, at: at(id), kind: "task", profile: "default", task_id: taskId, class: cls, source: "task",
  phrase: `the background task ${cls}`, text, attention, resolved: false, dismissed: false });
// Newest first, as the daemon orders the feed. `label` (zh-CN class label) and
// `showSource` (the source chip shown) are test-only keys, not on the wire.
const ENTRIES = [
  { ...entry(30, J1, "failed", "failed_gate", { attention: true }), label: "失败", showSource: "failed_gate" },
  { ...task(29, TASK_FAILED, "failed", "E2E-415 the task failed", true), label: "失败" },
  { ...entry(28, W3, "blocked", "blocked", { attention: true }), label: "已阻塞" },
  { ...entry(27, J2, "needs_input", "needs_input", { question_id: "q-colour", attention: true }), label: "等待你回答" },
  { ...entry(25, J1, "failed", "failed", { attention: true }), label: "失败" },
  { ...entry(24, J1, "candidates_exhausted", "candidates_exhausted", { attention: true }), label: "候选模型已用尽" },
  { ...entry(23, J1, "carrier_push_failed", "carrier_push_failed", { repo: "e2e-repo", attention: true }), label: "推送失败" },
  { ...entry(22, J1, "submit_rejected", "submit_rejected", { attention: true }), label: "提交被拒绝" },
  { ...entry(21, J1, "submit_dropped", "submit_dropped", { attention: true }), label: "提交被丢弃" },
  { ...entry(19, W3, "completed", "completed"), label: "已完成" },
  { ...entry(18, W3, "completed", "wrapped"), label: "已完成", showSource: "wrapped" },
  { ...entry(17, W3, "canceled", "canceled"), label: "已取消" },
  { ...entry(16, J2, "paused", "paused", { attention: true, resolved: true }), label: "已暂停" },
  { ...entry(15, W3, "terminal", "terminal"), label: "已结束" },
  { ...entry(14, J1, "step_dead", "step_dead"), label: "step 进程已退出" },
  { ...entry(13, J1, "daemon_restarted", "daemon_restarted", { dismissed: true }), label: "daemon 已重启" },
  { ...task(12, TASK_DONE, "completed", LONG_SUMMARY, false), label: "已完成" },
  { ...entry(11, J1, "brand_new_class", "brand_new_class", { phrase: "E2E-415 an unknown class phrase" }), label: "E2E-415 an unknown class phrase" },
];
const byItem = (item) => ENTRIES.filter((e) => e.kind === "work_item" && e.wi === item.wi);
const isOpen = (e) => e.attention && !e.resolved && !e.dismissed;
// J1's open entries; J2's needs_input entry is its waiting job (counted once).
const J1_OPEN_IDS = byItem(J1).filter(isOpen).map((e) => e.id);
assert.deepEqual(J1_OPEN_IDS, [30, 25, 24, 23, 22, 21], "J1's open entries (fixture drift fails loudly)");
const J1_OPEN = J1_OPEN_IDS.length;
const REMINDER = 1 + J1_OPEN + 1 /* W3 blocked */ + 1 /* failed task */;
let feed;
let posts;
let requested;
// S499-3: while set, the dismiss route refuses (records the post, marks nothing).
let refuseDismiss = false;
const resetFeed = () => {
  feed = ENTRIES.map((e) => Object.fromEntries(Object.entries(e).filter(([key]) => key !== "label" && key !== "showSource")));
  posts = [];
};
const feedBody = () => ({ pfPresent: true, feed: { decisions: feed, attention: feed.filter(isOpen).length } });
const statusBody = () => ({ pfPresent: true, status: { queue: { running: 1, max: 8 }, jobs: [J1, J2], tasks: { running: 0, completed: 1 } } });
const card = (job) => ({ job, goal: `E2E 415 goal of ${job.slug}`, startedAt: EPOCH,
  steps: [{ id: "build", state: "running", runs: 1 }], currentStep: "build", progress: { available: true, entries: [] } });
// a stale snapshot's options of J2's question (S415-7 only): never shown.
let optionsOn = false;
const OPTIONS = { questionId: "q-colour", options: [
  { id: "revert", label: "E2E-415 agent: please revert", action: "revert" },
  { id: "clear", label: "E2E-415 agent: clear the hold", action: "clear" },
  { id: "keep", label: "E2E-415 keep going" },
] };
const watchBody = (wi) => {
  if (wi === W3.wi) {
    return { status: { wi: W3.wi, slug: W3.slug, workflowStatus: "blocked", currentStep: "verification", updatedAt: EPOCH,
      steps: [{ name: "verification", status: "blocked", startedAt: EPOCH }], declared_steps: [{ id: "verification" }],
      events: [{ at: EPOCH, kind: "step_started", step: "verification" }, { at: EPOCH, kind: "terminal", status: "blocked" }], blocked: BLOCKED } };
  }
  const job = [J1, J2].find((candidate) => candidate.wi === wi || candidate.slug === wi) ?? J1;
  return { status: { wi: job.wi, slug: job.slug, workflowStatus: job.status, currentStep: "build", updatedAt: EPOCH,
    steps: [{ name: "build", status: job.status === "needs_input" ? "needs_input" : "running", startedAt: EPOCH }], declared_steps: [{ id: "build" }],
    events: [{ at: EPOCH, kind: "step_started", step: "build", model: "glm-e2e" },
      ...(job.status === "needs_input" ? [{ at: EPOCH, kind: "needs_input", step: "build", question: QUESTION, question_id: "q-colour" }] : [])],
    // workflow contract v2 (change 14): the question the step waits on is the snapshot's pendingQuestion
    ...(job.status === "needs_input" ? { pendingQuestion: { id: "q-colour", step: "build", question: QUESTION } } : {}),
    ...(optionsOn && job === J2 ? { questionOptions: OPTIONS } : {}) } };
};
const taskRow = (id, state) => ({ id, project: "demo", state, prompt_summary: `E2E-415 prompt of ${id}` });
const taskDetail = (id) => ({ id, project: "demo", state: id === TASK_DONE ? "completed" : "failed", candidates: ["pi:glm-e2e"], max_duration_ns: 0, max_tokens: 0,
  wall_consumed_ns: 1_000_000_000, tokens_consumed: 10, created_at: EPOCH, prompt: `E2E-415 TASK PROMPT ${id}`, report: "E2E-415 report", report_len: 14, truncated: false });

// ---- scenario bookkeeping and helpers ----
const failures = [];
const check = createCheck({ failures });
const CONTEXT_USAGE = { percent: 10, contextWindow: 200_000, tokens: 20_000 };
const h = createHarness({
  viewport: { width: 1280, height: 900 },
  locale: "zh-CN",
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN", "pi-web:polyforge-dock-width": "640" },
  timeout: ACTION_MS,
  errors: ["pageerror"],
  session: SESSION,
  ready: HOST_TEXT,
  async setup(page) {
    page.on("request", (request) => requested.push(new URL(request.url()).pathname));
    await mockPolyforge(page, {
      answer: (url, route) => route.fallback(), // the real route, the fake pf
      status: () => statusBody(),
      cards: () => ({ pfPresent: true, cards: [J1, J2].map(card) }),
      watch: (url) => watchBody(url.searchParams.get("wi")),
      progress: NO_PROGRESS,
      decisions: () => feedBody(),
      "decisions/dismiss": (url, route) => {
        if (route.request().method() !== "POST") return {};
        const body = route.request().postDataJSON();
        posts.push(body);
        if (refuseDismiss) return json(route, { ok: false, error: "E2E-499 refused" }, 409);
        let dismissed = 0;
        for (const e of feed) if (isOpen(e) && (body.ids ?? []).includes(e.id)) { e.dismissed = true; dismissed += 1; }
        return { ok: true, dismissed };
      },
      tasks: () => ({ pfPresent: true, capable: true, tasks: [taskRow(TASK_FAILED, "failed"), taskRow(TASK_DONE, "completed")] }),
      "tasks/*": (url) => ({ task: taskDetail(decodeURIComponent(url.pathname.split("/").pop())) }),
      messages: NO_MESSAGES,
    });
    await mockContextUsage(page, SESSION, CONTEXT_USAGE);
  },
});
const { errors, withPage } = h;
const waitFor = createWaitFor({ ms: 20_000, done: (value) => value === true, describe: (label, last) => `${label} (last: ${JSON.stringify(last)})` });
// owner-intended change the separate reminder is gone; the
// Polyforge button itself is amber and carries the count (data-pf-attention).
const badge = (page) => page.locator("[data-pf-toggle][data-pf-attention]");
const attentionCount = async (page) => page.locator("[data-pf-toggle]").first().getAttribute("data-pf-attention");
// One step of the walk: close the dock with the button when it is open, then
// open it with the button (the opening walks to the next item).
async function walkNext(page) {
  if ((await dock(page).count()) > 0) {
    await badge(page).click();
    await waitFor(async () => (await dock(page).count()) === 0, "the button closes the dock");
  }
  await badge(page).click();
  await dock(page).waitFor();
}
const entryIn = (scope, id) => scope.locator(`[data-pf-decision="${id}"]`);
const workTab = (page, name) => dock(page).locator('[data-pf-tabstrip="work"] [role="tab"]').filter({ hasText: name }).first();
const titleText = async (page) => {
  const title = dock(page).locator("[data-pf-detail-title]");
  return (await title.count()) > 0 ? (await title.first().innerText()).replace(/\s+/g, " ") : null;
};
const waitTitle = (page, slug, label) => waitFor(async () => { const t = await titleText(page); return Boolean(t && t.includes(slug)) || t; }, label);
// owner-intended change the click on the (amber) button opens
// the dock through the walk, on the waiting job J2.
async function openDock(page) {
  await badge(page).waitFor({ timeout: NAV_MS });
  await polyforgeToggle(page).click();
  await dock(page).waitFor();
  await waitTitle(page, J2.slug, "the opening walks to the waiting job");
}
async function openJob(page, job) {
  await dock(page).locator(`[data-pf-card-wi="${job.wi}"]`).click();
  await waitTitle(page, job.slug, `${job.slug}'s detail opens`);
}
const liveCard = (page, job) => dock(page).locator(`[data-pf-card][data-pf-card-wi="${job.wi}"]`);
const attentionCard = (page, item) => dock(page).locator(`[data-pf-card-attention][data-pf-card-wi="${item.wi}"]`);
const interactionsButton = (page) => dock(page).getByRole("button", { name: /^交互/ });
const shownCommands = (page) => dock(page).locator("[data-pf-chat] [data-pf-chat-commands] [data-pf-chat-command]")
  .evaluateAll((rows) => rows.map((row) => [row.getAttribute("data-pf-chat-command"), row.querySelector("code")?.textContent]));
async function assertEntries(scope, expected) {
  for (const e of expected) {
    const row = entryIn(scope, e.id);
    await row.waitFor({ timeout: 10_000 });
    assert.equal((await row.locator("[data-pf-decision-label]").innerText()).trim(), e.label, `#${e.id} ${e.class} label`);
    const source = row.locator("[data-pf-decision-source]");
    if (e.showSource) assert.equal((await source.innerText()).trim(), e.showSource, `#${e.id} shows its source`);
    else assert.equal(await source.count(), 0, `#${e.id} shows no source`);
    const state = e.resolved ? "resolved" : e.dismissed ? "dismissed" : "open";
    assert.equal(await row.getAttribute("data-pf-decision-state"), state, `#${e.id} state`);
    const opacity = Number(await row.evaluate((el) => getComputedStyle(el).opacity));
    assert.ok(state === "open" ? opacity === 1 : opacity < 1, `#${e.id} is ${state === "open" ? "not " : ""}muted (${opacity})`);
    assert.equal(await row.locator("[data-pf-decision-dismiss]").count(), isOpen(e) ? 1 : 0, `#${e.id} dismiss only while open`);
  }
}

try {
  await h.launchBrowser(() => chromium.launch());
  await h.startServer({ name: "polyforge-decisions-415", logFile: join(work, "server.log"), distDir, env: {
    PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "", PF_BIN: fakePf, POLYFORGE_DATA_DIR: join(work, "data") } });

  await check("S415-1", async () => {
    resetFeed(); requested = [];
    await withPage(async (page) => {
      await openDock(page);
      const tabs = await dock(page).locator('[data-pf-tabstrip="work"] [role="tab"]').allInnerTexts();
      // intentional fourth sub-tab: 待集成 joins the Work rail after 工单列表.
      assert.deepEqual(tabs.map((text) => text.trim().split(/\s/)[0]), ["作业", "任务", "工单列表", "待集成"], "no 决策 sub-tab");
      // J1: its card is the flow and the input box — no 交互 button, none of its entries listed.
      await openJob(page, J1);
      await dock(page).locator("[data-pf-chat]").waitFor();
      await dock(page).locator("[data-pf-chat-input]").waitFor();
      assert.equal(await interactionsButton(page).count(), 0, "the job card has no 交互 button");
      assert.equal(await dock(page).locator("[data-pf-decision]").count(), 0, "a live job's entries are not listed in Work › 作业");
      assert.equal(await dock(page).locator("[data-pf-chat-question]").count(), 0, "a running job asks nothing");
      // W3 (dropped): its feed-only attention card carries the label, the reason and 忽略.
      const w3 = attentionCard(page, W3);
      await w3.waitFor();
      assert.equal((await w3.locator("[data-pf-attention-marker]").innerText()).trim(), "已阻塞", "W3's class label");
      await waitFor(async () => (await w3.locator("[data-pf-attention-reason]").count()) === 1, "W3's card shows the entry's reason");
      assert.equal((await w3.locator("[data-pf-attention-reason]").innerText()).trim(), "E2E-415 the blocked phrase");
      assert.equal(await w3.locator("[data-pf-attention-dismiss]").count(), 1, "W3's card offers 忽略");
      // J2: the waiting job — its question verbatim above the input box.
      await openJob(page, J2);
      await dock(page).locator("[data-pf-chat-question]").waitFor();
      assert.equal(await dock(page).locator("[data-pf-chat-question-text]").innerText(), QUESTION, "the question, verbatim, above the input box");
      assert.equal(await interactionsButton(page).count(), 0, "no 交互 button on the waiting job either");
    });
  });

  await check("S415-2", async () => {
    resetFeed(); requested = [];
    await withPage(async (page) => {
      await badge(page).waitFor({ timeout: NAV_MS });
      assert.equal(await dock(page).count(), 0, "the dock is closed: the button says it on its own");
      // the wait ends only on the expected state — the count AND the zh-CN
      // name that carries it (the name renders with the count, but an early
      // render may still be in another locale); anything else is "not yet"
      const title = new RegExp(`本机有 ${REMINDER} 项需要你处理`);
      await waitFor(async () => {
        const seen = { count: await attentionCount(page), title: await badge(page).getAttribute("title") };
        return (seen.count === String(REMINDER) && title.test(seen.title ?? "")) || seen;
      }, `the button counts ${REMINDER} and its name says so`);
      assert.match(await badge(page).getAttribute("title"), title);
    });
  });

  await check("S415-3", async () => {
    resetFeed(); requested = [];
    await withPage(async (page) => {
      await badge(page).waitFor({ timeout: NAV_MS });
      await walkNext(page);
      await waitTitle(page, J2.slug, "the first click opens the waiting job");
      await dock(page).locator("[data-pf-chat-question]").waitFor(); // workflow contract v2 (change 14): the question sits in the card's conversation
      await walkNext(page);
      await waitTitle(page, J1.slug, "the next opening opens the newest open entry's live job");
      await dock(page).locator("[data-pf-chat]").waitFor();
      assert.equal(await dock(page).locator("[data-pf-resume-button]").count(), 0, "a running item offers no 继续");
      await walkNext(page);
      await waitFor(async () => (await workTab(page, "任务").getAttribute("aria-selected")) === "true", "then the failed task opens Tasks");
      await dock(page).getByText(`E2E-415 TASK PROMPT ${TASK_FAILED}`).waitFor();
      await walkNext(page);
      await waitTitle(page, W3.slug, "then the dropped work item's job detail opens from its watch snapshot");
      assert.equal(await workTab(page, "作业").getAttribute("aria-selected"), "true", "in Work › Jobs, no tab of its own");
      await waitFor(async () => (await attentionCard(page, W3).getAttribute("aria-pressed")) === "true", "the walk selects W3's attention card");
      // #858's read-only blocked display, above the card's input box: kind,
      // step, reason and the required action; evidence and the log tail never
      // leave the server.
      const blocked = dock(page).locator('[data-pf-chat] [data-pf-chat-park-cause="driver"]');
      await blocked.waitFor();
      assert.match(await blocked.innerText(), /已阻塞 · 被调度器暂停/, "a known kind is labelled");
      assert.equal((await blocked.locator("[data-pf-chat-park-step]").innerText()).trim(), "verification");
      assert.equal(await dock(page).locator("[data-pf-chat-park-cause]").count(), 1, "the blocked cause renders once");
      assert.equal(await blocked.locator("[data-pf-chat-park-reason]").innerText(), BLOCKED.reason);
      assert.match(await blocked.locator("[data-pf-chat-park-action]").innerText(), /E2E-415 resolve the cause, then resume the work item/);
      assert.doesNotMatch(await dock(page).innerText(), /runtime-state\/verify\.log/, "the evidence path never reaches the page");
      assert.deepEqual(await shownCommands(page), [["resume", "pf resume wi_E2E415W3"]], "the copyable pf resume, alone");
      // workflow contract 4 (/#488): the card's box stays open on
      // the park — a message wakes it — and no 继续 button exists anywhere
      await waitFor(async () => (await dock(page).locator("[data-pf-chat]").getAttribute("data-pf-chat-state")) === "parked", "the card's box stays open on the park");
      assert.equal(await dock(page).locator("[data-pf-resume-button], [data-pf-chat-menu]").count(), 0, "no 继续 in the dock, on the cause or the card");
      assert.deepEqual(pfCalls().filter((args) => args.includes("resume")), [], "nothing runs `pf resume`");
    });
  });

  await check("S415-4", async () => {
    resetFeed(); requested = [];
    await withPage(async (page) => {
      await openDock(page);
      await workTab(page, "任务").click();
      const failedRow = dock(page).locator(`button[data-pf-task="${TASK_FAILED}"]`);
      const doneRow = dock(page).locator(`button[data-pf-task="${TASK_DONE}"]`);
      await failedRow.waitFor();
      await waitFor(async () => (await failedRow.locator("[data-pf-task-attention]").count()) === 1, "the failed task's row is marked");
      assert.equal(await doneRow.locator("[data-pf-task-attention]").count(), 0, "a completed task needs nothing");
      await doneRow.click();
      const done = dock(page).locator("[data-pf-task-decisions]");
      await done.waitFor();
      await assertEntries(done, ENTRIES.filter((e) => e.id === 12));
      const text = entryIn(done, 12).locator("[data-pf-decision-text]");
      assert.equal(await text.evaluate((el) => getComputedStyle(el).whiteSpace), "pre-wrap");
      assert.equal(await text.innerText(), LONG_SUMMARY, "the summary verbatim");
      const folded = await text.evaluate((el) => el.clientHeight);
      await entryIn(done, 12).locator("[data-pf-decision-text-toggle]").click();
      await waitFor(async () => (await text.evaluate((el) => el.clientHeight)) > folded, "the long summary unfolds");
      await dock(page).getByRole("button", { name: /返回|back/i }).first().click();
      await failedRow.click();
      const failed = dock(page).locator("[data-pf-task-decisions]");
      await assertEntries(failed, ENTRIES.filter((e) => e.id === 29));
      await entryIn(failed, 29).locator("[data-pf-decision-dismiss]").click();
      await waitFor(async () => (await entryIn(failed, 29).getAttribute("data-pf-decision-state")) === "dismissed", "the task entry is dismissed");
      assert.deepEqual(posts, [{ ids: [29] }]);
    });
  });

  await check("S415-5", async () => {
    resetFeed(); requested = [];
    await withPage(async (page) => {
      await openDock(page);
      await waitFor(async () => (await attentionCount(page)) === String(REMINDER) || await attentionCount(page), "the button's count before");
      const w3 = attentionCard(page, W3);
      await w3.waitFor();
      await w3.locator("[data-pf-attention-dismiss]").click();
      await waitFor(async () => (await attentionCard(page, W3).count()) === 0, "W3's card is gone");
      assert.deepEqual(posts, [{ ids: [28] }], "W3's only open entry is posted");
      await waitFor(async () => (await attentionCount(page)) === String(REMINDER - 1) || await attentionCount(page), "the Polyforge button's count drops by one");
    });
  });

  await check("S499-3", async () => {
    resetFeed(); requested = [];
    try {
      await withPage(async (page) => {
        // 1. the opening walks to J2; the button counts every needed human.
        await openDock(page);
        await waitFor(async () => (await attentionCount(page)) === String(REMINDER) || await attentionCount(page), `the button counts ${REMINDER}`);
        // 2. J1's live card: the newest counted entry's reason and one 忽略; still no attention card.
        const j1 = liveCard(page, J1);
        const reason = j1.locator("[data-pf-attention-reason]");
        const dismiss = j1.locator("[data-pf-attention-dismiss]");
        const refusal = j1.locator("[data-pf-attention-dismiss-error]");
        await waitFor(async () => (await reason.count()) === 1 && (await reason.innerText()).trim() === "E2E-415 the failed_gate phrase"
          || (await reason.count() ? await reason.innerText() : "no reason line"), "J1's card shows its newest open entry's reason");
        await waitFor(async () => (await dismiss.count()) === 1 || await dismiss.count(), "J1's card has one 忽略");
        assert.equal((await dismiss.innerText()).trim(), "忽略", "the button says 忽略");
        assert.equal(await j1.getAttribute("data-pf-card-attention"), null, "J1's card stays a live card, not an attention card");
        const j2 = liveCard(page, J2);
        assert.equal(await j2.count(), 1, "J2 has its live card");
        assert.equal(await j2.locator("[data-pf-attention-dismiss]").count(), 0, "the waiting J2's card has no 忽略: its entry is the question in its composer");
        assert.equal(await j2.locator("[data-pf-attention-reason]").count(), 0, "the waiting J2's card has no reason line");
        // 3. refused: the reason and 忽略 stay, the refusal shows, the count holds, J1 is not selected.
        refuseDismiss = true;
        await dismiss.click();
        await waitFor(() => posts.length === 1 || posts.length, "the refused dismiss posts once");
        assert.deepEqual(posts, [{ ids: [30, 25, 24, 23, 22, 21] }], "every counted entry of J1, newest first");
        await waitFor(async () => (await refusal.count()) === 1 && (await refusal.innerText()).trim() === "忽略失败：E2E-499 refused"
          || (await refusal.count() ? await refusal.innerText() : "no refusal line"), "the refusal shows on J1's card");
        assert.equal(await reason.count(), 1, "the reason stays after a refusal");
        assert.equal(await dismiss.count(), 1, "忽略 stays after a refusal");
        assert.equal(await attentionCount(page), String(REMINDER), "the count holds after a refusal");
        await waitFor(async () => (await j1.getAttribute("aria-pressed")) === "false" || await j1.getAttribute("aria-pressed"), "the 忽略 click does not select J1");
        await waitTitle(page, J2.slug, "the detail stays J2's");
        // 4. accepted: one post of every counted entry; the reason, 忽略 and the error go; the card stays.
        refuseDismiss = false;
        await dismiss.click();
        await waitFor(() => posts.length === 2 || posts.length, "the second dismiss posts");
        assert.deepEqual(posts[1], { ids: J1_OPEN_IDS }, "the second post is exactly J1's counted entries, newest first");
        await waitFor(async () => (await reason.count()) === 0 && (await dismiss.count()) === 0 && (await refusal.count()) === 0
          || { reason: await reason.count(), dismiss: await dismiss.count(), refusal: await refusal.count() }, "J1's card loses the reason, 忽略 and the refusal");
        assert.equal(await liveCard(page, J1).count(), 1, "J1's live card stays");
        await waitFor(async () => (await attentionCount(page)) === String(REMINDER - J1_OPEN) || await attentionCount(page), `the count drops by ${J1_OPEN}`);
        assert.equal(await attentionCard(page, W3).locator("[data-pf-attention-dismiss]").count(), 1, "W3's feed-only card keeps its 忽略");
      });
    } finally {
      refuseDismiss = false;
      resetFeed(); requested = [];
    }
  });

  await check("S415-7", async () => {
    // workflow contract v2 change 19: no option path — a stale snapshot's
    // options are never shown, and a typed option-like word goes as words.
    resetFeed(); optionsOn = true;
    try {
      await withPage(async (page) => {
        // owner-intended change the opening walks to the
        // waiting job J2.
        await openDock(page);
        const question = dock(page).locator("[data-pf-chat-question]");
        await question.waitFor();
        assert.equal(await question.locator("[data-pf-chat-question-options], [data-pf-chat-option], button[data-pf-option]").count(), 0, "no option is shown");
        const input = dock(page).locator("[data-pf-chat-input]");
        await input.waitFor();
        await input.fill("revert");
        await input.press("Enter");
        await waitFor(async () => pfCalls().some((args) => args.includes("answer")), "the fake pf received the answer");
        assert.deepEqual(pfCalls().filter((args) => args.includes("answer")), [["--json", "answer", "wi_E2E415J2", "--text=revert"]],
          "the typed word rides as --text; no --option, no --question");
        await dock(page).locator('[data-pf-progress-kind="owner"]').waitFor();
        assert.match(await dock(page).locator('[data-pf-progress-kind="owner"]').innerText(), /revert/, "the owner's words are echoed into the flow");
      });
    } finally {
      optionsOn = false;
    }
  });

  await check("S415-6", async () => {
    const retired = requested.filter((path) => /\/api\/sessions\/[^/]+\/(chat-id|decisions)$/.test(path));
    assert.deepEqual(retired, [], "no retired chat route was requested");
    assert.ok(requested.some((path) => path === "/api/polyforge/decisions"), "the feed was read");
  });

  if (errors.length) console.log(`\nBROWSER ERRORS: ${errors.join(" | ")}`);
  if (failures.length || errors.length) {
    console.log(`\nFAILED ${failures.join(", ")} — ${failures.length} scenario(s) red`);
    process.exitCode = 1;
  } else {
    console.log("\nPASS all decisions-415 scenarios");
  }
} finally {
  await h.close();
  rmSync(work, { force: true, recursive: true });
}
