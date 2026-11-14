"use client";

import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type MouseEvent as ReactMouseEvent, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import type { PolyforgeJob, PolyforgeOpsStatus, PolyforgeVerifyQueue, PolyforgeWatchStatus } from "@/lib/polyforge-status";
import {
  cardRunningMinutes,
  formatCardDuration,
  formatProgressClock,
  isProgressPinnedToBottom,
  polyforgeCardKey,
  polyforgeContextDetailText,
  polyforgeContextMeterText,
  polyforgeContextMeterTone,
  polyforgeJobRef,
  progressBlocks,
  shortJobTitle,
  truncateCardTitle,
  type PolyforgeCard,
  type PolyforgeProgressBlock,
  type PolyforgeProgressBlockStatus,
  type PolyforgeProgressLine,
  type PolyforgeProgressSnapshot,
  type PolyforgeProgressTone,
} from "@/lib/polyforge-cards-pure";
import type { PolyforgeContextUsage } from "@/lib/polyforge-status";
import { POLYFORGE_BLOCK_STYLE } from "@/lib/panel-layout";
import { usePolyforgePanelPreferences } from "@/hooks/usePolyforgePanelPreferences";
import type { PolyforgeContact, PolyforgeConversation, PolyforgeMessage, PolyforgeProjectGroup } from "@/lib/polyforge-messages-pure";
import type { PolyforgeTab, PolyforgeWorkTab } from "@/hooks/usePolyforge";
import { useIsMobile } from "@/hooks/useIsMobile";
import { usePolyforgeProgress } from "@/hooks/usePolyforgeProgress";
import { PolyforgeMessages, type PolyforgeMessagesProps } from "./PolyforgeMessages";
import { PolyforgeMemory } from "./PolyforgeMemory";
import { PolyforgeIntegrate } from "./PolyforgeIntegrate";
import { PolyforgeTasks } from "./PolyforgeTasks";
import { PolyforgeWorkItems } from "./PolyforgeWorkItems";
import { MAX_MARKDOWN_CHARS, SafeMarkdownBody } from "./SafeMarkdownBody";
import {
  attentionItems,
  decisionSourceShown,
  droppedItemAttention,
  isKnownDecisionClass,
  isOpenDecision,
  itemDecisions,
  type PolyforgeDecision,
  type PolyforgeDecisionFeed,
} from "@/lib/polyforge-decisions-pure";
import {
  conversationState,
  currentPendingQuestion,
  needsInputJobs,
  pendingOwnerEchoes,
  type PolyforgeOwnerEcho,
} from "@/lib/polyforge-conversation-pure";
import { PolyforgeProgressToolCard, fetchProgressToolDetail, type PolyforgeProgressToolDetailState } from "./PolyforgeProgressToolCard";
import { PolyforgeProgressSubagentEntry } from "./PolyforgeProgressSubagent";
import { PolyforgeJobComposer } from "./PolyforgeJobComposer";
import { useI18n } from "@/hooks/useI18n";

const STEP_STATUS_COLORS: Record<string, string> = {
  in_progress: "var(--accent)",
  done: "#16a34a",
  failed: "#dc2626",
  blocked: "#d97706",
  // pi#114: state the full contract — pending/cancelled are gray explicitly;
  // unknown statuses keep the same gray via the fallback in stepNodeColor.
  pending: "var(--text-dim)",
  cancelled: "var(--text-dim)",
  // a step the lane skipped is gray too, stated explicitly.
  skipped: "var(--text-dim)",
};

const CANDIDATE_MAX = 24;

/** Candidate shortening (assumption 1): >24 chars ⇒ first 24 + `…`, client-side. */
export function shortenCandidate(candidate: string): string {
  return candidate.length > CANDIDATE_MAX ? candidate.slice(0, CANDIDATE_MAX) + "…" : candidate;
}

