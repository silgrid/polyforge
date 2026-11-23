import { getAdditionalAllowedRoots, normalizeSlashes } from "./allowed-roots";
import { isExistingPathWithinRoots, isPathWithinRoots } from "./path-security";
import { listAllSessions } from "./session-reader";
export { allowFileRoot, normalizeSlashes } from "./allowed-roots";
export { isWindowsAbsolutePath } from "./paths";

// Short-TTL cache for the allowed-roots set. Without this, every file list/read
// request re-scans every pi session on disk just to check access. pi-web's own
// writes are immediate: a created session's cwd joins through allowFileRoot(),
// and a successful delete drops the cache through invalidateAllowedFileRoots().
// The 5s TTL is what bounds an *external* change (another pi process creating or
// deleting a session): stale roots stay allowed at most that long. Stored on
// globalThis so it survives Next.js hot-reload.
declare global {
  var __piAllowedRootsCache: { roots: Set<string>; expiresAt: number } | undefined;
  // Bumped by every invalidation: a scan compares it before writing its result
  // back, so a delete that lands mid-scan is never undone by that scan.
  var __piAllowedRootsGeneration: number | undefined;
}

const ALLOWED_ROOTS_TTL_MS = 5_000;

function allowedRootsGeneration(): number {
  return globalThis.__piAllowedRootsGeneration ?? 0;
}

/**
 * Invalidate the allowed-roots cache (polyforge#252). A successful delete
 * drops it, so the deleted sessions' cwds stop being roots at once; the 5s
 * TTL still bounds what an external change takes (see
 * docs/agents/files-and-access.md). allowFileRoot()'s additional roots are a
 * separate set and stay granted until the process restarts.
 */
export function invalidateAllowedFileRoots(): void {
  globalThis.__piAllowedRootsGeneration = allowedRootsGeneration() + 1;
  globalThis.__piAllowedRootsCache = undefined;
}

export async function getAllowedFileRoots(): Promise<Set<string>> {
  const now = Date.now();
  const cached = globalThis.__piAllowedRootsCache;
  if (cached && cached.expiresAt > now) return cached.roots;

  const generation = allowedRootsGeneration();
  const sessions = await listAllSessions();
  const roots = new Set<string>();
  for (const s of sessions) {
    if (s.cwd) roots.add(normalizeSlashes(s.cwd));
    // The project root (main repo shared by all worktrees) is browsable too —
    // the project dropdown lists it even when only worktrees have sessions.
    if (s.projectRoot) roots.add(normalizeSlashes(s.projectRoot));
  }

  for (const root of getAdditionalAllowedRoots()) roots.add(root);

  // Only the current generation's scan may be cached: a delete that landed
  // mid-scan has already dropped the cache, and this pre-delete result would
  // otherwise re-authorize its cwd for a whole TTL.
  if (allowedRootsGeneration() === generation) {
    globalThis.__piAllowedRootsCache = { roots, expiresAt: now + ALLOWED_ROOTS_TTL_MS };
  }
  return roots;
}

/** Authorize a path lexically, without touching the filesystem. */
export function isFilePathAllowed(target: string, allowedRoots: Set<string>): boolean {
  return isPathWithinRoots(target, allowedRoots);
}

/** Authorize an existing path after resolving symbolic links. */
export function isExistingFilePathAllowed(target: string, allowedRoots: Set<string>): boolean {
  return isExistingPathWithinRoots(target, allowedRoots);
}
