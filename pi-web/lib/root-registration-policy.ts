import { realpathSync, statSync, type Stats } from "fs";
import { homedir } from "os";
import { isAbsolute } from "path";
import { getAdditionalAllowedRoots } from "./allowed-roots";
import { isPathWithinRoots } from "./path-security";

/**
 * Registrable-root guard seam.
 *
 * A client-supplied candidate cwd may be promoted into the file-roots
 * allowlist (allowFileRoot) only when its realpath lies within an allowed
 * registration prefix: the operator's home directory, any operator-configured
 * PI_WEB_ALLOWED_ROOT_PREFIXES entry, or an already-registered additional
 * allowed root (idempotent revalidation). Containment is decided on resolved
 * realpaths — never on lexical strings — so symlink escapes out of a prefix
 * are refused.
 *
 * Outcomes are typed codes only; the engine never formats English prose.
 */
export type RegistrableRootRefusalReason =
  | "outsideRegistrationPrefix"
  | "notDirectory"
  | "nonexistent";

export type RegistrableRootOutcome =
  | { ok: true; path: string }
  | { ok: false; reason: RegistrableRootRefusalReason };

const ALLOWED_ROOT_PREFIXES_ENV = "PI_WEB_ALLOWED_ROOT_PREFIXES";

declare global {
  // Lazily memoized registration prefixes; stored on globalThis so Next.js
  // hot-reload keeps exactly one parse per process.
  var __piRootRegistrationPrefixes: { prefixes: string[] } | undefined;
}

function realpathOrNull(candidate: string): string | null {
  try {
    return realpathSync(candidate);
  } catch {
    return null;
  }
}

/**
 * The allowed registration prefixes as realpaths: the operator's home
 * directory plus every resolvable absolute entry of
 * PI_WEB_ALLOWED_ROOT_PREFIXES (comma-separated). Empty, relative, and
 * unresolvable entries are silently dropped; the env var being unset or empty
 * yields no extra prefixes.
 */
export function registrationPrefixes(): string[] {
  if (!globalThis.__piRootRegistrationPrefixes) {
    const prefixes = new Set<string>();
    const home = realpathOrNull(homedir());
    if (home) prefixes.add(home);
    const raw = process.env[ALLOWED_ROOT_PREFIXES_ENV] ?? "";
    for (const entry of raw.split(",")) {
      const trimmed = entry.trim();
      if (!trimmed || !isAbsolute(trimmed)) continue;
      const real = realpathOrNull(trimmed);
      if (real) prefixes.add(real);
    }
    globalThis.__piRootRegistrationPrefixes = { prefixes: [...prefixes] };
  }
  return globalThis.__piRootRegistrationPrefixes.prefixes;
}

/**
 * TEST-ONLY seam: swap the memoized registration scopes for a controlled
 * home + env-prefix pair, so behavioral fixtures never depend on the real
 * os.homedir()/os.tmpdir() layout (on Windows the tmpdir lives UNDER the
 * homedir, which would make "outside-home" fixtures actually registrable).
 * Pass home=null to re-derive lazily from the real environment on the next
 * registrationPrefixes() call. Cross-instance safe: the memo lives on
 * globalThis, shared by every jiti/module instance in the process.
 */
export function __setRegistrationScopesForTesting(
  home: string | null,
  envPrefixes: string | null,
): void {
  if (envPrefixes === null) delete process.env[ALLOWED_ROOT_PREFIXES_ENV];
  else process.env[ALLOWED_ROOT_PREFIXES_ENV] = envPrefixes;
  if (home === null) {
    globalThis.__piRootRegistrationPrefixes = undefined;
    return;
  }
  const prefixes = new Set<string>();
  const realHome = realpathOrNull(home);
  if (realHome) prefixes.add(realHome);
  for (const entry of (envPrefixes ?? "").split(",")) {
    const trimmed = entry.trim();
    if (!trimmed || !isAbsolute(trimmed)) continue;
    const real = realpathOrNull(trimmed);
    if (real) prefixes.add(real);
  }
  globalThis.__piRootRegistrationPrefixes = { prefixes: [...prefixes] };
}

/**
 * Whether `candidate` may be registered as an additional allowed file root.
 * Returns the realpath-normalized candidate on success so callers register
 * the canonical location.
 */
export function isRegistrableRoot(candidate: string): RegistrableRootOutcome {
  if (typeof candidate !== "string" || !candidate || !isAbsolute(candidate)) {
    // A relative path can never lie within an absolute registration prefix.
    return { ok: false, reason: "outsideRegistrationPrefix" };
  }

  let stat: Stats;
  try {
    stat = statSync(candidate);
  } catch {
    return { ok: false, reason: "nonexistent" };
  }
  if (!stat.isDirectory()) {
    return { ok: false, reason: "notDirectory" };
  }

  let real: string;
  try {
    real = realpathSync(candidate);
  } catch {
    return { ok: false, reason: "nonexistent" };
  }

  const roots = new Set<string>(registrationPrefixes());
  // Already-registered roots revalidate idempotently. They are slash-
  // normalized Set keys; realpathSync restores a comparable real path.
  for (const registered of getAdditionalAllowedRoots()) {
    const realRegistered = realpathOrNull(registered);
    if (realRegistered) roots.add(realRegistered);
  }

  if (!isPathWithinRoots(real, roots)) {
    return { ok: false, reason: "outsideRegistrationPrefix" };
  }
  return { ok: true, path: real };
}
