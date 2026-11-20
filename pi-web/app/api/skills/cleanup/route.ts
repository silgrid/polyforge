import { NextResponse } from "next/server";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import { cleanupLegacyDisableModelInvocation } from "@/lib/skill-visibility-migration";
import { getSkillEditRoots, refuseSkillFileEdit } from "@/lib/skill-file-access";

export const dynamic = "force-dynamic";

// POST /api/skills/cleanup — the per-skill, user-confirmed legacy cleanup
// (polyforge#146, owner Q3 B): the panel confirms each skill on its own, this
// route writes the hidden preference first — only when none is stored — and
// only then removes the `disable-model-invocation` line from the shared
// SKILL.md, so the skill never
// flips visible mid-cleanup. The guards match PATCH /api/skills: `.md` files
// in loader-visible roots only. Body: { filePath }.
export async function POST(req: Request) {
  try {
    const body = await req.json() as { filePath?: unknown };
    const { filePath } = body;
    if (typeof filePath !== "string" || !filePath) {
      return NextResponse.json({ error: "filePath required" }, { status: 400 });
    }
    const refused = refuseSkillFileEdit(filePath, await getSkillEditRoots());
    if (refused) return NextResponse.json({ error: refused.error }, { status: refused.status });
    const { removed } = await cleanupLegacyDisableModelInvocation(filePath, getAgentDir());
    return NextResponse.json({ success: true, removed });
  } catch (e) {
    return NextResponse.json({ error: e instanceof Error ? e.message : String(e) }, { status: 500 });
  }
}
