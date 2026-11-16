// carries: components/ClaudeBackgroundTaskPanel.tsx
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { ClaudeBackgroundTaskPanel } = await jiti.import("./ClaudeBackgroundTaskPanel.tsx");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const NOW = Date.UTC(2026, 10, 14, 12, 0, 0);

function task(fields = {}) {
  return {
    taskId: "task-1",
    toolCallId: "toolu_task",
    kind: "shell",
    command: "npm test -- --watch",
    description: "Run the focused test suite",
    status: "running",
    startedAt: NOW - 9_000,
    ...fields,
  };
}

function render(fields = {}) {
  return renderToStaticMarkup(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(ClaudeBackgroundTaskPanel, {
        task: task(fields),
        onStop: async () => {},
        onReadOutput: async () => ({ output: "", totalBytes: 0, truncated: false }),
        onClose: () => {},
        now: NOW,
      }),
    ),
  );
}

/** The header's right column, from its opening marker to the output area. */
function sideColumn(html) {
  const start = html.indexOf('data-claude-task-side=""');
  assert.ok(start >= 0, `the header has no right column: ${html.slice(0, 400)}`);
  const end = html.indexOf('data-claude-task-output=""');
  assert.ok(end > start, "the right column runs into the output area");
  return html.slice(start, end);
}

test("running header puts close, stop and the ticking time in one right column with no status text", () => {
  const html = render();
  // No "Running" status text and no separate status line below the command.
  assert.doesNotMatch(html, /Running/, `the running panel shows a status text: ${html.slice(0, 400)}`);
  assert.doesNotMatch(html, /data-claude-task-meta/, "the running panel keeps a status line below the command");
  // One right column, after the command area: close, then stop, then the time.
  const command = html.indexOf('data-claude-task-command=""');
  const side = sideColumn(html);
  assert.ok(command >= 0 && command < html.indexOf('data-claude-task-side=""'), "the command area is not left of the right column");
  const close = side.indexOf('aria-label="Close"');
  const stop = side.indexOf('aria-label="Stop task"');
  const elapsed = side.indexOf('data-claude-task-elapsed=""');
  assert.ok(close >= 0 && stop > close && elapsed > stop, `the right column is not close, stop, then time: ${side}`);
  // The time is the elapsed 9s (startedAt 9s before the fixed now), in tabular numerals.
  assert.match(side.slice(elapsed), />9s</, "the right column shows no elapsed time");
  assert.match(side.slice(elapsed), /font-variant-numeric:tabular-nums/, "the elapsed time is not in tabular numerals");
  // Right-aligned column.
  assert.match(side.slice(0, elapsed), /align-items:flex-end/, "the right column is not right-aligned");
});

test("stop is an icon button whose accessible name and title are Stop task", () => {
  const html = render();
  assert.ok(html.includes('aria-label="Stop task"'), "the stop button has no accessible name");
  const start = html.lastIndexOf("<button", html.indexOf('aria-label="Stop task"'));
  const end = html.indexOf("</button>", start) + "</button>".length;
  const button = html.slice(start, end);
  assert.match(button, /aria-label="Stop task"/, "the accessible name is not on the stop button");
  assert.match(button, /title="Stop task"/, "the stop button has no title");
  assert.doesNotMatch(button, />Stop task</, "the stop button shows text instead of an icon");
  assert.match(button, /<svg/, "the stop button shows no icon");
});

test("an ended task hides stop and shows its final status and time under close in the right column", () => {
  const stopped = render({ status: "stopped", endedAt: NOW - 2_000 });
  assert.doesNotMatch(stopped, /aria-label="Stop task"/, "an ended task still offers stop");
  assert.match(stopped, /aria-label="Close"/, "close left the header");
  const side = sideColumn(stopped);
  const close = side.indexOf('aria-label="Close"');
  const statusLabel = side.indexOf('data-claude-task-status-label=""');
  assert.ok(statusLabel > close, `the final status is not below close: ${side}`);
  assert.match(side.slice(statusLabel), />Stopped</, "the ended panel shows no final status");
  assert.match(side, />2s</, "the ended panel shows no final duration");

  assert.match(render({ status: "completed", endedAt: NOW - 2_000 }), />Completed</, "a completed task shows no Completed status");

  const failed = render({ status: "failed", endedAt: NOW - 2_000 });
  assert.match(failed, />Failed</, "a failed task shows no Failed status");
  assert.match(failed, /data-claude-task-status-label="" style="[^"]*color:#f87171/, "a failed task's status is not red");
});
