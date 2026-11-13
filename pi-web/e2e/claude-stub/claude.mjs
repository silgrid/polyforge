#!/usr/bin/env node
/**
 * PATH-injected stand-in for the `claude` CLI ( e2e, authored
 * before `code`).
 *
 * The suite puts a copy of this file on the server's PATH as `claude`, so
 * pi-web's PATH-only binary resolution finds a real executable and spawns it
 * as a child. The stub speaks the same stream-json dialect that claude 2.1.312
 * emits (recorded from the real CLI: `system`/`init`, `stream_event`
 * `content_block_*` + `text_delta`/`input_json_delta`, full `assistant`
 * messages, a `user` tool_result event and the final `result` aggregate), and
 * it appends a real-shaped jsonl transcript under
 * `<config dir>/projects/<encoded cwd>/<session id>.jsonl` so the session's
 * history can be rendered from claude's own transcript. The config dir is the
 * child's inherited `CLAUDE_CONFIG_DIR` when pi-web passed one through, else
 * the child's `$HOME/.claude` (the system claude's own dir).
 *
 * It records — for the test only — each launch's argv, its child environment
 * variable NAMES (never values) and the resolved config dir, plus an `exit`
 * record when it is terminated. Nothing else leaves the process.
 *
 * Every stdin line is recorded too (`{ kind: "stdin", pid, line }`), so a suite
 * can assert the exact bytes pi-web wrote to the child. A `--resume` launch also
 * records the uuids its transcript holds at launch (`resumedUuids`),
 * the conversation a resumed claude would load.
 *
 * Transcript tree, as claude 2.1.312 writes it: every entry's
 * `parentUuid` is the entry written before it, and a resumed child chains its first
 * entry to where the conversation stood — the leaf of the newest `last-prompt`
 * checkpoint, else the last entry. `--resume-session-at <uuid>` (the truncating resume
 * of an in-session edit) chains it to that entry instead, so the old turns stay in the
 * file on a branch of their own; a uuid the loaded chain does not hold (another branch,
 * or before the latest compaction) is refused like claude refuses it (`No message found
 * with message.uuid of: <uuid>` on stderr, exit 1) before anything is written. Like claude, the child writes its checkpoint — `last-prompt`
 * with the last entry it wrote as `leafUuid` — only when it exits, never per turn: until
 * then the newest checkpoint of an edited session still names the old branch's leaf.
 *
 * Image mode (`PI_E2E_CLAUDE_STUB_MODE=image`): a user event whose
 * content carries an image block (text optional) is answered like the real claude
 * 2.1.312 answered one: the received user entry (content verbatim, inline base64)
 * and claude's hidden `isMeta` `[Image: source: …]` companion are written to the
 * transcript first, then the recorded `stream.jsonl` of
 * `$PI_E2E_CLAUDE_IMAGE_FIXTURE` is replayed with the recording's session id and
 * cwd replaced by this child's (its assistant messages appended before `result`).
 * A user event without an image keeps the synthesised reply; a missing fixture
 * falls back to it as well.
 *
 * Control channel and compaction (every mode): a `control_request`
 * `set_model` for any name `--model` takes (no fixed list) but `claude-bogus-0-0` is answered like claude 2.1.312
 * answered it (`{subtype: "success", response: {model}}`, its `/model` bookkeeping
 * entries appended to the transcript) and later replies report that model; any other
 * name gets the recorded `check_failed` refusal and the model stays. An
 * `apply_flag_settings` `{effortLevel}` request is answered `success` and the level in force is logged (`{ kind: "effort" }`). A
 * user line that is exactly `/compact` is claude's own compaction: the transcript's
 * `compact_boundary` entry, the hidden summary and the `Compacted` bookkeeping are
 * appended first, then `status` compacting, `compact_boundary` and a zero-usage
 * `result` are streamed (lib/__fixtures__/claude-2.1.312/README.md, `compact/`).
 *
 * Fixture modes (`PI_E2E_CLAUDE_STUB_MODE=switch|effort|compact`):
 * each stdin line replays the next segment of the recording in
 * `$PI_E2E_CLAUDE_SWITCH_FIXTURE` / `$PI_E2E_CLAUDE_EFFORT_FIXTURE` /
 * `$PI_E2E_CLAUDE_COMPACT_FIXTURE` (a user line: the events up to its `result`; a
 * control request: its `control_response`, under this request's id), with the
 * recording's session id and cwd replaced by this child's and its assistant
 * entries / compact boundary appended to the transcript before `result`. A missing
 * fixture, or a line the recording has no segment left for, falls back to the
 * synthesised answer above; nothing ever hangs.
 *
 * Shell turn (every mode's synthesised answer): a user line that starts
 * with `$ ` is run like claude's Bash tool runs a command — `/bin/sh -c <rest>` in this
 * child's own cwd and environment — and its output is the turn's `Bash` tool result
 * (instead of the `Read` probe), so a suite can see what a session's command inherits.
 *
 * Edit mode (`PI_E2E_CLAUDE_STUB_MODE=edit`): a fixture mode (below) over
 * `$PI_E2E_CLAUDE_EDIT_FIXTURE`, the real claude 2.1.322 `edit/` recording
 * (lib/__fixtures__/claude-2.1.322/README.md): one `Edit`, one `replace_all` `Edit` and a
 * `Write` update, each tool result carrying claude's `tool_use_result` receipt. A replayed
 * `user` event that holds `tool_result` blocks is appended to the transcript too, as claude
 * writes it (its receipt as the entry's `toolUseResult`), so the history read finds the
 * receipt's `structuredPatch` like the live stream does.
 *
 * Retry turn (every mode): a user line that starts with `retry: ` is
 * always answered by the synthesised reply (never a recording's segment), which first
 * emits claude 2.1.322's `system` / `api_retry` frame for a rate-limited request
 * (`attempt: 1`, `max_retries: 10`, `retry_delay_ms: 1500`, `error_status: 429`,
 * `error: "rate_limit"`), holds the retry's 1.5 s, then streams the reply.
 *
 * Initialize and the probe (every mode): an `initialize` control request
 * is answered with claude 2.1.322's recorded answer (`initialize/stream.jsonl` of
 * `$PI_E2E_CLAUDE_INITIALIZE_FIXTURE`, lib/__fixtures__/claude-2.1.322/initialize/) under
 * this request's id — claude's model aliases, version and capabilities, which GET
 * /api/claude reports — with `$PI_E2E_CLAUDE_STUB_VERSION` as its `claude_code_version`
 * when set. Without the fixture it is answered with an error, so the composer offers
 * only `default`. A launch with `--no-session-persistence` and no session (Pi Web's
 * probe, lib/claude-probe.ts) is recorded as `kind: "probe"` (never `launch` or `exit`),
 * its stdin lines as `kind: "probe-stdin"`, and streams no `init`, so a suite's launch
 * counts and stdin reads stay a session's own.
 *
 * Permission mode (every mode): a `set_permission_mode` control request for
 * default, acceptEdits or plan is answered like claude 2.1.322 answered it (the
 * `permission-mode/` recording): `success` with `{mode}`, then — when the mode changed —
 * the `system` / `status` frame carrying `permissionMode`; any other mode gets the
 * recorded `invalid_mode` refusal.
 *
 * Hook events (the synthesised answer): a launch with `--include-hook-events`
 * opens each synthesised turn with the hook frames (`system` `hook_started` /
 * `hook_progress` / `hook_response`) of `$PI_E2E_CLAUDE_HOOKS_FIXTURE` (the claude 2.1.322
 * `hooks/` recording) under this child's session id, then holds 1.5 s, as the hooks' run
 * does, before the reply streams. Without the flag (or the recording) no hook frame is
 * sent, as claude sends none.
 *
 * Permission turns (every mode): a user line that starts with `perm: ` asks
 * for claude's Bash tool with the rest as its command, one that starts with `ask: ` for
 * AskUserQuestion with one single-select question (`你喜欢哪种颜色？`, header `颜色`,
 * options `红色` / `蓝色`), in claude 2.1.322's permission shapes (synthetic, no recording;
 * the unit stub's `permission` mode, lib/__fixtures__/claude-stub/claude.mjs). argv decides
 * what claude does with the call. Launched with `--permission-prompt-tool stdio` (ask mode),
 * it streams the tool_use, sends `control_request` / `can_use_tool` (request id
 * `perm-<pid>-<n>`, logged as `{ kind: "permission_request" }`; Bash carries one
 * `localSettings` suggestion, so the always-allow choice is offered) and waits for the
 * `control_response` naming it: allow → the tool_result (the command, or
 * `answers: <json>`) and the reply `E2E allowed: <line>`; deny → an error tool_result (the
 * deny message) and `E2E denied: <line>`. Launched without it (`--permission-prompts none`,
 * deny mode) it sends `system` / `permission_denied` (decision_reason_type `mode`, logged as
 * `{ kind: "permission_denied" }`), an error tool_result and `E2E denied: <line>`. An
 * interrupt while a request waits withdraws it (`control_cancel_request`) and ends the turn
 * as stopped. Each turn ends with a `result`, and its entries go to the transcript.
 *
 * Inputs (environment):
 *   PI_E2E_CLAUDE_STUB_LOG       the jsonl record file (argv / env names / stdin / exits)
 *   PI_E2E_CLAUDE_STUB_MODE      unset, `image`, `switch`, `effort`, `compact` or `edit` (above)
 *   PI_E2E_CLAUDE_IMAGE_FIXTURE  the recording directory image mode replays
 *   PI_E2E_CLAUDE_SWITCH_FIXTURE   the `model-switch/` recording directory
 *   PI_E2E_CLAUDE_EFFORT_FIXTURE   the `effort/` recording directory
 *   PI_E2E_CLAUDE_COMPACT_FIXTURE  the `compact/` recording directory
 *   PI_E2E_CLAUDE_EDIT_FIXTURE     the claude 2.1.322 `edit/` recording directory
 *   PI_E2E_CLAUDE_INITIALIZE_FIXTURE  the claude 2.1.322 `initialize/` recording directory
 *   PI_E2E_CLAUDE_STUB_VERSION     the version the initialize answer reports instead of the recording's
 *   PI_E2E_CLAUDE_HOOKS_FIXTURE    the claude 2.1.322 `hooks/` recording directory
 *   CLAUDE_CONFIG_DIR            the inherited config dir, when pi-web has one
 *   HOME                         hosts the `~/.claude` fallback
 */
