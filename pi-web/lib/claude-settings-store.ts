// The one shared holder of Settings › Claude's values (polyforge#149,
// #85 85a1b, owner ruling Q5 A): the 界面与对话 pane and the legacy Claude
// page read and write the same store, so a save from either page can never
// overwrite the other's with a stale snapshot — every save merges its patch
// onto the newest snapshot and PUTs the whole package, one save at a time.
// The API and the file are unchanged (`/api/claude-settings`,
// `pi-web-claude-settings.json`); this holder only shares the state. It
// solves this batch's two editors on one page; it does not claim to solve
// cross-device concurrency.
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

function isClaudeSettings(value: unknown): value is ClaudeSettings {
  if (typeof value !== "object" || value === null) return false;
  const settings = value as Partial<ClaudeSettings>;
  return typeof settings.launchPermissionMode === "string"
    && Array.isArray(settings.customModels)
    && typeof settings.includeHookEvents === "boolean";
}

const snapshotOf = (settings: ClaudeSettings): ClaudeSettings => ({
  launchPermissionMode: settings.launchPermissionMode,
  customModels: [...settings.customModels],
  includeHookEvents: settings.includeHookEvents,
});

export function createClaudeSettingsStore(fetchImpl: typeof fetch = fetch): ClaudeSettingsStore {
  let snapshot: ClaudeSettingsSnapshot = { status: "loading", settings: null, planAvailable: false, loadError: null };
  const listeners = new Set<() => void>();
  // One save at a time: each save runs after the previous one settled, and
  // reads the snapshot at its own turn — the newest shared result (Q5 A).
  let saveChain: Promise<unknown> = Promise.resolve();
  // Bumped every time a save's answer lands. A refresh that began before a
  // save holds an older generation, so its answer — which predates the save —
  // is dropped instead of rolling the save back (the Q5 A overwrite, coming
  // in through a remount's in-flight GET; r1#3).
  let generation = 0;

  const set = (next: ClaudeSettingsSnapshot) => {
    if (next === snapshot) return;
    snapshot = next;
    for (const listener of listeners) listener();
  };

  return {
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getSnapshot(): ClaudeSettingsSnapshot {
      return snapshot;
    },
    async refresh() {
      const readAt = generation;
      try {
        const response = await fetchImpl("/api/claude-settings", { method: "GET" });
        const data: unknown = await response.json();
        if (!response.ok || !isClaudeSettings(data)) throw new Error(`HTTP ${response.status}`);
        // A save landed while this read was in flight: the answer predates it.
        if (readAt !== generation) return;
        const planAvailable = (data as ClaudeSettings & { planAvailable?: unknown }).planAvailable === true;
        const previous = snapshot.settings;
        if (previous
          && previous.launchPermissionMode === data.launchPermissionMode
          && previous.includeHookEvents === data.includeHookEvents
          && previous.customModels.length === data.customModels.length
          && previous.customModels.every((name, index) => name === data.customModels[index])
          && snapshot.planAvailable === planAvailable) {
          // An unchanged read keeps the snapshot identity (a re-render trigger only).
          return;
        }
        set({ status: "ready", settings: snapshotOf(data), planAvailable, loadError: null });
      } catch (cause) {
        if (readAt !== generation) return;
        set({ ...snapshot, status: "error", loadError: cause instanceof Error ? cause.message : String(cause) });
      }
    },
    save(patch: Partial<ClaudeSettings>): Promise<ClaudeSettingsSaveResult> {
      const run = saveChain.then(async (): Promise<ClaudeSettingsSaveResult> => {
        const current = snapshot.settings;
        if (!current) return { ok: false, error: "the settings have not been read yet" };
        const next: ClaudeSettings = {
          launchPermissionMode: patch.launchPermissionMode ?? current.launchPermissionMode,
          customModels: patch.customModels ?? current.customModels,
          includeHookEvents: patch.includeHookEvents ?? current.includeHookEvents,
        };
        try {
          const response = await fetchImpl("/api/claude-settings", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(next),
          });
          const data: unknown = await response.json();
          if (!response.ok) {
            const body = data as { error?: unknown } | null;
            throw new Error(typeof body?.error === "string" && body.error ? body.error : `HTTP ${response.status}`);
          }
          if (!isClaudeSettings(data)) throw new Error("unexpected answer");
          generation += 1;
          set({ ...snapshot, status: "ready", settings: snapshotOf(data), loadError: null });
          return { ok: true, settings: snapshotOf(data) };
        } catch (cause) {
          return { ok: false, error: cause instanceof Error ? cause.message : String(cause) };
        }
      });
      // The chain continues past a failure; only this save's caller sees it.
      saveChain = run.then(() => undefined, () => undefined);
      return run;
    },
  };
}

/**
 * The server snapshot (a fresh store's loading state): hydration agrees, since
 * a server render has read nothing.
 */
export const CLAUDE_SETTINGS_SERVER_SNAPSHOT: ClaudeSettingsSnapshot = Object.freeze({
  status: "loading",
  settings: null,
  planAvailable: false,
  loadError: null,
});

/** The singleton both pages share: the 界面与对话 pane and the legacy Claude page. */
export const claudeSettingsStore: ClaudeSettingsStore = createClaudeSettingsStore(fetch);
