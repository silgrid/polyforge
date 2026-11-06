#!/usr/bin/env node
// A `claude` stand-in for lib/claude-session.test.mjs. Put on
// PATH as `claude`, it records each launch (argv, environment variable NAMES,
// never values) and every stdin line to $PI_TEST_CLAUDE_STUB_LOG, and answers
// like claude 2.1.312. Never touches a network.
//
//   PI_TEST_CLAUDE_STUB_MODE  replay (default): each user message replays the
//                             recorded stream.jsonl and writes its transcript;
//                             hang: a reply that streams one delta and waits for
//                             the interrupt; deaf: like hang, interrupt ignored;
//                             exit: exits (code 3) on the first user message;
//                             subagent: replays the background-Agent recording
//                             (subagent/stream.jsonl), holding
//                             PI_TEST_CLAUDE_STUB_HOLD_MS after the subagent's text;
//                             subagent-early: the same with the parent turn's
//                             `result` moved before the subagent's activity (a
//                             synthetic order: the parent answered while the
//                             subagent still runs, which the real stdin recording
//                             cannot show).
//                             switch | effort | compact: each
//                             stdin line replays the next segment of the
//                             model-switch/, effort/ or compact/ recording (a user
//                             line: up to its `result`; a control request: its
//                             `control_response` under this request's id), the
//                             session id and cwd made this child's; past the
//                             recording a line gets the replay answers below.
//   In replay / subagent / exit modes a `set_model` control request
//   is answered as 2.1.312 answered it (a model name: success with the model; any
//   other: the recorded `check_failed` refusal), `apply_flag_settings` with success,
//   `background_tasks` with `{ backgrounded: true }` as 2.1.322 answered it,
//   and a user line that is exactly `/compact` replays the compact recording's
//   compaction turn (its transcript boundary appended before the stream). hang and
//   deaf leave every control request but interrupt unanswered.
//   absorbed-notification | steer-absorbed | steer-queued | idle-interrupt:
//                             the claude 2.1.319 recordings of that name
//                             (claude-2.1.319/<name>/), replayed stdin line by stdin line:
//                             the n-th stdin line of the recording's kind (a user line,
//                             an interrupt) emits its segment — the recorded stdout
//                             from the first frame that line caused (its `queued`
//                             lifecycle; an interrupt's first `task_updated killed` or its
//                             `control_response`) up to the next line's — after
//                             appending its transcript chunk (from the line's user
//                             entry or `queued_command` attachment). Session id, cwd and
//                             the recorded uuids / request ids are made this child's.
//                             An interrupt past the recording is answered as claude
//                             answered one while idle (idle-interrupt/:
//                             `still_queued: []`). PI_TEST_CLAUDE_STUB_PACE_MS spaces
//                             the emitted lines (default 0).
//   permission: synthetic frames in claude 2.1.322's permission
//                             shapes (no recording). A user line starts a turn: init and
//                             an assistant tool_use — AskUserQuestion (one single-select
//                             and one multi-select question) when the text starts with
//                             `ask:`, else Bash with the text as its command. Launched with
//                             `--permission-prompt-tool stdio`, it sends `control_request`
//                             `can_use_tool` (request id `perm-<n>`, Bash with one
//                             localSettings suggestion) and waits for the `control_response`
//                             naming it: allow → the tool_result (the command, or the
//                             answers), the reply "allowed" and a result; deny → an error
//                             tool_result, "denied" and a result. Launched with
//                             `--permission-prompts none` it sends `system` /
//                             `permission_denied` (decision_reason_type `mode`), an error
//                             tool_result, "denied" and a result. An interrupt while a
//                             request waits withdraws it (`control_cancel_request`), then
//                             ends the turn as stopped.
//   permission-silent-stop: the permission mode, except that an interrupt
//                             ends the turn without withdrawing the waiting request (a claude
//                             that sends no `control_cancel_request` on Stop).
//   PI_TEST_CLAUDE_STUB_GRANDCHILD=1  starts a long sleep child and logs its pid.
//   PI_TEST_CLAUDE_STUB_REFUSE_PERMISSION_MODE=1  answers every set_permission_mode with
//                             claude 2.1.322's recorded invalid_mode refusal.
//   In every mode (hang and deaf included) an `initialize` control request is answered with
//   claude 2.1.322's recorded answer (claude-2.1.322/initialize/,) under this
//   request's id; PI_TEST_CLAUDE_STUB_VERSION replaces its `claude_code_version`. A launch
//   with `--no-session-persistence` and no session (Pi Web's probe, lib/claude-probe.ts) is
//   logged as `kind: "probe"`, never `launch`, so a session's launch count is its own.
//   A `set_model` takes any name `--model` takes (no fixed list) but
//   `claude-bogus-0-0`, the name the 2.1.312 model-switch recording shows claude refusing.
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { appendFileSync, mkdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const recordings = join(here, "..", "claude-2.1.312");
const argv = process.argv.slice(2);
const valueOf = (flag) => (argv.includes(flag) ? argv[argv.indexOf(flag) + 1] : undefined);
const sessionId = valueOf("--resume") ?? valueOf("--session-id") ?? "no-id";
const mode = process.env.PI_TEST_CLAUDE_STUB_MODE || "replay";
const log = (entry) => {
  if (process.env.PI_TEST_CLAUDE_STUB_LOG) appendFileSync(process.env.PI_TEST_CLAUDE_STUB_LOG, `${JSON.stringify({ pid: process.pid, ...entry })}\n`);
};
const emit = (event) => process.stdout.write(`${JSON.stringify(event)}\n`);
// As claude: the inherited CLAUDE_CONFIG_DIR, else ~/.claude of the child's HOME.
const configDir = process.env.CLAUDE_CONFIG_DIR || join(process.env.HOME ?? "", ".claude");
const withId = (line) => line.replaceAll("5b203fcd-56c9-4d0c-b700-749b82c41afb", sessionId);
const transcriptFile = () => join(configDir, "projects", process.cwd().replace(/[^A-Za-z0-9]/g, "-"), `${sessionId}.jsonl`);
const appendTranscript = (text) => {
  mkdirSync(dirname(transcriptFile()), { recursive: true });
  appendFileSync(transcriptFile(), text);
};
// A name `--model` takes (lib/claude-shared.ts isClaudeModelName).
const isModelName = (value) => typeof value === "string" && value.length <= 128 && /^[A-Za-z0-9][A-Za-z0-9._:@/[\]-]*$/.test(value);
const probing = argv.includes("--no-session-persistence") && !argv.includes("--session-id") && !argv.includes("--resume");
/** claude 2.1.322's recorded `initialize` answer under `requestId`; an error when the recording is not beside this stub. */
function initializeAnswer(requestId) {
  try {
    const frames = readFileSync(join(here, "..", "claude-2.1.322", "initialize", "stream.jsonl"), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
    const answer = frames.find((frame) => frame.type === "control_response");
    const response = { ...answer.response.response };
    if (process.env.PI_TEST_CLAUDE_STUB_VERSION) response.claude_code_version = process.env.PI_TEST_CLAUDE_STUB_VERSION;
    return { type: "control_response", response: { ...answer.response, request_id: requestId, response } };
  } catch (error) {
    return { type: "control_response", response: { subtype: "error", request_id: requestId, error: `no initialize recording: ${error.message}` } };
  }
}
let model = valueOf("--model") ?? "claude-default";
// As claude 2.1.322 answers `set_permission_mode` (lib/__fixtures__/claude-2.1.322/permission-mode/,
//): the mode back on success, a status frame when it changed, claude's
// invalid_mode text otherwise. PI_TEST_CLAUDE_STUB_REFUSE_PERMISSION_MODE=1 refuses every mode.
const PERMISSION_MODES = ["acceptEdits", "auto", "bypassPermissions", "default", "dontAsk", "plan"];
const INVALID_PERMISSION_MODE = "Cannot set permission mode: must be one of acceptEdits, auto, bypassPermissions, default, dontAsk, plan";
let permissionMode = valueOf("--permission-mode") ?? "default";

// The recordings: session id and cwd made this child's, then cut into segments.
const RECORDED = {
  switch: ["model-switch", "27200000-0000-4000-8000-000000000001", "/tmp/claude-switch-rec"],
  effort: ["effort", "27200000-0000-4000-8000-000000000002", "/tmp/claude-effort-rec"],
  compact: ["compact", "27200000-0000-4000-8000-000000000003", "/tmp/claude-compact-rec"],
};
function recorded(name, file) {
  const [dir, id, cwd] = RECORDED[name];
  const encoded = (path) => path.replace(/[^A-Za-z0-9]/g, "-");
  return readFileSync(join(recordings, dir, file), "utf8")
    .replaceAll(id, sessionId).replaceAll(encoded(cwd), encoded(process.cwd())).replaceAll(cwd, process.cwd())
    .split("\n").filter(Boolean).map((line) => JSON.parse(line));
}
function segmentsOf(name) {
  const turns = [];
  const controls = [];
  let current = [];
  for (const event of recorded(name, "stream.jsonl")) {
    if (event.type === "control_response") {
      controls.push(event);
      continue;
    }
    current.push(event);
    if (event.type === "result") {
      turns.push(current);
      current = [];
    }
  }
  return { turns, controls };
}
const segments = RECORDED[mode] ? segmentsOf(mode) : null;

// The recordings (claude 2.1.319), cut at the stdin lines that caused each part.
const RECORDED_319 = {
  "absorbed-notification": "39500000-0000-4000-8000-000000000001",
  "steer-absorbed": "39500000-0000-4000-8000-000000000021",
  "steer-queued": "39500000-0000-4000-8000-000000000022",
  "idle-interrupt": "39500000-0000-4000-8000-000000000003",
};
const lineKind = (line) => (line.type === "control_request" ? line.request?.subtype : line.type);
function recording319(name) {
  const dir = join(here, "..", "claude-2.1.319", name);
  const lines = (file) => readFileSync(join(dir, file), "utf8").split("\n").filter(Boolean);
  const stdin = lines("stdin.jsonl").map((line) => JSON.parse(line));
  const stream = lines("stream.jsonl");
  const events = stream.map((line) => JSON.parse(line));
  const transcript = lines("transcript.jsonl");
  const entries = transcript.map((line) => JSON.parse(line));
  let from = 0;
  const starts = stdin.map((line, k) => {
    if (k === 0) return 0;
    const at = events.findIndex((event, index) => index >= from && (line.type === "user"
      ? event.type === "command_lifecycle" && event.state === "queued" && event.command_uuid === line.uuid
      : (event.type === "control_response" && event.response?.request_id === line.request_id)
        || (event.type === "system" && event.subtype === "task_updated" && event.patch?.status === "killed")));
    from = at < 0 ? events.length : at;
    return from;
  });
  const chunkStarts = stdin.map((line, k) => (k === 0 ? 0 : line.type !== "user" ? -1
    : entries.findIndex((entry) => (entry.type === "user" && entry.uuid === line.uuid) || (entry.type === "attachment" && entry.attachment?.source_uuid === line.uuid))));
  const segments = stdin.map((line, k) => {
    const nextChunk = chunkStarts.slice(k + 1).find((start) => start > 0) ?? entries.length;
    return {
      kind: lineKind(line),
      line,
      stream: stream.slice(starts[k], k + 1 < stdin.length ? starts[k + 1] : stream.length),
      transcript: chunkStarts[k] < 0 ? [] : transcript.slice(chunkStarts[k], nextChunk),
    };
  });
  return { recordedId: RECORDED_319[name], cwd: `/tmp/claude-rec-395-${name}`, segments, ids: new Map() };
}
const turns319 = Object.hasOwn(RECORDED_319, mode) ? recording319(mode) : null;
/** A recorded line made this child's: session id, cwd and every recorded id already mapped. */
function own319(text) {
  const encoded = (path) => path.replace(/[^A-Za-z0-9]/g, "-");
  let out = text.replaceAll(turns319.recordedId, sessionId)
    .replaceAll(encoded(turns319.cwd), encoded(process.cwd())).replaceAll(turns319.cwd, process.cwd());
  for (const [recorded, live] of turns319.ids) out = out.replaceAll(recorded, live);
  return out;
}
async function replay319(event) {
  const segment = turns319.segments[0];
  if (!segment || segment.kind !== lineKind(event)) {
    // Past the recording: an interrupt is answered as an idle claude answered it.
    if (lineKind(event) === "interrupt") emit({ type: "control_response", response: { subtype: "success", request_id: event.request_id, response: { still_queued: [] } } });
    return;
  }
  turns319.segments.shift();
  if (segment.line.uuid && event.uuid) turns319.ids.set(segment.line.uuid, event.uuid);
  if (segment.line.request_id && event.request_id) turns319.ids.set(segment.line.request_id, event.request_id);
  if (segment.transcript.length > 0) appendTranscript(segment.transcript.map((line) => `${own319(line)}\n`).join(""));
  const pace = Number(process.env.PI_TEST_CLAUDE_STUB_PACE_MS || 0);
  for (const line of segment.stream) {
    process.stdout.write(`${own319(line)}\n`);
    if (pace > 0) await new Promise((resolve) => setTimeout(resolve, pace));
  }
}
let chain319 = Promise.resolve();
/** The recorded transcript entries a replayed turn adds: its assistant entries, or the compaction's. */
function transcriptFor(name, segment) {
  const ids = new Set(segment.flatMap((event) => (event.type === "assistant" ? [event.message?.id] : [])));
  const compacting = segment.some((event) => event.type === "system" && event.subtype === "compact_boundary");
  const entries = recorded(name, "transcript.jsonl");
  const from = entries.findIndex((entry) => entry.subtype === "compact_boundary");
  return entries.filter((entry, index) => (compacting ? from >= 0 && index >= from : entry.type === "assistant" && ids.has(entry.message?.id)));
}
function replaySegment(name, segment) {
  const entries = transcriptFor(name, segment);
  if (entries.length > 0) appendTranscript(entries.map((entry) => `${JSON.stringify(entry)}\n`).join(""));
  for (const event of segment) emit(event);
}
function answerControl(event) {
  const request = event.request ?? {};
  const requestId = event.request_id;
  if (request.subtype === "set_model") {
    if (!isModelName(request.model) || request.model === "claude-bogus-0-0") {
      emit({ type: "control_response", response: { subtype: "error", request_id: requestId, error: "API error: 502 Upstream request failed · model not changed", error_code: "check_failed", model } });
      return;
    }
    model = request.model;
    emit({ type: "control_response", response: { subtype: "success", request_id: requestId, response: { model } } });
    return;
  }
  if (request.subtype === "set_permission_mode") {
    if (process.env.PI_TEST_CLAUDE_STUB_REFUSE_PERMISSION_MODE === "1" || !PERMISSION_MODES.includes(request.mode)) {
      emit({ type: "control_response", response: { subtype: "error", request_id: requestId, error: INVALID_PERMISSION_MODE, error_code: "invalid_mode" } });
      return;
    }
    const changed = request.mode !== permissionMode;
    permissionMode = request.mode;
    emit({ type: "control_response", response: { subtype: "success", request_id: requestId, response: { mode: permissionMode } } });
    if (changed) emit({ type: "system", subtype: "status", status: null, permissionMode, uuid: randomUUID(), session_id: sessionId });
    return;
  }
  if (request.subtype === "background_tasks") {
    emit({ type: "control_response", response: { subtype: "success", request_id: requestId, response: { backgrounded: true } } });
    return;
  }
  emit({ type: "control_response", response: { subtype: "success", request_id: requestId } });
}

log({ kind: probing ? "probe" : "launch", argv, cwd: process.cwd(), envKeys: Object.keys(process.env).sort(), configDir: process.env.CLAUDE_CONFIG_DIR ?? null });
if (process.env.PI_TEST_CLAUDE_STUB_GRANDCHILD === "1") {
  const sleeper = spawn("sleep", ["300"], { stdio: "ignore" });
  log({ kind: "grandchild", grandchildPid: sleeper.pid });
}
process.on("SIGINT", () => { log({ kind: "signal", signal: "SIGINT" }); process.exit(130); });
process.on("SIGTERM", () => { log({ kind: "signal", signal: "SIGTERM" }); process.exit(143); });

let waiting = null;
function replay() {
  // claude persists the turn's entries as it streams them, so by the time its
  // `result` is read the transcript holds the turn: write it first. Written
  // after the stream, a reader that acts on `result` (stats, history) races the
  // append and loses on a loaded host (verification s22, toolCalls 0 !== 1;
  // fixed the same transcript-before-reply order).
  const transcript = join(configDir, "projects", process.cwd().replace(/[^A-Za-z0-9]/g, "-"), `${sessionId}.jsonl`);
  mkdirSync(dirname(transcript), { recursive: true });
  appendFileSync(transcript, withId(readFileSync(join(recordings, "transcript.jsonl"), "utf8")));
  for (const line of readFileSync(join(recordings, "stream.jsonl"), "utf8").split("\n")) if (line) process.stdout.write(`${withId(line)}\n`);
}
async function replaySubagent(early) {
  const lines = readFileSync(join(recordings, "subagent", "stream.jsonl"), "utf8").split("\n").filter(Boolean);
  const events = lines.map((line) => JSON.parse(line));
  if (early) {
    const first = events.findIndex((event) => typeof event.parent_tool_use_id === "string" && event.parent_tool_use_id);
    const parentResult = events.findIndex((event) => event.type === "result");
    const [result] = events.splice(parentResult, 1);
    events.splice(first, 0, result);
  }
  const holdMs = Number(process.env.PI_TEST_CLAUDE_STUB_HOLD_MS || 0);
  for (const event of events) {
    emit(event);
    const subagentText = event.type === "assistant" && event.parent_tool_use_id && JSON.stringify(event.message.content).includes("\"text\"");
    if (subagentText && holdMs > 0) await new Promise((resolve) => setTimeout(resolve, holdMs));
  }
}
function hang() {
  emit({ type: "stream_event", event: { type: "message_start", message: { model: "stub", usage: {} } }, session_id: sessionId });
  emit({ type: "stream_event", event: { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } }, session_id: sessionId });
  emit({ type: "stream_event", event: { type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "partial answ" } }, session_id: sessionId });
  waiting = setInterval(() => {}, 1_000);
}

