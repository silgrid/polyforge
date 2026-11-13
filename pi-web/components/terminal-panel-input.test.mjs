import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

import { createTerminalPanelInput, insertedTerminalData } from "./terminal-panel-input.ts";

/**
 * Behavioral regression tests for the pi#84 terminal input bridge. Each test
 * drives the real bridge (its onData subscription and textarea listeners)
 * against a fake xterm target, so the event sequences below exercise the
 * same listener code the panel wires to a live xterm instance:
 *
 *  - `emitData` simulates what xterm itself delivered through its
 *    keydown/keypress/composition paths (terminal.onData). Compositions are
 *    delivered by the real CompositionHelper *after* compositionend, on a
 *    deferred timeout that reads the textarea's final value, so these tests
 *    emit the commit after compositionend — never before; the bridge's
 *    commit-echo guard expires on that same deferred turn, so tests that
 *    simulate xterm delivering *nothing* (a cancelled/empty composition)
 *    await one tick before asserting the guard is gone;
 *  - dispatching on the textarea simulates what the page sends to those
 *    listeners (bare `input` events, composition, key and blur lifecycle).
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
    /** What xterm's own key/keypress/composition paths delivered. */
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

function bareInsertText(text) {
  return inputEvent("insertText", text);
}

const bareEnter = () => inputEvent("insertLineBreak");

function lifecycle(type) {
  return new Event(type);
}

function setup() {
  const terminal = fakeTerminal();
  const sent = [];
  const bridge = createTerminalPanelInput(terminal, (data) => sent.push(data));
  return { terminal, bridge, sent };
}

test("insertion mapper maps shell input and ignores other input types", () => {
  assert.equal(insertedTerminalData({ inputType: "insertText", data: "ls" }), "ls");
  assert.equal(insertedTerminalData({ inputType: "insertText", data: "中" }), "中");
  assert.equal(insertedTerminalData({ inputType: "insertLineBreak", data: null }), "\r");
  assert.equal(insertedTerminalData({ inputType: "insertText", data: "" }), null);
  assert.equal(insertedTerminalData({ inputType: "insertText", data: null }), null);
  assert.equal(insertedTerminalData({ inputType: "insertCompositionText", data: "你" }), null);
  assert.equal(insertedTerminalData({ inputType: "insertFromPaste", data: "x" }), null);
  assert.equal(insertedTerminalData({ inputType: "deleteContentBackward", data: null }), null);
});

test("keystrokes typed before the shell exists are flushed in order, Enter included", () => {
  const { terminal, bridge, sent } = setup();
  // Physical keystroke during the settle window (xterm delivered via keydown).
  terminal.emitData("l");
  terminal.emitData("s");
  // Bare soft-keyboard Enter during the same window.
  terminal.textarea.dispatchEvent(bareEnter());
  assert.deepEqual(sent, [], "nothing may be sent before the server shell exists");
  bridge.markShellReady();
  // Enter reaches the writer as the CR the pty expects — the reported
  // pi#84 symptom was input doing nothing and Enter freezing the panel.
  // Held input flushes as one in-order write.
  assert.deepEqual(sent, ["ls\r"]);
});

test("physical key input is delivered exactly once (xterm path wins, insertion not doubled)", () => {
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  // In screenReaderMode xterm delivers keydown and does NOT cancel the event,
  // so the browser also fires a bare insertText the bridge must swallow.
  terminal.textarea.dispatchEvent(lifecycle("keydown"));
  terminal.emitData("a");
  terminal.textarea.dispatchEvent(bareInsertText("a"));
  terminal.textarea.dispatchEvent(lifecycle("keyup"));
  assert.deepEqual(sent, ["a"]);
});

test("arrow key followed later by bare text loses nothing (suppression is lifecycle-scoped)", () => {
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  // Arrow keys produce a keydown but no text insertion, so no input event
  // clears the old dedup flag. The next bare insertion must still arrive.
  terminal.textarea.dispatchEvent(lifecycle("keydown"));
  terminal.emitData("\x1b[A");
  terminal.textarea.dispatchEvent(lifecycle("keyup"));
  terminal.textarea.dispatchEvent(bareInsertText("x"));
  assert.deepEqual(sent, ["\x1b[A", "x"]);
});

