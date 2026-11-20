import { homedir } from "node:os";
import { DefaultResourceLoader, getAgentDir } from "@earendil-works/pi-coding-agent";
import type { SkillInfo, SkillsResponse, UnifiedSkillsResponse } from "@/lib/api-types";
import { annotateSkillsWithInstallInfo } from "@/lib/skill-lock";
import { getProjectTrustStatus, projectTrustReloadOptions } from "@/lib/project-trust";
import { importLegacySkillVisibility } from "@/lib/skill-visibility-migration";
import { effectiveDisableModelInvocation } from "@/lib/skill-visibility-config";

export async function loadSkillsWithInstallInfo(cwd: string): Promise<SkillsResponse> {
  const agentDir = getAgentDir();
  // Extensions add skills only through `resources_discover` at session_start,
  // which a listing never runs, so loading them here would only import every
  // extension for nothing (#1226).
  const loader = new DefaultResourceLoader({ cwd, agentDir, noExtensions: true });
  await loader.reload(projectTrustReloadOptions(cwd, agentDir));
  const { skills, diagnostics } = loader.getSkills();
  // One import pass per listing: a legacy `disable-model-invocation: true`
  // line older Pi Web wrote becomes a `hidden` preference, file untouched;
  // the merged visibility is applied right here, so the very listing that
  // imported it already reports the effective value.
  const visibility = await importLegacySkillVisibility(skills, agentDir);
  return {
    skills: annotateSkillsWithInstallInfo(skills.map((skill) => ({
      ...skill,
      declaredDisableModelInvocation: skill.disableModelInvocation,
      disableModelInvocation: effectiveDisableModelInvocation(
        skill.disableModelInvocation,
        visibility,
        skill.filePath,
      ),
    })) as SkillInfo[], { cwd, agentDir }),
    diagnostics,
    projectResourcesLoaded: getProjectTrustStatus(cwd, agentDir).trusted,
  };
}

/**
 * The unified listing (85d AC1/AC3) — the zero-value stub (review r1#1): the
 * signature with a body that answers no rows, so the unit's tests fail on
 * their own assertions at the test commit. The real merge is the
 * implementation commit's.
 */
export async function loadUnifiedSkills(cwd: string | null): Promise<UnifiedSkillsResponse> {
  const loaderCwd = cwd ?? homedir();
  const listing = await loadSkillsWithInstallInfo(loaderCwd);
  return { ...listing, rows: [] };
}