// the permission mode's turns.
let permissionSeq = 0;
let permissionWait = null;
const ASK_QUESTIONS = [
  { question: "Which colour?", header: "Colour", multiSelect: false, options: [{ label: "Red", description: "warm" }, { label: "Blue", description: "cool" }] },
  { question: "Which sizes?", header: "Sizes", multiSelect: true, options: [{ label: "S", description: "" }, { label: "M", description: "" }, { label: "L", description: "" }] },
];
function permissionTurnEnd(toolUseId, isError, content, reply) {
  emit({ type: "user", message: { role: "user", content: [{ type: "tool_result", tool_use_id: toolUseId, content, is_error: isError }] }, session_id: sessionId });
  emit({ type: "assistant", message: { model, id: `perm-reply-${permissionSeq}`, role: "assistant", content: [{ type: "text", text: reply }] }, session_id: sessionId });
  emit({ type: "result", subtype: "success", is_error: false, result: reply, session_id: sessionId, total_cost_usd: 0, usage: {} });
}
function permissionTurn(text) {
  permissionSeq += 1;
  const toolUseId = `toolu_perm_${permissionSeq}`;
  const asking = text.startsWith("ask:");
  const toolName = asking ? "AskUserQuestion" : "Bash";
  const input = asking ? { questions: ASK_QUESTIONS } : { command: text };
  emit({ type: "system", subtype: "init", session_id: sessionId, model, cwd: process.cwd(), tools: [] });
  emit({ type: "assistant", message: { model, id: `perm-call-${permissionSeq}`, role: "assistant", content: [{ type: "tool_use", id: toolUseId, name: toolName, input }] }, session_id: sessionId });
  if (valueOf("--permission-prompt-tool") !== "stdio") {
    emit({ type: "system", subtype: "permission_denied", tool_name: toolName, tool_use_id: toolUseId, decision_reason_type: "mode", session_id: sessionId });
    permissionTurnEnd(toolUseId, true, `Permission to use ${toolName} has been denied.`, "denied");
    return;
  }
  const requestId = `perm-${permissionSeq}`;
  const request = { subtype: "can_use_tool", tool_name: toolName, display_name: toolName, input, tool_use_id: toolUseId };
  if (!asking) request.permission_suggestions = [{ type: "addRules", rules: [{ toolName: "Bash", ruleContent: text }], behavior: "allow", destination: "localSettings" }];
  emit({ type: "control_request", request_id: requestId, request });
  permissionWait = { requestId, toolUseId };
}
function permissionAnswer(event) {
  const response = event.response ?? {};
  if (!permissionWait || response.request_id !== permissionWait.requestId) return;
  const { toolUseId } = permissionWait;
  permissionWait = null;
  const decision = response.response ?? {};
  if (decision.behavior === "allow") {
    const answers = decision.updatedInput?.answers;
    permissionTurnEnd(toolUseId, false, answers ? `answers: ${JSON.stringify(answers)}` : String(decision.updatedInput?.command ?? ""), "allowed");
  } else {
    permissionTurnEnd(toolUseId, true, String(decision.message ?? "denied"), "denied");
  }
}
function permissionInterrupt(event) {
  if (permissionWait && mode !== "permission-silent-stop") emit({ type: "control_cancel_request", request_id: permissionWait.requestId });
  permissionWait = null;
  emit({ type: "control_response", response: { subtype: "success", request_id: event.request_id, response: {} } });
  emit({ type: "result", subtype: "error_during_execution", is_error: true, session_id: sessionId, total_cost_usd: 0, usage: {} });
}

