// The only module that knows claude's native formats: claude
// 2.1.312's stream-json stdout events and its jsonl transcript, translated into
// the pi event vocabulary (lib/agent-event-wire.ts) and pi messages the chat
// already renders. Pure: no I/O. Contract-tested on the real recordings in
// lib/__fixtures__/claude-2.1.312/.
//
// Observed dialect (see the fixtures' README): with --include-partial-messages,
// `stream_event` wraps the API's message_start / content_block_* / message_*
// events; claude then emits one `assistant` event per finished content block
// (all with the API message's id), usually before that block's
// content_block_stop; tool results come back as a `user` event of tool_result
// blocks; a turn ends with one `result` event carrying the aggregate usage.
import type { AgentEventLike } from "./agent-event-wire";
import { CLAUDE_BACKGROUND_TASK_EVENT, type ClaudeBackgroundTask } from "./claude-background-task-view";
import { CLAUDE_PROVIDER, type ClaudeDelivery } from "./claude-shared";
import {
  CLAUDE_TASK_NOTIFICATION_TYPE,
  isClaudeSubagentToolName,
  type ClaudeSubagentEntry,
  type ClaudeSubagentRun,
  type ClaudeSubagentStatus,
  type ClaudeSubagentUsage,
} from "./claude-subagent-view";
import type { AgentMessage } from "./types";

/** pi's message shapes as the SDK writes them (tool calls as id / name / arguments). */
type PiTextBlock = { type: "text"; text: string };
type PiThinkingBlock = { type: "thinking"; thinking: string };
type PiToolCallBlock = { type: "toolCall"; id: string; name: string; arguments: Record<string, unknown> };
type PiImageBlock = { type: "image"; data: string; mimeType: string };
type PiAssistantBlock = PiTextBlock | PiThinkingBlock | PiToolCallBlock;

export interface PiAssistantMessage {
  role: "assistant";
  content: PiAssistantBlock[];
  api: string;
  provider: string;
  model: string;
  stopReason?: string;
  timestamp: number;
}

export interface PiToolResultMessage {
  role: "toolResult";
  toolCallId: string;
  toolName: string;
  content: (PiTextBlock | PiImageBlock)[];
  isError: boolean;
  timestamp: number;
  /** pi's tool details: `diff`, a unified patch of claude's `structuredPatch`. */
  details?: { diff: string };
}

export interface PiUserMessage {
  role: "user";
  content: string | (PiTextBlock | PiImageBlock)[];
  timestamp: number;
  /** A message sent while a turn ran: what claude did with it. */
  claudeDelivery?: ClaudeDelivery;
}

export type PiMessage = PiAssistantMessage | PiToolResultMessage | PiUserMessage;

/** A turn's `result` aggregate: the only source of a claude session's usage. */
export interface ClaudeTurnUsage {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  cost: number;
}

export interface ClaudeTurnEnd {
  usage: ClaudeTurnUsage;
  /** claude's own error text when the turn failed (an authentication failure, say). */
  error?: string;
}

/** claude's answer to one control request: never a chat message. */
export interface ClaudeControlResponse {
  requestId: string;
  subtype: "success" | "error";
  /** claude's own error text, for an error. */
  error?: string;
  /** claude's own error code (`check_failed` for a model it refused), for an error. */
  errorCode?: string;
  /** The request's result (`{ model }` for an accepted `set_model`). */
  response?: Record<string, unknown>;
}

export interface ClaudeTranslation {
  events: AgentEventLike[];
  /** Set by a `control_response`: the answer to the control request with its id. */
  control?: ClaudeControlResponse;
  /** Set by `compact_boundary`: claude compacted; its pre-compaction token count when it gave one. */
  compactBoundary?: { tokensBefore?: number };
  /** Set by the `result` event that ends a turn. */
  turnEnd?: ClaudeTurnEnd;
  /**
   * Set by the `init` that starts claude's own follow-up turn after a background
   * subagent's task notification: a turn no user message started (no `started`
   * lifecycle came before it). Its events open with the hidden turn
   * anchor (claudeTaskNotificationAnchor()).
   */
  claudeTurnStart?: true;
  /** Set by claude's `command_lifecycle` frame for a user message pi-web wrote with a uuid. */
  lifecycle?: ClaudeMessageLifecycle;
  /** Set by claude's `control_request` / `can_use_tool`: a tool call waiting for the user's answer. */
  permissionRequest?: ClaudePermissionRequest;
  /** Set by claude's `control_cancel_request`: the id of the request it withdrew. */
  permissionCancel?: string;
  /** Set by claude's `system` / `permission_denied`: a call claude denied on its own. */
  permissionDenied?: ClaudePermissionDenied;
}

/**
 * claude's `can_use_tool` request (claude 2.1.322; docs/agents/claude-sessions.md):
 * the fields pi-web reads, claude's other fields kept as they came.
 */
export interface ClaudeCanUseToolRequest {
  subtype: "can_use_tool";
  tool_name: string;
  input: Record<string, unknown>;
  tool_use_id?: string;
  display_name?: string;
  title?: string;
  description?: string;
  decision_reason?: unknown;
  /** claude's "always allow" rule updates (`PermissionUpdate[]`), sent back as `updatedPermissions`. */
  permission_suggestions?: Record<string, unknown>[];
  suppress_always_allow_rule?: boolean;
  [key: string]: unknown;
}

export interface ClaudePermissionRequest {
  requestId: string;
  request: ClaudeCanUseToolRequest;
}

export interface ClaudePermissionDenied {
  toolName: string;
  toolUseId: string;
  /** claude's `decision_reason_type` / `decision_reason_code`, when it gave either. */
  reason?: string;
}

/**
 * claude's own word on a user message pi-web wrote (claude 2.1.319:
 * lib/__fixtures__/claude-2.1.319/README.md): `queued` when it read the line, `started`
 * when it took the message in — inside the running turn (`absorbed`) or as a turn of its
 * own — and `completed` / `cancelled` after the turn that carried it.
 */
export interface ClaudeMessageLifecycle {
  uuid: string;
  state: "queued" | "started" | "completed" | "cancelled";
  /** A `started` inside a running turn: claude folded the message into it. */
  absorbed?: true;
}

const CLAUDE_LIFECYCLE_STATES: ReadonlySet<string> = new Set(["queued", "started", "completed", "cancelled"]);

/** The live wire event carrying a subagent run's header and its new entries. */
export const CLAUDE_SUBAGENT_EVENT = "claude_subagent";

/**
 * The one extension status key claude's hook activity uses: pi's
 * `setStatus` line above the composer, replaced by each finished hook.
 */
export const CLAUDE_HOOK_STATUS_KEY = "claude-hooks";
/** A failed hook's notice keeps at most this much of its first output line. */
const CLAUDE_HOOK_NOTICE_OUTPUT_MAX = 200;


