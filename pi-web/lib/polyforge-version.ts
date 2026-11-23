/**
 * The browser-safe half of the pf version comparison (polyforge#256 B1/B2).
 * Signatures first (the test-first commit): the implementation lands with
 * unit u1.
 */

/** The minimum pf the dock documents (the `--demand` floor, v2.18.1). */
export const POLYFORGE_MINIMUM_PF_VERSION = "2.18.1";

export interface ParsedPolyforgeVersion {
  core: [number, number, number];
  preRelease: string[];
}

export function parsePolyforgeVersion(_value: string): ParsedPolyforgeVersion | null {
  return null;
}

export function comparePolyforgeVersions(_a: string, _b: string): number | null {
  return null;
}

export function comparePolyforgeVersionOrder(_a: string, _b: string): "ahead" | "behind" | "equal" | null {
  return null;
}

export function isPolyforgePrerelease(_value: string): boolean {
  return false;
}

export function isPolyforgeVersionBelowMinimum(
  _version: string | null,
  _minimum: string = POLYFORGE_MINIMUM_PF_VERSION,
  _unknownIsBelow = false,
): boolean {
  return false;
}
