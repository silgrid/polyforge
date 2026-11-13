"use client";

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useI18n } from "@/hooks/useI18n";
import { MEMORY_COLLAPSE_BYTES, isMemoryBodyTruncated } from "@/lib/polyforge-memory-pure";
import {
  TASK_REPORTER_MACHINE,
  TASK_STATE_FILTERS,
  filterTaskRows,
  formatTaskAge,
  isPolyforgeTaskId,
  splitTaskSections,
  taskDurationSeconds,
  taskEndedAgo,
  taskProjectOptions,
  taskReporterOptions,
  type PolyforgeTaskDetail,
  type PolyforgeTaskRow,
  type PolyforgeTaskStateFilter,
} from "@/lib/polyforge-tasks-pure";
import { formatCardDuration } from "@/lib/polyforge-cards-pure";
import { POLYFORGE_BLOCK_STYLE } from "@/lib/panel-layout";
import { MarkdownBody } from "./MarkdownBody";
import { PolyforgeDecisionEntry, useDecisionDismiss } from "./PolyforgeDecisionEntry";
import { taskDecisions, taskNeedsAttention, type PolyforgeDecision } from "@/lib/polyforge-decisions-pure";
import { POLYFORGE_SEARCH_CONTROL_CLASS, PolyforgeRefreshButton, PolyforgeSearchBar } from "./PolyforgeSearchBar";

/**
 * The dock's read-only Tasks surface: every task of the local
 * daemon, listed once, in two sections — 正在运行 (`running`) on top and 全部
 * (the rest) below — with a
 * client-side search over the id, project, state and prompt summary (plus
 * project and state filters over the same rows), and a
 * detail read (`pf task show`) on selection. Reads follow gestures only: one
 * list read each time the tab is opened (this component mounts), one detail
 * read per selection, plus one list re-read when the status snapshot's task
 * counts change while the tab is open; searching, filtering
 * and going back read nothing. adds the third list read: the
 * search bar's square ↻ re-reads the list on each click (the only way to
 * refresh while the count key holds); moves it into the shared
 * search-bar action slot without changing the read or busy contract. No poll,
 * no storage, and no spawn or cancel control: the tab is a read surface.
 *
 * the list is a responsive grid of discrete blocks (state
 * dot + text, duration, project, a two-line prompt summary). A running
 * block's duration counts from `started_at` on a local 1 s render clock —
 * a clock, not a poll: it reads `Date.now()` and fetches nothing, and it
 * runs only while a running task is listed.
 *
 * below the prompt summary each block shows pf's
 * `report_tail` as plain text (React-escaped, so Markdown stays literal),
 * clipped to two lines by CSS only — the client never cuts it or appends an
 * ellipsis (pf already bounded it). The placeholder is keyed on state, never
 * on an empty tail: a running block always shows the dim 生成中…
 * (`polyforge.tasks.tailRunning`), even with a stale non-empty tail; any
 * other block whose tail is empty, whitespace-only or absent shows the dim
 * 暂无报告 (`polyforge.tasks.tailEmpty`). The tail rides the one list read:
 * no extra read. A third select filters by reporter: 全部发起人, the human
 * display names ascending, then 机器发起 (`machine`) last — options from the
 * whole loaded list (`taskReporterOptions`, never cascading), composed with
 * search/project/state in the one `filterTaskRows`; a change only sets
 * component state (no read, no storage, no URL — a reload resets it).
 */

interface ListResponse {
  pfPresent?: boolean;
  capable?: boolean;
  reason?: string;
  tasks?: PolyforgeTaskRow[];
  error?: string;
  /** the Cell this list is read from. */
  cell?: string;
}

interface ShowResponse {
  task?: PolyforgeTaskDetail | null;
  error?: string;
}

/** The collapsed report's display-height clip (the source is never sliced). */
const COLLAPSE_MAX_HEIGHT_PX = 280;

/** Known task states get a translated label; anything else renders raw. */
const TASK_STATE_KEYS = new Set(["running", "completed", "capped", "failed", "interrupted"]);

