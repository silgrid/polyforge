// carries: e2e/harness.mjs, e2e/polyforge.mjs
// polyforge#63 — the E2E_ONLY switch e2e/polyforge.mjs uses to run only the
// named scenarios (after e2e/polyforge-messages-prompt-281.mjs's local parse):
// unset runs every scenario unchanged; an id the suite does not know fails
// loudly naming it, never a silent zero-scenario run. The selection lives in
// e2e/harness.mjs, so the unit bar (npm test, no Chromium) pins it against a
// fake scenario table; the suite's own wiring is pinned by the
// source-structure test in a later commit (the suite script cannot be
// imported: it starts servers and browsers on evaluation).
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createCheck, e2eOnlySelection } from "../e2e/harness.mjs";

const SCENARIOS = {
  AC1: "AC1: first scenario",
  AC2: "AC2: second scenario",
  N1: "N1: third scenario",
};
const VALID_IDS = ["AC1", "AC2", "N1"];

test("unset or empty E2E_ONLY keeps every scenario", () => {
  for (const env of [{}, { E2E_ONLY: "" }, { E2E_ONLY: "," }]) {
    const only = e2eOnlySelection(SCENARIOS, env);
    assert.deepEqual(only.ids, [], `${JSON.stringify(env)}: no id parsed`);
    assert.deepEqual(only.titles, Object.values(SCENARIOS), `${JSON.stringify(env)}: every scenario's title`);
    for (const title of Object.values(SCENARIOS)) {
      assert.equal(only.skip(title), false, `${JSON.stringify(env)}: ${title} is not skipped`);
    }
  }
});

test("E2E_ONLY=<id>[,<id>…] selects exactly the listed scenarios", () => {
  const only = e2eOnlySelection(SCENARIOS, { E2E_ONLY: "N1,AC1" });
  assert.deepEqual(only.ids, ["N1", "AC1"], "the parsed selection, in the order written");
  assert.deepEqual(only.titles, [SCENARIOS.N1, SCENARIOS.AC1], "only the listed scenarios' titles, in table order");
  assert.equal(only.skip(SCENARIOS.N1), false, "a listed scenario's check runs");
  assert.equal(only.skip(SCENARIOS.AC1), false, "a listed scenario's check runs (second id)");
  assert.equal(only.skip(SCENARIOS.AC2), true, "an unlisted scenario's check is skipped");
});

test("an unknown E2E_ONLY id fails loudly naming it and the valid ids", () => {
  // a whitespace-prefixed id is unknown too: the parse follows 281's
  // split(",").filter(Boolean) with no trim, so " AC1" never silently
  // selects AC1.
  for (const [env, id] of [[{ E2E_ONLY: "AC9" }, "AC9"], [{ E2E_ONLY: "AC1,NOPE" }, "NOPE"], [{ E2E_ONLY: " AC1" }, " AC1"]]) {
    assert.throws(() => e2eOnlySelection(SCENARIOS, env), (error) =>
      error.message.includes(id) && VALID_IDS.every((valid) => error.message.includes(valid)),
      `${JSON.stringify(env)}: the error names ${JSON.stringify(id)} and the suite's valid ids`);
  }
});

test("createCheck skips an unselected scenario's checks through the selection's skip", async () => {
  const only = e2eOnlySelection(SCENARIOS, { E2E_ONLY: "N1" });
  const failures = [];
  const ran = [];
  const check = createCheck({ failures, before: (names) => ran.push(names[0]), skip: ([title]) => only.skip(title) });
  await check(SCENARIOS.AC2, "unselected", () => { throw new Error("must not run"); });
  await check(SCENARIOS.N1, "selected", () => {});
  assert.equal(check.count, 1, "only the selected scenario's check counts");
  assert.deepEqual(ran, [SCENARIOS.N1], "only the selected scenario ran");
  assert.deepEqual(failures, [], "the unselected scenario's throw never recorded a failure");
});
