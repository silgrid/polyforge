/**
 * Per-session active-branch search (pi#80).
 *
 * The in-tab search bar uses this to answer two questions the DOM cannot:
 * how many matches live in EARLIER (unloaded) history, and which entries
 * hold them so navigation can page backward to one. Scope is the ACTIVE
 * BRANCH only (leafId walk, same semantics as buildSessionContext), so the
 * counts describe exactly the conversation the user is looking at.
 */

import type { SessionEntry } from "./types";
import { sliceActiveBranch } from "./session-reader.ts";

export interface BranchSearchMatch {
  entryId: string;
  count: number;
}

export interface BranchSearchResponse {
  /** Matches ordered NEWEST -> OLDEST along the active branch. */
  matches: BranchSearchMatch[];
  /** Total match count across the branch (>= sum of per-entry counts). */
  totalMatches: number;
  /** True when the cap cut the scan short. */
  truncated: boolean;
}

const MAX_RESULT_ENTRIES = 500;

/** Text the transcript actually renders for an entry (best effort). */
export function entrySearchText(entry: SessionEntry): string {
  if (entry.type !== "message") return "";
  const message = (entry as { message?: { role?: string; content?: unknown } }).message;
  if (!message || message.role === "system") return "";
  const parts: string[] = [];
  collectContentText(message.content, parts);
  return parts.join("\n");
}

function collectContentText(content: unknown, out: string[]): void {
  if (typeof content === "string") {
    out.push(content);
    return;
  }
  if (!Array.isArray(content)) return;
  for (const block of content) {
    if (!block || typeof block !== "object") continue;
    const record = block as Record<string, unknown>;
    if (record.type === "text" && typeof record.text === "string") out.push(record.text);
    else if (record.type === "thinking" && typeof record.thinking === "string") out.push(record.thinking);
    else if (record.type === "tool_result") collectContentText(record.content, out);
    else if (typeof record.text === "string") out.push(record.text);
  }
}

function buildRegex(query: string, caseSensitive: boolean, regex: boolean): RegExp {
  const source = regex ? query : query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(source, caseSensitive ? "g" : "gi");
}

/**
 * Search the active branch for `query`. `entries` is the full parsed session
 * (all branches); `leafId` anchors the walk exactly like the context route
 * (undefined leaf = newest entry).
 */
export function searchActiveBranch(
  entries: readonly SessionEntry[],
  query: string,
  leafId: string | null | undefined,
  options: { caseSensitive?: boolean; regex?: boolean } = {},
): BranchSearchResponse {
  const needle = query.trim();
  if (!needle) return { matches: [], totalMatches: 0, truncated: false };
  const regexp = buildRegex(needle, Boolean(options.caseSensitive), Boolean(options.regex));

  // sliceActiveBranch(tail = entries.length) walks the WHOLE chain to the
  // root; order is oldest -> newest, the same order history pages render.
  const branch = sliceActiveBranch(entries as SessionEntry[], leafId ?? null, entries.length);
  const matches: BranchSearchMatch[] = [];
  let totalMatches = 0;
  let truncated = false;

  // Walk newest -> oldest so the caller sees the branch top first.
  for (let i = branch.length - 1; i >= 0; i--) {
    const entry = branch[i];
    const text = entrySearchText(entry);
    if (!text) continue;
    regexp.lastIndex = 0;
    let count = 0;
    let match: RegExpExecArray | null;
    while ((match = regexp.exec(text)) !== null) {
      count++;
      if (match[0].length === 0) regexp.lastIndex++; // zero-width guard
      if (count > 5000) break; // pathological-input guard
    }
    if (count === 0) continue;
    totalMatches += count;
    if (matches.length < MAX_RESULT_ENTRIES) {
      matches.push({ entryId: entry.id, count });
    } else {
      truncated = true;
    }
  }

  return { matches, totalMatches, truncated };
}
