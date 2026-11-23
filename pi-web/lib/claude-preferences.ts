// The Pi Web claude preferences (polyforge#150 85a4): the fields of the two
// retired self-stored files that have no claude-native home — the approval
// channel (ask: a claude child's permission prompts reach Pi Web as dialogs;
// deny: claude refuses them itself), the pending-permission push reminders
// and the hook-activity display switch — live in pi-web-settings.json's
// `claude` object beside the Agent language, with the same tolerant read and
// serialized atomic write. The claude-native fields (the launch permission
// mode, the custom model names) are claude's own settings.json now; the
// one-time move is `lib/claude-migration.ts`.
import { isClaudePermissionMode, type ClaudePermissionMode } from "./claude-shared";
import { patchPiWebSettings, piWebSettingsPath, readPiWebSettingsRecord } from "./pi-web-settings";

/** The stored claude preferences (the shape of pi-web-settings.json's `claude` object). */
export interface ClaudePreferences {
  /** How a claude child answers permission prompts: Pi Web dialogs (ask) or itself (deny). */
  approvalChannel: ClaudePermissionMode;
  /** Whether a pending permission prompt is pushed to subscribed devices. */
  pushReminders: boolean;
  /** Whether a claude session shows hook activity (`--include-hook-events`). */
  includeHookEvents: boolean;
}

/** The defaults when nothing is stored: dialogs, push on, hooks off. */
export const DEFAULT_CLAUDE_PREFERENCES: Readonly<ClaudePreferences> = Object.freeze({
  approvalChannel: "ask",
  pushReminders: true,
  includeHookEvents: false,
});

/** Whether `value` is a plain record. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** The stored preferences; a missing, malformed or partial `claude` object reads as the defaults. Never throws. */
export function readClaudePreferences(agentDir?: string): ClaudePreferences {
  const claude = readPiWebSettingsRecord(agentDir).claude;
  if (!isRecord(claude)) return { ...DEFAULT_CLAUDE_PREFERENCES };
  return {
    approvalChannel: isClaudePermissionMode(claude.approvalChannel) ? claude.approvalChannel : DEFAULT_CLAUDE_PREFERENCES.approvalChannel,
    pushReminders: typeof claude.pushReminders === "boolean" ? claude.pushReminders : DEFAULT_CLAUDE_PREFERENCES.pushReminders,
    includeHookEvents: typeof claude.includeHookEvents === "boolean" ? claude.includeHookEvents : DEFAULT_CLAUDE_PREFERENCES.includeHookEvents,
  }
}

/** The preferences file's path (pi-web-settings.json; the test asserts it exists at this shape). */
export function claudePreferencesPath(agentDir: string | undefined): string {
  return piWebSettingsPath(agentDir);
}

/**
 * Stores the keys `patch` names under the file's `claude` object, keeping
 * every other key (the Agent language's, the migration marker's); one write
 * at a time, atomic, unknown keys kept. Resolves to the preferences as
 * stored.
 */
export async function writeClaudePreferences(patch: Partial<ClaudePreferences>, agentDir?: string): Promise<ClaudePreferences> {
  const next = await patchPiWebSettings((record) => {
    const claude = isRecord(record.claude) ? record.claude : {};
    return {
      ...record,
      claude: {
        ...readClaudePreferences(agentDir),
        ...claude,
        ...(patch.approvalChannel !== undefined ? { approvalChannel: patch.approvalChannel } : {}),
        ...(patch.pushReminders !== undefined ? { pushReminders: patch.pushReminders } : {}),
        ...(patch.includeHookEvents !== undefined ? { includeHookEvents: patch.includeHookEvents } : {}),
      },
    };
  }, agentDir);
  const claude = isRecord(next.claude) ? next.claude : {};
  return {
    approvalChannel: isClaudePermissionMode(claude.approvalChannel) ? claude.approvalChannel : DEFAULT_CLAUDE_PREFERENCES.approvalChannel,
    pushReminders: typeof claude.pushReminders === "boolean" ? claude.pushReminders : DEFAULT_CLAUDE_PREFERENCES.pushReminders,
    includeHookEvents: typeof claude.includeHookEvents === "boolean" ? claude.includeHookEvents : DEFAULT_CLAUDE_PREFERENCES.includeHookEvents,
  };
}
