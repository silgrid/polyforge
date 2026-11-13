import type { PackageSource } from "@earendil-works/pi-coding-agent";

/**
 * The package-entry facts the plugins route and the Polyforge detection
 * (pi#122) share, extracted from `app/api/plugins/route.ts` so the
 * disabled-shape rule lives once: a package entry is disabled exactly when
 * it is an object whose four resource arrays are all empty.
 */

/** A package entry's source string, whether the entry is a bare string or an object. */
export function getPackageSource(entry: PackageSource): string {
  return typeof entry === "string" ? entry : entry.source;
}

/** Disabled = an object entry whose extensions/skills/prompts/themes arrays are all empty. */
export function isDisabledPackage(entry: PackageSource): boolean {
  if (typeof entry === "string") return false;
  return (
    Array.isArray(entry.extensions) && entry.extensions.length === 0 &&
    Array.isArray(entry.skills) && entry.skills.length === 0 &&
    Array.isArray(entry.prompts) && entry.prompts.length === 0 &&
    Array.isArray(entry.themes) && entry.themes.length === 0
  );
}