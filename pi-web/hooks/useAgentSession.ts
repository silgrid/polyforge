"use client";

import { useState, useCallback, useRef, useEffect, useLayoutEffect, useMemo, useReducer } from "react";
import type {
  AgentMessage,
  BackgroundTasksClientEvent,
  BlockingExtensionUiRequest,
  ExtensionStatusItem,
  ExtensionUiRequest,
  ExtensionWidgetItem,
  SessionInfo,
  SessionTreeNode,
  ToolResultMessage,
  UserMessage,
} from "@/lib/types";
import { isBlockingExtensionUiRequest } from "@/lib/browser-notifications";
import {
  CLAUDE_EFFORT_DEFAULT,
  CLAUDE_HARNESS,
  DEFAULT_CLAUDE_MODEL,
  PI_HARNESS,
  claudeFirstMessage,
  isClaudeModelName,
  type ClaudeEffort,
  type ClaudeLaunchPermissionMode,
  type SessionHarness,
} from "@/lib/claude-shared";
import { normalizeToolCalls } from "@/lib/normalize";
import { claudeSubagentRelayText, mergeClaudeSubagentHistory, mergeClaudeSubagentRuns, type ClaudeSubagentRun, type ClaudeSubagentRuns } from "@/lib/claude-subagent-view";
import { CLAUDE_BACKGROUND_TASK_EVENT, CLAUDE_BACKGROUND_TASKS_EVENT, CLAUDE_FORCE_STOP_EVENT, mergeClaudeBackgroundTask, replaceClaudeBackgroundTasks, type ClaudeBackgroundTask, type ClaudeBackgroundTasks, type ClaudeBackgroundWork } from "@/lib/claude-background-task-view";
import { AgentCommandError, agentStatusUrl, isPromptRejectedError, sendAgentCommand } from "@/lib/agent-client";
import { missingCwdFromBody } from "@/lib/missing-cwd";
import {
  deleteSessionViewSnapshot,
  getSessionViewSnapshot,
  setSessionViewSnapshot,
} from "@/lib/session-view-cache";
import { clearDraft, getDraft, parkedNewSessionDraftKey, rekeyDraft, restoreDraftSubmission } from "@/lib/draft-store";
import { getPreferredToolPreset, setPreferredToolPreset } from "@/lib/tool-preset-preference";
import { CONFIGURED_TOOL_PRESET, getPresetFromToolNames, getToolNamesForPreset, type ToolEntry, type ToolPreset } from "@/lib/tool-presets";
import type { SessionStatsInfo } from "@/lib/pi-types";
import { mergeSessionStats, type SessionFileStats } from "@/lib/session-stats";
import { formatSessionSummary } from "@/lib/session-stats-format";
import { userMessageKey } from "@/lib/prompt-recovery";
import { carryMessageKeys, messageRenderKeys, sameMessage } from "@/lib/message-keys";
import { MANUAL_FETCH_TIMEOUT_MS, formatFetchTime, hasFreshContent, mergeLoadedHistory } from "@/lib/manual-fetch";
import {
  AgentEventConnection,
  canReleaseHiddenEventStream,
  SESSION_LEASE_TIMEOUT_MS,
  sessionLeaseShowsActivity,
  staleCheckIntervalMs,
  type SessionLeaseReply,
} from "@/lib/agent-event-connection";
import { HIDDEN_PAGE_GRACE_MS } from "@/hooks/usePageHidden";
import type { DebugClientLease, DebugClientSession, DebugEventConnectionSnapshot, DebugStreamCloseReason } from "@/lib/debug-types";
import { pageId, peekPageId, trackEventSource } from "@/lib/page-debug";
import type { TranslationParams } from "@/lib/i18n/types";
import { AGENT_EVENT_HEARTBEAT_MS, isNestedToolExecutionEvent, isSystemMessageEvent } from "@/lib/agent-event-wire";
import { runStatePhase } from "@/lib/chat-phase-label";
import { getToolExecutionProgress } from "@/lib/tool-execution-progress";
import { CODEMODE_TOOL_NAME, getCodemodeProgress } from "@/lib/codemode-view";
import { mergeExtensionSnapshot, updateExtensionWidgets } from "@/lib/extension-widgets";
import { bareMcpOpensSettings } from "@/lib/mcp-command";
import type { SettingsSection } from "@/lib/settings-navigation";
import { withTranscriptNotices, type TranscriptNotice } from "@/lib/transcript-notices";
import {
  enqueueExtensionUiRequest,
  removeExtensionUiRequest,
  retainExtensionUiRequests,
  upsertExtensionUiRequest,
} from "@/lib/extension-ui-queue";
import {
  CHAT_SCROLL_REATTACH_TOLERANCE,
  CHAT_SCROLL_TAIL_TOLERANCE,
  getLiveFollowAttached,
  shouldShowScrollToLatest,
} from "@/lib/chat-lazy-load";
import {
  createStreamingStore,
  type ClientAssistantMessageEvent,
} from "@/lib/streaming-message";

export interface SessionData {
  sessionId: string;
  filePath: string;
  totalActiveMs: number;
  tree: SessionTreeNode[];
  leafId: string | null;
  toolNames?: string[];
  /** Opaque freshness token for the session view cache (summary tree reads). */
  snapshotRevision?: string | null;
  /** "summary" when `tree` carries the body-free navigation format. */
  treeFormat?: "summary";
  context: {
    messages: AgentMessage[];
    entryIds: string[];
    oldestEntryId: string | null;
    hasMore: boolean;
    /** Turns on the branch before `oldestEntryId` (the minimap's numbering offset). */
    turnsBefore: number;
    thinkingLevel: string;
    model: { provider: string; modelId: string } | null;
  };
  /** Cumulative usage over ALL session-file entries (incl. compacted history). */
  stats?: SessionFileStats;
  /** True when GET ?force=1 dropped a stale live wrapper and rebuilt from disk. */
  wrapperRebuilt?: boolean;
  /** The session's list row as the server sees it (a claude session's carries `harness`). */
  info?: SessionInfo | null;
  /** A claude session's subagent runs, rebuilt from claude's sidecars. */
  claudeSubagents?: ClaudeSubagentRun[];
  /** A claude session's user messages an in-session edit can start from. */
  claudeEditEntryIds?: string[];
}

/** A claude subagent run with its entries' tool calls in the chat's shape (lib/normalize.ts). */
function normalizeClaudeSubagentRun(run: ClaudeSubagentRun): ClaudeSubagentRun {
  return {
    ...run,
    entries: (run.entries ?? []).map((entry) => ({ ...entry, message: normalizeToolCalls(entry.message as unknown as AgentMessage) as unknown as ClaudeSubagentRun["entries"][number]["message"] })),
  };
}

/** The claude subagent runs a page holds, and the session they belong to. */
interface ClaudeSubagentState {
  sessionId: string | null;
  runs: ClaudeSubagentRuns;
}

const NO_CLAUDE_SUBAGENTS: ClaudeSubagentRuns = {};
const NO_CLAUDE_TASKS: ClaudeBackgroundTasks = {};

/** The claude background tasks a page holds, and the session they belong to. */
interface ClaudeTaskState {
  sessionId: string | null;
  tasks: ClaudeBackgroundTasks;
}

/** A live `claude_background_task` snapshot folded into the page's tasks for `sessionId`; another session's start over. */
export function foldClaudeBackgroundTask(state: ClaudeTaskState, sessionId: string, task: ClaudeBackgroundTask): ClaudeTaskState {
  const base = state.sessionId === sessionId ? state.tasks : NO_CLAUDE_TASKS;
  const next = mergeClaudeBackgroundTask(base, task);
  return next === state.tasks && state.sessionId === sessionId ? state : { sessionId, tasks: next };
}

/**
 * The server's whole list of running tasks (`claude_background_tasks`, a new stream's
 * first word) replacing the page's tasks for `sessionId` (replaceClaudeBackgroundTasks()):
 * a task that ended while the page was not listening leaves its list; another session's start over.
 */
export function replaceClaudeTaskState(state: ClaudeTaskState, sessionId: string, running: readonly ClaudeBackgroundTask[]): ClaudeTaskState {
  const base = state.sessionId === sessionId ? state.tasks : NO_CLAUDE_TASKS;
  const next = replaceClaudeBackgroundTasks(base, running);
  return next === state.tasks && state.sessionId === sessionId ? state : { sessionId, tasks: next };
}

/** A background task's recent output, as the `task_output` command answers it (claude's last 8 KiB at most). */
export interface ClaudeTaskOutput {
  output: string;
  totalBytes: number;
  truncated: boolean;
}
const NO_CLAUDE_EDIT_ENTRY_IDS: readonly string[] = [];

/**
 * Folds runs (a history read's whole list, or a live `claude_subagent` event's
 * delta) into the page's state for `sessionId`; another session's state starts over.
 */
export function foldClaudeSubagents(state: ClaudeSubagentState, sessionId: string, runs: readonly ClaudeSubagentRun[] | undefined, source: "history" | "live"): ClaudeSubagentState {
  const base = state.sessionId === sessionId ? state.runs : NO_CLAUDE_SUBAGENTS;
  const normalized = (runs ?? []).map(normalizeClaudeSubagentRun);
  const next = source === "history" ? mergeClaudeSubagentHistory(base, normalized) : mergeClaudeSubagentRuns(base, normalized);
  return next === state.runs && state.sessionId === sessionId ? state : { sessionId, runs: next };
}

/**
 * What GET /api/claude reports that the composer uses: whether claude is on PATH and the
 * names its model list offers — claude's aliases from its initialize answer, then
 * Settings › Claude's custom names.
 */
export interface ClaudeAvailability {
  available: boolean;
  models: string[];
}

/**
 * The POST /api/agent/new body a fresh composer sends to create its session. A pi
 * body is the one sent before claude sessions existed; a claude one carries only
 * the harness, its full model name and a non-default effort (no pi
 * model, reasoning or tools).
 */
export function newSessionRequestBody(options: {
  cwd: string;
  harness: SessionHarness;
  claudeModel: string;
  claudeEffort?: ClaudeEffort;
  toolNames?: string[];
  model?: SelectedModel | null;
  thinkingLevel?: string | null;
}): Record<string, unknown> {
  if (options.harness === CLAUDE_HARNESS) {
    return {
      cwd: options.cwd,
      type: "ensure_session",
      harness: CLAUDE_HARNESS,
      model: options.claudeModel,
      ...(options.claudeEffort && options.claudeEffort !== CLAUDE_EFFORT_DEFAULT ? { effort: options.claudeEffort } : {}),
    };
  }
  return {
    cwd: options.cwd,
    type: "ensure_session",
    ...(options.toolNames !== undefined ? { toolNames: options.toolNames } : {}),
    ...(options.model ? { provider: options.model.provider, modelId: options.model.modelId } : {}),
    ...(options.thinkingLevel ? { thinkingLevel: options.thinkingLevel } : {}),
  };
}

interface AgentEvent {
  type: string;
  [key: string]: unknown;
}

interface CompactCommandResult {
  tokensBefore?: number;
  estimatedTokensAfter?: number;
}

/** A claude session's answer to a model or effort switch or a permission-mode switch. */
interface ClaudeSwitchResult {
  model?: string;
  effort?: ClaudeEffort;
  launchPermissionMode?: ClaudeLaunchPermissionMode;
  rejected?: boolean;
  error?: string;
}

interface LastAssistantTextResponse {
  text?: string;
}

type AgentStateResponse = {
  model?: { provider: string; id: string };
  contextUsage?: { percent: number | null; contextWindow: number; tokens: number | null } | null;
  /** Active time over the live wrapper's entries (the snapshot carries the same field). */
  totalActiveMs?: number;
  systemPrompt?: string;
  thinkingLevel?: string;
  isStreaming?: boolean;
  isPromptRunning?: boolean;
  isBashRunning?: boolean;
  isCompacting?: boolean;
  autoCompactionEnabled?: boolean;
  extensionStatuses?: ExtensionStatusItem[];
  extensionWidgets?: ExtensionWidgetItem[];
  queuedMessages?: { steering?: string[]; followUp?: string[] } | null;
};

export interface QueuedMessages {
  steering: string[];
  followUp: string[];
}

/** The messages without that one (by identity, its last occurrence); the same array when it is absent. */
function withoutMessage(messages: AgentMessage[], message: AgentMessage): AgentMessage[] {
  const index = messages.lastIndexOf(message);
  return index === -1 ? messages : [...messages.slice(0, index), ...messages.slice(index + 1)];
}

function countUserMessages(messages: readonly AgentMessage[], key: string): number {
  return messages.reduce((count, message) => (
    message.role === "user" && userMessageKey(message) === key ? count + 1 : count
  ), 0);
}

function normalizeQueuedMessages(q?: { steering?: string[]; followUp?: string[] } | null): QueuedMessages {
  return { steering: q?.steering ?? [], followUp: q?.followUp ?? [] };
}

type ExtensionUiDialogRequest = Extract<ExtensionUiRequest, { method: "select" | "confirm" | "input" | "editor" }>;
type ExtensionUiCustomRequest = Extract<ExtensionUiRequest, { method: "custom" }>;
export type NoticeType = "info" | "success" | "warning" | "error";

export type NoticeItem = {
  id: string;
  message: string;
  type: NoticeType;
  exiting?: boolean;
};

type NoticeState = {
  visible: NoticeItem[];
  pending: NoticeItem[];
};

type NoticeAction =
  | { type: "add"; notice: NoticeItem }
  | { type: "mark_oldest_exiting" }
  | { type: "remove"; id: string };

export type AgentPhase =
  | { kind: "waiting_model" }
  | { kind: "running_command" }
  | { kind: "running_tools"; tools: { id: string; name: string; progress?: string }[] }
  | null;

export interface CompactResultInfo {
  reason: "manual" | "threshold" | "overflow" | "auto" | string;
  tokensBefore: number;
  /** Absent for claude's compaction, which reports only the pre-compaction count. */
  estimatedTokensAfter?: number;
}

export interface SlashCommandInfo {
  name: string;
  description?: string;
  source: "extension" | "prompt" | "skill";
  sourceInfo?: {
    path: string;
    source: string;
    scope: "user" | "project" | "temporary";
    origin: "package" | "top-level";
    baseDir?: string;
  };
}

export type BuiltinSlashCommandResult =
  | { handled: false }
  | { handled: true; message?: string; error?: string; action?: "openSessionStats" | "openSettings" | "compact" };

/** How a run ended, for the completion sound and notifications. */
export interface AgentEndInfo {
  /** The run was stopped (Esc, Stop, another client's abort), not finished: nothing to announce. */
  aborted: boolean;
  /**
   * Subagents of this session are still at work and will report back with a run of their own:
   * the task is not done yet, so nothing to announce either (#1434).
   */
  subagentsActive?: boolean;
}

export interface UseAgentSessionOptions {
  session: SessionInfo | null;
  sessionRunning?: boolean;
  newSessionCwd: string | null;
  newSessionDraftKey: string | null;
  /** A run ended; `aborted` when it was stopped rather than finished (pi's `agent_settled.aborted`). */
  onAgentEnd?: (end: AgentEndInfo) => void;
  /** A top-level tool call finished; the files it touched may have changed. */
  onToolEnd?: (toolName: string) => void;
  onAttentionNeeded?: (request: BlockingExtensionUiRequest) => void;
  /** Live background-task events (terminal transitions and list updates) from this session's stream. */
  onBackgroundTasksEvent?: (event: BackgroundTasksClientEvent) => void;
  onSessionCreated?: (session: SessionInfo, sourceDraftKey: string) => void;
  onSessionForked?: (newSessionId: string) => void;
  modelsRefreshKey?: number;
  chatInputRef?: React.RefObject<ChatInputHandle | null>;
  onBranchDataChange?: (tree: SessionTreeNode[], activeLeafId: string | null, onLeafChange: (leafId: string | null) => void, locked: boolean) => void;
  onSystemPromptChange?: (prompt: string | null) => void;
  onSystemToolsChange?: (tools: ToolEntry[] | null) => void;
  /** Registers an action that lazily starts the session and loads its prompt and tools. */
  onSystemInfoLoaderChange?: (loader: (() => Promise<void>) | null) => void;
  /** Registers the debug panel's reader of this page's stream and lease: it reads refs only, never a request. */
  onDebugSnapshotReaderChange?: (reader: (() => DebugClientSession) | null) => void;
  onSessionStatsPanelOpen?: () => void;
  /** Opens Settings on a section; a bare `/mcp` the built-in MCP extension owns opens Settings › MCP. */
  onOpenSettings?: (section: SettingsSection) => void;
  setToolPreset?: (preset: ToolPreset) => void;
  deferInitialScroll?: boolean;
  /** A fresh composer's model and reasoning picks to start with: the composer it replaces had them. */
  initialNewSessionChoices?: NewSessionChoices | null;
  /** A fresh composer reports its own model and reasoning picks, on mount and as they change. */
  onNewSessionChoicesChange?: (choices: NewSessionChoices) => void;
  /** The page's translator, for the notices the hook writes itself. */
  t: (key: string, params?: TranslationParams) => string;
}

export type ThinkingLevelOption = "auto" | "off" | "minimal" | "low" | "medium" | "high" | "xhigh" | "max";
type ConcreteThinkingLevel = Exclude<ThinkingLevelOption, "auto">;

/**
 * What a fresh composer was told to use instead of the automatic choice. The
 * bar above it moves the composer to another folder by remounting it; these
 * go along with the draft. (The tool preset needs no carrying: a pick is the
 * browser's preference, which every fresh composer starts from.)
 */
export interface NewSessionChoices {
  model: { provider: string; modelId: string } | null;
  thinkingLevel: ConcreteThinkingLevel | null;
  /** The harness picked for the new session (absent: pi), with claude's full model name and effort. */
  harness?: SessionHarness;
  claudeModel?: string;
  claudeEffort?: ClaudeEffort;
}

function asConcreteThinkingLevel(value?: string | null): ConcreteThinkingLevel | null {
  if (!value || value === "auto") return null;
  return value as ConcreteThinkingLevel;
}

// Session id -> user entry being edited. ChatWindow remounts per session, so a
// pending edit lives here as long as that session's in-memory draft does.
const pendingHistoryEdits = new Map<string, string>();

const PROMPT_SETTLE_INITIAL_DELAY_MS = 800;
const PROMPT_SETTLE_POLL_MS = 600;
const PROMPT_SETTLE_MAX_MS = 20_000;
const EVENT_STREAM_IDLE_GRACE_MS = 30_000;
const AGENT_STATE_RECONCILE_MS = 15_000;
const BASH_STATE_RECONCILE_MS = 1_000;
const EVENT_STREAM_READY_TIMEOUT_MS = 60_000;
const EVENT_STREAM_RECONNECT_DELAY_MS = 1_000;
const SESSION_LEASE_RENEW_INTERVAL_MS = 30_000;
// A ready stream silent for two and a half heartbeats is dead (AgentEventConnection reconnects).
const EVENT_STREAM_STALE_MS = AGENT_EVENT_HEARTBEAT_MS * 2.5;
// How long a manual fetch's tail lives (`lateTailExpired`), 100 s: the stale
// bound plus one watchdog check, by when a foreground tab's watchdog has
// replaced a stream silent for EVENT_STREAM_STALE_MS.
export const MANUAL_FETCH_TAIL_MS = EVENT_STREAM_STALE_MS + staleCheckIntervalMs(EVENT_STREAM_STALE_MS);
type SessionLeaseOutcome = SessionLeaseReply | "unanswered" | null;
// This page's id, the `?holder=` of its event streams: lib/page-debug.ts.
export { peekPageId };
function sessionLeaseUrl(sid: string): string {
  return `/api/agent/${encodeURIComponent(sid)}/lease`;
}
// Retry temporary model-list failures without requiring a page refresh.
const MODELS_RETRY_DELAYS_MS = [2_000, 5_000, 10_000];
const MAX_NOTICES = 5;
const NOTICE_VISIBLE_MS = 5000;
const NOTICE_EXIT_ANIMATION_MS = 180;
function createNoticeId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function markOldestNoticeExiting(notices: NoticeItem[]): NoticeItem[] {
  const index = notices.findIndex((notice) => !notice.exiting);
  if (index === -1) return notices;
  return notices.map((notice, i) => (
    i === index ? { ...notice, exiting: true } : notice
  ));
}

function fillPendingNotices(visible: NoticeItem[], pending: NoticeItem[]): NoticeState {
  let nextVisible = visible;
  let nextPending = pending;
  while (nextPending.length > 0 && nextVisible.length < MAX_NOTICES) {
    const [next, ...rest] = nextPending;
    nextVisible = [...nextVisible, next];
    nextPending = rest;
  }
  if (nextPending.length > 0 && !nextVisible.some((notice) => notice.exiting)) {
    nextVisible = markOldestNoticeExiting(nextVisible);
  }
  return { visible: nextVisible, pending: nextPending };
}

function noticeReducer(state: NoticeState, action: NoticeAction): NoticeState {
  switch (action.type) {
    case "add": {
      if (state.visible.some((notice) => notice.exiting) || state.visible.length >= MAX_NOTICES) {
        return {
          visible: state.visible.some((notice) => notice.exiting)
            ? state.visible
            : markOldestNoticeExiting(state.visible),
          pending: [...state.pending, action.notice],
        };
      }
      return { ...state, visible: [...state.visible, action.notice] };
    }
    case "mark_oldest_exiting":
      return { ...state, visible: markOldestNoticeExiting(state.visible) };
    case "remove": {
      const visible = state.visible.filter((notice) => notice.id !== action.id);
      return fillPendingNotices(visible, state.pending);
    }
    default:
      return state;
  }
}

function readCompactResult(result: unknown, reason: string): CompactResultInfo | null {
  if (!result || typeof result !== "object") return null;
  const r = result as CompactCommandResult;
  if (typeof r.tokensBefore !== "number") return null;
  // claude's compaction reports only the count before; nothing is estimated for it.
  if (r.estimatedTokensAfter === undefined) return { reason, tokensBefore: r.tokensBefore };
  if (typeof r.estimatedTokensAfter !== "number") return null;
  return { reason, tokensBefore: r.tokensBefore, estimatedTokensAfter: r.estimatedTokensAfter };
}

export interface ChatInputHandle {
  insertText: (text: string) => void;
  insertIfEmpty: (content: string) => void;
  replaceMessage: (message: UserMessage) => void;
  prependText: (text: string) => void;
  addImages: (files: File[]) => void;
  rekeyDraft: (previousKey: string, nextKey: string) => void;
  restoreSubmission: (text: string, images?: Array<{ data: string; mimeType: string }>, targetDraftKey?: string) => void;
}

export interface AttachedImage {
  data: string;
  mimeType: string;
  previewUrl: string;
}

type SelectedModel = { provider: string; modelId: string };
/**
 * A normal send whose prompt request failed without the server's refusal (#1432):
 * what to give back should the server turn out never to have got it.
 */
type UnconfirmedSend = {
  runId: number;
  sid: string;
  message: string;
  images?: AttachedImage[];
  draftKey: string | undefined;
  /** A fresh composer's: the cwd's parked draft, which the next fresh composer there takes back. */
  parkedDraftKey: string | null;
  /** The view's last saved entry when the prompt was sent: the prompt can only show up after it. */
  lastEntryIdAtSend: string | null;
  /** User messages with the same text the view already had after that entry (sent before, not saved yet when read). */
  sameTextAfterSend: number;
  /** The optimistic bubble, dropped with its text's return once a read shows the prompt is not there. */
  bubble: AgentMessage;
  /** userMessageKey() of the optimistic bubble: the user message the prompt becomes. */
  key: string;
  /** A slash command: an extension command or template need not become that user message. */
  command: boolean;
};
/** `clear_queue` and `abort`: queued texts, plus the images of prompts parked behind a compaction. */
type RecalledPrompts = { steering?: string[]; followUp?: string[]; images?: Array<{ data: string; mimeType: string }> };
type ModelEntry = { id: string; name: string; provider: string };
type ModelsResponse = {
  models: Record<string, string>;
  modelList?: ModelEntry[];
  defaultModel?: SelectedModel | null;
  defaultThinkingLevel?: string | null;
  savedDefaultThinkingLevel?: string | null;
  thinkingLevels?: Record<string, string[]>;
  thinkingLevelMaps?: Record<string, Record<string, string | null>>;
  thinkingLevelPins?: Record<string, string>;
  modelError?: string;
  modelScopeWarnings?: string[];
  isScoped?: boolean;
};

type SlashCommandsResponse = {
  commands?: SlashCommandInfo[];
};

/**
 * Messages a mid-run snapshot read loaded that were not in the view before it:
 * the stream may still deliver their `message_end` when it resumes (#153).
 * `beforeRun`: a manual fetch read them while the view saw no run (#175), so
 * the run's own `agent_start`, and the `connected` of the stream open then
 * (`openedAt`), may still be buffered ahead of them in a half-dead stream: the
 * first `agent_start` keeps the tail (the next discards it), and so does a
 * `connected` of that same stream. `recordedAt`: when a manual fetch recorded
 * the tail; it lives `MANUAL_FETCH_TAIL_MS` (`lateTailExpired`).
 */
