// carries: lib/claude-adapter.ts, lib/__fixtures__/claude-2.1.322/hooks/*
// AC5: with Settings › Claude's hook switch on, claude runs with
// --include-hook-events and reports its hooks as system hook_* frames. Pi Web shows them
// in a light form: one status line above the composer (pi's extension setStatus, one
// key) that each finished hook replaces and the turn's end clears, plus a warning
// notice for a failed hook. No message, no transcript entry.
import "#test-env";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { ClaudeStreamTranslator } = await jiti.import("./claude-adapter.ts");

const recording = (file) => readFileSync(new URL(`./__fixtures__/${file}`, import.meta.url), "utf8")
  .split("\n").filter(Boolean).map((line) => JSON.parse(line));

/** Every pushed frame's translation, in order, with the frame it came from. */
function translate(frames) {
  const translator = new ClaudeStreamTranslator(() => 1000);
  return frames.map((frame) => ({ frame, events: translator.push(frame).events }));
}

const uiRequests = (steps) => steps.flatMap(({ events }) => events).filter((event) => event.type === "extension_ui_request");
const isHookFrame = (frame) => frame.type === "system" && typeof frame.subtype === "string" && frame.subtype.startsWith("hook_");

test("the 2.1.322 hook recording: each finished hook replaces one status line naming its event and hook, the turn's end clears it, and a failed hook is a warning notice", () => {
  const frames = recording("claude-2.1.322/hooks/stream.jsonl");
  const steps = translate(frames);
  const requests = uiRequests(steps);

  // One status key: each hook_response replaces the line, the result clears it.
  const statuses = requests.filter((request) => request.method === "setStatus");
  assert.ok(statuses.length > 0, "no hook status line");
  assert.ok(statuses.every((request) => request.statusKey === "claude-hooks"), "one status key for every hook");
  assert.deepEqual(statuses.map((request) => request.statusText), [
    "hook: SessionStart · SessionStart:startup",
    "hook: PreToolUse · PreToolUse:Read",
    "hook: PreToolUse · PreToolUse:Read · failed",
    "hook: PostToolUse · PostToolUse:Read",
    undefined,
  ]);
  // Each status comes from a hook_response, the clearing one from the turn's result.
  const statusFrames = steps.filter(({ events }) => events.some((event) => event.type === "extension_ui_request" && event.method === "setStatus"));
  assert.deepEqual(statusFrames.map(({ frame }) => frame.subtype ?? frame.type), ["hook_response", "hook_response", "hook_response", "hook_response", "success"]);
  assert.equal(statusFrames.at(-1).frame.type, "result");
  assert.ok(statusFrames.at(-1).events.some((event) => event.type === "agent_end"), "the line clears with the turn's end");

  // The failed hook (exit 1) is one warning notice naming the hook, its exit code and its stderr.
  const notices = requests.filter((request) => request.method === "notify");
  assert.equal(notices.length, 1);
  assert.equal(notices[0].notifyType, "warning");
  assert.match(notices[0].message, /PreToolUse:Read/);
  assert.match(notices[0].message, /exit 1/);
  assert.match(notices[0].message, /failing-hook-says-no/);
  const failed = steps.find(({ events }) => events.includes(notices[0]));
  assert.equal(failed.frame.outcome, "error");

  // Every request has its own id (the page's dialog queue and reconnect key on it).
  const ids = requests.map((request) => request.id);
  assert.ok(ids.every((id) => typeof id === "string" && id), "every request has an id");
  assert.equal(new Set(ids).size, ids.length, "ids are unique");

  // Nothing else: the same stream without its hook frames translates to the same events.
  const without = translate(frames.filter((frame) => !isHookFrame(frame))).flatMap(({ events }) => events);
  const rest = steps.flatMap(({ events }) => events).filter((event) => event.type !== "extension_ui_request");
  assert.deepEqual(rest, without);

  // A cancelled hook names its outcome on the line and is no warning.
  const cancelled = new ClaudeStreamTranslator(() => 1000).push({
    type: "system", subtype: "hook_response", hook_id: "h-c", hook_name: "PreToolUse:Bash", hook_event: "PreToolUse",
    output: "", stdout: "", stderr: "", outcome: "cancelled", uuid: "u-c", session_id: "s",
  }).events;
  assert.deepEqual(cancelled.map((event) => [event.method, event.statusKey, event.statusText]), [["setStatus", "claude-hooks", "hook: PreToolUse · PreToolUse:Bash · cancelled"]]);
});

test("hook_started and hook_progress alone add nothing, and a stream without hook frames translates as before", () => {
  const translator = new ClaudeStreamTranslator(() => 1000);
  assert.deepEqual(translator.push({ type: "system", subtype: "hook_started", hook_id: "h-1", hook_name: "SessionStart:startup", hook_event: "SessionStart", uuid: "u-1", session_id: "s" }).events, []);
  assert.deepEqual(translator.push({
    type: "system", subtype: "hook_progress", hook_id: "h-1", hook_name: "SessionStart:startup", hook_event: "SessionStart",
    stdout: "partial", stderr: "", output: "partial", uuid: "u-2", session_id: "s",
  }).events, []);
  // No finished hook: the turn's end clears nothing.
  const end = translator.push({ type: "result", subtype: "success", is_error: false, result: "", usage: {} }).events;
  assert.deepEqual(end.filter((event) => event.type === "extension_ui_request"), []);

  // Without --include-hook-events claude sends no hook frame: every recorded stream
  // translates with no status line or notice at all.
  for (const file of [
    "claude-2.1.312/stream.jsonl",
    "claude-2.1.322/subagent-forward/stream.jsonl",
    "claude-2.1.322/relay/stream.jsonl",
    "claude-2.1.322/edit/stream.jsonl",
  ]) {
    const frames = recording(file);
    assert.ok(!frames.some(isHookFrame), `${file} has hook frames`);
    assert.deepEqual(uiRequests(translate(frames)), [], file);
  }
});
