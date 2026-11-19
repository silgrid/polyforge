// carries: lib/bg-tools.ts, lib/bg-protocol.ts
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { interopDefault: true, moduleCache: false });
const { createBgTasksExtension, BG_TOOL_NAMES } = await jiti.import("./bg-tools.ts");
const { utf8Truncate, BG_OUTPUT_MAX_BYTES } = await jiti.import("./bg-protocol.ts");

function makeToolContext(sessionId = "sess-1", cwd = "/tmp/w") {
  return {
    cwd,
    sessionManager: { getSessionId: () => sessionId },
  };
}

// Captures every registered tool definition, keyed by name.
function mountExtension(controller) {
  const registered = new Map();
  const extension = createBgTasksExtension(controller);
  extension.factory({
    registerTool: (definition) => registered.set(definition.name, definition),
  });
  return registered;
}

function makeInfo(overrides = {}) {
  return {
    id: "0123456789abcdef0123456789abcdef",
    sessionId: "sess-1",
    command: "echo hi",
    cwd: "/tmp/w",
    status: "running",
    startedAt: 1_700_000_000_000,
    endedAt: null,
    exitCode: null,
    signal: null,
    reason: null,
    bytesWritten: null,
    logPath: "/tmp/bg/0123456789abcdef0123456789abcdef/log",
    notified: false,
    ...overrides,
  };
}

function makeController(overrides = {}) {
  const calls = { run: [], status: [], readOutput: [], stop: [] };
  const controller = {
    calls,
    run(sessionId, cwd, command) {
      calls.run.push({ sessionId, cwd, command });
      return overrides.run?.({ sessionId, cwd, command }) ?? makeInfo();
    },
    status(sessionId, taskId) {
      calls.status.push({ sessionId, taskId });
      if (overrides.status) return overrides.status({ sessionId, taskId });
      throw new Error("unused");
    },
    readOutput(sessionId, taskId, maxBytes) {
      calls.readOutput.push({ sessionId, taskId, maxBytes });
      if (overrides.readOutput) return overrides.readOutput({ sessionId, taskId, maxBytes });
      throw new Error("unused");
    },
    stop(sessionId, taskId) {
      calls.stop.push({ sessionId, taskId });
      if (overrides.stop) return overrides.stop({ sessionId, taskId });
      throw new Error("unused");
    },
    beginSessionDeletion() {},
    async deleteSessionTasks() {},
    hasActiveWork() { return false; },
    setSink() {},
  };
  return controller;
}

async function runTool(registered, name, params, ctx = makeToolContext()) {
  const definition = registered.get(name);
  assert.ok(definition, `the ${name} tool must be registered`);
  const result = await definition.execute("call-1", params, undefined, undefined, ctx);
  return result;
}

const textOf = (result) => result.content.map((block) => block.type === "text" ? block.text : "").join("");

test("the factory registers exactly the four background-task tools", () => {
  const registered = mountExtension(makeController());
  assert.deepEqual([...registered.keys()].sort(), [...BG_TOOL_NAMES].sort());
});

test("bg_run starts a task in the session's cwd and answers with the id and the log path", async () => {
  const controller = makeController();
  const registered = mountExtension(controller);
  const result = await runTool(registered, "bg_run", { command: "echo hi" });
  assert.deepEqual(controller.calls.run, [{ sessionId: "sess-1", cwd: "/tmp/w", command: "echo hi" }]);
  const text = textOf(result);
  assert.ok(text.includes("0123456789abcdef"), "the answer names the task id");
  assert.ok(text.includes("/tmp/bg/0123456789abcdef0123456789abcdef/log"), "the answer names the log path");
});

test("bg_run refuses an empty command", async () => {
  const registered = mountExtension(makeController());
  await assert.rejects(() => runTool(registered, "bg_run", { command: "   " }), /command/i);
});

test("bg_status lists the session's tasks, or one when a task id names it", async () => {
  const controller = makeController({
    status: ({ taskId }) => taskId === "01234567"
      ? [makeInfo({ id: "01234567", status: "succeeded", exitCode: 0, endedAt: 1_700_000_005_000, bytesWritten: 6 })]
      : [makeInfo(), makeInfo({ id: "ffffffff", command: "sleep 1", status: "failed", exitCode: 1, endedAt: 1_700_000_002_000 })],
  });
  const registered = mountExtension(controller);
  const list = textOf(await runTool(registered, "bg_status", {}));
  assert.ok(list.includes("0123456789abcdef"), "the list names the first task");
  assert.ok(list.includes("running"), "the list names a running task");
  assert.ok(list.includes("failed"), "the list names a failed task");

  const one = textOf(await runTool(registered, "bg_status", { task_id: "01234567" }));
  assert.ok(one.includes("succeeded"), "one task's status is reported");
  assert.ok(!one.includes("ffffffff"), "no other task leaks into the answer");
  assert.deepEqual(controller.calls.status.at(-1), { sessionId: "sess-1", taskId: "01234567" });
});

