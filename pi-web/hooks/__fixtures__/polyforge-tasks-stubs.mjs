// The two non-fixture dependencies of components/PolyforgeTasks.tsx, stubbed
// for the react-hook-shim behaviour test: the i18n context
// needs React's useContext/createContext (the shim has neither), and
// MarkdownBody pulls react-markdown and its hooks at module load though the
// list view never renders it. A test aliases `@/hooks/useI18n` and
// `./MarkdownBody` here.
//
// `t` returns the key, so the test asserts keys (polyforge.refresh /
// polyforge.refreshing) rather than English copy. The one object is shared by
// every render, so `readList`'s useCallback([t]) identity stays stable.
const t = (key) => key;
const i18n = { t, locale: "en" };

export function useI18n() {
  return i18n;
}

export function SettingsDraftRegistration() { return null; }
// The shim renders standalone WorkflowCard; managed footer ownership is covered in the browser.
export function useSettingsDraftManaged() { return false; }

export function MarkdownBody() {
  return null;
}

// polyforge#151 (85b2, review r2 #4): the SettingsUi members PolyforgeConfig
// imports, as inert render pieces — a behavioural test aliases ./SettingsUi
// here so the module graph loads against the hook shim (the real SettingsUi
// calls createContext at load).
const inert = (name) => function Stub({ children }) { return children ?? null; };
export const ConfigButton = function StubConfigButton({ children, onClick, disabled }) {
  return { type: "button", props: { onClick, disabled, children } };
};
export const ConfigDetail = inert("ConfigDetail");
export const ConfigDetailActions = inert("ConfigDetailActions");
export const ConfigDetailNavigationContext = { Provider: function StubProvider() { return null; }, Consumer: function StubConsumer() { return null; } };
export const ConfigDetailGrid = inert("ConfigDetailGrid");
export const ConfigDetailGridRow = inert("ConfigDetailGridRow");
export const ConfigDetailHeader = inert("ConfigDetailHeader");
export const ConfigDetailHeaderInfo = inert("ConfigDetailHeaderInfo");
export const ConfigDetailStack = inert("ConfigDetailStack");
export const ConfigDetailTitle = inert("ConfigDetailTitle");
export const ConfigFooter = inert("ConfigFooter");
export const ConfigFooterStatus = inert("ConfigFooterStatus");
export const ConfigListAction = inert("ConfigListAction");
export const ConfigPanelShell = inert("ConfigPanelShell");
export const ConfigSectionTitle = inert("ConfigSectionTitle");
export const ConfigSidebar = inert("ConfigSidebar");
export const ConfigSidebarGroupLabel = inert("ConfigSidebarGroupLabel");
export const ConfigSidebarGroupStatus = inert("ConfigSidebarGroupStatus");
export const ConfigSidebarItem = inert("ConfigSidebarItem");
export const ConfigSidebarList = inert("ConfigSidebarList");
export const ConfigSidebarText = inert("ConfigSidebarText");
export const ConfigSplitView = inert("ConfigSplitView");
export const ConfigStatusDot = inert("ConfigStatusDot");
export const ConfigSwitch = inert("ConfigSwitch");

// components/AccountSettings.tsx's two heavy children, stubbed for the
// account read's hook behaviour test (polyforge#256 B3): the shell's state
// machine is what the test drives; the terminal and the login form never
// mount.
export function TerminalPanel() {
  return null;
}

export function PiAccountLogin() {
  return null;
}
