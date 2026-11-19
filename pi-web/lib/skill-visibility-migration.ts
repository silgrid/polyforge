// Legacy `disable-model-invocation: true` lines in shared SKILL.md files:
// older Pi Web wrote them as its switch, but other harnesses read the same
// frontmatter. Import reads them into pi-web's own preference store without
// touching any file; the confirmed cleanup is the one remaining SKILL.md
// edit — it writes the preference first (only when none is stored), then
// removes the line, so the skill never flips visible mid-cleanup
// (polyforge#146, owner Q3 A+B).
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
  const visibility = readSkillVisibility(agentDir);
  const candidates = skills.filter((skill) => skill.disableModelInvocation);
  if (candidates.length === 0) return visibility;
  // The unset check runs inside the file's write chain: a preference a
  // concurrent switch stored between the read above and this write is never
  // overwritten, and a second listing that finds every entry already set
  // writes nothing at all (r1#5).
  await writeSkillVisibility(
    candidates.map((skill) => ({ filePath: skill.filePath, visibility: "hidden" as const, onlyIfUnset: true })),
    agentDir,
  );
  return readSkillVisibility(agentDir);
}

/**
 * Removes the legacy `disable-model-invocation` line from one SKILL.md. The
 * hidden preference is written first — only when none is stored, so a user's
 *「shown」survives the cleanup (r1#1) — because once the line is gone the
 * author's `true` no longer hides an unset skill. The edit itself reuses the
 * surgical line editor (`skill-frontmatter.ts`) so the file's other
 * formatting survives. A file without the line is left alone and gets no
 * preference. Throws when the line cannot be edited surgically — after the
 * preference is in.
 */
export async function cleanupLegacyDisableModelInvocation(
  filePath: string,
  agentDir?: string,
): Promise<{ removed: boolean }> {
  const content = readFileSync(filePath, "utf8");
  const { frontmatter } = parseFrontmatter<Record<string, unknown>>(content);
  const declared = frontmatter["disable-model-invocation"] === true;
  if (declared) {
    await writeSkillVisibility([{ filePath, visibility: "hidden", onlyIfUnset: true }], agentDir);
  }
  const updated = setDisableModelInvocation(content, false);
  // A skill already without the line keeps its file and its mtime.
  if (updated === content) return { removed: false };
  writeFileSync(filePath, updated, "utf8");
  return { removed: true };
}