import { execFileSync } from "node:child_process";
import { appendFileSync, mkdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { randomUUID } from "node:crypto";

const logPath = process.env.PI_E2E_CLAUDE_STUB_LOG;
const argv = process.argv.slice(2);
const valueOf = (flag) => {
  const equals = argv.find((arg) => arg.startsWith(`${flag}=`));
  if (equals) return equals.slice(flag.length + 1);
  const index = argv.indexOf(flag);
  return index >= 0 ? argv[index + 1] : undefined;
};

const cwd = process.cwd();
const configDir = process.env.CLAUDE_CONFIG_DIR ?? join(process.env.HOME ?? "", ".claude");
const resumeId = valueOf("--resume");
const resumeAt = resumeId ? valueOf("--resume-session-at") : undefined;
const sessionId = resumeId ?? valueOf("--session-id") ?? randomUUID();
// The current model and effort: argv first, then whatever a control request switched to.
let model = valueOf("--model");
let effort = valueOf("--effort");
// A name `--model` takes (lib/claude-shared.ts isClaudeModelName).
const isModelName = (value) => typeof value === "string" && value.length <= 128 && /^[A-Za-z0-9][A-Za-z0-9._:@/[\]-]*$/.test(value);
// The name claude 2.1.312's model-switch recording shows it refusing (check_failed).
const REFUSED_MODEL = "claude-bogus-0-0";
// Pi Web's probe (lib/claude-probe.ts): no session, nothing persisted, only `initialize` asked.
const probing = argv.includes("--no-session-persistence") && !resumeId && valueOf("--session-id") === undefined;

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function record(entry) {
  if (!logPath) return;
  try {
    appendFileSync(logPath, `${JSON.stringify(entry)}\n`);
  } catch {
    /* the suite removes its scratch tree at the end */
  }
}

/**
 * What a resumed child finds to load: the uuids of its transcript's
 * entries at launch, in file order; null when it has none. A fresh child: undefined.
 */
function resumedEntries() {
  if (!resumeId) return undefined;
  try {
    return readFileSync(join(configDir ?? "", "projects", cwd.replace(/[^A-Za-z0-9._-]/g, "-"), `${sessionId}.jsonl`), "utf8")
      .split("\n")
      .filter(Boolean)
      .flatMap((line) => {
        try {
          return [JSON.parse(line)];
        } catch {
          return [];
        }
      });
  } catch {
    return null;
  }
}
const resumed = resumedEntries();
function resumedUuids() {
  return resumed?.flatMap((entry) => (typeof entry.uuid === "string" ? [entry.uuid] : []));
}
/**
 * Where a resumed child's first entry chains to: the leaf it loads — the newest checkpoint's
 * leaf, else the last entry — or, with `--resume-session-at`, that entry, which must lie on the
 * loaded chain (the leaf's ancestors up to the latest compaction boundary, as claude walks
 * them); undefined when it does not.
 */
function resumeParent() {
  if (!resumed) return null;
  const byUuid = new Map(resumed.filter((entry) => typeof entry.uuid === "string" && entry.isSidechain !== true).map((entry) => [entry.uuid, entry]));
  const checkpoint = resumed.findLast((entry) => entry.type === "last-prompt" && typeof entry.leafUuid === "string")?.leafUuid;
  const leaf = checkpoint && byUuid.has(checkpoint)
    ? checkpoint
    : resumed.findLast((entry) => typeof entry.uuid === "string" && entry.isSidechain !== true)?.uuid ?? null;
  if (resumeAt === undefined) return leaf;
  for (let at = leaf, seen = new Set(); at && byUuid.has(at) && !seen.has(at); at = byUuid.get(at).parentUuid) {
    if (at === resumeAt) return resumeAt;
    seen.add(at);
  }
  return undefined;
}

record({
  kind: probing ? "probe" : "launch",
  pid: process.pid,
  argv,
  cwd,
  configDir: configDir || null,
  envKeys: Object.keys(process.env).sort(),
  resumed: Boolean(resumeId),
  resumedUuids: resumedUuids(),
  resumeAt: resumeAt ?? null,
  sessionId,
});

const startParent = resumeParent();
if (startParent === undefined) {
  // claude 2.1.312 refuses a truncating resume at an entry its conversation does not hold.
  process.stderr.write(`No message found with message.uuid of: ${resumeAt}\n`);
  record({ kind: "refused", pid: process.pid, sessionId, resumeAt });
  process.exit(1);
}

let exited = false;
let lastWritten = null;
function recordExit() {
  if (exited) return;
  exited = true;
  // claude's exit checkpoint: the leaf this child left the conversation at.
  if (lastWritten) {
    try {
      appendFileSync(transcriptPath(), `${JSON.stringify({ type: "last-prompt", leafUuid: lastWritten, sessionId })}\n`);
    } catch {
      /* the suite removes its scratch tree at the end */
    }
  }
  if (!probing) record({ kind: "exit", pid: process.pid, sessionId });
}
process.on("exit", recordExit);
process.on("SIGTERM", () => { recordExit(); process.exit(0); });
process.on("SIGINT", () => { recordExit(); process.exit(0); });

function emit(event) {
  process.stdout.write(`${JSON.stringify(event)}\n`);
}

const transcriptPath = () => join(
  configDir ?? "",
  "projects",
  cwd.replace(/[^A-Za-z0-9._-]/g, "-"),
  `${sessionId}.jsonl`,
);
let parentUuid = startParent ?? null;
function appendTranscript(type, message, extra = {}) {
  const uuid = randomUUID();
  const entry = {
    type,
    message,
    uuid,
    parentUuid,
    sessionId,
    cwd,
    version: "2.1.312",
    timestamp: new Date().toISOString(),
    isSidechain: false,
    userType: "external",
    ...extra,
  };
  parentUuid = entry.uuid;
  try {
    mkdirSync(dirname(transcriptPath()), { recursive: true });
    appendFileSync(transcriptPath(), `${JSON.stringify(entry)}\n`);
    lastWritten = entry.uuid;
  } catch {
    /* the suite removes its scratch tree at the end */
  }
}

/** The shell turn's command, run as claude's Bash tool would: its output, stderr included. */
function runShell(command) {
  let output;
  try {
    output = execFileSync("/bin/sh", ["-c", command], { cwd, env: process.env, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], timeout: 10_000 });
  } catch (error) {
    output = `${error.stdout ?? ""}${error.stderr ?? ""}` || String(error);
  }
  record({ kind: "shell", pid: process.pid, command });
  return output;
}

