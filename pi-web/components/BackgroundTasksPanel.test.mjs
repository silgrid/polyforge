import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./BackgroundTasksPanel.tsx", import.meta.url), "utf8");
const appShellSource = await readFile(new URL("./AppShell.tsx", import.meta.url), "utf8");

test("panel rows show name, status dot, time, bytes, exit code, and the agent marker", () => {
  assert.match(source, /task\.name \|\| task\.id/);
  assert.match(source, /STATUS_COLORS/);
  assert.match(source, /formatStartTime\(task\.startTime, locale\)/);
  assert.match(source, /formatBytes\(task\.bytesWritten\)/);
  assert.match(source, /typeof task\.exitCode === "number"/);
  assert.match(source, /task\.isAgent/);
});

test("kill is a two-step confirmation and errors are surfaced inline", () => {
  assert.match(source, /killPendingId === task\.id \? \(/);
  assert.match(source, /setKillPendingId\(task\.id\)/);
  assert.match(source, /killError/);
  // Only running tasks offer the kill affordance.
  assert.match(source, /task\.status === "running" && \(/);
});

test("the tail view renders ANSI safely and marks truncation", () => {
  assert.match(source, /<AnsiText text=\{logs\[selectedTaskId\]\.text\} \/>/);
  assert.match(source, /logs\[selectedTaskId\]\?\.truncated/);
  assert.match(source, /fontFamily: "var\(--font-mono, monospace\)"/);
});

test("empty, loading, and unavailable states are all explicit", () => {
  assert.match(source, /state\.kind === "loading" && \(/);
  assert.match(source, /state\.kind === "unavailable" && \(/);
  assert.match(source, /state\.kind === "ready" && tasks\.length === 0 && \(/);
});

test("every user-facing string goes through i18n", () => {
  const usedKeys = [...source.matchAll(/t\("([a-zA-Z.]+)"\)/g)].map((m) => m[1]);
  assert.ok(usedKeys.length >= 12, `expected many i18n keys, got ${usedKeys.length}`);
  for (const key of usedKeys) {
    assert.ok(!key.includes(" "), `i18n key must not contain spaces: ${key}`);
    assert.ok(key.startsWith("bgTasks.") || key.startsWith("common."), `unnamespaced key: ${key}`);
  }
});

test("AppShell wires the panel and the event callback; the top-bar badge is gone", () => {
  // pi#28: the panel follows the focused pane's session (activeBgSessionId),
  // never the classic selectedSession alone.
  assert.match(appShellSource, /useBackgroundTasks\(activeBgSessionId, bgPanelOpen\)/);
  assert.match(appShellSource, /resolveBackgroundTasksSessionId\(\{/);
  // Split view: every session pane feeds its own stream into the handler.
  assert.match(appShellSource, /onBackgroundTasksEvent=\{\(event\) => handleBackgroundTasksEvent\(sid, event\)\}/);
  // Classic layout: the single chat feeds with its own session id.
  assert.match(appShellSource, /onBackgroundTasksEvent=\{selectedSession \? \(event\) => handleBackgroundTasksEvent\(selectedSession\.id, event\) : undefined\}/);
  // Panel state only accepts events from the followed session; terminal
  // notifications stay session-wide.
  assert.match(appShellSource, /sourceSessionId === activeBgSessionIdRef\.current/);
  assert.match(appShellSource, /<BackgroundTasksPanel/);
  assert.match(appShellSource, /sessionId=\{activeBgSessionId\}/);
  assert.doesNotMatch(appShellSource, /<BackgroundTasksPanel\n\s*sessionId=\{selectedSession/,
    "the panel must not read selectedSession directly (pi#28)");
  // owner-intended change: the pi top bar's bg-tasks toggle and its
  // running-count badge are gone; the dock opens from the terminal
  // notification's jump only (pinned below).
  assert.doesNotMatch(appShellSource, /bgRunningCount/, "the top bar still renders a bg-tasks running-count badge");
  assert.doesNotMatch(appShellSource, /translate\("bgTasks\.open"\)/, "the top bar still renders the bg-tasks toggle");
  // Terminal events notify exactly once and jump to the task.
  assert.match(appShellSource, /markTerminalNotified\(task\.id\)/);
  assert.match(appShellSource, /tag: `pi-bg-task:\$\{task\.id\}`/);
  assert.match(appShellSource, /setBgSelectedTaskId\(task\.id\)/);
  // Desktop dock vs mobile drawer.
  assert.match(appShellSource, /isMobile \? \(\s*<>/);
  assert.match(appShellSource, /position: "absolute", top: 0, right: 0, bottom: 0, width: 340/);
});
