"use client";

import { useEffect, useState, type ReactNode } from "react";
import { useI18n } from "@/hooks/useI18n";
import { formatRelativeTime } from "@/lib/i18n/format";
import type { SessionInfo } from "@/lib/types";
import type { SessionSearchResponse } from "@/lib/session-search";

export function SessionSearch({ open, query, children, selectedSessionId, archivedSessionIds, onSelectSession }: {
  open: boolean;
  query: string;
  children: ReactNode;
  selectedSessionId: string | null;
  /** Archived ids (wi pi#103): result rows whose session is archived render a
   *  localized badge chip. Read-only here — the search path NEVER mutates the
   *  archive set; clicking a result keeps the ordinary view-only open. */
  archivedSessionIds?: ReadonlySet<string>;
  onSelectSession: (session: SessionInfo, entryId?: string, blockIndex?: number) => void;
}) {
  const { t, locale } = useI18n();
  const [state, setState] = useState<{ query: string; response?: SessionSearchResponse; failed?: boolean }>({ query: "" });
  const search = query.trim();
  const response = state.query === search ? state.response : undefined;
  const failed = state.query === search && state.failed;

  // Re-runs only when the query changes. It deliberately does not depend on the
  // session-list version: ordinary agent activity bumps that version every few
  // seconds, which used to refetch (and re-order) results while they were being
  // read.
  useEffect(() => {
    if (!open || !search) return;
    const controller = new AbortController();
    setState({ query: search });
    const timer = setTimeout(async () => {
      try {
        const res = await fetch(`/api/sessions/search?${new URLSearchParams({ q: search })}`, { signal: controller.signal });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json() as SessionSearchResponse;
        if (!controller.signal.aborted) setState({ query: search, response: data });
      } catch {
        if (!controller.signal.aborted) setState({ query: search, failed: true });
      }
    }, 300);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [open, search]);

  return !open || !search ? children : (
    <div className="min-h-20 flex-1 overflow-y-auto" aria-busy={!response && !failed}>
      <div role="status" className="px-3 py-2 text-xs text-text-muted">
        {failed ? t("sidebar.sessionSearchFailed") : !response ? t("sidebar.sessionSearching")
          : response.results.length === 0 ? t("sidebar.sessionSearchEmpty")
          : t("sidebar.sessionSearchCount", { count: response.results.length })}
      </div>
      {response?.truncated && (
        <div role="status" className="px-3 pb-2 text-xs text-text-muted">{t("sidebar.sessionSearchPartial")}</div>
      )}
      {response?.results.map(({ session, entryId, blockIndex, titleMatch, before, match, after }) => (
        <button
          key={session.id}
          type="button"
          onClick={() => onSelectSession(session, entryId, blockIndex)}
          aria-current={session.id === selectedSessionId ? "true" : undefined}
          className={`block w-full cursor-pointer border-b border-border px-3 py-2 text-left hover:bg-bg-hover focus-visible:outline-2 focus-visible:outline-accent ${session.id === selectedSessionId ? "bg-bg-selected" : ""}`}
        >
          <span className="flex min-w-0 items-center gap-1.5">
            <span className="min-w-0 truncate text-xs font-medium text-text">
              {titleMatch ? <>{before}<mark className="rounded-sm bg-accent/20 text-text">{match}</mark>{after}</> : session.name || session.firstMessage}
            </span>
            {archivedSessionIds?.has(session.id) && (
              <span className="shrink-0 rounded-[4px] border border-border px-1 text-[10px] leading-[14px] text-text-dim">{t("sidebar.archived")}</span>
            )}
          </span>
          <span className="mt-1 flex min-w-0 gap-2 text-[10px] text-text-dim">
            {/* Archived badge (wi pi#103): the search reaches archived sessions
                (server-side search is untouched — a spec non-goal area); the
                badge marks them in the row's metadata line. Text is an e2e
                locator contract: 已归档 / 已封存 / Archived, exactly. */}
            {archivedSessionIds?.has(session.id) && (
              <span
                title={t("sidebar.archivedBadge")}
                style={{
                  flexShrink: 0,
                  padding: "0 5px",
                  color: "var(--text-dim)",
                  background: "var(--bg-hover)",
                  border: "1px solid var(--border)",
                  borderRadius: 4,
                }}
              >
                {t("sidebar.archivedBadge")}
              </span>
            )}
            <span className="min-w-0 flex-1 truncate" title={session.cwd}>{session.cwd}</span>
            <span className="shrink-0">{formatRelativeTime(session.modified, locale)}</span>
          </span>
          {!titleMatch && (
            <span className="mt-1 block text-xs leading-relaxed wrap-anywhere text-text-muted">
              {before}<mark className="rounded-sm bg-accent/20 text-text">{match}</mark>{after}
            </span>
          )}
        </button>
      ))}
    </div>
  );
}
