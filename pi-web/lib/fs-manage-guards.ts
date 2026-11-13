import type { Stats } from "fs";
import { basename, dirname, isAbsolute, join, resolve } from "path";
import { getAdditionalAllowedRoots } from "./allowed-roots";
import { isPathWithinRoots } from "./path-security";
import { registrationPrefixes } from "./root-registration-policy";

/**
 * Pure typed-guard engine for the fs-manage API (wi pi#59).
 *
 * Directory rename/delete through `app/api/fs-manage` is a DANGEROUS
 * filesystem mutation, so every rule lives here as pure helpers over
 * INJECTED fs seams (`lstatSync`, `realpathSync`) and an injected
 * session-cwd source — the route wires the real seams, seam tests drive
 * tmp-backed or throwing fakes. No route imports; no fs import either.
 *
 * Outcomes are typed codes only — the engine never formats English prose
 * and never lets a raw exception reach a response body.
 */

export type FsManageRefusalReason =
  | "invalidBody"
  | "nonexistent"
  | "symlinkEntry"
  | "notDirectory"
  | "outsideRegistrationPrefix"
  | "pathInUse"
  | "confirmMismatch"
  | "targetExists"
  | "ioFailure";

/** The fixed refusal-code order the guards check in (wi#59 contract). The
 *  route runs exactly this order; tests assert against the constant so
 *  ordering regressions are caught without HTTP. */
export const FS_MANAGE_REFUSAL_REASONS: readonly FsManageRefusalReason[] = [
  "invalidBody",
  "nonexistent",
  "symlinkEntry",
  "notDirectory",
  "outsideRegistrationPrefix",
  "pathInUse",
  "confirmMismatch",
  "targetExists",
  "ioFailure",
];

export interface FsManageRequest {
  action: "rename" | "delete";
  /** Lexically normalized absolute entry path. */
  path: string;
  /** Rename only: the sibling destination (same parent as `path`). */
  nextPath?: string;
  /** Delete only: the typed confirmation name. */
  confirm?: string;
}

export type FsManageOutcome =
  | { ok: true; action: FsManageRequest["action"]; source: string; path: string }
  | { ok: false; reason: FsManageRefusalReason };

/** Injected fs seams. The route passes the real `fs`; tests pass tmp-backed
 *  or throwing fakes. */
export interface FsManageFs {
  lstatSync(path: string): Stats;
  realpathSync(path: string): string;
}

/** Injected mutation surface (performed by the ROUTE, never here): delete
 *  is `fs.rm(path, { recursive: true, force: false })`, rename is
 *  `fs.rename(source, destination)`. Kept as a resolvable seam so route
 *  tests can inject EACCES-class failures through the HTTP boundary. */
export interface FsManageMutator {
  removeDirectory(path: string): void;
  renameDirectory(source: string, destination: string): void;
}

type SessionCwdSource = () => readonly string[];

// The seam wiring lives on globalThis (the repo convention for state that
// must survive hot-reload and be shared across duplicate module instances —
// jiti with moduleCache:false gives every import its own module copy).
declare global {
  var __piFsManageSessionCwds: SessionCwdSource | undefined;
  var __piFsManageSessionCwdsTestOverride: SessionCwdSource | undefined;
  var __piFsManageFsSeamsTestOverride: FsManageFs | undefined;
  var __piFsManageMutatorTestOverride: FsManageMutator | undefined;
}

/** Production wiring: the route injects the rpc-manager export
 *  (`listLiveSessionCwds`) so the engine never imports rpc-manager. */
export function setFsManageSessionCwdSource(source: SessionCwdSource): void {
  globalThis.__piFsManageSessionCwds = source;
}

/** The effective session-cwd source: a test override wins over the
 *  production wiring; no wiring at all yields no live cwds. */
export function resolveFsManageSessionCwds(): SessionCwdSource {
  return globalThis.__piFsManageSessionCwdsTestOverride
    ?? globalThis.__piFsManageSessionCwds
    ?? (() => []);
}

/** TEST-ONLY seam: inject fake cwd lists without constructing AgentSessions.
 *  Pass null to restore the production wiring. */
export function __setFsManageSessionCwdSourceForTesting(source: SessionCwdSource | null): void {
  globalThis.__piFsManageSessionCwdsTestOverride = source ?? undefined;
}

/** The effective fs seams: a test override (route tests inject
 *  EACCES-class failures) wins over the route's real fs. */
export function resolveFsManageFs(defaults: FsManageFs): FsManageFs {
  return globalThis.__piFsManageFsSeamsTestOverride ?? defaults;
}

/** TEST-ONLY seam for the route's guard-stage fs access. Pass null to
 *  restore the real seams. */
export function __setFsManageFsSeamsForTesting(fs: FsManageFs | null): void {
  globalThis.__piFsManageFsSeamsTestOverride = fs ?? undefined;
}

