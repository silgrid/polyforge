// claude sessions as managed child processes. pi sessions run
// in-process (lib/rpc-manager.ts); a claude session is one `claude -p` child
// per live session, speaking stream-json on stdin/stdout. Its events reach the
// page through the adapter (lib/claude-adapter.ts) and the existing SSE stream.
//
// Lifecycle as pi's: the child lives while a page renews the lease, a turn runs,
// a background subagent is unsettled or a background task runs
//, and is ended one
// PI_WEB_IDLE_TIMEOUT_MS after the last renewal; the next
// open or send starts a new child with `--resume <id>`. Every child runs in its
// own process group, which is what is signalled, so no descendant outlives it.
import { spawn, type ChildProcess } from "child_process";
import { randomUUID } from "crypto";
import { accessSync, constants, existsSync, mkdirSync, readFileSync, statSync } from "fs";
import { delimiter, dirname, join } from "path";
import { getBuiltinModels } from "@earendil-works/pi-ai/providers/all";
import type { AgentEventLike } from "./agent-event-wire";
import { writePrivateFileAtomicSync } from "./atomic-file";
import {
  ClaudeLineDecoder,
  ClaudeStreamTranslator,
  claudeBackgroundTaskLine,
  claudeCompactLine,
  claudeEditPoints,
  claudeEditResumePoint,
  claudeInterruptLine,
  claudeLinkedSidecars,
  claudeSetEffortLine,
  claudeSetModelLine,
  claudeSetPermissionModeLine,
  claudeStopTaskLine,
  claudeTaskOutputLine,
  claudeTranscriptContextTokens,
  claudeTranscriptFork,
  claudeTranscriptToMessages,
  claudeTranscriptWithSessionId,
  claudeUserInputLine,
  claudeUserPromptEvents,
  parseClaudeTranscript,
  type ClaudeControlResponse,
  type ClaudeMessageLifecycle,
  type ClaudePromptImage,
  type ClaudeSidecar,
  type ClaudeSidecars,
} from "./claude-adapter";
import { buildClaudeArgv, buildClaudeEnv } from "./claude-argv";
import { CLAUDE_BACKGROUND_TASK_EVENT, CLAUDE_BACKGROUND_TASKS_EVENT, CLAUDE_FORCE_STOP_EVENT, claudeBackgroundTaskLabel, type ClaudeBackgroundWork } from "./claude-background-task-view";
import { claudeConfigDir, claudeSidecarFiles, claudeTranscriptFile } from "./claude-config-dir";
import { ClaudePermissionBroker, claudePermissionDeniedNotice } from "./claude-permission";
import { readClaudePermissionConfig } from "./claude-permission-config";
import {
  EMPTY_CLAUDE_USAGE,
  getClaudeSessionRecord,
  readClaudeIndex,
  recordClaudeUsage,
  updateClaudeSessionRecord,
  upsertClaudeSessionRecord,
  type ClaudeSessionRecord,
} from "./claude-index";
import {
  CLAUDE_EFFORT_DEFAULT,
  CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT,
  CLAUDE_PLAN_UNAVAILABLE_REASON,
  CLAUDE_PROVIDER,
  claudeFirstMessage,
  claudePlanModeAllowed,
  isClaudeEffort,
  isClaudeModelName,
  isClaudeLaunchPermissionMode,
  type ClaudeEffort,
  type ClaudeLaunchPermissionMode,
  type ClaudePermissionMode,
} from "./claude-shared";
import { claudeResolvedModel } from "./claude-probe";
import type { ClaudeSubagentRun } from "./claude-subagent-view";
import type { ContextUsage } from "./pi-types";
import { resolveSessionIdleTimeoutMs } from "./rpc-manager";
import type { SessionFileStats } from "./session-stats";
import type { AgentMessage, ExtensionUiResponse, SessionInfo } from "./types";
import { notifyClaudePermissionPending, notifySessionComplete } from "./web-push";

/**
 * How long Stop's interrupt request gets to end the turn. Past it, a turn still running
 * gets SIGINT to the process group — unless background work is unfinished, which that
 * would end: then a force stop is asked for instead.
 */
export const CLAUDE_INTERRUPT_DEADLINE_MS = 5_000;
/** How long a control request (a model or effort switch) waits for claude's answer. */
export const CLAUDE_CONTROL_DEADLINE_MS = 15_000;
/** How long a child gets after SIGTERM before SIGKILL. */
export const CLAUDE_SHUTDOWN_DEADLINE_MS = 3_000;
const STDERR_TAIL_BYTES = 4096;

/** The first executable `claude` on PATH, looked up on every call (no cache, no path setting). */
export function findClaudeBinary(pathValue: string | undefined = process.env.PATH): string | null {
  for (const dir of (pathValue ?? "").split(delimiter)) {
    if (!dir) continue;
    const candidate = join(dir, process.platform === "win32" ? "claude.exe" : "claude");
    try {
      if (!statSync(candidate).isFile()) continue;
      accessSync(candidate, constants.X_OK);
      return candidate;
    } catch {
      // Not here.
    }
  }
  return null;
}

export interface ClaudeHistory {
  file: string | null;
  messages: AgentMessage[];
  entryIds: string[];
  /** The subagent runs the conversation started, their entries from claude's sidecars. */
  subagents: ClaudeSubagentRun[];
  /** The user messages an in-session edit can start from (claudeEditPoints()). */
  editEntryIds: string[];
}

/** One subagent sidecar as claude wrote it: its jsonl and its meta file's text (null when unreadable). */
interface ClaudeSidecarText {
  agentId: string;
  text: string;
  metaText: string | null;
}

/** Every sidecar of the session, read whole; a missing or unreadable one is left out. */
function readClaudeSidecarTexts(sessionId: string, transcript: string): ClaudeSidecarText[] {
  return claudeSidecarFiles(sessionId, undefined, transcript).flatMap(({ agentId, file, metaFile }) => {
    let text: string;
    try {
      text = readFileSync(file, "utf8");
    } catch {
      return [];
    }
    let metaText: string | null;
    try {
      metaText = readFileSync(metaFile, "utf8");
    } catch {
      metaText = null;
    }
    return [{ agentId, text, metaText }];
  });
}

/** The session's sidecars, parsed (a missing or unreadable one is left out: its run shows no entries). */
function claudeSidecarsOf(texts: readonly ClaudeSidecarText[]): ClaudeSidecars {
  const sidecars: Record<string, ClaudeSidecar> = {};
  for (const { agentId, text, metaText } of texts) {
    let meta: unknown;
    try {
      meta = metaText === null ? undefined : JSON.parse(metaText);
    } catch {
      meta = undefined;
    }
    sidecars[agentId] = { entries: parseClaudeTranscript(text), meta };
  }
  return sidecars;
}

function readClaudeSidecars(sessionId: string, transcript: string): ClaudeSidecars {
  return claudeSidecarsOf(readClaudeSidecarTexts(sessionId, transcript));
}

/**
 * The session's history and file stats, read from claude's own transcript (and its subagents'
 * sidecars) through the adapter: its active branch only, and the messages an
 * in-session edit can start from.
 */
export function readClaudeHistory(sessionId: string): ClaudeHistory {
  const file = claudeTranscriptFile(sessionId);
  if (!file) return { file: null, messages: [], entryIds: [], subagents: [], editEntryIds: [] };
  let text = "";
  try {
    text = readFileSync(file, "utf8");
  } catch {
    return { file, messages: [], entryIds: [], subagents: [], editEntryIds: [] };
  }
  const entries = parseClaudeTranscript(text);
  return {
    file,
    ...claudeTranscriptToMessages(entries, readClaudeSidecars(sessionId, file)),
    editEntryIds: [...claudeEditPoints(entries).keys()],
  };
}

/**
 * A claude model's context window before its child reported one (#368):
 * pi's built-in model catalog, the anthropic entry of the same full name — the catalog a
 * pi session on that model reads; one of claude's aliases reads the full name claude's
 * `initialize` answer resolves it to (claudeResolvedModel(),). null when the
 * catalog has no positive window for it: no meter rather than a guessed one.
 */
export function claudeContextWindow(model: string): number | null {
  const fullName = claudeResolvedModel(model);
  const contextWindow = getBuiltinModels("anthropic").find((entry) => entry.id === fullName)?.contextWindow;
  return typeof contextWindow === "number" && Number.isFinite(contextWindow) && contextWindow > 0 ? contextWindow : null;
}

