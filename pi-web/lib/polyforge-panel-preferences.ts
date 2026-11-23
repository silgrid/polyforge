/**
 * Browser-only preferences of the Polyforge Jobs-tab card stream,
 * set in Settings › Polyforge: the goal-title length and
 * the card poll interval. They live in this browser's localStorage only —
 * never pf config, never a server file. Client-safe: no Node import.
 */

export const POLYFORGE_TITLE_LENGTH_KEY = "pi-web:polyforge-title-length";
export const POLYFORGE_POLL_INTERVAL_KEY = "pi-web:polyforge-poll-interval";
/** polyforge#240: the browser notification switch (on by default). */
export const POLYFORGE_NOTIFICATIONS_KEY = "pi-web:polyforge-notifications";
/** Dispatched on window after a save, so an open panel applies it without a reload. */
export const POLYFORGE_PREFERENCES_EVENT = "pi-web:polyforge-preferences";

export interface PolyforgePreferenceSpec {
  key: string;
  defaultValue: number;
  min: number;
  max: number;
}

/** The goal's truncation length in code points. owner-intended change:
 *  the default is 60 (was 30) so a goal fills the card's two-line clamp; the
 *  10/200 bounds are unchanged, and a browser that stored 30 keeps 30. */
export const POLYFORGE_TITLE_LENGTH: PolyforgePreferenceSpec = { key: POLYFORGE_TITLE_LENGTH_KEY, defaultValue: 60, min: 10, max: 200 };
/** Seconds. */
export const POLYFORGE_POLL_INTERVAL: PolyforgePreferenceSpec = { key: POLYFORGE_POLL_INTERVAL_KEY, defaultValue: 3, min: 1, max: 60 };

/** A browser-stored boolean preference: its key and the value an absent or
 *  unreadable store reads as. */
export interface PolyforgeBooleanPreferenceSpec {
  key: string;
  defaultValue: boolean;
}

/** polyforge#240: owner notifications are on until the owner turns them off. */
export const POLYFORGE_NOTIFICATIONS: PolyforgeBooleanPreferenceSpec = {
  key: POLYFORGE_NOTIFICATIONS_KEY,
  defaultValue: true,
};

/** A stored value → the value to use: absent, corrupt, non-numeric or
 *  non-positive text (a zero or negative length/interval is never a real
 *  setting) reads as the default; a positive value outside the bounds
 *  clamps to the nearer bound; fractions round. Never NaN, never zero or
 *  negative, never out of range. Pure. */
export function clampPolyforgePreference(spec: PolyforgePreferenceSpec, raw: unknown): number {
  const text = typeof raw === "number" ? String(raw) : typeof raw === "string" ? raw.trim() : "";
  if (!/^\+?(\d+(\.\d*)?|\.\d+)$/.test(text)) return spec.defaultValue;
  const value = Math.round(Number(text));
  if (!Number.isFinite(value) || value <= 0) return spec.defaultValue;
  return Math.min(spec.max, Math.max(spec.min, value));
}

/** The stored value (clamped); the default when storage is unavailable. */
export function readPolyforgePreference(spec: PolyforgePreferenceSpec): number {
  try {
    return clampPolyforgePreference(spec, window.localStorage.getItem(spec.key));
  } catch {
    return spec.defaultValue;
  }
}

/** Store the clamped value and tell open panels. Returns the stored value,
 *  or null when storage refused the write. */
export function writePolyforgePreference(spec: PolyforgePreferenceSpec, raw: unknown): number | null {
  const value = clampPolyforgePreference(spec, raw);
  try {
    window.localStorage.setItem(spec.key, String(value));
  } catch {
    return null;
  }
  try {
    window.dispatchEvent(new Event(POLYFORGE_PREFERENCES_EVENT));
  } catch {
    // no window event target: the next mount reads the stored value
  }
  return value;
}

/** A stored boolean → the value to use: only `true`/`false` (trimmed,
 *  case-insensitive) are read; absent, corrupt or wrong-typed reads as the
 *  spec's default. Pure. */
export function clampPolyforgeBooleanPreference(spec: PolyforgeBooleanPreferenceSpec, raw: unknown): boolean {
  const text = typeof raw === "boolean" ? String(raw) : typeof raw === "string" ? raw.trim().toLowerCase() : "";
  if (text === "true") return true;
  if (text === "false") return false;
  return spec.defaultValue;
}

/** The stored boolean; the default when storage is unavailable. */
export function readPolyforgeBooleanPreference(spec: PolyforgeBooleanPreferenceSpec): boolean {
  try {
    return clampPolyforgeBooleanPreference(spec, window.localStorage.getItem(spec.key));
  } catch {
    return spec.defaultValue;
  }
}

/** Store the boolean and tell open panels. Returns the stored value, or null
 *  when storage refused the write. */
export function writePolyforgeBooleanPreference(spec: PolyforgeBooleanPreferenceSpec, raw: unknown): boolean | null {
  const value = clampPolyforgeBooleanPreference(spec, raw);
  try {
    window.localStorage.setItem(spec.key, value ? "true" : "false");
  } catch {
    return null;
  }
  try {
    window.dispatchEvent(new Event(POLYFORGE_PREFERENCES_EVENT));
  } catch {
    // no window event target: the next mount reads the stored value
  }
  return value;
}

/** The browser notification switch (polyforge#240), through the one boolean store. */
export function readPolyforgeNotificationPreference(spec: PolyforgeBooleanPreferenceSpec = POLYFORGE_NOTIFICATIONS): boolean {
  return readPolyforgeBooleanPreference(spec);
}

/** Set the browser notification switch; null when storage refused the write. */
export function writePolyforgeNotificationPreference(
  spec: PolyforgeBooleanPreferenceSpec,
  value: boolean,
): boolean | null {
  return writePolyforgeBooleanPreference(spec, value);
}