test("IME commit is delivered exactly once: the commit echo is swallowed whatever it says", () => {
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  // Real xterm ordering: candidates during the composition, then
  // compositionend, then the commit echo (if the browser fires one), then
  // xterm's deferred delivery from the textarea's final value.
  terminal.textarea.dispatchEvent(lifecycle("compositionstart"));
  terminal.textarea.dispatchEvent(inputEvent("insertCompositionText", "ni"));
  // The last candidate differs from the committed text (Korean-style):
  // xterm delivers from the final textarea value, not from the candidates.
  terminal.textarea.dispatchEvent(inputEvent("insertCompositionText", "니"));
  terminal.textarea.dispatchEvent(lifecycle("compositionend"));
  assert.deepEqual(sent, []);
  // Commit echo fired after compositionend, carrying a final value that
  // matches no candidate. The old equality guard forwarded this and then
  // xterm delivered the same text again — exactly the double-send this
  // test forbids.
  terminal.textarea.dispatchEvent(bareInsertText("니가"));
  assert.deepEqual(sent, []);
  // xterm's CompositionHelper delivers the commit itself (deferred).
  terminal.emitData("니가");
  assert.deepEqual(sent, ["니가"], "the commit reaches the shell exactly once");
  // The suppression ended with xterm's delivery: identical bare text is new
  // input and must flow.
  terminal.textarea.dispatchEvent(bareInsertText("니가"));
  assert.deepEqual(sent, ["니가", "니가"]);
});

test("a keystroke typed between the commit echo and xterm's deferred delivery is not double-sent", () => {
  // Review-found ordering defect: compositionend commits "X"; the browser's
  // commit echo insertText("X") fires and is swallowed, but a genuine
  // keystroke "y" typed right after (before xterm's deferred read) used to
  // clear the guard on the echo alone and get forwarded immediately. xterm's
  // CompositionHelper then reads the *current* textarea value at delivery
  // time — which by then already includes "y" — and delivers "Xy" itself,
  // so the shell received "yXy" instead of "Xy". The guard must stay armed
  // for every insertion until xterm's own delivery (or the deferred-turn
  // timer) ends it, not just for the first one.
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  terminal.textarea.dispatchEvent(lifecycle("compositionstart"));
  terminal.textarea.dispatchEvent(inputEvent("insertCompositionText", "X"));
  terminal.textarea.dispatchEvent(lifecycle("compositionend"));
  // The commit echo some browsers fire.
  terminal.textarea.dispatchEvent(bareInsertText("X"));
  assert.deepEqual(sent, [], "the commit echo is swallowed");
  // A genuine keystroke typed before xterm's deferred read runs.
  terminal.textarea.dispatchEvent(bareInsertText("y"));
  assert.deepEqual(sent, [], "a keystroke inside the pending-delivery window must not be forwarded on its own");
  // xterm's deferred delivery reads the textarea's current value, which now
  // includes the keystroke typed above, and sends it as one commit.
  terminal.emitData("Xy");
  assert.deepEqual(sent, ["Xy"], "delivered exactly once, in order, never as \"yXy\"");
});

test("a finished IME commit does not swallow a later soft-keyboard Enter", () => {
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  terminal.textarea.dispatchEvent(lifecycle("compositionstart"));
  terminal.textarea.dispatchEvent(inputEvent("insertCompositionText", "你"));
  terminal.textarea.dispatchEvent(lifecycle("compositionend"));
  // xterm's deferred delivery of the commit.
  terminal.emitData("你");
  // A soft keyboard that fires no keydown delivers Enter as insertLineBreak.
  terminal.textarea.dispatchEvent(bareEnter());
  assert.deepEqual(sent, ["你", "\r"]);
});

