// carries: components/PiBackgroundTaskPanel.tsx, hooks/useBackgroundTasks.ts, components/ChatWindow.tsx
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { PiBackgroundTaskPanel, PiBackgroundTaskHistory } = await jiti.import("./PiBackgroundTaskPanel.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
// The locale modules' import registers their message tables.
await jiti.import("@/lib/i18n/messages/en");
const { formatTurnDuration } = await jiti.import("@/lib/turn-duration");

const NOW = Date.UTC(2026, 10, 16, 12, 0, 0);

/** A registry record (lib/bg-protocol's BgTaskInfo), hand-made for the panel. */
function task(fields = {}) {
  return {
    id: "t-11111111",
    sessionId: "session-a",
    command: "npm run build",
    cwd: "/tmp/project",
    status: "running",
    startedAt: NOW - 9_000,
    endedAt: null,
    exitCode: null,
    signal: null,
    reason: null,
    bytesWritten: 2048,
    logPath: "/tmp/log",
    notified: false,
    ...fields,
  };
}

function renderPanel(fields = {}, props = {}) {
  return renderToStaticMarkup(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(PiBackgroundTaskPanel, {
        task: task(fields),
        onStop: async () => {},
        onReadOutput: async () => ({ text: "", startByte: 0, endByte: 0, totalBytes: 0, logPath: "/tmp/log", truncated: false, tail: true }),
        onClose: () => {},
        now: NOW,
        ...props,
      }),
    ),
  );
}

function renderHistory(tasks) {
  return renderToStaticMarkup(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(PiBackgroundTaskHistory, { tasks, onOpenTask: () => {}, onClose: () => {}, now: NOW }),
    ),
  );
}

/** The header's right column, from its opening marker to the output area. */
function sideColumn(html) {
  const start = html.indexOf('data-pi-task-side=""');
  assert.ok(start >= 0, `the header has no right column: ${html.slice(0, 400)}`);
  const end = html.indexOf('data-pi-task-output=""');
  assert.ok(end > start, "the right column runs into the output area");
  return html.slice(start, end);
}

test("a running task's panel holds close, stop and the ticking time in one right column, with no status text", () => {
  const html = renderPanel();
  assert.match(html, /data-pi-task-panel="t-11111111"/, "the panel names its task");
  assert.match(html, /data-pi-task-status="running"/);
  // No "Running" status text: the stop button is the state (polyforge#69's rule).
  assert.doesNotMatch(html, /Running/, `the running panel shows a status text: ${html.slice(0, 400)}`);
  const command = html.indexOf('data-pi-task-command=""');
  const side = sideColumn(html);
  assert.ok(command >= 0 && command < html.indexOf('data-pi-task-side=""'), "the command area is not left of the right column");
  const close = side.indexOf('aria-label="Close"');
  const stop = side.indexOf('aria-label="Stop task"');
  const elapsed = side.indexOf('data-pi-task-elapsed=""');
  assert.ok(close >= 0 && stop > close && elapsed > stop, `the right column is not close, stop, then time: ${side}`);
  // The time is the elapsed 9s (startedAt 9s before the fixed now).
  assert.match(side.slice(elapsed), new RegExp(`>${formatTurnDuration(9_000).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}<`), "the right column shows no elapsed time");
  assert.match(side.slice(elapsed), /font-variant-numeric:tabular-nums/, "the elapsed time is not in tabular numerals");
  assert.match(side.slice(0, elapsed), /align-items:flex-end/, "the right column is not right-aligned");
  // The whole command is in the header, in the mono font.
  assert.match(html.slice(command), /npm run build/, "the header lost the command");
  assert.match(html, /font-family:var\(--font-mono\)/, "the command is not in the mono font");
});

