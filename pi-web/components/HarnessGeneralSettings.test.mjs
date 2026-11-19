// carries: components/HarnessGeneralSettings*, components/GeneralSettings*, lib/settings-navigation.ts
// polyforge#149 (#85 85a1b, AC1): General is a left-column layout — the three
// switchable panes 界面与对话 / 环境 / 生效值与来源 — and 界面与对话 keeps the
// former GeneralSettings content whole. Expected labels come from the ticket's
// settled three items and the locale tables, never from the implementation's
// own output; the rendered GeneralSettings content is the section headings the
// moved page already showed.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { HarnessGeneralSettings } = await jiti.import("./HarnessGeneralSettings.tsx");
const { GeneralSettings } = await jiti.import("./GeneralSettings.tsx");
const { SETTINGS_TABS } = await jiti.import("../lib/settings-navigation.ts");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");

const h = React.createElement;
const noop = () => {};

function renderShell() {
  return renderToStaticMarkup(h(I18nProvider, null, h(HarnessGeneralSettings, {
    cwd: null,
    sessionId: null,
    onSessionReloaded: noop,
    quoteSelectionEnabled: false,
    onQuoteSelectionChange: noop,
  })));
}

function renderConversation() {
  return renderToStaticMarkup(h(I18nProvider, null, h(GeneralSettings, {
    sessionId: null,
    onSessionReloaded: noop,
    quoteSelectionEnabled: false,
    onQuoteSelectionChange: noop,
  })));
}

test("General is a left-column layout with three switchable items", () => {
  const html = renderShell();
  // The shared split view: the three panes of the ticket's settled list, in order.
  assert.match(html, /class="config-split-view"/, "General is not built on the shared split view");
  const labels = [...html.matchAll(/aria-current="page"[^>]*>\s*<span class="config-sidebar-text">([^<]*)<\/span>/g)].map((match) => match[1]);
  assert.deepEqual(labels, ["Interface &amp; conversation"], "the marked sidebar row is not the first item alone");
  const items = [...html.matchAll(/class="config-sidebar-item[^"]*"[^>]*>(?:(?!<\/button>).)*?<span class="config-sidebar-text">([^<]*)<\/span>/gs)].map((match) => match[1]);
  assert.deepEqual(items, ["Interface &amp; conversation", "Environment", "Effective values &amp; sources"], `General's left column (got: ${items.join(", ")})`);
  // The default detail pane is 界面与对话, and only the active pane is mounted.
  const panes = [...html.matchAll(/data-general-pane="([^"]*)"/g)].map((match) => match[1]);
  assert.deepEqual(panes, ["conversation"], "the default detail pane is not 界面与对话 alone");
  assert.match(html, /data-general-pane="conversation"[\s\S]*?Appearance/, "the conversation pane does not show the former General content");
});

test("the moved GeneralSettings keeps the former General page's sections whole", async () => {
  const html = renderConversation();
  for (const heading of ["Appearance", "Fonts &amp; layout", "Chat", "Language"]) {
    assert.ok(html.includes(heading), `the conversation pane lost the ${heading} section`);
  }
  // The claude tool permissions block stays in General (owner ruling Q7 A). It
  // renders once its GET settles, so the mount is pinned in the source; the
  // loaded block's behaviour is the e2e suites' (claude-permission-442).
  const source = await readFile(new URL("./GeneralSettings.tsx", import.meta.url), "utf8");
  assert.match(source, /<ClaudePermissionSettings sessionId=\{sessionId\} \/>/, "the claude tool permissions block is gone from General");
});

test("the tab rail the panes live under is the nine-plus-two model", () => {
  // One fact, one place: the shell's section ids are SETTINGS_TABS' ids.
  assert.deepEqual(
    [...new Set(SETTINGS_TABS.map((tab) => tab.id))],
    ["general", "account", "models", "permission", "skills", "mcp", "agents", "plugins", "polyforge", "legacy-models", "legacy-claude"],
  );
});
