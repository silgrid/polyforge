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

export function MarkdownBody() {
  return null;
}