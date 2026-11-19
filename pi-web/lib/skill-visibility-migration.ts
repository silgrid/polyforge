// Legacy `disable-model-invocation: true` lines in shared SKILL.md files:
// older Pi Web wrote them as its switch, but other harnesses read the same
// frontmatter. Import reads them into pi-web's own preference store without
// touching any file; the confirmed cleanup is the one remaining SKILL.md
// edit — it writes the preference first, then removes the line, so the
// skill never flips visible mid-cleanup (polyforge#146, owner Q3 A+B).
import { readFileSync, writeFileSync } from "fs";
import { parseFrontmatter } from "@earendil-works/pi-coding-agent";
import { setDisableModelInvocation } from "./skill-frontmatter";
import {
  readSkillVisibility,
  writeSkillVisibility,
  type SkillVisibility,
} from "./skill-visibility-config";

/** The loader-shaped skill fields the import reads. */
export interface LegacyVisibilitySkill {
  filePath: string;
  disableModelInvocation: boolean;
}

/**
 * Imports every author-hidden skill (declared `disable-model-invocation: true`)
 * that has no stored preference as a `hidden` preference, without touching any
 * file. Returns the merged visibility (the stored preferences plus the import),
 * so a listing that runs the import applies the result in the same breath.
 */
export async function importLegacySkillVisibility(
  skills: LegacyVisibilitySkill[],
  agentDir?: string,
): Promise<Map<string, SkillVisibility>> {
  const visibility = new Map(readSkillVisibility(agentDir));
  const imports = skills
    .filter((skill) => skill.disableModelInvocation && !visibility.has(skill.filePath))
    .map((skill) => ({ filePath: skill.filePath, visibility: "hidden" as const }));
  if (imports.length > 0) {
    await writeSkillVisibility(imports, agentDir);
    for (const entry of imports) visibility.set(entry.filePath, "hidden");
  }
  return visibility;
}

/**
 * Removes the legacy `disable-model-invocation` line from one SKILL.md. The
 * hidden preference is written first, so a skill the line was hiding stays
 * hidden when the line goes; the edit itself reuses the surgical line editor
 * (`skill-frontmatter.ts`) so the file's other formatting survives. A file
 * without the line is left alone and gets no preference. Throws when the
 * line cannot be edited surgically — after the preference is in.
 */
export async function cleanupLegacyDisableModelInvocation(
  filePath: string,
  agentDir?: string,
): Promise<{ removed: boolean }> {
  const content = readFileSync(filePath, "utf8");
  const { frontmatter } = parseFrontmatter<Record<string, unknown>>(content);
  const declared = frontmatter["disable-model-invocation"] === true;
  // The preference first: once the line is gone the author's `true` no longer
  // hides the skill, so the switch must already say hidden before the edit.
  if (declared) {
    const visibility = readSkillVisibility(agentDir);
    if (visibility.get(filePath) !== "hidden") {
      await writeSkillVisibility([{ filePath, visibility: "hidden" }], agentDir);
    }
  }
  const updated = setDisableModelInvocation(content, false);
  // A skill already without the line keeps its file and its mtime.
  if (updated === content) return { removed: false };
  writeFileSync(filePath, updated, "utf8");
  return { removed: true };
}
