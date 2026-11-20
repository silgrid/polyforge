import { NextResponse } from "next/server";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import type { SkillToggleResult } from "@/lib/api-types";
import { loadSkillsWithInstallInfo } from "@/lib/skills-service";
import { writeSkillVisibility } from "@/lib/skill-visibility-config";
import { getSkillEditRoots, refuseSkillFileEdit } from "@/lib/skill-file-access";
import { getAllowedFileRoots, isExistingFilePathAllowed } from "@/lib/file-access";

export const dynamic = "force-dynamic";

// GET /api/skills?cwd=<path>
// Uses DefaultResourceLoader (same logic as AgentSession startup) so settings.json
// skill paths, package skills, and .agents/skills directories are all included.
export async function GET(req: Request) {
  const { searchParams } = new URL(req.url);
  const cwd = searchParams.get("cwd");
  if (!cwd) return NextResponse.json({ error: "cwd required" }, { status: 400 });

  try {
    const allowedRoots = await getAllowedFileRoots();
    if (!isExistingFilePathAllowed(cwd, allowedRoots)) {
      return NextResponse.json({ error: "Access denied" }, { status: 403 });
    }
    return NextResponse.json(await loadSkillsWithInstallInfo(cwd));
  } catch (e) {
    return NextResponse.json({ error: String(e) }, { status: 500 });
  }
}

// PATCH /api/skills — switch a skill's visibility for pi sessions by writing
// pi-web's own preference store (`skillVisibility.pi` in
// `<agentDir>/pi-web-settings.json`), never the shared SKILL.md: other
// harnesses read the same file, so the switch must not touch it. The
// preference is keyed by the loader-reported skill file path and applied in
// memory at session load (lib/skill-visibility-config.ts); running pi in a
// terminal ignores it.
// Body: { filePath, disableModelInvocation } for one skill, or
// { filePaths, disableModelInvocation } for the panel's "Enable all" /
// "Disable all". A batch validates each target on its own and reports every
// one, so a target the route refuses does not stop the rest; the accepted
// ones land together in one atomic write, never half of them.
export async function PATCH(req: Request) {
  try {
    const body = await req.json() as {
      filePath?: string;
      filePaths?: unknown;
      disableModelInvocation: boolean;
    };
    const { filePath, filePaths, disableModelInvocation } = body;

    if (typeof disableModelInvocation !== "boolean") {
      return NextResponse.json({ error: "disableModelInvocation must be a boolean" }, { status: 400 });
    }

    if (filePaths !== undefined) {
      if (!Array.isArray(filePaths) || !filePaths.every((item) => typeof item === "string" && item)) {
        return NextResponse.json({ error: "filePaths must be a list of paths" }, { status: 400 });
      }
      const allowedRoots = await getSkillEditRoots();
      const targets = [...new Set(filePaths as string[])];
      const refusals = new Map<string, string>();
      const accepted: string[] = [];
      for (const target of targets) {
        const refused = refuseSkillFileEdit(target, allowedRoots);
        if (refused) refusals.set(target, refused.error);
        else accepted.push(target);
      }
      if (accepted.length > 0) {
        await writeSkillVisibility(
          accepted.map((target) => ({ filePath: target, visibility: disableModelInvocation ? "hidden" : "shown" })),
          getAgentDir(),
        );
      }
      const results: SkillToggleResult[] = targets.map((target) =>
        refusals.has(target) ? { filePath: target, error: refusals.get(target) } : { filePath: target });
      return NextResponse.json({ results });
    }

    if (!filePath) return NextResponse.json({ error: "filePath required" }, { status: 400 });
    const refused = refuseSkillFileEdit(filePath, await getSkillEditRoots());
    if (refused) return NextResponse.json({ error: refused.error }, { status: refused.status });
    await writeSkillVisibility(
      [{ filePath, visibility: disableModelInvocation ? "hidden" : "shown" }],
      getAgentDir(),
    );
    return NextResponse.json({ success: true });
  } catch (e) {
    return NextResponse.json({ error: String(e) }, { status: 500 });
  }
}
