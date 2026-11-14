// carries: lib/polyforge-overrides*, app/api/polyforge/override/*
// lib/polyforge-overrides.test.mjs (u8, polyforge#25): the pure halves of
// the override face — one action's argv (exactly one, the step required,
// the chain comma-joined and free of commas inside a candidate) and the
// wire sanitization of the view and the write result.
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { overrideActionArgv, sanitizeOverrideView, sanitizeOverrideWriteResult, POLYFORGE_OVERRIDE_MINIMUM_PF_VERSION } =
  await jiti.import("./polyforge-overrides.ts");

test("one action answers one argv: models comma-joined in order, clear-models, skip, unskip; a profile prefixes", () => {
  assert.deepEqual(
    overrideActionArgv("wi_A1", { kind: "models", step: "verification", models: ["claude:m-x:high", "pi:prov/m-a"] }),
    ["--json", "override", "wi_A1", "--step", "verification", "--models", "claude:m-x:high,pi:prov/m-a"],
  );
  assert.deepEqual(
    overrideActionArgv("wi_A1", { kind: "clearModels", step: "merge" }),
    ["--json", "override", "wi_A1", "--step", "merge", "--clear-models"],
  );
  assert.deepEqual(
    overrideActionArgv("wi_A1", { kind: "skip", step: "merge", value: true }),
    ["--json", "override", "wi_A1", "--step", "merge", "--skip"],
  );
  assert.deepEqual(
    overrideActionArgv("wi_A1", { kind: "skip", step: "merge", value: false }),
    ["--json", "override", "wi_A1", "--step", "merge", "--unskip"],
  );
  assert.deepEqual(
    overrideActionArgv("wi_A1", { kind: "models", step: "merge", models: ["pi:prov/m-a"] }, "lab"),
    ["--profile", "lab", "--json", "override", "wi_A1", "--step", "merge", "--models", "pi:prov/m-a"],
  );
});

test("an invalid action answers null (validated before any spawn): no step, an empty or comma-bearing chain, an unknown kind", () => {
  assert.equal(overrideActionArgv("wi_A1", { kind: "models", step: "", models: ["pi:a"] }), null);
  assert.equal(overrideActionArgv("wi_A1", { kind: "models", step: "merge", models: [] }), null);
  assert.equal(overrideActionArgv("wi_A1", { kind: "models", step: "merge", models: ["pi:a,b"] }), null);
  assert.equal(overrideActionArgv("wi_A1", { kind: "models", step: "merge", models: ["  "] }), null);
  assert.equal(overrideActionArgv("wi_A1", { kind: "nope", step: "merge" }), null);
  assert.equal(overrideActionArgv("wi_A1", null), null);
});

test("the view's wire sanitization keeps the lane order and each row's facts, dropping junk", () => {
  const view = sanitizeOverrideView({
    work_item: "wi_A1",
    posture: "running",
    steps: [
      { id: "build", status: "in_progress", skippable: false },
      { id: "merge", status: "pending", skippable: true, models: ["pi:prov/m-a"], models_by: "usr_1", skip: true, skip_by: "usr_2", effective: ["pi:prov/m-a"], source: "step override" },
      { id: "review", status: "not started", pending: true, effective: [], source: "" },
    ],
  });
  assert.deepEqual(view, {
    wi: "wi_A1",
    posture: "running",
    steps: [
      { id: "build", status: "in_progress", skippable: false },
      { id: "merge", status: "pending", skippable: true, models: ["pi:prov/m-a"], modelsBy: "usr_1", skip: true, skipBy: "usr_2", effective: ["pi:prov/m-a"], source: "step override" },
      { id: "review", status: "not started", skippable: false, pending: true },
    ],
  });
  assert.equal(sanitizeOverrideView(null), null);
  assert.equal(sanitizeOverrideView({ work_item: "", posture: "running", steps: [] }), null, "no work item names no view");
  assert.equal(sanitizeOverrideView({ work_item: "wi_A1", posture: "running", steps: [{ id: "" }] }), null, "a row without an id is no view");
  assert.equal(
    sanitizeOverrideView({ work_item: "wi_A1", posture: "running", steps: [{ id: "x", models: "not-a-list", skip: "yes", skippable: 1 }] })?.steps[0].models,
    undefined,
    "a non-list chain and a non-boolean skip drop",
  );
});

test("the write result's sanitization keeps the outcome and masks the note; junk answers null", () => {
  const res = sanitizeOverrideWriteResult({
    work_item: "wi_A1", changed: true, posture: "blocked", published: false,
    note: "recorded locally; it lands with the next claim of the work item here",
    step: { id: "review", status: "in_progress", skippable: false, models: ["pi:prov/m-b"], models_by: "usr_1", pending: true },
  });
  assert.deepEqual(res, {
    wi: "wi_A1", changed: true, posture: "blocked", published: false,
    note: "recorded locally; it lands with the next claim of the work item here",
    step: { id: "review", status: "in_progress", skippable: false, models: ["pi:prov/m-b"], modelsBy: "usr_1", pending: true },
  });
  assert.equal(sanitizeOverrideWriteResult({ work_item: "wi_A1" }), null, "no step row is no answer");
  assert.equal(sanitizeOverrideWriteResult("nope"), null);
  const masked = sanitizeOverrideWriteResult({
    work_item: "wi_A1", changed: true, posture: "running", published: true,
    step: { id: "x" }, note: "token=SECRET-CAPSULE-KEY sk-abc",
  });
  assert.ok(!masked.note.includes("SECRET"), "the note is masked");
});

test("the capability floor is declared for the panel's gate", () => {
  assert.equal(typeof POLYFORGE_OVERRIDE_MINIMUM_PF_VERSION, "string");
  assert.match(POLYFORGE_OVERRIDE_MINIMUM_PF_VERSION, /^\d+\.\d+\.\d+$/);
});
