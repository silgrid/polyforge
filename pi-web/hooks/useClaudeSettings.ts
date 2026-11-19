"use client";

// The React face of the shared claude-settings store (polyforge#149,
// #85 85a1b): both the 界面与对话 pane and the legacy Claude page read the
// same snapshot through this hook, so a save from either shows at once in
// the other. The store is the one writer; this hook never fetches itself.
import { useSyncExternalStore } from "react";
import { CLAUDE_SETTINGS_SERVER_SNAPSHOT, claudeSettingsStore, type ClaudeSettingsSaveResult, type ClaudeSettingsSnapshot, type ClaudeSettingsStore } from "@/lib/claude-settings-store";

export function useClaudeSettings(store: ClaudeSettingsStore = claudeSettingsStore): {
  snapshot: ClaudeSettingsSnapshot;
  refresh: () => Promise<void>;
  save: (patch: Parameters<ClaudeSettingsStore["save"]>[0]) => Promise<ClaudeSettingsSaveResult>;
} {
  const snapshot = useSyncExternalStore(
    store.subscribe,
    store.getSnapshot,
    // A server render has read nothing: the loading state, so hydration agrees.
    () => CLAUDE_SETTINGS_SERVER_SNAPSHOT,
  );
  return { snapshot, refresh: store.refresh, save: store.save };
}
