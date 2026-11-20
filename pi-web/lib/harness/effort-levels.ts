// The unified reasoning-effort mapping (#85 85a3-1, AC3): the five levels
// both harnesses spell the same — low, medium, high, xhigh, max — and each
// harness's own extra values listed apart, never forced onto the other side.
// pi holds off and minimal (its seven-level vocabulary), claude holds
// ultracode (its /effort command's own mode) and spells "default" for unset.
// Client-safe: never import a server-only module here.
//
// Skeleton (85a3 u1): every signature with zero-value behaviour, so the
// suite's red is its assertions, never a missing symbol.

/** The unified five levels, ordered. */
export const UNIFIED_EFFORT_LEVELS: readonly ("low" | "medium" | "high" | "xhigh" | "max")[] = [];

/** The unified five levels. */
export type UnifiedEffort = "low" | "medium" | "high" | "xhigh" | "max";

/** pi's own levels outside the unified five (单列, never written to claude). */
export const PI_SPECIFIC_EFFORTS: readonly ("off" | "minimal")[] = [];

/** A pi level: the unified five plus pi's own off and minimal. */
export type PiEffortLevel = "off" | "minimal" | UnifiedEffort;

/** claude's own values outside the unified five (单列). */
export const CLAUDE_SPECIFIC_EFFORTS: readonly ("ultracode")[] = [];

/** claude's spelling of an unset effort (未设置, never a level of its own). */
export const CLAUDE_EFFORT_UNSET = "default";

export function isUnifiedEffort(value: unknown): value is UnifiedEffort {
  return false;
}

/** Whether `value` is one of pi's own levels (off, minimal). */
export function isPiSpecificEffort(value: unknown): boolean {
  return false;
}

/** Whether `value` is one of claude's own values (ultracode). */
export function isClaudeSpecificEffort(value: unknown): boolean {
  return false;
}

/** Whether `value` is claude's unset spelling ("default"). */
export function isClaudeEffortUnset(value: unknown): boolean {
  return false;
}

/** A pi spelling onto the unified five; pi's own or unknown spellings answer null. */
export function unifiedFromPiEffort(raw: unknown): UnifiedEffort | null {
  return null;
}

/** A claude spelling onto the unified five; claude's own, unset or unknown spellings answer null. */
export function unifiedFromClaudeEffort(raw: unknown): UnifiedEffort | null {
  return null;
}

/** The unified level's pi spelling. */
export function piEffortFromUnified(level: UnifiedEffort): string {
  return "";
}

/** The unified level's claude spelling. */
export function claudeEffortFromUnified(level: UnifiedEffort): string {
  return "";
}

/**
 * The global default's one-write mapping (一写多读): a level the star saves
 * reaches every harness that spells it — pi's `defaultThinkingLevel` and
 * claude's `effortLevel` — while a pi-specific level (off, minimal) stays
 * pi's alone, claude answering null (单列, never forced).
 */
export function unifiedGlobalEffortWrite(level: PiEffortLevel): {
  unified: UnifiedEffort | null;
  pi: string;
  claude: string | null;
} {
  return { unified: null, pi: "", claude: null };
}
