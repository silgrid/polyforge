import { homedir } from "node:os";
import { DefaultResourceLoader, getAgentDir } from "@earendil-works/pi-coding-agent";
import type { SkillInfo, SkillsResponse, UnifiedSkillsResponse } from "@/lib/api-types";
import { annotateSkillsWithInstallInfo } from "@/lib/skill-lock";
import { getProjectTrustStatus, projectTrustReloadOptions } from "@/lib/project-trust";
import { mergeSkillListings, type ClaudeSkillsListing } from "@/lib/skills-unified";
import { createClaudeAdapter } from "@/lib/harness/claude";
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
 * The unified listing (85d AC1/AC3): the pi listing as before plus the merged
 * rows the Skills panel renders. `cwd === null` is the no-project mode: both
 * sides answer the user scope alone (claude reads no project layers; pi keeps
 * only its user-side skills), and nothing about project trust is reported.
 */
export async function loadUnifiedSkills(cwd: string | null): Promise<UnifiedSkillsResponse> {
  const agentDir = getAgentDir();
  // The loader needs some working directory: without a project the user's own
  // home stands in, an untrusted folder whose project resources stay out.
  const loaderCwd = cwd ?? homedir();
  const listing = await loadSkillsWithInstallInfo(loaderCwd);

  const claudeAdapter = createClaudeAdapter();
  const claudePayload = await claudeAdapter.read("skills", cwd ? { cwd } : {});
  const claude = claudePayload as { entries: ClaudeSkillsListing["entries"]; overrides?: Record<string, string> };

  // One trust boundary for both halves: a project pi does not trust keeps its
  // claude-side skills out too — the panel's projectResourcesLoaded notice
  // stays the single truth about project resources (the existing panel's
  // semantics, kept by the unified listing).
  const projectTrusted = cwd === null ? true : listing.projectResourcesLoaded;
  const piSkills = cwd === null
    ? listing.skills.filter((skill) => skill.sourceInfo?.source !== "project" && skill.sourceInfo?.scope !== "project")
    : listing.skills;
  const claudeEntries = (claude?.entries ?? []).filter((entry) =>
    cwd === null ? entry.source !== "project" : (entry.source !== "project" || projectTrusted));

  const rows = mergeSkillListings(piSkills, { entries: claudeEntries, ...(claude?.overrides ? { overrides: claude.overrides } : {}) });
  return {
    ...listing,
    skills: listing.skills,
    rows: cwd === null
      ? rows.filter((row) => row.scope === "user")
      : rows,
    projectResourcesLoaded: cwd === null ? true : listing.projectResourcesLoaded,
  };
}