test("a settled task swaps the stop button for its final status, and a stopped one says Stopped", () => {
  const done = renderPanel({ status: "succeeded", endedAt: NOW - 1_000, exitCode: 0 });
  assert.match(done, /data-pi-task-status="succeeded"/);
  const side = sideColumn(done);
  assert.ok(!side.includes('aria-label="Stop task"'), "a settled task still offers Stop");
  assert.match(side, /Completed/, "the settled panel shows no final status");
  assert.doesNotMatch(done, /data-pi-task-elapsed/, "a settled task shows no live ticking time");

  const stopped = renderPanel({ status: "stopped", endedAt: NOW - 1_000, reason: "stopped" });
  assert.match(sideColumn(stopped), /Stopped/, "the stopped panel shows no final status");
  assert.ok(!sideColumn(stopped).includes('aria-label="Stop task"'), "a stopped task still offers Stop");

  const failed = renderPanel({ status: "failed", endedAt: NOW - 1_000, exitCode: 2 });
  assert.match(sideColumn(failed), /Failed/, "the failed panel shows no final status");
  assert.match(sideColumn(failed), /color:#f87171/, "the failed status is not red");
});

test("the history panel lists the session's settled tasks, interrupted named and newest first, and no running one", () => {
  const html = renderHistory([
    task({ id: "t-early", status: "succeeded", startedAt: NOW - 60_000, endedAt: NOW - 50_000, exitCode: 0 }),
    task({ id: "t-orphan", status: "interrupted", startedAt: NOW - 40_000, endedAt: NOW - 30_000 }),
    task({ id: "t-live", status: "running" }),
  ]);
  assert.match(html, /data-pi-bg-history-panel/, "the history panel names itself");
  assert.doesNotMatch(html, /t-live/, "a running task appears in the history");
  const early = html.indexOf('data-pi-history-task="t-early"');
  const orphan = html.indexOf('data-pi-history-task="t-orphan"');
  assert.ok(early >= 0 && orphan >= 0, "the settled rows are missing");
  assert.ok(orphan < early, "the history is not newest ended first");
  const orphanRow = html.slice(orphan, early);
  assert.match(orphanRow, /Interrupted/, `the interrupted row does not say so: ${orphanRow}`);
  const earlyRow = html.slice(early);
  assert.match(earlyRow, /Completed/, "the succeeded row shows no status");
  // Every row is a button that opens that task's detail.
  assert.match(html, /<button[^>]*data-pi-history-task="t-orphan"/, "the history row is not an opening button");
  assert.match(html, /<button[^>]*data-pi-history-task="t-early"/, "the succeeded row is not an opening button");
  assert.doesNotMatch(html, /data-pi-task-panel/, "the history panel renders no task detail");
});

test("an empty history states it and renders no rows", () => {
  const html = renderHistory([]);
  assert.match(html, /data-pi-bg-history-panel/);
  assert.doesNotMatch(html, /data-pi-history-task/, "empty history renders rows");
  assert.match(html, /No finished background tasks/, "the empty state is missing");
});

test("the detail panel offers the settled history from its header", () => {
  // polyforge#189: the running area no longer carries the history entry; the
  // detail panel's header button is the settled history's entry (the owner's
  // B ruling on Q2's kept piHistory state).
  const withEntry = renderPanel({}, { onOpenHistory() {} });
  assert.match(withEntry, /<button[^>]*data-pi-bg-history-open=""/, "the header's history button is missing");
  const side = sideColumn(withEntry);
  const close = side.indexOf('aria-label="Close"');
  const history = side.indexOf('data-pi-bg-history-open=""');
  assert.ok(close >= 0 && history > close, "the history button does not sit in the right column beside Close");
  assert.match(side.slice(history, history + 300), /aria-label="Finished background tasks"/, "the history button is not named");
  // A caller that passes no callback gets no button.
  assert.doesNotMatch(renderPanel(), /data-pi-bg-history-open/, "the panel shows a history button with no callback");
});

test("a settled task's panel keeps reading its output once it ends, and a truncated tail says so", () => {
  const html = renderPanel({ status: "succeeded", endedAt: NOW - 1_000, exitCode: 0 });
  // renderToStaticMarkup runs no effects: the output area shows its loading
  // word, never a fabricated tail.
  assert.match(html, /data-pi-task-output=""/);
  assert.match(html, /Reading output/, "the output area shows no loading state");
  assert.doesNotMatch(html, /truncated/i, "the static render claims a truncation it never read");
});
