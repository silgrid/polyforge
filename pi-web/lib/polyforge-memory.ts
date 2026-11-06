import { execFileAsync } from "./exec-file";
import { parsePfError } from "./polyforge-config-write";
import { isPolyforgeVersionBelowMinimum } from "./polyforge-detect";
import { polyforgePfBin } from "./polyforge-pf-bin";
import { probePolyforgeVersion } from "./polyforge-status";
import { workspaceProjects } from "../components/polyforge-config-helpers";
import {
  capUtf8Bytes,
  isMemoryTagList,
  isPolyforgeMemoryId,
  MEMORY_BROWSE_LIMIT,
  MEMORY_RESULT_LIMIT,
  MEMORY_TYPE_FILTERS,
  type MemoryTypeFilter,
  type PolyforgeMemoryDetail,
  type PolyforgeMemoryHit,
  type PolyforgeMemoryPage,
} from "./polyforge-memory-pure";

/**
 * Server-side reads for the Memory tab and its browse mode.
 * Every fact comes from
 * a `pf` subprocess through `execFileAsync` (no shell); every argv value is
 * validated here, before any spawn. The HTTP faces under
 * `app/api/polyforge/memory/**` only degrade and answer.
 *
 * The sanitizers are the only passport off this module: a hit keeps exactly
 * id/type/title/visibility/status/tags/related/work_item_id/created_at/
 * updated_at/content_len and the detail adds `content` (capped at
 * MEMORY_BODY_FORWARD_CAP); a browse page is those hits plus a finite
 * non-negative `total` and a validated `next_cursor`. `owner_user_id`, `project_id`, `content_sha`,
 * `content_excerpt` and every unknown field never leave the server, and no
 * response carries the pf api key, user id, server address or an environment
 * value. No memory body is ever logged.
 */

/** The first pf whose `recall` browses with no query (`--after`, `--sort`).
 *  Below it the tab shows its upgrade state, no fallback. */
export const POLYFORGE_MEMORY_MINIMUM_PF_VERSION = "2.20.12";

/** The body this module forwards, cut on a code-point boundary (Assumption 4). */
export const MEMORY_BODY_FORWARD_CAP = 262144;

const READ_TIMEOUT_MS = 15000;
/** A large recall answer degrades through the normal path, not an unbounded
 *  buffer (D5): 8 MiB is far above a 200-hit sanitized-in-flight answer. */
const SPAWN_MAX_BUFFER = 8 * 1024 * 1024;
/** pf's own stderr passes through under this cap; nothing else is echoed. */
const STDERR_CAP = 512;
/** A cwd is a path: this bound keeps argv bounded, nothing more. */
const CWD_MAX_BYTES = 4096;
/** A browse cursor is opaque (aihub's keyset token): bounded, never parsed. */
const CURSOR_MAX_BYTES = 1024;

export type ValidationResult<T> = { ok: true; value: T } | { ok: false; error: string };

export { isMemoryTagList, isPolyforgeMemoryId } from "./polyforge-memory-pure";
export type { MemoryTypeFilter, PolyforgeMemoryDetail, PolyforgeMemoryHit, PolyforgeMemoryPage } from "./polyforge-memory-pure";

/** A search after validation, in the CLI's own face. */
export interface PolyforgeMemorySearchInput {
  cwd: string;
  q: string;
  project: string;
  type: MemoryTypeFilter;
  tags?: string;
  any?: boolean;
}

/** A browse page request after validation: no keyword. */
export interface PolyforgeMemoryBrowseInput {
  cwd: string;
  project: string;
  type: MemoryTypeFilter;
  tags?: string;
  after?: string;
}

/** pf's typed/plain failure text, bounded: never our argv, never env values. */
export class PolyforgeMemoryError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PolyforgeMemoryError";
  }

  static fromSpawnError(error: unknown): PolyforgeMemoryError {
    if (error instanceof Error) {
      const stderr = String((error as { stderr?: unknown }).stderr ?? "").trim();
      if (stderr) {
        return new PolyforgeMemoryError(parsePfError(stderr).message.slice(0, STDERR_CAP));
      }
    }
    return new PolyforgeMemoryError("polyforge is unavailable");
  }
}

// ---- validation (shape only, before any spawn) ----

