// The paths a skills route may touch: loader-visible skill files only. The
// allowed roots also hold auth.json, settings and project files, so a `.md`
// extension check and the file-access allow-list bound every target — the
// same guard the old frontmatter edit used. Both the visibility preference
// (keyed by the loader-reported skill file path) and the confirmed legacy
// cleanup (the one remaining SKILL.md edit) go through it.
import { existsSync } from "fs";
import { homedir } from "os";
import path from "path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import { getAllowedFileRoots, isExistingFilePathAllowed } from "./file-access";

export async function getSkillEditRoots(): Promise<Set<string>> {
  const allowedRoots = new Set(await getAllowedFileRoots());
  allowedRoots.add(getAgentDir());
  // Globally installed skills live in ~/.agents/skills and are symlinked into
  // the agent's skills dir; isExistingFilePathAllowed resolves the symlink, so
  // the real target sits outside getAgentDir(). Allow the global skills root
  // too (the SDK always treats ~/.agents/skills as trusted).
  const globalSkillsDir = path.join(homedir(), ".agents", "skills");
  if (existsSync(globalSkillsDir)) allowedRoots.add(globalSkillsDir);
  return allowedRoots;
}

/** The status and message that refuse `filePath`, or null when it is an editable skill file. */
export function refuseSkillFileEdit(
  filePath: string,
  allowedRoots: Set<string>,
): { status: number; error: string } | null {
  // Skills are SKILL.md or *.md files. The allowed roots also hold settings,
  // auth.json and project files, which neither a preference key nor a cleanup
  // edit may name.
  if (path.extname(filePath).toLowerCase() !== ".md") return { status: 400, error: "Not a skill file" };
  if (!existsSync(filePath)) return { status: 404, error: "file not found" };
  if (!isExistingFilePathAllowed(filePath, allowedRoots)) {
    return { status: 403, error: "Access denied" };
  }
  return null;
}
