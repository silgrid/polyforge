/**
 * Session hover prefetch (pi#83).
 *
 * Pointer-hover or keyboard-focus on a sidebar session row starts the same
 * detail request the tab will issue on click. The parsed response is fed
 * into the session view snapshot cache (lib/session-view-cache), so the
 * subsequent mount takes the snapshot fast path — history paints
 * immediately and the background freshness read lands on the server build
 * cache (lib/context-build-cache) instead of rebuilding.
 *
 * In-flight dedupe keeps rapid hover jitter to one request per session.
 */

import { setSessionViewSnapshot } from "./session-view-cache.ts";

const inflight = new Map<string, Promise<boolean>>();

export function prefetchSessionView(sessionId: string): Promise<boolean> {
  const existing = inflight.get(sessionId);
  if (existing) return existing;
  const params = new URLSearchParams({ deferThinking: "1", deferMedia: "1", tree: "summary" });
  const flight = (async () => {
    try {
      const response = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}?${params}`);
      if (!response.ok) return false;
      const data = await response.json() as {
        snapshotRevision?: string;
        leafId?: string | null;
        tree?: unknown;
        context?: {
          messages?: unknown[];
          entryIds?: string[];
          oldestEntryId?: string | null;
          hasMore?: boolean;
          turnsBefore?: number;
          thinkingLevel?: string;
          model?: { provider: string; modelId: string } | null;
        };
        stats?: unknown;
        totalActiveMs?: number;
      };
      if (!data.snapshotRevision || !data.context) return false;
      return setSessionViewSnapshot({
        sessionId,
        revision: data.snapshotRevision,
        messages: (data.context.messages ?? []) as never[],
        entryIds: data.context.entryIds ?? [],
        leafId: data.leafId ?? null,
        oldestEntryId: data.context.oldestEntryId ?? null,
        hasMore: data.context.hasMore ?? false,
        // the unloaded-turn offset travels with its cursor.
        turnsBefore: data.context.turnsBefore ?? 0,
        summaryTree: data.tree,
        thinkingLevel: data.context.thinkingLevel ?? "",
        model: data.context.model ?? null,
        stats: data.stats,
        totalActiveMs: data.totalActiveMs,
        loadedEntryIds: [],
      });
    } catch {
      return false;
    }
  })().finally(() => inflight.delete(sessionId));
  inflight.set(sessionId, flight);
  return flight;
}
