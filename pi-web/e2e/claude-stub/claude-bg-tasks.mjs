#!/usr/bin/env node
/**
 * PATH-injected stand-in for the `claude` CLI with background tasks,
 * shared by lib/claude-background-tasks-443.test.mjs and the e2e suite. Never touches
 * a network. The frames are the shapes claude 2.1.319 recorded
 * (lib/__fixtures__/claude-2.1.319/absorbed-notification/): a background Bash is a
 * `tool_use` with `run_in_background`, a `task_started` of `task_type: "local_bash"`
 * with `is_backgrounded`, and a `tool_result` naming its `backgroundTaskId`.
 *
 * Every user line opens a turn (`system/init` first, `result` last). Its text decides it:
 *   bg: <command>         a background `Bash` running <command>: a real `sleep` child in
 *                         this process's group stands in for it and keeps running
 *   monitor: <desc>       the same through the `Monitor` tool (claude's kind of local_bash)
 *   hang                  a turn that streams one text delta, then runs until an interrupt,
 *                         which ends it (`error_during_execution`); background tasks untouched
 *   stuck                 the same, but an interrupt is only answered: the turn runs on
 *   stuck-finish: <cmd>   a `stuck` turn during which the running task of <cmd> completes
 *                         (`task_updated` `completed`, `task_notification` `completed`):
 *                         a finished background task whose follow-up turn has not started
 *   anything else         a plain text reply
 * Control requests are answered from CONTROLS: an interrupt (`still_queued: []`; it also
 * ends a `hang` turn; without `spare_background_tasks: true` it also stops every running
 * task, as claude without that flag stops its background work: each settles as `stop_task`
 * settles one, below), `set_model` / `apply_flag_settings` with success, any other subtype
 * with an error. Each stdin line is logged as written, so a test reads the interrupt's flags.
 * The task requests take the shapes of claude 2.1.322's schema:
 *   stop_task { task_id }        a running task: answered `{}`, its child killed, then
 *                                `task_updated` `{ status: "killed", end_time }`,
 *                                `task_notification` `{ status: "stopped" }` and
 *                                `background_tasks_changed` (the tasks still running);
 *                                any other id: an error naming it
 *   get_task_output { task_id }  a known task: `{ output: taskOutput(task), total_bytes,
 *                                truncated: false }`; any other id: an error naming it
 *
 * Each turn's user and assistant entries go to claude's transcript,
 * `<config dir>/projects/<cwd made a dir name>/<session id>.jsonl`, before its stream (a
 * fork reads them). SIGINT / SIGTERM: logged, the task children killed, then exit.
 *
 * Log ($PI_TEST_CLAUDE_STUB_LOG, else $PI_E2E_CLAUDE_STUB_LOG), one JSON object per line,
 * each with this stub's `pid`: `launch` (argv, cwd), `stdin` (the raw line), `task`
 * (taskId, toolUseId, kind, command, taskPid), `signal`.
 */
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { appendFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";

const argv = process.argv.slice(2);
const valueOf = (flag) => (argv.includes(flag) ? argv[argv.indexOf(flag) + 1] : undefined);
const sessionId = valueOf("--resume") ?? valueOf("--session-id") ?? randomUUID();
const model = valueOf("--model") ?? "claude-haiku-5-5";
const cwd = process.cwd();
const logFile = process.env.PI_TEST_CLAUDE_STUB_LOG || process.env.PI_E2E_CLAUDE_STUB_LOG;
const log = (entry) => {
  if (logFile) appendFileSync(logFile, `${JSON.stringify({ pid: process.pid, ...entry })}\n`);
};
const emit = (event) => process.stdout.write(`${JSON.stringify(event)}\n`);
// As claude: the inherited CLAUDE_CONFIG_DIR, else ~/.claude of the child's HOME.
const configDir = process.env.CLAUDE_CONFIG_DIR || join(process.env.HOME ?? "", ".claude");
const transcriptFile = join(configDir, "projects", cwd.replace(/[^A-Za-z0-9]/g, "-"), `${sessionId}.jsonl`);

/** The transcript's last entry uuid: the next entry's `parentUuid`. */
let lastUuid = null;
function appendTranscript(entries) {
  mkdirSync(dirname(transcriptFile), { recursive: true });
  let text = "";
  for (const entry of entries) {
    const uuid = entry.uuid ?? randomUUID();
    text += `${JSON.stringify({ parentUuid: lastUuid, isSidechain: false, userType: "external", cwd, sessionId, version: "2.1.322", timestamp: new Date().toISOString(), ...entry, uuid })}\n`;
    lastUuid = uuid;
  }
  appendFileSync(transcriptFile, text);
}

/** claude's background tasks by task id: the child standing in for each command. */
const tasks = new Map();
/** A shell task id as claude makes one: `b` and 8 of [0-9a-z]. */
const taskId = () => `b${Array.from({ length: 8 }, () => "0123456789abcdefghijklmnopqrstuvwxyz"[Math.floor(Math.random() * 36)]).join("")}`;
function startTask(kind, toolUseId, command, description) {
  const id = taskId();
  // Same process group as this stub: claude's own tasks die with its group.
  const child = spawn("sleep", ["3600"], { stdio: "ignore" });
  tasks.set(id, { id, kind, toolUseId, command, description, child, status: "running" });
  log({ kind: "task", taskId: id, toolUseId, taskKind: kind, command, taskPid: child.pid });
  return id;
}
/** A task's output as this stub reports it: fixed lines naming its command. */
const taskOutput = (task) => `$ ${task.command}\nstub output line 1\nstub output line 2\n`;
/** A task ending (its child gone): `task_updated` with `status`, its `task_notification`, then `background_tasks_changed`. */
function endTask(task, status, notified) {
  task.status = status;
  try { task.child.kill("SIGKILL"); } catch { /* gone */ }
  const frame = (fields) => emit({ type: "system", ...fields, session_id: sessionId, uuid: randomUUID() });
  frame({ subtype: "task_updated", task_id: task.id, patch: { status, end_time: Date.now() } });
  frame({ subtype: "task_notification", task_id: task.id, tool_use_id: task.toolUseId, status: notified, output_file: `/tmp/claude-stub/${sessionId}/tasks/${task.id}.output`, summary: `${task.description} ${notified}` });
  frame({ subtype: "background_tasks_changed", tasks: [...tasks.values()].filter((entry) => entry.status === "running").map((entry) => ({ task_id: entry.id, task_type: "local_bash", description: entry.description })) });
}
function killTasks() {
  for (const task of tasks.values()) {
    try { task.child.kill("SIGKILL"); } catch { /* gone */ }
  }
}

const usage = { input_tokens: 1, output_tokens: 1 };
const assistant = (content) => ({
  type: "assistant",
  message: { model, id: `msg_${randomUUID().replaceAll("-", "")}`, type: "message", role: "assistant", content, usage },
  parent_tool_use_id: null,
  session_id: sessionId,
  uuid: randomUUID(),
});
const init = () => ({ type: "system", subtype: "init", cwd, session_id: sessionId, model, tools: ["Bash", "Monitor", "Read"], uuid: randomUUID() });
const result = (text) => ({ type: "result", subtype: "success", is_error: false, result: text, num_turns: 1, duration_ms: 1, total_cost_usd: 0, usage, session_id: sessionId, uuid: randomUUID() });

/** A turn: the user's entry and the replies to the transcript first, then the stream. */
function turn(userLine, frames) {
  const userUuid = typeof userLine.uuid === "string" ? userLine.uuid : randomUUID();
  const entries = [{ type: "user", uuid: userUuid, message: userLine.message, promptSource: "sdk", turnOrigin: "sdk" }];
  for (const frame of frames) {
    if (frame.type === "assistant") entries.push({ type: "assistant", message: frame.message });
    if (frame.type === "user") entries.push({ type: "user", message: frame.message, ...(frame.tool_use_result ? { toolUseResult: frame.tool_use_result } : {}) });
  }
  appendTranscript(entries);
  emit(init());
  for (const frame of frames) emit(frame);
}

function backgroundTurn(userLine, tool, argument) {
  const toolUseId = `toolu_${randomUUID().replaceAll("-", "").slice(0, 24)}`;
  const monitor = tool === "Monitor";
  const description = monitor ? argument : `Run ${argument} in the background`;
  const input = monitor ? { command: argument, description } : { command: argument, description, run_in_background: true };
  const id = startTask(monitor ? "monitor" : "shell", toolUseId, argument, description);
  const receipt = `Command running in background with ID: ${id}. Output is being written to: /tmp/claude-stub/${sessionId}/tasks/${id}.output. You will be notified when it completes.`;
  turn(userLine, [
    assistant([{ type: "tool_use", id: toolUseId, name: tool, input }]),
    { type: "system", subtype: "task_started", task_id: id, tool_use_id: toolUseId, description, is_backgrounded: true, task_type: "local_bash", session_id: sessionId, uuid: randomUUID() },
    {
      type: "user",
      message: { role: "user", content: [{ tool_use_id: toolUseId, type: "tool_result", content: receipt, is_error: false }] },
      parent_tool_use_id: null,
      session_id: sessionId,
      uuid: randomUUID(),
      tool_use_result: { stdout: "", stderr: "", interrupted: false, isImage: false, noOutputExpected: false, backgroundTaskId: id },
    },
    assistant([{ type: "text", text: `Started ${argument} in the background.` }]),
    result(`Started ${argument} in the background.`),
  ]);
}

function replyTurn(userLine, text) {
  const reply = `You said: ${text}`;
  turn(userLine, [assistant([{ type: "text", text: reply }]), result(reply)]);
}

/** The open `hang` / `stuck` turn: its mode; null when none runs. */
let hanging = null;
function hangingTurn(userLine, mode) {
  turn(userLine, [
    { type: "stream_event", event: { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } }, parent_tool_use_id: null, session_id: sessionId, uuid: randomUUID() },
    { type: "stream_event", event: { type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "Working on it" } }, parent_tool_use_id: null, session_id: sessionId, uuid: randomUUID() },
  ]);
  hanging = mode;
}

