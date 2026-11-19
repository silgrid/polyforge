// Legacy `disable-model-invocation: true` lines in shared SKILL.md files:
// older Pi Web wrote them as its switch, but other harnesses read the same
// frontmatter. Import reads them into pi-web's own preference store without
// touching any file; the confirmed cleanup is the one remaining SKILL.md
// edit — it writes the preference first, then removes the line, so the
// skill never flips visible mid-cleanup (polyforge#146, owner Q3 A+B).
import type { SkillVisibility } from "./skill-visibility-config";

/** The loader-shaped skill fields the import reads. */
export interface LegacyVisibilitySkill {
  filePath: string;
  disableModelInvocation: boolean;
}

/**
 * Imports every author-hidden skill (declared `disable-model-invocation: true`)
 * that has no stored preference as a `hidden` preference, without touching any
 * file. Returns the merged visibility (the stored preferences plus the import).
 */
export async function importLegacySkillVisibility(
  _skills: LegacyVisibilitySkill[],
  _agentDir?: string,
): Promise<Map<string, SkillVisibility>> {
  return new Map();
}

/**
 * Removes the legacy `disable-model-invocation` line from one SKILL.md. The
 * hidden preference is written first, so a skill the line was hiding stays
 * hidden when the line goes; the edit itself reuses the surgical line editor
 * (`skill-frontmatter.ts`) so the file's other formatting survives. Throws
 * when the line cannot be edited surgically — after the preference is in.
 */
export async function cleanupLegacyDisableModelInvocation(
  _filePath: string,
  _agentDir?: string,
): Promise<{ removed: boolean }> {
  return { removed: false };
}
