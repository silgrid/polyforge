import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

// the workflow card's pure row model.
const jiti = createJiti(import.meta.url);
const pure = await jiti.import("./polyforge-workflows-pure.ts");

const chain = (models, from) => ({ models, from });
const DOC = {
  workflows_dir: "/wf", daemon_reachable: true, reason: "", workspace: "/ws", projects: ["workbench", "other"],
  write_capable: true, write_reason: "",
  task: { workbench: chain(["pi:ws-default"], "steps.workbench.default.models") },
  bundles: [
    {
      name: "feature-full",
      defaults: { model_candidates: ["pi:ff-default"], effective: { workbench: chain(["pi:ws-default"], "steps.workbench.default.models") } },
      steps: [
        { id: "grill", model_candidates: ["pi:fg-grill"],
          effective: { workbench: chain(["claude:declared"], "steps.workbench.grill.models") } },
        { id: "plan", model_candidates: ["pi:fg-plan"], effective: { workbench: chain(["pi:fg-plan"], "bundle feature-full step plan model_candidates") } },
        { id: "code", model_candidates: ["pi:fg-code"], effective: { workbench: chain(["pi:fg-code"], "bundle feature-full step code model_candidates") } },
      ],
    },
    {
      name: "autopilot",
      defaults: { model_candidates: null, effective: { workbench: chain(["pi:ws-default"], "steps.workbench.default.models") } },
      steps: [
        { id: "grill", model_candidates: ["pi:auto-grill"],
          effective: { workbench: chain(["claude:declared"], "steps.workbench.grill.models") } },
        { id: "code", model_candidates: ["pi:auto-code"], effective: { workbench: chain(["pi:auto-code"], "bundle autopilot step code model_candidates") } },
      ],
    },
  ],
  bundle_errors: [],
};
const WORKSPACE = {
  workspace: "/ws", yaml: { sha256: "abc", exists: true },
  steps: {
    workbench: {
      grill: { declared: true, models: ["claude:declared"], source: "file", class: "live" },
      plan: { declared: false, models: null, source: "default", class: "live" },
      review: { declared: true, models: [], source: "file", class: "live" },
      default: { declared: true, models: ["pi:ws-default"], source: "file", class: "live" },
      task: { declared: false, models: null, source: "default", class: "live" },
    },
    other: {},
  },
};

test("the default project is the first mapped one", () => {
  assert.equal(pure.defaultProject(["workbench", "other"]), "workbench");
  assert.equal(pure.defaultProject([]), null);
});

test("rows: default first, step ids alphabetical and de-duplicated, task last", () => {
  const rows = pure.buildWorkflowRows(DOC, WORKSPACE, "workbench");
  assert.deepEqual(rows.map((row) => row.id), ["default", "code", "grill", "plan", "task"]);
  assert.equal(rows.filter((row) => row.id === "grill").length, 1, "a shared step appears once");
  const grill = rows.find((row) => row.id === "grill");
  assert.deepEqual(grill.bundles, [{ bundle: "autopilot", candidates: ["pi:auto-grill"] }, { bundle: "feature-full", candidates: ["pi:fg-grill"] }]);
  const task = rows.at(-1);
  assert.equal(task.kind, "task");
  assert.deepEqual(task.bundles, [], "task belongs to no bundle");
  assert.deepEqual(task.effective, [{ bundle: null, models: ["pi:ws-default"], from: "steps.workbench.default.models" }]);
});

test("a declared chain wins and is drawn as declared", () => {
  const grill = pure.buildWorkflowRows(DOC, WORKSPACE, "workbench").find((row) => row.id === "grill");
  assert.equal(grill.declared, true);
  assert.deepEqual(grill.declaredModels, ["claude:declared"]);
  assert.equal(grill.diverges, false);
  assert.deepEqual(grill.effective, [{ bundle: null, models: ["claude:declared"], from: "steps.workbench.grill.models" }]);
  assert.equal(grill.attrs, undefined, "a row carries no attrs");
});

test("an inheriting step shows the bundle's effective chain and its source", () => {
  const plan = pure.buildWorkflowRows(DOC, WORKSPACE, "workbench").find((row) => row.id === "plan");
  assert.equal(plan.declared, false);
  assert.equal(plan.declaredModels, null);
  assert.deepEqual(plan.effective, [{ bundle: null, models: ["pi:fg-plan"], from: "bundle feature-full step plan model_candidates" }]);
});

test("the same step id diverging across bundles keeps every bundle's chain", () => {
  const code = pure.buildWorkflowRows(DOC, WORKSPACE, "workbench").find((row) => row.id === "code");
  assert.equal(code.diverges, true);
  assert.deepEqual(code.effective.map((view) => [view.bundle, view.models[0]]), [["autopilot", "pi:auto-code"], ["feature-full", "pi:fg-code"]]);
});

test("a project with no declarations gets no declared value", () => {
  const rows = pure.buildWorkflowRows(DOC, WORKSPACE, "other");
  assert.ok(rows.every((row) => row.declared === false && row.declaredModels === null));
  const grill = rows.find((row) => row.id === "grill");
  assert.deepEqual(grill.effective, [], "no level governs other's grill in this document");
  assert.deepEqual(pure.buildWorkflowRows(null, WORKSPACE, "workbench"), []);
});