/**
 * pi's context usage from claude's count and the model's window: null
 * without a window (nothing is shown), unknown tokens and percent when claude reported
 * no count (the header shows `? / <window>`, as for pi after a compaction).
 */
export function claudeContextUsage(tokens: number | null, contextWindow: number | null): ContextUsage | null {
  if (contextWindow === null) return null;
  if (tokens === null) return { tokens: null, percent: null, contextWindow };
  return { tokens, percent: (tokens / contextWindow) * 100, contextWindow };
}

/** The context the session's transcript says its conversation fills (claudeTranscriptContextTokens()); undefined without one. */
export function readClaudeContextTokens(sessionId: string): number | null | undefined {
  const file = claudeTranscriptFile(sessionId);
  if (!file) return undefined;
  try {
    return claudeTranscriptContextTokens(parseClaudeTranscript(readFileSync(file, "utf8")));
  } catch {
    return undefined;
  }
}

/** pi's file stats for a claude session: counts from the transcript, tokens and cost from the index's `result` totals. */
export function claudeSessionStats(record: ClaudeSessionRecord, messages: readonly AgentMessage[]): SessionFileStats {
  const stats: SessionFileStats = {
    userMessages: 0,
    assistantMessages: 0,
    toolCalls: 0,
    toolResults: 0,
    totalMessages: messages.length,
    tokens: {
      input: record.usage.input,
      output: record.usage.output,
      cacheRead: record.usage.cacheRead,
      cacheWrite: record.usage.cacheWrite,
      total: record.usage.input + record.usage.output + record.usage.cacheRead + record.usage.cacheWrite,
    },
    cost: record.usage.cost,
  };
  for (const message of messages) {
    if (message.role === "user") stats.userMessages += 1;
    else if (message.role === "toolResult") stats.toolResults += 1;
    else if (message.role === "assistant") {
      stats.assistantMessages += 1;
      stats.toolCalls += message.content.filter((block) => (block as { type?: string }).type === "toolCall").length;
    }
  }
  return stats;
}

/** A claude session's row for the session list, from its index record (and its transcript's message count). */
export function claudeSessionInfo(
  record: ClaudeSessionRecord,
  history = readClaudeHistory(record.id),
  globalApprovalMode: ClaudePermissionMode = readClaudePermissionConfig().mode,
): SessionInfo {
  return {
    path: history.file ?? "",
    id: record.id,
    cwd: record.cwd,
    ...(record.name ? { name: record.name } : {}),
    created: record.created,
    modified: record.modified,
    messageCount: history.messages.length,
    firstMessage: record.firstMessage || "(no messages)",
    harness: "claude",
    // the permission mode only when it is not the default (absent reads as default);
    // planAvailable only when plan is not (r3: the session's approvals are deny; absent: offered).
    claude: {
      model: record.model,
      effort: record.effort,
      ...(record.launchPermissionMode !== CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT ? { launchPermissionMode: record.launchPermissionMode } : {}),
      ...(claudePlanModeAllowed(claudeSessionApprovalMode(record, globalApprovalMode)) ? {} : { planAvailable: false }),
    },
    ...(record.archived ? { archived: true } : {}),
  };
}

/** Every claude session pi-web created, as list rows. Only the index: no claude projects dir is scanned. */
export function getClaudeSessionInfos(): SessionInfo[] {
  const globalApprovalMode = readClaudePermissionConfig().mode;
  return readClaudeIndex().map((record) => claudeSessionInfo(record, undefined, globalApprovalMode));
}

type Listener = (event: AgentEventLike) => void;

/**
 * The agent commands a claude session answers; every other (pi-only) one is refused.
 * adds the model switch, the effort change and claude's own compaction;
 * adds `fork` (forkClaudeSession()) and a `prompt` with the `entryId` of
 * the user message it replaces (editClaudeSession()). adds `background_task`
 * (moveToBackground()), the first half of a relay to a running foreground subagent.
 * adds `set_permission_mode`, the in-session permission-mode switch.
 * adds `stop_task` (stopTask()) and `task_output` (taskOutput()): one
 * background task stopped, its output read, over claude's own control requests.
 * adds `extension_ui_response`, the page's answer to a permission dialog.
 */
export const CLAUDE_SESSION_COMMANDS: ReadonlySet<string> = new Set([
  "prompt",
  "abort",
  "get_state",
  "get_tools",
  "get_commands",
  "get_session_stats",
  "set_model",
  "set_effort",
  "set_permission_mode",
  "compact",
  "fork",
  "background_task",
  "stop_task",
  "task_output",
  "extension_ui_response",
]);

/** A `fork` (or an edit's `prompt`) command's entry id; null when it names none. */
export function claudeCommandEntryId(command: Record<string, unknown>): string | null {
  return typeof command.entryId === "string" && command.entryId ? command.entryId : null;
}

/** A `background_task` command's `Agent` / `Task` call id; null when it names none. */
export function claudeCommandToolCallId(command: Record<string, unknown>): string | null {
  return typeof command.toolCallId === "string" && command.toolCallId ? command.toolCallId : null;
}

/** A `stop_task` / `task_output` command's claude task id; null when it names none. */
export function claudeCommandTaskId(command: Record<string, unknown>): string | null {
  return typeof command.taskId === "string" && command.taskId ? command.taskId : null;
}

/** A background task's output as claude's `get_task_output` reads it: at most its last 8 KiB. */
export interface ClaudeTaskOutput {
  output: string;
  totalBytes: number;
  truncated: boolean;
}

/** A `set_model` command's model name (`model`, else pi's `modelId`; any name `--model` takes); null when it names none. */
export function claudeCommandModel(command: Record<string, unknown>): string | null {
  const model = command.model ?? command.modelId;
  return isClaudeModelName(model) ? model : null;
}

/** A `set_effort` command's effort (a level or the default entry); null when it names none. */
export function claudeCommandEffort(command: Record<string, unknown>): ClaudeEffort | null {
  return isClaudeEffort(command.effort) ? command.effort : null;
}

/**
 * The approval mode plan is judged by for session `record` ( r3,
 * plan D11): its live child's, fixed at that child's launch; with none, the one its next
 * launch would take — the record's override, else the global mode.
 */
export function claudeSessionApprovalMode(record: ClaudeSessionRecord, globalApprovalMode?: ClaudePermissionMode): ClaudePermissionMode {
  return getClaudeSession(record.id)?.permissionMode ?? record.permissionMode ?? globalApprovalMode ?? readClaudePermissionConfig().mode;
}

/**
 * Why a `set_permission_mode` command is refused before anything is sent:
 * a mode Pi Web does not offer (`mode` not default / acceptEdits / plan), or plan for a
 * session whose approval mode (claudeSessionApprovalMode) is not ask — claude would refuse
 * to leave it (r3, plan D11); null for a mode that may be sent.
 */
export function claudeLaunchPermissionModeRefusal(command: Record<string, unknown>, approvalMode: ClaudePermissionMode): string | null {
  if (!isClaudeLaunchPermissionMode(command.mode)) return `Not a claude permission mode: ${String(command.mode)}`;
  return command.mode === "plan" && !claudePlanModeAllowed(approvalMode) ? CLAUDE_PLAN_UNAVAILABLE_REASON : null;
}

/** A `set_permission_mode` command's mode; throws the refusal for one that may not be sent. */
function claudeCommandLaunchPermissionMode(command: Record<string, unknown>, approvalMode: ClaudePermissionMode): ClaudeLaunchPermissionMode {
  const refusal = claudeLaunchPermissionModeRefusal(command, approvalMode);
  if (refusal) throw new Error(refusal);
  return command.mode as ClaudeLaunchPermissionMode;
}

/** What a permission-mode switch did: the mode in force, and claude's refusal when it kept the old one. */
export interface ClaudeLaunchPermissionModeSwitch {
  launchPermissionMode: ClaudeLaunchPermissionMode;
  /** No live child: only the index changed; the next child starts with this mode. */
  pending?: true;
  rejected?: true;
  error?: string;
  errorCode?: string;
}