function hasControlBytes(value: string): boolean {
  for (const char of value) {
    const code = char.codePointAt(0) ?? 0;
    if (code < 0x20 || code === 0x7f) return true;
  }
  return false;
}

const PROJECT_NAME = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/;

function validCwd(cwd: unknown): cwd is string {
  return typeof cwd === "string" && !!cwd.trim() && !hasControlBytes(cwd) && Buffer.byteLength(cwd, "utf8") <= CWD_MAX_BYTES;
}

/** An opaque browse cursor as it may reach an argv: non-empty, bounded, no
 *  control bytes and no leading `-` (never read as a flag). */
export function isMemoryBrowseCursor(value: unknown): value is string {
  return typeof value === "string" && value !== "" && !value.startsWith("-") && !hasControlBytes(value)
    && Buffer.byteLength(value, "utf8") <= CURSOR_MAX_BYTES;
}

/** Validate the search face's raw params (Requirement 3 / D4): every value
 *  is checked here, so a refusal answers 400 with no pf process at all —
 *  the version probe included. Project membership is NOT here: "one of the
 *  projects the panel offered" needs the config read that defines it. */
export function validateMemorySearch(params: {
  cwd: unknown;
  q: unknown;
  project: unknown;
  type: unknown;
  tags?: unknown;
  any?: unknown;
}): ValidationResult<PolyforgeMemorySearchInput> {
  const cwd = params.cwd;
  if (!validCwd(cwd)) {
    return { ok: false, error: "cwd must be a directory path" };
  }
  const q = params.q;
  if (typeof q !== "string" || !q.trim()) return { ok: false, error: "a keyword is required" };
  if (q.startsWith("-")) return { ok: false, error: "the keyword must not start with '-'" };
  if (hasControlBytes(q)) return { ok: false, error: "the keyword carries control bytes" };
  const project = params.project;
  if (typeof project !== "string" || !PROJECT_NAME.test(project)) {
    return { ok: false, error: "project must be a project name" };
  }
  const type = params.type;
  if (typeof type !== "string" || !(MEMORY_TYPE_FILTERS as readonly string[]).includes(type)) {
    return { ok: false, error: "type must be one of the six dropdown values" };
  }
  const tags = params.tags;
  if (tags !== undefined && tags !== "" && !isMemoryTagList(tags)) {
    return { ok: false, error: "tags must be a comma-separated list of tag tokens" };
  }
  const value: PolyforgeMemorySearchInput = {
    cwd,
    q,
    project,
    type: type as MemoryTypeFilter,
  };
  if (tags !== undefined && tags !== "") value.tags = tags;
  if (params.any === "1" || params.any === true) value.any = true;
  return { ok: true, value };
}

/** Validate the browse face's raw params: the search face's
 *  cwd/project/type/tags rules plus an optional cursor, all before any
 *  spawn. Membership stays the route's (it needs the config read). */
export function validateMemoryBrowse(params: {
  cwd: unknown;
  project: unknown;
  type: unknown;
  tags?: unknown;
  after?: unknown;
}): ValidationResult<PolyforgeMemoryBrowseInput> {
  if (!validCwd(params.cwd)) return { ok: false, error: "cwd must be a directory path" };
  const project = params.project;
  if (typeof project !== "string" || !PROJECT_NAME.test(project)) {
    return { ok: false, error: "project must be a project name" };
  }
  const type = params.type ?? "*";
  if (typeof type !== "string" || !(MEMORY_TYPE_FILTERS as readonly string[]).includes(type)) {
    return { ok: false, error: "type must be one of the six dropdown values" };
  }
  const tags = params.tags;
  if (tags !== undefined && tags !== "" && !isMemoryTagList(tags)) {
    return { ok: false, error: "tags must be a comma-separated list of tag tokens" };
  }
  const after = params.after;
  if (after !== undefined && !isMemoryBrowseCursor(after)) {
    return { ok: false, error: "after must be a browse cursor" };
  }
  const value: PolyforgeMemoryBrowseInput = { cwd: params.cwd, project, type: type as MemoryTypeFilter };
  if (tags !== undefined && tags !== "") value.tags = tags;
  if (after !== undefined) value.after = after;
  return { ok: true, value };
}

/** The exact browse argv: one `pf recall` with no query,
 *  one 20-row page in the fixed updated-desc order, the cursor and the two
 *  filters opt-in. Exported for tests. */
