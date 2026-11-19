"use client";

// The React face of the shared claude-settings store (polyforge#149,
// #85 85a1b): both the 界面与对话 pane and the legacy Claude page read the
// same snapshot through this hook, so a save from either shows at once in
// the other. Placeholder signature; the body arrives with its unit and
// replaces this stub.
import { claudeSettingsStore, type ClaudeSettingsSnapshot, type ClaudeSettingsStore } from "@/lib/claude-settings-store";

export function useClaudeSettings(store: ClaudeSettingsStore = claudeSettingsStore): {
  snapshot: ClaudeSettingsSnapshot;
  refresh: () => Promise<void>;
} {
  return {
    snapshot: store.getSnapshot(),
    refresh: () => store.refresh(),
  };
}
