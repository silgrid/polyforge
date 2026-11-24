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

// polyforge#79 (u5): the param actions ride the same one-action write face
// — `--param <name>=<value>` and `--clear-param <name>` — and the rows
// carry the param overrides (the overlay layer's own list, with by).
test("the param actions answer one argv; a profile prefixes; a blank name or a multi-line value is invalid before any spawn", () => {
  assert.deepEqual(
    overrideActionArgv("wi_A1", { kind: "param", step: "review", name: "image", value: "reg/x:1" }),
    ["--json", "override", "wi_A1", "--step", "review", "--param", "image=reg/x:1"],
  );
  assert.deepEqual(
    overrideActionArgv("wi_A1", { kind: "clearParam", step: "review", name: "image" }),
    ["--json", "override", "wi_A1", "--step", "review", "--clear-param", "image"],
  );
  assert.deepEqual(
    overrideActionArgv("wi_A1", { kind: "param", step: "review", name: "flag", value: "" }, "lab"),
    ["--profile", "lab", "--json", "override", "wi_A1", "--step", "review", "--param", "flag="],
  );
  assert.equal(overrideActionArgv("wi_A1", { kind: "param", step: "review", name: "", value: "x" }), null, "a blank name");
  assert.equal(overrideActionArgv("wi_A1", { kind: "param", step: "review", name: "two words", value: "x" }), null, "a name with a space");
  assert.equal(overrideActionArgv("wi_A1", { kind: "param", step: "review", name: "x", value: "two\nlines" }), null, "a multi-line value");
  assert.equal(overrideActionArgv("wi_A1", { kind: "clearParam", step: "review", name: "" }), null);
  assert.equal(overrideActionArgv("wi_A1", { kind: "param", step: "review" }), null, "a param action without its name");
});

test("the rows carry the step's param overrides with the operator of each (the overlay layer only)", () => {
  const view = sanitizeOverrideView({
    work_item: "wi_A1", posture: "queued",
    steps: [{ id: "review", params: [
      { name: "flag", value: "", by: "usr_1", at: "2026-11-17T00:00:00Z" },
      { name: "image", value: "reg/x:1", by: "usr_2" },
      { name: "junk", value: 3 },
      "nope",
    ] }],
  });
  assert.deepEqual(view.steps[0].params, [
    { name: "flag", value: "", by: "usr_1", at: "2026-11-17T00:00:00Z" },
    { name: "image", value: "reg/x:1", by: "usr_2" },
    { name: "junk", value: "" },
  ]);
  const res = sanitizeOverrideWriteResult({
    work_item: "wi_A1", changed: true, posture: "queued", published: false,
    note: "queued: the params ride the work item's step_params attrs key — the claim merges them into its capsule and clears the key once that publish succeeded",
    step: { id: "review", skippable: false, params: [{ name: "image", value: "reg/x:1", by: "usr_1" }] },
  });
  assert.equal(res.posture, "queued");
  assert.ok(res.note.startsWith("queued:"));
  assert.deepEqual(res.step.params, [{ name: "image", value: "reg/x:1", by: "usr_1" }]);
});

// polyforge#270: the daemon's next-dispatch face rides the same sanitizer —
// a done or live step's model writability, the refusal reason, and the
// effect scope survive the proxy; the value block keeps its old shape (a
// row without the fields gains none).
test("the sanitizer keeps the next-dispatch model writability, reason and scope (polyforge#270)", () => {
  const view = sanitizeOverrideView({
    work_item: "wi_A1", posture: "running",
    steps: [
      { id: "build", status: "done", skippable: false, can_override_models: true, model_effect_scope: "next_dispatch",
        models: ["claude:strong"], models_by: "usr_1", effective: ["claude:strong"], source: "step override" },
      { id: "merge", status: "in_progress", skippable: true, can_override_models: false,
        model_override_reason: "step merge will not be dispatched again from the work item's current position" },
      { id: "review", status: "pending", skippable: false },
    ],
  });
  assert.deepEqual(view.steps[0], {
    id: "build", status: "done", skippable: false, models: ["claude:strong"], modelsBy: "usr_1",
    effective: ["claude:strong"], source: "step override",
    canOverrideModels: true, modelEffectScope: "next_dispatch",
  });
  assert.equal(view.steps[1].canOverrideModels, false, "a false writability is kept, not dropped");
  assert.equal(view.steps[1].modelOverrideReason, "step merge will not be dispatched again from the work item's current position");
  assert.equal(view.steps[1].modelEffectScope, undefined);
  assert.equal(view.steps[2].canOverrideModels, undefined, "a row without the field gains nothing");
  const res = sanitizeOverrideWriteResult({
    work_item: "wi_A1", changed: true, posture: "running", published: true,
    note: "the change applies on the step's next dispatch; the round that already ran is unchanged",
    step: { id: "build", status: "done", can_override_models: true, model_effect_scope: "next_dispatch", models: ["claude:strong"] },
  });
  assert.equal(res.step.canOverrideModels, true, "the write answer carries the same face");
  assert.equal(res.step.modelEffectScope, "next_dispatch");
});
