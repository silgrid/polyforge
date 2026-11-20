// carries: hooks/useBackgroundTasks.ts, app/api/agent/[id]/bg-tasks/*
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { stripTypeScriptTypes } from "node:module";
import vm from "node:vm";

const source = await readFile(new URL("./useBackgroundTasks.ts", import.meta.url), "utf8");

test("terminal-notification dedupe fires exactly once per task id", () => {
  const context = vm.createContext({ Set, console });
  vm.runInContext(stripTypeScriptTypes(source.slice(source.indexOf("const notifiedTerminalTaskIds"), source.indexOf("export type BackgroundTasksFetchState")).replace(/export /g, "")), context);
  const first = context.markTerminalNotified("task-a");
  const second = context.markTerminalNotified("task-a");
  const other = context.markTerminalNotified("task-b");
  assert.equal(first, true);
  assert.equal(second, false);
  assert.equal(other, true);
});

test("the hook lists through the built-in registry interface, once per session", () => {
  // The registry answers for any session of this server; the client state is
  // the registry's own record list (BgTaskInfo), not the old bridge's shape.
  assert.match(source, /export type BackgroundTasksFetchState/);
  assert.match(source, /\{ kind: "ready"; tasks: BgTaskInfo\[\] \}/);
  assert.match(source, /\/api\/agent\/\$\{encodeURIComponent\(sessionId\)\}\/bg-tasks`/);
  assert.doesNotMatch(source, /fetchEnabled/, "the fetch gate died with the dock that needed it");
  assert.doesNotMatch(source, /"unavailable"/, "the built-in registry never answers 'package absent'");
});

test("a session switch drops the in-flight answer of the session before it", () => {
  assert.match(source, /if \(seq !== fetchSeq\.current\) return;/);
  // The refresh sequence invalidates stale answers across a session change;
  // the mount effect re-reads for the new session id alone.
  assert.match(source, /useEffect\(\(\) => \{\s*if \(!sessionId\) \{\s*setState\(\{ kind: "loading" \}\);\s*return;\s*\}\s*refresh\(\);\s*return \(\) => \{ fetchSeq\.current \+= 1; \};\s*\}, \[sessionId, refresh\]\);/);
});

test("live SSE events are change signals: both kinds re-read the interface, nothing else", () => {
  // The events keep the old bridge's snapshot shape (#172's contract); the
  // registry's own HTTP answer is the richer truth, so the hook refetches.
  assert.match(source, /const applyEvent = useCallback\(\(event: BackgroundTasksClientEvent\) => \{\s*if \(event\.type === "background_task_terminal" \|\| event\.type === "background_tasks_update"\) refresh\(\);\s*\}, \[refresh\]\);/);
});

test("logs and kill go through the built-in routes; logs carry the registry's own answer", () => {
  assert.match(source, /\/logs`/);
  assert.match(source, /BgTaskOutput/, "the log state is the registry's BgTaskOutput, not the old bridge's shape");
  assert.doesNotMatch(source, /BgTaskLogsResult/, "the old bridge's logs type survives");
  assert.match(source, /\/kill`,\s*\{ method: "POST" \}/);
  assert.match(source, /refresh\(\);\s*\}, \[sessionId, refresh\]\);/);
});
