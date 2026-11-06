// carries: components/PolyforgeJobComposer*, components/ComposerBox*, components/PolyforgeProgressSubagent*
// carries: app/api/polyforge/answer/*, lib/polyforge-conversation-pure*, app/api/polyforge/polyforge-answer-route*, lib/clipboard*, e2e/polyforge-dock.mjs
// workflow contract v2, change 14 the job card is a chat-style
// session with one input box; the discussion assistant is gone. Workflow
// contract 4: a parked work item keeps the box — a message
// wakes it, and a copyable `pf resume` sits above it and
// only a park whose agent cannot start shows the reason with copyable
// `pf resume` / `pf cancel` instead of the box.
//
// The job detail's execution progress is the step's conversation: the agent's
// narration and tool cards as before, the agent's `pf step ask` question as a
// highlighted block where it was asked, the owner's messages as user bubbles
// with their time, `delivered` / `intent` / `blocked` as status rows. Under the
// scroll region sits the card's bottom: the question the step waits on
// (highlighted, verbatim) and ONE input box whose words go to the step's own
// agent through `POST /api/polyforge/answer` → `pf --json answer <wi> --text=…`,
// no question id — enabled while the step runs AND while it waits (a reply and
// an interjection are one path) AND while it is parked (the message wakes it;
// the card has no buttons,). Only a park whose agent cannot
// start gives the box way to the reason and the copyable commands.
//
// The suite starts its own `next dev` and drives the panel through its HTTP
// faces mocked in the browser with the routes' documented wire shapes
// (`/api/polyforge/status`, `/cards`, `/watch`, `/progress`, `/decisions`,
// `/tasks`, `/messages`), the panel-213 convention — except `/answer`, which
// reaches the real route and a fake pf (PF_BIN) that records its argv and answers the relay's `--json` acknowledgement; after
// an accepted message the suite moves the fixture the way the daemon would (the
// `owner_message` and `delivered` lines in the event log, the step running
// again). No daemon, harness or model runs. Every scenario prints
// `PASS S464-<n>` (`PASS S485-1`, `PASS S487-<n>`, `PASS S502-1`, `PASS S499-1`); the run ends `PASS all job-chat-464 scenarios` only when
// every scenario passed and the page logged no errors.
//
//   S464-1 a step waiting on its `pf step ask`: the ask highlighted in the flow (its
//          Markdown rendered) and above the input box (rendered, verbatim on hover, its stamp as the id), the button amber;
//          Enter posts the words to the relay (no question id), the user bubble
//          appears at once with the delivery note, and the daemon's own
//          `owner_message` line then replaces the echo while the `delivered`
//          row and the running state follow; the question strip leaves.
//   S464-2 a running step: the box is enabled (an interjection is the same
//          path), a message posts and echoes as queued; the card holds no
//          running sub-agent list the running subagent's
//          entry in the flow expands on its own toggle; the job card has no
//          交互 button and no 继续 / 取消 / 打回 control.
//   S464-3 a parked work item keeps its box: a dropped blocked
//          one and a paused job show the box with the parked hint and, above
//          it, the copyable `pf resume <wi>` its buttons are
//          that copy and send (no 继续 / 取消 / 打回到…,
//); a park whose agent cannot start —
//          the contract park, a step out of model candidates, a block the
//          runtime made — shows no box but the reason and the copyable
//          commands (`pf cancel` only for the contract park); a queued job
//          shows the reason alone.
//   S464-4 the discussion assistant is gone: no question panel,
//          discussion box, answer box or option buttons anywhere, no request
//          to its routes; the question sits above the one input box and the
//          job card has no 交互 button.
//   S464-5 no option path (workflow contract v2 change 19): options a stale
//          snapshot still names are never shown, and every message — an
//          option-like word or the old `/<id> <words>` form alike — goes out
//          verbatim as --text, never --option.
//   S485-1 the job composer is the main session's input box:
//          a `[data-composer-box]` row holds the input and the send button,
//          bordered and rounded like the host session's `.chat-composer-row`,
//          the button's box inside the row's; Shift+Enter is a new line and
//          sends nothing, a refused send keeps the words beside the refusal,
//          Enter sends the multi-line words verbatim and empties the box.
//   S487-1 a message wakes a parked work item (workflow contract 4): to a
//          dropped blocked one and to a paused job alike `pf --json answer
//          <wi> --text=…`, the echo noting the wake, the box emptied.
//   S487-2 a park whose agent cannot start, known only from the daemon's
//          WAKE_CANNOT_START refusal: the words stay in the box, the daemon's
//          words show under it with copyable `pf resume <wi>` / `pf cancel
//          <wi>` (the copy puts the command on the clipboard); no echo.
//   S502-1 a question holding the step-ask four-column table:
//          the flow's ask block renders it as a table (inline code and bold
//          rendered too) in its amber frame with its label, and so does the
//          question above the input box, folded to three lines' height until
//          its toggle opens it whole.
//   S499-1 the park's cause above the input box, from the
//          real panel and the watch snapshot's `blocked`: a dropped driver
//          park shows 已阻塞 · its kind, the step, the reason and 需要的操作
//          above the box with exactly the copyable `pf resume <wi>` (the copy
//          puts it on the clipboard), no `pf cancel`; the contract park its
//          cause and `pf cancel <wi>` alone, no box; a paused job one
//          `pf resume <wi>` above the box and no cause.
//
//   node e2e/polyforge-job-chat-464.mjs
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { polyforgeDock as dock, polyforgeToggle, waitForPolyforgeToggle } from "./polyforge-dock.mjs";
import { ACTION_MS } from "./timeouts.mjs";
import { assertNoDevServer, createCheck, createHarness, createWaitFor, mockContextUsage, mockPolyforge, NO_MESSAGES, NO_TASKS, reply, suiteDist, writeHostSession } from "./harness.mjs";

