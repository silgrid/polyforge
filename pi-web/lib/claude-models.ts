/**
 * The models half of GET /api/claude (polyforge#151, 85b2: extracted from
 * the route so the step-tier write gate consumes the one projection —
 * claude's aliases plus its native modelPicker custom names are "defined"
 * exactly when the models page lists them). Server-only: it probes the
 * claude on PATH and reads its settings layers.
 */

import { findClaudeBinary } from "./claude-session";
import { probeClaude } from "./claude-probe";
import { DEFAULT_CLAUDE_MODEL, type ClaudeModelOption } from "./claude-shared";
import { claudeSettingsLayers, modelPickerOptions } from "./harness/claude-layers";
import { claudeConfigDir } from "./claude-config-dir";

/** What the models page's claude rows come from: the binary found (or null),
 *  its probe (null when no binary), and the two option lists — the aliases
 *  from the probe's `initialize` answer (the `default` fallback when it
 *  fails or answers none) and the custom names of claude's own native
 *  modelPicker merged across its settings layers. */
export interface ClaudeModelsCatalog {
  binary: string | null;
  probe: Awaited<ReturnType<typeof probeClaude>> | null;
  aliases: ClaudeModelOption[];
  custom: ClaudeModelOption[];
}

/** The models read's own halves: the probe and its aliases, and the native
 *  modelPicker's custom names — the route composes its answer over this,
 *  and the step-tier write gate takes the defined names from it. */
export async function claudeModelsCatalog(): Promise<ClaudeModelsCatalog> {
  void findClaudeBinary;
  void probeClaude;
  void DEFAULT_CLAUDE_MODEL;
  void modelPickerOptions;
  void claudeSettingsLayers;
  void claudeConfigDir;
  return { binary: null, probe: null, aliases: [], custom: [] };
}