let turn = 0;
const probePath = join(cwd, "e2e-probe.txt");

async function respond(userText) {
  turn += 1;
  const messageId = `msg_e2e_${turn}`;
  const toolId = `toolu_e2e_${turn}`;
  const streamed = `E2E streamed reply to ${userText}`;
  const finalText = `E2E final reply to ${userText}`;
  const shellCommand = userText.startsWith("$ ") ? userText.slice(2) : null;
  const tool = shellCommand === null
    ? { name: "Read", input: { file_path: probePath }, output: `E2E tool output: ${userText}` }
    : { name: "Bash", input: { command: shellCommand }, output: runShell(shellCommand) };
  const modelId = model ?? "claude-default";
  const meta = { session_id: sessionId, parent_tool_use_id: null, uuid: randomUUID() };
  const usage = { input_tokens: 9, cache_creation_input_tokens: 0, cache_read_input_tokens: 0, output_tokens: 0 };

  appendTranscript("user", { role: "user", content: userText }, { promptSource: "sdk" });

  const hooks = recordedHookFrames();
  if (hooks.length > 0) {
    for (const frame of hooks) emit({ ...frame, uuid: randomUUID() });
    await sleep(1500);
  }

  if (userText.startsWith("retry: ")) {
    // claude 2.1.322's frame for a rate-limited request it retries, then the retry's wait.
    emit({ type: "system", subtype: "api_retry", attempt: 1, max_retries: 10, retry_delay_ms: 1500, error_status: 429, error: "rate_limit", uuid: randomUUID(), session_id: sessionId });
    await sleep(1500);
  }

  emit({
    type: "stream_event",
    event: { type: "message_start", message: { id: messageId, type: "message", role: "assistant", model: modelId, content: [], usage } },
    ...meta,
  });
  emit({ type: "stream_event", event: { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } }, ...meta });
  const chunks = [streamed.slice(0, 20), streamed.slice(20, 30), streamed.slice(30)];
  for (const text of chunks.filter(Boolean)) {
    emit({ type: "stream_event", event: { type: "content_block_delta", index: 0, delta: { type: "text_delta", text } }, ...meta });
    await sleep(200);
  }
  emit({ type: "stream_event", event: { type: "content_block_stop", index: 0 }, ...meta });
  const streamedMessage = { id: messageId, type: "message", role: "assistant", model: modelId, content: [{ type: "text", text: streamed }], stop_reason: null, usage };
  emit({ type: "assistant", message: streamedMessage, ...meta });
  appendTranscript("assistant", streamedMessage);

  // One tool call per turn, so the chat shows a tool call row and its result.
  emit({
    type: "stream_event",
    event: { type: "content_block_start", index: 1, content_block: { type: "tool_use", id: toolId, name: tool.name, input: {} } },
    ...meta,
  });
  const inputJson = JSON.stringify(tool.input);
  for (let at = 0; at < inputJson.length; at += 12) {
    emit({
      type: "stream_event",
      event: { type: "content_block_delta", index: 1, delta: { type: "input_json_delta", partial_json: inputJson.slice(at, at + 12) } },
      ...meta,
    });
    await sleep(100);
  }
  emit({ type: "stream_event", event: { type: "content_block_stop", index: 1 }, ...meta });
  const toolMessage = {
    id: `${messageId}_tool`,
    type: "message",
    role: "assistant",
    model: modelId,
    content: [{ type: "tool_use", id: toolId, name: tool.name, input: tool.input }],
    stop_reason: "tool_use",
    usage,
  };
  emit({ type: "assistant", message: toolMessage, ...meta });
  appendTranscript("assistant", toolMessage);

  emit({
    type: "user",
    message: { role: "user", content: [{ tool_use_id: toolId, type: "tool_result", content: tool.output }] },
    ...meta,
  });
  appendTranscript("user", { role: "user", content: [{ tool_use_id: toolId, type: "tool_result", content: tool.output }] });

  emit({ type: "stream_event", event: { type: "content_block_start", index: 2, content_block: { type: "text", text: "" } }, ...meta });
  for (const text of [finalText.slice(0, 14), finalText.slice(14)]) {
    emit({ type: "stream_event", event: { type: "content_block_delta", index: 2, delta: { type: "text_delta", text } }, ...meta });
    await sleep(120);
  }
  emit({ type: "stream_event", event: { type: "content_block_stop", index: 2 }, ...meta });
  const finalMessage = { id: `${messageId}_final`, type: "message", role: "assistant", model: modelId, content: [{ type: "text", text: finalText }], stop_reason: "end_turn", usage };
  emit({ type: "assistant", message: finalMessage, ...meta });
  appendTranscript("assistant", finalMessage);

  await sleep(200);
  emit({
    type: "result",
    subtype: "success",
    is_error: false,
    session_id: sessionId,
    result: finalText,
    num_turns: turn,
    duration_ms: 1200,
    total_cost_usd: 0.0125,
    permission_denials: [],
    usage: { input_tokens: 42, cache_creation_input_tokens: 0, cache_read_input_tokens: 0, output_tokens: 37 },
    uuid: randomUUID(),
  });
}

