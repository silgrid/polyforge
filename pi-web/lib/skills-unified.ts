// The unified skills listing's merge (#85 85d, AC1): pi's loader listing and
// claude's adapter listing become one row per real file. Owner Q2 B: within
// one scope (用户 / 项目) rows merge by realpath, so one real file listed by
// both harnesses is one row carrying both labels; the same display name on
// different files stays two rows, each naming the other (遮蔽注明). pi's own
// global and settings-declared ("path") skills are the user's configuration,
// so they group under 用户; claude's user skills are `<config dir>/skills`.
// Pure: no I/O beyond realpath resolution, no state, never a spawn.
import { realpathSync } from "node:fs";
import type { SkillInfo, UnifiedSkillRow } from "./api-types";
import type { SkillRef } from "./harness/concepts";

/** A listing's real-path key: realpath when it resolves, else the path as given. */
function realKey(filePath: string): string {
  try {
    return realpathSync(filePath);
  } catch {
    return filePath;
  }
}

/** The scope group of a pi loader skill: project resources alone are 项目 (its global and settings-declared skills are the user's own). */
function piScope(skill: SkillInfo): "user" | "project" {
  const source = skill.sourceInfo?.source;
  const scope = skill.sourceInfo?.scope;
  return source === "project" || scope === "project" ? "project" : "user";
}

/** The scope group of a claude entry (the adapter reports its own layer). */
function claudeScope(entry: SkillRef): "user" | "project" {
  return entry.source === "project" ? "project" : "user";
}

export interface ClaudeSkillsListing {
  entries: SkillRef[];
  /** claude's native `skillOverrides` (settings layers): name → state word. */
  overrides?: Record<string, string>;
}

/**
 * Merges the two listings into rows, one per real file per scope (Q2 B). The
 * caller owns the no-project filtering (a listing without a cwd keeps only the
 * user scope); this merge answers exactly what it is given.
 */
export function mergeSkillListings(
  piSkills: SkillInfo[],
  claude: ClaudeSkillsListing,
): UnifiedSkillRow[] {
  const rows = new Map<string, UnifiedSkillRow>();
  const get = (key: string, scope: "user" | "project"): UnifiedSkillRow => {
    let row = rows.get(key);
    if (!row) {
      row = { key, scope, name: "", harnesses: [], sameName: [] };
      rows.set(key, row);
    }
    return row;
  };

  for (const skill of piSkills) {
    const scope = piScope(skill);
    const row = get(realKey(skill.filePath), scope);
    row.pi = {
      name: skill.name,
      filePath: skill.filePath,
      disableModelInvocation: skill.disableModelInvocation,
      ...(skill.declaredDisableModelInvocation !== undefined
        ? { declaredDisableModelInvocation: skill.declaredDisableModelInvocation }
        : {}),
      sourceInfo: skill.sourceInfo ?? {},
      ...(skill.install ? { install: skill.install } : {}),
    };
  }
  for (const entry of claude.entries) {
    const scope = claudeScope(entry);
    const row = get(realKey(entry.path), scope);
    row.claude = {
      name: entry.name,
      filePath: entry.path,
      ...(claude.overrides && typeof claude.overrides[entry.name] === "string"
        ? { state: claude.overrides[entry.name] }
        : {}),
    };
  }

  // Display name and labels: pi's loader name first (the loader parses the
  // frontmatter), else claude's entry name; harnesses stay pi-first.
  for (const row of rows.values()) {
    row.name = row.pi?.name ?? row.claude!.name;
    if (row.pi) row.harnesses.push("pi");
    if (row.claude) row.harnesses.push("claude");
  }

  // The shadow note (Q2 B): same scope, same display name, different files.
  const byName = new Map<string, string[]>();
  for (const row of rows.values()) {
    const nameKey = `${row.scope}\u0000${row.name}`;
    byName.set(nameKey, [...(byName.get(nameKey) ?? []), row.key]);
  }
  for (const row of rows.values()) {
    const peers = (byName.get(`${row.scope}\u0000${row.name}`) ?? []).filter((key) => key !== row.key);
    row.sameName = peers;
  }

  return [...rows.values()].sort((a, b) =>
    a.scope === b.scope ? a.key.localeCompare(b.key) : a.scope === "user" ? -1 : 1,
  );
}
