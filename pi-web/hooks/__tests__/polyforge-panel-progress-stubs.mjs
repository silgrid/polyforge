// The non-fixture dependencies of components/PolyforgePanel.tsx, stubbed for
// the react-hook-shim behaviour tests that render pieces of the panel module
// (PolyforgeDetailProgress): the i18n context needs React's context hooks the
// shim has none of, SafeMarkdownBody pulls react-markdown at module load
// though the progress lines under test never render it, react-dom's
// createPortal needs a renderer, and the panel's sibling surfaces (Messages,
// Memory, Integrate, Tasks, WorkItems, Projects, the composer) are never
// reached by the piece under test. A test aliases "@/hooks/useI18n",
// "./SafeMarkdownBody", "react-dom" and the sibling component specifiers
// here.
//
// `t` returns the key, so a test asserts keys rather than English copy; the
// one object is shared by every render, so the callbacks that take `t` keep a
// stable identity across renders.
const t = (key) => key;
const i18n = { t, locale: "en" };

export function useI18n() {
  return i18n;
}

export const MAX_MARKDOWN_CHARS = 100_000;

export function SafeMarkdownBody() {
  return null;
}

export function createPortal() {
  return null;
}

export function PolyforgeMessages() {
  return null;
}

export function PolyforgeMemory() {
  return null;
}

export function PolyforgeIntegrate() {
  return null;
}

export function PolyforgeTasks() {
  return null;
}

export function PolyforgeWorkItems() {
  return null;
}

export function PolyforgeProjects() {
  return null;
}

export function PolyforgeJobComposer() {
  return null;
}