/** The effective mutator: a test override wins over the route's real one. */
export function resolveFsManageMutator(defaults: FsManageMutator): FsManageMutator {
  return globalThis.__piFsManageMutatorTestOverride ?? defaults;
}

/** TEST-ONLY seam for the route's mutation stage (partial-recursive-delete
 *  honesty, injected EACCES). Pass null to restore the real mutator. */
export function __setFsManageMutatorForTesting(mutator: FsManageMutator | null): void {
  globalThis.__piFsManageMutatorTestOverride = mutator ?? undefined;
}

// ---------------------------------------------------------------------------
// 1. invalidBody
// ---------------------------------------------------------------------------

export type FsManageBodyValidation =
  | { ok: true; request: FsManageRequest }
  | { ok: false };

/**
 * Structural body validation. `path` must be a non-empty absolute path
 * (normalized lexically with resolve()); a rename must carry an absolute
 * `nextPath` naming a SIBLING of the source — `dirname(nextPath)` must equal
 * `dirname(path)` (the UI only ever issues sibling renames, so a
 * cross-parent destination is refused as invalidBody, never resolved).
 * `confirm` is optional and only its exact equality matters (delete), so a
 * missing or mistyped value classifies as confirmMismatch, not invalidBody.
 */
export function validateFsManageBody(body: unknown): FsManageBodyValidation {
  if (typeof body !== "object" || body === null) return { ok: false };
  const { action, path, nextPath, confirm } = body as Record<string, unknown>;
  if (action !== "rename" && action !== "delete") return { ok: false };
  if (typeof path !== "string" || !isAbsolute(path)) return { ok: false };
  const normalizedPath = resolve(path);
  const request: FsManageRequest = { action, path: normalizedPath };
  if (action === "rename") {
    if (typeof nextPath !== "string" || !isAbsolute(nextPath)) return { ok: false };
    const normalizedNext = resolve(nextPath);
    // Sibling-only rename (wi#59): the destination must live in the source's
    // own parent — a cross-parent nextPath is refused as invalidBody.
    if (dirname(normalizedNext) !== dirname(normalizedPath)) return { ok: false };
    request.nextPath = normalizedNext;
  }
  if (typeof confirm === "string") request.confirm = confirm;
  return { ok: true, request };
}

// ---------------------------------------------------------------------------
// fs error classification
// ---------------------------------------------------------------------------

/** ENOENT classifies as nonexistent (missing intermediates included);
 *  every other filesystem error (EACCES/EPERM/EBUSY/ENAMETOOLONG/…) maps to
 *  exactly ioFailure. */
export function classifyFsError(error: unknown): "nonexistent" | "ioFailure" {
  const code = (error as { code?: unknown } | null)?.code;
  return code === "ENOENT" ? "nonexistent" : "ioFailure";
}

function realpathOrNull(fs: FsManageFs, candidate: string): string | null {
  try {
    return fs.realpathSync(candidate);
  } catch {
    return null;
  }
}

type RealpathResult = { real: string } | { refusal: FsManageRefusalReason };

function realpathOrRefuse(fs: FsManageFs, candidate: string): RealpathResult {
  try {
    return { real: fs.realpathSync(candidate) };
  } catch (error) {
    return { refusal: classifyFsError(error) };
  }
}

// ---------------------------------------------------------------------------
// 5. outsideRegistrationPrefix — protected-site set + containment
// ---------------------------------------------------------------------------

export interface FsManageProtectedSites {
  /** Containment roots: the entry's PARENT (the mutation site) must lie
   *  within one of these — registration prefixes plus resolved registered
   *  roots. */
  containmentRoots: Set<string>;
  /** Protected mutation targets: home, every registration prefix, every
   *  registered root (resolved, or kept LEXICALLY when stale), and every
   *  EXISTING ancestor of a registered root (canonical ancestors of live
   *  roots, nearest-existing ancestors of stale ones). */
  protectedSites: Set<string>;
  /** Stale registrations (their directories no longer exist), kept as
   *  resolve()-normalized lexical paths: a rename destination that lands ON
   *  or UNDER one of these must be refused — recreating a protected
   *  registration target is exactly what the guard exists to prevent
   *  (review r2 B1, pi#60). */
  staleLexical: Set<string>;
}

export type FsManageProtectedSitesResult =
  | { ok: true; sites: FsManageProtectedSites }
  | { ok: false; reason: "ioFailure" };

/**
 * The protection set (wi#59 contract refusal #5), FAIL-CLOSED on resolution
 * errors (review r2 B2, pi#60): a registered root that resolves (live)
 * protects its canonical realpath and every canonical existing ancestor;
 * a registered root whose directory is GONE (ENOENT — stale) protects its
 * lexical self and every nearest-existing lexical ancestor; a resolution
 * failure that is NEITHER (EACCES/EPERM/ELOOP/…) is an ioFailure refusal —
 * the pipeline never continues with an incomplete protection set.
 */
