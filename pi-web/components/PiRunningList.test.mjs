// carries: components/PiRunningList.tsx, hooks/useSubagentRuns.ts, components/ChatWindow.tsx, hooks/useBackgroundTasks*
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { PiRunningList } = await jiti.import("./PiRunningList.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
// The locale modules' import registers their message tables.
await jiti.import("@/lib/i18n/messages/en");
const { formatTurnDuration } = await jiti.import("@/lib/turn-duration");

const NOW = Date.UTC(2026, 10, 18, 12, 0, 0);

function subagent(fields = {}) {
  return {
    sessionId: "sub-1",
    parentSessionId: "parent-1",
    profile: "general-purpose",
    description: "Audit the parsers",
    status: "running",
    createdAt: new Date(NOW - 65_000).toISOString(),
    ...fields,
  };
}

function task(fields = {}) {
  return {
    id: "t-1",
    sessionId: "parent-1",
    command: "npm run build",
    cwd: "/tmp/project",
    status: "running",
    startedAt: NOW - 30_000,
    endedAt: null,
    exitCode: null,
    signal: null,
    reason: null,
    bytesWritten: null,
    logPath: "/tmp/log",
    notified: false,
    ...fields,
  };
}

function render({ subagents = [], tasks = [] } = {}) {
  return renderToStaticMarkup(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(PiRunningList, {
        subagents,
        tasks,
        onOpenSubagent: () => {},
        onOpenTask: () => {},
        onOpenHistory: () => {},
        now: NOW,
      }),
    ),
  );
}

function items(html) {
  return [...html.matchAll(/<button[^>]*data-pi-(subagent|task)-item="([^"]+)"[^>]*>([\s\S]*?)<\/button>/g)].map((match) => ({ kind: match[1], id: match[2], text: match[3].replace(/<[^>]+>/g, " ") }));
}

test("the list merges running subagents and tasks oldest first, each with its kind's looks and its own label", () => {
  const html = render({
    subagents: [subagent({ sessionId: "sub-new", createdAt: new Date(NOW - 5_000).toISOString() })],
    tasks: [task({ id: "t-old", startedAt: NOW - 30_000 })],
  });
  assert.match(html, /data-pi-running-list/, "the list names itself");
  const listed = items(html);
  // The owner's ruling: no grouping, one merged order, oldest first.
  assert.deepEqual(listed.map((item) => item.id), ["t-old", "sub-new"], JSON.stringify(listed));
  const [taskItem, subagentItem] = listed;
  assert.match(taskItem.text, /npm run build/, "the task button names its command");
  assert.match(taskItem.text, /Task/, "the task button carries its kind badge");
  assert.ok(taskItem.text.includes(formatTurnDuration(30_000)), `the task shows its elapsed time: ${taskItem.text}`);
  assert.match(subagentItem.text, /Audit the parsers/, "the subagent button names its description");
  assert.ok(subagentItem.text.includes(formatTurnDuration(5_000)), `the subagent shows its elapsed time: ${subagentItem.text}`);
  assert.doesNotMatch(subagentItem.text, /Task/, "a subagent button does not carry the task badge");
});

test("a queued subagent is listed, named Queued, after the older running ones", () => {
  const html = render({
    subagents: [
      subagent({ sessionId: "sub-run", createdAt: new Date(NOW - 20_000).toISOString() }),
      subagent({ sessionId: "sub-q", status: "queued", description: "", createdAt: new Date(NOW - 10_000).toISOString() }),
    ],
  });
  const listed = items(html);
  assert.deepEqual(listed.map((item) => item.id), ["sub-run", "sub-q"]);
  assert.match(listed[0].text, /Running/, "the running subagent shows it runs");
  assert.match(listed[1].text, /Queued/, "the queued subagent carries no Queued mark");
  // No description: the profile stands in.
  assert.match(listed[1].text, /general-purpose/, "the queued button shows no agent kind");
});

test("nothing renders while nothing runs and nothing settled; settled tasks leave only the 「已结束」 entry", () => {
  assert.equal(render(), "", "an idle session keeps its composer unshifted");
  assert.equal(render({ subagents: [], tasks: [] }), "");
  const settled = render({ tasks: [task({ id: "t-done", status: "succeeded", endedAt: NOW - 1_000, exitCode: 0 })] });
  // The settled task itself never lists (AC3: interrupted and friends stay
  // out of the running list); the entry to the history is the one thing left.
  const listed = items(settled);
  assert.deepEqual(listed, [], `a settled task appears as a running item: ${JSON.stringify(listed)}`);
  const entry = settled.match(/<button[^>]*data-pi-bg-history="[^"]*"/);
  assert.ok(entry, "the 「已结束」 entry did not render");
  assert.match(settled, /Finished/, "the entry carries its label");
  assert.doesNotMatch(render(), /data-pi-bg-history/, "an idle session shows the history entry");

  // A running item beside settled ones: both the item and the entry.
  const both = render({ tasks: [task({ id: "t-live" }), task({ id: "t-done", status: "stopped", endedAt: NOW - 1_000, reason: "stopped" })] });
  assert.deepEqual(items(both).map((item) => item.id), ["t-live"]);
  assert.match(both, /data-pi-bg-history/, "the 「已结束」 entry is missing while a task runs beside a settled one");
});

test("every button opens its own target: subagent items, task items and the history entry are buttons", () => {
  const html = render({
    subagents: [subagent()],
    tasks: [task(), task({ id: "t-done", status: "succeeded", endedAt: NOW - 1_000, exitCode: 0 })],
  });
  assert.match(html, /<button[^>]*data-pi-subagent-item="sub-1"/, "the subagent item is not a button");
  assert.match(html, /<button[^>]*data-pi-task-item="t-1"/, "the task item is not a button");
  // The entry renders because something settled; nothing settled, no entry.
  assert.match(html, /<button[^>]*data-pi-bg-history="/, "the history entry is not a button");
  assert.doesNotMatch(render({ subagents: [subagent()], tasks: [task()] }), /data-pi-bg-history/, "the entry shows with nothing settled");
  // The grid the Claude list established: equal-width columns from 260px.
  assert.match(html, /repeat\(auto-fill, ?minmax\(260px, ?1fr\)\)/, "the grid is not Claude's");
});
