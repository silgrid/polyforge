// The combined default model (polyforge#147, 85a3-1 AC2): 「harness + 模型」 —
// the harness new sessions start on, and the model snapshot the star saved.
// The harness part has no native home on either side (a harness is Pi Web's
// own concept), so per the owner's Q1 A ruling it lives in Pi Web's own
// settings file, `pi-web-settings.json`'s `defaultModel` key, through the
// one shared writer (lib/pi-web-settings.ts). The model snapshot is what the
// native write landed; behaviour always reads the native files as the truth
// (pi's settings.json, claude's settings.json), the snapshot serves display.
//
// Skeleton (85a3 u3): every signature with zero-value behaviour — the read
// answers the default and the write persists nothing — so the suite's red is
// its assertions, never a missing symbol.
import type { SessionHarness } from "./claude-shared";

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

/**
 * The stored combined default; a missing, malformed or partial record reads as
 * the default (pi). Never throws.
 */
export function readDefaultModelPreference(_agentDir?: string): DefaultModelPreference {
  return { harness: DEFAULT_MODEL_PREFERENCE.harness };
}

/**
 * Stores the combined default under the file's `defaultModel` key, keeping
 * every other key; one write at a time, atomic, unknown keys kept.
 * Resolves to the preference as stored.
 */
export async function writeDefaultModelPreference(
  preference: DefaultModelPreference,
  _agentDir?: string,
): Promise<DefaultModelPreference> {
  return { harness: DEFAULT_MODEL_PREFERENCE.harness };
}
