// carries: lib/claude-adapter.ts, lib/claude-config-dir.ts, lib/claude-session.ts, lib/claude-subagent-view.ts, lib/message-display.ts
// carries: components/ClaudeSubagentRow*, components/ClaudeSubagentRunningList*, components/ClaudeSubagentPanel*, components/ComposerBox*, e2e/claude-stub/claude-subagent.mjs
// carries: components/ChatInput*, components/ChatWindow*, components/ChatMinimap.test.mjs
// — first-class claude subagent rendering. Authored against
// artifacts/spec.md and artifacts/plan.md before `code`: the contract scenarios
// read the real 2.1.312 recording under lib/__fixtures__/claude-2.1.312/subagent/,
// and the browser scenarios drive the existing claude session surfaces with a
// PATH-injected stub that replays it (e2e/claude-stub/claude-subagent.mjs). No
// symbol that does not exist on the pre-code tree is imported; pre-implementation
// the suite fails at assertion level. The fold-exception state (parent turn folded
// while the subagent runs) is document-only: the real stdin recording emits the
// parent's result after the subagent finishes (see artifacts/e2e-scenarios.md).
// adds R441 (the running list above the composer, its panel, jump and
// close, inside the same hold window) and R441b (the list gone after the finish).
// adds R482a–e: a second session replays the same recording with 60 more
// background subagents that claude never settles (a scratch copy of the fixture,
// rewritten before that session's child starts), and the list, the panel, Close, Esc
// and switching between them are checked at a desktop and at a phone's width.
// drops the list's count label: R482a (and R482d/R482e, through
// checkManyList) assert nothing but the items shows in the list. R484a–b drag the
// panel's top edge: with a mouse at 1280x900 (border widths, taller and shorter, clamped
// to 160px and to the message area, the height shared by every panel, double-click back
// to the default) and with touch at 390x844 (taller, shorter, title row and Close on screen).
// adds R485 in that session: the panel's input box is the main session's
// (a `[data-composer-box]` row bordered and rounded like `.chat-composer-row`, the Send
// button's box inside it); Shift+Enter is a new line and relays nothing, Enter relays,
// and a refused relay keeps the text in the panel, not in the main composer.
// lays the list out as an equal-width grid: R505a starts a third session with
// three running subagents whose names differ widely in length and checks, at 390x844 and at
// 1280x900, that the buttons of one column share their left and right edges, that the
// elapsed times end on one right edge, that the long name ends in an ellipsis and that its
// button's title (the hover text) is the full name. checkManyList (R482a/d/e) now counts the
// grid rows inside the box (three at most); checkManyPanelClose (R482b/d/e) clicks an item
// with the composer focused and checks the composer keeps the focus.
// adds R531a in that third session: at 1280x900, 900x900 and 390x844 the
// running list's grid starts and ends where the composer's box does (within 1px), the long
// name is still cut with an ellipsis inside its cell, left of its elapsed time, and the
// page has no horizontal scrollbar.
import assert from "node:assert/strict";
import { chmodSync, copyFileSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { createJiti } from "jiti";
import { createCheck, createWaitUntil, errorText, launchBrowser, seedStorage } from "./harness.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const distDir = process.env.PI_WEB_DIST_DIR || ".next";
assert.ok(!existsSync(join(root, distDir, "dev/lock")), "Use a checkout without an active dev server");
const fixtureDir = join(root, "lib/__fixtures__/claude-2.1.312/subagent");
const fixture = (name) => readFileSync(join(fixtureDir, name), "utf8");
const fixtureLines = (name) => fixture(name).split("\n").filter(Boolean);
const jiti = createJiti(import.meta.url, { alias: { "@": root }, interopDefault: true });
const adapter = await jiti.import(join(root, "lib/claude-adapter.ts"));
const parse = (name) => adapter.parseClaudeTranscript(fixture(name));

const AGENT_TOOL_ID = "toolu_01F1YZd2Gseivs4hckhQVw5s";
const READ_TOOL_ID = "toolu_012N7brUKegpGiA36sdi1ApH";
const AGENT_ID = "a9fb2ff3aa8e8ff40";
// The sidecar's own entries (the prompt entry c281353e is the row's prompt, not a timeline entry).
const SIDECAR_ENTRY_IDS = [
  "989c7aa3-823f-4e39-b826-7a4fa64b76cc",
  "9e9ed764-c94f-4c81-9581-cf1ff958eb80",
  "95f1af84-d81a-4ab5-adfd-e41b2d9a338b",
];
const TOKEN = "SUBAGENT_PROBE_TOKEN_253";
const AGENT_DESCRIPTION = "Read note.txt first line";
const SUBAGENT_PROMPT = "Read the file note.txt in the current directory";
const MAIN_USER_TEXT = "Use the Agent tool exactly once to launch a general-purpose subagent";
const PROMPT = JSON.parse(fixtureLines("transcript.jsonl")[0]).message.content;
// The history path reads the sidecar beside the transcript, keyed by claude's agent id
// (owner ruling s14#2/#3): the main transcript alone cannot carry the sidecar's entries.
const sidecars = () => ({
  [AGENT_ID]: {
    entries: parse(`subagents/agent-${AGENT_ID}.jsonl`),
    meta: JSON.parse(fixture(`subagents/agent-${AGENT_ID}.meta.json`)),
  },
});
const historyOf = () => adapter.claudeTranscriptToMessages(parse("transcript.jsonl"), sidecars());

const TITLES = {
  C1: "The live stream attaches the subagent's Read, result and text to the Agent run, never to the main conversation",
  C2: "History rebuilds the same subagent entries from the sidecar and keeps them out of the main message list",
  C3: "Live and history produce the same subagent entries without duplicates",
  C4: "The task notification is a hidden turn anchor in history, not a user message",
  S1: "The Agent row header shows the type badge, description and live status",
  S2: "Expanding the Agent row while the subagent runs shows the prompt, its Read row and result; collapsing mounts nothing",
  S3: "The row expands by default while running, and a manual toggle wins over later state changes",
  S4: "The async launch receipt is not completion; task events set the final status and statistics",
  S5: "Progress updates change only the row header, adding no timeline line",
  S6: "Expanding or collapsing the Agent row leaves the main conversation and composer unchanged",
  S7: "The lease reports an unsettled subagent run while it runs",
  S8: "Reloading after completion shows the same entries once from the sidecar, with the follow-up reply as its own turn",
  S3b: "A manual expansion survives the settled state change",
  S4b: "Task events set the settled status and statistics after the follow-up turn",
  S7b: "The lease stops reporting an unsettled run after the last follow-up turn",
  R441: "While the subagent runs, the list above the composer shows it; its item opens the live panel, which jumps to the Agent row and closes",
  R441b: "After the subagent finishes, the list disappears and the Agent row stays",
  R482a: "With 60 subagents running, the list is at most three rows tall, scrolls inside itself, with no count label",
  R482b: "With 60 subagents running, clicking an item leaves the focus in the composer, an opened panel keeps its title row and Close on screen, Close closes it, and another item switches it",
  R482c: "With 60 subagents running, Esc from the composer closes the open panel",
  R485: "The panel's input box is the main composer's box with Send inside it; Shift+Enter is a new line, Enter relays, and a refused relay keeps the text",
  R482d: "At a 390x844 phone viewport, the list stays three rows tall and scrolls, and the panel keeps Close on screen, switches, and closes on Close and on Esc",
  R482e: "At a 390x480 viewport the panel shrinks to the space above the list and composer, its title row and Close still on screen",
  R484a: "At 1280x900 the panel's borders are equally wide, and dragging its top edge makes it taller and shorter, clamped to 160px and to the message area, kept for another subagent's panel, and reset by a double-click",
  R484b: "At a 390x844 phone viewport a touch drag on the panel's top edge makes it taller and shorter, its title row and Close still on screen",
  R505a: "With three running subagents named short, medium and very long, the buttons of a column share their left and right edges, the elapsed times end on one right edge, the long name ends in an ellipsis and its hover title is the full name, at 390x844 and at 1280x900",
  R531a: "With three running subagents, the running list's left and right edges match the composer box's within 1px at 1280x900, 900x900 and 390x844, the long name is cut with an ellipsis inside its cell on the left, and the page has no horizontal scrollbar",
};
const failures = [];
const scenario = createCheck({ failures, message: errorText, record: ([id], text) => `${id}: ${text}`, fail: ([id], text) => console.log(`FAIL ${id} (${TITLES[id]}): ${text}`) });

function translated(recorded) {
  const translator = new adapter.ClaudeStreamTranslator(() => 1000);
  const events = [];
  for (const event of recorded) events.push(...translator.push(event).events);
  return events;
}
const has = (value, needle) => JSON.stringify(value ?? null).includes(needle);
const toolIds = (value) => [...new Set(String(JSON.stringify(value ?? null)).match(/toolu_[A-Za-z0-9]+/g) ?? [])].sort();
const entryIds = (entries) => entries.map((entry) => entry?.id).filter((id) => typeof id === "string" && id);
const uniqueSorted = (list) => [...new Set(list)].sort();
const noDuplicateIds = (ids, what) => assert.equal(new Set(ids).size, ids.length, `${what} contains a duplicate id: ${ids.join(", ")}`);
const isSubagentEvent = (event) => String(event?.type) === "claude_subagent";

await scenario("C1", async () => {
  const events = translated(parse("stream.jsonl"));
  const subagentEvents = events.filter(isSubagentEvent);
  assert.ok(subagentEvents.length > 0, `no claude_subagent event is emitted (types: ${[...new Set(events.map((event) => event.type))].join(", ")})`);
  // The subagent's own ids, not the probe token: claude's follow-up reply legitimately quotes the token (owner ruling s14#1).
  const ownIds = [READ_TOOL_ID, ...SIDECAR_ENTRY_IDS];
  const carriers = events.filter((event) => ownIds.some((id) => has(event, id)));
  assert.ok(carriers.length > 0, "no stream event carries the subagent's own Read id or entry ids");
  for (const event of carriers) assert.ok(isSubagentEvent(event), `the subagent's own id leaked into a ${event.type} event`);
  assert.ok(has(subagentEvents, READ_TOOL_ID), "the subagent event carries no Read id");
  assert.ok(has(subagentEvents, TOKEN), "the subagent event carries no subagent text");
  const main = events.filter((event) => /^(message_|tool_execution_|agent_end$)/.test(String(event.type)));
  assert.ok(ownIds.every((id) => !has(main, id)), "a subagent entry id or tool id appears in a main-conversation event");
  assert.ok(has(subagentEvents, AGENT_TOOL_ID), "the subagent event is not linked to the Agent tool call");
});

await scenario("C2", async () => {
  const history = historyOf();
  assert.ok(Array.isArray(history.subagents) && history.subagents.length === 1, `history exposes ${history.subagents === undefined ? "no" : history.subagents.length} subagent runs`);
  for (const needle of [AGENT_TOOL_ID, READ_TOOL_ID, TOKEN, AGENT_DESCRIPTION]) {
    assert.ok(has(history.subagents, needle), `the history subagent run does not carry ${needle}`);
  }
  assert.ok(has(history.messages, AGENT_TOOL_ID), "the Agent tool call is missing from the main message list");
  // The main list keeps none of the subagent's own ids; the token may appear in the follow-up reply (s14#2).
  assert.ok(!has(history.messages, READ_TOOL_ID), "the subagent's Read id still appears in the main message list");
  for (const id of SIDECAR_ENTRY_IDS) assert.ok(!has(history.messages, id), `the sidecar entry ${id} appears in the main message list`);
});

await scenario("C3", async () => {
  const liveEntries = translated(parse("stream.jsonl"))
    .filter(isSubagentEvent)
    .flatMap((event) => (Array.isArray(event.run?.entries) ? event.run.entries : []));
  const liveIds = entryIds(liveEntries);
  noDuplicateIds(liveIds, "the live subagent entries");
  const history = historyOf();
  const historyRuns = Array.isArray(history.subagents) ? history.subagents : [];
  const historyEntries = historyRuns.flatMap((run) => (Array.isArray(run.entries) ? run.entries : []));
  const historyIds = entryIds(historyEntries);
  noDuplicateIds(historyIds, "the history subagent entries");
  assert.ok(historyIds.length > 0, "history carries no subagent entry ids");
  assert.deepEqual(uniqueSorted(liveIds), uniqueSorted(historyIds), `live subagent entries ${uniqueSorted(liveIds).join(",")} differ from history ${uniqueSorted(historyIds).join(",")}`);
  // Compare ids, not JSON substrings: the Read result, the text entry and the notification each carry the token.
  const liveTools = toolIds(liveEntries).filter((id) => id !== AGENT_TOOL_ID);
  const historyTools = toolIds(historyEntries).filter((id) => id !== AGENT_TOOL_ID);
  assert.deepEqual(liveTools, historyTools, `live subagent tool ids ${liveTools.join(",")} differ from history ${historyTools.join(",")}`);
  assert.ok(liveTools.includes(READ_TOOL_ID), "the subagent's Read tool id is missing from the live and history entries");
});

await scenario("C4", async () => {
  const history = adapter.claudeTranscriptToMessages(parse("transcript.jsonl"));
  const notificationUsers = history.messages.filter((message) => message.role === "user" && has(message, "<task-notification>"));
  assert.equal(notificationUsers.length, 0, "the task notification is rendered as a user message");
  const anchors = history.messages.filter((message) => message.role !== "user" && has(message, "task-notification"));
  assert.ok(anchors.length >= 1, "no hidden turn anchor carries the task notification");
  assert.ok(has(history.messages, "The subagent finished and reported"), "the follow-up reply is missing from history");
});

const artifacts = join(root, "test-results/e2e");
mkdirSync(artifacts, { recursive: true });
const scratch = mkdtempSync(join(tmpdir(), "pi-web-claude-subagent-e2e-"));
const agentDir = join(scratch, "agent");
const project = join(scratch, "project");
const stubDir = join(scratch, "bin");
const stubLog = join(scratch, "claude-stub.jsonl");
const claudeDir = join(agentDir, "claude");
// The stub replays this copy of the fixture: the first session the recording as is, the
// second (R482) the recording with 60 more subagents (manySubagentsStream()).
const stubFixtureDir = join(scratch, "fixture");
cpSync(fixtureDir, stubFixtureDir, { recursive: true });
mkdirSync(project, { recursive: true });
mkdirSync(agentDir, { recursive: true });
mkdirSync(stubDir, { recursive: true });
mkdirSync(claudeDir, { recursive: true });
writeFileSync(join(project, "note.txt"), `${TOKEN}\nsecond line of the note\n`);
copyFileSync(join(root, "e2e/claude-stub/claude-subagent.mjs"), join(stubDir, "claude"));
chmodSync(join(stubDir, "claude"), 0o755);

let stopServer = async () => {};
let browser;
let page;
let base;
let sessionId;
let leaseWhileRunning;

const stubLaunches = () => (existsSync(stubLog) ? readFileSync(stubLog, "utf8").split("\n").filter(Boolean).flatMap((line) => {
  try { return [JSON.parse(line)]; } catch { return []; }
}).filter((entry) => entry.kind === "launch") : []);

const waitFor = createWaitUntil({ every: 200 });
async function getJson(pathname, init) {
  const response = await fetch(`${base}${pathname}`, { ...init, signal: AbortSignal.timeout(60_000) });
  return { status: response.status, body: await response.json().catch(() => ({})) };
}
async function openSession() {
  await page.goto(`${base}/?session=${encodeURIComponent(sessionId)}`, { waitUntil: "domcontentloaded", timeout: 480_000 });
  await page.locator(".chat-input-textarea").waitFor({ state: "visible", timeout: 180_000 });
}
const agentText = () => page.getByText(AGENT_DESCRIPTION, { exact: true }).first();
// The Agent row's own container (components/ClaudeSubagentRow.tsx): the token/progress
// counts stay inside it, so the follow-up reply's inline token cannot inflate them.
const agentRow = () => page.locator(`[data-claude-subagent="${AGENT_TOOL_ID}"]`).first();
async function agentButton() {
  await agentText().waitFor({ state: "visible", timeout: 30_000 });
  const button = agentText().locator("xpath=ancestor::button[1]");
  if (await button.count()) return button;
  return agentText().locator("xpath=ancestor::*[@role='button'][1]");
}
async function headerText() {
  return (await (await agentButton()).textContent()) ?? "";
}
async function toggleRow() {
  await (await agentButton()).click({ timeout: 10_000 });
  await delay(150);
}
async function expandProcessDetails() {
  const folded = page.locator('button[aria-expanded="false"][aria-label^="Process details"]');
  for (let i = 0; i < 20; i += 1) {
    if (!(await folded.count())) break;
    await folded.first().click({ timeout: 5_000 }).catch(() => {});
    await delay(120);
  }
}
const tokenCount = () => agentRow().getByText(TOKEN, { exact: true }).count();
const pageTokenCount = () => page.getByText(TOKEN, { exact: true }).count();
const promptVisible = () => agentRow().getByText(SUBAGENT_PROMPT, { exact: false }).count();
const progressCount = () => agentRow().getByText("Reading note.txt", { exact: true }).count();
async function waitFollowUp() {
  await page.getByText("The subagent finished and reported", { exact: false }).first().waitFor({ timeout: 70_000 });
}
async function waitStats() {
  await waitFor(async () => /1\s*tools?|completed|finished/i.test(await headerText()), 20_000, "the finished row shows no status or tool statistics");
}
// the running list above the composer and the progress panel.
const subagentList = () => page.locator("[data-claude-subagent-list]");
const listItem = () => subagentList().locator(`[data-claude-subagent-item="${AGENT_TOOL_ID}"]`);
const subagentPanel = () => page.locator(`[data-claude-subagent-panel="${AGENT_TOOL_ID}"]`);
async function openPanelFromList() {
  await listItem().click({ timeout: 10_000 });
  await waitFor(async () => (await subagentPanel().count()) === 1 && (await subagentPanel().isVisible()), 10_000, "clicking the list item opened no panel");
}
// Whether the Agent row's top edge lies inside the visible box of its scrolling ancestor.
const agentRowInView = () => agentRow().evaluate((row) => {
  let container = row.parentElement;
  while (container && !/(auto|scroll)/.test(getComputedStyle(container).overflowY)) container = container.parentElement;
  if (!container) return false;
  const rowBox = row.getBoundingClientRect();
  const box = container.getBoundingClientRect();
  return rowBox.top >= box.top - 1 && rowBox.top < box.bottom;
});
let listSeen = false;

// 60 subagents running at once. Right after the recorded task_started come
// 60 more, backgrounded, with ids of their own and a long prompt (so a panel is as tall as
// it gets); claude's later background task lists name them as still running, and nothing
// settles them, so the 60 stay in the list after the recorded subagent finishes.
const MANY = 60;
// Four or five words each, as claude's Agent descriptions go: at 1280px wide the 60
// wrap into 20 rows, about as many as reported.
const MANY_LABELS = ["Audit the session store module", "Trace the gateway request path", "Review the config loader tests", "Check the retry policy edge cases"];
const manyToolCallId = (n) => `toolu_e2e482_${String(n).padStart(2, "0")}`;
function manySubagentsStream() {
  return subagentsStream(Array.from({ length: MANY }, (_, index) => `${MANY_LABELS[index % MANY_LABELS.length]} ${index + 1}`), manyToolCallId, "e2e482");
}
// The recording with one more backgrounded subagent per label, never settled: the n-th
// (from 1) gets the tool call id idOf(n) and the label as its description. With `calls`,
// the recorded Agent call's message also carries an Agent call per label, and its launch
// receipt one receipt per call: a run's elapsed time counts from its call's frame.
function subagentsStream(labels, idOf, tag, { calls = false } = {}) {
  const lines = fixtureLines("stream.jsonl");
  const startAt = lines.findIndex((line) => JSON.parse(line).subtype === "task_started");
  assert.ok(startAt > 0, "the recording has no task_started");
  const recorded = JSON.parse(lines[startAt]);
  const extra = labels.map((label, index) => {
    const n = index + 1;
    return {
      ...recorded,
      task_id: `${tag}agent${String(n).padStart(2, "0")}`,
      run_id: `${tag}-run-${n}`,
      tool_use_id: idOf(n),
      description: label,
      prompt: Array.from({ length: 30 }, (_, line) => `Brief line ${line + 1} for subagent ${n}.`).join("\n"),
      is_backgrounded: true,
      uuid: `${tag}-start-${n}`,
    };
  });
  const tasks = extra.map((event) => ({ task_id: event.task_id, run_id: event.run_id, task_type: "local_agent", subagent_type: event.subagent_type, description: event.description }));
  const withCalls = (line) => {
    if (!calls) return line;
    const event = JSON.parse(line);
    const content = event.message?.content;
    if (!Array.isArray(content)) return line;
    const call = content.find((block) => block.type === "tool_use" && block.id === recorded.tool_use_id);
    if (call) return JSON.stringify({ ...event, message: { ...event.message, content: [...content, ...extra.map((started) => ({ ...call, id: started.tool_use_id, input: { ...call.input, description: started.description, prompt: started.prompt, run_in_background: true } }))] } });
    // One receipt event per call after the recorded one: an event's receipt names one agent.
    const receipt = content.some((block) => block.type === "tool_result" && block.tool_use_id === recorded.tool_use_id);
    if (receipt) return [line, ...extra.map((started) => line.replaceAll(recorded.tool_use_id, started.tool_use_id).replaceAll(recorded.task_id, started.task_id).replaceAll(event.uuid, `${tag}-receipt-${started.tool_use_id}`))].join("\n");
    return line;
  };
  const after = lines.slice(startAt + 1).map((line) => {
    const event = JSON.parse(line);
    return event.subtype === "background_tasks_changed" ? JSON.stringify({ ...event, tasks: [...event.tasks, ...tasks] }) : withCalls(line);
  });
  return `${[...lines.slice(0, startAt).map(withCalls), lines[startAt], ...extra.map((event) => JSON.stringify(event)), ...after].join("\n")}\n`;
}
let manySessionId;
const listItems = () => subagentList().locator("[data-claude-subagent-item]");
const openPanels = () => page.locator("[data-claude-subagent-panel]");
const panelOf = (toolCallId) => page.locator(`[data-claude-subagent-panel="${toolCallId}"]`);
const itemOf = (toolCallId) => subagentList().locator(`[data-claude-subagent-item="${toolCallId}"]`);
// The list's height against one item's, the text it shows outside its items, and whether
// something inside it (not the page) scrolls: scrolled to its end, the last item comes
// into that box and the first leaves it.
async function manyListFacts() {
  return subagentList().evaluate((list) => {
    const items = [...list.querySelectorAll("[data-claude-subagent-item]")];
    const outside = [];
    const walker = document.createTreeWalker(list, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      if (!node.parentElement?.closest("[data-claude-subagent-item]")) outside.push(node.textContent ?? "");
    }
    const scroller = [list, ...list.querySelectorAll("*")].find((element) => /(auto|scroll)/.test(getComputedStyle(element).overflowY) && element.scrollHeight > element.clientHeight + 1);
    const inBox = (item, box) => {
      const rect = item.getBoundingClientRect();
      return rect.top >= box.top - 1 && rect.bottom <= box.bottom + 1;
    };
    let scrolls = false;
    let rows = 0;
    if (scroller && items.length > 1) {
      scroller.scrollTop = 0;
      const top = scroller.getBoundingClientRect();
      const firstAtTop = inBox(items[0], top);
      // The grid rows wholly inside the box: the distinct tops of the items in it.
      rows = new Set(items.filter((item) => inBox(item, top)).map((item) => Math.round(item.getBoundingClientRect().top))).size;
      scroller.scrollTop = scroller.scrollHeight;
      const end = scroller.getBoundingClientRect();
      scrolls = firstAtTop && inBox(items[items.length - 1], end) && !inBox(items[0], end);
      scroller.scrollTop = 0;
    }
    const box = list.getBoundingClientRect();
    return { count: items.length, listHeight: box.height, listTop: box.top, itemHeight: items[0]?.getBoundingClientRect().height ?? 0, outside: outside.join(" ").trim(), scrolls, rows };
  });
}
async function checkManyList() {
  await waitFor(async () => (await listItems().count()) === MANY, 10_000, `the running list does not hold the ${MANY} running subagents`);
  const facts = await manyListFacts();
  assert.ok(facts.itemHeight > 0, "the list's items have no height");
  assert.ok(facts.listHeight < 4 * facts.itemHeight, `the list is ${facts.listHeight}px tall for ${facts.itemHeight}px items: more than three rows`);
  assert.ok(facts.listTop >= 0, `the list starts above the viewport (${facts.listTop}px)`);
  assert.ok(facts.scrolls, "nothing inside the list scrolls its items into view");
  assert.equal(facts.rows, 3, `the list's box shows ${facts.rows} grid rows of items, not three`);
  assert.equal(facts.outside, "", `the list shows text outside its items (no count label,): "${facts.outside}"`);
  const list = await subagentList().boundingBox();
  const input = await page.locator(".chat-input-textarea").boundingBox();
  assert.ok(list && input && list.y + list.height <= input.y + 0.5, "the running list is not above the composer's text box");
}
// Whether the panel's title row and Close lie inside the viewport and Close is what a
// click at its centre would hit (not clipped away or covered).
async function panelHeaderFacts(toolCallId) {
  return panelOf(toolCallId).evaluate((panel) => {
    const close = [...panel.querySelectorAll("button")].find((button) => button.getAttribute("aria-label") === "Close");
    if (!close) return { close: false };
    const box = close.getBoundingClientRect();
    const hit = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2);
    return { close: true, panelTop: panel.getBoundingClientRect().top, closeTop: box.top, closeBottom: box.bottom, viewport: window.innerHeight, hit: Boolean(hit && close.contains(hit)) };
  });
}
async function openFromList(toolCallId) {
  await itemOf(toolCallId).click({ timeout: 10_000 });
  await waitFor(async () => (await panelOf(toolCallId).count()) === 1 && (await panelOf(toolCallId).isVisible()), 10_000, `clicking ${toolCallId} in the list opened no panel`);
}
async function assertHeaderOnScreen(toolCallId) {
  const header = await panelHeaderFacts(toolCallId);
  assert.ok(header.close, "the open panel has no Close button");
  assert.ok(header.panelTop >= 0, `the panel's title row starts ${-header.panelTop}px above the viewport`);
  assert.ok(header.closeTop >= 0 && header.closeBottom <= header.viewport, `Close lies outside the viewport (${header.closeTop}..${header.closeBottom} of ${header.viewport})`);
  assert.ok(header.hit, "a click at Close's centre does not reach Close");
}
async function closeWithButton(toolCallId) {
  await panelOf(toolCallId).getByRole("button", { name: "Close", exact: true }).click({ timeout: 10_000 });
  await waitFor(async () => (await openPanels().count()) === 0, 5_000, "Close left the panel open");
}
async function checkManyPanelClose() {
  const last = manyToolCallId(MANY);
  // Opening a panel from the list leaves the focus in the composer.
  const composer = page.locator(".chat-input-textarea");
  await composer.focus();
  await openFromList(last);
  await waitFor(() => composer.evaluate((element) => document.activeElement === element), 5_000, "clicking a list item took the focus away from the composer");
  await assertHeaderOnScreen(last);
  await closeWithButton(last);
  await openFromList(manyToolCallId(1));
  await openFromList(manyToolCallId(2));
  assert.equal(await openPanels().count(), 1, "another item opened a second panel instead of switching");
  await assertHeaderOnScreen(manyToolCallId(2));
  await closeWithButton(manyToolCallId(2));
}
async function checkManyPanelEscape() {
  // Opening the first item switches to it whatever was open: one panel stands open now.
  await openFromList(manyToolCallId(1));
  assert.equal(await openPanels().count(), 1, "not exactly one panel is open before Esc");
  // The composer keeps the focus while the panel is open: Esc from there closes it.
  await page.locator(".chat-input-textarea").focus();
  await page.keyboard.press("Escape");
  await waitFor(async () => (await openPanels().count()) === 0, 5_000, "Esc left the panel open");
}
// three running subagents, their names short, medium and longer than any cell.
const ALIGN_LABELS = [
  "Lint",
  "Trace the gateway request path",
  "Audit every module of the session store for stale lease handling, then report each finding with its file, its line and the smallest change that would fix it",
];
const alignToolCallId = (n) => `toolu_e2e505_${n}`;
// Each item's box, its elapsed time's right edge and whether its name overflows into an
// ellipsis. The elapsed time is the button's last span (as R441 reads it).
async function alignFacts() {
  return subagentList().evaluate((list) => [...list.querySelectorAll("[data-claude-subagent-item]")].map((item) => {
    const box = item.getBoundingClientRect();
    const spans = item.querySelectorAll(":scope > span");
    const name = spans[0];
    const elapsed = spans[spans.length - 1];
    return {
      id: item.getAttribute("data-claude-subagent-item"),
      title: item.getAttribute("title"),
      left: box.left,
      right: box.right,
      top: box.top,
      width: box.width,
      elapsedRight: elapsed.getBoundingClientRect().right,
      elapsedText: elapsed.textContent ?? "",
      elapsedNumeric: getComputedStyle(elapsed).fontVariantNumeric,
      nameText: name.textContent ?? "",
      nameOverflows: name.scrollWidth > name.clientWidth,
      nameTextOverflow: getComputedStyle(name).textOverflow,
    };
  }));
}
// The facts once two reads in a row agree on every edge (a resize settles a frame later).
async function settledAlignFacts() {
  let previous = "";
  let facts = [];
  await waitFor(async () => {
    facts = await alignFacts();
    const key = JSON.stringify(facts.map((item) => [item.left, item.right, item.top, item.elapsedRight]));
    const settled = facts.length === ALIGN_LABELS.length && key === previous;
    previous = key;
    return settled;
  }, 10_000, "the list's layout never settled at three items");
  return facts;
}
async function checkAligned(width, height) {
  await page.setViewportSize({ width, height });
  const facts = await settledAlignFacts();
  const near = (a, b) => Math.abs(a - b) <= 0.5;
  const at = `at ${width}x${height}`;
  for (const item of facts) {
    assert.match(item.elapsedText, /\d/, `${at} ${item.id}'s last span is not its elapsed time: "${item.elapsedText}"`);
    assert.match(item.elapsedNumeric, /tabular-nums/, `${at} ${item.id}'s elapsed time has no tabular digits`);
  }
  // Every cell is equally wide, and each elapsed time ends at the same distance from its
  // button's right edge.
  for (const item of facts.slice(1)) {
    assert.ok(near(item.width, facts[0].width), `${at} the buttons are not equally wide: ${facts.map((f) => f.width).join(", ")}`);
    assert.ok(near(item.right - item.elapsedRight, facts[0].right - facts[0].elapsedRight), `${at} the elapsed times do not end at one inset from their buttons' right edges: ${facts.map((f) => f.right - f.elapsedRight).join(", ")}`);
  }
  // The buttons of one column (one left edge) share their right edge, and their elapsed
  // times end on one right edge.
  const columns = new Map();
  for (const item of facts) columns.set(Math.round(item.left), [...(columns.get(Math.round(item.left)) ?? []), item]);
  if (width <= 520) assert.ok(Math.max(...[...columns.values()].map((column) => column.length)) >= 2, `${at} no column holds two buttons: ${[...columns.keys()].join(", ")}`);
  for (const column of columns.values()) {
    for (const item of column.slice(1)) {
      assert.ok(near(item.left, column[0].left) && near(item.right, column[0].right), `${at} the buttons of one column differ in their edges: ${column.map((f) => `${f.left}..${f.right}`).join(", ")}`);
      assert.ok(near(item.elapsedRight, column[0].elapsedRight), `${at} the elapsed times of one column end on different right edges: ${column.map((f) => f.elapsedRight).join(", ")}`);
    }
  }
  // The long name ends in an ellipsis; its button's hover title is the full name.
  const long = facts.find((item) => item.id === alignToolCallId(3));
  assert.ok(long, `${at} the long-named subagent is not in the list`);
  assert.equal(long.nameText, ALIGN_LABELS[2], `${at} the long name's text is not the full description`);
  assert.ok(long.nameOverflows, `${at} the long name fits its button: no ellipsis`);
  assert.equal(long.nameTextOverflow, "ellipsis", `${at} the long name is not cut with an ellipsis`);
  await itemOf(alignToolCallId(3)).hover({ timeout: 10_000 });
  assert.equal(await itemOf(alignToolCallId(3)).getAttribute("title"), ALIGN_LABELS[2], `${at} hovering the long name does not show the full name`);
  for (const [index, label] of ALIGN_LABELS.entries()) {
    assert.equal(await itemOf(alignToolCallId(index + 1)).getAttribute("title"), label, `${at} item ${index + 1}'s title is not its full name`);
  }
}
async function checkAlignedSession() {
  writeFileSync(join(stubFixtureDir, "stream.jsonl"), subagentsStream(ALIGN_LABELS, alignToolCallId, "e2e505", { calls: true }));
  const created = await getJson("/api/agent/new", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ cwd: project, type: "ensure_session", harness: "claude", modelAlias: "haiku", model: "claude-haiku-5-5", claudePreset: "read-only" }),
  });
  assert.ok(created.status === 200 && typeof created.body.sessionId === "string", `POST /api/agent/new for the third session failed: ${created.status}`);
  const alignSessionId = created.body.sessionId;
  assert.notEqual(alignSessionId, manySessionId, "the third session reused the second");
  await waitFor(() => stubLaunches().find((entry) => entry.sessionId === alignSessionId), 30_000, "no claude child was launched for the third session");
  await page.goto(`${base}/?session=${encodeURIComponent(alignSessionId)}`, { waitUntil: "domcontentloaded", timeout: 480_000 });
  await page.locator(".chat-input-textarea").waitFor({ state: "visible", timeout: 180_000 });
  await page.locator(".chat-input-textarea").fill(PROMPT);
  await page.getByRole("button", { name: "Send", exact: true }).click();
  // Measured once the list is final: the recorded subagent has finished, the three run on.
  await waitFollowUp();
  await waitFor(async () => (await listItems().count()) === ALIGN_LABELS.length, 30_000, `the running list never settled at the ${ALIGN_LABELS.length} subagents still running`);
  await checkAligned(390, 844);
  await checkAligned(1280, 900);
}
// the list's grid against the composer's box (the row the textarea sits in,
// stacked or not), the long name's span against its button and its elapsed time, and the
// page's own horizontal overflow.
async function composerAlignFacts() {
  return page.evaluate((longId) => {
    const rect = (element) => {
      if (!element) return null;
      const box = element.getBoundingClientRect();
      return { left: box.left, right: box.right };
    };
    const items = document.querySelector("[data-claude-subagent-list] [data-claude-subagent-items]");
    const buttons = items ? [...items.querySelectorAll("[data-claude-subagent-item]")].map(rect) : [];
    const textarea = document.querySelector(".chat-input-shell .chat-input-textarea");
    const longItem = items?.querySelector(`[data-claude-subagent-item="${longId}"]`) ?? null;
    const spans = longItem ? longItem.querySelectorAll(":scope > span") : [];
    const name = spans[0] ?? null;
    return {
      items: rect(items),
      box: rect(textarea?.parentElement ?? null),
      buttonsLeft: buttons.length ? Math.min(...buttons.map((b) => b.left)) : null,
      buttonsRight: buttons.length ? Math.max(...buttons.map((b) => b.right)) : null,
      count: buttons.length,
      long: rect(longItem),
      name: rect(name),
      elapsed: rect(spans.length > 1 ? spans[spans.length - 1] : null),
      nameTextOverflow: name ? getComputedStyle(name).textOverflow : null,
      nameOverflows: name ? name.scrollWidth > name.clientWidth : false,
      pageScrollWidth: document.documentElement.scrollWidth,
      pageClientWidth: document.documentElement.clientWidth,
    };
  }, alignToolCallId(3));
}
// The facts once two reads in a row agree (a resize settles a frame or two later).
async function settledComposerAlignFacts(at) {
  let previous = "";
  let facts;
  await waitFor(async () => {
    facts = await composerAlignFacts();
    const key = JSON.stringify(facts);
    const settled = facts.count === ALIGN_LABELS.length && facts.box !== null && key === previous;
    previous = key;
    return settled;
  }, 10_000, `${at} the list and the composer never settled`);
  return facts;
}
async function checkComposerAligned() {
  await waitFor(async () => (await listItems().count()) === ALIGN_LABELS.length, 30_000, `the running list never showed the ${ALIGN_LABELS.length} subagents still running`);
  const off = (a, b) => Math.abs(a - b) > 1;
  const problems = [];
  for (const [width, height] of [[1280, 900], [900, 900], [390, 844]]) {
    const at = `at ${width}x${height}`;
    await page.setViewportSize({ width, height });
    const f = await settledComposerAlignFacts(at);
    if (off(f.items.left, f.box.left) || off(f.items.right, f.box.right)) problems.push(`${at} the list spans ${f.items.left}..${f.items.right}, the composer box ${f.box.left}..${f.box.right}`);
    if (off(f.buttonsLeft, f.box.left) || off(f.buttonsRight, f.box.right)) problems.push(`${at} the list's buttons span ${f.buttonsLeft}..${f.buttonsRight}, the composer box ${f.box.left}..${f.box.right}`);
    if (f.nameTextOverflow !== "ellipsis" || !f.nameOverflows) problems.push(`${at} the long name is not cut with an ellipsis (text-overflow ${f.nameTextOverflow}, overflows ${f.nameOverflows})`);
    if (!f.long || !f.name || !f.elapsed || f.name.left < f.long.left - 0.5 || f.name.right > f.elapsed.left + 0.5 || f.elapsed.right > f.long.right + 0.5) problems.push(`${at} the long name is not inside its cell left of its elapsed time: button ${JSON.stringify(f.long)}, name ${JSON.stringify(f.name)}, elapsed ${JSON.stringify(f.elapsed)}`);
    if (f.long && f.long.right > f.items.right + 0.5) problems.push(`${at} the long name's button ends at ${f.long.right}, past the list's right edge ${f.items.right}`);
    if (f.pageScrollWidth > f.pageClientWidth) problems.push(`${at} the page scrolls sideways: scrollWidth ${f.pageScrollWidth} > clientWidth ${f.pageClientWidth}`);
  }
  assert.deepEqual(problems, [], problems.join("; "));
}
// the panel's input box, with a refused relay. The relay goes to the
// session's own prompt (the 60 runs are backgrounded with agent ids); the route answers it
// with the server's refusal envelope, so nothing reaches the stub's main agent.
async function checkPanelComposer() {
  const id = manyToolCallId(1);
  const panel = panelOf(id);
  const relays = [];
  const agentPath = `/api/agent/${encodeURIComponent(manySessionId)}`;
  const isAgentPost = (url) => new URL(url).pathname === agentPath;
  const refuse = async (route) => {
    const request = route.request();
    let body;
    try { body = request.postDataJSON(); } catch { body = undefined; }
    if (request.method() !== "POST" || body?.type !== "prompt") return route.fallback();
    relays.push(body);
    return route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: "E2E-485 refused", code: "prompt_rejected", accepted: false }) });
  };
  await page.route(isAgentPost, refuse);
  try {
    await openFromList(id);
    const box = panel.locator("[data-composer-box]");
    await waitFor(async () => (await box.count()) === 1, 5_000, "the panel renders no shared input box ([data-composer-box])");
    const input = box.locator("textarea");
    const send = box.getByRole("button", { name: "Send", exact: true });
    assert.equal(await input.count(), 1, "the panel's textarea is not inside the box");
    assert.equal(await send.count(), 1, "the panel's Send button is not inside the box");
    assert.equal(await panel.locator("textarea").count(), 1, "the panel holds another textarea outside the box");
    const style = (locator) => locator.evaluate((element) => ({ border: getComputedStyle(element).borderTopWidth, radius: getComputedStyle(element).borderRadius }));
    const own = await style(box);
    assert.ok(parseFloat(own.border) >= 1, `the box has no border (${own.border})`);
    assert.equal(own.radius, (await style(page.locator(".chat-composer-row").first())).radius, "the box is not rounded like the main composer's row");
    await waitFor(async () => input.isEnabled(), 10_000, "the panel's input stays disabled for a run with an agent id");
    await input.fill("E2E-485 one");
    await waitFor(async () => send.isEnabled(), 5_000, "Send is not enabled with text in the box");
    // Both boxes once they stop changing (the textarea sizes itself after the fill).
    let settled;
    let previous = null;
    await waitFor(async () => {
      const now = { row: await box.boundingBox(), button: await send.boundingBox() };
      const same = previous !== null && JSON.stringify(now) === JSON.stringify(previous);
      previous = now;
      settled = now;
      return same && Boolean(now.row && now.button);
    }, 5_000, "the box and its Send button never settled", 100);
    const { row, button } = settled;
    assert.ok(button.x >= row.x - 0.5 && button.y >= row.y - 0.5 && button.x + button.width <= row.x + row.width + 0.5 && button.y + button.height <= row.y + row.height + 0.5,
      `Send lies outside the box (button ${JSON.stringify(button)}, box ${JSON.stringify(row)})`);
    // Shift+Enter: a new line, nothing relayed.
    await input.press("Shift+Enter");
    await input.pressSequentially("two");
    await waitFor(async () => (await input.inputValue()) === "E2E-485 one\ntwo", 5_000, "Shift+Enter made no new line");
    assert.equal(relays.length, 0, "Shift+Enter relayed the text");
    // Enter relays; the refusal keeps the text in the panel and gives the main composer nothing.
    await input.press("Enter");
    await waitFor(async () => relays.length === 1 && String(relays[0].message ?? "").includes("E2E-485 one\ntwo") && (await send.isEnabled()), 10_000, `Enter relayed no message carrying the text, or Send stayed disabled (${relays.length} relays)`);
    assert.equal(await input.inputValue(), "E2E-485 one\ntwo", "the refused relay emptied the panel's box");
    assert.equal(await page.locator(".chat-input-textarea").first().inputValue(), "", "the refused relay put text into the main composer");
    assert.equal(relays.length, 1, "Enter relayed more than once");
    // The refusal shows as a notice; it auto-dismisses unless hovered, so move the mouse off it
    // and wait it out, leaving the page clean for the next scenario (at 390×480 it covers Close).
    const refusedNotice = page.locator(".notice-shelf-item").filter({ hasText: "E2E-485 refused" });
    await waitFor(async () => (await refusedNotice.count()) > 0, 5_000, "the refused relay showed no \"E2E-485 refused\" notice");
    await page.mouse.move(1, 1);
    await waitFor(async () => (await refusedNotice.count()) === 0, 15_000, "the \"E2E-485 refused\" notice never went away");
  } finally {
    await page.unroute(isAgentPost, refuse);
    if (await openPanels().count()) await closeWithButton(id).catch(() => {});
  }
}
function leasePath() {
  return `/api/agent/${encodeURIComponent(sessionId)}/lease?holder=e2e-lease`;
}
// the panel's top-edge resize handle and the height it shares.
const PANEL_HEIGHT_KEY = "pi-web:claude-subagent-panel-height";
const PANEL_MIN_HEIGHT = 160;
// The floating wrapper's vertical padding (8px + 6px) between the message area and the panel.
const PANEL_WRAPPER_PADDING_Y = 14;
const resizeHandle = (toolCallId) => panelOf(toolCallId).locator("[data-claude-subagent-panel-resize]");
const storedPanelHeight = () => page.evaluate((key) => window.localStorage.getItem(key), PANEL_HEIGHT_KEY);
// Polls the panel's rendered height until two consecutive reads agree and `accept` holds.
async function settledHeight(toolCallId, accept, message) {
  const deadline = Date.now() + 10_000;
  let previous;
  let last;
  while (Date.now() < deadline) {
    try { last = await panelOf(toolCallId).evaluate((panel) => panel.getBoundingClientRect().height); } catch { last = undefined; }
    if (last !== undefined && previous !== undefined && Math.abs(last - previous) < 0.5 && last > 0 && accept(last)) return last;
    previous = last;
    await delay(100);
  }
  assert.ok(false, `${message} (last height ${last}px)`);
}
async function handleCentre(toolCallId) {
  assert.equal(await resizeHandle(toolCallId).count(), 1, "the panel has no resize handle at its top edge");
  const box = await resizeHandle(toolCallId).boundingBox();
  assert.ok(box && box.height > 0, "the panel's resize handle is not rendered");
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}
// A target y moved by `dy`, kept inside the viewport (far drags stop at its edge).
const dragTargetY = (y, dy) => Math.min(page.viewportSize().height - 2, Math.max(2, y + dy));
async function mouseDrag(toolCallId, dy) {
  const { x, y } = await handleCentre(toolCallId);
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x, dragTargetY(y, dy), { steps: 8 });
  await page.mouse.up();
}
// A real touch drag: CDP touch events (pointerType touch), start, eight moves, end.
async function touchDrag(cdp, toolCallId, dy) {
  const { x, y } = await handleCentre(toolCallId);
  const end = dragTargetY(y, dy);
  await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y }] });
  for (let step = 1; step <= 8; step += 1) {
    await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x, y: y + ((end - y) * step) / 8 }] });
  }
  await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
}
async function checkPanelResizeMouse() {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.evaluate((key) => window.localStorage.removeItem(key), PANEL_HEIGHT_KEY);
  const first = manyToolCallId(1);
  const second = manyToolCallId(2);
  await openFromList(first);
  const h0 = await settledHeight(first, () => true, "the opened panel's height never settled");
  const borders = await panelOf(first).evaluate((panel) => {
    const style = getComputedStyle(panel);
    return [style.borderLeftWidth, style.borderTopWidth, style.borderRightWidth, style.borderBottomWidth];
  });
  assert.ok(borders.every((width) => width === borders[0]), `the panel's left border is ${borders[0]} wide, its top/right/bottom ${borders.slice(1).join("/")}`);
  await handleCentre(first);
  const handle = await resizeHandle(first).evaluate((element) => ({ role: element.getAttribute("role"), label: element.getAttribute("aria-label"), touchAction: getComputedStyle(element).touchAction }));
  assert.equal(handle.role, "separator", "the resize handle is not a separator");
  assert.equal(handle.label, "Resize panel", "the resize handle has no label");
  assert.equal(handle.touchAction, "none", "the resize handle lets touch scroll the page");
  const bounds = await panelOf(first).evaluate((panel) => panel.closest("[data-claude-subagent-panel-bounds]")?.clientHeight ?? -1);
  assert.ok(bounds > 0, "no message-area box marks the panel's bounds");
  const max = bounds - PANEL_WRAPPER_PADDING_Y;

  await mouseDrag(first, 200);
  const lower = await settledHeight(first, (height) => height < h0 - 1, `dragging the top edge down 200px did not make the ${h0}px panel shorter`);
  await mouseDrag(first, -200);
  await settledHeight(first, (height) => height > lower + 1, `dragging the top edge up 200px did not make the ${lower}px panel taller`);
  await mouseDrag(first, -2000);
  await settledHeight(first, (height) => Math.abs(height - max) <= 2, `dragging far up did not stop at the message area's height (${max}px)`);
  await assertHeaderOnScreen(first);
  await mouseDrag(first, 2000);
  await settledHeight(first, (height) => Math.abs(height - PANEL_MIN_HEIGHT) <= 1, `dragging far down did not stop at ${PANEL_MIN_HEIGHT}px`);
  await assertHeaderOnScreen(first);
  await mouseDrag(first, -150);
  const h1 = await settledHeight(first, (height) => height > PANEL_MIN_HEIGHT + 1 && height < max - 1, "dragging up from the minimum did not leave the panel between the bounds");
  await waitFor(async () => Math.abs(Number(await storedPanelHeight()) - h1) <= 1, 5_000, `the finished drag did not store the ${h1}px height`);

  await closeWithButton(first);
  await openFromList(second);
  await settledHeight(second, (height) => Math.abs(height - h1) <= 1, `another subagent's panel does not open at the dragged ${h1}px height`);
  await resizeHandle(second).dblclick({ timeout: 10_000 });
  await settledHeight(second, (height) => Math.abs(height - h0) <= 1, `double-clicking the handle did not restore the default ${h0}px height`);
  await waitFor(async () => (await storedPanelHeight()) === null, 5_000, "double-clicking the handle left the stored height");
  await assertHeaderOnScreen(second);
  await closeWithButton(second);
}
async function checkPanelResizeTouch() {
  await page.setViewportSize({ width: 390, height: 844 });
  const id = manyToolCallId(3);
  await openFromList(id);
  const cdp = await page.context().newCDPSession(page);
  try {
    await cdp.send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 1 });
    const start = await settledHeight(id, () => true, "the opened panel's height never settled");
    await assertHeaderOnScreen(id);
    await touchDrag(cdp, id, 200);
    const lower = await settledHeight(id, (height) => height < start - 1, `a touch drag down 200px did not make the ${start}px panel shorter`);
    await assertHeaderOnScreen(id);
    await touchDrag(cdp, id, -200);
    const higher = await settledHeight(id, (height) => height > lower + 1, `a touch drag up 200px did not make the ${lower}px panel taller`);
    await assertHeaderOnScreen(id);
    await touchDrag(cdp, id, 150);
    await settledHeight(id, (height) => height < higher - 1, `a touch drag down 150px did not make the ${higher}px panel shorter`);
    await assertHeaderOnScreen(id);
  } finally {
    await cdp.send("Emulation.setTouchEmulationEnabled", { enabled: false }).catch(() => {});
    await cdp.detach().catch(() => {});
  }
  await closeWithButton(id);
}