const distDir = suiteDist("polyforge-job-chat-464");
assertNoDevServer(distDir);
// the project path always holds "polyforge", so the toggle
// lookup meets it whatever TMPDIR is.
const work = mkdtempSync(join(tmpdir(), "pi-web-polyforge-464-e2e-"));
const agentDir = join(work, "agent");
const project = join(work, "project");
const SESSION = "e2e-pf-job-chat-464-session";
const HOST_TEXT = "E2E pf job chat 464 host session";
const EPOCH = "2026-11-10T10:00:00.000Z";
const ASK_AT = "2026-11-10T10:00:10.000Z";
const ACK_AT = "2026-11-10T10:00:20.000Z";
const MSG_AT = "2026-11-10T10:00:30.000Z";
const WAKE_AT = "2026-11-10T10:00:40.000Z";
const CANNOT_START = "wi_E2E464W9 is parked and its agent cannot start, so a message cannot wake it: no usable model: step build: E2E-487 the step has no candidate list to switch along — fix the cause, then `pf resume wi_E2E464W9` to continue, or `pf cancel wi_E2E464W9` to abandon it (nothing was recorded)";
mkdirSync(join(work, "data"));
mkdirSync(project);
writeHostSession(agentDir, { id: SESSION, cwd: project, text: HOST_TEXT, timestamp: EPOCH });

// The route that reaches the server: the input box → POST /api/polyforge/answer
// → `pf --json answer <wi> --text=…`. This fake pf records
// its argv and answers the relay's acknowledgement: a reply for the waiting
// job (delivered at once), a wake for a parked one, the
// daemon's WAKE_CANNOT_START refusal for W9, a queued message otherwise.
const fakePf = join(work, "fake-pf");
const pfArgv = join(work, "pf-argv.jsonl");
writeFileSync(fakePf, `#!${process.execPath}
const fs = require("fs");
const args = process.argv.slice(2);
if (args[0] === "--version") { process.stdout.write("v2.21.0\\n"); process.exit(0); }
fs.appendFileSync(${JSON.stringify(pfArgv)}, JSON.stringify(args) + "\\n");
if (args.includes("answer")) {
  const wi = args[args.indexOf("answer") + 1];
  if (wi === "wi_E2E464W9") {
    process.stderr.write(JSON.stringify({ code: "DAEMON-409", detail: ${JSON.stringify(CANNOT_START)}, extra: { reason: "WAKE_CANNOT_START" } }) + "\\n");
    process.exit(1);
  }
  const ack = wi === "wi_E2E464J2"
    ? { status: "accepted", at: ${JSON.stringify(ACK_AT)}, kind: "reply", ask_at: ${JSON.stringify(ASK_AT)}, step: "build", spawn_n: 1, delivered: true, disposition: "started" }
    : wi === "wi_E2E464W3" || wi === "wi_E2E464J4"
      ? { status: "accepted", at: ${JSON.stringify(WAKE_AT)}, kind: "message", step: "verification", spawn_n: 2, delivered: false, disposition: "wake", park: wi === "wi_E2E464J4" ? "paused" : "blocked", wake: "step" }
      : { status: "accepted", at: ${JSON.stringify(MSG_AT)}, kind: "message", step: "build", spawn_n: 1, delivered: false, disposition: "queued" };
  process.stdout.write(JSON.stringify(ack) + "\\n");
  process.exit(0);
}
process.stdout.write("ok\\n");
`);
chmodSync(fakePf, 0o755);
const pfCalls = () => (existsSync(pfArgv) ? readFileSync(pfArgv, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)) : []);
const resetPf = () => writeFileSync(pfArgv, "");

// ---- fixture ----
const QUESTION = "  Which colour should the badge be?\n- amber (matches the card frame)\n- red\n\nOne line is enough.  ";
// the step-ask convention's four-column table, with inline code and bold
const TABLE_QUESTION = "Two choices for the badge:\n\n| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. colour of `badge-ask`? | **amber** | matches the frame | ★★ |";
const J1 = { wi: "wi_E2E464J1", slug: "demo#4641", status: "running", stepAgeSec: 60, idleAgeSec: 5, candidate: "claude:claude-opus-x", spawnN: 1 };
const J2 = { wi: "wi_E2E464J2", slug: "demo#4642", status: "needs_input", stepAgeSec: 60, idleAgeSec: 5, candidate: "claude:claude-opus-x", spawnN: 1 };
const J4 = { wi: "wi_E2E464J4", slug: "demo#4644", status: "paused", stepAgeSec: 60, idleAgeSec: 5, candidate: "pi:glm-e2e", spawnN: 1 };
const J5 = { wi: "wi_E2E464J5", slug: "demo#4645", status: "queued", stepAgeSec: 0, idleAgeSec: 0, candidate: "pi:glm-e2e", spawnN: 0 };
// The daemon dropped this one (no job): its detail is the watch snapshot.
const W3 = { wi: "wi_E2E464W3", slug: "demo#4643" };
const W6 = { wi: "wi_E2E464W6", slug: "demo#4646" };
// review r2 B1: parked on write_failed in `collect`, with an open candidates_exhausted
// entry left by an earlier step (the chain ran out once, the same-model retry went on)
const W7 = { wi: "wi_E2E464W7", slug: "demo#4647" };
// review r2 B2: the workflow completed, then the final push failed (the runtime's block)
const W8 = { wi: "wi_E2E464W8", slug: "demo#4648" };
// a driver park the snapshot reads as wakeable, whose agent cannot start (the daemon refuses the message)
const W9 = { wi: "wi_E2E464W9", slug: "demo#4649" };
// a driver park whose step ran out of model candidates (the watch tail proves it): its agent cannot start
const W10 = { wi: "wi_E2E464W10", slug: "demo#46410" };
const WRITE_FAILED = { kind: "write_failed", step: "collect", reason: "write failed: the hub refused the move" };
const BLOCKED = { kind: "driver", step: "verification", reason: "E2E-464 the driver parked the workflow",
  requiredAction: "E2E-464 resolve the cause, then resume the work item (`pf resume`) or cancel it" };
const CONTRACT = { kind: "contract", step: "build", reason: "E2E-464 lane snapshot targets contract 1, this daemon runs 2", requiredAction: "cancel and re-file" };
const NO_MODEL = { kind: "driver", step: "verification", reason: "no usable model: step verification: E2E-487 the candidate list is spent" };
const entry = (id, item, cls) => ({ id, at: `2026-11-10T10:${String(id).padStart(2, "0")}:00Z`, kind: "work_item", profile: "default", wi: item.wi, slug: item.slug,
  class: cls, source: cls, phrase: `E2E-464 the ${cls} phrase`, attention: true, resolved: false, dismissed: false });
