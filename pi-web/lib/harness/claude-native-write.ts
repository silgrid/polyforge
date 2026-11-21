// The claude adapter's native writes (polyforge#150 85a4): the user-level
// `$CLAUDE_CONFIG_DIR/settings.json` — the only layer a permission write may
// touch (Q3 B: project / shared layers need the user's own explicit choice,
// managed and command-line sources display read-only) — and claude's state file
// for a project trust decision. 85d (polyforge#148) adds the skills writes:
// a user skill's state in the user-level settings.json and a project skill's
// state in that project's `.claude/settings.local.json` (owner Q3 B). Every
// write reads, merges its patch (unknown keys stay), replaces atomically with
// mode 0600, then reads back and verifies the merged record landed; anything
// else is refused. The version gate is the adapter's (writeGate in adapter.ts):
// this module never writes on a version the probe refused, and a file that
// exists but is not a JSON object is refused, never overwritten.
import { existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { writePrivateFileAtomicSync } from "../atomic-file";
import { serializeByKey } from "../key-serializer";
import {
  CLAUDE_CUSTOM_MODELS_MAX,
  CLAUDE_SKILL_OVERRIDES_MIN_VERSION,
  compareClaudeVersions,
  isClaudeSkillOverrideState,
  type ClaudeSkillOverrideState,
  isClaudeEffortLevel,
  isClaudeModelName,
} from "../claude-shared";
import { isClaudeApprovalMode, type PermissionRules } from "./concepts";
import { readJsonRecord } from "./redact";

const USER_SETTINGS_WRITE_CHAIN = Symbol.for("pi-web.claudeUserSettingsWrites");
const STATE_FILE_WRITE_CHAIN = Symbol.for("pi-web.claudeStateFileWrites");
const PROJECT_LOCAL_SETTINGS_WRITE_CHAIN = Symbol.for("pi-web.claudeProjectLocalSettingsWrites");

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

/** A validated skills change for the claude adapter's write() (85d): one
 * skill's native `skillOverrides` state on one declared layer. */
export type ClaudeSkillsChange =
  | { kind: "skillOverride"; name: string; state: ClaudeSkillOverrideState; scope: "user" | "project"; cwd?: string };

/**
 * Parses the opaque skills change (85d): exactly { name, state, scope }, one
 * skill per write. `scope: "user"` goes to `$CLAUDE_CONFIG_DIR/settings.json`;
 * `scope: "project"` additionally needs the project folder (cwd) and goes to
 * that project's `.claude/settings.local.json` (owner Q3 B) — the personal,
 * not-committed layer; the shared SKILL.md is never a target. Malformed shapes
 * answer with a reason, as parseClaudePermissionChange does.
 */
export function parseClaudeSkillsChange(
  change: Record<string, unknown>,
  cwd: string | undefined,
): { change: ClaudeSkillsChange } | { reason: string } {
  const keys = Object.keys(change);
  for (const key of ["name", "state", "scope"]) {
    if (!keys.includes(key)) return { reason: `the skills change needs ${key}` };
  }
  if (keys.length > 3) return { reason: `one skill per write; got ${keys.join(", ")}` };
  if (typeof change.name !== "string" || change.name.trim() === "") {
    return { reason: "name must be a non-empty string" };
  }
  if (!isClaudeSkillOverrideState(change.state)) {
    return { reason: `unknown skillOverrides state: ${String(change.state)}` };
  }
  if (change.scope !== "user" && change.scope !== "project") {
    return { reason: `unknown skills scope: ${String(change.scope)}` };
  }
  if (change.scope === "project" && !cwd) {
    return { reason: "a project skills write needs the project folder (cwd)" };
  }
  return {
    change: {
      kind: "skillOverride",
      name: change.name,
      state: change.state,
      scope: change.scope,
      cwd,
    },
  };
}

/**
 * Whether `version` may write `skillOverrides` (85d 风险4: the floor is the
 * first binary that proves the values): the adapter's write gate owns the
 * known-range check; this is the feature floor inside it.
 */
export function skillOverridesVersionAllowed(version: string | null): { allowed: true } | { allowed: false; reason: string } {
  const comparison = compareClaudeVersions(version, CLAUDE_SKILL_OVERRIDES_MIN_VERSION);
  if (comparison === null || comparison < 0) {
    return {
      allowed: false,
      reason: `skillOverrides writes need claude >= ${CLAUDE_SKILL_OVERRIDES_MIN_VERSION} (found ${version ?? "no version"})`,
    };
  }
  return { allowed: true };
}

/**
 * Applies one validated skills change (85d): the state lands in the target
 * layer's `skillOverrides`, the file's other keys kept.
 */
export async function applyClaudeSkillsChange(
  change: Extract<ClaudeSkillsChange, { kind: "skillOverride" }>,
  configDir: string,
): Promise<{ ok: true; summary: string } | { ok: false; reason: string }> {
  const merge = (record: Record<string, unknown>): Record<string, unknown> => {
    const overrides = { ...(isRecord(record.skillOverrides) ? record.skillOverrides : {}) };
    overrides[change.name] = change.state;
    return { ...record, skillOverrides: overrides };
  };
  const outcome = change.scope === "project"
    ? await patchClaudeProjectLocalSettings(change.cwd ?? "", merge)
    : await patchClaudeUserSettings(configDir, merge);
  const target = change.scope === "project"
    ? join(change.cwd ?? "", ".claude", "settings.local.json")
    : claudeUserSettingsPath(configDir);
  return outcome.ok
    ? { ok: true, summary: `skillOverrides[${change.name}] = ${change.state} (${target})` }
    : outcome;
}

/**
 * One serialized read-merge-write-verify on a project's
 * `.claude/settings.local.json` (85d, owner Q3 B): same refusal semantics as
 * the user settings patch; the `.claude` folder is created only when the
 * write itself is accepted.
 */
export async function patchClaudeProjectLocalSettings(
  cwd: string,
  merge: (record: Record<string, unknown>) => Record<string, unknown>,
): Promise<{ ok: true } | { ok: false; reason: string }> {
  const dir = join(cwd, ".claude");
  const path = join(dir, "settings.local.json");
  return serializeByKey(PROJECT_LOCAL_SETTINGS_WRITE_CHAIN, path, async () => {
    let current: Record<string, unknown> = {};
    if (existsSync(path)) {
      const record = readJsonRecord(path);
      if (record === null) {
        return { ok: false as const, reason: "settings.local.json has an unrecognized top-level structure (expected a JSON object)" };
      }
      current = record;
    }
    const next = merge(current);
    mkdirSync(dir, { recursive: true });
    writePrivateFileAtomicSync(path, `${JSON.stringify(next, null, 2)}\n`);
    const back = readJsonRecord(path);
    if (back === null || !jsonEqual(back, next)) {
      return { ok: false as const, reason: "the written settings.local.json did not read back as written; refusing to report success" };
    }
    return { ok: true as const };
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
 * The native picker's refusal for a customModels write, null when its shape
 * is writable (85a3-3): a modelPicker that exists must be a JSON object whose
 * options, when present, is an array of rows each with a string `model` —
 * a bad shape is refused, never overwritten (owner ruling Q3: 坏形状拒写).
 * The same preflight backs the one-time migration's preview, so the merge and
 * the preview can never disagree about what is writable.
 */
export function claudeModelPickerShapeRefusal(modelPicker: unknown): string | null {
  if (modelPicker === undefined) return null;
  if (!isRecord(modelPicker)) {
    return "modelPicker has an unrecognized shape (expected a JSON object) — refusing to overwrite it";
  }
  const options = (modelPicker as { options?: unknown }).options;
  if (options === undefined) return null;
  if (!Array.isArray(options)) {
    return "modelPicker.options has an unrecognized shape (expected an array) — refusing to overwrite it";
  }
  for (const option of options) {
    if (!isRecord(option) || typeof (option as { model?: unknown }).model !== "string") {
      return "modelPicker.options holds an entry without a model string — refusing to overwrite it";
    }
  }
  return null;
}

/**
 * The model keys' registered minimum versions (owner ruling Q4: the ticket
 * registers the model keys' minimums; the gate itself recognizes the major):
 * 2.1.312 is the first recorded version that establishes the model and
 * global effort keys. `modelPicker` is claude's own native key (v2.1.242+, the
 * official settings reference; the 2.1.329 native recording proves the
 * recognition — 85a3-3). Per-model modelSettings writes remain unsupported
 * until their native behaviour is verified (85a3-3: the initialize answer
 * does not surface a saved level, so the read stays display-only).
 */
export const CLAUDE_MODEL_KEY_MIN_VERSIONS: Readonly<Record<string, string>> = {
  model: "2.1.312",
  effortLevel: "2.1.312",
  modelPicker: "2.1.242",
};

/** A validated model change for the claude adapter's write(). One field per write. */
export type ClaudeModelChange =
  | { kind: "model"; model: string | null }
  | { kind: "effortLevel"; level: string | null }
  | { kind: "customModels"; models: string[] };

/** The native keys one change kind touches, for the key-floor check. */
export function claudeModelChangeKeys(change: ClaudeModelChange): readonly string[] {
  switch (change.kind) {
    case "model":
      return ["model"];
    case "effortLevel":
      return ["effortLevel"];
    case "customModels":
      return ["modelPicker"];
  }
}

/** 已核实的 Claude 原生全局强度值；ultracode 仅供命令侧读取。 */
function isClaudeEffortSettingValue(value: unknown): boolean {
  return isClaudeEffortLevel(value);
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
  if (key === "customModels") {
    // 85a3-3: the custom names claude's own native modelPicker lists. The list
    // is the full next state: every entry a name `--model` takes, distinct, at
    // most CLAUDE_CUSTOM_MODELS_MAX; a surviving entry keeps its metadata, a
    // new name lands as { model }, a dropped name's entry goes.
    const raw = change.customModels;
    if (!Array.isArray(raw)) {
      return { reason: "customModels must be an array of model names" };
    }
    const models: string[] = [];
    for (const name of raw) {
      if (!isClaudeModelName(name)) {
        return { reason: `not a claude model name: ${String(name)}` };
      }
      if (!models.includes(name)) models.push(name);
    }
    if (models.length > CLAUDE_CUSTOM_MODELS_MAX) {
      return { reason: `customModels is capped at ${CLAUDE_CUSTOM_MODELS_MAX} entries` };
    }
    return { change: { kind: "customModels", models } };
  }
  return { reason: `unknown model change: ${key}` };
}

/**
 * Applies one validated model change to the user-level settings.json.
 * The caller has already passed the version gate and key floors.
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
    case "customModels": {
      // 85a3-3: the full next state of the picker's model names. A surviving
      // entry keeps its own metadata and position, a new name is appended as
      // { model }, a dropped name's entry goes; the picker's other fields
      // (replaceBuiltInOptions, unknown legal fields) stay untouched.
      const models = change.models;
      const result = await patchClaudeUserSettings(configDir, (record) => {
        const refusal = claudeModelPickerShapeRefusal(record.modelPicker);
        if (refusal !== null) return { refuse: refusal };
        const existing = isRecord(record.modelPicker) && Array.isArray(record.modelPicker.options)
          ? record.modelPicker.options as Record<string, unknown>[]
          : [];
        const kept = existing.filter((option) => models.includes(option.model as string));
        const appended = models.filter((name) => !existing.some((option) => option.model === name));
        return {
          ...record,
          modelPicker: {
            ...(isRecord(record.modelPicker) ? record.modelPicker : {}),
            options: [...kept, ...appended.map((model) => ({ model }))],
          },
        };
      });
      return result.ok
        ? { ok: true, summary: models.length > 0 ? `modelPicker.options = ${models.join(", ")}` : "modelPicker.options = [] (the legal empty picker)" }
        : result;
    }
  }
}
