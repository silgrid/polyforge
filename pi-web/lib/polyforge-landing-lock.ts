/**
 * Client-safe pure model of the landing-lock projection (polyforge#245 epic
 * 3/3, Q11). The server forwards only the envelope a ticket's own queue
 * needs: a state, the source observation stamps, the consumer age and one
 * entry per lock key. The holder, its token, a machine/request id and other
 * tickets never cross the passport. Both the work-item list and the watch
 * snapshot use these functions, so the two faces cannot drift.
 */

export type PolyforgeLandingLockState = "waiting" | "none" | "stale" | "unavailable";

export interface PolyforgeLandingLockKey {
  repo: string;
  branch: string;
  position: number;
  since?: string;
}

export interface PolyforgeLandingLock {
  state: PolyforgeLandingLockState;
  observedAt?: string;
  asOf?: string;
  ageMs?: number;
  keys: PolyforgeLandingLockKey[];
}

const STATES = new Set<PolyforgeLandingLockState>(["waiting", "none", "stale", "unavailable"]);

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function nonEmpty(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() !== "" ? value : undefined;
}

function position(value: unknown): number | undefined {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0 ? value : undefined;
}

function nonNegative(value: unknown): number | undefined {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

/**
 * One raw projection through the whitelist, or undefined when it cannot be
 * shown honestly. A waiting/stale state with no valid key is undefined
 * (never a fabricated `#0`); unknown states and malformed positions degrade
 * the same way. Only the listed fields are copied.
 */
export function sanitizeLandingLock(raw: unknown): PolyforgeLandingLock | undefined {
  const row = asRecord(raw);
  if (!row) return undefined;
  const state = typeof row.state === "string" && STATES.has(row.state as PolyforgeLandingLockState)
    ? row.state as PolyforgeLandingLockState : undefined;
  if (!state) return undefined;
  const keys: PolyforgeLandingLockKey[] = [];
  if (Array.isArray(row.keys)) {
    for (const entry of row.keys) {
      const key = asRecord(entry);
      if (!key) continue;
      const repo = nonEmpty(key.repo);
      const pos = position(key.position);
      if (!repo || pos === undefined || typeof key.branch !== "string") continue;
      const since = nonEmpty(key.since);
      keys.push({ repo, branch: key.branch, position: pos, ...(since ? { since } : {}) });
    }
  }
  if ((state === "waiting" || state === "stale") && keys.length === 0) return undefined;
  const observedAt = nonEmpty(row.observed_at);
  const asOf = nonEmpty(row.as_of);
  const ageMs = nonNegative(row.age_ms);
  return {
    state,
    keys,
    ...(observedAt ? { observedAt } : {}),
    ...(asOf ? { asOf } : {}),
    ...(ageMs === undefined ? {} : { ageMs }),
  };
}

/** The 1-based positions in the projection's key order: `#1,#3`. */
export function landingLockPositions(lock: PolyforgeLandingLock): string {
  const out = lock.keys.map((key) => `#${key.position}`);
  return out.length > 0 ? out.join(",") : "";
}

/** The lock keys for a hover/detail: `repo (branch)`, comma separated. */
export function landingLockKeysText(lock: PolyforgeLandingLock): string {
  return lock.keys.map((key) => (key.branch ? `${key.repo} (${key.branch})` : key.repo)).join(", ");
}

/** The one-line human label; `t` is the panel's i18n translator. */
export function landingLockText(lock: PolyforgeLandingLock, t: (key: string, values?: Record<string, string>) => string): string {
  const positions = landingLockPositions(lock);
  if ((lock.state === "waiting" || lock.state === "stale") && positions === "") return "";
  switch (lock.state) {
    case "waiting":
      return t("polyforge.landingLock.waiting", { positions });
    case "stale":
      return t("polyforge.landingLock.stale", { positions });
    case "unavailable":
      return t("polyforge.landingLock.unavailable");
    default:
      return "";
  }
}