let fixture;
const reset = () => {
  fixture = {
    jobs: [J1, J2, J4, J5],
    // the decision feed's open entries (a dropped item's feed-only card)
    feed: [],
    pending: { [J2.wi]: { id: ASK_AT, step: "build", question: QUESTION, askedAt: ASK_AT } },
    options: {},
    // the progress stream per work item
    lines: {
      [J1.wi]: [
        { id: "e.0", kind: "event", text: "10:00:01 ▶ build #1 · claude:claude-opus-x", tone: "accent", event: "step_started", at: Date.parse(EPOCH) + 1000 },
        { id: "n1", kind: "narration", text: "E2E-464 J1 narration: exploring the tree" },
        { id: "t1", kind: "tool", text: "Agent: look at the panel", tool: { name: "Agent", argument: "look at the panel", status: "running" },
          subagents: [{ id: "sub-explore-1", description: "E2E-464 explore the panel sources", agent: "Explore", status: "running", startAt: Date.parse(EPOCH) + 5000, lines: [{ id: "sub-n1", kind: "narration", text: "E2E-464 subagent narration" }] }] },
      ],
      [J2.wi]: [
        { id: "e.0", kind: "event", text: "10:00:01 ▶ build #1 · claude:claude-opus-x", tone: "accent", event: "step_started", at: Date.parse(EPOCH) + 1000 },
        { id: "n1", kind: "narration", text: "E2E-464 J2 narration: I need a decision before I go on." },
        { id: "e.5", kind: "ask", text: QUESTION, tone: "amber", event: "awaiting_owner", at: Date.parse(ASK_AT) },
      ],
    },
    blocked: { [W3.wi]: BLOCKED, [W6.wi]: CONTRACT, [W7.wi]: WRITE_FAILED, [W9.wi]: NO_MODEL, [W10.wi]: NO_MODEL },
    // a dropped item's watch tail beyond its step start (W10: the candidates ran out in the parked step's current run)
    events: { [W10.wi]: [{ at: "2026-11-10T10:00:05.000Z", kind: "candidates_exhausted", step: "verification" }] },
    // a dropped item's workflow status when it is not the park's `blocked`
    workflow: { [W8.wi]: "completed" },
    // S485-1: the answer route refuses while set
    refuseAnswer: false,
  };
};
const statusBody = () => ({ pfPresent: true, status: { queue: { running: fixture.jobs.filter((j) => j.status === "running").length, max: 8 }, jobs: fixture.jobs, tasks: { running: 0, completed: 0 } } });
const card = (entry) => ({ job: entry, goal: `E2E 464 goal of ${entry.slug}`, startedAt: EPOCH,
  steps: [{ id: "build", state: entry.status === "needs_input" ? "unstarted" : "running", runs: 1 }], currentStep: "build", progress: { available: true, entries: [] } });
const cardsBody = () => ({ pfPresent: true, cards: fixture.jobs.map(card) });
const watchBody = (ref) => {
  const job = fixture.jobs.find((candidate) => candidate.wi === ref || candidate.slug === ref);
  const dropped = job ? null : [W3, W6, W7, W8, W9, W10].find((item) => item.wi === ref || item.slug === ref);
  if (dropped) {
    return { status: { wi: dropped.wi, slug: dropped.slug, workflowStatus: fixture.workflow[dropped.wi] ?? "blocked", currentStep: "verification", updatedAt: EPOCH,
      steps: [{ name: "verification", status: "blocked", startedAt: EPOCH }], declared_steps: [{ id: "plan" }, { id: "build" }, { id: "verification" }],
      events: [{ at: EPOCH, kind: "step_started", step: "verification" }, ...(fixture.events[dropped.wi] ?? [])], blocked: fixture.blocked[dropped.wi] } };
  }
  const shown = job ?? J1;
  const pending = fixture.pending[shown.wi];
  return { status: { wi: shown.wi, slug: shown.slug, workflowStatus: shown.status, currentStep: "build", updatedAt: EPOCH,
    steps: [{ name: "plan", status: "done", startedAt: EPOCH }, { name: "build", status: shown.status === "needs_input" ? "needs_input" : "running", startedAt: EPOCH }], declared_steps: [{ id: "plan" }, { id: "build" }],
    events: [{ at: EPOCH, kind: "step_started", step: "build", model: "claude-opus-x" }, ...(pending ? [{ at: ASK_AT, kind: "awaiting_owner", step: "build", question: pending.question, question_id: pending.id }] : [])],
    ...(pending ? { pendingQuestion: pending } : {}),
    ...(fixture.options[shown.wi] ? { questionOptions: fixture.options[shown.wi] } : {}) } };
};
const progressBody = (wi) => ({ available: true, omitted: 0, lines: fixture.lines[wi] ?? [] });
// What the daemon's record shows after an accepted message: the owner_message
// line (the ack's stamp), the delivered row, the step running again.
const acceptedByDaemon = (wi, text) => {
  // S464-5 keeps the v1 question pending across two messages (the daemon resumes the asking agent)
  if (fixture.frozen) return;
  if (wi === J2.wi) {
    fixture.lines[wi] = [...fixture.lines[wi],
      // the event log stamps its envelope after the record's fsync: a later
      // millisecond than the record's own `at` (the ack's), which rides as msgAt
      { id: "e.7", kind: "owner", text, event: "owner_message", at: Date.parse(ACK_AT) + 7, msgAt: ACK_AT, by: "usr_e2e" },
      { id: "e.8", kind: "event", text: "10:00:21 ✓ build · delivered · started", tone: "dim", event: "delivered", at: Date.parse(ACK_AT) + 1000 },
      { id: "n2", kind: "narration", text: "E2E-464 J2 narration: amber it is." }];
    delete fixture.pending[wi];
    fixture.jobs = fixture.jobs.map((job) => (job.wi === wi ? { ...job, status: "running" } : job));
  }
};

