// Settings › Claude: `<pi agent dir>/pi-web-claude-settings.json`,
// `{ "version": 1, "launchPermissionMode": "default" | "acceptEdits" | "plan",
// "customModels": string[], "includeHookEvents": boolean }`. pi-web's own file, apart
// from the Agent language's pi-web-settings.json; `app/api/claude-settings` is its one
// writer. A new claude session captures it (`app/api/agent/new`); a change never
// reaches a session already created. Whether plan may be stored follows the
// global claude tool permissions (claude-permissions.json, read only; r3).
import { mkdirSync, readFileSync } from "fs";
import { dirname, join } from "path";
import { writePrivateFileAtomicSync } from "./atomic-file";
import {
  CLAUDE_CUSTOM_MODELS_MAX,
  CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT,
  claudePlanModeAllowed,
  isClaudeModelName,
  isClaudeLaunchPermissionMode,
  type ClaudePermissionMode,
  type ClaudeSettings,
} from "./claude-shared";
import { readClaudePermissionConfig } from "./claude-permission-config";
import { defaultAgentDir } from "./http-dispatcher";
import { serializeByKey } from "./key-serializer";

const WRITE_CHAIN = Symbol.for("pi-web.claudeSettingsWrites");

export const DEFAULT_CLAUDE_SETTINGS: Readonly<ClaudeSettings> = Object.freeze({
  launchPermissionMode: CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT,
  customModels: [],
  includeHookEvents: false,
});

export function claudeSettingsPath(agentDir: string = defaultAgentDir()): string {
  return join(agentDir, "pi-web-claude-settings.json");
}

/**
 * Stored values as settings, each field on its own: an unknown permission mode reads as
 * default, and so does plan while the global approval mode `approvalMode` is
 * deny (claudePlanModeAllowed; the file keeps it), an invalid or repeated custom name is
 * dropped (at most 50 kept), anything but `true` is hooks off. Never throws.
 */
export function normalizeClaudeSettings(value: unknown, approvalMode: ClaudePermissionMode): ClaudeSettings {
  const raw = (typeof value === "object" && value !== null && !Array.isArray(value) ? value : {}) as Record<string, unknown>;
  const mode = raw.launchPermissionMode;
  const launchPermissionMode = isClaudeLaunchPermissionMode(mode) && (mode !== "plan" || claudePlanModeAllowed(approvalMode)) ? mode : CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT;
  const customModels = Array.isArray(raw.customModels)
    ? [...new Set(raw.customModels.filter(isClaudeModelName))].slice(0, CLAUDE_CUSTOM_MODELS_MAX)
    : [];
  return { launchPermissionMode, customModels, includeHookEvents: raw.includeHookEvents === true };
}

/**
 * The stored settings as a new session takes them (plan judged by the same agent dir's
 * claude-permissions.json); a missing or malformed file reads as the defaults (never throws).
 */
export function readClaudeSettings(agentDir?: string): ClaudeSettings {
  const approvalMode = readClaudePermissionConfig(agentDir).mode;
  try {
    return normalizeClaudeSettings(JSON.parse(readFileSync(claudeSettingsPath(agentDir), "utf8")), approvalMode);
  } catch {
    return normalizeClaudeSettings(undefined, approvalMode);
  }
}

/**
 * Stores `settings`, keeping the file's other keys; one write at a time per file,
 * replaced atomically with mode 0600. A file that exists but is not a JSON object is
 * refused (thrown), never overwritten.
 */
export function writeClaudeSettings(settings: ClaudeSettings, agentDir?: string): Promise<ClaudeSettings> {
  const path = claudeSettingsPath(agentDir);
  return serializeByKey(WRITE_CHAIN, path, async () => {
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
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
        throw new Error(`${path} is not a JSON object; refusing to overwrite it`);
      }
      current = parsed as Record<string, unknown>;
    }
    const next: ClaudeSettings = {
      launchPermissionMode: settings.launchPermissionMode,
      customModels: [...settings.customModels],
      includeHookEvents: settings.includeHookEvents,
    };
    mkdirSync(dirname(path), { recursive: true });
    writePrivateFileAtomicSync(path, `${JSON.stringify({ ...current, version: 1, ...next }, null, 2)}\n`);
    return next;
  });
}
