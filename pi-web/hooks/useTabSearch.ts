"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import {
  buildSearchMatcher,
  collectTextMatches,
  paintSearchHighlights,
  rangeHostElement,
  scrollRangeToCenter,
} from "@/lib/tab-search";
import type { BranchSearchMatch } from "@/lib/session-branch-search";

/**
 * In-tab search controller (pi#80): owns the query state, paints local DOM
 * matches via the CSS Custom Highlight API, fetches the server's
 * active-branch match list for EARLIER (unloaded) history, and navigates —
 * paging history backward until an earlier match's entry lands, then
 * re-highlighting inside it.
 *
 * Navigation order follows the conversation: index 0 is the OLDEST loaded
 * match; "next" moves toward newer. Earlier (unloaded) matches extend the
 * sequence below index 0 conceptually — stepping into them pages them in.
 */
export interface UseTabSearchOptions {
  /** Master gate (the owning pane is active and has content to search). */
  enabled: boolean;
  /** Root of the subtree to search/highlight (the chat message column). */
  contentRef: RefObject<HTMLElement | null>;
  /** Scroll container used to center matches. */
  scrollContainerRef: RefObject<HTMLElement | null>;
  sessionId: string | null;
  activeLeafId: string | null;
  /** loadContext(sessionId, leafId, before, opts) from useAgentSession. */
  loadContext: (
    sessionId: string,
    leafId: string | null,
    before?: string | null,
    options?: { tail?: number; signal?: AbortSignal },
  ) => Promise<{ entryIds: string[]; oldestEntryId: string | null; hasMore: boolean } | null | undefined>;
  /** entryIds of ALL loaded history (from useAgentSession). */
  loadedEntryIds: string[];
  historyCursor: string | null;
  hasEarlierMessages: boolean;
  /** Repaint triggers: message content/entry set changed. */
  repaintKey?: string | number;
}

/** How many 50-entry pages navigation may rewind to land an earlier match. */
const MAX_NAVIGATION_PAGES = 40;
const DEBOUNCE_MS = 200;

