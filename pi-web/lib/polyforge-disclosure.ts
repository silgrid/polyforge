/**
 * Open/closed state of the Settings › Polyforge page's disclosure regions
 * (the workflow card's region joins, and the
 * retired summarize card's and declared-candidates card's regions are gone —
 * a stored object that still names them reads through the defaults, the
 * unknown regions ignored). One namespaced key in this browser's localStorage holds a
 * boolean per region — never pf config, never a server file, never a request.
 * An absent, blocked, unreadable, corrupt or wrong-typed value reads as the
 * default (every region closed); a partial object merges the defaults; an
 * unknown region is ignored. A write storage refuses still takes effect for
 * this page's lifetime. Client-safe: no Node import.
 */

export const POLYFORGE_DISCLOSURE_KEY = "pi-web:polyforge-config-disclosure";
/** Dispatched on window after a write, so the open page re-reads it. */
export const POLYFORGE_DISCLOSURE_EVENT = "pi-web:polyforge-config-disclosure";

export const POLYFORGE_DISCLOSURE_REGIONS = [
  "detect",
  "cliToml",
  "daemon",
  "workspace",
  "history",
  "install",
  "workflow",
] as const;

export type PolyforgeDisclosureRegion = (typeof POLYFORGE_DISCLOSURE_REGIONS)[number];
export type PolyforgeDisclosure = Record<PolyforgeDisclosureRegion, boolean>;

export const POLYFORGE_DISCLOSURE_DEFAULT: PolyforgeDisclosure = Object.freeze({
  detect: false,
  cliToml: false,
  daemon: false,
  workspace: false,
  history: false,
  install: false,
  workflow: false,
});

/** A stored string → the region map. Pure; never throws. */
export function parsePolyforgeDisclosure(raw: unknown): PolyforgeDisclosure {
  const result: PolyforgeDisclosure = { ...POLYFORGE_DISCLOSURE_DEFAULT };
  if (typeof raw !== "string" || raw === "") return result;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return result;
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return result;
  for (const region of POLYFORGE_DISCLOSURE_REGIONS) {
    const value = (parsed as Record<string, unknown>)[region];
    if (typeof value === "boolean") result[region] = value;
  }
  return result;
}

/** The raw stored string (null when absent or storage is unavailable). */
export function readPolyforgeDisclosureRaw(): string | null {
  try {
    return window.localStorage.getItem(POLYFORGE_DISCLOSURE_KEY);
  } catch {
    return null;
  }
}

// A toggle's result for this page's lifetime when storage refused to keep it
// (blocked, quota): the regions stay reachable with storage unavailable.
// Null while storage holds the truth, so another tab's write still shows.
let session: PolyforgeDisclosure | null = null;
// One parsed object per stored string, so a snapshot read is stable.
let cachedRaw: string | null = null;
let cachedValue: PolyforgeDisclosure = POLYFORGE_DISCLOSURE_DEFAULT;

/** The regions in effect: the in-memory state after a refused write, else
 *  the stored map (the defaults when storage is unavailable). The same object
 *  until the state changes, so it serves as a useSyncExternalStore snapshot. */
export function readPolyforgeDisclosure(): PolyforgeDisclosure {
  if (session) return session;
  const raw = readPolyforgeDisclosureRaw();
  if (raw !== cachedRaw) {
    cachedRaw = raw;
    cachedValue = Object.freeze(parsePolyforgeDisclosure(raw));
  }
  return cachedValue;
}

/** Set one region, keeping the others, store it when storage allows, and tell
 *  the open page either way. Returns the map now in effect. */
export function writePolyforgeDisclosureRegion(region: PolyforgeDisclosureRegion, open: boolean): PolyforgeDisclosure {
  const next = Object.freeze({ ...readPolyforgeDisclosure(), [region]: open });
  try {
    window.localStorage.setItem(POLYFORGE_DISCLOSURE_KEY, JSON.stringify(next));
    session = null;
  } catch {
    session = next;
  }
  try {
    window.dispatchEvent(new Event(POLYFORGE_DISCLOSURE_EVENT));
  } catch {
    // no window event target: the next read still sees the new state
  }
  return next;
}