let buffer = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  buffer += chunk;
  let newline;
  while ((newline = buffer.indexOf("\n")) >= 0) {
    const line = buffer.slice(0, newline);
    buffer = buffer.slice(newline + 1);
    if (!line.trim()) continue;
    log({ kind: "stdin", line });
    const event = JSON.parse(line);
    const text = typeof event.message?.content === "string" ? event.message.content.trim() : "";
    if (event.type === "control_request" && event.request?.subtype === "initialize") {
      emit(initializeAnswer(event.request_id));
      continue;
    }
    if (turns319) {
      chain319 = chain319.then(() => replay319(event));
      continue;
    }
    if (mode === "permission" || mode === "permission-silent-stop") {
      if (event.type === "user") permissionTurn(text);
      else if (event.type === "control_response") permissionAnswer(event);
      else if (event.type === "control_request" && event.request?.subtype === "interrupt") permissionInterrupt(event);
      else if (event.type === "control_request") answerControl(event);
      continue;
    }
    if (segments && event.type === "control_request" && event.request?.subtype !== "interrupt") {
      const answer = segments.controls.shift();
      if (answer) emit({ ...answer, response: { ...answer.response, request_id: event.request_id } });
      else answerControl(event);
      continue;
    }
    if (segments && event.type === "user" && segments.turns.length > 0) {
      replaySegment(mode, segments.turns.shift());
      continue;
    }
    if (event.type === "control_request" && event.request?.subtype !== "interrupt" && mode !== "hang" && mode !== "deaf") {
      answerControl(event);
      continue;
    }
    if (event.type === "user" && text === "/compact" && mode !== "hang" && mode !== "deaf" && mode !== "exit") {
      replaySegment("compact", segmentsOf("compact").turns.at(-1));
      continue;
    }
    if (event.type === "user") {
      if (mode === "exit") process.exit(3);
      if (mode === "replay" || segments) replay();
      else if (mode === "subagent" || mode === "subagent-early") void replaySubagent(mode === "subagent-early");
      else hang();
    } else if (event.type === "control_request" && event.request?.subtype === "interrupt" && mode === "hang") {
      clearInterval(waiting);
      emit({ type: "control_response", response: { subtype: "success", request_id: event.request_id, response: {} } });
      emit({ type: "assistant", message: { model: "stub", id: "m1", role: "assistant", content: [{ type: "text", text: "partial answ" }] }, session_id: sessionId });
      emit({ type: "user", message: { role: "user", content: [{ type: "text", text: "[Request interrupted by user]" }] }, session_id: sessionId });
      emit({ type: "result", subtype: "error_during_execution", is_error: true, session_id: sessionId, total_cost_usd: 0, usage: {} });
    }
  }
});
process.stdin.on("end", () => process.exit(0));
