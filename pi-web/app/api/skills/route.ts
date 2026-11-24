import { NextResponse } from "next/server";
import { statSync } from "node:fs";
import { isAbsolute, resolve } from "node:path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import type { SkillApplyResult } from "@/lib/api-types";
import { loadUnifiedSkills } from "@/lib/skills-service";
import { writeSkillVisibility } from "@/lib/skill-visibility-config";
import { getSkillEditRoots, refuseSkillFileEdit } from "@/lib/skill-file-access";
import { getAllowedFileRoots, isExistingFilePathAllowed } from "@/lib/file-access";
import { createClaudeAdapter } from "@/lib/harness/claude";
import { isClaudeSkillOverrideState } from "@/lib/claude-shared";

export const dynamic = "force-dynamic";

// GET /api/skills?cwd=<path> — the unified listing (85d): pi's loader listing
// as before, plus the merged rows the Skills panel renders (both harnesses,
// grouped 用户 / 项目). Without a cwd the answer is the user scope alone
// (AC3). Uses DefaultResourceLoader (same logic as AgentSession startup) so
// settings paths, package skills, and .agents/skills directories are all
// included the way the runtime sees them; claude's half is the adapter's own
// directory read, never a spawn.
export async function GET(req: Request) {
  const { searchParams } = new URL(req.url);
  const cwdParam = searchParams.get("cwd");
  let cwd: string | null = null;
  if (cwdParam !== null) {
    if (!cwdParam.trim() || !isAbsolute(cwdParam)) {
      return NextResponse.json({ error: "cwd must be an absolute path" }, { status: 400 });
    }
    cwd = resolve(cwdParam);
    try {
      const allowedRoots = await getAllowedFileRoots();
      if (!isExistingFilePathAllowed(cwd, allowedRoots)) {
        return NextResponse.json({ error: "Access denied" }, { status: 403 });
      }
      if (!statSync(cwd).isDirectory()) {
        return NextResponse.json({ error: "cwd must be a directory" }, { status: 400 });
      }
    } catch {
      return NextResponse.json({ error: "cwd must be a directory" }, { status: 400 });
    }
  }

  try {
    return NextResponse.json(await loadUnifiedSkills(cwd));
  } catch (e) {
    return NextResponse.json({ error: String(e) }, { status: 500 });
  }
}

/** One parsed switch item: which harness's own store a skill's state lands in. */
interface SkillSwitchItem {
  skill: string;
  harness: "pi" | "claude";
  state: string;
  scope?: "user" | "project";
}

/** Parses one switch item; null with an error message when the shape is not one the route writes. */
function parseSwitchItem(raw: unknown): { item: SkillSwitchItem } | { error: string } {
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    return { error: "each item must be an object" };
  }
  const candidate = raw as Record<string, unknown>;
  if (typeof candidate.skill !== "string" || !candidate.skill) {
    return { error: "skill must be a non-empty string" };
  }
  if (candidate.harness !== "pi" && candidate.harness !== "claude") {
    return { error: "harness must be \"pi\" or \"claude\"" };
  }
  if (typeof candidate.state !== "string") {
    return { error: "state must be a string" };
  }
  const harness = candidate.harness;
  if (harness === "pi" && candidate.state !== "enabled" && candidate.state !== "disabled") {
    return { error: "a pi state is \"enabled\" or \"disabled\"" };
  }
  if (harness === "claude" && !isClaudeSkillOverrideState(candidate.state)) {
    return { error: `unknown skillOverrides state: ${String(candidate.state)}` };
  }
  let scope: "user" | "project" | undefined;
  if (candidate.scope !== undefined) {
    if (candidate.scope !== "user" && candidate.scope !== "project") {
      return { error: "scope must be \"user\" or \"project\"" };
    }
    scope = candidate.scope;
  }
  if (harness === "pi" && scope !== undefined) {
    return { error: "a pi item has no scope; the skill path is its identity" };
  }
  return { item: { skill: candidate.skill, harness, state: candidate.state, scope } };
}

/**
 * Applies every item (85d AC2): a pi item writes pi-web's own preference
 * store (`skillVisibility.pi` in `<agentDir>/pi-web-settings.json`), a
 * claude item writes claude's native `skillOverrides` — user scope in
 * `$CLAUDE_CONFIG_DIR/settings.json`, project scope in the request cwd's
 * `.claude/settings.local.json` (owner Q3 B) — through the adapter's
 * version-gated write. Never the shared SKILL.md: other harnesses read the
 * same file, so the switch must not touch it. The accepted pi items land
 * together in one atomic write; every item is reported on its own, so one a
 * guard or the gate refuses does not stop the rest.
 */