test("commit-echo suppression cannot outlive the commit sequence", () => {
  // No commit echo ever fires and the deferred delivery carries nothing:
  // the guard still ends at keyup...
  const afterKeyup = setup();
  afterKeyup.bridge.markShellReady();
  afterKeyup.terminal.textarea.dispatchEvent(lifecycle("compositionstart"));
  afterKeyup.terminal.textarea.dispatchEvent(inputEvent("insertCompositionText", "你"));
  afterKeyup.terminal.textarea.dispatchEvent(lifecycle("compositionend"));
  afterKeyup.terminal.textarea.dispatchEvent(lifecycle("keyup"));
  afterKeyup.terminal.textarea.dispatchEvent(bareInsertText("你"));
  assert.deepEqual(afterKeyup.sent, ["你"], "identical bare text after keyup must flow");
  // ...and at blur, including after losing and regaining focus.
  const afterBlur = setup();
  afterBlur.bridge.markShellReady();
  afterBlur.terminal.textarea.dispatchEvent(lifecycle("compositionstart"));
  afterBlur.terminal.textarea.dispatchEvent(inputEvent("insertCompositionText", "你"));
  afterBlur.terminal.textarea.dispatchEvent(lifecycle("compositionend"));
  afterBlur.terminal.textarea.dispatchEvent(lifecycle("blur"));
  afterBlur.terminal.textarea.dispatchEvent(bareInsertText("你"));
  assert.deepEqual(afterBlur.sent, ["你"], "identical bare text after blur must flow");
});

test("an empty or cancelled composition cannot arm the echo guard past the deferred turn", async () => {
  // pi#84 review blocker: a cancelled composition makes xterm's
  // CompositionHelper deliver nothing (its deferred read of the textarea's
  // final value comes up empty, so no onData ever fires) and no commit echo
  // arrives either. The echo guard must expire on that same deferred turn,
  // not stay armed until the next insertion and swallow it.
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  terminal.textarea.dispatchEvent(lifecycle("compositionstart"));
  // Cancelled before any candidate landed: xterm's deferred read is empty.
  terminal.textarea.dispatchEvent(lifecycle("compositionend"));
  assert.deepEqual(sent, []);
  // The deferred turn the CompositionHelper would have delivered on.
  await new Promise((resolve) => setTimeout(resolve, 0));
  // The first genuine insertions after the cancelled composition — soft
  // keyboard text and Enter — must flow, not be swallowed by a stale guard.
  terminal.textarea.dispatchEvent(bareInsertText("你"));
  terminal.textarea.dispatchEvent(bareEnter());
  assert.deepEqual(sent, ["你", "\r"], "the echo guard expired with the deferred turn");
});

test("the deferred-turn expiry does not weaken echo suppression in the compositionend cascade", async () => {
  // A real commit echo is dispatched in the same event cascade as
  // compositionend, before the deferred turn: it must still be swallowed
  // exactly once, and the commit still delivered exactly once.
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  terminal.textarea.dispatchEvent(lifecycle("compositionstart"));
  terminal.textarea.dispatchEvent(inputEvent("insertCompositionText", "ni"));
  terminal.textarea.dispatchEvent(lifecycle("compositionend"));
  terminal.textarea.dispatchEvent(bareInsertText("你"));
  assert.deepEqual(sent, [], "the commit echo in the cascade is swallowed");
  // xterm's deferred delivery of the commit.
  terminal.emitData("你");
  assert.deepEqual(sent, ["你"]);
  await new Promise((resolve) => setTimeout(resolve, 0));
  terminal.textarea.dispatchEvent(bareInsertText("好"));
  assert.deepEqual(sent, ["你", "好"], "later bare text is unaffected");
});

test("an insertion before the deferred turn is still swallowed, including after an empty composition", async () => {
  // Between compositionend and the deferred turn the bridge cannot tell an
  // echo from a genuine insertion, and the echo lives exactly there — so the
  // conservative swallow applies; the guarantee is only that the guard is
  // gone by the end of the deferred turn.
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  terminal.textarea.dispatchEvent(lifecycle("compositionstart"));
  terminal.textarea.dispatchEvent(lifecycle("compositionend"));
  terminal.textarea.dispatchEvent(bareInsertText("x"));
  assert.deepEqual(sent, [], "insertions before the deferred turn are swallowed");
  await new Promise((resolve) => setTimeout(resolve, 0));
  terminal.textarea.dispatchEvent(bareInsertText("y"));
  assert.deepEqual(sent, ["y"], "the guard is gone by the end of the deferred turn");
});

test("keydown then blur does not swallow later bare insertions", () => {
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  terminal.textarea.dispatchEvent(lifecycle("keydown"));
  terminal.textarea.dispatchEvent(lifecycle("blur"));
  terminal.textarea.dispatchEvent(bareInsertText("h"));
  assert.deepEqual(sent, ["h"]);
});

