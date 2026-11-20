// The claude adapter's native writes (polyforge#150 85a4): the user-level
// `$CLAUDE_CONFIG_DIR/settings.json` — the only layer a write may touch (Q3 B:
// project / shared layers need the user's own explicit choice, managed and
// command-line sources display read-only) — and claude's state file for a
// project trust decision. Every write reads, merges its patch (unknown keys
// stay), replaces atomically with mode 0600, then reads back and verifies the
// merged record landed; anything else is refused. The version gate is the
// adapter's (writeGate in adapter.ts): this module never writes on a version
// the probe refused, and a file that exists but is not a JSON object is
// refused, never overwritten.
import { existsSync } from "node:fs";
import { join } from "node:path";
import { writePrivateFileAtomicSync } from "../atomic-file";
import { serializeByKey } from "../key-serializer";
import { isClaudeEffortLevel, isClaudeModelName } from "../claude-shared";
import { isClaudeApprovalMode, type PermissionRules } from "./concepts";
import { readJsonRecord } from "./redact";

const USER_SETTINGS_WRITE_CHAIN = Symbol.for("pi-web.claudeUserSettingsWrites");
const STATE_FILE_WRITE_CHAIN = Symbol.for("pi-web.claudeStateFileWrites");

/** The user-level settings.json path for a config dir. */
export function claudeUserSettingsPath(configDir: string): string {
  return join(configDir, "settings.json");
}