/** polyforge#259 T4 (owner 2A): a refused item's error keeps its words and gains a
 *  code, so the panel can say it in Chinese and fold the original. */
function withRefusalCodes(results: SkillApplyResult[]): (SkillApplyResult & { code?: string })[] {
  return results.map((result) => (result.error ? { ...result, code: "skills-write-refused" } : result));
}

async function applySwitchItems(
  items: SkillSwitchItem[],
  cwd: string | null,
): Promise<SkillApplyResult[]> {
  // The same item twice is one request (the batch's collapse, as the old
  // filePaths switch had); a first occurrence keeps its place in the report.
  const seen = new Set<string>();
  items = items.filter((item) => {
    const key = JSON.stringify(item);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
  const allowedRoots = await getSkillEditRoots();
  const claudeAdapter = createClaudeAdapter();
  const piAccepted: { filePath: string; visibility: "hidden" | "shown" }[] = [];
  const piRefusals = new Map<number, string>();
  const claudeResults = new Map<number, { error?: string }>();

  for (let index = 0; index < items.length; index++) {
    const item = items[index];
    if (item.harness === "pi") {
      const refused = refuseSkillFileEdit(item.skill, allowedRoots);
      if (refused) piRefusals.set(index, refused.error);
      else piAccepted.push({ filePath: item.skill, visibility: item.state === "disabled" ? "hidden" : "shown" });
    } else {
      const scope = item.scope ?? "user";
      if (scope === "project" && !cwd) {
        claudeResults.set(index, { error: "a project skills write needs the project folder (cwd)" });
        continue;
      }
      const outcome = await claudeAdapter.write(
        "skills",
        { name: item.skill, state: item.state, scope },
        cwd ? { cwd } : {},
      );
      claudeResults.set(index, outcome.status === "written" ? {} : { error: outcome.status === "refused" ? outcome.reason : `the write was not made (${outcome.status})` });
    }
  }

  if (piAccepted.length > 0) {
    await writeSkillVisibility(piAccepted, getAgentDir());
  }

  return items.map((item, index) => {
    const refused = piRefusals.get(index) ?? claudeResults.get(index)?.error;
    return refused === undefined
      ? { skill: item.skill, harness: item.harness }
      : { skill: item.skill, harness: item.harness, error: refused };
  });
}

// PATCH /api/skills — switch a skill's state for one harness (85d AC2).
// Body: { skill, harness, state, scope? } for one row's 「应用到」, or
// { items: [{ skill, harness, state, scope? }, …] } for the panel's group
// switches. A pi item's `skill` is the loader-reported skill file path and
// its state is "enabled" | "disabled"; a claude item's `skill` is claude's
// own entry name (the native skillOverrides key), its state one of claude's
// native words and its scope "user" (default) or "project" — the unified
// 「停用」 the panel sends for claude is "user-invocable-only" (owner Q4).
export async function PATCH(req: Request) {
  try {
    const { searchParams } = new URL(req.url);
    const cwdParam = searchParams.get("cwd");
    let cwd: string | null = null;
    if (cwdParam !== null) {
      if (!cwdParam.trim() || !isAbsolute(cwdParam)) {
        return NextResponse.json({ error: "cwd must be an absolute path" }, { status: 400 });
      }
      cwd = resolve(cwdParam);
      const allowedRoots = await getAllowedFileRoots();
      if (!isExistingFilePathAllowed(cwd, allowedRoots)) {
        return NextResponse.json({ error: "Access denied" }, { status: 403 });
      }
    }

    const body = await req.json() as Record<string, unknown>;

    let items: SkillSwitchItem[];
    if (body.items !== undefined) {
      if (!Array.isArray(body.items)) {
        return NextResponse.json({ error: "items must be a list" }, { status: 400 });
      }
      const parsed = body.items.map((entry) => parseSwitchItem(entry));
      const malformed = parsed.findIndex((entry) => "error" in entry);
      if (malformed !== -1) {
        return NextResponse.json({ error: (parsed[malformed] as { error: string }).error }, { status: 400 });
      }
      items = (parsed as { item: SkillSwitchItem }[]).map((entry) => entry.item);
      return NextResponse.json({ results: withRefusalCodes(await applySwitchItems(items, cwd)) });
    }

    const single = parseSwitchItem(body);
    if ("error" in single) return NextResponse.json({ error: single.error }, { status: 400 });
    const results = withRefusalCodes(await applySwitchItems([single.item], cwd));
    const refused = results[0];
    // owner 2A: the words and the status stay; the refusal's code is appended.
    if (refused.error) return NextResponse.json({ error: refused.error, code: "skills-write-refused" }, { status: 400 });
    return NextResponse.json({ success: true });
  } catch (e) {
    return NextResponse.json({ error: String(e) }, { status: 500 });
  }
}