/** A `stuck` turn in which the running task of `command` completes. */
function stuckFinishTurn(userLine, command) {
  hangingTurn(userLine, "stuck");
  const task = [...tasks.values()].find((entry) => entry.command === command && entry.status === "running");
  if (task) endTask(task, "completed", "completed");
}

/** User line directives, by prefix (later units add theirs here). */
const DIRECTIVES = [
  ["bg:", (line, rest) => backgroundTurn(line, "Bash", rest)],
  ["monitor:", (line, rest) => backgroundTurn(line, "Monitor", rest)],
  ["hang", (line) => hangingTurn(line, "hang")],
  ["stuck-finish:", (line, rest) => stuckFinishTurn(line, rest)],
  ["stuck", (line) => hangingTurn(line, "stuck")],
];

const answer = (requestId, response) => emit({ type: "control_response", response: { subtype: "success", request_id: requestId, response } });
const refuse = (requestId, error) => emit({ type: "control_response", response: { subtype: "error", request_id: requestId, error } });
/** Control request handlers, by subtype (later units add theirs here). */
const CONTROLS = {
  interrupt: (requestId, request) => {
    answer(requestId, { still_queued: [] });
    // A plain interrupt stops claude's background work too; `spare_background_tasks` leaves it.
    if (request.spare_background_tasks !== true) {
      for (const task of [...tasks.values()].filter((entry) => entry.status === "running")) endTask(task, "killed", "stopped");
    }
    if (hanging !== "hang") return;
    // The interrupted turn ends; the background tasks run on.
    hanging = null;
    emit({ type: "result", subtype: "error_during_execution", is_error: true, num_turns: 1, duration_ms: 1, total_cost_usd: 0, usage, session_id: sessionId, uuid: randomUUID() });
  },
  set_model: (requestId, request) => answer(requestId, { model: request.model }),
  apply_flag_settings: (requestId) => answer(requestId, {}),
  stop_task: (requestId, request) => {
    const task = tasks.get(request.task_id);
    if (!task || task.status !== "running") return refuse(requestId, `No running task with ID: ${request.task_id}`);
    answer(requestId, {});
    endTask(task, "killed", "stopped");
  },
  get_task_output: (requestId, request) => {
    const task = tasks.get(request.task_id);
    if (!task) return refuse(requestId, `No task found with ID: ${request.task_id}`);
    const output = taskOutput(task);
    answer(requestId, { output, total_bytes: Buffer.byteLength(output), truncated: false });
  },
};

function handleLine(event) {
  if (event.type === "control_request") {
    const request = event.request ?? {};
    const handler = CONTROLS[request.subtype];
    if (handler) handler(event.request_id, request);
    else refuse(event.request_id, `Unsupported control request: ${request.subtype}`);
    return;
  }
  if (event.type !== "user") return;
  const content = event.message?.content;
  const text = (typeof content === "string" ? content : Array.isArray(content) ? content.filter((block) => block?.type === "text").map((block) => block.text).join("") : "").trim();
  for (const [prefix, run] of DIRECTIVES) {
    if (text.startsWith(prefix)) return run(event, text.slice(prefix.length).trim());
  }
  replyTurn(event, text);
}

log({ kind: "launch", argv, cwd });
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => {
    log({ kind: "signal", signal });
    killTasks();
    process.exit(signal === "SIGINT" ? 130 : 143);
  });
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
    let event;
    try { event = JSON.parse(line); } catch { continue; }
    handleLine(event);
  }
});
process.stdin.on("end", () => {
  killTasks();
  process.exit(0);
});