/** What a model switch did: the model in force, and claude's refusal when it kept the old one. */
export interface ClaudeModelSwitch {
  model: string;
  /** No live child: only the index changed; the next child starts with `--model <model>`. */
  pending?: true;
  rejected?: true;
  error?: string;
  errorCode?: string;
}

/** How claude's own compaction ended. */
export type ClaudeCompactOutcome = { tokensBefore?: number } | { aborted: true } | { error: string };

/** What an effort change did: the effort in force, and claude's refusal when it kept the old one. */
export interface ClaudeEffortSwitch {
  effort: ClaudeEffort;
  /** Only the index changed; the next child starts with this effort. */
  pending?: true;
  rejected?: true;
  error?: string;
}

/**
 * A switch with no live child (#445): the index is the whole change,
 * nothing is spawned; the next open or prompt starts `--resume` with the new values.
 */
export async function setIdleClaudeSetting(
  id: string,
  command: Record<string, unknown>,
): Promise<ClaudeModelSwitch | ClaudeEffortSwitch | ClaudeLaunchPermissionModeSwitch> {
  if (command.type === "set_permission_mode") {
    const record = getClaudeSessionRecord(id);
    const approvalMode = record ? claudeSessionApprovalMode(record) : readClaudePermissionConfig().mode;
    const launchPermissionMode = claudeCommandLaunchPermissionMode(command, approvalMode);
    await updateClaudeSessionRecord(id, (record) => ({ ...record, launchPermissionMode }));
    return { launchPermissionMode, pending: true };
  }
  if (command.type === "set_model") {
    const model = claudeCommandModel(command);
    if (!model) throw new Error(`Not a claude model: ${String(command.model ?? command.modelId)}`);
    await updateClaudeSessionRecord(id, (record) => ({ ...record, model }));
    return { model, pending: true };
  }
  const effort = claudeCommandEffort(command);
  if (!effort) throw new Error(`Not a claude effort: ${String(command.effort)}`);
  await updateClaudeSessionRecord(id, (record) => ({ ...record, effort }));
  return { effort, pending: true };
}

/** A prompt command's text and images (the composer's `{type: "image", data, mimeType}` entries); throws for an empty one. */
function claudePromptContent(command: Record<string, unknown>): { text: string; images: ClaudePromptImage[] } {
  const message = typeof command.message === "string" ? command.message : "";
  // The API validated the images (validateClaudeImages) before the session was reached.
  const images: ClaudePromptImage[] = Array.isArray(command.images)
    ? command.images.map((image: { data: string; mimeType: string }) => ({ data: image.data, mimeType: image.mimeType }))
    : [];
  if (!message.trim() && images.length === 0) throw new Error("A claude prompt needs text");
  // An image-only prompt sends no (whitespace) text block.
  return { text: message.trim() ? message : "", images };
}

/** What a fork made: pi's `fork` reply. */
export interface ClaudeForkResult {
  cancelled: false;
  newSessionId: string;
}

/**
 * 「新会话」 on a claude message: a new claude session whose conversation is
 * the source's before that user message — pi's fork point. Its transcript is the source's
 * active branch as claude had it when the message was sent
 * (claudeTranscriptFork()), written under a new session id beside the source's, with copies
 * of the subagent sidecars it refers to; then its index record (the source's cwd, current
 * model, effort, Agent language, permission mode and hook switch; no name, no usage yet). Nothing is spawned: the copy's
 * first open `--resume`s it, or starts it fresh when nothing came before the message. The
 * source's transcript, sidecars and record are only read. Refused while the source runs a
 * turn (a compaction included) or holds an unsettled background subagent (its sidecar is
 * still being written and its result has not reached the conversation), naming each one
 * still running (claudeForkRefusal(),).
 */
export async function forkClaudeSession(sourceId: string, entryId: string): Promise<ClaudeForkResult> {
  const record = getClaudeSessionRecord(sourceId);
  if (!record) throw new Error("Session not found");
  const live = getClaudeSession(sourceId);
  if (live?.isRunning()) throw new Error("A claude turn is running; fork when it ends");
  if (live?.hasUnsettledSubagents()) throw new Error(claudeForkRefusal(live.backgroundWork()));
  const transcript = claudeTranscriptFile(sourceId, claudeConfigDir());
  if (!transcript) throw new Error("Invalid entry ID for forking");
  const newSessionId = randomUUID();
  const fork = claudeTranscriptFork(readFileSync(transcript, "utf8"), entryId, newSessionId);
  if (fork.text !== null) {
    const sidecarTexts = readClaudeSidecarTexts(sourceId, transcript);
    const linked = new Set(claudeLinkedSidecars(fork.entries, claudeSidecarsOf(sidecarTexts)));
    const sidecarDir = join(dirname(transcript), newSessionId, "subagents");
    for (const sidecar of sidecarTexts.filter(({ agentId }) => linked.has(agentId))) {
      mkdirSync(sidecarDir, { recursive: true });
      writePrivateFileAtomicSync(join(sidecarDir, `agent-${sidecar.agentId}.jsonl`), claudeTranscriptWithSessionId(sidecar.text, newSessionId));
      if (sidecar.metaText !== null) writePrivateFileAtomicSync(join(sidecarDir, `agent-${sidecar.agentId}.meta.json`), sidecar.metaText);
    }
    // The transcript before the record: a listed session never opens without its history.
    writePrivateFileAtomicSync(join(dirname(transcript), `${newSessionId}.jsonl`), fork.text);
  }
  const now = new Date().toISOString();
  await upsertClaudeSessionRecord({
    id: newSessionId,
    cwd: record.cwd,
    model: record.model,
    effort: record.effort,
    agentLanguage: record.agentLanguage,
    launchPermissionMode: record.launchPermissionMode,
    includeHookEvents: record.includeHookEvents,
    created: now,
    modified: now,
    firstMessage: fork.text === null ? "" : record.firstMessage,
    usage: { ...EMPTY_CLAUDE_USAGE },
  });
  return { cancelled: false, newSessionId };
}

/**
 * Why a fork is refused while a background subagent is unsettled: each
 * subagent still running named by its description; with none running, one that finished
 * whose result claude has not taken up yet. A background task never refuses a fork.
 */
export function claudeForkRefusal(work: readonly ClaudeBackgroundWork[]): string {
  const agents = work.filter((piece) => piece.kind === "agent").map(({ label }) => `"${label}"`).join(", ");
  return agents
    ? `A claude background subagent is still running (${agents}); fork when its result is in`
    : "A claude background subagent finished and claude has not taken up its result yet; fork when it is in";
}

/**
 * Why an operation that restarts claude is refused: the work it would
 * end, each piece named; with none listed, a finished one whose result claude has not
 * taken up yet (its follow-up turn has not started).
 */
export function claudeRestartRefusal(action: string, work: readonly ClaudeBackgroundWork[]): string {
  const named = (kind: ClaudeBackgroundWork["kind"]) => work.filter((piece) => piece.kind === kind).map(({ label }) => `"${label}"`).join(", ");
  const running = [
    ...(named("agent") ? [`A claude background subagent is still running (${named("agent")})`] : []),
    ...(named("task") ? [`A claude background task is still running (${named("task")})`] : []),
  ];
  const what = running.length > 0
    ? running.join("; ")
    : "A claude background subagent or task finished and claude has not taken up its result yet";
  return `${what}. ${action} restarts claude, which would end it; try again when it is done.`;
}