export function computeProtectedSites(
  fs: FsManageFs,
  options: { prefixes?: string[]; registeredRoots?: Iterable<string> } = {},
): FsManageProtectedSitesResult {
  const prefixes = options.prefixes ?? registrationPrefixes();
  const registeredRoots = [...(options.registeredRoots ?? getAdditionalAllowedRoots())];
  const containmentRoots = new Set<string>(prefixes);
  const protectedSites = new Set<string>(prefixes);
  const staleLexical = new Set<string>();
  // One canonical-ancestor walk over a REAL path: every existing ancestor is
  // protected; any non-ENOENT resolution failure fails the whole set closed.
  const protectExistingAncestors = (realLeaf: string): "ioFailure" | undefined => {
    for (let ancestor = dirname(realLeaf); ;) {
      try {
        protectedSites.add(fs.realpathSync(ancestor));
      } catch (error) {
        if (classifyFsError(error) === "nonexistent") {
          // Unreachable for ancestors of a resolved path barring races;
          // fail closed rather than guess.
          return "ioFailure";
        }
        return "ioFailure";
      }
      const parent = dirname(ancestor);
      if (parent === ancestor) break;
      ancestor = parent;
    }
    return undefined;
  };
  // One LEXICAL-ancestor walk over a STALE registration: every nearest-
  // existing ancestor is protected by its realpath, every still-missing
  // ancestor is protected lexically; non-ENOENT failures fail closed.
  const protectStaleAncestors = (stalePath: string): "ioFailure" | undefined => {
    for (let ancestor = dirname(stalePath); ;) {
      try {
        protectedSites.add(fs.realpathSync(ancestor));
      } catch (error) {
        if (classifyFsError(error) !== "nonexistent") return "ioFailure";
        protectedSites.add(resolve(ancestor));
      }
      const parent = dirname(ancestor);
      if (parent === ancestor) break;
      ancestor = parent;
    }
    return undefined;
  };
  for (const root of registeredRoots) {
    if (typeof root !== "string" || !root) continue;
    const resolvedRoot = resolve(root);
    const resolution = realpathOrRefuse(fs, resolvedRoot);
    if ("refusal" in resolution) {
      if (resolution.refusal === "nonexistent") {
        // Stale registration: protect lexically, never drop.
        protectedSites.add(resolvedRoot);
        staleLexical.add(resolvedRoot);
        if (protectStaleAncestors(resolvedRoot)) return { ok: false, reason: "ioFailure" };
        continue;
      }
      return { ok: false, reason: "ioFailure" };
    }
    containmentRoots.add(resolution.real);
    protectedSites.add(resolution.real);
    if (protectExistingAncestors(resolution.real)) return { ok: false, reason: "ioFailure" };
  }
  return { ok: true, sites: { containmentRoots, protectedSites, staleLexical } };
}

/** Whether the mutation site (the entry's resolved parent) lies within a
 *  registration prefix or a resolved registered root. */
export function isMutationSiteAllowed(siteReal: string, sites: FsManageProtectedSites): boolean {
  return isPathWithinRoots(siteReal, sites.containmentRoots);
}

/** Whether the resolved entry is itself a protected site: home, a
 *  registration prefix, a registered root, or an ancestor of one. */
export function isProtectedSite(entryReal: string, sites: FsManageProtectedSites): boolean {
  return sites.protectedSites.has(entryReal);
}

// ---------------------------------------------------------------------------
// 6. pathInUse
// ---------------------------------------------------------------------------

/**
 * Whether a live or starting session cwd blocks the mutation: the entry
 * contains the cwd (or IS the cwd). Containment is decided on resolved
 * realpaths — a cwd that no longer resolves is compared lexically, so a
 * session whose cwd was deleted still blocks deleting its ancestor.
 */
export function sessionCwdBlocksEntry(
  fs: FsManageFs,
  entryReal: string,
  sessionCwds: readonly string[],
): boolean {
  for (const cwd of sessionCwds) {
    if (typeof cwd !== "string" || !cwd) continue;
    const realCwd = realpathOrNull(fs, cwd) ?? resolve(cwd);
    if (isPathWithinRoots(realCwd, new Set([entryReal]))) return true;
  }
  return false;
}

// ---------------------------------------------------------------------------
// The composed pipeline (the route runs exactly this, in order)
// ---------------------------------------------------------------------------

/**
 * Every guard, checked in the fixed order, all BEFORE any mutation:
 * invalidBody → nonexistent → symlinkEntry → notDirectory →
 * outsideRegistrationPrefix → pathInUse → confirmMismatch → targetExists,
 * with any non-ENOENT filesystem error surfacing as ioFailure wherever it
 * strikes. On success returns the RESOLVED source (`realpath(path)`) plus
 * the destination for renames (`realpath(parent) + basename(nextPath)`) —
 * the route mutates exactly these paths.
 */