export const CLAUDE_API = "claude-code";

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function num(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function timestampOf(value: unknown, fallback: number): number {
  const parsed = typeof value === "string" ? Date.parse(value) : NaN;
  return Number.isFinite(parsed) ? parsed : fallback;
}

/** The `result` event's aggregate (never the streamed per-message usage, which under-counts). */
export function claudeResultUsage(event: unknown): ClaudeTurnUsage {
  const raw = isObject(event) ? event : {};
  const usage = isObject(raw.usage) ? raw.usage : {};
  return {
    input: num(usage.input_tokens),
    output: num(usage.output_tokens),
    cacheRead: num(usage.cache_read_input_tokens),
    cacheWrite: num(usage.cache_creation_input_tokens),
    cost: num(raw.total_cost_usd),
  };
}

/** The four counts of one API response's usage; null when there is no usage object. */
interface ClaudeResponseCounts {
  input: number;
  cacheWrite: number;
  cacheRead: number;
  output: number;
}

function responseCounts(usage: unknown): ClaudeResponseCounts | null {
  if (!isObject(usage)) return null;
  return {
    input: num(usage.input_tokens),
    cacheWrite: num(usage.cache_creation_input_tokens),
    cacheRead: num(usage.cache_read_input_tokens),
    output: num(usage.output_tokens),
  };
}

function countsTotal(counts: ClaudeResponseCounts): number | null {
  const total = counts.input + counts.cacheWrite + counts.cacheRead + counts.output;
  return total > 0 ? total : null;
}

/**
 * The context one API response of the conversation filled: its prompt
 * (input, cache write, cache read) plus its output. That is the count claude reports
 * itself: in the compact/ recording the last reply's 2 + 3283 + 13449 + 5 is the
 * compaction's `pre_tokens`, 16739. null for a usage that counts nothing (claude's
 * synthetic and API-error replies carry all-zero usage) or no usage at all.
 */
export function claudeContextTokens(usage: unknown): number | null {
  const counts = responseCounts(usage);
  return counts ? countsTotal(counts) : null;
}

/**
 * The context windows a `result` reports: `modelUsage[<model>].contextWindow`,
 * claude's own window for each model the child has used — the window its own compaction
 * counts against, which depends on the account as well as the model. Keyed by the
 * `modelUsage` key (the full model name in every 2.1.312 recording); a missing or
 * non-positive window is left out.
 */
export function claudeReportedContextWindows(event: unknown): Map<string, number> {
  const windows = new Map<string, number>();
  const modelUsage = isObject(event) && isObject(event.modelUsage) ? event.modelUsage : {};
  for (const [model, usage] of Object.entries(modelUsage)) {
    const contextWindow = isObject(usage) ? usage.contextWindow : undefined;
    if (typeof contextWindow === "number" && Number.isFinite(contextWindow) && contextWindow > 0) windows.set(model, contextWindow);
  }
  return windows;
}

/** A compaction's own report of the context left (`post_tokens` / `postTokens`); null when it gave none. */
function compactedTokens(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : null;
}

/**
 * The context a transcript's main conversation fills, counted as the
 * live translator counts it: the last reply with a usage, or what claude reported its
 * last compaction left when that came later (null when it reported nothing). Undefined
 * when the transcript has neither. Subagent (sidechain) entries are another context, and
 * so is a branch an in-session edit left: only the active branch counts.
 */
export function claudeTranscriptContextTokens(entries: readonly unknown[]): number | null | undefined {
  let tokens: number | null | undefined;
  for (const entry of claudeActiveEntries(entries)) {
    if (!isObject(entry) || entry.isSidechain === true) continue;
    if (entry.type === "system" && entry.subtype === "compact_boundary") {
      tokens = compactedTokens(isObject(entry.compactMetadata) ? entry.compactMetadata.postTokens : undefined);
    } else if (entry.type === "assistant" && isObject(entry.message)) {
      tokens = claudeContextTokens(entry.message.usage) ?? tokens;
    }
  }
  return tokens;
}

function parseJsonObject(text: string): Record<string, unknown> {
  try {
    const parsed: unknown = JSON.parse(text);
    return isObject(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

/** claude content blocks → pi assistant blocks; empty (redacted) thinking and unknown blocks are left out. */
function toAssistantBlocks(content: unknown): PiAssistantBlock[] {
  if (typeof content === "string") return content ? [{ type: "text", text: content }] : [];
  if (!Array.isArray(content)) return [];
  const blocks: PiAssistantBlock[] = [];
  for (const block of content) {
    if (!isObject(block)) continue;
    if (block.type === "text" && typeof block.text === "string") {
      blocks.push({ type: "text", text: block.text });
    } else if (block.type === "thinking" && typeof block.thinking === "string" && block.thinking) {
      blocks.push({ type: "thinking", thinking: block.thinking });
    } else if (block.type === "tool_use" && typeof block.id === "string") {
      blocks.push({
        type: "toolCall",
        id: block.id,
        name: typeof block.name === "string" ? block.name : "tool",
        arguments: isObject(block.input) ? block.input : {},
      });
    }
  }
  return blocks;
}

function toResultContent(content: unknown): (PiTextBlock | PiImageBlock)[] {
  if (typeof content === "string") return [{ type: "text", text: content }];
  if (!Array.isArray(content)) return [];
  const blocks: (PiTextBlock | PiImageBlock)[] = [];
  for (const block of content) {
    if (!isObject(block)) continue;
    if (block.type === "text" && typeof block.text === "string") blocks.push({ type: "text", text: block.text });
    else if (block.type === "image" && isObject(block.source) && typeof block.source.data === "string") {
      blocks.push({ type: "image", data: block.source.data, mimeType: typeof block.source.media_type === "string" ? block.source.media_type : "image/png" });
    }
  }
  return blocks;
}

function toolResultBlocks(content: unknown): Record<string, unknown>[] {
  return Array.isArray(content) ? content.filter((block): block is Record<string, unknown> => isObject(block) && block.type === "tool_result") : [];
}

/**
 * claude's receipt `structuredPatch` (`[{oldStart, oldLines, newStart, newLines, lines[]}]`,
 * on an `Edit` and a `Write` update, claude 2.1.322) as a unified patch of its `filePath`;
 * undefined without a non-empty patch.
 */
function claudeReceiptDiff(receipt: unknown): string | undefined {
  if (!isObject(receipt) || !Array.isArray(receipt.structuredPatch) || typeof receipt.filePath !== "string" || !receipt.filePath) return undefined;
  const hunks: string[] = [];
  for (const hunk of receipt.structuredPatch) {
    if (!isObject(hunk) || !Array.isArray(hunk.lines)) return undefined;
    const { oldStart, oldLines, newStart, newLines } = hunk;
    if (![oldStart, oldLines, newStart, newLines].every((value) => typeof value === "number" && Number.isInteger(value) && value >= 0)) return undefined;
    if (!hunk.lines.every((line) => typeof line === "string")) return undefined;
    hunks.push(`@@ -${oldStart},${oldLines} +${newStart},${newLines} @@`, ...(hunk.lines as string[]));
  }
  if (hunks.length === 0) return undefined;
  // The path verbatim: claude's is absolute, which an a/ b/ prefix would garble in the card's header.
  return [`--- ${receipt.filePath}`, `+++ ${receipt.filePath}`, ...hunks].join("\n") + "\n";
}

/**
 * A tool_result block → pi's tool result. `receipt` is the entry's own result record
 * (the stream's `tool_use_result`, the transcript's `toolUseResult`); it is one per
 * entry, so it is read only when the entry holds one tool_result.
 */
function toToolResult(block: Record<string, unknown>, toolNames: Map<string, string>, timestamp: number, receipt?: unknown): PiToolResultMessage {
  const toolCallId = typeof block.tool_use_id === "string" ? block.tool_use_id : "";
  const diff = claudeReceiptDiff(receipt);
  return {
    role: "toolResult",
    toolCallId,
    toolName: toolNames.get(toolCallId) ?? "tool",
    content: toResultContent(block.content),
    // A permission denial arrives as an is_error result naming the denial.
    isError: block.is_error === true,
    timestamp,
    ...(diff !== undefined ? { details: { diff } } : {}),
  };
}

/** claude's task status → the run's state; undefined for a word that is no state (kept as it was). */
function claudeTaskStatus(value: unknown): ClaudeSubagentStatus | undefined {
  switch (value) {
    case "running":
    case "pending":
    case "async_launched":
      return "running";
    case "completed":
    case "success":
      return "completed";
    case "failed":
    case "error":
      return "failed";
    case "killed":
    case "stopped":
    case "cancelled":
    case "canceled":
    case "aborted":
      return "stopped";
    default:
      return undefined;
  }
}

function claudeTaskUsage(value: unknown): ClaudeSubagentUsage | undefined {
  if (!isObject(value)) return undefined;
  return { totalTokens: num(value.total_tokens), toolUses: num(value.tool_uses), durationMs: num(value.duration_ms) };
}

function textOf(content: unknown): string {
  if (typeof content === "string") return content;
  return toResultContent(content).flatMap((block) => (block.type === "text" ? [block.text] : [])).join("\n");
}

/** A new run from its `Agent` / `Task` tool call, starting at the time of the entry or frame holding it (0: unknown). */
function runFromToolCall(block: PiToolCallBlock, at: number): ClaudeSubagentRun {
  const input = block.arguments;
  return {
    toolCallId: block.id,
    ...(at > 0 ? { startedAt: at } : {}),
    ...(typeof input.subagent_type === "string" && input.subagent_type ? { agentType: input.subagent_type } : {}),
    ...(typeof input.description === "string" && input.description ? { description: input.description } : {}),
    ...(typeof input.prompt === "string" && input.prompt ? { prompt: input.prompt } : {}),
    background: input.run_in_background === true,
    status: "running",
    entries: [],
  };
}

/**
 * What an `Agent` / `Task` tool result says about its run: an async launch receipt
 * (`status: async_launched`) names the agent and keeps the run running; a finished
 * foreground run carries its result text and statistics.
 */
function runFromToolResult(toolCallId: string, receipt: unknown, content: unknown, isError: boolean): Partial<ClaudeSubagentRun> {
  const raw = isObject(receipt) ? receipt : {};
  const update: Partial<ClaudeSubagentRun> = { toolCallId };
  if (typeof raw.agentId === "string" && raw.agentId) update.agentId = raw.agentId;
  if (raw.isAsync === true || raw.status === "async_launched") {
    update.background = true;
    return update;
  }
  update.status = isError ? "failed" : (claudeTaskStatus(raw.status) ?? "completed");
  const text = textOf(raw.content ?? content).trim();
  if (text) update.result = text;
  if (num(raw.totalTokens) || num(raw.totalToolUseCount) || num(raw.totalDurationMs)) {
    update.usage = { totalTokens: num(raw.totalTokens), toolUses: num(raw.totalToolUseCount), durationMs: num(raw.totalDurationMs) };
  }
  return update;
}

/**
 * One subagent transcript / stream entry → its timeline entries, as the main
 * conversion maps them (an assistant reply, its tool results, a user text), each
 * keyed by claude's entry uuid (`<uuid>:<n>` for the 2nd+ tool result of an entry).
 * The live stream and the sidecar carry the same uuid, which is what makes both
 * paths yield the same entries.
 */
function subagentEntries(
  type: unknown,
  message: Record<string, unknown>,
  id: string,
  at: number,
  toolNames: Map<string, string>,
  model: string,
  receipt?: unknown,
): ClaudeSubagentEntry[] {
  if (type === "assistant") {
    const content = toAssistantBlocks(message.content);
    for (const block of content) if (block.type === "toolCall") toolNames.set(block.id, block.name);
    if (content.length === 0) return [];
    const assistant: PiAssistantMessage = {
      role: "assistant",
      content,
      api: CLAUDE_API,
      provider: CLAUDE_PROVIDER,
      model: typeof message.model === "string" && message.model ? message.model : model,
      ...(typeof message.stop_reason === "string" ? { stopReason: message.stop_reason } : {}),
      timestamp: at,
    };
    return [{ id, message: assistant as unknown as ClaudeSubagentEntry["message"] }];
  }
  const results = toolResultBlocks(message.content);
  if (results.length > 0) {
    return results.map((block, index) => ({
      id: index === 0 ? id : `${id}:${index}`,
      message: toToolResult(block, toolNames, at, results.length === 1 ? receipt : undefined) as unknown as ClaudeSubagentEntry["message"],
    }));
  }
  const content = typeof message.content === "string" ? message.content : toResultContent(message.content);
  if (typeof content === "string" ? !content : content.length === 0) return [];
  const user: PiUserMessage = { role: "user", content, timestamp: at };
  return [{ id, message: user as unknown as ClaudeSubagentEntry["message"] }];
}

/** The hidden custom message that starts claude's follow-up turn; it renders no bubble. */
export function claudeTaskNotificationAnchor(toolCallIds: readonly string[], timestamp: number): AgentMessage {
  return {
    role: "custom",
    customType: CLAUDE_TASK_NOTIFICATION_TYPE,
    content: "",
    display: false,
    details: { toolCallIds: [...toolCallIds] },
    timestamp,
  } as unknown as AgentMessage;
}

/** An image a prompt carries, as the composer sends it (pi's shape). */
export type ClaudePromptImage = { data: string; mimeType: string };

/**
 * The pi events a prompt pi-web wrote to claude's stdin is announced with: the text
 * alone as a string, or with images the text block (when any) then the image blocks,
 * pi's live user-message shape.
 */
export function claudeUserPromptEvents(
  text: string,
  images: readonly ClaudePromptImage[] = [],
  timestamp = Date.now(),
  delivery?: ClaudeDelivery,
): AgentEventLike[] {
  const content: PiUserMessage["content"] = images.length === 0
    ? text
    : [...(text ? [{ type: "text" as const, text }] : []), ...images.map((image) => ({ type: "image" as const, data: image.data, mimeType: image.mimeType }))];
  const message: PiUserMessage = { role: "user", content, timestamp, ...(delivery ? { claudeDelivery: delivery } : {}) };
  return [{ type: "message_start", message }, { type: "message_end", message }];
}

/**
 * The stdin line that delivers a user message (stream-json input). Text alone is a
 * string content; with images the content is the text block (omitted
 * when empty) then one base64 image block per image, data and media type unchanged —
 * the content of the line claude 2.1.312 accepted in
 * lib/__fixtures__/claude-2.1.312/image/stdin.jsonl; with `uuid` the line also carries it at
 * the top level, so claude reports the message's command_lifecycle.
 */
export function claudeUserInputLine(text: string, images: readonly ClaudePromptImage[] = [], uuid?: string): string {
  const content = images.length === 0
    ? text
    : [
      ...(text ? [{ type: "text", text }] : []),
      ...images.map((image) => ({ type: "image", source: { type: "base64", media_type: image.mimeType, data: image.data } })),
    ];
  // A uuid makes claude report the message's lifecycle (command_lifecycle).
  return `${JSON.stringify({ type: "user", ...(uuid ? { uuid } : {}), message: { role: "user", content } })}\n`;
}

/**
 * The stdin line of Stop's stream-json interrupt control request. It ends the turn only:
 * `spare_background_tasks` leaves claude's background agents and tasks
 * running — a plain interrupt stops its background agents too (2.1.319 idle-interrupt/);
 * claude 2.1.322's schema: "this Stop ends the turn and leaves running background agents
 * and workflows alone", and a CLI that does not read it treats it as a plain interrupt.
 */
export function claudeInterruptLine(requestId: string): string {
  return `${JSON.stringify({ type: "control_request", request_id: requestId, request: { subtype: "interrupt", spare_background_tasks: true } })}\n`;
}

/** The stdin line of a `set_model` control request, as claude 2.1.312 accepted it. */
export function claudeSetModelLine(requestId: string, model: string): string {
  return `${JSON.stringify({ type: "control_request", request_id: requestId, request: { subtype: "set_model", model } })}\n`;
}

/**
 * The stdin line that changes a live child's effort: `apply_flag_settings`
 * with `effortLevel`, which the recording shows reaching the next request's effort.
 */
export function claudeSetEffortLine(requestId: string, effort: string): string {
  return `${JSON.stringify({ type: "control_request", request_id: requestId, request: { subtype: "apply_flag_settings", settings: { effortLevel: effort } } })}\n`;
}

/**
 * The stdin line that switches a live child's permission mode: claude's own
 * `set_permission_mode` control request. claude 2.1.322 answers `{ mode }` (plus a `status`
 * frame with `permissionMode` when the mode changed) or `invalid_mode`
 * (lib/__fixtures__/claude-2.1.322/permission-mode/).
 */
export function claudeSetPermissionModeLine(requestId: string, mode: string): string {
  return `${JSON.stringify({ type: "control_request", request_id: requestId, request: { subtype: "set_permission_mode", mode } })}\n`;
}

/**
 * The stdin line that moves a running foreground `Agent` / `Task` call to the background:
 * claude's own `background_tasks` control request, the TUI's Ctrl+B. claude
 * answers `{ backgrounded: true }` and the call returns its async receipt; the relay recording
 * (lib/__fixtures__/claude-2.1.322/relay/) shows it.
 */
export function claudeBackgroundTaskLine(requestId: string, toolUseId: string): string {
  return `${JSON.stringify({ type: "control_request", request_id: requestId, request: { subtype: "background_tasks", tool_use_id: toolUseId } })}\n`;
}

/**
 * The stdin line that stops one running task: claude's own `stop_task`
 * control request (2.1.322 schema: "Stops a running task."), answered `{}`.
 */
export function claudeStopTaskLine(requestId: string, taskId: string): string {
  return `${JSON.stringify({ type: "control_request", request_id: requestId, request: { subtype: "stop_task", task_id: taskId } })}\n`;
}

/**
 * The stdin line that reads a background shell or Monitor task's output:
 * claude's own `get_task_output` control request (2.1.322 schema: at most the last 8 KiB,
 * "a host polls it while the task runs"), answered `{ output, total_bytes, truncated }`.
 */
export function claudeTaskOutputLine(requestId: string, taskId: string): string {
  return `${JSON.stringify({ type: "control_request", request_id: requestId, request: { subtype: "get_task_output", task_id: taskId } })}\n`;
}

/**
 * The stdin line that answers a control request claude sent: claude's
 * `control_response` envelope with `response` (a `can_use_tool` decision) as its result.
 */
export function claudeControlSuccessLine(requestId: string, response: Record<string, unknown>): string {
  return `${JSON.stringify({ type: "control_response", response: { subtype: "success", request_id: requestId, response } })}\n`;
}

/** A `control_request` frame → claude's `can_use_tool` request with its id; null for anything else. */
function claudePermissionRequest(raw: Record<string, unknown>): ClaudePermissionRequest | null {
  const request = raw.request;
  if (typeof raw.request_id !== "string" || !raw.request_id || !isObject(request)) return null;
  if (request.subtype !== "can_use_tool" || typeof request.tool_name !== "string" || !request.tool_name) return null;
  return {
    requestId: raw.request_id,
    request: { ...request, input: isObject(request.input) ? request.input : {} } as ClaudeCanUseToolRequest,
  };
}

/** A `system` / `permission_denied` frame → the denied call; null when it names no tool or call. */
function claudePermissionDenied(raw: Record<string, unknown>): ClaudePermissionDenied | null {
  if (typeof raw.tool_name !== "string" || !raw.tool_name || typeof raw.tool_use_id !== "string") return null;
  const reason = [raw.decision_reason_type, raw.decision_reason_code]
    .filter((part): part is string => typeof part === "string" && part.length > 0)
    .join("/");
  return { toolName: raw.tool_name, toolUseId: raw.tool_use_id, ...(reason ? { reason } : {}) };
}

/** The stdin line of claude's own compaction: the bare `/compact`, no instructions. */
export function claudeCompactLine(): string {
  return claudeUserInputLine("/compact");
}

/**
 * claude's transcript compaction boundary (`system` / `compact_boundary`) as pi's
 * compaction card: the marker and claude's pre-compaction token count, no summary body
 * (claude's summary stays its own hidden entry).
 */
export function claudeCompactionMarker(entry: Record<string, unknown>, timestamp: number): AgentMessage {
  const metadata = isObject(entry.compactMetadata) ? entry.compactMetadata : {};
  const tokensBefore = typeof metadata.preTokens === "number" && Number.isFinite(metadata.preTokens) ? metadata.preTokens : undefined;
  return {
    role: "custom",
    customType: "compaction",
    content: "",
    display: true,
    details: tokensBefore === undefined ? {} : { tokensBefore },
    timestamp,
  } as unknown as AgentMessage;
}

/** A `control_response` event → its answer; null for anything else. */
export function claudeControlResponse(raw: unknown): ClaudeControlResponse | null {
  if (!isObject(raw) || raw.type !== "control_response" || !isObject(raw.response)) return null;
  const response = raw.response;
  if (typeof response.request_id !== "string") return null;
  const error = response.subtype !== "success";
  return {
    requestId: response.request_id,
    subtype: error ? "error" : "success",
    ...(error ? { error: typeof response.error === "string" && response.error ? response.error : "claude refused the request" } : {}),
    ...(error && typeof response.error_code === "string" ? { errorCode: response.error_code } : {}),
    ...(isObject(response.response) ? { response: response.response } : {}),
  };
}

interface PartialBlock {
  local: number;
  kind: "text" | "thinking" | "toolCall";
  rawJson: string;
}

/**
 * Translates one claude child's stdout, event by event, into pi events. Keeps the
 * reply being streamed (`streamingMessage`) for a reconnecting page's snapshot.
 */
export class ClaudeStreamTranslator {
  /** The session id claude reported (init / every event); null until the first. */
  sessionId: string | null = null;
  /** The pi assistant message being streamed, or null between replies. */
  streamingMessage: PiAssistantMessage | null = null;
  private model = "";
  private blocks = new Map<number, PartialBlock>();
  private toolNames = new Map<string, string>();
  /** When each main tool call was made (its assistant frame's timestamp): a resume starts at its `SendMessage` call. */
  private callTimes = new Map<string, number>();
  /** Every subagent run seen, by its tool call id (header data; entries go out as they arrive). */
  private runs = new Map<string, ClaudeSubagentRun>();
  /** claude's task id → the tool call that started it. */
  private taskRuns = new Map<string, string>();
  /** Each `Bash` / `Monitor` call's tool name and `command`: a `local_bash` task's kind and command. */
  private shellCalls = new Map<string, { name: string; command: string }>();
  /**
   * claude's `local_bash` tasks by task id: never subagent runs. A task is
   * listed (`background`, its snapshots go out as `claude_background_task`) once it runs
   * in the background; a foreground one is only tracked until claude backgrounds it.
   */
  private tasks = new Map<string, { task: ClaudeBackgroundTask; background: boolean }>();
  /**
   * Background runs notified since the last `init`: claude starts a follow-up turn for
   * them, or took them into the running turn (`absorbed_mid_turn`), which only the next
   * user turn's `init` shows. A background task's notification joins
   * them by its tool call id.
   */
  private notified: string[] = [];
  /** Background runs (and notified background tasks) whose follow-up turn has not started yet. */
  private unsettled = new Set<string>();
  /** Background runs whose follow-up turn has started. */
  private followedUp = new Set<string>();
  /** Runs resumed by an awaited `SendMessage`: its caller takes the report inline, no follow-up turn comes. */
  private awaitedResumes = new Set<string>();
  /** Between an `init` and its `result`: claude is inside a turn. */
  private inTurn = false;
  /** A user message claude `started` outside a turn: the next `init` opens that message's turn. */
  private userTurnStarting = false;
  /**
   * The context the main conversation fills as of its latest API response
   * (claudeContextTokens()); after a compaction, what claude reported it
   * left, or null when it reported nothing. Undefined until this child reported either.
   */
  contextTokens: number | null | undefined = undefined;
  /** claude's own window per model, from this child's latest `result` that reported one. */
  private readonly contextWindows = new Map<string, number>();
  /** The main conversation's API message last counted: its id and its counts so far. */
  private counted: { id: string | undefined; counts: ClaudeResponseCounts } | null = null;
  /**
   * claude's retry wait: the attempt of its last `api_retry`, until the
   * retried request streams or the turn ends; null when none is open.
   */
  private retryAttempt: number | null = null;
  /** claude's own compaction: open from its `compacting` status to the boundary, a failure or the turn's end. */
  private compacting = false;
  /** A hook status line is shown: the turn's end clears it. */
  private hookStatusShown = false;
  /** Counts hook requests without claude's uuid, for their ids. */
  private hookRequests = 0;

  constructor(private readonly now: () => number = Date.now) {}

  /**
   * The session's runs as its transcript holds them (claudeTranscriptToMessages()), for a
   * child spawned for an existing session: claude's later task events for an
   * agent it knows — a `SendMessage` resuming a finished one names that call, not the
   * `Agent` call — then land on the original run instead of starting one of their own.
   * Headers only; nothing is announced until claude reports on a run.
   */
  seedRuns(runs: readonly ClaudeSubagentRun[]): void {
    for (const run of runs) {
      if (this.runs.has(run.toolCallId)) continue;
      this.runs.set(run.toolCallId, { ...run, entries: [] });
      if (run.agentId && !this.taskRuns.has(run.agentId)) this.taskRuns.set(run.agentId, run.toolCallId);
    }
  }

  /**
   * A background subagent still running, or one that finished — or a background task
   * that finished — whose follow-up turn claude has not started yet: the
   * session is not settled. A background task still running is not counted.
   */
  hasUnsettledSubagents(): boolean {
    return this.unsettled.size > 0;
  }

  /** A background task (`Bash` in the background, `Monitor`) claude still runs. */
  hasRunningBackgroundTasks(): boolean {
    return this.runningBackgroundTasks().length > 0;
  }

  /**
   * The background subagents this child reported running and claude has not followed up,
   * in start order: each `Agent` call id and its label (description, else
   * agent type). A run only seeded from the transcript is not one this child runs.
   */
  runningBackgroundSubagents(): { id: string; label: string }[] {
    return [...this.runs.values()]
      .filter((run) => run.background && run.status === "running" && this.unsettled.has(run.toolCallId))
      .map((run) => ({ id: run.toolCallId, label: run.description || run.agentType || "Agent" }));
  }

  /** Snapshots of the background tasks claude still runs, in start order. */
  runningBackgroundTasks(): ClaudeBackgroundTask[] {
    return [...this.tasks.values()].filter((entry) => entry.background && entry.task.status === "running").map((entry) => ({ ...entry.task }));
  }

  /** The context window claude reported for `model` (claudeReportedContextWindows()); undefined until it reported one. */
  contextWindow(model: string): number | undefined {
    return this.contextWindows.get(model);
  }

  /**
   * One report of a main-conversation API message's usage. claude reports a message's
   * usage several times — `message_start`, each content block's `assistant` event, then
   * `message_delta` (no id: the message last started) — and the streamed output
   * under-counts until the last of them, so within one message every count keeps its
   * largest value; a new message id starts over. A report that counts nothing (a
   * synthetic reply) leaves the context as it was.
   */
  private countUsage(messageId: unknown, usage: unknown): void {
    const counts = responseCounts(usage);
    if (!counts) return;
    const id = typeof messageId === "string" && messageId ? messageId : undefined;
    const previous = this.counted && (id === undefined || id === this.counted.id) ? this.counted : null;
    const merged: ClaudeResponseCounts = previous
      ? {
          input: Math.max(previous.counts.input, counts.input),
          cacheWrite: Math.max(previous.counts.cacheWrite, counts.cacheWrite),
          cacheRead: Math.max(previous.counts.cacheRead, counts.cacheRead),
          output: Math.max(previous.counts.output, counts.output),
        }
      : counts;
    this.counted = { id: previous ? previous.id : id, counts: merged };
    const tokens = countsTotal(merged);
    if (tokens !== null) this.contextTokens = tokens;
  }

  push(raw: unknown): ClaudeTranslation {
    if (!isObject(raw)) return { events: [] };
    if (typeof raw.session_id === "string" && raw.session_id) this.sessionId = raw.session_id;
    // claude's permission frames go to the session whoever's call they
    // name (a subagent's included): never chat events.
    if (raw.type === "control_request") {
      const permissionRequest = claudePermissionRequest(raw);
      return permissionRequest ? { events: [], permissionRequest } : { events: [] };
    }
    if (raw.type === "control_cancel_request") {
      return typeof raw.request_id === "string" && raw.request_id ? { events: [], permissionCancel: raw.request_id } : { events: [] };
    }
    if (raw.type === "system" && raw.subtype === "permission_denied") {
      const permissionDenied = claudePermissionDenied(raw);
      return permissionDenied ? { events: [], permissionDenied } : { events: [] };
    }
    // A subagent's own activity names the tool call that started it: its run's, never the main conversation's.
    if (typeof raw.parent_tool_use_id === "string" && raw.parent_tool_use_id) {
      return { events: this.subagentActivity(raw.parent_tool_use_id, raw) };
    }
    switch (raw.type) {
      case "system":
        return this.system(raw);
      case "stream_event": {
        // The retried request streams: claude's retry wait is over.
        const events = this.endRetry(true);
        if (isObject(raw.event)) events.push(...this.streamEvent(raw.event));
        return { events };
      }
      case "assistant":
        return { events: isObject(raw.message) ? this.assistant(raw.message, raw.timestamp) : [] };
      case "user":
        return { events: isObject(raw.message) ? this.user(raw.message, raw.timestamp, raw.tool_use_result) : [] };
      case "result":
        return this.result(raw);
      case "control_response": {
        const control = claudeControlResponse(raw);
        return control ? { events: [], control } : { events: [] };
      }
      case "command_lifecycle":
        return this.messageLifecycle(raw);
      default:
        return { events: [] };
    }
  }

  private messageLifecycle(raw: Record<string, unknown>): ClaudeTranslation {
    const uuid = raw.command_uuid;
    const state = raw.state;
    if (typeof uuid !== "string" || !uuid || typeof state !== "string" || !CLAUDE_LIFECYCLE_STATES.has(state)) return { events: [] };
    const absorbed = state === "started" && this.inTurn;
    if (state === "started" && !this.inTurn) this.userTurnStarting = true;
    return { events: [], lifecycle: { uuid, state: state as ClaudeMessageLifecycle["state"], ...(absorbed ? { absorbed: true as const } : {}) } };
  }

  private system(raw: Record<string, unknown>): ClaudeTranslation {
    if (this.isShellTaskEvent(raw)) return { events: this.shellTask(raw) };
    switch (raw.subtype) {
      case "compact_boundary": {
        const metadata = isObject(raw.compact_metadata) ? raw.compact_metadata : {};
        const tokensBefore = typeof metadata.pre_tokens === "number" && Number.isFinite(metadata.pre_tokens) ? metadata.pre_tokens : undefined;
        // The earlier replies' usage counted the context before the compaction.
        this.contextTokens = compactedTokens(metadata.post_tokens);
        this.counted = null;
        const events = this.endCompaction(tokensBefore === undefined ? {} : { result: { tokensBefore } });
        return { events, compactBoundary: tokensBefore === undefined ? {} : { tokensBefore } };
      }
      case "api_retry": {
        // claude waits to retry a failed request (a 429 rate limit among them): pi's retry banner.
        const attempt = typeof raw.attempt === "number" ? raw.attempt : 1;
        this.retryAttempt = attempt;
        const error = typeof raw.error === "string" && raw.error ? raw.error : "error";
        const status = typeof raw.error_status === "number" ? ` (${raw.error_status})` : "";
        return {
          events: [{
            type: "auto_retry_start",
            attempt,
            maxAttempts: typeof raw.max_retries === "number" ? raw.max_retries : attempt,
            delayMs: typeof raw.retry_delay_ms === "number" ? raw.retry_delay_ms : 0,
            errorMessage: `${error}${status}`,
          }],
        };
      }
      case "status": {
        // claude re-sends `compacting` every 30 s while it compacts: the start is announced once.
        if (raw.status === "compacting") {
          if (this.compacting) return { events: [] };
          this.compacting = true;
          return { events: [{ type: "auto_compaction_start", reason: "threshold" }] };
        }
        // A successful compaction ends at its boundary; a failed one ends here.
        if (typeof raw.compact_result === "string" && raw.compact_result !== "success") {
          const errorMessage = typeof raw.compact_error === "string" && raw.compact_error ? raw.compact_error : `claude's compaction ${raw.compact_result}`;
          return { events: this.endCompaction({ errorMessage }) };
        }
        return { events: [] };
      }
      case "hook_response":
        return { events: this.hookResponse(raw) };
      case "init": {
        if (typeof raw.model === "string") this.model = raw.model;
        this.inTurn = true;
        const userTurn = this.userTurnStarting;
        this.userTurnStarting = false;
        if (this.notified.length === 0) return { events: [] };
        const notified = this.notified;
        for (const toolCallId of notified) {
          this.unsettled.delete(toolCallId);
          this.followedUp.add(toolCallId);
        }
        this.notified = [];
        // A user message's turn: claude already took the notifications in (absorbed into
        // the turn before, absorbed_mid_turn, or into this one); no turn of its own follows.
        if (userTurn) return { events: [] };
        // claude starts its own turn for the notified runs: it opens with a hidden anchor.
        const anchor = claudeTaskNotificationAnchor(notified, this.now());
        return { events: [{ type: "message_start", message: anchor }, { type: "message_end", message: anchor }], claudeTurnStart: true };
      }
      case "task_started": {
        const toolCallId = typeof raw.tool_use_id === "string" ? raw.tool_use_id : "";
        const known = typeof raw.task_id === "string" && raw.task_id ? this.taskRuns.get(raw.task_id) : undefined;
        if (known && (known !== toolCallId || this.runs.get(known)?.status !== "running")) return { events: this.resume(known, toolCallId, raw) };
        if (!toolCallId) return { events: [] };
        if (typeof raw.task_id === "string" && raw.task_id) this.taskRuns.set(raw.task_id, toolCallId);
        return {
          events: this.updateRun(toolCallId, {
            ...(typeof raw.task_id === "string" && raw.task_id ? { agentId: raw.task_id } : {}),
            ...(typeof raw.subagent_type === "string" && raw.subagent_type ? { agentType: raw.subagent_type } : {}),
            ...(typeof raw.description === "string" && raw.description ? { description: raw.description } : {}),
            ...(typeof raw.prompt === "string" && raw.prompt ? { prompt: raw.prompt } : {}),
            ...(raw.is_backgrounded === true ? { background: true } : {}),
          }),
        };
      }
      case "task_progress": {
        const toolCallId = this.taskToolCall(raw);
        if (!toolCallId) return { events: [] };
        const usage = claudeTaskUsage(raw.usage);
        return {
          events: this.updateRun(toolCallId, {
            ...(typeof raw.description === "string" && raw.description ? { progress: raw.description } : {}),
            ...(usage ? { usage } : {}),
          }),
        };
      }
      case "task_updated": {
        const toolCallId = this.taskToolCall(raw);
        const status = isObject(raw.patch) ? claudeTaskStatus(raw.patch.status) : undefined;
        if (!toolCallId || !status) return { events: [] };
        return { events: this.updateRun(toolCallId, { status }) };
      }
      case "task_notification": {
        const toolCallId = this.taskToolCall(raw);
        if (!toolCallId) return { events: [] };
        const usage = claudeTaskUsage(raw.usage);
        const result = typeof raw.result === "string" ? raw.result : (typeof raw.summary === "string" ? raw.summary : "");
        const events = this.updateRun(toolCallId, {
          status: claudeTaskStatus(raw.status) ?? "completed",
          ...(usage ? { usage } : {}),
          ...(result.trim() ? { result: result.trim() } : {}),
        });
        const awaited = this.awaitedResumes.delete(toolCallId);
        if (!awaited && this.runs.get(toolCallId)?.background && !this.notified.includes(toolCallId)) this.notified.push(toolCallId);
        return { events };
      }
      default:
        return { events: [] };
    }
  }

  /** A unique id for a hook's extension UI request: claude's frame uuid when it has one. */
  private hookRequestId(raw: Record<string, unknown>, suffix: string): string {
    const base = typeof raw.uuid === "string" && raw.uuid ? raw.uuid : `${this.now()}-${++this.hookRequests}`;
    return `claude-hook:${base}${suffix}`;
  }

  /**
   * A finished hook (claude's `hook_response`, only with `--include-hook-events`):
   * the one hook status line names its event and hook, a failure or
   * cancellation its outcome; a failed hook (`outcome` error or a non-zero exit) is also a
   * warning notice with its exit code and first output line. `hook_started` and
   * `hook_progress` show nothing.
   */
  private hookResponse(raw: Record<string, unknown>): AgentEventLike[] {
    const event = typeof raw.hook_event === "string" && raw.hook_event ? raw.hook_event : "hook";
    const name = typeof raw.hook_name === "string" && raw.hook_name ? raw.hook_name : event;
    const exitCode = typeof raw.exit_code === "number" ? raw.exit_code : undefined;
    const failed = raw.outcome === "error" || (exitCode !== undefined && exitCode !== 0);
    const outcome = failed ? " · failed" : (raw.outcome === "cancelled" ? " · cancelled" : "");
    this.hookStatusShown = true;
    const events: AgentEventLike[] = [{
      type: "extension_ui_request",
      id: this.hookRequestId(raw, ""),
      method: "setStatus",
      statusKey: CLAUDE_HOOK_STATUS_KEY,
      statusText: `hook: ${event} · ${name}${outcome}`,
    }];
    if (failed) {
      const output = [raw.stderr, raw.output, raw.stdout].find((value): value is string => typeof value === "string" && value.trim() !== "");
      const line = output?.trim().split("\n")[0]?.slice(0, CLAUDE_HOOK_NOTICE_OUTPUT_MAX);
      const how = exitCode !== undefined ? `exit ${exitCode}` : String(raw.outcome);
      events.push({
        type: "extension_ui_request",
        id: this.hookRequestId(raw, ":notify"),
        method: "notify",
        message: `claude hook ${name} failed (${how})${line ? `: ${line}` : ""}`,
        notifyType: "warning",
      });
    }
    return events;
  }

  /** The turn's end clears the hook status line, if one is shown. */
  private clearHookStatus(raw: Record<string, unknown>): AgentEventLike[] {
    if (!this.hookStatusShown) return [];
    this.hookStatusShown = false;
    return [{ type: "extension_ui_request", id: this.hookRequestId(raw, ":clear"), method: "setStatus", statusKey: CLAUDE_HOOK_STATUS_KEY, statusText: undefined }];
  }

  /**
   * A task event of a `local_bash` task — a `Bash` command, foreground or background, or
   * a `Monitor`, told apart by its call's tool name: its `task_started`
   * names the type, the later events only the task id.
   */
  private isShellTaskEvent(raw: Record<string, unknown>): boolean {
    if (typeof raw.task_id !== "string" || !raw.task_id) return false;
    if (raw.subtype === "task_started") return raw.task_type === "local_bash";
    return (raw.subtype === "task_progress" || raw.subtype === "task_updated" || raw.subtype === "task_notification") && this.tasks.has(raw.task_id);
  }

  /**
   * A `local_bash` task's event. It is no subagent run and, running,
   * holds nothing unsettled; a background one's notification is followed up by claude
   * like a background run's, so it counts as notified until that turn starts. A stopped
   * one's (`status: "stopped"`, or any word claudeTaskStatus() reads as stopped, `killed`
   * included) is not: claude marked it notified when it killed the task, so no follow-up
   * turn comes for it.
   */
  private shellTask(raw: Record<string, unknown>): AgentEventLike[] {
    const taskId = raw.task_id as string;
    const held = this.tasks.get(taskId);
    if (raw.subtype === "task_started") {
      if (held) return [];
      const toolCallId = typeof raw.tool_use_id === "string" ? raw.tool_use_id : "";
      const call = this.shellCalls.get(toolCallId);
      const monitor = call?.name === "Monitor";
      return this.updateTask({
        taskId,
        toolCallId,
        kind: monitor ? "monitor" : "shell",
        command: call?.command ?? "",
        ...(typeof raw.description === "string" && raw.description ? { description: raw.description } : {}),
        status: "running",
        startedAt: this.now(),
      }, raw.is_backgrounded === true || monitor);
    }
    if (!held) return [];
    if (raw.subtype === "task_updated") {
      const patch = isObject(raw.patch) ? raw.patch : {};
      return this.updateTask(this.settleTask(held.task, claudeTaskStatus(patch.status), patch.end_time), held.background || patch.is_backgrounded === true);
    }
    if (raw.subtype === "task_notification") {
      const events = this.updateTask(this.settleTask(held.task, claudeTaskStatus(raw.status) ?? "completed"), held.background);
      const toolCallId = held.task.toolCallId;
      if (held.background && toolCallId && claudeTaskStatus(raw.status) !== "stopped" && !this.notified.includes(toolCallId)) {
        this.notified.push(toolCallId);
        this.unsettled.add(toolCallId);
      }
      return events;
    }
    return [];
  }

  /** A task's new status; a settled task stays settled (claude's last word stands) and gets its end. */
  private settleTask(task: ClaudeBackgroundTask, status: ClaudeBackgroundTask["status"] | undefined, endTime?: unknown): ClaudeBackgroundTask {
    if (!status || task.status !== "running" || status === "running") return task;
    return { ...task, status, endedAt: typeof endTime === "number" && Number.isFinite(endTime) ? endTime : this.now() };
  }

  /** Folds a task's snapshot in; a listed (background) task's change goes out once as `claude_background_task`. */
  private updateTask(task: ClaudeBackgroundTask, background: boolean): AgentEventLike[] {
    const held = this.tasks.get(task.taskId);
    this.tasks.set(task.taskId, { task, background });
    if (!background || (held?.background && held.task === task)) return [];
    return [{ type: CLAUDE_BACKGROUND_TASK_EVENT, task: { ...task } }];
  }

  /** Ends claude's open retry wait, if any: pi's auto_retry_end. */
  private endRetry(success: boolean): AgentEventLike[] {
    if (this.retryAttempt === null) return [];
    const attempt = this.retryAttempt;
    this.retryAttempt = null;
    return [{ type: "auto_retry_end", success, attempt }];
  }

  /** Ends claude's open compaction, if any: pi's auto_compaction_end with its outcome. */
  private endCompaction(outcome: { result?: { tokensBefore: number } } | { errorMessage: string } | { aborted: true }): AgentEventLike[] {
    if (!this.compacting) return [];
    this.compacting = false;
    return [{ type: "auto_compaction_end", reason: "threshold", aborted: false, willRetry: false, ...outcome }];
  }

  /** A turn that ends with a retry or a compaction still open: neither will finish. */
  private endOpenStatus(): AgentEventLike[] {
    return [...this.endRetry(false), ...this.endCompaction({ aborted: true })];
  }

  /** The run a task event belongs to: its task's run (a resume's events name the `SendMessage` call), else the call it names. */
  private taskToolCall(raw: Record<string, unknown>): string | undefined {
    const known = typeof raw.task_id === "string" ? this.taskRuns.get(raw.task_id) : undefined;
    if (known) return known;
    return typeof raw.tool_use_id === "string" && raw.tool_use_id ? raw.tool_use_id : undefined;
  }

  /**
   * A `task_started` for a task that already has its run: claude resumed the agent — by
   * a `SendMessage` to a finished agent (the event names that call), or itself with a
   * message queued after the agent's last tool round (the event names the `Agent` call
   * again). The run reopens from the `SendMessage` call's time (else now); the resume's
   * prompt (the relayed text) and description leave the run's own. A resume claude runs
   * in the background (not awaited) gets its own notification and follow-up turn.
   */
  private resume(toolCallId: string, callId: string, raw: Record<string, unknown>): AgentEventLike[] {
    if (this.runs.get(toolCallId)?.status === "running") return [];
    const awaited = raw.awaited === true;
    if (awaited) {
      this.awaitedResumes.add(toolCallId);
    } else {
      this.awaitedResumes.delete(toolCallId);
      this.followedUp.delete(toolCallId);
    }
    const startedAt = (callId !== toolCallId ? this.callTimes.get(callId) : undefined) ?? this.now();
    return this.updateRun(toolCallId, { status: "running", startedAt, ...(raw.is_backgrounded === true && !awaited ? { background: true } : {}) }, [], true);
  }

  /** Folds a header change (and new entries) into the run and announces it once. */
  private updateRun(toolCallId: string, update: Partial<ClaudeSubagentRun>, entries: ClaudeSubagentEntry[] = [], reopen = false): AgentEventLike[] {
    const current = this.runs.get(toolCallId) ?? { toolCallId, background: false, status: "running" as const, entries: [] };
    // A settled run never goes back to running (claude's last word stands), except by a resume.
    const status = !reopen && current.status !== "running" && update.status === "running" ? current.status : (update.status ?? current.status);
    const run: ClaudeSubagentRun = { ...current, ...update, toolCallId, background: Boolean(update.background || current.background), status, entries: [] };
    this.runs.set(toolCallId, run);
    if (run.background && !this.followedUp.has(toolCallId)) this.unsettled.add(toolCallId);
    return [{ type: CLAUDE_SUBAGENT_EVENT, run: { ...run, entries } }];
  }

  /** A subagent's assistant reply or tool result: entries of its run. Its partial stream is not shown. */
  private subagentActivity(parent: string, raw: Record<string, unknown>): AgentEventLike[] {
    if ((raw.type !== "assistant" && raw.type !== "user") || !isObject(raw.message)) return [];
    const id = typeof raw.uuid === "string" && raw.uuid ? raw.uuid : `${parent}:${this.now()}`;
    const at = timestampOf(raw.timestamp, this.now());
    const entries = subagentEntries(raw.type, raw.message, id, at, this.toolNames, this.model, raw.tool_use_result);
    if (entries.length === 0) return [];
    const events = this.updateRun(parent, {}, entries);
    events.push(...this.toolCallRuns(entries.map((entry) => entry.message), at), ...this.toolResultRuns(raw.message, raw.tool_use_result));
    return events;
  }

  /** The runs an `Agent` / `Task` call in these messages starts (at any depth), made at `at`. */
  private toolCallRuns(messages: readonly unknown[], at: number): AgentEventLike[] {
    const events: AgentEventLike[] = [];
    for (const message of messages) {
      if (!isObject(message) || message.role !== "assistant" || !Array.isArray(message.content)) continue;
      for (const block of message.content as PiAssistantBlock[]) {
        if (block.type === "toolCall") this.noteShellCall(block);
        if (block.type !== "toolCall" || !isClaudeSubagentToolName(block.name) || this.runs.has(block.id)) continue;
        const { entries: _entries, ...header } = runFromToolCall(block, at);
        void _entries;
        events.push(...this.updateRun(block.id, header));
      }
    }
    return events;
  }

  /** A `Bash` / `Monitor` call (main conversation or a subagent's): the command its task will run. */
  private noteShellCall(block: PiAssistantBlock): void {
    if (block.type !== "toolCall" || (block.name !== "Bash" && block.name !== "Monitor")) return;
    const command = isObject(block.arguments) && typeof block.arguments.command === "string" ? block.arguments.command : "";
    this.shellCalls.set(block.id, { name: block.name, command });
  }

  /** What an `Agent` / `Task` tool result (a launch receipt or a finished run) says about its run. */
  private toolResultRuns(message: Record<string, unknown>, receipt: unknown): AgentEventLike[] {
    const events: AgentEventLike[] = [];
    for (const block of toolResultBlocks(message.content)) {
      const toolCallId = typeof block.tool_use_id === "string" ? block.tool_use_id : "";
      if (!toolCallId || !this.runs.has(toolCallId)) continue;
      const update = runFromToolResult(toolCallId, receipt, block.content, block.is_error === true);
      if (update.agentId) this.taskRuns.set(update.agentId, toolCallId);
      events.push(...this.updateRun(toolCallId, update));
    }
    return events;
  }

  /**
   * Ends a turn that will see no `result` (a stopped run, a child that exited): the
   * reply streamed so far is kept as a final message, then the run ends.
   */
  flush(): AgentEventLike[] {
    const events: AgentEventLike[] = [];
    const partial = this.streamingMessage;
    if (partial) {
      const content = partial.content.filter((block) => block.type !== "thinking" || block.thinking);
      if (content.length > 0) events.push({ type: "message_end", message: { ...partial, content, stopReason: "aborted" } });
    }
    this.resetPartial();
    events.push(...this.endOpenStatus());
    events.push(...this.clearHookStatus({}));
    events.push({ type: "agent_end", messages: [] });
    return events;
  }

  /**
   * The child exited: no notification or tool result will come for a run
   * it still had running, at any depth, so each settles as `stopped` with the usual run
   * event, and none holds the session any longer. Its tasks ended with it: a running one
   * settles as `stopped` too.
   */
  settleOnExit(): AgentEventLike[] {
    const events: AgentEventLike[] = [];
    for (const [toolCallId, run] of [...this.runs]) {
      if (run.status === "running") events.push(...this.updateRun(toolCallId, { status: "stopped" }));
    }
    for (const { task, background } of [...this.tasks.values()]) {
      events.push(...this.updateTask(this.settleTask(task, "stopped"), background));
    }
    this.unsettled.clear();
    this.notified = [];
    this.awaitedResumes.clear();
    return events;
  }

  private resetPartial(): void {
    this.streamingMessage = null;
    this.blocks.clear();
  }

  private ensurePartial(events: AgentEventLike[]): PiAssistantMessage {
    if (this.streamingMessage) return this.streamingMessage;
    const message: PiAssistantMessage = {
      role: "assistant",
      content: [],
      api: CLAUDE_API,
      provider: CLAUDE_PROVIDER,
      model: this.model,
      timestamp: this.now(),
    };
    this.streamingMessage = message;
    events.push({ type: "message_start", message });
    return message;
  }

  private update(partial: PiAssistantMessage, assistantMessageEvent: Record<string, unknown>): AgentEventLike {
    // No `message` beside it: a reconnecting page's snapshot is this partial, and the stream
    // skips events whose `message` is the snapshot (isEventIncludedInSnapshot).
    return { type: "message_update", assistantMessageEvent: { ...assistantMessageEvent, partial } };
  }

  private streamEvent(event: Record<string, unknown>): AgentEventLike[] {
    const events: AgentEventLike[] = [];
    const index = typeof event.index === "number" ? event.index : -1;
    switch (event.type) {
      case "message_start":
        if (isObject(event.message) && typeof event.message.model === "string") this.model = event.message.model;
        if (isObject(event.message)) this.countUsage(event.message.id, event.message.usage);
        return events;
      case "message_delta":
        // The message's final output count (the `assistant` events carry a streamed one).
        this.countUsage(undefined, event.usage);
        return events;
      case "content_block_start": {
        const block = isObject(event.content_block) ? event.content_block : {};
        const partial = this.ensurePartial(events);
        const local = partial.content.length;
        if (block.type === "text") {
          partial.content.push({ type: "text", text: "" });
          this.blocks.set(index, { local, kind: "text", rawJson: "" });
          events.push(this.update(partial, { type: "text_start", contentIndex: local }));
        } else if (block.type === "thinking") {
          partial.content.push({ type: "thinking", thinking: "" });
          this.blocks.set(index, { local, kind: "thinking", rawJson: "" });
          events.push(this.update(partial, { type: "thinking_start", contentIndex: local }));
        } else if (block.type === "tool_use" && typeof block.id === "string") {
          const name = typeof block.name === "string" ? block.name : "tool";
          this.toolNames.set(block.id, name);
          partial.content.push({ type: "toolCall", id: block.id, name, arguments: {} });
          this.blocks.set(index, { local, kind: "toolCall", rawJson: "" });
          events.push(this.update(partial, { type: "toolcall_start", contentIndex: local }));
        }
        return events;
      }
      case "content_block_delta": {
        const known = this.blocks.get(index);
        const partial = this.streamingMessage;
        const delta = isObject(event.delta) ? event.delta : {};
        if (!known || !partial) return events;
        const block = partial.content[known.local];
        if (delta.type === "text_delta" && typeof delta.text === "string" && block?.type === "text") {
          block.text += delta.text;
          events.push(this.update(partial, { type: "text_delta", contentIndex: known.local, delta: delta.text }));
        } else if (delta.type === "thinking_delta" && typeof delta.thinking === "string" && delta.thinking && block?.type === "thinking") {
          block.thinking += delta.thinking;
          events.push(this.update(partial, { type: "thinking_delta", contentIndex: known.local, delta: delta.thinking }));
        } else if (delta.type === "input_json_delta" && typeof delta.partial_json === "string" && block?.type === "toolCall") {
          known.rawJson += delta.partial_json;
          if (delta.partial_json) {
            events.push(this.update(partial, { type: "toolcall_delta", contentIndex: known.local, delta: delta.partial_json }));
          }
        }
        return events;
      }
      case "content_block_stop": {
        // Usually the block's `assistant` event already ended the reply; then nothing is open.
        const known = this.blocks.get(index);
        const partial = this.streamingMessage;
        if (!known || !partial) return events;
        const block = partial.content[known.local];
        if (block?.type === "text") {
          events.push(this.update(partial, { type: "text_end", contentIndex: known.local, content: block.text }));
        } else if (block?.type === "thinking") {
          events.push(this.update(partial, { type: "thinking_end", contentIndex: known.local, content: block.thinking }));
        } else if (block?.type === "toolCall") {
          block.arguments = parseJsonObject(known.rawJson);
          events.push(this.update(partial, { type: "toolcall_end", contentIndex: known.local, toolCall: block }));
        }
        return events;
      }
      default:
        return events;
    }
  }

  /** One finished content block (or several): the authoritative reply, ending what was streamed. */
  private assistant(message: Record<string, unknown>, timestamp: unknown): AgentEventLike[] {
    const events: AgentEventLike[] = [];
    if (typeof message.model === "string" && message.model) this.model = message.model;
    this.countUsage(message.id, message.usage);
    const content = toAssistantBlocks(message.content);
    const partial = this.streamingMessage;
    this.resetPartial();
    const at = timestampOf(timestamp, this.now());
    for (const block of content) {
      if (block.type !== "toolCall") continue;
      this.toolNames.set(block.id, block.name);
      this.callTimes.set(block.id, at);
      this.noteShellCall(block);
    }
    // Redacted thinking alone renders nothing: the next block's reply replaces the stream.
    if (content.length === 0) return events;
    const final: PiAssistantMessage = {
      role: "assistant",
      content,
      api: CLAUDE_API,
      provider: CLAUDE_PROVIDER,
      model: this.model,
      ...(typeof message.stop_reason === "string" ? { stopReason: message.stop_reason } : {}),
      timestamp: partial?.timestamp ?? at,
    };
    if (!partial) events.push({ type: "message_start", message: final });
    events.push({ type: "message_end", message: final });
    for (const block of content) {
      if (block.type === "toolCall") {
        events.push({ type: "tool_execution_start", toolCallId: block.id, toolName: block.name, args: block.arguments });
      }
    }
    events.push(...this.toolCallRuns([final], at));
    return events;
  }

  private user(message: Record<string, unknown>, timestamp: unknown, receipt: unknown): AgentEventLike[] {
    const events: AgentEventLike[] = [];
    const at = timestampOf(timestamp, this.now());
    const blocks = toolResultBlocks(message.content);
    for (const block of blocks) {
      const result = toToolResult(block, this.toolNames, at, blocks.length === 1 ? receipt : undefined);
      events.push({ type: "tool_execution_end", toolCallId: result.toolCallId, toolName: result.toolName, isError: result.isError });
      events.push({ type: "message_start", message: result });
      events.push({ type: "message_end", message: result });
    }
    events.push(...this.toolResultRuns(message, receipt));
    return events;
  }

  private result(raw: Record<string, unknown>): ClaudeTranslation {
    this.inTurn = false;
    const events: AgentEventLike[] = [];
    // A turn that ends mid-block (an error) keeps what was streamed.
    if (this.streamingMessage) events.push(...this.flush().filter((event) => event.type !== "agent_end"));
    this.resetPartial();
    for (const [model, contextWindow] of claudeReportedContextWindows(raw)) this.contextWindows.set(model, contextWindow);
    events.push(...this.endOpenStatus());
    events.push(...this.clearHookStatus(raw));
    events.push({ type: "agent_end", messages: [] });
    const errors = Array.isArray(raw.errors) ? raw.errors.filter((entry): entry is string => typeof entry === "string") : [];
    const failed = raw.is_error === true || (typeof raw.subtype === "string" && raw.subtype !== "success");
    const error = failed
      ? (typeof raw.result === "string" && raw.result ? raw.result : errors.join("\n") || String(raw.subtype ?? "claude reported an error"))
      : undefined;
    return { events, turnEnd: { usage: claudeResultUsage(raw), ...(error ? { error } : {}) } };
  }
}

/**
 * claude's bookkeeping of a command it ran itself (`/model` from a
 * `set_model` request, `/compact`): `<command-name>` / `<local-command-stdout>` user
 * entries no user typed, and the compaction summary only claude's transcript view shows.
 */
function isClaudeCommandBookkeeping(entry: Record<string, unknown>, message: Record<string, unknown>): boolean {
  if (entry.type !== "user") return false;
  if (entry.isCompactSummary === true || entry.isVisibleInTranscriptOnly === true) return true;
  return entry.promptSource !== "sdk"
    && typeof message.content === "string"
    && /^<(command-name|local-command-stdout|local-command-stderr|local-command-caveat)>/.test(message.content);
}

/** A message the main agent's `SendMessage` delivered into a sidecar (claude marks it meta). */
function isCoordinatorMessage(entry: Record<string, unknown>): boolean {
  return entry.isSidechain === true && isObject(entry.origin) && entry.origin.kind === "coordinator";
}

/** A transcript entry claude wrote that is part of the conversation the user sees (a sidecar's own are sidechain). */
function conversationEntry(entry: unknown, sidechain: boolean): Record<string, unknown> | null {
  if (!isObject(entry) || (entry.type !== "user" && entry.type !== "assistant")) return null;
  if ((entry.isSidechain === true) !== sidechain || (entry.isMeta === true && !isCoordinatorMessage(entry)) || !isObject(entry.message)) return null;
  if (isClaudeCommandBookkeeping(entry, entry.message)) return null;
  return entry;
}

/** One subagent's sidecar, as claude writes it beside the transcript: `agent-<id>.jsonl` and `.meta.json`. */
export interface ClaudeSidecar {
  entries: readonly unknown[];
  meta?: unknown;
}

/** A session's sidecars by agent id (the `<id>` of `agent-<id>.jsonl`). */
export type ClaudeSidecars = Readonly<Record<string, ClaudeSidecar>>;

/** What the `<task-notification>` text claude adds to the main transcript reports for one run. */
interface ClaudeTaskNote {
  /** The call claude names (`<tool-use-id>`): the run's `Agent` call or the `SendMessage` that resumed it. */
  toolCallId?: string;
  /**
   * The task (`<task-id>`, a subagent's agent id). claude names no call for some runs —
   * for example one the agent resumed itself after its own background work
   * — and this is then the only key.
   */
  taskId?: string;
  /**
   * The agent stopped with background work of its own still running and notifies again
   * (summary "… is waiting on its own background work"; claude still says `completed`,
   * and the summary is the only signal it writes): it settles nothing.
   */
  interim?: boolean;
  status?: ClaudeSubagentStatus;
  result?: string;
  usage?: ClaudeSubagentUsage;
}

function xmlTag(text: string, tag: string): string | undefined {
  const open = text.indexOf(`<${tag}>`);
  if (open < 0) return undefined;
  const from = open + tag.length + 2;
  const close = text.indexOf(`</${tag}>`, from);
  return close < 0 ? undefined : text.slice(from, close).trim();
}

/** Every non-empty `<tag>…</tag>` of `text`, in order. */
function xmlTags(text: string, tag: string): string[] {
  const values: string[] = [];
  for (let open = text.indexOf(`<${tag}>`); open >= 0; open = text.indexOf(`<${tag}>`, open + 1)) {
    const from = open + tag.length + 2;
    const close = text.indexOf(`</${tag}>`, from);
    if (close < 0) break;
    const value = text.slice(from, close).trim();
    if (value) values.push(value);
  }
  return values;
}

function taskNotificationText(entry: Record<string, unknown>, message: Record<string, unknown>): string | null {
  const text = typeof message.content === "string" ? message.content : textOf(message.content);
  return entry.turnOrigin === "task_notification" || text.trimStart().startsWith("<task-notification>") ? text : null;
}

/**
 * The notes of one `<task-notification>`: one per `<task-id>` it names, all with its one
 * status — claude reports several agents in one notification, e.g. those that "didn't finish
 * before the previous session ended". Its `<tool-use-id>` stays with the first.
 */
function taskNotes(text: string): ClaudeTaskNote[] {
  const toolCallId = xmlTag(text, "tool-use-id");
  // Only the header names tasks: `<result>` is the agent's raw reply and may quote a notification.
  const body = Math.min(...["<status>", "<summary>", "<result>"].map((tag) => text.indexOf(tag)).filter((at) => at >= 0));
  const taskIds = xmlTags(Number.isFinite(body) ? text.slice(0, body) : text, "task-id");
  if (!toolCallId && taskIds.length === 0) return [];
  const tokens = xmlTag(text, "subagent_tokens") ?? xmlTag(text, "total_tokens");
  const toolUses = xmlTag(text, "tool_uses");
  const duration = xmlTag(text, "duration_ms");
  const result = xmlTag(text, "result");
  const status = claudeTaskStatus(xmlTag(text, "status"));
  const shared = {
    ...(xmlTag(text, "summary")?.endsWith("is waiting on its own background work") ? { interim: true } : {}),
    ...(status ? { status } : {}),
    ...(result ? { result } : {}),
    ...(tokens || toolUses || duration ? { usage: { totalTokens: Number(tokens) || 0, toolUses: Number(toolUses) || 0, durationMs: Number(duration) || 0 } } : {}),
  };
  return (taskIds.length > 0 ? taskIds : [undefined]).map((taskId, index) => ({
    ...(toolCallId && index === 0 ? { toolCallId } : {}),
    ...(taskId ? { taskId } : {}),
    ...shared,
  }));
}

interface HistoryWalk {
  runs: Map<string, ClaudeSubagentRun>;
  notes: ClaudeTaskNote[];
  toolNames: Map<string, string>;
  model: string;
  /**
   * claude's input queue as its transcript's `queue-operation` entries show it:
   * each open `enqueue`, and whether a conversation entry was written
   * after it — a message that waited behind a running turn.
   */
  queue: Array<{ content: string | null; behind: boolean }>;
  /** The `dequeue` just before the next entry took a message that waited behind a turn. */
  dequeuedBehind: boolean;
  /**
   * The main conversation's `SendMessage` calls, in order: to whom, when,
   * whether claude answered, and whether the answer revived the agent in the background
   * (`revivedAgentId`; an awaited resume answers with the agent's report inline instead).
   * `notes`: how many notes the walk held at the call, so the ones after it are known.
   */
  sends: Map<string, { to: string; at: number; answered: boolean; revived: boolean; notes: number }>;
}

/**
 * One `queue-operation` entry folded into the walk's view of claude's queue: an
 * `enqueue` opens an item (with the prompt's text when it was text); a `dequeue` (it
 * names no content) takes the oldest open item and says whether it waited behind a
 * turn; a `remove` (absorbed mid-turn) drops the oldest item of its content, else the
 * oldest without one.
 */
function foldQueueOperation(walk: HistoryWalk, entry: Record<string, unknown>): void {
  const content = typeof entry.content === "string" ? entry.content : null;
  if (entry.operation === "enqueue") {
    walk.queue.push({ content, behind: false });
    return;
  }
  let at = 0;
  if (entry.operation === "remove") {
    at = walk.queue.findIndex((item) => item.content === content);
    if (at < 0) at = walk.queue.findIndex((item) => item.content === null);
  }
  if (at < 0 || at >= walk.queue.length) return;
  const [item] = walk.queue.splice(at, 1);
  if (entry.operation === "dequeue") walk.dequeuedBehind = item.behind;
}

/**
 * A message claude took into a running turn: no user entry of its own, a
 * `queued_command` attachment carrying the prompt (a task notification's is not one). In
 * a sidecar, the one a `SendMessage` queued for the running agent.
 */
function absorbedPrompt(entry: unknown, sidechain: boolean): unknown[] | string | null {
  if (!isObject(entry) || entry.type !== "attachment" || (entry.isSidechain === true) !== sidechain || !isObject(entry.attachment)) return null;
  const attachment = entry.attachment;
  if (sidechain) {
    if (attachment.type !== "queued_command" || !isObject(attachment.origin) || attachment.origin.kind !== "coordinator") return null;
    return typeof attachment.prompt === "string" || Array.isArray(attachment.prompt) ? attachment.prompt : null;
  }
  if (attachment.type !== "queued_command" || (attachment.commandMode !== undefined && attachment.commandMode !== "prompt") || attachment.isMeta === true) return null;
  const prompt = attachment.prompt;
  if (typeof prompt === "string") return prompt.trimStart().startsWith("<task-notification>") ? null : prompt;
  return Array.isArray(prompt) ? prompt : null;
}

/**
 * A `<task-notification>` claude took into a running turn: no user entry
 * of its own, only its `queued_command` attachment. It reports its run as a notification
 * turn does, and shows nothing.
 */
function absorbedTaskNotification(entry: unknown, sidechain: boolean): string | null {
  if (!isObject(entry) || entry.type !== "attachment" || (entry.isSidechain === true) !== sidechain || !isObject(entry.attachment)) return null;
  const { type, prompt } = entry.attachment;
  return type === "queued_command" && typeof prompt === "string" && prompt.trimStart().startsWith("<task-notification>") ? prompt : null;
}

/**
 * Walks one transcript (the main one, or a sidecar with `sidechain`) in file order,
 * handing each displayed entry to `emit`: the same mapping the live stream uses
 * (subagentEntries()), the `Agent` / `Task` calls registered as runs and their
 * receipts / results folded in, and claude's `<task-notification>` turn as a hidden
 * anchor (its report kept as a note) instead of a user bubble.
 */
function walkTranscript(entries: readonly unknown[], walk: HistoryWalk, sidechain: boolean, emit: (entry: ClaudeSubagentEntry) => void): void {
  entries.forEach((raw, position) => {
    // claude's compaction boundary: the compaction card, where it happened.
    if (!sidechain && isObject(raw) && raw.type === "system" && raw.subtype === "compact_boundary") {
      const id = typeof raw.uuid === "string" ? raw.uuid : `claude-${position}`;
      emit({ id, message: claudeCompactionMarker(raw, timestampOf(raw.timestamp, 0)) as unknown as ClaudeSubagentEntry["message"] });
      return;
    }
    if (!sidechain && isObject(raw) && raw.type === "queue-operation") {
      foldQueueOperation(walk, raw);
      return;
    }
    const absorbedNotification = absorbedTaskNotification(raw, sidechain);
    if (absorbedNotification !== null) {
      walk.notes.push(...taskNotes(absorbedNotification));
      return;
    }
    const absorbed = absorbedPrompt(raw, sidechain);
    if (absorbed !== null && isObject(raw)) {
      const id = typeof raw.uuid === "string" ? raw.uuid : `claude-${position}`;
      for (const item of subagentEntries("user", { role: "user", content: absorbed }, id, timestampOf(raw.timestamp, 0), walk.toolNames, walk.model)) {
        emit(sidechain ? item : { ...item, message: { ...item.message, claudeDelivery: "absorbed" } as unknown as ClaudeSubagentEntry["message"] });
      }
    }
    const entry = conversationEntry(raw, sidechain);
    if (!entry) return;
    // Every entry the conversation gains is one an open queue item waited behind.
    for (const item of walk.queue) item.behind = true;
    const dequeuedBehind = walk.dequeuedBehind;
    walk.dequeuedBehind = false;
    const message = entry.message as Record<string, unknown>;
    const id = typeof entry.uuid === "string" ? entry.uuid : `claude-${position}`;
    const at = timestampOf(entry.timestamp, 0);
    if (entry.type === "assistant" && typeof message.model === "string" && message.model) walk.model = message.model;
    if (entry.type === "user") {
      const notification = taskNotificationText(entry, message);
      if (notification !== null) {
        const notes = taskNotes(notification);
        walk.notes.push(...notes);
        emit({ id, message: claudeTaskNotificationAnchor(notes.flatMap((note) => (note.toolCallId ? [note.toolCallId] : [])), at) as unknown as ClaudeSubagentEntry["message"] });
        return;
      }
    }
    const converted = subagentEntries(entry.type, message, id, at, walk.toolNames, walk.model, entry.toolUseResult);
    for (const item of converted) {
      // A prompt claude dequeued after a turn it waited behind: it was queued.
      if (dequeuedBehind && entry.type === "user" && item.message.role === "user" && entry.promptSource === "sdk") {
        emit({ ...item, message: { ...item.message, claudeDelivery: "queued" } as unknown as ClaudeSubagentEntry["message"] });
        continue;
      }
      emit(item);
      const out = item.message;
      if (out.role === "assistant" && Array.isArray(out.content)) {
        for (const block of out.content as PiAssistantBlock[]) {
          if (block.type !== "toolCall") continue;
          if (isClaudeSubagentToolName(block.name) && !walk.runs.has(block.id)) walk.runs.set(block.id, runFromToolCall(block, at));
          if (!sidechain && block.name === "SendMessage" && typeof block.arguments.to === "string") {
            walk.sends.set(block.id, { to: block.arguments.to, at, answered: false, revived: false, notes: walk.notes.length });
          }
        }
      }
    }
    if (entry.type === "user") {
      for (const block of toolResultBlocks(message.content)) {
        const toolCallId = typeof block.tool_use_id === "string" ? block.tool_use_id : "";
        const send = sidechain ? undefined : walk.sends.get(toolCallId);
        if (send) {
          send.answered = true;
          send.revived = isObject(entry.toolUseResult) && typeof entry.toolUseResult.revivedAgentId === "string";
        }
        const run = walk.runs.get(toolCallId);
        if (!run) continue;
        walk.runs.set(toolCallId, { ...run, ...runFromToolResult(toolCallId, entry.toolUseResult, block.content, block.is_error === true) });
      }
    }
  });
}

// ---------------------------------------------------------------------------
// The active branch. A claude transcript is a tree: each entry names
// its `parentUuid` (a compaction boundary, whose parentUuid is null, its
// `logicalParentUuid`). An in-session edit — a truncating resume, `--resume <id>
// --resume-session-at <entry>` — appends the new turn under an earlier entry and leaves
// the old turns in the file, so the file holds branches. claude resumes one branch, the
// ancestor chain of its active leaf, and that branch is all the chat shows.

/** A row of claude's conversation tree: a main-conversation entry of a chained type with its own uuid. */
function treeRow(entry: unknown): (Record<string, unknown> & { uuid: string }) | null {
  if (!isObject(entry) || typeof entry.uuid !== "string" || !entry.uuid || entry.isSidechain === true) return null;
  const type = entry.type;
  return type === "user" || type === "assistant" || type === "system" || type === "attachment"
    ? entry as Record<string, unknown> & { uuid: string }
    : null;
}

function isCompactBoundary(entry: Record<string, unknown>): boolean {
  return entry.type === "system" && entry.subtype === "compact_boundary";
}

/** The entry's chain parent as claude walks it: `parentUuid` alone (null on a compaction boundary). */
function chainParentOf(entry: Record<string, unknown> | undefined): string | undefined {
  return entry && typeof entry.parentUuid === "string" && entry.parentUuid ? entry.parentUuid : undefined;
}

/** The entry's place in the tree: its parentUuid; a compaction boundary hangs under its logicalParentUuid. */
function treeParentOf(entry: Record<string, unknown>): string | undefined {
  return chainParentOf(entry)
    ?? (isCompactBoundary(entry) && typeof entry.logicalParentUuid === "string" && entry.logicalParentUuid ? entry.logicalParentUuid : undefined);
}

/** A user entry that starts a turn (a prompt, claude's follow-up anchor or its command bookkeeping); never a tool result or a hidden companion. */
function startsTurn(entry: Record<string, unknown>): boolean {
  return entry.type === "user" && entry.isMeta !== true && entry.isCompactSummary !== true
    && isObject(entry.message) && toolResultBlocks(entry.message.content).length === 0;
}

/** A transcript's active branch (claudeActiveBranch()). */
export interface ClaudeActiveBranch {
  /** The active leaf: the user or assistant entry the conversation continues from; null without one. */
  leaf: string | null;
  /** The leaf's ancestors, leaf first; a compaction boundary continues through its logicalParentUuid. */
  path: readonly string[];
  /** The path's part a resume loads: from the leaf up to the latest compaction boundary, which it includes. */
  resumable: ReadonlySet<string>;
  /** Every entry of another branch: each turn that starts beside the path, with everything under it. */
  hidden: ReadonlySet<string>;
}

/**
 * The branch claude continues, read from the transcript's `parentUuid`
 * tree and its `last-prompt` checkpoints as claude 2.1.312 reads them: the leaf is the
 * nearest user or assistant entry at or above the anchor — the latest checkpoint while it
 * is explicit and nothing was written after it (claude's own rewind), else the last
 * conversation row (with no keyed checkpoint at all, that row's newest descendant).
 * claude 2.1.312 also lets a stale, non-explicit checkpoint win over a last row that does
 * not descend from it; pi-web does not, because right after a truncating resume claude's
 * last checkpoint still names the old branch's leaf until it writes the next one (after
 * 32 KiB of transcript, or at exit), while the live child already continues the new
 * branch (inferred from the 2.1.312 bundle; docs/agents/claude-sessions.md).
 *
 * Hidden: for each entry on the path, every child off the path that starts a turn — or is
 * an attachment or system entry with a turn under it (a resumed child's SessionStart hook
 * before its prompt) — and everything under it: the turns an edit replaced. Other
 * children off the path stay shown: a parallel tool call's results and the reply's split
 * entries hang beside the path inside one turn (claude re-collects them the same way).
 * Sidechain entries are another conversation and never part of the tree.
 */
export function claudeActiveBranch(entries: readonly unknown[]): ClaudeActiveBranch {
  const rows = new Map<string, Record<string, unknown> & { uuid: string }>();
  let checkpoint: string | undefined;
  let checkpointExplicit = false;
  let checkpointKeyed = false;
  let lastRow: string | undefined;
  let lastMessage: string | undefined;
  let newestRow: string | undefined;
  let newestAt = "";
  for (const raw of entries) {
    const row = treeRow(raw);
    if (row) {
      rows.set(row.uuid, row);
      const forkBriefing = row.type === "attachment" && isObject(row.attachment) && row.attachment.type === "fork_briefing";
      if (!forkBriefing) {
        lastRow = row.uuid;
        checkpointExplicit = false;
        if (typeof row.timestamp === "string" && row.timestamp > newestAt) {
          newestRow = row.uuid;
          newestAt = row.timestamp;
        }
      }
      if (row.type === "user" || row.type === "assistant") lastMessage = row.uuid;
      if (isCompactBoundary(row)) {
        checkpoint = undefined;
        checkpointExplicit = false;
      }
      continue;
    }
    if (isObject(raw) && raw.type === "last-prompt") {
      if ("leafUuid" in raw) checkpointKeyed = true;
      if (typeof raw.leafUuid === "string" && raw.leafUuid) {
        checkpointExplicit = raw.explicit === true || (checkpointExplicit && raw.leafUuid === checkpoint);
        checkpoint = raw.leafUuid;
      }
    }
  }

  let anchor = lastRow;
  if (checkpoint && checkpointExplicit && rows.has(checkpoint)) {
    anchor = checkpoint;
  } else if (!checkpointKeyed && lastRow && newestRow && newestRow !== lastRow) {
    // No checkpoint was ever written: claude takes the last row's newest descendant.
    const target = lastRow;
    // Whether an entry lies under the last row along the chain, memoized for long transcripts.
    const under = new Map<string, boolean>([[target, true]]);
    const isUnder = (from: string): boolean => {
      const walked = new Set<string>();
      let found = false;
      for (let at: string | undefined = from; at !== undefined; at = chainParentOf(rows.get(at))) {
        const known = under.get(at);
        if (known !== undefined) {
          found = known;
          break;
        }
        if (walked.has(at)) break;
        walked.add(at);
      }
      for (const at of walked) under.set(at, found);
      return found;
    };
    let bestAt = "";
    for (const row of rows.values()) {
      if (row.uuid === target || typeof row.timestamp !== "string" || row.timestamp < bestAt || !isUnder(row.uuid)) continue;
      anchor = row.uuid;
      bestAt = row.timestamp;
    }
  }

  let leaf: string | null = null;
  const seen = new Set<string>();
  for (let at = anchor; at && rows.has(at) && !seen.has(at); at = chainParentOf(rows.get(at))) {
    seen.add(at);
    const row = rows.get(at)!;
    if (row.type === "user" || row.type === "assistant") {
      leaf = at;
      break;
    }
  }
  leaf ??= lastMessage ?? null;

  const path: string[] = [];
  const onPath = new Set<string>();
  const resumable = new Set<string>();
  let crossedBoundary = false;
  for (let at = leaf ?? undefined; at && rows.has(at) && !onPath.has(at);) {
    const row = rows.get(at)!;
    path.push(at);
    onPath.add(at);
    if (!crossedBoundary) resumable.add(at);
    if (isCompactBoundary(row)) crossedBoundary = true;
    at = treeParentOf(row);
  }

  const children = new Map<string, string[]>();
  for (const row of rows.values()) {
    const parent = treeParentOf(row);
    if (!parent) continue;
    const list = children.get(parent);
    if (list) list.push(row.uuid);
    else children.set(parent, [row.uuid]);
  }
  const subtree = (root: string): string[] => {
    const found: string[] = [];
    const reached = new Set<string>();
    for (const pending = [root]; pending.length > 0;) {
      const next = pending.pop()!;
      if (reached.has(next) || onPath.has(next)) continue;
      reached.add(next);
      found.push(next);
      pending.push(...(children.get(next) ?? []));
    }
    return found;
  };
  const hidden = new Set<string>();
  for (const at of path) {
    for (const child of children.get(at) ?? []) {
      if (onPath.has(child) || hidden.has(child)) continue;
      const row = rows.get(child)!;
      if (startsTurn(row)) {
        for (const id of subtree(child)) hidden.add(id);
      } else if (row.type === "attachment" || row.type === "system") {
        // Furniture a resumed child wrote before its prompt (a SessionStart hook) can open a branch too.
        const under = subtree(child);
        if (under.some((id) => startsTurn(rows.get(id)!))) for (const id of under) hidden.add(id);
      }
    }
  }
  return { leaf, path, resumable, hidden };
}

/** A main-conversation entry another branch holds (an entry without a uuid of its own, or a sidechain's, never is). */
function isHiddenEntry(entry: unknown, hidden: ReadonlySet<string>): boolean {
  return hidden.size > 0 && isObject(entry) && entry.isSidechain !== true && typeof entry.uuid === "string" && hidden.has(entry.uuid);
}

/** The transcript's entries on its active branch, in file order: every entry but the hidden ones. */
export function claudeActiveEntries(entries: readonly unknown[], branch: ClaudeActiveBranch = claudeActiveBranch(entries)): unknown[] {
  return branch.hidden.size === 0 ? [...entries] : entries.filter((entry) => !isHiddenEntry(entry, branch.hidden));
}

/**
 * A claude jsonl transcript (parsed entries, file order) → the pi messages the
 * chat shows, with each one's transcript `uuid` as its entry id, and the subagent
 * runs its `Agent` / `Task` calls started. Only the active branch is
 * shown (claudeActiveBranch()); a run started on another branch is not
 * listed. A run's entries come
 * from its sidecar — linked by the receipt's `toolUseResult.agentId`, else the
 * sidecar meta's `toolUseId` — never from the main list; nested sidecars are
 * followed the same way. A missing sidecar leaves its run without entries.
 */
export function claudeTranscriptToMessages(
  entries: readonly unknown[],
  sidecars: ClaudeSidecars = {},
): { messages: AgentMessage[]; entryIds: string[]; subagents: ClaudeSubagentRun[] } {
  const messages: AgentMessage[] = [];
  const entryIds: string[] = [];
  const walk: HistoryWalk = { runs: new Map(), notes: [], toolNames: new Map(), model: "", queue: [], dequeuedBehind: false, sends: new Map() };
  walkTranscript(claudeActiveEntries(entries), walk, false, (entry) => {
    messages.push(entry.message as unknown as AgentMessage);
    entryIds.push(entry.id);
  });
  // The notes the main conversation got, in its order; the sidecars' follow.
  const mainNotes = walk.notes.length;
  const metaToolCall = new Map<string, string>();
  for (const [agentId, sidecar] of Object.entries(sidecars)) {
    if (isObject(sidecar.meta) && typeof sidecar.meta.toolUseId === "string") metaToolCall.set(sidecar.meta.toolUseId, agentId);
  }
  const linked = new Set<string>();
  for (let changed = true; changed;) {
    changed = false;
    for (const run of [...walk.runs.values()]) {
      const agentId = run.agentId ?? metaToolCall.get(run.toolCallId);
      const sidecar = agentId ? sidecars[agentId] : undefined;
      if (!agentId || !sidecar || linked.has(agentId)) continue;
      linked.add(agentId);
      changed = true;
      const meta = isObject(sidecar.meta) ? sidecar.meta : {};
      const timeline: ClaudeSubagentEntry[] = [];
      let first = true;
      let prompt = run.prompt;
      walkTranscript(sidecar.entries, walk, true, (entry) => {
        // A sidecar opens with the run's prompt: the row shows it once, above the timeline.
        if (first && entry.message.role === "user" && typeof entry.message.content === "string") {
          prompt ??= entry.message.content;
          first = false;
          return;
        }
        first = false;
        timeline.push(entry);
      });
      const current = walk.runs.get(run.toolCallId) ?? run;
      walk.runs.set(run.toolCallId, {
        ...current,
        agentId,
        ...(current.agentType ? {} : typeof meta.agentType === "string" ? { agentType: meta.agentType } : {}),
        ...(current.description ? {} : typeof meta.description === "string" ? { description: meta.description } : {}),
        ...(prompt ? { prompt } : {}),
        ...(meta.requestShape === "background" ? { background: true } : {}),
        ...(current.status === "running" && meta.finished === true ? { status: "completed" as const } : {}),
        entries: timeline,
      });
    }
  }
  // A `SendMessage` to a run's agent that resumed it: claude's notification
  // names the `SendMessage` call, its entries are the run's own (the same sidecar).
  const byAgent = new Map([...walk.runs.values()].flatMap((run) => (run.agentId ? [[run.agentId, run.toolCallId] as const] : [])));
  const runOf = (toolCallId: string) => (walk.runs.has(toolCallId) ? toolCallId : byAgent.get(walk.sends.get(toolCallId)?.to ?? ""));
  // A note claude names no call in is its task's: the agent's run.
  const noteRun = (note: ClaudeTaskNote) => (note.toolCallId ? runOf(note.toolCallId) : undefined) ?? byAgent.get(note.taskId ?? "");
  // The calls a note names (a `SendMessage` so named did resume its agent), and those a
  // final note names: an interim note ("waiting on its own background work") settles nothing.
  const noted = new Set<string>();
  const settledCalls = new Set<string>();
  for (const note of walk.notes) {
    const toolCallId = noteRun(note);
    const run = toolCallId ? walk.runs.get(toolCallId) : undefined;
    if (!toolCallId || !run) continue;
    if (note.toolCallId) {
      noted.add(note.toolCallId);
      if (!note.interim) settledCalls.add(note.toolCallId);
    }
    walk.runs.set(toolCallId, {
      ...run,
      ...(note.interim ? {} : note.status ? { status: note.status } : run.status === "running" ? { status: "completed" as const } : {}),
      ...(note.result ? { result: note.result } : {}),
      ...(note.usage ? { usage: note.usage } : {}),
    });
  }
  // A resume starts at its `SendMessage` call and runs until claude answers it with the
  // report (awaited) or, revived in the background, until its notification. A call
  // claude answered otherwise (queued for a running agent, refused) resumed nothing, and
  // so does one to an agent still running (claude queues it): only a settled run resumes.
  // Its notification is a final one naming the call, or a final one after it naming no
  // call but its agent; a later revival of the agent also ends it, since claude revives
  // only an agent that has stopped.
  const sends = [...walk.sends];
  const settled = (index: number): boolean => {
    const [callId, send] = sends[index];
    return settledCalls.has(callId)
      || walk.notes.slice(send.notes, mainNotes).some((note) => !note.toolCallId && !note.interim && note.taskId === send.to)
      || sends.slice(index + 1).some(([, later]) => later.to === send.to && later.revived);
  };
  sends.forEach(([callId, send], index) => {
    const toolCallId = byAgent.get(send.to);
    const run = toolCallId ? walk.runs.get(toolCallId) : undefined;
    if (!toolCallId || !run || (send.answered && !send.revived && !noted.has(callId))) return;
    const done = settled(index);
    if (run.status === "running" && !done) return;
    const running = (!send.answered || send.revived) && !done;
    walk.runs.set(toolCallId, { ...run, ...(send.at > 0 ? { startedAt: send.at } : {}), ...(running ? { status: "running" as const } : {}) });
  });
  return { messages, entryIds, subagents: [...walk.runs.values()] };
}

/** Parses a jsonl transcript's text; unreadable lines are skipped. */
export function parseClaudeTranscript(text: string): unknown[] {
  const entries: unknown[] = [];
  for (const line of text.split("\n")) {
    if (!line.trim()) continue;
    try {
      entries.push(JSON.parse(line));
    } catch {
      // A line claude is still writing.
    }
  }
  return entries;
}

/** One jsonl line as claude wrote it, and its entry (undefined when it does not parse). */
interface TranscriptLine {
  text: string;
  entry: unknown;
}

function transcriptLines(text: string): TranscriptLine[] {
  return text.split("\n").filter((line) => line.trim()).map((line) => {
    try {
      return { text: line, entry: JSON.parse(line) as unknown };
    } catch {
      return { text: line, entry: undefined };
    }
  });
}

/** The line with its entry's own `sessionId` made `sessionId`; every other line, and every other byte, as written. */
function lineWithSessionId(line: TranscriptLine, sessionId: string): string {
  return isObject(line.entry) && typeof line.entry.sessionId === "string" && line.entry.sessionId !== sessionId
    ? JSON.stringify({ ...line.entry, sessionId })
    : line.text;
}

/**
 * A transcript or subagent sidecar re-owned by another session: each
 * entry's top-level `sessionId` rewritten (never a value an entry quotes), the rest as written.
 */
export function claudeTranscriptWithSessionId(text: string, sessionId: string): string {
  const lines = transcriptLines(text);
  return lines.length === 0 ? "" : `${lines.map((line) => lineWithSessionId(line, sessionId)).join("\n")}\n`;
}

/** The ids of the tool calls a main-conversation `assistant` entry makes (a sidechain's are another conversation). */
function mainToolCallIds(entry: unknown): string[] {
  if (!isObject(entry) || entry.type !== "assistant" || entry.isSidechain === true || !isObject(entry.message)) return [];
  const content = entry.message.content;
  if (!Array.isArray(content)) return [];
  return content.flatMap((block) => (isObject(block) && block.type === "tool_use" && typeof block.id === "string" ? [block.id] : []));
}

/** The tool call ids a main-conversation `user` entry answers. */
function mainToolResultIds(entry: unknown): string[] {
  if (!isObject(entry) || entry.type !== "user" || entry.isSidechain === true || !isObject(entry.message)) return [];
  return toolResultBlocks(entry.message.content).flatMap((block) => (typeof block.tool_use_id === "string" ? [block.tool_use_id] : []));
}

/**
 * Which positions of these entries (file order) sit inside a tool exchange: a call made
 * before the position is answered after it. Each answer closes the latest open call of its
 * id, so an id used again later (by another child) opens an exchange of its own.
 */
function insideToolExchange(entries: readonly unknown[]): (index: number) => boolean {
  const open = new Map<string, number>();
  const spans: [number, number][] = [];
  entries.forEach((entry, index) => {
    for (const id of mainToolResultIds(entry)) {
      const from = open.get(id);
      if (from === undefined) continue;
      spans.push([from, index]);
      open.delete(id);
    }
    for (const id of mainToolCallIds(entry)) open.set(id, index);
  });
  return (index) => spans.some(([from, to]) => from < index && index < to);
}

/** The entry is one the chat shows as a user bubble: a prompt, never a tool result, claude's bookkeeping or its follow-up anchor. */
function isUserBubbleEntry(raw: unknown): boolean {
  const entry = conversationEntry(raw, false);
  if (!entry || entry.type !== "user") return false;
  const message = entry.message as Record<string, unknown>;
  if (taskNotificationText(entry, message) !== null) return false;
  const shown = subagentEntries("user", message, "", 0, new Map(), "");
  return shown.length === 1 && shown[0].message.role === "user";
}

const NOT_ON_BRANCH = "This message is not on the conversation's current branch";

/**
 * The user messages an in-session edit can start from, each with the entry
 * claude resumes at: the message's parent, the chain entry right before it, so
 * `--resume-session-at <parent>` keeps everything before the message and nothing after.
 * A message qualifies when it and its parent lie on the active path after the latest
 * compaction (claude loads no further back, and has nothing to keep before a first
 * message) and no tool exchange spans it.
 */
export function claudeEditPoints(entries: readonly unknown[], branch: ClaudeActiveBranch = claudeActiveBranch(entries)): Map<string, string> {
  const visible = claudeActiveEntries(entries, branch);
  const inside = insideToolExchange(visible);
  const points = new Map<string, string>();
  visible.forEach((entry, index) => {
    if (!isObject(entry) || typeof entry.uuid !== "string" || !branch.resumable.has(entry.uuid) || !isUserBubbleEntry(entry)) return;
    const parent = chainParentOf(entry);
    if (!parent || !branch.resumable.has(parent) || inside(index)) return;
    points.set(entry.uuid, parent);
  });
  return points;
}

/**
 * The entry a truncating resume keeps up to, for an in-session edit of the user message
 * `entryId` (claudeEditPoints()); throws, naming the reason, for a message no edit can
 * start from.
 */
export function claudeEditResumePoint(entries: readonly unknown[], entryId: string): string {
  const branch = claudeActiveBranch(entries);
  const point = claudeEditPoints(entries, branch).get(entryId);
  if (point) return point;
  const entry = entries.find((item) => isObject(item) && item.isSidechain !== true && item.uuid === entryId);
  if (!isObject(entry)) throw new Error("Invalid entry ID for editing");
  if (!isUserBubbleEntry(entry)) throw new Error("A claude session edits only at one of its user messages");
  if (branch.hidden.has(entryId)) throw new Error(NOT_ON_BRANCH);
  const visible = claudeActiveEntries(entries, branch);
  if (insideToolExchange(visible)(visible.indexOf(entry))) {
    throw new Error("Cannot edit in the middle of a tool call: a call made before this message is answered after it");
  }
  if (!branch.path.includes(entryId)) throw new Error(NOT_ON_BRANCH);
  if (!branch.resumable.has(entryId)) throw new Error("claude cannot resume before the conversation's last compaction: use New session to start from this message");
  throw new Error("Nothing comes before this message for claude to resume at: use New session to start over from it");
}

/** A transcript forked before one of its user messages (claudeTranscriptFork()). */
export interface ClaudeTranscriptFork {
  /** The new session's transcript; null when nothing the chat shows came before the message. */
  text: string | null;
  /** The entries `text` holds, as the source wrote them (empty when `text` is null). */
  entries: unknown[];
}

/** A `user` or `assistant` entry of the main conversation (never a sidechain's). */
function isMainMessageEntry(entry: unknown): boolean {
  return isObject(entry) && (entry.type === "user" || entry.type === "assistant") && entry.isSidechain !== true;
}

/**
 * The conversation before one of the user's messages as a new session's transcript:
 * every line of the active branch claude wrote before that message's entry — the
 * conversation as it stood when the message was sent; another branch's entries
 * (claudeActiveBranch()) and the checkpoints naming them are left out — minus the
 * `queue-operation` lines of the message's own
 * submission (the ones after the conversation's last message before it; claude can write
 * the previous turn's `stop_hook_summary` between them and the message), each entry's
 * `sessionId` made `sessionId`. Entry uuids, compaction boundaries and their summaries,
 * sidechain entries and claude's other bookkeeping are kept as written, so the copy's entry
 * ids are the source's. Nothing the chat shows before the message (the first one): `text`
 * is null and the new session starts fresh. Throws for an id that is no user bubble of the
 * active branch, and for a message that sits inside a tool exchange (a call made before
 * it answered after it).
 */
export function claudeTranscriptFork(text: string, entryId: string, sessionId: string): ClaudeTranscriptFork {
  const all = transcriptLines(text);
  const { hidden } = claudeActiveBranch(all.map((line) => line.entry));
  // Another branch's entries go, and so does a checkpoint naming one of them.
  const otherBranch = (entry: unknown) => isHiddenEntry(entry, hidden)
    || (isObject(entry) && entry.type === "last-prompt" && typeof entry.leafUuid === "string" && hidden.has(entry.leafUuid));
  const lines = hidden.size === 0 ? all : all.filter((line) => !otherBranch(line.entry));
  const at = lines.findIndex((line) => isObject(line.entry) && line.entry.uuid === entryId);
  if (at < 0) throw new Error(hidden.has(entryId) ? NOT_ON_BRANCH : "Invalid entry ID for forking");
  if (!isUserBubbleEntry(lines[at].entry)) throw new Error("A claude session forks only at one of its user messages");
  if (insideToolExchange(lines.map((line) => line.entry))(at)) {
    throw new Error("Cannot fork in the middle of a tool call: a call made before this message is answered after it");
  }
  let lastMessage = at - 1;
  while (lastMessage >= 0 && !isMainMessageEntry(lines[lastMessage].entry)) lastMessage -= 1;
  const kept = lines.slice(0, at).filter((line, index) => index <= lastMessage || !isObject(line.entry) || line.entry.type !== "queue-operation");
  const entries = kept.flatMap((line) => (line.entry === undefined ? [] : [line.entry]));
  if (claudeTranscriptToMessages(entries).messages.length === 0) return { text: null, entries: [] };
  return { text: `${kept.map((line) => lineWithSessionId(line, sessionId)).join("\n")}\n`, entries };
}

/**
 * The subagent sidecars a (forked) transcript refers to: the agent of each
 * of its calls, linked as the history links them — the receipt's `toolUseResult.agentId`,
 * else a sidecar meta's `toolUseId` — and, the same way, the agents those sidecars started.
 */
export function claudeLinkedSidecars(entries: readonly unknown[], sidecars: ClaudeSidecars): string[] {
  const byToolCall = new Map<string, string>();
  for (const [agentId, sidecar] of Object.entries(sidecars)) {
    if (isObject(sidecar.meta) && typeof sidecar.meta.toolUseId === "string") byToolCall.set(sidecar.meta.toolUseId, agentId);
  }
  const linked = new Set<string>();
  const pending: (readonly unknown[])[] = [entries];
  while (pending.length > 0) {
    for (const raw of pending.pop() ?? []) {
      if (!isObject(raw)) continue;
      const agents: string[] = [];
      if (isObject(raw.toolUseResult) && typeof raw.toolUseResult.agentId === "string") agents.push(raw.toolUseResult.agentId);
      const content = raw.type === "assistant" && isObject(raw.message) ? raw.message.content : undefined;
      if (Array.isArray(content)) {
        for (const block of content) {
          const agentId = isObject(block) && block.type === "tool_use" && typeof block.id === "string" ? byToolCall.get(block.id) : undefined;
          if (agentId) agents.push(agentId);
        }
      }
      for (const agentId of agents) {
        if (!sidecars[agentId] || linked.has(agentId)) continue;
        linked.add(agentId);
        pending.push(sidecars[agentId].entries);
      }
    }
  }
  return [...linked].sort();
}

/** Each stdout line → its decoded event; a partial last line waits for the next chunk. */
export class ClaudeLineDecoder {
  private buffer = "";

  push(chunk: string): unknown[] {
    this.buffer += chunk;
    const lines = this.buffer.split("\n");
    this.buffer = lines.pop() ?? "";
    const decoded: unknown[] = [];
    for (const line of lines) {
      if (!line.trim()) continue;
      try {
        decoded.push(JSON.parse(line));
      } catch {
        // Not an event (a stray log line): ignored.
      }
    }
    return decoded;
  }
}
