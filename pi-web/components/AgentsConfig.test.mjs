import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import ts from "typescript";
import vm from "node:vm";

const source = await readFile(new URL("./AgentsConfig.tsx", import.meta.url), "utf8");

test("editor draft preserves named and empty selections independently of activation", () => {
  const declaration = source.slice(source.indexOf("function editableProfile("), source.indexOf("function profileKey("));
  const code = ts.transpileModule(declaration, { compilerOptions: { target: ts.ScriptTarget.ES2020 } }).outputText;
  const editable = vm.runInNewContext(`${code}; editableProfile`);
  for (const skills of [["review", "audit"], []]) {
    const draft = editable({ name: "reviewer", tools: [], loadSkills: false, skills });
    assert.equal(draft.loadSkills, false);
    assert.deepEqual(Array.from(draft.skills ?? ["LOST"]), skills);
    draft.skills.push("new");
    assert.notEqual(draft.skills.length, skills.length);
  }
  for (const extensions of [["codegraph"], []]) {
    const draft = editable({ name: "reviewer", tools: [], loadExtensions: true, extensions });
    assert.deepEqual(Array.from(draft.extensions ?? ["LOST"]), extensions);
  }
});

test("draft backfills MCP as off and keeps an explicit choice", () => {
  const declaration = source.slice(source.indexOf("function editableProfile("), source.indexOf("function profileKey("));
  const code = ts.transpileModule(declaration, { compilerOptions: { target: ts.ScriptTarget.ES2020 } }).outputText;
  const editable = vm.runInNewContext(`${code}; editableProfile`);
  assert.equal(editable({ name: "legacy", tools: [], loadSkills: false, loadExtensions: false }).mcp, "off");
  for (const mcp of ["on", "off"]) {
    assert.equal(editable({ name: "reviewer", tools: [], loadSkills: false, loadExtensions: false, mcp }).mcp, mcp);
  }
});

test("draft backfills code mode for legacy profiles and keeps an explicit choice", () => {
  const declaration = source.slice(source.indexOf("function editableProfile("), source.indexOf("function profileKey("));
  const code = ts.transpileModule(declaration, { compilerOptions: { target: ts.ScriptTarget.ES2020 } }).outputText;
  const editable = vm.runInNewContext(`${code}; editableProfile`);
  // Profiles written before the setting existed have no value: code mode stays disabled.
  assert.equal(editable({ name: "legacy", tools: [], loadSkills: false, loadExtensions: false }).codemode, "off");
  // An explicit choice survives the draft, so duplicating a profile keeps it too.
  for (const codemode of ["on", "off", "inherit"]) {
    const draft = editable({ name: "reviewer", tools: [], loadSkills: false, loadExtensions: false, codemode });
    assert.equal(draft.codemode, codemode);
  }
});

test("a profile model's pi :level suffix names its base model; a listed colon id matches whole", () => {
  const constant = source.slice(source.indexOf("const THINKING_OPTIONS"), source.indexOf("\n", source.indexOf("const THINKING_OPTIONS")));
  const declaration = source.slice(source.indexOf("function profileModelBase("), source.indexOf("function profileKey("));
  const code = ts.transpileModule(`${constant}\n${declaration}`, { compilerOptions: { target: ts.ScriptTarget.ES2020 } }).outputText;
  const base = vm.runInNewContext(`${code}; profileModelBase`);
  const listed = new Set(["openai/gpt-5", "ollama/qwen3:8b", "local/tuned:high"]);
  const isListed = (reference) => listed.has(reference);
  assert.equal(base("openai/gpt-5:high", isListed), "openai/gpt-5");
  assert.equal(base("openai/gpt-5", isListed), "openai/gpt-5");
  assert.equal(base("ollama/qwen3:8b", isListed), "ollama/qwen3:8b");
  assert.equal(base("local/tuned:high", isListed), "local/tuned:high");
  assert.equal(base("openai/gpt-5:turbo", isListed), "openai/gpt-5:turbo");
  assert.equal(base("openai/gpt-5:", isListed), "openai/gpt-5:");
});

// polyforge#259 T4 AC1 (owner 1A, review r3#2): deleting a saved subagent
// profile (writable scope, edit mode) sits behind the detail header's ⋯ menu;
// the header has no bare Delete button.
const { createJiti } = await import("jiti");
const jiti = createJiti(import.meta.url, { jsx: { runtime: "automatic" }, tsconfigPaths: true });
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { AgentDetailActions } = await jiti.import("./AgentsConfig.tsx");

