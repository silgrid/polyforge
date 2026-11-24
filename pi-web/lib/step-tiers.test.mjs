// carries: lib/step-tiers.ts
// polyforge#151 (85b2, u3): the tier catalog's pure projection — the facts
// the models page's column, the shared picker and the write plan are built
// from, with pf's [step_tiers] contract as the only truth (one
// step_tiers.<tier> key per tier, its value a list of
// <harness>:<model>[:<effort>] candidates; an empty list is refused, so a
// vacated tier is unset). Expected values below are hand-computed from that
// contract and the ticket's settled rulings: add-before-remove (a crash
// between the two keys leaves a duplicate, never a loss), effort entries
// moving as they are (never merged, never dropped),「应用到：全部」expanded by
// the page into one plan per harness, and the picker's default tier first.
import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const m = await jiti.import("./step-tiers.ts");

test("the module is browser-safe: its only import is the fellow schema module", async () => {
  const source = await readFile(new URL("./step-tiers.ts", import.meta.url), "utf8");
  assert.doesNotMatch(source, /^\s*import\s+(?!type\b)(?!.*polyforge-config-schema)/m, "no runtime import but the schema module");
  assert.doesNotMatch(source, /from\s+"node:/, "no node built-in");
  assert.doesNotMatch(source, /\brequire\(/, "no require");
});

test("parseStepTierCandidate mirrors pf's candidate grammar", () => {
  assert.deepEqual(m.parseStepTierCandidate("pi:acme/one"), { harness: "pi", model: "acme/one", effort: "" });
  assert.deepEqual(m.parseStepTierCandidate(" pi:acme/one:high "), { harness: "pi", model: "acme/one", effort: "high" });
  assert.deepEqual(m.parseStepTierCandidate("claude:opus"), { harness: "claude", model: "opus", effort: "" });
  // claude's own names may carry colons: only a valid effort suffix splits
  assert.deepEqual(m.parseStepTierCandidate("claude:us.anthropic.claude-x-v1:0"), { harness: "claude", model: "us.anthropic.claude-x-v1:0", effort: "" });
  // pf keeps a trailing colon (only a valid effort suffix splits — harness.go)
  assert.deepEqual(m.parseStepTierCandidate("pi:acme/one:"), { harness: "pi", model: "acme/one:", effort: "" });
  for (const bad of ["", "pi:", "pi: :high", "foo:model", "acme/one", "pi:acme/one:high:low", "claude:opus:off", "x"]) {
    assert.equal(m.parseStepTierCandidate(bad), null, `refused: ${JSON.stringify(bad)}`);
  }
});

test("stepTierItems takes the step_tiers family of one config list, nothing else", () => {
  const items = m.stepTierItems([
    { key: "step_tiers.default", value: ["pi:a/one", "claude:sonnet"] },
    { key: "step_tiers.fast-chat", value: ["pi:b/two"] },
    { key: "step_tiers.Bad-Name", value: ["pi:a/one"] },
    { key: "step_tiers.novalue" },
    { key: "summarize.models", value: ["pi:a/one"] },
    { key: "max_concurrent_jobs", value: "6" },
  ]);
  assert.deepEqual(items, [
    { tier: "default", candidates: ["pi:a/one", "claude:sonnet"] },
    { tier: "fast-chat", candidates: ["pi:b/two"] },
  ]);
});

const CATALOG = [
  { tier: "default", candidates: ["pi:a/one", "pi:a/one:high", "claude:sonnet"] },
  { tier: "fast-chat", candidates: ["pi:b/two", "pi:a/one"] },
  { tier: "zen", candidates: ["claude:opus:low"] },
];

test("stepTierState covers a model at any effort and reports multi-tier", () => {
  assert.deepEqual(m.stepTierState(CATALOG, { harness: "pi", model: "a/one" }), ["default", "fast-chat"], "the effort variant counts as the model, display-ordered");
  assert.deepEqual(m.stepTierState(CATALOG, { harness: "claude", model: "sonnet" }), ["default"]);
  assert.deepEqual(m.stepTierState(CATALOG, { harness: "claude", model: "opus" }), ["zen"]);
  assert.deepEqual(m.stepTierState(CATALOG, { harness: "pi", model: "gone/old" }), []);
});

test("stepTierRowView classifies all / unset / specific (inconsistent or partial)", () => {
  assert.deepEqual(m.stepTierRowView(["pi", "claude"], { pi: ["default"], claude: ["default"] }), { kind: "all", tier: "default" });
  assert.deepEqual(m.stepTierRowView(["pi", "claude"], { pi: [], claude: [] }), { kind: "unset" });
  assert.deepEqual(m.stepTierRowView(["pi", "claude"], { pi: ["fast-chat"], claude: ["default"] }), {
    kind: "specific",
    per: [{ harness: "pi", tiers: ["fast-chat"] }, { harness: "claude", tiers: ["default"] }],
  }, "differing tiers list per harness");
  assert.deepEqual(m.stepTierRowView(["pi", "claude"], { pi: ["default"], claude: [] }), {
    kind: "specific",
    per: [{ harness: "pi", tiers: ["default"] }, { harness: "claude", tiers: [] }],
  }, "a partly unset row is specific, with the unset harness listed");
});

test("the picker groups: default first then alphabetical, an invalid reference marked, the unset group last", () => {
  const defined = { pi: ["a/one", "c/three"], claude: ["sonnet", "opus"] };
  const groups = m.stepTierPickerGroups(CATALOG, defined);
  assert.deepEqual(groups, [
    { tier: "default", candidates: [
      { value: "pi:a/one", invalid: false },
      { value: "pi:a/one:high", invalid: false },
      { value: "claude:sonnet", invalid: false },
    ] },
    { tier: "fast-chat", candidates: [
      { value: "pi:b/two", invalid: true },
      { value: "pi:a/one", invalid: false },
    ] },
    { tier: "zen", candidates: [
      { value: "claude:opus:low", invalid: false },
    ] },
    { tier: null, candidates: [
      { value: "pi:c/three", invalid: false },
    ] },
  ], "default first, the rest alphabetical; a catalog candidate no defined model answers is invalid; the defined models no candidate covers are the unset group (opus is covered by its effort entry, so it is NOT unset)");
  assert.deepEqual(m.stepTierInvalidReferences(CATALOG, defined), ["pi:b/two"]);
  // a claude probe failure leaves the alias fallback: default itself is covered
  assert.deepEqual(m.stepTierInvalidReferences([{ tier: "default", candidates: ["claude:default"] }], { pi: [], claude: ["default"] }), []);
});

// polyforge#258 (T3): the step-tier drawer's write plan, restored after
// #256 removed the editor. Expected values are hand-computed from pf's
// [step_tiers] contract and the ticket's settled rules: 先加后删 (a crash
// between the two keys leaves a duplicate, never a loss), effort entries move
// exactly as they are, a vacated tier is unset (pf refuses an empty list),
// and a move that changes nothing plans nothing.
test("the write plan moves one identity add-before-remove", () => {
  // pi:a/one sits in default (plain and :high) and fast-chat; moving it to zen
  // must add both entries to zen first, then strip them from the two others.
  assert.deepEqual(m.stepTierWritePlan(CATALOG, { harness: "pi", model: "a/one" }, "zen"), [
    { action: "set", tier: "zen", candidates: ["claude:opus:low", "pi:a/one", "pi:a/one:high"] },
    { action: "set", tier: "default", candidates: ["claude:sonnet"] },
    { action: "set", tier: "fast-chat", candidates: ["pi:b/two"] },
  ], "the add-target write leads; the effort variant moves as it is");

  // a tier the move leaves empty is unset, never written as []
  assert.deepEqual(m.stepTierWritePlan([{ tier: "only", candidates: ["pi:a/one"] }], { harness: "pi", model: "a/one" }, null), [
    { action: "unset", tier: "only", candidates: [] },
  ]);

  // unset: the removals are planned in catalog order, the untouched tier absent
  assert.deepEqual(m.stepTierWritePlan(CATALOG, { harness: "pi", model: "a/one" }, null), [
    { action: "set", tier: "default", candidates: ["claude:sonnet"] },
    { action: "set", tier: "fast-chat", candidates: ["pi:b/two"] },
  ]);

  // a model no tier names yet still gains the target as itself
  assert.deepEqual(m.stepTierWritePlan(CATALOG, { harness: "pi", model: "new/model" }, "default"), [
    { action: "set", tier: "default", candidates: ["pi:a/one", "pi:a/one:high", "claude:sonnet", "pi:new/model"] },
  ]);

  // moving to the tier it already sits in changes nothing
  assert.deepEqual(m.stepTierWritePlan(
    [{ tier: "default", candidates: ["pi:a/one"] }], { harness: "pi", model: "a/one" }, "default"), []);
});