/** One live claude child. A new one (with `--resume`) replaces it once it has exited. */
export class ClaudeSession {
  readonly sessionId: string;
  readonly record: ClaudeSessionRecord;
  readonly configDir: string;
  readonly argv: readonly string[];
  /**
   * The permission mode this child was launched with: the record's override,
   * else the global default read at spawn. Fixed for the child's life.
   */
  readonly permissionMode: ClaudePermissionMode;
  /** The entry an in-session edit's truncating resume keeps up to; null for a plain launch. */
  readonly resumeAt: string | null;
  private readonly child: ChildProcess;
  private readonly translator = new ClaudeStreamTranslator();
  private readonly decoder = new ClaudeLineDecoder();
  private readonly listeners = new Set<Listener>();
  private alive = true;
  private turnRunning = false;
  /**
   * The user messages claude reported `queued` and has not `started`:
   * a `result` ends the turn unless one waits here — that one is claude's next turn.
   * claude's own signals, never a count of `init`s: a task notification absorbed
   * mid-turn opens no turn of its own.
   */
  private readonly queuedMessages = new Set<string>();
  /**
   * Messages sent while a turn ran ( steer), by uuid: written to claude's
   * stdin at once, listed in pi's queue (`queue_update`) until claude starts one, then
   * delivered as a user message with what claude did with it.
   */
  private readonly steering = new Map<string, { text: string; images: ClaudePromptImage[] }>();
  private aborting = false;
  /** The force stop asked for (its CLAUDE_FORCE_STOP_EVENT) until the turn ends; null when none. */
  private forceStop: AgentEventLike | null = null;
  /**
   * The running turn is claude's own `/compact`: announced as pi's
   * compaction (compaction_start / compaction_end) and settled by `settleCompaction`.
   */
  private compaction: { boundary?: { tokensBefore?: number }; settle: (outcome: ClaudeCompactOutcome) => void } | null = null;
  private interruptTimer: ReturnType<typeof setTimeout> | null = null;
  private idleTimer: ReturnType<typeof setTimeout> | null = null;
  private stderrTail = "";
  /** The transcript's count, read once for a child that has reported none yet. */
  private transcriptContext: { tokens: number | null } | null = null;
  /** Control requests written to stdin, by request id, waiting for claude's answer. */
  private readonly pendingControls = new Map<string, (response: ClaudeControlResponse) => void>();
  /** claude's permission prompts waiting for the user's answer, as Pi Web dialogs. */
  private readonly permissions: ClaudePermissionBroker;
  private readonly exited: Promise<void>;
  /**
   * A page may hold this session's background tasks from before this subscription
   *: the child resumed a conversation (an earlier child, or an earlier
   * pi-web, ran it) or has listed a task itself. A new conversation's first child, before
   * any task, owes a new subscriber no task list.
   */
  private tasksOwed: boolean;

  constructor(record: ClaudeSessionRecord, binary: string, options: { resume: boolean; configDir: string; resumeAt?: string }) {
    this.sessionId = record.id;
    this.record = record;
    this.configDir = options.configDir;
    this.resumeAt = options.resume && options.resumeAt ? options.resumeAt : null;
    this.tasksOwed = options.resume;
    this.permissionMode = record.permissionMode ?? readClaudePermissionConfig().mode;
    this.argv = buildClaudeArgv({
      sessionId: record.id,
      resume: options.resume,
      ...(this.resumeAt ? { resumeAt: this.resumeAt } : {}),
      model: record.model,
      effort: record.effort,
      agentLanguage: record.agentLanguage,
      permissionMode: this.permissionMode,
      launchPermissionMode: record.launchPermissionMode,
      includeHookEvents: record.includeHookEvents,
    });
    this.permissions = new ClaudePermissionBroker({
      emit: (event) => this.emit(event),
      write: (line) => {
        this.child.stdin?.write(line);
      },
      language: record.agentLanguage,
      onOpen: (_requestId, request) => {
        // The switch is read per request: a change in Settings applies to the next prompt.
        if (!readClaudePermissionConfig().push) return;
        const name = typeof request.display_name === "string" && request.display_name ? request.display_name : request.tool_name;
        void notifyClaudePermissionPending(this.sessionId, this.currentRecord().name, name)
          .catch((error) => console.error("[pi-web] failed to send permission push:", error instanceof Error ? error.message : error));
      },
    });
    // A child resumed for an existing session knows its runs: a message
    // relayed to a finished subagent reopens that run, not a run of the SendMessage call.
    if (options.resume) this.translator.seedRuns(readClaudeHistory(record.id).subagents);
    this.child = spawn(binary, [...this.argv], {
      cwd: record.cwd,
      env: buildClaudeEnv(process.env) as NodeJS.ProcessEnv,
      stdio: ["pipe", "pipe", "pipe"],
      // Its own process group: shutdown signals the group, descendants included.
      detached: true,
    });
    this.exited = new Promise((resolve) => this.child.once("close", () => resolve()));
    this.child.stdout?.setEncoding("utf8");
    this.child.stdout?.on("data", (chunk: string) => {
      for (const event of this.decoder.push(chunk)) this.handleClaudeEvent(event);
    });
    this.child.stderr?.setEncoding("utf8");
    this.child.stderr?.on("data", (chunk: string) => {
      this.stderrTail = (this.stderrTail + chunk).slice(-STDERR_TAIL_BYTES);
    });
    // A write after the child died must not crash the server.
    this.child.stdin?.on("error", () => {});
    this.child.once("error", (error) => {
      this.stderrTail = (this.stderrTail + String(error.message)).slice(-STDERR_TAIL_BYTES);
      this.handleExit(null);
    });
    this.child.once("close", (code) => this.handleExit(code));
    this.resetIdleTimer();
  }

  get pid(): number | undefined {
    return this.child.pid;
  }

  get isStreaming(): boolean {
    return this.turnRunning;
  }

  get streamingMessage(): unknown {
    return this.translator.streamingMessage ?? undefined;
  }

  isAlive(): boolean {
    return this.alive;
  }

  isRunning(): boolean {
    return this.alive && this.turnRunning;
  }

  /** A permission prompt of claude's waits for the user's answer. */
  isAwaitingInput(): boolean {
    return this.alive && this.permissions.size > 0;
  }

  /**
   * A background subagent still running, or finished with claude's follow-up turn
   * not started yet: the session is unsettled — no idle reap, no
   * completion announcement, `subagentsActive` on the lease.
   */
  hasUnsettledSubagents(): boolean {
    return this.alive && this.translator.hasUnsettledSubagents();
  }

  /**
   * A background task (a `Bash` in the background, a `Monitor`) claude still runs
   *: not unsettled — it holds no announcement, lease flag or fork — but
   * the child is not reaped while it runs, since ending the child ends the task.
   */
  hasRunningBackgroundTasks(): boolean {
    return this.alive && this.translator.hasRunningBackgroundTasks();
  }

  /**
   * The background work this child would take down with it: background
   * subagents still running, then background tasks still running. One list for every
   * caller that has to name it.
   */
  backgroundWork(): ClaudeBackgroundWork[] {
    if (!this.alive) return [];
    const agents = this.translator.runningBackgroundSubagents()
      .map(({ id, label }): ClaudeBackgroundWork => ({ kind: "agent", id, label }));
    const tasks = this.translator.runningBackgroundTasks()
      .map((task): ClaudeBackgroundWork => ({ kind: "task", id: task.taskId, label: claudeBackgroundTaskLabel(task) }));
    return [...agents, ...tasks];
  }

  /**
   * Unfinished background work: an unsettled background subagent or a
   * background task still running. Restarting claude now would end it, so an operation
   * that restarts it is refused (restartRefusal()).
   */
  hasUnfinishedBackgroundWork(): boolean {
    return this.hasUnsettledSubagents() || this.hasRunningBackgroundTasks();
  }

  /** The refusal of an operation that restarts claude (`action`, e.g. "Editing a message"); null when nothing would be lost. */
  restartRefusal(action: string): string | null {
    return this.hasUnfinishedBackgroundWork() ? claudeRestartRefusal(action, this.backgroundWork()) : null;
  }

  /**
   * Subscribes a page's stream: a new subscriber (a reconnect, a session
   * switched back to, a page after a pi-web restart) first gets the whole list of the
   * background tasks this child runs (CLAUDE_BACKGROUND_TASKS_EVENT, empty included: the
   * page replaces its tasks by it, so one that ended unseen leaves its list), then a force
   * stop still asked for. Only a child a page may hold tasks of sends the list (tasksOwed).
   */
  onEvent(listener: Listener): () => void {
    this.listeners.add(listener);
    if (this.tasksOwed) this.deliver(listener, { type: CLAUDE_BACKGROUND_TASKS_EVENT, tasks: this.alive ? this.translator.runningBackgroundTasks() : [] });
    if (this.forceStop) this.deliver(listener, this.forceStop);
    // A page that (re)connects sees every permission dialog still waiting.
    for (const dialog of this.permissions.pending()) listener(dialog);
    return () => {
      this.listeners.delete(listener);
    };
  }

  /** A page's lease renewal: the idle countdown starts over. */
  renewIdleTimer(): void {
    this.resetIdleTimer();
  }

