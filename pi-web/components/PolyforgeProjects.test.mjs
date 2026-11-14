// carries: components/PolyforgeProjects*, components/PolyforgePanel*, hooks/usePolyforge.*, lib/polyforge-projects*, app/api/polyforge/projects/*, lib/i18n/*
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// source pins of the Work › projects sub-tab (polyforge#30, AC7): the
// mapped projects with their global states, deactivated ones hidden by
// default behind a toggle, marked, restorable in the same row.
const source = await readFile(new URL("./PolyforgeProjects.tsx", import.meta.url), "utf8");
const panel = await readFile(new URL("./PolyforgePanel.tsx", import.meta.url), "utf8");
const jiti = createJiti(import.meta.url);
const { getLocalePlugin } = await jiti.import("../lib/i18n/registry.ts");
const locales = Object.fromEntries(["en", "zh-CN", "zh-TW"].map((id) => [id, getLocalePlugin(id).messages]));

test("reads follow the discipline: the one route (read and write), one read per opening, no timer, storage or extra write", () => {
  const urls = [...source.matchAll(/fetch\((`[^`]+`|"[^"]+")/g)].map((m) => m[1]);
  assert.deepEqual(urls, ['"/api/polyforge/projects"', '"/api/polyforge/projects"'], "only the projects route: the read and the write");
  assert.equal((source.match(/method: "POST"/g) ?? []).length, 1, "exactly one write, the restore");
  assert.doesNotMatch(source, /setInterval|setTimeout|requestAnimationFrame/, "no timer, no polling");
  assert.doesNotMatch(source, /localStorage|sessionStorage/, "nothing persisted");
  assert.match(source, /if \(listReadRef\.current\) return;\s*listReadRef\.current = true;\s*readList\(\);/, "exactly one list read per opening");
});

test("the default hides the deactivated projects; the toggle reveals them and every reveal carries the marker", () => {
  assert.match(source, /showDeactivated \|\| p\.status !== "deactivated"/, "the default view filters the deactivated rows");
  assert.match(source, /data-testid="pf-projects-toggle"/, "the「show deactivated」entry");
  assert.match(source, /data-pf-project-mark/, "the deactivated marker rides its row");
  assert.match(source, /data-testid="pf-projects-restore"/, "the in-place restore entry rides the same row");
});

test("the restore is exactly one POST through the route's action door, then the one re-read", async () => {
  const start = source.indexOf("const restore = ");
  const restore = source.slice(start, source.indexOf("readList();", start) + "readList();".length);
  assert.match(restore, /method: "POST"/, "the write is a POST");
  assert.match(restore, /action: "restore"/, "the route's action door");
  assert.match(restore, /"Content-Type": "application\/json"/, "the JSON content type, like the panel's other writes");
  assert.match(restore, /readList\(\);/, "the answer settles the view with the one re-read");
});

test("the panel wires the projects sub-tab with its label and icon", () => {
  assert.match(panel, /"jobs", "tasks", "items", "projects", "integrate"/, "the Work rail carries the projects segment");
  assert.match(panel, /tab === "projects" \? t\("polyforge\.work\.projectsTab"\)/, "the segment label");
  assert.match(panel, /workTab === "projects"/, "the body renders the projects surface");
  assert.match(panel, /projects: workTabIcon\(/, "the segment icon");
});

test("the i18n keys exist in every locale", () => {
  for (const [id, messages] of Object.entries(locales)) {
    for (const key of [
      "polyforge.work.projectsTab",
      "polyforge.projects.title",
      "polyforge.projects.showDeactivated",
      "polyforge.projects.hideDeactivated",
      "polyforge.projects.deactivated",
      "polyforge.projects.deactivatedTitle",
      "polyforge.projects.active",
      "polyforge.projects.restore",
      "polyforge.projects.restoreTitle",
      "polyforge.projects.empty",
      "polyforge.projects.error",
    ]) {
      assert.ok(messages[key], `${id} misses ${key}`);
    }
  }
});
