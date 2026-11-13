/**
 * Shared structural type guards (audit P3): one `isRecord` instead of the
 * fifteen per-module copies that previously drifted across lib/ and app/.
 */

/** Narrow `unknown` to a plain, non-array object. */
export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