  async send(command: Record<string, unknown>): Promise<unknown> {
    const type = command.type;
    if (type !== "get_state") this.resetIdleTimer();
    switch (type) {
      case "prompt":
        return this.prompt(command);
      case "abort":
        this.abort(command.force === true);
        return null;
      case "get_state":
        return this.state();
      case "set_model":
        return this.setModel(command);
      case "set_effort":
        return this.setEffort(command);
      case "set_permission_mode":
        return this.setLaunchPermissionMode(command);
      case "compact":
        return this.compact();
      case "background_task":
        return this.moveToBackground(command);
      case "stop_task":
        return this.stopTask(command);
      case "task_output":
        return this.taskOutput(command);
      case "extension_ui_response":
        // An id the broker does not hold (answered, timed out, withdrawn) is ignored, as pi does.
        this.permissions.respond(command as unknown as ExtensionUiResponse);
        return null;
      case "fork": {
        const entryId = claudeCommandEntryId(command);
        if (!entryId) throw new Error("Invalid entry ID for forking");
        return forkClaudeSession(this.sessionId, entryId);
      }
      case "get_tools":
        // claude's tools are the user's claude config's, not pi-web's to select or describe.
        return [];
      case "get_commands":
        return { commands: [] };
      case "get_session_stats": {
        const { file, messages } = readClaudeHistory(this.sessionId);
        const record = this.currentRecord();
        return { ...claudeSessionStats(record, messages), sessionFile: file ?? undefined, sessionId: this.sessionId, sessionName: record.name };
      }
      default:
        throw new Error(`${String(type)} is not available for claude sessions`);
    }
  }

  /** The index's record as it is now (renamed, usage added); the launch's copy if it was removed. */
  private currentRecord(): ClaudeSessionRecord {
    return getClaudeSessionRecord(this.sessionId) ?? this.record;
  }

  private state(): Record<string, unknown> {
    // The index's current model and effort (a switch rewrites them), not the launch copy.
    const record = this.currentRecord();
    return {
      sessionId: this.sessionId,
      sessionFile: claudeTranscriptFile(this.sessionId) ?? "",
      isStreaming: this.turnRunning,
      isPromptRunning: this.turnRunning,
      isBashRunning: false,
      isCompacting: this.compaction !== null,
      autoCompactionEnabled: false,
      model: { id: record.model, provider: CLAUDE_PROVIDER },
      claudeEffort: record.effort,
      claudeLaunchPermissionMode: record.launchPermissionMode,
      // r3: plan is offered to this child only when it was launched with ask.
      claudePlanAvailable: claudePlanModeAllowed(this.permissionMode),
      messageCount: 0,
      pendingMessageCount: this.steering.size,
      queuedMessages: this.queue(),
      contextUsage: this.contextUsage(record.model),
      thinkingLevel: "off",
      extensionStatuses: [],
      extensionWidgets: [],
    };
  }

  /**
   * The header's context meter: what this child last reported the
   * conversation fills, else — a resumed child before its first reply — the transcript's
   * count, over the current model's window (a model switch changes the window only).
   * The window is the one this child's claude reported for the model in a `result`
   * (an account without the long context gets a smaller one than the
   * catalog's), else, before it reported one, pi's catalog entry.
   */
  private contextUsage(model: string): ContextUsage | null {
    // claude reports a window under the full name an alias resolves to.
    const contextWindow = this.translator.contextWindow(claudeResolvedModel(model)) ?? claudeContextWindow(model);
    if (contextWindow === null) return null;
    let tokens = this.translator.contextTokens;
    if (tokens === undefined) {
      // Read once: from here on only this child writes the transcript, and it reports as it writes.
      this.transcriptContext ??= { tokens: readClaudeContextTokens(this.sessionId) ?? null };
      tokens = this.transcriptContext.tokens;
    }
    return claudeContextUsage(tokens, contextWindow);
  }

  /** pi's queue of a claude session: the messages sent mid-turn that claude has not started. */
  private queue(): { steering: string[]; followUp: string[] } {
    return { steering: [...this.steering.values()].map((message) => message.text), followUp: [] };
  }

  private prompt(command: Record<string, unknown>): null {
    const { text, images } = claudePromptContent(command);
    if (!this.alive) throw new Error("The claude process has exited; send again to resume it");
    // A compaction is a guarded turn: nothing is sent until it ends.
    if (this.compaction) throw new Error("A claude compaction is running; send when it ends");
    if (!existsSync(this.record.cwd)) throw new Error(`The session folder no longer exists: ${this.record.cwd}`);
    const uuid = randomUUID();
    if (this.turnRunning) {
      // Steer: claude takes the message into the running turn or queues it
      // as its next turn; its `started` says which (messageLifecycle()).
      this.steering.set(uuid, { text, images });
      this.child.stdin?.write(claudeUserInputLine(text, images, uuid));
      this.emit({ type: "queue_update", ...this.queue() });
      this.touchRecord(text, images);
      return null;
    }
    this.turnRunning = true;
    this.aborting = false;
    this.child.stdin?.write(claudeUserInputLine(text, images, uuid));
    this.emit({ type: "agent_start" });
    for (const event of claudeUserPromptEvents(text, images)) this.emit(event);
    this.touchRecord(text, images);
    return null;
  }

  /** A sent message's mark in the index: the session was modified, and its first message if it had none. */
  private touchRecord(text: string, images: readonly ClaudePromptImage[]): void {
    void updateClaudeSessionRecord(this.sessionId, (record) => ({
      ...record,
      modified: new Date().toISOString(),
      firstMessage: record.firstMessage || claudeFirstMessage(text, images.length),
    })).catch((error) => console.error("[pi-web] claude index update failed:", error instanceof Error ? error.message : error));
  }

  /** Refuses a switch that would land mid-turn: it changes nothing. */
  private assertIdle(what: string): void {
    if (this.turnRunning) throw new Error(`A claude turn is running; ${what} when it ends`);
  }

  /** Writes a control request and waits for claude's answer; a missing answer is an error, never a hang. */
  private controlRequest(line: (requestId: string) => string): Promise<ClaudeControlResponse> {
    const requestId = randomUUID();
    return new Promise((resolve) => {
      const timer = setTimeout(() => {
        this.pendingControls.delete(requestId);
        this.resetIdleTimer();
        resolve({ requestId, subtype: "error", error: `claude did not answer within ${CLAUDE_CONTROL_DEADLINE_MS / 1000} s` });
      }, CLAUDE_CONTROL_DEADLINE_MS);
      timer.unref?.();
      this.pendingControls.set(requestId, (response) => {
        clearTimeout(timer);
        this.pendingControls.delete(requestId);
        // An answered request is activity: the idle countdown starts over from it.
        this.resetIdleTimer();
        resolve(response);
      });
      this.child.stdin?.write(line(requestId));
    });
  }

  /**
   * The hot model switch: claude's own `set_model` on the live child,
   * same process and transcript. Accepted: the index records the model and every later
   * child starts with it. Refused by a live child: claude's text comes back and nothing
   * changes. The child exits before answering: recorded as an idle switch, the next
   * child starts with `--model <model>`.
   */
  private async setModel(command: Record<string, unknown>): Promise<ClaudeModelSwitch> {
    const model = claudeCommandModel(command);
    if (!model) throw new Error(`Not a claude model: ${String(command.model ?? command.modelId)}`);
    this.assertIdle("switch the model");
    if (!this.alive) return setIdleClaudeSetting(this.sessionId, { type: "set_model", model }) as Promise<ClaudeModelSwitch>;
    const answer = await this.controlRequest((requestId) => claudeSetModelLine(requestId, model));
    // The child went away before answering: the switch is recorded like an idle one.
    if (!this.alive) return setIdleClaudeSetting(this.sessionId, { type: "set_model", model }) as Promise<ClaudeModelSwitch>;
    if (answer.subtype !== "success") {
      return {
        model: this.currentRecord().model,
        rejected: true,
        error: answer.error ?? "claude refused the model",
        ...(answer.errorCode ? { errorCode: answer.errorCode } : {}),
      };
    }
    const answered = answer.response?.model;
    const active = isClaudeModelName(answered) ? answered : model;
    await updateClaudeSessionRecord(this.sessionId, (record) => ({ ...record, model: active }));
    return { model: active };
  }

