/**
 * Client-safe pure model for the Memory tab (browse mode
 *): the wire types, the six type-filter values, the display
 * constants (page size, pf-side result limit, browse page size, collapse
 * threshold), the id/tag validators, the newest-first sort, the page slicer,
 * the browse-page merge, the UTF-8 cap (shared with the Tasks server) and the
 * truncation test. No Node import lives here, so both the server modules and
 * the client panels can use it.
 */

/** One recall hit, exactly the field set the sanitizer forwards (no body). */
export interface PolyforgeMemoryHit {
  id: string;
  type: string;
  title: string;
  visibility: string;
  status: string;
  tags: string[];
  related: string[];
  work_item_id: string | null;
  created_at: string;
  updated_at: string;
  content_len: number;
}

/** One single-memory read: the hit's fields plus the (capped) body. */
export interface PolyforgeMemoryDetail extends PolyforgeMemoryHit {
  content: string;
}

/** One browse page: `pf --json recall` with no query —
 *  the page's hits, the project total aihub reports and, while more entries
 *  remain, the opaque cursor of the next page. */
export interface PolyforgeMemoryPage {
  items: PolyforgeMemoryHit[];
  total: number;
  next_cursor?: string;
}

/** The fixed type dropdown (spec: `*` for all, else a `prefix.*` family). */
export const MEMORY_TYPE_FILTERS = [
  "*",
  "doc.*",
  "experience.*",
  "fact.*",
  "methodology.*",
  "rule.*",
] as const;

export type MemoryTypeFilter = (typeof MEMORY_TYPE_FILTERS)[number];

/** Client-side page size (rows per page). */
export const MEMORY_PAGE_SIZE = 20;

/** pf's own per-search result bound (`--limit`); also the narrowing notice's trigger. */
export const MEMORY_RESULT_LIMIT = 200;

/** Browse page size: one `pf recall --limit 20` per page. */
export const MEMORY_BROWSE_LIMIT = 20;

/** A body whose `content_len` exceeds this starts collapsed (Assumption 1). */
export const MEMORY_COLLAPSE_BYTES = 2000;

const MEMORY_ID_TOKEN = /^mem_[A-Za-z0-9]+$/;
const TAG_TOKEN = /^[A-Za-z0-9._:-]+$/;

/** A memory id: `mem_` + token. Anything else never reaches a spawn argv. */
export function isPolyforgeMemoryId(value: unknown): value is string {
  return typeof value === "string" && MEMORY_ID_TOKEN.test(value);
}

/** A non-empty comma-separated list of tag-shaped tokens, or false. */
export function isMemoryTagList(value: unknown): value is string {
  if (typeof value !== "string") return false;
  const trimmed = value.trim();
  if (!trimmed) return false;
  const parts = trimmed.split(",");
  return parts.length > 0 && parts.every((part) => TAG_TOKEN.test(part));
}

/** UTF-8 byte length of a string (the truncation and collapse tests). */
export function memoryUtf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

/** Newest first by `updated_at` (pf answers relevance order; the display
 *  order is ours). Unparseable timestamps fall back to a string compare so
 *  the sort stays deterministic. Pure, never throws. */
export function sortMemoryHitsNewestFirst(hits: PolyforgeMemoryHit[]): PolyforgeMemoryHit[] {
  return [...hits].sort((a, b) => {
    const at = Date.parse(a.updated_at);
    const bt = Date.parse(b.updated_at);
    if (Number.isFinite(at) && Number.isFinite(bt) && at !== bt) return bt - at;
    return b.updated_at.localeCompare(a.updated_at);
  });
}

/** One 1-based page of hits (20 rows); out-of-range pages are empty. Pure. */
export function memoryPage(hits: PolyforgeMemoryHit[], page: number): PolyforgeMemoryHit[] {
  if (!Number.isSafeInteger(page) || page < 1) return [];
  const start = (page - 1) * MEMORY_PAGE_SIZE;
  return hits.slice(start, start + MEMORY_PAGE_SIZE);
}

/** "Load more": the next browse page appended after the rows already shown,
 *  in its own (updated-desc) order, skipping any id already present — an
 *  entry updated between two pages never shows twice. Pure. */
export function mergeMemoryPages(previous: PolyforgeMemoryHit[], page: PolyforgeMemoryHit[]): PolyforgeMemoryHit[] {
  const seen = new Set(previous.map((hit) => hit.id));
  const merged = [...previous];
  for (const hit of page) {
    if (seen.has(hit.id)) continue;
    seen.add(hit.id);
    merged.push(hit);
  }
  return merged;
}

/** Cut a text to at most `cap` UTF-8 bytes on a code-point boundary (the
 *  memory body and the task report forward caps share this one rule). */
export function capUtf8Bytes(value: string, cap: number): string {
  if (memoryUtf8ByteLength(value) <= cap) return value;
  const encoder = new TextEncoder();
  let bytes = 0;
  let end = 0;
  for (const char of value) {
    const size = encoder.encode(char).length;
    if (bytes + size > cap) break;
    bytes += size;
    end += char.length;
  }
  return value.slice(0, end);
}

/** The forwarded body is shorter than pf says the memory is (Assumption 4):
 *  the visible truncation notice. Never a guess — equal lengths read whole. */
export function isMemoryBodyTruncated(content: string, contentLen: number): boolean {
  if (typeof contentLen !== "number" || !Number.isFinite(contentLen) || contentLen <= 0) return false;
  return memoryUtf8ByteLength(content) < contentLen;
}
