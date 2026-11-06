import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

import { createTerminalPanelInput } from "./terminal-panel-input.ts";
import { createTerminalPanelStream } from "./terminal-panel-stream.ts";

/**
 * Regression tests for the terminal panel's stream/lifecycle controller
 * (pi#84 review): the shell-start continuation, the SSE stream and the page
 * hide/offline suspension interact here, against the REAL input bridge and a
 * fake xterm target (same shape as terminal-panel-input.test.mjs), so the
 * races below exercise the same listener code the panel wires to a live
 * xterm instance and a real EventSource.
 */

function fakeTerminal() {
  const textarea = new EventTarget();
  const dataListeners = new Set();
  const options = { disableStdin: false };
  return {
    textarea,
    options,
    onData(listener) {
      dataListeners.add(listener);
      return { dispose: () => dataListeners.delete(listener) };
    },
    emitData(data) {
      for (const listener of [...dataListeners]) listener(data);
    },
  };
}

function inputEvent(inputType, data) {
  const event = new Event("input");
  event.inputType = inputType;
  event.data = data === undefined ? null : data;
  return event;
}

const bareEnter = () => inputEvent("insertLineBreak", null);

class FakeEventSource {
  constructor(after) {
    this.after = after;
    this.readyState = 0;
    this.onopen = null;
    this.onmessage = null;
    this.onerror = null;
    this.closed = false;
  }
  close() {
    this.closed = true;
    this.readyState = 2;
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  emit(event) {
    this.onmessage?.({ data: JSON.stringify(event) });
  }
  fail() {
    this.onerror?.();
  }
}

function fakePanel({ start } = {}) {
  const terminal = fakeTerminal();
  const sent = [];
  const bridge = createTerminalPanelInput(terminal, (data) => sent.push(data));
  const sources = [];
  const listeners = new Map();
  const statuses = [];
  const errors = [];
  const exits = [];
  const outputs = [];
  const resets = [];
  const streamReadies = [];
  let online = true;
  const panel = {
    start: start ?? (async () => {}),
    input: bridge,
    isOnline: () => online,
    openStream(after) {
      const source = new FakeEventSource(after);
      sources.push(source);
      return source;
    },
    addWindowListener(type, handler) {
      listeners.set(type, handler);
    },
    removeWindowListener(type) {
      listeners.delete(type);
    },
    onOutput: (data) => outputs.push(data),
    onReset: () => resets.push(true),
    onStatus: (status) => statuses.push(status),
    onError: (reason) => errors.push(reason),
    onExited: (exitCode) => exits.push(exitCode),
    onStreamReady: () => streamReadies.push(true),
  };
  return {
    panel,
    terminal,
    bridge,
    sent,
    sources,
    listeners,
    statuses,
    errors,
    exits,
    outputs,
    resets,
    streamReadies,
    setOnline(value) {
      online = value;
    },
  };
}

test("failed startup disables stdin, drops the buffered input and surfaces the error", async () => {
  const fake = fakePanel({ start: async () => { throw new Error("shell create failed"); } });
  const stream = createTerminalPanelStream(fake.panel);
  // Keystrokes typed while the panel settles, before the failure is known.
  fake.terminal.emitData("ls");
  fake.terminal.textarea.dispatchEvent(bareEnter());
  await stream.started();
  assert.equal(fake.terminal.options.disableStdin, true, "a failed start disables stdin");
  assert.deepEqual(fake.sent, [], "the buffered input is dropped, not held for a replay");
  assert.deepEqual(fake.errors, ["shell create failed"]);
  assert.deepEqual(fake.statuses.slice(-1), ["error"]);
  assert.equal(fake.sources.length, 0, "no stream is opened for a shell that never came up");
  // Even a stream that opens afterwards must not replay the dropped buffer.
  fake.listeners.get("pageshow")({ persisted: true });
  assert.equal(fake.sources.length, 1);
  fake.sources[0].open();
  assert.equal(fake.terminal.options.disableStdin, true, "a failed writer's stdin stays disabled");
  assert.deepEqual(fake.sent, [], "nothing typed before the failure ever reaches a shell");
  // Reconnect: the old effect (bridge and buffer included) is disposed and a
  // fresh panel starts from an empty buffer — only new input flows.
  stream.dispose();
  fake.bridge.dispose();
  const revived = fakePanel({ start: async () => {} });
  const revivedStream = createTerminalPanelStream(revived.panel);
  await revivedStream.started();
  revived.sources[0].open();
  revived.terminal.emitData("n");
  assert.deepEqual(revived.sent, ["n"], "only input typed after the revive reaches the new shell");
  revivedStream.dispose();
});

test("a start that resolves after pagehide opens no stream and flushes nothing until the page returns", async () => {
  let releaseStart;
  const fake = fakePanel({ start: () => new Promise((resolve) => { releaseStart = resolve; }) });
  const stream = createTerminalPanelStream(fake.panel);
  // Input typed while the shell is still starting is buffered by the bridge.
  fake.terminal.emitData("l");
  fake.terminal.textarea.dispatchEvent(bareEnter());
  // bfcache pagehide: the page hides while the start request is in flight,
  // and navigator.onLine stays true, so only the suspension can gate it.
  fake.listeners.get("pagehide")();
  assert.equal(fake.terminal.options.disableStdin, true, "page hide suspends stdin");
  releaseStart();
  await stream.started();
  assert.equal(fake.sources.length, 0, "the startup continuation must not open a stream while the page is hidden");
  assert.ok(!fake.statuses.includes("ready"), "a hidden page never reports ready");
  assert.deepEqual(fake.sent, [], "nothing flushes while the page is hidden");
  // Returning to the page reconnects, and only then flushes in order.
  fake.listeners.get("pageshow")({ persisted: true });
  assert.equal(fake.sources.length, 1);
  fake.sources[0].open();
  assert.equal(fake.terminal.options.disableStdin, false, "returning re-enables stdin");
  assert.deepEqual(fake.sent, ["l\r"], "retained startup input flushes once, after the page is visible");
  assert.deepEqual(fake.statuses.slice(-1), ["ready"]);
  stream.dispose();
});

test("a stream that opens after pagehide cannot lift the suspension or re-enable stdin", async () => {
  const fake = fakePanel();
  const stream = createTerminalPanelStream(fake.panel);
  await stream.started();
  assert.equal(fake.sources.length, 1);
  fake.listeners.get("pagehide")();
  // Input arriving while suspended is retained by the bridge, never sent.
  fake.terminal.emitData("x");
  assert.deepEqual(fake.sent, []);
  // The stale stream's onopen fires after the hide (queued open, or a socket
  // that outlived the close): it must not recover, flush or report ready.
  fake.sources[0].open();
  assert.equal(fake.terminal.options.disableStdin, true, "a stale stream cannot re-enable stdin while hidden");
  assert.deepEqual(fake.sent, [], "retained input must not flush while the page is hidden");
  assert.ok(!fake.statuses.includes("ready"));
  assert.equal(fake.streamReadies.length, 0);
  // The page returns: a fresh stream recovers and flushes exactly once.
  fake.listeners.get("pageshow")({ persisted: true });
  assert.equal(fake.sources.length, 2);
  fake.sources[1].open();
  assert.deepEqual(fake.sent, ["x"], "retained input flushes when the page is visible again");
  stream.dispose();
});

test("offline suspends input; online resumes the stream and flushes retained input in order", async () => {
  const fake = fakePanel();
  const stream = createTerminalPanelStream(fake.panel);
  await stream.started();
  fake.sources[0].open();
  assert.equal(stream.canResize(), true, "resize flows while a healthy stream is attached");
  fake.setOnline(false);
  fake.listeners.get("offline")();
  assert.equal(stream.canResize(), false, "a suspended panel does not resize a dead shell");
  assert.equal(fake.terminal.options.disableStdin, true, "offline suspends stdin");
  fake.terminal.emitData("a");
  fake.terminal.emitData("b");
  assert.deepEqual(fake.sent, [], "nothing is sent while offline");
  fake.setOnline(true);
  fake.listeners.get("online")();
  assert.equal(fake.sources.length, 2, "online reconnects the output stream");
  fake.sources[1].open();
  assert.equal(fake.terminal.options.disableStdin, false);
  assert.deepEqual(fake.sent, ["ab"], "retained input flushes in order on reconnect");
  stream.dispose();
});

test("SSE errors interrupt the output stream without freezing input", async () => {
  const fake = fakePanel();
  const stream = createTerminalPanelStream(fake.panel);
  await stream.started();
  const source = fake.sources[0];
  source.readyState = 0; // CONNECTING: EventSource retries on its own.
  source.fail();
  assert.equal(fake.terminal.options.disableStdin, false, "recoverable SSE errors must not disable stdin");
  assert.deepEqual(fake.statuses.slice(-1), ["connecting"]);
  source.readyState = 2; // CLOSED: a dead stream surfaces as an error.
  source.fail();
  assert.equal(fake.terminal.options.disableStdin, false, "a broken output stream still leaves typing alone");
  assert.deepEqual(fake.statuses.slice(-1), ["error"]);
  stream.dispose();
});

test("output replay keeps offset and reset semantics, and exit closes the stream for good", async () => {
  const fake = fakePanel();
  const stream = createTerminalPanelStream(fake.panel);
  await stream.started();
  const source = fake.sources[0];
  source.emit({ type: "output", data: "a", offset: 5 });
  source.emit({ type: "output", data: "a", offset: 5 }); // duplicate offset: suppressed
  assert.deepEqual(fake.outputs, ["a"]);
  source.emit({ type: "output", data: "", offset: 6, reset: true });
  assert.deepEqual(fake.resets, [true]);
  source.emit({ type: "exit", exitCode: 0 });
  assert.deepEqual(fake.exits, [0]);
  assert.equal(source.closed, true, "exit closes the stream");
  assert.equal(fake.terminal.options.disableStdin, true, "exit disables stdin");
  // Reconnects open with the recorded offset, and an exited terminal never
  // reconnects at all.
  stream.dispose();
  const resumed = fakePanel();
  const resumedStream = createTerminalPanelStream(resumed.panel);
  await resumedStream.started();
  assert.equal(resumed.sources[0].after, undefined, "a fresh panel starts from the live tail");
  resumed.sources[0].open();
  resumed.sources[0].emit({ type: "output", data: "b", offset: 7 });
  resumed.sources[0].emit({ type: "output", data: "b", offset: 7 });
  resumed.listeners.get("pageshow")({ persisted: true });
  assert.equal(resumed.sources.length, 2);
  assert.equal(resumed.sources[1].after, 7, "reconnects resume from the recorded offset");
  resumedStream.dispose();
});