// ---- scenario bookkeeping and helpers ----
const failures = [];
const check = createCheck({ failures });
const CONTEXT_USAGE = { percent: 10, contextWindow: 200_000, tokens: 20_000 };
let requested = [];
const h = createHarness({
  viewport: { width: 1280, height: 900 },
  locale: "zh-CN",
  // S464-3 / S487-2 read the copied command back
  permissions: ["clipboard-read", "clipboard-write"],
  permissionOrigin: true,
  storage: { "pi-web:split-view-enabled": "false", "pi-locale": "zh-CN", "pi-web:polyforge-dock-width": "640" },
  timeout: ACTION_MS,
  errors: ["pageerror"],
  session: SESSION,
  ready: HOST_TEXT,
  async setup(page) {
    page.on("request", (request) => requested.push(new URL(request.url()).pathname));
    await mockPolyforge(page, {
      answer: async (url, route) => {
        // the real route and the fake pf; on success the fixture moves as the daemon's record would
        const body = route.request().postDataJSON();
        // S485-1: a refusal without reaching the real route
        if (fixture.refuseAnswer) return reply(route, { ok: false, error: "E2E-485 refused" }, 409);
        const response = await route.fetch();
        const answer = await response.json().catch(() => ({}));
        if (answer.ok === true) acceptedByDaemon(body.wi, body.text);
        return route.fulfill({ response, body: JSON.stringify(answer), headers: { ...response.headers(), "content-type": "application/json" } });
      },
      status: () => statusBody(),
      cards: () => cardsBody(),
      watch: (url) => watchBody(url.searchParams.get("wi")),
      progress: (url) => progressBody(url.searchParams.get("wi")),
      decisions: () => ({ pfPresent: true, feed: { decisions: fixture.feed, attention: fixture.feed.length } }),
      tasks: NO_TASKS,
      messages: NO_MESSAGES,
    });
    await mockContextUsage(page, SESSION, CONTEXT_USAGE);
  },
});
const { errors, withPage } = h;
const waitFor = createWaitFor({ ms: 20_000, done: (value) => value === true, describe: (label, last) => `${label} (last: ${JSON.stringify(last)})` });
const titleText = async (page) => {
  const title = dock(page).locator("[data-pf-detail-title]");
  return (await title.count()) > 0 ? (await title.first().innerText()).replace(/\s+/g, " ") : null;
};
const waitTitle = (page, slug, label) => waitFor(async () => { const t = await titleText(page); return Boolean(t && t.includes(slug)) || t; }, label);
async function openDock(page) {
  await waitForPolyforgeToggle(page);
  await polyforgeToggle(page).click();
  await dock(page).waitFor();
}
// Select a job's detail; the amber button's opening already walks to the
// waiting job, and a click on a selected card deselects it,
// so a job already shown is left alone.
async function openJob(page, item) {
  const shown = await titleText(page);
  if (!(shown && shown.includes(item.slug))) await dock(page).locator(`[data-pf-card-wi="${item.wi}"]`).click();
  await waitTitle(page, item.slug, `${item.slug}'s detail opens`);
}
const chat = (page) => dock(page).locator("[data-pf-chat]");
const input = (page) => chat(page).locator("[data-pf-chat-input]");
const ownerBubbles = (page) => dock(page).locator('[data-pf-detail-scroll] [data-pf-progress-kind="owner"]');
// the card's controls in DOM order (no 继续 / 取消 / 打回到… —
// the send button, the question's fold, the copyable commands' copy)
const chatButtons = (page) => chat(page).locator("button").evaluateAll((buttons) => buttons.map((b) => (
  b.hasAttribute("data-pf-chat-send") ? "send"
    : b.hasAttribute("data-pf-chat-question-toggle") ? "question-toggle"
      : b.hasAttribute("data-pf-chat-command-copy") ? `copy:${b.getAttribute("data-pf-chat-command-copy")}`
        : `other:${b.textContent?.trim()}`)));