// the permission turns (`perm: ` / `ask: `), in claude 2.1.322's shapes.
let permissionSeq = 0;
/** The request waiting for Pi Web's answer: `{ requestId, resolve(decision | null) }`. */
let permissionWait = null;
const ASK_QUESTION = { question: "你喜欢哪种颜色？", header: "颜色", multiSelect: false, options: [{ label: "红色", description: "暖色" }, { label: "蓝色", description: "冷色" }] };

async function permissionTurn(userText) {
  turn += 1;
  permissionSeq += 1;
  const messageId = `msg_e2e_perm_${turn}`;
  const toolId = `toolu_e2e_perm_${turn}`;
  const asking = userText.startsWith("ask: ");
  const toolName = asking ? "AskUserQuestion" : "Bash";
  const input = asking ? { questions: [ASK_QUESTION] } : { command: userText.slice("perm: ".length) };
  const modelId = model ?? "claude-default";
  const meta = { session_id: sessionId, parent_tool_use_id: null, uuid: randomUUID() };
  const usage = { input_tokens: 9, cache_creation_input_tokens: 0, cache_read_input_tokens: 0, output_tokens: 0 };
  appendTranscript("user", { role: "user", content: userText }, { promptSource: "sdk" });

  emit({ type: "stream_event", event: { type: "message_start", message: { id: messageId, type: "message", role: "assistant", model: modelId, content: [], usage } }, ...meta });
  emit({ type: "stream_event", event: { type: "content_block_start", index: 0, content_block: { type: "tool_use", id: toolId, name: toolName, input: {} } }, ...meta });
  emit({ type: "stream_event", event: { type: "content_block_delta", index: 0, delta: { type: "input_json_delta", partial_json: JSON.stringify(input) } }, ...meta });
  emit({ type: "stream_event", event: { type: "content_block_stop", index: 0 }, ...meta });
  const toolMessage = { id: messageId, type: "message", role: "assistant", model: modelId, content: [{ type: "tool_use", id: toolId, name: toolName, input }], stop_reason: "tool_use", usage };
  emit({ type: "assistant", message: toolMessage, ...meta });
  appendTranscript("assistant", toolMessage);

  let decision;
  if (valueOf("--permission-prompt-tool") !== "stdio") {
    // No approval surface: claude denies the call itself and says so.
    emit({ type: "system", subtype: "permission_denied", tool_name: toolName, tool_use_id: toolId, decision_reason_type: "mode", session_id: sessionId, uuid: randomUUID() });
    record({ kind: "permission_denied", pid: process.pid, sessionId, toolName });
    decision = { behavior: "deny", message: `Permission to use ${toolName} has been denied.` };
  } else {
    const requestId = `perm-${process.pid}-${permissionSeq}`;
    const request = { subtype: "can_use_tool", tool_name: toolName, display_name: toolName, input, tool_use_id: toolId };
    if (!asking) request.permission_suggestions = [{ type: "addRules", rules: [{ toolName: "Bash", ruleContent: input.command }], behavior: "allow", destination: "localSettings" }];
    record({ kind: "permission_request", pid: process.pid, sessionId, requestId, toolName });
    decision = await new Promise((resolve) => {
      permissionWait = { requestId, resolve };
      emit({ type: "control_request", request_id: requestId, request });
    });
  }
  if (decision === null) {
    // Withdrawn by an interrupt: the turn ends as stopped.
    emit({ type: "result", subtype: "error_during_execution", is_error: true, session_id: sessionId, num_turns: turn, duration_ms: 100, total_cost_usd: 0, usage: { input_tokens: 0, cache_creation_input_tokens: 0, cache_read_input_tokens: 0, output_tokens: 0 }, uuid: randomUUID() });
    return;
  }
  const allowed = decision.behavior === "allow";
  const answers = decision.updatedInput?.answers;
  const output = allowed ? (answers ? `answers: ${JSON.stringify(answers)}` : String(decision.updatedInput?.command ?? "")) : String(decision.message ?? "denied");
  const toolResult = { role: "user", content: [{ tool_use_id: toolId, type: "tool_result", content: output, is_error: !allowed }] };
  emit({ type: "user", message: toolResult, ...meta });
  appendTranscript("user", toolResult);
  const finalText = `E2E ${allowed ? "allowed" : "denied"}: ${userText}`;
  emit({ type: "stream_event", event: { type: "content_block_start", index: 1, content_block: { type: "text", text: "" } }, ...meta });
  emit({ type: "stream_event", event: { type: "content_block_delta", index: 1, delta: { type: "text_delta", text: finalText } }, ...meta });
  emit({ type: "stream_event", event: { type: "content_block_stop", index: 1 }, ...meta });
  const finalMessage = { id: `${messageId}_final`, type: "message", role: "assistant", model: modelId, content: [{ type: "text", text: finalText }], stop_reason: "end_turn", usage };
  emit({ type: "assistant", message: finalMessage, ...meta });
  appendTranscript("assistant", finalMessage);
  await sleep(100);
  emit({
    type: "result",
    subtype: "success",
    is_error: false,
    session_id: sessionId,
    result: finalText,
    num_turns: turn,
    duration_ms: 300,
    total_cost_usd: 0.001,
    permission_denials: allowed ? [] : [{ tool_name: toolName, tool_use_id: toolId, tool_input: input }],
    usage: { input_tokens: 12, cache_creation_input_tokens: 0, cache_read_input_tokens: 0, output_tokens: 7 },
    uuid: randomUUID(),
  });
}

