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

test("status and elapsed time are in a separate row below command and description", () => {
  const html = render();
  const command = html.indexOf('data-claude-task-command=""');
  const status = html.indexOf('data-claude-task-status-label=""');
  assert.ok(command >= 0, html);
  assert.ok(status > command, html);
  const commandRow = html.slice(command, status);
  assert.doesNotMatch(commandRow, />Running</);
  assert.doesNotMatch(commandRow, />9s</);
  assert.match(html.slice(status), /Running/);
  assert.match(html.slice(status), /9s/);
});

test("stop remains available only while running and close remains in the header actions", () => {
  const running = render();
  assert.match(running, /data-claude-task-status-label=""/);
  assert.match(running, />Stop<\/button>/);
  assert.match(running, /aria-label="Close"/);

  const stopped = render({ status: "stopped", endedAt: NOW - 2_000 });
  assert.doesNotMatch(stopped, />Stop<\/button>/);
  assert.match(stopped, /aria-label="Close"/);
});

test("status presentation keeps running pulse and failure color", () => {
  const running = render();
  assert.match(running, /data-claude-task-status-label="" class="animate-\[pulse_1\.5s_infinite\]"/);
  assert.match(running, /color:teal/);

  const failed = render({ status: "failed", endedAt: NOW - 2_000 });
  assert.doesNotMatch(failed, /animate-\[pulse_1\.5s_infinite\]/);
  assert.match(failed, /data-claude-task-status-label="" style="[^\"]*color:#f87171/);
});
