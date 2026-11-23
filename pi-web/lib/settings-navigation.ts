/**
 * The settings rail contains the unified model page; the former legacy Models page is removed.
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
] as const;

export type SettingsSection = (typeof SETTINGS_SECTION_VALUES)[number];
export type SettingsDetailSection = Exclude<SettingsSection, "general">;

const STORAGE_KEY = "pi-web:settings-navigation";
// Sections that need a project to show anything. Settings › MCP is not one: it
// lists the global mcp.json without a project and adds a Project group with one.
// Settings › Skills is not one either (85d AC3): without a project it shows the
// user scope alone — both harnesses' user halves, no project group.
const PROJECT_SECTIONS = new Set<SettingsSection>(["agents", "plugins"]);

/**
 * `models` is the unified model page. A historical `models` selection is
 * read once and then saved under the same page key.
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
  if (section === "models") return JSON.stringify(["models", "pi"]);
  if (cwd) return JSON.stringify([section, cwd]);
  // MCP works without a project; its selection then lives under the bare name,
  // which no project key (always an array) can collide with. Skills likewise
  // (85d): the no-project listing keeps its own selection, separate from any
  // project's.
  return section === "mcp" || section === "skills" ? section : null;
}

export function getLastSettingsSection(
  cwd: string | null,
  storage: StorageLike | null = getBrowserStorage(),
): SettingsSection {
  if (!storage) return "general";
  try {
    const state = readState(storage);
    if (state.section === "legacy-models") {
      state.section = "models";
      storage.setItem(STORAGE_KEY, JSON.stringify(state));
    }
    const section = state.section;
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
    const state = readState(storage);
    if (section === "models" && typeof state.selections?.models === "string") {
      state.selections = { ...state.selections };
      if (!(key in state.selections)) state.selections[key] = state.selections.models;
      delete state.selections.models;
      storage.setItem(STORAGE_KEY, JSON.stringify(state));
    }
    const value = state.selections?.[key];
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
    if (section === "models") delete state.selections.models;
    storage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch {
    // Browser storage is best-effort.
  }
}