test("a subagent profile's deletion is behind the detail header's ⋯, with no bare Delete button (review r3#2)", () => {
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(AgentDetailActions, {
    canDuplicate: true,
    deletableName: "Reviewer",
    busy: false,
    enabled: true,
    switchDisabled: false,
    onDuplicate: () => {},
    onDelete: () => {},
    onToggle: () => {},
  })));
  const actions = html.match(/<div class="config-detail-actions">([\s\S]*?)<\/div>/)?.[1];
  assert.ok(actions, "the detail header renders its actions");
  assert.match(actions, /<button[^>]*>Duplicate<\/button><button[^>]*class="config-danger-menu-trigger"[^>]*aria-label="More actions"/);
  assert.doesNotMatch(html, /<button[^>]*>\s*Delete\s*<\/button>/);
  assert.doesNotMatch(html, /config-button-danger/);
});

// polyforge#271 AC2 (B1, Q7 A): the built-in sub-agent settings are a sidebar
// destination, not a banner above the list; the profile editor is unchanged and
// a missing profile still opens the settings pane. Navigating there sends no
// write — the pane's switch and concurrency field are the only writers.
test("agents settings is a sidebar destination", async () => {
  const { AgentFeatureSettings } = await jiti.import("./AgentsConfig.tsx");
  const page = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement((await jiti.import("./AgentsConfig.tsx")).AgentsConfig, {
    cwd: "/tmp/project", onClose: () => {}, embedded: true,
  })));
  assert.match(page, /data-agents-nav="settings"/, "the sidebar carries its own settings item");
  assert.ok(page.indexOf("agents-feature-setting") > page.indexOf("config-split-view"), "the settings block lives inside the sidebar detail, not above the split view");
  assert.match(page, /data-agents-settings-pane=""/, "the selected settings detail renders the block");
  assert.match(page, /data-agents-nav="settings"[^>]*aria-current="page"|aria-current="page"[^>]*data-agents-nav="settings"/, "the settings item is the default selection when no profile is valid");

  const settings = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(AgentFeatureSettings, {
    builtInEnabled: true, maxConcurrent: 4, settingsLoading: false, settingsSaving: false,
    reloadNeeded: true, reloading: false, sessionId: "s1",
    onToggle: () => {}, onMaxConcurrent: () => {}, onReload: () => {},
  })));
  assert.match(settings, /role="switch"/, "the built-in sub-agent switch lives in the settings pane");
  assert.match(settings, /type="number"/, "the concurrency field lives in the settings pane");
  assert.match(settings, /data-agents-reload/, "the reload notice and action move with the settings");
});

// polyforge#271 round 2 (review r2#2): the concurrency field shows the value
// read back from the server and a blur that changes nothing sends no write.
test("the concurrency field shows the read-back value and an unchanged blur sends no write", async () => {
  const { AgentFeatureSettings, concurrencyCommit } = await jiti.import("./AgentsConfig.tsx");
  const render = (maxConcurrent) => renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(AgentFeatureSettings, {
    builtInEnabled: true, maxConcurrent, settingsLoading: false, settingsSaving: false,
    reloadNeeded: false, reloading: false, sessionId: null,
    onToggle: () => {}, onMaxConcurrent: () => {}, onReload: () => {},
  })));
  assert.match(render(4), /value="4"/, "the input shows the value read back, not the initial default");
  assert.match(render(6), /value="6"/, "a later read-back remounts the input with the new value");
  assert.equal(concurrencyCommit("4", 4), null, "an unchanged blur writes nothing");
  assert.equal(concurrencyCommit("6", 4), 6, "a changed value is committed");
  assert.equal(concurrencyCommit("0", 4), null, "an out-of-range value is not written");
  assert.equal(concurrencyCommit("", 4), null, "an empty value is not written");
});