  /**
   * An effort change. A level goes to the live child as
   * `apply_flag_settings {effortLevel}`, which the recording proves reaches the next
   * turn; the default entry has no such value, so the idle child is ended and the next
   * one starts without `--effort`. Either way the index records it first for every later child.
   * The default is refused, changing nothing, while background work is unfinished
   *: ending the child would end it, and claude has no live equivalent
   * (`effortLevel: null` is its own default, not "no --effort").
   */
  private async setEffort(command: Record<string, unknown>): Promise<ClaudeEffortSwitch> {
    const effort = claudeCommandEffort(command);
    if (!effort) throw new Error(`Not a claude effort: ${String(command.effort)}`);
    this.assertIdle("change the effort");
    const refusal = effort === CLAUDE_EFFORT_DEFAULT ? this.restartRefusal("Changing the effort to default") : null;
    if (refusal) return { effort: this.currentRecord().effort, rejected: true, error: refusal };
    if (!this.alive || effort === CLAUDE_EFFORT_DEFAULT) {
      const result = await setIdleClaudeSetting(this.sessionId, { type: "set_effort", effort }) as ClaudeEffortSwitch;
      if (this.alive && !this.turnRunning) await this.shutdown();
      return result;
    }
    const answer = await this.controlRequest((requestId) => claudeSetEffortLine(requestId, effort));
    if (!this.alive) return setIdleClaudeSetting(this.sessionId, { type: "set_effort", effort }) as Promise<ClaudeEffortSwitch>;
    if (answer.subtype !== "success") {
      return { effort: this.currentRecord().effort, rejected: true, error: answer.error ?? "claude refused the effort" };
    }
    await updateClaudeSessionRecord(this.sessionId, (record) => ({ ...record, effort }));
    return { effort };
  }

  /**
   * The in-session permission-mode switch: claude's own
   * `set_permission_mode` on the live child, not idle-guarded (claude takes it mid-turn).
   * Accepted: the index records it and every later child starts with it; it is the
   * session's own, Settings › Claude is not changed. Refused by claude: its text comes
   * back and nothing changes. No live child (or one that exits before answering): the
   * index alone. Plan is refused before anything is sent unless this child was launched
   * with's ask (r3, plan D11); with no live child, the next launch's mode decides.
   */
  private async setLaunchPermissionMode(command: Record<string, unknown>): Promise<ClaudeLaunchPermissionModeSwitch> {
    const launchPermissionMode = claudeCommandLaunchPermissionMode(command, this.alive ? this.permissionMode : claudeSessionApprovalMode(this.currentRecord()));
    const idle = () => setIdleClaudeSetting(this.sessionId, { type: "set_permission_mode", mode: launchPermissionMode }) as Promise<ClaudeLaunchPermissionModeSwitch>;
    if (!this.alive) return idle();
    const answer = await this.controlRequest((requestId) => claudeSetPermissionModeLine(requestId, launchPermissionMode));
    if (!this.alive) return idle();
    if (answer.subtype !== "success") {
      return {
        launchPermissionMode: this.currentRecord().launchPermissionMode,
        rejected: true,
        error: answer.error ?? "claude refused the permission mode",
        ...(answer.errorCode ? { errorCode: answer.errorCode } : {}),
      };
    }
    await updateClaudeSessionRecord(this.sessionId, (record) => ({ ...record, launchPermissionMode }));
    return { launchPermissionMode };
  }

  /**
   * Moves a running foreground subagent to the background: claude's own
   * `background_tasks` control request for its `Agent` / `Task` call, the TUI's Ctrl+B.
   * Meant for a running turn, so not idle-guarded; it pauses or stops nothing, the main
   * agent only stops waiting for the call. Resolves with claude's answer, rejects with
   * claude's error (or a child that went away before answering).
   */
  private async moveToBackground(command: Record<string, unknown>): Promise<{ backgrounded: boolean }> {
    const toolCallId = claudeCommandToolCallId(command);
    if (!toolCallId) throw new Error("background_task needs the tool call id of a running Agent call");
    if (!this.alive) throw new Error("background_task needs a running session: no live claude child");
    const answer = await this.controlRequest((requestId) => claudeBackgroundTaskLine(requestId, toolCallId));
    if (answer.subtype !== "success") throw new Error(answer.error ?? "claude refused to move the subagent to the background");
    return { backgrounded: answer.response?.backgrounded === true };
  }

  /**
   * Stops one background task: claude's own `stop_task` control request,
   * on the live child only; claude settles the task (task_updated `killed`, its
   * notification), which reaches the page as its `claude_background_task` snapshot.
   * Not idle-guarded. Resolves once claude accepts, rejects with claude's error.
   */
  private async stopTask(command: Record<string, unknown>): Promise<{ stopped: true }> {
    const taskId = this.liveTaskId(command, "stop_task");
    const answer = await this.controlRequest((requestId) => claudeStopTaskLine(requestId, taskId));
    if (answer.subtype !== "success") throw new Error(answer.error ?? "claude refused to stop the task");
    return { stopped: true };
  }

  /** Reads a background task's output: claude's own `get_task_output`, on the live child only; rejects with claude's error. */
  private async taskOutput(command: Record<string, unknown>): Promise<ClaudeTaskOutput> {
    const taskId = this.liveTaskId(command, "task_output");
    const answer = await this.controlRequest((requestId) => claudeTaskOutputLine(requestId, taskId));
    if (answer.subtype !== "success") throw new Error(answer.error ?? "claude refused to read the task's output");
    const response = answer.response ?? {};
    return {
      output: typeof response.output === "string" ? response.output : "",
      totalBytes: typeof response.total_bytes === "number" ? response.total_bytes : 0,
      truncated: response.truncated === true,
    };
  }

  /** A task command's task id, refused before anything is written when it names none or no child is alive. */
  private liveTaskId(command: Record<string, unknown>, type: string): string {
    const taskId = claudeCommandTaskId(command);
    if (!taskId) throw new Error(`${type} needs the task id of a background task`);
    if (!this.alive) throw new Error(`${type} needs a running session: no live claude child`);
    return taskId;
  }

  /**
   * claude's own compaction: the bare `/compact` on stdin, as a turn
   * (nothing else is sent until it ends; Stop interrupts it like any turn). Resolves
   * with claude's pre-compaction token count once its `result` arrives, `{ aborted }`
   * after Stop; rejects with claude's own error. No restart, resume or instructions.
   */
  private compact(): Promise<{ tokensBefore?: number } | { aborted: true }> {
    this.assertIdle("compact");
    if (!this.alive) throw new Error("The claude process has exited; send again to resume it");
    this.turnRunning = true;
    this.aborting = false;
    const outcome = new Promise<ClaudeCompactOutcome>((settle) => {
      this.compaction = { settle };
    });
    this.child.stdin?.write(claudeCompactLine());
    this.emit({ type: "compaction_start", reason: "manual" });
    return outcome.then((result) => {
      if ("error" in result) throw new Error(result.error);
      return result;
    });
  }

  /** Ends the compaction turn: pi's compaction_end with claude's number, or the stop / error. */
  private settleCompaction(outcome: ClaudeCompactOutcome): void {
    const compaction = this.compaction;
    if (!compaction) return;
    this.compaction = null;
    this.turnRunning = false;
    this.aborting = false;
    // As endTurn(): the page drops a force stop on this compaction_end.
    this.forceStop = null;
    if (this.interruptTimer) {
      clearTimeout(this.interruptTimer);
      this.interruptTimer = null;
    }
    if ("error" in outcome) this.emit({ type: "compaction_end", reason: "manual", errorMessage: outcome.error });
    else if ("aborted" in outcome) this.emit({ type: "compaction_end", reason: "manual", aborted: true });
    else this.emit({ type: "compaction_end", reason: "manual", result: outcome });
    this.resetIdleTimer();
    compaction.settle(outcome);
  }

