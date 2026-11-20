// The pi adapter's native writes (#85 85a3-1): the global `settings.json` in
// the agent dir — the one file pi's defaults live in. Every write takes the
// lock pi's own SettingsManager takes on the same path (lib/global-settings-file's
// proper-lockfile call, mirrored here so neither writer overwrites the other),
// reads, merges its patch — every key the merge does not name stays — and only
// then writes; a merge that answers a refusal writes nothing, so a file with
// an unrecognized shape is never touched. The version gate is the adapter's
// (writeGate in adapter.ts); the model keys' registered minimum versions
// (PI_MODEL_KEY_MIN_VERSIONS) floor what the gate's same-major rule lets
// through.
import { chmodSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import lockfile from "proper-lockfile";

/** The pi settings.json path for an agent dir. */
export function piUserSettingsPath(agentDir: string): string {
  return join(agentDir, "settings.json");
}

/** Whether `value` is a plain record. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** A merge's refusal: the reason the write does not happen, nothing written. */
export type PiSettingsMergeRefusal = { refuse: string };

export function isPiSettingsRefusal(value: unknown): value is PiSettingsMergeRefusal {
  return isRecord(value) && typeof value.refuse === "string" && Object.keys(value).length === 1;
}

/**
 * One locked read-merge-write on the global settings.json. `merge` receives
 * the current record (an absent file reads as `{}`) and answers the next one,
 * or a `{ refuse }` — a refusal writes nothing, so the file is left exactly
 * as it was. The write keeps the file's own formatting (2-space JSON) and
 * mode 0600, matching lib/global-settings-file's writer.
 */
export async function patchPiUserSettings(
  agentDir: string,
  merge: (record: Record<string, unknown>) => Record<string, unknown> | PiSettingsMergeRefusal,
): Promise<{ ok: true } | { ok: false; reason: string }> {
  const path = piUserSettingsPath(agentDir);
  mkdirSync(dirname(path), { recursive: true });
  try {
    writeFileSync(path, "{}", { flag: "wx", mode: 0o600 });
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
  }
  const release = await lockfile.lock(path, { realpath: false, retries: 10 });
  try {
    let current: Record<string, unknown> = {};
    if (existsSync(path)) {
      let parsed: unknown;
      try {
        parsed = JSON.parse(readFileSync(path, "utf8"));
      } catch {
        return { ok: false, reason: "settings.json is not valid JSON — refusing to overwrite it" };
      }
      if (!isRecord(parsed)) {
        return { ok: false, reason: "settings.json has an unrecognized top-level structure (expected a JSON object)" };
      }
      current = parsed;
    }
    const next = merge(current);
    if (isPiSettingsRefusal(next)) return { ok: false, reason: next.refuse };
    writeFileSync(path, JSON.stringify(next, null, 2), "utf8");
    chmodSync(path, 0o600);
    return { ok: true };
  } finally {
    await release();
  }
}

/** pi's thinking levels, the settings schema's own vocabulary. */
export const PI_MODEL_THINKING_LEVELS = [
  "off",
  "minimal",
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
] as const;
export type PiModelThinkingLevel = (typeof PI_MODEL_THINKING_LEVELS)[number];

export function isPiModelThinkingLevel(value: unknown): value is PiModelThinkingLevel {
  return typeof value === "string" && (PI_MODEL_THINKING_LEVELS as readonly string[]).includes(value);
}

/**
 * The model keys' registered minimum versions (owner ruling Q4: the ticket
 * registers the model keys' minimums; the gate itself recognizes the major):
 * the installed SDK's settings-schema proves each key at 1.5.0.
 */
export const PI_MODEL_KEY_MIN_VERSIONS: Readonly<Record<string, string>> = {
  defaultProvider: "1.5.0",
  defaultModel: "1.5.0",
  defaultThinkingLevel: "1.5.0",
  modelThinkingLevels: "1.5.0",
};

/** A validated model change for the pi adapter's write(). One field per write. */
export type PiModelChange =
  | { kind: "model"; provider: string; modelId: string }
  | { kind: "thinkingLevel"; level: PiModelThinkingLevel | null }
  | { kind: "modelThinkingLevel"; key: string; level: PiModelThinkingLevel | null };

/** The native keys one change kind touches, for the key-floor check. */
export function piModelChangeKeys(change: PiModelChange): readonly string[] {
  switch (change.kind) {
    case "model":
      return ["defaultProvider", "defaultModel"];
    case "thinkingLevel":
      return ["defaultThinkingLevel"];
    case "modelThinkingLevel":
      return ["modelThinkingLevels"];
  }
}

/**
 * Parses the opaque write change into one validated change; a reason when
 * the change is not one the adapter knows (an unknown key, an invalid value).
 */
export function parsePiModelChange(
  change: Record<string, unknown>,
): { change: PiModelChange } | { reason: string } {
  const keys = Object.keys(change);
  if (keys.length === 0) return { reason: "the change is empty" };
  if (keys.length > 1) return { reason: `one field per write; got ${keys.join(", ")}` };
  const key = keys[0];
  if (key === "model") {
    const raw = change.model;
    if (!isRecord(raw) || typeof raw.provider !== "string" || typeof raw.modelId !== "string" || !raw.provider || !raw.modelId) {
      return { reason: "model must be { provider: string, modelId: string }" };
    }
    return { change: { kind: "model", provider: raw.provider, modelId: raw.modelId } };
  }
  if (key === "thinkingLevel") {
    const raw = change.thinkingLevel;
    if (raw !== null && !isPiModelThinkingLevel(raw)) {
      return { reason: `unknown thinking level: ${String(raw)}` };
    }
    return { change: { kind: "thinkingLevel", level: raw } };
  }
  if (key === "modelThinkingLevel") {
    const raw = change.modelThinkingLevel;
    if (!isRecord(raw) || typeof raw.key !== "string" || !raw.key || !raw.key.includes("/")) {
      return { reason: "modelThinkingLevel must be { key: \"provider/modelId\", level }" };
    }
    if (raw.level !== null && !isPiModelThinkingLevel(raw.level)) {
      return { reason: `unknown thinking level: ${String(raw.level)}` };
    }
    return { change: { kind: "modelThinkingLevel", key: raw.key, level: raw.level as PiModelThinkingLevel | null } };
  }
  return { reason: `unknown model change: ${key}` };
}

/** A string-or-absent shape guard: an existing value of another type refuses. */
function requireStringOrAbsent(record: Record<string, unknown>, key: string): string | null {
  const value = record[key];
  if (value === undefined) return null;
  if (typeof value !== "string") return `${key} is set but is not a string`;
  return null;
}

/** The per-model map's shape guard: a record of level strings, or absent. */
function requireLevelMapOrAbsent(record: Record<string, unknown>, key: string): string | null {
  const value = record[key];
  if (value === undefined) return null;
  if (!isRecord(value)) return `${key} has an unrecognized shape (expected an object keyed by model)`;
  for (const level of Object.values(value)) {
    if (!isPiModelThinkingLevel(level)) return `${key} holds a value that is not a thinking level`;
  }
  return null;
}

/**
 * Applies one validated model change to the global settings.json. The caller
 * has already passed the version gate and the key floors; a merge that finds
 * an unrecognized shape refuses, writing nothing.
 */
export async function applyPiModelChange(
  change: PiModelChange,
  agentDir: string,
): Promise<{ ok: true; summary: string } | { ok: false; reason: string }> {
  switch (change.kind) {
    case "model": {
      const provider = change.provider;
      const modelId = change.modelId;
      const result = await patchPiUserSettings(agentDir, (record) => {
        const shape = requireStringOrAbsent(record, "defaultProvider") ?? requireStringOrAbsent(record, "defaultModel");
        if (shape) return { refuse: `settings.json ${shape}` };
        return { ...record, defaultProvider: provider, defaultModel: modelId };
      });
      return result.ok ? { ok: true, summary: `defaultProvider = ${provider}, defaultModel = ${modelId}` } : result;
    }
    case "thinkingLevel": {
      const level = change.level;
      const result = await patchPiUserSettings(agentDir, (record) => {
        const shape = requireStringOrAbsent(record, "defaultThinkingLevel");
        if (shape) return { refuse: `settings.json ${shape}` };
        if (level === null) {
          const next: Record<string, unknown> = { ...record };
          delete next.defaultThinkingLevel;
          return next;
        }
        return { ...record, defaultThinkingLevel: level };
      });
      return result.ok
        ? { ok: true, summary: level === null ? "defaultThinkingLevel removed" : `defaultThinkingLevel = ${level}` }
        : result;
    }
    case "modelThinkingLevel": {
      const modelKey = change.key;
      const level = change.level;
      const result = await patchPiUserSettings(agentDir, (record) => {
        const shape = requireLevelMapOrAbsent(record, "modelThinkingLevels");
        if (shape) return { refuse: `settings.json ${shape}` };
        const levels = isRecord(record.modelThinkingLevels) ? { ...record.modelThinkingLevels } : {};
        const next: Record<string, unknown> = { ...record };
        if (level === null) {
          delete levels[modelKey];
          if (Object.keys(levels).length === 0) delete next.modelThinkingLevels;
          else next.modelThinkingLevels = levels;
        } else {
          levels[modelKey] = level;
          next.modelThinkingLevels = levels;
        }
        return next;
      });
      return result.ok
        ? { ok: true, summary: level === null ? `modelThinkingLevels[${modelKey}] removed` : `modelThinkingLevels[${modelKey}] = ${level}` }
        : result;
    }
  }
}
