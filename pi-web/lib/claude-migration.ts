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
// A native value that already exists wins; the flow is copy → write → re-read
// and verify → delete both old files → mark the migration in
// pi-web-settings.json, so an interruption at any point replays safely. The
// claude version gate (adapter.ts) applies to the native writes: an unknown
// or undetected version defers the whole migration.
//
// SKELETON (the pre-test ordinary commit): the signatures exist so the
// suite's red is its assertions, never a missing symbol; the steps land with
// this unit's implementation commit.

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

/**
 * Runs the migration once per agent dir per process. Not implemented yet
 * (the skeleton): every call answers that it did not run.
 */
export function ensureClaudeSelfStoredMigration(_options: ClaudeMigrationOptions = {}): Promise<ClaudeMigrationResult> {
  return Promise.resolve({ ran: false, reason: "not implemented yet" });
}

/** Tests reset the per-process memo between fixtures. */
export function resetClaudeMigrationForTests(): void {}