try {
  const { startNextServer } = await import("./next-server.mjs");
  const started = await startNextServer({
    name: "claude-subagent-253",
    logFile: join(artifacts, "claude-subagent-253-server.log"),
    env: {
      ...process.env,
      PI_CODING_AGENT_DIR: agentDir,
      CLAUDE_CONFIG_DIR: claudeDir,
      PI_WEB_PASSWORD: "",
      PI_WEB_ALLOWED_ROOT_PREFIXES: scratch,
      PATH: `${stubDir}:${process.env.PATH ?? ""}`,
      PI_E2E_CLAUDE_STUB_LOG: stubLog,
      PI_E2E_CLAUDE_FIXTURE_DIR: stubFixtureDir,
      PI_E2E_SUBAGENT_HOLD_MS: "30000",
      NEXT_TELEMETRY_DISABLED: "1",
    },
  });
  stopServer = started.stop;
  base = started.base;
  const { chromium } = await import("playwright");
  browser = await launchBrowser(() => chromium.launch());
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: "en-US", baseURL: base });
  await seedStorage(context);
  page = await context.newPage();
  page.setDefaultTimeout(30_000);

  // The session is created without a prompt (the recording's prompt is sent from
  // the composer) so the page can attach its stream before the subagent starts.
  const created = await getJson("/api/agent/new", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ cwd: project, type: "ensure_session", harness: "claude", modelAlias: "haiku", model: "claude-haiku-5-5", claudePreset: "read-only" }),
  });
  assert.ok(created.status === 200, `POST /api/agent/new failed: ${created.status} ${JSON.stringify(created.body).slice(0, 300)}`);
  sessionId = created.body.sessionId;
  assert.ok(typeof sessionId === "string" && sessionId.length > 0, "the create route returned no session id");
  const launch = await waitFor(() => stubLaunches()[0], 30_000, "no claude child was launched");
  assert.equal(launch.sessionId, sessionId, "the child's session id differs from the session id pi-web returned");
  await openSession();
  await page.locator(".chat-input-textarea").fill(PROMPT);
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await waitFor(async () => (await agentText().count()) > 0, 30_000, "the chat rendered no Agent row");
  await waitFor(async () => (await pageTokenCount()) >= 1, 20_000, "the subagent's text entry never arrived on the stream");

  // Inside the hold window, before S1: the panel is closed again when it ends, so the
  // S-scenarios' token counts are unchanged.
  await scenario("R441", async () => {
    await waitFor(async () => (await subagentList().count()) === 1 && (await subagentList().isVisible()), 10_000, "no running list shows above the composer while the subagent runs");
    await waitFor(async () => (await subagentList().locator("[data-claude-subagent-item]").count()) === 1 && (await listItem().count()) === 1, 5_000, "the running list does not hold exactly the one running subagent");
    const itemText = (await listItem().textContent()) ?? "";
    assert.ok(itemText.includes(AGENT_DESCRIPTION), `the list item carries no description: ${itemText}`);
    assert.match(itemText, /Running/, `the list item carries no running status: ${itemText}`);
    // Format only: the stub replays recorded timestamps; the magnitude is the unit tests'.
    await waitFor(async () => {
      const text = ((await listItem().locator("span").last().textContent()) ?? "").trim();
      return /^\d+(s|m \d+s|h \d+m \d+s)$/.test(text) ? text : undefined;
    }, 5_000, "the list item shows no elapsed time");
    await waitFor(async () => {
      const list = await subagentList().boundingBox();
      const input = await page.locator(".chat-input-textarea").boundingBox();
      return Boolean(list && input && list.y + list.height <= input.y + 0.5);
    }, 5_000, "the running list is not above the composer's text box");

    await openPanelFromList();
    await waitFor(async () => (await subagentPanel().getByText(TOKEN, { exact: true }).count()) >= 1, 10_000, "the panel does not show the subagent's live text");
    await subagentPanel().getByRole("button", { name: "Close", exact: true }).click({ timeout: 10_000 });
    await waitFor(async () => (await subagentPanel().count()) === 0, 5_000, "Close left the panel open");

    await openPanelFromList();
    // The row is on screen in this short conversation, so "in view" alone proves nothing:
    // the jump must also run its highlight animation on the Agent row (review s1#2).
    const rowAnimations = () => agentRow().evaluate((row) => row.getAnimations().length);
    await waitFor(async () => (await rowAnimations()) === 0, 5_000, "the Agent row is already animating before the jump");
    await subagentPanel().getByRole("button", { name: "Jump to launch", exact: true }).click({ timeout: 10_000 });
    await waitFor(async () => (await subagentPanel().count()) === 0, 5_000, "the jump left the panel open");
    await waitFor(async () => (await rowAnimations()) > 0, 2_000, "the jump did not highlight the Agent row", 50);
    await waitFor(agentRowInView, 5_000, "the jump did not bring the Agent row into the message list's view");
    listSeen = true;
  });

  await scenario("S1", async () => {
    const header = await headerText();
    assert.match(header, /general-purpose/i, `the header carries no subagent type badge: ${header}`);
    assert.match(header, /Read note\.txt first line/i, `the header carries no description: ${header}`);
    assert.match(header, /running/i, `the header carries no live status: ${header}`);
  });

  await scenario("S2", async () => {
    assert.equal(await tokenCount(), 1, "the running row is not expanded by default");
    await toggleRow();
    assert.equal(await tokenCount(), 0, "collapsing the row kept the subagent timeline mounted");
    assert.equal(await promptVisible(), 0, "collapsing the row kept the subagent prompt mounted");
    await toggleRow();
    await waitFor(async () => (await tokenCount()) === 1, 5_000, "expanding the row did not reveal the subagent text");
    assert.equal(await promptVisible(), 1, "the expanded body shows no subagent prompt");
    await page.getByRole("button", { name: /read/i }).first().waitFor({ state: "visible", timeout: 10_000 });
  });

  // Running-state facts come first, inside the stub's 30 s hold window (owner ruling s14#5).
  await scenario("S4", async () => {
    const header = await headerText();
    assert.match(header, /running/i, "the async launch receipt already ended the run");
    assert.doesNotMatch(header, /completed|finished|done/i, "the async launch receipt was treated as completion");
  });

  await scenario("S5", async () => {
    await waitFor(async () => /Reading note\.txt/i.test(await headerText()), 15_000, "the progress description never reached the header");
    assert.equal(await progressCount(), 1, "the progress description appears outside the Agent row header");
    assert.equal(await tokenCount(), 1, "a progress update replaced the already-rendered subagent entry");
    await page.getByRole("button", { name: /read/i }).first().waitFor({ state: "visible", timeout: 10_000 });
  });

  await scenario("S7", async () => {
    const running = await getJson(leasePath(), { method: "POST" });
    assert.equal(running.status, 200, `lease failed: ${running.status}`);
    assert.equal(running.body.subagentsActive, true, "the lease does not report the running subagent run");
    leaseWhileRunning = running.body;
  });

  await scenario("S6", async () => {
    const textarea = page.locator(".chat-input-textarea");
    await textarea.fill("draft survives the toggle");
    await textarea.focus();
    const mainUserCount = await page.getByText(MAIN_USER_TEXT, { exact: false }).count();
    const lastReplyCount = await page.getByText("The agent was launched.", { exact: true }).count();
    assert.equal(await tokenCount(), 1, "the row is not expanded before the toggle");
    await toggleRow();
    await toggleRow();
    assert.equal(await tokenCount(), 1, "the row did not reopen after toggling");
    assert.equal(await promptVisible(), 1, "the row shows no subagent prompt after toggling");
    assert.equal(await page.getByText(MAIN_USER_TEXT, { exact: false }).count(), mainUserCount, "toggling changed the main conversation");
    assert.equal(await page.getByText("The agent was launched.", { exact: true }).count(), lastReplyCount, "toggling changed the last reply");
    assert.equal(await textarea.inputValue(), "draft survives the toggle", "toggling changed the composer draft");
    assert.equal(await textarea.evaluate((element) => document.activeElement === element), true, "toggling took focus away from the composer");
  });

  await scenario("S3", async () => {
    assert.equal(await tokenCount(), 1, "the running row is not expanded by default");
    await toggleRow();
    assert.equal(await tokenCount(), 0, "the manual collapse did not close the row");
    await waitFor(async () => /Reading note\.txt/i.test(await headerText()), 15_000, "the header never showed the progress description");
    assert.equal(await tokenCount(), 0, "a later progress update reopened the manually collapsed row");
    await toggleRow();
    assert.equal(await tokenCount(), 1, "the manual expand did not open the row");
  });

  // Every running-state assertion has now run inside the hold window: wait for claude's follow-up turn.
  await waitFollowUp();
  await expandProcessDetails();

  await scenario("R441b", async () => {
    assert.ok(listSeen, "R441 never saw the running list, so its disappearance would be vacuous");
    await waitFor(async () => (await subagentList().count()) === 0, 20_000, "the running list stayed after the subagent finished");
    await waitFor(async () => (await agentRow().count()) === 1, 5_000, "the Agent row left the conversation when the subagent finished");
  });

  await scenario("S3b", async () => {
    assert.equal(await tokenCount(), 1, "the finished row discarded the user's manual expansion");
  });

  await scenario("S4b", async () => {
    await waitStats();
    const header = await headerText();
    assert.doesNotMatch(header, /running/i, `the settled header still reports running: ${header}`);
    assert.match(header, /1\s*tools?/i, `the finished header shows no tool count: ${header}`);
    assert.match(header, /10[,.]?379|10\.4k/i, `the finished header shows no recorded token count: ${header}`);
  });

  await scenario("S7b", async () => {
    assert.equal(leaseWhileRunning?.subagentsActive, true, "the lease never reported the run while it was running, so the settled check would be vacuous");
    const settled = await getJson(leasePath(), { method: "POST" });
    assert.equal(typeof settled.body.subagentsActive, "boolean", "the lease reply carries no subagentsActive field");
    await waitFor(async () => {
      const idle = await getJson(leasePath(), { method: "POST" });
      return idle.body.subagentsActive === false;
    }, 30_000, "the lease still reports an unsettled run after the last follow-up turn");
  });

  await scenario("S8", async () => {
    const live = { token: await tokenCount(), prompt: await promptVisible() };
    assert.deepEqual(live, { token: 1, prompt: 1 }, "the live row did not show exactly one prompt and one text entry");
    await page.reload({ waitUntil: "domcontentloaded", timeout: 480_000 });
    await page.locator(".chat-input-textarea").waitFor({ state: "visible", timeout: 180_000 });
    assert.equal(await page.getByText("<task-notification>", { exact: false }).count(), 0, "the task notification rendered as a user message");
    await page.getByText("The subagent finished and reported", { exact: false }).first().waitFor({ timeout: 30_000 });
    await expandProcessDetails();
    if ((await tokenCount()) === 0) await toggleRow();
    assert.equal(await tokenCount(), 1, "the history row shows the subagent text zero times or more than once");
    assert.equal(await promptVisible(), 1, "the history body shows no subagent prompt");
    await page.getByRole("button", { name: /read/i }).first().waitFor({ state: "visible", timeout: 10_000 });
  });

  // the second session, its child started on the 60-subagent recording.
  writeFileSync(join(stubFixtureDir, "stream.jsonl"), manySubagentsStream());
  const many = await getJson("/api/agent/new", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ cwd: project, type: "ensure_session", harness: "claude", modelAlias: "haiku", model: "claude-haiku-5-5", claudePreset: "read-only" }),
  });
  assert.ok(many.status === 200 && typeof many.body.sessionId === "string", `POST /api/agent/new for the second session failed: ${many.status}`);
  manySessionId = many.body.sessionId;
  await waitFor(() => stubLaunches().find((entry) => entry.sessionId === manySessionId), 30_000, "no claude child was launched for the second session");
  await page.goto(`${base}/?session=${encodeURIComponent(manySessionId)}`, { waitUntil: "domcontentloaded", timeout: 480_000 });
  await page.locator(".chat-input-textarea").waitFor({ state: "visible", timeout: 180_000 });
  await page.locator(".chat-input-textarea").fill(PROMPT);
  await page.getByRole("button", { name: "Send", exact: true }).click();
  // Measured once the list is final: the recorded subagent has finished, the 60 run on.
  await waitFollowUp();
  await waitFor(async () => (await listItems().count()) === MANY, 30_000, `the running list never settled at the ${MANY} subagents still running`);

  await scenario("R482a", checkManyList);
  await scenario("R482b", checkManyPanelClose);
  await scenario("R482c", checkManyPanelEscape);
  await scenario("R485", checkPanelComposer);
  await scenario("R482d", async () => {
    await page.setViewportSize({ width: 390, height: 844 });
    await checkManyList();
    await checkManyPanelClose();
    await checkManyPanelEscape();
  });
  await scenario("R482e", async () => {
    await page.setViewportSize({ width: 390, height: 480 });
    await checkManyList();
    await checkManyPanelClose();
    await checkManyPanelEscape();
  });
  await scenario("R484a", checkPanelResizeMouse);
  await scenario("R484b", checkPanelResizeTouch);
  await scenario("R505a", checkAlignedSession);
  await scenario("R531a", checkComposerAligned);
} catch (error) {
  console.error(error);
  failures.push(`setup: ${error instanceof Error ? error.message : String(error)}`);
} finally {
  if (page) await page.screenshot({ path: join(artifacts, "claude-subagent-253.png") }).catch(() => {});
  await browser?.close().catch(() => {});
  await stopServer();
  rmSync(scratch, { recursive: true, force: true });
}

if (failures.length) {
  console.log(`FAIL claude-subagent-253: ${failures.length} scenario(s) failed`);
  process.exitCode = 1;
}