type LateSnapshotMessages = { sid: string; runId: number; messages: AgentMessage[]; beforeRun?: { openedAt: number | null }; recordedAt?: number };

/**
 * A manual fetch's tail outlives no `MANUAL_FETCH_TAIL_MS`:
 * the fetch keeps a stream heard within one heartbeat, and the watchdog
 * replaces a ready stream silent for `EVENT_STREAM_STALE_MS` only at its next
 * check, up to one `staleCheckIntervalMs` later. In a foreground tab the kept
 * stream therefore either delivers the buffered run (SSE order puts it ahead
 * of any later heartbeat) or is replaced, and the new stream's `connected`
 * drops the tail, within that bound. A background tab whose timers the browser
 * throttles may check later still: a buffered run that half-dead stream
 * flushes after the bound renders twice (the #175 duplicate). Past the bound
 * the tail is dropped, so a later same-text message is rendered instead of
 * being swallowed as "already loaded". A mid-run read's tail carries no
 * `recordedAt`: the run's settle drops it.
 */
function lateTailExpired(late: LateSnapshotMessages | null): boolean {
  return late?.recordedAt !== undefined && Date.now() - late.recordedAt > MANUAL_FETCH_TAIL_MS;
}

/**
 * The messages a mid-run read newly showed whose `message_end` the stream can
 * still deliver (#153): the file tail after the view's anchor, minus the live
 * messages the view already held (their event has landed). A window that slid
 * past the anchor records no tail at all: naming the whole window would swallow
 * a later message that happens to repeat an older one.
 */
function lateSnapshotTail(
  previous: { messages: readonly AgentMessage[]; entryIds: readonly string[] },
  next: { messages: readonly AgentMessage[]; entryIds: readonly string[] },
  optimisticKey: string | null,
): AgentMessage[] {
  const anchor = previous.entryIds.at(-1);
  const start = anchor === undefined ? 0 : next.entryIds.indexOf(anchor) + 1;
  if (anchor !== undefined && start === 0) return [];
  const liveStart = previous.entryIds.length;
  // The latest user message with the optimistic key is the waiting bubble; an
  // earlier same-text live message is one the stream already delivered.
  const bubble = optimisticKey === null
    ? -1
    : previous.messages.findLastIndex((message) => message.role === "user" && userMessageKey(message) === optimisticKey);
  let delivered = 0;
  for (let index = liveStart; index < previous.messages.length; index++) {
    // The optimistic bubble's message_end may still arrive: keep it in the tail
    // so that event replaces the bubble instead of appending it after the reply.
    if (index === bubble) break;
    const loaded = next.messages[start + delivered];
    if (!loaded || !sameMessage(previous.messages[index], loaded)) break;
    delivered++;
  }
  return next.messages.slice(start + delivered);
}

/**
 * A read's snapshot can end before messages the stream delivered after the read
 * was issued: applying it wholesale would remove a reply already on screen.
 * The snapshot stays the truth for the messages it holds; the
 * view's live tail (messages after its last loaded entry) that the snapshot
 * does not already hold at its end stays after it. A window that slid past the
 * view's anchor merges nothing, as `lateSnapshotTail()` records no tail there.
 */
function keepStreamedMessages(
  snapshot: { messages: readonly AgentMessage[]; entryIds: readonly string[] },
  view: { messages: readonly AgentMessage[]; entryIds: readonly string[] },
): AgentMessage[] {
  const live = view.messages.slice(view.entryIds.length);
  const anchor = view.entryIds.at(-1);
  const start = anchor === undefined ? 0 : snapshot.entryIds.indexOf(anchor) + 1;
  if (live.length === 0 || (anchor !== undefined && start === 0)) return [...snapshot.messages];
  let first = -1;
  for (let index = start; index < snapshot.messages.length; index++) {
    if (sameMessage(live[0], snapshot.messages[index])) { first = index; break; }
  }
  if (first === -1) return [...snapshot.messages, ...live];
  let matched = 1;
  while (matched < live.length && first + matched < snapshot.messages.length
    && sameMessage(live[matched], snapshot.messages[first + matched])) matched++;
  return [...snapshot.messages, ...live.slice(matched)];
}

/**
 * Whether the stream may never report this message with a `message_end` of its
 * own during a run: a compaction divider, an extension boundary draft
 * (`custom_message`) and a bash execution entry reach the file beside the run
 * without one. A custom message the stream appended itself does
 * report one, so a head that is the delivered message still consumes below.
 */
function mayLackMessageEnd(message: AgentMessage): boolean {
  return message.role === "custom" || message.role === "bashExecution";
}

/**
 * Consumes `message` from the head of the read's undelivered tail when it is
 * the same message, so a buffered stream event cannot render it twice. The
 * stream delivers in file order, so only its head is due. Returns true when the
 * read already showed it.
 */
function takeLateSnapshotMessage(
  ref: { current: LateSnapshotMessages | null },
  sid: string | null,
  runId: number,
  message: AgentMessage,
): boolean {
  const pending = ref.current;
  if (pending && lateTailExpired(pending)) {
    ref.current = null;
    return false;
  }
  if (!pending || pending.sid !== sid || pending.runId !== runId) return false;
  // The stream delivers in file order, so a message it reports is at the tail's
  // head once everything before it has been consumed. A head the stream may
  // never report must not hold back that message: pass over it
  // when it is not the delivered message. A custom message whose own
  // `message_end` arrives first still matches at the head and is consumed.
  while (pending.messages[0] && mayLackMessageEnd(pending.messages[0]) && !sameMessage(message, pending.messages[0])) {
    pending.messages.shift();
  }
  const loaded = pending.messages[0];
  if (!loaded || !sameMessage(message, loaded)) {
    if (pending.messages.length === 0) ref.current = null;
    return false;
  }
  pending.messages.shift();
  if (pending.messages.length === 0) ref.current = null;
  return true;
}

/**
 * The keys of `touches` whose last event sequence is newer than `sinceSeq`: the
 * keys an event touched after a state read issued at `sinceSeq` began.
 */
function touchedSince(touches: Map<string, number>, sinceSeq: number): Set<string> {
  return new Set(
    [...touches].filter(([, seq]) => seq > sinceSeq).map(([key]) => key),
  );
}

