/** The model page's unsaved draft (polyforge#258 T3): default model and
 *  per-model reasoning changes live here until the user presses 「保存（N）」,
 *  and each landed write clears only its own entry. */

export interface ModelPageModel {
  harness: string;
  provider: string;
  id: string;
}

export interface ModelPageDraft {
  /** A default model the user changed but did not save; null = unchanged. */
  defaultModel: ModelPageModel | null;
  /** Per-model pi thinking levels the user changed: `provider/id` → level;
   *  "" means follow the global level. */
  efforts: Record<string, string>;
}

/** Stub (polyforge#258 u4): filled in by the implementation commit. */
export function emptyDraft(): ModelPageDraft {
  return { defaultModel: null, efforts: {} };
}

/** Stub (polyforge#258 u4): filled in by the implementation commit. */
export function setDraftDefaultModel(draft: ModelPageDraft, model: ModelPageModel | null): ModelPageDraft {
  return draft;
}

/** Stub (polyforge#258 u4): filled in by the implementation commit. */
export function setDraftEffort(draft: ModelPageDraft, key: string, level: string): ModelPageDraft {
  return draft;
}

/** Stub (polyforge#258 u4): filled in by the implementation commit. */
export function draftDirtyCount(draft: ModelPageDraft): number {
  return 0;
}

export interface ModelPageWrite {
  kind: "default" | "effort";
  /** The effort write's `provider/id`; absent for the default write. */
  key?: string;
  url: string;
  body: Record<string, unknown>;
}

/** Stub (polyforge#258 u4): filled in by the implementation commit. */
export function draftWrites(draft: ModelPageDraft, cwd: string | null): ModelPageWrite[] {
  return [];
}

/** Stub (polyforge#258 u4): filled in by the implementation commit. */
export function settleWrite(draft: ModelPageDraft, write: ModelPageWrite): ModelPageDraft {
  return draft;
}