/** Whether `value` is a plain record. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Deep equality of two JSON-round-tripped values. */
function jsonEqual(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/** A validated permission change for the claude adapter's write(). */
export type ClaudePermissionChange =
  | { kind: "approvalMode"; mode: string }
  | { kind: "denyInsteadOfPrompting"; value: boolean }
  | { kind: "sandboxEnabled"; value: boolean }
  | { kind: "autoAllowBashIfSandboxed"; value: boolean }
  | { kind: "rules"; rules: Partial<PermissionRules> }
  | { kind: "projectTrust"; trusted: boolean; cwd: string };

/**
 * Parses the opaque write change into one validated change; null with a reason
 * when the change is not one the adapter knows (an unknown key, an invalid
 * value, a trust write without a project).
 */
export function parseClaudePermissionChange(
  change: Record<string, unknown>,
  cwd: string | undefined,
): { change: ClaudePermissionChange } | { reason: string } {
  const keys = Object.keys(change);
  if (keys.length === 0) return { reason: "the change is empty" };
  if (keys.length > 1) {
    return { reason: `one field per write; got ${keys.join(", ")}` };
  }
  const key = keys[0];
  if (key === "approvalMode") {
    if (!isClaudeApprovalMode(change.approvalMode)) {
      return { reason: `unknown claude permission mode: ${String(change.approvalMode)}` };
    }
    return { change: { kind: "approvalMode", mode: change.approvalMode } };
  }
  if (key === "denyInsteadOfPrompting" || key === "sandboxEnabled" || key === "autoAllowBashIfSandboxed") {
    if (typeof change[key] !== "boolean") {
      return { reason: `${key} must be a boolean` };
    }
    return { change: { kind: key, value: change[key] as boolean } };
  }
  if (key === "rules") {
    const raw = change.rules;
    if (!isRecord(raw)) return { reason: "rules must be { allow?, ask?, deny? }" };
    const rules: Partial<PermissionRules> = {};
    for (const list of ["allow", "ask", "deny"] as const) {
      if (list in raw) {
        const value = raw[list];
        if (!Array.isArray(value) || !value.every((entry) => typeof entry === "string")) {
          return { reason: `rules.${list} must be an array of strings` };
        }
        rules[list] = [...(value as string[])];
      }
    }
    return { change: { kind: "rules", rules } };
  }
  if (key === "projectTrust") {
    const raw = change.projectTrust;
    if (!isRecord(raw) || typeof raw.trusted !== "boolean") {
      return { reason: "projectTrust must be { trusted: boolean }" };
    }
    if (!cwd) return { reason: "a project trust write needs the project folder (cwd)" };
    return { change: { kind: "projectTrust", trusted: raw.trusted, cwd } };
  }
  return { reason: `unknown permission change: ${key}` };
}

/** A merge's refusal: the reason the write does not happen, nothing written. */
export type ClaudeSettingsMergeRefusal = { refuse: string };

function isMergeRefusal(value: unknown): value is ClaudeSettingsMergeRefusal {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    && typeof (value as Record<string, unknown>).refuse === "string"
    && Object.keys(value).length === 1;
}

/**
 * One serialized read-merge-write-verify on the user-level settings.json.
 * `merge` receives the current record and answers the next one — or a
 * `{ refuse }` refusal, which writes nothing; every key it does not name
 * stays as it was. A file that exists but is not a JSON object is refused,
 * never overwritten; a write whose read-back check fails is reported as an
 * error, never as written.
 */
export async function patchClaudeUserSettings(
  configDir: string,
  merge: (record: Record<string, unknown>) => Record<string, unknown> | ClaudeSettingsMergeRefusal,
): Promise<{ ok: true } | { ok: false; reason: string }> {
  const path = claudeUserSettingsPath(configDir);
  return serializeByKey(USER_SETTINGS_WRITE_CHAIN, path, async () => {
    let current: Record<string, unknown> = {};
    if (existsSync(path)) {
      const record = readJsonRecord(path);
      if (record === null) {
        return { ok: false, reason: "settings.json has an unrecognized top-level structure (expected a JSON object)" };
      }
      current = record;
    }
    const next = merge(current);
    if (isMergeRefusal(next)) return { ok: false, reason: next.refuse };
    writePrivateFileAtomicSync(path, `${JSON.stringify(next, null, 2)}\n`);
    const back = readJsonRecord(path);
    if (back === null || !jsonEqual(back, next)) {
      return { ok: false, reason: "the written settings.json did not read back as written; refusing to report success" };
    }
    return { ok: true };
  });
}

/** Sets `subKeys` under `topKey` in the permissions object, deleting an empty list's key. */
function mergePermissionsList(
  permissions: Record<string, unknown>,
  lists: Partial<PermissionRules>,
): Record<string, unknown> {
  const next: Record<string, unknown> = { ...permissions };
  for (const list of ["allow", "ask", "deny"] as const) {
    const value = lists[list];
    if (value === undefined) continue;
    if (value.length === 0) delete next[list];
    else next[list] = [...value];
  }
  return next;
}

/**
 * Applies one validated permission change. The caller has already passed the
 * version gate; this never touches a layer other than the user settings.json
 * (Q3 B) or, for a trust decision, the state file's projects entry.
 */
export async function applyClaudePermissionChange(
  change: ClaudePermissionChange,
  configDir: string,
  statePath: string,
): Promise<{ ok: true; summary: string } | { ok: false; reason: string }> {
  switch (change.kind) {
    case "approvalMode": {
      const mode = change.mode;
      const result = await patchClaudeUserSettings(configDir, (record) => ({
        ...record,
        permissions: { ...(isRecord(record.permissions) ? record.permissions : {}), defaultMode: mode },
      }));
      return result.ok ? { ok: true, summary: `permissions.defaultMode = ${mode}` } : result;
    }
    case "denyInsteadOfPrompting":
    case "sandboxEnabled":
    case "autoAllowBashIfSandboxed": {
      const value = change.value;
      const result = change.kind === "sandboxEnabled" || change.kind === "autoAllowBashIfSandboxed"
        ? await patchClaudeUserSettings(configDir, (record) => ({
          ...record,
          sandbox: { ...(isRecord(record.sandbox) ? record.sandbox : {}), [change.kind === "sandboxEnabled" ? "enabled" : "autoAllowBashIfSandboxed"]: value },
        }))
        : await patchClaudeUserSettings(configDir, (record) => ({
          ...record,
          permissions: { ...(isRecord(record.permissions) ? record.permissions : {}), denyInsteadOfPrompting: value },
        }));
      return result.ok ? { ok: true, summary: `${change.kind} = ${String(value)}` } : result;
    }
    case "rules": {
      const lists = change.rules;
      const result = await patchClaudeUserSettings(configDir, (record) => ({
        ...record,
        permissions: mergePermissionsList(isRecord(record.permissions) ? record.permissions : {}, lists),
      }));
      return result.ok ? { ok: true, summary: `rules: ${(["allow", "ask", "deny"] as const).filter((list) => lists[list] !== undefined).join(", ")}` } : result;
    }
    case "projectTrust": {
      const cwd = change.cwd;
      const trusted = change.trusted;
      const outcome = await serializeByKey(STATE_FILE_WRITE_CHAIN, statePath, async () => {
        let current: Record<string, unknown> = {};
        if (existsSync(statePath)) {
          const record = readJsonRecord(statePath);
          if (record === null) {
            return { ok: false as const, reason: "claude's state file has an unrecognized top-level structure (expected a JSON object)" };
          }
          current = record;
        }
        const projects = isRecord(current.projects) ? { ...current.projects } : {};
        const entry = isRecord(projects[cwd]) ? { ...projects[cwd] } : {};
        entry.hasTrustDialogAccepted = trusted;
        projects[cwd] = entry;
        const next = { ...current, projects };
        writePrivateFileAtomicSync(statePath, `${JSON.stringify(next, null, 2)}\n`);
        const back = readJsonRecord(statePath);
        if (back === null || !jsonEqual(back, next)) {
          return { ok: false as const, reason: "the written state file did not read back as written; refusing to report success" };
        }
        return { ok: true as const };
      });
      return outcome.ok ? { ok: true, summary: `projects[${cwd}].hasTrustDialogAccepted = ${String(trusted)}` } : outcome;
    }
  }
}

// ── the model write (85a3) ────────────────────────────────────────────────

/**
 * The model keys' registered minimum versions (owner ruling Q4: the ticket
 * registers the model keys' minimums; the gate itself recognizes the major):
 * 2.1.312, the first recorded version, is where each key is evidenced.
 */
export const CLAUDE_MODEL_KEY_MIN_VERSIONS: Readonly<Record<string, string>> = {
  model: "2.1.312",
  effortLevel: "2.1.312",
  modelSettings: "2.1.312",
};

/** A validated model change for the claude adapter's write(). One field per write. */
export type ClaudeModelChange =
  | { kind: "model"; model: string | null }
  | { kind: "effortLevel"; level: string | null }
  | { kind: "modelEffort"; model: string; effort: string | null };

/** The native keys one change kind touches, for the key-floor check. */
export function claudeModelChangeKeys(change: ClaudeModelChange): readonly string[] {
  switch (change.kind) {
    case "model":
      return ["model"];
    case "effortLevel":
      return ["effortLevel"];
    case "modelEffort":
      return ["modelSettings"];
  }
}

/** A claude effort spelling the settings layer may hold: the five levels
 * plus claude's own ultracode (85a3's mapping lists it apart); null removes. */
function isClaudeEffortSettingValue(value: unknown): boolean {
  return isClaudeEffortLevel(value) || value === "ultracode";
}

/**
 * Parses the opaque write change into one validated model change; a reason
 * when the change is not one the adapter knows (an unknown key, an invalid
 * value). `null` removes the key; the model must be a name `--model` takes.
 */
export function parseClaudeModelChange(
  change: Record<string, unknown>,
): { change: ClaudeModelChange } | { reason: string } {
  const keys = Object.keys(change);
  if (keys.length === 0) return { reason: "the change is empty" };
  if (keys.length > 1) {
    return { reason: `one field per write; got ${keys.join(", ")}` };
  }
  const key = keys[0];
  if (key === "model") {
    const raw = change.model;
    if (raw !== null && !isClaudeModelName(raw)) {
      return { reason: `not a claude model name: ${String(raw)}` };
    }
    return { change: { kind: "model", model: raw as string | null } };
  }
  if (key === "effortLevel") {
    const raw = change.effortLevel;
    if (raw !== null && !isClaudeEffortSettingValue(raw)) {
      return { reason: `unknown effort level: ${String(raw)}` };
    }
    return { change: { kind: "effortLevel", level: raw as string | null } };
  }
  if (key === "modelEffort") {
    const raw = change.modelEffort;
    if (!isRecord(raw) || !isClaudeModelName(raw.model)) {
      return { reason: "modelEffort must be { model: <name>, effort: <level|null> }" };
    }
    if (raw.effort !== null && raw.effort !== undefined && !isClaudeEffortSettingValue(raw.effort)) {
      return { reason: `unknown effort level: ${String(raw.effort)}` };
    }
    return { change: { kind: "modelEffort", model: raw.model, effort: (raw.effort ?? null) as string | null } };
  }
  return { reason: `unknown model change: ${key}` };
}

/** The user-level settings.json's modelSettings shape guard (85a3). */
function requireModelSettingsShape(record: Record<string, unknown>): string | null {
  const value = record.modelSettings;
  if (value === undefined) return null;
  if (!isRecord(value)) return "modelSettings has an unrecognized shape (expected an object of model entries)";
  for (const entry of Object.values(value)) {
    if (!isRecord(entry)) return "modelSettings holds an entry that is not an object";
  }
  return null;
}

/**
 * Applies one validated model change to the user-level settings.json. The
 * caller has already passed the version gate and the key floors; a merge
 * that finds an unrecognized shape refuses, writing nothing. An entry whose
 * effort is removed keeps its other keys, and modelSettings itself is
 * dropped when it holds nothing.
 */
export async function applyClaudeModelChange(
  change: ClaudeModelChange,
  configDir: string,
): Promise<{ ok: true; summary: string } | { ok: false; reason: string }> {
  switch (change.kind) {
    case "model": {
      const model = change.model;
      const result = await patchClaudeUserSettings(configDir, (record) => {
        const next: Record<string, unknown> = { ...record };
        if (model === null) delete next.model;
        else next.model = model;
        return next;
      });
      return result.ok
        ? { ok: true, summary: model === null ? "model removed" : `model = ${model}` }
        : result;
    }
    case "effortLevel": {
      const level = change.level;
      const result = await patchClaudeUserSettings(configDir, (record) => {
        const next: Record<string, unknown> = { ...record };
        if (level === null) delete next.effortLevel;
        else next.effortLevel = level;
        return next;
      });
      return result.ok
        ? { ok: true, summary: level === null ? "effortLevel removed" : `effortLevel = ${level}` }
        : result;
    }
    case "modelEffort": {
      const model = change.model;
      const effort = change.effort;
      const result = await patchClaudeUserSettings(configDir, (record) => {
        const shape = requireModelSettingsShape(record);
        if (shape !== null) return { refuse: shape };
        const settings = isRecord(record.modelSettings) ? { ...record.modelSettings } : {};
        if (model in settings && !isRecord(settings[model])) {
          return { refuse: `modelSettings[${model}] has an unrecognized shape (expected an object)` };
        }
        const entry = isRecord(settings[model]) ? { ...settings[model] } : {};
        if (effort === null) delete entry.effort;
        else entry.effort = effort;
        if (Object.keys(entry).length === 0) delete settings[model];
        else settings[model] = entry;
        const next: Record<string, unknown> = { ...record };
        if (Object.keys(settings).length === 0) delete next.modelSettings;
        else next.modelSettings = settings;
        return next;
      });
      return result.ok
        ? { ok: true, summary: effort === null ? `modelSettings[${model}].effort removed` : `modelSettings[${model}].effort = ${effort}` }
        : result;
    }
  }
}
