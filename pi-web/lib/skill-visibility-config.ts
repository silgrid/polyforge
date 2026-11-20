// pi-web's per-harness skill visibility preference: `<pi agent dir>/pi-web-settings.json`,
// `{ "version": 1, "skillVisibility": { "pi": { "<loader-reported skill file path>":
// "hidden" | "shown" } } }`. Never pi's settings.json, never a SKILL.md.
// The skills routes under `app/api/skills/**` are skillVisibility's one writer
// (PATCH toggles, the legacy import on listing, the confirmed cleanup); sessions
// only read it. The `pi` key reserves the harness dimension (polyforge#85d).
// The switch keeps pi's semantics — hidden from the model prompt, still
// manually invocable — by rewriting `disableModelInvocation` in memory through
// `DefaultResourceLoader.skillsOverride`; running pi in a terminal ignores it.
import { mkdirSync, readFileSync } from "fs";
import { dirname } from "path";
import { writePrivateFileAtomicSync } from "./atomic-file";
import {
  agentLanguageConfigPath as piWebSettingsPath,
  PI_WEB_SETTINGS_WRITE_CHAIN,
} from "./agent-language-config";
import { defaultAgentDir } from "./http-dispatcher";
import { serializeByKey } from "./key-serializer";
import type { ResourceDiagnostic, Skill } from "@earendil-works/pi-coding-agent";

/** A visibility preference: `hidden` (out of the prompt, still invocable) or `shown`. */
export type SkillVisibility = "hidden" | "shown";

/** One entry of a batch write; `visibility: null` removes the entry. */
export interface SkillVisibilityChange {
  filePath: string;
  visibility: SkillVisibility | null;
  /**
   * Write only when no preference is stored for the path yet. The check runs
   * inside the file's write chain, so a preference a concurrent request
   * stored between the caller's read and this write is never overwritten
   * (r1#1, r1#5).
   */
  onlyIfUnset?: boolean;
}

/** The shape `DefaultResourceLoader` accepts as `skillsOverride`. */
export type SkillsOverride = (base: {
  skills: Skill[];
  diagnostics: ResourceDiagnostic[];
}) => {
  skills: Skill[];
  diagnostics: ResourceDiagnostic[];
};

function plainObject(value: unknown): Record<string, unknown> | undefined {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return undefined;
  return value as Record<string, unknown>;
}

/** The stored preferences; a missing, malformed or unknown value reads as none (never throws). */
export function readSkillVisibility(agentDir?: string): Map<string, SkillVisibility> {
  try {
    const parsed: unknown = JSON.parse(readFileSync(piWebSettingsPath(agentDir), "utf8"));
    const pi = plainObject(plainObject(parsed)?.skillVisibility)?.pi;
    const entries = Object.entries(plainObject(pi) ?? {});
    const result = new Map<string, SkillVisibility>();
    for (const [filePath, value] of entries) {
      // A key without any path separator cannot name a skill file; a value
      // other than the two preferences is history this build never wrote.
      if ((filePath.includes("/") || filePath.includes("\\")) && (value === "hidden" || value === "shown")) {
        result.set(filePath, value);
      }
    }
    return result;
  } catch {
    return new Map();
  }
}

/**
 * Stores every entry of `changes` in one atomic write, keeping the file's other
 * keys (and other harness dimensions under `skillVisibility`); one write at a
 * time per file, replaced atomically with mode 0600. A file that exists but is
 * not a JSON object is refused (thrown), never overwritten.
 */
export function writeSkillVisibility(
  changes: SkillVisibilityChange[],
  agentDir?: string,
): Promise<void> {
  if (changes.length === 0) return Promise.resolve();
  const path = piWebSettingsPath(agentDir);
  return serializeByKey(PI_WEB_SETTINGS_WRITE_CHAIN, path, async () => {
    let current: Record<string, unknown> = {};
    let text: string | undefined;
    try {
      text = readFileSync(path, "utf8");
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    }
    if (text !== undefined) {
      let parsed: unknown;
      try {
        parsed = JSON.parse(text);
      } catch {
        throw new Error(`${path} is not valid JSON; refusing to overwrite it`);
      }
      const object = plainObject(parsed);
      if (object === undefined) {
        throw new Error(`${path} is not a JSON object; refusing to overwrite it`);
      }
      current = object;
    }
    const skillVisibility = { ...plainObject(current.skillVisibility) };
    const pi = { ...plainObject(skillVisibility.pi) };
    for (const { filePath, visibility, onlyIfUnset } of changes) {
      if (typeof filePath !== "string" || !filePath) throw new Error("filePath must be a non-empty string");
      if (visibility === null) delete pi[filePath];
      else if (visibility === "hidden" || visibility === "shown") {
        if (onlyIfUnset && Object.prototype.hasOwnProperty.call(pi, filePath)) continue;
        pi[filePath] = visibility;
      }
      else throw new Error(`visibility must be "hidden", "shown" or null`);
    }
    if (Object.keys(pi).length === 0) delete skillVisibility.pi;
    else skillVisibility.pi = pi;
    const next: Record<string, unknown> = { ...current, version: 1, skillVisibility };
    if (Object.keys(skillVisibility).length === 0) delete next.skillVisibility;
    const nextText = `${JSON.stringify(next, null, 2)}\n`;
    // An unchanged store keeps its file and its mtime: an onlyIfUnset write
    // that found every entry already set writes nothing at all.
    if (nextText === text) return;
    mkdirSync(dirname(path), { recursive: true });
    writePrivateFileAtomicSync(path, nextText);
  });
}

/**
 * The tri-state rule: `hidden` hides the skill, `shown` shows it even over the
 * author's frontmatter, an unset entry keeps the author's declared value.
 */
export function effectiveDisableModelInvocation(
  declared: boolean,
  visibility: ReadonlyMap<string, SkillVisibility>,
  filePath: string,
): boolean {
  const preference = visibility.get(filePath);
  if (preference === "hidden") return true;
  if (preference === "shown") return false;
  return declared;
}

/** A `skillsOverride` that applies the preferences, or undefined when none are stored. */
export function skillVisibilityOverride(
  visibility: ReadonlyMap<string, SkillVisibility>,
): SkillsOverride | undefined {
  if (visibility.size === 0) return undefined;
  return (base) => ({
    diagnostics: base.diagnostics,
    skills: base.skills.map((skill) =>
      visibility.has(skill.filePath)
        ? {
            ...skill,
            disableModelInvocation: effectiveDisableModelInvocation(
              skill.disableModelInvocation,
              visibility,
              skill.filePath,
            ),
          }
        : skill,
    ),
  });
}

/** Applies `first`, then `second` on its result; undefined when both are. */
export function composeSkillsOverrides(
  first?: SkillsOverride,
  second?: SkillsOverride,
): SkillsOverride | undefined {
  if (first === undefined) return second;
  if (second === undefined) return first;
  return (base) => second(first(base));
}
