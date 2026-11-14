/**
 * Pure presentation helpers for Settings › Polyforge's configuration panel.
 * Import-free on purpose, like `lib/polyforge-config-schema.ts`:
 * the client component imports it, so it must never pull a server module into
 * the browser bundle. Nothing here changes what is read or written — it only
 * decides which card a `pf --json config list` item renders in and how a
 * workspace mapping row is displayed.
 */

/** The minimal item shape the helpers need (structurally a PolyforgeConfigItem). */
export interface ConfigItemLike {
  key: string;
  value?: unknown;
  section?: boolean;
}

/**
 * pf's `[daemon]` table keys (`internal/polyforge/cli/config_keys.go`).
 * `pf --json config list` does not mark table membership, so the panel keeps
 * this copy to put them in the daemon card. Drift cost: a `[daemon]` key pf
 * adds later renders in the cli.toml card until this list is extended —
 * cosmetic only. Distinct from the panel's daemon *column* (the three keys
 * whose live value the daemon reports).
 */
export const DAEMON_TABLE_KEYS: readonly string[] = [
  "step_timeout",
  "stall_window",
  "harness_root",
  "operator_agent_dir",
  // the interpreter the step scripts run under (restart-only).
  "python",
  "max_concurrent_jobs",
  // the rest of pf's [daemon] table — the Cell ceiling is a
  // visible row of the default Cell's pane, so its zone must be right.
  "cell_max_concurrent_jobs",
  "machine_id",
  "heartbeat_every",
  // the step agents' language (Pi Web's Agent language save
  // sets it; the panel lists it read-only).
  "agent_language",
];

export type ConfigGroupId = "flat" | "daemon" | "model" | "workspace" | "summarize";

/** Fixed group order: a Cell file's flat keys, `[daemon]`, `[model]`,
 *  workspace mapping, `[summarize]`. The flat, daemon, model and summarize
 *  groups are the file card's zones (summarize is a cli.toml
 *  table; `[model]` holds a named Cell's `model.source`, which
 *  pf lists for named Cells only); the workspace group is the 工作区 card's
 *  mapping rows. */
export const CONFIG_GROUPS: readonly ConfigGroupId[] = ["flat", "daemon", "model", "workspace", "summarize"];

/** The card an item renders in; an unknown key falls into the flat card (never dropped). */
export function configGroupOf(item: ConfigItemLike): ConfigGroupId {
  if (item.section === true) return "workspace";
  if (item.key.startsWith("summarize.")) return "summarize";
  if (item.key.startsWith("model.")) return "model";
  if (DAEMON_TABLE_KEYS.includes(item.key)) return "daemon";
  return "flat";
}

/** Items bucketed by card in CONFIG_GROUPS order; empty cards are dropped, pf's order kept inside a card. */
export function groupConfigItems<T extends ConfigItemLike>(items: readonly T[]): { id: ConfigGroupId; items: T[] }[] {
  return CONFIG_GROUPS
    .map((id) => ({ id, items: items.filter((item) => configGroupOf(item) === id) }))
    .filter((group) => group.items.length > 0);
}

/** `[workspace."<abs path>"]` → `<abs path>`; anything else → null (rendered verbatim by the caller). */
export function workspaceSectionPath(key: string): string | null {
  const match = /^\[workspace\."(.*)"\]$/.exec(key);
  return match ? match[1] : null;
}

/** A workspace section's comma-joined project list as chips; empty → []. */
export function workspaceProjects(value: unknown): string[] {
  if (typeof value !== "string") return [];
  return value.split(",").map((part) => part.trim()).filter(Boolean);
}

/** Whether a value is unset (renders the placeholder instead of a value). */
export function isUnsetValue(value: unknown): boolean {
  return value === null || value === undefined || value === "";
}

/** The minimal candidate-step shape (structurally a PolyforgeConfigStep). */
export interface ConfigStepLike {
  project?: string;
  step: string;
}

/**
 * Declared candidates grouped per project for the collapsible groups. pf builds
 * the list from Go maps, so its project/step order is unstable: groups sort by
 * project and steps by id. A step without a project lands in one group with
 * `project: null` (rendered as "all projects"), last. Each step object is
 * passed through as-is — the model order inside a step is pf's and is kept.
 */
export function groupStepsByProject<T extends ConfigStepLike>(steps: readonly T[]): { project: string | null; steps: T[] }[] {
  const byProject = new Map<string | null, T[]>();
  for (const step of steps) {
    const project = step.project ? step.project : null;
    const bucket = byProject.get(project);
    if (bucket) bucket.push(step);
    else byProject.set(project, [step]);
  }
  return [...byProject.entries()]
    .sort(([a], [b]) => (a === null ? 1 : b === null ? -1 : a.localeCompare(b)))
    .map(([project, bucket]) => ({ project, steps: [...bucket].sort((x, y) => x.step.localeCompare(y.step)) }));
}

/**
 * One workspace path's memory_policy entries in display order: `default`
 * first, then the projects sorted (pf builds the block from a Go map, so its
 * key order is unstable).
 */
export function policyEntryNames(entries: Record<string, unknown>): string[] {
  return Object.keys(entries).sort((a, b) => (a === "default" ? -1 : b === "default" ? 1 : a.localeCompare(b)));
}

/** the Add-Cell form's draft. */
export interface CellAddDraft {
  profile: string;
  server: string;
  apiKey: string;
  modelSource: string;
}

/** The first problem of an Add-Cell draft (an i18n key), or null when it may
 *  be posted. Mirrors the server's validation so nothing obviously invalid
 *  is sent; pf stays the authority. */
export function cellAddDraftError(draft: CellAddDraft): string | null {
  const profile = draft.profile.trim();
  if (!/^[a-z0-9][a-z0-9-]{0,31}$/.test(profile)) return "polyforge.cells.errProfile";
  if (profile === "default") return "polyforge.cells.errReserved";
  if (!/^https?:\/\/\S+$/.test(draft.server.trim())) return "polyforge.cells.errServer";
  if (draft.apiKey === "" || /\s/.test(draft.apiKey)) return "polyforge.cells.errApiKey";
  const source = draft.modelSource.trim();
  if (source !== "" && source !== "shared-default" && !source.startsWith("/")) return "polyforge.cells.errModelSource";
  return null;
}

/** the Host region's scoped configuration read (`--host`,
 *  never the cwd's Cell). the Host is the one layered region
 *  left — every Cell's settings are its file card. */
export const HOST_REGION_READ_URL = "/api/polyforge/config?host=1";

/** The scoped writer's body for one Host-region write: the existing
 *  `POST /api/polyforge/config` (pf's CAS on cli.toml's sha256), addressed to
 *  the Host scope. An unset carries no value. */
export function hostRegionWriteBody(action: "set" | "unset", key: string, value: unknown, fileSha256: string | null): Record<string, unknown> {
  return {
    action,
    key,
    ...(action === "set" ? { value } : {}),
    host: true,
    expect_state: { file_sha256: fileSha256 },
  };
}

/** A region row's shown value: the file's value, "" when the key is not set
 *  in that file (the default applies). api_key is never shown. */
export function regionShownValue(item: { key: string; value?: unknown; set?: boolean; source?: string } | undefined): string {
  if (!item || item.key === "api_key") return "";
  if (item.source !== undefined && item.source !== "file") return "";
  if (item.value === undefined || item.value === null) return "";
  return Array.isArray(item.value) ? item.value.join(", ") : String(item.value);
}
