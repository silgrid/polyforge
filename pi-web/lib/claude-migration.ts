// The one-time migration of Pi Web's two retired self-stored claude files
// (polyforge#150 85a4, owner rulings Q4 B + Q5 A; 85a3-3 reworked the
// customModels half): pi-web-claude-settings.json and claude-permissions.json
// move field by field into what now owns them —
//
//   launchPermissionMode → claude's native permissions.defaultMode
//   includeHookEvents     → pi-web-settings.json's claude object
//   mode (ask/deny)       →   "    "        "   approvalChannel
//   push                  →   "    "        "   pushReminders
//   customModels          → claude's native modelPicker.options, through the
//                          models page's own preview-bearing phase (85a3-3,
//                          below — the startup flow never writes modelPicker)
//
// A native value that already exists wins (the native file is the single
// source of truth; the stored copy is never written over it). The startup
// flow is copy → write → re-read and verify → delete what it owns → mark the
// migration in pi-web-settings.json, so an interruption at any point replays
// safely: every write is idempotent, the old files are deleted only after the
// fields landed, and a replay with the files gone but no marker only writes
// the marker. The claude version gate (adapter.ts) applies to the native
// writes: an unknown or undetected version defers those — the old files stay
// and the next run retries.
//
// Deletion condition (the root AGENTS.md no-compat rule): this whole module
// is transitional code — remove it, its route and instrumentation calls and
// the claudeMigrated marker once every supported deployment has run it (the
// next minor version's upgrade floor, i.e. no install this version replaces
// can still hold the retired files), leaving lib/claude-preferences.ts as
// the only reader of the preference fields.
import { existsSync, unlinkSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { join } from "node:path";
import { claudeConfigDir } from "./claude-config-dir";
import { CLAUDE_CUSTOM_MODELS_MAX, CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT, isClaudeLaunchPermissionMode, isClaudeModelName, type ClaudeLaunchPermissionMode, type ClaudePermissionMode } from "./claude-shared";
import { findClaudeBinary } from "./claude-session";
import { defaultAgentDir } from "./http-dispatcher";
import { createClaudeAdapter } from "./harness/claude";
import { applyClaudePermissionChange, claudeModelPickerShapeRefusal, claudeUserSettingsPath, type ClaudePermissionChange } from "./harness/claude-native-write";
import { managedSettingsPath as defaultManagedSettingsPath } from "./harness/claude-layers";
import { readJsonRecord } from "./harness/redact";
import { serializeByKey } from "./key-serializer";
import { writePrivateFileAtomicSync } from "./atomic-file";
import { patchPiWebSettings, readPiWebSettingsRecord } from "./pi-web-settings";
import { writeClaudePreferences, type ClaudePreferences } from "./claude-preferences";

/** The preference keys the claude object can hold (its no-native-home fields). */
const PREFERENCE_KEYS = ["approvalChannel", "pushReminders", "includeHookEvents"] as const;
import type { HarnessReadContext } from "./harness/adapter";

/** The migration's outcome: it ran (with its summary), or it did not (with the reason). */
export type ClaudeMigrationResult =
  | { ran: true; summary: string }
  | { ran: false; reason: string };

/** The migration's input: which dirs to read and which claude to probe (tests override; defaults come from the environment). */
export interface ClaudeMigrationOptions {
  agentDir?: string;
  claudeConfigDir?: string;
  claudeBinary?: string;
  env?: NodeJS.ProcessEnv;
  managedSettingsPath?: string;
}

const inFlight = new Map<string, Promise<ClaudeMigrationResult>>();

/** Both old files' paths under an agent dir. */
export function retiredClaudeSelfStoredPaths(agentDir: string): { settings: string; permissions: string } {
  return {
    settings: join(agentDir, "pi-web-claude-settings.json"),
    permissions: join(agentDir, "claude-permissions.json"),
  };
}

/** Whether `value` is a plain record. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Reads an old file tolerantly: null when missing, malformed or not an object. */
function readOldRecord(path: string): Record<string, unknown> | null {
  if (!existsSync(path)) return null;
  const record = readJsonRecord(path);
  return record;
}

/** The stored launch permission mode as the old reader normalized it (plan needs the ask channel). */
function storedLaunchMode(settings: Record<string, unknown> | null, channel: ClaudePermissionMode): ClaudeLaunchPermissionMode | null {
  const mode = settings?.launchPermissionMode;
  if (!isClaudeLaunchPermissionMode(mode)) return null;
  if (mode === "plan" && channel !== "ask") return CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT;
  return mode;
}

/** The migration marker: present when the migration already completed. */
function isMigrated(agentDir: string): boolean {
  return readPiWebSettingsRecord(agentDir).claudeMigrated === true;
}

/** Marks the migration complete (the marker write is itself atomic and idempotent). */
async function markMigrated(agentDir: string): Promise<void> {
  await patchPiWebSettings((record) => ({ ...record, claudeMigrated: true }), agentDir);
}

/**
 * Runs the migration once per agent dir per process (a second call while one
 * runs awaits the same result; a completed run is decided by the marker, not
 * the memo, so another process or a test reset is honoured).
 */
export function ensureClaudeSelfStoredMigration(options: ClaudeMigrationOptions = {}): Promise<ClaudeMigrationResult> {
  const agentDir = options.agentDir ?? defaultAgentDir();
  const running = inFlight.get(agentDir);
  if (running) return running;
  const promise = runMigration(options, agentDir).finally(() => inFlight.delete(agentDir));
  inFlight.set(agentDir, promise);
  return promise;
}

/** The migration's own steps, in Q4 B's order. */
async function runMigration(options: ClaudeMigrationOptions, agentDir: string): Promise<ClaudeMigrationResult> {
  if (isMigrated(agentDir)) {
    return { ran: false, reason: "the self-stored claude files are already migrated" };
  }
  const configDir = options.claudeConfigDir ?? claudeConfigDir(options.env ?? process.env);
  const retired = retiredClaudeSelfStoredPaths(agentDir);
  const oldSettings = readOldRecord(retired.settings);
  const oldPermissions = readOldRecord(retired.permissions);
  const migrated: string[] = [];

  // The preference fields migrate FIRST, ungated (review r1#3): they have no
  // claude-native home, so no version gate applies — deferring them would
  // silently reset a user's channel, push and hook settings to the defaults
  // for as long as the native writes defer. A key the claude object already
  // holds is never overwritten, so a preference changed during a deferral
  // survives the completing run.
  const storedClaude = readPiWebSettingsRecord(agentDir).claude;
  const storedPreferenceKeys = isRecord(storedClaude)
    ? new Set(PREFERENCE_KEYS.filter((key) => typeof storedClaude[key] !== "undefined"))
    : new Set<string>();
  const preferences: Partial<ClaudePreferences> = {};
  if (oldSettings !== null && typeof oldSettings.includeHookEvents === "boolean" && !storedPreferenceKeys.has("includeHookEvents")) {
    preferences.includeHookEvents = oldSettings.includeHookEvents;
    migrated.push("includeHookEvents");
  }
  if (oldPermissions !== null) {
    const channel: ClaudePermissionMode = oldPermissions.mode === "ask" || oldPermissions.mode === "deny"
      ? oldPermissions.mode
      : "ask";
    if (!storedPreferenceKeys.has("approvalChannel")) {
      preferences.approvalChannel = channel;
      migrated.push("mode");
    }
    if (typeof oldPermissions.push === "boolean" && !storedPreferenceKeys.has("pushReminders")) {
      preferences.pushReminders = oldPermissions.push;
      migrated.push("push");
    }
  }
  if (Object.keys(preferences).length > 0) await writeClaudePreferences(preferences, agentDir);

  if (!existsSync(retired.settings) && !existsSync(retired.permissions)) {
    // Nothing to migrate: only the marker is missing (an interrupted replay,
    // or a machine that never had the old files).
    await markMigrated(agentDir);
    return { ran: true, summary: "nothing to migrate; marked" };
  }

  // The write gate: claude's version must be detected and known before any
  // NATIVE write (Q2 C). Unknown or absent: defer those — the old files stay
  // and the next run retries (the preference fields above already moved).
  const binary = options.claudeBinary ?? findClaudeBinary();
  const ctx: HarnessReadContext = {
    claudeConfigDir: configDir,
    env: options.env ?? process.env,
    ...(binary !== null ? { claudeBinary: binary } : {}),
    ...(options.managedSettingsPath !== undefined ? { managedSettingsPath: options.managedSettingsPath } : {}),
  };
  const probe = await createClaudeAdapter().probeVersion(ctx);
  if (probe.version === null || !probe.known) {
    return { ran: false, reason: `deferred (the preference fields already moved): ${probe.reason ?? `claude version ${probe.version ?? "unknown"} is not known`}` };
  }

  // Which fields still need their native write: a native value that already
  // exists wins and is never overwritten (the single source of truth).
  const nativeRecord = readJsonRecord(claudeUserSettingsPath(configDir)) ?? {};
  const nativeHasDefaultMode = isRecord((nativeRecord as Record<string, unknown>).permissions)
    && (nativeRecord as { permissions?: Record<string, unknown> }).permissions?.defaultMode !== undefined;
  if (!nativeHasDefaultMode) {
    const channel: ClaudePermissionMode = oldPermissions !== null && (oldPermissions.mode === "ask" || oldPermissions.mode === "deny")
      ? oldPermissions.mode
      : "ask";
    const launch = storedLaunchMode(oldSettings, channel);
    if (launch !== null) {
      const change: ClaudePermissionChange = { kind: "approvalMode", mode: launch };
      const applied = await applyClaudePermissionChange(change, configDir, join(configDir, ".claude.json"));
      if (!applied.ok) return { ran: false, reason: `deferred: ${applied.reason}` };
      migrated.push("launchPermissionMode");
    }
  }

  // 85a3-3: the custom names are NOT this flow's any more — the models page's
  // own phase (below) migrates them, so an old settings file that still holds
  // a customModels key (any value: a non-empty list to merge, or illegal data
  // the phase must report as a blockage) is kept for it. Delete what this
  // flow owns, and mark only once both old files are gone.
  const settingsHoldsCustomModels = oldSettings !== null && "customModels" in oldSettings;
  if (existsSync(retired.permissions)) unlinkSync(retired.permissions);
  if (!settingsHoldsCustomModels && existsSync(retired.settings)) unlinkSync(retired.settings);
  if (existsSync(retired.settings) || existsSync(retired.permissions)) {
    const landed = migrated.length > 0 ? `migrated ${migrated.join(", ")}; ` : "";
    return { ran: true, summary: `${landed}customModels await the models page's migration` };
  }
  await markMigrated(agentDir);
  return { ran: true, summary: migrated.length > 0 ? `migrated ${migrated.join(", ")}` : "nothing to migrate; marked" };
}

/** Tests reset the per-process memo between fixtures. */
export function resetClaudeMigrationForTests(): void {
  inFlight.clear();
}

// ── the customModels phase (85a3-3) ────────────────────────────────────────

/**
 * The customModels phase (85a3-3, owner rulings Q2/Q3/Q4): the models page's
 * protected POST — not the startup flow — migrates the retired customModels
 * into the native modelPicker. The target empty (the key absent or a legal
 * options: []) migrates at once; non-empty answers a merge preview (the
 * native rows win, only the source's missing names would be appended) and
 * writes nothing until the confirm carries the preview's digest; the source
 * or target changing in between answers a conflict with a fresh preview.
 * After the target lands and reads back, only the old file's customModels key
 * is removed (never the whole file — 85a4 owns its other fields); illegal or
 * over-cap source data reports a blockage and is kept. The done marker is
 * keyed by the target config root in pi-web-settings.json (the adapter's
 * persistent state; no private key in the native settings), so a new config
 * root is never skipped by a global boolean. Every intermediate crash
 * replays: each phase re-derives from the files (a re-run sees the names the
 * target already holds and only retries the clear), so only "done" is
 * recorded.
 */

/** The customModels phase's outcome for the migration POST's first call. */
export type ClaudeCustomModelsMigrationStatus =
  | { status: "done"; changed: boolean; shadowed: boolean }
  | { status: "blocked"; reason: string; shadowed: boolean }
  | { status: "deferred"; reason: string; shadowed: boolean }
  | { status: "preview"; native: { model: string; label?: string }[]; missing: string[]; digest: string; shadowed: boolean };

/** The customModels phase's outcome for the confirm call. */
export type ClaudeCustomModelsMigrationApply =
  | { status: "done"; changed: boolean; shadowed: boolean }
  | { status: "blocked"; reason: string; shadowed: boolean }
  | { status: "deferred"; reason: string; shadowed: boolean }
  | { status: "conflict"; preview: Extract<ClaudeCustomModelsMigrationStatus, { status: "preview" }> };

/** The old settings file's customModels phase serialized on its one path (a clear never interleaves with another writer). */
const RETIRED_SETTINGS_WRITE_CHAIN = Symbol.for("pi-web.retiredClaudeSettingsWrites");

/** The done marker for one target config root (Q4: keyed by root + storage, version 1). */
async function markCustomModelsMigrated(agentDir: string, configDir: string): Promise<void> {
  await patchPiWebSettings((record) => ({
    ...record,
    claudeCustomModelsMigration: {
      ...(isRecord(record.claudeCustomModelsMigration) ? record.claudeCustomModelsMigration : {}),
      [configDir]: { version: 1, done: true },
    },
  }), agentDir);
}

/** Removes only the customModels key from the old settings file, keeping its other fields (不整份删除旧文件). */
async function clearSourceCustomModels(agentDir: string): Promise<void> {
  const retired = retiredClaudeSelfStoredPaths(agentDir);
  await serializeByKey(RETIRED_SETTINGS_WRITE_CHAIN, retired.settings, async () => {
    if (!existsSync(retired.settings)) return;
    // The file parsed when the phase reached here; a fresh parse keeps every
    // key but customModels.
    const record = readJsonRecord(retired.settings);
    if (record === null || !("customModels" in record)) return;
    const next = { ...record };
    delete next.customModels;
    writePrivateFileAtomicSync(retired.settings, `${JSON.stringify(next, null, 2)}\n`);
  });
}

/** A stable JSON spelling for the digest (sorted keys, no formatting). */
function canonicalJson(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(",")}]`;
  if (isRecord(value)) {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonicalJson(value[key])}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

/** The phase's derived state: what the files say, before anything is written. */
type CustomModelsPhase =
  | { kind: "done"; sourceEmpty: boolean }
  | { kind: "blocked"; reason: string }
  | { kind: "deferred"; reason: string }
  | { kind: "auto"; names: string[]; ctx: HarnessReadContext }
  | { kind: "covered"; ctx: HarnessReadContext }  // the target already holds every source name
  | { kind: "preview"; native: { model: string; label?: string }[]; nativeNames: string[]; missing: string[]; digest: string; ctx: HarnessReadContext };

async function customModelsPhase(options: ClaudeMigrationOptions): Promise<CustomModelsPhase & { agentDir: string; configDir: string; shadowed: boolean }> {
  const agentDir = options.agentDir ?? defaultAgentDir();
  const configDir = options.claudeConfigDir ?? claudeConfigDir(options.env ?? process.env);
  // A managed modelPicker shadows the user target whole (claude reads the key
  // from the highest of managed/--settings/user only) — reported so the page
  // can say 已保存、未生效 (Q6); never written.
  const managedPath = options.managedSettingsPath ?? defaultManagedSettingsPath();
  const managed = readJsonRecord(managedPath);
  const shadowed = managed !== null && isRecord(managed.modelPicker);
  const state = { agentDir, configDir, shadowed };
  const retired = retiredClaudeSelfStoredPaths(agentDir);
  const done = readPiWebSettingsRecord(agentDir).claudeCustomModelsMigration;
  if (isRecord(done) && isRecord(done[configDir]) && done[configDir].done === true) {
    return { ...state, kind: "done", sourceEmpty: false };
  }
  const old = readOldRecord(retired.settings);
  if (old === null || !("customModels" in old)) {
    await markCustomModelsMigrated(agentDir, configDir);
    return { ...state, kind: "done", sourceEmpty: false };
  }
  const raw = old.customModels;
  if (!Array.isArray(raw)) {
    return { ...state, kind: "blocked", reason: `the retired customModels entry is not an array — nothing was migrated; fix or remove it in ${retired.settings}` };
  }
  if (raw.length === 0) {
    return { ...state, kind: "done", sourceEmpty: true };
  }
  const names: string[] = [];
  for (const entry of raw) {
    if (!isClaudeModelName(entry)) {
      return { ...state, kind: "blocked", reason: `the retired customModels list holds an invalid name (${JSON.stringify(entry)}) — nothing was migrated; fix or remove it in ${retired.settings}` };
    }
    if (!names.includes(entry)) names.push(entry);
  }
  if (names.length > CLAUDE_CUSTOM_MODELS_MAX) {
    return { ...state, kind: "blocked", reason: `the retired customModels list holds more than ${CLAUDE_CUSTOM_MODELS_MAX} names — nothing was migrated; fix or remove it in ${retired.settings}` };
  }
  // The write gate (AC1): an unknown or undetected version defers the phase.
  const binary = options.claudeBinary ?? findClaudeBinary();
  const ctx: HarnessReadContext = {
    claudeConfigDir: configDir,
    env: options.env ?? process.env,
    ...(binary !== null ? { claudeBinary: binary } : {}),
    ...(options.managedSettingsPath !== undefined ? { managedSettingsPath: options.managedSettingsPath } : {}),
  };
  const probe = await createClaudeAdapter().probeVersion(ctx);
  if (probe.version === null || !probe.known) {
    return { ...state, kind: "deferred", reason: probe.reason ?? `claude version ${probe.version ?? "unknown"} is not known` };
  }
  // The native target's shape: a bad shape blocks the phase with the write's
  // own preflight reason (坏形状拒写).
  const native = readJsonRecord(claudeUserSettingsPath(configDir)) ?? {};
  const refusal = claudeModelPickerShapeRefusal(native.modelPicker);
  if (refusal !== null) return { ...state, kind: "blocked", reason: refusal };
  const picker = isRecord(native.modelPicker) ? native.modelPicker : undefined;
  const optionRows = picker !== undefined && Array.isArray(picker.options) ? picker.options as Record<string, unknown>[] : [];
  const nativeNames = optionRows.map((row) => row.model as string);
  // 缺 key 或合法 options=[] 为空（保留 replaceBuiltInOptions 等字段）。
  const targetEmpty = picker === undefined || optionRows.length === 0;
  if (targetEmpty) return { ...state, kind: "auto", names, ctx };
  const missing = names.filter((name) => !nativeNames.includes(name));
  if (missing.length === 0) return { ...state, kind: "covered", ctx };
  const nativeRows = optionRows.map((row) => {
    const entry: { model: string; label?: string } = { model: row.model as string };
    if (typeof row.label === "string") entry.label = row.label;
    return entry;
  });
  const digest = createHash("sha256").update(canonicalJson({ source: raw, target: native.modelPicker ?? null })).digest("hex");
  return { ...state, kind: "preview", native: nativeRows, nativeNames, missing, digest, ctx };
}

/**
 * The migration POST's first call: the phase as it stands now. An empty target
 * migrates at once (自动迁移); a non-empty one answers the merge preview and
 * writes nothing.
 */
export async function claudeCustomModelsMigrationStep(
  options: ClaudeMigrationOptions = {},
): Promise<ClaudeCustomModelsMigrationStatus> {
  const phase = await customModelsPhase(options);
  switch (phase.kind) {
    case "done": {
      if (phase.sourceEmpty) {
        await clearSourceCustomModels(phase.agentDir);
        await markCustomModelsMigrated(phase.agentDir, phase.configDir);
        return { status: "done", changed: true, shadowed: phase.shadowed };
      }
      return { status: "done", changed: false, shadowed: phase.shadowed };
    }
    case "blocked":
      return { status: "blocked", reason: phase.reason, shadowed: phase.shadowed };
    case "deferred":
      return { status: "deferred", reason: phase.reason, shadowed: phase.shadowed };
    case "auto": {
      const write = await createClaudeAdapter().write("model", { customModels: phase.names }, phase.ctx);
      if (write.status !== "written") {
        return { status: "blocked", reason: write.reason, shadowed: phase.shadowed };
      }
      await clearSourceCustomModels(phase.agentDir);
      await markCustomModelsMigrated(phase.agentDir, phase.configDir);
      return { status: "done", changed: true, shadowed: phase.shadowed };
    }
    case "covered":
      // The target already holds every source name (a crash between the write
      // and the clear): only the clear is retried.
      await clearSourceCustomModels(phase.agentDir);
      await markCustomModelsMigrated(phase.agentDir, phase.configDir);
      return { status: "done", changed: true, shadowed: phase.shadowed };
    case "preview":
      return { status: "preview", native: phase.native, missing: phase.missing, digest: phase.digest, shadowed: phase.shadowed };
  }
}

/** The migration POST's confirm call: apply the preview under its digest. */
export async function applyClaudeCustomModelsMigration(
  options: ClaudeMigrationOptions = {},
  digest: string,
): Promise<ClaudeCustomModelsMigrationApply> {
  const phase = await customModelsPhase(options);
  switch (phase.kind) {
    case "done":
      return { status: "done", changed: false, shadowed: phase.shadowed };
    case "blocked":
      return { status: "blocked", reason: phase.reason, shadowed: phase.shadowed };
    case "deferred":
      return { status: "deferred", reason: phase.reason, shadowed: phase.shadowed };
    case "auto":
    case "covered":
      // The state moved while the user looked at the preview (the target was
      // emptied, or the names landed another way): nothing to confirm against.
      await clearSourceCustomModels(phase.agentDir);
      await markCustomModelsMigrated(phase.agentDir, phase.configDir);
      return { status: "done", changed: true, shadowed: phase.shadowed };
    case "preview": {
      if (phase.digest !== digest) {
        return {
          status: "conflict",
          preview: { status: "preview", native: phase.native, missing: phase.missing, digest: phase.digest, shadowed: phase.shadowed },
        };
      }
      // 同 id 以原生为准，只追加旧列表缺项 — the adapter's write keeps every
      // native row's metadata and appends the missing names as { model }.
      const write = await createClaudeAdapter().write("model", { customModels: [...phase.nativeNames, ...phase.missing] }, phase.ctx);
      if (write.status !== "written") {
        return { status: "blocked", reason: write.reason, shadowed: phase.shadowed };
      }
      await clearSourceCustomModels(phase.agentDir);
      await markCustomModelsMigrated(phase.agentDir, phase.configDir);
      return { status: "done", changed: true, shadowed: phase.shadowed };
    }
  }
}