/** Pi Web's `control_response` to the waiting permission request; any other id is ignored. */
function permissionAnswer(event) {
  const response = event.response ?? {};
  if (!permissionWait || response.request_id !== permissionWait.requestId) return;
  const waiting = permissionWait;
  permissionWait = null;
  waiting.resolve(response.response ?? {});
}

// The recording's own identifiers (lib/__fixtures__/claude-2.1.312/README.md, `image/`).
const RECORDED_SESSION_ID = "27000000-0000-4000-8000-000000000270";
const RECORDED_CWD = "/tmp/claude-img-rec";

function recordedImageStream() {
  const dir = process.env.PI_E2E_CLAUDE_IMAGE_FIXTURE;
  if (!dir) return null;
  try {
    const text = readFileSync(join(dir, "stream.jsonl"), "utf8")
      .split(RECORDED_SESSION_ID).join(sessionId)
      .split(RECORDED_CWD).join(cwd);
    return text.split("\n").filter(Boolean).map((line) => JSON.parse(line));
  } catch {
    return null;
  }
}

async function respondWithImage(content, userText) {
  const events = recordedImageStream();
  if (!events) return respond(userText || "[Image]");
  turn += 1;
  // The transcript holds the received image before anything is streamed (the order).
  appendTranscript("user", { role: "user", content }, { promptSource: "sdk" });
  const images = content.filter((block) => block?.type === "image");
  images.forEach((block, index) => {
    const ext = String(block.source?.media_type ?? "image/png").split("/")[1] ?? "png";
    appendTranscript(
      "user",
      { role: "user", content: [{ type: "text", text: `[Image: source: /tmp/claude-0/${cwd.replace(/[^A-Za-z0-9]/g, "-")}/${sessionId}/images/${index + 1}.${ext}]` }] },
      { isMeta: true, turnCompanion: true },
    );
  });
  for (const event of events) {
    if (event.type === "system" && event.subtype === "init") continue; // this child already sent its own
    if (event.type === "assistant") appendTranscript("assistant", event.message);
    if (event.type === "result") await sleep(50);
    emit(event);
    if (event.type === "stream_event") await sleep(20);
  }
}

