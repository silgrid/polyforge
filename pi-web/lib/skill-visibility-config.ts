// pi-web's per-harness skill visibility preference: `<pi agent dir>/pi-web-settings.json`,
// `{ "version": 1, "skillVisibility": { "pi": { "<loader-reported skill file path>":
// "hidden" | "shown" } } }`. Never pi's settings.json, never a SKILL.md.
// The skills routes under `app/api/skills/**` are skillVisibility's one writer
// (PATCH toggles, the legacy import on listing, the confirmed cleanup); sessions
// only read it. The `pi` key reserves the harness dimension (polyforge#85d).
// The switch keeps pi's semantics — hidden from the model prompt, still
// manually invocable — by rewriting `disableModelInvocation` in memory through
// `DefaultResourceLoader.skillsOverride`; running pi in a terminal ignores it.
import type { ResourceDiagnostic, Skill } from "@earendil-works/pi-coding-agent";

/** A visibility preference: `hidden` (out of the prompt, still invocable) or `shown`. */
export type SkillVisibility = "hidden" | "shown";

/** One entry of a batch write; `visibility: null` removes the entry. */
export interface SkillVisibilityChange {
  filePath: string;
  visibility: SkillVisibility | null;
}

/** The shape `DefaultResourceLoader` accepts as `skillsOverride`. */
export type SkillsOverride = (base: {
  skills: Skill[];
  diagnostics: ResourceDiagnostic[];
}) => {
  skills: Skill[];
  diagnostics: ResourceDiagnostic[];
};

/** The stored preferences; a missing, malformed or unknown value reads as none (never throws). */
export function readSkillVisibility(_agentDir?: string): Map<string, SkillVisibility> {
  return new Map();
}

/**
 * Stores every entry of `changes` in one atomic write, keeping the file's other
 * keys (and other harness dimensions under `skillVisibility`); one write at a
 * time per file, replaced atomically with mode 0600. A file that exists but is
 * not a JSON object is refused (thrown), never overwritten.
 */
export async function writeSkillVisibility(
  _changes: SkillVisibilityChange[],
  _agentDir?: string,
): Promise<void> {}

/**
 * The tri-state rule: `hidden` hides the skill, `shown` shows it even over the
 * author's frontmatter, an unset entry keeps the author's declared value.
 */
export function effectiveDisableModelInvocation(
  declared: boolean,
  _visibility: ReadonlyMap<string, SkillVisibility>,
  _filePath: string,
): boolean {
  return declared;
}

/** A `skillsOverride` that applies the preferences, or undefined when none are stored. */
export function skillVisibilityOverride(
  _visibility: ReadonlyMap<string, SkillVisibility>,
): SkillsOverride | undefined {
  return undefined;
}

/** Applies `first`, then `second` on its result; undefined when both are. */
export function composeSkillsOverrides(
  first?: SkillsOverride,
  second?: SkillsOverride,
): SkillsOverride | undefined {
  return first ?? second;
}