test("bg_output answers with the byte range and the full log path, within the 8192-byte budget", async () => {
  // 15,000 bytes of 3-byte characters: the budget must cut on a character boundary.
  const huge = "世".repeat(5_000);
  const controller = makeController({
    readOutput: () => ({
      text: huge, startByte: 15_000 - huge.length * 0, endByte: 15_000,
      totalBytes: 15_000, logPath: "/tmp/bg/0123456789abcdef0123456789abcdef/log", truncated: true,
    }),
  });
  const registered = mountExtension(controller);
  const result = await runTool(registered, "bg_output", { task_id: "0123456789abcdef" });
  const text = textOf(result);
  assert.ok(Buffer.byteLength(text, "utf8") <= BG_OUTPUT_MAX_BYTES, `the whole answer must stay within ${BG_OUTPUT_MAX_BYTES} bytes`);
  assert.ok(!text.includes("\uFFFD"), "the cut must not split a multi-byte character");
  assert.ok(/bytes \d+-\d+ of 15000/.test(text), "the answer names the byte range");
  assert.ok(text.includes("/tmp/bg/0123456789abcdef0123456789abcdef/log"), "the answer names the full log path");
  assert.deepEqual(controller.calls.readOutput.at(-1), { sessionId: "sess-1", taskId: "0123456789abcdef", maxBytes: undefined });
});

test("bg_output passes a smaller max_bytes through as the excerpt's cap", async () => {
  const controller = makeController({
    readOutput: ({ maxBytes }) => ({
      text: "tail", startByte: 1, endByte: 5, totalBytes: 5,
      logPath: "/tmp/bg/x/log", truncated: true,
    }),
  });
  const registered = mountExtension(controller);
  const text = textOf(await runTool(registered, "bg_output", { task_id: "t1", max_bytes: 4096 }));
  assert.ok(text.includes("tail"));
  assert.deepEqual(controller.calls.readOutput.at(-1).maxBytes, 4096);
});

test("bg_stop stops a running task and answers idempotently for an already-terminal one", async () => {
  const controller = makeController({
    status: ({ taskId }) => taskId === "running-1"
      ? [makeInfo({ id: "running-1" })]
      : [makeInfo({ id: "done-1", status: "stopped", reason: "stopped", endedAt: 1_700_000_001_000 })],
    stop: () => makeInfo({ id: "running-1", status: "stopped", reason: "stopped", endedAt: 1_700_000_001_000 }),
  });
  const registered = mountExtension(controller);

  const stopped = textOf(await runTool(registered, "bg_stop", { task_id: "running-1" }));
  assert.ok(stopped.includes("running-1"), "the answer names the stopped task");
  assert.deepEqual(controller.calls.stop, [{ sessionId: "sess-1", taskId: "running-1" }]);

  // An already-terminal task: the answer says so, and no second stop is issued.
  const again = textOf(await runTool(registered, "bg_stop", { task_id: "done-1" }));
  assert.ok(/already/.test(again), "an already-terminal task answers as such");
  assert.deepEqual(controller.calls.stop, [{ sessionId: "sess-1", taskId: "running-1" }]);
});

test("a task of another session reads as not found through every tool", async () => {
  const controller = makeController({
    status: () => { throw new Error("Background task not found: t1"); },
    readOutput: () => { throw new Error("Background task not found: t1"); },
    stop: () => { throw new Error("Background task not found: t1"); },
  });
  const registered = mountExtension(controller);
  const other = makeToolContext("sess-2");
  await assert.rejects(() => runTool(registered, "bg_status", { task_id: "t1" }, other), /not found/i);
  await assert.rejects(() => runTool(registered, "bg_output", { task_id: "t1" }, other), /not found/i);
  await assert.rejects(() => runTool(registered, "bg_stop", { task_id: "t1" }, other), /not found/i);
});

test("utf8Truncate cuts on a character boundary (hand-computed table)", () => {
  assert.equal(utf8Truncate("hello", 100), "hello");
  assert.equal(utf8Truncate("hello", 3), "hel");
  // "é" is two bytes: h + é fills the three-byte budget exactly, "hél" needs four.
  assert.equal(utf8Truncate("héllo", 3), "hé");
  assert.equal(utf8Truncate("héllo", 4), "hél");
  // "世" is three bytes: four bytes hold one character, not one and a third.
  assert.equal(utf8Truncate("世界", 4), "世");
  assert.equal(utf8Truncate("世界", 2), "");
  assert.equal(Buffer.byteLength(utf8Truncate("世".repeat(4_000), 8_190), "utf8") <= 8_190, true);
  assert.ok(!utf8Truncate("世".repeat(4_000), 8_190).includes("\uFFFD"));
});