/** Age in seconds → compact duration (7325 → "2h 2m"). */
export function formatAge(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "—";
  const total = Math.round(seconds);
  if (total < 60) return `${total}s`;
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return `${minutes}m`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

// Known labels get a translated string; unknown values pass through raw
// (never a missing i18n key — no raw `polyforge.*` text can reach the UI).
const JOB_STATUS_KEYS = new Set(["running", "needs_input", "paused", "queued", "completed", "failed", "cancelled"]);
const STEP_STATUS_KEYS = new Set(["done", "in_progress", "pending", "failed", "blocked", "cancelled", "skipped"]);
const WORKFLOW_STATUS_KEYS = new Set(["running", "needs_input", "queued", "completed", "blocked", "failed", "cancelled", "done"]);

/** Node colour for the step flow graph (pi#114, R6): status colour from the
 *  explicit map; unknown statuses read gray, never a missing entry. */
export function stepNodeColor(status: string): string {
  return STEP_STATUS_COLORS[status] ?? "var(--text-dim)";
}

export interface StepGraph {
  nodes: { id: string; status: string }[];
  sequenceEdges: [string, string][];
  rewindEdges: [string, string][];
}

/** Flow-graph model for the step display (pi#114, R2–R5). Pure: no I/O, no
 *  throw, so a malformed snapshot degrades silently instead of surfacing an
 *  error.
 *  - `declaredSteps` present: nodes follow declaration order; declared ids
 *    missing from `steps` become pending gray placeholders; steps absent from
 *    the declaration are appended in started-at fallback order (the order
 *    `steps` already carries) with no sequence edges.
 *  - `declaredSteps` null (absent/malformed/empty): nodes follow the
 *    started-at fallback order of `steps`; the started segment is the prefix of
 *    steps up to and including the LAST node whose status is not 'pending',
 *    and sequence edges join every consecutive pair inside that prefix, so
 *    never-started (always pending) steps trail unedged.
 *    Basis: pf stamps `step_started_at` on claim (transition.go ~293-309), so a
 *    step that never started is always pending; but a rewind
 *    (applyRewindRangeLocked) resets [target, gate) to 'pending' while keeping
 *    `step_started_at`, so pending steps can sit inside the started segment —
 *    testing pairs individually would wrongly break the chain around them.
 *  - Rewind edges: only tail `rewind` events whose gate AND target are both
 *    nodes, one edge per distinct gate→target pair. `on_fail` targets are
 *    not nodes in this model, so they can never be drawn. */
export function buildStepGraph(
  steps: { name: string; status: string }[],
  declaredSteps: { id: string }[] | null,
  events: { kind: string; gate?: string; target?: string }[],
): StepGraph {
  const statusByName = new Map(steps.map((step) => [step.name, step.status]));
  const sequenceEdges: [string, string][] = [];
  let nodes: { id: string; status: string }[];
  if (declaredSteps && declaredSteps.length > 0) {
    nodes = declaredSteps.map(({ id }) => ({ id, status: statusByName.get(id) ?? "pending" }));
    const declared = new Set(declaredSteps.map(({ id }) => id));
    for (const step of steps) {
      if (!declared.has(step.name)) nodes.push({ id: step.name, status: step.status });
    }
    for (let i = 1; i < declaredSteps.length; i++) {
      sequenceEdges.push([declaredSteps[i - 1].id, declaredSteps[i].id]);
    }
  } else {
    nodes = steps.map((step) => ({ id: step.name, status: step.status }));
    // Started segment = prefix up to and including the LAST non-pending node.
    // pf rewinds reset [target, gate) to 'pending' while keeping started_at, so
    // pending steps can sit inside the started segment; edge the whole prefix
    // and leave the trailing never-started steps unedged.
    let lastStarted = -1;
    for (let i = 0; i < nodes.length; i++) {
      if (nodes[i].status !== "pending") lastStarted = i;
    }
    for (let i = 1; i <= lastStarted; i++) {
      sequenceEdges.push([nodes[i - 1].id, nodes[i].id]);
    }
  }
  const nodeIds = new Set(nodes.map((node) => node.id));
  const rewindEdges: [string, string][] = [];
  const seenRewinds = new Set<string>();
  for (const event of events) {
    if (event.kind !== "rewind" || !event.gate || !event.target) continue;
    if (!nodeIds.has(event.gate) || !nodeIds.has(event.target)) continue;
    const key = `${event.gate}\u0000${event.target}`;
    if (seenRewinds.has(key)) continue;
    seenRewinds.add(key);
    rewindEdges.push([event.gate, event.target]);
  }
  return { nodes, sequenceEdges, rewindEdges };
}

/** Per-step timing model (pi#117). `durationSec` is null when the step
 *  shows no duration: a done step's duration is its started_at → the NEXT
 *  started step's started_at (null when nothing started after it), the
 *  in_progress step's is the live age since its started_at, and every other
 *  started status (failed, blocked, …) shows started_at only. Never-started
 *  steps carry no entry at all. Pure: no I/O, no throw — an unparseable
 *  started_at degrades that step to no timing. */
export interface StepTiming {
  startedAt: string;
  durationSec: number | null;
}

export function buildStepTimings(
  steps: { name: string; status: string; startedAt?: string }[],
  now: number,
): Map<string, StepTiming> {
  const timings = new Map<string, StepTiming>();
  for (let i = 0; i < steps.length; i++) {
    const step = steps[i];
    if (!step.startedAt) continue;
    const ownMs = Date.parse(step.startedAt);
    if (!Number.isFinite(ownMs)) continue;
    let durationSec: number | null = null;
    if (step.status === "in_progress") {
      durationSec = Math.max(0, Math.round((now - ownMs) / 1000));
    } else if (step.status === "done") {
      // steps arrive in execution order (started by started_at ascending),
      // so the next array entry, when started, is the next started step.
      const next = steps[i + 1];
      if (next?.startedAt) {
        const nextMs = Date.parse(next.startedAt);
        if (Number.isFinite(nextMs)) {
          const diffSec = Math.round((nextMs - ownMs) / 1000);
          if (diffSec >= 0) durationSec = diffSec;
        }
      }
    }
    timings.set(step.name, { startedAt: step.startedAt, durationSec });
  }
  return timings;
}

/** Live per-step timing for the current snapshot (pi#117): the
 *  in_progress age reads the clock at render — the module-level helper
 *  follows BackgroundTasksPanel.formatDuration's clock-at-render pattern —
 *  and the 3 s watch poll re-renders the panel, so the age stays live
 *  without a dedicated timer. */
function liveStepTimings(steps: { name: string; status: string; startedAt?: string }[]): Map<string, StepTiming> {
  return buildStepTimings(steps, Date.now());
}

/** The job's total elapsed time at render, the
 *  liveStepTimings clock-at-render pattern: the 3 s watch poll re-renders the
 *  panel, so it stays live without a dedicated timer. */
function liveElapsedSec(startMs: number): number {
  return (Date.now() - startMs) / 1000;
}

/** A status job shown before the first card read: the slug and status
 *  from the status poll (the needs-input marker included), no goal, no
 *  steps, no narration (the card renders none), and whether
 *  the card read is unavailable. */
function statusOnlyCard(job: PolyforgeJob, available: boolean): PolyforgeCard {
  return { job, steps: [], progress: { available, entries: [] } };
}

/** Event-line colours: the ruled tones; absent = neutral. */
const PROGRESS_TONE_COLORS: Record<PolyforgeProgressTone, string> = {
  accent: "var(--accent)",
  dim: "var(--text-dim)",
  red: "#dc2626",
  amber: "#d97706",
  green: "#16a34a",
};

/** A spawn block header's mark, from the block's own lifecycle lines;
 *  an older block with none carries no mark. */
const PROGRESS_BLOCK_STATUS_MARKS: Record<PolyforgeProgressBlockStatus, string> = {
  done: "✓",
  failed: "✕",
  needs_input: "?",
  stopped: "⏸",
  running: "▶",
  none: "",
};

/** One stream line's style: narration in the text colour (a `thinking`
 *  narration dim italic), an event line in its tone (neutral
 *  `var(--text-muted)`); a tool line is a card of its own.
 *  The Markdown body sets its own `color: var(--text)`, so a thinking line
 *  also rebinds `--text` to the dim colour for its subtree: the body's own
 *  colour (and its strong/heading mixes) resolve dim, not just the wrapper. */
function progressLineStyle(line: PolyforgeProgressLine): CSSProperties {
  if (line.kind === "event") return { color: line.tone ? PROGRESS_TONE_COLORS[line.tone] : "var(--text-muted)", fontFamily: "var(--font-mono, monospace)" };
  if (line.variant === "thinking") return { color: "var(--text-dim)", fontStyle: "italic", "--text": "var(--text-dim)" } as CSSProperties;
  return { color: "var(--text)" };
}

/** Narration Markdown two pixels under the chat's 14px: the
 *  progress area is a dense side panel. */
const PROGRESS_MARKDOWN_STYLE = { "--chat-font-size-offset": "-2px" } as CSSProperties;

/** A spawn block head's duration: the block's own lifecycle
 *  times when both exist, a running block's age at render (the 2 s poll
 *  re-renders it, liveElapsedSec's clock-at-render pattern), else none. */
function progressBlockDurationSec(block: PolyforgeProgressBlock): number | undefined {
  if (block.durationMs !== undefined) return Math.round(block.durationMs / 1000);
  if (block.status === "running" && block.startAt !== undefined) return Math.max(0, Math.round(liveElapsedSec(block.startAt)));
  return undefined;
}

/** Lines added per animation frame by the progress renderer (
 *  a large transcript is rendered in bounded chunks, never truncated). */
export const PROGRESS_RENDER_CHUNK = 200;

/**
 * The job detail's execution progress, whole-job,
 * full lines, spawn blocks and the open-at-bottom follow
 * and structured rendering:
 * a heading, the omitted-lines marker when a snapshot still carries one,
 * then the selected job's stream — every spawn of every step, oldest first,
 * inline in the detail's one scroll region (no overflowY of its own, pi#120).
 * Each entry renders by kind: narration through the
 * transcript's SafeMarkdownBody (paragraphs, lists, code; a `thinking` one
 * in the same Markdown, dim italic, never folded) — one over
 * MAX_MARKDOWN_CHARS skips Markdown and renders inline in full as plain
 * wrapped text (the guard's click-to-reveal box would be a per-entry gate
 * and a nested scroller here); a tool call as one
 * PolyforgeProgressToolCard on the transcript's ToolCallCard shell (header
 * from the poll's light fields, the full detail fetched only when that card
 * is expanded, at most once per call per selection, a failed read leaving
 * only that card unavailable); every other lifecycle event a restrained
 * marker row in its tone, wrapping in full, its raw kind and text on hover.
 * A snapshot renders the first PROGRESS_RENDER_CHUNK lines at once, then
 * appends the rest one chunk per animation frame, so a large stream stays
 * responsive without dropping a line (the measured bound); a
 * poll keeps the already-shown count, so it never re-chunks an unchanged
 * stream. The localized loading (only until the first read settles), empty
 * and unavailable states; a failed or degraded later read keeps the last
 * good snapshot without an error readout (usePolyforgeProgress). Mounted
 * only while the Jobs detail of a selected job renders, so deselecting or
 * closing the dock stops its reads.
 *
 * Spawn blocks: `progressBlocks` cuts the stream at every
 * `step_started` line in stream order; lines before the first stay leading
 * and unfolded. Every block — a lone one too — has a group
 * head (`aria-expanded`) carrying its step, spawn number, status mark,
 * duration (its lifecycle times; a running block's age; none without times)
 * and line count. The head absorbs the block's `step_started` and terminal
 * lines: neither renders as a body row, both texts and raw
 * kinds are on the head's title. Only the newest block starts expanded; a
 * user's toggle is kept per block until another job is selected or the
 * detail remounts, while a new spawn moves the default to the new block. No
 * line is dropped: leading lines plus every head count equal the snapshot's
 * length, and each block's body is its count minus the absorbed lines.
 * a heavy dispatch's queued/admitted lines right before its
 * `step_started` belong to its block (`first`) and open its body.
 *
 * Workflow contract v2 (change 14): the flow is the job card's
 * conversation. An `owner` line (an `owner_message` event: the owner's
 * words, verbatim) renders as the user's bubble on the right with its time
 * and sender; an `ask` line (an `awaiting_owner` event: the agent's
 * question) as a highlighted amber block — the question the owner answers,
 * in the flow where it was asked, rendered as Markdown like narration (the
 * step-ask question table as a table; one over MAX_MARKDOWN_CHARS plain
 * wrapped text); `delivered`, `intent` and
 * `blocked` events are status rows like the other lifecycle lines. The
 * owner's messages the route just accepted (`echoes`, the panel's) render
 * as bubbles after the stream until the event log's own line arrives. The
 * snapshot is the panel's (`usePolyforgeProgress` is called there, so the
 * composer under the scroll region reads the same stream).
 *
 * Follow (reversed at the owner's ruling):
 * `scrollRef` is the detail's one scroll region. `pinned` starts true for
 * every selection and remount; once the first snapshot with lines has
 * rendered (every chunk), the whole region sits at its bottom. A passive
 * scroll listener keeps `pinned` — whether the reader is at its bottom
 * (isProgressPinnedToBottom); loading, empty and unavailable states force
 * nothing. A new snapshot or chunk follows growth only while pinned. The
 * `↓ Latest` button (`[data-pf-detail-latest]`) floats over the region's
 * bottom-right whenever it is not at its bottom (overlay, no in-flow
 * height); one click jumps to the bottom immediately and restores
 * following. Folding never treats its own click as a scroll: collapsing
 * while pinned clamps the region to its new bottom and stays pinned,
 * expanding an older block leaves `scrollTop` untouched (so the reader
 * leaves the bottom, unpins, and the button appears). A tool card's
 * expand/collapse and its detail settling are folds too.
 */
export function PolyforgeDetailProgress({ wi, profile, running, snapshot, echoes, scrollRef }: {
  wi: string | null;
  profile?: string;
  running: boolean;
  snapshot: PolyforgeProgressSnapshot | null;
  echoes: readonly PolyforgeOwnerEcho[];
  scrollRef: RefObject<HTMLDivElement | null>;
}) {
  const { t, locale } = useI18n();
  const [visibleCount, setVisibleCount] = useState(PROGRESS_RENDER_CHUNK);
  const [folded, setFolded] = useState<Map<string, boolean>>(() => new Map());
  const [foldVersion, setFoldVersion] = useState(0);
  const [details, setDetails] = useState<Map<string, PolyforgeProgressToolDetailState>>(() => new Map());
  const detailsRef = useRef(details);
  detailsRef.current = details;
  const selection = useRef(wi);
  const pinned = useRef(true);
  const [atBottom, setAtBottom] = useState(true);
  const lines = snapshot?.lines ?? [];
  const layout = progressBlocks(lines);
  const lastBlockId = layout.blocks.length > 0 ? lines[layout.blocks[layout.blocks.length - 1].start]?.id ?? null : null;
  const streamed = Boolean(snapshot?.available && lines.length > 0);
  useEffect(() => {
    selection.current = wi;
    setVisibleCount(PROGRESS_RENDER_CHUNK);
    setFolded(new Map());
    setFoldVersion(0);
    setDetails(new Map());
    pinned.current = true;
    setAtBottom(true);
  }, [wi]);
  useEffect(() => {
    const total = snapshot?.lines.length ?? 0;
    if (visibleCount >= total) return;
    const frame = requestAnimationFrame(() => {
      setVisibleCount((current) => Math.min(total, current + PROGRESS_RENDER_CHUNK));
    });
    return () => cancelAnimationFrame(frame);
  }, [snapshot, visibleCount]);
  useEffect(() => {
    const region = scrollRef.current;
    if (!region) return;
    const onScroll = () => {
      const at = isProgressPinnedToBottom(region);
      pinned.current = at;
      setAtBottom(at);
    };
    region.addEventListener("scroll", onScroll, { passive: true });
    return () => region.removeEventListener("scroll", onScroll);
  }, [scrollRef]);
  const shownEchoes = pendingOwnerEchoes(echoes, lines);
  useLayoutEffect(() => {
    const region = scrollRef.current;
    if (region && streamed && pinned.current) region.scrollTop = region.scrollHeight;
  }, [snapshot, visibleCount, streamed, shownEchoes.length, scrollRef]);
  useLayoutEffect(() => {
    const region = scrollRef.current;
    if (region) setAtBottom(isProgressPinnedToBottom(region));
  }, [snapshot, visibleCount, foldVersion, scrollRef]);
  useLayoutEffect(() => {
    // A fold click is not a scroll: measure the position the toggle left
    // behind. A collapse at the bottom clamps to the new bottom (still
    // pinned); an expansion of an older block leaves scrollTop where it was
    // (no longer at the bottom, so it unpins).
    if (foldVersion === 0) return;
    const region = scrollRef.current;
    if (!region) return;
    const at = isProgressPinnedToBottom(region);
    pinned.current = at;
    setAtBottom(at);
  }, [foldVersion, scrollRef]);
  // One tool card's detail, read when that card is expanded:
  // a call already loading or loaded is never asked again; an unavailable one
  // is asked again on its next expansion. An answer for another selection
  // writes nothing.
  const loadDetail = useCallback((id: string) => {
    const held = detailsRef.current.get(id);
    if (!wi || (held && held.status !== "unavailable")) return;
    const asked = wi;
    const settle = (state: PolyforgeProgressToolDetailState) => {
      if (selection.current !== asked) return;
      setDetails((current) => new Map(current).set(id, state));
      setFoldVersion((current) => current + 1);
    };
    settle({ status: "loading" });
    void fetchProgressToolDetail(asked, id, profile).then(settle);
  }, [wi, profile]);
  // A tool card's expand/collapse is a fold like a block's: it
  // and its detail settling re-measure through foldVersion, so an expansion
  // that leaves the view off the bottom unpins (↓ Latest, no pull on the next
  // poll) and a collapse at the bottom stays pinned.
  const toggleTool = useCallback((id: string, open: boolean) => {
    if (open) loadDetail(id);
    setFoldVersion((current) => current + 1);
  }, [loadDetail]);
  const message = !wi || snapshot?.available === false ? t("polyforge.progress.unavailable")
    : !snapshot ? t("polyforge.loading")
      : snapshot.lines.length === 0 ? t("polyforge.progress.empty") : null;
  const chunkLimit = Math.max(PROGRESS_RENDER_CHUNK, visibleCount);
  const expanded = (start: number) => {
    const id = lines[start]?.id;
    return id && folded.has(id) ? folded.get(id)! : id === lastBlockId;
  };
  const toggle = (start: number) => {
    const id = lines[start]?.id;
    if (!id) return;
    const open = expanded(start);
    setFolded((current) => new Map(current).set(id, !open));
    setFoldVersion((current) => current + 1);
  };
  const jumpToLatest = () => {
    const region = scrollRef.current;
    if (region) region.scrollTop = region.scrollHeight;
    pinned.current = true;
    setAtBottom(true);
  };
  // a subagent entry's expand/collapse is a fold too; a
  // subagent is live only inside a running spawn block (one still marked
  // running in an ended block has stopped and never finishes) — a leading
  // line outside every block is live while the job runs.
  const foldSubagent = () => setFoldVersion((current) => current + 1);
  const liveAt = (index: number) => (index < layout.leading ? running : layout.blocks.some((block) => block.status === "running"
    && index >= (block.first ?? block.start) && index < (block.first ?? block.start) + block.count));
  const toolRow = (line: PolyforgeProgressLine, live: boolean): ReactNode => {
    const card = <PolyforgeProgressToolCard key={line.id} line={line} detail={details.get(line.id)} onToggle={toggleTool} />;
    if (!line.subagents?.length) return card;
    return (
      <div key={line.id} data-pf-progress-subagents="" style={{ display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}>
        {card}
        {line.subagents.map((run) => (
          <PolyforgeProgressSubagentEntry key={run.id} run={run} live={live} renderLine={(inner) => (inner.kind === "tool" ? toolRow(inner, live) : lineRow(inner, 0))} onToggle={foldSubagent} />
        ))}
      </div>
    );
  };
  const clock = (at: string | number | undefined) => {
    if (at === undefined) return undefined;
    const date = new Date(at);
    if (Number.isNaN(date.getTime())) return undefined;
    try {
      return new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(date);
    } catch {
      return formatProgressClock(at);
    }
  };
  // workflow contract v2 (change 14): the owner's bubble — the words
  // verbatim (plain text, never Markdown: they are the owner's own), the
  // sender and the time; an echo adds how far the delivery got.
  const ownerRow = (key: string, text: string, at: string | number | undefined, by: string | undefined, note?: string) => (
    <div key={key} data-pf-progress-kind="owner" style={{ display: "flex", flexDirection: "column", alignItems: "flex-end", minWidth: 0 }}>
      <div style={{ maxWidth: "85%", minWidth: 0, padding: "4px 8px", borderRadius: 8, background: "var(--user-bg)", color: "var(--text)", whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{text}</div>
      <div style={{ display: "flex", gap: 6, fontSize: 10, color: "var(--text-dim)" }}>
        <span data-pf-progress-owner-by="">{by ?? t("polyforge.chat.owner")}</span>
        {clock(at) && <span>{clock(at)}</span>}
        {note && <span data-pf-progress-owner-note="">{note}</span>}
      </div>
    </div>
  );
  const lineRow = (line: PolyforgeProgressLine, index: number): ReactNode => {
    if (index >= chunkLimit) return null;
    if (line.kind === "tool") {
      return toolRow(line, liveAt(index));
    }
    if (line.kind === "owner") return ownerRow(line.id, line.text, line.at, line.by);
    if (line.kind === "ask") {
      // the agent's question, highlighted where it was asked, its Markdown
      // (the step-ask question table, code, bold) rendered like narration
      return (
        <div key={line.id} data-pf-progress-kind="ask" title={line.event} style={{ display: "flex", flexDirection: "column", gap: 2, padding: "6px 8px", borderLeft: "3px solid #d97706", borderRadius: 4, background: "rgba(217, 119, 6, 0.10)", minWidth: 0 }}>
          <div style={{ display: "flex", gap: 6, color: "#d97706", fontWeight: 600, fontSize: 11 }}>
            <span aria-hidden="true">?</span>
            <span>{t("polyforge.eventKind.awaiting_owner")}</span>
            {clock(line.at) && <span style={{ fontWeight: 400, color: "var(--text-dim)" }}>{clock(line.at)}</span>}
          </div>
          <div style={{ minWidth: 0, color: "var(--text)", ...PROGRESS_MARKDOWN_STYLE }}>
            {line.text.length > MAX_MARKDOWN_CHARS
              ? <div style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{line.text}</div>
              : <SafeMarkdownBody>{line.text}</SafeMarkdownBody>}
          </div>
        </div>
      );
    }
    if (line.kind === "narration") {
      return (
        <div key={line.id} data-pf-progress-kind="narration" data-pf-progress-variant={line.variant} style={{ minWidth: 0, ...PROGRESS_MARKDOWN_STYLE, ...progressLineStyle(line) }}>
          {line.text.length > MAX_MARKDOWN_CHARS
            ? <div style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{line.text}</div>
            : <SafeMarkdownBody>{line.text}</SafeMarkdownBody>}
        </div>
      );
    }
    return (
      <div
        key={line.id}
        data-pf-progress-kind={line.kind}
        data-pf-progress-tone={line.tone ?? "neutral"}
        title={line.event ? `${line.event}\n${line.text}` : line.text}
        style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere", minWidth: 0, ...progressLineStyle(line) }}
      >
        {line.text}
      </div>
    );
  };
  const blockBody = (block: PolyforgeProgressBlock) => {
    const first = block.first ?? block.start;
    return lines.slice(first, first + block.count)
      .map((line, offset) => (first + offset === block.start || first + offset === block.terminal ? null : lineRow(line, first + offset)));
  };
  return (
    <div data-pf-progress="" style={{ padding: "6px 12px", borderBottom: "1px solid var(--border)", flexShrink: 0, display: "flex", flexDirection: "column", gap: 4, fontSize: 11, lineHeight: "16px", minWidth: 0 }}>
      <span style={{ color: "var(--text-dim)" }}>{t("polyforge.progress.title")}</span>
      {!snapshot || message !== null ? (
        <span style={{ color: "var(--text-dim)" }}>{message}</span>
      ) : (
        <>
          {snapshot.omitted > 0 && (
            <span data-pf-progress-omitted="" style={{ color: "var(--text-dim)" }}>{t("polyforge.progress.omitted", { count: snapshot.omitted })}</span>
          )}
          {lines.slice(0, layout.leading).map((line, index) => lineRow(line, index))}
          {layout.blocks.map((block) => {
            if (block.start >= chunkLimit) return null;
            const open = expanded(block.start);
            const mark = PROGRESS_BLOCK_STATUS_MARKS[block.status];
            const durationSec = progressBlockDurationSec(block);
            const header = `${open ? "▾" : "▸"} ${block.step ?? ""}${block.spawn ? ` #${block.spawn}` : ""}${mark ? ` ${mark}` : ""}${durationSec !== undefined ? ` ${formatCardDuration(durationSec)}` : ""} · ${t("polyforge.progress.lines", { count: block.count })}`;
            const absorbed = [lines[block.start], block.terminal !== undefined ? lines[block.terminal] : undefined]
              .filter((line): line is PolyforgeProgressLine => Boolean(line))
              .map((line) => `${line.event ?? line.kind}\n${line.text}`).join("\n\n");
            return (
              <div key={`block-${lines[block.start].id}`} data-pf-progress-block="" data-pf-progress-block-status={block.status} data-pf-progress-block-count={block.count} style={{ display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}>
                <button
                  type="button"
                  data-pf-progress-block-toggle=""
                  data-pf-progress-block-step={block.step}
                  data-pf-progress-block-spawn={block.spawn}
                  aria-expanded={open}
                  title={absorbed}
                  onClick={() => toggle(block.start)}
                  style={{ alignSelf: "flex-start", padding: 0, background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", font: "inherit", fontFamily: "var(--font-mono, monospace)", textAlign: "left", whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
                >
                  {header}
                </button>
                {open && blockBody(block)}
              </div>
            );
          })}
        </>
      )}
      {/* the owner's accepted messages, after the stream — an empty or
          still-loading stream too (a message at a step's very start) */}
      {shownEchoes.map((echo) => ownerRow(`echo-${echo.at}`, echo.text, echo.at, undefined,
        echo.ack.delivered ? t("polyforge.chat.echo.delivered")
            : echo.ack.disposition === "queued" ? t("polyforge.chat.echo.queued")
              : echo.ack.disposition === "resume" ? t("polyforge.chat.echo.resume")
                : echo.ack.disposition === "wake" ? t("polyforge.chat.echo.wake")
                  : t("polyforge.chat.echo.pending")))}
      {!atBottom && (
        <div data-pf-detail-latest="" style={{ position: "sticky", bottom: 8, alignSelf: "flex-end", width: 0, height: 0, marginTop: -2, zIndex: 1 }}>
          <button
            type="button"
            onClick={jumpToLatest}
            title={t("polyforge.progress.latest")}
            aria-label={t("polyforge.progress.latest")}
            style={{ position: "absolute", right: 0, bottom: 0, whiteSpace: "nowrap", padding: "3px 8px", borderRadius: 10, border: "1px solid var(--border)", background: "var(--bg-panel)", color: "var(--text)", fontSize: 11, lineHeight: "16px", cursor: "pointer", boxShadow: "0 1px 4px rgba(0,0,0,0.25)" }}
          >
            {t("polyforge.progress.latest")}
          </button>
        </div>
      )}
    </div>
  );
}

/** (owner's intentional change): a job's status shows on the
 *  card frame's colour — 1px width unchanged, the four loud states get their
 *  own colour, the three quiet ones one shared gray and an unknown status keeps
 *  the shared block default (`var(--border)`). Existing tokens and the panel's
 *  own status colours only; no new colour value. */
const JOB_STATUS_BORDER_COLORS: Record<string, string> = {
  running: "var(--accent)",
  needs_input: "#d97706",
  completed: "#16a34a",
  failed: "#dc2626",
  queued: "var(--text-dim)",
  paused: "var(--text-dim)",
  cancelled: "var(--text-dim)",
};

/** The card frame colour for a job status; an unknown status keeps the block
 *  default. */
export function jobCardBorderColor(status: string): string {
  return JOB_STATUS_BORDER_COLORS[status] ?? "var(--border)";
}

/** Job card width: 180px, decoupled from the Tasks grid's own 150px column
 *  minimum (the owner's intentional change of the earlier
 *  shared 150px), fixed rather than stretched. */
const JOB_CARD_WIDTH = 180;

/** an attention card (a `needs_input` job's, or a work item
 *  only the decision feed still names) is amber as a whole: the amber tint
 *  over the panel (stronger when selected) inside the `#d97706` frame, its
 *  text the theme's own `var(--text)` / `var(--text-muted)`, legible over the
 *  light tint in both themes. The existing amber only; no new token. */
const ATTENTION_AMBER = "#d97706";
const ATTENTION_TINT = "rgba(217, 119, 6, 0.16)";
const ATTENTION_TINT_SELECTED = "rgba(217, 119, 6, 0.30)";

/** the marker text of a feed-only attention card — the
 *  entry's class label (its wire phrase for a class without one), then
 *  ` · <source>` when the entry shows a source beside its class. */
function attentionCardLabel(decision: PolyforgeDecision, t: (key: string) => string): string {
  const label = isKnownDecisionClass(decision.class) ? t(`polyforge.decisions.class.${decision.class}`) : (decision.phrase ?? decision.class);
  const source = decisionSourceShown(decision);
  return source ? `${label} · ${source}` : label;
}

/**
 * the card of a work item that needs attention but that the
 * daemon no longer holds (blocked, failed, submit_rejected, … — an open
 * entry `droppedItemAttention` keeps, its newest). The live card's 180px
 * amber block (tint + `#d97706` frame) with the slug, the Cell when not the
 * default one, and the solid amber marker naming what happened; no goal or
 * step line (the feed carries neither). A click selects the item — its job
 * detail, with the park cause above the input box (nothing
 * else opens); a second click deselects. It lasts while the entry is open and
 * the item has no live job: a wake brings the live card back in its place, a
 * dismiss or a resolving cancel removes it on the next feed read.
 * under the first row, the entry's reason (its phrase, for a
 * known class — an unknown class's label already is its phrase) and a
 * right-aligned 忽略 that dismisses every open entry of the item (`openIds`,
 * newest first) in one post, kept off the card's centre; a refusal keeps the
 * card and shows pf's text on it.
 */
const PolyforgeAttentionCard = memo(function PolyforgeAttentionCard({
  decision,
  selected,
  onSelect,
  openIds,
  onDismiss,
}: {
  decision: PolyforgeDecision;
  selected: boolean;
  onSelect: (wi: string | null, profile?: string) => void;
  /** Every open entry of the item, newest first: what 忽略 dismisses. */
  openIds: number[];
  onDismiss: (ids: number[]) => Promise<void>;
}) {
  const { t } = useI18n();
  const wi = decision.wi ?? "";
  const profile = decision.profile ?? "default";
  const slug = decision.slug ?? wi;
  const label = attentionCardLabel(decision, t);
  const reason = isKnownDecisionClass(decision.class) ? decision.phrase : undefined;
  const toggle = () => {
    if (selected) {
      onSelect(null);
      return;
    }
    onSelect(wi, profile);
  };
  return (
    <div
      data-pf-card-wi={wi}
      data-pf-card-attention=""
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      aria-label={`${slug} · ${label}`}
      onClick={toggle}
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          toggle();
        }
      }}
      style={{
        ...POLYFORGE_BLOCK_STYLE,
        borderColor: ATTENTION_AMBER,
        flex: `0 0 ${JOB_CARD_WIDTH}px`,
        display: "flex", flexDirection: "column", gap: 4, minWidth: 0,
        padding: "4px 8px",
        cursor: "pointer", fontSize: 12,
        background: selected ? ATTENTION_TINT_SELECTED : ATTENTION_TINT,
      }}
    >
      <div style={{ display: "flex", alignItems: "flex-start", gap: 6, minWidth: 0 }}>
        <div style={{ flex: "1 1 0", minWidth: 0, color: "var(--text)", fontWeight: 600, overflowWrap: "anywhere", lineHeight: "16px" }}>{slug}</div>
        {profile !== "default" && (
          <div data-pf-job-profile={profile} title={profile} style={{ flex: "0 0 auto", color: "var(--text-dim)", fontSize: 11, lineHeight: "16px", whiteSpace: "nowrap" }}>{profile}</div>
        )}
        <div data-pf-attention-marker={decision.class} title={decision.phrase ?? label} style={{ flex: "0 1 auto", minWidth: 0, maxWidth: "60%", padding: "0 5px", borderRadius: 4, border: "1px solid #d97706", background: ATTENTION_AMBER, color: "#fff", fontWeight: 600, fontSize: 11, lineHeight: "14px", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
          {label}
        </div>
      </div>
      <PolyforgeEntriesDismiss reason={reason} openIds={openIds} onDismiss={onDismiss} />
    </div>
  );
});

/**
 * the reason line and 忽略 a card shows for the entries it
 * carries — a feed-only attention card (every open entry of its item) and a
 * live job's card (the entries the Polyforge button counts for that item).
 * The reason (two-line clamp, the full text as its title), a refusal line
 * (pf's text) and the right-aligned 忽略 in its own bottom row, kept off the
 * card's centre; one click dismisses every id in `openIds` (newest first) in
 * one post and never reaches the card's toggle.
 */
function PolyforgeEntriesDismiss({ reason, openIds, onDismiss }: { reason?: string; openIds: number[]; onDismiss: (ids: number[]) => Promise<void> }) {
  const { t } = useI18n();
  const [dismissing, setDismissing] = useState(false);
  const [dismissError, setDismissError] = useState<string | null>(null);
  const dismiss = (event: ReactMouseEvent) => {
    event.stopPropagation();
    if (dismissing || openIds.length === 0) return;
    setDismissing(true);
    setDismissError(null);
    onDismiss(openIds)
      .catch((error: unknown) => setDismissError(error instanceof Error ? error.message : String(error)))
      .finally(() => setDismissing(false));
  };
  return (
    <>
      {reason && (
        <div data-pf-attention-reason="" title={reason} style={{ color: "var(--text-muted)", fontSize: 11, lineHeight: "14px", overflowWrap: "anywhere", display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>
          {reason}
        </div>
      )}
      {dismissError !== null && (
        <div data-pf-attention-dismiss-error="" style={{ color: "#dc2626", fontSize: 11, lineHeight: "14px", overflowWrap: "anywhere" }}>
          {t("polyforge.decisions.dismissFailed", { error: dismissError })}
        </div>
      )}
      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <button
          type="button"
          data-pf-attention-dismiss=""
          disabled={dismissing}
          onClick={dismiss}
          style={{ flexShrink: 0, padding: "2px 8px", fontSize: 11, border: "1px solid var(--border)", borderRadius: 4, background: "none", color: "var(--text-muted)", cursor: dismissing ? "default" : "pointer" }}
        >
          {t("polyforge.decisions.dismiss")}
        </button>
      </div>
    </>
  );
}

/**
 * One Jobs card (compact card) in the shared
 * block style, one fixed-width item of the single card row (a
 * grid cell until then): the slug (wrapping) with
 * the small status label at the right end of its row, for a
 * `needs_input` job the emphasized 等待输入 marker there instead of the dim
 * label; the truncated goal under them (at most 2 lines). No narration on the
 * card (removed the 32px scroller added; the
 * assistant narration stays in the job detail's execution progress and
 * summary). No step information on the card: the DAG, per-step durations and
 * reruns, model and step/idle age live in the job detail.
 * Keyed by wi in the stream and memoized, so a poll updates the same DOM node
 * (a trusted click never lands on a replaced one). Clicking (or Enter/Space)
 * toggles the existing slug selection and its detail. A status-only stand-in
 * (before the first card read) has no select handler and takes no pointer:
 * the first card read may reorder the stream once (server
 * start order replaces `pf ops status` order), and a click in that window
 * could otherwise land on a neighbouring job.
 *
 * (owner's intentional change, not a regression): the card is
 * denser — a 4px outer column gap, explicit 16px lines for the slug row and
 * the goal span (the inherited 18px inflated them), the needs-input badge at
 * padding 0 5px / line-height 14px and the dim status label at 16px, plus the
 * shared block padding 6px 8px. A 1-line slug + 2-line goal card was 100px.
 *
 * (owner's intentional change, reversing the earlier
 * narration): the card-only vertical padding drops to 4px (left/right stay
 * 8px) and the slug/goal gap to 0, so a card with a goal is 42px + its slug
 * row — 58px with a 1-line slug, 74px with a 2-line one. A `running` job's
 * age in whole minutes comes from the card's existing earliest `job_started`,
 * advanced by a local render clock and no request; shows that
 * age alone (`23m`) as the visible status text (the full `运行中 23m` stays in
 * the title/aria-label).
 *
 * (owner's intentional change): the frame's 1px colour is the
 * job's status (see `JOB_STATUS_BORDER_COLORS`); a `running` card shows the
 * duration alone (`23m`) and falls back to the localized status word when the
 * start time is unknown; every other status keeps its status text. The full
 * status text (`运行中 23m`) stays in the card's `aria-label` and in the
 * visible status element's `title`.
 *
 * (owner's intentional change): a `needs_input` card is an
 * attention card — the amber tint background (stronger when selected) inside
 * its amber frame, `data-pf-card-attention`, and the 等待输入 marker solid
 * amber with white text so it reads over the tint. An answered job's next
 * status read is `running`, which drops the tint.
 *
 * a card whose item has entries the Polyforge button counts
 * (`entries`: every open entry of the item but a waiting job's needs_input
 * one, which is its question in the composer; a blocked entry included)
 * shows the newest one's reason (its phrase, else its class label) and 忽略
 * as its last row, dismissing all of them in one post. Frame, background and
 * name are unchanged — it stays a live card — and the row goes on the next
 * feed read after the dismiss. A pending stand-in shows none.
 */
const PolyforgeJobCard = memo(function PolyforgeJobCard({
  card,
  verifyQueue,
  pending,
  selected,
  titleLength,
  onSelect,
  entries,
  onDismiss,
}: {
  card: PolyforgeCard;
  /** the job's queued verification, from the status
   *  snapshot the queue counter reads (absent when not queued). */
  verifyQueue?: PolyforgeVerifyQueue;
  /** A status-only stand-in before the first card read: no card identity yet. */
  pending: boolean;
  selected: boolean;
  titleLength: number;
  onSelect: (wi: string | null, profile?: string) => void;
  /** the item's entries the Polyforge button counts (newest
   *  first) — its reason line and what 忽略 dismisses. */
  entries: PolyforgeDecision[];
  onDismiss: (ids: number[]) => Promise<void>;
}) {
  const { t } = useI18n();
  const { job, goal } = card;
  const jobStatusLabel = (status: string) => (JOB_STATUS_KEYS.has(status) ? t(`polyforge.jobStatus.${status}`) : status);
  const needsInput = job.status === "needs_input";
  const running = job.status === "running";
  // a local render clock for the running age — it reads
  // `Date.now()` and fetches nothing (the Tasks blocks' pattern); a 30s tick
  // keeps the label within half a minute of the wall clock.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!running) return;
    const clock = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(clock);
  }, [running]);
  const runningMinutes = running ? cardRunningMinutes(card.startedAt, now) : null;
  const baseStatusLabel = jobStatusLabel(job.status);
  // only a running job carries the age; a missing/unparsable
  // `startedAt` keeps the bare status word. The full text (status + age) stays
  // in the aria-label and the status element's title, while a running card's
  // visible text is the duration alone — the frame colour already says it runs.
  const fullStatusLabel = runningMinutes === null ? baseStatusLabel : `${baseStatusLabel} ${runningMinutes}m`;
  const visibleStatusLabel = runningMinutes === null ? baseStatusLabel : `${runningMinutes}m`;
  // the step session's context meter, the chat header's text;
  // none without a window (the status poll carries it, no fetch of its own).
  const contextMeter = pending ? null : polyforgeContextMeterText(job.context);
  const contextTone = polyforgeContextMeterTone(job.context);

  const toggle = () => onSelect(selected ? null : job.slug, job.profile);
  return (
    <div
      data-pf-card={pending ? undefined : ""}
      data-pf-card-wi={pending ? undefined : polyforgeCardKey(card)}
      data-pf-card-attention={needsInput ? "" : undefined}
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      aria-label={`${job.slug} · ${fullStatusLabel}`}
      aria-disabled={pending || undefined}
      onClick={pending ? undefined : toggle}
      onKeyDown={pending ? undefined : (event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          toggle();
        }
      }}
      style={{
        ...POLYFORGE_BLOCK_STYLE,
        // the shared block keeps its 1px width; only the colour
        // follows the job's status.
        borderColor: jobCardBorderColor(job.status),
        // a fixed 180px cell of the one horizontally scrolling
        // row — a flex basis, so a narrow row scrolls instead of squeezing it.
        flex: `0 0 ${JOB_CARD_WIDTH}px`,
        display: "flex", flexDirection: "column", gap: 4, minWidth: 0,
        // the card-only vertical padding is 4px (the shared
        // block keeps 6px, so the Tasks blocks are unchanged).
        padding: "4px 8px",
        cursor: pending ? "default" : "pointer", pointerEvents: pending ? "none" : undefined, fontSize: 12,
        // a needs_input card is an attention card, amber as a
        // whole (its frame is already the amber status colour).
        background: needsInput
          ? (selected ? ATTENTION_TINT_SELECTED : ATTENTION_TINT)
          : selected ? "var(--bg-selected)" : POLYFORGE_BLOCK_STYLE.background,
      }}
    >
      {/* Title: the slug (wraps, never clipped) and the status at the right
          end of its row — the small dim label, or for a job awaiting a human
          answer the emphasized marker; the truncated goal under them (at most
          2 lines). The status ellipsizes before it squeezes the slug. */}
      <div style={{ display: "flex", flexDirection: "column", gap: 0, minWidth: 0 }}>
        <div style={{ display: "flex", alignItems: "flex-start", gap: 6, minWidth: 0 }}>
          <div style={{ flex: "1 1 0", minWidth: 0, color: "var(--text)", fontWeight: 600, overflowWrap: "anywhere", lineHeight: "16px" }}>{job.slug}</div>
          {/* the row's Cell (equal literals on two Cells stay distinguishable) */}
          {job.profile && (
            <div data-pf-job-profile={job.profile} title={job.profile} style={{ flex: "0 0 auto", color: "var(--text-dim)", fontSize: 11, lineHeight: "16px", whiteSpace: "nowrap" }}>{job.profile}</div>
          )}
          {needsInput ? (
            <div data-pf-needs-input="" title={fullStatusLabel} style={{ flex: "0 1 auto", minWidth: 0, maxWidth: "60%", padding: "0 5px", borderRadius: 4, border: "1px solid #d97706", background: ATTENTION_AMBER, color: "#fff", fontWeight: 600, fontSize: 11, lineHeight: "14px", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
              {visibleStatusLabel}
            </div>
          ) : (
            <div title={fullStatusLabel} style={{ flex: "0 1 auto", minWidth: 0, maxWidth: "60%", fontSize: 11, lineHeight: "16px", color: "var(--text-dim)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{visibleStatusLabel}</div>
          )}
        </div>
        {/* the running step session's context usage, under the
            status at the row's right end (`42% / 1.0M`, `? / 1.0M`). */}
        {contextMeter && job.context && (
          <div style={{ display: "flex", justifyContent: "flex-end", minWidth: 0 }}>
            <span data-pf-context-meter="" title={contextMeterTitle(job.context) || undefined} style={{ fontSize: 11, lineHeight: "14px", whiteSpace: "nowrap", fontVariantNumeric: "tabular-nums", color: contextTone ? CONTEXT_METER_COLORS[contextTone] : "var(--text-dim)" }}>{contextMeter}</span>
          </div>
        )}
        {goal && (
          <span title={goal} style={{ color: "var(--text-muted)", overflowWrap: "anywhere", lineHeight: "16px", display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{truncateCardTitle(goal, titleLength)}</span>
        )}
      </div>
      {/* a job whose verification waits in the heavy-run
          admission queue names its step with the queue position, then the
          gate holding it; the hover is the `pf ops status` VERIFY face. */}
      {verifyQueue && (
        <div data-pf-verify-queue="" title={verifyQueueFace(verifyQueue)} style={{ display: "flex", flexDirection: "column", minWidth: 0, fontSize: 11, lineHeight: "14px" }}>
          <span style={{ color: "var(--text)", fontWeight: 600, overflowWrap: "anywhere" }}>{t("polyforge.verifyQueued", { position: verifyQueue.position })}</span>
          <span style={{ color: "var(--text-dim)", overflowWrap: "anywhere", display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{verifyQueue.reason}</span>
        </div>
      )}
      {/* the item's counted entries — the newest one's reason and 忽略. */}
      {!pending && entries.length > 0 && <PolyforgeEntriesDismiss reason={entries[0].phrase ?? attentionCardLabel(entries[0], t)} openIds={entries.map((e) => e.id)} onDismiss={onDismiss} />}
    </div>
  );
});

/** the chat header's meter colours (above 70 amber, above 90 red). */
const CONTEXT_METER_COLORS = { amber: "rgba(234,179,8,0.95)", red: "#ef4444" } as const;

/** the hover of a job's context meter — the model and when
 *  the count was taken. */
function contextMeterTitle(context: PolyforgeContextUsage): string {
  return [context.model, context.asOf].filter(Boolean).join(" · ");
}

/** Work's sub-tab badges' accessible-name keys. */
const WORK_TAB_BADGES = { jobs: "polyforge.work.jobsRunning", tasks: "polyforge.work.tasksRunning", items: "polyforge.work.itemsRunning" } as const;

/** The ≤640px Work rail's 16px icons, in the panel's 24-viewBox stroke style. */
const workTabIcon = (paths: ReactNode) => (
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    {paths}
  </svg>
);
const WORK_TAB_ICONS = {
  // 作业: a stack of running jobs.
  jobs: workTabIcon(<><path d="m12 3 9 5-9 5-9-5z" /><path d="m3 13 9 5 9-5" /></>),
  // 任务: a checked box.
  tasks: workTabIcon(<><rect x="4" y="4" width="16" height="16" rx="2" /><path d="m9 12 2 2 4-4" /></>),
  // 工单列表: a bulleted list.
  items: workTabIcon(<><path d="M9 6h11" /><path d="M9 12h11" /><path d="M9 18h11" /><path d="M4 6h.01" /><path d="M4 12h.01" /><path d="M4 18h.01" /></>),
  // 待集成: branches merging into one line.
  integrate: workTabIcon(<><circle cx="6" cy="5" r="2" /><circle cx="18" cy="5" r="2" /><circle cx="12" cy="19" r="2" /><path d="M6 7v2a3 3 0 0 0 3 3h6a3 3 0 0 0 3-3V7" /><path d="M12 12v5" /></>),
} as const;

/** a queued verification as the `pf ops status` VERIFY
 *  column renders it (`queued #2: <reason>`). Pure. */
export function verifyQueueFace(queue: PolyforgeVerifyQueue): string {
  return `queued #${queue.position}: ${queue.reason}`;
}

/** the 工单列表 re-read key — `queue.running` and the count of
 *  terminal (completed/failed/cancelled) jobs in the status snapshot; null
 *  without a snapshot (no re-read). Pure. */
export function workItemCountKey(opsStatus: PolyforgeOpsStatus | null): string | null {
  if (!opsStatus) return null;
  const terminal = opsStatus.jobs.filter((job) => job.status === "completed" || job.status === "failed" || job.status === "cancelled").length;
  return `${opsStatus.queue.running}:${terminal}`;
}

export function PolyforgePanel({
  opsStatus,
  opsError,
  cards,
  cardsError,
  watchStatus,
  selectedWi,
  selectedProfile,
  onSelect,
  onClose,
  onRefresh,
  activeTab,
  onActiveTab,
  workTab,
  onWorkTab,
  workItemsRunning,
  onWorkItemsRunningChange,
  memoryCwd,
  messagesSurface,
  decisionsSurface,
  showPin,
  pinned,
  onTogglePin,
}: {
  opsStatus: PolyforgeOpsStatus | null;
  opsError: string | null;
  /** the Jobs-tab card stream (null until its first good read). */
  cards: PolyforgeCard[] | null;
  cardsError: string | null;
  watchStatus: PolyforgeWatchStatus | null;
  selectedWi: string | null;
  /** the selected job's Cell (WorkRef.profile); undefined =
   *  the selection names no Cell (the first matching job is shown). */
  selectedProfile?: string;
  onSelect: (wi: string | null, profile?: string) => void;
  onClose: () => void;
  // May return the status fetch's settle promise (usePolyforge.refreshStatus
  // does); the manual-refresh feedback (pi#113) awaits it.
  onRefresh: () => void | Promise<void>;
  activeTab: PolyforgeTab;
  onActiveTab: (tab: PolyforgeTab) => void;
  /** Work's selected sub-tab (page memory, held by the hook). */
  workTab: PolyforgeWorkTab;
  onWorkTab: (tab: PolyforgeWorkTab) => void;
  /** the 工单列表 badge — the last list read's running count
   *  (null until the first read), held by the hook, reported by the surface. */
  workItemsRunning: number | null;
  onWorkItemsRunningChange: (count: number) => void;
  /** The session cwd the Memory tab scopes its project dropdown to. */
  memoryCwd: string | null;
  /** the desktop dock's pin control (absent on the mobile sheet). */
  showPin: boolean;
  pinned: boolean;
  onTogglePin: () => void;
  /** the decision feed (the hook's, riding the status read),
   *  dismiss, and the hook's request to open a task in Work › Tasks (a task
   *  entry's target from the walk). */
  decisionsSurface: {
    feed: PolyforgeDecisionFeed | null;
    onDismiss: (ids: number[]) => Promise<void>;
    taskRequest: { id: string; seq: number } | null;
    /** The request was handed to the Tasks surface: the hook drops it, so a
     *  dock reopen does not reopen the task (PR #860 minor 1). */
    onTaskRequestHandled: () => void;
  };
  messagesSurface: {
    pfPresent: boolean;
    capable: boolean;
    reason: string | null;
    conversations: PolyforgeConversation[];
    contacts: PolyforgeContact[];
    groups: PolyforgeProjectGroup[];
    membershipAdmin: boolean | null;
    users: PolyforgeContact[];
    onChangeMembership: (input: { action: "add" | "remove"; project: string; userId: string }) => Promise<void>;
    error: string | null;
    selectedConversation: string | null;
    onSelectConversation: (id: string | null) => void;
    selectedContact: PolyforgeContact | null;
    onSelectContact: (contact: PolyforgeContact) => void;
    messages: PolyforgeMessage[];
    initialPageLoaded: boolean;
    blocked: boolean;
    loadingOlder: boolean;
    onLoadOlder: () => void;
    onAcknowledge: () => void;
    onSend: (input: { to: string; text: string; aboutWi?: string; replyTo?: string }) => Promise<unknown>;
    onAnalyze: PolyforgeMessagesProps["onAnalyze"];
    analyses: PolyforgeMessagesProps["analyses"];
    onOpenAnchor: (ref: string) => void;
    unreadTotal: number;
    /** the 「@」 action's click-time fill-target binding
     *  (AppShell's focused composer snapshot), passed through unchanged. */
    bindPromptTarget: PolyforgeMessagesProps["bindPromptTarget"];
    onPrompt: PolyforgeMessagesProps["onPrompt"];
    onPromptBusyChange: PolyforgeMessagesProps["onPromptBusyChange"];
  };
}) {
  const { t, locale } = useI18n();
  const { titleLength } = usePolyforgePanelPreferences();
  // the Work sub-tab controls follow the viewport, not the
  // dock kind — a 340px desktop dock keeps the 72px text rail, ≤640px gets
  // the 40px icon rail, both on the right of the main area.
  const isMobile = useIsMobile();
  const itemCountKey = workItemCountKey(opsStatus);
  // Work's lower, full-width part shows while there is a detail
  // to show: Jobs with a status snapshot (the selected job, else the
  // select-a-job hint); Tasks and 工单列表 while their surface reports an open
  // detail, which it renders into the lower part's slot through a portal.
  const [workDetailSlot, setWorkDetailSlot] = useState<HTMLDivElement | null>(null);
  const [subDetailOpen, setSubDetailOpen] = useState(false);
  const renderWorkDetail = useCallback((detail: ReactNode) => (workDetailSlot ? createPortal(detail, workDetailSlot) : null), [workDetailSlot]);
  const workDetailOpen = workTab === "jobs" ? Boolean(opsStatus) : subDetailOpen;

  // an analysis card's task link switches to Work › Tasks and
  // hands that sub-tab the one task id to open (its existing detail read);
  // the Tasks surface clears it once handled. Component memory only.
  const [taskToOpen, setTaskToOpen] = useState<string | null>(null);
  const openTask = useCallback((taskId: string) => {
    setTaskToOpen(taskId);
    onWorkTab("tasks");
    onActiveTab("work");
  }, [onActiveTab, onWorkTab]);
  const clearTaskToOpen = useCallback(() => setTaskToOpen(null), []);
  // the walk's task target reaches the Tasks surface through
  // the hand-off above.
  const taskRequest = decisionsSurface.taskRequest;
  const onTaskRequestHandled = decisionsSurface.onTaskRequestHandled;
  useEffect(() => {
    if (!taskRequest) return;
    setTaskToOpen(taskRequest.id);
    onTaskRequestHandled();
  }, [taskRequest, onTaskRequestHandled]);

  // Manual-refresh in-flight feedback (pi#113): the icon spins and the button
  // disables until the status fetch settles, floored at ~400 ms so a fast
  // response still reads as feedback (an explicit UX floor, not
  // synchronization). Failure settles the same way — the panel's existing
  // error text is the only error signal. Background polls call the hook's
  // refreshStatus directly and never touch this state.
  const [refreshBusy, setRefreshBusy] = useState(false);
  const handleRefresh = useCallback(() => {
    if (refreshBusy) return;
    setRefreshBusy(true);
    const settled = Promise.resolve(onRefresh()).catch(() => undefined);
    const floor = new Promise<void>((resolve) => { setTimeout(resolve, 400); });
    void Promise.all([settled, floor]).then(() => setRefreshBusy(false));
  }, [refreshBusy, onRefresh]);

  const jobStatusLabel = useCallback((status: string) => (
    JOB_STATUS_KEYS.has(status) ? t(`polyforge.jobStatus.${status}`) : status
  ), [t]);
  const workflowStatusLabel = useCallback((status: string) => (
    WORKFLOW_STATUS_KEYS.has(status) ? t(`polyforge.workflowStatus.${status}`) : status
  ), [t]);
  const stepStatusLabel = useCallback((status: string) => (
    STEP_STATUS_KEYS.has(status) ? t(`polyforge.stepStatus.${status}`) : status
  ), [t]);
  const formatEventTime = useCallback((at: string) => {
    const date = new Date(at);
    if (Number.isNaN(date.getTime())) return at;
    try {
      return new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(date);
    } catch {
      return at;
    }
  }, [locale]);

  // Step flow graph (pi#114): derived from the sanitized watch snapshot.
  // Null exactly when watchStatus is null (the detail section's loading state).
  const graph = watchStatus
    ? buildStepGraph(watchStatus.steps, watchStatus.declared_steps, watchStatus.events)
    : null;

  // pi#117 facts — the tool-call count, recent files and the chat origin —
  // and the pi#119 summarize narration, all inside the summary block since
  // (the command row is gone). Display-only, each absent piece
  // renders nothing (no placeholder). Every field is already absent when
  // missing or malformed server-side (the sanitizer guarantees it); the 3 s
  // watch poll refreshes the summary age — no client-side ticking between
  // polls (a non-goal).
  const progress = watchStatus?.progress;
  const origin = watchStatus?.origin;
  const summary = watchStatus?.summary;
  const summaryAgeSec = watchStatus?.summary_age_sec;
  const summaryError = watchStatus?.summary_error;
  const summaryCapped = watchStatus?.summary_capped;
  const summaryVisible = Boolean(summary || summaryError || summaryCapped);
  // The in_progress live age is computed at render from `now`; the watch
  // poll re-renders every 3s while the panel is open with a selection, so the
  // age stays live without a dedicated timer.
  const stepTimings = watchStatus ? liveStepTimings(watchStatus.steps) : null;
  const originHasText = Boolean(origin?.owner);
  const detailInfoVisible = progress?.tool_calls != null
    || Boolean(progress?.recent_files)
    || originHasText;

  // Until the first card read lands, the status jobs render as line-1-only
  // cards (same key, so the real card reuses the node): the job list never
  // blanks to "Loading…" while the status poll already knows the jobs.
  const shownCards = cards ?? (opsStatus?.jobs ?? []).map((job) => statusOnlyCard(job, !cardsError));
  // the work items that need attention but have no live job
  // (neither a card nor a status job) — one feed-only card each, first in the
  // row, newest first; a waiting job's needs_input entry is that job's card.
  const droppedItems = droppedItemAttention(decisionsSurface.feed?.decisions ?? [], [...shownCards.map((card) => card.job), ...(opsStatus?.jobs ?? [])]);
  // a live card's entries are exactly the ones the Polyforge
  // button counts for that item (`attentionItems`, the count's own rule), so
  // the card and the count cannot drift.
  const waitingJobs = needsInputJobs(opsStatus);
  const liveCardEntries = (job: PolyforgeJob) => attentionItems(waitingJobs, itemDecisions(decisionsSurface.feed?.decisions ?? [], job))
    .flatMap((item) => (item.kind === "decision" ? [item.decision] : []));
  // the queued verifications by work reference, from the one
  // status snapshot: its size is the counter's "waiting for verification"
  // count (those jobs hold no slot, so `queue.running` leaves them out) and
  // its entries mark the cards, so the counter and the marks always agree
  // and both follow the status poll.
  const verifyQueues = new Map((opsStatus?.jobs ?? []).flatMap((job) => (job.verifyQueue ? [[polyforgeJobRef(job), job.verifyQueue] as const] : [])));
  // The selected job's status facts for the detail: from the
  // rendered cards, else the status job list; matched by slug or wi id (an
  // anchor selects by either).
  // a selection that names its Cell matches only that Cell's
  // job (equal literals of two Cells are two jobs).
  const isSelected = (job: { slug: string; wi: string; profile?: string }) =>
    (job.slug === selectedWi || job.wi === selectedWi) && (selectedProfile === undefined || (job.profile ?? "default") === selectedProfile);
  const selectedCard = selectedWi
    ? shownCards.find((card) => isSelected(card.job))
    : undefined;
  const selectedJob = selectedWi
    ? (selectedCard?.job ?? opsStatus?.jobs.find((job) => isSelected(job)))
    : undefined;
  // workflow contract v2 (change 14): the question the selected work item's
  // step waits on (the watch snapshot's pending question — a contract-1
  // question or the current dispatch's open `pf step ask`), shown highlighted
  // above the card's input box; the conversation state decides whether the
  // box takes a message now (an agent, or a park the message wakes) or a
  // cannot-start park's reason and commands show (design #483 §7).
  const pendingQuestion = currentPendingQuestion(watchStatus);
  // The progress stream is read here (not in the flow component) so the
  // composer under the scroll region shares it: its running subagents, and
  // the owner's own messages echoed until the event log shows them. The
  // poll runs while the job runs (the rule, unchanged): a job
  // that starts waiting is read once on that change (its ask line), and a
  // reply makes it run again — the status read flips it back, the poll
  // resumes, and the echo covers the gap.
  const progressWi = selectedWi ? (selectedJob?.wi ?? watchStatus?.wi ?? null) : null;
  const progressLive = selectedJob?.status === "running";
  const progressSnapshot = usePolyforgeProgress(progressWi, progressLive, selectedJob?.profile);
  const [echoes, setEchoes] = useState<{ wi: string | null; list: PolyforgeOwnerEcho[] }>({ wi: null, list: [] });
  const detailEchoes = echoes.wi === progressWi ? echoes.list : [];
  const onSent = useCallback((echo: PolyforgeOwnerEcho) => {
    setEchoes((current) => (current.wi === progressWi ? { wi: progressWi, list: [...current.list, echo] } : { wi: progressWi, list: [echo] }));
    void Promise.resolve(onRefresh()).catch(() => undefined);
  }, [progressWi, onRefresh]);
  // the selected work item's feed entries (its id, else the
  // selection's slug, in its Cell), for the conversation state.
  const selectedItemEntries = selectedWi
    ? itemDecisions(decisionsSurface.feed?.decisions ?? [], {
      wi: selectedJob?.wi ?? watchStatus?.wi ?? (selectedWi.startsWith("wi_") ? selectedWi : undefined),
      slug: selectedJob?.slug ?? watchStatus?.slug ?? (selectedWi.startsWith("wi_") ? undefined : selectedWi),
      profile: selectedJob?.profile ?? selectedProfile,
    })
    : [];
  // the input box (a park's message wakes it, workflow contract 4) or, for a
  // park whose agent cannot start, the reason and its commands: the job, the
  // watch snapshot's park, and the item's open feed entries (a block the
  // runtime made before any workflow park)
  const conversation = conversationState({ jobStatus: selectedJob?.status, workflowStatus: watchStatus?.workflowStatus, currentStep: watchStatus?.currentStep, blocked: watchStatus?.blocked,
    steps: watchStatus?.steps, events: watchStatus?.events, entries: selectedItemEntries, daemonPresent: opsStatus !== null });
  // the per-step rerun count the card no longer shows, from the
  // selected card's own read (no new read); shown on the DAG timing line.
  const stepRuns = new Map((selectedCard?.steps ?? []).map((step) => [step.id, step.runs]));
  // title row: the slug (the watch read's, else the selected
  // job's, else the selection ref), the goal's pure first-clause short title
  // (absent without a readable goal — never an invented title) and the job
  // status (the watch read's workflow status once the job left the list).
  // The hover carries the full goal, the workflow status (the old header's
  // visible label), the update time and the current step.
  const detailSlug = watchStatus?.slug ?? selectedJob?.slug ?? selectedWi;
  const detailGoal = selectedCard?.goal;
  const detailShortTitle = shortJobTitle(detailGoal);
  const detailStatus = selectedJob
    ? jobStatusLabel(selectedJob.status)
    : watchStatus ? workflowStatusLabel(watchStatus.workflowStatus) : null;
  // The detail's one scroll region, open at its bottom:
  // the progress block seeds the follow pinned for every
  // selection and remount, so the first stream with lines ends at the bottom.
  const detailScrollRef = useRef<HTMLDivElement | null>(null);
  // The goal's expanded state, for one selected job only: selecting another
  // job folds it again without an effect.
  const [goalExpandedFor, setGoalExpandedFor] = useState<string | null>(null);
  const goalExpanded = goalExpandedFor !== null && goalExpandedFor === selectedWi;
  const detailCurrentStep = watchStatus?.currentStep ?? selectedCard?.currentStep;
  // a selected job queued for verification admission shows
  // `verification（排队 #n）` as its current step.
  const selectedVerifyQueue = selectedJob ? verifyQueues.get(polyforgeJobRef(selectedJob)) : undefined;
  const detailStepText = selectedVerifyQueue
    ? t("polyforge.verifyQueued", { position: selectedVerifyQueue.position })
    : detailCurrentStep;
  const jobStartParsed = selectedCard?.startedAt ? Date.parse(selectedCard.startedAt) : Number.NaN;
  const jobStartMs = Number.isFinite(jobStartParsed) ? jobStartParsed : null;
  const detailTitleHover = [
    detailGoal,
    watchStatus ? workflowStatusLabel(watchStatus.workflowStatus) : undefined,
    watchStatus?.updatedAt ? `${t("polyforge.updatedAt")}: ${formatEventTime(watchStatus.updatedAt)}` : undefined,
    watchStatus?.currentStep ? `${t("polyforge.currentStep")}: ${watchStatus.currentStep}` : undefined,
  ].filter(Boolean).join("\n");

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%", minHeight: 0, background: "var(--bg-panel)" }} role="complementary" aria-label={t("polyforge.title")}>
      {/* Header */}
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "8px 12px", borderBottom: "1px solid var(--border)", flexShrink: 0 }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: "var(--text)", flex: 1 }}>{t("polyforge.title")}</span>
        {/* the desktop pin — while pressed an outside press no
            longer dismisses the dock; close and the topbar toggle still do. */}
        {showPin && (
          <button
            type="button"
            onClick={onTogglePin}
            aria-pressed={pinned}
            title={pinned ? t("polyforge.dock.unpin") : t("polyforge.dock.pin")}
            aria-label={t("polyforge.dock.pin")}
            style={{ display: "flex", alignItems: "center", justifyContent: "center", width: 26, height: 26, background: pinned ? "var(--bg-selected)" : "none", border: "none", borderRadius: 4, color: pinned ? "var(--text)" : "var(--text-muted)", cursor: "pointer", fontSize: 14 }}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill={pinned ? "currentColor" : "none"} stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M12 17v5" /><path d="M9 10.76V6h6v4.76l2 3.24H7z" /><path d="M8 2h8" />
            </svg>
          </button>
        )}
        <button
          type="button"
          onClick={handleRefresh}
          disabled={refreshBusy}
          title={t("polyforge.refresh")}
          aria-label={t("polyforge.refresh")}
          style={{ display: "flex", alignItems: "center", justifyContent: "center", width: 26, height: 26, background: "none", border: "none", color: "var(--text-muted)", cursor: refreshBusy ? "default" : "pointer", fontSize: 14 }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={refreshBusy ? { animation: "spin 0.8s linear infinite" } : undefined}>
            <path d="M21 12a9 9 0 1 1-2.64-6.36" /><path d="M21 3v6h-6" />
          </svg>
        </button>
        <button
          type="button"
          onClick={onClose}
          title={t("polyforge.close")}
          aria-label={t("polyforge.close")}
          style={{ display: "flex", alignItems: "center", justifyContent: "center", width: 26, height: 26, background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 14 }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M18 6 6 18" /><path d="m6 6 12 12" />
          </svg>
        </button>
      </div>

      {/* Work | Memory | Messages tab strip (order by owner
          2026-10-30) (the former Jobs and Tasks tabs are Work's
          sub-tabs). The unread indicator is its own element beside the
          Messages label: it never replaces any badge. Work carries no count
          of its own. */}
      <div role="tablist" data-pf-tabstrip="top" aria-label={t("polyforge.title")} style={{ display: "flex", borderBottom: "1px solid var(--border)", flexShrink: 0 }}>
        <button type="button" role="tab" aria-selected={activeTab === "work"}
          onClick={() => onActiveTab("work")}
          style={{ flex: 1, padding: "6px 12px", background: activeTab === "work" ? "var(--bg-selected)" : "none", border: "none", borderBottom: activeTab === "work" ? "2px solid var(--accent)" : "2px solid transparent", color: activeTab === "work" ? "var(--text)" : "var(--text-muted)", cursor: "pointer", fontSize: 12 }}>
          {t("polyforge.work.tab")}
        </button>
        <button type="button" role="tab" aria-selected={activeTab === "memory"}
          onClick={() => onActiveTab("memory")}
          style={{ flex: 1, padding: "6px 12px", background: activeTab === "memory" ? "var(--bg-selected)" : "none", border: "none", borderBottom: activeTab === "memory" ? "2px solid var(--accent)" : "2px solid transparent", color: activeTab === "memory" ? "var(--text)" : "var(--text-muted)", cursor: "pointer", fontSize: 12 }}>
          {t("polyforge.memory.tab")}
        </button>
        <button type="button" role="tab" aria-selected={activeTab === "messages"}
          onClick={() => onActiveTab("messages")}
          style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", gap: 6, padding: "6px 12px", background: activeTab === "messages" ? "var(--bg-selected)" : "none", border: "none", borderBottom: activeTab === "messages" ? "2px solid var(--accent)" : "2px solid transparent", color: activeTab === "messages" ? "var(--text)" : "var(--text-muted)", cursor: "pointer", fontSize: 12 }}>
          {t("polyforge.messages.tab")}
          {messagesSurface.unreadTotal > 0 && (
            <span aria-label={t("polyforge.messages.unread")} title={t("polyforge.messages.unread")}
              style={{ width: 8, height: 8, borderRadius: 4, background: "var(--accent)", display: "inline-block" }} />
          )}
        </button>
      </div>

      {/* Body */}
      <div style={{ flex: 1, minHeight: 0, overflow: "hidden", display: "flex", flexDirection: "column" }}>
        {activeTab === "messages" ? (
          <PolyforgeMessages
            pfPresent={messagesSurface.pfPresent}
            capable={messagesSurface.capable}
            reason={messagesSurface.reason}
            conversations={messagesSurface.conversations}
            contacts={messagesSurface.contacts}
            groups={messagesSurface.groups}
            membershipAdmin={messagesSurface.membershipAdmin}
            users={messagesSurface.users}
            onChangeMembership={messagesSurface.onChangeMembership}
            error={messagesSurface.error}
            selectedConversation={messagesSurface.selectedConversation}
            onSelectConversation={messagesSurface.onSelectConversation}
            selectedContact={messagesSurface.selectedContact}
            onSelectContact={messagesSurface.onSelectContact}
            messages={messagesSurface.messages}
            initialPageLoaded={messagesSurface.initialPageLoaded}
            blocked={messagesSurface.blocked}
            loadingOlder={messagesSurface.loadingOlder}
            onLoadOlder={messagesSurface.onLoadOlder}
            onAcknowledge={messagesSurface.onAcknowledge}
            onSend={messagesSurface.onSend}
            onAnalyze={messagesSurface.onAnalyze}
            analyses={messagesSurface.analyses}
            onOpenAnchor={messagesSurface.onOpenAnchor}
            onOpenTask={openTask}
            unreadTotal={messagesSurface.unreadTotal}
            bindPromptTarget={messagesSurface.bindPromptTarget}
            onPrompt={messagesSurface.onPrompt}
            onPromptBusyChange={messagesSurface.onPromptBusyChange}
          />
        ) : activeTab === "memory" ? (
          <PolyforgeMemory cwd={memoryCwd} onOpenWorkItem={messagesSurface.onOpenAnchor} />
        ) : (
          // Work (#419): two parts stacked at every width. The
          // top part is a row: the main area shows the selected sub-tab's
          // surface (the Jobs card row, the Tasks list, the 工单列表 list),
          // the sub-tab controls sit in a right rail: ~72px with text labels
          // on desktop, a 40px icon rail at ≤640px. The
          // selected item's detail sits below across the whole panel width;
          // with a detail the top part takes its content height, at most 45%.
          // The rail comes after the main area in the DOM and the containers
          // stay the same nodes, so switching never re-mounts them.
          <div data-pf-work-layout="" style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
            <div data-pf-work-top="" style={{ ...(workDetailOpen ? { flex: "0 1 auto", maxHeight: "45%" } : { flex: 1 }), minHeight: 0, display: "flex", flexDirection: "row", overflow: "hidden" }}>
            <div data-pf-work-main="" style={{ flex: 1, minWidth: 0, minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden" }}>
              {workTab === "items" ? (
                // one list read per opening, one re-read per
                // change of queue.running or the terminal-job count while
                // mounted (null without a snapshot); none elsewhere.
                <PolyforgeWorkItems itemCountKey={itemCountKey} onRunningCount={onWorkItemsRunningChange} renderDetail={renderWorkDetail} onDetailOpenChange={setSubDetailOpen} />
              ) : workTab === "integrate" ? (
                // its own read per opening, ↻, accepted answer
                // and status change of a listed batch's job row
                // (`/api/polyforge/integration`); never on the status poll.
                <PolyforgeIntegrate jobs={opsStatus?.jobs ?? null} workflowStatusLabel={workflowStatusLabel} />
              ) : workTab === "tasks" ? (
                // one list re-read per status task-count change
                // while this sub-page is visible; none elsewhere (it unmounts).
                <PolyforgeTasks
                  taskCountKey={opsStatus ? `${opsStatus.tasks?.running ?? 0}:${opsStatus.tasks?.completed ?? 0}` : null}
                  openTaskId={taskToOpen}
                  onOpenTaskHandled={clearTaskToOpen}
                  decisions={decisionsSurface.feed?.decisions ?? []}
                  onDismissDecisions={decisionsSurface.onDismiss}
                  renderDetail={renderWorkDetail}
                  onDetailOpenChange={setSubDetailOpen}
                />
              ) : opsStatus ? (
                  <div style={{ flex: "0 1 auto", minHeight: 0, overflowY: "auto", overflowX: "hidden" }}>
                    {/* Queue line + job card stream: its own
                        scroller in Work's top part; the selected job's detail
                        is below it across the whole width. */}
                    <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 12px", fontSize: 11, color: "var(--text-dim)", borderBottom: "1px solid var(--border)" }}>
                      <span>{t("polyforge.queue")}</span>
                      {/* the jobs holding a slot and the jobs
                          waiting for verification admission, side by side;
                          with none waiting, the plain running / max. */}
                      <span data-pf-queue-count="" style={{ fontWeight: 600, color: "var(--text)" }}>
                        {verifyQueues.size > 0
                          ? `${t("polyforge.queueSplit", { running: opsStatus.queue.running, waiting: verifyQueues.size })} / ${opsStatus.queue.max}`
                          : <>{opsStatus.queue.running} / {opsStatus.queue.max}</>}
                      </span>
                    </div>
                    {/* a known snapshot with no job is the empty
                        state at once — the stand-ins are the status jobs, so an
                        empty stream before the first card read means the status
                        already says "no jobs" (no Loading… flash). */}
                    {/* the empty state needs no live card and no
                        feed-only attention card either. */}
                    {shownCards.length === 0 && droppedItems.length === 0 ? (
                      <div style={{ padding: 16, color: "var(--text-dim)", fontSize: 12 }}>{t("polyforge.empty")}</div>
                    ) : (
                      // (intentional change per the 2026-10-31 ruling, not a
                      // regression):
                      // one row of cards that scrolls left and right — each card is a
                      // fixed 150px (the Tasks blocks' column minimum), so
                      // a wider main area leaves room at the right and a narrower one
                      // (the 340px dock) never squeezes the card. Native touch inertia, no
                      // scroll-snap; the thin scrollbar stays visible because a desktop
                      // mouse has no horizontal wheel and a hidden bar would strand the
                      // cards past the fold.
                      <div style={{ display: "flex", flexWrap: "nowrap", gap: 8, padding: 8, overflowX: "auto", scrollbarWidth: "thin" }}>
                        {/* the feed-only attention cards lead
                            the row (newest first), visible without scrolling. */}
                        {droppedItems.map((decision) => (
                          <PolyforgeAttentionCard
                            key={`attention:${decision.profile ?? "default"}/${decision.wi}`}
                            decision={decision}
                            selected={isSelected({ slug: decision.slug ?? "", wi: decision.wi ?? "", profile: decision.profile })}
                            onSelect={onSelect}
                            openIds={itemDecisions(decisionsSurface.feed?.decisions ?? [], { wi: decision.wi, slug: decision.slug, profile: decision.profile }).filter(isOpenDecision).map((e) => e.id)}
                            onDismiss={decisionsSurface.onDismiss}
                          />
                        ))}
                        {shownCards.map((card: PolyforgeCard) => (
                          <PolyforgeJobCard
                            key={polyforgeCardKey(card)}
                            card={card}
                            verifyQueue={verifyQueues.get(polyforgeJobRef(card.job))}
                            pending={cards === null}
                            selected={isSelected(card.job)}
                            titleLength={titleLength}
                            onSelect={onSelect}
                            entries={liveCardEntries(card.job)}
                            onDismiss={decisionsSurface.onDismiss}
                          />
                        ))}
                      </div>
                    )}
                  </div>
              ) : opsError ? (
                <div style={{ padding: 16, color: "var(--text-dim)", fontSize: 12 }}>{t("polyforge.unavailable")}</div>
              ) : (
                <div style={{ padding: 16, color: "var(--text-dim)", fontSize: 12 }}>{t("polyforge.loading")}</div>
              )}
            </div>
            <div
              role="tablist"
              data-pf-tabstrip="work"
              aria-label={t("polyforge.work.tab")}
              aria-orientation="vertical"
              style={isMobile
                ? { flex: "0 0 40px", width: 40, display: "flex", flexDirection: "column", borderLeft: "1px solid var(--border)", overflowY: "auto" }
                : { flex: "0 0 72px", width: 72, display: "flex", flexDirection: "column", borderLeft: "1px solid var(--border)", overflowY: "auto" }}
            >
              {(["jobs", "tasks", "items", "integrate"] as const).map((tab) => {
                const active = workTab === tab;
                // The running count rides the existing status snapshot (no
                // timer of its own): queue.running for Jobs, tasks.running
                // for Tasks; 0 stays visible but greyed; no snapshot (daemon
                // down) shows no badge at all. 工单列表 shows
                // the last list read's running count, absent before the first.
                // 待集成 shows no badge.
                const count = tab === "items" ? workItemsRunning
                  : opsStatus ? (tab === "jobs" ? opsStatus.queue.running : (opsStatus.tasks?.running ?? 0)) : null;
                const label = tab === "jobs" ? t("polyforge.messages.jobsTab") : tab === "tasks" ? t("polyforge.tasks.tab")
                  : tab === "items" ? t("polyforge.work.itemsTab") : t("polyforge.work.integrateTab");
                return (
                  <button key={tab} type="button" role="tab" aria-selected={active}
                    onClick={() => onWorkTab(tab)}
                    // icon-only at ≤640px, so the tab copy names the button.
                    aria-label={isMobile ? label : undefined}
                    title={isMobile ? label : undefined}
                    style={{
                      display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: isMobile ? 0 : 2,
                      flex: "0 0 auto", minWidth: 0,
                      ...(isMobile ? { width: "100%", height: 40, padding: 0 } : { padding: "10px 4px" }),
                      background: active ? "var(--bg-selected)" : "none", border: "none",
                      borderRight: active ? "2px solid var(--accent)" : "2px solid transparent",
                      color: active ? "var(--text)" : "var(--text-muted)", cursor: "pointer", fontSize: 12,
                    }}>
                    {isMobile ? WORK_TAB_ICONS[tab] : <span>{label}</span>}
                    {count !== null && tab !== "integrate" && (
                      <span
                        data-pf-work-badge={tab}
                        aria-label={t(WORK_TAB_BADGES[tab], { count })}
                        title={t(WORK_TAB_BADGES[tab], { count })}
                        style={{ fontSize: 11, fontWeight: 600, lineHeight: "14px", color: count > 0 ? "var(--accent)" : "var(--text-dim)" }}
                      >
                        {count}
                      </span>
                    )}
                  </button>
                );
              })}
            </div>
            </div>
            {/* the selected item's detail, across the whole
                panel width below the top part: the job detail here, a task's
                report or a work item's detail in the slot (a portal target,
                so the node stays mounted and only hides). */}
              <div data-pf-work-detail="" style={{ flex: 1, minHeight: 0, display: workDetailOpen ? "flex" : "none", flexDirection: "column", borderTop: "1px solid var(--border)" }}>
                {workTab === "jobs" && opsStatus && (<>
                  {/* Detail for the selected work item */}
                  {/* pi#120: with a job selected, the whole detail — summary,
                      graph and execution progress — is ONE
                      vertical scroll region (this container,
                      [data-pf-detail-scroll]). No nested vertical scroller exists
                      inside it, so every line is reachable by scrolling at any
                      sheet height and in the desktop dock. */}
                  {/* the detail's frame — the parent of the one
                      scroll region. */}
                  <div data-pf-detail-frame="" style={{ flex: 1, minHeight: 0, position: "relative", display: "flex", flexDirection: "column" }}>
                  <div ref={detailScrollRef} data-pf-detail-scroll="" style={{ flex: 1, minHeight: 0, overflowY: "auto", overflowAnchor: "none", display: "flex", flexDirection: "column" }}>
                    {selectedWi ? (
                        <>
                          {/* order: ① summary → ② DAG → ③ execution
                              progress last; no fourth block (the command row, the
                              event-mode toggle and the standalone event tail are
                              gone — lifecycle events are lines of the progress). */}
                          {/* ① Summary: the title row, the goal folded to three
                              lines with its expand control, the job facts, the
                              summarize narration and the pi#117 facts. */}
                          <div data-pf-detail-summary="" style={{ flexShrink: 0, display: "flex", flexDirection: "column", borderBottom: "1px solid var(--border)" }}>
                            {/* title row (was the detail header): the
                                deselect control, the slug, the goal's short title
                                (one line, ellipsized) and the job status on one line;
                                the full goal, workflow status, update time and current
                                step on hover. The deselect control is the only
                                reliable gesture once the selected job has left the
                                list (the row that could toggle it off is gone) or
                                while its watch snapshot is still loading, so the row
                                sits above the watchStatus split. */}
                            <div
                              data-pf-detail-title=""
                              title={detailTitleHover || undefined}
                              style={{ padding: "4px 12px", fontSize: 11, color: "var(--text-dim)", borderBottom: "1px solid var(--border)", flexShrink: 0, display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}
                            >
                              <button
                                type="button"
                                onClick={() => onSelect(null)}
                                title={t("polyforge.deselect")}
                                aria-label={t("polyforge.deselect")}
                                style={{ display: "flex", alignItems: "center", justifyContent: "center", width: 22, height: 22, padding: 0, background: "none", border: "1px solid var(--border)", borderRadius: 4, color: "var(--text-muted)", cursor: "pointer", flexShrink: 0 }}
                              >
                                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                                  <path d="M15 18l-6-6 6-6" />
                                </svg>
                              </button>
                              <span style={{ flex: "0 0 auto", whiteSpace: "nowrap", fontWeight: 600, color: "var(--text)" }}>{detailSlug}</span>
                              {detailShortTitle && (
                                <span style={{ flex: "1 1 0", minWidth: 0, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", color: "var(--text-muted)" }}>{detailShortTitle}</span>
                              )}
                              {detailStatus && (
                                <span style={{ flex: "0 0 auto", marginLeft: "auto", whiteSpace: "nowrap" }}>{detailStatus}</span>
                              )}
                            </div>
                            {/* The full goal, folded to three lines (CSS
                                line clamp, the whole text in the DOM and on
                                hover), with a visible expand/collapse control;
                                the expanded state resets per selected job. No
                                readable goal: no goal line, never an invented one. */}
                            {detailGoal && (
                              <div style={{ padding: "4px 12px 0", display: "flex", flexDirection: "column", alignItems: "flex-start", gap: 2, fontSize: 11 }}>
                                <span
                                  data-pf-summary-goal=""
                                  title={detailGoal}
                                  style={goalExpanded
                                    ? { color: "var(--text)", whiteSpace: "pre-wrap", overflowWrap: "anywhere", lineHeight: "16px" }
                                    : { color: "var(--text)", whiteSpace: "pre-wrap", overflowWrap: "anywhere", lineHeight: "16px", display: "-webkit-box", WebkitLineClamp: 3, WebkitBoxOrient: "vertical", overflow: "hidden" }}
                                >
                                  {detailGoal}
                                </span>
                                <button
                                  type="button"
                                  data-pf-goal-toggle=""
                                  aria-expanded={goalExpanded}
                                  onClick={() => setGoalExpandedFor(goalExpanded ? null : selectedWi)}
                                  style={{ padding: 0, background: "none", border: "none", color: "var(--accent)", cursor: "pointer", fontSize: 11, lineHeight: "16px" }}
                                >
                                  {t(goalExpanded ? "polyforge.goal.collapse" : "polyforge.goal.expand")}
                                </button>
                              </div>
                            )}
                            {/* Job facts (+ #238): current step, the
                                model (24 chars + …, full on hover) with the spawn
                                number, step/idle age from the status snapshot (so
                                they show while the watch read loads), the job start
                                (the card's earliest job_started) and the total
                                elapsed time, computed at render and refreshed by the
                                3 s watch poll — no timer of its own. */}
                            {(selectedJob || detailStepText || jobStartMs !== null) && (
                              <div style={{ padding: "4px 12px", display: "flex", flexWrap: "wrap", columnGap: 12, rowGap: 2, fontSize: 11 }}>
                                {detailStepText && (
                                  <span style={{ display: "flex", gap: 6, minWidth: 0 }}>
                                    <span style={{ color: "var(--text-dim)", flexShrink: 0 }}>{t("polyforge.currentStep")}</span>
                                    <span style={{ color: "var(--text)", fontFamily: "var(--font-mono, monospace)", overflowWrap: "anywhere" }}>{detailStepText}</span>
                                  </span>
                                )}
                                {selectedJob && (
                                  <>
                                    <span style={{ display: "flex", gap: 6, minWidth: 0 }}>
                                      <span style={{ color: "var(--text-dim)", flexShrink: 0 }}>{t("polyforge.candidate")}</span>
                                      <span title={selectedJob.candidate} style={{ color: "var(--text)", fontFamily: "var(--font-mono, monospace)", overflowWrap: "anywhere" }}>{selectedJob.candidate ? shortenCandidate(selectedJob.candidate) : "—"}</span>
                                      <span style={{ color: "var(--text-dim)", flexShrink: 0 }}>#{selectedJob.spawnN}</span>
                                    </span>
                                    <span style={{ display: "flex", gap: 6, whiteSpace: "nowrap" }}>
                                      <span style={{ color: "var(--text-dim)" }}>{t("polyforge.stepAge")}</span>
                                      <span style={{ color: "var(--text)" }}>{formatAge(selectedJob.stepAgeSec)}</span>
                                      <span style={{ color: "var(--text-dim)" }}>{t("polyforge.idleAge")}</span>
                                      <span style={{ color: "var(--text)" }}>{formatAge(selectedJob.idleAgeSec)}</span>
                                    </span>
                                    {/* the current step session's context,
                                        tokens over window (`?` for an unknown side) and the
                                        model; no row when neither is known. */}
                                    {selectedJob.context && polyforgeContextDetailText(selectedJob.context) && (
                                      <span style={{ display: "flex", gap: 6, minWidth: 0 }}>
                                        <span style={{ color: "var(--text-dim)", flexShrink: 0 }}>{t("polyforge.context")}</span>
                                        <span data-pf-detail-context="" title={selectedJob.context.asOf} style={{ color: "var(--text)", overflowWrap: "anywhere", fontVariantNumeric: "tabular-nums" }}>
                                          {polyforgeContextDetailText(selectedJob.context)}
                                          {selectedJob.context.model && <span style={{ color: "var(--text-dim)", fontFamily: "var(--font-mono, monospace)" }}>{` · ${selectedJob.context.model}`}</span>}
                                        </span>
                                      </span>
                                    )}
                                  </>
                                )}
                                {jobStartMs !== null && (
                                  <span style={{ display: "flex", gap: 6, whiteSpace: "nowrap" }}>
                                    <span style={{ color: "var(--text-dim)" }}>{t("polyforge.jobStart")}</span>
                                    <span style={{ color: "var(--text)" }}>{formatProgressClock(jobStartMs)}</span>
                                    <span style={{ color: "var(--text-dim)" }}>{t("polyforge.elapsed")}</span>
                                    <span style={{ color: "var(--text)" }}>{formatAge(liveElapsedSec(jobStartMs))}</span>
                                  </span>
                                )}
                              </div>
                            )}
                            {/* pi#119 summarize narration: the line, its age, a
                                cap-reached marker and the raw error line, which may
                                show together with an older summary. Absent when the
                                snapshot carries none of the four fields: no
                                heading, placeholder or loading text. */}
                            {summaryVisible && (
                              <div style={{ padding: "4px 12px", display: "flex", flexDirection: "column", gap: 2, fontSize: 11 }}>
                                {summary && (
                                  <>
                                    <span style={{ color: "var(--text-dim)" }}>
                                      {t("polyforge.summary")}{summaryAgeSec != null ? ` · ${formatAge(summaryAgeSec)}` : ""}
                                    </span>
                                    <span style={{ color: "var(--text)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{summary}</span>
                                  </>
                                )}
                                {summaryCapped && (
                                  <span style={{ color: "#d97706" }}>{t("polyforge.summaryCapped")}</span>
                                )}
                                {summaryError && (
                                  <span style={{ color: "#dc2626", fontFamily: "var(--font-mono, monospace)", whiteSpace: "pre-wrap", wordBreak: "break-all" }}>{summaryError}</span>
                                )}
                              </div>
                            )}
                            {/* pi#117 facts, moved into the summary:
                                tool calls, recent files, chat origin. Display-only;
                                absent when the snapshot carries none of them. */}
                            {detailInfoVisible && (
                              <div style={{ padding: "4px 12px", display: "flex", flexDirection: "column", gap: 4, fontSize: 11 }}>
                                {progress?.tool_calls != null && (
                                  <div style={{ display: "flex", gap: 6 }}>
                                    <span style={{ color: "var(--text-dim)" }}>{t("polyforge.toolCalls")}</span>
                                    <span style={{ color: "var(--text)" }}>{progress.tool_calls}</span>
                                  </div>
                                )}
                                {progress?.recent_files && (
                                  <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
                                    <span style={{ color: "var(--text-dim)" }}>{t("polyforge.recentFiles")}</span>
                                    {progress.recent_files.map((file) => (
                                      <span
                                        key={file}
                                        title={file}
                                        style={{ color: "var(--text-dim)", fontFamily: "var(--font-mono, monospace)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                                      >
                                        {file}
                                      </span>
                                    ))}
                                  </div>
                                )}
                                {originHasText && origin && (
                                  <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
                                    <span style={{ color: "var(--text-dim)" }}>{t("polyforge.origin")}</span>
                                    {origin.owner && <span style={{ color: "var(--text)" }}>{origin.owner}</span>}
                                  </div>
                                )}
                              </div>
                            )}
                          </div>
                          {/* ② Step flow graph (pi#114; moved verbatim to the
                              second block): display-only nodes with
                              status dots, solid sequence connectors between
                              consecutive nodes, dashed rewind chips below.
                              Nodes wrap; the container never scrolls. */}
                          {graph ? (
                          <div data-pf-detail-graph="" style={{ padding: "6px 12px", borderBottom: "1px solid var(--border)", flexShrink: 0, overflow: "hidden" }}>
                            <div role="list" style={{ display: "flex", flexWrap: "wrap", alignItems: "center", rowGap: 6 }}>
                              {graph.nodes.map((node, index) => {
                                // the per-step timing lives under its
                                // node — `+<duration>` (done: own start → the next
                                // started step's start; in_progress: the live age at
                                // render) and `×N` for a rerun; otherwise an empty
                                // line of the same height keeps the nodes aligned.
                                // The start time joins the node's hover title.
                                const timing = stepTimings?.get(node.id);
                                const runs = stepRuns.get(node.id) ?? 0;
                                // a skipped step is grayed as a whole —
                                // gray name and a dashed frame.
                                const skipped = node.status === "skipped";
                                const timingText = [
                                  timing && timing.durationSec != null ? `+${formatCardDuration(timing.durationSec)}` : "",
                                  runs > 1 ? `×${runs}` : "",
                                ].filter(Boolean).join(" ");
                                return (
                                <span key={`${node.id}-${index}`} style={{ display: "inline-flex", alignItems: "flex-start" }}>
                                  {/* Connector only for pairs the model actually
                                      edges: started-only in fallback (G5), and
                                      declared-consecutive pairs only (G6). */}
                                  {index > 0 &&
                                    graph.sequenceEdges.some(
                                      ([from, to]) => from === graph.nodes[index - 1].id && to === node.id,
                                    ) && (
                                    <span aria-hidden="true" style={{ color: "var(--text-dim)", fontSize: 10, lineHeight: "20px", padding: "0 3px", flexShrink: 0 }}>
                                      →
                                    </span>
                                  )}
                                  {/* The node column: the listitem (name + dot, exact
                                      name-only text) and its timing line as a sibling. */}
                                  <span style={{ display: "inline-flex", flexDirection: "column", alignItems: "center" }}>
                                  <span
                                    role="listitem"
                                    title={`${node.id} · ${stepStatusLabel(node.status)}${timing ? ` · ${formatEventTime(timing.startedAt)}` : ""}`}
                                    aria-label={`${node.id} · ${stepStatusLabel(node.status)}`}
                                    style={{ display: "inline-flex", alignItems: "center", gap: 4, padding: "2px 6px", borderRadius: 6, border: skipped ? "1px dashed var(--border)" : "1px solid var(--border)", fontSize: 11, color: skipped ? "var(--text-dim)" : "var(--text)", fontFamily: "var(--font-mono, monospace)" }}
                                  >
                                    <span
                                      aria-hidden="true"
                                      className={node.status === "in_progress" ? "pf-step-node-pulse" : undefined}
                                      style={node.status === "skipped"
                                        // skipped is a hollow gray ring (pending stays the filled gray dot).
                                        ? { width: 7, height: 7, borderRadius: 4, flexShrink: 0, boxSizing: "border-box", background: "transparent", border: "1.5px solid var(--text-dim)" }
                                        : { width: 7, height: 7, borderRadius: 4, flexShrink: 0, background: stepNodeColor(node.status) }}
                                    />
                                    {/* name + dot only; the status lives in title/aria-label. */}
                                    {shortenCandidate(node.id)}
                                  </span>
                                  <span data-pf-step-timing={node.id} style={{ height: 14, lineHeight: "14px", fontSize: 10, color: "var(--text-dim)", fontFamily: "var(--font-mono, monospace)", whiteSpace: "nowrap" }}>{timingText}</span>
                                  </span>
                                </span>
                                );
                              })}
                            </div>
                            {graph.rewindEdges.length > 0 && (
                              <div style={{ display: "flex", flexWrap: "wrap", gap: 4, paddingTop: 4 }}>
                                {graph.rewindEdges.map(([gate, target]) => (
                                  <span
                                    key={`${gate}->${target}`}
                                    title={`${t("polyforge.eventKind.rewind")}: ${gate} → ${target}`}
                                    aria-label={`${t("polyforge.eventKind.rewind")}: ${gate} → ${target}`}
                                    style={{ display: "inline-flex", alignItems: "center", gap: 4, padding: "1px 6px", borderRadius: 6, border: "1px dashed var(--border)", fontSize: 10, color: "var(--text-dim)", fontFamily: "var(--font-mono, monospace)" }}
                                  >
                                    <span aria-hidden="true">⇢</span>
                                    {`${shortenCandidate(gate)} → ${shortenCandidate(target)}`}
                                  </span>
                                ))}
                              </div>
                            )}
                          </div>
                          ) : (
                            <div data-pf-detail-graph="" style={{ padding: 16, color: "var(--text-dim)", fontSize: 12, flexShrink: 0 }}>{t("polyforge.loading")}</div>
                          )}
                          {/* ③ Execution progress, the last block: the whole
                              job's stream (every spawn, lifecycle lines included),
                              inline in this one scroll region — no nested scroller.
                              Its own fixed 2 s read while running
                              (usePolyforgeProgress). The daemon drops a
                              finished job from the status/cards lists, so the
                              wi falls back to the selection's watch snapshot
                              (reset per selection) and the last stream stays. */}
                          <PolyforgeDetailProgress wi={progressWi} profile={selectedJob?.profile} running={progressLive} snapshot={progressSnapshot} echoes={detailEchoes} scrollRef={detailScrollRef} />
                        </>
                      ) : (
                      <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: "var(--text-dim)", fontSize: 12 }}>{t("polyforge.selectJob")}</div>
                    )}
                  </div>
                  </div>
                  {/* workflow contract v2 (change 14): the card's bottom, under
                      the scroll region — the question the step waits on and
                      the ONE input box to the step's agent (running or
                      awaiting), or, with no agent to talk to, the no-agent
                      menu. */}
                  {selectedWi && (
                    <PolyforgeJobComposer
                      wi={selectedJob?.wi ?? watchStatus?.wi ?? (selectedWi.startsWith("wi_") ? selectedWi : undefined)}
                      profile={selectedJob?.profile ?? selectedProfile}
                      state={conversation}
                      question={pendingQuestion}
                      blocked={watchStatus?.blocked}
                      onSent={onSent}
                    />
                  )}
                </>)}
                <div ref={setWorkDetailSlot} data-pf-work-detail-slot="" style={{ flex: 1, minHeight: 0, display: workTab === "jobs" ? "none" : "flex", flexDirection: "column" }} />
              </div>
          </div>
        )}
      </div>
    </div>
  );
}
