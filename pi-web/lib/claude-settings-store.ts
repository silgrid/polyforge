// The one shared holder of Settings › Claude's values (polyforge#149,
// #85 85a1b, owner ruling Q5 A): the 界面与对话 pane and the legacy Claude
// page read and write the same store, so a save from either page can never
// overwrite the other's with a stale snapshot — every save merges its patch
// onto the newest snapshot and PUTs the whole package, one save at a time.
// The API and the file are unchanged (`/api/claude-settings`,
// `pi-web-claude-settings.json`); this holder only shares the state.
// Placeholder signature; the body arrives with its unit and replaces this
// stub.
import type { ClaudeSettings } from "./claude-shared";

export interface ClaudeSettingsSnapshot {
  status: "loading" | "ready" | "error";
  settings: ClaudeSettings | null;
  /** GET's planAvailable: plan is offered while the global claude tool permissions are ask. */
  planAvailable: boolean;
  loadError: string | null;
}

export type ClaudeSettingsSaveResult = { ok: true; settings: ClaudeSettings } | { ok: false; error: string };

export interface ClaudeSettingsStore {
  subscribe(listener: () => void): () => void;
  getSnapshot(): ClaudeSettingsSnapshot;
  /** One GET; the store's only read. */
  refresh(): Promise<void>;
  /** One serialized whole-package PUT of the newest snapshot merged with `patch`. */
  save(patch: Partial<ClaudeSettings>): Promise<ClaudeSettingsSaveResult>;
}

export function createClaudeSettingsStore(_fetchImpl: typeof fetch): ClaudeSettingsStore {
  const listeners = new Set<() => void>();
  return {
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getSnapshot() {
      return { status: "loading", settings: null, planAvailable: false, loadError: null };
    },
    async refresh() {},
    async save() {
      return { ok: false, error: "not implemented yet" };
    },
  };
}

/** The singleton both pages share. */
export const claudeSettingsStore: ClaudeSettingsStore = createClaudeSettingsStore(fetch);
