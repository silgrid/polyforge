// The client-safe model of a claude subagent run: the run an
// `Agent` / `Task` tool call started, its header data and its timeline entries.
// Both paths build it — the live stream (ClaudeStreamTranslator) and the stored
// transcript + sidecar (claudeTranscriptToMessages), both in lib/claude-adapter.ts,
// the only module that knows claude's formats — and both merge through
// mergeClaudeSubagentRun(), so a run reads the same however it arrived.
// No claude format is parsed here.

/** claude's own reported state of a run; nothing here invents one (a Stop adds none). */
export type ClaudeSubagentStatus = "running" | "completed" | "failed" | "stopped";

/** The run's statistics as claude's task events report them. */
export interface ClaudeSubagentUsage {
  totalTokens: number;
  toolUses: number;
  durationMs: number;
}

/** One timeline entry: a pi message (as the chat renders it), keyed by claude's entry uuid. */
export interface ClaudeSubagentEntry {
  id: string;
  message: Record<string, unknown> & { role: string };
}

export interface ClaudeSubagentRun {
  /** The `Agent` / `Task` tool call that started the run (at any depth). */
  toolCallId: string;
  /** claude's agent / task id: the sidecar file's `agent-<id>`. */
  agentId?: string;
  /** `subagent_type`, the header's badge. */
  agentType?: string;
  description?: string;
  prompt?: string;
  /** Started with `run_in_background` / `is_backgrounded`. */
  background: boolean;
  status: ClaudeSubagentStatus;
  /**
   * Epoch ms the run started: the entry or frame holding its `Agent` / `Task` call, or,
   * once a `SendMessage` resumed it, that call's. Absent when unknown.
   */
  startedAt?: number;
  /** The latest `task_progress` description (header only). */
  progress?: string;
  usage?: ClaudeSubagentUsage;
  /** The run's final result text; absent while running or when claude reported none. */
  result?: string;
  /** In claude's order. A delta on the live wire (new entries only), the whole list in history. */
  entries: ClaudeSubagentEntry[];
}

/** The runs a session holds, by their tool call id. */
export type ClaudeSubagentRuns = Readonly<Record<string, ClaudeSubagentRun>>;

/** The tool names that start a claude subagent. */
export const CLAUDE_SUBAGENT_TOOL_NAMES: ReadonlySet<string> = new Set(["Agent", "Task"]);

/** The hidden custom message that anchors claude's follow-up turn (a turn start, no bubble). */
export const CLAUDE_TASK_NOTIFICATION_TYPE = "claude:task-notification";

/** The timeline's first page, then "show N more". */
export const CLAUDE_SUBAGENT_PAGE_SIZE = 25;

export function isClaudeSubagentToolName(name: unknown): boolean {
  return typeof name === "string" && CLAUDE_SUBAGENT_TOOL_NAMES.has(name);
}

export function isSettledClaudeSubagentStatus(status: ClaudeSubagentStatus): boolean {
  return status !== "running";
}

/**
 * Folds `incoming` into `current` (either may come from the live stream or from
 * history): an entry id already held keeps its place and takes the incoming copy,
 * new ids append once in their order; header fields take the incoming value when
 * it has one, and a settled status never goes back to running — except for a resume
 *: a running copy that started later than the held run. A read with
 * the same or no start (an older read) never reopens, and the later start is kept.
 */
export function mergeClaudeSubagentRun(current: ClaudeSubagentRun | undefined, incoming: ClaudeSubagentRun): ClaudeSubagentRun {
  if (!current) return { ...incoming, entries: dedupeEntries([], incoming.entries) };
  const resumed = incoming.startedAt !== undefined && current.startedAt !== undefined && incoming.startedAt > current.startedAt;
  const status = isSettledClaudeSubagentStatus(current.status) && !isSettledClaudeSubagentStatus(incoming.status) && !resumed
    ? current.status
    : incoming.status;
  const startedAt = current.startedAt === undefined || (incoming.startedAt !== undefined && incoming.startedAt > current.startedAt)
    ? incoming.startedAt
    : current.startedAt;
  return {
    toolCallId: current.toolCallId,
    agentId: incoming.agentId ?? current.agentId,
    agentType: incoming.agentType ?? current.agentType,
    description: incoming.description ?? current.description,
    prompt: incoming.prompt ?? current.prompt,
    background: incoming.background || current.background,
    status,
    ...(startedAt !== undefined ? { startedAt } : {}),
    progress: incoming.progress ?? current.progress,
    usage: incoming.usage ?? current.usage,
    result: incoming.result ?? current.result,
    entries: dedupeEntries(current.entries, incoming.entries),
  };
}

