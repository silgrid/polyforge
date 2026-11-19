/**
 * The settings rail (85a1b AC1; 85a4 removed the legacy Claude page): nine
 * new sections plus the one legacy-marked old Models page, in their settled
 * order. `legacy` marks the temporary Models page (owner ruling Q1 A): 85a3
 * deletes `legacy-models`, and everything that names it — this table, the
 * `legacy-models` selection key and SettingsPanel's mount — goes with it.
 */
export const SETTINGS_TABS = [
  { id: "general", labelKey: "settings.general" },
  { id: "account", labelKey: "settings.tabAccount" },
  { id: "models", labelKey: "common.models" },
  { id: "permission", labelKey: "settings.tabPermission" },
  { id: "skills", labelKey: "common.skills" },
  { id: "mcp", labelKey: "settings.mcp" },
  { id: "agents", labelKey: "common.agents" },
  { id: "plugins", labelKey: "common.plugins" },
  { id: "polyforge", labelKey: "polyforge.title" },
  { id: "legacy-models", labelKey: "settings.tabLegacyModels", legacy: true },
] as const;

export const SETTINGS_SECTION_VALUES = [
  "general",
  "account",
  "models",
  "permission",
  "skills",
  "mcp",
  "agents",
  "plugins",
  "polyforge",
  "legacy-models",
] as const;

export type SettingsSection = (typeof SETTINGS_SECTION_VALUES)[number];
export type SettingsDetailSection = Exclude<SettingsSection, "general">;

const STORAGE_KEY = "pi-web:settings-navigation";
// Sections that need a project to show anything. Settings › MCP is not one: it
// lists the global mcp.json without a project and adds a Project group with one.
const PROJECT_SECTIONS = new Set<SettingsSection>(["skills", "agents", "plugins"]);

/**
 * `models` keeps its spelling: it is the new Models concept page's id now,
 * and the old Models page lives under `legacy-models`. The `claude` key of
 * the deleted Claude page is gone with the page (85a4, as the 85a1b comment
 * promised): a stored value it leaves reads as unknown and lands on 常规.
 */

/** Whether a section is unavailable until a project is open. */
export function settingsSectionRequiresProject(section: SettingsSection): boolean {
  return PROJECT_SECTIONS.has(section);
}

interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

interface SettingsNavigationState {
  section?: string;
  selections?: Record<string, string>;
}

function getBrowserStorage(): StorageLike | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function isSettingsSection(value: unknown): value is SettingsSection {
  return typeof value === "string"
    && SETTINGS_SECTION_VALUES.includes(value as SettingsSection);
}

function readState(storage: StorageLike): SettingsNavigationState {
  const raw = storage.getItem(STORAGE_KEY);
  if (!raw) return {};
  try {
    const parsed: unknown = JSON.parse(raw);
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    const state = parsed as SettingsNavigationState;
    return {
      section: typeof state.section === "string" ? state.section : undefined,
      selections: state.selections !== null
        && typeof state.selections === "object"
        && !Array.isArray(state.selections)
        ? state.selections
        : undefined,
    };
  } catch {
    return {};
  }
}

function selectionKey(section: SettingsDetailSection, cwd?: string | null): string | null {
  // The legacy Models page (85a1b) keeps the historical global "models"
  // selection key, so a stored provider/model selection survives the temporary
  // id; removed with 85a3 (删除旧版模型页时一并移除).
  if (section === "legacy-models") return "models";
  if (cwd) return JSON.stringify([section, cwd]);
  // MCP works without a project; its selection then lives under the bare name,
  // which no project key (always an array) can collide with.
  return section === "mcp" ? section : null;
}

export function getLastSettingsSection(
  cwd: string | null,
  storage: StorageLike | null = getBrowserStorage(),
): SettingsSection {
  if (!storage) return "general";
  try {
    const section = readState(storage).section;
    if (!isSettingsSection(section)) return "general";
    return settingsSectionRequiresProject(section) && !cwd ? "general" : section;
  } catch {
    return "general";
  }
}

export function setLastSettingsSection(
  section: SettingsSection,
  storage: StorageLike | null = getBrowserStorage(),
): void {
  if (!storage) return;
  try {
    const state = readState(storage);
    state.section = section;
    storage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch {
    // Browser storage is best-effort.
  }
}

export function getLastSettingsSelection(
  section: SettingsDetailSection,
  cwd?: string | null,
  storage: StorageLike | null = getBrowserStorage(),
): string | null {
  if (!storage) return null;
  const key = selectionKey(section, cwd);
  if (!key) return null;
  try {
    const value = readState(storage).selections?.[key];
    return typeof value === "string" && value.length > 0 ? value : null;
  } catch {
    return null;
  }
}

export function setLastSettingsSelection(
  section: SettingsDetailSection,
  value: string,
  cwd?: string | null,
  storage: StorageLike | null = getBrowserStorage(),
): void {
  if (!storage || !value) return;
  const key = selectionKey(section, cwd);
  if (!key) return;
  try {
    const state = readState(storage);
    state.selections = { ...state.selections, [key]: value };
    storage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch {
    // Browser storage is best-effort.
  }
}