export function buildBrowseRecallArgv(input: PolyforgeMemoryBrowseInput): string[] {
  const argv = ["--json", "recall", "--project", input.project, "--limit", String(MEMORY_BROWSE_LIMIT), "--sort", "updated"];
  if (input.after) argv.push("--after", input.after);
  if (input.tags) argv.push("--tags", input.tags);
  if (input.type !== "*") argv.push("--type", input.type);
  return argv;
}

/** The exact search argv (D2): one `pf recall`, the keyword as the one
 *  positional, `--limit 200`, and the three optional filters opt-in. `*` is
 *  sent as no `--type` (the default search). Exported for tests. */
export function buildRecallArgv(input: PolyforgeMemorySearchInput): string[] {
  const argv = ["--json", "recall", "--project", input.project, "--limit", String(MEMORY_RESULT_LIMIT)];
  if (input.any) argv.push("--any");
  if (input.tags) argv.push("--tags", input.tags);
  if (input.type !== "*") argv.push("--type", input.type);
  argv.push(input.q);
  return argv;
}

// ---- pure sanitizers (the only passport) ----

function str(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function num(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? Math.max(0, Math.trunc(value)) : 0;
}

function strList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string" && item.trim() !== "").map((item) => item.trim());
}

/** One hit, cut to the allowed field set. `owner_user_id`, `project_id`,
 *  `content_sha`, `content_excerpt` and unknown fields never pass; an
 *  invalid id drops the row. */
export function sanitizeMemoryHit(raw: unknown): PolyforgeMemoryHit | null {
  if (!raw || typeof raw !== "object") return null;
  const row = raw as Record<string, unknown>;
  const id = str(row.id);
  if (!isPolyforgeMemoryId(id)) return null;
  const workItem = str(row.work_item_id);
  return {
    id,
    type: str(row.type),
    title: str(row.title),
    visibility: str(row.visibility),
    status: str(row.status),
    tags: strList(row.tags),
    related: strList(row.related),
    work_item_id: workItem || null,
    created_at: str(row.created_at),
    updated_at: str(row.updated_at),
    content_len: num(row.content_len),
  };
}

/** The recall answer: only its `items` list, each through the hit passport. */
export function sanitizeMemorySearchAnswer(raw: unknown): PolyforgeMemoryHit[] {
  if (!raw || typeof raw !== "object") return [];
  const items = (raw as { items?: unknown }).items;
  if (!Array.isArray(items)) return [];
  return items.flatMap((entry) => {
    const hit = sanitizeMemoryHit(entry);
    return hit ? [hit] : [];
  });
}

/** A browse answer: the hits through the hit passport, a finite
 *  non-negative `total` (the page length when pf's is missing or malformed)
 *  and `next_cursor` only when it is a valid cursor. */
export function sanitizeMemoryBrowseAnswer(raw: unknown): PolyforgeMemoryPage {
  const items = sanitizeMemorySearchAnswer(raw);
  const answer = raw && typeof raw === "object" ? raw as { total?: unknown; next_cursor?: unknown } : {};
  const total = typeof answer.total === "number" && Number.isFinite(answer.total) && answer.total >= 0
    ? Math.trunc(answer.total)
    : items.length;
  const page: PolyforgeMemoryPage = { items, total };
  if (isMemoryBrowseCursor(answer.next_cursor)) page.next_cursor = answer.next_cursor;
  return page;
}

/** One single-memory read: the hit's field set plus `content`, capped at
 *  MEMORY_BODY_FORWARD_CAP. There is no truncation wire field — the client
 *  compares the body with `content_len` (D6). */
export function sanitizeMemoryDetail(raw: unknown): PolyforgeMemoryDetail | null {
  const hit = sanitizeMemoryHit(raw);
  if (!hit) return null;
  const content = typeof (raw as Record<string, unknown>).content === "string"
    ? (raw as Record<string, unknown>).content as string
    : "";
  return { ...hit, content: capUtf8Bytes(content, MEMORY_BODY_FORWARD_CAP) };
}

// ---- transport ----

async function runPfJson(argv: string[], timeoutMs = READ_TIMEOUT_MS): Promise<unknown> {
  try {
    const { stdout } = await execFileAsync(polyforgePfBin(), argv, { timeout: timeoutMs, maxBuffer: SPAWN_MAX_BUFFER });
    return JSON.parse(stdout);
  } catch (error) {
    throw PolyforgeMemoryError.fromSpawnError(error);
  }
}