export function runFsManageGuards(body: unknown, fs: FsManageFs): FsManageOutcome {
  // 1. invalidBody
  const validated = validateFsManageBody(body);
  if (!validated.ok) return { ok: false, reason: "invalidBody" };
  const { action, path, nextPath, confirm } = validated.request;

  // 2. nonexistent — lstat-based existence; a missing intermediate classifies
  //    here too (lstat reports ENOENT for the deepest missing component).
  let stats: Stats;
  try {
    stats = fs.lstatSync(path);
  } catch (error) {
    return { ok: false, reason: classifyFsError(error) };
  }

  // 3. symlinkEntry — refused OUTRIGHT: no realpath-following, no referent
  //    discussion; the entry is simply not mutable through this API.
  if (stats.isSymbolicLink()) return { ok: false, reason: "symlinkEntry" };

  // 4. notDirectory
  if (!stats.isDirectory()) return { ok: false, reason: "notDirectory" };

  // Resolve the canonical entry and its parent; a path we cannot resolve is
  // one whose safety we cannot establish — fail closed (ENOENT →
  // nonexistent, anything else → ioFailure).
  const entry = realpathOrRefuse(fs, path);
  if ("refusal" in entry) return { ok: false, reason: entry.refusal };
  const parent = realpathOrRefuse(fs, dirname(path));
  if ("refusal" in parent) return { ok: false, reason: parent.refusal };

  // 5. outsideRegistrationPrefix — the mutation site must lie within the
  //    registration prefixes, and the entry itself must not be home, a
  //    prefix, a registered root, or an ancestor of one. Fail-closed: an
  //    unresolvable registration (non-ENOENT) refuses the whole request.
  const sitesResult = computeProtectedSites(fs);
  if (!sitesResult.ok) return { ok: false, reason: sitesResult.reason };
  const sites = sitesResult.sites;
  if (!isMutationSiteAllowed(parent.real, sites)) {
    return { ok: false, reason: "outsideRegistrationPrefix" };
  }
  if (isProtectedSite(entry.real, sites)) {
    return { ok: false, reason: "outsideRegistrationPrefix" };
  }

  // 6. pathInUse — a live or starting session cwd inside the entry blocks it.
  if (sessionCwdBlocksEntry(fs, entry.real, resolveFsManageSessionCwds()())) {
    return { ok: false, reason: "pathInUse" };
  }

  if (action === "delete") {
    // 7. confirmMismatch — delete requires confirm === basename(path) exactly.
    if (confirm !== basename(path)) return { ok: false, reason: "confirmMismatch" };
    return { ok: true, action, source: entry.real, path: entry.real };
  }

  // rename → 8. targetExists. The destination is realpath(resolved parent) +
  // basename(nextPath); its resolved parent runs the same prefix checks as
  // the source's (for a sibling rename that parent IS the source's parent,
  // so the check executes without ever resolving a foreign directory).
  // PROTECTION BEFORE EXISTENCE (review r2 B1, pi#60): the destination
  // itself is checked against the protected-site set AND the stale-lexical
  // registrations BEFORE the existence probe — renaming onto a stale (absent)
  // registered root must refuse, not recreate the protected target.
  const destination = join(parent.real, basename(nextPath!));
  if (isProtectedSite(destination, sites)) {
    return { ok: false, reason: "outsideRegistrationPrefix" };
  }
  const destinationResolved = resolve(destination);
  for (const stale of sites.staleLexical) {
    if (destinationResolved === stale || isPathWithinRoots(stale, new Set([destinationResolved]))) {
      // The destination IS a stale registration, or an ancestor of one.
      return { ok: false, reason: "outsideRegistrationPrefix" };
    }
  }
  const destParent = realpathOrRefuse(fs, dirname(destination));
  if ("refusal" in destParent) return { ok: false, reason: destParent.refusal };
  if (!isMutationSiteAllowed(destParent.real, sites)) {
    return { ok: false, reason: "outsideRegistrationPrefix" };
  }
  // nextPath must not exist as ANY dirent per lstat — dangling symlinks
  // count as existing, and there is NO same-identity exemption (an identical
  // nextPath names the still-existing source, so it refuses here too; the
  // UI closes without requesting when the name is unchanged).
  let destinationExists: boolean;
  try {
    fs.lstatSync(destination);
    destinationExists = true;
  } catch (error) {
    if (classifyFsError(error) !== "nonexistent") return { ok: false, reason: "ioFailure" };
    destinationExists = false;
  }
  if (destinationExists) return { ok: false, reason: "targetExists" };
  return { ok: true, action, source: entry.real, path: destination };
}
