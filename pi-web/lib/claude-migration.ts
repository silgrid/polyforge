// The one-time migration of Pi Web's two retired self-stored claude files
// (polyforge#150 85a4, owner rulings Q4 B + Q5 A): pi-web-claude-settings.json
// and claude-permissions.json move field by field into what now owns them —
//
//   launchPermissionMode → claude's native permissions.defaultMode
//   customModels          → claude's native modelPicker.options
//   includeHookEvents     → pi-web-settings.json's claude object
//   mode (ask/deny)       →   "    "        "   approvalChannel
//   push                  →   "    "        "   pushReminders
//
// A native value that already exists wins (the native file is the single
// source of truth; the stored copy is never written over it). The flow is
// copy → write → re-read and verify → delete both old files → mark the
// migration in pi-web-settings.json, so an interruption at any point replays
// safely: every write is idempotent, the old files are deleted only after the
// fields landed, and a replay with the files gone but no marker only writes
// the marker. The claude version gate (adapter.ts) applies to the native
// writes: an unknown or undetected version defers the whole migration —
// nothing is written, the old files stay, the next run retries.
import { existsSync, unlinkSync } from "node:fs";
import { join } from "node:path";
import { claudeConfigDir } from "./claude-config-dir";
import { CLAUDE_CUSTOM_MODELS_MAX, CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT, isClaudeLaunchPermissionMode, isClaudeModelName, type ClaudeLaunchPermissionMode, type ClaudePermissionMode } from "./claude-shared";
import { findClaudeBinary } from "./claude-session";
import { defaultAgentDir } from "./http-dispatcher";
import { createClaudeAdapter } from "./harness/claude";
import { applyClaudePermissionChange, claudeUserSettingsPath, patchClaudeUserSettings, type ClaudePermissionChange } from "./harness/claude-native-write";
import { readJsonRecord } from "./harness/redact";
import { patchPiWebSettings, readPiWebSettingsRecord } from "./pi-web-settings";
import { writeClaudePreferences, type ClaudePreferences } from "./claude-preferences";
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

/** The stored custom model names, valid and distinct. */
function storedCustomModels(settings: Record<string, unknown> | null): string[] {
  const raw = settings?.customModels;
  if (!Array.isArray(raw)) return [];
  return [...new Set(raw.filter(isClaudeModelName))].slice(0, CLAUDE_CUSTOM_MODELS_MAX);
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
  if (!existsSync(retired.settings) && !existsSync(retired.permissions)) {
    // Nothing to migrate: only the marker is missing (an interrupted replay,
    // or a machine that never had the old files).
    await markMigrated(agentDir);
    return { ran: true, summary: "nothing to migrate; marked" };
  }

  // The write gate: claude's version must be detected and known before any
  // native write (Q2 C). Unknown or absent: defer, keep everything, retry later.
  const binary = options.claudeBinary ?? findClaudeBinary();
  const ctx: HarnessReadContext = {
    claudeConfigDir: configDir,
    env: options.env ?? process.env,
    ...(binary !== null ? { claudeBinary: binary } : {}),
    ...(options.managedSettingsPath !== undefined ? { managedSettingsPath: options.managedSettingsPath } : {}),
  };
  const probe = await createClaudeAdapter().probeVersion(ctx);
  if (probe.version === null || !probe.known) {
    return { ran: false, reason: `deferred: ${probe.reason ?? `claude version ${probe.version ?? "unknown"} is not known`}` };
  }

  // Which fields still need their native write: a native value that already
  // exists wins and is never overwritten (the single source of truth).
  const nativeRecord = readJsonRecord(claudeUserSettingsPath(configDir)) ?? {};
  const migrated: string[] = [];
  const channel: ClaudePermissionMode = oldPermissions !== null && (oldPermissions.mode === "ask" || oldPermissions.mode === "deny")
    ? oldPermissions.mode
    : "ask";
  const nativeHasDefaultMode = isRecord((nativeRecord as Record<string, unknown>).permissions)
    && (nativeRecord as { permissions?: Record<string, unknown> }).permissions?.defaultMode !== undefined;
  if (!nativeHasDefaultMode) {
    const launch = storedLaunchMode(oldSettings, channel);
    if (launch !== null) {
      const change: ClaudePermissionChange = { kind: "approvalMode", mode: launch };
      const applied = await applyClaudePermissionChange(change, configDir, join(configDir, ".claude.json"));
      if (!applied.ok) return { ran: false, reason: `deferred: ${applied.reason}` };
      migrated.push("launchPermissionMode");
    }
  }
  const nativeHasPicker = isRecord((nativeRecord as Record<string, unknown>).modelPicker);
  const models = storedCustomModels(oldSettings);
  if (!nativeHasPicker && models.length > 0) {
    const applied = await applyClaudeModelPicker(models, configDir);
    if (!applied.ok) return { ran: false, reason: `deferred: ${applied.reason}` };
    migrated.push("customModels");
  }

  // The preference fields always migrate (they have no native home to conflict with).
  const preferences: Partial<ClaudePreferences> = {};
  if (oldSettings !== null && typeof oldSettings.includeHookEvents === "boolean") {
    preferences.includeHookEvents = oldSettings.includeHookEvents;
    migrated.push("includeHookEvents");
  }
  if (oldPermissions !== null) {
    preferences.approvalChannel = channel;
    migrated.push("mode");
    if (typeof oldPermissions.push === "boolean") {
      preferences.pushReminders = oldPermissions.push;
      migrated.push("push");
    }
  }
  if (Object.keys(preferences).length > 0) await writeClaudePreferences(preferences, agentDir);

  // The fields landed and verified: delete both old files, then mark. A crash
  // between the two replays as the "nothing to migrate" path above.
  for (const path of [retired.settings, retired.permissions]) {
    if (existsSync(path)) unlinkSync(path);
  }
  await markMigrated(agentDir);
  return { ran: true, summary: `migrated ${migrated.join(", ")}` };
}

/** Writes the custom names into claude's native modelPicker.options (user level, same read-back verify as the permission writes). */
function applyClaudeModelPicker(models: string[], configDir: string): Promise<{ ok: true } | { ok: false; reason: string }> {
  // modelPicker.options is claude's own native shape (v2.1.242+): one
  // { model } entry per name, replacing the user layer's list.
  return patchClaudeUserSettings(configDir, (record) => ({
    ...record,
    modelPicker: {
      ...(isRecord(record.modelPicker) ? record.modelPicker : {}),
      options: models.map((model) => ({ model })),
    },
  }));
}

/** Tests reset the per-process memo between fixtures. */
export function resetClaudeMigrationForTests(): void {
  inFlight.clear();
}
