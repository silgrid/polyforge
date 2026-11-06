import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const pure = await jiti.import("./polyforge-work-items-pure.ts");

const row = (n, project, status, created_at, extra = {}) => ({
  id: `wi_ROW${n}`, slug: `${project}#${n}`, project, status, priority: "normal", bundle_name: null,
  goal: `goal ${n}`, created_at, ...extra,
});

test("a work-item id is wi_ + alphanumerics and nothing else", () => {
  for (const good of ["wi_NIUXmUdZVw2Q", "wi_7oKHIcJ3v7Ue", "wi_1"]) assert.equal(pure.isPolyforgeWorkItemId(good), true, good);
  for (const bad of ["wi_", "wi-abc", "not-a-wi-id", "wi_abc/../x", "wi_abc def", "WI_abc", "", 7, null, undefined]) {
    assert.equal(pure.isPolyforgeWorkItemId(bad), false, `${bad} is not a work-item id`);
  }
});

test("the seven real statuses are offered in order and the cap is 500", () => {
  assert.deepEqual([...pure.POLYFORGE_WORK_ITEM_STATUSES], ["queued", "running", "paused", "blocked", "wrapped", "failed", "canceled"]);
  assert.equal(pure.WORK_ITEMS_PROJECT_CAP, 500);
});

test("newest first is global, stable for equal instants, unparseable last in pf's order", () => {
  const rows = [
    row(1, "ieops", "wrapped", "2026-10-01T00:00:00Z"),
    row(2, "ieops", "wrapped", "not a time"),
    row(3, "workbench", "queued", "2026-10-03T00:00:00Z"),
    row(4, "workbench", "queued", "2026-10-02T00:00:00Z"),
    row(5, "polyforge", "running", "2026-10-02T00:00:00Z"),
    row(6, "polyforge", "running", null),
    row(7, "pi", "failed", "2026-10-02T00:00:00.000000000Z"),
  ];
  const sorted = pure.sortWorkItemsNewestFirst(rows).map((entry) => entry.id);
  assert.deepEqual(sorted, ["wi_ROW3", "wi_ROW4", "wi_ROW5", "wi_ROW7", "wi_ROW1", "wi_ROW2", "wi_ROW6"]);
  assert.equal(rows[0].id, "wi_ROW1", "the input is never mutated");
});

test("project options are the distinct projects of the loaded list, sorted", () => {
  const rows = [row(1, "workbench", "queued", null), row(2, "ieops", "queued", null), row(3, "workbench", "queued", null), row(4, null, "queued", null)];
  assert.deepEqual(pure.workItemProjectOptions(rows), ["ieops", "workbench"]);
});

test("the cap hint fires at exactly 500 rows of one project, never 499 or 501", () => {
  const many = (project, count) => Array.from({ length: count }, (_, i) => row(i, project, "wrapped", null));
  assert.deepEqual([...pure.workItemsAtCap([...many("ieops", 500), ...many("pi", 3)])], ["ieops"]);
  assert.equal(pure.workItemsAtCap(many("ieops", 499)).size, 0);
  assert.equal(pure.workItemsAtCap(many("ieops", 501)).size, 0);
});

test("one combined predicate: slug/goal/project search, case-insensitive, never content; project and status compose", () => {
  const rows = [
    row(1, "workbench", "wrapped", null, { goal: "GOAL-A", content: "SECRET-CONTENT" }),
    row(2, "polyforge", "running", null, { goal: "GOAL-B" }),
    row(3, "workbench", "canceled", null, { goal: "GOAL-C", slug: "task#77" }),
    row(4, "ieops", "failed", null, { goal: null }),
  ];
  const ids = (filter) => pure.filterWorkItemRows(rows, { search: "", project: "", status: "all", ...filter }).map((entry) => entry.id);
  assert.deepEqual(ids({}), ["wi_ROW1", "wi_ROW2", "wi_ROW3", "wi_ROW4"]);
  assert.deepEqual(ids({ search: "secret-content" }), [], "content is never searched");
  assert.deepEqual(ids({ search: "goal-b" }), ["wi_ROW2"], "goal, case-insensitive");
  assert.deepEqual(ids({ search: "#77" }), ["wi_ROW3"], "slug");
  assert.deepEqual(ids({ search: "IEOPS" }), ["wi_ROW4"], "project");
  assert.deepEqual(ids({ search: "workbench", status: "canceled" }), ["wi_ROW3"]);
  assert.deepEqual(ids({ project: "polyforge" }), ["wi_ROW2"]);
});

test("the running count is the rows whose status is exactly running", () => {
  assert.equal(pure.workItemsRunningCount([row(1, "a", "running", null), row(2, "a", "Running", null), row(3, "a", null, null), row(4, "b", "running", null)]), 2);
  assert.equal(pure.workItemsRunningCount([]), 0);
});

test("the pure module imports nothing from Node", async () => {
  const source = await readFile(new URL("./polyforge-work-items-pure.ts", import.meta.url), "utf8");
  assert.doesNotMatch(source, /from "(node:|fs|child_process|path|os)/);
  assert.doesNotMatch(source, /\(\?<[=!]/, "no RegExp lookbehind");
});