/** claude's own `/compact` (the `compact/` recording's shape): transcript first, then the stream. */
async function compact() {
  const preTokens = 1000 * (turn + 1) + 234;
  const boundaryUuid = randomUUID();
  appendTranscript("system", undefined, {
    subtype: "compact_boundary",
    content: "Conversation compacted",
    isMeta: false,
    level: "info",
    uuid: boundaryUuid,
    parentUuid: null,
    logicalParentUuid: parentUuid,
    compactMetadata: { trigger: "manual", preTokens, postTokens: 321 },
  });
  appendTranscript("user", { role: "user", content: "This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\nE2E summary." }, { isVisibleInTranscriptOnly: true, isCompactSummary: true });
  appendTranscript("user", { role: "user", content: "<local-command-stdout>Compacted </local-command-stdout>" });
  const meta = { session_id: sessionId, uuid: randomUUID() };
  emit({ type: "system", subtype: "status", status: "compacting", ...meta });
  await sleep(300);
  emit({ type: "system", subtype: "status", status: null, compact_result: "success", ...meta });
  emit({
    type: "system",
    subtype: "compact_boundary",
    session_id: sessionId,
    uuid: boundaryUuid,
    compact_metadata: { trigger: "manual", pre_tokens: preTokens, post_tokens: 321 },
  });
  emit({ type: "user", message: { role: "user", content: "<local-command-stdout>Compacted </local-command-stdout>" }, session_id: sessionId, parent_tool_use_id: null, uuid: randomUUID(), isReplay: true });
  emit({
    type: "result",
    subtype: "success",
    is_error: false,
    duration_ms: 300,
    num_turns: 0,
    session_id: sessionId,
    total_cost_usd: 0,
    usage: { input_tokens: 0, cache_creation_input_tokens: 0, cache_read_input_tokens: 0, output_tokens: 0 },
    uuid: randomUUID(),
  });
}

// The `hooks/` recording's session id (lib/__fixtures__/claude-2.1.322/README.md).
const RECORDED_HOOKS_SESSION_ID = "44500000-0000-4000-8000-000000000445";