test("chain edits: add (trimmed, no duplicate), remove, move", () => {
  assert.deepEqual(pure.addCandidate(["pi:a"], "  pi:b "), ["pi:a", "pi:b"]);
  assert.deepEqual(pure.addCandidate(["pi:a"], "pi:a"), ["pi:a"]);
  assert.deepEqual(pure.addCandidate(["pi:a"], "   "), ["pi:a"]);
  assert.deepEqual(pure.removeCandidate(["pi:a", "pi:b", "pi:c"], 1), ["pi:a", "pi:c"]);
  assert.deepEqual(pure.moveCandidate(["pi:a", "pi:b", "pi:c"], 2, -1), ["pi:a", "pi:c", "pi:b"]);
  assert.deepEqual(pure.moveCandidate(["pi:a", "pi:b"], 0, -1), ["pi:a", "pi:b"]);
});

test("the module is client-safe: type-only imports", async () => {
  const source = await readFile(new URL("./polyforge-workflows-pure.ts", import.meta.url), "utf8");
  assert.doesNotMatch(source, /^\s*import\s+(?!type\b)/m);
  assert.doesNotMatch(source, /from\s+"node:|from "react"/);
});

// the project union across mapped workspaces.
test("the union labels each project with its owning workspace's yaml, keeps the mapping order and drops failed workspaces", () => {
  const a = { ...DOC, workspace: "/a", projects: ["polyforge", "pi"] };
  const b = { ...DOC, workspace: "/b", projects: ["workbench", "pi"] };
  const union = pure.workflowUnion({
    workflows: b, workspace: null,
    workspaces: [
      { workspace: "/a", workflows: a, declared: { workspace: "/a", yaml: { path: "/a/.polyforge.yaml", sha256: "sa", exists: true }, steps: {} } },
      { workspace: "/b", workflows: b, declared: null },
      { workspace: "/c", workflows: null, declared: null, reason: "permission denied" },
    ],
  });
  assert.deepEqual(union.projects.map((entry) => [entry.project, entry.owner.workspace]), [["polyforge", "/a"], ["pi", "/a"], ["workbench", "/b"]], "a duplicate project stays with the first workspace listing it");
  assert.deepEqual(union.sources.map((entry) => entry.yamlPath), ["/a/.polyforge.yaml", "/b/.polyforge.yaml"], "a missing yaml path falls back to the workspace's own");
  assert.deepEqual(union.failures.map((entry) => [entry.workspace, entry.reason]), [["/c", "permission denied"]]);
  assert.equal(pure.projectOwner(union, "workbench").workspace, "/b");
  assert.equal(pure.projectOwner(union, "demo"), null, "a failed workspace's project has no owner, so no write");
});

test("an answer without workspaces[] is one workspace built from its top-level fields", () => {
  const union = pure.workflowUnion({ workflows: DOC, workspace: WORKSPACE });
  assert.equal(union.sources.length, 1);
  assert.deepEqual(union.projects.map((entry) => entry.project), ["workbench", "other"]);
  assert.equal(union.projects[0].owner.declared, WORKSPACE);
  assert.deepEqual(pure.workflowUnion(null), { projects: [], sources: [], failures: [] });
});

// a step its lane declares `skippable` carries the switch,
// and each project's current `skip` (pf's judgment, never the panel's).
test(" rows carry skippable and skip", async () => {
  const lib = await jiti.import("./polyforge-workflows.ts");
  const raw = {
    workflows_dir: "/wf", daemon_reachable: true, reason: "", workspace: "/ws", projects: ["alpha", "beta"],
    write_capable: true, write_reason: "",
    bundles: [
      {
        name: "develop", defaults: { model_candidates: null },
        steps: [
          { id: "review", skippable: "true", skip: { alpha: true } },
          { id: "merge", skippable: true, skip: { alpha: true, beta: false, gamma: "yes" } },
          { id: "publish", skippable: false },
        ],
      },
      { name: "micro", defaults: { model_candidates: null }, steps: [{ id: "merge" }, { id: "publish" }] },
    ],
    bundle_errors: [],
  };
  const doc = lib.sanitizeWorkflowsDoc(raw);
  const [develop, micro] = doc.bundles;
  const step = (bundle, id) => bundle.steps.find((s) => s.id === id);
  assert.equal(step(develop, "merge").skippable, true);
  assert.deepEqual(step(develop, "merge").skip, { alpha: true, beta: false }, "skip keeps project → boolean only");
  assert.equal(step(develop, "review").skippable, undefined, "a non-boolean skippable is dropped");
  assert.equal(step(develop, "publish").skippable, undefined, "skippable is kept only when true");
  assert.equal(step(develop, "publish").skip, undefined);
  assert.equal(step(micro, "merge").skippable, undefined);
  assert.equal(step(micro, "merge").skip, undefined);

  const rowsOf = (project) => Object.fromEntries(pure.buildWorkflowRows(doc, null, project).map((row) => [row.id, row]));
  const alpha = rowsOf("alpha");
  assert.equal(alpha.merge.skippable, true, "a step any lane declares skippable is skippable");
  assert.equal(alpha.merge.skip, true, "the project's current skip");
  assert.equal(rowsOf("beta").merge.skip, false);
  assert.equal(rowsOf("beta").merge.skippable, true);
  for (const id of ["default", "review", "publish", "task"]) {
    assert.equal(alpha[id].skippable, false, `${id} is not skippable`);
    assert.equal(alpha[id].skip, false, `${id} is not skipped`);
  }
});
