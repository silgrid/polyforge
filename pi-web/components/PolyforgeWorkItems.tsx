"use client";

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useI18n } from "@/hooks/useI18n";
import { MEMORY_COLLAPSE_BYTES, isMemoryBodyTruncated } from "@/lib/polyforge-memory-pure";
import {
  POLYFORGE_WORK_ITEM_STATUSES,
  filterWorkItemRows,
  isPolyforgeWorkItemId,
  sortWorkItemsNewestFirst,
  workItemProjectOptions,
  workItemsAtCap,
  workItemsRunningCount,
  type PolyforgeWorkItemDetail,
  type PolyforgeWorkItemRow,
} from "@/lib/polyforge-work-items-pure";
import { MarkdownBody } from "./MarkdownBody";
import { POLYFORGE_SEARCH_CONTROL_CLASS, PolyforgeRefreshButton, PolyforgeSearchBar } from "./PolyforgeSearchBar";

/**
 * The Work panel's read-only 工单列表 surface: every mapped
 * project's work items from ONE merged list read, newest first, with the
 * total and — when a project contributed exactly 500 rows (pf's per-project
 * cap) — the 仅显示最近 500 条 hint on the header and on that project's
 * filter option. Status/project filters and the slug/goal/project search are
 * one client-side predicate over the loaded rows (content is never listed or
 * searched); every row renders in the one scrolling list (no virtualisation,
 * no paging). A whole-row button opens the detail (one `file show` read) in
 * place of the list; ← 返回 restores the list with its filters and search
 * and reads nothing.
 *
 * Reads follow gestures only: one list read when the sub-tab opens (this
 * component mounts), one more per change of the local count key while it
 * stays mounted and one per click of the search bar's ↻ (
 *'s busy contract) — never a timer, a filter, a search, a
 * detail or a back. Every list answer reports its running count to the
 * parent, which keeps the last one for the sub-tab badge for the rest of the
 * page session. No storage, no write control: the surface is read-only.
 */

interface ListResponse {
  pfPresent?: boolean;
  capable?: boolean;
  reason?: string;
  items?: PolyforgeWorkItemRow[];
  total?: number;
  error?: string;
}

interface ShowResponse {
  item?: PolyforgeWorkItemDetail | null;
  reason?: string;
  error?: string;
}

/** The collapsed body's display-height clip (the source is never sliced). */
const COLLAPSE_MAX_HEIGHT_PX = 280;

const STATUS_KEYS = new Set<string>(POLYFORGE_WORK_ITEM_STATUSES);