export interface PolyforgeMemoryCapability {
  pfPresent: boolean;
  pfVersion: string | null;
  capable: boolean;
  reason?: string;
}

/** The Memory floor gate: presence + the 2.20.12 capability floor, from the
 *  one existing version probe. Below it no memory verb is spawned. */
export async function readPolyforgeMemoryCapability(): Promise<PolyforgeMemoryCapability> {
  const presence = await probePolyforgeVersion();
  if (!presence.present) {
    return { pfPresent: false, pfVersion: null, capable: false };
  }
  if (isPolyforgeVersionBelowMinimum(presence.version, POLYFORGE_MEMORY_MINIMUM_PF_VERSION)) {
    return {
      pfPresent: true,
      pfVersion: presence.version,
      capable: false,
      reason: `the Memory tab needs pf ${POLYFORGE_MEMORY_MINIMUM_PF_VERSION} or newer (found ${presence.version ?? "an unparseable version"})`,
    };
  }
  return { pfPresent: true, pfVersion: presence.version, capable: true };
}

// ---- the project scope (D3: the config panel's cwd→workspace rule) ----

/** The projects of the workspace the cwd maps to (Assumption 8): first
 *  `pf --json config workspace --cwd <cwd>`, then the
 *  `[workspace."<name>"]` item of `pf --json config list`, split with the
 *  config panel's own `workspaceProjects` helper — one rule, one place. An
 *  unmapped cwd answers `{ mapped: false }` with no second read. A spawn
 *  failure throws (the route degrades it); an unparsable answer maps to
 *  nothing, never a guess. */
export async function readPolyforgeMemoryProjects(cwd: string): Promise<{ mapped: boolean; projects: string[] }> {
  const workspaceAnswer = await runPfJson(["--json", "config", "workspace", "--cwd", cwd]);
  const mapped = !!(workspaceAnswer && typeof workspaceAnswer === "object"
    && (workspaceAnswer as { mapped?: unknown }).mapped === true);
  if (!mapped) return { mapped: false, projects: [] };
  const workspace = str((workspaceAnswer as { workspace?: unknown }).workspace);
  if (!workspace) return { mapped: false, projects: [] };
  const listAnswer = await runPfJson(["--json", "config", "list"]);
  const items = listAnswer && typeof listAnswer === "object" && Array.isArray((listAnswer as { items?: unknown }).items)
    ? (listAnswer as { items: unknown[] }).items
    : [];
  const wantedKey = `[workspace."${workspace}"]`;
  for (const item of items) {
    if (!item || typeof item !== "object") continue;
    const row = item as { key?: unknown; value?: unknown };
    if (row.key === wantedKey) return { mapped: true, projects: workspaceProjects(row.value) };
  }
  return { mapped: false, projects: [] };
}

// ---- the two read faces (D2) ----

/** The search face: exactly one `pf recall` (the argv of buildRecallArgv).
 *  The caller has validated the shape and the capability; membership is the
 *  route's, so this function only transports and sanitizes. */
export async function searchPolyforgeMemory(input: PolyforgeMemorySearchInput): Promise<PolyforgeMemoryHit[]> {
  const raw = await runPfJson(buildRecallArgv(input));
  return sanitizeMemorySearchAnswer(raw);
}

/** The browse face: exactly one `pf recall` with no query
 *  (the argv of buildBrowseRecallArgv). Validation, capability and
 *  membership are the caller's; this only transports and sanitizes. */
export async function browsePolyforgeMemory(input: PolyforgeMemoryBrowseInput): Promise<PolyforgeMemoryPage> {
  const raw = await runPfJson(buildBrowseRecallArgv(input));
  return sanitizeMemoryBrowseAnswer(raw);
}

/** The single-memory face: exactly one `pf memory show`, body capped. A
 *  pf-side miss (MEM-404) throws pf's own text; a malformed upstream answer
 *  degrades to null, never a guess. */
export async function showPolyforgeMemory(id: string): Promise<PolyforgeMemoryDetail | null> {
  const raw = await runPfJson(["--json", "memory", "show", id]);
  return sanitizeMemoryDetail(raw);
}