/** The recorded hook frames a `--include-hook-events` child sends in a turn; none without the flag. */
function recordedHookFrames() {
  if (!argv.includes("--include-hook-events")) return [];
  try {
    return readFileSync(join(process.env.PI_E2E_CLAUDE_HOOKS_FIXTURE ?? "", "stream.jsonl"), "utf8")
      .split(RECORDED_HOOKS_SESSION_ID).join(sessionId)
      .split("\n").filter(Boolean).map((line) => JSON.parse(line))
      .filter((frame) => frame.type === "system" && typeof frame.subtype === "string" && frame.subtype.startsWith("hook_"));
  } catch {
    return [];
  }
}

// The permission modes claude 2.1.322 takes (its `invalid_mode` refusal lists them).
const PERMISSION_MODES = ["acceptEdits", "auto", "bypassPermissions", "default", "dontAsk", "plan"];
let permissionMode = valueOf("--permission-mode") ?? "default";

/** claude 2.1.322's recorded `initialize` answer under `requestId`; an error without the recording. */
function initializeAnswer(requestId) {
  try {
    const frames = readFileSync(join(process.env.PI_E2E_CLAUDE_INITIALIZE_FIXTURE ?? "", "stream.jsonl"), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
    const answer = frames.find((frame) => frame.type === "control_response");
    const response = { ...answer.response.response };
    if (process.env.PI_E2E_CLAUDE_STUB_VERSION) response.claude_code_version = process.env.PI_E2E_CLAUDE_STUB_VERSION;
    return { type: "control_response", response: { ...answer.response, request_id: requestId, response } };
  } catch (error) {
    return { type: "control_response", response: { subtype: "error", request_id: requestId, error: `no initialize recording: ${error.message}` } };
  }
}

/** A control request answered as claude 2.1.312 answered it (`model-switch/`, `effort/`). */
function control(event) {
  const requestId = event.request_id;
  const request = event.request ?? {};
  if (request.subtype === "set_model") {
    if (!isModelName(request.model) || request.model === REFUSED_MODEL) {
      emit({ type: "control_response", response: { subtype: "error", request_id: requestId, error: "API error: 502 Upstream request failed · model not changed", error_code: "check_failed", model: model ?? "claude-default" } });
      return;
    }
    model = request.model;
    appendTranscript("user", { role: "user", content: "<local-command-caveat>The command below was run directly in Claude Code, not sent to you as a request, and its output goes straight to the user. It's recorded here as context for later messages.</local-command-caveat>" }, { isMeta: true });
    appendTranscript("user", { role: "user", content: `<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args>${model}</command-args>` });
    appendTranscript("user", { role: "user", content: `<local-command-stdout>Set model to \`${model}\`</local-command-stdout>` });
    emit({ type: "control_response", response: { subtype: "success", request_id: requestId, response: { model } } });
    return;
  }
  if (request.subtype === "apply_flag_settings") {
    if (typeof request.settings?.effortLevel === "string") effort = request.settings.effortLevel;
    record({ kind: "effort", pid: process.pid, sessionId, effort: effort ?? null });
    emit({ type: "control_response", response: { subtype: "success", request_id: requestId } });
    return;
  }
  if (request.subtype === "interrupt") {
    // A permission request still waiting is withdrawn first, as claude withdraws it.
    if (permissionWait) {
      const waiting = permissionWait;
      permissionWait = null;
      emit({ type: "control_cancel_request", request_id: waiting.requestId });
      waiting.resolve(null);
    }
    emit({ type: "control_response", response: { subtype: "success", request_id: requestId, response: {} } });
    return;
  }
  if (request.subtype === "initialize") {
    emit(initializeAnswer(requestId));
    return;
  }
  if (request.subtype === "set_permission_mode") {
    if (!PERMISSION_MODES.includes(request.mode)) {
      emit({ type: "control_response", response: { subtype: "error", request_id: requestId, error: `Cannot set permission mode: must be one of ${PERMISSION_MODES.join(", ")}`, error_code: "invalid_mode" } });
      return;
    }
    const changed = request.mode !== permissionMode;
    permissionMode = request.mode;
    emit({ type: "control_response", response: { subtype: "success", request_id: requestId, response: { mode: permissionMode } } });
    if (changed) emit({ type: "system", subtype: "status", status: null, permissionMode, uuid: randomUUID(), session_id: sessionId });
    return;
  }
  emit({ type: "control_response", response: { subtype: "error", request_id: requestId, error: `Unsupported control request subtype: ${String(request.subtype)}` } });
}

// Fixture modes: the recording's segments, consumed one per stdin line of the same kind.
const FIXTURE_ENV = { switch: "PI_E2E_CLAUDE_SWITCH_FIXTURE", effort: "PI_E2E_CLAUDE_EFFORT_FIXTURE", compact: "PI_E2E_CLAUDE_COMPACT_FIXTURE", edit: "PI_E2E_CLAUDE_EDIT_FIXTURE" };
const FIXTURE_IDS = {
  switch: ["27200000-0000-4000-8000-000000000001", "/tmp/claude-switch-rec"],
  effort: ["27200000-0000-4000-8000-000000000002", "/tmp/claude-effort-rec"],
  compact: ["27200000-0000-4000-8000-000000000003", "/tmp/claude-compact-rec"],
  edit: ["44400000-0000-4000-8000-000000000444", "/tmp/claude-rec-444-edit"],
};
const fixtureMode = Object.hasOwn(FIXTURE_ENV, process.env.PI_E2E_CLAUDE_STUB_MODE ?? "") ? process.env.PI_E2E_CLAUDE_STUB_MODE : null;
function fixtureSegments() {
  const dir = fixtureMode ? process.env[FIXTURE_ENV[fixtureMode]] : undefined;
  if (!dir) return null;
  try {
    const [recordedId, recordedCwd] = FIXTURE_IDS[fixtureMode];
    const events = readFileSync(join(dir, "stream.jsonl"), "utf8")
      .split(recordedId).join(sessionId)
      .split(recordedCwd).join(cwd)
      .split("\n").filter(Boolean).map((line) => JSON.parse(line));
    const segments = { turn: [], control: [] };
    let current = [];
    for (const event of events) {
      if (event.type === "control_response") {
        segments.control.push([event]);
        continue;
      }
      current.push(event);
      if (event.type === "result") {
        segments.turn.push(current);
        current = [];
      }
    }
    return segments;
  } catch {
    return null;
  }
}
const segments = fixtureSegments();

async function replayTurn(segment) {
  turn += 1;
  for (const event of segment) {
    if (event.type === "system" && event.subtype === "init") continue; // this child already sent its own
    if (event.type === "assistant") appendTranscript("assistant", event.message);
    // A tool result as claude writes it: the stream's receipt is the entry's toolUseResult.
    if (event.type === "user" && Array.isArray(event.message?.content) && event.message.content.some((block) => block?.type === "tool_result")) {
      appendTranscript("user", event.message, event.tool_use_result === undefined ? {} : { toolUseResult: event.tool_use_result });
    }
    if (event.type === "system" && event.subtype === "compact_boundary") {
      appendTranscript("system", undefined, { subtype: "compact_boundary", content: "Conversation compacted", isMeta: false, level: "info", compactMetadata: { trigger: "manual", preTokens: event.compact_metadata?.pre_tokens, postTokens: event.compact_metadata?.post_tokens } });
    }
    if (event.type === "result") await sleep(50);
    emit(event);
    if (event.type === "stream_event") await sleep(10);
  }
}

const imageMode = process.env.PI_E2E_CLAUDE_STUB_MODE === "image";
const hasImage = (event) => Array.isArray(event?.message?.content) && event.message.content.some((block) => block?.type === "image");

function userTextOf(event) {
  const content = event?.message?.content;
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content.filter((block) => block?.type === "text").map((block) => block.text ?? "").join("\n");
}

if (!probing) emit({
  type: "system",
  subtype: "init",
  cwd,
  session_id: sessionId,
  tools: ["Read", "Glob", "Grep"],
  model: model ?? "claude-default",
  permissionMode: "dontAsk",
  slash_commands: [],
  uuid: randomUUID(),
});

let chain = Promise.resolve();
let buffer = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  buffer += chunk;
  let newline = buffer.indexOf("\n");
  while (newline >= 0) {
    const line = buffer.slice(0, newline).trim();
    buffer = buffer.slice(newline + 1);
    newline = buffer.indexOf("\n");
    if (!line) continue;
    record({ kind: probing ? "probe-stdin" : "stdin", pid: process.pid, line });
    let event;
    try {
      event = JSON.parse(line);
    } catch {
      continue;
    }
    if (event?.type === "control_request" && event.request?.subtype === "initialize") {
      control(event);
      continue;
    }
    if (event?.type === "control_request") {
      const recorded = segments?.control.shift();
      if (recorded) emit({ ...recorded[0], response: { ...recorded[0].response, request_id: event.request_id } });
      else control(event);
      continue;
    }
    if (event?.type === "control_response") {
      permissionAnswer(event);
      continue;
    }
    if (event?.type !== "user") continue;
    if (/^(perm|ask): /.test(userTextOf(event).trim())) {
      // A permission turn is synthesised in every mode, never a recording's segment.
      const permissionText = userTextOf(event).trim();
      chain = chain.then(() => permissionTurn(permissionText)).catch(() => {});
      continue;
    }
    if (userTextOf(event).trim().startsWith("retry: ")) {
      // The retry turn is the synthesised answer in every mode, never a recording's segment.
      const retryText = userTextOf(event).trim();
      chain = chain.then(() => respond(retryText)).catch(() => {});
      continue;
    }
    if (segments && segments.turn.length > 0) {
      const segment = segments.turn.shift();
      const text = userTextOf(event).trim();
      if (text !== "/compact") appendTranscript("user", { role: "user", content: text }, { promptSource: "sdk" });
      chain = chain.then(() => replayTurn(segment)).catch(() => {});
      continue;
    }
    if (userTextOf(event).trim() === "/compact") {
      chain = chain.then(() => compact()).catch(() => {});
      continue;
    }
    if (imageMode && hasImage(event)) {
      const content = event.message.content;
      const imageText = userTextOf(event).trim();
      chain = chain.then(() => respondWithImage(content, imageText)).catch(() => {});
      continue;
    }
    const text = userTextOf(event).trim();
    if (!text) continue;
    chain = chain.then(() => respond(text)).catch(() => {});
  }
});
process.stdin.on("end", () => {
  chain = chain.then(() => { recordExit(); process.exit(0); });
});