  /**
   * Stop, only while a turn runs (an idle interrupt stops claude's background agents,
   *): claude's interrupt for the turn alone, its background work spared
   * (claudeInterruptLine(),). A turn still running at the deadline gets
   * SIGINT to the process group when no background work runs (backgroundWork(): a finished
   * one awaiting claude's follow-up turn has nothing a signal would end); otherwise nothing
   * is signalled and a force stop is asked for (CLAUDE_FORCE_STOP_EVENT, naming the work).
   * `force` (the page's confirmation) signals the group at once, background work and all.
   */
  private abort(force: boolean): void {
    if (!this.turnRunning || !this.alive) return;
    this.aborting = true;
    if (force) {
      this.signalGroup("SIGINT");
      return;
    }
    this.child.stdin?.write(claudeInterruptLine(randomUUID()));
    if (this.interruptTimer) clearTimeout(this.interruptTimer);
    this.interruptTimer = setTimeout(() => {
      this.interruptTimer = null;
      if (!this.turnRunning) return;
      const work = this.backgroundWork();
      if (work.length === 0) {
        this.signalGroup("SIGINT");
        return;
      }
      this.forceStop = { type: CLAUDE_FORCE_STOP_EVENT, work };
      this.emit(this.forceStop);
    }, CLAUDE_INTERRUPT_DEADLINE_MS);
    this.interruptTimer.unref?.();
  }

  private handleClaudeEvent(event: unknown): void {
    const { events, turnEnd, claudeTurnStart, control, compactBoundary, lifecycle, permissionRequest, permissionCancel, permissionDenied } =
      this.translator.push(event);
    if (control) this.pendingControls.get(control.requestId)?.(control);
    // claude's permission frames: a prompt opens a dialog, a withdrawal closes
    // it, a denial claude made on its own is a warning notice.
    if (permissionRequest) {
      this.permissions.open(permissionRequest.requestId, permissionRequest.request);
      this.resetIdleTimer();
    }
    if (permissionCancel) this.permissions.cancel(permissionCancel);
    if (turnEnd) this.permissions.endTurn();
    if (permissionDenied) this.emit(claudePermissionDeniedNotice(permissionDenied, this.record.agentLanguage));
    const compaction = this.compaction;
    if (compaction) {
      // A compaction turn is not a reply: only its boundary and its result count.
      if (compactBoundary) compaction.boundary = compactBoundary;
      if (!turnEnd) return;
      void recordClaudeUsage(this.sessionId, turnEnd.usage)
        .catch((error) => console.error("[pi-web] claude usage update failed:", error instanceof Error ? error.message : error));
      if (this.aborting) this.settleCompaction({ aborted: true });
      else if (turnEnd.error) this.settleCompaction({ error: this.withConfigDir(turnEnd.error) });
      else if (compaction.boundary) this.settleCompaction(compaction.boundary);
      else this.settleCompaction({ error: "claude did not compact the conversation" });
      return;
    }
    if (lifecycle) this.messageLifecycle(lifecycle);
    if (claudeTurnStart) this.startClaudeTurn();
    for (const translated of events) this.emit(translated);
    if (!turnEnd) return;
    void recordClaudeUsage(this.sessionId, turnEnd.usage)
      .catch((error) => console.error("[pi-web] claude usage update failed:", error instanceof Error ? error.message : error));
    const aborted = this.aborting;
    if (turnEnd.error && !aborted) this.emit({ type: "prompt_error", errorMessage: this.withConfigDir(turnEnd.error) });
    // A stopped turn ends with its result; otherwise a result ends the turn unless claude
    // still holds a message of ours it has not started (its next turn, steer-queued/).
    if (aborted || this.queuedMessages.size === 0) this.endTurn(aborted, !turnEnd.error);
  }

  /**
   * claude's word on a message pi-web wrote: waiting in its queue until it
   * starts. A message sent mid-turn leaves pi's queue then and is delivered, absorbed into
   * the running turn or queued as a turn of its own; one claude cancelled just leaves.
   */
  private messageLifecycle({ uuid, state, absorbed }: ClaudeMessageLifecycle): void {
    if (state === "queued") {
      this.queuedMessages.add(uuid);
      return;
    }
    this.queuedMessages.delete(uuid);
    // A message claude still held when a stopped turn ended runs now, as a turn of its own.
    if (state === "started") this.startClaudeTurn();
    const steered = this.steering.get(uuid);
    if (!steered || (state !== "started" && state !== "cancelled")) return;
    this.steering.delete(uuid);
    if (state === "started") {
      for (const event of claudeUserPromptEvents(steered.text, steered.images, Date.now(), absorbed ? "absorbed" : "queued")) this.emit(event);
    }
    this.emit({ type: "queue_update", ...this.queue() });
  }

  /**
   * A turn no prompt of this moment started: claude's own follow-up after a background
   * subagent finished, or a message claude kept queued past a Stop. Alone it is a turn
   * of its own (`agent_start`, then its normal end); within a running turn it changes nothing.
   */
  private startClaudeTurn(): void {
    if (this.turnRunning) return;
    this.turnRunning = true;
    this.aborting = false;
    this.emit({ type: "agent_start" });
  }

  private withConfigDir(message: string): string {
    return `${message} (claude config dir: ${this.configDir})`;
  }

  private endTurn(aborted: boolean, completed: boolean): void {
    if (!this.turnRunning) return;
    this.turnRunning = false;
    this.aborting = false;
    // The turn ended: no force stop stands (the page drops it on this agent_settled).
    this.forceStop = null;
    if (this.interruptTimer) {
      clearTimeout(this.interruptTimer);
      this.interruptTimer = null;
    }
    // A subagent whose end starts another turn here: the page holds its chime and
    // notification until the last follow-up turn ends, and so does the push.
    const subagentsActive = this.hasUnsettledSubagents();
    const flags = { ...(aborted ? { aborted: true } : {}), ...(subagentsActive ? { subagentsActive: true } : {}) };
    this.emit({ type: "agent_settled", ...flags });
    this.emit({ type: "prompt_done", ...flags });
    this.resetIdleTimer();
    if (completed && !aborted && !subagentsActive) {
      void notifySessionComplete(this.sessionId, this.currentRecord().name)
        .catch((error) => console.error("[pi-web] failed to send completion push:", error instanceof Error ? error.message : error));
    }
  }

  private handleExit(code: number | null): void {
    if (!this.alive) return;
    this.alive = false;
    if (this.idleTimer) clearTimeout(this.idleTimer);
    this.idleTimer = null;
    // Whatever the group still holds goes with it.
    this.signalGroup("SIGKILL");
    for (const [requestId, answer] of [...this.pendingControls]) answer({ requestId, subtype: "error", error: "claude exited before it answered" });
    // Its permission prompts die with it: the dialogs close, nothing is answered.
    this.permissions.closeAll();
    this.queuedMessages.clear();
    this.steering.clear();
    if (this.compaction) {
      const detail = this.stderrTail.trim() || `claude exited${code === null ? "" : ` with code ${code}`}`;
      this.settleCompaction(this.aborting ? { aborted: true } : { error: this.withConfigDir(detail) });
    }
    if (this.turnRunning) {
      const aborted = this.aborting;
      for (const event of this.translator.flush()) this.emit(event);
      if (!aborted) {
        const detail = this.stderrTail.trim() || `claude exited${code === null ? "" : ` with code ${code}`}`;
        this.emit({ type: "prompt_error", errorMessage: this.withConfigDir(detail) });
      }
      this.endTurn(aborted, false);
    }
    // The runs it still had running end with it: the running list drops them.
    for (const event of this.translator.settleOnExit()) this.emit(event);
    unregister(this);
    this.emit({ type: "session_shutdown" });
    this.listeners.clear();
  }

  private signalGroup(signal: NodeJS.Signals): void {
    const pid = this.child.pid;
    if (!pid) return;
    try {
      process.kill(process.platform === "win32" ? pid : -pid, signal);
    } catch {
      // Already gone.
    }
  }

  private resetIdleTimer(): void {
    if (this.idleTimer) clearTimeout(this.idleTimer);
    this.idleTimer = null;
    if (!this.alive) return;
    const timeoutMs = resolveSessionIdleTimeoutMs();
    if (timeoutMs === 0) return;
    this.idleTimer = setTimeout(() => {
      this.idleTimer = null;
      // A running turn keeps the child, as a running pi session is kept; so does an
      // unsettled subagent, whose end starts claude's follow-up turn here, a background
      // task still running, which ending the child would kill, and a
      // control request still waiting for claude's answer, and a permission
      // prompt waiting for the user's.
      if (this.turnRunning || this.hasUnsettledSubagents() || this.hasRunningBackgroundTasks() || this.pendingControls.size > 0 || this.permissions.size > 0) {
        this.resetIdleTimer();
        return;
      }
      void this.shutdown();
    }, timeoutMs);
    this.idleTimer.unref?.();
  }

