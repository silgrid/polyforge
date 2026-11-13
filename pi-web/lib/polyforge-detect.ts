import { createHash } from "node:crypto";
import { hasPolyforgeCellCapabilities, sanitizePolyforgeCapabilities } from "./polyforge-cells-pure";
import { existsSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { homedir } from "node:os";
import { type PackageSource } from "@earendil-works/pi-coding-agent";
import { execFileAsync } from "./exec-file";
import { polyforgePfBin } from "./polyforge-pf-bin";
import { getGlobalSettingsPath, readGlobalSettings } from "./global-settings-file";
import { getPackageSource, isDisabledPackage } from "./package-settings";
import { probePolyforgeVersion, readPolyforgeOpsStatus } from "./polyforge-status";

/**
 * Server-side machine-level detection for the Settings Polyforge section
 * (pi#122). The presence/version probe is the dock's own single
 * implementation (`probePolyforgeVersion` in `lib/polyforge-status.ts` — the
 * only `pf --version` spawn site); this module assembles the flat
 * booleans/labels the section renders and nothing else: no `api_key`, no
 * server address, no `user_id`, no `cli.toml` contents, no absolute home
 * paths, never `process.env` values (R8). The resolved clone root, the
 * package source and the Claude hook command text all stay server-side.
 *
 * Field independence: line, pi-skill and Claude Code state are machine facts
 * read independently of pf presence; the pf-dependent fields degrade when pf
 * is absent — version absent, daemon 「不适用」 (null, no probe attempted).
 * Every read degrades on its own (a missing or unparsable file, a failed
 * spawn, a broken link): never an error, never a guess.
 */

/** The minimum pf the dock documents (the `--demand` floor, v2.18.1): the
 *  settings section's non-blocking 「版本过低」 hint threshold. */
export const POLYFORGE_MINIMUM_PF_VERSION = "2.18.1";

/** The sha256 (hex) of the internal line's origin owner/repo pair — the
 *  only form the pair is stored in (never the plaintext pair, never a URL,
 *  never a path). */
const INTERNAL_ORIGIN_OWNER_REPO_SHA256 =
  "6d6a1a5b62f974b5f5ae9fb72397a5a021d5f779cb1dcdd491885238033700d6";

/** The harness package's package.json name (the one identification fact). */
const POLYFORGE_PACKAGE_NAME = "polyforge";

const GIT_TIMEOUT_MS = 3000;

export type PolyforgeLine = "internal" | "public" | "unknown";

export interface PolyforgeDaemonStatus {
  reachable: boolean;
  /** pf's own stderr, capped at 512 bytes (PolyforgeError); present only when unreachable. */
  error?: string;
}

export interface PolyforgeDetectStatus {
  pfPresent: boolean;
  pfVersion: string | null;
  pfVersionBelowMinimum: boolean;
  line: PolyforgeLine;
  piSkillInstalled: boolean;
  piSkillEnabled: boolean;
  claudeSkillsSymlink: boolean;
  claudeSessionStartHook: boolean;
  /** null = 「不适用」 — pf absent, no probe attempted. */
  daemon: PolyforgeDaemonStatus | null;
  /** the protocol capabilities `pf --json version` reports
   *  ([] for an older pf or pf absent); multi-Cell writes gate on them. */
  capabilities: string[];
  cellsSupported: boolean;
}

// ---- pure helpers ----

/** The X.Y.Z core and, when present, a SemVer 2.0 pre-release
 *  (`-<id>[.<id>...]`, ids of [0-9A-Za-z-]); whatever follows is ignored. */
const VERSION_PATTERN = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?/;

interface ParsedVersion {
  core: [number, number, number];
  preRelease: string[];
}

function parseVersion(value: string): ParsedVersion | null {
  const match = VERSION_PATTERN.exec(value.trim());
  if (!match) return null;
  return {
    core: [Number(match[1]), Number(match[2]), Number(match[3])],
    preRelease: match[4] === undefined ? [] : match[4].split("."),
  };
}

/** SemVer 2.0 §11 pre-release precedence (sign of a - b): none ranks above
 *  any; identifiers one by one — numeric ones as numbers and below
 *  alphanumeric ones, alphanumeric ones in ASCII order — and a shorter
 *  prefix first. */
function comparePreRelease(a: string[], b: string[]): number {
  if (a.length === 0 || b.length === 0) return b.length - a.length;
  for (let i = 0; i < Math.min(a.length, b.length); i++) {
    if (a[i] === b[i]) continue;
    const aNumeric = /^\d+$/.test(a[i]);
    const bNumeric = /^\d+$/.test(b[i]);
    if (aNumeric && bNumeric) {
      if (Number(a[i]) !== Number(b[i])) return Number(a[i]) - Number(b[i]);
      continue;
    }
    if (aNumeric !== bNumeric) return aNumeric ? -1 : 1;
    return a[i] < b[i] ? -1 : 1;
  }
  return a.length - b.length;
}

/** The named version-line cutover: the unified product line
 *  v0.2.x follows the retired internal v2.20.x line, so a v0.2.x core ranks
 *  above every 2.20.x floor (major 0 / minor 2 compares as major 1000);
 *  v0.1.x and every other core compare as before. Delete this mapping
 *  together with .polyforge/release.sh's CUTOVER_* branches, once no v2.20.x
 *  release is the current line anywhere (every deployed aihub and installed
 *  pf runs v0.2.x) and the floors are restated on the v0.2.x line. */
function versionLineRank(core: [number, number, number]): [number, number, number] {
  return core[0] === 0 && core[1] === 2 ? [1000, core[1], core[2]] : core;
}

/** Non-blocking minimum-version hint (pure): SemVer 2.0 precedence
 * — the X.Y.Z cores, line-aware (see versionLineRank: a
 *  v0.2.x pf is newer than every 2.20.x floor), then a pre-release below its
 *  core release and pre-release identifiers by comparePreRelease, so
 *  `v0.2.0-<name>.9` is below `v0.2.0-<name>.10` (leading `v` tolerated, the
 *  hint blocks nothing). Unparseable → false, never a guess —
 *  unless the caller passes `unknownIsBelow`: then a null/unparseable
 *  version counts as below the floor (the summarize floor
 *  never treats an unknown pf as capable). Every other floor keeps the
 *  default. */
export function isPolyforgeVersionBelowMinimum(
  version: string | null,
  minimum: string = POLYFORGE_MINIMUM_PF_VERSION,
  unknownIsBelow = false,
): boolean {
  const parsed = typeof version === "string" ? parseVersion(version) : null;
  const floor = parseVersion(minimum);
  if (!floor) return false;
  if (!parsed) return unknownIsBelow;
  const core = versionLineRank(parsed.core);
  const floorCore = versionLineRank(floor.core);
  for (let i = 0; i < 3; i++) {
    if (core[i] !== floorCore[i]) return core[i] < floorCore[i];
  }
  return comparePreRelease(parsed.preRelease, floor.preRelease) < 0;
}

/** The owner/repo pair of a git origin URL (`git@host:O/R.git` and
 *  `https://host/O/R.git` forms, `.git` stripped) — null when the URL carries
 *  no parseable owner/repo pair. Pure, no lookbehind, never throws. */
export function polyforgeOriginOwnerRepo(originUrl: string): string | null {
  const value = originUrl.trim();
  if (!value || /\s/.test(value)) return null;
  let path: string | null = null;
  if (value.startsWith("git@")) {
    const colon = value.indexOf(":");
    if (colon < 0) return null;
    path = value.slice(colon + 1);
  } else {
    try {
      path = new URL(value).pathname;
    } catch {
      return null;
    }
  }
  const pair = path.replace(/^\/+/, "").replace(/\.git$/, "").replace(/\/+$/, "");
  const segments = pair.split("/");
  if (segments.length !== 2 || segments.some((segment) => !segment)) return null;
  return pair;
}

function sha256Hex(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

/** Line from a git origin URL: 内部线 when the parsed owner/repo pair's
 *  sha256 equals `digest` (the built-in internal-line digest constant by
 *  default — the pair is never compared in plaintext), 公开线 for any other
 *  parseable origin, 未知 when undeterminable — never a guess. */
export function polyforgeLineFromOrigin(
  originUrl: string,
  digest: string = INTERNAL_ORIGIN_OWNER_REPO_SHA256,
): PolyforgeLine {
  const pair = polyforgeOriginOwnerRepo(originUrl);
  if (pair === null) return "unknown";
  return sha256Hex(pair) === digest ? "internal" : "public";
}

/** Resolve a package source to the polyforge harness package root: an
 *  existing path whose `package.json` `name` is `polyforge` (install.sh's
 *  `release/current` package root). Null when the source is not such a path —
 *  never an error. */
export function polyforgePackageRoot(source: string): string | null {
  try {
    const stats = statSync(source);
    const root = stats.isDirectory() ? source : dirname(source);
    const packageJsonPath = join(root, "package.json");
    if (!existsSync(packageJsonPath)) return null;
    const parsed = JSON.parse(readFileSync(packageJsonPath, "utf8")) as { name?: unknown };
    return parsed.name === POLYFORGE_PACKAGE_NAME ? root : null;
  } catch {
    return null;
  }
}

/** Enabled = a plain string entry, or an object entry the plugins route's
 *  disabled-shape rule (`lib/package-settings.ts`) does not count disabled —
 *  one fact for the write path and the detection. */
export function isPolyforgePackageEnabled(entry: PackageSource): boolean {
  return typeof entry === "string" || !isDisabledPackage(entry);
}

/** A parsed `~/.claude/settings.json` carries a SessionStart hook whose
 *  command references polyforge (pure, never throws; the command text itself
 *  never leaves the server — only the boolean does). Both the matcher form
 *  (`[{hooks: [...]}]`) and a bare hook list are accepted. */
export function claudeSessionStartHookPresent(settings: unknown): boolean {
  if (!settings || typeof settings !== "object") return false;
  const hooks = (settings as { hooks?: unknown }).hooks;
  if (!hooks || typeof hooks !== "object") return false;
  const sessionStart = (hooks as { SessionStart?: unknown }).SessionStart;
  if (!Array.isArray(sessionStart)) return false;
  for (const matcher of sessionStart) {
    if (!matcher || typeof matcher !== "object") continue;
    const hookList = Array.isArray((matcher as { hooks?: unknown }).hooks)
      ? (matcher as { hooks: unknown[] }).hooks
      : [matcher];
    if (!Array.isArray(hookList)) continue;
    for (const hook of hookList) {
      if (!hook || typeof hook !== "object") continue;
      const command = (hook as { command?: unknown }).command;
      if (typeof command === "string" && command.toLowerCase().includes(POLYFORGE_PACKAGE_NAME)) {
        return true;
      }
    }
  }
  return false;
}

// ---- machine reads (each degrades on its own) ----

/** The global settings' package entries (project scope is never consulted —
 *  pi#122 R5). A missing or unparsable file degrades to no entries, never an
 *  error. Reads through the same lock SettingsManager takes. */
export async function readGlobalPackageEntries(): Promise<PackageSource[]> {
  try {
    return await readGlobalSettings(getGlobalSettingsPath(), (settings) => {
      const packages = settings.packages;
      if (!Array.isArray(packages)) return [];
      return packages.filter((entry): entry is PackageSource =>
        typeof entry === "string"
          || (typeof entry === "object" && entry !== null && typeof (entry as { source?: unknown }).source === "string"));
    });
  } catch {
    return [];
  }
}

/** install.sh's conventional clone path (a fixed
 *  `$HOME/.local/share/polyforge` root, verified in its source). */
export function conventionalPolyforgeRoot(home: string = homedir()): string {
  return join(home, ".local", "share", "polyforge", "release", "current");
}

/** `~/.claude/skills/polyforge` resolves through the symlink (stat follows
 *  links; a broken or missing link reads absent). */
export function claudeSkillsSymlinkExists(claudeDir: string): boolean {
  try {
    statSync(join(claudeDir, "skills", "polyforge"));
    return true;
  } catch {
    return false;
  }
}

function claudeSettingsHookExists(claudeDir: string): boolean {
  try {
    return claudeSessionStartHookPresent(JSON.parse(readFileSync(join(claudeDir, "settings.json"), "utf8")));
  } catch {
    return false;
  }
}

/** The line from a git origin URL read off the resolved clone root: no
 *  root (or a failed git read) → 未知, never a guess. The root itself never
 *  leaves the server. */
async function detectPolyforgeLine(cloneRoot: string | null): Promise<PolyforgeLine> {
  if (!cloneRoot) return "unknown";
  try {
    const { stdout } = await execFileAsync("git", ["-C", cloneRoot, "remote", "get-url", "origin"], {
      timeout: GIT_TIMEOUT_MS,
    });
    return polyforgeLineFromOrigin(stdout);
  } catch {
    return "unknown";
  }
}

// ---- assembly ----

/** Assemble the Settings Polyforge section's machine-level detection
 *  (pi#122, R8: flat booleans/labels only). `force` bypasses the 60 s
 *  presence cache read (the 「重新检测」 control). */
export async function detectPolyforge(force = false): Promise<PolyforgeDetectStatus> {
  const entries = await readGlobalPackageEntries();
  // The clone root the line is read from: the global polyforge package
  // entry's source when that entry exists, else install.sh's conventional
  // path when it does (null → line 未知, no git spawn). The resolved
  // home-path root never leaves the server.
  let cloneRoot: string | null = null;
  let piSkillInstalled = false;
  let piSkillEnabled = false;
  for (const entry of entries) {
    const root = polyforgePackageRoot(getPackageSource(entry));
    if (!root) continue;
    cloneRoot = root;
    piSkillInstalled = true;
    piSkillEnabled = isPolyforgePackageEnabled(entry);
    break;
  }
  if (!cloneRoot) {
    const conventional = conventionalPolyforgeRoot();
    if (existsSync(conventional)) cloneRoot = conventional;
  }
  const line = await detectPolyforgeLine(cloneRoot);
  const claudeDir = join(homedir(), ".claude");
  const claudeSkillsSymlink = claudeSkillsSymlinkExists(claudeDir);
  const claudeSessionStartHook = claudeSettingsHookExists(claudeDir);
  const { present, version } = await probePolyforgeVersion(force);
  const pfVersion = present ? version : null;
  let daemon: PolyforgeDaemonStatus | null = null;
  let capabilities: string[] = [];
  if (present) {
    try {
      const { stdout } = await execFileAsync(polyforgePfBin(), ["--json", "version"], { timeout: GIT_TIMEOUT_MS });
      capabilities = sanitizePolyforgeCapabilities(JSON.parse(stdout));
    } catch {
      capabilities = [];
    }
    try {
      await readPolyforgeOpsStatus();
      daemon = { reachable: true };
    } catch (error) {
      // Only pf's own bounded stderr surfaces (PolyforgeError caps it at
      // 512); never argv, never `process.env` values.
      daemon = { reachable: false, error: error instanceof Error ? error.message : "polyforge is unavailable" };
    }
  }
  return {
    pfPresent: present,
    pfVersion,
    pfVersionBelowMinimum: isPolyforgeVersionBelowMinimum(pfVersion),
    line,
    piSkillInstalled,
    piSkillEnabled,
    claudeSkillsSymlink,
    claudeSessionStartHook,
    daemon,
    capabilities,
    cellsSupported: hasPolyforgeCellCapabilities(capabilities),
  };
}