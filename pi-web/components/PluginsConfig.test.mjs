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