/** The state dot, existing CSS variables only: filled for running/completed/
 *  capped, a hollow ring for failed/interrupted (the text names the state). */
function stateDotStyle(state: string): React.CSSProperties {
  const base: React.CSSProperties = { width: 8, height: 8, borderRadius: 4, flexShrink: 0, boxSizing: "border-box" };
  if (state === "running") return { ...base, background: "var(--accent)" };
  if (state === "completed") return { ...base, background: "var(--text-muted)" };
  if (state === "failed" || state === "interrupted") return { ...base, border: "2px solid var(--text)" };
  return { ...base, background: "var(--text-dim)" };
}

/** Nanoseconds → compact duration (1.2e12 → "20m"). */
function formatNs(ns: number): string {
  const total = Math.round(ns / 1e9);
  if (total < 60) return `${total}s`;
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return `${minutes}m`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

export function PolyforgeTasks({
  taskCountKey,
  openTaskId = null,
  onOpenTaskHandled,
  decisions = [],
  onDismissDecisions,
  renderDetail,
  onDetailOpenChange,
}: {
  /** the status snapshot's `running:completed` task counts
   *  (null without a snapshot). A change while this surface is mounted
   *  re-reads the list exactly once; the mount value never does. */
  taskCountKey: string | null;
  /** a task to open from outside (an analysis card's task
   *  link) — its existing detail read runs once, then onOpenTaskHandled. */
  openTaskId?: string | null;
  onOpenTaskHandled?: () => void;
  /** the decision feed's entries (the hook's read); a task's
   *  terminal summary entries are matched to its row by task id. */
  decisions?: readonly PolyforgeDecision[];
  onDismissDecisions?: (ids: number[]) => Promise<void>;
  /** shows the selected item's detail in Work's full-width
   *  lower part (the list stays above it). */
  renderDetail: (detail: ReactNode) => ReactNode;
  /** told whether a detail is open (false on back and when
   *  this surface unmounts). */
  onDetailOpenChange?: (open: boolean) => void;
}) {
  const { t, locale } = useI18n();
  const [list, setList] = useState<ListResponse | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  // project/state filters over the one list read — component
  // memory only (a reload resets them), never a read.
  const [projectFilter, setProjectFilter] = useState("");
  const [stateFilter, setStateFilter] = useState<PolyforgeTaskStateFilter>("all");
  // the reporter filter — null = 全部发起人; component memory
  // only, like the other two.
  const [reporterFilter, setReporterFilter] = useState<string | null>(null);
  // the refresh button's busy state. Its one source is the
  // newest list read still in flight — however it started (the mount read,
  // a count change or the button). Only that newest read clears it.
  const [listRefreshing, setListRefreshing] = useState(false);

  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<PolyforgeTaskDetail | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState(false);
  // Only the newest selection may write detail state (a slow earlier read
  // never overwrites a later selection's answer).
  const detailSeqRef = useRef(0);

  // The list read: once when the sub-tab opens (the component mounts), once
  // more per status task-count change while it stays mounted
  // and once per click of the search row's ↻ button never
  // on a timer, a search, a filter, a detail or a back. Only the newest read
  // may write the list, and only it clears the refreshing state: a stale read
  // that lands late neither writes nor ends the newer one's spin.
  const listSeqRef = useRef(0);
  const readList = useCallback(() => {
    const seq = ++listSeqRef.current;
    setListRefreshing(true);
    fetch("/api/polyforge/tasks")
      .then(async (response) => {
        const body = await response.json() as ListResponse;
        if (seq !== listSeqRef.current) return;
        setListRefreshing(false);
        if (!response.ok) {
          // Keep the list already on screen; the red listError sits above it.
          setListError(body.error ?? t("polyforge.tasks.error"));
          return;
        }
        setList(body);
        setListError(body.error ?? null);
      })
      .catch(() => {
        if (seq !== listSeqRef.current) return;
        setListRefreshing(false);
        setListError(t("polyforge.tasks.error"));
      });
  }, [t]);
  // The mount read. The ref keeps it to exactly one read per mounted tab
  // even where React re-runs mount effects (StrictMode's dev double invoke
  // keeps refs), so the answer of that one read is never dropped by a
  // cleanup.
  const listReadRef = useRef(false);
  useEffect(() => {
    if (listReadRef.current) return;
    listReadRef.current = true;
    readList();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- one read per tab opening; readList changes only with the locale
  }, []);
  // The count-change re-read: the ref remembers the key this surface last
  // saw (the mount value included), so entering the sub-tab reads once and
  // each later change reads once more. A missing snapshot on either side
  // (daemon blip) is not a change.
  const countKeyRef = useRef(taskCountKey);
  useEffect(() => {
    const previous = countKeyRef.current;
    countKeyRef.current = taskCountKey;
    if (previous === null || taskCountKey === null || previous === taskCountKey) return;
    readList();
  }, [taskCountKey, readList]);

  // The block clock: re-render about once per second while a running task
  // is listed, so its duration ticks from started_at. No I/O inside.
  const [now, setNow] = useState(() => Date.now());
  const hasRunning = Array.isArray(list?.tasks) && list.tasks.some((row) => row.state === "running");
  useEffect(() => {
    if (!hasRunning) return;
    setNow(Date.now());
    const clock = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(clock);
  }, [hasRunning]);

  const openDetail = (id: string) => {
    if (!isPolyforgeTaskId(id)) return;
    const seq = ++detailSeqRef.current;
    setSelected(id);
    setDetail(null);
    setDetailError(null);
    setExpanded(false);
    fetch(`/api/polyforge/tasks/${encodeURIComponent(id)}`)
      .then(async (response) => {
        const body = await response.json() as ShowResponse;
        if (seq !== detailSeqRef.current) return;
        if (!response.ok || !body.task) {
          setDetailError(body.error ?? t("polyforge.tasks.error"));
          return;
        }
        setDetail(body.task);
      })
      .catch(() => {
        if (seq === detailSeqRef.current) setDetailError(t("polyforge.tasks.error"));
      });
  };

  // an external open (the analysis card's task link) is the
  // same one detail read a row click makes, once per handed id.
  useEffect(() => {
    if (!openTaskId) return;
    openDetail(openTaskId);
    onOpenTaskHandled?.();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- one detail read per handed id; openDetail is a plain closure
  }, [openTaskId]);

  const back = () => {
    detailSeqRef.current += 1;
    setSelected(null);
    setDetail(null);
    setDetailError(null);
    setExpanded(false);
  };

  // Work shows its full-width lower part while a detail is
  // open here, and hides it on back and when this sub-tab closes.
  useEffect(() => {
    onDetailOpenChange?.(selected !== null);
    return () => onDetailOpenChange?.(false);
  }, [selected, onDetailOpenChange]);

  const notice = (text: string, detailText?: string | null, color = "var(--text-dim)") => (
    <div style={{ padding: 16, display: "flex", flexDirection: "column", gap: 6, fontSize: 12 }}>
      <span style={{ color }}>{text}</span>
      {detailText && <span style={{ color: "var(--text-dim)" }}>{detailText}</span>}
    </div>
  );

  if (listError && !list) return notice(t("polyforge.tasks.error"), listError, "#dc2626");
  if (!list) return notice(t("polyforge.loading"));
  if (list.pfPresent === false) return notice(t("polyforge.tasks.notInstalled"));
  if (list.capable === false) return notice(t("polyforge.tasks.upgrade"), list.reason ?? t("polyforge.tasks.upgradeReason"), "var(--text)");

  // the detail no longer replaces the list — it is handed
  // to renderDetail, which shows it below Work's top part, full width.
  const detailView = selected ? (() => {
    const selectedDecisions = taskDecisions(decisions, selected);
    const reportCapped = detail ? isMemoryBodyTruncated(detail.report, detail.report_len) || detail.truncated : false;
    const collapsible = detail ? detail.report_len > MEMORY_COLLAPSE_BYTES : false;
    const meta = (label: string, value: string) => (
      <div style={{ display: "flex", gap: 6 }}>
        <span style={{ color: "var(--text-dim)", flexShrink: 0, width: 96 }}>{label}</span>
        <span style={{ color: "var(--text)", minWidth: 0, overflowWrap: "anywhere" }}>{value}</span>
      </div>
    );
    return (
      <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 12px", borderBottom: "1px solid var(--border)", flexShrink: 0 }}>
          <button type="button" onClick={back}
            style={{ background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 11, padding: 0 }}>
            ← {t("polyforge.tasks.back")}
          </button>
          <span data-pf-task={selected} style={{ fontSize: 11, color: "var(--text-dim)", fontFamily: "var(--font-mono)" }}>{selected}</span>
        </div>
        {detailError && <div style={{ padding: "8px 12px", color: "#dc2626", fontSize: 12 }}>{detailError}</div>}
        {!detail && !detailError && notice(t("polyforge.loading"))}
        {detail && (
          <div style={{ flex: 1, minHeight: 0, overflowY: "auto", padding: "8px 12px", display: "flex", flexDirection: "column", gap: 8 }}>
            {/* the task's decision-feed entries (its terminal
                summary), with dismiss while open. */}
            {selectedDecisions.length > 0 && (
              <TaskDecisionEntries entries={selectedDecisions} onDismiss={onDismissDecisions} />
            )}
            <div style={{ display: "flex", flexDirection: "column", gap: 2, fontSize: 11 }}>
              {meta(t("polyforge.tasks.status"), detail.state)}
              {meta(t("polyforge.tasks.project"), detail.project)}
              {meta(t("polyforge.tasks.candidates"), detail.candidates.join(", ") || "—")}
              {meta(t("polyforge.tasks.budget"), t("polyforge.tasks.budgetValue", {
                wall: formatNs(detail.wall_consumed_ns),
                maxWall: formatNs(detail.max_duration_ns),
                tokens: detail.tokens_consumed.toLocaleString(locale),
                maxTokens: detail.max_tokens.toLocaleString(locale),
              }))}
              {meta(t("polyforge.tasks.created"), detail.created_at)}
              {meta(t("polyforge.tasks.started"), detail.started_at ?? "—")}
              {meta(t("polyforge.tasks.ended"), detail.ended_at ?? "—")}
            </div>
            <span style={{ fontSize: 11, color: "var(--text-dim)" }}>{t("polyforge.tasks.prompt")}</span>
            <pre style={{ margin: 0, whiteSpace: "pre-wrap", overflowWrap: "anywhere", fontSize: 12, color: "var(--text)", fontFamily: "inherit" }}>{detail.prompt}</pre>
            <span style={{ fontSize: 11, color: "var(--text-dim)" }}>{t("polyforge.tasks.report")}</span>
            {reportCapped && (
              <div style={{ fontSize: 11, color: "#d97706" }}>{t("polyforge.tasks.truncated")}</div>
            )}
            {/* The height clip never slices the markdown source. */}
            <div style={collapsible && !expanded ? { maxHeight: COLLAPSE_MAX_HEIGHT_PX, overflowY: "hidden" } : undefined}>
              <MarkdownBody>{detail.report}</MarkdownBody>
            </div>
            {collapsible && (
              <button type="button" onClick={() => setExpanded((value) => !value)}
                style={{ alignSelf: "flex-start", background: "none", border: "1px solid var(--border)", borderRadius: 4, color: "var(--text-muted)", cursor: "pointer", fontSize: 11, padding: "2px 8px", flexShrink: 0 }}>
                {expanded ? t("polyforge.tasks.collapse") : t("polyforge.tasks.expand")}
              </button>
            )}
          </div>
        )}
      </div>
    );
  })() : null;

  const rows = Array.isArray(list.tasks) ? list.tasks : [];
  const sections = splitTaskSections(filterTaskRows(rows, { search, project: projectFilter, state: stateFilter, reporter: reporterFilter }));
  const projects = taskProjectOptions(rows);
  const reporters = taskReporterOptions(rows);
  const stateLabel = (state: string) => (TASK_STATE_KEYS.has(state) ? t(`polyforge.tasks.state.${state}`) : state);
  const renderRow = (row: PolyforgeTaskRow) => {
    const duration = taskDurationSeconds(row, now);
    // an open entry of the task (failed / capped /
    // interrupted) marks its row; the entry itself is in the detail.
    const rowDecisions = taskDecisions(decisions, row.id);
    const attention = taskNeedsAttention(rowDecisions);
    const endedAgo = row.state === "running" ? undefined : taskEndedAgo(row, now);
    return (
      <button key={row.id} type="button" data-pf-task={row.id} onClick={() => openDetail(row.id)}
        style={{
          ...POLYFORGE_BLOCK_STYLE,
          display: "flex", flexDirection: "column", alignItems: "stretch", gap: 4, minWidth: 0, width: "100%",
          cursor: "pointer", textAlign: "left", font: "inherit", fontSize: 12, color: "var(--text)",
        }}>
        <span style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
          <span aria-hidden="true" style={stateDotStyle(row.state)} />
          <span style={{ color: "var(--text-muted)", flexShrink: 0 }}>{stateLabel(row.state)}</span>
          {attention && (
            <span data-pf-task-attention="" title={t("polyforge.tasks.needsAttention")} aria-label={t("polyforge.tasks.needsAttention")}
              style={{ width: 8, height: 8, borderRadius: 4, background: "var(--accent)", flexShrink: 0 }} />
          )}
          <span style={{ marginLeft: "auto", flexShrink: 0, fontFamily: "var(--font-mono)", color: "var(--text)" }}>
            {duration === undefined ? "—" : formatCardDuration(duration)}
          </span>
        </span>
        <span style={{ fontSize: 11, color: "var(--text-dim)", fontFamily: "var(--font-mono)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{row.project || "—"}</span>
        <span style={{ display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden", lineHeight: "16px", overflowWrap: "anywhere" }}>{row.prompt_summary}</span>
        {row.state === "running" ? (
          <span style={{ fontSize: 11, color: "var(--text-dim)" }}>{t("polyforge.tasks.tailRunning")}</span>
        ) : row.report_tail?.trim() ? (
          <span style={{ display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden", lineHeight: "16px", overflowWrap: "anywhere", fontSize: 11, color: "var(--text-muted)" }}>{row.report_tail}</span>
        ) : (
          <span style={{ fontSize: 11, color: "var(--text-dim)" }}>{t("polyforge.tasks.tailEmpty")}</span>
        )}
        <span style={{ display: "flex", gap: 6, minWidth: 0, fontSize: 11, color: "var(--text-dim)" }}>
          <span style={{ fontFamily: "var(--font-mono)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0 }}>{row.id}</span>
          {endedAgo !== undefined && (
            <span style={{ marginLeft: "auto", flexShrink: 0 }}>{t("polyforge.tasks.endedAgo", { age: formatTaskAge(endedAgo) })}</span>
          )}
        </span>
      </button>
    );
  };
  // One responsive grid per section with the 150px column minimum the Jobs
  // cards share: two blocks side by side at the 460px dock and
  // the 375px sheet (beside its 40px rail), one at the 340px dock, more as the
  // main area widens, separated by the gap.
  const grid = (rowsInSection: PolyforgeTaskRow[]) => (
    <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))", gap: 8, padding: 8, flexShrink: 0 }}>
      {rowsInSection.map(renderRow)}
    </div>
  );
  const heading = (text: string) => (
    <div style={{ padding: "6px 12px", fontSize: 11, fontWeight: 600, color: "var(--text-dim)", borderBottom: "1px solid var(--border)", flexShrink: 0 }}>{text}</div>
  );
  return (
    <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
      <div style={{ padding: "8px 12px", borderBottom: "1px solid var(--border)", flexShrink: 0 }}>
        {list?.cell && (
          <div data-pf-tab-cell={list.cell} style={{ fontSize: 11, color: "var(--text-dim)", marginBottom: 4 }}>{t("polyforge.tabCell", { cell: list.cell })}</div>
        )}
        <PolyforgeSearchBar
          value={search}
          onChange={setSearch}
          placeholder={t("polyforge.tasks.searchPlaceholder")}
          label={t("polyforge.tasks.searchPlaceholder")}
          action={(
            /* The one manual list re-read, now the shared action
               slot. It spins, disables and reports busy while
               the newest read is in flight (the count key's re-read included);
               disabled keeps a click from stacking a second read. */
            <PolyforgeRefreshButton refreshing={listRefreshing} onClick={readList} data-pf-tasks-refresh="" />
          )}
        >
          <select value={projectFilter} onChange={(event) => setProjectFilter(event.target.value)}
            aria-label={t("polyforge.tasks.filterProject")} title={t("polyforge.tasks.filterProject")}
            className={POLYFORGE_SEARCH_CONTROL_CLASS}>
            <option value="">{t("polyforge.tasks.filterAllProjects")}</option>
            {projects.map((project) => <option key={project} value={project}>{project}</option>)}
          </select>
          <select value={stateFilter} onChange={(event) => setStateFilter(event.target.value as PolyforgeTaskStateFilter)}
            aria-label={t("polyforge.tasks.filterState")} title={t("polyforge.tasks.filterState")}
            className={POLYFORGE_SEARCH_CONTROL_CLASS}>
            {TASK_STATE_FILTERS.map((state) => (
              <option key={state} value={state}>{state === "all" ? t("polyforge.tasks.all") : t(`polyforge.tasks.state.${state}`)}</option>
            ))}
          </select>
          <select value={reporterFilter ?? ""} onChange={(event) => setReporterFilter(event.target.value || null)}
            aria-label={t("polyforge.tasks.filterReporter")} title={t("polyforge.tasks.filterReporter")}
            className={POLYFORGE_SEARCH_CONTROL_CLASS}>
            <option value="">{t("polyforge.tasks.filterAllReporters")}</option>
            {reporters.humans.map((reporter) => <option key={reporter} value={reporter}>{reporter}</option>)}
            {reporters.machine && <option value={TASK_REPORTER_MACHINE}>{t("polyforge.tasks.reporterMachine")}</option>}
          </select>
        </PolyforgeSearchBar>
      </div>
      <div style={{ flex: 1, minHeight: 0, overflowY: "auto", display: "flex", flexDirection: "column" }}>
        {listError && <div style={{ padding: "8px 12px", color: "#dc2626", fontSize: 12 }}>{listError}</div>}
        {rows.length === 0 ? (
          // the machine has no task at all — say so, and why.
          notice(t("polyforge.tasks.empty"), t("polyforge.tasks.emptyHint"))
        ) : (
          <>
            {heading(t("polyforge.tasks.running"))}
            {sections.running.length === 0
              ? <div style={{ padding: "8px 12px", color: "var(--text-dim)", fontSize: 12 }}>{t("polyforge.tasks.empty")}</div>
              : grid(sections.running)}
            {heading(t("polyforge.tasks.all"))}
            {sections.all.length === 0
              ? <div style={{ padding: "8px 12px", color: "var(--text-dim)", fontSize: 12 }}>{t("polyforge.tasks.empty")}</div>
              : grid(sections.all)}
          </>
        )}
      </div>
      {detailView && renderDetail(detailView)}
    </div>
  );
}

/** A task's feed entries in its detail. */
function TaskDecisionEntries({ entries, onDismiss }: { entries: readonly PolyforgeDecision[]; onDismiss?: (ids: number[]) => Promise<void> }) {
  const { t } = useI18n();
  const { busy, error, dismiss } = useDecisionDismiss(onDismiss ?? (() => Promise.resolve()));
  return (
    <section data-pf-task-decisions="" style={{ display: "flex", flexDirection: "column", padding: "4px 8px", border: "1px solid var(--border)", borderRadius: 6 }}>
      {entries.map((decision) => <PolyforgeDecisionEntry key={decision.id} decision={decision} busy={busy} onDismiss={dismiss} />)}
      {error && <div style={{ fontSize: 11, color: "#dc2626", overflowWrap: "anywhere" }}>{t("polyforge.decisions.dismissFailed", { error })}</div>}
    </section>
  );
}