// polyforge#271 round 3 (review r3#1): driven through the hook shim, the
// concurrency field follows the value read back from the server — a 10 shown
// before the read must become 4 once the read answers 4 — and a blur that
// leaves the read-back value alone writes nothing; an edit writes once.
test("the concurrency field follows the read-back value through a re-render", async (t) => {
  const { mkdtempSync, rmSync, writeFileSync } = await import("node:fs");
  const { join } = await import("node:path");
  const { tmpdir } = await import("node:os");
  const fixture = (name) => new URL(`../hooks/__fixtures__/${name}`, import.meta.url).pathname;
  const lib = (name) => new URL(`../lib/${name}`, import.meta.url).pathname;
  const stubDir = mkdtempSync(join(tmpdir(), "pf271-agents-stub-"));
  t.after(() => rmSync(stubDir, { recursive: true, force: true }));
  const stubs = join(stubDir, "stubs.mjs");
  writeFileSync(stubs, `const t = (key) => key;
const i18n = { t, locale: "en" };
export function useI18n() { return i18n; }
export function useIsMobile() { return false; }
const inert = function Stub({ children }) { return children ?? null; };
for (const name of []) void name;
export const ConfigButton = function Btn({ children, onClick, disabled }) { return { type: "button", props: { onClick, disabled, children } }; };
export const ConfigDetail = inert, ConfigDetailActions = inert, ConfigDetailHeader = inert, ConfigDetailHeaderInfo = inert,
  ConfigDetailStack = inert, ConfigDetailTitle = inert, ConfigEmptyState = inert, ConfigField = inert, ConfigFooter = inert,
  ConfigListAction = inert, ConfigPanelShell = inert, ConfigSaveFooter = inert, ConfigSaveTarget = inert, ConfigScopeTag = inert,
  ConfigSidebar = inert, ConfigSidebarGroupLabel = inert, ConfigSidebarItem = inert, ConfigSidebarList = inert,
  ConfigSidebarText = inert, ConfigSplitView = inert, ConfigStatusDot = inert, ConfigSwitch = inert,
  ConfigDangerMenu = inert, ModelSelector = inert;
`);
  const shim = createJiti(import.meta.url, {
    jsx: { runtime: "automatic" },
    moduleCache: false,
    alias: {
      react: fixture("react-hook-shim.mjs"),
      "react/jsx-runtime": fixture("react-jsx-runtime.mjs"),
      "@/hooks/useI18n": stubs,
      "@/hooks/useIsMobile": stubs,
      "@/lib/settings-dirty": lib("settings-dirty.ts"),
      "@/lib/agent-client": lib("agent-client.ts"),
      "@/lib/display-path": lib("display-path.ts"),
      "@/lib/subagent-profile-precedence": lib("subagent-profile-precedence.ts"),
      "@/lib/settings-navigation": lib("settings-navigation.ts"),
      "./SettingsUi": stubs,
      "./ConfigDangerMenu": stubs,
      "./ModelSelector": stubs,
    },
  });
  const { renderHook } = await import(fixture("react-hook-shim.mjs"));
  const { AgentFeatureSettings } = await shim.import("./AgentsConfig.tsx");
  const writes = [];
  const props = (maxConcurrent) => ({
    builtInEnabled: true, maxConcurrent, settingsLoading: false, settingsSaving: false,
    reloadNeeded: false, reloading: false, sessionId: null,
    onToggle: () => {}, onMaxConcurrent: (value) => writes.push(value), onReload: () => {},
  });
  const walk = (node, out = []) => {
    if (Array.isArray(node)) { for (const child of node) walk(child, out); return out; }
    if (!node || typeof node !== "object" || !("props" in node)) return out;
    if (typeof node.type === "function") return walk(node.type(node.props), out);
    out.push(node);
    walk(node.props.children, out);
    return out;
  };
  const input = (hook) => walk(hook.result).find((node) => node.type === "input");
  const hook = renderHook(AgentFeatureSettings, props(10));
  hook.rerender(props(4));
  assert.equal(String(input(hook).props.value), "4", "the field still shows the value from before the read");
  input(hook).props.onBlur({ target: { value: String(input(hook).props.value) } });
  assert.deepEqual(writes, [], "a blur on the read-back value wrote to the server");
  input(hook).props.onChange({ target: { value: "6" } });
  hook.flush();
  assert.equal(String(input(hook).props.value), "6", "an edit is shown");
  input(hook).props.onBlur({ target: { value: "6" } });
  assert.deepEqual(writes, [6], "an edit is written once");
  hook.unmount();
});