export function useAgentSession(opts: UseAgentSessionOptions) {
  const {
    session, newSessionCwd, newSessionDraftKey, onAgentEnd, onToolEnd, onAttentionNeeded, onBackgroundTasksEvent, onSessionCreated, onSessionForked,
    modelsRefreshKey, onBranchDataChange, onSystemPromptChange, onSystemToolsChange, onSystemInfoLoaderChange, onSessionStatsPanelOpen,
    onOpenSettings, onDebugSnapshotReaderChange,
  } = opts;

  const isNew = session === null && newSessionCwd !== null;
  const initialChoices = isNew ? opts.initialNewSessionChoices ?? null : null;

  const [data, setData] = useState<SessionData | null>(null);
  // a claude session's subagent runs (history seeds them, `claude_subagent` events merge in).
  const [claudeSubagentState, setClaudeSubagentState] = useState<ClaudeSubagentState>({ sessionId: null, runs: NO_CLAUDE_SUBAGENTS });
  // a claude session's background tasks (live `claude_background_task`
  // snapshots; a new stream starts with the server's whole list, which replaces them), and a Stop's pending
  // force stop (`claude_force_stop_needed`, cleared when the turn ends or the user keeps waiting).
  const [claudeTaskState, setClaudeTaskState] = useState<ClaudeTaskState>({ sessionId: null, tasks: NO_CLAUDE_TASKS });
  const [claudeForceStop, setClaudeForceStop] = useState<{ sessionId: string; work: ClaudeBackgroundWork[] } | null>(null);
  const [loading, setLoading] = useState(!isNew);
  const [error, setError] = useState<string | null>(null);
  const [activeLeafId, setActiveLeafId] = useState<string | null>(null);
  const [messages, setMessages] = useState<AgentMessage[]>([]);
  const [activeToolResults, setActiveToolResults] = useState<Map<string, ToolResultMessage>>(new Map());
  const [entryIds, setEntryIds] = useState<string[]>([]);
  // Entry ids that took over a run's message keys when the file was read again (lib/message-keys.ts).
  const [messageKeyAliases, setMessageKeyAliases] = useState<ReadonlyMap<string, string>>(() => new Map());
  const [historyCursor, setHistoryCursor] = useState<string | null>(null);
  const [hasEarlierMessages, setHasEarlierMessages] = useState(false);
  // Turns before `historyCursor` (server `turnsBefore`): one copy, the
  // context page's own field, which moves with the cursor.
  const earlierTurnCount = data?.context.turnsBefore ?? 0;
  // Outside React state: a streamed chunk must not re-render the whole chat (see StreamingStore).
  const [streamingStore] = useState(createStreamingStore);
  const dispatch = streamingStore.dispatch;
  const [agentRunning, setAgentRunning] = useState(false);
  // The manual fetch under way: a second trigger joins it.
  const [manualFetching, setManualFetching] = useState(false);
  const manualFetchFlightRef = useRef<Promise<void> | null>(null);
  const [bashRunning, setBashRunning] = useState(false);
  const [pendingBash, setPendingBash] = useState<{ command: string; excludeFromContext: boolean } | null>(null);
  const [modelNames, setModelNames] = useState<Record<string, string>>({});
  const [modelList, setModelList] = useState<ModelEntry[]>([]);
  const [modelError, setModelError] = useState<string | null>(null);
  // The session's or composer's folder, once a route said it no longer exists (#1422).
  // Kept apart by source: a model list read from a removed worktree's main repo
  // succeeds, and must not clear what a refused prompt reported.
  const [modelsMissingCwd, setModelsMissingCwd] = useState<string | null>(null);
  const [promptMissingCwd, setPromptMissingCwd] = useState<string | null>(null);
  const missingCwd = promptMissingCwd ?? modelsMissingCwd;
  const [modelScopeWarnings, setModelScopeWarnings] = useState<string[]>([]);
  const [isModelScopeActive, setIsModelScopeActive] = useState(false);
  // Every available model, fetched only when the selector switches to "all" or the
  // shown model is outside the scope; null until then and after each loadModels.
  const [allModelList, setAllModelList] = useState<ModelEntry[] | null>(null);
  const allModelsLoadRef = useRef<object | null>(null);
  const [modelThinkingLevels, setModelThinkingLevels] = useState<Record<string, string[]>>({});
  const [modelThinkingLevelMaps, setModelThinkingLevelMaps] = useState<Record<string, Record<string, string | null>>>({});
  const [newSessionModel, setNewSessionModel] = useState<SelectedModel | null>(() => initialChoices?.model ?? null);
  const [newSessionDefaultModel, setNewSessionDefaultModel] = useState<SelectedModel | null>(null);
  const [toolPreset, setToolPreset] = useState<ToolPreset>(CONFIGURED_TOOL_PRESET);
  const [newSessionThinkingLevel, setNewSessionThinkingLevel] = useState<ConcreteThinkingLevel | null>(() => initialChoices?.thinkingLevel ?? null);
  // A fresh composer's harness (#250): pi by default; claude with a full model name.
  const [newSessionHarness, setNewSessionHarness] = useState<SessionHarness>(() => initialChoices?.harness ?? PI_HARNESS);
  // a claude session (or a fresh composer set to claude).
  const isClaudeSession = isNew ? newSessionHarness === CLAUDE_HARNESS : (session?.harness ?? data?.info?.harness) === CLAUDE_HARNESS;
  const [newSessionClaudeModel, setNewSessionClaudeModel] = useState<string>(() => initialChoices?.claudeModel ?? DEFAULT_CLAUDE_MODEL);
  const [claudeAvailability, setClaudeAvailability] = useState<ClaudeAvailability | null>(null);
  // a fresh claude composer's effort; an open claude session's switched values
  // (until its row catches up) and the visible line of a switch claude refused.
  const [newSessionClaudeEffort, setNewSessionClaudeEffort] = useState<ClaudeEffort>(() => initialChoices?.claudeEffort ?? CLAUDE_EFFORT_DEFAULT);
  const [claudeChoiceOverride, setClaudeChoiceOverride] = useState<{ sessionId: string; model?: string; effort?: ClaudeEffort; launchPermissionMode?: ClaudeLaunchPermissionMode } | null>(null);
  const [claudeSwitchError, setClaudeSwitchError] = useState<{ sessionId: string; message: string } | null>(null);
  // The session whose event stream said whether it is busy (`connected`): until then an open
  // claude session's controls cannot know it is idle, so they stay disabled.
  const [streamStateSessionId, setStreamStateSessionId] = useState<string | null>(null);
  const [newSessionDefaultThinkingLevel, setNewSessionDefaultThinkingLevel] = useState<ConcreteThinkingLevel | null>(null);
  const [savedDefaultThinkingLevel, setSavedDefaultThinkingLevel] = useState<ConcreteThinkingLevel | null>(null);
  const [currentThinkingOverride, setCurrentThinkingOverride] = useState<ConcreteThinkingLevel | null>(null);
  const [liveThinkingLevel, setLiveThinkingLevel] = useState<ConcreteThinkingLevel | null>(null);
  const [retryInfo, setRetryInfo] = useState<{ attempt: number; maxAttempts: number; errorMessage?: string } | null>(null);
  const [contextUsage, setContextUsage] = useState<{ percent: number | null; contextWindow: number; tokens: number | null } | null>(null);
  // Active time read from the live state, with the session it belongs to: the
  // panel's snapshot is older than the transcript on screen, and this is what
  // keeps the value current while a run appends entries (#1334).
  const [liveTotalActiveMs, setLiveTotalActiveMs] = useState<{ sessionId: string; value: number } | null>(null);
  const contextUsageRequestIdRef = useRef(0);
  // Highest request id whose reply was applied. A reply applies only when it
  // is newer, so a failed newer read never discards an older good one.
  const contextUsageAppliedIdRef = useRef(0);
  const [systemPrompt, setSystemPrompt] = useState<string | null>(null);
  const [forkingEntryId, setForkingEntryId] = useState<string | null>(null);
  const [currentModelOverride, setCurrentModelOverride] = useState<{ provider: string; modelId: string } | null>(null);
  const [liveModel, setLiveModel] = useState<{ provider: string; modelId: string } | null>(null);
  const [pendingModel, setPendingModel] = useState<{ provider: string; modelId: string } | null>(null);
  const [modelSwitching, setModelSwitching] = useState(false);
  const [isCompacting, setIsCompacting] = useState(false);
  const [autoCompactionEnabled, setAutoCompactionEnabled] = useState(true);
  const [compactError, setCompactError] = useState<string | null>(null);
  const [compactResult, setCompactResult] = useState<CompactResultInfo | null>(null);
  const [agentPhase, setAgentPhase] = useState<AgentPhase>(null);
  const [promptAnchorActive, setPromptAnchorActive] = useState(false);
  const [showScrollToBottom, setShowScrollToBottom] = useState(false);
  const [slashCommands, setSlashCommands] = useState<SlashCommandInfo[]>([]);
  const [slashCommandsLoading, setSlashCommandsLoading] = useState(false);
  const [noticeState, dispatchNotice] = useReducer(noticeReducer, { visible: [], pending: [] });
  const [sessionStatsOverride, setSessionStatsOverride] = useState<SessionStatsInfo | null>(null);
  const [extensionDialogs, setExtensionDialogs] = useState<ExtensionUiDialogRequest[]>([]);
  const [extensionCustomUis, setExtensionCustomUis] = useState<ExtensionUiCustomRequest[]>([]);
  const [extensionStatuses, setExtensionStatuses] = useState<ExtensionStatusItem[]>([]);
  const [extensionWidgets, setExtensionWidgets] = useState<ExtensionWidgetItem[]>([]);
  // The event that last touched each status/widget key, numbered: a state read
  // keeps the live value of keys touched after it began (mergeExtensionSnapshot).
  const extensionUiTouchRef = useRef({ seq: 0, status: new Map<string, number>(), widget: new Map<string, number>() });
  const [queuedMessages, setQueuedMessages] = useState<QueuedMessages>({ steering: [], followUp: [] });

  const eventConnectionRef = useRef<AgentEventConnection | null>(null);
  const eventStreamGraceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const eventStreamGraceGenerationRef = useRef(0);
  const eventStreamGraceActiveRef = useRef(false);
  // True while a hidden page has let go of the selected session's idle stream
  // (HTTP/1.1 allows a browser six connections per origin, shared by its tabs).
  const eventStreamReleasedRef = useRef(false);
  // Moves on each time a released stream is opened again, so the release
  // effect arms its grace anew for a page still hidden.
  const [eventStreamReattachCount, setEventStreamReattachCount] = useState(0);
  // The lease request in flight: the renewal tick and the release check share it.
  const leaseRequestRef = useRef<{ sid: string; promise: Promise<SessionLeaseOutcome> } | null>(null);
  const reattachEventStreamRef = useRef<(sid: string, reason: "visible" | "activity") => void>(() => {});
  // What the debug panel reads (readDebugSnapshot below): written beside the
  // timers and requests they describe, read by nothing that decides anything.
  const leaseDebugRef = useRef<Pick<DebugClientLease, "renewStartedAt" | "inFlightSince" | "requests" | "unanswered" | "errors" | "last">>({
    renewStartedAt: null, inFlightSince: null, requests: 0, unanswered: 0, errors: 0, last: null,
  });
  const releaseDebugRef = useRef<{ count: number; lastAt: number | null }>({ count: 0, lastAt: null });
  const reattachDebugRef = useRef<DebugClientSession["reattached"]>({ count: 0, lastAt: null, lastReason: null });
  const hiddenReleaseDueAtRef = useRef<number | null>(null);
  const eventStreamGraceDueAtRef = useRef<number | null>(null);
  const isCompactingRef = useRef(false);
  const extensionUiCountRef = useRef(0);
  const sessionIdRef = useRef<string | null>(session?.id ?? null);
  const sessionPropIdRef = useRef<string | null>(session?.id ?? null);
  // False while the session carries no tool selection of its own, so its loadout
  // follows settings.json defaultTools and the picker must say so rather than
  // labelling it with whichever preset the resolved tools happen to match.
  const sessionToolsPinnedRef = useRef(false);
  const agentRunningRef = useRef(false);
  // Completed messages the stream has delivered during the current run. An
  // external-write signal (the sidebar's watcher ring) needs a read only when
  // the stream has not delivered content since the previous one: on a healthy
  // stream it has, and the SSE path is authoritative (#153).
  const runContentEventsRef = useRef(0);
  const externalWriteContentEventsRef = useRef(0);
  // Messages a mid-run read newly loaded: the stream may still deliver their
  // buffered message_end events, which must not append them a second time.
  const lateSnapshotMessagesRef = useRef<LateSnapshotMessages | null>(null);
  const sdkAgentActiveRef = useRef(false);
  const rpcPromptPendingRef = useRef(false);
  const notifiedPromptRunIdRef = useRef(-1);
  const bashRunningRef = useRef(false);
  const bashRecoveryIdRef = useRef(0);
  const handleAgentEventRef = useRef<((event: AgentEvent) => void) | null>(null);
  const initialScrollDoneRef = useRef(Boolean(opts.deferInitialScroll));
  const lastUserMsgRef = useRef<HTMLDivElement | null>(null);
  const pendingScrollToUserRef = useRef(false);
  const isNearBottomRef = useRef(true);
  const previousScrollTopRef = useRef(0);
  const liveFollowFrameRef = useRef<number | null>(null);
  const executeBashRef = useRef<(command: string, excludeFromContext: boolean) => Promise<void> | undefined>(undefined);
  const handleNavigateRef = useRef<((entryId: string) => Promise<boolean>) | undefined>(undefined);
  const scrollContainerRef = useRef<HTMLDivElement | null>(null);
  const ensuringNewSessionRef = useRef<Promise<string | null> | null>(null);
  // The command list as last loaded, and the get_commands request under way, if
  // any. A bare /mcp reads them to tell whose /mcp it is (handleBuiltinSlashCommand).
  // The generation moves on with every request and every clear, and only a request
  // still current writes its answer: one that set_tools outdated (or a newer request)
  // would otherwise put back the list of a session that is no longer there.
  const slashCommandsRef = useRef<SlashCommandInfo[]>([]);
  const slashCommandsLoadRef = useRef<Promise<SlashCommandInfo[] | null> | null>(null);
  const slashCommandsGenerationRef = useRef(0);
  const newSessionPromotedRef = useRef(false);
  const newSessionModelOverrideRef = useRef<SelectedModel | null>(initialChoices?.model ?? null);
  const thinkingLevelOverrideRef = useRef<ConcreteThinkingLevel | null>(initialChoices?.thinkingLevel ?? null);
  const thinkingLevelPinsRef = useRef<Record<string, string>>({});
  const defaultThinkingLevelRef = useRef<ConcreteThinkingLevel | null>(null);
  const promptRunIdRef = useRef(0);
  const optimisticUserMessageKeyRef = useRef<string | null>(null);
  // The run whose prompt the server is known to have got: one of its events
  // (agent_start, prompt_done, queue_update, its user message) arrived, maybe
  // before its request failed. Later ones clear the unconfirmed record itself.
  const landedPromptRunIdRef = useRef<number | null>(null);
  const unconfirmedSendRef = useRef<UnconfirmedSend | null>(null);
  // The context the newest session read found, and the order its request
  // started in: only a read started after the run settled can settle it.
  const sessionReadSeqRef = useRef(0);
  const lastReadContextRef = useRef<{ sid: string; seq: number; messages: AgentMessage[]; entryIds: string[] } | null>(null);
  // The read whose answer the view holds: a response that started before it is
  // older than what is on screen and is dropped.
  const lastAppliedReadSeqRef = useRef(0);
  // The read that last raised the loading screen: a read found stale clears
  // the loading only when it is still its own.
  const loadingReadSeqRef = useRef(0);
  const markPromptLanded = useCallback(() => {
    landedPromptRunIdRef.current = promptRunIdRef.current;
    if (unconfirmedSendRef.current?.runId === promptRunIdRef.current) unconfirmedSendRef.current = null;
  }, []);
  const modelSwitchPendingRef = useRef(false);
  const draftKeyAliasesRef = useRef(new Map<string, string>());
  const sessionHookMountedRef = useRef(true);
  // In-flight session reads, keyed by session id (or force:<id> for fresh reads).
  const loadFlightsRef = useRef(new Map<string, Promise<unknown>>());
  // Latest settled view state, readable from the unmount cleanup without
  // re-subscribing it. Assigned every render like sessionPropIdRef below.
  const dataRef = useRef<SessionData | null>(null);
  const messagesRef = useRef<AgentMessage[]>([]);
  const entryIdsRef = useRef<string[]>([]);
  const activeLeafIdRef = useRef<string | null>(null);
  const historyCursorRef = useRef<string | null>(null);
  const hasEarlierMessagesRef = useRef(false);

  sessionPropIdRef.current = session?.id ?? null;
  dataRef.current = data;
  messagesRef.current = messages;
  entryIdsRef.current = entryIds;
  activeLeafIdRef.current = activeLeafId;
  historyCursorRef.current = historyCursor;
  hasEarlierMessagesRef.current = hasEarlierMessages;
  isCompactingRef.current = isCompacting;
  extensionUiCountRef.current = extensionDialogs.length + extensionCustomUis.length;

  // Whether the page wants the stream of `sid` open: the connection asks it
  // before each (re)connect, the debug reader to show it.
  const wantsEventStream = useCallback((sid: string) => (
    sessionHookMountedRef.current
    && sessionIdRef.current === sid
    && (
      agentRunningRef.current
      || eventStreamGraceActiveRef.current
      || (sessionPropIdRef.current === sid && !eventStreamReleasedRef.current)
    )
  ), []);

  if (!eventConnectionRef.current) {
    eventConnectionRef.current = new AgentEventConnection({
      createSource: (sid) => trackEventSource(
        "agent",
        new EventSource(`/api/agent/${encodeURIComponent(sid)}/events?holder=${encodeURIComponent(pageId())}`),
        sid,
      ),
      onEvent: (event) => handleAgentEventRef.current?.(event as AgentEvent),
      shouldMaintain: wantsEventStream,
      readinessTimeoutMs: EVENT_STREAM_READY_TIMEOUT_MS,
      reconnectDelayMs: EVENT_STREAM_RECONNECT_DELAY_MS,
      staleAfterMs: EVENT_STREAM_STALE_MS,
      onUnexpectedError: (error) => {
        console.error("Failed to maintain the agent event stream:", error);
      },
    });
  }

  const setToolPresetState = opts.setToolPreset ?? setToolPreset;
  const existingSessionId = session?.id;

  useLayoutEffect(() => {
    if (!existingSessionId && (!isNew || sessionIdRef.current)) return;
    setToolPresetState(getPreferredToolPreset());
  }, [existingSessionId, isNew, setToolPresetState]);

  const onNewSessionChoicesChange = opts.onNewSessionChoicesChange;
  useEffect(() => {
    if (isNew) {
      onNewSessionChoicesChange?.({
        model: newSessionModel,
        thinkingLevel: newSessionThinkingLevel,
        harness: newSessionHarness,
        claudeModel: newSessionClaudeModel,
        claudeEffort: newSessionClaudeEffort,
      });
    }
  }, [isNew, newSessionModel, newSessionThinkingLevel, newSessionHarness, newSessionClaudeModel, newSessionClaudeEffort, onNewSessionChoicesChange]);

  // A fresh composer asks once whether claude is on PATH (the harness picker's state) and
  // which models claude offers; an open claude session asks for its model list.
  const wantsClaudeAvailability = isNew || isClaudeSession;
  useEffect(() => {
    if (!wantsClaudeAvailability) return;
    let current = true;
    void (async () => {
      try {
        const response = await fetch("/api/claude");
        if (!response.ok) return;
        const body = await response.json() as { available?: unknown; models?: unknown };
        const models = Array.isArray(body.models)
          ? body.models.map((model: { value?: unknown } | null) => model?.value).filter(isClaudeModelName)
          : [];
        if (current) setClaudeAvailability({ available: body.available === true, models });
      } catch {
        // Unknown: the claude option stays unavailable.
      }
    })();
    return () => { current = false; };
  }, [wantsClaudeAvailability]);

  const scrollToBottom = useCallback((behavior: ScrollBehavior = "smooth") => {
    const container = scrollContainerRef.current;
    if (!container) return;
    // Scroll the chat container itself instead of scrolling a sentinel element
    // into view: that propagates to every scrollable ancestor, and on mobile
    // the keyboard-shifted document layer visibly jumps the whole app while
    // streaming content follows the tail.
    container.scrollTo({ top: container.scrollHeight, behavior });
    previousScrollTopRef.current = container.scrollTop;
  }, []);

  const currentModel = currentModelOverride ?? liveModel ?? data?.context.model ?? pendingModel ?? null;
  const displayModel = isNew
    ? (newSessionModel ?? newSessionDefaultModel)
    : currentModel ?? (data?.context.messages.length === 0 ? newSessionDefaultModel : null);
  const contextThinkingLevel = asConcreteThinkingLevel(
    data?.context.thinkingLevel && data.context.thinkingLevel !== "off"
      ? data.context.thinkingLevel
      : null,
  );
  const currentThinkingLevel = currentThinkingOverride ?? liveThinkingLevel ?? contextThinkingLevel;
  const displayThinkingLevel = isNew
    ? (newSessionThinkingLevel ?? newSessionDefaultThinkingLevel)
    : currentThinkingLevel ?? (data?.context.messages.length === 0 ? newSessionDefaultThinkingLevel : null);
  const composerDraftKey = session?.id ?? newSessionDraftKey ?? undefined;

  const syncLiveModel = useCallback((state?: AgentStateResponse) => {
    setLiveModel(state?.model
      ? { provider: state.model.provider, modelId: state.model.id }
      : null);
    if (state?.thinkingLevel !== undefined) {
      setLiveThinkingLevel(asConcreteThinkingLevel(state.thinkingLevel));
    }
  }, []);

  const resolveComposerDraftKey = useCallback((key: string | undefined) => {
    if (!key) return undefined;
    let resolved = key;
    const visited = new Set<string>();
    while (!visited.has(resolved)) {
      visited.add(resolved);
      const next = draftKeyAliasesRef.current.get(resolved);
      if (!next) break;
      resolved = next;
    }
    return resolved;
  }, []);

  const restoreSubmission = useCallback((
    text: string,
    images: Array<{ data: string; mimeType: string }> | undefined,
    targetDraftKey: string | undefined,
  ) => {
    const draftImages = images?.map(({ data, mimeType }) => ({ data, mimeType }));
    const destinationDraftKey = resolveComposerDraftKey(targetDraftKey);
    if (
      !sessionHookMountedRef.current
      && !newSessionPromotedRef.current
      && targetDraftKey === newSessionDraftKey
    ) return;
    const input = opts.chatInputRef?.current;
    if (input) {
      input.restoreSubmission(text, draftImages, destinationDraftKey);
    } else if (destinationDraftKey) {
      restoreDraftSubmission(destinationDraftKey, text, draftImages);
    }
  }, [newSessionDraftKey, opts.chatInputRef, resolveComposerDraftKey]);

  // Editing a past message only prefills the composer; the branch moves when it is sent,
  // so cancelling or reloading never hides the rest of the conversation.
  const [editEntryId, setEditEntryId] = useState(() => {
    const sid = session?.id;
    if (sid && !getDraft(sid)) pendingHistoryEdits.delete(sid);
    return (sid && pendingHistoryEdits.get(sid)) || null;
  });
  const setEdit = useCallback((entryId: string | null) => {
    if (!session?.id) return;
    if (entryId) pendingHistoryEdits.set(session.id, entryId);
    else pendingHistoryEdits.delete(session.id);
    setEditEntryId(entryId);
  }, [session?.id]);
  const handleEditContent = useCallback((message: UserMessage, entryId: string) => {
    if (!session?.id) return;
    opts.chatInputRef?.current?.replaceMessage(message);
    setEdit(entryId);
  }, [opts.chatInputRef, session?.id, setEdit]);
  const cancelEdit = useCallback(() => setEdit(null), [setEdit]);

  const sessionStats = useMemo(() => {
    // Active time is a monotone sum over the session's entries, so of the two
    // readings the larger one is the newer one: the live state leads while a run
    // appends entries behind the panel's snapshot, and the snapshot leads after
    // a compaction or a reload, which install a newer one without reading the
    // state (#1334).
    const snapshotActiveMs = data?.totalActiveMs;
    const liveActiveMs = liveTotalActiveMs && liveTotalActiveMs.sessionId === data?.sessionId
      ? liveTotalActiveMs.value
      : undefined;
    const totalActiveMs = snapshotActiveMs === undefined && liveActiveMs === undefined
      ? undefined
      : Math.max(snapshotActiveMs ?? 0, liveActiveMs ?? 0);
    if (sessionStatsOverride) {
      return {
        ...sessionStatsOverride,
        totalActiveMs,
        ...(contextUsage ? { contextUsage } : {}),
      };
    }
    const fileStats = data?.stats;
    const stats = mergeSessionStats(fileStats, data?.context.messages ?? [], messages);
    if (stats.tokens.total === 0 && messages.length === 0 && !fileStats) return null;
    return {
      sessionFile: data?.filePath || undefined,
      sessionId: sessionIdRef.current ?? session?.id ?? "",
      sessionName: session?.name,
      ...stats,
      totalActiveMs,
      ...(contextUsage ? { contextUsage } : {}),
    } satisfies SessionStatsInfo;
  }, [messages, sessionStatsOverride, contextUsage, liveTotalActiveMs, data?.sessionId, data?.context.messages, data?.filePath, data?.totalActiveMs, data?.stats, session?.id, session?.name]);

  const applyLiveUsage = useCallback((state: AgentStateResponse | undefined, sid: string, runId: number, requestId: number) => {
    if (!sessionHookMountedRef.current || sessionIdRef.current !== sid
      || promptRunIdRef.current !== runId || requestId <= contextUsageAppliedIdRef.current) return;
    contextUsageAppliedIdRef.current = requestId;
    if (state?.contextUsage !== undefined) setContextUsage(state.contextUsage ?? null);
    if (typeof state?.totalActiveMs === "number") setLiveTotalActiveMs({ sessionId: sid, value: state.totalActiveMs });
  }, []);

  // Applies a reply's widget section key by key, through the merge the mount's
  // state read uses (#169): a key whose `setWidget` event
  // reached the page after the read was issued keeps the page's state (present
  // or removed), and every other key takes the snapshot's entry, including a
  // removal the stream never touched. The reply's other fields apply as usual.
  // `touchSeqAtRead` is `extensionUiTouchRef`'s sequence captured immediately
  // before the read's request; every widget read site applies it this way.
  const applySnapshotWidgets = useCallback((state: AgentStateResponse | undefined, touchSeqAtRead: number) => {
    const snapshot = state?.extensionWidgets;
    if (snapshot === undefined) return;
    const touched = touchedSince(extensionUiTouchRef.current.widget, touchSeqAtRead);
    setExtensionWidgets((prev) => mergeExtensionSnapshot(snapshot, prev, touched));
  }, []);

  const refreshContextUsage = useCallback(async (sid: string) => {
    const runId = promptRunIdRef.current;
    const requestId = ++contextUsageRequestIdRef.current;
    try {
      const res = await fetch(agentStatusUrl(sid));
      if (!res.ok) return;
      const data = await res.json() as { state?: AgentStateResponse };
      applyLiveUsage(data.state, sid, runId, requestId);
    } catch {
      // A later message or the running-state poll retries the usage read.
    }
  }, [applyLiveUsage]);

  const loadSession = useCallback(async (sid: string, showLoading = false, includeState = false, options?: {
    force?: boolean;
    quiet?: boolean;
    expectRunId?: number;
    // The manual fetch's: `signal` aborts the read at its 15 s
    // bound; `preserveHistory` merges a changed revision's newest window into the
    // paged-in history instead of replacing it; `suppressReconnect` keeps a
    // `wrapperRebuilt` reply from reconnecting a stream the trigger already
    // reconnected; `onLoaded` gets the fresh read before it applies.
    signal?: AbortSignal;
    preserveHistory?: boolean;
    suppressReconnect?: boolean;
    onLoaded?: (data: SessionData) => void;
  }) => {
    // Single-flight: concurrent reads for the same session (mount + SSE settle +
    // reconcile) share one request unless the caller forces a fresh read.
    const flightKey = options?.force ? `force:${sid}` : sid;
    const inflight = options?.force ? undefined : loadFlightsRef.current.get(flightKey);
    if (inflight) return await inflight;
  const flight = (async (): Promise<unknown> => {
    let messagesLoaded = false;
    const readSeq = ++sessionReadSeqRef.current;
    // Completed messages already delivered on the stream when this read was
    // issued: any that arrive later are newer than the answer.
    const contentEventsAtRead = runContentEventsRef.current;
    try {
      if (showLoading) {
        loadingReadSeqRef.current = readSeq;
        setLoading(true);
      }
      const params = new URLSearchParams({ deferThinking: "1", deferMedia: "1", tree: "summary" });
      if (options?.force) params.set("force", "1");
      const res = await fetch(`/api/sessions/${encodeURIComponent(sid)}?${params}`, { signal: options?.signal });
      if (res.status === 404) {
        if (showLoading) {
          setData(null);
          setActiveLeafId(null);
          setMessages([]);
          setEntryIds([]);
          setHistoryCursor(null);
          setHasEarlierMessages(false);
          setError(null);
        }
        deleteSessionViewSnapshot(sid);
        return null;
      }
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const d = await res.json() as SessionData;
      if (sessionIdRef.current !== sid) return null;
      // A recovery read started for one run must not apply after the next run
      // has begun: it would replace the view with an older snapshot.
      if (options?.expectRunId !== undefined && promptRunIdRef.current !== options.expectRunId) return null;
      // Timed out: nothing of the read applies, the view stays as it was.
      if (options?.signal?.aborted) return "error";
      options?.onLoaded?.(d);
      // A response is not ordered against later reads by anything else: one
      // that started before the last applied read would put back a snapshot
      // the view has already moved past.
      const staleRead = readSeq < lastAppliedReadSeqRef.current;
      // Only the snapshot/message half is stale: an includeState read's state
      // is a separate, later /state request whose fields are ordered by the
      // usage request id and the extension touch sequence, so it still runs
      // and applies (#177 code review).
      if (staleRead && showLoading) {
        // The newer read has already applied the view: the loading screen this
        // read raised ends now, not after its /state round trip. A newer read's
        // own loading is never cleared here.
        messagesLoaded = true;
        if (loadingReadSeqRef.current === readSeq) setLoading(false);
      }
      if (!staleRead) {
        lastAppliedReadSeqRef.current = readSeq;
        if ((lastReadContextRef.current?.seq ?? 0) < readSeq) {
          lastReadContextRef.current = { sid, seq: readSeq, messages: d.context.messages, entryIds: d.context.entryIds ?? [] };
        }
        // Freshness check: when the disk snapshot is unchanged (same opaque
        // revision), keep any history the user already paged in instead of
        // collapsing back to the fresh 50-entry window. The state hooks then
        // only need the server's tree/leaf/stats — never a history reset.
        const cached = getSessionViewSnapshot(sid);
        // pi#83: the window-preservation branch also requires the client to
        // actually HOLD those messages. A hover prefetch (lib/session-prefetch)
        // can land a snapshot WHILE this force read is in flight on a fresh
        // mount — messagesRef.current is still [] then, and trusting the
        // revision alone blanked the pane (adopt the server window instead).
        const revisionUnchanged = Boolean(
          d.snapshotRevision
          && cached?.revision === d.snapshotRevision
          && cached.entryIds.length >= (d.context.entryIds ?? []).length
          && messagesRef.current.length > 0,
        );
        const loadedWindow = {
          messages: messagesRef.current,
          entryIds: entryIdsRef.current,
          oldestEntryId: historyCursorRef.current,
          hasMore: hasEarlierMessagesRef.current,
        };
        const freshWindow = {
          messages: d.context.messages,
          entryIds: d.context.entryIds ?? [],
          oldestEntryId: d.context.oldestEntryId,
          hasMore: d.context.hasMore,
        };
        // A manual fetch keeps the history already paged in: the
        // fresh newest window replaces the view from its first entry on.
        const baseWindow = revisionUnchanged ? loadedWindow
          : options?.preserveHistory ? mergeLoadedHistory(loadedWindow, freshWindow)
            : freshWindow;
        // A snapshot can end before messages the stream delivered after the read
        // was issued: the view's live tail stays after it. The
        // merged manual-fetch window already holds the loaded view's tail.
        const streamedSinceRead = runContentEventsRef.current > contentEventsAtRead;
        const appliedMessages = !revisionUnchanged && !options?.preserveHistory && streamedSinceRead
          ? keepStreamedMessages(
              { messages: freshWindow.messages, entryIds: freshWindow.entryIds },
              { messages: messagesRef.current, entryIds: entryIdsRef.current },
            )
          : baseWindow.messages;
        const view = appliedMessages === baseWindow.messages ? baseWindow : { ...baseWindow, messages: appliedMessages };
        // The offset belongs to the page that owns a cursor: the fresh read's
        // own, else the one already loaded (a kept or merged older window).
        // A missing value reads as zero.
        const turnsBeforeFor = (oldestEntryId: string | null) => oldestEntryId === d.context.oldestEntryId
          ? d.context.turnsBefore ?? 0
          : dataRef.current?.context.turnsBefore ?? 0;
        const viewTurnsBefore = turnsBeforeFor(view.oldestEntryId);
        setData(view === freshWindow ? d : { ...d, context: { ...d.context, ...view, turnsBefore: viewTurnsBefore } });
        setClaudeSubagentState((state) => foldClaudeSubagents(state, sid, d.claudeSubagents, "history"));
        setActiveLeafId(d.leafId);
        if (revisionUnchanged) {
          // The kept window's offset (set on `data` above) still comes from
          // the page owning its cursor: a snapshot written without one (a hover
          // prefetch from an older build) must not pin the view at zero.
          // Refresh the cached snapshot with the newest leaf/tree/stats while
          // preserving the wider loaded window.
          setSessionViewSnapshot({
            sessionId: sid,
            revision: d.snapshotRevision!,
            messages: messagesRef.current,
            entryIds: entryIdsRef.current,
            leafId: d.leafId,
            oldestEntryId: historyCursorRef.current,
            hasMore: hasEarlierMessagesRef.current,
            turnsBefore: viewTurnsBefore,
            summaryTree: d.tree,
            thinkingLevel: d.context.thinkingLevel,
            model: d.context.model,
            stats: d.stats,
            totalActiveMs: d.totalActiveMs,
            loadedEntryIds: entryIdsRef.current,
          });
        } else {
          const previous = { messages: loadedWindow.messages, entryIds: loadedWindow.entryIds };
          const next = { messages: view.messages, entryIds: view.entryIds };
          // A read that applies during a run can show messages the stream has not
          // delivered yet; when it resumes, their buffered message_end events must
          // not render them again. Only messages after the view's anchor that the
          // view did not already hold live are late.
          if (agentRunningRef.current) {
            const runId = promptRunIdRef.current;
            const late = lateSnapshotTail(previous, next, optimisticUserMessageKeyRef.current);
            if (late.length > 0) {
              const prior = lateSnapshotMessagesRef.current;
              lateSnapshotMessagesRef.current = {
                sid,
                runId,
                messages: [...(prior && prior.sid === sid && prior.runId === runId ? prior.messages : []), ...late],
              };
            }
            // The snapshot is now the truth for these messages: its copy of the
            // prompt replaces the optimistic bubble, and a completed reply ends
            // the partial bubble that streams the same message. The prompt only
            // counts when every message the view held before its bubble precedes
            // it in the snapshot: a same-text prompt from an earlier turn is not
            // this send.
            const optimisticKey = optimisticUserMessageKeyRef.current;
            if (optimisticKey) {
              const bubble = previous.messages.findLastIndex((message) => message.role === "user" && userMessageKey(message) === optimisticKey);
              const beforeBubble = bubble === -1 ? [] : previous.messages.slice(0, bubble);
              let shared = 0;
              while (shared < beforeBubble.length && shared < next.messages.length && sameMessage(beforeBubble[shared], next.messages[shared])) shared++;
              const landed = next.messages[shared];
              if (shared === beforeBubble.length && landed?.role === "user" && userMessageKey(landed) === optimisticKey) {
                optimisticUserMessageKeyRef.current = null;
                markPromptLanded();
              }
            }
            const streaming = streamingStore.getState().streamingMessage;
            if (streaming && next.messages.some((message) => sameMessage(streaming, message))) {
              dispatch({ type: "end" });
            }
          } else if (options?.preserveHistory) {
            // A manual fetch while the view saw no run: a half-dead stream may
            // still hold that run's events, and must not render them again. A
            // fetch that reconnected left that stream: the new one replays none.
            const late = options.suppressReconnect ? [] : lateSnapshotTail(previous, next, null);
            lateSnapshotMessagesRef.current = late.length > 0
              ? { sid, runId: promptRunIdRef.current, messages: late, beforeRun: { openedAt: eventConnectionRef.current?.snapshot().openedAt ?? null }, recordedAt: Date.now() }
              : null;
          }
          setMessageKeyAliases((aliases) => carryMessageKeys({ ...previous, aliases }, next));
          setMessages(next.messages);
          setEntryIds(next.entryIds);
          setHistoryCursor(view.oldestEntryId);
          setHasEarlierMessages(view.hasMore);
        }
        // Tool-preset state is independent of the view cache: it must be applied
        // on every read, cached window or not (#700).
        sessionToolsPinnedRef.current = d.toolNames !== undefined;
        setToolPresetState(d.toolNames !== undefined ? getPresetFromToolNames(d.toolNames) : CONFIGURED_TOOL_PRESET);
        setCurrentModelOverride((current) => modelSwitchPendingRef.current ? current : null);
        setCurrentThinkingOverride(null);
        setError(null);
        if (d.treeFormat === "summary" && d.snapshotRevision) {
          // A merged window is cached whole; otherwise the read's own entries.
          const stored = view === freshWindow || revisionUnchanged ? { ...freshWindow, messages: appliedMessages } : view;
          setSessionViewSnapshot({
            sessionId: sid,
            revision: d.snapshotRevision,
            messages: appliedMessages,
            entryIds: stored.entryIds,
            leafId: d.leafId,
            oldestEntryId: stored.oldestEntryId,
            hasMore: stored.hasMore,
            turnsBefore: turnsBeforeFor(stored.oldestEntryId),
            summaryTree: d.tree,
            thinkingLevel: d.context.thinkingLevel,
            model: d.context.model,
            stats: d.stats,
            totalActiveMs: d.totalActiveMs,
            loadedEntryIds: stored.entryIds,
          });
        }
        if (d.wrapperRebuilt && !options?.suppressReconnect) {
          eventConnectionRef.current?.close("rebuilt");
          eventConnectionRef.current?.maintain(sid);
        }
        if (!includeState && d.context.thinkingLevel && d.context.thinkingLevel !== "off") {
          setLiveThinkingLevel(asConcreteThinkingLevel(d.context.thinkingLevel));
        }

        messagesLoaded = true;
        if (showLoading) setLoading(false);
      }
      if (!includeState) return null;

      try {
        const runId = promptRunIdRef.current;
        const usageRequestId = ++contextUsageRequestIdRef.current;
        const touchSeq = extensionUiTouchRef.current.seq;
        const stateRes = await fetch(`/api/sessions/${encodeURIComponent(sid)}/state`);
        if (!stateRes.ok) throw new Error(`HTTP ${stateRes.status}`);
        const agentState = await stateRes.json() as { running: boolean; state?: AgentStateResponse };
        if (sessionIdRef.current !== sid) return null;

        const liveState = agentState.state;
        syncLiveModel(liveState);
        if (liveState) {
          applyLiveUsage(liveState, sid, runId, usageRequestId);
          if (liveState.systemPrompt !== undefined) setSystemPrompt(liveState.systemPrompt ?? null);
          // Events the stream delivered during the read are newer than it.
          if (liveState.extensionStatuses !== undefined) {
            setExtensionStatuses((prev) => mergeExtensionSnapshot(
              liveState.extensionStatuses ?? [],
              prev,
              touchedSince(extensionUiTouchRef.current.status, touchSeq),
            ));
          }
          applySnapshotWidgets(liveState, touchSeq);
          if (liveState.queuedMessages !== undefined) setQueuedMessages(normalizeQueuedMessages(liveState.queuedMessages));
          if (liveState.autoCompactionEnabled !== undefined) setAutoCompactionEnabled(liveState.autoCompactionEnabled ?? true);
        } else if (!agentState.running) {
          setQueuedMessages({ steering: [], followUp: [] });
        }
        return agentState;
      } catch (e) {
        console.error("Failed to load agent state:", e);
        return null;
      }
    } catch (e) {
      // A background or recovery read (an external write, a running stream's
      // missed messages) must never replace the page with the error screen; a
      // failed read keeps the last good conversation and the composer (#153).
      if (options?.quiet) console.error("Failed to refresh session:", e);
      else setError(String(e));
      return "error";
    } finally {
      if (showLoading && !messagesLoaded) setLoading(false);
    }
    })();
    if (!options?.force) loadFlightsRef.current.set(flightKey, flight);
    flight.finally(() => {
      if (loadFlightsRef.current.get(flightKey) === flight) loadFlightsRef.current.delete(flightKey);
    });
    return await flight;
  }, [applyLiveUsage, applySnapshotWidgets, dispatch, markPromptLanded, setToolPresetState, streamingStore, syncLiveModel]);

  /**
   * Reloads the given session from disk after an external write (another pi
   * process appended to the same file). While a run, bash execution or
   * compaction is active the stream is authoritative, but a half-dead stream
   * delivers nothing: a read is made only when no completed message has
   * arrived on it since the previous external-write signal. The read keeps the
   * live streaming bubble (it lives outside `messages`), the run id and the
   * settlement state (loadSession does not touch them), and a failed read is
   * quiet so the conversation and composer stay.
   */
  const refreshFromDisk = useCallback(async (sid: string) => {
    if (!sessionHookMountedRef.current) return;
    if (sessionIdRef.current !== sid) return;
    const running = agentRunningRef.current || bashRunningRef.current || isCompacting;
    if (!running) {
      await loadSession(sid, false, false, { force: true, quiet: true, expectRunId: promptRunIdRef.current });
      return;
    }
    const delivered = runContentEventsRef.current;
    const covered = delivered > externalWriteContentEventsRef.current;
    externalWriteContentEventsRef.current = delivered;
    if (covered) return;
    await loadSession(sid, false, false, { force: true, quiet: true, expectRunId: promptRunIdRef.current });
  }, [isCompacting, loadSession]);

  const loadContext = useCallback(async (sid: string, leafId: string | null, before?: string | null, options?: { tail?: number; signal?: AbortSignal }) => {
    try {
      const params = new URLSearchParams({ deferThinking: "1", deferMedia: "1" });
      if (leafId) params.set("leafId", leafId);
      // Page upward: ask the server for the `tail` ancestors preceding `before`,
      // then prepend them. Omitting `before` fetches the most-recent `tail`.
      if (before) params.set("before", before);
      if (options?.tail) params.set("tail", String(options.tail));
      const url = `/api/sessions/${encodeURIComponent(sid)}/context?${params}`;
      const res = await fetch(url, { signal: options?.signal });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const d = await res.json() as { context: SessionData["context"] };
      if (sessionIdRef.current !== sid || options?.signal?.aborted || !sessionHookMountedRef.current) return;
      setHistoryCursor(d.context.oldestEntryId);
      setHasEarlierMessages(d.context.hasMore);
      // The page owning the new cursor carries its offset (older page or first page).
      setData((prev) => {
        if (!prev || prev.sessionId !== sid) return prev;
        const context = before ? {
          ...prev.context,
          messages: [...d.context.messages, ...prev.context.messages],
          entryIds: [...d.context.entryIds, ...prev.context.entryIds],
          oldestEntryId: d.context.oldestEntryId,
          hasMore: d.context.hasMore,
          turnsBefore: d.context.turnsBefore ?? 0,
        } : d.context;
        return { ...prev, context };
      });
      if (before) {
        // Older page: prepend so scroll position stays anchored.
        setMessages((prev) => [...d.context.messages, ...prev]);
        setEntryIds((prev) => [...d.context.entryIds, ...prev]);
      } else {
        setMessages(d.context.messages);
        setEntryIds(d.context.entryIds ?? []);
      }
      return d.context;
    } catch (e) {
      if (!options?.signal?.aborted) console.error("Failed to load context:", e);
    }
  }, []);

  const loadTools = useCallback(async (sid: string) => {
    try {
      const tools = await sendAgentCommand<ToolEntry[]>(sid, { type: "get_tools" });
      if (!tools || !sessionHookMountedRef.current || sessionIdRef.current !== sid) return null;
      const { getPresetFromTools } = await import("@/lib/tool-presets");
      setToolPresetState(sessionToolsPinnedRef.current ? getPresetFromTools(tools) : CONFIGURED_TOOL_PRESET);
      onSystemToolsChange?.(tools);
      return tools;
    } catch (e) {
      console.error("Failed to load tools:", e);
      return null;
    }
  }, [onSystemToolsChange, setToolPresetState]);

  const promoteNewSession = useCallback((messageCount = 0, firstMessage = "(no messages)") => {
    const sid = sessionIdRef.current;
    if (!isNew || !newSessionCwd || !sid || newSessionPromotedRef.current) return;
    newSessionPromotedRef.current = true;
    const provisionalDraftKey = newSessionDraftKey;
    if (!provisionalDraftKey) return;
    if (provisionalDraftKey !== sid) {
      draftKeyAliasesRef.current.set(provisionalDraftKey, sid);
      const input = opts.chatInputRef?.current;
      if (input) input.rekeyDraft(provisionalDraftKey, sid);
      else rekeyDraft(provisionalDraftKey, sid);
    }
    onSessionCreated?.({
      id: sid,
      path: "",
      cwd: newSessionCwd,
      name: undefined,
      created: new Date().toISOString(),
      modified: new Date().toISOString(),
      messageCount,
      firstMessage,
      transient: true,
      ...(newSessionHarness === CLAUDE_HARNESS
        ? { harness: CLAUDE_HARNESS, claude: { model: newSessionClaudeModel, effort: newSessionClaudeEffort } }
        : {}),
    }, provisionalDraftKey);
  }, [isNew, newSessionCwd, newSessionDraftKey, onSessionCreated, opts.chatInputRef, newSessionHarness, newSessionClaudeModel, newSessionClaudeEffort]);

  const ensureNewSession = useCallback(async () => {
    if (sessionIdRef.current) return sessionIdRef.current;
    if (!isNew || !newSessionCwd) return sessionIdRef.current;
    if (ensuringNewSessionRef.current) return ensuringNewSessionRef.current;

    const promise = (async () => {
      // Only send explicit user overrides. The server resolves the current
      // enabledModels scope atomically with AgentSession construction.
      const selectedModel = newSessionModelOverrideRef.current;
      const selectedThinkingLevel = thinkingLevelOverrideRef.current;
      if (selectedModel) setPendingModel(selectedModel);
      // Undefined means the user never overrode the loadout: omit the field entirely
      // so pi resolves settings.json defaultTools instead of being pinned to ours (#700).
      const toolNames = getToolNamesForPreset(toolPreset);
      sessionToolsPinnedRef.current = toolNames !== undefined;
      const res = await fetch("/api/agent/new", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(newSessionRequestBody({
          cwd: newSessionCwd,
          harness: newSessionHarness,
          claudeModel: newSessionClaudeModel,
          claudeEffort: newSessionClaudeEffort,
          toolNames,
          model: selectedModel,
          thinkingLevel: selectedThinkingLevel,
        })),
      });
      if (!res.ok) {
        // The server's reason (a project folder that no longer exists, a model Chat only
        // cannot load) beats a bare status.
        const body = await res.json().catch(() => null) as { error?: unknown } | null;
        const message = typeof body?.error === "string" ? body.error : `HTTP ${res.status}`;
        const cwdGone = missingCwdFromBody(body);
        throw cwdGone ? new AgentCommandError(message, res.status, undefined, undefined, cwdGone) : new Error(message);
      }
      const result = await res.json() as {
        sessionId: string;
        model?: SelectedModel | null;
        thinkingLevel?: ThinkingLevelOption;
      };
      const realId = result.sessionId;
      sessionIdRef.current = realId;
      if (result.model && newSessionModelOverrideRef.current === selectedModel) {
        setPendingModel(result.model);
        if (!selectedModel) setNewSessionDefaultModel(result.model);
      }
      if (
        result.thinkingLevel
        && thinkingLevelOverrideRef.current === selectedThinkingLevel
      ) {
        setLiveThinkingLevel(asConcreteThinkingLevel(result.thinkingLevel));
        if (!selectedThinkingLevel) {
          setNewSessionDefaultThinkingLevel(asConcreteThinkingLevel(result.thinkingLevel));
        }
      }
      return realId;
    })();

    ensuringNewSessionRef.current = promise;
    try {
      return await promise;
    } finally {
      ensuringNewSessionRef.current = null;
    }
  }, [isNew, newSessionCwd, toolPreset, newSessionHarness, newSessionClaudeModel, newSessionClaudeEffort]);

  // Opening the System or Tools panel may initialize an otherwise dormant
  // session. This is deliberately a non-prompt command: it creates no message
  // or model run, but lets users inspect the exact prompt before sending one.
  const loadSystemInfo = useCallback(async () => {
    const sid = sessionIdRef.current ?? await ensureNewSession();
    if (!sid) return;

    const [state] = await Promise.all([
      sendAgentCommand<AgentStateResponse>(sid, { type: "get_state" }),
      loadTools(sid),
    ]);
    if (!sessionHookMountedRef.current || sessionIdRef.current !== sid) return;
    syncLiveModel(state);
    setSystemPrompt(state.systemPrompt ?? "");
  }, [ensureNewSession, loadTools, syncLiveModel]);

  const replaceSlashCommands = useCallback((commands: SlashCommandInfo[]) => {
    slashCommandsRef.current = commands;
    setSlashCommands(commands);
  }, []);

  // The session's tools changed (set_tools may have rebuilt it as Chat only): forget the
  // list and the request under way, so the next reader asks the session as it is now.
  const clearSlashCommands = useCallback(() => {
    slashCommandsGenerationRef.current += 1;
    slashCommandsLoadRef.current = null;
    replaceSlashCommands([]);
  }, [replaceSlashCommands]);

  // Null when get_commands failed, which an empty list (a Chat-only session's) must not be mistaken for.
  const requestSlashCommands = useCallback((): Promise<SlashCommandInfo[] | null> => {
    const generation = ++slashCommandsGenerationRef.current;
    const current = () => slashCommandsGenerationRef.current === generation;
    const load = (async () => {
      const sid = sessionIdRef.current ?? await ensureNewSession();
      if (!sid) {
        if (current()) replaceSlashCommands([]);
        return [] as SlashCommandInfo[];
      }
      setSlashCommandsLoading(true);
      try {
        const data = await sendAgentCommand<SlashCommandsResponse>(sid, { type: "get_commands" });
        const commands = data?.commands ?? [];
        if (current()) replaceSlashCommands(commands);
        return commands;
      } catch (e) {
        console.error("Failed to load slash commands:", e);
        if (current()) replaceSlashCommands([]);
        return null;
      } finally {
        // A newer request under way keeps the palette's "Loading" until it answers.
        if (current() || !slashCommandsLoadRef.current) setSlashCommandsLoading(false);
      }
    })();
    slashCommandsLoadRef.current = load;
    const settle = () => {
      if (slashCommandsLoadRef.current === load) slashCommandsLoadRef.current = null;
    };
    load.then(settle, settle);
    return load;
  }, [ensureNewSession, replaceSlashCommands]);

  const loadSlashCommands = useCallback(async () => (await requestSlashCommands()) ?? [], [requestSlashCommands]);

  // The list a bare /mcp is decided by: the request under way (the palette starts
  // one as "/mcp" is typed, often still unanswered at Enter), else the list already
  // loaded, else a new request. Null when it cannot be read: guessing would either
  // swallow another extension's /mcp or send one Settings should have taken.
  const slashCommandsForMcp = useCallback(async (): Promise<SlashCommandInfo[] | null> => {
    const pending = slashCommandsLoadRef.current;
    const known = slashCommandsRef.current;
    if (!pending && known.length > 0) return known;
    const loaded = await (pending ?? requestSlashCommands()).catch(() => null);
    return loaded ?? (known.length > 0 ? known : null);
  }, [requestSlashCommands]);

  const cancelEventStreamGrace = useCallback(() => {
    eventStreamGraceGenerationRef.current += 1;
    eventStreamGraceActiveRef.current = false;
    eventStreamGraceDueAtRef.current = null;
    if (eventStreamGraceTimerRef.current) {
      clearTimeout(eventStreamGraceTimerRef.current);
      eventStreamGraceTimerRef.current = null;
    }
  }, []);

  // `reason` only labels the close for the debug panel.
  const closeEvents = useCallback((reason?: DebugStreamCloseReason) => {
    eventConnectionRef.current?.close(reason);
  }, []);

  const ensureEventsConnected = useCallback((sid: string) => (
    eventConnectionRef.current!.ensureConnected(sid)
  ), []);

  const maintainEventsConnected = useCallback((sid: string) => {
    eventConnectionRef.current!.maintain(sid);
  }, []);

  // Keep the selected session's stream open even while its agent is idle. The
  // session itself stays alive on the page's lease renewals (below), which stop
  // if the browser disappears.
  useEffect(() => {
    const sid = session?.id;
    if (!sid) return;
    // React Strict Mode re-runs every effect after a simulated unmount, in
    // declaration order. The mount-only effect below flips this ref to false
    // in its cleanup and only restores it when it re-runs *after* this one,
    // so without re-asserting it here shouldMaintain() refuses the connection
    // and the selected session never opens its event stream.
    sessionHookMountedRef.current = true;
    eventStreamReleasedRef.current = false;
    maintainEventsConnected(sid);
    return () => {
      if (sessionIdRef.current === sid) eventConnectionRef.current?.close("deselected");
    };
  }, [maintainEventsConnected, session?.id]);

  // One lease request: it resets the session's idle countdown (renewIdleTimer())
  // and reports what is under way there (SessionLeaseReply). A reply that does
  // not come within SESSION_LEASE_TIMEOUT_MS, or no network, is "unanswered";
  // an error reply is null.
  const requestLease = useCallback((sid: string): Promise<SessionLeaseOutcome> => {
    const pending = leaseRequestRef.current;
    if (pending?.sid === sid) return pending.promise;
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), SESSION_LEASE_TIMEOUT_MS);
    const request: { sid: string; promise: Promise<SessionLeaseOutcome> } = { sid, promise: Promise.resolve(null) };
    request.promise = (async (): Promise<SessionLeaseOutcome> => {
      let httpStatus: number | null = null;
      let result: SessionLeaseOutcome = null;
      const startedAt = Date.now();
      leaseDebugRef.current.inFlightSince = startedAt;
      leaseDebugRef.current.requests += 1;
      try {
        const response = await fetch(sessionLeaseUrl(sid), { method: "POST", cache: "no-store", signal: controller.signal });
        httpStatus = response.status;
        result = response.ok ? await response.json() as SessionLeaseReply : null;
        return result;
      } catch (error) {
        // Timed out (queued behind open streams) or no network. A body that is no
        // JSON counts as an error reply.
        result = controller.signal.aborted || error instanceof TypeError ? "unanswered" : null;
        return result;
      } finally {
        clearTimeout(timeout);
        if (leaseRequestRef.current === request) leaseRequestRef.current = null;
        // The debug panel's record of it; whatever the reply holds, it cannot throw from here.
        try {
          const lease = leaseDebugRef.current;
          const at = Date.now();
          const outcome = result === "unanswered" ? "unanswered" : result === null ? "error" : "reply";
          if (outcome === "unanswered") lease.unanswered += 1;
          if (outcome === "error") lease.errors += 1;
          const reply = typeof result === "object" && result !== null ? result : null;
          lease.inFlightSince = null;
          lease.last = {
            at,
            durationMs: Math.max(0, at - startedAt),
            outcome,
            httpStatus,
            reply: reply && {
              running: reply.running === true,
              awaitingInput: reply.awaitingInput === true,
              subagentsActive: reply.subagentsActive === true,
            },
          };
        } catch {}
      }
    })();
    leaseRequestRef.current = request;
    return request.promise;
  }, []);

  // A page hidden for HIDDEN_PAGE_GRACE_MS lets go of the selected session's
  // stream while nothing needs it (#1283): each one holds a browser connection,
  // and over HTTP/1.1 a few background tabs exhaust the six per origin, so every
  // other request (Settings, message sync) queues in the browser until a tab
  // closes. A run, a `!` command, a compaction or an open extension dialog in the
  // page keeps it without asking. Otherwise the grace ends with a lease request,
  // and the stream closes only if its reply shows nothing under way
  // (canReleaseHiddenEventStream(): a run another tab started, an extension
  // dialog, a subagent whose end starts a turn here); the check repeats each
  // grace while hidden. A run that settles while hidden starts the grace again,
  // which also covers extension follow-ups right after prompt_done.
  const eventStreamIdle = !agentRunning && !bashRunning && !isCompacting
    && extensionDialogs.length === 0 && extensionCustomUis.length === 0;
  useEffect(() => {
    const sid = session?.id;
    if (!sid || !eventStreamIdle) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const releasable = () => (
      !disposed
      && document.visibilityState === "hidden"
      && !eventStreamReleasedRef.current
      && sessionIdRef.current === sid
      && sessionPropIdRef.current === sid
      && !agentRunningRef.current
      && !bashRunningRef.current
    );
    const check = async () => {
      timer = null;
      hiddenReleaseDueAtRef.current = null;
      if (!releasable()) return;
      const reply = await requestLease(sid);
      if (!releasable()) return;
      if (!canReleaseHiddenEventStream(reply)) {
        arm();
        return;
      }
      eventStreamReleasedRef.current = true;
      releaseDebugRef.current = { count: releaseDebugRef.current.count + 1, lastAt: Date.now() };
      closeEvents("released");
    };
    const arm = () => {
      if (timer !== null) return;
      timer = setTimeout(() => void check(), HIDDEN_PAGE_GRACE_MS);
      hiddenReleaseDueAtRef.current = Date.now() + HIDDEN_PAGE_GRACE_MS;
    };
    const onVisibilityChange = () => {
      if (document.visibilityState === "hidden") {
        arm();
      } else if (timer) {
        clearTimeout(timer);
        timer = null;
        hiddenReleaseDueAtRef.current = null;
      }
    };
    if (document.visibilityState === "hidden") arm();
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      disposed = true;
      if (timer) clearTimeout(timer);
      hiddenReleaseDueAtRef.current = null;
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [closeEvents, eventStreamIdle, eventStreamReattachCount, requestLease, session?.id]);

  const respondToExtensionUi = useCallback(async (
    request: ExtensionUiDialogRequest,
    response: { value: string } | { confirmed: boolean } | { cancelled: true },
  ) => {
    const sid = sessionIdRef.current;
    setExtensionDialogs((queue) => removeExtensionUiRequest(queue, request.id));
    if (!sid) return;
    try {
      await sendAgentCommand(sid, {
        type: "extension_ui_response",
        id: request.id,
        ...response,
      });
    } catch (e) {
      console.error("Failed to send extension UI response:", e);
    }
  }, []);

  const sendExtensionCustomInput = useCallback(async (request: ExtensionUiCustomRequest, data: string) => {
    const sid = sessionIdRef.current;
    if (!sid) return;
    try {
      await sendAgentCommand(sid, {
        type: "extension_ui_input",
        id: request.id,
        data,
      });
    } catch (e) {
      console.error("Failed to send extension custom UI input:", e);
    }
  }, []);

  const addNotice = useCallback((notice: { id?: string; message: string; type?: NoticeType }) => {
    const message = notice.message.trim();
    if (!message) return;
    dispatchNotice({
      type: "add",
      notice: {
        id: notice.id ?? createNoticeId(),
        message,
        type: notice.type ?? "info",
      },
    });
  }, []);
  const translateRef = useRef(opts.t);
  translateRef.current = opts.t;
  const translate = useCallback((key: string, params?: TranslationParams) => translateRef.current(key, params), []);
  // A steer or follow-up whose request failed without the server's refusal (the
  // network dropped, a proxy answered) may still have been accepted. Its text
  // goes back to the composer all the same (restoreSubmission merges it in front
  // of anything typed since) and is never sent again on its own; this notice
  // says the send is unconfirmed rather than failed.
  const addUnconfirmedSendNotice = useCallback(() => {
    addNotice({ type: "warning", message: translate("chat.sendUnconfirmed") });
  }, [addNotice, translate]);

  // A normal send keeps its bubble while unconfirmed (#1432). Once the server is
  // idle, a session read started after that settles it: the prompt arrived only
  // if the read shows it, one more user message with its text past the view's
  // last saved entry at the send than the view already had there. Otherwise
  // the text goes back, as not received when the read ends on that entry, else
  // as unconfirmed: a read that moved on (messages the view had not saved, or
  // more than its window), failed, or a command, whose prompt need not become
  // that user message. Any of the run's events clears the record first
  // (markPromptLanded()).
  // The text is back in the composer and the prompt is not in the transcript:
  // its bubble goes, from the view and from the session's cached view. A read of
  // a file the prompt never changed keeps the view's messages (the snapshot
  // revision is the same), so it would never drop the bubble itself.
  const dropUnconfirmedBubble = useCallback((pending: UnconfirmedSend) => {
    if (sessionHookMountedRef.current) setMessages((prev) => withoutMessage(prev, pending.bubble));
    const snapshot = getSessionViewSnapshot(pending.sid);
    if (snapshot?.messages.includes(pending.bubble)) {
      setSessionViewSnapshot({ ...snapshot, messages: withoutMessage(snapshot.messages, pending.bubble) });
    }
  }, []);
  const settleUnconfirmedSend = useCallback((sid: string, runId: number, readSeqBefore: number) => {
    const pending = unconfirmedSendRef.current;
    if (!pending || pending.runId !== runId || pending.sid !== sid) return;
    unconfirmedSendRef.current = null;
    const read = lastReadContextRef.current;
    const reread = read?.sid === sid && read.seq > readSeqBefore ? read : null;
    // Where the read goes past the send's last entry; -1 when that entry fell out of its window.
    const start = !reread ? -1
      : pending.lastEntryIdAtSend === null ? 0
        : reread.entryIds.indexOf(pending.lastEntryIdAtSend) + 1 || -1;
    if (reread && start >= 0) {
      const sameText = countUserMessages(reread.messages.slice(start), pending.key);
      if (sameText > pending.sameTextAfterSend) return;
    }
    restoreSubmission(pending.message, pending.images, pending.draftKey);
    // A failed read leaves the view stale: the bubble stays with "could not confirm".
    if (reread) dropUnconfirmedBubble(pending);
    addNotice(reread && !pending.command && start === reread.entryIds.length
      ? { type: "error", message: translate("chat.sendNotReceived") }
      : { type: "warning", message: translate("chat.sendUnconfirmed") });
  }, [addNotice, dropUnconfirmedBubble, restoreSubmission, translate]);
  // The hook unmounts (a session switch) with a send still unconfirmed: nothing
  // here will settle it, so its text goes back unless one of the run's events
  // cleared the record. With the stream closed, a prompt that did arrive is
  // given back too, without a notice. A fresh composer's goes to its cwd's
  // parked draft: its session has no row and no draft of its own until a
  // prompt reaches it (getRpcSessionInfos()). The cached view loses the bubble:
  // coming back, a read of an unchanged file keeps that view, and a changed
  // one replaces it with the prompt if it did arrive.
  const releaseUnconfirmedSend = useCallback(() => {
    const pending = unconfirmedSendRef.current;
    unconfirmedSendRef.current = null;
    if (!pending) return;
    if (pending.parkedDraftKey) {
      const images = pending.images?.map(({ data, mimeType }) => ({ data, mimeType }));
      restoreDraftSubmission(pending.parkedDraftKey, pending.message, images);
    } else {
      restoreSubmission(pending.message, pending.images, pending.draftKey);
    }
    dropUnconfirmedBubble(pending);
  }, [dropUnconfirmedBubble, restoreSubmission]);
  const releaseUnconfirmedSendRef = useRef(releaseUnconfirmedSend);
  releaseUnconfirmedSendRef.current = releaseUnconfirmedSend;

  // A prompt submission that found the session file ahead of the live
  // wrapper reloads the wrapper server-side; tell the user why the view
  // shifted under them without a remount (external writes by another pi
  // process, e.g. the TUI).
  const notifyPromptReloadedFromDisk = useCallback((result: unknown) => {
    if (
      result
      && typeof result === "object"
      && (result as { reloadedFromDisk?: boolean }).reloadedFromDisk === true
    ) {
      addNotice({
        type: "info",
        message: "This session was updated by another Pi process. Reloaded from disk.",
      });
    }
  }, [addNotice]);

  const handleExtensionUiRequest = useCallback((request: ExtensionUiRequest) => {
    if (isBlockingExtensionUiRequest(request)) onAttentionNeeded?.(request);

    switch (request.method) {
      case "select":
      case "confirm":
      case "input":
      case "editor":
        setExtensionDialogs((queue) => enqueueExtensionUiRequest(queue, request));
        break;
      case "notify": {
        addNotice({
          id: request.id,
          message: request.message,
          type: request.notifyType ?? "info",
        });
        break;
      }
      case "setStatus":
        extensionUiTouchRef.current.status.set(request.statusKey, ++extensionUiTouchRef.current.seq);
        setExtensionStatuses((prev) => {
          const rest = prev.filter((item) => item.key !== request.statusKey);
          return request.statusText !== undefined
            ? [...rest, { key: request.statusKey, text: request.statusText }]
            : rest;
        });
        break;
      case "setWidget":
        extensionUiTouchRef.current.widget.set(request.widgetKey, ++extensionUiTouchRef.current.seq);
        setExtensionWidgets((prev) => updateExtensionWidgets(
          prev,
          request.widgetKey,
          request.widgetLines,
          request.widgetPlacement,
        ));
        break;
      case "setTitle":
        if (request.title) document.title = request.title;
        break;
      case "set_editor_text":
        opts.chatInputRef?.current?.insertText(request.text);
        break;
      case "custom":
        setExtensionCustomUis((queue) => request.closed
          ? removeExtensionUiRequest(queue, request.id)
          : upsertExtensionUiRequest(queue, request));
        break;
    }
  }, [addNotice, onAttentionNeeded, opts.chatInputRef]);

  const settleUiStage = useCallback(() => {
    const wasRunning = agentRunningRef.current;
    agentRunningRef.current = false;
    // With the run over, a read's late-event tail can no longer arrive.
    lateSnapshotMessagesRef.current = null;
    setAgentRunning(false);
    setAgentPhase(null);
    setRetryInfo(null);
    setActiveToolResults(new Map());
    dispatch({ type: "end" });
    return wasRunning;
  }, [dispatch]);

  const notifyPromptStage = useCallback((runId: number, aborted = false, subagentsActive = false) => {
    if (notifiedPromptRunIdRef.current === runId) return false;
    notifiedPromptRunIdRef.current = runId;
    onAgentEnd?.({ aborted, ...(subagentsActive ? { subagentsActive } : {}) });
    return true;
  }, [onAgentEnd]);

  const scheduleEventStreamClose = useCallback((sid: string) => {
    if (sessionPropIdRef.current === sid) {
      cancelEventStreamGrace();
      return;
    }
    cancelEventStreamGrace();
    eventStreamGraceActiveRef.current = true;
    const generation = eventStreamGraceGenerationRef.current;

    const checkServerIdle = async () => {
      if (
        generation !== eventStreamGraceGenerationRef.current
        || sessionIdRef.current !== sid
        || !eventStreamGraceActiveRef.current
      ) return;

      try {
        const res = await fetch(agentStatusUrl(sid));
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json() as { running?: boolean; state?: AgentStateResponse };
        if (
          generation !== eventStreamGraceGenerationRef.current
          || sessionIdRef.current !== sid
          || !eventStreamGraceActiveRef.current
        ) return;

        const state = data.state;
        syncLiveModel(state);
        const promptActive = Boolean(data.running && state && (state.isStreaming || state.isPromptRunning));
        if (promptActive) {
          eventStreamGraceActiveRef.current = false;
          eventStreamGraceTimerRef.current = null;
          eventStreamGraceDueAtRef.current = null;
          sdkAgentActiveRef.current = Boolean(state?.isStreaming);
          rpcPromptPendingRef.current = Boolean(state?.isPromptRunning);
          agentRunningRef.current = true;
          setAgentRunning(true);
          setAgentPhase((prev) => runStatePhase(prev, Boolean(state?.isStreaming)));
          return;
        }

        if (data.running && state?.isCompacting) {
          setIsCompacting(true);
          eventStreamGraceTimerRef.current = setTimeout(() => void checkServerIdle(), PROMPT_SETTLE_POLL_MS);
          eventStreamGraceDueAtRef.current = Date.now() + PROMPT_SETTLE_POLL_MS;
          return;
        }

        eventStreamGraceActiveRef.current = false;
        eventStreamGraceTimerRef.current = null;
        eventStreamGraceDueAtRef.current = null;
        closeEvents("grace_end");
      } catch {
        // Keep the stream alive while state cannot be verified.
        if (
          generation !== eventStreamGraceGenerationRef.current
          || sessionIdRef.current !== sid
          || !eventStreamGraceActiveRef.current
        ) return;
        eventStreamGraceTimerRef.current = setTimeout(() => void checkServerIdle(), PROMPT_SETTLE_POLL_MS);
        eventStreamGraceDueAtRef.current = Date.now() + PROMPT_SETTLE_POLL_MS;
      }
    };

    eventStreamGraceTimerRef.current = setTimeout(() => void checkServerIdle(), EVENT_STREAM_IDLE_GRACE_MS);
    eventStreamGraceDueAtRef.current = Date.now() + EVENT_STREAM_IDLE_GRACE_MS;
  }, [cancelEventStreamGrace, closeEvents, syncLiveModel]);

  // `settledRead`: a manual fetch (#175) already read the session; its read
  // (started after `readSeqBefore`) settles the run, and no second read is made.
  const finishPromptWithoutStream = useCallback(async (sid: string | null = sessionIdRef.current, runId = promptRunIdRef.current, settledRead?: { readSeqBefore: number }) => {
    // Bail out before loadSession too: a stale finish for a previous run
    // must not overwrite the messages of the run currently streaming.
    if (promptRunIdRef.current !== runId) return;
    const readSeqBefore = settledRead?.readSeqBefore ?? sessionReadSeqRef.current;
    try {
      if (sid && !settledRead) await loadSession(sid);
    } finally {
      if (promptRunIdRef.current !== runId) return;
      // The server is idle: an unconfirmed send of this run either is in the
      // read just made or never arrived.
      if (sid) settleUnconfirmedSend(sid, runId, readSeqBefore);
      const promptWasPending = rpcPromptPendingRef.current;
      const agentWasActive = sdkAgentActiveRef.current;
      rpcPromptPendingRef.current = false;
      sdkAgentActiveRef.current = false;
      optimisticUserMessageKeyRef.current = null;
      const wasRunning = settleUiStage();
      if (promptWasPending) {
        notifyPromptStage(runId);
      } else if (agentWasActive && wasRunning) {
        onAgentEnd?.({ aborted: false });
      }
      if (sid) scheduleEventStreamClose(sid);
    }
  }, [loadSession, notifyPromptStage, onAgentEnd, scheduleEventStreamClose, settleUiStage, settleUnconfirmedSend]);

  const waitForPromptSettlement = useCallback(async (sid: string, runId?: number) => {
    await delay(PROMPT_SETTLE_INITIAL_DELAY_MS);
    const startedAt = Date.now();

    while (agentRunningRef.current && Date.now() - startedAt < PROMPT_SETTLE_MAX_MS) {
      if (runId !== undefined && promptRunIdRef.current !== runId) return;
      try {
        const res = await fetch(agentStatusUrl(sid));
        if (res.ok) {
          const data = await res.json() as { running?: boolean; state?: AgentStateResponse };
          const state = data.state;
          syncLiveModel(state);
          if (!data.running || !state || (!state.isStreaming && !state.isPromptRunning)) {
            await finishPromptWithoutStream(sid, runId);
            return;
          }
        }
      } catch {
        // SSE remains the primary completion path.
      }
      await delay(PROMPT_SETTLE_POLL_MS);
    }
  }, [finishPromptWithoutStream, syncLiveModel]);

  const waitForBashSettlement = useCallback(async (sid: string) => {
    const recoveryId = bashRecoveryIdRef.current + 1;
    bashRecoveryIdRef.current = recoveryId;

    while (
      bashRunningRef.current
      && bashRecoveryIdRef.current === recoveryId
      && sessionIdRef.current === sid
    ) {
      await delay(BASH_STATE_RECONCILE_MS);
      try {
        const res = await fetch(agentStatusUrl(sid));
        if (!res.ok) continue;
        const data = await res.json() as { state?: AgentStateResponse };
        syncLiveModel(data.state);
        if (data.state?.isBashRunning) continue;

        await loadSession(sid);
        if (bashRecoveryIdRef.current !== recoveryId || sessionIdRef.current !== sid) return;
        bashRunningRef.current = false;
        setBashRunning(false);
        setPendingBash(null);
        return;
      } catch {
        // Keep polling while the page is mounted; network recovery is transparent.
      }
    }
  }, [loadSession, syncLiveModel]);

  // Reconcile client streaming state with the server. When SSE events are
  // missed (network drop, mobile tab backgrounded, half-open connection),
  // agent_end never arrives and the UI stays in streaming state forever.
  // If the server reports idle while we still think it's running, finish
  // through the same settlement path used by non-streaming prompts.
  //
  // A manual fetch (#175) passes `manual`: the read goes under the fetch's abort
  // signal and, instead of settling, it resolves "idle" for a stale run; the
  // fetch settles it on its own forced read, made after this one.
  const reconcileAgentState = useCallback(async (sid: string, manual?: { signal: AbortSignal }): Promise<"idle" | void> => {
    if (!agentRunningRef.current || sessionIdRef.current !== sid) return;
    const runId = promptRunIdRef.current;
    const usageRequestId = ++contextUsageRequestIdRef.current;
    const touchSeqAtRead = extensionUiTouchRef.current.seq;
    try {
      const res = await fetch(agentStatusUrl(sid), { signal: manual?.signal });
      if (!res.ok) return;
      const data = await res.json() as { running?: boolean; state?: AgentStateResponse };
      // A slow response can straddle a run boundary (previous run finished
      // and the user already started the next one while this request was in
      // flight) — everything in it is stale, drop it.
      if (sessionIdRef.current !== sid || promptRunIdRef.current !== runId) return;
      const state = data.state;
      applyLiveUsage(state, sid, runId, usageRequestId);
      syncLiveModel(state);
      // Mirror compaction state unconditionally: a missed compaction_end
      // would otherwise leave the "Stop compaction" UI stuck. No state
      // (wrapper destroyed) means nothing is compacting.
      setIsCompacting(state?.isCompacting ?? false);
      setAutoCompactionEnabled(state?.autoCompactionEnabled ?? true);
      setQueuedMessages(normalizeQueuedMessages(state?.queuedMessages));
      const busy = data.running && state
        && (state.isStreaming || state.isPromptRunning || state.isCompacting);
      if (busy) {
        sdkAgentActiveRef.current = Boolean(state.isStreaming);
        rpcPromptPendingRef.current = Boolean(state.isPromptRunning);
        return;
      }
      if (!agentRunningRef.current) return;
      if (state) {
        if (state.extensionStatuses !== undefined) {
          setExtensionStatuses((prev) => mergeExtensionSnapshot(
            state.extensionStatuses ?? [],
            prev,
            touchedSince(extensionUiTouchRef.current.status, touchSeqAtRead),
          ));
        }
        applySnapshotWidgets(state, touchSeqAtRead);
      }
      if (manual) return "idle";
      if (state) {
        // The poll leaves the prompt out: read the one the run ended with once, as agent_end does.
        fetch(`/api/agent/${encodeURIComponent(sid)}`)
          .then((r) => r.ok ? r.json() : null)
          .then((d: { state?: AgentStateResponse } | null) => {
            if (!d || !sessionHookMountedRef.current || sessionIdRef.current !== sid) return;
            if (d.state?.systemPrompt !== undefined) setSystemPrompt(d.state.systemPrompt ?? null);
          })
          .catch(() => {});
      }
      await finishPromptWithoutStream(sid, runId);
    } catch {
      // Network still down — the next poll / visibility / online tick retries.
    }
  }, [applyLiveUsage, applySnapshotWidgets, finishPromptWithoutStream, syncLiveModel]);

  // The manual "fetch latest": at most one run-state read, one
  // forced session read and, only for a stream silent for more than a
  // heartbeat, one reconnect. The forced read bypasses refreshFromDisk's covered
  // branch, which a half-dead stream fakes. While the view shows a run, the
  // run-state read comes first (reconcileAgentState) and a run it finds idle
  // settles on the forced read that follows it, as the passive reconcile
  // settles on a read made after idle was seen: a run ending between the two
  // reads is then in the read, and an unconfirmed send is judged on it. Both
  // reads give up MANUAL_FETCH_TIMEOUT_MS after the trigger; the reconnect is
  // not awaited. The fetch ends in exactly one notice: updated (with the local
  // hh:mm), already current, or failed (the view and composer stay as they
  // were). A trigger until then joins it. Its triggers are ChatWindow's own:
  // the floating button's ↻ form at the list's tail and the touch pull-up
  // (the toolbar has no ↻), so each pane fetches for itself.
  const manualFetchAvailable = !isNew && Boolean(session?.id);
  const manualFetch = useCallback((): Promise<void> => {
    const sid = sessionIdRef.current;
    if (!manualFetchAvailable || !sid || !sessionHookMountedRef.current) return Promise.resolve();
    if (manualFetchFlightRef.current) return manualFetchFlightRef.current;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), MANUAL_FETCH_TIMEOUT_MS);
    const flight = (async () => {
      setManualFetching(true);
      let notice: "updated" | "current" | "failed" | null = "failed";
      try {
        // Silent since its last byte, or since it opened when none came (as the
        // stale watchdog counts); no open stream is the retry machinery's.
        const stream = eventConnectionRef.current?.snapshot();
        const lastHeard = stream?.lastSeenAt ?? stream?.openedAt ?? null;
        const reconnected = lastHeard !== null && Date.now() - lastHeard > AGENT_EVENT_HEARTBEAT_MS;
        if (reconnected) {
          closeEvents();
          maintainEventsConnected(sid);
        }
        const runId = promptRunIdRef.current;
        const status = agentRunningRef.current ? await reconcileAgentState(sid, { signal: controller.signal }) : undefined;
        const before = { messages: messagesRef.current, entryIds: entryIdsRef.current };
        const readSeqBefore = sessionReadSeqRef.current;
        const read: { data?: SessionData } = {};
        await loadSession(sid, false, false, {
          force: true,
          quiet: true,
          expectRunId: promptRunIdRef.current,
          preserveHistory: true,
          suppressReconnect: reconnected,
          signal: controller.signal,
          onLoaded: (data) => { read.data = data; },
        });
        if (!sessionHookMountedRef.current || sessionIdRef.current !== sid) notice = null;
        else if (read.data && !controller.signal.aborted) {
          const fresh = { messages: read.data.context.messages, entryIds: read.data.context.entryIds ?? [] };
          notice = hasFreshContent(before, fresh) ? "updated" : "current";
          if (status === "idle" && promptRunIdRef.current === runId && agentRunningRef.current) {
            // Settling drops the read's late tail, but a half-dead stream can
            // still hold this run's events, its agent_start included (the view
            // may know the run from a state read): keep the tail as a manual
            // idle read keeps it. A reconnected stream replays none of them.
            const late = reconnected ? null : lateSnapshotMessagesRef.current;
            await finishPromptWithoutStream(sid, runId, { readSeqBefore });
            if (late && late.sid === sid && late.runId === promptRunIdRef.current && !lateSnapshotMessagesRef.current) {
              lateSnapshotMessagesRef.current = { ...late, beforeRun: { openedAt: eventConnectionRef.current?.snapshot().openedAt ?? null }, recordedAt: Date.now() };
            }
          }
        }
      } finally {
        clearTimeout(timer);
        manualFetchFlightRef.current = null;
        if (sessionHookMountedRef.current) setManualFetching(false);
        if (notice && sessionHookMountedRef.current && sessionIdRef.current === sid) {
          addNotice(notice === "updated"
            ? { type: "info", message: translate("chat.manualFetchUpdated", { time: formatFetchTime(new Date()) }) }
            : notice === "current"
              ? { type: "info", message: translate("chat.manualFetchCurrent") }
              : { type: "error", message: translate("chat.manualFetchFailed") });
        }
      }
    })();
    manualFetchFlightRef.current = flight;
    return flight;
  }, [addNotice, closeEvents, finishPromptWithoutStream, loadSession, maintainEventsConnected, manualFetchAvailable, reconcileAgentState, translate]);

  // Recovery net for missed SSE events: while the agent is running, verify
  // against the server periodically and whenever the tab returns to the
  // foreground or the network comes back.
  useEffect(() => {
    if (!agentRunning) return;
    const reconcile = () => {
      // Read the ref on every tick: for brand-new sessions the id is
      // assigned only after ensure_session returns.
      const sid = sessionIdRef.current;
      if (sid) void reconcileAgentState(sid);
    };
    const onVisible = () => {
      if (document.visibilityState === "visible") reconcile();
    };
    const interval = setInterval(reconcile, AGENT_STATE_RECONCILE_MS);
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("online", reconcile);
    return () => {
      clearInterval(interval);
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("online", reconcile);
    };
  }, [agentRunning, reconcileAgentState]);

  useEffect(() => {
    agentRunningRef.current = agentRunning;
  }, [agentRunning]);

  const handleAgentEvent = useCallback((event: AgentEvent) => {
    // The stream said whether the session is busy (claude's controls wait for it).
    if (event.type === "connected") setStreamStateSessionId(sessionIdRef.current);
    switch (event.type) {
      case "connected": {
        dispatch({ type: "end" });
        // A new stream replays no completed message: a read's late-event tail
        // belongs to the previous connection. A manual fetch's tail stays for the
        // buffered `connected` of the stream that was open when it read (#175),
        // within its bound (MANUAL_FETCH_TAIL_MS).
        {
          const late = lateSnapshotMessagesRef.current;
          const openedAt = late?.beforeRun?.openedAt ?? null;
          if (openedAt === null || openedAt !== eventConnectionRef.current?.snapshot().openedAt || lateTailExpired(late)) {
            lateSnapshotMessagesRef.current = null;
          }
        }
        if (Array.isArray(event.pendingExtensionUiIds)) {
          // The server replays what it still holds right after this event.
          const pending = new Set(event.pendingExtensionUiIds as string[]);
          setExtensionDialogs((queue) => retainExtensionUiRequests(queue, pending));
          setExtensionCustomUis((queue) => retainExtensionUiRequests(queue, pending));
        }
        if (event.isStreaming === true) {
          cancelEventStreamGrace();
          sdkAgentActiveRef.current = true;
          agentRunningRef.current = true;
          externalWriteContentEventsRef.current = runContentEventsRef.current;
          setAgentRunning(true);
          // The server replays every tool call still running right after this
          // event, so start from none: a call that ended while the stream was
          // down must not stay listed.
          setAgentPhase({ kind: "waiting_model" });
        }
        // Opening the stream is what resumes an idle-reaped session, so the
        // mount's state read may have found no runtime and no usage to show (#1031).
        if (sessionIdRef.current) void refreshContextUsage(sessionIdRef.current);
        break;
      }
      case "agent_start": {
        markPromptLanded();
        cancelEventStreamGrace();
        // A new run discards a read's late tail, except the first start after a
        // manual fetch read the run while the view saw none (#175): that start
        // is the run's own, buffered in a half-dead stream ahead of its messages
        // — only within the tail's bound (MANUAL_FETCH_TAIL_MS).
        const late = lateSnapshotMessagesRef.current;
        lateSnapshotMessagesRef.current = late?.beforeRun && !lateTailExpired(late) ? { ...late, beforeRun: undefined } : null;
        sdkAgentActiveRef.current = true;
        agentRunningRef.current = true;
        externalWriteContentEventsRef.current = runContentEventsRef.current;
        setAgentRunning(true);
        setAgentPhase({ kind: "waiting_model" });
        // A retry's wait is over once its run starts, as pi's TUI shows it: the
        // successful auto_retry_end comes only with the retry's first complete
        // reply, which can stream for minutes.
        setRetryInfo(null);
        dispatch({ type: "start" });
        break;
      }
      case "background_task_terminal":
      case "background_tasks_update":
        onBackgroundTasksEvent?.(event as unknown as BackgroundTasksClientEvent);
        break;
      case "agent_end":
        // One logical prompt can emit multiple agent_end events before retrying,
        // compacting, or continuing messages queued by extension handlers.
        // Keep the stream open until prompt_done/agent_settled and the idle grace.
        if (!agentRunningRef.current) break;
        setAgentPhase(null);
        setRetryInfo(null);
        dispatch({ type: "end" });
        if (sessionIdRef.current) {
          const sid = sessionIdRef.current;
          const runId = promptRunIdRef.current;
          const usageRequestId = ++contextUsageRequestIdRef.current;
          loadSession(sid);
          const touchSeqAtRead = extensionUiTouchRef.current.seq;
          fetch(`/api/agent/${encodeURIComponent(sid)}`)
            .then((r) => r.ok ? r.json() : null)
            .then((d: { state?: AgentStateResponse } | null) => {
              if (!d || !sessionHookMountedRef.current || sessionIdRef.current !== sid || promptRunIdRef.current !== runId) return;
              syncLiveModel(d.state);
              applyLiveUsage(d.state, sid, runId, usageRequestId);
              if (d.state?.systemPrompt !== undefined) setSystemPrompt(d.state.systemPrompt ?? null);
              const statuses = d.state?.extensionStatuses;
              if (statuses !== undefined) {
                setExtensionStatuses((prev) => mergeExtensionSnapshot(
                  statuses,
                  prev,
                  touchedSince(extensionUiTouchRef.current.status, touchSeqAtRead),
                ));
              }
              applySnapshotWidgets(d.state, touchSeqAtRead);
              // Aborted turns can leave messages queued in pi (delivered with the
              // next turn); dead wrapper (no state) means the queue is gone.
              setQueuedMessages(normalizeQueuedMessages(d.state?.queuedMessages));
            })
            .catch(() => {});
        }
        break;
      case "agent_settled": {
        // The turn ended: a force stop asked for in it no longer stands.
        setClaudeForceStop(null);
        const agentWasActive = sdkAgentActiveRef.current;
        sdkAgentActiveRef.current = false;
        if (!agentWasActive || rpcPromptPendingRef.current) break;

        const sid = sessionIdRef.current;
        const wasRunning = settleUiStage();
        setIsCompacting(false);
        if (sid) {
          void loadSession(sid);
          scheduleEventStreamClose(sid);
        }
        if (wasRunning) {
          onAgentEnd?.({ aborted: event.aborted === true, ...(event.subagentsActive === true ? { subagentsActive: true } : {}) });
        }
        break;
      }
      case "prompt_done":
        {
          setClaudeForceStop(null);
          markPromptLanded();
          const runId = promptRunIdRef.current;
          const promptWasPending = rpcPromptPendingRef.current;
          rpcPromptPendingRef.current = false;
          optimisticUserMessageKeyRef.current = null;
          const firstNotification = notifyPromptStage(runId, event.aborted === true, event.subagentsActive === true);
          if (!promptWasPending && !firstNotification) break;

          const sid = sessionIdRef.current;
          if (sid) void loadSession(sid);
          // An extension-injected agent may already have started before the
          // command's prompt_done. Keep that active stage visible and let its
          // agent_settled event perform the next completion transition.
          if (!sdkAgentActiveRef.current) {
            settleUiStage();
            if (sid) scheduleEventStreamClose(sid);
          }
        }
        break;
      case "prompt_error":
        addNotice({ type: "error", message: (event.errorMessage as string | undefined) ?? "Command failed" });
        break;
      case "extension_error":
        addNotice({
          type: "error",
          message: (event.error as string | undefined) ?? "Extension command failed",
        });
        break;
      case "message_start":
      case "message_update": {
        // Ignore streaming events arriving after this run already finished
        // (e.g. SSE data buffered while the tab was frozen, flushed after
        // reconcile) — they would resurrect a ghost streaming bubble.
        if (!agentRunningRef.current) break;
        // Transcript system messages (prompt and tool loadout) are filtered
        // server-side; keep them out of the chat should one arrive.
        if (isSystemMessageEvent(event)) break;
        if (event.type === "message_start") {
          const msg = event.message as AgentMessage | undefined;
          if (msg?.role === "user") break;
          if (msg?.role === "assistant") {
            dispatch({ type: "snapshot", message: msg });
            if (msg.content.length > 0) setAgentPhase(null);
          } else if (msg) {
            setAgentPhase(null);
          }
        } else {
          const delta = event.assistantMessageEvent as ClientAssistantMessageEvent | undefined;
          if (delta) {
            dispatch({ type: "delta", event: delta });
            if (delta.type !== "toolcall_start" && delta.type !== "toolcall_delta") {
              setAgentPhase(null);
            }
          }
        }
        // Live-follow the streaming output only when the user is already near
        // the bottom of the message list. If they scrolled up, leave them there.
        if (!pendingScrollToUserRef.current && isNearBottomRef.current && liveFollowFrameRef.current === null) {
          // Defer the scroll so React has time to update the DOM with the new
          // streaming content; otherwise scrollIntoView may target stale layout.
          liveFollowFrameRef.current = requestAnimationFrame(() => {
            liveFollowFrameRef.current = null;
            if (isNearBottomRef.current) scrollToBottom("auto");
          });
        }
        break;
      }
      case "message_end": {
        // Same late-event guard: after reconcile finished this run,
        // loadSession already loaded this message from the session file —
        // appending it again would duplicate it.
        if (!agentRunningRef.current) break;
        if (isSystemMessageEvent(event)) break;
        const completed = event.message as AgentMessage | undefined;
        if (completed) runContentEventsRef.current += 1;
        if (completed && completed.role === "user") {
          // Delivered steering/follow-up messages surface here as user
          // messages. The run's initial prompt also emits one, but handleSend
          // already appended it optimistically. Consume only the still-adjacent
          // optimistic bubble; later same-text queue deliveries must render.
          const delivered = normalizeToolCalls(completed);
          const deliveredKey = userMessageKey(delivered);
          const optimisticKey = optimisticUserMessageKeyRef.current;
          optimisticUserMessageKeyRef.current = null;
          // While the run's own bubble waits, a delivered user message is its prompt.
          if (optimisticKey) markPromptLanded();
          // A mid-run read already loaded the prompt with no bubble left to
          // consume: the buffered event must not append it after the reply.
          const alreadyLoaded = takeLateSnapshotMessage(lateSnapshotMessagesRef, sessionIdRef.current, promptRunIdRef.current, completed);
          if (!alreadyLoaded) {
            setMessages((prev) => {
              const last = prev[prev.length - 1];
              if (optimisticKey && last?.role === "user" && userMessageKey(last) === optimisticKey) {
                return optimisticKey === deliveredKey
                  ? prev
                  : [...prev.slice(0, -1), delivered];
              }
              return [...prev, delivered];
            });
          }
        } else if (completed) {
          // A mid-run read already showed this completed message: a buffered
          // message_end for it must not append it again.
          if (!takeLateSnapshotMessage(lateSnapshotMessagesRef, sessionIdRef.current, promptRunIdRef.current, completed)) {
            // pi's showCacheMissNotices (#1265): the wrapper sends them beside the message.
            const notices = Array.isArray(event.transcriptNotices) ? event.transcriptNotices as TranscriptNotice[] : undefined;
            setMessages((prev) => [...prev, withTranscriptNotices(normalizeToolCalls(completed), notices)]);
            if (completed.role === "assistant") {
              const sid = sessionIdRef.current;
              if (sid) void refreshContextUsage(sid);
            }
          }
        }
        dispatch({ type: "end" });
        setAgentPhase({ kind: "waiting_model" });
        break;
      }
      case "tool_execution_start": {
        // A call a tool made itself (a codemode script's) belongs to its
        // parent's card; listed here it would show as a top-level running tool,
        // and a call cut off by its script can end after the parent did.
        if (isNestedToolExecutionEvent(event)) break;
        const id = event.toolCallId as string;
        const name = event.toolName as string;
        setAgentPhase((prev) => {
          const tools = prev?.kind === "running_tools" ? [...prev.tools] : [];
          if (!tools.some((t) => t.id === id)) tools.push({ id, name });
          return { kind: "running_tools", tools };
        });
        break;
      }
      case "tool_execution_update": {
        if (isNestedToolExecutionEvent(event)) break;
        const id = event.toolCallId as string;
        const name = event.toolName as string;
        const partialResult = event.partialResult as Partial<ToolResultMessage> | undefined;
        const content = partialResult?.content;
        // Live output for shells; for codemode, the calls its script has made so far.
        if ((name === "bash" || name === "powershell" || name === CODEMODE_TOOL_NAME) && Array.isArray(content)) {
          setActiveToolResults((prev) => {
            const next = new Map(prev);
            next.set(id, {
              role: "toolResult",
              toolCallId: id,
              toolName: name,
              content,
              isError: partialResult?.isError,
              details: partialResult?.details,
            });
            return next;
          });
        }
        const progress = name === CODEMODE_TOOL_NAME
          ? getCodemodeProgress(event.partialResult)
          : getToolExecutionProgress(event.partialResult);
        setAgentPhase((prev) => {
          const tools = prev?.kind === "running_tools" ? [...prev.tools] : [];
          const existing = tools.find((tool) => tool.id === id);
          const updated = {
            id,
            name: name || existing?.name || "tool",
            progress: progress ?? existing?.progress,
          };
          return {
            kind: "running_tools",
            tools: [...tools.filter((tool) => tool.id !== id), updated],
          };
        });
        break;
      }
      case "tool_execution_end": {
        if (isNestedToolExecutionEvent(event)) break;
        const id = event.toolCallId as string;
        onToolEnd?.(event.toolName as string);
        setActiveToolResults((prev) => {
          if (!prev.has(id)) return prev;
          const next = new Map(prev);
          next.delete(id);
          return next;
        });
        setAgentPhase((prev) => {
          if (prev?.kind !== "running_tools") return prev;
          const tools = prev.tools.filter((t) => t.id !== id);
          if (tools.length === 0) return { kind: "waiting_model" };
          return { kind: "running_tools", tools };
        });
        break;
      }
      case "claude_subagent": {
        // A claude subagent run's header and new entries: never a main message.
        const sid = sessionIdRef.current;
        const run = event.run as ClaudeSubagentRun | undefined;
        if (sid && run && typeof run.toolCallId === "string") setClaudeSubagentState((state) => foldClaudeSubagents(state, sid, [run], "live"));
        break;
      }
      case CLAUDE_BACKGROUND_TASK_EVENT: {
        // A claude background task's snapshot: the running list and its panel.
        const sid = sessionIdRef.current;
        const task = event.task as ClaudeBackgroundTask | undefined;
        if (sid && task && typeof task.taskId === "string") setClaudeTaskState((state) => foldClaudeBackgroundTask(state, sid, task));
        break;
      }
      case CLAUDE_BACKGROUND_TASKS_EVENT: {
        // The server's whole list of running tasks a new stream starts with: it replaces the page's.
        const sid = sessionIdRef.current;
        const tasks = Array.isArray(event.tasks)
          ? (event.tasks as ClaudeBackgroundTask[]).filter((task) => task && typeof task.taskId === "string")
          : null;
        if (sid && tasks) setClaudeTaskState((state) => replaceClaudeTaskState(state, sid, tasks));
        break;
      }
      case CLAUDE_FORCE_STOP_EVENT: {
        // A Stop claude did not honour by its deadline while background work runs.
        const sid = sessionIdRef.current;
        const work = Array.isArray(event.work) ? event.work as ClaudeBackgroundWork[] : [];
        if (sid) setClaudeForceStop({ sessionId: sid, work });
        break;
      }
      case "queue_update":
        markPromptLanded();
        setQueuedMessages({
          steering: [...((event.steering as string[] | undefined) ?? [])],
          followUp: [...((event.followUp as string[] | undefined) ?? [])],
        });
        break;
      case "auto_retry_start":
        setRetryInfo({ attempt: event.attempt as number, maxAttempts: event.maxAttempts as number, errorMessage: event.errorMessage as string | undefined });
        break;
      case "auto_retry_end":
        setRetryInfo(null);
        break;
      case "auto_compaction_start":
      case "compaction_start":
        setIsCompacting(true);
        setCompactError(null);
        setCompactResult(null);
        break;
      case "auto_compaction_end":
      case "compaction_end":
        // claude's own /compact turn ends here: a force stop asked for in it no longer stands.
        // An automatic compaction inside a running turn ends no turn.
        if (event.type === "compaction_end") setClaudeForceStop(null);
        setIsCompacting(false);
        if (event.errorMessage) {
          setCompactError(event.errorMessage as string);
          setCompactResult(null);
        } else if (!event.aborted) {
          setCompactResult(readCompactResult(event.result, (event.reason as string | undefined) ?? "auto"));
          const sid = sessionIdRef.current;
          if (sid) {
            loadSession(sid);
            // The compaction changed the context the header's meter shows (pi: unknown
            // until the next reply; claude: the size it reported it left), and no reply
            // follows a manual one to read it: read it now.
            void refreshContextUsage(sid);
          }
        }
        break;
      case "extension_ui_request":
        handleExtensionUiRequest(event as ExtensionUiRequest);
        break;
      case "extension_ui_closed":
        setExtensionDialogs((queue) => removeExtensionUiRequest(queue, event.id as string));
        break;
    }
  }, [addNotice, applyLiveUsage, applySnapshotWidgets, cancelEventStreamGrace, dispatch, handleExtensionUiRequest, loadSession, markPromptLanded, notifyPromptStage, onAgentEnd, onToolEnd, onBackgroundTasksEvent, refreshContextUsage, scheduleEventStreamClose, scrollToBottom, settleUiStage, syncLiveModel]);
  handleAgentEventRef.current = handleAgentEvent;

  /**
   * The prompt path behind handleSend. `relay` is the subagent panel's
   * relay message: it is no composer submission, so a staged edit stays staged and is not
   * sent with it, nothing is put back into the composer's draft, and a failure rejects
   * (after the usual notice) so the panel keeps its own text.
   */
  const sendPrompt = useCallback(async (message: string, images?: AttachedImage[], relay = false) => {
    const trimmedMessage = message.trim();
    if (!trimmedMessage && !images?.length) return;
    if (agentRunningRef.current || bashRunningRef.current) {
      if (relay) throw new Error(translate("claudeSubagent.relayBusy"));
      restoreSubmission(message, images, composerDraftKey);
      return;
    }
    // A claude edit is one command: the prompt names the message it
    // replaces, and the server resumes the session truncated before it. A `!` message is a
    // prompt like any other in a claude session, so it carries the edit too.
    let claudeEditEntryId: string | null = null;
    if (relay) {
      // A relay leaves a staged edit staged.
    } else if (editEntryId && isClaudeSession) {
      claudeEditEntryId = editEntryId;
      setEdit(null);
      // As pi's navigate re-reads the branch: the view ends before the edited message.
      const cut = entryIdsRef.current.indexOf(editEntryId);
      if (cut >= 0) {
        messagesRef.current = messagesRef.current.slice(0, cut);
        entryIdsRef.current = entryIdsRef.current.slice(0, cut);
        setMessages(messagesRef.current);
        setEntryIds(entryIdsRef.current);
      }
    } else if (editEntryId) {
      // Navigate and prompt are two RPCs: a prompt rejected after navigating
      // keeps the new leaf until the server can apply both atomically.
      const entryId = editEntryId;
      setEdit(null);
      if (!(await handleNavigateRef.current?.(entryId))) {
        setEdit(entryId);
        restoreSubmission(message, images, composerDraftKey);
        return;
      }
    }
    const isSlashCommandPrompt = !images?.length && trimmedMessage.startsWith("/");

    // A claude session has no shell: its `!` text is sent as a prompt.
    const isBashCommand = !images?.length && !isClaudeSession && trimmedMessage.startsWith("!");
    if (isBashCommand) {
      const isExcluded = trimmedMessage.startsWith("!!");
      const bashCmd = (isExcluded ? trimmedMessage.slice(2) : trimmedMessage.slice(1)).trim();
      if (!bashCmd) {
        restoreSubmission(message, images, composerDraftKey);
        return;
      }
      await executeBashRef.current?.(bashCmd, isExcluded);
      return;
    }

    const promptRunId = promptRunIdRef.current + 1;
    externalWriteContentEventsRef.current = runContentEventsRef.current;
    cancelEventStreamGrace();
    rpcPromptPendingRef.current = true;

    const imageBlocks = images?.map((img) => ({ type: "image" as const, source: { type: "base64" as const, media_type: img.mimeType, data: img.data } }));
    const userMsg: AgentMessage = {
      role: "user",
      content: imageBlocks?.length
        ? [...(message.trim() ? [{ type: "text" as const, text: message }] : []), ...imageBlocks]
        : message,
      timestamp: Date.now(),
    };
    // What the view holds before the bubble: its last saved entry, and the same
    // text already sent since (the run messages the stream added), for #1432.
    const userKey = userMessageKey(userMsg);
    const savedViewIds = entryIdsRef.current;
    const lastEntryIdAtSend = savedViewIds.at(-1) ?? null;
    const sameTextAfterSend = countUserMessages(messagesRef.current.slice(savedViewIds.length), userKey);
    setMessages((prev) => [...prev, userMsg]);
    optimisticUserMessageKeyRef.current = userKey;
    promptRunIdRef.current = promptRunId;
    agentRunningRef.current = true;
    setAgentRunning(true);
    setAgentPhase(isSlashCommandPrompt ? { kind: "running_command" } : { kind: "waiting_model" });
    dispatch({ type: "start" });
    pendingScrollToUserRef.current = true;
    setPromptAnchorActive(true);

    // A queued prompt (parked behind compaction, or queued into a run) is not a
    // sent message yet: the queue rows show it, and the transcript shows it when
    // the flush delivers it. Drop the optimistic bubble for it.
    const dropOptimisticUserMessage = () => {
      setMessages((prev) => {
        const index = prev.lastIndexOf(userMsg);
        return index === -1 ? prev : [...prev.slice(0, index), ...prev.slice(index + 1)];
      });
      optimisticUserMessageKeyRef.current = null;
    };
    const piImages = images?.map((img) => ({ type: "image" as const, data: img.data, mimeType: img.mimeType }));
    let sentSessionId: string | null = null;
    let promptRequestStarted = false;

    try {
      if (isNew && newSessionCwd) {
        const selectedModel = newSessionModel;
        const existingSid = sessionIdRef.current ?? await ensuringNewSessionRef.current;
        const sid = existingSid ?? await ensureNewSession();

        if (!sid) throw new Error("Unable to create a session for the prompt");
        sentSessionId = sid;
        // A claude session's model is its fixed full name: no pi model is set on it.
        if (selectedModel && newSessionHarness !== CLAUDE_HARNESS) {
          setPendingModel(selectedModel);
          if (existingSid) {
            await sendAgentCommand(sid, { type: "set_model", provider: selectedModel.provider, modelId: selectedModel.modelId });
          }
        }
        // Switching sessions unmounts this composer and closes its stream. The
        // prompt still goes out, so the new session runs and gets its sidebar
        // row instead of losing the submitted text (#1146).
        if (sessionHookMountedRef.current) {
          await ensureEventsConnected(sid).catch((error) => {
            if (sessionHookMountedRef.current) throw error;
          });
        }
        promptRequestStarted = true;
        const queuedAck = await sendAgentCommand<{ queued?: boolean }>(sid, {
          type: "prompt",
          message,
          ...(piImages?.length ? { images: piImages } : {}),
        });
        notifyPromptReloadedFromDisk(queuedAck);
        if (queuedAck?.queued) dropOptimisticUserMessage();
        // A claude image-only send is listed as [Image] before the list re-read.
        promoteNewSession(1, newSessionHarness === CLAUDE_HARNESS ? claudeFirstMessage(message, piImages?.length ?? 0) : message);
      } else if (session) {
        sentSessionId = session.id;
        await ensureEventsConnected(session.id);
        promptRequestStarted = true;
        const queuedAck = await sendAgentCommand<{ queued?: boolean }>(session.id, {
          type: "prompt",
          message,
          ...(piImages?.length ? { images: piImages } : {}),
          ...(claudeEditEntryId ? { entryId: claudeEditEntryId } : {}),
        });
        notifyPromptReloadedFromDisk(queuedAck);
        if (queuedAck?.queued) dropOptimisticUserMessage();
      } else {
        throw new Error("No active session for the prompt");
      }
      setPromptMissingCwd(null);
      if (isSlashCommandPrompt && sentSessionId) {
        void waitForPromptSettlement(sentSessionId, promptRunId);
      }
    } catch (e) {
      console.error("Failed to send message:", e);
      const definitivelyRejected = !promptRequestStarted || isPromptRejectedError(e);
      // A transport/proxy failure after dispatch is ambiguous: the server may
      // have accepted the prompt before the response was lost. Keep SSE alive
      // and the bubble until server state confirms the run is idle; the text
      // goes back only if the server never got it (settleUnconfirmedSend).
      // Nothing is sent again (#1432).
      if (!definitivelyRejected && sentSessionId && relay) {
        // The server may hold the relay: the bubble waits for the settled state, and the
        // panel keeps its text under the unconfirmed notice; the composer gets nothing.
        addUnconfirmedSendNotice();
        void waitForPromptSettlement(sentSessionId, promptRunId);
        throw e;
      }
      if (!definitivelyRejected && sentSessionId) {
        if (landedPromptRunIdRef.current !== promptRunId) {
          unconfirmedSendRef.current = {
            runId: promptRunId,
            sid: sentSessionId,
            message,
            images,
            draftKey: composerDraftKey,
            parkedDraftKey: isNew && newSessionCwd ? parkedNewSessionDraftKey(newSessionCwd) : null,
            lastEntryIdAtSend,
            sameTextAfterSend,
            bubble: userMsg,
            key: userKey,
            command: isSlashCommandPrompt,
          };
          if (!sessionHookMountedRef.current) releaseUnconfirmedSend();
        }
        void waitForPromptSettlement(sentSessionId, promptRunId);
        return;
      }
      rpcPromptPendingRef.current = false;
      setMessages((prev) => withoutMessage(prev, userMsg));
      // A folder that no longer exists never reached the model: the composer's
      // banner names it (translated) instead of the server's English notice.
      if (e instanceof AgentCommandError && e.missingCwd) setPromptMissingCwd(e.missingCwd);
      else addNotice({ type: "error", message: e instanceof Error ? e.message : String(e) });
      if (claudeEditEntryId && sentSessionId) {
        // A refused edit changed nothing: the edit is pending again over the whole history,
        // unless the message left it (another page edited before it): then there is none.
        const entryId = claudeEditEntryId;
        const sid = sentSessionId;
        setEdit(entryId);
        void loadSession(sid, false, false, {
          force: true,
          onLoaded: (fresh) => {
            if (pendingHistoryEdits.get(sid) === entryId && !(fresh.context.entryIds ?? []).includes(entryId)) setEdit(null);
          },
        });
      }
      if (!relay) restoreSubmission(message, images, composerDraftKey);
      optimisticUserMessageKeyRef.current = null;
      // Rejection only describes this submission. Another tab or an event we
      // missed may still have a real run active for the same session, so keep
      // its SSE connection until server state says the wrapper is idle.
      if (sentSessionId) {
        void reconcileAgentState(sentSessionId);
        if (relay) throw e;
        return;
      }
      agentRunningRef.current = false;
      closeEvents("send_failed");
      setAgentRunning(false);
      setAgentPhase(null);
      dispatch({ type: "end" });
      if (relay) throw e;
    }
  }, [isNew, newSessionCwd, newSessionModel, newSessionHarness, session, ensureNewSession, ensureEventsConnected, promoteNewSession, waitForPromptSettlement, addNotice, addUnconfirmedSendNotice, notifyPromptReloadedFromDisk, releaseUnconfirmedSend, cancelEventStreamGrace, closeEvents, composerDraftKey, dispatch, reconcileAgentState, restoreSubmission, editEntryId, setEdit, isClaudeSession, loadSession, translate]);

  const handleSend = useCallback((message: string, images?: AttachedImage[]) => sendPrompt(message, images), [sendPrompt]);

  const executeBash = useCallback(async (command: string, excludeFromContext: boolean) => {
    if (agentRunningRef.current || bashRunningRef.current) return;
    const inputText = `${excludeFromContext ? "!!" : "!"}${command}`;
    bashRunningRef.current = true;
    setPendingBash({ command, excludeFromContext });
    setBashRunning(true);
    try {
      const sid = sessionIdRef.current ?? session?.id ?? await ensureNewSession();
      if (!sid) throw new Error("Unable to create a session for the shell command");
      await sendAgentCommand(sid, {
        type: "bash",
        command,
        excludeFromContext,
      });
      await loadSession(sid);
      promoteNewSession(1, inputText);
    } catch (e) {
      console.error("Failed to execute shell command:", e);
      addNotice({ type: "error", message: e instanceof Error ? e.message : String(e) });
      restoreSubmission(inputText, undefined, composerDraftKey);
    } finally {
      bashRunningRef.current = false;
      setPendingBash(null);
      setBashRunning(false);
    }
  }, [addNotice, composerDraftKey, ensureNewSession, loadSession, promoteNewSession, restoreSubmission, session]);
  executeBashRef.current = executeBash;

  /** Recall's and Stop's queued texts, and the images of prompts parked behind a compaction, back in the composer. */
  const restoreRecalledPrompts = useCallback((recalled: RecalledPrompts | null | undefined) => {
    const text = [...(recalled?.steering ?? []), ...(recalled?.followUp ?? [])].join("\n\n");
    if (recalled?.images?.length) restoreSubmission(text, recalled.images, composerDraftKey);
    else if (text) opts.chatInputRef?.current?.prependText(text);
  }, [composerDraftKey, opts.chatInputRef, restoreSubmission]);

  const handleAbort = useCallback(async () => {
    const sid = sessionIdRef.current;
    if (!sid) return;
    if (bashRunningRef.current) {
      try {
        await sendAgentCommand(sid, { type: "abort_bash" });
      } catch (e) {
        console.error("Failed to abort bash:", e);
      }
      return;
    }
    try {
      // Prompts parked behind a compaction come back for the composer, as Recall's do.
      restoreRecalledPrompts(await sendAgentCommand<RecalledPrompts | null>(sid, { type: "abort" }));
    } catch (e) {
      console.error("Failed to abort:", e);
    }
  }, [restoreRecalledPrompts]);

  /**
   * The force-stop notice's two answers: Force stop signals claude's
   * process group (`abort { force: true }`), ending the background work it listed; Keep
   * waiting only dismisses the notice (a later Stop asks again at its deadline).
   */
  const forceStopClaudeTurn = useCallback(async () => {
    const sid = sessionIdRef.current;
    setClaudeForceStop(null);
    if (!sid) return;
    try {
      await sendAgentCommand(sid, { type: "abort", force: true });
    } catch (e) {
      addNotice({ type: "error", message: e instanceof Error ? e.message : String(e) });
    }
  }, [addNotice]);
  const dismissClaudeForceStop = useCallback(() => setClaudeForceStop(null), []);

  /** Stops one claude background task (claude's `stop_task`); rejects with the server's error. */
  const stopClaudeBackgroundTask = useCallback(async (taskId: string) => {
    const sid = sessionIdRef.current;
    if (!sid) throw new Error("No active session");
    await sendAgentCommand(sid, { type: "stop_task", taskId });
  }, []);

  /** Reads one claude background task's recent output (claude's `get_task_output`); rejects with the server's error. */
  const readClaudeTaskOutput = useCallback(async (taskId: string): Promise<ClaudeTaskOutput> => {
    const sid = sessionIdRef.current;
    if (!sid) throw new Error("No active session");
    const answer = await sendAgentCommand<Partial<ClaudeTaskOutput> | null>(sid, { type: "task_output", taskId });
    return {
      output: typeof answer?.output === "string" ? answer.output : "",
      totalBytes: typeof answer?.totalBytes === "number" ? answer.totalBytes : 0,
      truncated: answer?.truncated === true,
    };
  }, []);

  const handleFork = useCallback(async (entryId: string) => {
    if (bashRunningRef.current) return;
    const sid = sessionIdRef.current;
    if (!sid) return;
    setForkingEntryId(entryId);
    try {
      const result = await sendAgentCommand<{ cancelled?: boolean; newSessionId?: string }>(sid, {
        type: "fork",
        entryId,
      });
      const { cancelled, newSessionId } = result ?? {};
      if (!cancelled && newSessionId) {
        onSessionForked?.(newSessionId);
      }
    } catch (e) {
      console.error("Fork failed:", e);
      addNotice({ type: "error", message: e instanceof Error ? e.message : String(e) });
    } finally {
      setForkingEntryId(null);
    }
  }, [addNotice, onSessionForked]);

  const handleNavigate = useCallback(async (entryId: string): Promise<boolean> => {
    if (bashRunningRef.current) return false;
    const sid = sessionIdRef.current;
    if (!sid) return false;
    try {
      const result = await sendAgentCommand<{ cancelled?: boolean }>(sid, {
        type: "navigate_tree",
        targetId: entryId,
      });
      if (result?.cancelled || sessionIdRef.current !== sid) return false;
      await loadSession(sid);
      return sessionIdRef.current === sid;
    } catch (e) {
      console.error("Failed to navigate:", e);
      return false;
    }
  }, [loadSession]);
  handleNavigateRef.current = handleNavigate;

  const handleLeafChange = useCallback(async (leafId: string | null) => {
    // pi refuses navigate_tree mid-run: it moves the one leaf the running agent
    // appends to. Switching only the view would render the live run under
    // another branch, so the switch waits for the run like the server does.
    if (bashRunningRef.current || agentRunningRef.current || isCompacting) return;
    setActiveLeafId(leafId);
    const sid = sessionIdRef.current;
    if (!sid) return;
    await loadContext(sid, leafId);
    if (leafId) {
      sendAgentCommand(sid, { type: "navigate_tree", targetId: leafId }).catch(() => {});
    }
  }, [isCompacting, loadContext]);

  // a fresh composer's harness switch resets the destination harness's model —
  // claude to its default full name, pi to no override (pi's default model again).
  const chooseNewSessionHarness = useCallback((harness: SessionHarness) => {
    if (harness === newSessionHarness) return;
    setNewSessionHarness(harness);
    if (harness === CLAUDE_HARNESS) {
      setNewSessionClaudeModel(DEFAULT_CLAUDE_MODEL);
      setNewSessionClaudeEffort(CLAUDE_EFFORT_DEFAULT);
      return;
    }
    newSessionModelOverrideRef.current = null;
    setNewSessionModel(null);
    if (thinkingLevelOverrideRef.current === null) {
      const pinned = newSessionDefaultModel && thinkingLevelPinsRef.current[`${newSessionDefaultModel.provider}/${newSessionDefaultModel.modelId}`];
      setNewSessionDefaultThinkingLevel(asConcreteThinkingLevel(pinned) ?? defaultThinkingLevelRef.current);
    }
  }, [newSessionHarness, newSessionDefaultModel]);

  const handleModelChange = useCallback(async (provider: string, modelId: string) => {
    if (isNew) {
      const selectedModel = { provider, modelId };
      newSessionModelOverrideRef.current = selectedModel;
      setNewSessionModel(selectedModel);
      setPendingModel(selectedModel);
      if (thinkingLevelOverrideRef.current === null) {
        const pinned = thinkingLevelPinsRef.current[`${provider}/${modelId}`];
        setNewSessionDefaultThinkingLevel(
          asConcreteThinkingLevel(pinned) ?? defaultThinkingLevelRef.current,
        );
      }
      const sid = sessionIdRef.current ?? await ensuringNewSessionRef.current;
      if (!sid) return;
      try {
        await sendAgentCommand(sid, { type: "set_model", provider, modelId });
      } catch (e) {
        console.error("Failed to set model:", e);
      }
      return;
    }
    const sid = sessionIdRef.current;
    if (!sid || modelSwitchPendingRef.current) return;
    const target = { provider, modelId };
    const previousOverride = currentModelOverride;
    modelSwitchPendingRef.current = true;
    setCurrentModelOverride(target);
    setModelSwitching(true);
    try {
      const selected = await sendAgentCommand<{ provider: string; id: string }>(sid, { type: "set_model", provider, modelId });
      setLiveModel({ provider: selected.provider, modelId: selected.id });
      // Pi persists model_change synchronously. Reload the canonical session so
      // the model, thinking level, and active leaf all advance together.
      modelSwitchPendingRef.current = false;
      await loadSession(sid);
    } catch (e) {
      console.error("Failed to set model:", e);
      modelSwitchPendingRef.current = false;
      setCurrentModelOverride(previousOverride);
      addNotice({
        type: "error",
        message: `Failed to switch model: ${e instanceof Error ? e.message : String(e)}`,
      });
      // A failed response can still follow a server-side write (for example, a
      // dropped connection), so let the session file settle the displayed model.
      await loadSession(sid, false, true);
    } finally {
      modelSwitchPendingRef.current = false;
      setModelSwitching(false);
    }
  }, [addNotice, currentModelOverride, isNew, loadSession, setNewSessionModel]);

  /**
   * Manual compaction (the Compact button and `/compact`). Never awaited by a
   * caller that holds the composer: the compact POST is blocking and lasts the
   * whole compaction, and `runBuiltinCommand` keeps the composer's fieldset
   * disabled until the command resolves. The compaction reports itself — the
   * Compacting status, Stop compaction, the result banner and its error.
   */
  /** Resolves false when the compaction failed. */
  const handleCompact = useCallback(async (customInstructions?: string): Promise<boolean> => {
    const sid = sessionIdRef.current;
    if (!sid || isCompacting) return true;
    setIsCompacting(true);
    setCompactError(null);
    setCompactResult(null);
    try {
      const result = await sendAgentCommand<CompactCommandResult>(sid, {
        type: "compact",
        ...(customInstructions ? { customInstructions } : {}),
      });
      setCompactResult(readCompactResult(result, "manual"));
      if (await loadSession(sid, true)) promoteNewSession();
      return true;
    } catch (e) {
      setCompactError(e instanceof Error ? e.message : String(e));
      setCompactResult(null);
      return false;
    } finally {
      setIsCompacting(false);
    }
  }, [isCompacting, loadSession, promoteNewSession]);

  // The banner otherwise stays until the next compaction, e.g. "Nothing to compact".
  const dismissCompactError = useCallback(() => setCompactError(null), []);

  const loadModels = useCallback(async (signal?: AbortSignal) => {
    const modelCwd = newSessionCwd ?? session?.cwd ?? "";
    // An empty-state composer (no session, no new-session cwd) has no project
    // context — fetching bare "/api/models" would resolve the cwd to the
    // server's process.cwd() and 403. Skip instead; a mount with a real cwd
    // (or modelsRefreshKey change) reloads.
    if (!modelCwd) return;
    const modelsUrl = modelCwd ? `/api/models?cwd=${encodeURIComponent(modelCwd)}` : "/api/models";
    let d: ModelsResponse;
    let cwdGone: string | null = null;
    try {
      const res = await fetch(modelsUrl, signal ? { signal } : undefined);
      if (!res.ok) {
        let detail = "";
        try {
          const body: unknown = await res.json();
          if (body && typeof body === "object" && "error" in body && typeof body.error === "string") {
            detail = body.error;
          }
          cwdGone = missingCwdFromBody(body);
        } catch (e) {
          if (e instanceof DOMException && e.name === "AbortError") throw e;
          // Non-JSON error responses fall back to the HTTP status.
        }
        throw new Error(detail || `Failed to load models (HTTP ${res.status})`);
      }
      d = await res.json() as ModelsResponse;
      signal?.throwIfAborted();
    } catch (e) {
      if (!signal?.aborted && !(e instanceof DOMException && e.name === "AbortError")) {
        // A moved or deleted project folder is not a model problem (#1422).
        setModelsMissingCwd(cwdGone);
        setModelError(cwdGone ? null : e instanceof Error ? e.message : String(e));
      }
      throw e;
    }
    setModelsMissingCwd(null);
    setModelNames(d.models);
    setModelError(d.modelError ?? null);
    setModelScopeWarnings(d.modelScopeWarnings ?? []);
    setIsModelScopeActive(Boolean(d.isScoped));
    // The maps below replace the ones loadAllModels merged into: load that list again.
    setAllModelList(null);
    allModelsLoadRef.current = null;
    setModelThinkingLevels(d.thinkingLevels ?? {});
    setModelThinkingLevelMaps(d.thinkingLevelMaps ?? {});
    const nextModelList = d.modelList ?? [];
    setModelList(nextModelList);
    const displayDefaultModel = d.defaultModel
      ? nextModelList.find((m) => m.id === d.defaultModel?.modelId && m.provider === d.defaultModel?.provider)
      : undefined;
    setNewSessionDefaultModel(displayDefaultModel
      ? { provider: displayDefaultModel.provider, modelId: displayDefaultModel.id }
      : null);
    thinkingLevelPinsRef.current = d.thinkingLevelPins ?? {};
    defaultThinkingLevelRef.current = asConcreteThinkingLevel(d.defaultThinkingLevel);
    setSavedDefaultThinkingLevel(asConcreteThinkingLevel(d.savedDefaultThinkingLevel));
    if (isNew && !sessionIdRef.current) {
      // A picked model this folder does not offer (one carried over from the
      // composer this one replaced, or since scoped out) goes back to automatic.
      // An empty list offers nothing to compare with (a failed load answers
      // with one): the pick stays. An active scope does not wipe out an
      // intentionally selected unscoped model.
      const picked = newSessionModelOverrideRef.current;
      const kept = picked && (nextModelList.length === 0
        || Boolean(d.isScoped)
        || nextModelList.some((m) => m.provider === picked.provider && m.id === picked.modelId));
      if (picked && !kept) {
        newSessionModelOverrideRef.current = null;
        setNewSessionModel(null);
      }
      // The first listed model is not necessarily the runtime's automatic choice.
      // An `enabledModels` pattern may pin a thinking level (`anthropic/*:high`).
      // Like pi, apply it to the model a new session starts with.
      const startModel = kept ? picked : displayDefaultModel && { provider: displayDefaultModel.provider, modelId: displayDefaultModel.id };
      const pinned = startModel && d.thinkingLevelPins?.[`${startModel.provider}/${startModel.modelId}`];
      if (thinkingLevelOverrideRef.current === null) {
        setNewSessionDefaultThinkingLevel(
          asConcreteThinkingLevel(pinned) ?? defaultThinkingLevelRef.current,
        );
      }
    }
  }, [isNew, newSessionCwd, session?.cwd]);

  // pi's /model "all" scope: names and reasoning levels of models outside
  // `enabledModels` join the maps, so a picked unscoped model shows them too.
  const loadAllModels = useCallback(() => {
    if (allModelsLoadRef.current) return;
    const load = {};
    allModelsLoadRef.current = load;
    const modelCwd = newSessionCwd ?? session?.cwd ?? "";
    fetch(`/api/models?scope=all${modelCwd ? `&cwd=${encodeURIComponent(modelCwd)}` : ""}`)
      .then(async (res) => {
        if (!res.ok) throw new Error(`Failed to load models (HTTP ${res.status})`);
        const d = await res.json() as ModelsResponse;
        if (allModelsLoadRef.current !== load) return;
        setModelNames((prev) => ({ ...d.models, ...prev }));
        setModelThinkingLevels((prev) => ({ ...d.thinkingLevels, ...prev }));
        setModelThinkingLevelMaps((prev) => ({ ...d.thinkingLevelMaps, ...prev }));
        setAllModelList(d.modelList ?? []);
      })
      .catch((e) => {
        if (allModelsLoadRef.current !== load) return;
        allModelsLoadRef.current = null;
        addNotice({ type: "error", message: e instanceof Error ? e.message : String(e) });
      });
  }, [addNotice, newSessionCwd, session?.cwd]);

  useEffect(() => {
    if (!isModelScopeActive || !displayModel || allModelList) return;
    if (modelList.some((m) => m.provider === displayModel.provider && m.id === displayModel.modelId)) return;
    loadAllModels();
  }, [allModelList, displayModel, isModelScopeActive, loadAllModels, modelList]);

  // Picking a model or reasoning level is session-scoped, as in the TUI. The
  // selectors' star is the explicit Ctrl+S equivalent: it saves the global
  // default new sessions start with and also selects it for this chat.
  const saveDefaultPreferences = useCallback(async (
    edit: { provider: string; modelId: string } | { thinkingLevel: ConcreteThinkingLevel },
  ) => {
    const cwd = newSessionCwd ?? session?.cwd;
    const res = await fetch("/api/models/default", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...(cwd ? { cwd } : {}), ...edit }),
    });
    if (res.ok) return;
    let detail = "";
    try {
      const body: unknown = await res.json();
      if (body && typeof body === "object" && "error" in body && typeof body.error === "string") detail = body.error;
    } catch {
      // Non-JSON error responses fall back to the HTTP status.
    }
    throw new Error(detail || `HTTP ${res.status}`);
  }, [newSessionCwd, session?.cwd]);

  const reloadModelsQuietly = useCallback(() => {
    // The star already updated the marker; a failed refresh keeps the old list.
    loadModels().catch(() => {});
  }, [loadModels]);

  const handleSetDefaultModel = useCallback(async (provider: string, modelId: string) => {
    try {
      await saveDefaultPreferences({ provider, modelId });
    } catch (e) {
      addNotice({ type: "error", message: `Failed to save default model: ${e instanceof Error ? e.message : String(e)}` });
      return;
    }
    addNotice({ type: "success", message: `Default model: ${provider}/${modelId}` });
    setNewSessionDefaultModel({ provider, modelId });
    if (displayModel?.provider !== provider || displayModel?.modelId !== modelId) {
      await handleModelChange(provider, modelId);
    }
    reloadModelsQuietly();
  }, [addNotice, displayModel, handleModelChange, reloadModelsQuietly, saveDefaultPreferences]);



  const handleBuiltinSlashCommand = useCallback(async (text: string): Promise<BuiltinSlashCommandResult> => {
    if (!text.startsWith("/")) return { handled: false };
    const match = text.match(/^\/([^\s]+)(?:\s+([\s\S]*))?$/);
    if (!match) return { handled: false };

    const [, commandName, rawArgs = ""] = match;
    const args = rawArgs.trim();
    const sid = sessionIdRef.current ?? await ensureNewSession();
    const complete = (result: BuiltinSlashCommandResult): BuiltinSlashCommandResult => {
      if (!result.handled) return result;
      if (result.error) {
        addNotice({ type: "error", message: result.error });
      } else if (!result.action) {
        // A command that opens a panel says nothing: the panel is the answer.
        addNotice({ type: "success", message: result.message ?? "Command completed" });
      }
      return result;
    };

    try {
      switch (commandName) {
        case "compact": {
          if (!sid || isCompacting) return complete({ handled: true, error: "No active session to compact" });
          // Detached on purpose. This must resolve now: runBuiltinCommand holds
          // the composer's fieldset disabled until the command resolves, and the
          // compact POST blocks for the whole compaction, which read as an input
          // box that stopped taking text until the session was switched away and
          // back. The compaction reports itself (Compacting status, Stop
          // compaction, result banner, error), so no completion notice here.
          // A failed compaction gives its instructions back, as the awaited command
          // left them in the composer.
          void handleCompact(args || undefined).then((compacted) => {
            if (!compacted && args) opts.chatInputRef?.current?.prependText(`/compact ${args}`);
          });
          return complete({ handled: true, action: "compact" });
        }

        case "auto-compact": {
          if (!sid) return complete({ handled: true, error: "No active session" });
          // Read the live wrapper (this POST starts it if idle) so the toggle
          // follows settings.json, not the React default of `true`.
          const liveState = await sendAgentCommand<AgentStateResponse>(sid, { type: "get_state" });
          const nextEnabled = !(liveState?.autoCompactionEnabled ?? true);
          await sendAgentCommand(sid, {
            type: "set_auto_compaction",
            enabled: nextEnabled,
          });
          setAutoCompactionEnabled(nextEnabled);
          return complete({
            handled: true,
            message: nextEnabled
              ? "Auto-compaction enabled"
              : "Auto-compaction disabled",
          });
        }

        case "reload": {
          if (!sid) return complete({ handled: true, error: "No active session to reload" });
          await sendAgentCommand(sid, { type: "reload" });
          await Promise.all([
            loadSession(sid, false, true),
            loadTools(sid),
            loadSlashCommands(),
            loadModels(),
          ]);
          return complete({ handled: true, message: "Reloaded session resources" });
        }

        case "name": {
          if (!sid) return complete({ handled: true, error: "No active session to name" });
          if (!args) return complete({ handled: true, error: "Usage: /name <name>" });
          await sendAgentCommand(sid, { type: "set_session_name", name: args });
          if (await loadSession(sid)) promoteNewSession();
          return complete({ handled: true, message: `Session renamed to ${args}` });
        }

        case "session": {
          if (!sid) return complete({ handled: true, error: "No active session" });
          if (!onSessionStatsPanelOpen) return complete({ handled: true, message: formatSessionSummary(sessionStats) });
          const stats = await sendAgentCommand<SessionStatsInfo>(sid, { type: "get_session_stats" });
          if (stats) {
            setSessionStatsOverride(stats);
          }
          onSessionStatsPanelOpen();
          return complete({ handled: true, action: "openSessionStats" });
        }

        case "mcp": {
          // Only a bare /mcp, and only when pi's built-in MCP extension owns it or
          // nothing does (lib/mcp-command.ts): another extension's /mcp is sent as
          // before, and so is every subcommand, since `/mcp login`, `logout` and
          // `reconnect` act on this session's own connections. Returning before
          // onSend leaves no "/mcp" bubble and no sidebar row for a new chat; a
          // streaming run is not touched.
          if (args || !onOpenSettings) return { handled: false };
          const commands = await slashCommandsForMcp();
          if (!commands || !bareMcpOpensSettings(commands)) return { handled: false };
          onOpenSettings("mcp");
          return complete({ handled: true, action: "openSettings" });
        }

        case "copy": {
          if (!sid) return complete({ handled: true, error: "No active session" });
          const data = await sendAgentCommand<LastAssistantTextResponse>(sid, { type: "get_last_assistant_text" });
          const textToCopy = data?.text ?? "";
          if (!textToCopy) return complete({ handled: true, error: "No assistant message to copy" });
          await navigator.clipboard.writeText(textToCopy);
          return complete({ handled: true, message: "Copied last assistant message" });
        }

        case "clone": {
          if (!sid) return complete({ handled: true, error: "No active session to clone" });
          if (agentRunningRef.current || bashRunningRef.current) {
            return complete({ handled: true, error: "Cannot clone while the session is running" });
          }
          const result = await sendAgentCommand<{ cancelled?: boolean; newSessionId?: string }>(sid, {
            type: "clone",
            leafId: activeLeafId,
          });
          if (result?.cancelled || !result?.newSessionId) {
            return complete({ handled: true, error: "Cannot clone an empty or unsaved session" });
          }
          const completed = complete({ handled: true, message: "Cloned current session branch" });
          onSessionForked?.(result.newSessionId);
          return completed;
        }

        default:
          return { handled: false };
      }
    } catch (e) {
      return complete({ handled: true, error: e instanceof Error ? e.message : String(e) });
    }
  }, [activeLeafId, addNotice, ensureNewSession, handleCompact, isCompacting, loadModels, loadSession, loadSlashCommands, loadTools, promoteNewSession, onOpenSettings, onSessionForked, onSessionStatsPanelOpen, opts.chatInputRef, sessionStats, slashCommandsForMcp]);

  // Let AgentSession.prompt decide atomically whether to queue against the
  // current run or start a new turn if it settled while the request was in
  // flight. Direct steer/followUp calls can strand a message in an idle queue.
  const sendStreamingPrompt = useCallback(async (
    message: string,
    behavior: "steer" | "followUp",
    images?: AttachedImage[],
    // the subagent panel's relay (see sendPrompt): no composer restore, a
    // staged edit is left staged, and a failure rejects.
    relay = false,
  ) => {
    const sid = sessionIdRef.current;
    const restore = () => {
      if (!relay) restoreSubmission(message, images, composerDraftKey);
    };
    // A pending history edit must branch when sent, never join the current run.
    if (editEntryId && !relay) {
      restore();
      return;
    }
    if (!sid) {
      restore();
      addNotice({ type: "error", message: "No active session for the queued message" });
      if (relay) throw new Error("No active session for the queued message");
      return;
    }
    const piImages = images?.map((img) => ({ type: "image" as const, data: img.data, mimeType: img.mimeType }));
    try {
      notifyPromptReloadedFromDisk(await sendAgentCommand(sid, {
        type: "prompt",
        message,
        streamingBehavior: behavior,
        ...(piImages?.length ? { images: piImages } : {}),
      }));
    } catch (e) {
      console.error("Failed to submit streaming prompt:", e);
      restore();
      // A transport failure after dispatch is ambiguous: the server may have
      // accepted the queued prompt before the response was lost, so the notice
      // says the send is unconfirmed instead of failed (#1432).
      if (e instanceof AgentCommandError && e.missingCwd) setPromptMissingCwd(e.missingCwd);
      else if (isPromptRejectedError(e)) addNotice({ type: "error", message: e.message });
      else addUnconfirmedSendNotice();
      if (relay) throw e;
    }
  }, [addNotice, addUnconfirmedSendNotice, notifyPromptReloadedFromDisk, composerDraftKey, editEntryId, restoreSubmission]);

  const handleSteer = useCallback(async (message: string, images?: AttachedImage[]) => {
    await sendStreamingPrompt(message, "steer", images);
  }, [sendStreamingPrompt]);

  /**
   * The subagent panel's relay. claude has no channel to a subagent, so the
   * main session gets one visible user message (claudeSubagentRelayText()) and its agent
   * passes the text on with `SendMessage`: a running subagent gets it at its next tool
   * round, a finished one is resumed with it. A running foreground subagent is first moved
   * to the background (`background_task`, claude's Ctrl+B); without claude's
   * `backgrounded: true` nothing is sent. The message is steered into a running main turn,
   * else sent as a plain prompt. Rejects on any failure, so the panel keeps its text.
   */
  const relayToClaudeSubagent = useCallback(async (run: ClaudeSubagentRun, text: string) => {
    const sid = sessionIdRef.current;
    if (!sid) throw new Error("No active session for the relay");
    if (!run.agentId) throw new Error("claude has not reported this subagent's agent id yet");
    if (!text.trim()) throw new Error("The relay needs text");
    if (run.status === "running" && !run.background) {
      let answer: { backgrounded?: boolean } | null;
      try {
        answer = await sendAgentCommand<{ backgrounded?: boolean } | null>(sid, { type: "background_task", toolCallId: run.toolCallId });
      } catch (e) {
        addNotice({ type: "error", message: e instanceof Error ? e.message : String(e) });
        throw e;
      }
      if (answer?.backgrounded !== true) {
        const message = translate("claudeSubagent.relayNotBackgrounded");
        addNotice({ type: "error", message });
        throw new Error(message);
      }
    }
    const message = claudeSubagentRelayText(run, text);
    if (agentRunningRef.current) await sendStreamingPrompt(message, "steer", undefined, true);
    else await sendPrompt(message, undefined, true);
  }, [addNotice, sendPrompt, sendStreamingPrompt, translate]);

  const handlePromptWithStreamingBehavior = useCallback(async (
    message: string,
    behavior: "steer" | "followUp",
    images?: AttachedImage[],
  ) => {
    await sendStreamingPrompt(message, behavior, images);
  }, [sendStreamingPrompt]);

  const handleFollowUp = useCallback(async (message: string, images?: AttachedImage[]) => {
    await sendStreamingPrompt(message, "followUp", images);
  }, [sendStreamingPrompt]);

  /**
   * An open claude session's model or effort switch, sent only while idle
   * (the composer disables the controls meanwhile), or its permission-mode switch
   * (any time: claude takes it mid-turn). The control shows the new value only
   * once the server accepted it; a refusal is one visible line with claude's own text,
   * replaced by the next successful switch.
   */
  const switchClaudeSetting = useCallback(async (command: { type: "set_model"; model: string } | { type: "set_effort"; effort: ClaudeEffort } | { type: "set_permission_mode"; mode: ClaudeLaunchPermissionMode }) => {
    const sid = sessionIdRef.current;
    if (!sid) return;
    const prefix = command.type === "set_model" ? "claude.switchModelError" : command.type === "set_effort" ? "claude.effortError" : "claude.permissionModeError";
    try {
      const result = await sendAgentCommand<ClaudeSwitchResult | null>(sid, command);
      if (result?.rejected) {
        setClaudeSwitchError({ sessionId: sid, message: translateRef.current(prefix, { error: result.error ?? "" }) });
        return;
      }
      setClaudeSwitchError(null);
      setClaudeChoiceOverride((current) => ({
        ...(current?.sessionId === sid ? current : {}),
        sessionId: sid,
        ...(command.type === "set_model"
          ? { model: result?.model ?? command.model }
          : command.type === "set_effort"
            ? { effort: result?.effort ?? command.effort }
            : { launchPermissionMode: result?.launchPermissionMode ?? command.mode }),
      }));
    } catch (e) {
      setClaudeSwitchError({ sessionId: sid, message: translateRef.current(prefix, { error: e instanceof Error ? e.message : String(e) }) });
    }
  }, []);

  const handleClaudeModelChange = useCallback((model: string) => {
    if (isNew) {
      setNewSessionClaudeModel(model);
      return;
    }
    void switchClaudeSetting({ type: "set_model", model });
  }, [isNew, switchClaudeSetting]);

  const handleClaudeEffortChange = useCallback((effort: ClaudeEffort) => {
    if (isNew) {
      setNewSessionClaudeEffort(effort);
      return;
    }
    void switchClaudeSetting({ type: "set_effort", effort });
  }, [isNew, switchClaudeSetting]);

  // an open claude session's permission mode, for that session alone.
  const handleClaudeLaunchPermissionModeChange = useCallback((mode: ClaudeLaunchPermissionMode) => {
    if (isNew) return;
    void switchClaudeSetting({ type: "set_permission_mode", mode });
  }, [isNew, switchClaudeSetting]);

  const handleAbortCompaction = useCallback(async () => {
    const sid = sessionIdRef.current;
    if (!sid) return;
    try {
      await sendAgentCommand(sid, { type: "abort_compaction" });
    } catch (e) {
      console.error("Failed to abort compaction:", e);
    }
  }, []);

  const handleRecallQueue = useCallback(async () => {
    const sid = sessionIdRef.current;
    if (!sid) return;
    try {
      const result = await sendAgentCommand<RecalledPrompts>(sid, { type: "clear_queue" });
      // clearQueue also emits an empty queue_update, but that only reaches us
      // while SSE is connected — clear locally so idle recalls update the UI.
      setQueuedMessages({ steering: [], followUp: [] });
      restoreRecalledPrompts(result);
    } catch (e) {
      console.error("Failed to recall queued messages:", e);
      addNotice({ type: "error", message: "Failed to recall queued messages" });
    }
  }, [addNotice, restoreRecalledPrompts]);

  const handleThinkingLevelChange = useCallback(async (level: ThinkingLevelOption) => {
    if (level === "auto") {
      thinkingLevelOverrideRef.current = null;
      setNewSessionThinkingLevel(null);
      setCurrentThinkingOverride(null);
      return;
    }
    if (isNew) {
      thinkingLevelOverrideRef.current = level;
      setNewSessionThinkingLevel(level);
    } else {
      setCurrentThinkingOverride(level);
    }
    const sid = sessionIdRef.current ?? await ensuringNewSessionRef.current;
    if (!sid) return;
    try {
      await sendAgentCommand(sid, { type: "set_thinking_level", level });
      if (sessionHookMountedRef.current && sessionIdRef.current === sid) {
        setLiveThinkingLevel(level);
        setCurrentThinkingOverride(null);
      }
    } catch (e) {
      console.error("Failed to set thinking level:", e);
      setCurrentThinkingOverride(null);
    }
  }, [isNew]);

  const handleSetDefaultThinkingLevel = useCallback(async (level: ConcreteThinkingLevel) => {
    try {
      await saveDefaultPreferences({ thinkingLevel: level });
    } catch (e) {
      addNotice({ type: "error", message: `Failed to save default reasoning level: ${e instanceof Error ? e.message : String(e)}` });
      return;
    }
    addNotice({ type: "success", message: `Default reasoning level: ${level}` });
    setSavedDefaultThinkingLevel(level);
    if (displayThinkingLevel !== level) await handleThinkingLevelChange(level);
    reloadModelsQuietly();
  }, [addNotice, displayThinkingLevel, handleThinkingLevelChange, reloadModelsQuietly, saveDefaultPreferences]);

  const handleToolPresetChange = useCallback(async (preset: ToolPreset) => {
    const toolNames = getToolNamesForPreset(preset);
    setPreferredToolPreset(preset);
    setToolPresetState(preset);
    const sid = sessionIdRef.current ?? await ensuringNewSessionRef.current;
    if (!sid) {
      sessionToolsPinnedRef.current = toolNames !== undefined;
      return;
    }
    try {
      // Omitting toolNames retracts the session's pin so it follows the configured
      // defaults again; the server rebuilds the session to resolve them.
      const result = await sendAgentCommand<{ sessionId?: string; recreated?: boolean }>(sid, {
        type: "set_tools",
        ...(toolNames !== undefined ? { toolNames } : {}),
      });
      sessionToolsPinnedRef.current = toolNames !== undefined;
      const activeSessionId = result?.sessionId ?? sid;
      if (activeSessionId !== sid || result?.recreated) {
        cancelEventStreamGrace();
        closeEvents("set_tools");
        // The old wrapper cancels its pending extension UI only after its stream has
        // closed, so those close events never arrive; drop the requests here instead
        // of leaving them queued in front of the new wrapper's.
        setExtensionDialogs([]);
        setExtensionCustomUis([]);
        sessionIdRef.current = activeSessionId;
        if (result?.recreated && sessionPropIdRef.current === activeSessionId) {
          maintainEventsConnected(activeSessionId);
        }
      }
      clearSlashCommands();
      setExtensionStatuses([]);
      setExtensionWidgets([]);
      const [state] = await Promise.all([
        sendAgentCommand<AgentStateResponse>(activeSessionId, { type: "get_state" }),
        loadTools(activeSessionId),
      ]);
      if (sessionHookMountedRef.current && sessionIdRef.current === activeSessionId) {
        setSystemPrompt(state.systemPrompt ?? "");
        syncLiveModel(state);
      }
    } catch (e) {
      console.error("Failed to set tools:", e);
    }
  }, [cancelEventStreamGrace, clearSlashCommands, closeEvents, loadTools, maintainEventsConnected, setToolPresetState, syncLiveModel]);

  const scrollToMessage = useCallback((element: HTMLElement, viewportOffset = 16) => {
    const container = scrollContainerRef.current;
    if (!container) return;
    if (liveFollowFrameRef.current !== null) {
      cancelAnimationFrame(liveFollowFrameRef.current);
      liveFollowFrameRef.current = null;
    }
    initialScrollDoneRef.current = true;
    pendingScrollToUserRef.current = false;
    isNearBottomRef.current = false;
    setPromptAnchorActive(false);
    container.scrollTo({
      top: element.getBoundingClientRect().top
        - container.getBoundingClientRect().top
        + container.scrollTop
        - viewportOffset,
      behavior: "instant",
    });
    previousScrollTopRef.current = container.scrollTop;
  }, []);

  /**
   * Programmatic jump to an absolute content offset (minimap navigation).
   * Shares scrollToMessage's tail-stick suppression: the messages-length
   * effect must not "helpfully" scroll back to the bottom while a pagination
   * batch lands after this jump (the race pi#4's pane mount exposed).
   */
  const scrollToOffset = useCallback((top: number, viewportOffset = 0) => {
    const container = scrollContainerRef.current;
    if (!container) return;
    if (liveFollowFrameRef.current !== null) {
      cancelAnimationFrame(liveFollowFrameRef.current);
      liveFollowFrameRef.current = null;
    }
    initialScrollDoneRef.current = true;
    pendingScrollToUserRef.current = false;
    isNearBottomRef.current = false;
    setPromptAnchorActive(false);
    container.scrollTo({ top: Math.max(0, top - viewportOffset), behavior: "instant" });
    previousScrollTopRef.current = container.scrollTop;
  }, []);

  const scrollUserMsgToTop = useCallback(() => {
    const container = scrollContainerRef.current;
    const el = lastUserMsgRef.current;
    if (!container || !el) return;
    const elAbsTop = el.getBoundingClientRect().top - container.getBoundingClientRect().top + container.scrollTop;
    const maxScrollTop = Math.max(0, container.scrollHeight - container.clientHeight);
    const targetTop = Math.min(Math.max(0, elAbsTop - 16), maxScrollTop);

    if (liveFollowFrameRef.current !== null) {
      cancelAnimationFrame(liveFollowFrameRef.current);
      liveFollowFrameRef.current = null;
    }
    isNearBottomRef.current = true;
    previousScrollTopRef.current = targetTop;
    container.scrollTo({ top: targetTop, behavior: "auto" });
  }, []);

  const handleScrollPositionChange = useCallback(() => {
    const container = scrollContainerRef.current;
    if (container) {
      const { scrollTop, clientHeight, scrollHeight } = container;
      const isAgentRunning = agentRunningRef.current;
      const wasAttached = isNearBottomRef.current;
      const isAttached = getLiveFollowAttached(
        wasAttached,
        previousScrollTopRef.current,
        scrollTop,
        clientHeight,
        scrollHeight,
        isAgentRunning
          ? CHAT_SCROLL_REATTACH_TOLERANCE
          : CHAT_SCROLL_TAIL_TOLERANCE,
      );
      isNearBottomRef.current = isAttached;
      previousScrollTopRef.current = scrollTop;
      const shouldShow = shouldShowScrollToLatest(scrollTop, clientHeight, scrollHeight);
      setShowScrollToBottom((previous) => (previous === shouldShow ? previous : shouldShow));
      if (!wasAttached && isAttached && isAgentRunning) {
        scrollToBottom("auto");
      } else if (!isAttached && liveFollowFrameRef.current !== null) {
        cancelAnimationFrame(liveFollowFrameRef.current);
        liveFollowFrameRef.current = null;
      }
    }
  }, [scrollToBottom]);

  // What a session read with its state (`loadSession(…, includeState)`) says is
  // under way on the server: a run or command, a `!` command, a compaction. The
  // mount and a hidden page's return show it the same way.
  const applyLoadedAgentState = useCallback((sid: string, loadedAgentState: unknown) => {
    const agentState = loadedAgentState as { running: boolean; state?: AgentStateResponse } | null;
    if (agentState?.running) {
      // Tool reload rides actual resumable activity only: an alive-but-idle
      // wrapper (any previously opened session reports running:true from its
      // live wrapper) must not issue a get_tools POST on every reopen; the
      // preset already came from the detail payload's toolNames (wi pi#103).
      if (agentState.state?.isStreaming || agentState.state?.isPromptRunning) {
        loadTools(sid);
        sdkAgentActiveRef.current = Boolean(agentState.state.isStreaming);
        rpcPromptPendingRef.current = Boolean(agentState.state.isPromptRunning);
        agentRunningRef.current = true;
        setAgentRunning(true);
        setAgentPhase((prev) => runStatePhase(prev, agentState.state?.isStreaming === true));
        dispatch({ type: "resume" });
        if (!agentState.state.isStreaming && agentState.state.isPromptRunning) {
          void waitForPromptSettlement(sid);
        }
      }
      if (agentState.state?.isBashRunning) {
        loadTools(sid);
        bashRunningRef.current = true;
        setBashRunning(true);
        void waitForBashSettlement(sid);
      }
    }
    if (agentState?.state) {
      if (agentState.state.isCompacting !== undefined) setIsCompacting(agentState.state.isCompacting);
      if (agentState.state.systemPrompt !== undefined) setSystemPrompt(agentState.state.systemPrompt ?? null);
      if (agentState.state.queuedMessages !== undefined) setQueuedMessages(normalizeQueuedMessages(agentState.state.queuedMessages));
    }
  }, [dispatch, loadTools, waitForBashSettlement, waitForPromptSettlement]);

  // Open a released stream again and read what it missed (a run another tab, a
  // device or an extension started or finished, statuses), shown as the mount
  // shows it. `connected` resumes a streaming run and replays pending extension
  // dialogs, so their completion and attention notifications reach the page as
  // if it had never let go. Nothing is resent.
  reattachEventStreamRef.current = (sid: string, reason: "visible" | "activity") => {
    if (!eventStreamReleasedRef.current) return;
    eventStreamReleasedRef.current = false;
    reattachDebugRef.current = { count: reattachDebugRef.current.count + 1, lastAt: Date.now(), lastReason: reason };
    setEventStreamReattachCount((count) => count + 1);
    if (!sessionHookMountedRef.current || sessionIdRef.current !== sid || sessionPropIdRef.current !== sid) return;
    maintainEventsConnected(sid);
    void loadSession(sid, false, true).then((loadedAgentState) => {
      if (!sessionHookMountedRef.current || sessionIdRef.current !== sid) return;
      applyLoadedAgentState(sid, loadedAgentState);
    });
  };

  // The page keeps its selected session alive with a lease renewal every
  // SESSION_LEASE_RENEW_INTERVAL_MS, each one resetting the session's idle
  // countdown, whether or not it holds the event stream: a hidden page lets go
  // of the stream (#1283), not of the session, so the idle wrapper and an
  // extension's timer in it live on. A released page
  // reads each reply and opens the stream again as soon as something is under
  // way (sessionLeaseShowsActivity()); one that comes back opens it at once. A
  // dead stream is the connection's heartbeat watchdog's to notice.
  useEffect(() => {
    const sid = session?.id;
    if (!sid) return;
    let disposed = false;
    const leaseDebug = leaseDebugRef.current;
    const renew = async () => {
      const reply = await requestLease(sid);
      if (disposed || document.visibilityState !== "hidden") return;
      if (sessionLeaseShowsActivity(reply)) reattachEventStreamRef.current(sid, "activity");
    };
    void renew();
    const interval = setInterval(() => void renew(), SESSION_LEASE_RENEW_INTERVAL_MS);
    leaseDebug.renewStartedAt = Date.now();
    const onVisibilityChange = () => {
      if (document.visibilityState !== "visible") return;
      reattachEventStreamRef.current(sid, "visible");
      // A frozen background page renewed nothing; coming back renews at once.
      void renew();
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      disposed = true;
      clearInterval(interval);
      leaseDebug.renewStartedAt = null;
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [requestLease, session?.id]);

  // Load session on mount
  useEffect(() => {
    sessionHookMountedRef.current = true;
    // Usage reads started before a remount are stale.
    contextUsageAppliedIdRef.current = contextUsageRequestIdRef.current;
    if (session) {
      sessionIdRef.current = session.id;
      // Snapshot fast path: show the cached history window immediately, then
      // run the normal forced read in the background as the freshness check.
      // Only the settled history fields are restored — no streaming, queue, or
      // run state — and the background read remains authoritative.
      const cached = getSessionViewSnapshot(session.id);
      if (cached) {
        setData({
          sessionId: session.id,
          filePath: "",
          totalActiveMs: cached.totalActiveMs ?? 0,
          tree: cached.summaryTree as SessionData["tree"],
          leafId: cached.leafId,
          context: {
            messages: cached.messages,
            entryIds: cached.entryIds,
            oldestEntryId: cached.oldestEntryId,
            hasMore: cached.hasMore,
            // A snapshot written before the offset was tracked reads as zero.
            turnsBefore: cached.turnsBefore ?? 0,
            thinkingLevel: cached.thinkingLevel,
            model: cached.model,
          },
          stats: cached.stats as SessionData["stats"],
        });
        setActiveLeafId(cached.leafId);
        setMessages(cached.messages);
        setEntryIds(cached.entryIds);
        setHistoryCursor(cached.oldestEntryId);
        setHasEarlierMessages(cached.hasMore);
        setError(null);
        setLoading(false);
      }
      loadSession(session.id, !cached, true, { force: true }).then((loadedAgentState) => {
        applyLoadedAgentState(session.id, loadedAgentState);
      });
    }
    return () => {
      sessionHookMountedRef.current = false;
      const abandonedDraftKey = isNew ? newSessionDraftKey : null;
      if (abandonedDraftKey) {
        queueMicrotask(() => {
          if (!sessionHookMountedRef.current && !newSessionPromotedRef.current) {
            clearDraft(abandonedDraftKey);
          }
        });
      }
      // Persist the settled view of the outgoing session so switching back can
      // restore it instantly. Only when the cached window still covers every
      // entry the UI has loaded (paged-in history included).
      const sid = sessionIdRef.current;
      const currentData = dataRef.current;
      if (sid && currentData && currentData.sessionId === sid && currentData.snapshotRevision) {
        const existing = getSessionViewSnapshot(sid);
        const entryIds = entryIdsRef.current;
        const coverable = !existing || entryIds.every((id) => existing.entryIds.includes(id) || (existing.loadedEntryIds ?? []).includes(id));
        if (coverable) {
          setSessionViewSnapshot({
            sessionId: sid,
            revision: currentData.snapshotRevision,
            messages: messagesRef.current,
            entryIds: entryIdsRef.current,
            leafId: activeLeafIdRef.current,
            oldestEntryId: historyCursorRef.current,
            hasMore: hasEarlierMessagesRef.current,
            turnsBefore: currentData.context.turnsBefore ?? 0,
            summaryTree: currentData.tree,
            thinkingLevel: currentData.context.thinkingLevel,
            model: currentData.context.model,
            stats: currentData.stats,
            totalActiveMs: currentData.totalActiveMs,
            loadedEntryIds: entryIdsRef.current,
          });
        } else {
          deleteSessionViewSnapshot(sid);
        }
      }
      // After the snapshot above, so the bubble of a send given back leaves it.
      releaseUnconfirmedSendRef.current();
      if (liveFollowFrameRef.current !== null) {
        cancelAnimationFrame(liveFollowFrameRef.current);
        liveFollowFrameRef.current = null;
      }
      bashRecoveryIdRef.current += 1;
      cancelEventStreamGrace();
      closeEvents();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    onSystemPromptChange?.(systemPrompt);
  }, [systemPrompt, onSystemPromptChange]);

  useEffect(() => {
    onSystemInfoLoaderChange?.(loadSystemInfo);
    return () => onSystemInfoLoaderChange?.(null);
  }, [loadSystemInfo, onSystemInfoLoaderChange]);

  // The debug panel's view of this page's stream and lease, read on its own
  // clock. Refs and plain copies only: it requests, opens, arms and renders nothing.
  const readDebugSnapshot = useCallback((): DebugClientSession => {
    let connection: DebugEventConnectionSnapshot | null = null;
    try {
      // A connection built before a hot reload may lack snapshot().
      connection = eventConnectionRef.current?.snapshot?.() ?? null;
    } catch {
      connection = null;
    }
    const sid = sessionIdRef.current;
    const lease = leaseDebugRef.current;
    const reattached = reattachDebugRef.current;
    return {
      pageId: peekPageId(),
      sessionId: sid,
      selectedSessionId: sessionPropIdRef.current,
      mounted: sessionHookMountedRef.current,
      wantsStream: sid !== null && wantsEventStream(sid),
      connection,
      released: { active: eventStreamReleasedRef.current, ...releaseDebugRef.current },
      reattached: { ...reattached },
      hiddenReleaseDueAt: hiddenReleaseDueAtRef.current,
      hiddenGraceMs: HIDDEN_PAGE_GRACE_MS,
      graceDueAt: eventStreamGraceDueAtRef.current,
      run: {
        agentRunning: agentRunningRef.current,
        bashRunning: bashRunningRef.current,
        compacting: isCompactingRef.current,
        dialogs: extensionUiCountRef.current,
      },
      lease: {
        renewIntervalMs: SESSION_LEASE_RENEW_INTERVAL_MS,
        renewStartedAt: lease.renewStartedAt,
        inFlightSince: lease.inFlightSince,
        requests: lease.requests,
        unanswered: lease.unanswered,
        errors: lease.errors,
        last: lease.last && { ...lease.last, reply: lease.last.reply && { ...lease.last.reply } },
      },
    };
  }, [wantsEventStream]);

  useEffect(() => {
    onDebugSnapshotReaderChange?.(readDebugSnapshot);
    return () => onDebugSnapshotReaderChange?.(null);
  }, [readDebugSnapshot, onDebugSnapshotReaderChange]);

  const branchSwitchLocked = agentRunning || bashRunning || isCompacting;
  useEffect(() => {
    if (!onBranchDataChange) return;
    onBranchDataChange(data?.tree ?? [], activeLeafId, handleLeafChange, branchSwitchLocked);
  }, [data?.tree, activeLeafId, handleLeafChange, branchSwitchLocked, onBranchDataChange]);

  useEffect(() => {
    const container = scrollContainerRef.current;
    if (!container) return;
    previousScrollTopRef.current = container.scrollTop;
    container.addEventListener("scroll", handleScrollPositionChange, { passive: true });
    return () => {
      container.removeEventListener("scroll", handleScrollPositionChange);
    };
  }, [messages.length, loading, handleScrollPositionChange]);

  useEffect(() => {
    if (!agentRunning) setPromptAnchorActive(false);
  }, [agentRunning]);

  useLayoutEffect(() => {
    if (messages.length > 0) {
      if (pendingScrollToUserRef.current) {
        pendingScrollToUserRef.current = false;
        initialScrollDoneRef.current = true;
        scrollUserMsgToTop();
      } else if (!initialScrollDoneRef.current) {
        initialScrollDoneRef.current = true;
        scrollToBottom("instant");
      } else if (!agentRunningRef.current && isNearBottomRef.current) {
        scrollToBottom("auto");
      }
    }
  }, [messages.length, agentRunning, scrollToBottom, scrollUserMsgToTop]);

  // Load the model list with bounded retries; loadModels exposes each failure.
  useEffect(() => {
    const controller = new AbortController();
    (async () => {
      for (let attempt = 0; ; attempt++) {
        try {
          await loadModels(controller.signal);
          return;
        } catch (e) {
          if (controller.signal.aborted) return;
          if (e instanceof DOMException && e.name === "AbortError") return;
          if (attempt >= MODELS_RETRY_DELAYS_MS.length) return;
          await delay(MODELS_RETRY_DELAYS_MS[attempt]);
          if (controller.signal.aborted) return;
        }
      }
    })();
    return () => controller.abort();
  }, [loadModels, modelsRefreshKey]);

  useEffect(() => {
    if (!compactResult) return;
    const t = setTimeout(() => setCompactResult(null), 6000);
    return () => clearTimeout(t);
  }, [compactResult]);

  // Pause notice expiry while hovered or focused.
  // The remainingMs/startedAt/oldestId refs implement a true pause-and-resume instead of resetting the 5s timer.
  const [pausedNoticeId, setPausedNoticeId] = useState<string | null>(null);
  const noticeRemainingMsRef = useRef(NOTICE_VISIBLE_MS);
  const noticeTimerStartedAtRef = useRef<number | null>(null);
  const noticeOldestIdRef = useRef<string | null>(null);

  useEffect(() => {
    if (noticeState.visible.length === 0) {
      noticeOldestIdRef.current = null;
      return;
    }
    const exiting = noticeState.visible.find((notice) => notice.exiting);
    if (exiting) {
      const t = setTimeout(() => {
        dispatchNotice({ type: "remove", id: exiting.id });
      }, NOTICE_EXIT_ANIMATION_MS);
      return () => clearTimeout(t);
    }
    const oldest = noticeState.visible[0];
    if (!oldest) return;
    // Oldest visible notice changed; restart the countdown
    if (noticeOldestIdRef.current !== oldest.id) {
      noticeOldestIdRef.current = oldest.id;
      noticeRemainingMsRef.current = NOTICE_VISIBLE_MS;
    }
    if (noticeState.visible.some((notice) => notice.id === pausedNoticeId)) return;
    noticeTimerStartedAtRef.current = Date.now();
    const t = setTimeout(() => {
      dispatchNotice({ type: "mark_oldest_exiting" });
    }, noticeRemainingMsRef.current);
    return () => {
      clearTimeout(t);
      // Accrue the elapsed time so the countdown resumes from the remaining time
      if (noticeTimerStartedAtRef.current !== null) {
        noticeRemainingMsRef.current = Math.max(
          0,
          noticeRemainingMsRef.current - (Date.now() - noticeTimerStartedAtRef.current),
        );
        noticeTimerStartedAtRef.current = null;
      }
    };
  }, [noticeState.visible, pausedNoticeId]);

  useEffect(() => {
    setSessionStatsOverride(null);
  }, [messages.length, contextUsage?.tokens, contextUsage?.percent, contextUsage?.contextWindow]);

  const thinkingLevel: ThinkingLevelOption = displayThinkingLevel ?? "auto";
  // The head of each queue is on screen; the rest wait behind it.
  const extensionDialog = extensionDialogs[0] ?? null;
  const waitingExtensionDialogCount = Math.max(0, extensionDialogs.length - 1);
  const extensionCustomUi = extensionCustomUis[0] ?? null;
  const waitingExtensionCustomUiCount = Math.max(0, extensionCustomUis.length - 1);
  const messageKeys = useMemo(
    () => messageRenderKeys(entryIds, messages.length, messageKeyAliases),
    [entryIds, messages.length, messageKeyAliases],
  );

  // A claude session's row, with the values a switch on this page set since it was read.
  const claudeRow = session?.claude ?? data?.info?.claude ?? null;
  const claudeOverride = claudeChoiceOverride && claudeChoiceOverride.sessionId === (session?.id ?? data?.sessionId) ? claudeChoiceOverride : null;
  const claudeSessionChoices = claudeRow && claudeOverride
    ? {
        ...claudeRow,
        ...(claudeOverride.model ? { model: claudeOverride.model } : {}),
        ...(claudeOverride.effort ? { effort: claudeOverride.effort } : {}),
        ...(claudeOverride.launchPermissionMode ? { launchPermissionMode: claudeOverride.launchPermissionMode } : {}),
      }
    : claudeRow;

  return {
    // State
    data, loading, error, activeLeafId, messages, activeToolResults, entryIds, messageKeys, historyCursor, hasEarlierMessages, earlierTurnCount, streamingStore,
    agentRunning, modelNames, modelList, modelError, missingCwd, modelScopeWarnings, isModelScopeActive, allModelList, modelThinkingLevels, modelThinkingLevelMaps, newSessionModel, toolPreset, thinkingLevel,
    retryInfo, contextUsage, systemPrompt, forkingEntryId,
    isCompacting, compactError, compactResult, currentModel, displayModel, modelSwitching, sessionStats, autoCompactionEnabled,
    slashCommands, slashCommandsLoading, queuedMessages,
    notices: noticeState.visible, extensionDialog, waitingExtensionDialogCount, extensionCustomUi, waitingExtensionCustomUiCount, extensionStatuses, extensionWidgets, respondToExtensionUi, sendExtensionCustomInput,
    isAutoModelSelection: isNew && newSessionModel === null,
    isAutoThinkingSelection: isNew && newSessionThinkingLevel === null,
    defaultModel: newSessionDefaultModel,
    // a claude session (or a fresh composer set to claude) and the composer's claude choices.
    isClaudeSession,
    // the claude session's subagent runs by tool call id (empty for pi and for a session without any).
    claudeSubagents: claudeSubagentState.sessionId !== null && claudeSubagentState.sessionId === (session?.id ?? data?.sessionId) ? claudeSubagentState.runs : NO_CLAUDE_SUBAGENTS,
    // the claude session's background tasks by task id, and a Stop's force stop asking for confirmation.
    claudeBackgroundTasks: claudeTaskState.sessionId !== null && claudeTaskState.sessionId === (session?.id ?? data?.sessionId) ? claudeTaskState.tasks : NO_CLAUDE_TASKS,
    claudeForceStopWork: claudeForceStop && claudeForceStop.sessionId === (session?.id ?? data?.sessionId) ? claudeForceStop.work : null,
    claudeSessionChoices: isNew ? null : claudeSessionChoices,
    // the claude user messages 「从此处编辑」 can start from (pi: none needed, every one can).
    claudeEditEntryIds: isClaudeSession && !isNew ? data?.claudeEditEntryIds ?? NO_CLAUDE_EDIT_ENTRY_IDS : NO_CLAUDE_EDIT_ENTRY_IDS,
    newSessionHarness, chooseNewSessionHarness,
    newSessionClaudeModel, setNewSessionClaudeModel,
    // claude's model / effort controls and the line of a switch claude refused.
    newSessionClaudeEffort, handleClaudeModelChange, handleClaudeEffortChange,
    // an open claude session's permission-mode switch (its mode is claudeSessionChoices.launchPermissionMode, absent = default).
    handleClaudeLaunchPermissionModeChange,
    claudeSwitchError: claudeSwitchError && claudeSwitchError.sessionId === (session?.id ?? data?.sessionId) ? claudeSwitchError.message : null,
    claudeStateKnown: streamStateSessionId !== null && streamStateSessionId === (session?.id ?? data?.sessionId),
    claudeAvailability,
    savedDefaultThinkingLevel,
    agentPhase,
    isNew,
    editEntryId,
    promptAnchorActive,
    showScrollToBottom,
    // Refs
    sessionIdRef, scrollContainerRef,
    lastUserMsgRef, pendingScrollToUserRef, initialScrollDoneRef,
    // Actions
    handleSend, handleAbort, handleFork, handleNavigate, handleModelChange,
    handleCompact, handleSteer, handleFollowUp, handlePromptWithStreamingBehavior, handleAbortCompaction,
    // the subagent panel's relay (background_task first for a running foreground run).
    relayToClaudeSubagent,
    // one background task's stop and output read; the force-stop notice's answers.
    stopClaudeBackgroundTask, readClaudeTaskOutput, forceStopClaudeTurn, dismissClaudeForceStop,
    dismissCompactError,
    handleRecallQueue,
    handleBuiltinSlashCommand,
    // Both harnesses edit in place (a claude edit is sent as a truncating resume, see handleSend).
    handleEditContent,
    // Present only while a history edit is pending.
    cancelEdit: editEntryId ? cancelEdit : undefined,
    addNotice,
    setNoticePaused: setPausedNoticeId,
    handleToolPresetChange, handleThinkingLevelChange, handleSetDefaultModel, handleSetDefaultThinkingLevel, loadTools, loadSlashCommands, loadAllModels, setActiveLeafId, setData, setMessages, loadContext,
    refreshFromDisk,
    manualFetch, manualFetching, manualFetchAvailable,
    scrollToBottom, scrollUserMsgToTop, scrollToMessage, scrollToOffset,
    dispatch, setAgentRunning, setForkingEntryId,
    bashRunning, pendingBash,
    // Subscriptions
    handleAgentEventRef,
  };
}