export function useTabSearch(options: UseTabSearchOptions) {
  const {
    enabled,
    contentRef,
    scrollContainerRef,
    sessionId,
    activeLeafId,
    loadContext,
    loadedEntryIds,
    historyCursor,
    hasEarlierMessages,
    repaintKey,
  } = options;

  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [caseSensitive, setCaseSensitive] = useState(false);
  const [regexEnabled, setRegexEnabled] = useState(false);
  const [localRanges, setLocalRanges] = useState<Range[]>([]);
  const [currentLocalIndex, setCurrentLocalIndex] = useState(-1);
  const [earlierMatches, setEarlierMatches] = useState<BranchSearchMatch[] | null>(null);
  const [loadingEarlier, setLoadingEarlier] = useState(false);
  const [navigatingEarlier, setNavigatingEarlier] = useState(false);

  const matcher = useMemo(
    () => buildSearchMatcher(query, { caseSensitive, regex: regexEnabled }),
    [query, caseSensitive, regexEnabled],
  );

  const loadedSet = useMemo(() => new Set(loadedEntryIds), [loadedEntryIds]);

  // The "current" match is identified by entry + occurrence so a repaint
  // (streaming, page load) can restore it after ranges are rebuilt.
  const currentAnchorRef = useRef<{ entryId: string | null; occurrence: number } | null>(null);

  const recollect = useCallback(() => {
    if (!matcher || !open) {
      setLocalRanges([]);
      return;
    }
    const ranges = collectTextMatches(contentRef.current, matcher);
    setLocalRanges(ranges);
  }, [matcher, open, contentRef]);

  // Recollect + restore the current anchor whenever the query, the toggles
  // or the rendered content change. Debounced: typing should feel instant.
  useEffect(() => {
    if (!open) return;
    const handle = setTimeout(() => {
      recollect();
      setCurrentLocalIndex(-1);
      currentAnchorRef.current = null;
    }, DEBOUNCE_MS);
    return () => clearTimeout(handle);
    // repaintKey covers streaming/page-load DOM changes.
  }, [open, query, caseSensitive, regexEnabled, repaintKey, recollect]);

  // Repaint on any structural state change (without resetting the index).
  useEffect(() => {
    if (!open) return;
    const anchor = currentAnchorRef.current;
    const ranges = localRanges;
    if (anchor) {
      // Re-identify the current range from the anchor entry.
      let index = -1;
      let occurrence = 0;
      for (let i = 0; i < localRanges.length; i++) {
        const host = rangeHostElement(localRanges[i]);
        const entryId = host?.closest<HTMLElement>("[data-entry-id]")?.dataset.entryId ?? null;
        if (entryId === anchor.entryId) {
          if (occurrence === anchor.occurrence) {
            index = i;
            break;
          }
          occurrence++;
        }
      }
      if (index >= 0) setCurrentLocalIndex(index);
    }
    const current = ranges[Math.min(currentLocalIndex, ranges.length - 1)] ?? null;
    paintSearchHighlights(ranges, current);
  }, [open, localRanges, currentLocalIndex]);

  // Clear highlights when the bar closes or the pane unmounts.
  useEffect(() => {
    if (!open) paintSearchHighlights([], null);
  }, [open]);
  useEffect(() => () => paintSearchHighlights([], null), []);

  // Fetch the server's branch match list for the counter. Only earlier
  // (unloaded) entries matter: loaded ones are already highlighted locally.
  useEffect(() => {
    if (!open || !enabled || !sessionId || !matcher || !hasEarlierMessages) {
      setEarlierMatches(null);
      setLoadingEarlier(false);
      return;
    }
    const controller = new AbortController();
    const handle = setTimeout(async () => {
      setLoadingEarlier(true);
      try {
        const params = new URLSearchParams({ q: query.trim() });
        if (activeLeafId) params.set("leafId", activeLeafId);
        if (caseSensitive) params.set("case", "1");
        if (regexEnabled) params.set("regex", "1");
        const response = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/search?${params}`, {
          signal: controller.signal,
        });
        if (!response.ok) throw new Error(String(response.status));
        const data = (await response.json()) as { matches?: BranchSearchMatch[] };
        setEarlierMatches((data.matches ?? []).filter((match) => !loadedSet.has(match.entryId)));
      } catch (error) {
        if ((error as Error).name !== "AbortError") setEarlierMatches(null);
      } finally {
        setLoadingEarlier(false);
      }
    }, DEBOUNCE_MS + 50);
    return () => {
      clearTimeout(handle);
      controller.abort();
    };
    // loadedSet intentionally excluded: refetching on every streamed entry
    // would hammer the endpoint during long runs; the filter below trims.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, enabled, sessionId, query, caseSensitive, regexEnabled, hasEarlierMessages, activeLeafId]);

  // Trim earlierMatches when their entries become loaded (navigation or
  // the user's own "load earlier"): once visible they are local matches.
  useEffect(() => {
    setEarlierMatches((current) =>
      current ? current.filter((match) => !loadedSet.has(match.entryId)) : current,
    );
  }, [loadedSet]);

  const earlierTotal = useMemo(
    () => earlierMatches?.reduce((sum, match) => sum + match.count, 0) ?? 0,
    [earlierMatches],
  );

  // Navigation into earlier history: rewind pages until the target entry
  // lands, then re-highlight and select its first local match.
  const locateEarlierMatch = useCallback(
    async (target: BranchSearchMatch) => {
      if (!sessionId || navigatingEarlier) return;
      setNavigatingEarlier(true);
      try {
        const history = { cursor: historyCursor, more: hasEarlierMessages };
        let before = history.cursor;
        let hasMore = history.more;
        let landed = loadedSet.has(target.entryId);
        const controller = new AbortController();
        for (let page = 0; page < MAX_NAVIGATION_PAGES && hasMore && before && !landed; page++) {
          const context = await loadContext(sessionId, activeLeafId, before, {
            tail: 200,
            signal: controller.signal,
          });
          if (!context) break;
          landed = context.entryIds.includes(target.entryId);
          if (context.oldestEntryId === before) break; // no progress guard
          before = context.oldestEntryId;
          hasMore = context.hasMore;
        }
        if (!landed) return;
        // Wait for React to commit the prepended page, then highlight.
        const deadline = Date.now() + 5000;
        while (Date.now() < deadline) {
          const element = contentRef.current?.querySelector(
            `[data-entry-id="${CSS.escape(target.entryId)}"]`,
          );
          if (element) break;
          await new Promise((resolve) => requestAnimationFrame(resolve));
        }
        recollect();
        // The repaint effect picks this anchor on the next ranges commit and
        // selects the FIRST match inside the target entry.
        currentAnchorRef.current = { entryId: target.entryId, occurrence: 0 };
      } finally {
        setNavigatingEarlier(false);
      }
    },
    [sessionId, activeLeafId, loadContext, historyCursor, hasEarlierMessages, loadedSet, contentRef, recollect, navigatingEarlier],
  );

  // Stepping below the oldest local match pages in the next earlier target.
  // -2 is the "step into earlier" sentinel set by goPrevious; landing the
  // page re-runs collect, and the repaint effect restores the current match
  // from the anchor that locateEarlierMatch recorded.
  useEffect(() => {
    if (!open || currentLocalIndex !== -2 || !earlierMatches || earlierMatches.length === 0) return;
    setCurrentLocalIndex(-1);
    void locateEarlierMatch(earlierMatches[0]); // newest earlier match (closest above)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, currentLocalIndex, earlierMatches]);

  const goNext = useCallback(() => {
    if (localRanges.length === 0) return;
    setCurrentLocalIndex((index) => Math.min(index + 1, localRanges.length - 1));
  }, [localRanges]);

  const goPrevious = useCallback(() => {
    if (localRanges.length === 0) return;
    setCurrentLocalIndex((index) => {
      if (index <= 0) {
        if (earlierMatches && earlierMatches.length > 0) return -2; // page in earlier history
        return 0;
      }
      return index - 1;
    });
  }, [localRanges, earlierMatches]);

  // Scroll the current match to the viewport center.
  useEffect(() => {
    if (!open || currentLocalIndex < 0 || currentLocalIndex >= localRanges.length) return;
    const range = localRanges[currentLocalIndex];
    const host = rangeHostElement(range);
    const entryId = host?.closest<HTMLElement>("[data-entry-id]")?.dataset.entryId ?? null;
    let occurrence = 0;
    for (let i = 0; i < currentLocalIndex; i++) {
      const previousHost = rangeHostElement(localRanges[i]);
      if (previousHost?.closest<HTMLElement>("[data-entry-id]")?.dataset.entryId === entryId) occurrence++;
    }
    currentAnchorRef.current = { entryId, occurrence };
    scrollRangeToCenter(range, scrollContainerRef.current);
  }, [currentLocalIndex, localRanges, open, scrollContainerRef]);

  const openBar = useCallback(() => {
    setOpen(true);
    // Focus the input after mount.
    setTimeout(() => {
      const input = document.querySelector<HTMLInputElement>("[data-testid='tab-search-input']");
      input?.focus();
      input?.select();
    }, 0);
  }, []);

  const closeBar = useCallback(() => {
    setOpen(false);
    setQuery("");
    setCurrentLocalIndex(-1);
    currentAnchorRef.current = null;
    setEarlierMatches(null);
  }, []);

  return {
    open,
    query,
    setQuery,
    caseSensitive,
    toggleCase: () => setCaseSensitive((value) => !value),
    regexEnabled,
    toggleRegex: () => setRegexEnabled((value) => !value),
    localCount: localRanges.length,
    earlierCount: earlierTotal,
    loadingEarlier: loadingEarlier || navigatingEarlier,
    current: currentLocalIndex >= 0 ? currentLocalIndex + 1 : 0,
    total: localRanges.length + earlierTotal,
    goNext,
    goPrevious,
    openBar,
    closeBar,
    /** True when a query with no local matches still has earlier hits. */
    hasEarlierMatches: (earlierMatches?.length ?? 0) > 0,
  };
}