export function PolyforgeWorkItems({
  itemCountKey,
  onRunningCount,
  renderDetail,
  onDetailOpenChange,
}: {
  /** The status snapshot's `queue.running:<terminal jobs>` key (null without
   *  a snapshot). A change while this surface is mounted re-reads the list
   *  exactly once; the mount value and a missing snapshot never do. */
  itemCountKey: string | null;
  /** Receives the running count of every list answer (the badge's value). */
  onRunningCount: (count: number) => void;
  /** shows the selected item's detail in Work's full-width
   *  lower part (the list stays above it). */
  renderDetail: (detail: ReactNode) => ReactNode;
  /** told whether a detail is open (false on back and when
   *  this surface unmounts). */
  onDetailOpenChange?: (open: boolean) => void;
}) {
  const { t } = useI18n();
  const [list, setList] = useState<ListResponse | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [projectFilter, setProjectFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  // the manual re-read's busy state. Its one source is the
  // newest list read still in flight — the mount read, a count-key change or
  // the button. Only that newest read clears it.
  const [listRefreshing, setListRefreshing] = useState(false);

  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<PolyforgeWorkItemDetail | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState(false);
  const detailSeqRef = useRef(0);

  // The list read; only the newest read may write the list.
  const listSeqRef = useRef(0);
  const readList = useCallback(() => {
    const seq = ++listSeqRef.current;
    setListRefreshing(true);
    fetch("/api/polyforge/work-items")
      .then(async (response) => {
        const body = await response.json() as ListResponse;
        if (seq !== listSeqRef.current) return;
        setListRefreshing(false);
        if (!response.ok) {
          // Keep the list already on screen; the red listError sits above it.
          setListError(body.error ?? t("polyforge.workItems.error"));
          return;
        }
        setList(body);
        setListError(body.error ?? null);
        onRunningCount(workItemsRunningCount(Array.isArray(body.items) ? body.items : []));
      })
      .catch(() => {
        if (seq !== listSeqRef.current) return;
        setListRefreshing(false);
        setListError(t("polyforge.workItems.error"));
      });
  }, [t, onRunningCount]);
  // The mount read: the ref keeps it to exactly one read per opening even
  // where React re-runs mount effects (StrictMode's dev double invoke keeps refs).
  const listReadRef = useRef(false);
  useEffect(() => {
    if (listReadRef.current) return;
    listReadRef.current = true;
    readList();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- one read per sub-tab opening
  }, []);
  // The count-key re-read: the ref remembers the key this surface last saw
  // (the mount value included); a missing snapshot on either side is not a change.
  const countKeyRef = useRef(itemCountKey);
  useEffect(() => {
    const previous = countKeyRef.current;
    countKeyRef.current = itemCountKey;
    if (previous === null || itemCountKey === null || previous === itemCountKey) return;
    readList();
  }, [itemCountKey, readList]);

  const openDetail = (id: string) => {
    if (!isPolyforgeWorkItemId(id)) return;
    const seq = ++detailSeqRef.current;
    setSelected(id);
    setDetail(null);
    setDetailError(null);
    setExpanded(false);
    fetch(`/api/polyforge/work-items/${encodeURIComponent(id)}`)
      .then(async (response) => {
        const body = await response.json() as ShowResponse;
        if (seq !== detailSeqRef.current) return;
        if (!response.ok || !body.item) {
          setDetailError(body.error ?? body.reason ?? t("polyforge.workItems.error"));
          return;
        }
        setDetail(body.item);
      })
      .catch(() => {
        if (seq === detailSeqRef.current) setDetailError(t("polyforge.workItems.error"));
      });
  };

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
      {detailText && <span style={{ color: "var(--text-dim)", overflowWrap: "anywhere" }}>{detailText}</span>}
    </div>
  );
  const statusLabel = (status: string | null) => (status && STATUS_KEYS.has(status) ? t(`polyforge.workItems.status.${status}`) : (status ?? "—"));

  if (listError && !list) return notice(t("polyforge.workItems.error"), listError, "#dc2626");
  if (!list) return notice(t("polyforge.workItems.loading"));
  if (list.pfPresent === false || list.capable === false) return notice(t("polyforge.workItems.unavailable"), list.reason, "var(--text)");

  // the detail no longer replaces the list — it is handed
  // to renderDetail, which shows it below Work's top part, full width.
  const detailView = selected ? (() => {
    const content = detail?.content ?? "";
    const truncated = detail ? isMemoryBodyTruncated(content, detail.content_len) : false;
    const collapsible = detail ? detail.content_len > MEMORY_COLLAPSE_BYTES : false;
    const waiting = detail?.waiting_for?.length
      ? detail.waiting_for.map((blocker) => `${blocker.slug ?? "—"} (${statusLabel(blocker.status)})`).join(", ")
      : detail?.waiting_on ?? null;
    const meta = (label: string, value: string | null) => (
      <div style={{ display: "flex", gap: 6 }}>
        <span style={{ color: "var(--text-dim)", flexShrink: 0, width: 96 }}>{label}</span>
        <span style={{ color: "var(--text)", minWidth: 0, overflowWrap: "anywhere" }}>{value || "—"}</span>
      </div>
    );
    return (
      <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 12px", borderBottom: "1px solid var(--border)", flexShrink: 0 }}>
          <button type="button" onClick={back}
            style={{ background: "none", border: "none", color: "var(--text-muted)", cursor: "pointer", fontSize: 11, padding: 0 }}>
            ← {t("polyforge.workItems.back")}
          </button>
          <span style={{ fontSize: 11, color: "var(--text-dim)", fontFamily: "var(--font-mono)" }}>{detail?.slug ?? selected}</span>
        </div>
        {detailError && <div style={{ padding: "8px 12px", color: "#dc2626", fontSize: 12 }}>{detailError}</div>}
        {!detail && !detailError && notice(t("polyforge.workItems.loading"))}
        {detail && (
          <div style={{ flex: 1, minHeight: 0, overflowY: "auto", padding: "8px 12px", display: "flex", flexDirection: "column", gap: 8 }}>
            <div style={{ fontSize: 13, color: "var(--text)", whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{detail.goal}</div>
            <div style={{ display: "flex", flexDirection: "column", gap: 2, fontSize: 11 }}>
              {meta(t("polyforge.workItems.field.slug"), detail.slug)}
              {meta(t("polyforge.workItems.field.status"), statusLabel(detail.status))}
              {meta(t("polyforge.workItems.field.priority"), detail.priority)}
              {meta(t("polyforge.workItems.field.bundle"), detail.bundle_name)}
              {meta(t("polyforge.workItems.field.project"), detail.project)}
              {meta(t("polyforge.workItems.field.labels"), detail.labels.join(", "))}
              {meta(t("polyforge.workItems.field.milestone"), detail.milestone)}
              {meta(t("polyforge.workItems.field.reporter"), detail.reporter)}
              {meta(t("polyforge.workItems.field.assignee"), detail.assignee)}
              {meta(t("polyforge.workItems.field.created"), detail.created_at)}
              {meta(t("polyforge.workItems.field.updated"), detail.updated_at)}
              {meta(t("polyforge.workItems.field.closed"), detail.closed_at)}
              {waiting && meta(t("polyforge.workItems.field.waitingFor"), waiting)}
            </div>
            {truncated && <div style={{ fontSize: 11, color: "#d97706" }}>{t("polyforge.workItems.truncated")}</div>}
            {/* The height clip never slices the markdown source. */}
            <div style={collapsible && !expanded ? { maxHeight: COLLAPSE_MAX_HEIGHT_PX, overflowY: "hidden" } : undefined}>
              <MarkdownBody>{content}</MarkdownBody>
            </div>
            {collapsible && (
              <button type="button" onClick={() => setExpanded((value) => !value)}
                style={{ alignSelf: "flex-start", background: "none", border: "1px solid var(--border)", borderRadius: 4, color: "var(--text-muted)", cursor: "pointer", fontSize: 11, padding: "2px 8px", flexShrink: 0 }}>
                {expanded ? t("polyforge.workItems.collapse") : t("polyforge.workItems.expand")}
              </button>
            )}
          </div>
        )}
      </div>
    );
  })() : null;

  const rows = sortWorkItemsNewestFirst(Array.isArray(list.items) ? list.items : []);
  const shown = filterWorkItemRows(rows, { search, project: projectFilter, status: statusFilter });
  const projects = workItemProjectOptions(rows);
  const capped = workItemsAtCap(rows);
  const capHint = t("polyforge.workItems.capHint");
  return (
    <div role="region" aria-label={t("polyforge.workItems.title")} style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
      <div style={{ padding: "8px 12px", borderBottom: "1px solid var(--border)", flexShrink: 0, display: "flex", flexDirection: "column", gap: 6 }}>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, fontSize: 11, color: "var(--text-dim)" }}>
          <span style={{ fontWeight: 600, color: "var(--text)" }}>{t("polyforge.workItems.total", { count: typeof list.total === "number" ? list.total : rows.length })}</span>
          {capped.size > 0 && <span style={{ color: "#d97706" }}>{capHint} ({[...capped].sort().join(", ")})</span>}
        </div>
        <PolyforgeSearchBar
          value={search}
          onChange={setSearch}
          placeholder={t("polyforge.workItems.searchPlaceholder")}
          label={t("polyforge.workItems.searchPlaceholder")}
          action={(
            /* the manual re-read (Tasks' #204 contract) — one
               list read per click through the existing GET face. */
            <PolyforgeRefreshButton refreshing={listRefreshing} onClick={readList} data-pf-items-refresh="" />
          )}
        >
          <select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}
            aria-label={t("polyforge.workItems.field.status")} title={t("polyforge.workItems.field.status")} className={POLYFORGE_SEARCH_CONTROL_CLASS}>
            <option value="all">{t("polyforge.workItems.filterAllStatuses")}</option>
            {POLYFORGE_WORK_ITEM_STATUSES.map((status) => <option key={status} value={status}>{statusLabel(status)}</option>)}
          </select>
          <select value={projectFilter} onChange={(event) => setProjectFilter(event.target.value)}
            aria-label={t("polyforge.workItems.field.project")} title={t("polyforge.workItems.field.project")} className={POLYFORGE_SEARCH_CONTROL_CLASS}>
            <option value="">{t("polyforge.workItems.filterAllProjects")}</option>
            {projects.map((project) => (
              <option key={project} value={project}>{capped.has(project) ? `${project} · ${capHint}` : project}</option>
            ))}
          </select>
        </PolyforgeSearchBar>
      </div>
      <div style={{ flex: 1, minHeight: 0, overflowY: "auto", display: "flex", flexDirection: "column" }}>
        {listError && <div style={{ padding: "8px 12px", color: "#dc2626", fontSize: 12, overflowWrap: "anywhere" }}>{listError}</div>}
        {!listError && shown.length === 0 && notice(t("polyforge.workItems.empty"))}
        {shown.map((row) => (
          <button key={row.id} type="button" data-pf-work-item={row.id} onClick={() => openDetail(row.id)}
            style={{
              display: "flex", flexDirection: "column", alignItems: "stretch", gap: 2, width: "100%", minWidth: 0, flexShrink: 0,
              padding: "6px 12px", background: "none", border: "none", borderBottom: "1px solid var(--border)",
              cursor: "pointer", textAlign: "left", font: "inherit", fontSize: 12, color: "var(--text)",
            }}>
            <span style={{ display: "flex", alignItems: "baseline", gap: 6, minWidth: 0, fontSize: 11, color: "var(--text-dim)" }}>
              <span style={{ fontFamily: "var(--font-mono)", color: "var(--text)", flexShrink: 0 }}>{row.slug}</span>
              <span style={{ color: row.status === "running" ? "var(--accent)" : "var(--text-muted)", flexShrink: 0 }}>{statusLabel(row.status)}</span>
              {row.acceptance_missing && <span style={{ color: "var(--text-dim)", flexShrink: 0 }}>{t("polyforge.workItems.acceptanceMissing")}</span>}
              <span style={{ flexShrink: 0 }}>{row.priority ?? "—"}</span>
              <span style={{ flexShrink: 0 }}>{row.bundle_name || "—"}</span>
              <span style={{ marginLeft: "auto", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0 }}>{row.project ?? "—"}</span>
            </span>
            {/* One line, clipped by CSS only — the goal is never cut in code. */}
            <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0 }}>{row.goal ?? ""}</span>
          </button>
        ))}
      </div>
      {detailView && renderDetail(detailView)}
    </div>
  );
}