function dedupeEntries(current: readonly ClaudeSubagentEntry[], incoming: readonly ClaudeSubagentEntry[]): ClaudeSubagentEntry[] {
  const entries = [...current];
  const at = new Map(entries.map((entry, index) => [entry.id, index]));
  for (const entry of incoming) {
    const index = at.get(entry.id);
    if (index === undefined) {
      at.set(entry.id, entries.length);
      entries.push(entry);
    } else {
      entries[index] = entry;
    }
  }
  return entries;
}

/** Every run of `incoming` folded into `current`; a new object only when something changed. */
export function mergeClaudeSubagentRuns(current: ClaudeSubagentRuns, incoming: readonly ClaudeSubagentRun[]): ClaudeSubagentRuns {
  if (incoming.length === 0) return current;
  const next: Record<string, ClaudeSubagentRun> = { ...current };
  for (const run of incoming) next[run.toolCallId] = mergeClaudeSubagentRun(next[run.toolCallId], run);
  return next;
}

/**
 * A history read (the session detail's `claudeSubagents`) folded into the runs a page
 * holds: history wins for every entry id it carries (its copy replaces the held one in
 * place), entries the stream delivered after the read was taken stay appended, and a
 * run the read does not know yet is kept. A reload mid-run therefore shows each
 * on-disk entry once and the later live ones after it.
 */
export function mergeClaudeSubagentHistory(current: ClaudeSubagentRuns, history: readonly ClaudeSubagentRun[] | undefined): ClaudeSubagentRuns {
  return mergeClaudeSubagentRuns(current, history ?? []);
}

/**
 * A row never toggled opens while its run is running, failed or interrupted (claude's
 * killed/stopped/cancelled → "stopped"), and stays closed once it completed.
 */
export function isClaudeSubagentExpandedByDefault(status: ClaudeSubagentStatus): boolean {
  return status !== "completed";
}

// The user's manual toggles, by tool call id, for this page's life: a row remounts as
// it moves from the stream into the list and into the folded process details, and a
// manual choice must win over every later state change. Never persisted.
const manualExpansion = new Map<string, boolean>();

export function claudeSubagentManualExpansion(toolCallId: string): boolean | undefined {
  return manualExpansion.get(toolCallId);
}

export function setClaudeSubagentManualExpansion(toolCallId: string, expanded: boolean): void {
  manualExpansion.set(toolCallId, expanded);
}

export function isClaudeSubagentExpanded(run: Pick<ClaudeSubagentRun, "toolCallId" | "status">): boolean {
  return manualExpansion.get(run.toolCallId) ?? isClaudeSubagentExpandedByDefault(run.status);
}

/** A run's statistics as header parts: tool count, duration, tokens (each only when known). */
export interface ClaudeSubagentStatParts {
  toolUses?: number;
  durationMs?: number;
  totalTokens?: number;
}

export function claudeSubagentStatParts(usage: ClaudeSubagentUsage | undefined): ClaudeSubagentStatParts {
  if (!usage) return {};
  return {
    ...(usage.toolUses > 0 ? { toolUses: usage.toolUses } : {}),
    ...(usage.durationMs > 0 ? { durationMs: usage.durationMs } : {}),
    ...(usage.totalTokens > 0 ? { totalTokens: usage.totalTokens } : {}),
  };
}

/** A token count with the locale's grouping (10,379). */
export function formatClaudeSubagentTokens(count: number, locale?: string): string {
  return new Intl.NumberFormat(locale ?? "en-US").format(count);
}

/** What an expanded row's body shows below the prompt. */
export interface ClaudeSubagentBody {
  /** The timeline's steps (tool results render under their calls, so they are no steps). */
  steps: ClaudeSubagentEntry[];
  /** Settled: the Result block shows (`result`, or "no result" when null). */
  settled: boolean;
  result: string | null;
}

function entryText(entry: ClaudeSubagentEntry): string | null {
  const { message } = entry;
  if (message.role !== "assistant" || !Array.isArray(message.content)) return null;
  const blocks = message.content as { type?: string; text?: string }[];
  if (blocks.some((block) => block.type !== "text")) return null;
  return blocks.map((block) => block.text ?? "").join("\n").trim();
}

/**
 * The body of a row: prompt → timeline → Result. A run's final reply is usually also
 * its result; it shows once, as the Result, not a second time as the last step.
 */
