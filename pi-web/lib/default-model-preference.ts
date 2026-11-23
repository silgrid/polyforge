// The combined default model (polyforge#147, 85a3-1 AC2): 「harness + 模型」 —
// the harness new sessions start on, and the model snapshot the star saved.
// The harness part has no native home on either side (a harness is Pi Web's
// own concept), so per the owner's Q1 A ruling it lives in Pi Web's own
// settings file, `pi-web-settings.json`'s `defaultModel` key, through the
// one shared writer (lib/pi-web-settings.ts). The model snapshot is what the
// native write landed; behaviour always reads the native files as the truth
// (pi's settings.json, claude's settings.json), the snapshot serves display.
import { isSessionHarness, type SessionHarness } from "./claude-shared";
import { patchPiWebSettings, readPiWebSettingsRecord } from "./pi-web-settings";

/** The stored combined default (the shape of pi-web-settings.json's `defaultModel` key). */
export interface DefaultModelPreference {
  /** The harness new sessions start on. */
  harness: SessionHarness;
  /** pi's snapshot: the provider/modelId the native write landed. */
  model?: { provider: string; modelId: string };
  /** claude's snapshot: the model name the native write landed. */
  claudeModel?: string;
}

/** The default when nothing is stored: pi, its own native default model. */
export const DEFAULT_MODEL_PREFERENCE: Readonly<Pick<DefaultModelPreference, "harness">> = Object.freeze({
  harness: "pi",
});

/** Whether `value` is a plain record. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** One stored record onto the preference, tolerantly; malformed parts read as
 * absent, and a snapshot only follows its own harness (pi's model, claude's
 * claudeModel) — a stale snapshot of the other harness is not a default. */
function parsePreference(raw: unknown): DefaultModelPreference {
  if (!isRecord(raw)) return { harness: DEFAULT_MODEL_PREFERENCE.harness };
  const harness = isSessionHarness(raw.harness) ? raw.harness : DEFAULT_MODEL_PREFERENCE.harness;
  const preference: DefaultModelPreference = { harness };
  const model = raw.model;
  if (harness === "pi" && isRecord(model) && typeof model.provider === "string" && model.provider
    && typeof model.modelId === "string" && model.modelId) {
    preference.model = { provider: model.provider, modelId: model.modelId };
  }
  if (harness === "claude" && typeof raw.claudeModel === "string" && raw.claudeModel) {
    preference.claudeModel = raw.claudeModel;
  }
  return preference;
}

/**
 * The stored combined default; a missing, malformed or partial record reads as
 * the default (pi). Never throws.
 */
export function readDefaultModelPreference(agentDir?: string): DefaultModelPreference {
  return parsePreference(readPiWebSettingsRecord(agentDir).defaultModel);
}

/**
 * Stores the combined default under the file's `defaultModel` key, keeping
 * every other key (the Agent language's, the claude preferences', the
 * migration marker's); one write at a time, atomic, unknown keys kept.
 * Resolves to the preference as stored.
 */
export async function writeDefaultModelPreference(
  preference: DefaultModelPreference,
  agentDir?: string,
): Promise<DefaultModelPreference> {
  await patchPiWebSettings((record) => ({
    ...record,
    defaultModel: {
      harness: preference.harness,
      ...(preference.model ? { model: { ...preference.model } } : {}),
      ...(preference.claudeModel !== undefined ? { claudeModel: preference.claudeModel } : {}),
    },
  }), agentDir);
  return parsePreference(preference);
}
