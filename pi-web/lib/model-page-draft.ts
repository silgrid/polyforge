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

export function emptyDraft(): ModelPageDraft {
  return { defaultModel: null, efforts: {} };
}

/** The user's default-model choice; the change is the draft, never a write. */
export function setDraftDefaultModel(draft: ModelPageDraft, model: ModelPageModel | null): ModelPageDraft {
  return { ...draft, defaultModel: model };
}

/** The user's per-model level ("" = follow the global level). */
export function setDraftEffort(draft: ModelPageDraft, key: string, level: string): ModelPageDraft {
  return { ...draft, efforts: { ...draft.efforts, [key]: level } };
}

/** How many fields the explicit save would write. */
export function draftDirtyCount(draft: ModelPageDraft): number {
  return (draft.defaultModel === null ? 0 : 1) + Object.keys(draft.efforts).length;
}

export interface ModelPageWrite {
  kind: "default" | "effort";
  /** The effort write's `provider/id`; absent for the default write. */
  key?: string;
  url: string;
  body: Record<string, unknown>;
}

/** The requests one explicit save sends: the combined default first (pi's
 *  provider/modelId, claude's harness/model), then each per-model level as
 *  the pi adapter's `modelThinkingLevel` write. */
export function draftWrites(draft: ModelPageDraft, cwd: string | null): ModelPageWrite[] {
  const writes: ModelPageWrite[] = [];
  if (draft.defaultModel) {
    const body = draft.defaultModel.harness === "claude"
      ? { harness: "claude", model: draft.defaultModel.id, ...(cwd ? { cwd } : {}) }
      : { provider: draft.defaultModel.provider, modelId: draft.defaultModel.id, ...(cwd ? { cwd } : {}) };
    writes.push({ kind: "default", url: "/api/models/default", body });
  }
  for (const [key, level] of Object.entries(draft.efforts)) {
    writes.push({ kind: "effort", key, url: "/api/harness/pi/model",
      body: { modelThinkingLevel: { key, level: level || null } } });
  }
  return writes;
}

/** Removes the landed write's entry; an unsaved one stays for the retry. */
export function settleWrite(draft: ModelPageDraft, write: ModelPageWrite): ModelPageDraft {
  if (write.kind === "default") return { ...draft, defaultModel: null };
  const efforts = { ...draft.efforts };
  if (write.key !== undefined) delete efforts[write.key];
  return { ...draft, efforts };
}