test("typing plus Enter keeps working across an SSE-only interruption (stdin stays enabled)", () => {
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  // The output stream broke while the input path (writer) is still healthy.
  bridge.outputStreamInterrupted();
  assert.equal(terminal.options.disableStdin, false, "recoverable SSE errors must not disable stdin");
  terminal.textarea.dispatchEvent(bareInsertText("echo hi"));
  terminal.textarea.dispatchEvent(bareEnter());
  assert.deepEqual(sent, ["echo hi", "\r"]);
  bridge.outputStreamRecovered();
  assert.equal(terminal.options.disableStdin, false);
  // stdin only stays off for genuine input-path failures.
  bridge.markInputFailed();
  assert.equal(terminal.options.disableStdin, true);
  bridge.outputStreamRecovered();
  assert.equal(terminal.options.disableStdin, true, "recovery must not resurrect a failed writer");
});

test("page hide before the shell exists suspends the bridge itself and holds the startup flush", () => {
  const { terminal, bridge, sent } = setup();
  bridge.suspendStdin();
  assert.equal(terminal.options.disableStdin, true, "page hide / offline suspends stdin");
  terminal.emitData("l");
  terminal.textarea.dispatchEvent(bareInsertText("s"));
  terminal.textarea.dispatchEvent(bareEnter());
  assert.deepEqual(sent, [], "nothing may be sent while suspended");
  bridge.markShellReady();
  assert.deepEqual(sent, [], "the startup flush must be held while suspended");
  bridge.outputStreamRecovered();
  assert.equal(terminal.options.disableStdin, false, "reconnect re-enables stdin");
  assert.deepEqual(sent, ["ls\r"], "retained input flushes in order on recovery");
});

test("input dispatched while the page is hidden is retained, not sent", () => {
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  terminal.emitData("ec");
  bridge.suspendStdin();
  // Both delivery paths must respect the suspension: xterm's own keydown
  // delivery and the bare-input fallback.
  terminal.textarea.dispatchEvent(bareInsertText("ho"));
  terminal.emitData("!");
  assert.deepEqual(sent, ["ec"], "suspension gates the bridge's own forwarding");
  bridge.outputStreamRecovered();
  assert.deepEqual(sent, ["ec", "ho!"], "retained input flushes in order on recovery");
});

test("genuine exit, input failure, and page hide still disable stdin and stop input", () => {
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  bridge.suspendStdin();
  assert.equal(terminal.options.disableStdin, true, "page hide / offline suspends stdin");
  bridge.outputStreamRecovered();
  assert.equal(terminal.options.disableStdin, false, "reconnect re-enables stdin");
  terminal.textarea.dispatchEvent(bareInsertText("a"));
  assert.deepEqual(sent, ["a"]);
  bridge.markExited();
  assert.equal(terminal.options.disableStdin, true);
  terminal.textarea.dispatchEvent(bareInsertText("b"));
  terminal.emitData("c");
  assert.deepEqual(sent, ["a"], "exited terminals accept no further input");
  bridge.markInputFailed();
  assert.equal(terminal.options.disableStdin, true);
});

test("a failed shell start drops the startup buffer instead of replaying it", () => {
  const { terminal, bridge, sent } = setup();
  // Keystrokes typed while the panel settles, buffered by the bridge.
  terminal.emitData("danger");
  terminal.textarea.dispatchEvent(bareEnter());
  // The shell never came up (create/attach rejected): input stops outright
  // and the buffer is dropped — input typed around a dead start must never
  // be silently replayed into the fresh shell a later Reconnect spawns.
  bridge.markInputFailed();
  assert.equal(terminal.options.disableStdin, true, "a failed start disables stdin");
  // A late recovery (a stream that opens afterwards) must not replay it.
  bridge.outputStreamRecovered();
  assert.deepEqual(sent, [], "the startup buffer is dropped, never replayed");
  // Keystrokes after the failure are neither buffered nor sent.
  terminal.emitData("y");
  terminal.textarea.dispatchEvent(bareInsertText("z"));
  assert.deepEqual(sent, []);
});

test("dispose removes the textarea listeners and the onData subscription", () => {
  const { terminal, bridge, sent } = setup();
  bridge.markShellReady();
  bridge.dispose();
  terminal.textarea.dispatchEvent(bareInsertText("a"));
  terminal.emitData("b");
  assert.deepEqual(sent, []);
  bridge.dispose(); // idempotent
});