try {
  await h.launchBrowser(() => chromium.launch());
  await h.startServer({ name: "polyforge-job-chat-464", logFile: join(work, "server.log"), distDir, env: {
    PI_CODING_AGENT_DIR: agentDir, PI_WEB_ALLOWED_ROOT_PREFIXES: work, POLYFORGE_V1_API_KEY: "", PF_BIN: fakePf, POLYFORGE_DATA_DIR: join(work, "data") } });

  await check("S464-1", async () => {
    reset(); resetPf(); requested = [];
    await withPage(async (page) => {
      await waitForPolyforgeToggle(page);
      await waitFor(async () => (await page.locator("[data-pf-toggle]").first().getAttribute("data-pf-attention")) === "1", "the button is amber: one job waits on the owner");
      await openDock(page);
      await openJob(page, J2);
      // the ask in the flow, highlighted, its Markdown rendered
      const ask = dock(page).locator('[data-pf-detail-scroll] [data-pf-progress-kind="ask"]');
      await ask.waitFor();
      await waitFor(async () => (await ask.locator("li").count()) === 2, "the agent's question is in the flow where it was asked, its list rendered");
      assert.deepEqual(await ask.locator("li").allInnerTexts(), ["amber (matches the card frame)", "red"]);
      assert.deepEqual(await ask.locator("p").allInnerTexts(), ["Which colour should the badge be?", "One line is enough."]);
      assert.match(await ask.evaluate((el) => getComputedStyle(el).borderLeftColor), /rgb\(217, 119, 6\)/, "highlighted amber");
      // the question above the input box: verbatim, the ask's stamp as its id
      const strip = chat(page).locator("[data-pf-chat-question]");
      await strip.waitFor();
      assert.equal(await strip.getAttribute("data-pf-chat-question-id"), ASK_AT, "an ask's id is its stamp");
      // its Markdown rendered, the verbatim (untrimmed) question on hover
      await waitFor(async () => (await strip.locator("[data-pf-chat-question-text] li").count()) === 2, "the question's list is rendered above the box");
      assert.deepEqual(await strip.locator("[data-pf-chat-question-text] li").allInnerTexts(), ["amber (matches the card frame)", "red"]);
      assert.equal(await strip.locator("[data-pf-chat-question-text]").getAttribute("title"), QUESTION, "the question verbatim (untrimmed)");
      assert.equal(await chat(page).getAttribute("data-pf-chat-state"), "awaiting");
      assert.deepEqual(await chatButtons(page), ["question-toggle", "send"], "with an agent to talk to the box is the only control (and the question's fold)");
      assert.ok(await input(page).isEnabled(), "the box is open while the step waits");
      assert.equal(await input(page).getAttribute("placeholder"), "回复 agent");
      // Enter posts the words to the relay — no question id — and the echo lands at once
      await input(page).fill("amber, and keep the frame");
      await input(page).press("Enter");
      await waitFor(async () => pfCalls().some((args) => args.includes("answer")), "the fake pf received the message");
      assert.deepEqual(pfCalls().filter((args) => args.includes("answer")), [["--json", "answer", J2.wi, "--text=amber, and keep the frame"]],
        "`pf --json answer <wi> --text=…`, no --question");
      await ownerBubbles(page).first().waitFor();
      assert.equal(await ownerBubbles(page).count(), 1, "one bubble: the echo");
      assert.match(await ownerBubbles(page).first().innerText(), /amber, and keep the frame/);
      assert.match(await ownerBubbles(page).first().locator("[data-pf-progress-owner-note]").innerText(), /已送达 agent/, "the ack's delivery note");
      await waitFor(async () => (await input(page).inputValue()) === "", "the box is cleared");
      // the daemon's record arrives on the next polls: the owner_message line replaces the echo, the delivered row and the running state follow
      await waitFor(async () => (await ownerBubbles(page).count()) === 1 && (await ownerBubbles(page).first().locator("[data-pf-progress-owner-by]").innerText()) === "usr_e2e",
        "the record's own owner line (its sender) replaces the echo without doubling it");
      const delivered = dock(page).locator('[data-pf-detail-scroll] [data-pf-progress-kind="event"][data-pf-progress-tone="dim"]').filter({ hasText: /delivered/ });
      await delivered.first().waitFor();
      await waitFor(async () => (await chat(page).getAttribute("data-pf-chat-state")) === "running", "the step runs again");
      assert.equal(await chat(page).locator("[data-pf-chat-question]").count(), 0, "the question strip leaves with the ask");
      assert.ok(await input(page).isEnabled(), "the box stays open: the next message is an interjection");
    });
  });

  await check("S464-2", async () => {
    reset(); resetPf();
    await withPage(async (page) => {
      await openDock(page);
      await openJob(page, J1);
      await waitFor(async () => (await chat(page).getAttribute("data-pf-chat-state")) === "running", "a running step");
      assert.ok(await input(page).isEnabled(), "the box is open while the step runs (a live interjection is the same path)");
      assert.equal(await input(page).getAttribute("placeholder"), "给正在工作的 step agent 发消息");
      // §5.5 / Q7: with an agent to talk to the input box is the only channel —
      // no 继续 / 取消 / 打回 control anywhere in the dock, and no 交互 button
      assert.equal(await dock(page).getByRole("button", { name: /^交互/ }).count(), 0, "the job card has no 交互 button");
      assert.equal(await dock(page).locator("[data-pf-resume-button], [data-pf-cancel-button], [data-pf-rewind-open], [data-pf-rewind-form], [data-pf-chat-menu]").count(), 0,
        "a running card offers no 继续 / 取消 / 打回 control");
      assert.equal(await chat(page).locator("[data-pf-chat-question]").count(), 0, "nothing asked");
      // the running subagent's entry is in the flow, and the card holds no running sub-agent list
      const entry = dock(page).locator('[data-pf-progress-subagent-id="sub-explore-1"]');
      await entry.waitFor({ state: "attached" });
      assert.equal(await dock(page).locator("[data-claude-subagent-list]").count(), 0, "no running sub-agent list on the job page");
      // a message while it works: queued by the agent's process
      await input(page).fill("check the dark theme too");
      await input(page).press("Enter");
      await waitFor(async () => pfCalls().some((args) => args.includes("answer")), "the fake pf received the interjection");
      assert.deepEqual(pfCalls().filter((args) => args.includes("answer")), [["--json", "answer", J1.wi, "--text=check the dark theme too"]]);
      await ownerBubbles(page).first().waitFor();
      assert.match(await ownerBubbles(page).first().locator("[data-pf-progress-owner-note]").innerText(), /已排队/, "the queued disposition shows");
      // the entry's own toggle expands it
      const toggle = entry.locator("[data-pf-progress-subagent-toggle]");
      assert.equal(await toggle.getAttribute("aria-expanded"), "false", "collapsed before the click");
      await toggle.click();
      await waitFor(async () => (await toggle.getAttribute("aria-expanded")) === "true", "the toggle opens the entry");
      await waitFor(async () => /E2E-464 subagent narration/.test(await entry.locator("[data-pf-progress-subagent-body]").innerText()), "the entry shows the subagent's narration");
    });
  });

  await check("S464-3", async () => {
    reset(); resetPf();
    const parkedFor = async (page, park, item) => {
      await waitFor(async () => (await chat(page).getAttribute("data-pf-chat-state")) === "parked", `${park}: parked, the box stays`);
      assert.equal(await chat(page).getAttribute("data-pf-chat-park"), park);
      assert.ok(await input(page).isEnabled(), "the input box is open: a message wakes it");
      assert.equal(await input(page).getAttribute("placeholder"), "给停住的 step 发消息：发送即叫醒它");
      assert.equal(await chat(page).locator(`[data-pf-chat-parked="${park}"]`).count(), 1, "the parked hint");
      // a wakeable park also names the command that wakes it without words
      const shown = chat(page).locator("[data-pf-chat-commands] [data-pf-chat-command]");
      assert.deepEqual(await shown.evaluateAll((rows) => rows.map((row) => [row.getAttribute("data-pf-chat-command"), row.querySelector("code")?.textContent])),
        [["resume", `pf resume ${item.wi}`]], "a park the message wakes shows the copyable pf resume alone");
      // no 继续 / 取消 / 打回到… — the command's copy and send
      assert.deepEqual(await chatButtons(page), ["copy:resume", "send"], "no 继续 / 取消 / 打回到… on a park");
      assert.equal(await dock(page).locator("[data-pf-resume-button], [data-pf-cancel-button], [data-pf-rewind-open], [data-pf-rewind-form], [data-pf-chat-menu]").count(), 0, "none anywhere in the dock");
    };
    await withPage(async (page) => {
      await openDock(page);
      // a paused job: the message resumes it
      await openJob(page, J4);
      await parkedFor(page, "paused", J4);
      assert.match(await chat(page).locator('[data-pf-chat-parked="paused"]').innerText(), /已暂停。发一条消息/);
      // a queued job: no agent and no park — the reason alone
      await openJob(page, J5);
      await waitFor(async () => (await chat(page).getAttribute("data-pf-chat-state")) === "queued", "queued");
      assert.equal(await chat(page).locator("[data-pf-chat-commands]").count(), 0, "nothing to run for a state that is no park");
      assert.deepEqual(await chatButtons(page), [], "and no control");
      assert.equal(await input(page).count(), 0);
    });
    // the dropped items, reached through their feed-only attention cards
    //: the daemon holds no job for them, their detail is the
    // watch snapshot
    fixture.feed = [entry(7, W10, "blocked"), entry(5, W8, "blocked"), entry(4, W7, "candidates_exhausted"), entry(3, W7, "blocked"), entry(2, W3, "blocked"), entry(1, W6, "blocked")];
    for (const [item, state, commands] of [
      // a driver park: the message wakes it
      [W3, "parked", []],
      // review r2 B1: an earlier step's candidates entry does not close a later park's box
      [W7, "parked", []],
      // cannot start: the contract park — cancel and re-file
      [W6, "contract", ["cancel"]],
      // review r2 B2: completed, then the final push failed — a resume saves the run
      [W8, "runtime", ["resume", "cancel"]],
      // cannot start: the parked step ran out of model candidates in its current run
      [W10, "candidates", ["resume", "cancel"]]]) {
      await withPage(async (page) => {
        await openDock(page);
        await openJob(page, item);
        if (state === "parked") await parkedFor(page, "blocked", item);
        else {
          await waitFor(async () => (await chat(page).getAttribute("data-pf-chat-state")) === state, `${state}: its agent cannot start`);
          assert.equal(await input(page).count(), 0, "no input box: no message wakes it");
          const shown = chat(page).locator("[data-pf-chat-commands] [data-pf-chat-command]");
          assert.deepEqual(await shown.evaluateAll((rows) => rows.map((row) => [row.getAttribute("data-pf-chat-command"), row.querySelector("code")?.textContent])),
            commands.map((name) => [name, `pf ${name} ${item.wi}`]), "the copyable commands");
          assert.deepEqual(await chatButtons(page), commands.map((name) => `copy:${name}`), "the copy buttons and nothing else: no 继续 / 取消 button");
        }
        if (item === W8) {
          // the copy puts the command on the clipboard
          await chat(page).locator('[data-pf-chat-command-copy="resume"]').click();
          await waitFor(async () => (await chat(page).locator('[data-pf-chat-command-copy="resume"]').innerText()).trim() === "已复制", "copied");
          assert.equal(await page.evaluate(() => navigator.clipboard.readText()), `pf resume ${W8.wi}`);
        }
      });
    }
  });

  await check("S464-4", async () => {
    reset(); resetPf(); requested = [];
    await withPage(async (page) => {
      await openDock(page);
      await openJob(page, J2);
      await chat(page).locator("[data-pf-chat-question]").waitFor();
      for (const gone of ["[data-pf-question-panel]", "[data-pf-discussion-input]", "[data-pf-discussion-send]", "[data-pf-answer-input]", "[data-pf-answer-submit]", "[data-pf-question-options]", "[data-pf-option]", "[data-pf-discussion-use]"]) {
        assert.equal(await page.locator(gone).count(), 0, `${gone} is gone`);
      }
      assert.equal(await dock(page).getByRole("button", { name: /^交互/ }).count(), 0, "the job card has no 交互 button");
      assert.equal(await dock(page).locator("textarea").count(), 1, "one input box in the dock");
      assert.deepEqual(requested.filter((path) => path.startsWith("/api/polyforge/discussion")), [], "no discussion route is requested");
    });
  });

  await check("S464-5", async () => {
    reset(); resetPf();
    fixture.frozen = true;
    fixture.pending[J2.wi] = { id: "q-colour", step: "build", question: QUESTION };
    // a stale snapshot field the client no longer reads
    fixture.options[J2.wi] = { questionId: "q-colour", options: [
      { id: "revert", label: "E2E-464 agent: please revert", action: "revert" },
      { id: "keep", label: "E2E-464 keep going" },
    ] };
    await withPage(async (page) => {
      await openDock(page);
      await openJob(page, J2);
      await chat(page).locator("[data-pf-chat-question]").waitFor();
      assert.equal(await chat(page).locator("[data-pf-chat-question-options], [data-pf-chat-option]").count(), 0, "no option is shown");
      await input(page).fill("keep");
      await input(page).press("Enter");
      await waitFor(async () => pfCalls().filter((args) => args.includes("answer")).length === 1, "the fake pf received the words");
      await waitFor(async () => (await input(page).inputValue()) === "", "the box is cleared");
      await input(page).fill("/keep but add a note");
      await input(page).press("Enter");
      await waitFor(async () => pfCalls().filter((args) => args.includes("answer")).length === 2, "the fake pf received the second message");
      assert.deepEqual(pfCalls().filter((args) => args.includes("answer")), [
        ["--json", "answer", J2.wi, "--text=keep"],
        ["--json", "answer", J2.wi, "--text=/keep but add a note"],
      ], "every message goes as --text, verbatim; never --option or --question");
    });
  });

  await check("S485-1", async () => {
    reset(); resetPf();
    await withPage(async (page) => {
      await openDock(page);
      await openJob(page, J1);
      await waitFor(async () => (await chat(page).getAttribute("data-pf-chat-state")) === "running", "a running step");
      // the shared box: one bordered row holding the input and the send button
      const box = chat(page).locator("[data-composer-box]");
      await waitFor(async () => (await box.count()) === 1, "the job composer renders the shared input box");
      assert.equal(await box.locator("[data-pf-chat-input]").count(), 1, "the input sits in the box");
      assert.equal(await box.locator("[data-pf-chat-send]").count(), 1, "the send button sits in the box");
      const style = (locator) => locator.evaluate((el) => ({ border: getComputedStyle(el).borderTopWidth, radius: getComputedStyle(el).borderRadius }));
      const own = await style(box);
      assert.ok(parseFloat(own.border) >= 1, `the box is bordered (${own.border})`);
      const host = page.locator(".chat-composer-row").first();
      await host.waitFor({ state: "attached" });
      assert.equal(own.radius, (await style(host)).radius, "rounded like the host session's composer");
      const send = box.locator("[data-pf-chat-send]");
      await input(page).fill("E2E-485 one");
      await waitFor(async () => send.isEnabled(), "the send button is enabled with text");
      // both boxes once they stop changing
      let settled; let previous = null;
      await waitFor(async () => {
        const now = { row: await box.boundingBox(), button: await send.boundingBox() };
        const same = previous !== null && JSON.stringify(now) === JSON.stringify(previous);
        previous = now; settled = now;
        return same && Boolean(now.row && now.button);
      }, "the box and its button settle");
      const { row, button } = settled;
      assert.ok(button.x >= row.x - 0.5 && button.y >= row.y - 0.5 && button.x + button.width <= row.x + row.width + 0.5 && button.y + button.height <= row.y + row.height + 0.5,
        `the send button lies inside the box (button ${JSON.stringify(button)}, box ${JSON.stringify(row)})`);
      // Shift+Enter: a new line, nothing sent
      await input(page).press("Shift+Enter");
      await input(page).pressSequentially("two");
      await waitFor(async () => (await input(page).inputValue()) === "E2E-485 one\ntwo", "Shift+Enter makes a new line");
      assert.deepEqual(pfCalls().filter((args) => args.includes("answer")), [], "Shift+Enter sends nothing");
      // a refused send keeps the words beside the refusal
      fixture.refuseAnswer = true;
      await input(page).press("Enter");
      const refusal = chat(page).locator("[data-pf-chat-error]");
      await waitFor(async () => (await refusal.count()) === 1 && /E2E-485 refused/.test(await refusal.innerText()), "the refusal shows");
      assert.equal(await input(page).inputValue(), "E2E-485 one\ntwo", "a refused send keeps the words");
      assert.equal(await ownerBubbles(page).count(), 0, "a refused send adds no bubble");
      // Enter sends the words verbatim and empties the box
      fixture.refuseAnswer = false;
      await waitFor(async () => input(page).isEnabled(), "the box is open again");
      await input(page).press("Enter");
      await waitFor(async () => pfCalls().some((args) => args.includes("answer")), "the fake pf received the message");
      assert.deepEqual(pfCalls().filter((args) => args.includes("answer")), [["--json", "answer", J1.wi, "--text=E2E-485 one\ntwo"]], "Enter sends the words verbatim");
      await waitFor(async () => (await input(page).inputValue()) === "", "the box is cleared");
    });
  });

  await check("S487-1", async () => {
    reset(); resetPf();
    fixture.feed = [entry(2, W3, "blocked")];
    for (const [item, park] of [[W3, "blocked"], [J4, "paused"]]) {
      await withPage(async (page) => {
        await openDock(page);
        await openJob(page, item);
        await waitFor(async () => (await chat(page).getAttribute("data-pf-chat-park")) === park, `${item.slug}: parked ${park}`);
        await input(page).fill(`E2E-487 wake ${park}`);
        await input(page).press("Enter");
        await waitFor(async () => pfCalls().some((args) => args.includes("answer") && args.includes(item.wi)), "the fake pf received the message");
        assert.deepEqual(pfCalls().filter((args) => args.includes("answer") && args.includes(item.wi)), [["--json", "answer", item.wi, `--text=E2E-487 wake ${park}`]],
          "the same command as any message: pf opens the door and the daemon wakes the step");
        await ownerBubbles(page).first().waitFor();
        assert.match(await ownerBubbles(page).first().innerText(), new RegExp(`E2E-487 wake ${park}`));
        assert.match(await ownerBubbles(page).first().locator("[data-pf-progress-owner-note]").innerText(), /停住的工单已被它叫醒/, "the wake shows on the echo");
        await waitFor(async () => (await input(page).inputValue()) === "", "the box is cleared");
        assert.equal(await chat(page).locator("[data-pf-chat-error], [data-pf-chat-cannot-start]").count(), 0, "no refusal");
      });
    }
  });

  await check("S487-2", async () => {
    reset(); resetPf();
    fixture.feed = [entry(9, W9, "blocked")];
    await withPage(async (page) => {
      await openDock(page);
      await openJob(page, W9);
      // the snapshot cannot tell (no candidates event, no list at all): the box is open, the daemon decides
      await waitFor(async () => (await chat(page).getAttribute("data-pf-chat-state")) === "parked", "a driver park: the box is open");
      await input(page).fill("E2E-487 go on please");
      await input(page).press("Enter");
      const refusal = chat(page).locator("[data-pf-chat-cannot-start]");
      await refusal.waitFor();
      assert.equal(await refusal.locator("[data-pf-chat-cannot-start-reason]").innerText(), `DAEMON-409: ${CANNOT_START}`, "the daemon's words, verbatim");
      assert.match(await refusal.innerText(), /未发出：此工单的 agent 起不来/);
      assert.deepEqual(await refusal.locator("[data-pf-chat-command]").evaluateAll((rows) => rows.map((row) => row.querySelector("code")?.textContent)),
        [`pf resume ${W9.wi}`, `pf cancel ${W9.wi}`], "the two moves left, copyable");
      assert.equal(await input(page).inputValue(), "E2E-487 go on please", "the words stay in the box");
      assert.equal(await chat(page).locator("[data-pf-chat-error]").count(), 0, "a cannot-start refusal is not a plain error");
      assert.equal(await ownerBubbles(page).count(), 0, "nothing was recorded: no echo");
      await refusal.locator('[data-pf-chat-command-copy="cancel"]').click();
      await waitFor(async () => (await page.evaluate(() => navigator.clipboard.readText())) === `pf cancel ${W9.wi}`, "the cancel command is on the clipboard");
    });
  });

  await check("S502-1", async () => {
    reset(); resetPf();
    fixture.lines[J2.wi][2].text = TABLE_QUESTION;
    fixture.pending[J2.wi].question = TABLE_QUESTION;
    await withPage(async (page) => {
      await openDock(page);
      await openJob(page, J2);
      const ask = dock(page).locator('[data-pf-detail-scroll] [data-pf-progress-kind="ask"]');
      await ask.locator("table").waitFor();
      assert.deepEqual(await ask.locator("th").allInnerTexts(), ["问题", "推荐选项", "理由", "重点关注程度"], "the four-column question table, as a table");
      assert.equal(await ask.locator("td code").innerText(), "badge-ask", "inline code");
      assert.equal(await ask.locator("td strong").innerText(), "amber", "bold");
      assert.ok(!(await ask.innerText()).includes("|---|"), "no raw table syntax");
      assert.match(await ask.evaluate((el) => getComputedStyle(el).borderLeftColor), /rgb\(217, 119, 6\)/, "still highlighted amber");
      assert.match(await ask.innerText(), /Waiting for the owner|等待 owner/, "its label stays");
      // the question above the input box: the same table, folded to three lines until opened
      const text = chat(page).locator("[data-pf-chat-question] [data-pf-chat-question-text]");
      await text.locator("table").waitFor();
      assert.deepEqual(await text.locator("th").allInnerTexts(), ["问题", "推荐选项", "理由", "重点关注程度"], "the question table above the box, as a table");
      assert.equal(await text.locator("td strong").innerText(), "amber");
      const heights = () => text.evaluate((el) => ({ client: el.clientHeight, scroll: el.scrollHeight }));
      let settled = null;
      await waitFor(async () => { const now = await heights(); const same = JSON.stringify(now) === JSON.stringify(settled); settled = now; return same || now; }, "the folded height settles");
      assert.ok(settled.client > 0 && settled.client <= 62, `folded to three lines' height (${settled.client}px)`);
      assert.ok(settled.scroll > settled.client, "the rest of the table is hidden while folded");
      await chat(page).locator("[data-pf-chat-question-toggle]").click();
      await waitFor(async () => (await chat(page).locator("[data-pf-chat-question-toggle]").getAttribute("aria-expanded")) === "true", "the toggle opens the question");
      await waitFor(async () => { const now = await heights(); return now.scroll <= now.client + 1 || now; }, "opened: the whole table shows");
    });
  });

  await check("S499-1", async () => {
    reset(); resetPf();
    // the dropped items' feed-only attention cards make them reachable
    fixture.feed = [entry(2, W3, "blocked"), entry(1, W6, "blocked")];
    const cause = (page) => chat(page).locator("[data-pf-chat-park-cause]");
    const shownCommands = (page) => chat(page).locator("[data-pf-chat-commands] [data-pf-chat-command]").evaluateAll((rows) => rows.map((row) => [row.getAttribute("data-pf-chat-command"), row.querySelector("code")?.textContent]));
    const box = async (locator) => { const b = await locator.boundingBox(); return b ? { top: b.y, bottom: b.y + b.height } : null; };
    // a wakeable driver park: its cause and pf resume above the box
    await withPage(async (page) => {
      await openDock(page);
      await openJob(page, W3);
      await waitFor(async () => (await chat(page).locator('[data-pf-chat-park-cause="driver"]').count()) === 1 || await chat(page).getAttribute("data-pf-chat-state"), "W3: the driver park's cause shows in the composer");
      await waitFor(async () => (await cause(page).innerText()).includes("已阻塞 · 被调度器暂停") || await cause(page).innerText(), "the kind, labelled");
      assert.equal((await cause(page).locator("[data-pf-chat-park-step]").innerText()).trim(), "verification", "the step");
      assert.equal(await cause(page).locator("[data-pf-chat-park-reason]").innerText(), BLOCKED.reason, "the reason, verbatim");
      assert.match(await cause(page).locator("[data-pf-chat-park-action]").innerText(), /^需要的操作：E2E-464 resolve the cause/, "the required action");
      await waitFor(async () => { const c = await box(cause(page)); const i = await box(input(page)); return Boolean(c && i && c.bottom <= i.top) || { cause: c, input: i }; }, "the cause sits above the input box");
      await waitFor(async () => JSON.stringify(await shownCommands(page)) === JSON.stringify([["resume", `pf resume ${W3.wi}`]]) || await shownCommands(page), "exactly the copyable pf resume");
      await waitFor(async () => { const c = await box(chat(page).locator('[data-pf-chat-command="resume"]')); const i = await box(input(page)); return Boolean(c && i && c.bottom <= i.top) || { command: c, input: i }; }, "the command sits above the input box");
      assert.equal(await chat(page).locator('[data-pf-chat-command="cancel"]').count(), 0, "no pf cancel");
      await chat(page).locator('[data-pf-chat-command-copy="resume"]').click();
      await waitFor(async () => (await page.evaluate(() => navigator.clipboard.readText())) === `pf resume ${W3.wi}` || await page.evaluate(() => navigator.clipboard.readText()), "the copy puts pf resume on the clipboard");
    });
    // the contract park: its cause and pf cancel alone, no box
    await withPage(async (page) => {
      await openDock(page);
      await openJob(page, W6);
      await waitFor(async () => (await chat(page).locator('[data-pf-chat-park-cause="contract"]').count()) === 1 || await chat(page).getAttribute("data-pf-chat-state"), "W6: the contract park's cause shows in the composer");
      assert.match(await cause(page).innerText(), /已阻塞 · 车道快照的工作流契约版本与本 daemon 不符（取消后重提）/, "the contract kind, labelled");
      assert.equal((await cause(page).locator("[data-pf-chat-park-step]").innerText()).trim(), "build", "the step");
      assert.equal(await cause(page).locator("[data-pf-chat-park-reason]").innerText(), CONTRACT.reason, "the reason, verbatim");
      assert.equal(await cause(page).locator("[data-pf-chat-park-action]").innerText(), "需要的操作：cancel and re-file", "the required action");
      await waitFor(async () => JSON.stringify(await shownCommands(page)) === JSON.stringify([["cancel", `pf cancel ${W6.wi}`]]) || await shownCommands(page), "pf cancel alone");
      assert.equal(await chat(page).locator('[data-pf-chat-command="resume"]').count(), 0, "no pf resume");
      assert.equal(await input(page).count(), 0, "no input box");
    });
    // a paused job: one pf resume above the box, no cause
    await withPage(async (page) => {
      await openDock(page);
      await openJob(page, J4);
      await waitFor(async () => (await chat(page).getAttribute("data-pf-chat-park")) === "paused", "J4: paused");
      await waitFor(async () => JSON.stringify(await shownCommands(page)) === JSON.stringify([["resume", `pf resume ${J4.wi}`]]) || await shownCommands(page), "one pf resume");
      await waitFor(async () => { const c = await box(chat(page).locator('[data-pf-chat-command="resume"]')); const i = await box(input(page)); return Boolean(c && i && c.bottom <= i.top) || { command: c, input: i }; }, "the command sits above the input box");
      assert.equal(await cause(page).count(), 0, "no cause: the paused job carries no blocked field");
    });
  });

  if (errors.length) console.log(`\nBROWSER ERRORS: ${errors.join(" | ")}`);
  if (failures.length || errors.length) {
    console.log(`\nFAIL job-chat-464: ${failures.join(", ") || "browser errors"}`);
    process.exitCode = 1;
  } else {
    console.log("\nPASS all job-chat-464 scenarios");
  }
} finally {
  await h.close();
  rmSync(work, { force: true, recursive: true });
}
