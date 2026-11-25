import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, {
  jsx: { runtime: "automatic" },
  tsconfigPaths: true,
});
const { filteredPackagesKeptOn, packagesToSwitch } = await jiti.import("./PluginsConfig.tsx");

const packages = [
  { source: "npm:global-on", scope: "global", disabled: false, filtered: false },
  { source: "npm:global-off", scope: "global", disabled: true, filtered: true },
  { source: "npm:project-on", scope: "project", disabled: false, filtered: false },
];

test("a group switch targets the packages that would change", () => {
  assert.deepEqual(packagesToSwitch(packages, true).map((pkg) => pkg.source), ["npm:global-off"]);
  assert.deepEqual(packagesToSwitch(packages, false).map((pkg) => pkg.source), ["npm:global-on", "npm:project-on"]);
  assert.deepEqual(packagesToSwitch(packages.filter((pkg) => !pkg.disabled), true), []);
});

test("switching a group off leaves an enabled filtered package on and says so", () => {
  // Disabling empties its resource lists, and nothing would bring the filters back.
  const withFiltered = [...packages, { source: "npm:filtered-on", scope: "global", disabled: false, filtered: true }];
  assert.deepEqual(packagesToSwitch(withFiltered, false).map((pkg) => pkg.source), ["npm:global-on", "npm:project-on"]);
  assert.deepEqual(filteredPackagesKeptOn(withFiltered).map((pkg) => pkg.source), ["npm:filtered-on"]);
});

// polyforge#259 T4 AC1 (owner 1A, review r3#2): a plugin's irreversible removal
// sits behind the detail header's ⋯ menu; the header has no bare Remove button.
const React = await jiti.import("react");
const { renderToStaticMarkup } = await jiti.import("react-dom/server");
const { I18nProvider } = await jiti.import("@/hooks/useI18n");
const { PackageDetail } = await jiti.import("./PluginsConfig.tsx");

test("a plugin's removal is behind the detail header's ⋯, with no bare Remove button (review r3#2)", () => {
  const pkg = {
    source: "npm:example-plugin",
    scope: "global",
    canCheckForUpdates: true,
    filtered: false,
    disabled: false,
    counts: { extensions: 0, skills: 0, prompts: 0, themes: 0 },
    resources: [],
    status: "loaded",
  };
  const html = renderToStaticMarkup(React.createElement(I18nProvider, null, React.createElement(PackageDetail, {
    pkg,
    cwd: "/work",
    busyKey: null,
    actionError: null,
    actionMessage: null,
    sessionId: "session-1",
    checkingUpdate: false,
    updateError: null,
    onAction: () => {},
    onCheckUpdate: () => {},
    onReloadSession: () => {},
  })));
  const actions = html.match(/<div class="config-detail-actions">([\s\S]*?)<\/div>/)?.[1];
  assert.ok(actions, "the detail header renders its actions");
  assert.match(actions, /<button[^>]*class="config-danger-menu-trigger"[^>]*aria-label="More actions"/);
  assert.doesNotMatch(html, /<button[^>]*>\s*Remove\s*<\/button>/);
  assert.doesNotMatch(html, /config-button-danger/);
});
