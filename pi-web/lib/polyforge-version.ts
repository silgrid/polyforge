/**
 * The browser-safe half of the pf version comparison (polyforge#256 B1/B2).
 *
 * The version precedence already lived in `lib/polyforge-detect.ts`, but that
 * module also opens `node:crypto`, `node:fs` and the pi SDK, so the browser's
 * install card (`lib/polyforge-install-pure.ts`) could not reuse it. This
 * module carries exactly the pure comparison — the SemVer 2.0 core and
 * pre-release precedence, the named v0.2.x -> v2.20.x line cutover and the
 * minimum-version hint — with no import at all. `polyforge-detect.ts`
 * re-exports the same implementation for its old callers, so there is one
 * comparator, never a second copy.
 */

/** The minimum pf the dock documents (the `--demand` floor, v2.18.1): the
 *  settings section's non-blocking 「版本过低」 hint threshold. */
export const POLYFORGE_MINIMUM_PF_VERSION = "2.18.1";

/** The X.Y.Z core and, when present, a SemVer 2.0 pre-release
 *  (`-<id>[.<id>...]`, ids of [0-9A-Za-z-]); whatever follows (build
 *  metadata included) is ignored, so `1.2.3+build.9` equals `1.2.3`. */
const VERSION_PATTERN = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?/;

export interface ParsedPolyforgeVersion {
  core: [number, number, number];
  preRelease: string[];
}

/** A version string as its core and pre-release identifiers, or null when it
 *  is not a version at all (never a guess). */
export function parsePolyforgeVersion(value: string | null | undefined): ParsedPolyforgeVersion | null {
  if (typeof value !== "string") return null;
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

/** The SemVer precedence of two pf versions (sign of a - b), line-aware (a
 *  v0.2.x version ranks above every v2.20.x version, see versionLineRank):
 *  negative when `a` is below `b`, 0 when equal, positive when above. A
 *  leading `v` is tolerated; unparseable input answers null — never a guess. */
export function comparePolyforgeVersions(a: string | null | undefined, b: string | null | undefined): number | null {
  const left = parsePolyforgeVersion(a);
  const right = parsePolyforgeVersion(b);
  if (!left || !right) return null;
  const lc = versionLineRank(left.core);
  const rc = versionLineRank(right.core);
  for (let i = 0; i < 3; i++) {
    if (lc[i] !== rc[i]) return lc[i] < rc[i] ? -1 : 1;
  }
  return comparePreRelease(left.preRelease, right.preRelease);
}

/** `ahead`, `behind` or `equal` by comparePolyforgeVersions; null when either
 *  side is unparseable. `ahead` means `a` is the higher version. */
export function comparePolyforgeVersionOrder(a: string | null | undefined, b: string | null | undefined): "ahead" | "behind" | "equal" | null {
  const sign = comparePolyforgeVersions(a, b);
  return sign === null ? null : sign > 0 ? "ahead" : sign < 0 ? "behind" : "equal";
}

/** Whether a version token carries a SemVer pre-release (`-dev.49`, `-rc.1`).
 *  Unparseable input is not a pre-release — never a guess. */
export function isPolyforgePrerelease(value: string | null | undefined): boolean {
  return (parsePolyforgeVersion(value)?.preRelease.length ?? 0) > 0;
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
  const parsed = typeof version === "string" ? parsePolyforgeVersion(version) : null;
  const floor = parsePolyforgeVersion(minimum);
  if (!floor) return false;
  if (!parsed) return unknownIsBelow;
  const core = versionLineRank(parsed.core);
  const floorCore = versionLineRank(floor.core);
  for (let i = 0; i < 3; i++) {
    if (core[i] !== floorCore[i]) return core[i] < floorCore[i];
  }
  return comparePreRelease(parsed.preRelease, floor.preRelease) < 0;
}