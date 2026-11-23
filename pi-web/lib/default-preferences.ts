import { join } from "path";
import type { ThinkingLevel } from "@earendil-works/pi-agent-core";
import type { SettingsManager } from "@earendil-works/pi-coding-agent";
import { displaySettingsPath } from "./enabled-models-runtime";

const THINKING_LEVELS = new Set<ThinkingLevel>(["off", "minimal", "low", "medium", "high", "xhigh", "max"]);

export function isThinkingLevel(value: unknown): value is ThinkingLevel {
  return typeof value === "string" && THINKING_LEVELS.has(value as ThinkingLevel);
}

/** What the model and reasoning selectors' "save as default" action writes. */
export interface DefaultPreferencesEdit {
  model?: { provider: string; modelId: string };
  thinkingLevel?: ThinkingLevel;
}

/**
 * Project settings keys that would hide this edit.
 *
 * `SettingsManager` only ever writes the global file, and a project
 * `.pi/settings.json` value wins over it, so writing a default the project
 * overrides would report success while new chats kept starting with the
 * project's choice. The route refuses instead and names the file.
 */
export function shadowingProjectKeys(
  settingsManager: SettingsManager,
  edit: DefaultPreferencesEdit,
): string[] {
  const project = settingsManager.getProjectSettings();
  const keys: string[] = [];
  if (edit.model) {
    if (project.defaultProvider !== undefined) keys.push("defaultProvider");
    if (project.defaultModel !== undefined) keys.push("defaultModel");
  }
  if (edit.thinkingLevel && project.defaultThinkingLevel !== undefined) keys.push("defaultThinkingLevel");
  return keys;
}

export function projectSettingsPath(cwd: string): string {
  return displaySettingsPath(join(cwd, ".pi", "settings.json"));
}

// The global defaults' native write is the pi adapter's model write
// (lib/harness/pi-native-write.ts, 85a3-1): the same locked read-merge-write
// this module's SettingsManager flows take, behind the adapter's version gate.
// The star's route (app/api/models/default) composes the two.