  /** Ends the child: stdin closed, SIGTERM to the group, SIGKILL after the deadline. */
  async shutdown(): Promise<void> {
    if (!this.alive) return;
    try { this.child.stdin?.end(); } catch { /* closed */ }
    this.signalGroup("SIGTERM");
    let timer: ReturnType<typeof setTimeout> | undefined;
    const deadline = new Promise<void>((resolve) => {
      timer = setTimeout(() => {
        this.signalGroup("SIGKILL");
        resolve();
      }, CLAUDE_SHUTDOWN_DEADLINE_MS);
    });
    await Promise.race([this.exited, deadline]);
    if (timer) clearTimeout(timer);
    await Promise.race([this.exited, new Promise<void>((resolve) => setTimeout(resolve, 1_000).unref?.())]);
  }

  private emit(event: AgentEventLike): void {
    if (event.type === CLAUDE_BACKGROUND_TASK_EVENT) this.tasksOwed = true;
    for (const listener of [...this.listeners]) this.deliver(listener, event);
  }

  private deliver(listener: Listener, event: AgentEventLike): void {
    try {
      listener(event);
    } catch (error) {
      console.error(`[pi-web] failed to deliver ${event.type} event:`, error instanceof Error ? error.message : error);
    }
  }
}

// Shared across route bundles and hot reloads, like the pi registry.
const REGISTRY = Symbol.for("pi-web.claudeSessions");
function registry(): Map<string, ClaudeSession> {
  const store = globalThis as Record<symbol, Map<string, ClaudeSession> | undefined>;
  return (store[REGISTRY] ??= new Map());
}

function unregister(session: ClaudeSession): void {
  if (registry().get(session.sessionId) === session) registry().delete(session.sessionId);
}

/**
 * The truncation the next child of a session starts with: set by an edit
 * before it ends the live child, taken by whichever start comes next — the edit's own, or
 * a page's stream reconnecting first — so no plain `--resume` slips in between.
 */
const PENDING_RESUMES = Symbol.for("pi-web.claudePendingResumes");
function pendingResumes(): Map<string, string> {
  const store = globalThis as Record<symbol, Map<string, string> | undefined>;
  return (store[PENDING_RESUMES] ??= new Map());
}

/** The live child of a session, if any. */
export function getClaudeSession(sessionId: string): ClaudeSession | undefined {
  const session = registry().get(sessionId);
  return session?.isAlive() ? session : undefined;
}

/**
 * The session's live child, or a new one: `--resume <id>` once claude has a
 * transcript for the id, `--session-id <id>` before (a new conversation); an edit's
 * pending truncation adds `--resume-session-at <entry>`.
 * Throws when claude is not on PATH or the folder is gone.
 */
export function startClaudeSession(record: ClaudeSessionRecord): ClaudeSession {
  const existing = getClaudeSession(record.id);
  if (existing) return existing;
  const binary = findClaudeBinary();
  if (!binary) throw new Error("claude was not found on PATH");
  if (!existsSync(record.cwd)) throw new Error(`The session folder no longer exists: ${record.cwd}`);
  // The system claude's own config dir: resolved for the resume check and error text, never created.
  const configDir = claudeConfigDir();
  const resumeAt = pendingResumes().get(record.id);
  pendingResumes().delete(record.id);
  const session = new ClaudeSession(record, binary, {
    resume: claudeTranscriptFile(record.id, configDir) !== null,
    configDir,
    ...(resumeAt ? { resumeAt } : {}),
  });
  registry().set(record.id, session);
  return session;
}

/**
 * 「从此处编辑」 on a claude user message, sent: pi's in-session edit. The same
 * session continues from before that message with the edited prompt, through claude's own
 * truncating resume: the idle child is ended, the next one starts `--resume <id>
 * --resume-session-at <the message's parent>` (claudeEditResumePoint()) and gets the prompt.
 * The replaced turns stay in claude's transcript on a branch of their own, which the history
 * no longer shows. Refused, changing nothing, for a message no edit can start from, while a
 * turn or compaction runs, and while background work is unfinished — an unsettled background
 * subagent or a running background task, each named: ending the child would end it.
 */
export async function editClaudeSession(record: ClaudeSessionRecord, entryId: string, command: Record<string, unknown>): Promise<null> {
  claudePromptContent(command);
  const live = getClaudeSession(record.id);
  if (live?.isRunning()) throw new Error("A claude turn is running; edit when it ends");
  const refusal = live?.restartRefusal("Editing a message");
  if (refusal) throw new Error(refusal);
  const transcript = claudeTranscriptFile(record.id, claudeConfigDir());
  if (!transcript) throw new Error("Invalid entry ID for editing");
  const resumeAt = claudeEditResumePoint(parseClaudeTranscript(readFileSync(transcript, "utf8")), entryId);
  if (!existsSync(record.cwd)) throw new Error(`The session folder no longer exists: ${record.cwd}`);
  pendingResumes().set(record.id, resumeAt);
  try {
    if (live) await live.shutdown();
    const session = startClaudeSession(record);
    // The old child still listed (it did not exit in time), or a child started without this truncation.
    if (session === live || session.resumeAt !== resumeAt) throw new Error("The claude session restarted before the edit; edit again");
    await session.send({ type: "prompt", message: command.message, images: command.images });
    return null;
  } finally {
    if (pendingResumes().get(record.id) === resumeAt) pendingResumes().delete(record.id);
  }
}

export function getRunningClaudeSessionIds(): string[] {
  return [...registry().values()].filter((session) => session.isRunning()).map((session) => session.sessionId);
}

/** Live claude sessions with an unsettled subagent run. */
export function getClaudeSessionIdsWithUnsettledSubagents(): string[] {
  return [...registry().values()].filter((session) => session.hasUnsettledSubagents()).map((session) => session.sessionId);
}

/** Live claude sessions with a permission prompt waiting for the user's answer. */
export function getAwaitingClaudeSessionIds(): string[] {
  return [...registry().values()].filter((session) => session.isAwaitingInput()).map((session) => session.sessionId);
}

/**
 * A permission override refused because the session's child is busy: a turn
 * runs, a background subagent is unsettled or a permission prompt waits. Nothing changed.
 */
export class ClaudeSessionBusyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ClaudeSessionBusyError";
  }
}

/** What a permission override did. */
export interface ClaudePermissionOverrideResult {
  override: ClaudePermissionMode | null;
  /** The idle child was ended; the next open or send starts one with the new mode. */
  restarted: boolean;
}

/**
 * Sets (a mode) or clears (null: follow the global default) a claude session's permission
 * override. A child's mode is fixed at its launch, so the index is written
 * first and an idle live child is ended: the next open or send starts `--resume` with the
 * new argv (the effort 「默认」 rule). Refused with ClaudeSessionBusyError, changing nothing,
 * while the child is busy: a turn runs (a permission prompt only waits inside one), or a
 * background subagent is unsettled (ending the child would end it). Throws "Session not
 * found" for an id the index does not hold.
 */
export async function setClaudePermissionOverride(id: string, mode: ClaudePermissionMode | null): Promise<ClaudePermissionOverrideResult> {
  if (!getClaudeSessionRecord(id)) throw new Error("Session not found");
  const live = getClaudeSession(id);
  if (live?.isRunning() || live?.isAwaitingInput()) throw new ClaudeSessionBusyError("A claude turn is running; change the permission mode when it ends");
  if (live?.hasUnsettledSubagents()) throw new ClaudeSessionBusyError("A claude background subagent is still running; change the permission mode when its result is in");
  await updateClaudeSessionRecord(id, (record) => {
    const next = { ...record };
    if (mode === null) delete next.permissionMode;
    else next.permissionMode = mode;
    return next;
  });
  // A turn may have started while the index was written: the override then waits for the
  // child's next launch rather than ending the turn.
  if (!live || live.isRunning() || live.isAwaitingInput() || live.hasUnsettledSubagents()) return { override: mode, restarted: false };
  await live.shutdown();
  return { override: mode, restarted: true };
}

/** Every live child, ended (process groups signalled at once; resolves when they exited or the deadline passed). */
export function shutdownAllClaudeSessions(): Promise<void> {
  return Promise.all([...registry().values()].map((session) => session.shutdown())).then(() => undefined);
}