export function claudeSubagentBody(run: ClaudeSubagentRun): ClaudeSubagentBody {
  const steps = run.entries.filter((entry) => entry.message.role !== "toolResult");
  const settled = isSettledClaudeSubagentStatus(run.status);
  const result = settled && run.result?.trim() ? run.result.trim() : null;
  const last = steps.at(-1);
  if (result !== null && last && entryText(last) === result) steps.pop();
  return { steps, settled, result };
}

/** The fields of a displayed assistant block the fold decision reads. */
interface FoldBlock {
  type: string;
  toolCallId?: string;
  toolName?: string;
}

/**
 * The one fold exception: a step whose displayed blocks are all
 * `Agent` / `Task` calls of background runs still running stays outside its turn's
 * folded process details, so the user can watch it after the turn answered; once
 * the run settles it returns into the fold. A foreground call is an ordinary step.
 */
export function isRunningBackgroundAgentMessage(blocks: readonly FoldBlock[], runs: ClaudeSubagentRuns): boolean {
  return blocks.length > 0 && blocks.every((block) => {
    if (block.type !== "toolCall" || !isClaudeSubagentToolName(block.toolName) || !block.toolCallId) return false;
    const run = runs[block.toolCallId];
    return run !== undefined && run.background && run.status === "running";
  });
}

/** A main-conversation message as far as the launch position reads it (a pi message). */
export interface AgentMessageLike {
  role?: unknown;
  content?: unknown;
}

/**
 * The running list: every run claude reports `running`, at any depth,
 * oldest start first (an unknown start last). Derived from the run map alone, so a
 * reload rebuilds it from history and the live fill.
 */
export function runningClaudeSubagents(runs: ClaudeSubagentRuns): ClaudeSubagentRun[] {
  return Object.values(runs)
    .filter((run) => run.status === "running")
    .sort((a, b) => (a.startedAt ?? Infinity) - (b.startedAt ?? Infinity) || 0);
}

/** How long the run has run at `now` (never negative); undefined without a start. */
export function claudeSubagentElapsedMs(run: ClaudeSubagentRun, now: number): number | undefined {
  return run.startedAt === undefined ? undefined : Math.max(0, now - run.startedAt);
}

/** The message holds that tool call: the adapter's block (`id`) or the chat's normalized one (`toolCallId`, lib/normalize.ts). */
function holdsCall(message: AgentMessageLike, toolCallId: string): boolean {
  return message.role === "assistant" && Array.isArray(message.content)
    && (message.content as { type?: unknown; id?: unknown; toolCallId?: unknown }[]).some((block) => block.type === "toolCall" && (block.toolCallId ?? block.id) === toolCallId);
}

/**
 * The launch position: the entry id of the main message holding the
 * run's `Agent` / `Task` call — for a nested run, its outermost ancestor's call (the run
 * whose timeline holds the call is the parent). Undefined when no main message holds it.
 */
export function claudeSubagentLaunchEntryId(toolCallId: string, messages: readonly AgentMessageLike[], entryIds: readonly string[], runs: ClaudeSubagentRuns): string | undefined {
  const seen = new Set<string>();
  let id: string | undefined = toolCallId;
  while (id !== undefined && !seen.has(id)) {
    const callId: string = id;
    seen.add(callId);
    const index = messages.findIndex((message) => holdsCall(message, callId));
    if (index >= 0) return entryIds[index];
    id = Object.values(runs).find((run) => run.entries.some((entry) => holdsCall(entry.message, callId)))?.toolCallId;
  }
  return undefined;
}

/**
 * Whether a pending jump to a launch position targets the message with
 * this entry id, so the chat reveals the folded process details holding it. No jump
 * targets nothing: a live step has no entry id until the post-run reload, and an unguarded
 * `undefined === undefined` opened every answered live turn.
 */
export function isSubagentJumpTarget(jump: { entryId: string } | null | undefined, entryId: string | undefined): boolean {
  return jump != null && entryId !== undefined && jump.entryId === entryId;
}

/**
 * The relay message: the one visible user message the main session gets
 * when the panel messages a subagent — the main agent passes the text on with its own
 * `SendMessage`, addressed by claude's agent id (a description does not resolve). The
 * fixed wording is the owner's; the text follows verbatim after a newline.
 */
export function claudeSubagentRelayText(run: ClaudeSubagentRun, text: string): string {
  return `请用 SendMessage 把下面这段话原样转给 subagent ${run.agentId ?? ""}（${run.description || run.agentType || "Agent"}），不要做别的：\n${text}`;
}
